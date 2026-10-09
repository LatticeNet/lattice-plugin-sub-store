package parse

import (
	"strings"
)

// The VLESS URI parser of parser.md section 4.7. VMess links in the VLESS
// shape (4.6 form 1) and AnyTLS links (4.14) reuse it.

// vlessShape is vless://uuid@host:port[/][?query][#fragment] cut apart.
type vlessShape struct {
	uuid, host, port string
	query            string
	fragment         string
	hasFragment      bool
}

// matchVLESSShape matches the text after the scheme. The uuid is the text
// before the first "@"; the host is the shortest text that leaves ":digits",
// an optional "/", an optional query and an optional fragment to the end,
// so bracketed and bare IPv6 hosts both work. The query runs to the first
// "#" after its "?"; the fragment is everything after that "#". Like the
// regular expression it stands for, it fails on a line terminator inside the
// text.
func matchVLESSShape(rest string) (vlessShape, bool) {
	if hasLineTerminator(rest) {
		return vlessShape{}, false
	}
	at := strings.IndexByte(rest, '@')
	if at < 0 {
		return vlessShape{}, false
	}
	tail := rest[at+1:]
	// Every colon is a candidate end of the host. The digit runs after
	// different colons never overlap, so the scan is linear.
	for c := 0; c < len(tail); c++ {
		if tail[c] != ':' {
			continue
		}
		d := c + 1
		for d < len(tail) && tail[d] >= '0' && tail[d] <= '9' {
			d++
		}
		if d == c+1 {
			continue
		}
		e := d
		if e < len(tail) && tail[e] == '/' {
			e++
		}
		s := vlessShape{uuid: rest[:at], host: tail[:c], port: tail[c+1 : d]}
		switch {
		case e == len(tail):
			return s, true
		case tail[e] == '?':
			q := tail[e+1:]
			if h := strings.IndexByte(q, '#'); h >= 0 {
				s.query, s.fragment, s.hasFragment = q[:h], q[h+1:], true
			} else {
				s.query = q
			}
			return s, true
		case tail[e] == '#':
			s.fragment, s.hasFragment = tail[e+1:], true
			return s, true
		}
	}
	return vlessShape{}, false
}

// vlessFlavour says which scheme reuses the VLESS parser.
type vlessFlavour int

const (
	flavourVLESS  vlessFlavour = iota
	flavourVMess               // vmess:// in the VLESS shape (4.6 form 1)
	flavourAnyTLS              // anytls:// (4.14), overlaid by its own parser
)

// vlessResult is the VLESS parser's output: the node fields, the split query
// (AnyTLS copies its unhandled keys), and the shape's user info as written,
// before decoding (AnyTLS decodes it again as its password).
type vlessResult struct {
	fields   map[string]any
	query    query
	userInfo string
}

// parseVLESS parses the text after "vless://".
func parseVLESS(rest string, _ *lineState) (map[string]any, error) {
	r, err := parseVLESSBody(rest, flavourVLESS)
	if err != nil {
		return nil, err
	}
	return r.fields, nil
}

