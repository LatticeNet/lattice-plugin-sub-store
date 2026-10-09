// Package normalise turns every line parser's output into one model: the
// node rules N1 to N35 of normaliser.md section 2, the document rules D1 and
// D2 of section 3, and Lattice's hostile-content stage and size bounds
// (sections 4 and 5).
//
// The rules follow the specification, quirks included, because the parse
// conformance number counts deep equality with the oracle. Values are read
// with ECMAScript semantics (truthiness, String(value), trim) since that is
// what the specification's words mean.
//
// One difference is structural. The model is a map, so the input order of a
// node's keys is not kept. Where upstream's result depends on that order (two
// spellings of one key that differ only in letter case, both not lower case,
// under N1), keys are visited in byte order instead. No corpus case has such a
// pair.
//
// Nothing in this package imports package main, the SDK or the script engine.
package normalise

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// ErrCertificateNotPEM is N33's whole-document failure: a CA text without a
// PEM header, on a node whose fingerprint has to be computed from it. No
// nodes are returned for the document, in Lattice as in upstream.
var ErrCertificateNotPEM = errors.New("normalise: certificate text has no PEM header")

// rule is one node rule of normaliser.md section 2.
type rule struct {
	id    string
	apply func(f map[string]any) error
}

// nodeRules are N1 to N35 in specification order. Later rules read fields
// earlier rules wrote, so the order is part of the behaviour.
var nodeRules = []rule{
	{"N1", optionKeys},
	{"N2", realityMLKEM},
	{"N3", udp},
	{"N4", cipher},
	{"N5", numericPassword},
	{"N6", hopInterval},
	{"N7", emptySSPassword},
	{"N8", interfaceName},
	{"N9", port},
	{"N10", server},
	{"N11", shadowTLS},
	{"N12", snellShadowTLS},
	{"N13", shadowTLSALPN},
	{"N14", xhttpDownloadShadowTLS},
	{"N15", legacyWebSocketKeys},
	{"N16", transportPath},
	{"N17", networkDefaults},
	{"N18", packetEncoding},
	{"N19", forcedTLS},
	{"N20", hostHeaderCase},
	{"N21", h2Host},
	{"N22", hostPinning},
	{"N23", httpLists},
	{"N24", sniPinning},
	{"N25", ports},
	{"N26", hysteria2ObfsShorthand},
	{"N27", hysteria2PasswordAlias},
	{"N28", vlessCleanup},
	{"N29", name},
	{"N30", defaultPaths},
	{"N31", anyTLSReuse},
	{"N32", disableSNI},
	{"N33", certificateFingerprint},
	{"N34", tuicDefaults},
	{"N35", wireGuard},
}

// Node applies N1 to N35 in order (normaliser.md section 2), then H2 and
// H3, then the size bounds. H1 is applied by the Clash object parser before
// it calls this. Returns drop=true for an exec-shaped node under H3 or a
// bounds violation, with the reason. The one error is N33's whole-document
// failure (ErrCertificateNotPEM), which the caller turns into a failed
// document.
func Node(n *nodemodel.Node, allowExternal bool) (drop bool, reason string, err error) {
	if n.Fields == nil {
		n.Fields = map[string]any{}
	}
	for _, r := range nodeRules {
		if err := r.apply(n.Fields); err != nil {
			return true, r.id, err
		}
	}
	drop, reason = hostile(n, allowExternal)
	return drop, reason, nil
}

// N1: lower-case option keys, and the keys of each options object, with the
// lower-case spelling winning a collision. Only keys that change are sorted
// (byte order stands in for input order, see the package comment), so a node
// that is already lower case costs one pass and no allocation.
func optionKeys(f map[string]any) error {
	var moving []string
	for k, v := range f {
		lower := strings.ToLower(k)
		if !strings.HasSuffix(lower, "-opts") {
			continue
		}
		if lower != k {
			moving = append(moving, k)
			continue
		}
		if o, ok := v.(map[string]any); ok {
			lowerCaseKeys(o)
		}
	}
	sort.Strings(moving)
	for _, k := range moving {
		lower := strings.ToLower(k)
		lowerCaseKey(f, k, lower)
		if o, ok := f[lower].(map[string]any); ok {
			lowerCaseKeys(o)
		}
	}
	return nil
}

