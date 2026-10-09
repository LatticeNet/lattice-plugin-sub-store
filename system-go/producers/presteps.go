package producers

import (
	"errors"
	"maps"
	"math"
	"strconv"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// The steps every target runs before its producer (specs/producers/uri.md
// and its siblings, "Steps before the producer"), in their order: the
// admission filter (support map, root headers, Shadowsocks over TLS, broken
// VLESS Reality, xhttp stream-one), then the resolved marker, name fill,
// disable-sni, port hopping and the WireGuard interface.

// stepRules is how the steps differ between the native targets.
type stepRules struct {
	// rootHeaderTypes are the types dropped when they carry a non-empty
	// root headers map.
	rootHeaderTypes map[string]bool
	// disableSNI says whether the disable-sni step runs.
	disableSNI bool
	// keepPortsSlash keeps "/" in ports instead of turning it into ",".
	keepPortsSlash bool
}

var (
	allHeaderTypes   = map[string]bool{"http": true, "h2-connect": true, "trusttunnel": true}
	tunnelHeaderType = map[string]bool{"h2-connect": true, "trusttunnel": true}
)

// stepRulesByID holds each native target's rules by harness id: uri.md and
// v2ray.md drop every header type, json.md keeps them all, clashmeta.md and
// singbox.md keep http's; singbox.md skips disable-sni; clashmeta.md keeps
// "/" in ports.
var stepRulesByID = map[string]stepRules{
	"uri":       {rootHeaderTypes: allHeaderTypes, disableSNI: true},
	"v2ray":     {rootHeaderTypes: allHeaderTypes, disableSNI: true},
	"json":      {disableSNI: true},
	"clashmeta": {rootHeaderTypes: tunnelHeaderType, disableSNI: true, keepPortsSlash: true},
	"singbox":   {rootHeaderTypes: tunnelHeaderType},
}

// prepared is one node after the steps before the producer.
type prepared struct {
	// index is the node's position in the caller's list.
	index int
	// node is a copy of the caller's node at the top level, which is the
	// only level the steps write. Nested objects and lists are shared with
	// the caller, so a producer that changes one copies it first
	// (nodemodel.CloneValue).
	node *nodemodel.Node
	// added lists the top-level keys created after the producer received
	// the node, by the steps and then by the producer's own transforms, in
	// creation order (put). Producers that keep upstream's key order write
	// them after the received keys (keyOrder); of the steps' keys only sni
	// reaches a URI parameter walk (uri.md, "Input").
	added []string
}

// ordered is the node's fields with their keys in keyOrder, for the
// producers that write upstream's key order.
func (p *prepared) ordered() *object {
	f := p.node.Fields
	keys := keyOrder(f, p.added)
	o := newObject(len(keys))
	for _, k := range keys {
		o.members = append(o.members, member{k, f[k]})
	}
	return o
}

// put writes a top-level field and records it as created when it is new.
func (p *prepared) put(key string, v any) {
	if _, ok := p.node.Fields[key]; !ok {
		p.added = append(p.added, key)
	}
	p.node.Fields[key] = v
}

// prepare runs the steps for target, the exact string the caller used, with
// the rules of the producer whose harness id is id. It returns the nodes that
// reach the producer, in order, and the ones the admission filter removed.
func prepare(nodes []*nodemodel.Node, target, id string, opts Options) ([]prepared, []Dropped) {
	rules := stepRulesByID[id]
	include := opts.Truthy("include-unsupported-proxy")
	out := make([]prepared, 0, len(nodes))
	var dropped []Dropped
	for i, n := range nodes {
		var src map[string]any
		var script map[string]any
		var lattice *nodemodel.LatticeFields
		if n != nil {
			src, script, lattice = n.Fields, n.Script, n.Lattice
		}
		if reason := admission(src, target, rules, include); reason != "" {
			dropped = append(dropped, Dropped{Index: i, Type: typeOf(src), Reason: reason})
			continue
		}
		f := make(map[string]any, len(src)+2)
		maps.Copy(f, src)
		p := prepared{index: i, node: &nodemodel.Node{Fields: f, Script: script, Lattice: lattice}}

		// Resolved marker: _resolved mirrors resolved whenever resolved is
		// present, null included (json.md).
		if v, ok := f["resolved"]; ok {
			p.put("_resolved", v)
		}

		// Name fill, with port as it is before port hopping fills it.
		if !notBlank(f["name"]) {
			p.put("name", textOf(f, "type")+" "+textOf(f, "server")+":"+textOf(f, "port"))
		}

		if rules.disableSNI && set(f, "disable-sni") && f["type"] != "tuic" {
			server, _ := f["server"].(string)
			if normalise.IsIPv4Literal(server) || normalise.IsIPv6Literal(server) {
				p.put("sni", server)
			} else {
				p.put("sni", "127.0.0.1")
			}
		}

		if set(f, "ports") {
			ports := text(f["ports"])
			if !rules.keepPortsSlash {
				ports = strings.ReplaceAll(ports, "/", ",")
			}
			f["ports"] = ports
			if !set(f, "port") {
				p.put("port", drawPort(ports))
			}
		}

		if f["type"] == "wireguard" {
			_, hadV4 := f["ip-cidr"]
			_, hadV6 := f["ipv6-cidr"]
			normalise.WireGuardInterface(f)
			for _, k := range [...]struct {
				key string
				had bool
			}{{"ip-cidr", hadV4}, {"ipv6-cidr", hadV6}} {
				if _, has := f[k.key]; has && !k.had {
					p.added = append(p.added, k.key)
				}
			}
		}
		out = append(out, p)
	}
	return out, dropped
}

// admission is the filter at the head of the steps. It returns the reason a
// node is dropped, or "" when it reaches the producer. include-unsupported-
// proxy bypasses the first three rows only.
func admission(f map[string]any, target string, rules stepRules, include bool) string {
	typ := f["type"]
	if !include {
		if supported, ok := obj(f, "supported"); ok && supported[target] == false {
			return ReasonSupportMap
		}
		if t, ok := typ.(string); ok && rules.rootHeaderTypes[t] {
			if headers, ok := obj(f, "headers"); ok && len(headers) > 0 {
				return ReasonRootHeaders
			}
		}
		if typ == "ss" && set(f, "tls") && !set(f, "plugin") &&
			(!set(f, "network") || strings.ToLower(trimES(text(f["network"]))) == "tcp") {
			return ReasonSSTLS
		}
	}
	if typ == "vless" {
		if brokenReality(f) {
			return ReasonBrokenReality
		}
		if xo, ok := obj(f, "xhttp-opts"); ok && f["network"] == "xhttp" && xo["mode"] == "stream-one" {
			if _, ok := obj(xo, "download-settings"); ok {
				return ReasonXHTTPStreamOne
			}
		}
	}
	return ""
}

// brokenReality reports a VLESS Reality block without a non-blank public key.
// The download settings' own Reality block may carry an explicit empty key
// when the top-level key is non-blank.
func brokenReality(f map[string]any) bool {
	topKey := false
	if top, ok := f["reality-opts"]; ok && truthy(top) {
		r, _ := top.(map[string]any)
		if !notBlank(r["public-key"]) {
			return true
		}
		topKey = true
	}
	xo, _ := obj(f, "xhttp-opts")
	ds, _ := obj(xo, "download-settings")
	if dr, ok := ds["reality-opts"]; ok && truthy(dr) {
		r, _ := dr.(map[string]any)
		key, has := r["public-key"]
		if !notBlank(key) && !(has && key == "" && topKey) {
			return true
		}
	}
	return false
}

// drawPort picks the port a node without one uses from its ports. Upstream
// draws an entry and, within a range, an integer at random; the oracle's
// random source always returns 0, so the goldens pin the first entry and the
// lower bound of a range, and this makes that choice on purpose. The entry is
// read as ECMAScript's Number reads text, so "443;8443" gives not-a-number,
// which the links write as NaN.
func drawPort(ports string) float64 {
	entry, _, _ := strings.Cut(strings.ReplaceAll(ports, "/", ","), ",")
	if lower, _, ok := strings.Cut(entry, "-"); ok {
		entry = lower
	}
	return jsNumber(entry)
}

// jsNumber is ECMAScript's Number(text) for decimal text: trimmed, empty
// gives 0, Infinity with an optional sign, and anything that is not a decimal
// literal gives not-a-number. Hexadecimal, octal and binary literals, which
// no port draw meets, also give not-a-number here.
func jsNumber(s string) float64 {
	t := trimES(s)
	switch t {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if strings.Trim(t, "0123456789+-.eE") != "" {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
	return v
}

func typeOf(f map[string]any) string {
	t, _ := f["type"].(string)
	return t
}
