package normalise

import (
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// realityNode is the shape most of the fleet runs: VLESS Reality over TCP.
const realityNode = `{"name":"JP 01 Reality","type":"vless","server":"edge-01.example.net","port":443,` +
	`"uuid":"5840f636-1f16-4327-8520-2c6d190b4fb9","network":"tcp","tls":true,"udp":true,"flow":"xtls-rprx-vision",` +
	`"sni":"www.example.com","client-fingerprint":"chrome","skip-cert-verify":false,"alpn":["h2","http/1.1"],` +
	`"reality-opts":{"public-key":"2INb8aTAxDnTjtFZPu1cHNrvLO9oMJ7yXr6nlSPpn1g","short-id":"ab12cd34","_spider-x":"/"}}`

// BenchmarkNormaliseRealityNode is the node stage per parsed node, including
// the clone a benchmark needs to start from the same input each time.
func BenchmarkNormaliseRealityNode(b *testing.B) {
	base := &nodemodel.Node{}
	if err := base.UnmarshalJSON([]byte(realityNode)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if drop, reason, err := Node(base.Clone(), false); drop || err != nil {
			b.Fatal(reason, err)
		}
	}
}

// BenchmarkBoundsRealityNode is H4 alone on an accepted node, the common case.
func BenchmarkBoundsRealityNode(b *testing.B) {
	n := &nodemodel.Node{}
	if err := n.UnmarshalJSON([]byte(realityNode)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := nodemodel.Bounds(n); err != nil {
			b.Fatal(err)
		}
	}
}