// lowerCaseKeys applies lowerCaseKey to every key of an options object.
func lowerCaseKeys(o map[string]any) {
	var moving []string
	for k := range o {
		if strings.ToLower(k) != k {
			moving = append(moving, k)
		}
	}
	sort.Strings(moving)
	for _, k := range moving {
		lowerCaseKey(o, k, strings.ToLower(k))
	}
}

// lowerCaseKey moves m[k] to m[lower] unless lower is already present, and
// removes k either way.
func lowerCaseKey(m map[string]any, k, lower string) {
	if k == lower {
		return
	}
	if _, exists := m[lower]; !exists {
		m[lower] = m[k]
	}
	delete(m, k)
}

// N2: a Loon TLS profile that negotiates ML-KEM marks a Reality node.
func realityMLKEM(f map[string]any) error {
	ro, ok := f["reality-opts"].(map[string]any)
	if !ok || !truthyKey(ro, "public-key") {
		return nil
	}
	if _, exists := ro["support-x25519mlkem768"]; exists {
		return nil
	}
	profile, ok := f["_loon_tls_profile"]
	if !ok {
		return nil
	}
	switch trimES(text(profile)) {
	case "safari-ios-26", "chrome147":
		ro["support-x25519mlkem768"] = true
	}
	return nil
}

// N3: udp is always written, false only for false, 0 and the texts 0, false
// and off in any letter case.
func udp(f map[string]any) error {
	value := true
	switch x := f["udp"].(type) {
	case bool:
		value = x
	case float64:
		value = x != 0
	case int64:
		value = x != 0
	case int:
		value = x != 0
	case string:
		switch strings.ToLower(x) {
		case "0", "false", "off":
			value = false
		}
	}
	f["udp"] = value
	return nil
}

// N4: a text cipher is lower-cased.
func cipher(f map[string]any) error {
	if s, ok := f["cipher"].(string); ok {
		f["cipher"] = strings.ToLower(s)
	}
	return nil
}

// N5: a numeric password becomes text: the plain digits for a safe integer,
// otherwise the exact decimal value of the double it was read as.
func numericPassword(f map[string]any) error {
	switch x := f["password"].(type) {
	case float64:
		f["password"] = exactDecimal(x)
	case int64:
		f["password"] = strconv.FormatInt(x, 10)
	case int:
		f["password"] = strconv.Itoa(x)
	}
	return nil
}

func exactDecimal(x float64) string {
	switch {
	case math.IsNaN(x) || math.IsInf(x, 0):
		return text(x)
	case x == 0:
		return "0"
	case x == math.Trunc(x):
		return new(big.Float).SetFloat64(x).Text('f', 0)
	}
	// A double's exact decimal expansion has at most 1074 fractional digits.
	s := new(big.Float).SetFloat64(x).Text('f', 1100)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

var hopRangePattern = regexp.MustCompile(`^([0-9]+)[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*-[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*([0-9]+)$`)

// N6: hop-interval as a range A-B, as a positive integer, or removed.
func hopInterval(f map[string]any) error {
	v, ok := f["hop-interval"]
	if !ok || v == nil {
		return nil
	}
	t := trimES(text(v))
	if m := hopRangePattern.FindStringSubmatch(t); m != nil {
		a, b := digitsValue(m[1]), digitsValue(m[2])
		if a > 0 && a <= b {
			f["hop-interval"] = a
			f["hop-interval-max"] = b
			return nil
		}
	}
	if digitsOnly(t) {
		if a := digitsValue(t); a > 0 {
			f["hop-interval"] = a
			delete(f, "hop-interval-max")
			return nil
		}
	}
	delete(f, "hop-interval")
	delete(f, "hop-interval-max")
	return nil
}

// N7: Shadowsocks with cipher none gets an empty password.
func emptySSPassword(f map[string]any) error {
	if f["type"] == "ss" && f["cipher"] == "none" && !truthyKey(f, "password") {
		f["password"] = ""
	}
	return nil
}

// N8: interface moves to interface-name.
func interfaceName(f map[string]any) error {
	if truthyKey(f, "interface") {
		f["interface-name"] = f["interface"]
		delete(f, "interface")
	}
	return nil
}

// N9: a valid port spelling becomes the integer it starts with; anything
// else is left as it is.
func port(f map[string]any) error {
	v, ok := f["port"]
	if !ok {
		return nil
	}
	t := text(v)
	if !validPortSpelling(t) {
		return nil
	}
	if t == "" {
		f["port"] = math.NaN()
		return nil
	}
	f["port"] = digitsValue(t)
	return nil
}

// validPortSpelling is normaliser.md section 2.1: 10000 to 65535 in five
// digits, one to four digits, or up to five characters each from 0 to 5
// (the empty text included).
func validPortSpelling(t string) bool {
	if len(t) == 5 && digitsOnly(t) && t >= "10000" && t <= "65535" {
		return true
	}
	if len(t) >= 1 && len(t) <= 4 && digitsOnly(t) {
		return true
	}
	if len(t) > 5 {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] < '0' || t[i] > '5' {
			return false
		}
	}
	return true
}

