package producers

import (
	"bytes"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// The Stash producer (specs/producers/stash.md): the proxies: document of
// proxies.go under Stash's admission filter, an allow list of types with a
// Shadowsocks cipher list, and Stash's field rules. Unlike ClashMeta it keeps
// the shadow-tls plugin object, adds no client fingerprint, renames the test
// fields to benchmark-*, rewrites the hysteria speeds and moves tfo to
// fast-open.

type stashProducer struct{}

func (stashProducer) ID() string { return "stash" }

// EmptyDocument is "proxies:" and a newline, or "proxies: []" and a newline
// in the pretty form (stash.md, "Empty document").
func (stashProducer) EmptyDocument(opts Options) []byte { return emptyProxies(opts) }

func (stashProducer) Produce(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options) (Result, error) {
	return produceProxies(dst, nodes, target, opts, proxyRules{id: "stash", admits: stashAdmits, transform: stashTransform})
}

// stashTypes is the admission filter's allow list of types.
var stashTypes = map[string]bool{
	"ss": true, "ssr": true, "vmess": true, "socks5": true, "http": true, "snell": true, "trojan": true,
	"tuic": true, "vless": true, "wireguard": true, "hysteria": true, "hysteria2": true, "ssh": true,
	"juicity": true, "anytls": true, "tailscale": true, "trusttunnel": true, "masque": true, "mieru": true,
}

// stashSSCiphers are the Shadowsocks ciphers Stash admits, verbatim from the
// specification.
var stashSSCiphers = map[string]bool{
	"aes-128-gcm": true, "aes-192-gcm": true, "aes-256-gcm": true,
	"aes-128-cfb": true, "aes-192-cfb": true, "aes-256-cfb": true,
	"aes-128-ctr": true, "aes-192-ctr": true, "aes-256-ctr": true,
	"rc4-md5": true, "chacha20-ietf": true, "xchacha20": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true,
}

// stashAdmits is Stash's admission filter (stash.md, "Admission filter"):
// false drops the node.
func stashAdmits(f map[string]any) bool {
	typ, _ := f["type"].(string)
	if !stashTypes[typ] {
		return false
	}
	switch typ {
	case "ss":
		if !stashSSCiphers[text(f["cipher"])] {
			return false
		}
		if f["plugin"] == "v2ray-plugin" && v2rayPluginMode(f) != "websocket" {
			return false
		}
	case "snell":
		if v, ok := f["version"]; ok && jsGreaterThan(v, 6) {
			return false
		}
	case "vless":
		if set(f, "reality-opts") && f["network"] != "tcp" {
			return false
		}
	case "anytls":
		if net, ok := f["network"]; ok && truthy(net) && (net != "tcp" || set(f, "reality-opts")) {
			return false
		}
	}
	if typ != "vless" && f["network"] == "xhttp" {
		return false
	}
	return !wsHTTPUpgrade(f)
}

// stashTransform applies Stash's field rules to one admitted node.
func stashTransform(p *prepared) {
	f := p.node.Fields
	typ, _ := f["type"].(string)
	switch typ {
	case "vmess":
		vmessAEAD(p)
		moveKey(p, "sni", "servername")
		p.put("cipher", stashVMessCipher(f))
	case "vless":
		moveKey(p, "sni", "servername")
	case "tuic":
		if !truthy(f["alpn"]) {
			p.put("alpn", []any{"h3"})
		} else {
			alpnList(f)
		}
		moveKeyIfAbsent(p, "tfo", "fast-open")
		tuicVersion(p)
	case "hysteria":
		copyKey(p, "auth_str", "auth-str")
		alpnList(f)
		hysteriaSpeeds(p)
	case "hysteria2":
		if v, ok := f["password"]; ok {
			if _, has := f["auth"]; !has {
				delete(f, "password")
				p.put("auth", v)
			}
		}
		hysteriaSpeeds(p)
	case "wireguard":
		mirrorKey(p, "keepalive", "persistent-keepalive")
		mirrorKey(p, "preshared-key", "pre-shared-key")
	case "snell":
		if v, ok := f["version"]; ok && jsLessThan(v, 3) {
			delete(f, "udp")
		}
	}
	streamTransports(p, typ)
	pluginSkipCertVerify(f)
	switch typ {
	case "trojan", "tuic", "hysteria", "hysteria2", "juicity", "anytls", "trusttunnel", "naive":
		delete(f, "tls")
	}
	renameSet(p, "tls-fingerprint", "server-cert-fingerprint")
	renameSet(p, "underlying-proxy", "dialer-proxy")
	dropNonBooleanTLS(f)
	renameIfSet(p, "test-url", "benchmark-url")
	renameIfSet(p, "test-timeout", "benchmark-timeout")
	dropPipelineFields(f)
	dropAnnotations(f)
	dropGRPCAnnotations(f)
}

// hysteriaSpeeds is the hysteria and hysteria2 tfo and speed rows: tfo moves
// to an absent fast-open, down and up move to absent down-speed and
// up-speed, and both speeds become their first run of decimal digits.
func hysteriaSpeeds(p *prepared) {
	f := p.node.Fields
	moveKeyIfAbsent(p, "tfo", "fast-open")
	moveKeyIfAbsent(p, "down", "down-speed")
	moveKeyIfAbsent(p, "up", "up-speed")
	for _, k := range []string{"down-speed", "up-speed"} {
		if v, ok := f[k]; ok {
			f[k] = speedDigits(v)
		}
	}
}

// speedDigits is the first run of decimal digits in v's text form, as text,
// or the number 0 when there is none ("100 Mbps" gives "100").
func speedDigits(v any) any {
	s := text(v)
	i := strings.IndexAny(s, "0123456789")
	if i < 0 {
		return float64(0)
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return s[i:j]
}

// stashVMessCipher is Stash's VMess cipher normalisation: four values stay,
// chacha20-ietf-poly1305 becomes chacha20-poly1305 and anything else,
// zero included, becomes auto.
func stashVMessCipher(f map[string]any) string {
	c := ""
	if v, ok := f["cipher"]; ok {
		c = strings.ToLower(trimES(text(v)))
	}
	switch c {
	case "auto", "aes-128-gcm", "chacha20-poly1305", "none":
		return c
	case "chacha20-ietf-poly1305":
		return "chacha20-poly1305"
	}
	return "auto"
}

// v2rayPluginMode is plugin-opts.mode trimmed and in lower case, "" when
// absent.
func v2rayPluginMode(f map[string]any) string {
	po, _ := obj(f, "plugin-opts")
	if v, ok := po["mode"]; ok {
		return strings.ToLower(trimES(text(v)))
	}
	return ""
}

// wsHTTPUpgrade reports a ws node whose ws-opts set v2ray-http-upgrade.
func wsHTTPUpgrade(f map[string]any) bool {
	if f["network"] != "ws" {
		return false
	}
	o, _ := obj(f, "ws-opts")
	return set(o, "v2ray-http-upgrade")
}

// moveKeyIfAbsent moves a present from to an absent to; when to is present
// both stay.
func moveKeyIfAbsent(p *prepared, from, to string) {
	f := p.node.Fields
	v, ok := f[from]
	if !ok {
		return
	}
	if _, has := f[to]; has {
		return
	}
	delete(f, from)
	p.put(to, v)
}

// renameIfSet moves a set from to to, replacing what to held; a from that is
// not set stays.
func renameIfSet(p *prepared, from, to string) {
	f := p.node.Fields
	if v := f[from]; truthy(v) {
		delete(f, from)
		p.put(to, v)
	}
}

// jsGreaterThan is ECMAScript's v > n for a present value: text is read as a
// number, null as 0 and a boolean as 0 or 1; anything else compares false.
func jsGreaterThan(v any, n float64) bool {
	x, ok := jsNumberOf(v)
	return ok && x > n
}

// jsNumberOf is ECMAScript's ToNumber over model values: ok is false for an
// object or a list, which no comparison in the specifications meets.
func jsNumberOf(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	case string:
		return jsNumber(t), true
	case nil:
		return 0, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}
