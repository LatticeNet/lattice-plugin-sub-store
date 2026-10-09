package producers

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"gopkg.in/yaml.v3"
)

// The ClashMeta pretty form (clashmeta.md, "Output shape"): a block YAML 1.2
// dump of {proxies: [...]} with two-space indentation, sequence items indented
// under their key and no line folding, then the short-id rewrite.

// prettyProxies dumps items, the nodes as YAML values, under proxies and
// applies the short-id rewrite. items is never empty: the empty pretty
// document is "proxies: []", which EmptyDocument writes.
func prettyProxies(items []*yaml.Node) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		yamlString("proxies"),
		{Kind: yaml.SequenceNode, Content: items},
	}}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("producers: pretty ClashMeta document: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("producers: pretty ClashMeta document: %w", err)
	}
	// The rewrite's guard (the text holds proxies: and short-id: and reads
	// back as a mapping with a non-empty proxies list) holds for every
	// non-empty dump, so only the short-id: half is left to test.
	return rewriteShortID(buf.Bytes()), nil
}

// yamlValue is a model value as a YAML node: objects as block mappings in
// their key order (ECMAScript property order for a model object), lists as block sequences ([] and {} when empty),
// numbers as Number::toString writes them (.nan and .inf for the values JSON
// cannot carry) and text under the quoting rule of yamlString.
func yamlValue(v any, depth int) (*yaml.Node, error) {
	if depth > maxJSONDepth {
		return nil, fmt.Errorf("producers: value nested too deeply to write")
	}
	scalar := func(tag, value string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
	}
	switch x := v.(type) {
	case nil:
		return scalar("!!null", "null"), nil
	case bool:
		return scalar("!!bool", strconv.FormatBool(x)), nil
	case string:
		return yamlString(x), nil
	case float64:
		switch {
		case math.IsNaN(x):
			return scalar("!!float", ".nan"), nil
		case math.IsInf(x, 1):
			return scalar("!!float", ".inf"), nil
		case math.IsInf(x, -1):
			return scalar("!!float", "-.inf"), nil
		}
		return scalar("", nodemodel.FormatNumber(x)), nil
	case int64:
		return scalar("!!int", strconv.FormatInt(x, 10)), nil
	case int:
		return scalar("!!int", strconv.Itoa(x)), nil
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Content: make([]*yaml.Node, 0, len(x))}
		for _, e := range x {
			c, err := yamlValue(e, depth+1)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, c)
		}
		return n, nil
	case map[string]any:
		return yamlMapping(propertyOrder(x), x, depth)
	case *object:
		return yamlMapping(x.keys, x.vals, depth)
	}
	return nil, fmt.Errorf("producers: value of type %T is not part of the model", v)
}

func yamlMapping(keys []string, m map[string]any, depth int) (*yaml.Node, error) {
	n := &yaml.Node{Kind: yaml.MappingNode, Content: make([]*yaml.Node, 0, 2*len(keys))}
	for _, k := range keys {
		c, err := yamlValue(m[k], depth+1)
		if err != nil {
			return nil, err
		}
		n.Content = append(n.Content, yamlString(k), c)
	}
	return n, nil
}

// yamlString is text as a YAML scalar: plain when a plain scalar reads back as
// the same text, double-quoted otherwise, as the dumper upstream uses quotes
// it. The pretty-form goldens compare structurally except where the short-id
// rewrite corrupts a quoted name (clash-pretty-name-contains-short-id), and
// that case needs the double quotes.
func yamlString(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if yamlNeedsQuotes(s) {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

// yamlCoreNonString matches the plain scalars the YAML 1.2 core schema reads
// as null, a boolean, an integer or a float.
var yamlCoreNonString = regexp.MustCompile(`^(?:~|null|Null|NULL|true|True|TRUE|false|False|FALSE|[-+]?[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+|[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)(?:[eE][-+]?[0-9]+)?|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)

// yamlNeedsQuotes reports text that a plain block scalar would not carry as
// itself: empty text, a core-schema null, boolean or number, a leading
// indicator or white space, trailing white space or colon, ": " or " #"
// inside, and any control or line-separator character. It errs on the side of
// quoting, which never changes what the document reads back as; yaml.v3 also
// quotes a plain scalar its own resolver would read as something else.
func yamlNeedsQuotes(s string) bool {
	if s == "" || yamlCoreNonString.MatchString(s) {
		return true
	}
	if strings.IndexByte("-?:,[]{}#&*!|>'\"%@` \t", s[0]) >= 0 {
		return true
	}
	if last := s[len(s)-1]; last == ' ' || last == '\t' || last == ':' {
		return true
	}
	if strings.Contains(s, ": ") || strings.Contains(s, " #") {
		return true
	}
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) || r == 0x2028 || r == 0x2029 || r == 0xfeff {
			return true
		}
	}
	return false
}

// rewriteShortID is the pretty form's short-id rewrite. Every "short-id:" in
// the text is rewritten with the run V of characters after it up to the next
// "#", newline, "," or "}": with T the trimmed V, an empty T gives
// short-id: "", a T quoted at both ends with the same quote character or the
// text null stays as it is, and any other T is double-quoted. The rewrite is
// textual, as upstream's is: it also reaches "short-id:" inside another
// scalar and can leave text that is not valid YAML.
func rewriteShortID(doc []byte) []byte {
	const key = "short-id:"
	if !bytes.Contains(doc, []byte(key)) {
		return doc
	}
	out := make([]byte, 0, len(doc)+64)
	rest := doc
	for {
		i := bytes.Index(rest, []byte(key))
		if i < 0 {
			break
		}
		out = append(out, rest[:i+len(key)]...)
		rest = rest[i+len(key):]
		j := bytes.IndexAny(rest, "#\n,}")
		if j < 0 {
			j = len(rest)
		}
		v := trimES(string(rest[:j]))
		switch {
		case v == "":
			out = append(out, ` ""`...)
		case v == "null" || len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0]:
			out = append(append(out, ' '), v...)
		default:
			out = append(append(append(out, ` "`...), v...), '"')
		}
		rest = rest[j:]
	}
	return append(out, rest...)
}
