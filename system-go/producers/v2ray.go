package producers

import (
	"bytes"
	"encoding/base64"
	"errors"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// The V2Ray producer (specs/producers/v2ray.md): the classic v2rayN
// subscription, the URI producer's per-node links joined by "\n" and
// Base64-encoded as a whole.

type v2rayProducer struct{}

func (v2rayProducer) ID() string { return "v2ray" }

// EmptyDocument is the empty string under every option. It is not the only
// document without links: nodes without a URI form leave empty lines, so a
// list of them gives "Cg==" and so on, and the zero-node rule counts
// Result.Entries instead.
func (v2rayProducer) EmptyDocument(Options) []byte { return []byte{} }

func (v2rayProducer) Produce(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options) (Result, error) {
	ps, dropped := prepare(nodes, target, "v2ray", opts)
	res := Result{Dropped: dropped}
	var text bytes.Buffer
	contributions := 0
	for _, p := range ps {
		line, err := uriLine(p)
		if err != nil {
			res.Dropped = append(res.Dropped, droppedBy(p, err))
			// A node without a URI form contributes an empty line; a
			// failing node contributes nothing.
			if !errors.Is(err, errNoForm) {
				continue
			}
		} else {
			res.Entries++
		}
		if contributions > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(line)
		contributions++
	}
	sortDropped(res.Dropped)
	out := make([]byte, base64.StdEncoding.EncodedLen(text.Len()))
	base64.StdEncoding.Encode(out, text.Bytes())
	dst.Write(out)
	return res, nil
}
