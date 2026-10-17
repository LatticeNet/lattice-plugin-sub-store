package producers

import (
	"bytes"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"gopkg.in/yaml.v3"
)

// The proxies: document ClashMeta, Stash and Shadowrocket share
// (clashmeta.md, stash.md and shadowrocket.md, "Output shape"): the line
// proxies: and one flow mapping line per node, or with prettyYaml a block
// YAML dump with the short-id rewrite (clashmeta_yaml.go). The three differ
// only in their admission filter and their field transforms.

// proxyRules is one proxies: target: admits is its admission filter, which
// include-unsupported-proxy bypasses, and transform rewrites one admitted
// node's fields.
type proxyRules struct {
	id        string
	admits    func(f map[string]any) bool
	transform func(p *prepared)
}

// emptyProxies is "proxies:" and a newline, or "proxies: []" and a newline in
// the pretty form. Neither is the empty string, so the zero-node rule counts
// Result.Entries.
func emptyProxies(opts Options) []byte {
	if prettyYAML(opts) {
		return []byte("proxies: []\n")
	}
	return []byte("proxies:\n")
}

// produceProxies runs the steps for r.id, the admission filter and the
// transforms, and writes the document into dst.
func produceProxies(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options, r proxyRules) (Result, error) {
	ps, dropped := prepare(nodes, target, r.id, opts)
	res := Result{Dropped: dropped}
	include := opts.Truthy("include-unsupported-proxy")
	kept := ps[:0]
	for i := range ps {
		p := &ps[i]
		if !include && !r.admits(p.node.Fields) {
			res.Dropped = append(res.Dropped, Dropped{Index: p.index, Type: typeOf(p.node.Fields), Reason: ReasonUnsupported})
			continue
		}
		r.transform(p)
		kept = append(kept, *p)
	}
	// Only a value outside the model fails to write, which decoded input
	// never holds; such a node is reported instead of failing the document.
	failed := func(p prepared) {
		res.Dropped = append(res.Dropped, Dropped{Index: p.index, Type: typeOf(p.node.Fields), Reason: ReasonFailed})
	}
	var out []byte
	if prettyYAML(opts) {
		items := make([]*yaml.Node, 0, len(kept))
		for _, p := range kept {
			item, err := yamlValue(p.ordered(), 0)
			if err != nil {
				failed(p)
				continue
			}
			items = append(items, item)
		}
		if res.Entries = len(items); res.Entries > 0 {
			var err error
			if out, err = prettyProxies(items); err != nil {
				return Result{}, err
			}
		}
	} else {
		out = []byte("proxies:\n")
		for _, p := range kept {
			next, err := appendJSON(append(out, "  - "...), p.ordered())
			if err != nil {
				failed(p)
				continue
			}
			out = append(next, '\n')
			res.Entries++
		}
	}
	if res.Entries == 0 {
		out = emptyProxies(opts)
	}
	sortDropped(res.Dropped)
	dst.Write(out)
	return res, nil
}

func prettyYAML(opts Options) bool {
	return opts.Truthy("prettyYaml") || opts.Truthy("pretty-yaml")
}

// The transforms the three share, each a row of their specifications.

// dropPipelineFields removes the fields the pipeline adds (ClashMeta T19 and
// the Stash and Shadowrocket common rows).
func dropPipelineFields(f map[string]any) {
	for _, k := range []string{"subName", "collectionName", "id", "resolved", "no-resolve", "ip-cidr", "ipv6-cidr"} {
		delete(f, k)
	}
}

// dropAnnotations removes every top-level null value and every top-level key
// starting with "_", and the http-upgrade record from the node's transport
// options (ClashMeta T20 and the Stash and Shadowrocket common rows).
func dropAnnotations(f map[string]any) {
	for k, v := range f {
		if v == nil || len(k) > 0 && k[0] == '_' {
			delete(f, k)
		}
	}
	key := textOf(f, "network") + "-opts"
	if o, ok := obj(f, key); ok {
		if _, ok := o["_v2ray-http-upgrade-ed"]; ok {
			delete(own(f, key), "_v2ray-http-upgrade-ed")
		}
	}
}

// dropGRPCAnnotations removes the parser's two annotations from grpc-opts
// (ClashMeta T21 and the Stash and Shadowrocket transport rows).
func dropGRPCAnnotations(f map[string]any) {
	if f["network"] != "grpc" {
		return
	}
	if g, ok := obj(f, "grpc-opts"); ok {
		_, a := g["_grpc-type"]
		_, b := g["_grpc-authority"]
		if a || b {
			g = own(f, "grpc-opts")
			delete(g, "_grpc-type")
			delete(g, "_grpc-authority")
		}
	}
}

// dropNonBooleanTLS removes a tls value that is not a boolean (ClashMeta T18
// and the Stash and Shadowrocket common rows).
func dropNonBooleanTLS(f map[string]any) {
	if v, ok := f["tls"]; ok {
		if _, isBool := v.(bool); !isBool {
			delete(f, "tls")
		}
	}
}

// streamTransports applies the http and h2 rows to vmess and vless (ClashMeta
// T11 and T12, and the Stash and Shadowrocket transport rows) and the ws row
// to any node (T13).
func streamTransports(p *prepared, typ string) {
	f := p.node.Fields
	if typ == "vmess" || typ == "vless" {
		switch f["network"] {
		case "http":
			httpOptsLists(f)
		case "h2":
			h2OptsHost(f)
		}
	}
	if f["network"] == "ws" {
		wsEarlyData(p)
	}
}

// vmessAEAD is the vmess aead row: alterId 0 when aead is set, and aead
// removed either way.
func vmessAEAD(p *prepared) {
	f := p.node.Fields
	if v, ok := f["aead"]; ok {
		if truthy(v) {
			p.put("alterId", float64(0))
		}
		delete(f, "aead")
	}
}

// tuicVersion sets version 5 on a tuic node without a token or version.
func tuicVersion(p *prepared) {
	f := p.node.Fields
	if !truthy(f["token"]) {
		if _, ok := f["version"]; !ok {
			p.put("version", float64(5))
		}
	}
}
