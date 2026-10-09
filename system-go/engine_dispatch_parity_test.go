package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
	"gopkg.in/yaml.v3"
)

// checkerForms is how the vendored checker compares each native target
// (conformance/oracle/check.mjs and oracle/lib/canonical.mjs): URI and V2Ray
// byte for byte, the others parsed back and compared as values with object
// key order ignored and list order kept.
var checkerForms = map[string]string{"URI": "bytes", "V2Ray": "bytes", "JSON": "json", "sing-box": "json", "ClashMeta": "yaml"}

// checkerValue parses a document as the checker does: JSON.parse for json,
// a YAML 1.2 document for yaml, then the JSON value of it.
func checkerValue(t *testing.T, form, document string) any {
	t.Helper()
	var v any
	switch form {
	case "json":
		if err := json.Unmarshal([]byte(document), &v); err != nil {
			t.Fatalf("document does not parse as JSON: %v\n%s", err, head(document, 200))
		}
		return v
	case "yaml":
		if err := yaml.Unmarshal([]byte(document), &v); err != nil {
			t.Fatalf("document does not parse as YAML: %v\n%s", err, head(document, 200))
		}
		return checkerJSONValue(v)
	}
	t.Fatalf("no checker form %q", form)
	return nil
}

// checkerJSONValue is JSON.parse(JSON.stringify(v)) over a decoded YAML value.
func checkerJSONValue(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = checkerJSONValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = checkerJSONValue(e)
		}
		return out
	}
	return v
}

// checkerDiff is the first path at which two values differ, as check.mjs's
// firstDiff reports it, or "" when they are equal.
func checkerDiff(a, b any, path string) string {
	if reflect.DeepEqual(a, b) {
		return ""
	}
	switch x := a.(type) {
	case []any:
		if y, ok := b.([]any); ok {
			for i := 0; i < max(len(x), len(y)); i++ {
				p := path + "[" + strconv.Itoa(i) + "]"
				if i >= len(x) || i >= len(y) {
					return p
				}
				if d := checkerDiff(x[i], y[i], p); d != "" {
					return d
				}
			}
		}
	case map[string]any:
		if y, ok := b.(map[string]any); ok {
			keys := slices.Sorted(maps.Keys(x))
			for k := range y {
				if _, ok := x[k]; !ok {
					keys = append(keys, k)
				}
			}
			slices.Sort(keys)
			for _, k := range keys {
				xv, inX := x[k]
				yv, inY := y[k]
				if !inX || !inY {
					return path + "." + k
				}
				if d := checkerDiff(xv, yv, path+"."+k); d != "" {
					return d
				}
			}
		}
	}
	return fmt.Sprintf("%s (%v against %v)", path, a, b)
}

// checkerAgree reports where a native and a bundle document of one target
// differ under the checker's rule, or "" when they agree.
func checkerAgree(t *testing.T, target, native, bundle string) string {
	t.Helper()
	form := checkerForms[target]
	if form == "bytes" {
		if native == bundle {
			return ""
		}
		at := 0
		for at < len(native) && at < len(bundle) && native[at] == bundle[at] {
			at++
		}
		return fmt.Sprintf("byte %d: native %q, bundle %q", at, head(native[at:], 120), head(bundle[at:], 120))
	}
	return checkerDiff(checkerValue(t, form, native), checkerValue(t, form, bundle), "$")
}

// parityRecord is a record the fleet would hold: perfgen's VLESS Reality
// links across their transports plus one node of each other common type,
// under a native chain.
func parityRecord(n int) (string, []json.RawMessage) {
	return parityDocument(n), steps(
		`{"type":"Regex Rename Operator","args":[{"expr":"^(\\w+) (\\w+) (\\d+)$","now":"$1 $3"}]}`,
		`{"type":"Sort Operator","args":"asc"}`,
		`{"type":"Flag Operator","args":{"mode":"add"}}`,
	)
}

// paritySSLink is parityDocument's plain Shadowsocks link, the one node on
// which the two parsers disagree.
const paritySSLink = "ss://YWVzLTEyOC1nY206cGFzcw==@192.0.2.11:8388#SG ss 9002"

