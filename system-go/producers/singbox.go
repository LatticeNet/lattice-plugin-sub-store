package producers

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// The sing-box producer (specs/producers/singbox.md): a configuration
// fragment {"outbounds": [...], "endpoints": [...]}. After the steps before
// the producer, the ClashMeta producer's internal mode (ClashMetaInternal)
// restores mihomo's field shapes, the shadow-tls plugin keys are put back,
// and each node converts to one outbound, an outbound and its shadow-tls
// helper, or one endpoint. A node that fails a row of the unsupported rule
// yields nothing.
//
// Entries are written with tag, type, server and server_port first and then
// the fields in the order they are set; the structural comparison sorts keys,
// so that order is not part of conformance.

type singBoxProducer struct{}

func (singBoxProducer) ID() string { return "singbox" }

// EmptyDocument is the skeleton with two empty lists under every option
// (singbox.md, "Empty document"). It is never the empty string, so the
// zero-node rule counts Result.Entries.
func (singBoxProducer) EmptyDocument(Options) []byte {
	return []byte("{\n  \"outbounds\": [],\n  \"endpoints\": []\n}")
}

func (singBoxProducer) Produce(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options) (Result, error) {
	ps, dropped := prepare(nodes, target, "singbox", opts)
	res := Result{Dropped: dropped}
	include := opts.Truthy("include-unsupported-proxy")
	in := make([]*nodemodel.Node, len(ps))
	for i := range ps {
		in[i] = ps[i].node
	}
	passed := ClashMetaInternal(in, opts)
	var outbounds, endpoints [][]byte
	for i, p := range ps {
		received := p.node.Fields
		f := passed[i].Fields
		putBackShadowTLS(f, received)
		entries, err := singBoxEntries(f, received, include)
		var written [][]byte
		for _, e := range entries {
			if err != nil {
				break
			}
			var b []byte
			b, err = appendJSONIndent(nil, e, "    ")
			written = append(written, b)
		}
		if err != nil {
			reason := ReasonFailed
			if errors.Is(err, errNoSingBoxForm) {
				reason = ReasonUnsupported
			}
			res.Dropped = append(res.Dropped, Dropped{Index: p.index, Type: typeOf(received), Reason: reason})
			continue
		}
		for j, e := range entries {
			if t, _ := e.get("type"); t == "wireguard" || t == "tailscale" {
				endpoints = append(endpoints, written[j])
			} else {
				outbounds = append(outbounds, written[j])
			}
		}
		res.Entries += len(entries)
	}
	sortDropped(res.Dropped)
	out := append([]byte(`{`+"\n"+`  "outbounds": `), singBoxList(outbounds)...)
	out = append(out, ",\n  \"endpoints\": "...)
	out = append(append(out, singBoxList(endpoints)...), "\n}"...)
	dst.Write(out)
	return res, nil
}

// singBoxList writes entries already indented for the second level as the
// list JSON.stringify(value, null, 2) writes at the first.
func singBoxList(entries [][]byte) []byte {
	if len(entries) == 0 {
		return []byte("[]")
	}
	out := []byte("[")
	for i, e := range entries {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(append(out, "\n    "...), e...)
	}
	return append(out, "\n  ]"...)
}

// putBackShadowTLS is pipeline step 2: a node that entered the producer with
// a shadow-tls plugin gets its plugin, plugin-opts and obfs-opts (or their
// absence) back after the ClashMeta pass, undoing T1 and T10; the other
// effects of T1 stay.
func putBackShadowTLS(f, received map[string]any) {
	if _, ok := shadowTLSPlugin(received); !ok {
		return
	}
	f["plugin"] = received["plugin"]
	f["plugin-opts"] = received["plugin-opts"]
	if v, ok := received["obfs-opts"]; ok {
		f["obfs-opts"] = v
	} else {
		delete(f, "obfs-opts")
	}
}

// errNoSingBoxForm marks the rows of the unsupported rule that are about the
// type itself rather than the node's settings: F8 (ssr without the option),
// F9 (a snell version sing-box does not know) and F15 (a type with no
// sing-box form). They are reported as unsupported, the other rows as failed.
var errNoSingBoxForm = errors.New("no sing-box form")

func sbFail(row, what string) error { return fmt.Errorf("sing-box %s: %s", row, what) }

func sbUnsupported(row, what string) error {
	return fmt.Errorf("sing-box %s: %s: %w", row, what, errNoSingBoxForm)
}

// singBoxEntries converts one node after the ClashMeta pass: the unsupported
// rule's rows F1 to F15 in order, then the per-type conversion, which applies
// F16 to F19. received is the node as the producer received it, for the
// plugin options' key order.
func singBoxEntries(f, received map[string]any, include bool) ([]*object, error) {
	typ, _ := f["type"].(string)
	st, hasST := shadowTLSBlock(f, typ)
	enabled := hasST && shadowTLSEnabled(st)
	stream := typ == "vmess" || typ == "vless" || typ == "trojan"
	chained := enabled && stream
	switch {
	case chained && set(f, "reality-opts"):
		return nil, sbFail("F1", "shadow-tls with reality")
	case enabled && (typ == "vmess" || typ == "vless") && f["network"] == "h2":
		return nil, sbFail("F2", "shadow-tls with h2")
	case enabled && typ == "vless" && f["flow"] == "xtls-rprx-vision":
		return nil, sbFail("F3", "shadow-tls with xtls-rprx-vision")
	}
	var stVersion float64
	if chained {
		v, ok := sbStreamShadowTLSVersion(st)
		if !ok {
			return nil, sbFail("F4", "shadow-tls version")
		}
		stVersion = v
	}
	switch {
	case enabled && typ == "anytls":
		return nil, sbFail("F5", "anytls with shadow-tls")
	case f["network"] == "xhttp":
		return nil, sbFail("F6", "xhttp")
	case typ == "socks5" && set(f, "tls"):
		return nil, sbFail("F7", "socks5 with tls")
	case typ == "ssr" && !include:
		return nil, sbUnsupported("F8", "ssr")
	}
	if v, ok := f["version"]; ok && typ == "snell" && !sbSnellVersionOK(v, include) {
		return nil, sbUnsupported("F9", "snell version")
	}
	if typ == "vmess" && set(f, "network") {
		switch f["network"] {
		case "tcp", "ws", "grpc", "h2", "http":
		default:
			return nil, sbFail("F10", "vmess network")
		}
	}
	switch {
	case typ == "vless" && set(f, "encryption") && f["encryption"] != "none":
		return nil, sbFail("F11", "vless encryption")
	case typ == "vless" && set(f, "flow") && f["flow"] != "xtls-rprx-vision":
		return nil, sbFail("F12", "vless flow")
	case typ == "trojan" && set(f, "flow"):
		return nil, sbFail("F13", "trojan flow")
	case typ == "tuic" && set(f, "token"):
		return nil, sbFail("F14", "tuic token")
	}

	var entries []*object
	var err error
	switch typ {
	case "ssh":
		entries, err = one(sbSSH(f))
	case "http":
		entries, err = one(sbHTTPProxy(f))
	case "socks5":
		entries, err = one(sbSocks(f))
	case "ss":
		entries, err = sbShadowsocks(f, received, st, hasST)
	case "ssr":
		entries, err = one(sbShadowsocksR(f))
	case "snell":
		entries, err = sbSnell(f, include, st, hasST)
	case "vmess", "vless", "trojan":
		entries, err = sbStream(f, typ, chained, stVersion, st)
	case "naive":
		entries, err = one(sbNaive(f))
	case "hysteria":
		entries, err = one(sbHysteria(f))
	case "hysteria2":
		entries, err = one(sbHysteria2(f))
	case "tuic":
		entries, err = one(sbTUIC(f))
	case "anytls":
		entries, err = one(sbAnyTLS(f))
	case "wireguard":
		entries, err = one(sbWireGuard(f))
	case "tailscale":
		entries, err = one(sbTailscale(f))
	default:
		return nil, sbUnsupported("F15", fmt.Sprintf("type %q", typ))
	}
	if err != nil {
		return nil, err
	}
	// With the option, name-cert-verify becomes every TLS block's
	// certificate_server_name.
	if include && set(f, "name-cert-verify") {
		for _, e := range entries {
			if t, ok := e.get("tls"); ok {
				if tls, ok := t.(*object); ok {
					tls.set("certificate_server_name", f["name-cert-verify"])
				}
			}
		}
	}
	return entries, nil
}

