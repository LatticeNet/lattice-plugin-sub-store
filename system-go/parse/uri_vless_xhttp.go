package parse

import (
	"math"
	"strings"
)

// VLESS xhttp (parser.md section 4.8): the extra item's JSON becomes
// xhttp-opts fields and xhttp-opts.download-settings, and whatever mihomo
// does not model is kept in _extra_unsupported so a later URI export can
// reproduce it.

// xhttpExtra applies the extra item to a VLESS node on xhttp. An empty or
// absent item counts as {}; text that is not JSON is kept in _extra and
// processing continues with {}.
func xhttpExtra(f, opts map[string]any, raw string, present bool) {
	if !present || raw == "" {
		return
	}
	v, err := decodeJSON(raw)
	if err != nil {
		f["_extra"] = raw
		return
	}
	extra, ok := v.(map[string]any)
	if !ok {
		return
	}
	unsupported := map[string]any{}
	for k, e := range extra {
		if k == "downloadSettings" {
			continue
		}
		if modelled, partial := modelExtraFieldRest(opts, extra, k, e); !modelled {
			if partial != nil {
				unsupported[k] = partial
			} else {
				unsupported[k] = e
			}
		}
	}
	if ds, ok := extra["downloadSettings"]; ok {
		target, kept := downloadSettings(ds)
		if len(target) > 0 {
			opts["download-settings"] = target
		}
		if kept != nil {
			unsupported["downloadSettings"] = kept
		}
	}
	if c, ok := compactKept(unsupported); ok {
		f["_extra_unsupported"] = c
	}
}

// modelExtraFieldRest models one key of an extra object into target and
// reports whether the key was modelled. A key that is not modelled (no
// mapping, or a value failing its condition) belongs in _extra_unsupported
// with its value, except headers and xmux, whose modelled parts are taken
// and whose remainder (the non-string headers, the unknown or failing xmux
// keys) comes back as rest.
func modelExtraFieldRest(target, extra map[string]any, key string, v any) (modelled bool, rest any) {
	switch key {
	case "headers":
		h, ok := v.(map[string]any)
		if !ok {
			return false, nil
		}
		restMap := map[string]any{}
		for name, value := range h {
			s, isString := value.(string)
			if !isString {
				restMap[name] = value
				continue
			}
			if strings.EqualFold(name, "host") && targetHasHost(target) {
				continue
			}
			headers, _ := target["headers"].(map[string]any)
			if headers == nil {
				headers = map[string]any{}
				target["headers"] = headers
			}
			headers[name] = s
		}
		if len(restMap) > 0 {
			return false, restMap
		}
		return true, nil
	case "noGRPCHeader":
		return setIf(target, "no-grpc-header", true, v == true)
	case "xPaddingBytes":
		r, ok := rangeValue(v, rangeStrictPositiveText)
		return setIf(target, "x-padding-bytes", r, ok)
	case "xPaddingObfsMode":
		return setIf(target, "x-padding-obfs-mode", true, v == true)
	case "xPaddingKey", "xPaddingHeader", "xPaddingPlacement", "xPaddingMethod":
		return setIf(target, kebab(key), v, nonBlankString(v))
	case "uplinkHTTPMethod":
		return setIf(target, "uplink-http-method", v, nonBlankString(v))
	case "sessionIDPlacement", "sessionPlacement":
		return eitherKey(target, extra, key, "sessionIDPlacement", "sessionPlacement", "session-placement")
	case "sessionIDKey", "sessionKey":
		return eitherKey(target, extra, key, "sessionIDKey", "sessionKey", "session-key")
	case "sessionIDTable":
		_, isString := v.(string)
		return setIf(target, "session-table", v, isString)
	case "sessionIDLength":
		r, ok := rangeValue(v, rangeStrictPositiveText)
		return setIf(target, "session-length", r, ok)
	case "seqPlacement", "seqKey", "uplinkDataPlacement", "uplinkDataKey":
		return setIf(target, kebab(key), v, nonBlankString(v))
	case "uplinkChunkSize":
		r, ok := rangeValue(v, rangeNonNegative)
		return setIf(target, "uplink-chunk-size", r, ok)
	case "scMaxEachPostBytes":
		r, ok := rangeValue(v, rangeStrictPositiveValue)
		return setIf(target, "sc-max-each-post-bytes", r, ok)
	case "scMinPostsIntervalMs":
		r, ok := rangeValue(v, rangePositive)
		return setIf(target, "sc-min-posts-interval-ms", r, ok)
	case "xmux":
		x, ok := v.(map[string]any)
		if !ok {
			return false, nil
		}
		reuse := map[string]any{}
		restMap := map[string]any{}
		for k, e := range x {
			switch k {
			case "maxConnections", "maxConcurrency", "cMaxReuseTimes", "hMaxRequestTimes", "hMaxReusableSecs":
				if r, ok := rangeText(e, rangeNonNegative); ok {
					reuse[kebab(k)] = r
					continue
				}
			case "hKeepAlivePeriod":
				if n, ok := integerValue(e, false); ok {
					reuse["h-keep-alive-period"] = n
					continue
				}
			}
			restMap[k] = e
		}
		if len(reuse) > 0 {
			target["reuse-settings"] = reuse
		}
		if len(restMap) > 0 {
			return false, restMap
		}
		return true, nil
	}
	return false, nil
}