// A native render and a bundle render of the same record and target agree
// under the checker's comparison rule for every native target, with and
// without the record's chain. The two engines parse independently (the
// native parser follows the 2.42.3 specifications, the bundle is 2.36.22),
// so this is the end-to-end check that a record moving onto the native path
// serves its clients what the bundle served them.
//
// One difference is known and held separately: a plain Shadowsocks URI parses
// with udp true natively, as the 2.42.3 goldens have it
// (conformance/goldens/parse/ss-*.json), and with udp false on the 2.36.22
// bundle, so URI gains ?udp=1, JSON and ClashMeta say udp: true, and sing-box
// drops its network "tcp". When a bundle re-pin closes it, the second half of
// the test says so and the node joins the compared record.
func TestNativeAndBundleRendersAgreeUnderCheckerRule(t *testing.T) {
	rt := dispatchRuntime(t)
	document, chain := parityRecord(64)
	if !strings.Contains(document, paritySSLink+"\n") {
		t.Fatal("parityDocument no longer carries the plain Shadowsocks link")
	}
	document = strings.Replace(document, paritySSLink+"\n", "", 1)
	for _, withChain := range []bool{false, true} {
		rec := savedRecord(t, rt, subscriptionRecord{ID: fmt.Sprintf("parity-%v", withChain), Content: document})
		if withChain {
			rec = savedRecord(t, rt, subscriptionRecord{ID: rec.ID, Content: document, Process: chain})
		}
		plan, err := rt.chainPlan(rec)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range nativeTargets {
			native, served, err := rt.convertNodes(nodeConvertRequest{Parts: []string{document}, Target: target, Plan: plan})
			if err != nil || served != servedNative {
				t.Fatalf("%s: native render served %s err %v", target, served, err)
			}
			bundle, err := rt.engine.convertIsolated(subStoreConversionRequest{Raw: document, Target: target, Operators: bundleOperators(plan)})
			if err != nil {
				t.Fatalf("%s: bundle render: %v", target, err)
			}
			if native.NodeCount != bundle.NodeCount || native.ZeroNodes != bundle.ZeroNodes {
				t.Fatalf("%s chain=%v: native %d nodes zero=%v, bundle %d nodes zero=%v", target, withChain, native.NodeCount, native.ZeroNodes, bundle.NodeCount, bundle.ZeroNodes)
			}
			if diff := checkerAgree(t, target, native.Output, bundle.Output); diff != "" {
				t.Errorf("%s chain=%v: native and bundle renders differ at %s", target, withChain, diff)
			}
		}
	}

	native, _, err := rt.convertNodes(nodeConvertRequest{Parts: []string{paritySSLink}, Target: "JSON"})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := rt.engine.convertIsolated(subStoreConversionRequest{Raw: paritySSLink, Target: "JSON"})
	if err != nil {
		t.Fatal(err)
	}
	udp := func(document string) any {
		nodes, _ := checkerValue(t, "json", document).([]any)
		if len(nodes) != 1 {
			t.Fatalf("want one node: %s", document)
		}
		return nodes[0].(map[string]any)["udp"]
	}
	if n, b := udp(native.Output), udp(bundle.Output); n != true || b != false {
		t.Errorf("Shadowsocks udp is native %v, bundle %v; the known difference changed, so fold the node back into the compared record", n, b)
	}
}

// A native record rendered from its version 2 envelope equals its live
// render byte for byte, for every native target, a subscription and a
// collection alike, and reads the envelope's nodes rather than parsing its
// text again: with the text replaced by something that is not a subscription
// the render is still the same.
func TestSnapshotRenderParityNative(t *testing.T) {
	rt := dispatchRuntime(t)
	document, chain := parityRecord(32)
	savedRecord(t, rt, subscriptionRecord{ID: "native", Source: subscriptionSourceLocal, Content: document, Process: chain})
	savedRecord(t, rt, subscriptionRecord{ID: "plain", Source: subscriptionSourceLocal, Content: shareFleetFixture(4)})
	savedRecord(t, rt, subscriptionRecord{ID: "coll", Kind: kindCollection, Members: []string{"native", "plain"}, Process: steps(`{"type":"Sort Operator","args":"desc"}`)})
	warm, isolated := rt.engine.pathCounts()
	for _, id := range []string{"native", "coll"} {
		snap, err := rt.fetchSubscription(id)
		if err != nil {
			t.Fatalf("fetch %s: %v", id, err)
		}
		env, ok := decodeSnapshotEnvelope(snap.Raw)
		if !ok || env.NodesOmitted != "" {
			t.Fatalf("%s: fetch wrote no version 2 envelope with nodes: %.160s", id, snap.Raw)
		}
		probe := env
		probe.Raw = "this is not a subscription"
		probe.Members = append([]envelopeMember(nil), env.Members...)
		for i := range probe.Members {
			probe.Members[i].Raw = "this is not a subscription"
		}
		if id == "native" && len(env.Nodes) == 0 || id == "coll" && (len(env.Members) != 2 || len(env.Members[0].Nodes) == 0 || len(env.Members[1].Nodes) == 0) {
			t.Fatalf("%s: the envelope carries no nodes: %.160s", id, snap.Raw)
		}
		probeRaw, err := json.Marshal(probe)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range nativeTargets {
			render := func(raw string) string {
				t.Helper()
				out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: id, Target: target, Format: "plain", Raw: raw})
				if err != nil {
					t.Fatalf("%s %s: render: %v", id, target, err)
				}
				return out.Content
			}
			live := render("")
			if fromSnapshot := render(snap.Raw); fromSnapshot != live {
				t.Fatalf("%s %s: snapshot render differs from live at %s", id, target, checkerAgree(t, "URI", fromSnapshot, live))
			}
			if fromNodes := render(string(probeRaw)); fromNodes != live {
				t.Fatalf("%s %s: the snapshot render did not read the envelope's nodes", id, target)
			}
		}
	}
	if w, i := rt.engine.pathCounts(); w != warm || i != isolated {
		t.Fatalf("native fetches and renders reached the bundle: warm %d->%d isolated %d->%d", warm, w, isolated, i)
	}
}

