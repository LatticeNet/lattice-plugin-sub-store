// Command perfgate judges a benchmark run against the perf targets and a
// committed baseline (S1 plan section 5.2, S2 plan section 5):
//
//	go run ./tools/perfgate -baseline system-go/testdata/bench/ubuntu-24.04.txt -current bench.txt
//
// The target table is the plugin's own unless -target names one: each
// -target Name=duration (repeatable) bounds that benchmark's median, and
// the flags together replace the built-in table, which is how another
// repository's perf job (lattice-server's bind and propose benchmarks)
// judges its own benchmarks with this tool.
//
// Both files are `go test -bench ... -count N -benchmem` output, and every
// result line belongs to the CPU named by the `cpu:` header above it. For
// every benchmark perfgate takes medians over the runs and fails when:
//
//   - a benchmark in the target table is missing or its ns/op median is not
//     under its bound;
//   - a baseline benchmark is missing from the current run;
//   - the ns/op median is not under 1.5 times the baseline median taken on
//     the same CPU as the current run. The runner hands out CPUs up to 1.8
//     times apart, so a baseline pooled over several would let a fast CPU
//     pass a large regression. When the baseline holds no run of the
//     current CPU for a benchmark, the slowest CPU's median stands in, the
//     verdict says so and perfgate warns: that comparison is looser, and the
//     CPU's run belongs in the baseline;
//   - the B/op or allocs/op median is not under 1.2 times the baseline
//     median over every CPU. Allocation does not depend on the CPU, so this
//     part of the gate is as tight on a fast CPU as on a slow one.
//
// A benchmark new since the baseline is held only to the target table.
// Without -baseline only the target table applies, which is how the first
// run that becomes the baseline is judged.
//
// It prints the table either way, and exits 1 when the gate fails and 2 when
// the input cannot be judged (no benchmark results, an unreadable file, a
// current run whose lines come from more than one CPU).
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

// defaultTargets are the bounds on the median of the plugin's gated
// benchmarks: the S1 pipeline (S1 plan section 5.2) and the S2 fleet plan,
// catalogue validation and fleet_preview figures (S2 plan section 5), and
// the fetch and convert handlers at 1000 lines. Those two carry the rest of
// the plugin's share of the 400 ms fleet-bound cache miss (S2 plan section
// 5): fetch 100 ms, plan 30 ms and convert 75 ms leave 195 ms for core's
// catalogue page, its 50 ms bind and the transport; each bound is about 1.5
// times its median on the slowest runner CPU seen (65.8 ms and 49.1 ms on an
// EPYC 7763). The producer, parse and isolated-engine benchmarks have no
// bound of their own; the regression rules cover them once the baseline
// holds them.
var defaultTargets = map[string]time.Duration{
	"BenchmarkPipelineReality1000SingBox": 20 * time.Millisecond,
	"BenchmarkPipelineReality4096SingBox": 100 * time.Millisecond,
	"BenchmarkFleetPlan1000":              30 * time.Millisecond,
	"BenchmarkFleetPlan4096":              120 * time.Millisecond,
	"BenchmarkValidateCatalogueRows4096":  20 * time.Millisecond,
	"BenchmarkFleetPreview10000":          500 * time.Millisecond,
	"BenchmarkFleetFetch1000":             100 * time.Millisecond,
	"BenchmarkFleetConvert1000":           75 * time.Millisecond,
}

// targetFlags collects -target Name=duration flags.
type targetFlags map[string]time.Duration

func (f targetFlags) String() string { return fmt.Sprint(map[string]time.Duration(f)) }

func (f targetFlags) Set(v string) error {
	name, bound, ok := strings.Cut(v, "=")
	if !ok || !strings.HasPrefix(name, "Benchmark") {
		return fmt.Errorf("want Name=duration with a Benchmark name, got %q", v)
	}
	d, err := time.ParseDuration(bound)
	if err != nil || d <= 0 {
		return fmt.Errorf("bad duration in %q", v)
	}
	if _, dup := f[name]; dup {
		return fmt.Errorf("%s is bounded twice", name)
	}
	f[name] = d
	return nil
}

// regressionFactor is the 1.5 times rule: an ns/op median must stay under
// this multiple of its baseline median on the same CPU.
const regressionFactor = 1.5