// N10: server as trimmed text without one leading "[" and one trailing "]".
func server(f map[string]any) error {
	if !truthyKey(f, "server") {
		return nil
	}
	s := trimES(text(f["server"]))
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	f["server"] = s
	return nil
}

// N11: shadow-tls-opts becomes the shadow-tls plugin for VMess, VLESS,
// Trojan and AnyTLS. Shadowsocks keeps shadow-tls-opts.
func shadowTLS(f map[string]any) error {
	switch f["type"] {
	case "vmess", "vless", "trojan", "anytls":
	default:
		return nil
	}
	sto, ok := f["shadow-tls-opts"]
	if !ok || sto == nil {
		return nil
	}
	opts := map[string]any{}
	copyKey(opts, "host", f, "sni")
	if m, ok := sto.(map[string]any); ok {
		copyKey(opts, "password", m, "password")
		copyKey(opts, "version", m, "version")
	}
	f["plugin"] = "shadow-tls"
	f["plugin-opts"] = opts
	delete(f, "shadow-tls-opts")
	return nil
}

// N12: Snell's shadow-tls obfs mode becomes the shadow-tls plugin.
func snellShadowTLS(f map[string]any) error {
	if f["type"] != "snell" || truthyKey(f, "plugin") {
		return nil
	}
	oo, ok := f["obfs-opts"].(map[string]any)
	if !ok || oo["mode"] != "shadow-tls" {
		return nil
	}
	opts := map[string]any{}
	for _, k := range []string{"host", "password", "version", "alpn"} {
		copyKey(opts, k, oo, k)
	}
	f["plugin"] = "shadow-tls"
	f["plugin-opts"] = opts
	delete(f, "obfs-opts")
	return nil
}

// N13: with the shadow-tls plugin, a top-level alpn moves into plugin-opts
// unless plugin-opts has one, and the top-level alpn is removed either way.
func shadowTLSALPN(m map[string]any) error {
	if m["plugin"] != "shadow-tls" {
		return nil
	}
	po, ok := m["plugin-opts"]
	if !ok || po == nil {
		return nil
	}
	if opts, ok := po.(map[string]any); ok && truthyKey(m, "alpn") && !truthyKey(opts, "alpn") {
		opts["alpn"] = nodemodel.CloneValue(m["alpn"])
	}
	delete(m, "alpn")
	return nil
}

// N14: Shadow-TLS inside VLESS xhttp download settings, then N13's ALPN move
// inside them.
func xhttpDownloadShadowTLS(f map[string]any) error {
	if f["type"] != "vless" || f["network"] != "xhttp" {
		return nil
	}
	xo, ok := f["xhttp-opts"].(map[string]any)
	if !ok {
		return nil
	}
	ds, ok := xo["download-settings"].(map[string]any)
	if !ok {
		return nil
	}
	sto, ok := ds["shadow-tls-opts"]
	if !ok || sto == nil {
		return nil
	}
	opts := map[string]any{}
	copyKey(opts, "host", ds, "servername")
	if m, ok := sto.(map[string]any); ok {
		copyKey(opts, "password", m, "password")
		copyKey(opts, "version", m, "version")
	}
	ds["plugin"] = "shadow-tls"
	ds["plugin-opts"] = opts
	delete(ds, "shadow-tls-opts")
	return shadowTLSALPN(ds)
}

