package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/parse"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/producers"
	"github.com/LatticeNet/lattice-sdk/model"
)

// nativeProducerTargets are the five targets S1 produces natively, by the
// platform name the plugin and the harness call them.
var nativeProducerTargets = []string{"URI", "V2Ray", "JSON", "sing-box", "ClashMeta"}

func nativeProducer(t *testing.T, platform string) producers.Producer {
	t.Helper()
	p, ok := producers.Lookup(platform)
	if !ok {
		t.Fatalf("%s has no native producer", platform)
	}
	return p
}

// The zero-node check compares an output with the empty document only when
// the output is at most subStoreMaxEmptyDocumentBytes long, which
// TestEmptyDocumentsFitTheZeroNodeBound holds the bundle to. The native
// producers meet the same bound under the same two option sets, and Produce
// of no nodes writes exactly EmptyDocument with no entries.
func TestEmptyDocumentsMatchBundleBound(t *testing.T) {
	for _, target := range nativeProducerTargets {
		t.Run(target, func(t *testing.T) {
			p := nativeProducer(t, target)
			for _, opts := range []producers.Options{{}, {"include-unsupported-proxy": true, "pretty-yaml": true}} {
				empty := p.EmptyDocument(opts)
				if size := len(strings.TrimSpace(string(empty))); size > subStoreMaxEmptyDocumentBytes {
					t.Errorf("%v: the empty document is %d bytes; the zero-node check only looks at outputs up to %d", opts, size, subStoreMaxEmptyDocumentBytes)
				}
				var buf bytes.Buffer
				res, err := p.Produce(&buf, nil, target, opts)
				if err != nil || res.Entries != 0 || !bytes.Equal(buf.Bytes(), empty) {
					t.Errorf("%v: Produce of no nodes gave %q, %d entries, err %v; want EmptyDocument %q and no entries", opts, buf.Bytes(), res.Entries, err, empty)
				}
			}
		})
	}
}

// A native render of 4096 nodes stays inside the response bound the server
// accepts (model.MaxSubscriptionResponseBytes) and, carried as the JSON body
// of the render reply, inside the render method's acked stdout budget (S1
// plan section 5.2). The nodes are the perf gate's VLESS Reality mix.
func TestDocumentSizesAt4096Nodes(t *testing.T) {
	const count = 4096
	raw := perfgen.Nodes(count)
	stdout := ackedRuntimeBudgets()[pluginID+"/subscription/render"].StdoutBytes
	for _, target := range nativeProducerTargets {
		t.Run(target, func(t *testing.T) {
			p := nativeProducer(t, target)
			nodes := make([]*nodemodel.Node, len(raw))
			for i, r := range raw {
				nodes[i] = &nodemodel.Node{}
				if err := json.Unmarshal(r, nodes[i]); err != nil {
					t.Fatal(err)
				}
			}
			var buf bytes.Buffer
			res, err := p.Produce(&buf, nodes, target, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := count
			if target == "sing-box" {
				// sing-box has no xhttp transport (singbox.md, row F6), so
				// the mix's xhttp nodes yield nothing there.
				for _, n := range nodes {
					if n.Fields["network"] == "xhttp" {
						want--
					}
				}
			}
			if res.Entries != want || len(res.Dropped) != count-want {
				t.Errorf("produced %d entries for %d nodes, want %d (dropped %d)", res.Entries, count, want, len(res.Dropped))
			}
			body, err := json.Marshal(buf.String())
			if err != nil {
				t.Fatal(err)
			}
			if buf.Len() > model.MaxSubscriptionResponseBytes {
				t.Errorf("document is %d bytes, over the %d byte response bound", buf.Len(), model.MaxSubscriptionResponseBytes)
			}
			if len(body) > stdout {
				t.Errorf("render body is %d bytes, over the %d byte render stdout budget", len(body), stdout)
			}
			t.Logf("%s at %d nodes: document %d bytes, render body %d bytes (bounds %d and %d)", target, count, buf.Len(), len(body), model.MaxSubscriptionResponseBytes, stdout)
		})
	}
}

// A fetch of 4096 nodes envelopes the provider text and the parsed nodes
// (snapshotEnvelope.Nodes, the compact model before the chain) inside the
// core's raw bound, so nodes_omitted: size is not needed at the fleet's
// largest record, and a render from the envelope's nodes writes the same
// document as a live render of the text (S1 plan section 5.2). The nodes are
// the perf gate's VLESS Reality mix through the perf gate's chain.
func TestEnvelopeAt4096NodesFitsRawBound(t *testing.T) {
	doc := perfDocument(4096)
	plan := perfPlan(t)
	parsed, _, err := parse.Document(doc, parse.Options{})
	if err != nil {
		t.Fatal(err)
	}
	env := textEnvelope(kindSub, doc, "")
	for _, n := range parsed {
		b, err := n.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		env.Nodes = append(env.Nodes, b)
	}
	raw, err := encodeSnapshotEnvelope(env)
	if err != nil {
		t.Fatalf("the 4096-node envelope does not fit: %v", err)
	}
	t.Logf("4096-node envelope: %d bytes (text %d bytes), bound %d", len(raw), len(doc), model.MaxSubscriptionRawBytes)

	stored, ok := decodeSnapshotEnvelope(raw)
	if !ok || stored.NodesOmitted != "" || len(stored.Nodes) != len(parsed) {
		t.Fatalf("decoded envelope: version 2 %v, nodes_omitted %q, %d nodes; want %d nodes", ok, stored.NodesOmitted, len(stored.Nodes), len(parsed))
	}
	nodes := make([]*nodemodel.Node, len(stored.Nodes))
	for i, b := range stored.Nodes {
		nodes[i] = &nodemodel.Node{}
		if err := json.Unmarshal(b, nodes[i]); err != nil {
			t.Fatal(err)
		}
	}
	var fromEnvelope, live bytes.Buffer
	if _, err := nativeProducer(t, perfTarget).Produce(&fromEnvelope, plan.Run(nodes, &operators.Context{Target: perfTarget}), perfTarget, nil); err != nil {
		t.Fatal(err)
	}
	renderNative(t, &live, doc, plan)
	if !bytes.Equal(fromEnvelope.Bytes(), live.Bytes()) {
		t.Fatalf("the render from the envelope's nodes differs from the live render at byte %d", firstDifferingByte(fromEnvelope.Bytes(), live.Bytes()))
	}
}

func firstDifferingByte(a, b []byte) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}
