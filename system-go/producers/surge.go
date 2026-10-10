package producers

import (
	"bytes"
	"errors"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// The Surge producer (specs/producers/surge.md): one [Proxy] line per node,
// "<name>=<kind>,<server>,<port>" and ",key=value" parameters, entries joined
// by "\n" with no trailing newline. A mihomo WireGuard node, written only
// with include-unsupported-proxy, is a commented proxy line and the
// [WireGuard] section it needs. The parameters follow the specification's
// reference order (surge.md, "Ordering"), so the output also matches the
// golden bytes, which the structural comparison does not require.

type surgeProducer struct{}

func (surgeProducer) ID() string { return "surge" }

// EmptyDocument is the empty string (surge.md, "Empty document"). The module
// header case is not empty text but holds no entry.
func (surgeProducer) EmptyDocument(Options) []byte { return []byte{} }

// errSurgeUnsupported marks a node the Surge producer rejects.
var errSurgeUnsupported = errors.New("no Surge form")

func (surgeProducer) Produce(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options) (Result, error) {
	ps, dropped := prepare(nodes, target, "surge", opts)
	res := Result{Dropped: dropped}
	include := opts.Truthy("include-unsupported-proxy")

	// The module header: a target string starting with an upper-case Surge,
	// and every node that reached the producer a mihomo WireGuard node.
	if strings.HasPrefix(target, "Surge") && len(ps) > 0 && allWireGuard(ps) {
		dst.WriteString("#!name=" + textOf(ps[0].node.Fields, "_subName") + "\n")
		// The description and category exist only when a script sets them,
		// and a scripted chain never reaches a native producer.
		dst.WriteString("#!desc=\n#!category=\n")
	}
	for _, p := range ps {
		entry, err := surgeEntry(p.node.Fields, include)
		if err != nil {
			res.Dropped = append(res.Dropped, Dropped{Index: p.index, Type: typeOf(p.node.Fields), Reason: ReasonUnsupported})
			continue
		}
		if res.Entries > 0 {
			dst.WriteByte('\n')
		}
		dst.WriteString(entry)
		res.Entries++
	}
	sortDropped(res.Dropped)
	return res, nil
}

func allWireGuard(ps []prepared) bool {
	for _, p := range ps {
		if p.node.Fields["type"] != "wireguard" {
			return false
		}
	}
	return true
}

// surgeLine builds one proxy line or WireGuard parameter list.
type surgeLine struct {
	b strings.Builder
}

func (l *surgeLine) add(key, value string) {
	l.b.WriteByte(',')
	l.b.WriteString(key)
	l.b.WriteByte('=')
	l.b.WriteString(value)
}

// verbatim writes key=value from f[src] when present.
func (l *surgeLine) verbatim(key string, f map[string]any, src string) {
	if v, ok := present(f, src); ok {
		l.add(key, text(v))
	}
}

// quoted writes key="value" from f[src] when present.
func (l *surgeLine) quoted(key string, f map[string]any, src string) {
	if v, ok := present(f, src); ok {
		l.add(key, `"`+text(v)+`"`)
	}
}

// present reads a key that is there with a value other than null.
func present(m map[string]any, key string) (any, bool) {
	v, ok := m[key]
	return v, ok && v != nil
}

// strippedQuoted is "stripped and quoted": trimmed, one pair of matching
// outer quotes removed, then wrapped in double quotes.
func strippedQuoted(v any) string {
	return `"` + stripQuotes(trimES(text(v))) + `"`
}

// stripQuotes removes one pair of matching outer quotes, single or double.
func stripQuotes(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// surgeIPVersions maps ip-version to Surge's names; other values are written
// verbatim.
var surgeIPVersions = map[string]string{
	"dual": "dual", "ipv4": "v4-only", "ipv6": "v6-only", "ipv4-prefer": "prefer-v4", "ipv6-prefer": "prefer-v6",
}

func surgeIPVersion(v any) string {
	s := text(v)
	if m, ok := surgeIPVersions[s]; ok {
		return m
	}
	return s
}

// ipGroup is IP: ip-version, no-error-alert.
func (l *surgeLine) ipGroup(f map[string]any) {
	if v, ok := present(f, "ip-version"); ok {
		l.add("ip-version", surgeIPVersion(v))
	}
	l.verbatim("no-error-alert", f, "no-error-alert")
}

// tlsGroup is TLS: server-cert-fingerprint-sha256, sni,
// server-cert-verify-name, alpn (left to STLS under the shadow-tls plugin),
// skip-cert-verify, client-cert.
func (l *surgeLine) tlsGroup(f map[string]any) {
	l.verbatim("server-cert-fingerprint-sha256", f, "tls-fingerprint")
	l.quoted("sni", f, "sni")
	if v, ok := present(f, "name-cert-verify"); ok {
		l.add("server-cert-verify-name", strippedQuoted(v))
	}
	if f["plugin"] != "shadow-tls" {
		if a, ok := surgeALPN(f["alpn"]); ok {
			l.add("alpn", a)
		}
	}
	l.verbatim("skip-cert-verify", f, "skip-cert-verify")
	if v, ok := present(f, "keystore-client-cert"); ok {
		l.add("client-cert", strippedQuoted(v))
	} else if v, ok := present(f, "client-cert"); ok {
		l.add("client-cert", strippedQuoted(v))
	}
}

// surgeALPN is the alpn value: a list, or comma-separated text, with each
// item stripped of outer quotes and trimmed, empty items dropped, joined by
// "," and quoted; ok is false when no item remains.
func surgeALPN(v any) (string, bool) {
	var items []string
	switch x := v.(type) {
	case nil:
		return "", false
	case []any:
		for _, e := range x {
			if e != nil {
				items = append(items, text(e))
			}
		}
	default:
		items = strings.Split(text(x), ",")
	}
	kept := items[:0]
	for _, it := range items {
		if s := trimES(stripQuotes(trimES(it))); s != "" {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		return "", false
	}
	return `"` + strings.Join(kept, ",") + `"`, true
}

// testGroup is TEST: test-url, test-timeout, test-udp, hybrid, tos,
// allow-other-interface, then interface from interface-name and from
// interface, both when both are present.
func (l *surgeLine) testGroup(f map[string]any) {
	for _, k := range []string{"test-url", "test-timeout", "test-udp", "hybrid", "tos", "allow-other-interface"} {
		l.verbatim(k, f, k)
	}
	l.verbatim("interface", f, "interface-name")
	l.verbatim("interface", f, "interface")
}

// shadowTLSGroup is STLS, written when the group is active: the shadow-tls
// plugin with plugin-opts and a non-empty password. ss adds udp-port.
func (l *surgeLine) shadowTLSGroup(f map[string]any, ss bool) {
	po, ok := surgeShadowTLS(f)
	if !ok {
		return
	}
	l.add("shadow-tls-password", `"`+textOf(po, "password")+`"`)
	if v := po["host"]; truthy(v) {
		l.add("shadow-tls-sni", text(v))
	}
	if v := po["version"]; truthy(v) {
		l.add("shadow-tls-version", text(v))
	}
	alpn := po["alpn"]
	if alpn == nil {
		alpn = f["alpn"]
	}
	if a, ok := surgeALPN(alpn); ok {
		l.add("alpn", a)
	}
	if ss {
		l.verbatim("udp-port", f, "udp-port")
	}
}

// surgeShadowTLS is the active ShadowTLS group's plugin options.
func surgeShadowTLS(f map[string]any) (map[string]any, bool) {
	po, ok := shadowTLSPlugin(f)
	if !ok || !notEmptyText(po["password"]) {
		return nil, false
	}
	return po, true
}

// notEmptyText reports a present value whose text form is not empty.
func notEmptyText(v any) bool {
	return v != nil && text(v) != ""
}

// endGroup is END: block-quic, underlying-proxy.
func (l *surgeLine) endGroup(f map[string]any) {
	l.verbatim("block-quic", f, "block-quic")
	l.verbatim("underlying-proxy", f, "underlying-proxy")
}

// tfo writes tfo, or for tuic, hysteria2 and masque fast-open when tfo is
// absent.
func (l *surgeLine) tfo(f map[string]any, fastOpen bool) {
	if v, ok := present(f, "tfo"); ok {
		l.add("tfo", text(v))
	} else if fastOpen {
		l.verbatim("tfo", f, "fast-open")
	}
}

// headers writes key="K:"v";K:"v"" from a headers map, entries with a
// non-blank key and a non-null value joined by sep; nothing when no entry
// qualifies.
func (l *surgeLine) headers(key string, h map[string]any, sep string) {
	var parts []string
	for _, k := range propertyOrder(h) {
		v := h[k]
		if trimES(k) == "" || v == nil {
			continue
		}
		parts = append(parts, k+":"+strippedQuoted(v))
	}
	if len(parts) > 0 {
		l.add(key, `"`+strings.Join(parts, sep)+`"`)
	}
}

// portHopping writes port-hopping from ports, "," replaced by ";" and quoted,
// and port-hopping-interval from hop-interval, each when not blank.
func (l *surgeLine) portHopping(f map[string]any) {
	if v, ok := present(f, "ports"); ok && trimES(text(v)) != "" {
		l.add("port-hopping", `"`+strings.ReplaceAll(text(v), ",", ";")+`"`)
	}
	if v, ok := present(f, "hop-interval"); ok && trimES(text(v)) != "" {
		l.add("port-hopping-interval", text(v))
	}
}

// surgeSSCiphers are the Shadowsocks ciphers Surge accepts.
var surgeSSCiphers = map[string]bool{
	"aes-128-gcm": true, "aes-192-gcm": true, "aes-256-gcm": true, "chacha20-ietf-poly1305": true,
	"xchacha20-ietf-poly1305": true, "rc4": true, "rc4-md5": true, "aes-128-cfb": true, "aes-192-cfb": true,
	"aes-256-cfb": true, "aes-128-ctr": true, "aes-192-ctr": true, "aes-256-ctr": true, "bf-cfb": true,
	"camellia-128-cfb": true, "camellia-192-cfb": true, "camellia-256-cfb": true, "cast5-cfb": true,
	"des-cfb": true, "idea-cfb": true, "rc2-cfb": true, "seed-cfb": true, "salsa20": true, "chacha20": true,
	"chacha20-ietf": true, "none": true, "2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true,
}

// surgeNameCleaner removes every "=" and "," from a node name. It is built
// once: a strings.Replacer builds its byte table on construction, which per
// node was most of the producer's allocation.
var surgeNameCleaner = strings.NewReplacer("=", "", ",", "")

// surgeEntry writes one node's entry, or returns errSurgeUnsupported for a
// node the producer rejects (surge.md, "Unsupported rule").
func surgeEntry(f map[string]any, include bool) (string, error) {
	typ, _ := f["type"].(string)
	if wsHTTPUpgrade(f) {
		return "", errSurgeUnsupported
	}
	if po, ok := surgeShadowTLS(f); ok {
		if v := po["version"]; truthy(v) && jsLessThan(v, 2) {
			return "", errSurgeUnsupported
		}
	}
	name := surgeNameCleaner.Replace(textOf(f, "name"))
	if typ == "wireguard" {
		if !include {
			return "", errSurgeUnsupported
		}
		return surgeWireGuardTemplate(f, name), nil
	}

	var l surgeLine
	head := func(kind string) {
		l.b.WriteString(name + "=" + kind + "," + textOf(f, "server") + "," + textOf(f, "port"))
	}
	switch typ {
	case "ss":
		cipher := "none"
		if v, ok := present(f, "cipher"); ok {
			cipher = text(v)
		}
		if !surgeSSCiphers[cipher] {
			return "", errSurgeUnsupported
		}
		plugin, hasPlugin := present(f, "plugin")
		if hasPlugin && plugin != "obfs" && plugin != "shadow-tls" {
			return "", errSurgeUnsupported
		}
		head("ss")
		l.add("encrypt-method", cipher)
		l.quoted("password", f, "password")
		l.ipGroup(f)
		if plugin == "obfs" {
			po, _ := obj(f, "plugin-opts")
			l.add("obfs", textOf(po, "mode"))
			l.verbatim("obfs-host", po, "host")
			l.verbatim("obfs-uri", po, "path")
		}
		l.tfo(f, false)
		l.verbatim("udp-relay", f, "udp")
		l.testGroup(f)
		l.shadowTLSGroup(f, true)
		l.endGroup(f)
	case "trojan":
		if !surgeTransportAllowed(f, false, include) {
			return "", errSurgeUnsupported
		}
		head("trojan")
		l.quoted("password", f, "password")
		l.ipGroup(f)
		l.ws(f)
		l.verbatim("tls", f, "tls")
		l.tlsGroup(f)
		l.tfo(f, false)
		l.testGroup(f)
		l.shadowTLSGroup(f, false)
		l.endGroup(f)
	case "anytls":
		if net, ok := f["network"]; ok && truthy(net) && (net != "tcp" || set(f, "reality-opts")) {
			return "", errSurgeUnsupported
		}
		head("anytls")
		l.quoted("password", f, "password")
		l.ipGroup(f)
		l.tlsGroup(f)
		l.tfo(f, false)
		l.testGroup(f)
		l.endGroup(f)
		l.verbatim("reuse", f, "reuse")
	case "vmess":
		if !surgeTransportAllowed(f, true, include) {
			return "", errSurgeUnsupported
		}
		head("vmess")
		l.verbatim("username", f, "uuid")
		if m, ok := surgeVMessMethod(f); ok {
			l.add("encrypt-method", m)
		}
		l.ipGroup(f)
		l.ws(f)
		if v, ok := present(f, "aead"); ok {
			l.add("vmess-aead", text(v))
		} else {
			l.add("vmess-aead", boolText(isZero(f["alterId"])))
		}
		l.verbatim("tls", f, "tls")
		if set(f, "tls") {
			l.tlsGroup(f)
		}
		l.tfo(f, false)
		l.testGroup(f)
		l.shadowTLSGroup(f, false)
		l.endGroup(f)
	case "http":
		kind := "http"
		if set(f, "tls") {
			kind = "https"
		}
		head(kind)
		l.quoted("username", f, "username")
		l.quoted("password", f, "password")
		if h, ok := obj(f, "headers"); ok {
			l.headers("headers", h, ";")
		}
		l.ipGroup(f)
		if set(f, "tls") {
			l.tlsGroup(f)
		}
		l.tfo(f, false)
		l.testGroup(f)
		l.shadowTLSGroup(f, false)
		l.endGroup(f)
	case "socks5":
		kind := "socks5"
		if set(f, "tls") {
			kind = "socks5-tls"
		}
		head(kind)
		l.quoted("username", f, "username")
		l.quoted("password", f, "password")
		l.ipGroup(f)
		if set(f, "tls") {
			l.tlsGroup(f)
		}
		l.verbatim("udp-relay", f, "udp")
		l.testGroup(f)
		l.shadowTLSGroup(f, false)
		l.endGroup(f)
	case "h2-connect":
		head("h2-connect")
		l.quoted("username", f, "username")
		l.quoted("password", f, "password")
		if h, ok := obj(f, "headers"); ok {
			l.headers("headers", h, ";")
		}
		l.verbatim("max-streams", f, "max-streams")
		l.ipGroup(f)
		l.tlsGroup(f)
		l.verbatim("udp-relay", f, "udp")
		l.testGroup(f)
		l.shadowTLSGroup(f, false)
		l.endGroup(f)
	case "trusttunnel":
		head("trust-tunnel")
		l.quoted("username", f, "username")
		l.quoted("password", f, "password")
		if h, ok := obj(f, "headers"); ok {
			l.headers("headers", h, ";")
		}
		l.verbatim("max-streams", f, "max-streams")
		if f["network"] == "h3" {
			l.add("h3", "true")
		}
		l.ipGroup(f)
		l.tlsGroup(f)
		l.tfo(f, false)
		l.testGroup(f)
		l.endGroup(f)
		l.verbatim("reuse", f, "reuse")
	case "masque-surge":
		head("masque")
		l.quoted("username", f, "username")
		l.quoted("password", f, "password")
		l.portHopping(f)
		l.ipGroup(f)
		l.tlsGroup(f)
		l.tfo(f, true)
		l.testGroup(f)
		l.endGroup(f)
		l.verbatim("ecn", f, "ecn")
	case "ssh":
		head("ssh")
		l.quoted("username", f, "username")
		l.quoted("password", f, "password")
		if v, ok := present(f, "keystore-private-key"); ok {
			l.add("private-key", strippedQuoted(v))
		} else if v, ok := present(f, "private-key"); ok {
			l.add("private-key", strippedQuoted(v))
		}
		l.verbatim("idle-timeout", f, "idle-timeout")
		l.quoted("server-fingerprint", f, "server-fingerprint")
		l.ipGroup(f)
		l.tfo(f, false)
		l.testGroup(f)
		l.endGroup(f)
	case "direct":
		l.b.WriteString(name + "=direct")
		l.ipGroup(f)
		l.tfo(f, false)
		l.testGroup(f)
		l.endGroup(f)
	case "snell":
		version, hasVersion := present(f, "version")
		v6 := hasVersion && numberIs(version, 6)
		oo, _ := obj(f, "obfs-opts")
		if v6 {
			for _, k := range []string{"mode", "host", "path"} {
				if _, ok := oo[k]; ok {
					return "", errSurgeUnsupported
				}
			}
		}
		head("snell")
		l.verbatim("version", f, "version")
		l.quoted("psk", f, "psk")
		if v6 {
			l.verbatim("mode", f, "mode")
		}
		l.ipGroup(f)
		l.verbatim("obfs", oo, "mode")
		l.verbatim("obfs-host", oo, "host")
		l.verbatim("obfs-uri", oo, "path")
		l.tfo(f, false)
		l.testGroup(f)
		l.shadowTLSGroup(f, false)
		l.endGroup(f)
		l.verbatim("reuse", f, "reuse")
	case "tuic":
		kind := "tuic-v5"
		if notEmptyText(f["token"]) {
			kind = "tuic"
		}
		head(kind)
		l.verbatim("uuid", f, "uuid")
		l.quoted("password", f, "password")
		l.verbatim("token", f, "token")
		l.portHopping(f)
		l.ipGroup(f)
		l.tlsGroup(f)
		l.tfo(f, true)
		l.testGroup(f)
		l.shadowTLSGroup(f, false)
		l.endGroup(f)
		l.verbatim("ecn", f, "ecn")
	case "hysteria2":
		obfs := text(f["obfs"])
		if set(f, "obfs-password") && obfs != "salamander" && obfs != "gecko" {
			return "", errSurgeUnsupported
		}
		head("hysteria2")
		l.quoted("password", f, "password")
		l.portHopping(f)
		if set(f, "obfs-password") {
			l.add(obfs+"-password", `"`+text(f["obfs-password"])+`"`)
		}
		l.ipGroup(f)
		l.tlsGroup(f)
		l.tfo(f, true)
		l.testGroup(f)
		l.shadowTLSGroup(f, false)
		l.endGroup(f)
		if v, ok := present(f, "down"); ok {
			l.add("download-bandwidth", text(speedDigits(v)))
		}
		l.verbatim("ecn", f, "ecn")
	case "wireguard-surge":
		l.b.WriteString(name + "=wireguard")
		l.verbatim("section-name", f, "section-name")
		l.verbatim("no-error-alert", f, "no-error-alert")
		if v, ok := present(f, "ip-version"); ok {
			l.add("ip-version", surgeIPVersion(v))
		}
		l.testGroup(f)
		l.shadowTLSGroup(f, false)
		l.endGroup(f)
	default:
		return "", errSurgeUnsupported
	}
	return l.b.String(), nil
}

// ws writes ws=true, ws-path and ws-headers for a ws node.
func (l *surgeLine) ws(f map[string]any) {
	if f["network"] != "ws" {
		return
	}
	l.add("ws", "true")
	o, _ := obj(f, "ws-opts")
	if v, ok := present(o, "path"); ok && text(v) != "" {
		l.add("ws-path", text(v))
	}
	if h, ok := obj(o, "headers"); ok {
		l.headers("ws-headers", h, "|")
	}
}

// surgeTransportAllowed is the trojan and vmess transport rule: no network
// and ws pass, tcp passes without reality-opts, and for vmess http passes as
// tcp with include-unsupported-proxy; anything else is unsupported.
func surgeTransportAllowed(f map[string]any, vmess, include bool) bool {
	net, ok := f["network"]
	if !ok || net == nil {
		return true
	}
	switch net {
	case "ws":
		return true
	case "tcp":
		return !set(f, "reality-opts")
	case "http":
		return vmess && include
	}
	return false
}

// surgeVMessMethod is VMess encrypt-method: aes-128-gcm stays, both chacha20
// spellings become chacha20-ietf-poly1305, and anything else is omitted.
func surgeVMessMethod(f map[string]any) (string, bool) {
	c := ""
	if v, ok := f["cipher"]; ok {
		c = strings.ToLower(trimES(text(v)))
	}
	switch c {
	case "aes-128-gcm":
		return c, true
	case "chacha20-poly1305", "chacha20-ietf-poly1305":
		return "chacha20-ietf-poly1305", true
	}
	return "", false
}

// surgeWireGuardTemplate is the WireGuard template (surge.md, "WireGuard
// template"): a commented proxy line and its [WireGuard] section, one entry.
func surgeWireGuardTemplate(src map[string]any, name string) string {
	f := src
	if peers, ok := f["peers"].([]any); ok && len(peers) > 0 {
		// The first peer's values replace the node's, as they are; a value
		// the peer lacks removes the node's.
		peer, _ := peers[0].(map[string]any)
		f = make(map[string]any, len(src))
		for k, v := range src {
			f[k] = v
		}
		for _, k := range []string{"server", "port", "ip", "ipv6", "public-key", "allowed-ips", "reserved"} {
			replaceFrom(f, k, peer, k)
		}
		replaceFrom(f, "preshared-key", peer, "pre-shared-key")
	}
	section := name
	if v := f["section-name"]; notBlank(v) {
		section = text(v)
	}
	var l surgeLine
	l.b.WriteString("# " + name + "=wireguard,section-name=" + section)
	l.verbatim("no-error-alert", f, "no-error-alert")
	if v, ok := present(f, "ip-version"); ok {
		l.add("ip-version", surgeIPVersion(v))
	}
	l.testGroup(f)
	l.shadowTLSGroup(f, false)
	l.endGroup(f)

	lines := []string{
		"# > WireGuard Proxy " + name,
		l.b.String(),
		"# > WireGuard Section " + name,
		"[WireGuard " + section + "]",
		"private-key = " + textOf(f, "private-key"),
	}
	if v, ok := present(f, "ip"); ok {
		lines = append(lines, "self-ip = "+text(v))
	}
	if v, ok := present(f, "ipv6"); ok {
		lines = append(lines, "self-ip-v6 = "+text(v))
	}
	if v := f["dns"]; truthy(v) {
		lines = append(lines, "dns-server = "+joinText(v, ", "))
	}
	if v, ok := present(f, "mtu"); ok {
		lines = append(lines, "mtu = "+text(v))
	}
	if v, ok := present(f, "ip-version"); ok && surgeIPVersion(v) == "prefer-v6" {
		lines = append(lines, "prefer-ipv6 = true")
	}
	var pairs []string
	pair := func(key string, v any, ok bool) {
		if ok {
			pairs = append(pairs, key+" = "+text(v))
		}
	}
	pk, ok := present(f, "public-key")
	pair("public-key", pk, ok)
	if v, ok := present(f, "allowed-ips"); ok {
		pairs = append(pairs, `allowed-ips = "`+joinText(v, ",")+`"`)
	}
	if _, ok := present(f, "server"); ok {
		pairs = append(pairs, "endpoint = "+textOf(f, "server")+":"+textOf(f, "port"))
	}
	if v, ok := present(f, "persistent-keepalive"); ok {
		pair("keepalive", v, true)
	} else {
		v, ok := present(f, "keepalive")
		pair("keepalive", v, ok)
	}
	if v, ok := present(f, "reserved"); ok {
		pairs = append(pairs, "client-id = "+joinText(v, "/"))
	}
	if v, ok := present(f, "preshared-key"); ok {
		pair("preshared-key", v, true)
	} else {
		v, ok := present(f, "pre-shared-key")
		pair("preshared-key", v, ok)
	}
	lines = append(lines, "peer = ("+strings.Join(pairs, ", ")+")")
	return strings.Join(lines, "\n")
}

// replaceFrom sets f[key] to src[from], or removes it when src lacks it.
func replaceFrom(f map[string]any, key string, src map[string]any, from string) {
	if v, ok := src[from]; ok {
		f[key] = v
	} else {
		delete(f, key)
	}
}

// joinText is a list's elements' text forms joined by sep, or a scalar's
// text form.
func joinText(v any, sep string) string {
	l, ok := v.([]any)
	if !ok {
		return text(v)
	}
	parts := make([]string, len(l))
	for i, e := range l {
		if e != nil {
			parts[i] = text(e)
		}
	}
	return strings.Join(parts, sep)
}

// isZero is ECMAScript's v == 0 over model values: 0, "0", "" and false.
func isZero(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return !x
	}
	n, ok := jsNumberOf(v)
	return ok && n == 0
}

// numberIs reports whether v is numerically n.
func numberIs(v any, n float64) bool {
	x, ok := jsNumberOf(v)
	return ok && x == n
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
