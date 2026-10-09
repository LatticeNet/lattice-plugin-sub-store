package parse

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The YAML reader of parser.md section 1.4: YAML 1.2 with the core schema
// and merge keys. gopkg.in/yaml.v3 does the syntax and keeps the node tree;
// this file resolves scalars, merge keys, aliases and duplicate keys itself,
// because yaml.v3's own decoding follows rules the specification does not
// ("0088" is a float to it, it has no alias-count limit of 100, and it does
// not stop at a second document).

// maxYAMLAliasCount is the alias-count limit of the specification's YAML
// library: an anchor's uses times the aliases inside it may not exceed it.
const maxYAMLAliasCount = 100

// maxYAMLMergedEntries bounds the entries merge keys copy across one
// document. Upstream has no such bound and walks every merge source again
// for each merge, so a few nested merges can be made to cost exponential
// time; real profiles merge a shared block into each proxy, which costs a
// few entries per proxy.
const maxYAMLMergedEntries = 1 << 20

var (
	errYAMLMultipleDocuments = errors.New("parse: YAML source contains multiple documents")
	errYAMLAliasCount        = errors.New("parse: YAML alias count exceeds the limit")
	errYAMLMergeSource       = errors.New("parse: YAML merge sources must be maps or map aliases")
	errYAMLMergeBudget       = errors.New("parse: YAML merge keys copy too many entries")
	errYAMLDuplicateKey      = errors.New("parse: YAML map keys must be unique")
	errYAMLRecursiveAlias    = errors.New("parse: YAML alias refers to its own anchor")
)

// decodeYAML reads one YAML document into model values. A text that fails
// is retried once with every "!<str>" tag and the white space after it read
// as a plain-string marker; a text that still fails is an error.
func decodeYAML(s string) (any, error) {
	v, err := decodeYAMLOnce(s)
	if err != nil && strings.Contains(s, "!<str>") {
		return decodeYAMLOnce(markStrTags(s))
	}
	return v, err
}

// markStrTags replaces "!<str>" and the white space after it with the core
// schema's string tag, which reads the scalar as its source text.
func markStrTags(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "!<str>")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString("!!str ")
		s = strings.TrimLeftFunc(s[i+len("!<str>"):], isESWhiteSpace)
	}
}

func decodeYAMLOnce(s string) (any, error) {
	dec := yaml.NewDecoder(strings.NewReader(s))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errYAMLMultipleDocuments
		}
		return nil, err
	}
	r := yamlResolver{anchors: map[*yaml.Node]*yamlAnchor{}, resolving: map[*yaml.Node]bool{}}
	return r.value(&doc)
}

// yamlAnchor is what the resolver knows about one anchored node: how often it
// was used (counting its definition), the alias weight inside it, and its
// value, which every alias shares.
type yamlAnchor struct {
	count, aliasCount int
	value             any
}

// yamlResolver resolves one document. resolving holds the anchored nodes
// whose value is being built: an alias to one of them would make the value
// contain itself, which upstream's library builds as a circular object that
// JSON.stringify then rejects, so it is an error here.
type yamlResolver struct {
	anchors   map[*yaml.Node]*yamlAnchor
	resolving map[*yaml.Node]bool
	merged    int
}

func (r *yamlResolver) value(n *yaml.Node) (any, error) {
	if n.Anchor != "" {
		r.resolving[n] = true
		defer delete(r.resolving, n)
	}
	var v any
	var err error
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return r.value(n.Content[0])
	case yaml.AliasNode:
		return r.alias(n)
	case yaml.ScalarNode:
		v = yamlScalar(n)
	case yaml.SequenceNode:
		l := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			e, err := r.value(c)
			if err != nil {
				return nil, err
			}
			l = append(l, e)
		}
		v = l
	case yaml.MappingNode:
		v, err = r.mapping(n)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("parse: unknown YAML node kind %d", n.Kind)
	}
	if n.Anchor != "" {
		r.anchors[n] = &yamlAnchor{count: 1, value: v}
	}
	return v, nil
}

// alias returns the shared value of the anchored node and applies the alias
// count limit: each use counts, and an anchor whose content itself holds
// aliases weighs as much as the heaviest of them.
func (r *yamlResolver) alias(n *yaml.Node) (any, error) {
	a, err := r.anchor(n.Alias)
	if err != nil {
		return nil, err
	}
	a.count++
	if a.aliasCount == 0 {
		a.aliasCount = r.aliasWeight(n.Alias)
	}
	if a.count*a.aliasCount > maxYAMLAliasCount {
		return nil, errYAMLAliasCount
	}
	return a.value, nil
}

