package parse

import (
	"strings"
)

// The ShadowsocksR URI parser of parser.md section 4.5. The text after
// ssr:// is Lenient-Base64 of host:port:protocol:method:obfs:password/?params.

func parseSSR(rest string, _ *lineState) (map[string]any, error) {
	decoded := Base64DecodeLenient(rest)
	// The host and port end at the first ":origin", or else at the first
	// ":auth_".
	marker := strings.Index(decoded, ":origin")
	if marker < 0 {
		marker = strings.Index(decoded, ":auth_")
	}
	hostPort, fields := "", decoded
	if marker >= 0 {
		hostPort, fields = decoded[:marker], decoded[marker+1:]
	}
	server, port := hostPort, ""
	if c := strings.LastIndexByte(hostPort, ':'); c >= 0 {
		server, port = hostPort[:c], hostPort[c+1:]
	}
	slash := strings.Index(fields, "/?")
	if slash < 0 {
		return nil, errReject
	}
	parts := strings.Split(fields[:slash], ":")
	part := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	f := map[string]any{
		"type":     "ssr",
		"server":   server,
		"port":     port,
		"protocol": part(0),
		"cipher":   part(1),
		"obfs":     part(2),
		"password": Base64DecodeLenient(part(3)),
		"name":     server,
	}

	// Parameters count only when there are at least two items.
	items := strings.Split(fields[slash+2:], "&")
	if len(items) < 2 {
		return f, nil
	}
	params := map[string]string{}
	for _, item := range items {
		k, v, _ := strings.Cut(item, "=")
		v = TrimECMAScript(v)
		if v == "" || v == "(null)" {
			continue
		}
		params[k] = v
	}
	if v, ok := params["remarks"]; ok {
		f["name"] = Base64DecodeLenient(v)
	}
	protocolParam, ok := params["protoparam"]
	if !ok {
		protocolParam, ok = params["protocolparam"]
	}
	if ok {
		if s := withoutWhiteSpace(Base64DecodeLenient(protocolParam)); nonBlank(s) {
			f["protocol-param"] = s
		}
	}
	if v, ok := params["obfsparam"]; ok {
		if s := withoutWhiteSpace(Base64DecodeLenient(v)); nonBlank(s) {
			f["obfs-param"] = s
		}
	}
	return f, nil
}

// withoutWhiteSpace removes every ECMAScript white space and line terminator.
func withoutWhiteSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if isESWhiteSpace(r) {
			return -1
		}
		return r
	}, s)
}
