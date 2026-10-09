package parse

// The AnyTLS URI parser of parser.md section 4.14: the VLESS parser runs
// first (so a link without a port is rejected) and its result is overlaid.

// anyTLSPresent are the keys the VLESS pass always creates, with or without
// a value; a query key of one of these names is never copied.
var anyTLSPresent = map[string]bool{
	"type": true, "name": true, "server": true, "port": true, "uuid": true,
	"udp": true, "tls": true, "sni": true, "flow": true,
	"client-fingerprint": true, "alpn": true, "skip-cert-verify": true,
	"_echConfigList": true, "tls-fingerprint": true, "_vcn": true,
	"name-cert-verify": true, "_h2": true, "packet-encoding": true,
	"network": true, "password": true,
}

func parseAnyTLS(rest string, _ *lineState) (map[string]any, error) {
	r, err := parseVLESSBody(rest, flavourAnyTLS)
	if err != nil {
		return nil, err
	}
	// The password is the user info strict-decoded twice.
	once, ok := PercentDecodeStrict(r.shape.uuid)
	if !ok {
		return nil, errReject
	}
	password, ok := PercentDecodeStrict(once)
	if !ok {
		return nil, errReject
	}
	f := r.fields
	f["type"] = "anytls"
	f["password"] = password
	// With a fragment the VLESS pass has already decoded it as the name.
	if !r.shape.hasFragment {
		f["name"] = "AnyTLS " + r.shape.host + ":" + jsNumber(numberValue(r.shape.port))
	}

	q, ok := splitQuery(r.shape.query, q5All)
	if !ok {
		return nil, errReject
	}
	for _, it := range q {
		v := jsString(it.Value, true)
		switch it.Key {
		case "alpn":
			if v != "" {
				f["alpn"] = splitList(v, ",")
			}
		case "insecure":
			f["skip-cert-verify"] = ContainsTrueOr1(v)
		case "udp":
			f["udp"] = ContainsTrueOr1(v)
		default:
			if !anyTLSPresent[it.Key] {
				copyUnhandled(f, it)
			}
		}
	}
	delete(f, "uuid")
	if f["network"] == "tcp" {
		if _, reality := f["reality-opts"]; !reality {
			delete(f, "network")
			delete(f, "security")
		}
	}
	return f, nil
}
