package parse

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// The JSON and JSON5 readers of parser.md section 1.4. Both return model
// values: map[string]any, []any, string, float64, bool and nil. A duplicate
// key keeps the last value, as JSON.parse and JSON5.parse do.

// maxStructuredDepth bounds the nesting the readers accept. It matches the
// limit gopkg.in/yaml.v3 applies, sits far above the node nesting bound of
// normaliser.md section 5, and keeps recursion shallow for hostile input.
const maxStructuredDepth = 10000

var errJSONTrailing = errors.New("parse: data after the JSON value")

var (
	inf = math.Inf(1)
	nan = math.NaN()
)

// decodeJSON reads RFC 8259 JSON as JSON.parse does: numbers become doubles
// (a literal past the double range becomes an infinity rather than an
// error), and text after the value is an error.
func decodeJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errJSONTrailing
		}
		return nil, err
	}
	return jsonNumbers(v), nil
}

// jsonNumbers replaces json.Number with the double JSON.parse would read.
func jsonNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, _ := strconv.ParseFloat(string(x), 64)
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = jsonNumbers(e)
		}
	case []any:
		for i, e := range x {
			x[i] = jsonNumbers(e)
		}
	}
	return v
}

// json5Error is a JSON5 syntax error at a byte offset.
type json5Error struct {
	offset int
	msg    string
}

func (e *json5Error) Error() string {
	return "parse: JSON5: " + e.msg + " at byte " + strconv.Itoa(e.offset)
}

// decodeJSON5 reads JSON5 2.x: unquoted identifier keys, single-quoted
// strings, trailing commas, comments, hexadecimal numbers, Infinity, NaN, a
// leading "+", a leading or trailing decimal point. A duplicate key keeps the
// last value.
func decodeJSON5(s string) (any, error) {
	p := json5Parser{s: s}
	p.space()
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.err != nil {
		return nil, p.err
	}
	if p.i != len(p.s) {
		return nil, p.fail("unexpected text after the value")
	}
	return v, nil
}

type json5Parser struct {
	s   string
	i   int
	err error
}

func (p *json5Parser) fail(msg string) error {
	return &json5Error{offset: p.i, msg: msg}
}

// space skips white space and comments. An unterminated block comment sets
// p.err.
func (p *json5Parser) space() {
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '/' && p.i+1 < len(p.s) && p.s[p.i+1] == '/':
			p.i += 2
			for p.i < len(p.s) {
				r, size := utf8.DecodeRuneInString(p.s[p.i:])
				if isESLineTerminator(r) {
					break
				}
				p.i += size
			}
		case c == '/' && p.i+1 < len(p.s) && p.s[p.i+1] == '*':
			end := strings.Index(p.s[p.i+2:], "*/")
			if end < 0 {
				p.err = p.fail("unterminated comment")
				p.i = len(p.s)
				return
			}
			p.i += 2 + end + 2
		case c < utf8.RuneSelf:
			if c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r' || c == ' ' {
				p.i++
				continue
			}
			return
		default:
			r, size := utf8.DecodeRuneInString(p.s[p.i:])
			if !isESWhiteSpace(r) {
				return
			}
			p.i += size
		}
	}
}

func (p *json5Parser) value(depth int) (any, error) {
	if depth > maxStructuredDepth {
		return nil, p.fail("nested too deeply")
	}
	if p.err != nil {
		return nil, p.err
	}
	if p.i >= len(p.s) {
		return nil, p.fail("unexpected end of input")
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"' || c == '\'':
		return p.str()
	case c == 't':
		return p.literal("true", true)
	case c == 'f':
		return p.literal("false", false)
	case c == 'n':
		return p.literal("null", nil)
	default:
		return p.number()
	}
}

func (p *json5Parser) literal(word string, v any) (any, error) {
	if !strings.HasPrefix(p.s[p.i:], word) {
		return nil, p.fail("invalid literal")
	}
	p.i += len(word)
	return v, nil
}