// N15: legacy ws-path and ws-headers build ws-opts when it is absent, and are
// removed on every WebSocket node.
func legacyWebSocketKeys(f map[string]any) error {
	if f["network"] != "ws" {
		return nil
	}
	if _, exists := f["ws-opts"]; !exists && (truthyKey(f, "ws-path") || truthyKey(f, "ws-headers")) {
		opts := map[string]any{}
		copyKey(opts, "path", f, "ws-path")
		copyKey(opts, "headers", f, "ws-headers")
		f["ws-opts"] = opts
	}
	delete(f, "ws-path")
	delete(f, "ws-headers")
	return nil
}

// N16: the transport path as trimmed text with a leading "/", for every
// network, element by element for a list.
func transportPath(f map[string]any) error {
	o, ok := f[optsKey(f)].(map[string]any)
	if !ok {
		return nil
	}
	switch p := o["path"].(type) {
	case string, float64, int64, int:
		o["path"] = formatPath(text(p))
	case []any:
		for i, e := range p {
			switch e.(type) {
			case string, float64, int64, int:
				p[i] = formatPath(text(e))
			}
		}
	}
	return nil
}

func formatPath(s string) string {
	s = trimES(s)
	if s == "" {
		return "/"
	}
	if !strings.HasPrefix(s, "/") {
		return "/" + s
	}
	return s
}

// N17: default network for Trojan, VMess and VLESS; VMess cipher and alterId.
func networkDefaults(f map[string]any) error {
	switch f["type"] {
	case "trojan", "vmess", "vless":
	default:
		return nil
	}
	if !truthyKey(f, "network") {
		f["network"] = "tcp"
	}
	if f["type"] == "vmess" {
		if !truthyKey(f, "cipher") {
			f["cipher"] = "none"
		}
		if !truthyKey(f, "alterId") {
			f["alterId"] = float64(0)
		}
	}
	return nil
}

// N18: packet encoding from the xudp and packet-addr flags.
func packetEncoding(f map[string]any) error {
	if f["type"] != "vmess" && f["type"] != "vless" {
		return nil
	}
	if v, ok := f["packet-encoding"]; ok && v != nil {
		return nil
	}
	switch {
	case truthyKey(f, "xudp"):
		f["packet-encoding"] = "xudp"
	case truthyKey(f, "packet-addr"):
		f["packet-encoding"] = "packetaddr"
	}
	return nil
}

// forcedTLSTypes always run over TLS (N19). masque-surge is not one: the
// Surge grammar sets it.
var forcedTLSTypes = map[string]bool{
	"trojan": true, "tuic": true, "hysteria": true, "hysteria2": true, "juicity": true, "anytls": true,
	"trusttunnel": true, "h2-connect": true, "naive": true, "masque": true, "shadowquic": true,
}

// N19: forced TLS.
func forcedTLS(f map[string]any) error {
	if t, ok := f["type"].(string); ok && forcedTLSTypes[t] {
		f["tls"] = true
	}
	return nil
}

// N20: a lower-case host header becomes Host, except on h2.
func hostHeaderCase(f map[string]any) error {
	if !truthyKey(f, "network") || f["network"] == "h2" {
		return nil
	}
	o, _ := f[optsKey(f)].(map[string]any)
	h, ok := o["headers"].(map[string]any)
	if !ok {
		return nil
	}
	if truthyKey(h, "host") && !truthyKey(h, "Host") {
		h["Host"] = h["host"]
		delete(h, "host")
	}
	return nil
}

