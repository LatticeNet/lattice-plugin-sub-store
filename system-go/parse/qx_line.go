package parse

import (
	"strings"
)

// The Quantumult X line grammar of parser.md section 8 (parser rows 40 to
// 47): type=host:port, key=value, ...

var qxCiphers = []string{
	"aes-128-cfb", "aes-128-ctr", "aes-128-gcm", "aes-192-cfb", "aes-192-ctr",
	"aes-192-gcm", "aes-256-cfb", "aes-256-ctr", "aes-256-gcm", "bf-cfb",
	"cast5-cfb", "chacha20-ietf-poly1305", "chacha20-ietf", "chacha20-poly1305",
	"chacha20", "des-cfb", "none", "rc2-cfb", "rc4-md5-6", "rc4-md5", "salsa20",
	"xchacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm",
}

// qxHTTPObfs are the spellings that select HTTP obfs; the word as written
// is kept in the annotation _qx_obfs_http.
var qxHTTPObfs = []string{"http", "vmess-http", "vemss-http", "shadowsocks-http"}

// qxObfs are the obfs words each type accepts, in the order they are tried.
var qxObfs = map[string][]string{
	"trojan":      {"wss", "ws", "over-tls", "http"},
	"shadowsocks": append(append(append([]string{}, ssrObfs...), "tls", "wss", "ws", "over-tls"), qxHTTPObfs...),
	"vmess":       append([]string{"wss", "ws", "over-tls"}, qxHTTPObfs...),
	"vless":       append([]string{"wss", "ws", "over-tls"}, qxHTTPObfs...),
}

func qxObfsOption(keyword string) optSpec {
	return optSpec{kind: optEnum, words: qxObfs[keyword], set: func(n *lineNode, v any) error {
		w := v.(string)
		for _, h := range qxHTTPObfs {
			if w == h {
				n.f["_qx_obfs_http"] = w
				w = "http"
				break
			}
		}
		n.x["obfs"] = w
		return nil
	}}
}

// qxOption is one row of the option table of 8.2 with the types that accept
// it (nil for all); a key two rows share is split by type.
type qxOption struct {
	key   string
	spec  optSpec
	types []string
}

