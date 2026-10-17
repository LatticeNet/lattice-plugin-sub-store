package producers

import (
	"regexp"
	"strings"
	"testing"
)

// The vendored checker's two text forms (oracle/lib/canonical.mjs): lines
// for Surge and qx for Quantumult X. Each line becomes an entry with its
// positional head kept in place and its key=value parameters in a map, so the
// comparison keeps line order and ignores parameter order. These are ports of
// kvLines and qxLines, splitTopLevel and unquote, written against the same
// golden canon files.

// sectionLine is canonical.mjs's /^\[.*\]$/ on a trimmed line.
var sectionLine = regexp.MustCompile(`^\[.*\]$`)

// proxySection is /^\[proxy\]$/i.
var proxySection = regexp.MustCompile(`(?i)^\[proxy\]$`)

func TestCanonicalLinesKeepsRepeatedParamsAndQuotes(t *testing.T) {
	got := kvLines("#!name=x\n[Proxy]\na=trojan,h,443,interface=en0,interface=en1,headers=\"X-A:\"1\";X-B:\"2\"\"\n[WireGuard w]\nprivate-key = k\n# c\n")
	want := []any{
		map[string]any{"header": "#!name=x"},
		map[string]any{"section": "[Proxy]"},
		map[string]any{"name": "a", "type": "trojan", "args": []any{"h", "443"}, "params": map[string]any{
			"interface": []any{"en0", "en1"}, "headers": `X-A:"1";X-B:"2"`,
		}},
		map[string]any{"section": "[WireGuard w]"},
		map[string]any{"section": "[WireGuard w]", "key": "private-key", "value": "k"},
		map[string]any{"comment": "# c"},
	}
	if p, g, c, same := firstDiff(want, got, "$"); !same {
		t.Fatalf("kvLines differs at %s: want %s, got %s", p, g, c)
	}
	got = qxLines("trojan=h:443,password=a=b,over-tls=true,x,tag=n")
	want = []any{map[string]any{"type": "trojan", "address": "h:443", "args": []any{"x"}, "params": map[string]any{
		"password": "a=b", "over-tls": "true", "tag": "n",
	}}}
	if p, g, c, same := firstDiff(want, got, "$"); !same {
		t.Fatalf("qxLines differs at %s: want %s, got %s", p, g, c)
	}
}

// kvLines is canonical.mjs's kvLines.
func kvLines(text string) []any {
	out := []any{}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		tl := trimES(strings.TrimSuffix(line, "\r"))
		switch {
		case tl == "":
			continue
		case strings.HasPrefix(tl, "#!"):
			out = append(out, map[string]any{"header": tl})
			continue
		case sectionLine.MatchString(tl):
			section = tl
			out = append(out, map[string]any{"section": tl})
			continue
		case strings.HasPrefix(tl, "#"), strings.HasPrefix(tl, "//"), strings.HasPrefix(tl, ";"):
			out = append(out, map[string]any{"comment": tl})
			continue
		}
		eq := strings.IndexByte(tl, '=')
		if eq < 0 {
			out = append(out, map[string]any{"raw": tl})
			continue
		}
		name, body := trimES(tl[:eq]), tl[eq+1:]
		if section != "" && !proxySection.MatchString(section) {
			out = append(out, map[string]any{"section": section, "key": name, "value": trimES(body)})
			continue
		}
		tokens := splitTopLevel(body, ',')
		for i := range tokens {
			tokens[i] = trimES(tokens[i])
		}
		args := []any{}
		params := map[string]any{}
		for _, tok := range tokens[1:] {
			if k, v, ok := kvParam(tok); ok {
				addParam(params, k, unquote(v))
			} else {
				args = append(args, unquote(tok))
			}
		}
		out = append(out, map[string]any{"name": unquote(name), "type": tokens[0], "args": args, "params": params})
	}
	return out
}

// qxLines is canonical.mjs's qxLines.
func qxLines(text string) []any {
	out := []any{}
	for _, line := range strings.Split(text, "\n") {
		tl := trimES(strings.TrimSuffix(line, "\r"))
		switch {
		case tl == "":
			continue
		case sectionLine.MatchString(tl):
			out = append(out, map[string]any{"section": tl})
			continue
		case strings.HasPrefix(tl, "#"), strings.HasPrefix(tl, ";"):
			out = append(out, map[string]any{"comment": tl})
			continue
		}
		tokens := splitTopLevel(tl, ',')
		for i := range tokens {
			tokens[i] = trimES(tokens[i])
		}
		typ, address, ok := qxHead(tokens[0])
		if !ok {
			out = append(out, map[string]any{"raw": tl})
			continue
		}
		args := []any{}
		params := map[string]any{}
		for _, tok := range tokens[1:] {
			if i := strings.IndexByte(tok, '='); i > 0 {
				addParam(params, trimES(tok[:i]), trimES(tok[i+1:]))
			} else {
				args = append(args, tok)
			}
		}
		out = append(out, map[string]any{"type": typ, "address": address, "args": args, "params": params})
	}
	return out
}

// kvParam is /^([A-Za-z0-9_-]+)\s*=\s*(.*)$/s.
func kvParam(tok string) (key, value string, ok bool) {
	i := 0
	for i < len(tok) && (isAlnum(tok[i]) || tok[i] == '_' || tok[i] == '-') {
		i++
	}
	if i == 0 {
		return "", "", false
	}
	rest := strings.TrimLeftFunc(tok[i:], isESWhiteSpace)
	if !strings.HasPrefix(rest, "=") {
		return "", "", false
	}
	return tok[:i], strings.TrimLeftFunc(rest[1:], isESWhiteSpace), true
}

// qxHead is /^([A-Za-z0-9-]+)\s*=\s*(.*)$/s.
func qxHead(tok string) (typ, address string, ok bool) {
	i := 0
	for i < len(tok) && (isAlnum(tok[i]) || tok[i] == '-') {
		i++
	}
	if i == 0 {
		return "", "", false
	}
	rest := strings.TrimLeftFunc(tok[i:], isESWhiteSpace)
	if !strings.HasPrefix(rest, "=") {
		return "", "", false
	}
	return tok[:i], strings.TrimLeftFunc(rest[1:], isESWhiteSpace), true
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// splitTopLevel splits on sep outside double quotes, brackets, braces and
// parentheses.
func splitTopLevel(s string, sep rune) []string {
	var out []string
	depth := 0
	inQuote := false
	var cur strings.Builder
	for _, ch := range s {
		if inQuote {
			cur.WriteRune(ch)
			if ch == '"' {
				inQuote = false
			}
			continue
		}
		switch ch {
		case '"':
			inQuote = true
			cur.WriteRune(ch)
			continue
		case '[', '{', '(':
			depth++
		case ']', '}', ')':
			depth = max(0, depth-1)
		}
		if ch == sep && depth == 0 {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(ch)
	}
	return append(out, cur.String())
}

func unquote(s string) string {
	t := trimES(s)
	if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
		return t[1 : len(t)-1]
	}
	return t
}

func addParam(params map[string]any, key string, value any) {
	prev, ok := params[key]
	if !ok {
		params[key] = value
		return
	}
	if l, isList := prev.([]any); isList {
		params[key] = append(l, value)
		return
	}
	params[key] = []any{prev, value}
}
