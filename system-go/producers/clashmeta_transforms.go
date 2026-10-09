package producers

import (
	"maps"
	"strconv"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// clashMetaTransform applies the ClashMeta transforms (clashmeta.md,
// "Transforms") to p, one admitted node after the steps, in the
// specification's order. internal is the sing-box pass, which skips T20.
//
// A top-level key a transform creates goes through p.put, so it is written
// after the received keys as upstream writes it. The node's fields are a
// top-level copy that shares its nested objects with the caller's node, so a
// transform that writes inside one first replaces it with a copy (own).
func clashMetaTransform(p *prepared, internal bool) {
	f := p.node.Fields
	typ, _ := f["type"].(string)
	stream := typ == "vmess" || typ == "vless"

	// T1: the mihomo shadow-tls shape.
	switch typ {
	case "vmess", "vless", "trojan", "anytls":
		if po, ok := shadowTLSPlugin(f); ok {
			delete(f, "plugin")
			delete(f, "plugin-opts")
			restoreShadowTLS(p.put, po, "sni", stream)
		}
	}
	// T1x: the same inside the xhttp download settings.
	if typ == "vless" && f["network"] == "xhttp" {
		xo, _ := obj(f, "xhttp-opts")
		ds, _ := obj(xo, "download-settings")
		if po, ok := shadowTLSPlugin(ds); ok {
			ds = own(own(f, "xhttp-opts"), "download-settings")
			delete(ds, "plugin")
			delete(ds, "plugin-opts")
			restoreShadowTLS(func(k string, v any) { ds[k] = v }, po, "servername", true)
		}
	}
	// T2: Reality needs a client fingerprint.
	if set(f, "reality-opts") && !set(f, "client-fingerprint") {
		p.put("client-fingerprint", "chrome")
	}

	switch typ {
	case "vmess": // T3
		if v, ok := f["aead"]; ok {
			if truthy(v) {
				p.put("alterId", float64(0))
			}
			delete(f, "aead")
		}
		moveKey(p, "sni", "servername")
		p.put("cipher", vmessSecurity(f))
	case "tuic": // T4
		alpnList(f)
		copyKey(p, "tfo", "fast-open")
		if !truthy(f["token"]) {
			if _, ok := f["version"]; !ok {
				p.put("version", float64(5))
			}
		}
	case "hysteria": // T5
		copyKey(p, "auth_str", "auth-str")
		alpnList(f)
		copyKey(p, "tfo", "fast-open")
	case "wireguard": // W
		mirrorKey(p, "keepalive", "persistent-keepalive")
		mirrorKey(p, "preshared-key", "pre-shared-key")
		prefixedAddress(f, "ip", "ip-cidr", normalise.IsIPv4Literal, 32)
		prefixedAddress(f, "ipv6", "ipv6-cidr", normalise.IsIPv6Literal, 128)
	case "snell": // T6
		if v, ok := f["version"]; ok && jsLessThan(v, 3) {
			delete(f, "udp")
		}
	case "vless": // T7
		moveKey(p, "sni", "servername")
		if f["network"] == "xhttp" {
			xo, _ := obj(f, "xhttp-opts")
			if ds, ok := obj(xo, "download-settings"); ok && set(f, "tls") && set(ds, "tls") &&
				set(f, "reality-opts") && !set(ds, "reality-opts") {
				own(own(f, "xhttp-opts"), "download-settings")["reality-opts"] = map[string]any{"public-key": ""}
			}
		}
	case "anytls": // T8
		if v, ok := f["reuse"]; ok && !truthy(v) {
			p.put("disable-reuse", true)
			delete(f, "reuse")
		}
	}

	// T9: plugin mux as a boolean.
	if po, ok := obj(f, "plugin-opts"); ok {
		if v, ok := po["mux"]; ok {
			own(f, "plugin-opts")["mux"] = muxBool(v)
		}
	}
	// T10: snell's shadow-tls lives in obfs-opts.
	if typ == "snell" {
		if block, ok := shadowTLSBlock(f, typ); ok {
			oo := map[string]any{"mode": "shadow-tls"}
			for _, k := range []string{"host", "password", "version"} {
				if v, ok := block[k]; ok {
					oo[k] = v
				}
			}
			if set(block, "alpn") {
				oo["alpn"] = block["alpn"]
			}
			delete(f, "plugin")
			delete(f, "plugin-opts")
			p.put("obfs-opts", oo)
		}
	}
	if stream {
		switch f["network"] {
		case "http": // T11
			httpOptsLists(f)
		case "h2": // T12
			h2OptsHost(f)
		}
	}
	if f["network"] == "ws" { // T13
		wsEarlyData(p)
	}
	// T14: the plugin's own skip-cert-verify, else the node's.
	if po, ok := obj(f, "plugin-opts"); ok && set(po, "tls") {
		if scv, ok := f["skip-cert-verify"]; ok && po["skip-cert-verify"] == nil {
			own(f, "plugin-opts")["skip-cert-verify"] = scv
		}
	}
	// T15
	switch typ {
	case "trojan", "tuic", "hysteria", "hysteria2", "juicity", "anytls", "trusttunnel", "naive", "masque", "shadowquic":
		delete(f, "tls")
	}
	// T16, T17: mihomo's key names.
	renameSet(p, "tls-fingerprint", "fingerprint")
	renameSet(p, "underlying-proxy", "dialer-proxy")
	// T18
	if v, ok := f["tls"]; ok {
		if _, isBool := v.(bool); !isBool {
			delete(f, "tls")
		}
	}
	// T19: pipeline fields.
	for _, k := range []string{"subName", "collectionName", "id", "resolved", "no-resolve", "ip-cidr", "ipv6-cidr"} {
		delete(f, k)
	}
	// T20: null values and top-level annotations; only the http-upgrade
	// record goes from the transport options.
	if !internal {
		for k, v := range f {
			if v == nil || strings.HasPrefix(k, "_") {
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
	// T21
	if f["network"] == "grpc" {
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
	// T22: mihomo's ip-version names.
	if set(f, "ip-version") {
		if v, ok := ipVersionNames[text(f["ip-version"])]; ok {
			f["ip-version"] = v
		}
	}
}

var ipVersionNames = map[string]string{
	"v4-only": "ipv4", "v6-only": "ipv6", "prefer-v4": "ipv4-prefer", "prefer-v6": "ipv6-prefer",
}

// own replaces m[key], an object, with a copy and returns the copy, so a
// transform can write it without reaching the caller's node. A value that is
// not an object is left alone and a new empty object is returned, which
// nothing reads.
func own(m map[string]any, key string) map[string]any {
	o, ok := m[key].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	c := maps.Clone(o)
	m[key] = c
	return c
}

// restoreShadowTLS is T1 with po, the shadow-tls plugin options, writing
// through put: shadow-tls-opts from the password and version, the host into
// hostKey, the alpn, and tls on when setTLS and the block is enabled. The
// caller removes plugin and plugin-opts first, so a top-level put appends the
// new keys after them as upstream does.
func restoreShadowTLS(put func(string, any), po map[string]any, hostKey string, setTLS bool) {
	opts := map[string]any{}
	for _, k := range []string{"password", "version"} {
		if v, ok := po[k]; ok {
			opts[k] = v
		}
	}
	put("shadow-tls-opts", opts)
	if v, ok := po["host"]; ok {
		put(hostKey, v)
	}
	if v, ok := po["alpn"]; ok {
		put("alpn", v)
	}
	if setTLS && shadowTLSEnabled(po) {
		put("tls", true)
	}
}

// moveKey moves a present from to to, replacing what to held.
func moveKey(p *prepared, from, to string) {
	f := p.node.Fields
	if v, ok := f[from]; ok {
		delete(f, from)
		p.put(to, v)
	}
}

// copyKey copies a present from to an absent to; both stay.
func copyKey(p *prepared, from, to string) {
	f := p.node.Fields
	if v, ok := f[from]; ok {
		if _, has := f[to]; !has {
			p.put(to, v)
		}
	}
}

// mirrorKey sets a and b both to a's value, else b's.
func mirrorKey(p *prepared, a, b string) {
	f := p.node.Fields
	v := f[a]
	if v == nil {
		v = f[b]
	}
	if v == nil {
		return
	}
	p.put(a, v)
	p.put(b, v)
}

// renameSet writes from's value to to when it is set and removes from either
// way.
func renameSet(p *prepared, from, to string) {
	f := p.node.Fields
	if v := f[from]; truthy(v) {
		p.put(to, v)
	}
	delete(f, from)
}

// alpnList turns a set scalar alpn into a one-element list.
func alpnList(f map[string]any) {
	if v := f["alpn"]; truthy(v) {
		if _, isList := v.([]any); !isList {
			f["alpn"] = []any{v}
		}
	}
}

// prefixedAddress is W for one family: the address with its prefix, or no
// field when it is not a literal of the family.
func prefixedAddress(f map[string]any, field, cidrKey string, literal func(string) bool, limit uint64) {
	if a, ok := interfaceAddress(f, field, cidrKey, literal, limit); ok {
		f[field] = a
	} else {
		delete(f, field)
	}
}

// jsLessThan is ECMAScript's v < n for a present value: text is read as a
// number, null as 0 and a boolean as 0 or 1; anything else compares false.
func jsLessThan(v any, n float64) bool {
	var x float64
	switch t := v.(type) {
	case float64:
		x = t
	case int64:
		x = float64(t)
	case int:
		x = float64(t)
	case string:
		x = jsNumber(t)
	case nil:
		x = 0
	case bool:
		if t {
			x = 1
		}
	default:
		return false
	}
	return x < n
}

// muxBool is T9: true and false stay, the texts true and false and digit
// strings are read as numbers, and anything else is its truthiness.
func muxBool(v any) bool {
	if s, ok := v.(string); ok {
		switch t := strings.ToLower(trimES(s)); {
		case t == "true":
			return true
		case t == "false":
			return false
		case digitsOnly(s):
			n, _ := strconv.ParseFloat(s, 64)
			return n != 0
		}
	}
	return truthy(v)
}

// httpOptsLists is T11: a set scalar path and Host header become lists.
func httpOptsLists(f map[string]any) {
	o, ok := obj(f, "http-opts")
	if !ok {
		return
	}
	if p := o["path"]; truthy(p) && !isList(p) {
		own(f, "http-opts")["path"] = []any{p}
		o = f["http-opts"].(map[string]any)
	}
	if h, ok := obj(o, "headers"); ok {
		if v := h["Host"]; truthy(v) && !isList(v) {
			own(own(f, "http-opts"), "headers")["Host"] = []any{v}
		}
	}
}

// h2OptsHost is T12: the first host as a list, host headers removed, and a
// list path as its first element.
func h2OptsHost(f map[string]any) {
	if _, ok := obj(f, "h2-opts"); !ok {
		return
	}
	o := own(f, "h2-opts")
	h, _ := obj(o, "headers")
	var host any
	switch {
	case o["host"] != nil:
		host = o["host"]
	case h["host"] != nil:
		host = h["host"]
	case h["Host"] != nil:
		host = h["Host"]
	}
	if truthy(host) {
		if !isList(host) {
			host = []any{host}
		}
		o["host"] = host
	}
	if h != nil {
		h = own(o, "headers")
		delete(h, "host")
		delete(h, "Host")
		if len(h) == 0 {
			delete(o, "headers")
		}
	}
	if l, ok := o["path"].([]any); ok {
		if len(l) > 0 {
			o["path"] = l[0]
		} else {
			delete(o, "path")
		}
	}
}

// wsEarlyData is T13: ws-opts with a path, then the websocket early-data
// normalisation.
func wsEarlyData(p *prepared) {
	f := p.node.Fields
	o, ok := obj(f, "ws-opts")
	switch {
	case ok && set(o, "path"):
		o = own(f, "ws-opts")
	case ok:
		o = own(f, "ws-opts")
		o["path"] = "/"
	case f["ws-opts"] == nil:
		o = map[string]any{"path": "/"}
		p.put("ws-opts", o)
	default:
		return
	}
	path, _ := o["path"].(string)
	ed := ""
	if v, ok := firstQueryValue(path, "ed"); ok {
		ed, _ = safeInteger(v)
	}
	if set(o, "v2ray-http-upgrade") {
		if ed != "" {
			o["path"] = removeQueryParam(path, "ed")
			o["v2ray-http-upgrade-fast-open"] = true
			if !set(o, "_v2ray-http-upgrade-ed") {
				o["_v2ray-http-upgrade-ed"] = edNumber(ed)
			}
		}
		delete(o, "early-data-header-name")
		delete(o, "max-early-data")
		return
	}
	if ed != "" {
		o["path"] = removeQueryParam(path, "ed")
		if _, ok := o["early-data-header-name"]; !ok {
			o["early-data-header-name"] = "Sec-WebSocket-Protocol"
		}
		if _, ok := o["max-early-data"]; !ok {
			o["max-early-data"] = edNumber(ed)
		}
	}
}

func edNumber(s string) float64 {
	n, _ := strconv.ParseFloat(s, 64)
	return n
}

func isList(v any) bool {
	_, ok := v.([]any)
	return ok
}
