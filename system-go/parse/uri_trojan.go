package parse

import (
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// The Trojan URI parser of parser.md section 4.9.

func parseTrojan(rest string, _ *lineState) (map[string]any, error) {
	rest = trojanDefaultPort(rest)

	// The fragment starts at the first "#" that has a character after it.
	fragment, hasFragment := "", false
	for i := strings.IndexByte(rest, '#'); i >= 0; {
		if i+1 < len(rest) {
			rest, fragment, hasFragment = rest[:i], rest[i+1:], true
			break
		}
		next := strings.IndexByte(rest[i+1:], '#')
		if next < 0 {
			break
		}
		i += 1 + next
	}

	// trojan://password@host:port[/][?query]: the password runs to the
	// first "@", the host is a bracketed IPv6 literal or a run without "/",
	// "?" and "#", the port is digits.
	at := strings.IndexByte(rest, '@')
	if at <= 0 || hasLineTerminator(rest) {
		return nil, errReject
	}
	password, tail := rest[:at], rest[at+1:]
	host, port, rawQuery, ok := trojanHostPort(tail)
	if !ok {
		return nil, errReject
	}
	bare := host
	if strings.HasPrefix(host, "[") {
		bare = host[1 : len(host)-1]
	}
	if (bare != host || strings.Contains(host, ":")) && !normalise.IsIPv6Literal(bare) {
		return nil, errReject
	}
	portValue := numberValue(port)
	if portValue < 1 || portValue > 65535 {
		return nil, errReject
	}
	f := map[string]any{
		"type":     "trojan",
		"server":   host,
		"port":     portValue,
		"password": PercentDecodeLenient(password),
		"name":     host + ":" + port,
	}
	if hasFragment && nonBlank(fragment) {
		if name, ok := PercentDecodeStrict(fragment); ok {
			f["name"] = name
		}
	}

	q, _ := splitQuery(rawQuery, q3)
	truthyTest := func(key string) (bool, bool) {
		v, ok := q.get(key)
		if !ok {
			return false, false
		}
		return ContainsTrueOr1(jsString(v, true)), true
	}
	if v, ok := truthyTest("allowInsecure"); ok {
		f["skip-cert-verify"] = v
	}
	if v, present := q.or("sni", "peer"); present {
		f["sni"] = v
	}
	if v, present := q.get("fp"); present {
		f["client-fingerprint"] = v
	}
	if v, present := q.get("pcs"); present {
		f["tls-fingerprint"] = v
	}
	if v, present := q.get("vcn"); present {
		s, isText := v.(string)
		if !isText {
			return nil, errReject // splitting the boolean of a bare vcn throws
		}
		names := splitTrimNonEmpty(s, ",")
		f["_vcn"] = names
		if len(names) > 0 {
			f["name-cert-verify"] = names[0]
		}
	}
	if v, present := q.get("alpn"); truthy(v, present) {
		s, isText := v.(string)
		if !isText {
			return nil, errReject
		}
		f["alpn"] = splitList(s, ",")
	}
	if ws, _ := truthyTest("ws"); ws {
		f["network"] = "ws"
		opts := map[string]any{}
		if p, present := q.get("wspath"); present {
			opts["path"] = p
		}
		f["ws-opts"] = opts
	}
	if err := streamTransport(f, q, false); err != nil {
		return nil, err
	}
	if v, ok := truthyTest("udp"); ok {
		f["udp"] = v
	}
	if v, ok := truthyTest("tfo"); ok {
		f["tfo"] = v
	}
	return f, nil
}

// trojanDefaultPort inserts ":443" when the authority has no port. The
// authority ends at the first "?"; without one, the whole text is the
// authority, fragment included, so the port lands after the fragment
// (quirk). A "/" that ends the authority stays after the inserted port.
func trojanDefaultPort(rest string) string {
	end := len(rest)
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		end = q
	}
	auth := strings.TrimSuffix(rest[:end], "/")
	if c := strings.LastIndexByte(auth, ':'); c >= 0 && digitsOnly(auth[c+1:]) {
		return rest
	}
	return auth + ":443" + rest[len(auth):]
}

// trojanHostPort matches host:port[/][?query] after the password.
func trojanHostPort(tail string) (host, port, rawQuery string, ok bool) {
	var after string
	if strings.HasPrefix(tail, "[") {
		end := strings.IndexByte(tail, ']')
		if end < 0 || end+1 >= len(tail) || tail[end+1] != ':' {
			return "", "", "", false
		}
		host, after = tail[:end+1], tail[end+2:]
	} else {
		stop := strings.IndexAny(tail, "/?#")
		run := tail
		if stop >= 0 {
			run = tail[:stop]
		}
		c := strings.LastIndexByte(run, ':')
		if c <= 0 {
			return "", "", "", false
		}
		host, after = run[:c], tail[c+1:]
	}
	d := 0
	for d < len(after) && after[d] >= '0' && after[d] <= '9' {
		d++
	}
	if d == 0 {
		return "", "", "", false
	}
	port, after = after[:d], after[d:]
	after = strings.TrimPrefix(after, "/")
	switch {
	case after == "":
	case after[0] == '?':
		rawQuery = after[1:]
	default:
		return "", "", "", false
	}
	return host, port, rawQuery, true
}