// targetHasHost reports a target that already names a host, as an option or
// a header in either spelling.
func targetHasHost(target map[string]any) bool {
	if _, ok := target["host"]; ok {
		return true
	}
	headers, _ := target["headers"].(map[string]any)
	_, upper := headers["Host"]
	_, lower := headers["host"]
	return upper || lower
}

// eitherKey models the first of two spellings whose value is a non-blank
// string. The spelling that supplied the value, and a second spelling that
// is itself a non-blank string, count as modelled.
func eitherKey(target, extra map[string]any, key, primary, secondary, field string) (bool, any) {
	pv, pok := extra[primary]
	sv, sok := extra[secondary]
	switch {
	case pok && nonBlankString(pv):
		target[field] = pv
	case sok && nonBlankString(sv):
		target[field] = sv
	}
	if key == primary {
		return nonBlankString(pv), nil
	}
	return nonBlankString(sv), nil
}

func setIf(target map[string]any, field string, v any, ok bool) (bool, any) {
	if ok {
		target[field] = v
	}
	return ok, nil
}

func nonBlankString(v any) bool {
	s, ok := v.(string)
	return ok && nonBlank(s)
}

// kebab turns a camelCase extra key into the model's kebab-case field.
func kebab(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteByte(c + 'a' - 'A')
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// downloadSettings models extra.downloadSettings. It returns the model
// fields and the unmodelled remainder (nil when nothing remains); a value
// that is not a plain object is kept as given.
func downloadSettings(v any) (map[string]any, any) {
	ds, ok := v.(map[string]any)
	if !ok {
		return nil, v
	}
	target := map[string]any{}
	kept := map[string]any{}
	keep := func(k string, e any) { kept[k] = e }

	for k, e := range ds {
		switch k {
		case "network":
			s, isString := e.(string)
			if l := strings.ToLower(s); isString && (l == "xhttp" || l == "splithttp") {
				target["network"] = "xhttp"
				continue
			}
			keep(k, e)
		case "address":
			if nonBlankString(e) {
				target["server"] = e
				continue
			}
			keep(k, e)
		case "port":
			if n, ok := integerValue(e, true); ok {
				target["port"] = n
				continue
			}
			keep(k, e)
		case "security":
			s, _ := e.(string)
			switch strings.ToLower(s) {
			case "tls":
				target["tls"] = true
				continue
			case "reality":
				target["tls"] = true
				if _, ok := target["reality-opts"]; !ok {
					target["reality-opts"] = map[string]any{}
				}
				continue
			}
			keep(k, e)
		case "tlsSettings", "realitySettings", "xhttpSettings":
			// Handled below, in a fixed order, so the Reality names override
			// the TLS ones whatever order the object came in.
		default:
			keep(k, e)
		}
	}
	if e, ok := ds["tlsSettings"]; ok {
		if rest := tlsSettings(target, e); rest != nil {
			keep("tlsSettings", rest)
		}
	}
	if e, ok := ds["realitySettings"]; ok {
		if rest := realitySettings(target, e); rest != nil {
			keep("realitySettings", rest)
		}
	}
	if e, ok := ds["xhttpSettings"]; ok {
		if rest := xhttpSettings(target, e); rest != nil {
			keep("xhttpSettings", rest)
		}
	}
	if len(kept) == 0 {
		return target, nil
	}
	return target, kept
}

func tlsSettings(target map[string]any, v any) any {
	ts, ok := v.(map[string]any)
	if !ok {
		return v
	}
	rest := map[string]any{}
	echList, hasList := ts["echConfigList"]
	echText, _ := echList.(string)
	ech, echOK := map[string]any(nil), false
	if hasList {
		ech, echOK = echOpts(echText)
	}
	for k, e := range ts {
		switch k {
		case "serverName":
			if nonBlankString(e) {
				target["servername"] = e
				continue
			}
		case "fingerprint":
			if nonBlankString(e) {
				target["client-fingerprint"] = e
				continue
			}
		case "alpn":
			if l, ok := nonEmptyStringList(e); ok {
				target["alpn"] = l
				continue
			}
		case "allowInsecure":
			if e == true {
				target["skip-cert-verify"] = true
				continue
			}
		case "echConfigList":
			if echOK {
				continue
			}
		case "echForceQuery":
			if s, _ := e.(string); echOK && (s == "none" || s == "half" || s == "full") {
				ech["_force-query"] = s
				continue
			}
		case "echSockopt":
			if m, isObject := e.(map[string]any); echOK && isObject {
				ech["_sockopt"] = m
				continue
			}
		}
		rest[k] = e
	}
	if echOK {
		target["ech-opts"] = ech
	}
	if len(rest) == 0 {
		return nil
	}
	return rest
}

func realitySettings(target map[string]any, v any) any {
	rs, ok := v.(map[string]any)
	if !ok {
		return v
	}
	rest := map[string]any{}
	reality := func() map[string]any {
		ro, _ := target["reality-opts"].(map[string]any)
		if ro == nil {
			ro = map[string]any{}
			target["reality-opts"] = ro
		}
		return ro
	}
	for k, e := range rs {
		if nonBlankString(e) {
			switch k {
			case "publicKey":
				reality()["public-key"] = e
				continue
			case "shortId":
				reality()["short-id"] = e
				continue
			case "serverName":
				target["servername"] = e
				continue
			case "fingerprint":
				target["client-fingerprint"] = e
				continue
			}
		}
		rest[k] = e
	}
	if len(rest) == 0 {
		return nil
	}
	return rest
}

func xhttpSettings(target map[string]any, v any) any {
	xs, ok := v.(map[string]any)
	if !ok {
		return v
	}
	rest := map[string]any{}
	for _, k := range []string{"path", "host", "mode"} {
		if e, present := xs[k]; present {
			if nonBlankString(e) {
				target[k] = e
			} else {
				rest[k] = e
			}
		}
	}
	for k, e := range xs {
		switch k {
		case "path", "host", "mode", "extra":
			continue
		}
		if modelled, partial := modelExtraFieldRest(target, xs, k, e); !modelled {
			if partial != nil {
				rest[k] = partial
			} else {
				rest[k] = e
			}
		}
	}
	if e, present := xs["extra"]; present {
		inner, isObject := e.(map[string]any)
		if !isObject {
			rest["extra"] = e
		} else {
			innerRest := map[string]any{}
			for k, ie := range inner {
				if modelled, partial := modelExtraFieldRest(target, inner, k, ie); !modelled {
					if partial != nil {
						innerRest[k] = partial
					} else {
						innerRest[k] = ie
					}
				}
			}
			if len(innerRest) > 0 {
				rest["extra"] = innerRest
			}
		}
	}
	if len(rest) == 0 {
		return nil
	}
	return rest
}

func nonEmptyStringList(v any) ([]any, bool) {
	l, ok := v.([]any)
	if !ok || len(l) == 0 {
		return nil, false
	}
	for _, e := range l {
		if s, isString := e.(string); !isString || s == "" {
			return nil, false
		}
	}
	return l, true
}

// compactKept copies a kept value with objects that end up with no keys
// dropped and list elements that are dropped objects removed; ok is false
// when the value itself is dropped.
func compactKept(v any) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if c, ok := compactKept(e); ok {
				out[k] = c
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			if c, ok := compactKept(e); ok {
				out = append(out, c)
			}
		}
		return out, true
	}
	return v, true
}

