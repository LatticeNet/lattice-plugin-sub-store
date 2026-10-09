package parse

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// The text primitives of parser.md section 1.3. Every parser names one of
// them instead of reaching for a standard library call that looks alike:
// strings.TrimSpace keeps U+FEFF, url.QueryUnescape turns "+" into a space,
// and base64.StdEncoding refuses the inputs upstream decodes leniently. The
// producers use the same primitives, so they are exported.

// TrimECMAScript removes leading and trailing white space and line
// terminators as ECMAScript's String.prototype.trim does: tab, line feed,
// vertical tab, form feed, carriage return, space, no-break space, U+FEFF,
// every Unicode space separator, U+2028 and U+2029.
func TrimECMAScript(s string) string {
	return strings.TrimFunc(s, isESWhiteSpace)
}

// isESWhiteSpace is ECMAScript's WhiteSpace or LineTerminator, which is also
// what \s matches in an ECMAScript regular expression.
func isESWhiteSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0xfeff, 0x2028, 0x2029:
		return true
	}
	return r > 0x7f && unicode.Is(unicode.Zs, r)
}

// isESLineTerminator is what ECMAScript treats as the end of a line: the
// characters "^" and "$" match beside in multiline mode and "." never matches.
func isESLineTerminator(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029
}

// nonBlank reports a text holding at least one character that is not white
// space (parser.md 1.3, "Non-blank").
func nonBlank(s string) bool {
	for _, r := range s {
		if !isESWhiteSpace(r) {
			return true
		}
	}
	return false
}

// PercentDecodeStrict is decodeURIComponent: every "%" must be followed by
// two hexadecimal digits and the decoded bytes must be well-formed UTF-8 (no
// overlong form, no surrogate, nothing above U+10FFFF, no truncated
// sequence). "+" stays "+". ok is false on any violation.
func PercentDecodeStrict(s string) (string, bool) {
	i := strings.IndexByte(s, '%')
	if i < 0 {
		return s, true
	}
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:i])
	for i < len(s) {
		c := s[i]
		if c != '%' {
			b.WriteByte(c)
			i++
			continue
		}
		first, ok := hexByte(s, i)
		if !ok {
			return "", false
		}
		i += 3
		if first < utf8.RuneSelf {
			b.WriteByte(first)
			continue
		}
		var n int
		switch {
		case first&0xe0 == 0xc0:
			n = 2
		case first&0xf0 == 0xe0:
			n = 3
		case first&0xf8 == 0xf0:
			n = 4
		default:
			return "", false
		}
		var seq [4]byte
		seq[0] = first
		for k := 1; k < n; k++ {
			if i >= len(s) || s[i] != '%' {
				return "", false
			}
			cont, ok := hexByte(s, i)
			if !ok || cont&0xc0 != 0x80 {
				return "", false
			}
			seq[k] = cont
			i += 3
		}
		r, size := utf8.DecodeRune(seq[:n])
		if r == utf8.RuneError || size != n {
			return "", false
		}
		b.Write(seq[:n])
	}
	return b.String(), true
}

// hexByte reads the "%XX" escape at s[i].
func hexByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) {
		return 0, false
	}
	hi, ok1 := hexValue(s[i+1])
	lo, ok2 := hexValue(s[i+2])
	return hi<<4 | lo, ok1 && ok2
}

func hexValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// PercentDecodeLenient is PercentDecodeStrict that keeps the input unchanged
// when it cannot be decoded.
func PercentDecodeLenient(s string) string {
	if d, ok := PercentDecodeStrict(s); ok {
		return d
	}
	return s
}

// Base64Valid is the Base64 validity test: with all white space removed and
// up to two trailing "=" dropped, every character is in the standard
// alphabet or every character is in the URL-safe one. A text mixing "+" or
// "/" with "-" or "_" is invalid; the empty text is valid.
func Base64Valid(s string) bool {
	end := len(s)
	// Trailing white space and up to two "=" (white space may sit between
	// them, since it is removed first).
	pads := 0
	for end > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:end])
		if isESWhiteSpace(r) {
			end -= size
			continue
		}
		if r == '=' && pads < 2 {
			pads++
			end--
			continue
		}
		break
	}
	standard, urlSafe := false, false
	for _, r := range s[:end] {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '+' || r == '/':
			standard = true
		case r == '-' || r == '_':
			urlSafe = true
		case isESWhiteSpace(r):
		default:
			return false
		}
		if standard && urlSafe {
			return false
		}
	}
	return true
}

// base64Value maps a character of the standard alphabet, with "-" and "_"
// read as "+" and "/", to its six bits; ok is false for every other byte.
func base64Value(c byte) (byte, bool) {
	switch {
	case c >= 'A' && c <= 'Z':
		return c - 'A', true
	case c >= 'a' && c <= 'z':
		return c - 'a' + 26, true
	case c >= '0' && c <= '9':
		return c - '0' + 52, true
	case c == '+' || c == '-':
		return 62, true
	case c == '/' || c == '_':
		return 63, true
	}
	return 0, false
}