func (p *json5Parser) object(depth int) (any, error) {
	p.i++ // {
	m := map[string]any{}
	for {
		p.space()
		if p.err != nil {
			return nil, p.err
		}
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return m, nil
		}
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		p.space()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, p.fail("expected ':'")
		}
		p.i++
		p.space()
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		m[key] = v
		p.space()
		if p.err != nil {
			return nil, p.err
		}
		if p.i >= len(p.s) {
			return nil, p.fail("unterminated object")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return m, nil
		default:
			return nil, p.fail("expected ',' or '}'")
		}
	}
}

func (p *json5Parser) array(depth int) (any, error) {
	p.i++ // [
	l := []any{}
	for {
		p.space()
		if p.err != nil {
			return nil, p.err
		}
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return l, nil
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		l = append(l, v)
		p.space()
		if p.err != nil {
			return nil, p.err
		}
		if p.i >= len(p.s) {
			return nil, p.fail("unterminated array")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return l, nil
		default:
			return nil, p.fail("expected ',' or ']'")
		}
	}
}

// key reads a quoted string or an ECMAScript IdentifierName, which may
// contain \uXXXX escapes.
func (p *json5Parser) key() (string, error) {
	if p.i >= len(p.s) {
		return "", p.fail("unexpected end of input")
	}
	if c := p.s[p.i]; c == '"' || c == '\'' {
		v, err := p.str()
		if err != nil {
			return "", err
		}
		return v.(string), nil
	}
	var b strings.Builder
	first := true
	for p.i < len(p.s) {
		var r rune
		if p.s[p.i] == '\\' {
			if p.i+1 >= len(p.s) || p.s[p.i+1] != 'u' {
				return "", p.fail("invalid identifier escape")
			}
			p.i += 2
			u, ok := p.hex(4)
			if !ok {
				return "", p.fail("invalid identifier escape")
			}
			r = rune(u)
			if !identifierRune(r, first) {
				return "", p.fail("invalid identifier character")
			}
		} else {
			var size int
			r, size = utf8.DecodeRuneInString(p.s[p.i:])
			if !identifierRune(r, first) {
				break
			}
			p.i += size
		}
		b.WriteRune(r)
		first = false
	}
	if first {
		return "", p.fail("expected a key")
	}
	return b.String(), nil
}

// identifierRune is ES5.1 IdentifierStart (Unicode letters, letter numbers,
// "$", "_") or, after the first character, IdentifierPart (also combining
// marks, decimal digits, connector punctuation, ZWNJ and ZWJ).
func identifierRune(r rune, first bool) bool {
	if r == '$' || r == '_' || unicode.In(r, unicode.L, unicode.Nl) {
		return true
	}
	if first {
		return false
	}
	return r == 0x200c || r == 0x200d || unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc)
}

// hex reads n hexadecimal digits.
func (p *json5Parser) hex(n int) (int, bool) {
	if p.i+n > len(p.s) {
		return 0, false
	}
	v := 0
	for k := 0; k < n; k++ {
		d, ok := hexValue(p.s[p.i+k])
		if !ok {
			return 0, false
		}
		v = v<<4 | int(d)
	}
	p.i += n
	return v, true
}

