package producers

import (
	"bytes"
	"maps"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"gopkg.in/yaml.v3"
)

// The ClashMeta producer (specs/producers/clashmeta.md): a mihomo proxies:
// list. After the steps before the producer, an admission filter (A1 to A12)
// drops what mihomo cannot load, the transforms (clashmeta_transforms.go)
// restore mihomo's field shapes, and each node is written as one flow mapping
// line, or as a block YAML dump with the prettyYaml option
// (clashmeta_yaml.go).
//
// Keys are written in upstream's order (clashmeta.md, "Ordering"): the keys
// the producer received in ECMAScript property order, then the keys the steps
// and the transforms created (servername, client-fingerprint and so on) in the
// order they were created (prepared.put). The structural comparison sorts
// keys, but the pretty form's short-id rewrite is textual, and one golden
// (clash-pretty-name-contains-short-id) is compared as text because of it.

type clashMetaProducer struct{}

func (clashMetaProducer) ID() string { return "clashmeta" }

// EmptyDocument is "proxies:" and a newline, or "proxies: []" and a newline
// in the pretty form (clashmeta.md, "Empty document"). Neither is the empty
// string, so the zero-node rule counts Result.Entries.
func (clashMetaProducer) EmptyDocument(opts Options) []byte {
	if prettyYAML(opts) {
		return []byte("proxies: []\n")
	}
	return []byte("proxies:\n")
}

func (c clashMetaProducer) Produce(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options) (Result, error) {
	ps, dropped := prepare(nodes, target, "clashmeta", opts)
	res := Result{Dropped: dropped}
	include := opts.Truthy("include-unsupported-proxy")
	kept := ps[:0]
	for i := range ps {
		p := &ps[i]
		if !include && !clashMetaAdmits(p.node.Fields) {
			res.Dropped = append(res.Dropped, Dropped{Index: p.index, Type: typeOf(p.node.Fields), Reason: ReasonUnsupported})
			continue
		}
		clashMetaTransform(p, false)
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
			mark := len(out)
			out = append(out, "  - "...)
			var err error
			if out, err = appendJSON(out, p.ordered()); err != nil {
				out = out[:mark]
				failed(p)
				continue
			}
			out = append(out, '\n')
			res.Entries++
		}
	}
	if res.Entries == 0 {
		out = c.EmptyDocument(opts)
	}
	sortDropped(res.Dropped)
	dst.Write(out)
	return res, nil
}

func prettyYAML(opts Options) bool {
	return opts.Truthy("prettyYaml") || opts.Truthy("pretty-yaml")
}

// ClashMetaInternal is the ClashMeta producer's internal mode, which the
// sing-box producer runs first (singbox.md, "Pipeline"): every node, because
// include-unsupported-proxy is forced on and the admission filter never
// applies, goes through the transforms T1 to T22 and W except T20, so null
// values and underscore keys stay on the node. nodes are the output of the
// steps before the producer; the result holds new nodes in the same order and
// the input is not changed. The transforms read no option, so opts changes
// nothing.
func ClashMetaInternal(nodes []*nodemodel.Node, opts Options) []*nodemodel.Node {
	out := make([]*nodemodel.Node, len(nodes))
	for i, n := range nodes {
		var src map[string]any
		c := &nodemodel.Node{}
		if n != nil {
			src = n.Fields
			c.Script, c.Lattice = n.Script, n.Lattice
		}
		c.Fields = make(map[string]any, len(src)+4)
		maps.Copy(c.Fields, src)
		clashMetaTransform(&prepared{index: i, node: c}, true)
		out[i] = c
	}
	return out
}

