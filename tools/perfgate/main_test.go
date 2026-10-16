package main

import (
	"bytes"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// benchOutput writes go test -bench lines for name with the given ns/op
// samples, the way `-count N` repeats them, at 1234 B/op and 56 allocs/op.
func benchOutput(name string, ns ...float64) string {
	return benchMem(name, 1234, 56, ns...)
}

// benchMem is benchOutput with the B/op and allocs/op of every line.
func benchMem(name string, bytes, allocs float64, ns ...float64) string {
	var b strings.Builder
	for _, v := range ns {
		fmt.Fprintf(&b, "%s-4   \t      60\t  %g ns/op\t  %g B/op\t      %g allocs/op\n", name, v, bytes, allocs)
	}
	return b.String()
}

// onCPU heads lines with the cpu: header go test prints for a package.
func onCPU(cpu string, lines ...string) string {
	return "goos: linux\ngoarch: amd64\npkg: github.com/LatticeNet/lattice-plugin-sub-store/system-go\ncpu: " + cpu + "                \n" +
		strings.Join(lines, "") + "PASS\nok  \tgithub.com/LatticeNet/lattice-plugin-sub-store/system-go\t12.3s\n"
}

// mustParse parses bench text a test wrote.
func mustParse(t *testing.T, text string) bench {
	t.Helper()
	b, err := parseBench(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const (
	epyc7763 = "AMD EPYC 7763 64-Core Processor"
	epyc9v45 = "AMD EPYC 9V45 96-Core Processor"
	xeon     = "INTEL(R) XEON(R) PLATINUM 8573C"
)

const (
	pipe1000 = "BenchmarkPipelineReality1000SingBox"
	pipe4096 = "BenchmarkPipelineReality4096SingBox"
	prodSB   = "BenchmarkProduce4096/singbox"
)

// pipeTargets is the S1 half of the default table, which the judge tests
// exercise on their own.
var pipeTargets = map[string]time.Duration{pipe1000: 20 * time.Millisecond, pipe4096: 100 * time.Millisecond}

// pipeFlags bounds the two pipeline benchmarks through -target.
var pipeFlags = []string{"-target", pipe1000 + "=20ms", "-target", pipe4096 + "=100ms"}

// TestDefaultTargetsHoldTheS2Bounds pins the plugin's table: the S1
// pipeline bounds and the S2 fleet figures of the plan's section 5.
func TestDefaultTargetsHoldTheS2Bounds(t *testing.T) {
	want := map[string]time.Duration{
		pipe1000: 20 * time.Millisecond, pipe4096: 100 * time.Millisecond,
		"BenchmarkFleetPlan1000": 30 * time.Millisecond, "BenchmarkFleetPlan4096": 120 * time.Millisecond,
		"BenchmarkValidateCatalogueRows4096": 20 * time.Millisecond, "BenchmarkFleetPreview10000": 500 * time.Millisecond,
		"BenchmarkFleetFetch1000": 100 * time.Millisecond, "BenchmarkFleetConvert1000": 75 * time.Millisecond,
	}
	if len(defaultTargets) != len(want) {
		t.Fatalf("default targets = %v, want %v", defaultTargets, want)
	}
	for name, d := range want {
		if defaultTargets[name] != d {
			t.Errorf("%s: bound %v, want %v", name, defaultTargets[name], d)
		}
	}
}

func TestTargetFlagsReplaceTheTable(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "server.txt")
	if err := os.WriteFile(cur, []byte(benchOutput("BenchmarkSubstoreBindPlan1000", 31e6, 32e6, 33e6)), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-target", "BenchmarkSubstoreBindPlan1000=50ms", "-current", cur}, &stdout, &stderr); code != 0 || strings.Contains(stdout.String(), pipe1000) {
		t.Fatalf("a -target run judged the built-in table or failed: %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"-target", "BenchmarkSubstoreBindPlan1000=30ms", "-current", cur}, &stdout, &stderr); code != 1 || !strings.Contains(stdout.String(), "not under 30ms") {
		t.Fatalf("a median over its -target bound passed: %d\n%s", code, stdout.String())
	}
	if code := run([]string{"-current", cur}, &stdout, &stderr); code != 1 {
		t.Fatalf("without -target the plugin's gated benchmarks must be present: %d", code)
	}
	for _, bad := range []string{"BenchmarkX", "BenchmarkX=fast", "X=1ms", "BenchmarkX=-1ms"} {
		if code := run([]string{"-target", bad, "-current", cur}, &stdout, &stderr); code != 2 {
			t.Errorf("-target %q was accepted", bad)
		}
	}
	if code := run([]string{"-target", "BenchmarkX=1ms", "-target", "BenchmarkX=2ms", "-current", cur}, &stdout, &stderr); code != 2 {
		t.Error("a benchmark bounded twice was accepted")
	}
}

func TestMedianTakesTheMiddle(t *testing.T) {
	for _, c := range []struct {
		in   []float64
		want float64
	}{
		{[]float64{7}, 7},
		{[]float64{9, 1, 5}, 5},
		{[]float64{4, 1, 3, 2}, 2.5},
		{[]float64{10, 10, 10, 10, 10, 11, 12, 13, 14, 1000}, 10.5},
	} {
		if got := median(c.in); got != c.want {
			t.Errorf("median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseBenchReadsEachMetricByNameAndCPU(t *testing.T) {
	out := onCPU(epyc7763,
		pipe1000+"\n", // the name line -v prints before the result
		benchMem(pipe1000, 4e6, 22000, 18e6, 19e6),
		"BenchmarkProduce4096/singbox     \t      12\t  9512345.5 ns/op\n", // GOMAXPROCS 1, no -benchmem
		"BenchmarkFleetConvert1000-4             \t      22\t  50270194 ns/op\t  23.42 MB/s\t26233936 B/op\t  359221 allocs/op\n",
		"--- BENCH: BenchmarkProduce4096/uri-4\n    perf_test.go:12: note\n") +
		onCPU(epyc9v45, benchOutput(pipe1000, 11e6))
	got := mustParse(t, out)
	if cpus := got.cpus(); len(cpus) != 2 || cpus[0] != epyc7763 || cpus[1] != epyc9v45 {
		t.Fatalf("cpus = %q", cpus)
	}
	slow := got[epyc7763]
	if len(slow[pipe1000]) != 2 || slow[pipe1000][1].ns != 19e6 || slow[pipe1000][0].bytes != 4e6 || slow[pipe1000][0].allocs != 22000 {
		t.Fatalf("7763 %s = %+v", pipe1000, slow[pipe1000])
	}
	if s := slow[prodSB]; len(s) != 1 || s[0].ns != 9512345.5 || !math.IsNaN(s[0].bytes) || !math.IsNaN(s[0].allocs) {
		t.Fatalf("7763 %s = %+v", prodSB, s)
	}
	if s := slow["BenchmarkFleetConvert1000"]; len(s) != 1 || s[0].bytes != 26233936 || s[0].allocs != 359221 {
		t.Fatalf("a MB/s column moved the memory figures: %+v", s)
	}
	if s := got[epyc9v45][pipe1000]; len(s) != 1 || s[0].ns != 11e6 {
		t.Fatalf("9V45 %s = %+v", pipe1000, s)
	}
	if _, err := parseBench(strings.NewReader(pipe1000 + "-4 60 fast ns/op\n")); err == nil {
		t.Fatal("a non-numeric ns/op was accepted")
	}
	if _, err := parseBench(strings.NewReader(pipe1000 + "-4 60 5 ns/op lots B/op\n")); err == nil {
		t.Fatal("a non-numeric B/op was accepted")
	}
}

func verdicts(rows []row) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		out[r.name] = r.verdict
	}
	return out
}

func TestJudgeAppliesTheTargetTable(t *testing.T) {
	samples := func(p1000, p4096 float64) bench {
		return mustParse(t, benchOutput(pipe1000, p1000, p1000, p1000)+benchOutput(pipe4096, p4096))
	}
	if v := verdicts(judge(samples(19.9e6, 99.9e6), nil, pipeTargets)); v[pipe1000] != "ok" || v[pipe4096] != "ok" {
		t.Fatalf("medians under both bounds: %v", v)
	}
	// The bound is "under": a median equal to it fails.
	if v := verdicts(judge(samples(20e6, 99e6), nil, pipeTargets)); !strings.Contains(v[pipe1000], "not under 20ms") || v[pipe4096] != "ok" {
		t.Fatalf("1000-node median at 20ms: %v", v)
	}
	if v := verdicts(judge(samples(5e6, 100.1e6), nil, pipeTargets)); !strings.Contains(v[pipe4096], "not under 100ms") {
		t.Fatalf("4096-node median over 100ms: %v", v)
	}
	// A gated benchmark that did not run fails rather than passing by absence.
	only := mustParse(t, benchOutput(pipe1000, 1e6))
	if v := verdicts(judge(only, nil, pipeTargets)); !strings.Contains(v[pipe4096], "missing from the current run") {
		t.Fatalf("4096-node benchmark absent: %v", v)
	}
	// The median decides, not one slow run.
	cur := mustParse(t, benchOutput(pipe1000, 1e6, 1e6, 500e6)+benchOutput(pipe4096, 1e6))
	if v := verdicts(judge(cur, nil, pipeTargets)); v[pipe1000] != "ok" {
		t.Fatalf("one outlier among three runs: %v", v)
	}
}

func TestJudgeAppliesTheRegressionRule(t *testing.T) {
	base := mustParse(t, benchOutput(pipe1000, 10e6)+benchOutput(pipe4096, 40e6)+benchOutput(prodSB, 2e6)+benchOutput("BenchmarkProduce4096/uri", 1e6))
	cur := mustParse(t, benchOutput(pipe1000, 14.9e6)+benchOutput(pipe4096, 60e6)+benchOutput(prodSB, 3.1e6)+benchOutput("BenchmarkProduce4096/json", 1e6))
	v := verdicts(judge(cur, base, pipeTargets))
	want := map[string]string{
		pipe1000:                    "ok",                           // 1.49x
		pipe4096:                    "not under 1.5x baseline",      // exactly 1.5x
		prodSB:                      "not under 1.5x baseline",      // 1.55x, no absolute bound
		"BenchmarkProduce4096/uri":  "missing from the current run", // renamed or dropped
		"BenchmarkProduce4096/json": "ok (new, no baseline)",
	}
	for name, w := range want {
		if w == "ok" || strings.HasPrefix(w, "ok ") {
			if v[name] != w {
				t.Errorf("%s: verdict %q, want %q", name, v[name], w)
			}
		} else if !strings.HasPrefix(v[name], "FAIL: ") || !strings.Contains(v[name], w) {
			t.Errorf("%s: verdict %q, want a failure saying %q", name, v[name], w)
		}
	}
}

// The ns/op rule compares runs of one CPU. A baseline pooled over a slow and
// a fast CPU let a 2.2 times slowdown on the fast one pass; judged against
// its own CPU's median it fails, while the slow CPU's run at the same figure
// passes.
func verdictOf(t *testing.T, rows []row, name string) row {
	t.Helper()
	for _, r := range rows {
		if r.name == name {
			return r
		}
	}
	t.Fatalf("no row for %s", name)
	return row{}
}

func TestJudgeComparesNsPerOpOnTheSameCPU(t *testing.T) {
	base := mustParse(t, onCPU(epyc7763, benchOutput(pipe1000, 18e6, 19e6, 20e6, 19e6))+onCPU(epyc9v45, benchOutput(pipe1000, 10.9e6, 11e6)))
	fast := mustParse(t, onCPU(epyc9v45, benchOutput(pipe1000, 24.2e6, 24.2e6, 24.2e6)))
	if r := verdictOf(t, judge(fast, base, nil), pipe1000); !r.fail || !strings.Contains(r.verdict, "not under 1.5x baseline") || r.baseline != 10.95e6 {
		t.Fatalf("a 2.2x slowdown on the fast CPU: %+v", r)
	}
	slow := mustParse(t, onCPU(epyc7763, benchOutput(pipe1000, 24.2e6, 24.2e6, 24.2e6)))
	if r := verdictOf(t, judge(slow, base, nil), pipe1000); r.fail || r.verdict != "ok" || r.baseline != 19e6 {
		t.Fatalf("the slow CPU at 1.27x its own median: %+v", r)
	}
}

// A CPU the baseline never saw is judged against the slowest CPU's median,
// the loosest comparison, and both the verdict and run's warning say so.
func TestJudgeFallsBackToTheSlowestCPUAndSaysSo(t *testing.T) {
	base := mustParse(t, onCPU(epyc7763, benchOutput(pipe1000, 19e6))+onCPU(epyc9v45, benchOutput(pipe1000, 11e6)))
	cur := mustParse(t, onCPU(xeon, benchOutput(pipe1000, 25e6)))
	r := verdictOf(t, judge(cur, base, nil), pipe1000)
	if r.fail || !r.slowestCPU || r.baseline != 19e6 || !strings.Contains(r.verdict, "slowest CPU") {
		t.Fatalf("an unseen CPU: %+v", r)
	}
	if r := verdictOf(t, judge(mustParse(t, onCPU(xeon, benchOutput(pipe1000, 28.5e6))), base, nil), pipe1000); !r.fail {
		t.Fatalf("1.5x the slowest CPU's median on an unseen CPU passed: %+v", r)
	}
	dir := t.TempDir()
	basePath, curPath := filepath.Join(dir, "base.txt"), filepath.Join(dir, "cur.txt")
	if err := os.WriteFile(basePath, []byte(onCPU(epyc7763, benchOutput(pipe1000, 19e6))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(curPath, []byte(onCPU(xeon, benchOutput(pipe1000, 19e6))), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-target", pipe1000 + "=30ms", "-baseline", basePath, "-current", curPath}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stderr.String(), "no run on \""+xeon+"\"") || !strings.Contains(stderr.String(), pipe1000) {
		t.Fatalf("run on an unseen CPU = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
}

// Allocation does not depend on the CPU, so its rule holds on every CPU and
// pools the baseline over all of them.
func TestJudgeGatesAllocations(t *testing.T) {
	base := mustParse(t, onCPU(epyc7763, benchMem(pipe1000, 1e6, 1000, 19e6))+onCPU(epyc9v45, benchMem(pipe1000, 1e6, 1000, 11e6)))
	for _, c := range []struct {
		bytes, allocs float64
		want          string
	}{
		{1.19e6, 1190, ""},
		{1e6, 1200, "allocs/op not under 1.2x baseline"},
		{1.2e6, 1000, "B/op not under 1.2x baseline"},
	} {
		cur := mustParse(t, onCPU(epyc9v45, benchMem(pipe1000, c.bytes, c.allocs, 11e6)))
		r := verdictOf(t, judge(cur, base, nil), pipe1000)
		if c.want == "" && r.fail || c.want != "" && (!r.fail || !strings.Contains(r.verdict, c.want)) {
			t.Errorf("%g B/op, %g allocs/op: %+v", c.bytes, c.allocs, r)
		}
	}
	// A run without -benchmem is judged on ns/op alone.
	cur := mustParse(t, onCPU(epyc9v45, pipe1000+"-4 \t 60 \t 11000000 ns/op\n"))
	if r := verdictOf(t, judge(cur, base, nil), pipe1000); r.fail {
		t.Fatalf("a run without memory figures: %+v", r)
	}
}

// The review's scenario on the committed baseline: a 9V45 run of every S2
// benchmark 2.2 times slower than the 9V45 baseline lines, every other
// benchmark at its baseline, passed the pooled gate. It fails now, and the
// same run unscaled passes.
func TestCommittedBaselineCatchesAFastCPURegression(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "system-go", "testdata", "bench", "ubuntu-24.04.txt"))
	if err != nil {
		t.Fatal(err)
	}
	base := mustParse(t, string(raw))
	if len(base[epyc9v45]) == 0 {
		t.Fatalf("the committed baseline has no %s lines", epyc9v45)
	}
	synth := func(scale float64) bench {
		lines := []string{}
		for _, name := range slices.Sorted(maps.Keys(namesOf(base))) {
			samples := base[epyc9v45][name]
			factor := scale
			if len(samples) == 0 {
				samples, factor = base[epyc7763][name], 1
			}
			for _, s := range samples {
				lines = append(lines, benchMem(name, s.bytes, s.allocs, s.ns*factor))
			}
		}
		return mustParse(t, onCPU(epyc9v45, lines...))
	}
	for _, r := range judge(synth(1), base, defaultTargets) {
		if r.fail {
			t.Errorf("the 9V45 baseline lines judged against themselves: %s %s", r.name, r.verdict)
		}
	}
	failed := map[string]bool{}
	for _, r := range judge(synth(2.2), base, defaultTargets) {
		failed[r.name] = r.fail
	}
	for name := range base[epyc9v45] {
		if !failed[name] {
			t.Errorf("%s 2.2x slower on the 9V45 passes the gate", name)
		}
	}
}

func namesOf(b bench) map[string]bool {
	out := map[string]bool{}
	for _, byName := range b {
		for n := range byName {
			out[n] = true
		}
	}
	return out
}

func TestRunExitsWithTheTable(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := write("base.txt", benchOutput(pipe1000, 10e6)+benchOutput(pipe4096, 50e6))
	good := write("good.txt", benchOutput(pipe1000, 11e6)+benchOutput(pipe4096, 52e6))
	slow := write("slow.txt", benchOutput(pipe1000, 16e6)+benchOutput(pipe4096, 52e6))
	empty := write("empty.txt", "PASS\nok\n")
	twoCPUs := write("two.txt", onCPU(epyc7763, benchOutput(pipe1000, 11e6))+onCPU(epyc9v45, benchOutput(pipe4096, 52e6)))

	for _, c := range []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"-baseline", base, "-current", good}, 0, "1.10x", ""},
		{[]string{"-current", good}, 0, pipe4096, ""},
		{[]string{"-baseline", base, "-current", slow}, 1, "1.60x", "1 of 2 benchmarks fail"},
		{[]string{"-baseline", base, "-current", empty}, 2, "", "no benchmark results"},
		{[]string{"-baseline", filepath.Join(dir, "absent.txt"), "-current", good}, 2, "", "absent.txt"},
		{[]string{"-baseline", base}, 2, "", "usage"},
		{[]string{"-baseline", base, "-current", twoCPUs}, 2, "", "come from 2 CPUs"},
	} {
		var stdout, stderr bytes.Buffer
		c.args = append(append([]string(nil), pipeFlags...), c.args...)
		code := run(c.args, &stdout, &stderr)
		if code != c.code || !strings.Contains(stdout.String(), c.stdout) || !strings.Contains(stderr.String(), c.stderr) {
			t.Errorf("run(%q) = %d\nstdout:\n%s\nstderr:\n%s\nwant %d, stdout with %q, stderr with %q", c.args, code, stdout.String(), stderr.String(), c.code, c.stdout, c.stderr)
		}
		// On failure the table is still printed.
		if code == 1 && !strings.Contains(stdout.String(), "benchmark") {
			t.Errorf("run(%q) failed without the table", c.args)
		}
	}
}
