package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/parse"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/producers"
)

// The perf gate (S1 plan section 5.2) times design 28's measure: 1000 and
// 4096 of perfgen's VLESS Reality nodes through four non-script operators to
// sing-box output. The chain is compiled once, as a record revision's plan
// is, and runs over a fresh copy of the nodes each iteration (it renames in
// place); the producer then writes the document. The parse of the same
// nodes' share links is timed on its own (BenchmarkParseReality1000 and
// 4096): design 28 bounds it inside the provider fetch and the render round
// trip, not in this measure, and here it is held to the regression rule.
// tools/perfgate holds the medians of the two pipeline benchmarks under 20 ms
// and 100 ms and every benchmark under 1.5 times the baseline committed in
// testdata/bench/ubuntu-24.04.txt.
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

// perfNodes is perfgen's first n nodes in the model.
func perfNodes(tb testing.TB, n int) []*nodemodel.Node {
	tb.Helper()
	raw := perfgen.Nodes(n)
	nodes := make([]*nodemodel.Node, len(raw))
	for i, r := range raw {
		nodes[i] = &nodemodel.Node{}
		if err := json.Unmarshal(r, nodes[i]); err != nil {
			tb.Fatal(err)
		}
	}
	return nodes
}

// renderNodes runs the chain over nodes, in place, and writes the sing-box
// document. It returns how many entries the document has.
func renderNodes(tb testing.TB, dst *bytes.Buffer, nodes []*nodemodel.Node, plan *operators.Plan) int {
	tb.Helper()
	nodes = plan.Run(nodes, &operators.Context{Target: perfTarget})
	p, ok := producers.Lookup(perfTarget)
	if !ok {
		tb.Fatalf("%s has no native producer", perfTarget)
	}
	res, err := p.Produce(dst, nodes, perfTarget, nil)
	if err != nil {
		tb.Fatal(err)
	}
	return res.Entries
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
	return len(nodes), renderNodes(tb, dst, nodes, plan)
}

// copyNodes fills dst with fresh copies of src.
func copyNodes(dst, src []*nodemodel.Node) {
	for i, n := range src {
		dst[i] = n.Clone()
	}
}

func benchmarkPipeline(b *testing.B, n int) {
	src := perfNodes(b, n)
	nodes := make([]*nodemodel.Node, n)
	plan := perfPlan(b)
	var buf bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		copyNodes(nodes, src)
		buf.Reset()
		b.StartTimer()
		renderNodes(b, &buf, nodes, plan)
	}
}

func BenchmarkPipelineReality1000SingBox(b *testing.B) { benchmarkPipeline(b, 1000) }

func BenchmarkPipelineReality4096SingBox(b *testing.B) { benchmarkPipeline(b, 4096) }

func benchmarkParse(b *testing.B, n int) {
	doc := perfDocument(n)
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := parse.Document(doc, parse.Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseReality1000(b *testing.B) { benchmarkParse(b, 1000) }

func BenchmarkParseReality4096(b *testing.B) { benchmarkParse(b, 4096) }

// TestPerfPipelineRendersTheChain keeps the benchmarks honest: the parse
// benchmark's links parse to exactly the pipeline benchmark's nodes, the
// Regex Filter keeps the eight countries' nodes, sing-box writes every kept
// node except the XHTTP ones, which it has no transport for (singbox.md, row
// F6), and every outbound carries its flag and its renamed name, in
// ascending order.
func TestPerfPipelineRendersTheChain(t *testing.T) {
	const n = 1000
	parsed, _, err := parse.Document(perfDocument(n), parse.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var fromLinks, fromNodes []any
	if wire, err := json.Marshal(parsed); err != nil || json.Unmarshal(wire, &fromLinks) != nil {
		t.Fatalf("encode the parsed nodes: %v", err)
	}
	var want int
	for _, raw := range perfgen.Nodes(n) {
		var v any
		var node struct{ Name, Network string }
		if json.Unmarshal(raw, &v) != nil || json.Unmarshal(raw, &node) != nil {
			t.Fatalf("perfgen node %s is not JSON", raw)
		}
		fromNodes = append(fromNodes, v)
		switch node.Name[:3] {
		case "HK ", "JP ", "SG ", "US ", "TW ", "KR ", "DE ", "GB ":
			if node.Network != perfgen.XHTTP {
				want++
			}
		}
	}
	if !reflect.DeepEqual(fromLinks, fromNodes) {
		t.Fatalf("the %d links parse to %d nodes that are not perfgen's nodes", n, len(fromLinks))
	}
	var buf bytes.Buffer
	if entries := renderNodes(t, &buf, perfNodes(t, n), perfPlan(t)); entries != want || want == 0 || want == n {
		t.Fatalf("produced %d entries from %d nodes; want %d", entries, n, want)
	}
	var doc struct {
		Outbounds []struct {
			Tag string
			TLS struct{ Reality struct{ Enabled bool } }
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil || len(doc.Outbounds) != want {
		t.Fatalf("the document has %d outbounds (%v), want %d", len(doc.Outbounds), err, want)
	}
	tag := regexp.MustCompile(`^[\x{1F1E6}-\x{1F1FF}]{2} ((?:HK|JP|SG|US|TW|KR|DE|GB) \d{4} [a-z][a-z0-9]{4})$`)
	var names []string
	for _, o := range doc.Outbounds {
		m := tag.FindStringSubmatch(o.Tag)
		if m == nil || !o.TLS.Reality.Enabled {
			t.Fatalf("outbound %q is not a flagged, renamed Reality outbound", o.Tag)
		}
		names = append(names, m[1])
	}
	if !slices.IsSorted(names) {
		t.Fatalf("the outbounds are not in ascending order: %q", names[:min(len(names), 8)])
	}
}

// pipelineAllocsPerNodeBound bounds the allocations of the 1000-node chain
// and producer, per input node, without the copy the benchmark makes first:
// the first measurement, 22.5 on go1.26.9, rounded up by a quarter. Raise it
// only with the reason in the commit.
const pipelineAllocsPerNodeBound = 29

func TestPipelineAllocsPerNodeBound(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates on its own account")
	}
	const n = 1000
	src := perfNodes(t, n)
	nodes := make([]*nodemodel.Node, n)
	plan := perfPlan(t)
	var buf bytes.Buffer
	copies := testing.AllocsPerRun(5, func() { copyNodes(nodes, src) })
	allocs := testing.AllocsPerRun(5, func() {
		copyNodes(nodes, src)
		buf.Reset()
		renderNodes(t, &buf, nodes, plan)
	}) - copies
	perNode := allocs / n
	t.Logf("%.0f allocations per 1000-node chain and render, %.1f per node (bound %d)", allocs, perNode, pipelineAllocsPerNodeBound)
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