var qxOptions = []qxOption{
	{"password", optSpec{kind: optTextToKey, set: func(n *lineNode, v any) error {
		n.f["password"] = TrimECMAScript(v.(string))
		return nil
	}}, []string{"shadowsocks", "trojan", "anytls", "http", "socks5"}},
	{"password", setText("uuid", trimText), []string{"vmess", "vless"}},
	{"username", setText("username", trimText), []string{"http", "socks5"}},
	{"method", setEnum("cipher", qxCiphers...), []string{"shadowsocks", "vmess", "vless"}},
	{"aead", setKind(optBool, "aead"), []string{"vmess", "vless"}},
	{"over-tls", setKind(optBool, "tls"), []string{"vmess", "vless", "anytls", "trojan", "http", "socks5"}},
	{"tls-host", setText("sni", dequote), []string{"vmess", "vless", "anytls", "trojan", "http", "socks5"}},
	{"tls-verification", optSpec{kind: optText, set: func(n *lineNode, v any) error {
		switch s := TrimECMAScript(v.(string)); s {
		case "true":
			n.f["skip-cert-verify"] = false
		case "false":
			n.f["skip-cert-verify"] = true
		default:
			n.f["name-cert-verify"] = s
		}
		return nil
	}}, nil},
	{"tls-cert-sha256", setText("tls-fingerprint", trimText), nil},
	{"tls-pubkey-sha256", setKind(optTextNoEq, "tls-pubkey-sha256"), nil},
	{"tls-alpn", setKind(optTextNoEq, "tls-alpn"), nil},
	{"tls-no-session-ticket", setKind(optBool, "tls-no-session-ticket"), nil},
	{"tls-no-session-reuse", setKind(optBool, "tls-no-session-reuse"), nil},
	{"udp-relay", setKind(optBool, "udp"), nil},
	{"fast-open", setKind(optBool, "tfo"), nil},
	{"udp-over-tcp", optSpec{kind: optText, set: func(n *lineNode, v any) error {
		switch strings.TrimRight(v.(string), " \t\r") {
		case "sp.v1":
			n.f["udp-over-tcp"], n.f["udp-over-tcp-version"] = true, 1.0
		case "sp.v2":
			n.f["udp-over-tcp"], n.f["udp-over-tcp-version"] = true, 2.0
		case "true":
			n.f["_ssr_python_uot"] = true
		default:
			return errReject
		}
		return nil
	}}, []string{"shadowsocks"}},
	{"udp-over-tcp", optSpec{kind: optBool, set: func(*lineNode, any) error {
		return errReject // a boolean udp-over-tcp rejects the line
	}}, []string{"trojan", "vmess", "vless", "http", "socks5"}},
	{"obfs", qxObfsOption("shadowsocks"), []string{"shadowsocks"}},
	{"obfs", qxObfsOption("trojan"), []string{"trojan"}},
	{"obfs", qxObfsOption("vmess"), []string{"vmess"}},
	{"obfs", qxObfsOption("vless"), []string{"vless"}},
	{"obfs-host", keepText("obfs-host", dequote), []string{"shadowsocks", "trojan", "vmess", "vless"}},
	{"obfs-uri", keepText("obfs-uri", nil), []string{"shadowsocks", "trojan", "vmess", "vless"}},
	{"ssr-protocol", optSpec{kind: optEnum, words: ssrProtocols, set: func(n *lineNode, v any) error {
		n.f["protocol"] = v
		n.x["ssr"] = true
		return nil
	}}, []string{"shadowsocks"}},
	{"ssr-protocol-param", setKind(optTextNoEq, "protocol-param"), []string{"shadowsocks"}},
	{"reality-base64-pubkey", optSpec{kind: optTextNoEq, set: func(n *lineNode, v any) error {
		realityOpts(n.f)["public-key"] = v
		return nil
	}}, nil},
	{"reality-hex-shortid", optSpec{kind: optTextNoEq, set: func(n *lineNode, v any) error {
		realityOpts(n.f)["short-id"] = v
		return nil
	}}, nil},
	{"vless-flow", setKind(optTextNoEq, "flow"), []string{"vless"}},
	{"server_check_url", setKind(optTextNoEq, "test-url"), nil},
	{"tag", setKind(optTextNoEq, "name"), nil},
}

// qxTypes are the type words with their node type.
var qxTypes = []struct{ keyword, nodeType string }{
	{"shadowsocks", "ss"}, {"vmess", "vmess"}, {"vless", "vless"},
	{"anytls", "anytls"}, {"trojan", "trojan"}, {"http", "http"},
	{"socks5", "socks5"},
}

var qxOptionTables = func() map[string]map[string]optSpec {
	out := map[string]map[string]optSpec{}
	for _, t := range qxTypes {
		table := map[string]optSpec{}
		for _, o := range qxOptions {
			accepted := o.types == nil
			for _, k := range o.types {
				accepted = accepted || k == t.keyword
			}
			if accepted {
				table[o.key] = o.spec
			}
		}
		out[t.keyword] = table
	}
	return out
}()

// parseQX is the Quantumult X grammar shared by rows 40 to 47.
func parseQX(line string, _ *lineState) (map[string]any, error) {
	keyword, nodeType, eq := "", "", 0
	for _, t := range qxTypes {
		if strings.HasPrefix(line, t.keyword) {
			k := skipGap(line, len(t.keyword))
			if k < len(line) && line[k] == '=' {
				keyword, nodeType, eq = t.keyword, t.nodeType, k
				break
			}
		}
	}
	if keyword == "" {
		return nil, errReject
	}
	// The address is the text up to the last ":" before the first comma,
	// trimmed, and the digits after it.
	start := skipGap(line, eq+1)
	comma := commaFrom(line, start)
	c := strings.LastIndexByte(line[start:comma], ':')
	if c < 0 {
		return nil, errReject
	}
	c += start
	d := c + 1
	for d < comma && line[d] >= '0' && line[d] <= '9' {
		d++
	}
	if d == c+1 || !atOptionEnd(line, d) {
		return nil, errReject
	}
	n := newLineNode()
	n.f["type"] = nodeType
	n.f["server"] = TrimECMAScript(line[start:c])
	if port := numberValue(line[c+1 : d]); port <= 65535 {
		n.f["port"] = port
	}
	if !readOptions(line, d, qxOptionTables[keyword], n) {
		return nil, errReject
	}
	return qxPost(keyword, n), nil
}