func (p *json5Parser) str() (any, error) {
	quote := p.s[p.i]
	p.i++
	var b strings.Builder
	var pending rune = -1 // a high surrogate waiting for its low half
	flush := func() {
		if pending >= 0 {
			b.WriteRune(utf8.RuneError)
			pending = -1
		}
	}
	for {
		if p.i >= len(p.s) {
			return nil, p.fail("unterminated string")
		}
		c := p.s[p.i]
		switch {
		case c == quote:
			p.i++
			flush()
			return b.String(), nil
		case c == '\n' || c == '\r':
			return nil, p.fail("line break in string")
		case c == '\\':
			p.i++
			if p.i >= len(p.s) {
				return nil, p.fail("unterminated string")
			}
			e := p.s[p.i]
			p.i++
			var r rune = -1
			switch e {
			case 'b':
				r = '\b'
			case 'f':
				r = '\f'
			case 'n':
				r = '\n'
			case 'r':
				r = '\r'
			case 't':
				r = '\t'
			case 'v':
				r = '\v'
			case '0':
				if p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
					return nil, p.fail("octal escape")
				}
				r = 0
			case 'x':
				v, ok := p.hex(2)
				if !ok {
					return nil, p.fail("invalid \\x escape")
				}
				r = rune(v)
			case 'u':
				v, ok := p.hex(4)
				if !ok {
					return nil, p.fail("invalid \\u escape")
				}
				u := rune(v)
				switch {
				case utf16.IsSurrogate(u) && u < 0xdc00:
					flush()
					pending = u
					continue
				case utf16.IsSurrogate(u) && pending >= 0:
					b.WriteRune(utf16.DecodeRune(pending, u))
					pending = -1
					continue
				}
				r = u
			case '\n':
				flush()
				continue
			case '\r':
				if p.i < len(p.s) && p.s[p.i] == '\n' {
					p.i++
				}
				flush()
				continue
			case '1', '2', '3', '4', '5', '6', '7', '8', '9':
				return nil, p.fail("invalid escape")
			default:
				// Any other character escapes to itself; U+2028 and U+2029
				// after a backslash are line continuations.
				p.i--
				er, size := utf8.DecodeRuneInString(p.s[p.i:])
				p.i += size
				if er == 0x2028 || er == 0x2029 {
					flush()
					continue
				}
				r = er
			}
			flush()
			b.WriteRune(r)
		default:
			flush()
			r, size := utf8.DecodeRuneInString(p.s[p.i:])
			b.WriteRune(r)
			p.i += size
		}
	}
}

// number reads a JSON5 number: an optional sign, then Infinity, NaN, a
// hexadecimal integer or a decimal literal with an optional fraction and
// exponent, either side of the point may be empty but not both.
func (p *json5Parser) number() (any, error) {
	start := p.i
	sign := 1.0
	if c := p.s[p.i]; c == '+' || c == '-' {
		if c == '-' {
			sign = -1
		}
		p.i++
	}
	rest := p.s[p.i:]
	switch {
	case strings.HasPrefix(rest, "Infinity"):
		p.i += len("Infinity")
		return sign * inf, nil
	case strings.HasPrefix(rest, "NaN"):
		p.i += len("NaN")
		return nan, nil
	case len(rest) > 1 && rest[0] == '0' && (rest[1] == 'x' || rest[1] == 'X'):
		p.i += 2
		digits := p.i
		for p.i < len(p.s) {
			if _, ok := hexValue(p.s[p.i]); !ok {
				break
			}
			p.i++
		}
		if p.i == digits {
			return nil, p.fail("invalid hexadecimal number")
		}
		v, _ := strconv.ParseFloat("0x"+p.s[digits:p.i]+"p0", 64)
		return sign * v, nil
	}
	intStart := p.i
	if p.i < len(p.s) && p.s[p.i] == '0' {
		p.i++
	} else {
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	}
	intDigits := p.i - intStart
	fracDigits := 0
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		f := p.i
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
		fracDigits = p.i - f
	}
	if intDigits == 0 && fracDigits == 0 {
		p.i = start
		return nil, p.fail("invalid value")
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		e := p.i
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
		if p.i == e {
			return nil, p.fail("invalid exponent")
		}
	}
	text := p.s[intStart:p.i]
	if strings.HasSuffix(strings.SplitN(strings.ToLower(text), "e", 2)[0], ".") {
		// "5." and "5.e3" are JSON5 numbers; ParseFloat reads them too, but
		// spell the fraction out so the two readers cannot disagree.
		text = strings.Replace(text, ".", ".0", 1)
	}
	if strings.HasPrefix(text, ".") {
		text = "0" + text
	}
	v, _ := strconv.ParseFloat(text, 64)
	return sign * v, nil
}
