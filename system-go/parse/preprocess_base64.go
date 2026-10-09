package parse

import (
	"strings"
	"unicode/utf8"
)

// The two Base64 preprocessors of parser.md section 2 (rows 3 and 6).

// knownSchemeMarkers are the literal substrings of parser.md 2.2: the Base64
// spelling of a protocol prefix that starts at a multiple of three bytes.
var knownSchemeMarkers = []string{
	"dm1lc3M",          // vmess
	"c3NyOi8v",         // ssr://
	"c29ja3M6Ly",       // socks://
	"dHJvamFu",         // trojan
	"c3M6Ly",           // ss:/
	"c3NkOi8v",         // ssd://
	"c2hhZG93",         // shadow
	"aHR0c",            // the first bytes of http
	"dmxlc3M=",         // vless at the end of the text, with its padding
	"aHlzdGVyaWEy",     // hysteria2
	"aHkyOi8v",         // hy2://
	"d2lyZWd1YXJkOi8v", // wireguard://
	"d2c6Ly8=",         // wg:// with padding
	"dHVpYzovLw==",     // tuic:// with padding
}

// preprocessBase64Known is row 3: a valid Base64 document that holds one of
// the known-scheme markers.
func preprocessBase64Known(text string) (string, bool, string, error) {
	if !Base64Valid(text) || !containsAny(text, knownSchemeMarkers) {
		return "", false, "", nil
	}
	out, warning := decodeBase64Document(text)
	return out, true, warning, nil
}

// preprocessBase64Fallback is row 6: any valid Base64 document.
func preprocessBase64Fallback(text string) (string, bool, string, error) {
	if !Base64Valid(text) {
		return "", false, "", nil
	}
	out, warning := decodeBase64Document(text)
	return out, true, warning, nil
}

// decodeBase64Document is the shared output step: the lenient decode of the
// whole document when some decoded line starts like a protocol line,
// otherwise the raw document with a warning (and no later preprocessor).
func decodeBase64Document(text string) (string, string) {
	decoded := Base64DecodeLenient(text)
	if startsLikeProtocolLine(decoded) {
		return decoded, ""
	}
	return text, "Base64 document decodes to no protocol line; parsed as written"
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// startsLikeProtocolLine reports a line (after the start of the text or a
// line terminator) that begins with word characters followed by "://" or by
// "=" with optional white space either side, and then a word character
// (parser.md 2.3). The white-space runs after different lines never overlap,
// so the scan is linear.
func startsLikeProtocolLine(s string) bool {
	for start := 0; start <= len(s); {
		if protocolLineAt(s, start) {
			return true
		}
		next := nextLineStart(s, start)
		if next < 0 {
			return false
		}
		start = next
	}
	return false
}

// nextLineStart returns the position after the next line terminator at or
// after i, or -1.
func nextLineStart(s string, i int) int {
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if isESLineTerminator(r) {
			return i
		}
	}
	return -1
}

func isWordByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}

func protocolLineAt(s string, i int) bool {
	j := i
	for j < len(s) && isWordByte(s[j]) {
		j++
	}
	if j == i {
		return false
	}
	if strings.HasPrefix(s[j:], "://") {
		return j+3 < len(s) && isWordByte(s[j+3])
	}
	k := j + skipWhiteSpace(s[j:])
	if k >= len(s) || s[k] != '=' {
		return false
	}
	k++
	k += skipWhiteSpace(s[k:])
	return k < len(s) && isWordByte(s[k])
}

// skipWhiteSpace returns the length of the leading white space of s.
func skipWhiteSpace(s string) int {
	return len(s) - len(strings.TrimLeftFunc(s, isESWhiteSpace))
}
