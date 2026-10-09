package producers

import (
	"bytes"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// The JSON producer (specs/producers/json.md): the model itself after the
// steps before the producer, as JSON.stringify(list, null, 2) writes it.
// There is no field mapping and no type rule, so a node is left out only when
// the steps drop it.
//
// Keys are written as upstream writes them (json.md, "Ordering"): the
// received keys in ECMAScript property order, then the keys the steps added
// (_resolved, a filled port, the WireGuard prefixes) in the order they were
// added. The structural comparison sorts keys, so the order is not part of
// conformance.

type jsonProducer struct{}

func (jsonProducer) ID() string { return "json" }

// EmptyDocument is "[]" under every option (json.md, "Empty document").
func (jsonProducer) EmptyDocument(Options) []byte { return []byte("[]") }

func (jsonProducer) Produce(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options) (Result, error) {
	ps, dropped := prepare(nodes, target, "json", opts)
	res := Result{Dropped: dropped}
	out := []byte{'['}
	for _, p := range ps {
		mark := len(out)
		if res.Entries > 0 {
			out = append(out, ',')
		}
		out = append(out, "\n  "...)
		next, err := appendJSONIndent(out, p.ordered(), "  ")
		if err != nil {
			// Only a value outside the model fails, which decoded input
			// never holds; the node is reported instead of the document.
			out = out[:mark]
			res.Dropped = append(res.Dropped, Dropped{Index: p.index, Type: typeOf(p.node.Fields), Reason: ReasonFailed})
			continue
		}
		out = next
		res.Entries++
	}
	if res.Entries > 0 {
		out = append(out, '\n')
	}
	out = append(out, ']')
	sortDropped(res.Dropped)
	dst.Write(out)
	return res, nil
}
