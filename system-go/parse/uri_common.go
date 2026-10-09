package parse

import (
	"errors"
	"strings"
)

// The shared building blocks of parser.md section 4.1.

// errReject is the error a line parser returns for a line it does not
// accept. The document skips the line; parser selection tries the next
// parser.
var errReject = errors.New("parse: line rejected")

// queryVariant names one row of the query-splitting table of parser.md 4.1.
type queryVariant int

const (
	// q1 (Shadowsocks, Hysteria2): the value ends at a second "=", a bare
	// key reads as the text "undefined", strict decoding.
	q1 queryVariant = iota + 1
	// q2 (VLESS, and VMess and AnyTLS links that reuse it): the value is
	// everything after the first "=", a bare key reads as "", strict.
	q2
	// q3 (Trojan): as q2, but a bare key reads as the boolean true and
	// values are decoded leniently.
	q3
	// q4 (VMess Shadowrocket form): as q1, and a decoded value holding a
	// comma becomes a list split on commas.
	q4
	// q5All (AnyTLS, TUIC): as q1, then every "_" in the key becomes "-".
	q5All
	// q5First (Hysteria v1): as q1, then the first "_" in the key becomes
	// "-".
	q5First
	// q6 (WireGuard): the value is everything after the first "=", a bare
	// key reads as "", strict, and the first "_" in the key becomes "-".
	q6
)

// queryItem is one item of a split query. Value is a string, the boolean
// true (q3 bare key) or a list (q4).
type queryItem struct {
	Key   string
	Value any
}

// query is a split query in item order. Lookups take the last item with the
// key, the "last wins" rule every variant shares for keys a parser handles.
type query []queryItem

// splitQuery splits raw (the text after "?") on "&", skips empty items, cuts
// each item as the variant says and decodes its value. Keys are never
// percent-decoded. ok is false when a strict decode fails, which rejects
// the line.
func splitQuery(raw string, v queryVariant) (query, bool) {
	var q query
	for _, item := range strings.Split(raw, "&") {
		if item == "" {
			continue
		}
		key, value, hasEq := strings.Cut(item, "=")
		var decoded any
		switch {
		case !hasEq && (v == q1 || v == q4 || v == q5All || v == q5First):
			decoded = "undefined"
		case !hasEq && v == q3:
			decoded = true
		case !hasEq:
			decoded = ""
		default:
			if v == q1 || v == q4 || v == q5All || v == q5First {
				value, _, _ = strings.Cut(value, "=")
			}
			var d string
			if v == q3 {
				d = PercentDecodeLenient(value)
			} else {
				var ok bool
				if d, ok = PercentDecodeStrict(value); !ok {
					return nil, false
				}
			}
			decoded = d
			if v == q4 && strings.Contains(d, ",") {
				decoded = splitList(d, ",")
			}
		}
		switch v {
		case q5All:
			key = strings.ReplaceAll(key, "_", "-")
		case q5First, q6:
			key = strings.Replace(key, "_", "-", 1)
		}
		q = append(q, queryItem{Key: key, Value: decoded})
	}
	return q, true
}

// get returns the value of the last item named key.
func (q query) get(key string) (any, bool) {
	for i := len(q) - 1; i >= 0; i-- {
		if q[i].Key == key {
			return q[i].Value, true
		}
	}
	return nil, false
}

// str returns the last value named key as text (String(value)), and
// whether the key is present.
func (q query) str(key string) (string, bool) {
	v, ok := q.get(key)
	if !ok {
		return "", false
	}
	return jsString(v, true), true
}

// or is ECMAScript's a || b over two query keys: the first value when it is
// truthy, else the second (which may be "" or absent).
func (q query) or(a, b string) (any, bool) {
	if v, ok := q.get(a); truthy(v, ok) {
		return v, true
	}
	return q.get(b)
}

// earlyData splits WebSocket early data out of a path (parser.md 4.1): the
// first non-empty "ed" value of the path's own query counts when it is
// digits-only and a safe integer; every "ed" item is then removed, the
// remaining items keep their spelling and order, and the "?" goes when none
// remain. ok is false when the path carries no early data, and the path is
// then returned untouched.
func earlyData(path string) (newPath, digits string, ok bool) {
	qi := strings.IndexByte(path, '?')
	if qi < 0 {
		return path, "", false
	}
	type item struct {
		raw   string
		isED  bool
		value string
	}
	var items []item
	found := false
	for _, raw := range strings.Split(path[qi+1:], "&") {
		if raw == "" {
			continue
		}
		k, v, _ := strings.Cut(raw, "=")
		k = PercentDecodeLenient(strings.ReplaceAll(k, "+", " "))
		v = PercentDecodeLenient(strings.ReplaceAll(v, "+", " "))
		it := item{raw: raw, isED: k == "ed", value: v}
		if it.isED && v != "" && !found {
			found = true
			digits = v
		}
		items = append(items, it)
	}
	if !found {
		return path, "", false
	}
	if _, safe := safeDigits(digits); !safe {
		return path, "", false
	}
	var kept []string
	for _, it := range items {
		if !it.isED {
			kept = append(kept, it.raw)
		}
	}
	newPath = path[:qi]
	if len(kept) > 0 {
		newPath += "?" + strings.Join(kept, "&")
	}
	return newPath, digits, true
}

// echOpts maps an ECH configuration value to ech-opts (parser.md 4.1); ok
// is false when the value maps to nothing.
func echOpts(v string) (map[string]any, bool) {
	if !nonBlank(v) {
		return nil, false
	}
	if !strings.Contains(v, "://") {
		return map[string]any{"enable": true, "config": v}, true
	}
	switch strings.Count(v, "+") {
	case 0:
		return map[string]any{"enable": true, "_dns": v}, true
	case 1:
		left, right, _ := strings.Cut(v, "+")
		if nonBlank(left) && nonBlank(right) {
			return map[string]any{"enable": true, "query-server-name": left, "_dns": right}, true
		}
	}
	return nil, false
}

// vmessCipher is the VMess security normalisation of parser.md 4.6, also
// used by the Clash object parser.
func vmessCipher(v string) string {
	switch s := strings.ToLower(TrimECMAScript(v)); s {
	case "":
		return "auto"
	case "auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305":
		return s
	case "chacha20-ietf-poly1305":
		return "chacha20-poly1305"
	}
	return "auto"
}
