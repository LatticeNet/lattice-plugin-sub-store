package parse

import (
	"strings"
)

// The Loon line grammar of parser.md section 7.1 (parser rows 30 to 38) and
// the Loon WireGuard parser of 7.2 (row 39).

var loonCiphers = []string{
	"aes-128-cfb", "aes-128-ctr", "aes-128-gcm", "aes-192-cfb", "aes-192-ctr",
	"aes-192-gcm", "aes-256-cfb", "aes-256-ctr", "aes-256-gcm", "auto",
	"bf-cfb", "camellia-128-cfb", "camellia-192-cfb", "camellia-256-cfb",
	"chacha20-ietf-poly1305", "chacha20-ietf", "chacha20-poly1305", "chacha20",
	"none", "rc4-md5", "rc4", "salsa20", "xchacha20-ietf-poly1305",
	"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm",
}

// ssrProtocols and ssrObfs are the ShadowsocksR enumerations Loon and
// Quantumult X share.
var ssrProtocols = []string{"origin", "auth_sha1_v4", "auth_aes128_md5", "auth_aes128_sha1", "auth_chain_a", "auth_chain_b"}

var ssrObfs = []string{"plain", "http_simple", "http_post", "random_head", "tls1.2_ticket_auth", "tls1.2_ticket_fastauth"}

// loonOption is one row of the option table of 7.1: the spec and the type
// keywords that accept it (nil for all).
type loonOption struct {
	spec  optSpec
	types []string
}

