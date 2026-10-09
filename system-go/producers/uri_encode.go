package producers

import (
	"encoding/base64"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// The encoding rules of specs/producers/uri.md ("Encoding rules"), shared by
// the URI and V2Ray producers.

// encodeComponent is ECMAScript's encodeURIComponent: every byte of the UTF-8
// text except A-Z, a-z, 0-9 and - _ . ! ~ * ' ( ) becomes %XX with upper-case
// hex. url.QueryEscape writes a space as "+" and escapes ! * ' ( ), and
// url.PathEscape leaves : @ & = + $ alone, so neither matches.
func encodeComponent(s string) string {
	n := 0
	for i := 0; i < len(s); i++ {
		if !unreservedComponent(s[i]) {
			n++
		}
	}
	if n == 0 {
		return s
	}
	const hex = "0123456789ABCDEF"
	b := make([]byte, 0, len(s)+2*n)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if unreservedComponent(c) {
			b = append(b, c)
			continue
		}
		b = append(b, '%', hex[c>>4], hex[c&0xf])
	}
	return string(b)
}

func unreservedComponent(c byte) bool {
	switch {
	case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9':
		return true
	}
	switch c {
	case '-', '_', '.', '!', '~', '*', '\'', '(', ')':
		return true
	}
	return false
}

// decodeQueryKey percent-decodes a query key with "+" read as a space. ok is
// false for a malformed escape or an escape that is not UTF-8, where
// ECMAScript's decodeURIComponent throws.
func decodeQueryKey(s string) (string, bool) {
	s = strings.ReplaceAll(s, "+", " ")
	if !strings.Contains(s, "%") {
		return s, true
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b = append(b, s[i])
			continue
		}
		if i+2 >= len(s) || !isHexDigit(s[i+1]) || !isHexDigit(s[i+2]) {
			return "", false
		}
		v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
		b = append(b, byte(v))
		i += 2
	}
	if !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// b64 is Base64 with the standard alphabet and "=" padding over the UTF-8
// bytes. No producer here uses the URL-safe alphabet.
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// text is the text form of a model value, ECMAScript's String(value): a
// string as it is, a number as Number::toString writes it, a boolean as true
// or false, a list as its elements' text forms joined by "," (null elements
// as empty text) and an object as "[object Object]".
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		switch {
		case math.IsNaN(x):
			return "NaN"
		case math.IsInf(x, 1):
			return "Infinity"
		case math.IsInf(x, -1):
			return "-Infinity"
		}
		return nodemodel.FormatNumber(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = text(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// textOf is the text form of m[key], "undefined" when the key is absent.
func textOf(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return "undefined"
	}
	return text(v)
}

// set reports whether m[key] is truthy, which is what the specifications
// mean by a field being "set".
func set(m map[string]any, key string) bool {
	v, ok := m[key]
	return ok && truthy(v)
}

// obj reads m[key] as an object.
func obj(m map[string]any, key string) (map[string]any, bool) {
	o, ok := m[key].(map[string]any)
	return o, ok
}

// str reads m[key] as text.
func str(m map[string]any, key string) (string, bool) {
	s, ok := m[key].(string)
	return s, ok
}

// first is a list's first element, or the value itself when it is not a
// list. An empty list gives absent.
func first(v any) (any, bool) {
	if l, ok := v.([]any); ok {
		if len(l) == 0 {
			return nil, false
		}
		return l[0], true
	}
	return v, true
}

// trimES removes what ECMAScript's String.prototype.trim removes: white space
// and line terminators, U+FEFF included (strings.TrimSpace keeps it).
func trimES(s string) string { return strings.TrimFunc(s, isESWhiteSpace) }

func isESWhiteSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0xfeff, 0x2028, 0x2029:
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// notBlank reports whether v is text with something other than white space.
func notBlank(v any) bool {
	s, ok := v.(string)
	return ok && trimES(s) != ""
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// maxSafeInteger is ECMAScript's Number.MAX_SAFE_INTEGER, 2^53 - 1.
const maxSafeInteger = 1<<53 - 1

// safeInteger reports whether v's text form is all ASCII digits and reads as
// an integer no larger than 2^53 - 1, and returns that text.
func safeInteger(v any) (string, bool) {
	s := text(v)
	if !digitsOnly(s) {
		return "", false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n > maxSafeInteger {
		return "", false
	}
	return s, true
}
