package parse

import (
	"strings"
)

// The VMess URI parser of parser.md section 4.6. A link takes one of four
// forms, decided in order: the VLESS shape, the Quantumult form, the v2rayN
// JSON form and the Shadowrocket form.

func parseVMess(rest string, _ *lineState) (map[string]any, error) {
	if i := strings.IndexAny(rest, "@/?#"); i >= 0 && rest[i] == '@' {
		r, err := parseVLESSBody(rest, flavourVMess)
		if err != nil {
			return nil, err
		}
		return r.fields, nil
	}
	body, rawFragment, hasFragment := strings.Cut(rest, "#")
	fragment := ""
	if hasFragment {
		var ok bool
		if fragment, ok = PercentDecodeStrict(rawFragment); !ok {
			return nil, errReject
		}
	}
	b64, _, _ := strings.Cut(body, "?")
	decoded := Base64DecodeLenient(b64)
	if quantumultVMess(decoded) {
		return vmessQuantumult(decoded, fragment)
	}
	if v, err := decodeJSON(decoded); err == nil {
		if v == nil {
			// Reading a property of null throws.
			return nil, errReject
		}
		params, _ := v.(map[string]any)
		return vmessFields(params, fragment)
	}
	return vmessShadowrocket(body, fragment)
}

// quantumultVMess reports the Quantumult form: "=" followed by optional
// white space and "vmess".
func quantumultVMess(s string) bool {
	for i := strings.IndexByte(s, '='); i >= 0; {
		rest := strings.TrimLeftFunc(s[i+1:], isESWhiteSpace)
		if strings.HasPrefix(rest, "vmess") {
			return true
		}
		next := strings.IndexByte(s[i+1:], '=')
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return false
}

// vmessQuantumult parses name = vmess, server, port, method, "uuid",
// key=value, ... Any obfs rejects the line, so the node never has TLS or a
// transport.
func vmessQuantumult(decoded, fragment string) (map[string]any, error) {
	parts := strings.Split(decoded, ",")
	for i := range parts {
		parts[i] = TrimECMAScript(parts[i])
	}
	if len(parts) < 5 {
		return nil, errReject
	}
	uuid := parts[4]
	if len(uuid) < 2 || uuid[0] != '"' || uuid[len(uuid)-1] != '"' {
		return nil, errReject
	}
	name, _, _ := strings.Cut(parts[0], "=")
	f := map[string]any{
		"type":   "vmess",
		"name":   TrimECMAScript(name),
		"server": parts[1],
		"port":   parts[2],
		"cipher": vmessCipher(parts[3]),
		"uuid":   uuid[1 : len(uuid)-1],
		"tls":    false,
	}
	for _, p := range parts[5:] {
		key, value, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		value, _, _ = strings.Cut(value, "=")
		key, value = TrimECMAScript(key), TrimECMAScript(value)
		switch key {
		case "obfs":
			return nil, errReject
		case "udp-relay":
			f["udp"] = value
		case "fast-open":
			f["tfo"] = value
		case "tls-verification":
			f["skip-cert-verify"] = value == ""
		}
	}
	if nonBlank(fragment) {
		f["name"] = fragment
	}
	return f, nil
}

// vmessShadowrocket parses base64[/]?query, where the Base64 part decodes to
// cipher:uuid@server:port. The query (variant Q4) feeds the v2rayN key table,
// with scy, id, port and add taken from the decoded text.
func vmessShadowrocket(body, fragment string) (map[string]any, error) {
	b64, rawQuery, hasQuery := strings.Cut(body, "?")
	if !hasQuery {
		return nil, errReject
	}
	decoded := Base64DecodeLenient(strings.TrimSuffix(b64, "/"))
	if strings.ContainsRune(decoded, '\n') || hasLineTerminator(decoded) {
		return nil, errReject
	}
	cipher, rest, ok := strings.Cut(decoded, ":")
	if !ok || cipher == "" {
		return nil, errReject
	}
	run := rest
	if c := strings.IndexByte(run, ':'); c >= 0 {
		run = run[:c]
	}
	at := strings.LastIndexByte(run, '@')
	if at <= 0 {
		return nil, errReject
	}
	uuid := run[:at]
	hostPort := rest[at+1:]
	colon := strings.LastIndexByte(hostPort, ':')
	if colon < 0 || !digitsOnly(hostPort[colon+1:]) {
		return nil, errReject
	}
	q, ok := splitQuery(rawQuery, q4)
	if !ok {
		return nil, errReject
	}
	params := map[string]any{}
	for _, it := range q {
		params[it.Key] = it.Value
	}
	params["scy"] = cipher
	params["id"] = uuid
	params["port"] = hostPort[colon+1:]
	params["add"] = hostPort[:colon]
	if _, isList := params["alpn"].([]any); isList {
		return nil, errReject
	}
	return vmessFields(params, fragment)
}

// vmessFields applies the v2rayN key table to params (a JSON object, or the
// Shadowrocket query). A nil map stands for a JSON value that is not an
// object, whose properties all read as undefined.
func vmessFields(p map[string]any, fragment string) (map[string]any, error) {
	get := func(k string) (any, bool) {
		v, ok := p[k]
		return v, ok
	}
	f := map[string]any{"type": "vmess"}
	server, hasServer := get("add")
	if hasServer {
		f["server"] = server
	}
	portValue, hasPort := get("port")
	port := LeadingInteger(jsString(portValue, hasPort))
	f["port"] = port

	name, hasName := any(nil), false
	for _, k := range []string{"ps", "remarks", "remark"} {
		if v, ok := get(k); ok && v != nil {
			name, hasName = v, true
			break
		}
	}
	if !hasName {
		name = "VMess " + jsString(server, hasServer) + ":" + jsNumber(port)
	}
	f["name"] = name

	if v, ok := get("scy"); ok {
		f["cipher"] = vmessCipher(jsString(v, true))
	} else {
		f["cipher"] = "auto"
	}
	if v, ok := get("id"); ok {
		f["uuid"] = v
	}
	alterID := any(0.0)
	if v, ok := get("aid"); truthy(v, ok) {
		alterID = v
	} else if v, ok := get("alterId"); truthy(v, ok) {
		alterID = v
	}
	f["alterId"] = LeadingInteger(jsString(alterID, true))

	tls := false
	switch v, _ := get("tls"); x := v.(type) {
	case string:
		tls = x == "tls" || x == "1"
	case bool:
		tls = x
	case float64:
		tls = x == 1
	}
	f["tls"] = tls
	if v, ok := get("verify_cert"); ok {
		f["skip-cert-verify"] = !truthy(v, true)
	}
	if v, ok := get("allowInsecure"); ok && f["skip-cert-verify"] != true {
		f["skip-cert-verify"] = ContainsTrueOr1(jsString(v, true))
	}
	if tls {
		if v, ok := get("sni"); truthy(v, ok) {
			f["sni"] = v
		} else if v, ok := get("peer"); truthy(v, ok) {
			f["sni"] = v
		}
	}
	if v, ok := get("fp"); ok {
		f["client-fingerprint"] = v
	}
	if v, ok := get("alpn"); truthy(v, ok) {
		s, isString := v.(string)
		if !isString {
			return nil, errReject // .split of a non-string throws
		}
		f["alpn"] = splitList(s, ",")
	}

	netValue, _ := get("net")
	net, _ := netValue.(string)
	obfsValue, _ := get("obfs")
	obfs, _ := obfsValue.(string)
	typeValue, hasType := get("type")
	typ, _ := typeValue.(string)
	network, httpUpgrade := "", false
	switch {
	case net == "ws" || obfs == "websocket":
		network = "ws"
	case obfs == "http" || typ == "http":
		network = "http"
	case net == "http":
		network = "h2"
	case net == "grpc" || net == "kcp" || net == "quic":
		network = net
	case net == "httpupgrade":
		network, httpUpgrade = "ws", true
	case net == "h2":
		network = "h2"
	}
	if network != "" {
		f["network"] = network
		if err := vmessTransport(f, p, network, httpUpgrade, typeValue, hasType); err != nil {
			return nil, err
		}
	}
	if nonBlank(fragment) {
		f["name"] = fragment
	}
	return f, nil
}

// vmessTransport builds the transport options of a v2rayN node.
func vmessTransport(f, p map[string]any, network string, httpUpgrade bool, typeValue any, hasType bool) error {
	host, hasHost := p["host"]
	if !truthy(host, hasHost) {
		host, hasHost = p["obfsParam"]
	}
	if s, ok := host.(string); ok {
		if v, err := decodeJSON(s); err == nil {
			if m, isObject := v.(map[string]any); isObject {
				if h, ok := m["Host"]; truthy(h, ok) {
					host, hasHost = h, true
				}
			}
		}
	}
	path, hasPath := p["path"]
	edDigits, haveED := "", false
	switch network {
	case "ws":
		if s, ok := path.(string); ok {
			if np, digits, ok := earlyData(s); ok {
				path, edDigits, haveED = np, digits, true
			}
		}
		if !truthy(path, hasPath) {
			path, hasPath = "/", true
		}
	case "http":
		if s, ok := host.(string); ok && s != "" {
			first, _, _ := strings.Cut(s, ",")
			host = TrimECMAScript(first)
		}
		if !truthy(path, hasPath) {
			path, hasPath = "/", true
		}
	case "h2":
		if !truthy(path, hasPath) {
			path, hasPath = "/", true
		}
	}
	if !truthy(path, hasPath) && !truthy(host, hasHost) && network != "kcp" && network != "quic" {
		delete(f, "network")
		return nil
	}
	pathText := jsString(path, hasPath)
	hostText := jsString(host, hasHost)
	typeText := jsString(typeValue, hasType)
	switch network {
	case "grpc":
		opts := map[string]any{}
		if hasPath && nonBlank(pathText) {
			opts["grpc-service-name"] = path
		}
		if hasType && nonBlank(typeText) {
			opts["_grpc-type"] = typeValue
		}
		if v, ok := p["authority"]; ok && nonBlank(jsString(v, true)) {
			opts["_grpc-authority"] = v
		}
		f["grpc-opts"] = opts
	case "kcp", "quic":
		opts := map[string]any{}
		if hasType && nonBlank(typeText) {
			opts["_"+network+"-type"] = typeValue
		}
		if hasHost && nonBlank(hostText) {
			opts["_"+network+"-host"] = host
		}
		if hasPath && nonBlank(pathText) {
			opts["_"+network+"-path"] = path
		}
		f[network+"-opts"] = opts
	default: // ws, http, h2
		opts := map[string]any{}
		if hasPath && nonBlank(pathText) {
			opts["path"] = path
		}
		if network == "h2" {
			if truthy(host, hasHost) {
				opts["host"] = splitTrimNonEmpty(hostText, ",")
			}
		} else {
			headers := map[string]any{}
			if truthy(host, hasHost) {
				headers["Host"] = host
			}
			opts["headers"] = headers
		}
		if httpUpgrade {
			opts["v2ray-http-upgrade"] = true
			if !haveED {
				if v, ok := p["ed"]; ok {
					if digits := jsString(v, true); digitsOnly(digits) {
						edDigits, haveED = digits, true
					}
				}
			}
			if haveED {
				opts["v2ray-http-upgrade-fast-open"] = true
				opts["_v2ray-http-upgrade-ed"] = edDigits
			}
		} else if haveED {
			opts["max-early-data"] = numberValue(edDigits)
			opts["early-data-header-name"] = "Sec-WebSocket-Protocol"
		}
		f[network+"-opts"] = opts
	}
	return nil
}