var loonOptions = map[string]loonOption{
	"over-tls":          {setKind(optBool, "tls"), []string{"vmess", "vless", "trojan", "anytls", "socks5"}},
	"tls-name":          {setText("sni", dequote), loonTLSNameTypes},
	"sni":               {setText("sni", dequote), loonTLSNameTypes},
	"skip-cert-verify":  {setKind(optBool, "skip-cert-verify"), loonTLSNameTypes},
	"tls-cert-sha256":   {setText("tls-fingerprint", dequote), loonTLSNameTypes},
	"tls-pubkey-sha256": {setText("tls-pubkey-sha256", dequote), loonTLSNameTypes},
	"tls-profile": {optSpec{kind: optText, set: func(n *lineNode, v any) error {
		p := TrimECMAScript(dequote(v.(string)))
		n.f["_loon_tls_profile"] = p
		switch p {
		case "chrome", "chrome147":
			n.f["client-fingerprint"] = "chrome"
		case "safari-ios18", "safari-ios-26":
			n.f["client-fingerprint"] = "ios"
		}
		return nil
	}}, loonNotHTTP},
	"alpn":      {optSpec{kind: optQuoted, set: setALPN}, loonNotHTTP},
	"transport": {keepKind(optEnum, "transport", "tcp", "ws", "http"), loonTransportTypes},
	"host":      {keepText("host", dequote), loonTransportTypes},
	"path":      {keepText("path", nil), loonTransportTypes},
	"alterId":   {setKind(optDigits, "alterId"), []string{"vmess"}},
	"flow":      {setText("flow", dequote), []string{"vless"}},
	"public-key": {optSpec{kind: optText, set: func(n *lineNode, v any) error {
		realityOpts(n.f)["public-key"] = dequote(v.(string))
		return nil
	}}, loonTransportTypes},
	"short-id": {optSpec{kind: optText, set: func(n *lineNode, v any) error {
		realityOpts(n.f)["short-id"] = dequote(v.(string))
		return nil
	}}, loonTransportTypes},
	"fast-open": {setKind(optBool, "tfo"), nil},
	"udp":       {setKind(optBool, "udp"), nil},
	"ip-mode":   {setText("ip-version", nil), nil},
	"block-quic": {optSpec{kind: optBool, set: func(n *lineNode, v any) error {
		if v == true {
			n.f["block-quic"] = "on"
		} else {
			n.f["block-quic"] = "off"
		}
		return nil
	}}, nil},
	"obfs-name":      {keepKind(optEnum, "obfs", "http", "tls"), []string{"shadowsocks"}},
	"obfs-host":      {keepText("obfs-host", dequote), []string{"shadowsocks", "shadowsocksr"}},
	"obfs-uri":       {keepText("obfs-uri", nil), []string{"shadowsocks", "shadowsocksr"}},
	"protocol":       {setEnum("protocol", ssrProtocols...), []string{"shadowsocksr"}},
	"protocol-param": {setKind(optTextNoEq, "protocol-param"), []string{"shadowsocksr"}},
	"obfs":           {keepKind(optEnum, "obfs", ssrObfs...), []string{"shadowsocksr"}},
	"obfs-param":     {setText("obfs-param", nil), []string{"shadowsocksr"}},
	"udp-port":       {setKind(optDigits, "udp-port"), []string{"shadowsocks", "shadowsocksr"}},
	"udp-over-tcp": {optSpec{kind: optBool, set: func(n *lineNode, _ any) error {
		// false has the same effect as true (quirk).
		n.f["udp-over-tcp"] = true
		n.f["udp-over-tcp-version"] = 2.0
		return nil
	}}, []string{"shadowsocks"}},
	"download-bandwidth": {setText("down", nil), []string{"hysteria2"}},
	"server-ports": {optSpec{kind: optQuoted, set: func(n *lineNode, v any) error {
		s := TrimECMAScript(v.(string))
		n.f["ports"] = removeGapsAround(s, "-,")
		return nil
	}}, []string{"hysteria2"}},
	"hop-interval":                {setKind(optDigits, "hop-interval"), []string{"hysteria2"}},
	"salamander-password":         {obfsPassword("salamander", nil), []string{"hysteria2"}},
	"ecn":                         {setKind(optBool, "ecn"), []string{"hysteria2"}},
	"idle-session-check-interval": {setKind(optDigits, "idle-session-check-interval"), []string{"anytls"}},
	"idle-session-timeout":        {setKind(optDigits, "idle-session-timeout"), []string{"anytls"}},
	"min-idle-session":            {setKind(optDigits, "min-idle-session"), []string{"anytls"}},
	"max-stream-count":            {setKind(optDigits, "max-stream-count"), []string{"anytls"}},
	"shadow-tls-version":          {keepKind(optDigits, "shadow-tls-version"), []string{"shadowsocks", "shadowsocksr"}},
	"shadow-tls-sni":              {keepText("shadow-tls-sni", nil), []string{"shadowsocks", "shadowsocksr"}},
	"shadow-tls-password":         {keepText("shadow-tls-password", stripQuotes), []string{"shadowsocks", "shadowsocksr"}},
	"server-dns": {optSpec{kind: optQuotedOrToKey, set: func(n *lineNode, v any) error {
		n.f["server-dns"] = splitTrimNonEmpty(v.(string), ",")
		return nil
	}}, nil},
}

var (
	// tls-name, sni and the certificate pins: every type but ss, ssr and http.
	loonTLSNameTypes   = []string{"vmess", "vless", "trojan", "anytls", "hysteria2", "https", "socks5"}
	loonNotHTTP        = []string{"shadowsocks", "shadowsocksr", "vmess", "vless", "trojan", "anytls", "hysteria2", "https", "socks5"}
	loonTransportTypes = []string{"vmess", "vless", "trojan", "anytls"}
)

// loonTypes are the type keywords with their node type; https is http with
// TLS.
var loonTypes = []struct{ keyword, nodeType string }{
	{"shadowsocks", "ss"}, {"shadowsocksr", "ssr"}, {"vmess", "vmess"},
	{"vless", "vless"}, {"trojan", "trojan"}, {"anytls", "anytls"},
	{"hysteria2", "hysteria2"}, {"https", "http"}, {"http", "http"},
	{"socks5", "socks5"},
}

// loonAccepts is the option table of one type keyword.
func loonAccepts(keyword string) map[string]optSpec {
	out := map[string]optSpec{}
	for key, o := range loonOptions {
		if o.types == nil {
			out[key] = o.spec
			continue
		}
		for _, t := range o.types {
			if t == keyword {
				out[key] = o.spec
				break
			}
		}
	}
	return out
}