func (r *yamlResolver) anchor(src *yaml.Node) (*yamlAnchor, error) {
	if src == nil {
		return nil, errors.New("parse: YAML alias without an anchor")
	}
	if a, ok := r.anchors[src]; ok {
		return a, nil
	}
	if r.resolving[src] {
		return nil, errYAMLRecursiveAlias
	}
	if _, err := r.value(src); err != nil {
		return nil, err
	}
	a, ok := r.anchors[src]
	if !ok {
		return nil, errors.New("parse: YAML alias target was not resolved")
	}
	return a, nil
}

// aliasWeight is 1 for a scalar, the heaviest item for a collection (0 when
// empty), and uses times weight of the anchor an alias names.
func (r *yamlResolver) aliasWeight(n *yaml.Node) int {
	switch n.Kind {
	case yaml.AliasNode:
		if a, ok := r.anchors[n.Alias]; ok {
			return a.count * a.aliasCount
		}
		return 0
	case yaml.MappingNode, yaml.SequenceNode, yaml.DocumentNode:
		w := 0
		for _, c := range n.Content {
			if cw := r.aliasWeight(c); cw > w {
				w = cw
			}
		}
		return w
	}
	return 1
}

func (r *yamlResolver) mapping(n *yaml.Node) (map[string]any, error) {
	m := make(map[string]any, len(n.Content)/2)
	seen := make(map[string]bool, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, vn := n.Content[i], n.Content[i+1]
		if isMergeKey(k) {
			if err := r.merge(m, vn); err != nil {
				return nil, err
			}
			continue
		}
		key, err := r.value(k)
		if err != nil {
			return nil, err
		}
		if id, ok := yamlKeyIdentity(key); ok {
			if seen[id] {
				return nil, errYAMLDuplicateKey
			}
			seen[id] = true
		}
		v, err := r.value(vn)
		if err != nil {
			return nil, err
		}
		m[yamlKeyString(key)] = v
	}
	return m, nil
}

// isMergeKey reports a plain "<<" key without an explicit tag.
func isMergeKey(k *yaml.Node) bool {
	return k.Kind == yaml.ScalarNode && k.Tag == "!!merge" && k.Style == 0 && k.Value == "<<"
}

// merge copies the entries of a merge source (a map, an alias to a map, or a
// list of those) that m does not hold yet. Earlier sources win over later
// ones, and keys written in the map itself win over every source.
func (r *yamlResolver) merge(m map[string]any, src *yaml.Node) error {
	sources := []*yaml.Node{src}
	if src.Kind == yaml.SequenceNode {
		sources = src.Content
	}
	for _, s := range sources {
		var v any
		var err error
		if s.Kind == yaml.AliasNode {
			var a *yamlAnchor
			if a, err = r.anchor(s.Alias); err == nil {
				v = a.value
			}
		} else {
			v, err = r.value(s)
		}
		if err != nil {
			return err
		}
		sm, ok := v.(map[string]any)
		if !ok {
			return errYAMLMergeSource
		}
		r.merged += len(sm)
		if r.merged > maxYAMLMergedEntries {
			return errYAMLMergeBudget
		}
		for k, e := range sm {
			if _, exists := m[k]; !exists {
				m[k] = e
			}
		}
	}
	return nil
}

// yamlKeyIdentity is what makes two keys duplicates: the same scalar value
// of the same type. Collections and not-a-number never collide.
func yamlKeyIdentity(k any) (string, bool) {
	switch x := k.(type) {
	case nil:
		return "z", true
	case string:
		return "s" + x, true
	case bool:
		if x {
			return "b1", true
		}
		return "b0", true
	case float64:
		if math.IsNaN(x) {
			return "", false
		}
		if x == 0 {
			return "n0", true // 0 and -0 are the same value
		}
		return "n" + strconv.FormatFloat(x, 'g', -1, 64), true
	}
	return "", false
}

// yamlKeyString is the property name a key becomes on a JavaScript object:
// String(value), with the empty text for null.
func yamlKeyString(k any) string {
	switch x := k.(type) {
	case nil:
		return ""
	case map[string]any, []any:
		return jsonText(x)
	}
	return jsString(k, true)
}