// N21: the HTTP/2 host moves into h2-opts.host as a list, host headers are
// removed, and a list path becomes its first element.
func h2Host(f map[string]any) error {
	if f["network"] != "h2" {
		return nil
	}
	o, ok := f["h2-opts"].(map[string]any)
	if !ok {
		return nil
	}
	h, _ := o["headers"].(map[string]any)
	var host any
	switch {
	case o["host"] != nil:
		host = o["host"]
	case h != nil && h["host"] != nil:
		host = h["host"]
	case h != nil && h["Host"] != nil:
		host = h["Host"]
	}
	if truthy(host) {
		if _, isList := host.([]any); !isList {
			host = []any{host}
		}
		o["host"] = host
	}
	if h != nil {
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
	return nil
}

// N22: on plain WebSocket and HTTP, the server is pinned as the Host header
// so a later DNS resolution of server cannot lose it.
func hostPinning(f map[string]any) error {
	network := f["network"]
	if truthyKey(f, "tls") || (network != "ws" && network != "http") {
		return nil
	}
	key := network.(string) + "-opts"
	if o, ok := f[key].(map[string]any); ok {
		if h, ok := o["headers"].(map[string]any); ok && truthyKey(h, "Host") {
			return nil
		}
	}
	sv, hasServer := f["server"]
	if hasServer && isIPLiteral(text(sv)) {
		return nil
	}
	o, ok := containerAt(f, key)
	if !ok {
		return nil
	}
	h, ok := containerAt(o, "headers")
	if !ok {
		return nil
	}
	listForm := network == "http" && (f["type"] == "vmess" || f["type"] == "vless")
	switch {
	case listForm && hasServer:
		h["Host"] = []any{nodemodel.CloneValue(sv)}
	case listForm:
		h["Host"] = []any{nil}
	case hasServer:
		h["Host"] = nodemodel.CloneValue(sv)
	default:
		delete(h, "Host")
	}
	return nil
}

// N23: VMess and VLESS on HTTP carry the Host header and the path as lists.
func httpLists(f map[string]any) error {
	if (f["type"] != "vmess" && f["type"] != "vless") || f["network"] != "http" {
		return nil
	}
	o, ok := f["http-opts"].(map[string]any)
	if !ok {
		return nil
	}
	if h, ok := o["headers"].(map[string]any); ok {
		if v := h["Host"]; truthy(v) && !isList(v) {
			h["Host"] = []any{v}
		}
	}
	if p := o["path"]; truthy(p) && !isList(p) {
		o["path"] = []any{p}
	}
	return nil
}

// N24: on TLS, sni is pinned from the transport host, else from a server
// that is not an IP literal.
func sniPinning(f map[string]any) error {
	if !truthyKey(f, "tls") {
		return nil
	}
	if s, ok := f["sni"]; ok && (truthy(s) || s == "") {
		return nil
	}
	if truthyKey(f, "network") {
		var host any
		if f["network"] == "h2" {
			if o, ok := f["h2-opts"].(map[string]any); ok {
				host = o["host"]
			}
		} else if o, ok := f[optsKey(f)].(map[string]any); ok {
			if h, ok := o["headers"].(map[string]any); ok {
				host = h["Host"]
			}
		}
		if l, ok := host.([]any); ok {
			host = nil
			if len(l) > 0 {
				host = l[0]
			}
		}
		if truthy(host) {
			f["sni"] = nodemodel.CloneValue(host)
		}
	}
	if truthyKey(f, "sni") {
		return nil
	}
	sv, hasServer := f["server"]
	if hasServer && isIPLiteral(text(sv)) {
		return nil
	}
	if hasServer {
		f["sni"] = nodemodel.CloneValue(sv)
	} else {
		delete(f, "sni")
	}
	return nil
}

// N25: ports as text with "/" written as ","; a non-truthy ports is removed.
func ports(f map[string]any) error {
	v, ok := f["ports"]
	if !ok {
		return nil
	}
	if !truthy(v) {
		delete(f, "ports")
		return nil
	}
	f["ports"] = strings.ReplaceAll(text(v), "/", ",")
	return nil
}

// N26: a Hysteria2 obfs word other than salamander is the password of
// salamander.
func hysteria2ObfsShorthand(f map[string]any) error {
	if f["type"] == "hysteria2" && truthyKey(f, "obfs") && f["obfs"] != "salamander" && !truthyKey(f, "obfs-password") {
		f["obfs-password"] = f["obfs"]
		f["obfs"] = "salamander"
	}
	return nil
}

// N27: obfs_password is an alias of obfs-password on Hysteria2.
func hysteria2PasswordAlias(f map[string]any) error {
	if f["type"] == "hysteria2" && !truthyKey(f, "obfs-password") && truthyKey(f, "obfs_password") {
		f["obfs-password"] = f["obfs_password"]
		delete(f, "obfs_password")
	}
	return nil
}

// N28: VLESS cleanup of empty option objects, meaningless flow values and a
// missing HTTP path.
func vlessCleanup(f map[string]any) error {
	if f["type"] != "vless" {
		return nil
	}
	for _, k := range []string{"reality-opts", "grpc-opts"} {
		if m, ok := f[k].(map[string]any); ok && len(m) == 0 {
			delete(f, k)
		}
	}
	if flow, ok := f["flow"]; ok && ((!truthyKey(f, "reality-opts") && !truthy(flow)) || flow == nil || flow == "null") {
		delete(f, "flow")
	}
	if f["network"] == "http" {
		if o, ok := containerAt(f, "http-opts"); ok && !truthyKey(o, "path") {
			o["path"] = []any{"/"}
		}
	}
	return nil
}

// N29: a name that is not text becomes text: its digits, the UTF-8 decoding
// of a byte list, or the fallback "<type> <server>:<port>".
func name(f map[string]any) error {
	v, ok := f["name"]
	if _, isText := v.(string); ok && isText {
		return nil
	}
	if ok {
		if t := text(v); digitsOnly(t) {
			f["name"] = t
			return nil
		}
		var list []any
		switch x := v.(type) {
		case map[string]any:
			if d, ok := x["data"]; ok && truthy(d) {
				list, _ = d.([]any)
			}
		case []any:
			list = x
		}
		if list != nil {
			f["name"] = decodeUTF8(byteList(list))
			return nil
		}
	}
	f["name"] = textOf(f, "type") + " " + textOf(f, "server") + ":" + textOf(f, "port")
	return nil
}

// byteList converts each element to an integer modulo 256, as a Uint8Array
// does; anything that is not a number counts as 0.
func byteList(l []any) []byte {
	out := make([]byte, len(l))
	for i, e := range l {
		var x float64
		switch v := e.(type) {
		case float64:
			x = v
		case int64:
			x = float64(v)
		case int:
			x = float64(v)
		}
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		m := math.Mod(math.Trunc(x), 256)
		if m < 0 {
			m += 256
		}
		out[i] = byte(m)
	}
	return out
}

// N30: default transport paths on ws, h2 and http, except for masque.
func defaultPaths(f map[string]any) error {
	network, _ := f["network"].(string)
	if (network != "ws" && network != "http" && network != "h2") || f["type"] == "masque" {
		return nil
	}
	key := network + "-opts"
	if network == "http" {
		p := map[string]any(nil)
		if o, ok := f[key].(map[string]any); ok {
			p = o
		}
		if l, ok := p["path"].([]any); ok && anyTruthy(l) {
			return nil
		}
		if o, ok := containerAt(f, key); ok {
			o["path"] = []any{"/"}
		}
		return nil
	}
	if o, ok := f[key].(map[string]any); ok && truthyKey(o, "path") {
		return nil
	}
	if o, ok := containerAt(f, key); ok {
		o["path"] = "/"
	}
	return nil
}

// N31: AnyTLS disable-reuse also writes reuse: false.
func anyTLSReuse(f map[string]any) error {
	if f["type"] == "anytls" && truthyKey(f, "disable-reuse") {
		f["reuse"] = false
	}
	return nil
}

// N32: an empty or "off" sni disables SNI; sni itself is kept.
func disableSNI(f map[string]any) error {
	if s := f["sni"]; s == "" || s == "off" {
		f["disable-sni"] = true
	}
	return nil
}

var (
	pemBegin = regexp.MustCompile(`-----BEGIN [^-]+-----`)
	pemEnd   = regexp.MustCompile(`-----END [^-]+-----`)
)

// N33: tls-fingerprint from the CA text. H2 lives here: a _ca value is never
// read as a file path, so the fingerprint comes only from ca-str or ca_str.
func certificateFingerprint(f map[string]any) error {
	var ca string
	switch {
	case truthyKey(f, "ca-str"):
		ca = text(f["ca-str"])
	case truthyKey(f, "ca_str"):
		f["ca-str"] = f["ca_str"]
		delete(f, "ca_str")
		ca = text(f["ca-str"])
	default:
		return nil
	}
	if truthyKey(f, "tls-fingerprint") {
		return nil
	}
	fp, err := pemFingerprint(ca)
	if err != nil {
		return err
	}
	f["tls-fingerprint"] = fp
	return nil
}

// pemFingerprint is normaliser.md section 2.2: the body of the last
// certificate, its Base64 decoded up to the first "=", hashed with SHA-256
// and written as upper-case hexadecimal pairs joined by ":".
func pemFingerprint(ca string) (string, error) {
	if !strings.Contains(ca, "-----BEGIN ") {
		return "", ErrCertificateNotPEM
	}
	body := ca
	if m := pemBegin.FindAllStringIndex(body, -1); m != nil {
		body = body[m[len(m)-1][1]:]
	}
	if m := pemEnd.FindStringIndex(body); m != nil {
		body = body[:m[0]]
	}
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=' {
			b.WriteByte(c)
		}
	}
	b64 := b.String()
	if i := strings.IndexByte(b64, '='); i >= 0 {
		b64 = b64[:i]
	}
	if len(b64)%4 == 1 {
		b64 = b64[:len(b64)-1]
	}
	der, err := base64.RawStdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	const hex = "0123456789ABCDEF"
	out := make([]byte, 0, len(sum)*3-1)
	for i, c := range sum {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, hex[c>>4], hex[c&0xf])
	}
	return string(out), nil
}