// rangeKind is one row of the range table of parser.md 4.1.
type rangeKind struct {
	minLower, minUpper, minSingle float64
	alwaysText                    bool
}

var (
	rangeNonNegative         = rangeKind{0, 0, 0, false}
	rangePositive            = rangeKind{0, 1, 1, false}
	rangeStrictPositiveText  = rangeKind{1, 1, 1, true}
	rangeStrictPositiveValue = rangeKind{1, 1, 1, false}
)

// rangeBounds reads a range value: a number or a string that, trimmed and
// split on "-", is one or two parts, each an optional "+" and digits after
// trimming and a safe integer, with the lower part not above the upper.
func rangeBounds(v any, kind rangeKind) (lower, upper float64, ok bool) {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case float64:
		s = jsNumber(x)
	default:
		return 0, 0, false
	}
	parts := strings.Split(TrimECMAScript(s), "-")
	if len(parts) > 2 {
		return 0, 0, false
	}
	vals := make([]float64, len(parts))
	for i, p := range parts {
		p = strings.TrimPrefix(TrimECMAScript(p), "+")
		n, safe := safeDigits(p)
		if !safe {
			return 0, 0, false
		}
		vals[i] = n
	}
	if len(vals) == 1 {
		return vals[0], vals[0], vals[0] >= kind.minSingle
	}
	lower, upper = vals[0], vals[1]
	return lower, upper, lower <= upper && lower >= kind.minLower && upper >= kind.minUpper
}