// memoryFactor is the allocation rule: a B/op or allocs/op median must stay
// under this multiple of its baseline median. The committed medians agree
// across CPUs to within 0.3 percent, so the rule can be tighter than the
// wall-clock one.
const memoryFactor = 1.2

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
	custom := targetFlags{}
	fs.Var(custom, "target", "Name=duration bound on a benchmark's median (repeatable; replaces the built-in table)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *currentPath == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: perfgate [-baseline <bench.txt>] [-target Name=duration ...] -current <bench.txt>")
		return 2
	}
	targets := map[string]time.Duration(defaultTargets)
	if len(custom) > 0 {
		targets = custom
	}
	current, err := readBench(*currentPath)
	if err != nil {
		fmt.Fprintf(stderr, "perfgate: %v\n", err)
		return 2
	}
	if cpus := current.cpus(); len(cpus) > 1 {
		fmt.Fprintf(stderr, "perfgate: %s: the run's lines come from %d CPUs (%s); judge one machine's run\n", *currentPath, len(cpus), strings.Join(cpus, ", "))
		return 2
	}
	var baseline bench
	if *baselinePath != "" {
		if baseline, err = readBench(*baselinePath); err != nil {
			fmt.Fprintf(stderr, "perfgate: %v\n", err)
			return 2
		}
	}
	rows := judge(current, baseline, targets)
	writeTable(stdout, rows)
	var unmatched []string
	failed := 0
	for _, r := range rows {
		if r.fail {
			failed++
		}
		if r.slowestCPU {
			unmatched = append(unmatched, r.name)
		}
	}
	if len(unmatched) > 0 {
		fmt.Fprintf(stderr, "perfgate: the baseline holds no run on %q for %d benchmarks (%s); their ns/op is judged against the slowest CPU's median, a looser gate. Add this CPU's run to the baseline.\n",
			current.cpu(), len(unmatched), strings.Join(unmatched, ", "))
	}
	if failed > 0 {
		fmt.Fprintf(stderr, "perfgate: %d of %d benchmarks fail the gate\n", failed, len(rows))
		return 1
	}
	return 0
}

// sample is one result line: its ns/op, and its B/op and allocs/op when the
// run was -benchmem (NaN otherwise).
type sample struct {
	ns, bytes, allocs float64
}

// bench is a parsed go test -bench file: the samples of every benchmark,
// keyed by the cpu: header they followed ("" before any) and then by the
// benchmark name without its -GOMAXPROCS suffix.
type bench map[string]map[string][]sample

// cpus answers the CPUs the file names, sorted, "" left out.
func (b bench) cpus() []string {
	var out []string
	for cpu := range b {
		if cpu != "" {
			out = append(out, cpu)
		}
	}
	sort.Strings(out)
	return out
}

// cpu answers the one CPU of a single machine's run, or "".
func (b bench) cpu() string {
	if cpus := b.cpus(); len(cpus) == 1 {
		return cpus[0]
	}
	return ""
}

// all answers a benchmark's samples over every CPU.
func (b bench) all(name string) []sample {
	var out []sample
	for _, cpu := range slices.Sorted(maps.Keys(b)) {
		out = append(out, b[cpu][name]...)
	}
	return out
}

func readBench(path string) (bench, error) {
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

// parseBench returns the samples of every benchmark result line under the
// CPU of the last cpu: header before it. Other lines (the goos and pkg
// headers, PASS, ok, a -v name line) are skipped.
func parseBench(r io.Reader) (bench, error) {
	out := bench{}
	cpu := ""
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, "cpu:"); ok {
			cpu = strings.Join(strings.Fields(rest), " ")
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasPrefix(f[0], "Benchmark") {
			continue
		}
		if _, err := strconv.ParseInt(f[1], 10, 64); err != nil {
			continue
		}
		s := sample{ns: math.NaN(), bytes: math.NaN(), allocs: math.NaN()}
		for k := 3; k < len(f); k += 2 {
			var into *float64
			switch f[k] {
			case "ns/op":
				into = &s.ns
			case "B/op":
				into = &s.bytes
			case "allocs/op":
				into = &s.allocs
			default:
				continue
			}
			v, err := strconv.ParseFloat(f[k-1], 64)
			if err != nil {
				return nil, fmt.Errorf("bad %s value in %q", f[k], line)
			}
			*into = v
		}
		if math.IsNaN(s.ns) {
			continue
		}
		if out[cpu] == nil {
			out[cpu] = map[string][]sample{}
		}
		name := procsSuffix.ReplaceAllString(f[0], "")
		out[cpu][name] = append(out[cpu][name], s)
	}
	return out, sc.Err()
}

// median of a sample set's values, NaN ones left out; NaN when none is
// left; the mean of the middle two for an even count.
func median(xs []float64) float64 {
	s := make([]float64, 0, len(xs))
	for _, x := range xs {
		if !math.IsNaN(x) {
			s = append(s, x)
		}
	}
	if len(s) == 0 {
		return math.NaN()
	}
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func values(samples []sample, field func(sample) float64) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = field(s)
	}
	return out
}