// jsonText is a compact JSON rendering used only to name a collection key.
func jsonText(v any) string {
	var b strings.Builder
	writeJSONText(&b, v)
	return b.String()
}

func writeJSONText(b *strings.Builder, v any) {
	switch x := v.(type) {
	case map[string]any:
		b.WriteByte('{')
		first := true
		for k, e := range x {
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.WriteString(strconv.Quote(k))
			b.WriteByte(':')
			writeJSONText(b, e)
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONText(b, e)
		}
		b.WriteByte(']')
	case string:
		b.WriteString(strconv.Quote(x))
	default:
		b.WriteString(jsString(x, true))
	}
}

// yamlScalar resolves one scalar under the core schema. A scalar with an
// explicit tag the core schema does not resolve (!!str, !<str>, !foo) is
// the text written; a quoted, literal or folded scalar is text.
func yamlScalar(n *yaml.Node) any {
	if n.Style&yaml.TaggedStyle != 0 {
		switch n.Tag {
		case "!!null", "!!bool", "!!int", "!!float":
			if v := yamlPlain(n.Value); yamlTagAccepts(n.Tag, v) {
				return v
			}
		}
		return n.Value
	}
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return n.Value
	}
	return yamlPlain(n.Value)
}

// yamlTagAccepts reports whether a core-schema value has the kind an
// explicit !!null, !!bool, !!int or !!float tag asks for.
func yamlTagAccepts(tag string, v any) bool {
	switch v.(type) {
	case nil:
		return tag == "!!null"
	case bool:
		return tag == "!!bool"
	case float64:
		return tag == "!!int" || tag == "!!float"
	}
	return false
}

// yamlPlain resolves a plain scalar with the YAML 1.2 core schema: null,
// booleans, decimal integers (leading zeros allowed and still decimal),
// 0o octal, 0x hexadecimal, floats, .inf and .nan; anything else is text.
func yamlPlain(s string) any {
	switch s {
	case "", "~", "null", "Null", "NULL":
		return nil
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	case ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF":
		return math.Inf(1)
	case "-.inf", "-.Inf", "-.INF":
		return math.Inf(-1)
	case ".nan", ".NaN", ".NAN":
		return math.NaN()
	}
	if len(s) > 2 && s[0] == '0' && s[1] == 'o' && allIn(s[2:], "01234567") {
		v, _ := strconv.ParseFloat("0x"+octalToHex(s[2:])+"p0", 64)
		return v
	}
	if len(s) > 2 && s[0] == '0' && s[1] == 'x' && allIn(s[2:], "0123456789abcdefABCDEF") {
		v, _ := strconv.ParseFloat("0x"+s[2:]+"p0", 64)
		return v
	}
	if yamlFloatSyntax(s) {
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	return s
}

func allIn(s, set string) bool {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(set, s[i]) < 0 {
			return false
		}
	}
	return s != ""
}

// octalToHex rewrites octal digits as hexadecimal digits of the same value,
// so ParseFloat rounds a long octal literal exactly once.
func octalToHex(oct string) string {
	// Three octal digits are nine bits; pad to a multiple of four bits by
	// building the bit string.
	bits := make([]byte, 0, len(oct)*3)
	for i := 0; i < len(oct); i++ {
		d := oct[i] - '0'
		bits = append(bits, '0'+(d>>2)&1, '0'+(d>>1)&1, '0'+d&1)
	}
	for len(bits)%4 != 0 {
		bits = append([]byte{'0'}, bits...)
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 0, len(bits)/4)
	for i := 0; i < len(bits); i += 4 {
		v := (bits[i]-'0')<<3 | (bits[i+1]-'0')<<2 | (bits[i+2]-'0')<<1 | (bits[i+3] - '0')
		out = append(out, hex[v])
	}
	return string(out)
}

// yamlFloatSyntax is [-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?,
// which covers the core schema's integers and floats.
func yamlFloatSyntax(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	intStart := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	intDigits := i - intStart
	fracDigits := 0
	if i < len(s) && s[i] == '.' {
		i++
		f := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		fracDigits = i - f
	}
	if intDigits == 0 && fracDigits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		e := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == e {
			return false
		}
	}
	return i == len(s)
}
