package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/producers"
	"github.com/LatticeNet/lattice-sdk/model"
)

// nativeProducerTargets are the five targets S1 produces natively, by the
// platform name the plugin and the harness call them. pending names the step
// that brings a target's producer; a target that gains one fails here until
// its pending note goes, so none is skipped silently.
var nativeProducerTargets = []struct{ platform, pending string }{
	{"URI", ""},
	{"V2Ray", ""},
	{"JSON", "lane 4 step B"},
	{"sing-box", "lane 4 step B"},
	{"ClashMeta", "lane 4 step B"},
}

func nativeProducer(t *testing.T, platform, pending string) producers.Producer {
	t.Helper()
	p, ok := producers.Lookup(platform)
	if pending != "" {
		if ok {
			t.Fatalf("%s has a native producer: drop its pending note", platform)
		}
		t.Skipf("pending: the native %s producer lands in %s", platform, pending)
	}
	if !ok {
		t.Fatalf("%s has no native producer and no pending note", platform)
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
		t.Run(target.platform, func(t *testing.T) {
			p := nativeProducer(t, target.platform, target.pending)
			for _, opts := range []producers.Options{{}, {"include-unsupported-proxy": true, "pretty-yaml": true}} {
				empty := p.EmptyDocument(opts)
				if size := len(strings.TrimSpace(string(empty))); size > subStoreMaxEmptyDocumentBytes {
					t.Errorf("%v: the empty document is %d bytes; the zero-node check only looks at outputs up to %d", opts, size, subStoreMaxEmptyDocumentBytes)
				}
				var buf bytes.Buffer
				res, err := p.Produce(&buf, nil, target.platform, opts)
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
		t.Run(target.platform, func(t *testing.T) {
			p := nativeProducer(t, target.platform, target.pending)
			nodes := make([]*nodemodel.Node, len(raw))
			for i, r := range raw {
				nodes[i] = &nodemodel.Node{}
				if err := json.Unmarshal(r, nodes[i]); err != nil {
					t.Fatal(err)
				}
			}
			var buf bytes.Buffer
			res, err := p.Produce(&buf, nodes, target.platform, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.Entries != count {
				t.Errorf("produced %d entries for %d nodes (dropped %v)", res.Entries, count, res.Dropped)
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
			t.Logf("%s at %d nodes: document %d bytes, render body %d bytes (bounds %d and %d)", target.platform, count, buf.Len(), len(body), model.MaxSubscriptionResponseBytes, stdout)
		})
	}
}
