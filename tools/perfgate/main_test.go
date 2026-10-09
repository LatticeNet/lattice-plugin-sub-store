package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchOutput writes go test -bench lines for name with the given ns/op
// samples, the way `-count N` repeats them.
func benchOutput(name string, ns ...float64) string {
	var b strings.Builder
	for _, v := range ns {
		fmt.Fprintf(&b, "%s-4   \t      60\t  %g ns/op\t  1234 B/op\t      56 allocs/op\n", name, v)
	}
	return b.String()
}

const (
	pipe1000 = "BenchmarkPipelineReality1000SingBox"
	pipe4096 = "BenchmarkPipelineReality4096SingBox"
	prodSB   = "BenchmarkProduce4096/singbox"
)

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

func TestParseBenchReadsNsPerOpByName(t *testing.T) {
	out := "goos: linux\ngoarch: amd64\npkg: github.com/LatticeNet/lattice-plugin-sub-store/system-go\ncpu: AMD EPYC 7763 64-Core Processor\n" +
		pipe1000 + "\n" + // the name line -v prints before the result
		benchOutput(pipe1000, 18e6, 19e6) +
		"BenchmarkProduce4096/singbox     \t      12\t  9512345.5 ns/op\n" + // GOMAXPROCS 1: no suffix
		"--- BENCH: BenchmarkProduce4096/uri-4\n    perf_test.go:12: note\n" +
		"PASS\nok  \tgithub.com/LatticeNet/lattice-plugin-sub-store/system-go\t12.3s\n"
	got, err := parseBench(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got[pipe1000]) != 2 || got[pipe1000][1] != 19e6 || len(got[prodSB]) != 1 || got[prodSB][0] != 9512345.5 {
		t.Fatalf("parseBench = %v", got)
	}
	if _, err := parseBench(strings.NewReader(pipe1000 + "-4 60 fast ns/op\n")); err == nil {
		t.Fatal("a non-numeric ns/op was accepted")
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
	samples := func(p1000, p4096 float64) map[string][]float64 {
		cur, _ := parseBench(strings.NewReader(benchOutput(pipe1000, p1000, p1000, p1000) + benchOutput(pipe4096, p4096)))
		return cur
	}
	if v := verdicts(judge(samples(19.9e6, 99.9e6), nil)); v[pipe1000] != "ok" || v[pipe4096] != "ok" {
		t.Fatalf("medians under both bounds: %v", v)
	}
	// The bound is "under": a median equal to it fails.
	if v := verdicts(judge(samples(20e6, 99e6), nil)); !strings.Contains(v[pipe1000], "not under 20ms") || v[pipe4096] != "ok" {
		t.Fatalf("1000-node median at 20ms: %v", v)
	}
	if v := verdicts(judge(samples(5e6, 100.1e6), nil)); !strings.Contains(v[pipe4096], "not under 100ms") {
		t.Fatalf("4096-node median over 100ms: %v", v)
	}
	// A gated benchmark that did not run fails rather than passing by absence.
	only, _ := parseBench(strings.NewReader(benchOutput(pipe1000, 1e6)))
	if v := verdicts(judge(only, nil)); !strings.Contains(v[pipe4096], "missing from the current run") {
		t.Fatalf("4096-node benchmark absent: %v", v)
	}
	// The median decides, not one slow run.
	cur, _ := parseBench(strings.NewReader(benchOutput(pipe1000, 1e6, 1e6, 500e6) + benchOutput(pipe4096, 1e6)))
	if v := verdicts(judge(cur, nil)); v[pipe1000] != "ok" {
		t.Fatalf("one outlier among three runs: %v", v)
	}
}

func TestJudgeAppliesTheRegressionRule(t *testing.T) {
	base, _ := parseBench(strings.NewReader(benchOutput(pipe1000, 10e6) + benchOutput(pipe4096, 40e6) + benchOutput(prodSB, 2e6) + benchOutput("BenchmarkProduce4096/uri", 1e6)))
	cur, _ := parseBench(strings.NewReader(benchOutput(pipe1000, 14.9e6) + benchOutput(pipe4096, 60e6) + benchOutput(prodSB, 3.1e6) + benchOutput("BenchmarkProduce4096/json", 1e6)))
	v := verdicts(judge(cur, base))
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
	} {
		var stdout, stderr bytes.Buffer
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