var loonOptionTables = func() map[string]map[string]optSpec {
	out := map[string]map[string]optSpec{}
	for _, t := range loonTypes {
		out[t.keyword] = loonAccepts(t.keyword)
	}
	return out
}()

// realityOpts returns the node's reality-opts, created when absent.
func realityOpts(f map[string]any) map[string]any {
	ro, ok := f["reality-opts"].(map[string]any)
	if !ok {
		ro = map[string]any{}
		f["reality-opts"] = ro
	}
	return ro
}

// removeGapsAround removes runs of white space next to any of chars.
func removeGapsAround(s, chars string) string {
	var b []byte
	for i := 0; i < len(s); {
		if !isGap(s[i]) {
			b = append(b, s[i])
			i++
			continue
		}
		j := skipGap(s, i)
		prev := len(b) > 0 && strings.IndexByte(chars, b[len(b)-1]) >= 0
		next := j < len(s) && strings.IndexByte(chars, s[j]) >= 0
		if !prev && !next {
			b = append(b, s[i:j]...)
		}
		i = j
	}
	return string(b)
}

// quotedField reads ', "text"' at i: a comma, optional white space and a
// double-quoted text that may hold commas.
func quotedField(s string, i int, nonEmpty bool) (string, int, bool) {
	j := skipGap(s, i)
	if j >= len(s) || s[j] != ',' {
		return "", 0, false
	}
	j = skipGap(s, j+1)
	if j >= len(s) || s[j] != '"' {
		return "", 0, false
	}
	k := strings.IndexByte(s[j+1:], '"')
	if k < 0 || (nonEmpty && k == 0) {
		return "", 0, false
	}
	end := j + 1 + k + 1
	if !atOptionEnd(s, end) {
		return "", 0, false
	}
	return s[j+1 : j+1+k], end, true
}

// enumField reads ", word" at i for the first of words that starts there.
// The field after it begins with a comma, so a word with characters after it
// fails there.
func enumField(s string, i int, words []string) (string, int, bool) {
	j := skipGap(s, i)
	if j >= len(s) || s[j] != ',' {
		return "", 0, false
	}
	j = skipGap(s, j+1)
	for _, w := range words {
		if strings.HasPrefix(s[j:], w) {
			return w, j + len(w), true
		}
	}
	return "", 0, false
}

// textField reads ", text" at i: the text runs to the next comma.
func textField(s string, i int) (string, int, bool) {
	j := skipGap(s, i)
	if j >= len(s) || s[j] != ',' {
		return "", 0, false
	}
	j = skipGap(s, j+1)
	end := commaFrom(s, j)
	return s[j:end], end, true
}

// loonVMessCipher is the Loon VMess security rule.
func loonVMessCipher(s string) string {
	switch c := strings.ToLower(TrimECMAScript(s)); c {
	case "none", "auto", "aes-128-gcm":
		return c
	case "chacha20-ietf-poly1305":
		return "chacha20-poly1305"
	}
	return "auto"
}