// rangeValue is a range in its kind's output form: a number when the bounds
// are equal (unless the kind is always text), else "lower-upper".
func rangeValue(v any, kind rangeKind) (any, bool) {
	lower, upper, ok := rangeBounds(v, kind)
	if !ok {
		return nil, false
	}
	if lower == upper && !kind.alwaysText {
		return lower, true
	}
	return rangeString(lower, upper), true
}

// rangeText is a range always written as text (the xmux fields).
func rangeText(v any, kind rangeKind) (any, bool) {
	lower, upper, ok := rangeBounds(v, kind)
	if !ok {
		return nil, false
	}
	return rangeString(lower, upper), true
}

func rangeString(lower, upper float64) string {
	if lower == upper {
		return jsNumber(lower)
	}
	return jsNumber(lower) + "-" + jsNumber(upper)
}

// integerValue reads the Integer of parser.md 4.1: a JSON number that is a
// safe integer, or a string that after trimming is an optional sign and
// digits within the safe range. nonNegative refuses a minus sign and
// negative numbers.
func integerValue(v any, nonNegative bool) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > maxSafeInteger || (nonNegative && x < 0) {
			return 0, false
		}
		return x, true
	case string:
		s := TrimECMAScript(x)
		neg := false
		switch {
		case strings.HasPrefix(s, "-"):
			if nonNegative {
				return 0, false
			}
			neg, s = true, s[1:]
		case strings.HasPrefix(s, "+"):
			s = s[1:]
		}
		n, safe := safeDigits(s)
		if !safe {
			return 0, false
		}
		if neg {
			n = -n
		}
		return n, true
	}
	return 0, false
}