// Base64DecodeLenient is the lenient Base64 decode: "-" and "_" read as "+"
// and "/", every other character outside the standard alphabet deleted
// ("=" and white space included), groups of four decoded, a final group of
// two or three decoded to one or two bytes, a final single character and
// non-zero trailing bits ignored. The bytes are decoded as UTF-8 with the
// WHATWG decoder's replacement. It never fails.
func Base64DecodeLenient(s string) string {
	out := make([]byte, 0, len(s)*3/4+3)
	var acc uint32
	n := 0
	for i := 0; i < len(s); i++ {
		v, ok := base64Value(s[i])
		if !ok {
			continue
		}
		acc = acc<<6 | uint32(v)
		n++
		if n == 4 {
			out = append(out, byte(acc>>16), byte(acc>>8), byte(acc))
			acc, n = 0, 0
		}
	}
	switch n {
	case 2:
		out = append(out, byte(acc>>4))
	case 3:
		out = append(out, byte(acc>>10), byte(acc>>2))
	}
	return decodeUTF8(out)
}

// decodeUTF8 decodes bytes as UTF-8 the way the WHATWG decoder does: each
// maximal ill-formed subsequence becomes one U+FFFD. Valid input is returned
// without copying.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return wellFormed(string(b))
}

// wellFormed replaces each maximal ill-formed UTF-8 subsequence of s with
// U+FFFD, as the WHATWG decoder does (Unicode table 3-7 ranges).
func wellFormed(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var out strings.Builder
	out.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			out.WriteByte(c)
			i++
			continue
		}
		need, lo, hi := 0, byte(0x80), byte(0xbf)
		switch {
		case c >= 0xc2 && c <= 0xdf:
			need = 1
		case c == 0xe0:
			need, lo = 2, 0xa0
		case c == 0xed:
			need, hi = 2, 0x9f
		case c >= 0xe1 && c <= 0xef:
			need = 2
		case c == 0xf0:
			need, lo = 3, 0x90
		case c == 0xf4:
			need, hi = 3, 0x8f
		case c >= 0xf1 && c <= 0xf3:
			need = 3
		default:
			out.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < need; k++ {
			if j >= len(s) {
				ok = false
				break
			}
			low, high := byte(0x80), byte(0xbf)
			if k == 0 {
				low, high = lo, hi
			}
			if s[j] < low || s[j] > high {
				ok = false
				break
			}
			j++
		}
		if !ok {
			out.WriteRune(utf8.RuneError)
			i = j
			continue
		}
		out.WriteString(s[i:j])
		i = j
	}
	return out.String()
}

// ContainsTrueOr1 is true when the text contains "true" in any letter case
// or contains the character "1": "1", "10", "TRUE" and "untrue" are true;
// "0", "false", "yes" and the empty text are false. Callers pass "" for an
// absent value.
func ContainsTrueOr1(s string) bool {
	if strings.IndexByte(s, '1') >= 0 {
		return true
	}
	for i := 0; i+4 <= len(s); i++ {
		if lowerASCII(s[i]) == 't' && lowerASCII(s[i+1]) == 'r' && lowerASCII(s[i+2]) == 'u' && lowerASCII(s[i+3]) == 'e' {
			return true
		}
	}
	return false
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// LeadingInteger is parseInt(s, 10): leading white space skipped, an
// optional sign, then decimal digits; everything after the digits ignored.
// No digits gives not-a-number. The value is the nearest double, as
// ECMAScript computes it, so "-0" is negative zero.
func LeadingInteger(s string) float64 {
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isESWhiteSpace(r) {
			break
		}
		i += size
	}
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == digits {
		return math.NaN()
	}
	return numberValue(s[start:i])
}

// DigitsOnlyInteger reads a text of one or more decimal digits and nothing
// else; ok is false for anything else.
func DigitsOnlyInteger(s string) (float64, bool) {
	if !digitsOnly(s) {
		return 0, false
	}
	return numberValue(s), true
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

// numberValue is Number(text) for a text that is an optional sign and
// decimal digits: the nearest double, infinity past the double range.
func numberValue(s string) float64 {
	// Callers pass only a sign and digits, so the one possible error is a
	// range error, for which ParseFloat already returns the signed infinity.
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// maxSafeInteger is 2^53 - 1, ECMAScript's Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

// safeDigits reads a digits-only text that is at most 2^53 - 1 (parser.md
// 4.1, early data).
func safeDigits(s string) (float64, bool) {
	v, ok := DigitsOnlyInteger(s)
	return v, ok && v <= maxSafeInteger
}

// jsString is ECMAScript's String(value) over model values, with
// "undefined" for an absent value (present=false).
func jsString(v any, present bool) string {
	if !present {
		return "undefined"
	}
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return jsNumber(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = jsString(e, true)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// jsNumber is String(number).
func jsNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return nodemodel.FormatNumber(f)
}

// truthy is ECMAScript truthiness over model values.
func truthy(v any, present bool) bool {
	if !present {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

// encodeURIComponent percent-encodes every byte of s except the unreserved
// marks ECMAScript's encodeURIComponent keeps: A-Z a-z 0-9 - _ . ! ~ * ' ( ).
func encodeURIComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
		}
	}
	return b.String()
}

// splitTrimNonEmpty splits on sep, trims each piece and drops empty pieces.
// The list is sized once: a line may hold some 30000 pieces, and growing the
// list piece by piece doubled the cost per piece at that length.
func splitTrimNonEmpty(s, sep string) []any {
	parts := strings.Split(s, sep)
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		if p = TrimECMAScript(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitList is value.split(sep) as a model list.
func splitList(s, sep string) []any {
	parts := strings.Split(s, sep)
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return out
}

// hasLineTerminator reports a character ECMAScript's "." does not match
// other than line feed (lines are already split on line feeds).
func hasLineTerminator(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r':
			return true
		case 0xe2:
			if i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xa8 || s[i+2] == 0xa9) {
				return true
			}
		}
	}
	return false
}
