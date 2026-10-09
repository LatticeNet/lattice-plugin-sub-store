package parse

import (
	"strings"
)

// The Shadowsocks URI parser of parser.md section 4.4. Its type item and
// its Reality items are shared with the Trojan parser (4.9).

func parseSS(rest string, _ *lineState) (map[string]any, error) {
	body, rawFragment, hasFragment := strings.Cut(rest, "#")
	f := map[string]any{"type": "ss"}
	if hasFragment {
		name, ok := PercentDecodeStrict(rawFragment)
		if !ok {
			return nil, errReject
		}
		f["name"] = name
	}

	// search is the text the plugin, shadow-tls and gost items are looked
	// up in: the query, and for the legacy form the decoded text as well.
	var userInfo, hostPart, rawQuery, search string
	legacy := false
	if at := strings.IndexByte(body, '@'); at >= 0 {
		// SIP002: user info before the first "@", the host part up to the
		// first "/" or "?", the query from the first "?".
		ui, ok := ssUserInfo(body[:at])
		if !ok {
			return nil, errReject
		}
		userInfo = ui
		hostPart = body[at+1:]
		if i := strings.IndexAny(hostPart, "/?"); i >= 0 {
			hostPart = hostPart[:i]
		}
		if i := strings.IndexByte(body, '?'); i >= 0 {
			rawQuery = body[i+1:]
		}
		search = rawQuery
	} else {
		// Legacy: Base64 of cipher:password@host:port, cut from the query at
		// the last "?".
		legacy = true
		b64 := body
		if i := strings.LastIndexByte(body, '?'); i >= 0 {
			b64, rawQuery = body[:i], body[i+1:]
		}
		decoded := Base64DecodeLenient(b64)
		at := strings.LastIndexByte(decoded, '@')
		if at < 0 {
			return nil, errReject
		}
		userInfo = decoded[:at]
		hostPart = decoded[at+1:]
		if i := strings.IndexByte(hostPart, '/'); i >= 0 {
			hostPart = hostPart[:i]
		}
		search = decoded + "&" + rawQuery
	}
	cipher, password, ok := strings.Cut(userInfo, ":")
	if !ok {
		return nil, errReject
	}
	server, port, ok := ssHostPort(hostPart)
	if !ok {
		return nil, errReject
	}
	f["server"] = server
	f["port"] = port
	f["cipher"] = cipher
	f["password"] = password
	if !hasFragment {
		f["name"] = "SS " + server + ":" + jsNumber(port)
	}

	q, ok := splitQuery(rawQuery, q1)
	if !ok {
		return nil, errReject
	}
	if v, present := q.str("v2ray-plugin"); present && legacy {
		opts, err := decodeJSON(Base64DecodeLenient(v))
		if err != nil {
			return nil, errReject
		}
		f["plugin"] = "v2ray-plugin"
		f["plugin-opts"] = opts
	}
	if v, present := q.str("security"); present {
		switch v {
		case "none":
			f["tls"] = false
		case "":
			f["tls"] = ""
		default:
			f["tls"] = true
		}
	}
	allowInsecure, _ := q.str("allowInsecure")
	f["skip-cert-verify"] = allowInsecure != ""
	if v, present := q.or("sni", "peer"); present {
		f["sni"] = v
	}
	if v, present := q.get("fp"); present {
		f["client-fingerprint"] = v
	}
	if v, present := q.str("alpn"); present {
		d, ok := PercentDecodeStrict(v)
		if !ok {
			return nil, errReject
		}
		f["alpn"] = splitList(d, ",")
	}
	if v, _ := q.str("ws"); v != "" {
		f["network"] = "ws"
		opts := map[string]any{}
		if p, present := q.get("wspath"); present {
			opts["path"] = p
		}
		f["ws-opts"] = opts
	}
	if err := streamTransport(f, q, true); err != nil {
		return nil, err
	}
	switch udp, _ := q.str("udp"); strings.ToLower(udp) {
	case "false", "0", "off":
		f["udp"] = false
	default:
		f["udp"] = true
	}
	if err := ssPlugins(f, search); err != nil {
		return nil, err
	}
	if hasFlagItem(rawQuery, "uot=") {
		f["udp-over-tcp"] = true
	}
	if hasFlagItem(rawQuery, "tfo=") {
		f["tfo"] = true
	}
	return f, nil
}

// ssUserInfo reads SIP002 user info: with a ":" the two sides are
// strict-decoded separately; without one the whole text is strict-decoded
// and, when the result holds no ":", Lenient-Base64-decoded.
func ssUserInfo(raw string) (string, bool) {
	if a, b, found := strings.Cut(raw, ":"); found {
		da, ok := PercentDecodeStrict(a)
		if !ok {
			return "", false
		}
		db, ok := PercentDecodeStrict(b)
		if !ok {
			return "", false
		}
		return da + ":" + db, true
	}
	d, ok := PercentDecodeStrict(raw)
	if !ok {
		return "", false
	}
	if strings.Contains(d, ":") {
		return d, true
	}
	return Base64DecodeLenient(d), true
}