// N34: TUIC defaults for alpn, congestion controller and relay mode.
func tuicDefaults(f map[string]any) error {
	if f["type"] != "tuic" {
		return nil
	}
	switch a := f["alpn"]; {
	case isList(a):
	case truthy(a):
		f["alpn"] = []any{a}
	default:
		f["alpn"] = []any{"h3"}
	}
	if !truthyKey(f, "congestion-controller") {
		f["congestion-controller"] = "cubic"
	}
	if !truthyKey(f, "udp-relay-mode") {
		f["udp-relay-mode"] = "native"
	}
	return nil
}

// N35: WireGuard peer addresses, then interface address normalisation.
func wireGuard(f map[string]any) error {
	if f["type"] != "wireguard" {
		return nil
	}
	if peers, ok := f["peers"].([]any); ok && len(peers) > 0 {
		some := false
		for _, p := range peers {
			if m, ok := p.(map[string]any); ok && (truthyKey(m, "ip") || truthyKey(m, "ipv6")) {
				some = true
				break
			}
		}
		if some {
			// The first peer is read even when it has neither address
			// (quirk): the node address then becomes absent.
			first, _ := peers[0].(map[string]any)
			for _, k := range []string{"ip", "ipv6"} {
				if truthyKey(f, k) {
					continue
				}
				if v, ok := first[k]; ok {
					f[k] = nodemodel.CloneValue(v)
				} else {
					delete(f, k)
				}
			}
		}
	}
	interfaceAddress(f, "ip", "ip-cidr", IsIPv4Literal, 32)
	interfaceAddress(f, "ipv6", "ipv6-cidr", IsIPv6Literal, 128)
	return nil
}

