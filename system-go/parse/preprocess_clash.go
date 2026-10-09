package parse

import (
	"errors"
	"sort"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// preprocessClash is row 2 of parser.md section 2 (details in 2.1): a
// document that contains "proxies" and parses as YAML into a mapping whose
// proxies or proxy-groups value is a list yields one line per proxies entry,
// each the compact JSON of the entry. A YAML error makes the row fail, and
// the next row is tried.
func preprocessClash(text string) (string, bool, string, error) {
	if !strings.Contains(text, "proxies") {
		return "", false, "", nil
	}
	v, err := decodeYAML(text)
	if err != nil {
		return "", false, "", nil
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return "", false, "", nil
	}
	proxies, proxiesList := doc["proxies"].([]any)
	_, groupsList := doc["proxy-groups"].([]any)
	if !proxiesList && !groupsList {
		return "", false, "", nil
	}
	if proxiesList && len(proxies) > 0 && strings.Contains(text, "proxies:") && strings.Contains(text, "short-id:") {
		// Protect short ids from number inference, then read the document
		// again for the output step.
		v, err := decodeYAML(quoteShortIDs(text))
		if err != nil {
			return "", false, "", nil
		}
		doc, _ = v.(map[string]any)
		proxies, proxiesList = doc["proxies"].([]any)
	}
	if !proxiesList {
		return "", true, "", nil
	}
	out, err := clashLines(proxies)
	if err != nil {
		return "", false, "", err
	}
	return out, true, "", nil
}

// quoteShortIDs rewrites the value after every "short-id:" on the same line
// as a double-quoted string: an empty value becomes "", a value already in
// matching quotes and the bare word null are kept, anything else (up to a
// "#", a comma, a closing brace or the end of the line, trimmed) is wrapped
// in double quotes. White space after the value is kept so a following
// comment stays a comment.
func quoteShortIDs(text string) string {
	const key = "short-id:"
	var b strings.Builder
	b.Grow(len(text) + 64)
	for {
		i := strings.Index(text, key)
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		b.WriteString(text[:i+len(key)])
		text = text[i+len(key):]
		end := strings.IndexAny(text, "#,}\n")
		if end < 0 {
			end = len(text)
		}
		raw := text[:end]
		value := TrimECMAScript(raw)
		trailing := raw[len(strings.TrimRightFunc(raw, isESWhiteSpace)):]
		switch {
		case value == "":
			b.WriteString(` ""`)
			b.WriteString(trailing)
		case value == "null" || quotedWith(value, '"') || quotedWith(value, '\''):
			b.WriteString(raw)
		default:
			b.WriteString(` "`)
			b.WriteString(value)
			b.WriteString(`"`)
			b.WriteString(trailing)
		}
		text = text[end:]
	}
}

func quotedWith(s string, q byte) bool {
	return len(s) >= 2 && s[0] == q && s[len(s)-1] == q
}

var errClashBudget = errors.New("parse: clash document expands past the bound")

// clashLines writes one compact JSON line per entry, as JSON.stringify
// writes them. Entries that are not mappings still produce a line. The
// output may not exceed MaxExpandedBytes: aliases let a small document name
// one large value many times.
func clashLines(entries []any) (string, error) {
	w := jsonBudgetWriter{limit: MaxExpandedBytes}
	for i, e := range entries {
		if i > 0 {
			w.buf = append(w.buf, '\n')
		}
		if err := w.value(e, 0); err != nil {
			return "", ErrExpansionTooLarge
		}
	}
	return string(w.buf), nil
}

// jsonBudgetWriter appends JSON text and fails once the text passes limit.
type jsonBudgetWriter struct {
	buf   []byte
	limit int
}

func (w *jsonBudgetWriter) value(v any, depth int) error {
	if len(w.buf) > w.limit {
		return errClashBudget
	}
	if depth > maxStructuredDepth {
		return errClashBudget
	}
	switch x := v.(type) {
	case nil:
		w.buf = append(w.buf, "null"...)
	case bool:
		if x {
			w.buf = append(w.buf, "true"...)
		} else {
			w.buf = append(w.buf, "false"...)
		}
	case string:
		w.buf = nodemodel.AppendJSONString(w.buf, x)
	case float64:
		w.buf = nodemodel.AppendNumber(w.buf, x)
	case []any:
		w.buf = append(w.buf, '[')
		for i, e := range x {
			if i > 0 {
				w.buf = append(w.buf, ',')
			}
			if err := w.value(e, depth+1); err != nil {
				return err
			}
		}
		w.buf = append(w.buf, ']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w.buf = append(w.buf, '{')
		for i, k := range keys {
			if i > 0 {
				w.buf = append(w.buf, ',')
			}
			w.buf = nodemodel.AppendJSONString(w.buf, k)
			w.buf = append(w.buf, ':')
			if err := w.value(x[k], depth+1); err != nil {
				return err
			}
		}
		w.buf = append(w.buf, '}')
	default:
		return errClashBudget
	}
	if len(w.buf) > w.limit {
		return errClashBudget
	}
	return nil
}