// parseLoon is the Loon grammar shared by rows 30 to 38.
func parseLoon(line string, _ *lineState) (map[string]any, error) {
	name, eq, ok := lineName(line)
	if !ok {
		return nil, errReject
	}
	start := skipGap(line, eq+1)
	keyword, nodeType := "", ""
	for _, t := range loonTypes {
		if _, ok := keywordAt(line, start, t.keyword, true); ok {
			keyword, nodeType = t.keyword, t.nodeType
			break
		}
	}
	if keyword == "" {
		return nil, errReject
	}
	n := newLineNode()
	f := n.f
	f["name"] = name
	f["type"] = nodeType
	if keyword == "https" {
		f["tls"] = true
	}
	i, ok := serverPort(line, start+len(keyword), f, true)
	if !ok {
		return nil, errReject
	}
	switch keyword {
	case "shadowsocks", "shadowsocksr":
		var cipher, password string
		if cipher, i, ok = enumField(line, i, loonCiphers); !ok {
			return nil, errReject
		}
		if password, i, ok = quotedField(line, i, false); !ok {
			return nil, errReject
		}
		f["cipher"], f["password"] = cipher, password
		if keyword == "shadowsocks" {
			if mode, j, ok := enumField(line, i, []string{"http", "tls"}); ok {
				if host, k, ok := textField(line, j); ok && !strings.ContainsRune(host, '=') {
					n.x["obfs"], n.x["obfs-host"] = mode, host
					i = k
				}
			}
		}
	case "vmess":
		var security, uuid string
		if security, i, ok = textField(line, i); !ok {
			return nil, errReject
		}
		if uuid, i, ok = quotedField(line, i, true); !ok {
			return nil, errReject
		}
		f["cipher"], f["uuid"] = loonVMessCipher(security), uuid
	case "vless":
		var uuid string
		if uuid, i, ok = quotedField(line, i, false); !ok {
			return nil, errReject
		}
		f["uuid"] = uuid
	case "trojan", "anytls", "hysteria2":
		var password string
		if password, i, ok = quotedField(line, i, false); !ok {
			return nil, errReject
		}
		f["password"] = password
	default: // http, https, socks5: optionally a user, and a quoted password
		if j := skipGap(line, i); j < len(line) && line[j] == ',' {
			// The user is not trimmed: the white space after the comma stays.
			u := j + 1
			uEnd := commaFrom(line, u)
			if uEnd > u && uEnd < len(line) && !strings.ContainsRune(line[u:uEnd], '=') {
				if password, k, ok := quotedField(line, uEnd, false); ok {
					f["username"], f["password"] = line[u:uEnd], password
					i = k
					break
				}
			}
			if password, k, ok := quotedField(line, i, false); ok {
				f["password"] = password
				i = k
			}
		}
	}
	if !readOptions(line, i, loonOptionTables[keyword], n) {
		return nil, errReject
	}
	return loonPost(keyword, n)
}