// An envelope whose nodes would pass the raw bound leaves them out with
// nodes_omitted "size", and the render parses the text instead.
func TestEnvelopeLeavesNodesOutAtTheRawBound(t *testing.T) {
	links := perfgen.URIs(4096)
	nodes := perfgen.Nodes(4096)
	env := textEnvelope(kindSub, strings.Join(links, "\n"), "")
	env.Nodes = nodes
	for len(env.Raw) < 3<<20 {
		env.Raw += "\n" + env.Raw[:len(env.Raw)/2]
	}
	raw, err := encodeSnapshotEnvelope(env)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, ok := decodeSnapshotEnvelope(raw)
	if !ok || len(decoded.Nodes) != 0 || decoded.NodesOmitted != nodesOmittedSize {
		t.Fatalf("nodes kept past the bound: %d nodes, omitted %q", len(decoded.Nodes), decoded.NodesOmitted)
	}
	if envelopeNodes(decoded) != nil {
		t.Fatal("a size-omitted envelope offered nodes")
	}
}

// A chain that runs natively is previewed in Go with what previewScript
// computes on the bundle: the same reduction to the visible fields before
// the chain (so a step that reads a credential sees it absent), the same
// summaries, and the same pairing of each kept node with its source and of
// renamed nodes with their earlier names.
func TestNativePreviewMatchesBundlePreview(t *testing.T) {
	rt := dispatchRuntime(t)
	// Without the plain Shadowsocks link, whose udp flag the two parsers
	// read differently (TestNativeAndBundleRendersAgreeUnderCheckerRule).
	document := strings.Replace(parityDocument(48), paritySSLink+"\n", "", 1)
	for _, chain := range [][]json.RawMessage{
		nil,
		steps(`{"type":"Regex Rename Operator","args":[{"expr":"^(\\w+) (\\w+) (\\d+)$","now":"$1 $3"}]}`),
		steps(`{"type":"Regex Filter","args":{"regex":["(?i)^(hk|jp)"],"keep":true}}`, `{"type":"Flag Operator","args":{"mode":"add"}}`),
		// uuid is not visible to a reduced preview: the filter matches nothing
		// there, and one node when the content was already synthetic.
		steps(`{"type":"Conditional Filter","args":{"rule":{"proposition":"EQUALS","attr":"uuid","value":"0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35"}}}`),
		steps(`{"type":"Conditional Filter","args":{"rule":{"proposition":"EQUALS","attr":"type","value":"trojan"}}}`),
		steps(`{"type":"Handle Duplicate Operator","args":{"action":"rename","field":["type"],"position":"front","link":"_"}}`),
	} {
		for _, reduce := range []bool{true, false} {
			native, err := rt.previewSubscription(document, chain, "URI", !reduce)
			if err != nil {
				t.Fatalf("native preview: %v", err)
			}
			out, err := rt.engine.runCoreScript("preview", "preview.js", previewScript(document, chain, "URI", reduce))
			if err != nil {
				t.Fatalf("bundle preview: %v", err)
			}
			var bundle previewNodes
			if err := json.Unmarshal([]byte(out), &bundle); err != nil {
				t.Fatal(err)
			}
			if native.SourceNodeCount != bundle.SourceNodeCount || !reflect.DeepEqual(native.Nodes, bundle.Nodes) || !reflect.DeepEqual(native.Dropped, bundle.Dropped) && len(native.Dropped)+len(bundle.Dropped) > 0 {
				t.Errorf("chain %s reduce=%v: native %d nodes, %d dropped; bundle %d nodes, %d dropped\n native %+v\n bundle %+v",
					chain, reduce, len(native.Nodes), len(native.Dropped), len(bundle.Nodes), len(bundle.Dropped), head(fmt.Sprint(native.Nodes), 300), head(fmt.Sprint(bundle.Nodes), 300))
			}
		}
	}
}
