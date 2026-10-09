package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/parse"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/producers"
)

// The perf gate (S1 plan section 5.2) times the native render of perfgen's
// VLESS Reality nodes through four non-script operators to sing-box: the
// subscription text is parsed, the compiled chain runs, and the producer
// writes the document. The chain is compiled once, as a record revision's
// plan is. tools/perfgate holds the medians of the two pipeline benchmarks
// under 20 ms and 100 ms and every benchmark under 1.5 times the baseline
// committed in testdata/bench/ubuntu-24.04.txt.
var perfChain = []json.RawMessage{
	json.RawMessage(`{"type":"Regex Filter","args":{"regex":["^(HK|JP|SG|US|TW|KR|DE|GB) "],"keep":true}}`),
	json.RawMessage(`{"type":"Regex Rename Operator","args":[{"expr":"^(\\w+) (\\w+) (\\d+)$","now":"$1 $3 $2"}]}`),
	json.RawMessage(`{"type":"Sort Operator","args":"asc"}`),
	json.RawMessage(`{"type":"Flag Operator","args":{"mode":"add"}}`),
}

const perfTarget = "sing-box"

func perfPlan(tb testing.TB) *operators.Plan {
	tb.Helper()
	plan, err := operators.Compile("perf", perfChain)
	if err != nil || !plan.Native() {
		tb.Fatalf("the perf chain does not compile natively: %v", err)
	}
	return plan
}

// perfDocument is the subscription text of perfgen's first n nodes, one
// share link per line, as a provider serves it.
func perfDocument(n int) string {
	return strings.Join(perfgen.URIs(n), "\n")
}

// renderNative is the native render of one document: parse, the chain, the
// producer. It returns how many nodes the parse gave and how many entries
// the document has.
func renderNative(tb testing.TB, dst *bytes.Buffer, doc string, plan *operators.Plan) (parsed int, entries int) {
	tb.Helper()
	nodes, _, err := parse.Document(doc, parse.Options{})
	if err != nil {
		tb.Fatal(err)
	}
	parsed = len(nodes)
	nodes = plan.Run(nodes, &operators.Context{Target: perfTarget, Raw: doc})
	p, ok := producers.Lookup(perfTarget)
	if !ok {
		tb.Fatalf("%s has no native producer", perfTarget)
	}
	res, err := p.Produce(dst, nodes, perfTarget, nil)
	if err != nil {
		tb.Fatal(err)
	}
	return parsed, res.Entries
}

func benchmarkPipeline(b *testing.B, n int) {
	doc := perfDocument(n)
	plan := perfPlan(b)
	var buf bytes.Buffer
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		renderNative(b, &buf, doc, plan)
	}
}

func BenchmarkPipelineReality1000SingBox(b *testing.B) { benchmarkPipeline(b, 1000) }

func BenchmarkPipelineReality4096SingBox(b *testing.B) { benchmarkPipeline(b, 4096) }

// TestPerfPipelineRendersTheChain keeps the benchmarks honest: the parse
// yields every node, the Regex Filter keeps the eight countries' nodes, and
// sing-box writes every kept node except the XHTTP ones, which it has no
// transport for (singbox.md, row F6).
func TestPerfPipelineRendersTheChain(t *testing.T) {
	const n = 1000
	var want int
	for _, raw := range perfgen.Nodes(n) {
		var node struct{ Name, Network string }
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatal(err)
		}
		switch node.Name[:3] {
		case "HK ", "JP ", "SG ", "US ", "TW ", "KR ", "DE ", "GB ":
			if node.Network != perfgen.XHTTP {
				want++
			}
		}
	}
	var buf bytes.Buffer
	parsed, entries := renderNative(t, &buf, perfDocument(n), perfPlan(t))
	if parsed != n || entries != want || want == 0 || want == n {
		t.Fatalf("parsed %d nodes and produced %d entries; want %d and %d", parsed, entries, n, want)
	}
	if !strings.Contains(buf.String(), `"server_name"`) || !strings.Contains(buf.String(), "\U0001F1ED\U0001F1F0 HK ") {
		t.Fatalf("the document lacks the flagged, renamed Reality outbounds: %.300s", buf.String())
	}
}

// pipelineAllocsPerNodeBound is the allocation bound of the 1000-node
// render, per input node: the first measurement, 61.6 on go1.26.9 (darwin
// and linux alike), rounded up by a quarter. Raise it only with the reason in
// the commit.
const pipelineAllocsPerNodeBound = 77

func TestPipelineAllocsPerNodeBound(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates on its own account")
	}
	const n = 1000
	doc := perfDocument(n)
	plan := perfPlan(t)
	var buf bytes.Buffer
	allocs := testing.AllocsPerRun(5, func() {
		buf.Reset()
		renderNative(t, &buf, doc, plan)
	})
	perNode := allocs / n
	t.Logf("%.0f allocations per 1000-node render, %.1f per node (bound %d)", allocs, perNode, pipelineAllocsPerNodeBound)
	if perNode > pipelineAllocsPerNodeBound {
		t.Errorf("%.1f allocations per node, over the bound of %d", perNode, pipelineAllocsPerNodeBound)
	}
}

// heapChildEnv makes TestHeapInuseAfter4096Render measure in a child test
// process, where no other test has booted the warm runtime or left its heap
// behind.
const heapChildEnv = "SUBSTORE_HEAP_GATE_CHILD"

// maxHeapInuseAfterRender is the heap bound after a native 4096-node render
// (S1 plan section 5.2).
const maxHeapInuseAfterRender = 64 << 20

func TestHeapInuseAfter4096Render(t *testing.T) {
	if os.Getenv(heapChildEnv) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHeapInuseAfter4096Render$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), heapChildEnv+"=1")
		out, err := cmd.CombinedOutput()
		t.Logf("child:\n%s", out)
		if err != nil {
			t.Fatalf("the measuring child failed: %v", err)
		}
		if !strings.Contains(string(out), "HeapInuse after") {
			t.Fatal("the child did not measure")
		}
		return
	}
	var buf bytes.Buffer
	parsed, entries := renderNative(t, &buf, perfDocument(4096), perfPlan(t))
	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)
	t.Logf("HeapInuse after a native %d-node render (%d entries, %d bytes): %d bytes, %.1f MiB (bound %d MiB)", parsed, entries, buf.Len(), ms.HeapInuse, float64(ms.HeapInuse)/(1<<20), maxHeapInuseAfterRender>>20)
	if ms.HeapInuse >= maxHeapInuseAfterRender {
		t.Errorf("HeapInuse is %d bytes after the render, not under %d", ms.HeapInuse, maxHeapInuseAfterRender)
	}
	goruntime.KeepAlive(buf.Bytes())
}