// loonPost applies obfs, the VMess defaults, the transport and Shadow-TLS.
func loonPost(keyword string, n *lineNode) (map[string]any, error) {
	f, x := n.f, n.x
	switch keyword {
	case "shadowsocks":
		if mode, ok := x["obfs"]; ok {
			f["plugin"] = "obfs"
			f["plugin-opts"] = obfsOpts(mode, x)
		}
	case "shadowsocksr":
		if word, ok := x["obfs"]; ok {
			f["obfs"] = word
		}
	case "vmess":
		if _, ok := f["alterId"]; !ok {
			f["alterId"] = 0.0
		}
	}
	switch keyword {
	case "vmess", "vless", "trojan", "anytls":
		if t, ok := x["transport"].(string); ok && t != "tcp" {
			f["network"] = t
			opts := map[string]any{}
			if p, ok := x["path"]; ok {
				opts["path"] = p
			}
			headers := map[string]any{}
			if h, ok := x["host"]; ok {
				headers["Host"] = h
			}
			opts["headers"] = headers
			f[t+"-opts"] = opts
		}
	}
	if keyword == "shadowsocks" || keyword == "shadowsocksr" {
		if err := applyShadowTLS(n, 0); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// parseLoonWireGuard is row 39: name = wireguard, key = value, ...,
// peers = [{key = value, ...}]. Values are found by key anywhere in the line
// (line fields) or in the peers block (peer fields); the first occurrence
// wins and a value ends at the next comma, white space after "=" included.
func parseLoonWireGuard(line string, _ *lineState) (map[string]any, error) {
	name, eq, ok := lineName(line)
	if !ok {
		return nil, errReject
	}
	if _, ok := keywordAt(line, skipGap(line, eq+1), "wireguard", true); !ok {
		return nil, errReject
	}
	peerStart, ok := keyValueAt(line, "peers")
	if !ok {
		return nil, errReject
	}
	rest := line[skipGap(line, peerStart):]
	if !strings.HasPrefix(rest, "[{") {
		return nil, errReject
	}
	end := strings.Index(rest, "}]")
	if end < 0 {
		return nil, errReject
	}
	block := rest[2:end]

	f := map[string]any{"name": name, "type": "wireguard", "udp": true}
	peer := map[string]any{}
	if v, ok := wgText(block, "endpoint"); ok {
		v = unquote(v, '"')
		if c := strings.LastIndexByte(v, ':'); c >= 0 && digitsOnly(v[c+1:]) {
			f["server"], f["port"] = v[:c], numberValue(v[c+1:])
			peer["server"], peer["port"] = f["server"], f["port"]
		}
	}
	for _, k := range [][3]string{{"interface-ip", "ip", "ip"}, {"interface-ipv6", "ipv6", "ipv6"}} {
		if v, ok := wgText(line, k[0]); ok {
			f[k[1]] = unquote(v, '"')
			peer[k[2]] = f[k[1]]
		}
	}
	if v, ok := wgText(line, "private-key"); ok {
		f["private-key"] = unquote(v, '"')
	}
	if v, ok := wgText(block, "public-key"); ok {
		f["public-key"] = unquote(v, '"')
		peer["public-key"] = f["public-key"]
	}
	if v, ok := wgText(block, "preshared-key"); ok {
		f["preshared-key"] = unquote(v, '"')
		peer["pre-shared-key"] = f["preshared-key"]
	}
	if start, ok := keyValueAt(block, "allowed-ips"); ok {
		s := block[skipGap(block, start):]
		if strings.HasPrefix(s, `"`) {
			if k := strings.IndexByte(s[1:], '"'); k >= 0 {
				ips := []any{}
				for _, p := range strings.Split(s[1:k+1], ",") {
					ips = append(ips, TrimECMAScript(p))
				}
				f["allowed-ips"] = ips
				peer["allowed-ips"] = ips
			}
		}
	}
	if start, ok := keyValueAt(block, "reserved"); ok {
		s := strings.TrimPrefix(block[skipGap(block, start):], `"`)
		if strings.HasPrefix(s, "[") {
			if k := strings.IndexByte(s, ']'); k >= 0 {
				if v, err := decodeJSON(s[:k+1]); err == nil {
					f["reserved"] = v
					peer["reserved"] = v
				}
			}
		}
	}
	for _, k := range []string{"mtu", "keepalive"} {
		if start, ok := keyValueAt(line, k); ok {
			j := skipGap(line, start)
			e := j
			for e < len(line) && line[e] >= '0' && line[e] <= '9' {
				e++
			}
			if e > j {
				f[k] = numberValue(line[j:e])
			}
		}
	}
	var dns []any
	for _, k := range []string{"dns", "dnsv6"} {
		if v, ok := wgText(line, k); ok {
			dns = append(dns, v)
		}
	}
	if dns != nil {
		f["dns"] = dns
		f["remote-dns-resolve"] = true
	}
	if start, ok := keyValueAt(line, "server-dns"); ok {
		s := line[skipGap(line, start):]
		v := s[:commaFrom(s, 0)]
		if strings.HasPrefix(s, `"`) {
			if k := strings.IndexByte(s[1:], '"'); k >= 0 {
				v = s[1 : k+1]
			}
		}
		f["server-dns"] = splitTrimNonEmpty(v, ",")
	}
	f["peers"] = []any{peer}
	return f, nil
}

// keyValueAt finds the first "key =" in s whose key starts s or follows a
// comma, "{" or white space, and returns where the text after "=" starts.
func keyValueAt(s, key string) (int, bool) {
	for i := strings.Index(s, key); i >= 0; {
		if i == 0 || s[i-1] == ',' || s[i-1] == '{' || isGap(s[i-1]) {
			k := skipGap(s, i+len(key))
			if k < len(s) && s[k] == '=' {
				return k + 1, true
			}
		}
		next := strings.Index(s[i+1:], key)
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return 0, false
}

// wgText is the text value of key: everything after "=" to the next comma.
func wgText(s, key string) (string, bool) {
	start, ok := keyValueAt(s, key)
	if !ok {
		return "", false
	}
	return s[start:commaFrom(s, start)], true
}