// interfaceAddress is normaliser.md section 2.3 for one address family.
func interfaceAddress(f map[string]any, field, cidrKey string, literal func(string) bool, limit float64) {
	v, has := f[field]
	s := ""
	if has {
		s = trimES(text(v))
	}
	addr, suffix := s, ""
	if i := strings.LastIndexByte(s, '/'); i >= 0 && digitsOnly(s[i+1:]) {
		addr, suffix = s[:i], s[i+1:]
	}
	if len(addr) >= 2 && addr[0] == '[' && addr[len(addr)-1] == ']' {
		addr = addr[1 : len(addr)-1]
	}
	if !literal(addr) {
		if !has || s == "" {
			delete(f, cidrKey)
		}
		return
	}
	f[field] = addr
	cidr := limit
	if c, ok := f[cidrKey]; ok && digitsOnly(text(c)) && digitsValue(text(c)) <= limit {
		cidr = digitsValue(text(c))
	} else if suffix != "" && digitsValue(suffix) <= limit {
		cidr = digitsValue(suffix)
	}
	f[cidrKey] = cidr
}

// truthy is ECMAScript truthiness over model values: null, false, 0,
// not-a-number and the empty text are false; objects and lists, empty ones
// included, are true.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	case int64:
		return x != 0
	case int:
		return x != 0
	}
	return true
}

