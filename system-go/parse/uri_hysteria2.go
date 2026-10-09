package parse

import (
	"math"
	"strings"
)

// The Hysteria2 URI parser of parser.md section 4.10.

func parseHysteria2(rest string, _ *lineState) (map[string]any, error) {
	p := splitURI(rest, true, "0123456789,;-")
	if !p.hasUserInfo {
		return nil, errReject
	}
	password, ok := PercentDecodeStrict(p.userInfo)
	if !ok {
		return nil, errReject
	}
	f := map[string]any{"type": "hysteria2", "server": p.host, "password": password}
	port := 443.0
	if p.port != "" {
		if digitsOnly(p.port) {
			port = numberValue(p.port)
		} else {
			// A list: the oracle picks a member at random; Lattice takes the
			// first entry, the lower bound of a range (normaliser.md 7). A
			// list written with ";" gives not-a-number.
			f["ports"] = p.port
			port = math.NaN()
			if !strings.Contains(p.port, ";") {
				first, _, _ := strings.Cut(p.port, ",")
				lower, _, _ := strings.Cut(first, "-")
				port = LeadingInteger(lower)
			}
		}
	}
	f["port"] = port
	if p.hasFragment {
		name, ok := PercentDecodeStrict(p.fragment)
		if !ok {
			return nil, errReject
		}
		f["name"] = name
	} else {
		f["name"] = "Hysteria2 " + p.host + ":" + jsNumber(port)
	}

	q, ok := splitQuery(p.query, q1)
	if !ok {
		return nil, errReject
	}
	if v, present := q.or("sni", "peer"); present {
		f["sni"] = v
	}
	if v, present := q.str("obfs"); present && v != "none" {
		f["obfs"] = v
	}
	if v, present := q.get("obfs-password"); present {
		f["obfs-password"] = v
	}
	if v, present := q.get("mport"); present {
		f["ports"] = v
	}
	insecure, _ := q.str("insecure")
	f["skip-cert-verify"] = ContainsTrueOr1(insecure)
	fastopen, _ := q.str("fastopen")
	f["tfo"] = ContainsTrueOr1(fastopen)
	if v, present := q.get("pinSHA256"); present {
		f["tls-fingerprint"] = v
	}
	if v, present := q.or("hop-interval", "hop_interval"); present {
		f["hop-interval"] = v
	}
	if v, present := q.str("keepalive"); present && digitsOnly(v) {
		f["keepalive"] = numberValue(v)
	}
	if v, present := q.get("upmbps"); present {
		f["up"] = v
	}
	if v, present := q.get("downmbps"); present {
		f["down"] = v
	}
	if v, present := q.str("ech"); present {
		if opts, ok := echOpts(v); ok {
			f["ech-opts"] = opts
		}
	}
	return f, nil
}
