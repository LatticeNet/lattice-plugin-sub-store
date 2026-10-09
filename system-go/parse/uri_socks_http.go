package parse

import (
	"strings"
)

// The proxy URI parsers of parser.md sections 4.2 (socks5://, socks5+tls://,
// http://, https://) and 4.3 (socks://).

// proxySchemes are the 4.2 schemes with their node type and TLS flag.
var proxySchemes = []struct {
	prefix, typ string
	tls         bool
}{
	{"socks5+tls://", "socks5", true},
	{"socks5://", "socks5", false},
	{"http://", "http", false},
	{"https://", "http", true},
}

var proxySchemePrefixes = []string{"socks5+tls://", "socks5://", "http://", "https://"}

// parseProxyLine is row 1 of the parser table: one parser for the four
// schemes of 4.2.
func parseProxyLine(line string, _ *lineState) (map[string]any, error) {
	for _, s := range proxySchemes {
		if strings.HasPrefix(line, s.prefix) {
			return parseProxyURI(line[len(s.prefix):], s.typ, s.tls)
		}
	}
	return nil, errReject
}

// parseProxyURI reads [user:password@]authority[#fragment]. User info is
// recognised only with a ":" before an "@"; the port is the digits after a
// ":" that ends the authority, optionally followed by "/" and a query, and
// anything else after the host (a path) stays in the server.
func parseProxyURI(rest, typ string, tls bool) (map[string]any, error) {
	body, rawFragment, hasFragment := strings.Cut(rest, "#")
	f := map[string]any{"type": typ, "tls": tls}
	if c := strings.IndexByte(body, ':'); c >= 0 {
		if at := strings.IndexByte(body[c+1:], '@'); at >= 0 {
			user, ok := PercentDecodeStrict(body[:c])
			if !ok {
				return nil, errReject
			}
			password, ok := PercentDecodeStrict(body[c+1 : c+1+at])
			if !ok {
				return nil, errReject
			}
			f["username"], f["password"] = user, password
			body = body[c+1+at+1:]
		}
	}
	server, port := body, ""
	if c := strings.LastIndexByte(body, ':'); c >= 0 {
		d := c + 1
		for d < len(body) && body[d] >= '0' && body[d] <= '9' {
			d++
		}
		tail := strings.TrimPrefix(body[d:], "/")
		if d > c+1 && (tail == "" || tail[0] == '?') {
			server, port = body[:c], body[c+1:d]
		}
	}
	var portValue float64
	switch {
	case port != "":
		portValue = numberValue(port)
	case tls:
		portValue = 443
	case typ == "http":
		portValue = 80
	default:
		return nil, errReject // socks5 without a port
	}
	f["server"] = server
	f["port"] = portValue
	if hasFragment {
		name, ok := PercentDecodeStrict(rawFragment)
		if !ok {
			return nil, errReject
		}
		f["name"] = name
	} else {
		f["name"] = typ + " " + server + ":" + jsNumber(portValue)
	}
	return f, nil
}

// parseSocks reads socks://[base64@]host:port[?query][#fragment]: the user
// info before the last "@" is strict-decoded, Lenient-Base64-decoded and
// split on ":" into user name and password.
func parseSocks(rest string, _ *lineState) (map[string]any, error) {
	body, rawFragment, hasFragment := strings.Cut(rest, "#")
	body, _, _ = strings.Cut(body, "?")
	f := map[string]any{"type": "socks5"}
	if at := strings.LastIndexByte(body, '@'); at >= 0 {
		d, ok := PercentDecodeStrict(body[:at])
		if !ok {
			return nil, errReject
		}
		parts := strings.Split(Base64DecodeLenient(d), ":")
		f["username"] = parts[0]
		if len(parts) > 1 {
			f["password"] = parts[1]
		}
		body = body[at+1:]
	}
	c := strings.LastIndexByte(body, ':')
	if c < 0 || !digitsOnly(body[c+1:]) {
		return nil, errReject
	}
	server, port := body[:c], numberValue(body[c+1:])
	f["server"], f["port"] = server, port
	if hasFragment {
		name, ok := PercentDecodeStrict(rawFragment)
		if !ok {
			return nil, errReject
		}
		f["name"] = name
	} else {
		f["name"] = "socks " + server + ":" + jsNumber(port)
	}
	return f, nil
}