// parseVLESSBody is section 4.7 for all three flavours.
func parseVLESSBody(rest string, flavour vlessFlavour) (vlessResult, error) {
	shape, ok := matchVLESSShape(rest)
	shadowrocket := false
	if !ok {
		// Shadowrocket form: Base64 before the first "?", query re-attached.
		qi := strings.IndexByte(rest, '?')
		if qi < 0 {
			return vlessResult{}, errReject
		}
		if shape, ok = matchVLESSShape(Base64DecodeLenient(rest[:qi]) + rest[qi:]); !ok {
			return vlessResult{}, errReject
		}
		shadowrocket = true
	}
	uuid, ok := PercentDecodeStrict(shape.uuid)
	if !ok {
		return vlessResult{}, errReject
	}
	if shadowrocket {
		if _, after, found := strings.Cut(uuid, ":"); found {
			uuid = after
		}
	}
	q, ok := splitQuery(shape.query, q2)
	if !ok {
		return vlessResult{}, errReject
	}
	port := numberValue(shape.port)
	f := map[string]any{
		"type":   "vless",
		"server": shape.host,
		"port":   port,
		"uuid":   uuid,
		"udp":    true,
	}

	// Name: the fragment when present (an empty one gives the empty name),
	// else remarks, else remark, else the default.
	defaultPrefix := "VLESS "
	if flavour == flavourVMess {
		defaultPrefix = "VMess "
	}
	switch {
	case shape.hasFragment:
		name, ok := PercentDecodeStrict(shape.fragment)
		if !ok {
			return vlessResult{}, errReject
		}
		f["name"] = name
	default:
		if v, present := q.or("remarks", "remark"); truthy(v, present) {
			f["name"] = v
		} else {
			f["name"] = defaultPrefix + shape.host + ":" + jsNumber(port)
		}
	}

	// tls first: false for none, the empty text for an empty value, true
	// for anything else; absent without a security item.
	security, hasSecurity := q.str("security")
	if hasSecurity {
		switch security {
		case "none":
			f["tls"] = false
		case "":
			f["tls"] = ""
		default:
			f["tls"] = true
		}
	}
	_, hasPBK := q.get("pbk")
	reality := security == "reality" || hasPBK
	if shadowrocket {
		if v, present := q.str("tls"); present && ContainsTrueOr1(v) {
			f["tls"] = true
			if !hasSecurity {
				reality = true
			}
		}
	}
	if v, present := q.or("sni", "peer"); present {
		f["sni"] = v
	}
	if v, present := q.get("flow"); present {
		f["flow"] = v
	}
	if shadowrocket {
		if v, present := q.get("flow"); !truthy(v, present) {
			switch x, _ := q.str("xtls"); x {
			case "1":
				f["flow"] = "xtls-rprx-direct"
			case "2":
				f["flow"] = "xtls-rprx-vision"
			}
		}
	}
	if v, present := q.get("fp"); present {
		f["client-fingerprint"] = v
	}
	if v, present := q.str("alpn"); present && v != "" {
		f["alpn"] = splitList(v, ",")
	}
	allowInsecure, _ := q.str("allowInsecure")
	f["skip-cert-verify"] = ContainsTrueOr1(allowInsecure)
	if v, present := q.str("ech"); present {
		f["_echConfigList"] = v
		if opts, ok := echOpts(v); ok {
			f["ech-opts"] = opts
		}
	}
	if v, present := q.get("pcs"); present {
		f["tls-fingerprint"] = v
	}
	if v, present := q.str("vcn"); present && v != "" {
		names := splitTrimNonEmpty(v, ",")
		f["_vcn"] = names
		if len(names) > 0 {
			f["name-cert-verify"] = names[0]
		}
	}
	h2, _ := q.str("h2")
	f["_h2"] = ContainsTrueOr1(h2)
	pe, _ := q.str("packetEncoding")
	switch strings.ToLower(TrimECMAScript(pe)) {
	case "none":
		f["packet-encoding"] = ""
	case "packet":
		f["packet-encoding"] = "packetaddr"
	default:
		f["packet-encoding"] = "xudp"
	}
	if reality {
		ro := map[string]any{}
		if pbk, present := q.get("pbk"); present {
			ro["public-key"] = pbk
			switch flag, _ := q.str("support-x25519mlkem768"); flag {
			case "1", "t", "T", "true", "TRUE", "True":
				ro["support-x25519mlkem768"] = true
			}
		}
		if v, present := q.get("sid"); present {
			ro["short-id"] = v
		}
		if v, present := q.get("spx"); present {
			ro["_spider-x"] = v
		}
		if len(ro) > 0 {
			f["reality-opts"] = ro
		}
	}

	network := "tcp"
	if v, present := q.str("type"); present && v != "" {
		network = v
	} else if shadowrocket {
		if obfs, present := q.str("obfs"); present && obfs != "" {
			network = obfs
			if obfs == "none" {
				network = "tcp"
			}
		}
	}
	httpUpgrade := false
	switch network {
	case "tcp":
		if ht, _ := q.str("headerType"); ht == "http" {
			network = "http"
		}
	case "http":
		network = "h2"
	case "httpupgrade":
		network, httpUpgrade = "ws", true
	case "websocket":
		network = "ws"
	}
	f["network"] = network

	encryption, _ := q.str("encryption")
	if flavour == flavourVMess {
		f["cipher"] = vmessCipher(encryption)
	} else if encryption != "" {
		f["encryption"] = encryption
	}
	if v, present := q.get("pqv"); present {
		f["_pqv"] = v
	}
	if v, present := q.str("fm"); present {
		if obj, err := decodeJSON(v); err == nil {
			if m, isObject := obj.(map[string]any); isObject {
				f["_finalmask"] = m
			} else {
				f["_finalmask"] = v
			}
		} else {
			f["_finalmask"] = v
		}
	}

	if network != "tcp" && network != "none" {
		if err := vlessTransport(f, q, network, httpUpgrade, shadowrocket); err != nil {
			return vlessResult{}, err
		}
	}

	if flavour == flavourVMess {
		f["type"] = "vmess"
		f["alterId"] = 0.0
		delete(f, "flow")
	}
	return vlessResult{fields: f, query: q, userInfo: shape.uuid}, nil
}