func one(e *object, err error) ([]*object, error) {
	if err != nil {
		return nil, err
	}
	return []*object{e}, nil
}

// sbStreamShadowTLSVersion is the shadow-tls version of vmess, vless and
// trojan: absent and 0 map to 2, and anything else must be an integer from 1
// to 3 (F4).
func sbStreamShadowTLSVersion(st map[string]any) (float64, bool) {
	v, ok := st["version"]
	if !ok || numberIsZero(v) {
		return 2, true
	}
	if !integerIn(v, 1, 3) {
		return 0, false
	}
	return sbNumber(v), true
}

// sbSnellVersionOK is F9's test of a present snell version: trimmed digits
// from 4 to 6, or 1 to 6 with include-unsupported-proxy.
func sbSnellVersionOK(v any, include bool) bool {
	t := trimES(text(v))
	if !digitsOnly(t) {
		return false
	}
	lo := 4.0
	if include {
		lo = 1
	}
	n := sbNumber(t)
	return n >= lo && n <= 6
}

// sbNumber is a value read as a number: numbers as they are, text trimmed and
// read as ECMAScript's Number reads it.
func sbNumber(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	return jsNumber(trimES(text(v)))
}

// jsParseInt is ECMAScript's parseInt(s, 10), what the specification calls
// "the integer" of a value's text form: leading white space skipped, an
// optional sign, then the digits up to the first non-digit. No digits gives
// not-a-number, which the writer emits as null.
func jsParseInt(s string) float64 {
	s = strings.TrimLeftFunc(s, isESWhiteSpace)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return math.NaN()
	}
	n, _ := strconv.ParseFloat(s[:i], 64)
	if neg {
		n = -n
	}
	return n
}

// sbInt is the integer of v's text form.
func sbInt(v any) float64 { return jsParseInt(text(v)) }

// sbCopy writes f[src] as key when it is present, null included, as an
// ECMAScript object literal writes a property whose value is not undefined.
func sbCopy(e *object, key string, f map[string]any, src string) {
	if v, ok := f[src]; ok {
		e.set(key, v)
	}
}