func truthyKey(m map[string]any, k string) bool {
	v, ok := m[k]
	return ok && truthy(v)
}

func anyTruthy(l []any) bool {
	for _, e := range l {
		if truthy(e) {
			return true
		}
	}
	return false
}

func isList(v any) bool {
	_, ok := v.([]any)
	return ok
}

// text is ECMAScript's String(value) over model values.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		switch {
		case math.IsNaN(x):
			return "NaN"
		case math.IsInf(x, 1):
			return "Infinity"
		case math.IsInf(x, -1):
			return "-Infinity"
		}
		return nodemodel.FormatNumber(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = text(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// textOf is String(m[k]), with "undefined" for an absent key.
func textOf(m map[string]any, k string) string {
	v, ok := m[k]
	if !ok {
		return "undefined"
	}
	return text(v)
}

// trimES removes what ECMAScript's String.prototype.trim removes: white
// space and line terminators, the byte order mark U+FEFF included (which
// strings.TrimSpace keeps).
func trimES(s string) string {
	return strings.TrimFunc(s, isESWhiteSpace)
}

func isESWhiteSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0xfeff, 0x2028, 0x2029:
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// digitsValue is Number(digits) for a text of decimal digits: the nearest
// double, as ECMAScript reads it.
func digitsValue(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.Inf(1) // only for runs of digits too long for a double
	}
	return v
}

func isIPLiteral(s string) bool { return IsIPv4Literal(s) || IsIPv6Literal(s) }

// optsKey is "<network>-opts" with the node's current network, written as
// a template literal would write it.
func optsKey(f map[string]any) string { return textOf(f, "network") + "-opts" }

// copyKey copies src[from] to dst[to] when it is present (null included), as
// an object literal over an undefined value would leave the key out.
func copyKey(dst map[string]any, to string, src map[string]any, from string) {
	if v, ok := src[from]; ok {
		dst[to] = nodemodel.CloneValue(v)
	}
}

// containerAt returns the object at m[k], creating it when the key is absent
// or null. A key that holds anything else is not replaced, and ok is false.
func containerAt(m map[string]any, k string) (map[string]any, bool) {
	switch v := m[k].(type) {
	case map[string]any:
		return v, true
	case nil:
		o := map[string]any{}
		m[k] = o
		return o, true
	}
	return nil, false
}

// decodeUTF8 decodes bytes as UTF-8 with replacement the way the WHATWG
// decoder does: each maximal ill-formed subsequence becomes one U+FFFD.
func decodeUTF8(b []byte) string {
	var out strings.Builder
	for i := 0; i < len(b); {
		c := b[i]
		if c < utf8.RuneSelf {
			out.WriteByte(c)
			i++
			continue
		}
		// Expected continuation count and the allowed range of the first
		// continuation byte (Unicode table 3-7).
		need, lo, hi := 0, byte(0x80), byte(0xbf)
		switch {
		case c >= 0xc2 && c <= 0xdf:
			need = 1
		case c == 0xe0:
			need, lo = 2, 0xa0
		case c == 0xed:
			need, hi = 2, 0x9f
		case c >= 0xe1 && c <= 0xef:
			need = 2
		case c == 0xf0:
			need, lo = 3, 0x90
		case c == 0xf4:
			need, hi = 3, 0x8f
		case c >= 0xf1 && c <= 0xf3:
			need = 3
		default:
			out.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < need; k++ {
			if j >= len(b) {
				ok = false
				break
			}
			low, high := byte(0x80), byte(0xbf)
			if k == 0 {
				low, high = lo, hi
			}
			if b[j] < low || b[j] > high {
				ok = false
				break
			}
			j++
		}
		if !ok {
			out.WriteRune(utf8.RuneError)
			i = j
			continue
		}
		out.Write(b[i:j])
		i = j
	}
	return out.String()
}
