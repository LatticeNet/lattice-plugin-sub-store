// Command perfgate judges a benchmark run against the S1 perf targets and a
// committed baseline (S1 plan section 5.2):
//
//	go run ./tools/perfgate -baseline system-go/testdata/bench/ubuntu-24.04.txt -current bench.txt
//
// Both files are `go test -bench ... -count N` output. For every benchmark
// perfgate takes the median ns/op over the runs, then fails when a benchmark
// in the target table is missing or not under its bound, when a baseline
// benchmark is missing from the current run, or when a current median is not
// under 1.5 times its baseline median. A benchmark new since the baseline is
// held only to the target table. Without -baseline only the target table
// applies, which is how the first run that becomes the baseline is judged.
//
// It prints the table either way, and exits 1 when the gate fails and 2 when
// the input cannot be judged (no benchmark results, an unreadable file).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// targets are the bounds on the median of the pipeline benchmarks. The
// producer benchmarks have no bound of their own; the regression rule covers
// them.
var targets = map[string]time.Duration{
	"BenchmarkPipelineReality1000SingBox": 20 * time.Millisecond,
	"BenchmarkPipelineReality4096SingBox": 100 * time.Millisecond,
}

// regressionFactor is the 1.5 times rule: a median must stay under this
// multiple of its baseline median.
const regressionFactor = 1.5

// procsSuffix is the -GOMAXPROCS suffix go test appends to a benchmark name
// when GOMAXPROCS is above 1. No gated benchmark name ends in a hyphen and
// digits of its own.
var procsSuffix = regexp.MustCompile(`-\d+$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("perfgate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baselinePath := fs.String("baseline", "", "committed go test -bench output (optional)")
	currentPath := fs.String("current", "", "go test -bench output of this run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *currentPath == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: perfgate [-baseline <bench.txt>] -current <bench.txt>")
		return 2
	}
	current, err := readBench(*currentPath)
	if err != nil {
		fmt.Fprintf(stderr, "perfgate: %v\n", err)
		return 2
	}
	var baseline map[string][]float64
	if *baselinePath != "" {
		if baseline, err = readBench(*baselinePath); err != nil {
			fmt.Fprintf(stderr, "perfgate: %v\n", err)
			return 2
		}
	}
	rows := judge(current, baseline)
	writeTable(stdout, rows)
	failed := 0
	for _, r := range rows {
		if r.fail {
			failed++
		}
	}
	if failed > 0 {
		fmt.Fprintf(stderr, "perfgate: %d of %d benchmarks fail the gate\n", failed, len(rows))
		return 1
	}
	return 0
}

func readBench(path string) (map[string][]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	samples, err := parseBench(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("%s: no benchmark results", path)
	}
	return samples, nil
}

// parseBench returns the ns/op samples of every benchmark result line, keyed
// by the benchmark name without its -GOMAXPROCS suffix. Other lines (the
// goos, pkg and cpu headers, PASS, ok, a -v name line) are skipped.
func parseBench(r io.Reader) (map[string][]float64, error) {
	samples := map[string][]float64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || !strings.HasPrefix(f[0], "Benchmark") {
			continue
		}
		if _, err := strconv.ParseInt(f[1], 10, 64); err != nil {
			continue
		}
		for k := 3; k < len(f); k += 2 {
			if f[k] != "ns/op" {
				continue
			}
			v, err := strconv.ParseFloat(f[k-1], 64)
			if err != nil {
				return nil, fmt.Errorf("bad ns/op value in %q", sc.Text())
			}
			name := procsSuffix.ReplaceAllString(f[0], "")
			samples[name] = append(samples[name], v)
		}
	}
	return samples, sc.Err()
}

// median of a non-empty sample set; the mean of the middle two for an even
// count.
func median(xs []float64) float64 {
	s := slices.Clone(xs)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

type row struct {
	name     string
	runs     int
	median   float64 // ns/op; NaN when the current run lacks the benchmark
	baseline float64 // ns/op; NaN when there is none
	target   time.Duration
	verdict  string
	fail     bool
}

func judge(current, baseline map[string][]float64) []row {
	names := map[string]bool{}
	for n := range current {
		names[n] = true
	}
	for n := range baseline {
		names[n] = true
	}
	for n := range targets {
		names[n] = true
	}
	var rows []row
	for _, name := range slices.Sorted(maps.Keys(names)) {
		r := row{name: name, median: math.NaN(), baseline: math.NaN(), target: targets[name]}
		cur, inCurrent := current[name]
		if b, ok := baseline[name]; ok {
			r.baseline = median(b)
		}
		var problems []string
		if inCurrent {
			r.runs = len(cur)
			r.median = median(cur)
		} else {
			problems = append(problems, "missing from the current run")
		}
		if inCurrent && r.target > 0 && r.median >= float64(r.target) {
			problems = append(problems, "not under "+r.target.String())
		}
		if inCurrent && !math.IsNaN(r.baseline) && r.median >= regressionFactor*r.baseline {
			problems = append(problems, fmt.Sprintf("not under %.1fx baseline", regressionFactor))
		}
		switch {
		case len(problems) > 0:
			r.fail = true
			r.verdict = "FAIL: " + strings.Join(problems, "; ")
		case baseline != nil && math.IsNaN(r.baseline):
			r.verdict = "ok (new, no baseline)"
		default:
			r.verdict = "ok"
		}
		rows = append(rows, r)
	}
	return rows
}

func writeTable(w io.Writer, rows []row) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "benchmark\truns\tmedian\tbaseline\tratio\ttarget\tverdict")
	for _, r := range rows {
		ratio, target := "-", "-"
		if !math.IsNaN(r.median) && !math.IsNaN(r.baseline) && r.baseline > 0 {
			ratio = fmt.Sprintf("%.2fx", r.median/r.baseline)
		}
		if r.target > 0 {
			target = "< " + r.target.String()
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n", r.name, r.runs, formatNs(r.median), formatNs(r.baseline), ratio, target, r.verdict)
	}
	tw.Flush()
}

func formatNs(ns float64) string {
	if math.IsNaN(ns) {
		return "-"
	}
	d := time.Duration(math.Round(ns))
	if d >= time.Millisecond {
		d = d.Round(time.Microsecond)
	}
	return d.String()
}