func sbObject(kv ...any) *object {
	o := &object{}
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

// sbBase starts an entry: tag and type, then server and server_port for the
// types that have them.
func sbBase(f map[string]any, outType string, server bool) (*object, error) {
	e := &object{}
	sbCopy(e, "tag", f, "name")
	e.set("type", outType)
	if server {
		sbCopy(e, "server", f, "server")
		port, err := sbPort(f)
		if err != nil {
			return nil, err
		}
		e.set("server_port", port)
	}
	return e, nil
}

// sbPort is the integer of the port's text form; not-a-number is written as
// null, and a number outside 0 to 65535 fails the node (F16).
func sbPort(f map[string]any) (float64, error) {
	n := jsParseInt(textOf(f, "port"))
	if n < 0 || n > 65535 {
		return 0, sbFail("F16", "port out of range")
	}
	return n, nil
}

// The common fields (singbox.md, "Common fields"), as a set of flags each
// type picks from.
type sbFields uint8

const (
	sbDetour sbFields = 1 << iota
	sbTCPFastOpen
	sbUDPFragment
	sbNetwork
	sbResolver
	sbMultiplex
	sbUoT
)

func sbCommon(e *object, f map[string]any, fields sbFields) {
	if fields&sbDetour != 0 {
		if set(f, "dialer-proxy") {
			e.set("detour", f["dialer-proxy"])
		} else if set(f, "detour") {
			e.set("detour", f["detour"])
		}
	}
	if fields&sbTCPFastOpen != 0 && (set(f, "tfo") || set(f, "tcp_fast_open") || set(f, "tcp-fast-open")) {
		e.set("tcp_fast_open", true)
	}
	if fields&sbUDPFragment != 0 && set(f, "fast-open") {
		e.set("udp_fragment", true)
	}
	if fields&sbNetwork != 0 {
		switch {
		case f["_network"] == "tcp" || f["_network"] == "udp":
			e.set("network", f["_network"])
		case f["udp"] == false:
			e.set("network", "tcp")
		}
	}
	if fields&sbResolver != 0 {
		if dr := sbDomainResolver(f); dr != nil {
			e.set("domain_resolver", dr)
		}
	}
	if fields&sbMultiplex != 0 {
		if m := sbMux(f); m != nil {
			e.set("multiplex", m)
		}
	}
	if fields&sbUoT != 0 {
		if set(f, "uot") {
			e.set("udp_over_tcp", true)
		}
		if set(f, "udp-over-tcp") {
			version := 2.0
			if v, ok := f["udp-over-tcp-version"]; !ok || text(v) == "1" {
				version = 1
			}
			e.set("udp_over_tcp", sbObject("enabled", true, "version", version))
		}
	}
}

var sbStrategies = map[string]string{
	"ipv4": "ipv4_only", "v4-only": "ipv4_only",
	"ipv6": "ipv6_only", "v6-only": "ipv6_only",
	"ipv4-prefer": "prefer_ipv4", "prefer-v4": "prefer_ipv4",
	"ipv6-prefer": "prefer_ipv6", "prefer-v6": "prefer_ipv6",
}

// sbDomainResolver is {server, strategy} from _dns_server and ip-version,
// then _domain_resolver: text sets server, a mapping is merged over it.
func sbDomainResolver(f map[string]any) *object {
	var dr *object
	if v, ok := f["ip-version"]; ok && set(f, "_dns_server") {
		if s, ok := sbStrategies[text(v)]; ok {
			dr = sbObject("server", f["_dns_server"], "strategy", s)
		}
	}
	switch x := f["_domain_resolver"].(type) {
	case string:
		if x != "" {
			if dr == nil {
				dr = &object{}
			}
			dr.set("server", x)
		}
	case map[string]any:
		if dr == nil {
			dr = &object{}
		}
		for _, k := range propertyOrder(x) {
			dr.set(k, x[k])
		}
	}
	return dr
}

// sbMux is multiplex from an enabled smux block.
func sbMux(f map[string]any) *object {
	smux, _ := obj(f, "smux")
	if !set(smux, "enabled") {
		return nil
	}
	m := sbObject("enabled", true)
	if set(smux, "protocol") {
		m.set("protocol", smux["protocol"])
	}
	for _, k := range [...][2]string{{"max-connections", "max_connections"}, {"max-streams", "max_streams"}, {"min-streams", "min_streams"}} {
		if set(smux, k[0]) {
			m.set(k[1], sbInt(smux[k[0]]))
		}
	}
	if set(smux, "padding") {
		m.set("padding", true)
	}
	if b, _ := obj(smux, "brutal-opts"); set(b, "up") || set(b, "down") {
		m.set("brutal", sbObject("enabled", true, "up_mbps", sbInt(b["up"]), "down_mbps", sbInt(b["down"])))
	}
	return m
}

// sbTLSStartsEnabled are the types whose TLS block starts enabled; http,
// vmess and vless start disabled.
var sbTLSStartsEnabled = map[string]bool{
	"trojan": true, "naive": true, "hysteria": true, "hysteria2": true, "tuic": true, "anytls": true,
}

var sbFingerprints = map[string]bool{
	"chrome": true, "firefox": true, "edge": true, "safari": true, "360": true, "qq": true,
	"ios": true, "android": true, "random": true, "randomized": true,
}

// sbUTLS is {enabled, fingerprint} for a known client fingerprint.
func sbUTLS(f map[string]any) (*object, bool) {
	v, ok := f["client-fingerprint"]
	if !ok || v == nil {
		return nil, false
	}
	fp := strings.ToLower(trimES(text(v)))
	if !sbFingerprints[fp] {
		return nil, false
	}
	return sbObject("enabled", true, "fingerprint", fp), true
}

// sbTLS is the TLS block (singbox.md, "TLS block"), or nil when it ends
// disabled. h2 says an h2 transport enables it.
func sbTLS(f map[string]any, typ string, h2 bool) (*object, error) {
	t := sbObject("enabled", sbTLSStartsEnabled[typ])
	sbCopy(t, "server_name", f, "server")
	t.set("insecure", false)
	if set(f, "tls") || h2 {
		t.set("enabled", true)
	}
	for _, k := range []string{"servername", "peer", "sni"} {
		if set(f, k) {
			t.set("server_name", f[k])
		}
	}
	if set(f, "skip-cert-verify") || set(f, "insecure") {
		t.set("insecure", true)
	}
	if set(f, "disable-sni") {
		t.set("disable_sni", true)
	}
	switch a := f["alpn"].(type) {
	case string:
		if a != "" {
			t.set("alpn", []any{a})
		}
	case []any:
		t.set("alpn", a)
	}
	reality := set(f, "reality-opts")
	if reality {
		r, _ := obj(f, "reality-opts")
		ro := sbObject("enabled", true)
		if set(r, "public-key") {
			ro.set("public_key", r["public-key"])
		}
		if set(r, "short-id") {
			ro.set("short_id", r["short-id"])
		}
		t.set("reality", ro)
		t.set("utls", sbObject("enabled", true))
	}
	switch typ {
	case "hysteria", "hysteria2", "tuic":
	default:
		if u, ok := sbUTLS(f); ok {
			t.set("utls", u)
		}
	}
	sbECH(t, f)
	if l, ok := f["_curve_preferences"].([]any); ok {
		t.set("curve_preferences", l)
	}
	if set(f, "_fragment") {
		t.set("fragment", true)
	}
	if set(f, "_record_fragment") {
		t.set("record_fragment", true)
	}
	if set(f, "_fragment_fallback_delay") {
		t.set("fragment_fallback_delay", f["_fragment_fallback_delay"])
	}
	for _, k := range []string{"client_certificate", "client_certificate_path", "client_key", "client_key_path"} {
		if set(f, "_"+k) {
			t.set(k, f["_"+k])
		}
	}
	if v, _ := t.get("enabled"); v != true {
		return nil, nil
	}
	if err := sbCertificates(t, f, reality); err != nil {
		return nil, err
	}
	return t, nil
}

// sbECH is ech: _ech as it is when it is a mapping, else built from an
// ech-opts mapping.
func sbECH(t *object, f map[string]any) {
	if m, ok := f["_ech"].(map[string]any); ok {
		t.set("ech", m)
		return
	}
	eo, ok := f["ech-opts"].(map[string]any)
	if !ok {
		return
	}
	ech := &object{}
	sbCopy(ech, "enabled", eo, "enable")
	if lines := sbPEMLines(eo["config"]); len(lines) > 0 {
		ech.set("config", lines)
	}
	for _, k := range [...][2]string{
		{"query-server-name", "query_server_name"}, {"config-path", "config_path"}, {"fragment", "fragment"},
		{"fragment-fallback-delay", "fragment_fallback_delay"}, {"record-fragment", "record_fragment"},
	} {
		sbCopy(ech, k[1], eo, k[0])
	}
	t.set("ech", ech)
}

// sbPEMLines is the ECH configuration as PEM lines: each text trimmed, the
// literal sequences \r\n and \n turned into line breaks, the lines trimmed
// and the empty ones dropped, wrapped in ECH CONFIGS markers unless a line
// already begins a PEM block.
func sbPEMLines(v any) []any {
	var items []string
	switch x := v.(type) {
	case string:
		items = []string{x}
	case []any:
		for _, e := range x {
			items = append(items, text(e))
		}
	}
	var lines []any
	begin := false
	for _, item := range items {
		s := strings.ReplaceAll(trimES(item), `\r\n`, "\n")
		s = strings.ReplaceAll(s, `\n`, "\n")
		for _, l := range strings.Split(s, "\n") {
			if l = trimES(l); l == "" {
				continue
			}
			if len(l) > len("-----BEGIN -----") && strings.HasPrefix(l, "-----BEGIN ") && strings.HasSuffix(l, "-----") {
				begin = true
			}
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 || begin {
		return lines
	}
	return append(append([]any{"-----BEGIN ECH CONFIGS-----"}, lines...), "-----END ECH CONFIGS-----")
}

// sbCertificates applies the certificate rules to an enabled TLS block or a
// shadow-tls helper's (singbox.md, "Certificates").
func sbCertificates(t *object, f map[string]any, reality bool) error {
	if set(f, "ca") {
		t.set("certificate_path", f["ca"])
	}
	for _, k := range []string{"ca_str", "ca-str"} {
		if set(f, k) {
			t.set("certificate", []any{f[k]})
		}
	}
	if set(f, "_certificate") {
		t.set("certificate", f["_certificate"])
	}
	if set(f, "_certificate_path") {
		t.set("certificate_path", f["_certificate_path"])
	}
	if reality {
		return nil
	}
	if set(f, "_certificate_public_key_sha256") {
		t.set("certificate_public_key_sha256", f["_certificate_public_key_sha256"])
	}
	_, cert := t.get("certificate")
	_, path := t.get("certificate_path")
	_, pin := t.get("certificate_public_key_sha256")
	if v, ok := f["_certificate_sha256"]; ok {
		t.set("certificate_sha256", v)
	} else if fp := sbFingerprintPin(f); fp != nil && !cert && !path && !pin {
		digits := strings.ReplaceAll(text(fp), ":", "")
		raw, err := hex.DecodeString(digits)
		if len(digits) != 64 || err != nil {
			return sbFail("F19", "certificate pin is not 64 hex digits")
		}
		t.set("certificate_sha256", []any{base64.StdEncoding.EncodeToString(raw)})
	}
	if cert || path {
		for _, k := range []string{"certificate_sha256", "certificate_public_key_sha256"} {
			if v, ok := t.get(k); ok && !sbBlank(v) {
				return sbFail("F19", "certificate pin together with a certificate")
			}
		}
	}
	return nil
}

func sbFingerprintPin(f map[string]any) any {
	for _, k := range []string{"fingerprint", "tls-fingerprint"} {
		if set(f, k) {
			return f[k]
		}
	}
	return nil
}

// sbBlank reports an absent or empty value: null, empty text, an empty list
// or an empty mapping.
func sbBlank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// sbTransport writes the transport of vmess, vless and trojan (singbox.md,
// "Transports") and reports whether it is h2, which enables TLS. vmess and
// vless convert ws, http, h2 and grpc; trojan ws and grpc only; any other
// network writes none.
func sbTransport(e *object, f map[string]any, typ string) (bool, error) {
	both := typ == "vmess" || typ == "vless"
	var tr *object
	var err error
	h2 := false
	switch f["network"] {
	case "ws":
		tr, err = sbWS(f)
	case "grpc":
		tr = sbObject("type", "grpc")
		if g, _ := obj(f, "grpc-opts"); set(g, "grpc-service-name") {
			tr.set("service_name", g["grpc-service-name"])
		}
	case "http":
		if both {
			tr = sbHTTPTransport(f)
		}
	case "h2":
		if both {
			tr, err = sbH2(f)
			h2 = true
		}
	}
	if err != nil {
		return false, err
	}
	if tr != nil {
		e.set("transport", tr)
	}
	return h2, nil
}

// sbWS is the ws or httpupgrade transport.
func sbWS(f map[string]any) (*object, error) {
	o, _ := obj(f, "ws-opts")
	upgrade := set(o, "v2ray-http-upgrade")
	tr := sbObject("type", "ws")
	if upgrade {
		tr.set("type", "httpupgrade")
	}
	headers, err := sbWSHeaders(f, o)
	if err != nil {
		return nil, err
	}
	tr.set("headers", headers)
	if set(o, "path") {
		tr.set("path", o["path"])
	}
	if set(f, "ws-path") {
		tr.set("path", f["ws-path"])
	}
	if set(o, "early-data-header-name") {
		tr.set("early_data_header_name", o["early-data-header-name"])
	}
	if set(o, "max-early-data") {
		tr.set("max_early_data", sbInt(o["max-early-data"]))
	}
	if p, ok := tr.get("path"); ok {
		path := text(p)
		if v, ok := firstQueryValue(path, "ed"); ok {
			if ed, ok := safeInteger(v); ok {
				tr.set("path", removeQueryParam(path, "ed"))
				tr.set("early_data_header_name", "Sec-WebSocket-Protocol")
				tr.set("max_early_data", edNumber(ed))
			}
		}
	}
	if upgrade {
		if h, ok := headers.get("Host"); ok {
			if host, ok := first(h); ok {
				tr.set("host", host)
			}
			headers.del("Host")
		}
		tr.del("early_data_header_name")
		tr.del("max_early_data")
	}
	return tr, nil
}

// sbWSHeaders builds the websocket headers: ws-opts.headers, then legacy
// ws-headers over them, empty text skipped and scalars as one-element lists
// of their text. A non-empty ws-opts.headers needs a Host with a value (F17),
// and a single Host value is read as "Host:" lines, which drops a port and
// splits a comma list. One-element lists are then written as their element.
func sbWSHeaders(f, o map[string]any) (*object, error) {
	h := &object{}
	add := func(m map[string]any) {
		for _, k := range propertyOrder(m) {
			switch v := m[k].(type) {
			case string:
				if v != "" {
					h.set(k, []any{v})
				}
			case []any:
				h.set(k, v)
			default:
				h.set(k, []any{text(v)})
			}
		}
	}
	wo, _ := obj(o, "headers")
	add(wo)
	if wh, ok := obj(f, "ws-headers"); ok {
		add(wh)
	}
	if len(wo) > 0 {
		v, _ := h.get("Host")
		host, _ := v.([]any)
		if len(host) == 0 || !truthy(host[0]) {
			return nil, sbFail("F17", "websocket headers without a Host")
		}
		if len(host) == 1 {
			for _, line := range strings.Split("Host:"+text(host[0]), "\n") {
				parts := strings.Split(line, ":")
				if len(parts) < 2 {
					continue
				}
				value := trimES(parts[1])
				if value == "" {
					continue
				}
				var list []any
				for _, s := range strings.Split(value, ",") {
					list = append(list, s)
				}
				h.set(trimES(parts[0]), list)
			}
		}
	}
	unwrapSingles(h)
	return h, nil
}

// unwrapSingles writes every one-element list in h as its element.
func unwrapSingles(h *object) {
	for _, k := range h.keys {
		if l, ok := h.vals[k].([]any); ok && len(l) == 1 {
			h.vals[k] = l[0]
		}
	}
}

// splitTrim splits text on "," and trims each part.
func splitTrim(s string) []any {
	parts := strings.Split(s, ",")
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = trimES(p)
	}
	return out
}

// sbHTTPTransport is the http transport of vmess and vless.
func sbHTTPTransport(f map[string]any) *object {
	o, _ := obj(f, "http-opts")
	tr := sbObject("type", "http")
	headers := &object{}
	hm, _ := obj(o, "headers")
	var host any
	for _, k := range propertyOrder(hm) {
		v := hm[k]
		if strings.EqualFold(k, "host") {
			if host == nil {
				host = v
			}
			continue
		}
		switch x := v.(type) {
		case string:
			if x != "" {
				headers.set(k, splitTrim(x))
			}
		case []any:
			headers.set(k, x)
		default:
			headers.set(k, splitTrim(text(v)))
		}
	}
	unwrapSingles(headers)
	tr.set("headers", headers)
	if set(o, "method") {
		tr.set("method", o["method"])
	}
	if l, ok := o["path"].([]any); ok {
		if len(l) > 0 {
			tr.set("path", l[0])
		}
	} else if set(o, "path") {
		tr.set("path", o["path"])
	}
	if set(f, "http-path") {
		tr.set("path", f["http-path"])
	}
	if host != nil && !isList(host) {
		host = splitTrim(text(host))
	}
	if set(f, "http-host") {
		host = f["http-host"]
	}
	if l, ok := host.([]any); ok && len(l) == 1 {
		host = l[0]
	}
	if host != nil {
		tr.set("host", host)
	}
	return tr
}

// sbH2 is the h2 transport: http with a path and a required host (F18).
func sbH2(f map[string]any) (*object, error) {
	o, _ := obj(f, "h2-opts")
	tr := sbObject("type", "http")
	if set(o, "path") {
		tr.set("path", o["path"])
	}
	host := o["host"]
	if set(f, "h2-host") {
		host = f["h2-host"]
	}
	if set(f, "h2-path") {
		tr.set("path", f["h2-path"])
	}
	if s, ok := host.(string); ok {
		var list []any
		for _, p := range strings.Split(s, ",") {
			list = append(list, p)
		}
		host = list
	}
	l, _ := host.([]any)
	if len(l) == 0 || (len(l) == 1 && !truthy(l[0])) {
		return nil, sbFail("F18", "h2 without a host")
	}
	if len(l) == 1 {
		tr.set("host", l[0])
	} else {
		tr.set("host", l)
	}
	return tr, nil
}

// sbIPv6 reports whether the node carries an IPv6 interface address.
func sbIPv6(f map[string]any) bool {
	_, ok := interfaceAddress(f, "ipv6", "ipv6-cidr", normalise.IsIPv6Literal, 128)
	return ok
}

var sbBandwidthUnit = regexp.MustCompile(`^[0-9]+[ \t]*[KMGT]?[bB]ps$`)

// The per-type conversions (singbox.md, "Per-type mapping"). Each picks its
// common fields from the table of the same name.

const (
	sbAll      = sbDetour | sbTCPFastOpen | sbUDPFragment | sbResolver
	sbStreamed = sbAll | sbNetwork | sbMultiplex
)

func sbSSH(f map[string]any) (*object, error) {
	e, err := sbBase(f, "ssh", true)
	if err != nil {
		return nil, err
	}
	sbCopy(e, "user", f, "username")
	sbCopy(e, "password", f, "password")
	key, ok := f["private-key"]
	if !ok {
		key, ok = f["privateKey"]
	}
	if ok {
		if strings.Contains(text(key), "PRIVATE KEY") {
			e.set("private_key", key)
		} else {
			e.set("private_key_path", key)
		}
	}
	sbCopy(e, "private_key_passphrase", f, "private-key-passphrase")
	if set(f, "server-fingerprint") {
		fp := text(f["server-fingerprint"])
		algorithm, _, _ := strings.Cut(fp, " ")
		e.set("host_key", []any{fp})
		e.set("host_key_algorithms", []any{algorithm})
	}
	sbCopy(e, "host_key", f, "host-key")
	sbCopy(e, "host_key_algorithms", f, "host-key-algorithms")
	sbCommon(e, f, sbAll)
	return e, nil
}

func sbHTTPProxy(f map[string]any) (*object, error) {
	e, err := sbBase(f, "http", true)
	if err != nil {
		return nil, err
	}
	sbCopy(e, "username", f, "username")
	sbCopy(e, "password", f, "password")
	if hm, ok := obj(f, "headers"); ok {
		headers := &object{}
		for _, k := range propertyOrder(hm) {
			if v := text(hm[k]); v != "" {
				headers.set(k, v)
			}
		}
		if headers.len() > 0 {
			e.set("headers", headers)
		}
	}
	if err := sbSetTLS(e, f, "http", false); err != nil {
		return nil, err
	}
	sbCommon(e, f, sbAll)
	return e, nil
}

func sbSocks(f map[string]any) (*object, error) {
	e, err := sbBase(f, "socks", true)
	if err != nil {
		return nil, err
	}
	e.set("version", "5")
	sbCopy(e, "username", f, "username")
	sbCopy(e, "password", f, "password")
	sbCommon(e, f, sbAll|sbNetwork|sbUoT)
	return e, nil
}

// sbSetTLS writes the TLS block when it ends enabled.
func sbSetTLS(e *object, f map[string]any, typ string, h2 bool) error {
	t, err := sbTLS(f, typ, h2)
	if err != nil {
		return err
	}
	if t != nil {
		e.set("tls", t)
	}
	return nil
}

// sbShadowsocks is the shadowsocks outbound with its plugin, or with a
// shadow-tls plugin the chained outbound and its helper.
func sbShadowsocks(f, received, st map[string]any, chained bool) ([]*object, error) {
	if chained {
		e := &object{}
		sbCopy(e, "tag", f, "name")
		e.set("type", "shadowsocks")
		sbCopy(e, "method", f, "cipher")
		sbCopy(e, "password", f, "password")
		e.set("detour", textOf(f, "name")+"_shadowtls")
		sbCommon(e, f, sbUoT|sbNetwork|sbMultiplex)
		version, hasVersion := st["version"]
		helper, err := sbShadowTLSHelper(f, st, version, hasVersion)
		if err != nil {
			return nil, err
		}
		return []*object{e, helper}, nil
	}
	e, err := sbBase(f, "shadowsocks", true)
	if err != nil {
		return nil, err
	}
	sbCopy(e, "method", f, "cipher")
	sbCopy(e, "password", f, "password")
	sbCommon(e, f, sbStreamed|sbUoT)
	if !set(f, "plugin") {
		return []*object{e}, nil
	}
	receivedOpts, _ := obj(received, "plugin-opts")
	po := sbPluginOpts(f, receivedOpts)
	var entries []string
	switch f["plugin"] {
	case "obfs":
		e.set("plugin", "obfs-local")
		if set(f, "obfs-host") {
			po.set("host", f["obfs-host"])
		}
		for _, k := range po.keys {
			switch v := po.vals[k]; k {
			case "mode":
				entries = append(entries, "obfs="+text(v))
			case "host":
				entries = append(entries, "obfs-host="+text(v))
			default:
				entries = append(entries, k+"="+text(v))
			}
		}
	case "v2ray-plugin":
		e.set("plugin", "v2ray-plugin")
		if set(f, "ws-host") {
			po.set("host", f["ws-host"])
		}
		if set(f, "ws-path") {
			po.set("path", f["ws-path"])
		}
		for _, k := range po.keys {
			switch v := po.vals[k]; k {
			case "tls":
				if truthy(v) {
					entries = append(entries, "tls")
				}
			case "headers":
				j, err := jsonText(v)
				if err != nil {
					return nil, err
				}
				entries = append(entries, "headers="+j)
			case "mux":
				mux := "0"
				if truthy(v) {
					mux = "1"
					e.set("multiplex", sbObject("enabled", true))
				}
				entries = append(entries, "mux="+mux)
			default:
				entries = append(entries, k+"="+text(v))
			}
		}
	}
	e.set("plugin_opts", strings.Join(entries, ";"))
	return []*object{e}, nil
}

// sbPluginOpts is plugin-opts in the order its entries are written: the keys
// the producer received in ascending order, then the keys the ClashMeta pass
// added (skip-cert-verify from T14). The caller appends the node-level host
// and path after them.
func sbPluginOpts(f, received map[string]any) *object {
	po, _ := obj(f, "plugin-opts")
	o := &object{}
	for _, k := range propertyOrder(received) {
		if v, ok := po[k]; ok {
			o.set(k, v)
		}
	}
	for _, k := range propertyOrder(po) {
		if _, ok := o.get(k); !ok {
			o.set(k, po[k])
		}
	}
	return o
}

func sbShadowsocksR(f map[string]any) (*object, error) {
	e, err := sbBase(f, "shadowsocksr", true)
	if err != nil {
		return nil, err
	}
	sbCopy(e, "method", f, "cipher")
	sbCopy(e, "password", f, "password")
	sbCopy(e, "obfs", f, "obfs")
	sbCopy(e, "protocol", f, "protocol")
	sbCopy(e, "obfs_param", f, "obfs-param")
	sbCopy(e, "protocol_param", f, "protocol-param")
	sbCommon(e, f, sbStreamed)
	return e, nil
}

// sbSnell is the snell outbound, chained to a helper when it carries
// shadow-tls.
func sbSnell(f map[string]any, include bool, st map[string]any, chained bool) ([]*object, error) {
	e, err := sbBase(f, "snell", !chained)
	if err != nil {
		return nil, err
	}
	sbCopy(e, "psk", f, "psk")
	v, hasVersion := f["version"]
	version := 0.0
	if hasVersion {
		version = sbNumber(v)
		if version == 5 && !include {
			version = 4
		}
		e.set("version", version)
	}
	sbCopy(e, "userkey", f, "_userkey")
	if hasVersion && version == 6 {
		if set(f, "mode") {
			e.set("mode", f["mode"])
		}
		if include && set(f, "quic-proxy-mode") {
			e.set("quic_proxy_mode", true)
		}
	} else if oo, ok := obj(f, "obfs-opts"); ok && oo["mode"] != "shadow-tls" {
		sbCopy(e, "obfs_mode", oo, "mode")
		sbCopy(e, "obfs_host", oo, "host")
	}
	if set(f, "reuse") && (!hasVersion || version >= 4) {
		e.set("reuse", true)
	}
	if !chained {
		sbCommon(e, f, sbAll|sbNetwork)
		return []*object{e}, nil
	}
	e.set("detour", textOf(f, "name")+"_shadowtls")
	sbCommon(e, f, sbNetwork)
	stVersion, hasSTVersion := st["version"]
	helper, err := sbShadowTLSHelper(f, st, stVersion, hasSTVersion)
	if err != nil {
		return nil, err
	}
	return []*object{e, helper}, nil
}

// sbStream is vmess, vless and trojan, chained to a shadow-tls helper when
// the block is enabled: the TLS block then goes and detour names the helper.
func sbStream(f map[string]any, typ string, chained bool, stVersion float64, st map[string]any) ([]*object, error) {
	e, err := sbBase(f, typ, true)
	if err != nil {
		return nil, err
	}
	switch typ {
	case "vmess":
		sbCopy(e, "uuid", f, "uuid")
		e.set("security", vmessSecurity(f))
		e.set("alter_id", sbInt(f["alterId"]))
		if pe, ok := sbPacketEncoding(f); ok {
			e.set("packet_encoding", pe)
		}
		for _, k := range [...][2]string{{"global-padding", "global_padding"}, {"authenticated-length", "authenticated_length"}} {
			if v, ok := f[k[0]]; ok {
				e.set(k[1], truthy(v))
			}
		}
	case "vless":
		sbCopy(e, "uuid", f, "uuid")
		if pe, ok := sbPacketEncoding(f); ok {
			e.set("packet_encoding", pe)
		}
		sbCopy(e, "flow", f, "flow")
	case "trojan":
		sbCopy(e, "password", f, "password")
	}
	h2, err := sbTransport(e, f, typ)
	if err != nil {
		return nil, err
	}
	if err := sbSetTLS(e, f, typ, h2); err != nil {
		return nil, err
	}
	sbCommon(e, f, sbStreamed)
	if !chained {
		return []*object{e}, nil
	}
	e.del("tls")
	e.set("detour", textOf(f, "name")+"_shadowtls")
	helper, err := sbShadowTLSHelper(f, st, stVersion, true)
	if err != nil {
		return nil, err
	}
	return []*object{e, helper}, nil
}

// sbPacketEncoding is the packet-encoding rule of vmess and vless.
func sbPacketEncoding(f map[string]any) (string, bool) {
	if v, ok := f["packet-encoding"]; ok {
		switch s := strings.ToLower(trimES(text(v))); s {
		case "", "packetaddr", "xudp":
			return s, true
		}
		return "", false
	}
	switch {
	case set(f, "xudp"):
		return "xudp", true
	case set(f, "packet-addr"):
		return "packetaddr", true
	}
	return "", false
}

// sbShadowTLSHelper is the shadowtls outbound a chained node dials through.
func sbShadowTLSHelper(f, st map[string]any, version any, hasVersion bool) (*object, error) {
	h := sbObject("tag", textOf(f, "name")+"_shadowtls", "type", "shadowtls")
	sbCopy(h, "server", f, "server")
	port, err := sbPort(f)
	if err != nil {
		return nil, err
	}
	h.set("server_port", port)
	if hasVersion {
		h.set("version", version)
	}
	sbCopy(h, "password", st, "password")
	t := sbObject("enabled", true)
	sbCopy(t, "server_name", st, "host")
	if set(f, "skip-cert-verify") {
		t.set("insecure", true)
	}
	if u, ok := sbUTLS(f); ok {
		t.set("utls", u)
	}
	if alpn := sbHelperALPN(st["alpn"]); alpn != nil {
		t.set("alpn", alpn)
	} else if alpn := sbHelperALPN(f["alpn"]); alpn != nil {
		t.set("alpn", alpn)
	}
	if err := sbCertificates(t, f, set(f, "reality-opts")); err != nil {
		return nil, err
	}
	h.set("tls", t)
	if f["fast-open"] == true {
		h.set("udp_fragment", true)
	}
	sbCommon(h, f, sbTCPFastOpen|sbDetour|sbResolver)
	return h, nil
}

// sbHelperALPN is a helper's alpn source: a list as it is, text split on ","
// and trimmed with the empty parts dropped.
func sbHelperALPN(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case string:
		var out []any
		for _, p := range strings.Split(x, ",") {
			if p = trimES(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

func sbNaive(f map[string]any) (*object, error) {
	e, err := sbBase(f, "naive", true)
	if err != nil {
		return nil, err
	}
	sbCopy(e, "username", f, "username")
	sbCopy(e, "password", f, "password")
	if v, ok := f["insecure-concurrency"]; ok {
		if n := sbInt(v); n >= 0 {
			e.set("insecure_concurrency", n)
		}
	}
	sbCopy(e, "extra_headers", f, "extra-headers")
	if set(f, "quic") {
		e.set("quic", true)
	}
	sbCopy(e, "quic_congestion_control", f, "quic-congestion-control")
	// singbox.md says tls.insecure is removed for naive; both naive goldens
	// (clash-naive, clash-include-unsupported-bypass) keep insecure: false,
	// and the goldens are what conformance judges.
	if err := sbSetTLS(e, f, "naive", false); err != nil {
		return nil, err
	}
	sbCommon(e, f, sbAll|sbMultiplex|sbUoT)
	return e, nil
}

// sbHopAndPorts writes hop_interval and server_ports for hysteria and
// hysteria2.
func sbHopAndPorts(e *object, f map[string]any) {
	if v, ok := f["hop-interval"]; ok {
		if s := text(v); digitsOnly(s) {
			e.set("hop_interval", s+"s")
		} else {
			e.set("hop_interval", v)
		}
	}
	if set(f, "ports") {
		var ports []any
		for _, item := range sbCommaSplit.Split(text(f["ports"]), -1) {
			item = sbRangeDash.ReplaceAllString(item, "-")
			if lo, hi, ok := strings.Cut(item, "-"); ok {
				ports = append(ports, lo+":"+hi)
			} else {
				ports = append(ports, item+":"+item)
			}
		}
		e.set("server_ports", ports)
	}
}

var (
	sbCommaSplit = regexp.MustCompile(`\s*,\s*`)
	sbRangeDash  = regexp.MustCompile(`\s*-\s*`)
)

func sbHysteria(f map[string]any) (*object, error) {
	e, err := sbBase(f, "hysteria", true)
	if err != nil {
		return nil, err
	}
	d := f["disable_mtu_discovery"]
	e.set("disable_mtu_discovery", d == true || sbIsOne(d))
	sbHopAndPorts(e, f)
	for _, k := range []string{"auth_str", "auth-str"} {
		if v, ok := f[k]; ok {
			e.set("auth_str", text(v))
		}
	}
	for _, k := range [...][2]string{{"up", "up_mbps"}, {"down", "down_mbps"}} {
		if u := textOf(f, k[0]); sbBandwidthUnit.MatchString(u) && !strings.HasSuffix(u, "Mbps") {
			e.set(k[0], u)
		} else {
			e.set(k[1], jsParseInt(u))
		}
	}
	sbCopy(e, "obfs", f, "obfs")
	for _, k := range [...][3]string{{"recv_window_conn", "recv_window_conn", "recv-window-conn"}, {"recv_window", "recv_window", "recv-window"}} {
		sbCopy(e, k[0], f, k[1])
		sbCopy(e, k[0], f, k[2])
	}
	if err := sbSetTLS(e, f, "hysteria", false); err != nil {
		return nil, err
	}
	sbCommon(e, f, sbStreamed)
	return e, nil
}

func sbIsOne(v any) bool {
	switch x := v.(type) {
	case float64:
		return x == 1
	case int64:
		return x == 1
	case int:
		return x == 1
	}
	return false
}

func sbHysteria2(f map[string]any) (*object, error) {
	e, err := sbBase(f, "hysteria2", true)
	if err != nil {
		return nil, err
	}
	sbCopy(e, "password", f, "password")
	if obfs, ok := f["obfs"].(string); ok && (obfs == "salamander" || obfs == "gecko") {
		o := sbObject("type", obfs)
		if obfs == "gecko" {
			sbGeckoSizes(o, f)
		}
		sbCopy(o, "password", f, "obfs-password")
		e.set("obfs", o)
	}
	sbHopAndPorts(e, f)
	for _, k := range [...][2]string{{"up", "up_mbps"}, {"down", "down_mbps"}} {
		if set(f, k[0]) {
			e.set(k[1], sbInt(f[k[0]]))
		}
	}
	sbCopy(e, "bbr_profile", f, "bbr-profile")
	if set(f, "disable-chrome-parrot") {
		e.set("disable_chrome_parrot", true)
	}
	if err := sbSetTLS(e, f, "hysteria2", false); err != nil {
		return nil, err
	}
	sbCommon(e, f, sbStreamed&^sbUDPFragment)
	return e, nil
}

// sbGeckoSizes writes the gecko packet sizes when the given ones are digits,
// positive and, with 512 and 1200 for a missing side and the maximum clamped
// to 2048, not inverted.
func sbGeckoSizes(o *object, f map[string]any) {
	sizes := [2]float64{512, 1200}
	given := [2]bool{}
	for i, k := range []string{"obfs-min-packet-size", "obfs-max-packet-size"} {
		v, ok := f[k]
		if !ok || v == nil || text(v) == "" {
			continue
		}
		s := text(v)
		if !digitsOnly(s) {
			return
		}
		n, _ := strconv.ParseFloat(s, 64)
		if n <= 0 {
			return
		}
		sizes[i], given[i] = n, true
	}
	if !given[0] && !given[1] {
		return
	}
	sizes[1] = math.Min(sizes[1], 2048)
	if sizes[1] < sizes[0] {
		return
	}
	if given[0] {
		o.set("min_packet_size", sizes[0])
	}
	if given[1] {
		o.set("max_packet_size", sizes[1])
	}
}

func sbTUIC(f map[string]any) (*object, error) {
	e, err := sbBase(f, "tuic", true)
	if err != nil {
		return nil, err
	}
	sbCopy(e, "uuid", f, "uuid")
	sbCopy(e, "password", f, "password")
	if v := f["congestion-controller"]; truthy(v) && v != "cubic" {
		e.set("congestion_control", v)
	}
	if v := f["udp-relay-mode"]; truthy(v) && v != "native" {
		e.set("udp_relay_mode", v)
	}
	if set(f, "reduce-rtt") {
		e.set("zero_rtt_handshake", true)
	}
	if set(f, "udp-over-stream") {
		e.set("udp_over_stream", true)
	}
	if v, ok := f["heartbeat-interval"]; ok {
		e.set("heartbeat", text(v)+"ms")
	}
	if err := sbSetTLS(e, f, "tuic", false); err != nil {
		return nil, err
	}
	sbCommon(e, f, sbStreamed)
	return e, nil
}

func sbAnyTLS(f map[string]any) (*object, error) {
	e, err := sbBase(f, "anytls", true)
	if err != nil {
		return nil, err
	}
	sbCopy(e, "password", f, "password")
	if v, ok := f["client-metadata"]; ok {
		e.set("client_metadata", text(v))
	}
	for _, k := range [...][2]string{{"idle-session-check-interval", "idle_session_check_interval"}, {"idle-session-timeout", "idle_session_timeout"}} {
		if s := textOf(f, k[0]); digitsOnly(s) {
			e.set(k[1], s+"s")
		}
	}
	if s := textOf(f, "min-idle-session"); digitsOnly(s) {
		e.set("min_idle_session", sbNumber(s))
	}
	if v, ok := f["disable-reuse"]; ok {
		e.set("disable_reuse", truthy(v))
	}
	if err := sbSetTLS(e, f, "anytls", false); err != nil {
		return nil, err
	}
	sbCommon(e, f, sbDetour|sbResolver)
	return e, nil
}

// sbDigitsUpTo is v's text when it is all digits and at most limit, as a
// number.
func sbDigitsUpTo(v any, limit float64) (float64, bool) {
	s := text(v)
	if v == nil || !digitsOnly(s) {
		return 0, false
	}
	n, _ := strconv.ParseFloat(s, 64)
	return n, n <= limit
}

var sbUDPNAT = map[any]bool{"endpoint_independent": true, "address_dependent": true, "address_and_port_dependent": true}

func sbWireGuard(f map[string]any) (*object, error) {
	if _, err := sbPort(f); err != nil {
		return nil, err
	}
	e := &object{}
	sbCopy(e, "tag", f, "name")
	e.set("type", "wireguard")
	e.set("system", truthy(f["system"]))
	if s, ok := f["_name"].(string); ok {
		e.set("name", s)
	}
	if n, ok := sbDigitsUpTo(f["_listen_port"], 65535); ok {
		e.set("listen_port", n)
	}
	if b, ok := f["_on_demand"].(bool); ok {
		e.set("on_demand", b)
	}
	if set(f, "mtu") {
		e.set("mtu", sbInt(f["mtu"]))
	}
	sbCopy(e, "udp_timeout", f, "udp-timeout")
	if n, ok := sbDigitsUpTo(f["_udp_nat_max"], 4294967295); ok {
		e.set("udp_nat_max", n)
	}
	if set(f, "workers") {
		e.set("workers", sbInt(f["workers"]))
	}
	address := []any{}
	if a, ok := interfaceAddress(f, "ip", "ip-cidr", normalise.IsIPv4Literal, 32); ok {
		address = append(address, a)
	}
	if a, ok := interfaceAddress(f, "ipv6", "ipv6-cidr", normalise.IsIPv6Literal, 128); ok {
		address = append(address, a)
	}
	e.set("address", address)
	sbCopy(e, "private_key", f, "private-key")
	for _, k := range []string{"udp_mapping", "udp_filtering"} {
		if v := f["_"+k]; sbUDPNAT[v] {
			e.set(k, v)
		}
	}
	peers, _ := f["peers"].([]any)
	if len(peers) == 0 {
		peers = []any{map[string]any{}}
	}
	out := make([]any, 0, len(peers))
	for _, raw := range peers {
		p, _ := raw.(map[string]any)
		out = append(out, sbPeer(f, p))
	}
	e.set("peers", out)
	sbCommon(e, f, sbAll|sbMultiplex)
	return e, nil
}

// sbPeer is one WireGuard peer, filled from the node where the peer is
// silent. The node-level allowed-ips and keepalive are not read.
func sbPeer(f, p map[string]any) *object {
	o := &object{}
	if set(p, "server") && set(p, "port") {
		o.set("address", p["server"])
		o.set("port", sbInt(p["port"]))
	} else {
		sbCopy(o, "address", f, "server")
		o.set("port", sbInt(f["port"]))
	}
	if set(p, "persistent-keepalive-interval") {
		o.set("persistent_keepalive_interval", sbInt(p["persistent-keepalive-interval"]))
	}
	if v := firstNonNil(p["public-key"], p["public_key"], f["public-key"]); v != nil {
		o.set("public_key", v)
	}
	if v := firstNonNil(p["pre-shared-key"], p["pre_shared_key"], f["pre-shared-key"]); v != nil {
		o.set("pre_shared_key", v)
	}
	if v := firstNonNil(p["allowed-ips"], p["allowed_ips"]); v != nil {
		o.set("allowed_ips", v)
	} else if sbIPv6(f) {
		o.set("allowed_ips", []any{"0.0.0.0/0", "::/0"})
	} else {
		o.set("allowed_ips", []any{"0.0.0.0/0"})
	}
	var reserved any
	switch x := p["reserved"].(type) {
	case string:
		reserved = []any{x}
	case []any:
		reserved = append([]any(nil), x...)
	}
	if l, _ := reserved.([]any); len(l) == 0 {
		reserved = nil
		switch x := f["reserved"].(type) {
		case []any:
			reserved = append([]any{}, x...)
		case nil:
		default:
			reserved = x
		}
	}
	if reserved != nil {
		o.set("reserved", reserved)
	}
	return o
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func sbTailscale(f map[string]any) (*object, error) {
	e := &object{}
	sbCopy(e, "tag", f, "name")
	e.set("type", "tailscale")
	if n, ok := sbDigitsUpTo(f["_listen_port"], 65535); ok {
		e.set("listen_port", n)
	}
	if s, ok := f["_taildrop_directory"].(string); ok {
		e.set("taildrop_directory", s)
	}
	if b, ok := f["_on_demand"].(bool); ok {
		e.set("on_demand", b)
	}
	sbCopy(e, "control_http_client", f, "control-http-client")
	sbCopy(e, "udp_timeout", f, "udp-timeout")
	if v, ok := f["state-dir"]; ok {
		e.set("state_directory", v)
	} else {
		sbCopy(e, "state_directory", f, "state-directory")
	}
	for _, k := range []string{"auth-key", "control-url", "ephemeral", "hostname", "accept-routes", "exit-node", "exit-node-allow-lan-access"} {
		sbCopy(e, strings.ReplaceAll(k, "-", "_"), f, k)
	}
	for _, k := range []string{"advertise-routes", "advertise-tags", "relay-server-static-endpoints"} {
		if l, ok := f[k].([]any); ok {
			e.set(strings.ReplaceAll(k, "-", "_"), l)
		}
	}
	for _, k := range []string{"advertise-exit-node", "system-interface", "system-interface-name"} {
		sbCopy(e, strings.ReplaceAll(k, "-", "_"), f, k)
	}
	for _, k := range []string{"system-interface-mtu", "relay-server-port"} {
		if s := textOf(f, k); digitsOnly(s) {
			e.set(strings.ReplaceAll(k, "-", "_"), sbNumber(s))
		}
	}
	if !sbControlHTTPClient(f["control-http-client"]) {
		sbCommon(e, f, sbDetour|sbResolver)
	}
	switch x := f["ssh-server"].(type) {
	case map[string]any:
		s := sbObject("enabled", x["enabled"] != false)
		for _, k := range []string{"disable-pty", "disable-sftp", "disable-forwarding"} {
			sbCopy(s, strings.ReplaceAll(k, "-", "_"), x, k)
		}
		e.set("ssh_server", s)
	default:
		if truthy(x) {
			e.set("ssh_server", true)
		}
	}
	return e, nil
}

// sbControlHTTPClient reports a control HTTP client: anything but absent,
// null, blank text or a mapping whose values are all absent, null or empty.
func sbControlHTTPClient(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return trimES(x) != ""
	case map[string]any:
		for _, e := range x {
			if !sbBlank(e) {
				return true
			}
		}
		return false
	}
	return true
}