// clashMetaAdmits is the admission filter A1 to A12 (clashmeta.md,
// "Admission filter"): false drops the node.
func clashMetaAdmits(f map[string]any) bool {
	typ, _ := f["type"].(string)
	switch typ {
	case "h2-connect", "masque-surge", "juicity", "naive": // A1, A2, A9
		return false
	case "trusttunnel": // A3
		if h, ok := obj(f, "headers"); ok && len(h) > 0 {
			return false
		}
	case "ss":
		if f["plugin"] == "v2ray-plugin" { // A4
			po, _ := obj(f, "plugin-opts")
			mode := ""
			if v, ok := po["mode"]; ok {
				mode = strings.ToLower(trimES(text(v)))
			}
			if mode != "websocket" {
				return false
			}
		}
	case "snell":
		if v, ok := f["version"]; ok && !integerIn(v, 1, 5) { // A5
			return false
		}
	}
	// A6: shadow-tls on a type that cannot carry it, or of a version mihomo
	// does not know.
	if block, ok := shadowTLSBlock(f, typ); ok {
		lo := 0.0
		switch typ {
		case "ss", "snell":
			lo = 1
		case "vmess", "vless", "trojan", "anytls":
		default:
			return false
		}
		if v, ok := block["version"]; ok && !integerIn(v, lo, 3) {
			return false
		}
	}
	if typ == "vless" && f["network"] == "xhttp" { // A7
		xo, _ := obj(f, "xhttp-opts")
		ds, _ := obj(xo, "download-settings")
		if po, ok := shadowTLSPlugin(ds); ok {
			if v, ok := po["version"]; ok && !integerIn(v, 0, 3) {
				return false
			}
		}
	}
	if typ == "snell" && f["plugin"] == "shadow-tls" { // A8
		oo, _ := obj(f, "obfs-opts")
		for _, k := range []string{"mode", "host", "path"} {
			if _, ok := oo[k]; ok {
				return false
			}
		}
	}
	if typ == "ss" && !ssCiphers[text(f["cipher"])] { // A10
		return false
	}
	if typ == "anytls" { // A11
		if net, ok := f["network"]; ok && truthy(net) && (net != "tcp" || set(f, "reality-opts")) {
			return false
		}
	}
	if typ != "vless" && f["network"] == "xhttp" { // A12
		return false
	}
	return true
}

// ssCiphers are the Shadowsocks ciphers ClashMeta admits (A10), verbatim from
// the specification.
var ssCiphers = map[string]bool{
	"aes-128-ctr": true, "aes-192-ctr": true, "aes-256-ctr": true,
	"aes-128-cfb": true, "aes-192-cfb": true, "aes-256-cfb": true,
	"aes-128-gcm": true, "aes-192-gcm": true, "aes-256-gcm": true,
	"aes-128-ccm": true, "aes-192-ccm": true, "aes-256-ccm": true,
	"aes-128-gcm-siv": true, "aes-256-gcm-siv": true,
	"chacha20-ietf": true, "chacha20": true, "xchacha20": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
	"chacha8-ietf-poly1305": true, "xchacha8-ietf-poly1305": true,
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true, "2022-blake3-chacha20-poly1305": true,
	"lea-128-gcm": true, "lea-192-gcm": true, "lea-256-gcm": true,
	"rabbit128-poly1305": true, "aegis-128l": true, "aegis-256": true, "aez-384": true,
	"deoxys-ii-256-128": true, "rc4-md5": true, "none": true,
}

// shadowTLSPlugin is the plugin form of a shadow-tls block: plugin
// shadow-tls with a plugin-opts object.
func shadowTLSPlugin(m map[string]any) (map[string]any, bool) {
	if m["plugin"] != "shadow-tls" {
		return nil, false
	}
	return obj(m, "plugin-opts")
}

// shadowTLSBlock is a node's shadow-tls block: the plugin form, or on snell
// an obfs-opts whose mode is shadow-tls.
func shadowTLSBlock(f map[string]any, typ string) (map[string]any, bool) {
	if po, ok := shadowTLSPlugin(f); ok {
		return po, true
	}
	if typ == "snell" {
		if oo, ok := obj(f, "obfs-opts"); ok && oo["mode"] == "shadow-tls" {
			return oo, true
		}
	}
	return nil, false
}

// shadowTLSEnabled is the specifications' "enabled": a set password, or a
// version that is present and not 0.
func shadowTLSEnabled(block map[string]any) bool {
	if set(block, "password") {
		return true
	}
	v, ok := block["version"]
	return ok && !numberIsZero(v)
}

func numberIsZero(v any) bool {
	switch x := v.(type) {
	case float64:
		return x == 0
	case int64:
		return x == 0
	case int:
		return x == 0
	}
	return false
}

// integerIn reports whether v is an integer from lo to hi. Text is trimmed
// and read as a number first, and empty text is not an integer.
func integerIn(v any, lo, hi float64) bool {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case int64:
		n = float64(x)
	case int:
		n = float64(x)
	case string:
		t := trimES(x)
		if t == "" {
			return false
		}
		n = jsNumber(t)
	default:
		return false
	}
	return n == float64(int64(n)) && n >= lo && n <= hi
}
