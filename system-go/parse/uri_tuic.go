package parse

import "strings"

// The TUIC URI parser of parser.md section 4.12.

func parseTUIC(rest string, _ *lineState) (map[string]any, error) {
	p := splitURI(rest, true, "0123456789")
	userInfo, ok := PercentDecodeStrict(p.userInfo)
	if !ok {
		return nil, errReject
	}
	uuid, rawPassword, _ := strings.Cut(userInfo, ":")
	password, ok := PercentDecodeStrict(rawPassword)
	if !ok {
		return nil, errReject
	}
	port := 443.0
	if p.port != "" {
		port = numberValue(p.port)
	}
	f := map[string]any{
		"type":     "tuic",
		"server":   p.host,
		"port":     port,
		"uuid":     uuid,
		"password": password,
	}
	if p.hasFragment {
		name, ok := PercentDecodeStrict(p.fragment)
		if !ok {
			return nil, errReject
		}
		f["name"] = name
	} else {
		f["name"] = "TUIC " + p.host + ":" + jsNumber(port)
	}
	q, ok := splitQuery(p.query, q5All)
	if !ok {
		return nil, errReject
	}
	for _, it := range q {
		v := jsString(it.Value, true)
		switch it.Key {
		case "alpn":
			f["alpn"] = splitList(v, ",")
		case "allow-insecure", "insecure":
			f["skip-cert-verify"] = ContainsTrueOr1(v)
		case "fast-open":
			f["tfo"] = true
		case "disable-sni", "reduce-rtt":
			f[it.Key] = ContainsTrueOr1(v)
		case "congestion-control":
			f["congestion-controller"] = v
		default:
			copyUnhandled(f, it)
		}
	}
	return f, nil
}
