package parse

// The Hysteria (v1) URI parser of parser.md section 4.11. There is no user
// info; an "@" stays in the server.

func parseHysteria(rest string, _ *lineState) (map[string]any, error) {
	p := splitURI(rest, false, "0123456789")
	port := 443.0
	if p.port != "" {
		port = numberValue(p.port)
	}
	f := map[string]any{"type": "hysteria", "server": p.host, "port": port}
	if p.hasFragment {
		name, ok := PercentDecodeStrict(p.fragment)
		if !ok {
			return nil, errReject
		}
		f["name"] = name
	} else {
		f["name"] = "Hysteria " + p.host + ":" + jsNumber(port)
	}
	q, ok := splitQuery(p.query, q5First)
	if !ok {
		return nil, errReject
	}
	for _, it := range q {
		v := jsString(it.Value, true)
		switch it.Key {
		case "alpn":
			if v == "" {
				delete(f, "alpn")
			} else {
				f["alpn"] = splitList(v, ",")
			}
		case "insecure":
			f["skip-cert-verify"] = ContainsTrueOr1(v)
		case "auth":
			f["auth-str"] = v
		case "mport":
			f["ports"] = v
		case "obfsParam":
			f["obfs"] = v
		case "upmbps":
			f["up"] = v
		case "downmbps":
			f["down"] = v
		case "obfs":
			f["_obfs"] = v
		case "peer", "fast-open":
			// peer is read after the loop; fast-open is read and never used.
		default:
			copyUnhandled(f, it)
		}
	}
	if _, ok := f["sni"]; !ok {
		if v, present := q.get("peer"); present {
			f["sni"] = v
		}
	}
	if _, ok := f["protocol"]; !ok {
		f["protocol"] = "udp"
	}
	return f, nil
}