// ssHostPort splits a host part at its last ":": the server is the text
// before it (brackets kept), the port the first run of digits after it.
func ssHostPort(hostPart string) (string, float64, bool) {
	colon := strings.LastIndexByte(hostPart, ':')
	if colon < 0 {
		return "", 0, false
	}
	after := hostPart[colon+1:]
	end := 0
	for end < len(after) && after[end] >= '0' && after[end] <= '9' {
		end++
	}
	if end == 0 {
		return "", 0, false
	}
	return hostPart[:colon], numberValue(after[:end]), true
}

// hasFlagItem is the prefix rule of the uot and tfo items: the query holds
// key (in any letter case) followed by "1" or "true" in any letter case, so
// "uot=10" counts.
func hasFlagItem(rawQuery, key string) bool {
	lower := strings.ToLower(rawQuery)
	for i := strings.Index(lower, key); i >= 0; {
		v := lower[i+len(key):]
		if strings.HasPrefix(v, "1") || strings.HasPrefix(v, "true") {
			return true
		}
		next := strings.Index(lower[i+1:], key)
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return false
}

// rawItem finds key=value in text, where the key starts the text or follows
// "?" or "&", and returns the raw value up to the next "&".
func rawItem(text, key string) (string, bool) {
	needle := key + "="
	for i := strings.Index(text, needle); i >= 0; {
		if i == 0 || text[i-1] == '?' || text[i-1] == '&' {
			v := text[i+len(needle):]
			if amp := strings.IndexByte(v, '&'); amp >= 0 {
				v = v[:amp]
			}
			return v, true
		}
		next := strings.Index(text[i+1:], needle)
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return "", false
}

// textOrNumber reports a value a non-blank rule accepts: a text with a
// character that is not white space, or a number. An option given without a
// value reads as the boolean true and is not accepted.
func textOrNumber(v any) bool {
	switch x := v.(type) {
	case string:
		return nonBlank(x)
	case float64:
		return true
	}
	return false
}

// ssPlugins applies the plugin, shadow-tls and gost items of parser.md 4.4,
// looked up in search.
func ssPlugins(f map[string]any, search string) error {
	if raw, ok := rawItem(search, "plugin"); ok {
		value, ok := PercentDecodeStrict(raw)
		if !ok {
			return errReject
		}
		if err := ssPluginValue(f, value); err != nil {
			return err
		}
	}
	if raw, ok := rawItem(search, "shadow-tls"); ok {
		v, err := decodeJSON(Base64DecodeLenient(raw))
		if err != nil {
			return errReject
		}
		params, _ := v.(map[string]any)
		opts := map[string]any{}
		if h := params["host"]; textOrNumber(h) {
			opts["host"] = h
		}
		if p := params["password"]; textOrNumber(p) {
			opts["password"] = p
		}
		if ver := params["version"]; textOrNumber(ver) {
			opts["version"] = LeadingInteger(jsString(ver, true))
		}
		f["plugin"] = "shadow-tls"
		f["plugin-opts"] = opts
		ssEndpointOverride(f, params)
	}
	if raw, ok := rawItem(search, "gost"); ok {
		d, ok := PercentDecodeStrict(raw)
		if !ok {
			return errReject
		}
		v, err := decodeJSON(Base64DecodeLenient(d))
		if err != nil {
			return errReject
		}
		params, _ := v.(map[string]any)
		opts := map[string]any{}
		route, hasRoute := params["route"]
		r := strings.ToLower(TrimECMAScript(jsString(route, hasRoute)))
		switch {
		case r == "ws" || r == "wss" || r == "websocket":
			opts["mode"] = "websocket"
		case hasRoute:
			opts["mode"] = route
		}
		if h := params["host"]; textOrNumber(h) {
			opts["host"] = h
		}
		if p := params["path"]; textOrNumber(p) {
			opts["path"] = p
		}
		if r == "wss" {
			opts["tls"] = true
		}
		f["plugin"] = "gost-plugin"
		f["plugin-opts"] = opts
		ssEndpointOverride(f, params)
	}
	return nil
}

// ssEndpointOverride applies a non-blank address and port from a
// shadow-tls or gost object.
func ssEndpointOverride(f, params map[string]any) {
	if a := params["address"]; textOrNumber(a) {
		f["server"] = a
	}
	if p := params["port"]; textOrNumber(p) {
		f["port"] = LeadingInteger(jsString(p, true))
	}
}

// ssPluginValue reads a decoded plugin value: the plugin name, then
// key=value items or bare words separated by ";".
func ssPluginValue(f map[string]any, value string) error {
	items := strings.Split(value, ";")
	opts := map[string]any{}
	for _, item := range items[1:] {
		k, v, hasEq := strings.Cut(item, "=")
		if !hasEq || v == "" {
			opts[k] = true
			continue
		}
		opts[k] = strings.ReplaceAll(v, `\=`, "=")
	}
	first := func(keys ...string) (any, bool) {
		for _, k := range keys {
			if v, ok := opts[k]; ok && textOrNumber(v) {
				return v, true
			}
		}
		return nil, false
	}
	po := map[string]any{}
	switch items[0] {
	case "obfs-local", "simple-obfs":
		f["plugin"] = "obfs"
		if v, ok := opts["obfs"]; ok {
			po["mode"] = v
		}
		if v, ok := first("obfs-host"); ok {
			po["host"] = v
		}
	case "v2ray-plugin":
		f["plugin"] = "v2ray-plugin"
		po["mode"] = "websocket"
		if v, ok := first("obfs", "mode"); ok {
			po["mode"] = v
		}
		if v, ok := first("obfs-host", "host"); ok {
			po["host"] = v
		}
		if v, ok := first("path"); ok {
			po["path"] = v
		}
		for _, k := range []string{"tls", "sni"} {
			if v, ok := opts[k]; ok {
				po[k] = v
			}
		}
		switch opts["skip-cert-verify"] {
		case true, "1", "true":
			po["skip-cert-verify"] = true
		default:
			po["skip-cert-verify"] = false
		}
		if v, ok := opts["mux"].(string); ok && digitsOnly(v) {
			po["mux"] = numberValue(v)
		}
	case "shadow-tls":
		f["plugin"] = "shadow-tls"
		if v, ok := first("host"); ok {
			po["host"] = v
		}
		if v, ok := first("password"); ok {
			po["password"] = v
		}
		if v, ok := opts["version"]; ok {
			po["version"] = LeadingInteger(jsString(v, true))
		}
	default:
		return errReject
	}
	f["plugin-opts"] = po
	return nil
}

// streamTransport applies the type item of parser.md 4.4 and the Reality
// items that need it. Trojan (4.9) shares it with decodeHost false: only
// Shadowsocks decodes the host a second time.
func streamTransport(f map[string]any, q query, decodeHost bool) error {
	typeValue, hasType := q.get("type")
	if !hasType {
		return nil
	}
	network := jsString(typeValue, true)
	httpUpgrade := false
	if network == "httpupgrade" {
		network, httpUpgrade = "ws", true
	}
	f["network"] = network
	if network == "grpc" {
		opts := map[string]any{}
		if v, ok := q.get("serviceName"); ok {
			opts["grpc-service-name"] = v
		}
		if v, ok := q.get("mode"); ok {
			opts["_grpc-type"] = v
		}
		if v, ok := q.get("authority"); ok {
			opts["_grpc-authority"] = v
		}
		f["grpc-opts"] = opts
	} else {
		opts, _ := f[network+"-opts"].(map[string]any)
		if opts == nil {
			opts = map[string]any{}
		}
		edDigits, haveED := "", false
		if v, ok := q.get("path"); ok {
			if s, isText := v.(string); isText && network == "ws" {
				if p, digits, ok := earlyData(s); ok {
					v, edDigits, haveED = p, digits, true
				}
			}
			opts["path"] = v
		}
		if v, ok := q.get("host"); ok {
			if decodeHost {
				d, ok := PercentDecodeStrict(jsString(v, true))
				if !ok {
					return errReject
				}
				v = d
			}
			opts["headers"] = map[string]any{"Host": v}
		}
		if httpUpgrade {
			opts["v2ray-http-upgrade"] = true
			if !haveED {
				if v, ok := q.str("ed"); ok && digitsOnly(v) {
					edDigits, haveED = v, true
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
		if len(opts) > 0 {
			f[network+"-opts"] = opts
		}
	}
	if security, _ := q.str("security"); security == "reality" {
		ro := map[string]any{}
		for _, k := range [][2]string{{"pbk", "public-key"}, {"sid", "short-id"}, {"spx", "_spider-x"}} {
			if v, ok := q.get(k[0]); ok {
				ro[k[1]] = v
			}
		}
		if len(ro) > 0 {
			f["reality-opts"] = ro
		}
		if v, ok := q.get("mode"); ok {
			f["_mode"] = v
		}
		if v, ok := q.get("extra"); ok {
			f["_extra"] = v
		}
	}
	return nil
}