// qxPost decodes tls-alpn and applies what each type sets.
func qxPost(keyword string, n *lineNode) map[string]any {
	f, x := n.f, n.x
	if v, ok := f["tls-alpn"].(string); ok {
		if names, ok := decodeQXALPN(v); ok {
			f["alpn"] = names
		}
	}
	obfs, hasObfs := x["obfs"].(string)
	host, hasHost := x["obfs-host"]
	path, hasPath := x["obfs-uri"]
	switch keyword {
	case "shadowsocks":
		isSSR := x["ssr"] == true
		for _, w := range ssrObfs {
			isSSR = isSSR || obfs == w
		}
		if isSSR {
			f["type"] = "ssr"
			if _, ok := f["protocol"]; !ok {
				f["protocol"] = "origin"
			}
			if hasHost {
				f["obfs-param"] = host
			}
			if hasObfs {
				f["obfs"] = obfs
			}
			break
		}
		switch obfs {
		case "http", "tls", "ws", "wss":
			opts := map[string]any{}
			if obfs == "http" || obfs == "tls" {
				f["plugin"] = "obfs"
				opts["mode"] = obfs
			} else {
				f["plugin"] = "v2ray-plugin"
				opts["mode"] = "websocket"
				if obfs == "wss" {
					opts["tls"] = true
				}
			}
			if hasHost {
				opts["host"] = host
			}
			if hasPath {
				opts["path"] = path
			}
			f["plugin-opts"] = opts
		case "over-tls":
			f["tls"] = true
			if hasHost {
				f["sni"] = host
			}
		}
	case "vmess", "vless", "trojan":
		if keyword != "trojan" {
			if _, ok := f["cipher"]; !ok {
				f["cipher"] = "none"
			}
		}
		if keyword == "vmess" {
			if f["aead"] == false {
				f["alterId"] = 1.0
			} else {
				f["alterId"] = 0.0
			}
		}
		switch obfs {
		case "ws", "wss", "http":
			network := obfs
			if obfs == "wss" {
				network = "ws"
				f["tls"] = true
			}
			f["network"] = network
			opts := map[string]any{}
			if hasPath {
				opts["path"] = path
			}
			headers := map[string]any{}
			if hasHost {
				headers["Host"] = host
			}
			opts["headers"] = headers
			f[network+"-opts"] = opts
		case "over-tls":
			f["tls"] = true
			if _, ok := f["sni"]; !ok && hasHost {
				f["sni"] = host
			}
		}
	case "anytls":
		f["tls"] = true
	}
	return f
}

// decodeQXALPN reads a tls-alpn value as hexadecimal (colons ignored, an
// even number of digits) holding length-prefixed protocol names; ok is false
// unless the bytes decode completely.
func decodeQXALPN(v string) ([]any, bool) {
	h := strings.ReplaceAll(TrimECMAScript(v), ":", "")
	if h == "" || len(h)%2 != 0 {
		return nil, false
	}
	b := make([]byte, len(h)/2)
	for i := range b {
		hi, ok1 := hexValue(h[2*i])
		lo, ok2 := hexValue(h[2*i+1])
		if !ok1 || !ok2 {
			return nil, false
		}
		b[i] = hi<<4 | lo
	}
	names := []any{}
	for i := 0; i < len(b); {
		l := int(b[i])
		i++
		if i+l > len(b) {
			return nil, false
		}
		names = append(names, decodeUTF8(b[i:i+l]))
		i += l
	}
	return names, true
}