func nsOf(s sample) float64     { return s.ns }
func bytesOf(s sample) float64  { return s.bytes }
func allocsOf(s sample) float64 { return s.allocs }

type row struct {
	name     string
	runs     int
	median   float64 // ns/op; NaN when the current run lacks the benchmark
	baseline float64 // ns/op on the current CPU, or the slowest CPU's; NaN when there is none
	// slowestCPU is set when the baseline holds no run on the current CPU
	// and baseline is the slowest CPU's median.
	slowestCPU bool
	allocs     float64 // allocs/op median of the current run
	baseAllocs float64 // allocs/op median of the baseline over every CPU
	bytes      float64
	baseBytes  float64
	target     time.Duration
	verdict    string
	fail       bool
}

func judge(current, baseline bench, targets map[string]time.Duration) []row {
	names := map[string]bool{}
	for _, b := range []bench{current, baseline} {
		for _, byName := range b {
			for n := range byName {
				names[n] = true
			}
		}
	}
	for n := range targets {
		names[n] = true
	}
	cpu := current.cpu()
	var rows []row
	for _, name := range slices.Sorted(maps.Keys(names)) {
		r := row{name: name, median: math.NaN(), baseline: math.NaN(), allocs: math.NaN(), baseAllocs: math.NaN(),
			bytes: math.NaN(), baseBytes: math.NaN(), target: targets[name]}
		cur := current.all(name)
		inCurrent := len(cur) > 0
		inBaseline := len(baseline.all(name)) > 0
		if inBaseline {
			if same := baseline[cpu][name]; len(same) > 0 {
				r.baseline = median(values(same, nsOf))
			} else {
				for _, byName := range baseline {
					if s := byName[name]; len(s) > 0 {
						r.baseline = math.Max(nanTo(r.baseline, 0), median(values(s, nsOf)))
					}
				}
				r.slowestCPU = true
			}
			every := baseline.all(name)
			r.baseAllocs, r.baseBytes = median(values(every, allocsOf)), median(values(every, bytesOf))
		}
		var problems []string
		if inCurrent {
			r.runs = len(cur)
			r.median = median(values(cur, nsOf))
			r.allocs, r.bytes = median(values(cur, allocsOf)), median(values(cur, bytesOf))
		} else {
			problems = append(problems, "missing from the current run")
		}
		if inCurrent && r.target > 0 && r.median >= float64(r.target) {
			problems = append(problems, "not under "+r.target.String())
		}
		if inCurrent && inBaseline && r.median >= regressionFactor*r.baseline {
			problems = append(problems, fmt.Sprintf("not under %.1fx baseline", regressionFactor))
		}
		if inCurrent && inBaseline && !math.IsNaN(r.baseAllocs) && r.allocs >= memoryFactor*r.baseAllocs {
			problems = append(problems, fmt.Sprintf("allocs/op not under %.1fx baseline", memoryFactor))
		}
		if inCurrent && inBaseline && !math.IsNaN(r.baseBytes) && r.bytes >= memoryFactor*r.baseBytes {
			problems = append(problems, fmt.Sprintf("B/op not under %.1fx baseline", memoryFactor))
		}
		switch {
		case len(problems) > 0:
			r.fail = true
			r.verdict = "FAIL: " + strings.Join(problems, "; ")
		case baseline != nil && !inBaseline:
			r.verdict = "ok (new, no baseline)"
		case r.slowestCPU:
			r.verdict = "ok (no baseline on this CPU; ns/op against the slowest CPU's)"
		default:
			r.verdict = "ok"
		}
		rows = append(rows, r)
	}
	return rows
}

func nanTo(x, v float64) float64 {
	if math.IsNaN(x) {
		return v
	}
	return x
}

func writeTable(w io.Writer, rows []row) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "benchmark\truns\tmedian\tbaseline\tratio\tallocs\tB/op\ttarget\tverdict")
	for _, r := range rows {
		target := "-"
		if r.target > 0 {
			target = "< " + r.target.String()
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.name, r.runs, formatNs(r.median), formatNs(r.baseline),
			ratio(r.median, r.baseline), ratio(r.allocs, r.baseAllocs), ratio(r.bytes, r.baseBytes), target, r.verdict)
	}
	tw.Flush()
}

func ratio(cur, base float64) string {
	if math.IsNaN(cur) || math.IsNaN(base) || base <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2fx", cur/base)
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