// vlessTransport builds <network>-opts and the transport annotations for a
// network that is neither tcp nor none.
func vlessTransport(f map[string]any, q query, network string, httpUpgrade, shadowrocket bool) error {
	opts := map[string]any{}
	host, hasHost := q.or("host", "obfsParam")
	obfsParam, hasObfsParam := q.str("obfsParam")
	headersFromJSON := false
	if hasObfsParam {
		if v, err := decodeJSON(obfsParam); err == nil {
			opts["headers"] = v
			headersFromJSON = true
		}
	}
	if !headersFromJSON && truthy(host, hasHost) {
		opts["headers"] = map[string]any{"Host": host}
	}
	if headers, ok := opts["headers"].(map[string]any); ok {
		switch network {
		case "xhttp":
			if h, ok := headers["Host"]; ok {
				opts["host"] = h
				delete(headers, "Host")
				if len(headers) == 0 {
					delete(opts, "headers")
				}
			}
		case "h2":
			var names any
			present := false
			if v, ok := headers["Host"]; ok {
				names, present = v, true
			} else if v, ok := headers["host"]; ok {
				names, present = v, true
			}
			if present {
				opts["host"] = splitTrimNonEmpty(jsString(names, true), ",")
				delete(headers, "Host")
				delete(headers, "host")
			}
		}
	}

	serviceName, hasServiceName := q.get("serviceName")
	path, hasPath := q.str("path")
	if hasServiceName {
		opts[network+"-service-name"] = serviceName
	} else if shadowrocket && hasPath && network != "ws" && network != "http" && network != "h2" {
		opts[network+"-service-name"] = path
		hasPath = false
	}
	if network == "grpc" {
		if v, present := q.get("authority"); present {
			opts["_grpc-authority"] = v
		}
	}

	if network == "ws" {
		var edDigits string
		haveED := false
		if hasPath {
			if p, digits, ok := earlyData(path); ok {
				path, edDigits, haveED = p, digits, true
			}
		}
		if !haveED {
			if v, present := q.str("ed"); present && v != "" {
				if _, safe := safeDigits(v); !safe {
					return errReject
				}
				edDigits, haveED = v, true
			}
		}
		eh, hasEH := q.str("eh")
		if haveED {
			if httpUpgrade {
				opts["v2ray-http-upgrade-fast-open"] = true
				opts["_v2ray-http-upgrade-ed"] = edDigits
			} else {
				opts["max-early-data"] = numberValue(edDigits)
				opts["early-data-header-name"] = "Sec-WebSocket-Protocol"
			}
		}
		if hasEH && eh != "" {
			opts["early-data-header-name"] = eh
		}
	}
	if hasPath {
		opts["path"] = path
	} else if network == "h2" {
		opts["path"] = "/"
	}
	if network == "http" {
		if v, present := q.get("method"); present {
			opts["method"] = v
		}
	}
	mode, hasMode := q.get("mode")
	switch network {
	case "grpc":
		if truthy(mode, hasMode) {
			opts["_grpc-type"] = mode
		} else {
			opts["_grpc-type"] = "gun"
		}
	case "xhttp":
		if hasMode {
			opts["mode"] = mode
		}
	}
	if network != "xhttp" && hasMode {
		f["_mode"] = mode
	}
	if httpUpgrade {
		opts["v2ray-http-upgrade"] = true
	}
	if network == "kcp" {
		if seed, _ := q.str("seed"); seed != "" {
			f["seed"] = seed
		}
		if ht, _ := q.str("headerType"); ht != "" {
			f["headerType"] = ht
		} else {
			f["headerType"] = "none"
		}
	}
	extra, hasExtra := q.str("extra")
	if network == "xhttp" {
		xhttpExtra(f, opts, extra, hasExtra)
	} else if hasExtra {
		f["_extra"] = extra
	}
	if len(opts) > 0 {
		f[network+"-opts"] = opts
	}
	return nil
}
