package operators

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

var (
	truthy = normalise.Truthy
	text   = normalise.Text
	trimES = normalise.TrimES
)

// filterNodes keeps the nodes keep answers true for, in order, reusing the
// backing array: a filter mutates nothing, so the input is not copied.
func filterNodes(nodes []*nodemodel.Node, keep func(*nodemodel.Node) bool) []*nodemodel.Node {
	out := nodes[:0]
	for _, n := range nodes {
		if keep(n) {
			out = append(out, n)
		}
	}
	// The tail still points at dropped nodes; clear it so they can be
	// collected while the chain runs on.
	clear(nodes[len(out):])
	return out
}

// uselessName is upstream's provider-notice test over a name (upstream.md
// 5.2): website, traffic, time, emergency, expired, in Chinese, and two
// English words, case-sensitive as the bundle matches them.
var uselessName = regexp.MustCompile(`网址|流量|时间|应急|过期|Bandwidth|expire`)

// useless reports whether a node is a provider notice rather than a server:
// its name matches uselessName, or its cipher, password or transport Host
// header carries a character beyond ASCII.
func useless(n *nodemodel.Node) bool {
	if uselessName.MatchString(n.Name()) {
		return true
	}
	for _, path := range [][]string{{"cipher"}, {"password"}, {textOf(n.Fields, "network") + "-opts", "headers", "Host"}} {
		if v, ok := n.Get(path...); ok && !isASCII(text(v)) {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// textOf is String(m[k]), "undefined" when the key is absent.
func textOf(m map[string]any, k string) string {
	v, ok := m[k]
	if !ok {
		return "undefined"
	}
	return text(v)
}

// compileUselessFilter drops provider notices. It takes no arguments and
// ignores any it is given, as the bundle does.
func compileUselessFilter(_ *stepCompiler, _ json.RawMessage) stepFunc {
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		return filterNodes(nodes, func(n *nodemodel.Node) bool { return !useless(n) })
	}
}

// validPort is the Quick Setting useless check's port test: an integer
// from 1 to 65535.
func validPort(n *nodemodel.Node) bool {
	v, ok := n.Get("port")
	if !ok {
		return false
	}
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case int64:
		f = float64(x)
	case int:
		f = float64(x)
	default:
		return false
	}
	return !math.IsNaN(f) && f == math.Trunc(f) && f >= 1 && f <= 65535
}

// regionFlags are the regions the Region Filter offers (upstream.md 5.3)
// and the flag each one stands for. Any other code matches nothing.
var regionFlags = map[string]string{
	"HK": "🇭🇰",
	"TW": "🇹🇼",
	"SG": "🇸🇬",
	"JP": "🇯🇵",
	"UK": "🇬🇧",
	"US": "🇺🇸",
	"DE": "🇩🇪",
	"KR": "🇰🇷",
}

// compileRegionFilter keeps (or with keep false drops) the nodes whose name
// carries one of the chosen regions, by the flag the Flag Operator would
// give the name (before its Taiwan choice). Arguments: {"value": [...],
// "keep"} or the bare list.
func compileRegionFilter(c *stepCompiler, raw json.RawMessage) stepFunc {
	v, _, err := decodeArgs(raw)
	if err != nil {
		return c.shape("arguments are not JSON: %v", err)
	}
	keep := true
	list := v
	if obj, ok := v.(map[string]any); ok {
		list = obj["value"]
		keep = keepArg(obj, true)
	}
	regions, ok := stringList(list)
	if !ok {
		return c.shape("regions must be a list of region codes")
	}
	flags := map[string]bool{}
	for _, r := range regions {
		if f, ok := regionFlags[r]; ok {
			flags[f] = true
		}
	}
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		return filterNodes(nodes, func(n *nodemodel.Node) bool { return flags[nameFlag(n.Name())] == keep })
	}
}

// compileTypeFilter keeps (or drops) the nodes of the chosen types.
// Arguments: {"value": [...], "keep"} or the bare list.
func compileTypeFilter(c *stepCompiler, raw json.RawMessage) stepFunc {
	v, _, err := decodeArgs(raw)
	if err != nil {
		return c.shape("arguments are not JSON: %v", err)
	}
	keep := true
	list := v
	if obj, ok := v.(map[string]any); ok {
		list = obj["value"]
		keep = keepArg(obj, true)
	}
	types, ok := stringList(list)
	if !ok {
		return c.shape("types must be a list of node types")
	}
	set := make(map[string]bool, len(types))
	for _, t := range types {
		set[t] = true
	}
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		return filterNodes(nodes, func(n *nodemodel.Node) bool {
			t, isText := n.String("type")
			return (isText && set[t]) == keep
		})
	}
}

// compileRegexFilter keeps (or drops) the nodes whose name any pattern
// matches. Arguments: {"regex": [...], "keep"}; a leading (?i) makes one
// pattern case-insensitive.
func compileRegexFilter(c *stepCompiler, raw json.RawMessage) stepFunc {
	args, ok := argsObject(c, raw)
	if !ok {
		return nil
	}
	var patterns []string
	if v, present := args["regex"]; present {
		if patterns, ok = stringList(v); !ok {
			return c.shape("regex must be a list of patterns")
		}
	}
	keep := keepArg(args, false)
	res := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if re := c.compilePattern(p); re != nil {
			res = append(res, re)
		} else if keep && len(patterns) == 1 {
			// Only a single-pattern keep filter is the idiom: beside other
			// patterns, turning the step into a drop filter would change
			// what the rest of them keep.
			if drop, ok := RewriteNegativeLookahead(p); ok {
				c.diags[len(c.diags)-1].Rewrite = drop
			}
		}
	}
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		return filterNodes(nodes, func(n *nodemodel.Node) bool {
			name := n.Name()
			for _, re := range res {
				if re.MatchString(name) {
					return keep
				}
			}
			return !keep
		})
	}
}

// compileRemoveDuplicateFilter keeps every node. The pinned bundle
// (2.36.22) keeps every node under this step whatever its arguments,
// identical nodes included, and S1 changes no output; dropping repeats is
// Handle Duplicate's delete action.
func compileRemoveDuplicateFilter(_ *stepCompiler, _ json.RawMessage) stepFunc {
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node { return nodes }
}

// Conditional Filter: a tree of {"operator": "AND"|"OR", "child": [...]},
// {"operator": "NOT", "child": {...}} and leaves {"proposition", "attr",
// "value"} with IN, CONTAINS, EQUALS and EXISTS over a top-level field.

type condition func(n *nodemodel.Node) bool

// compileConditionalFilter keeps the nodes the rule holds for. Arguments:
// {"rule": tree}. A tree the bundle would refuse falls back to it.
func compileConditionalFilter(c *stepCompiler, raw json.RawMessage) stepFunc {
	args, ok := argsObject(c, raw)
	if !ok {
		return nil
	}
	cond, msg := compileCondition(args["rule"], 0)
	if cond == nil {
		return c.shape("rule: %s", msg)
	}
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		return filterNodes(nodes, cond)
	}
}

// maxConditionDepth bounds a rule tree. A stored record is bounded in bytes,
// but nothing else stops a deep tree from costing a deep recursion per node.
const maxConditionDepth = 64

func compileCondition(v any, depth int) (condition, string) {
	if depth > maxConditionDepth {
		return nil, "the tree is nested too deeply"
	}
	rule, ok := v.(map[string]any)
	if !ok {
		return nil, "every node of the tree must be an object"
	}
	if op, present := rule["operator"]; present && truthy(op) {
		switch op {
		case "AND", "OR":
			list, ok := rule["child"].([]any)
			if !ok {
				return nil, "AND and OR take a list of children"
			}
			children := make([]condition, len(list))
			for i, child := range list {
				var msg string
				if children[i], msg = compileCondition(child, depth+1); children[i] == nil {
					return nil, msg
				}
			}
			if op == "AND" {
				return func(n *nodemodel.Node) bool {
					for _, child := range children {
						if !child(n) {
							return false
						}
					}
					return true
				}, ""
			}
			return func(n *nodemodel.Node) bool {
				for _, child := range children {
					if child(n) {
						return true
					}
				}
				return false
			}, ""
		case "NOT":
			child, msg := compileCondition(rule["child"], depth+1)
			if child == nil {
				return nil, msg
			}
			return func(n *nodemodel.Node) bool { return !child(n) }, ""
		}
		return nil, "unknown operator"
	}
	attr, ok := rule["attr"].(string)
	if !ok {
		return nil, "a proposition names its field in attr"
	}
	value, hasValue := rule["value"]
	switch rule["proposition"] {
	case "EQUALS":
		return func(n *nodemodel.Node) bool {
			got, present := n.Fields[attr]
			if !present || !hasValue {
				return !present && !hasValue
			}
			return strictEqual(got, value)
		}, ""
	case "IN":
		switch in := value.(type) {
		case []any:
			return func(n *nodemodel.Node) bool {
				got, present := n.Fields[attr]
				if !present {
					return false
				}
				for _, e := range in {
					if strictEqual(got, e) {
						return true
					}
				}
				return false
			}, ""
		case string:
			// String.prototype.indexOf reads its argument with String(), an
			// absent field included.
			return func(n *nodemodel.Node) bool { return strings.Contains(in, textOf(n.Fields, attr)) }, ""
		}
		return nil, "IN takes a list or a text"
	case "CONTAINS":
		needle := "undefined"
		if hasValue {
			if _, isObject := value.(map[string]any); isObject {
				return nil, "CONTAINS takes a text"
			}
			needle = text(value)
		}
		return func(n *nodemodel.Node) bool {
			s, isText := n.String(attr)
			return isText && strings.Contains(s, needle)
		}, ""
	case "EXISTS":
		// Upstream's EXISTS is true for every node (upstream.md 5.6); the
		// native test is the one it meant: the field is present and not
		// null (design-28.md:168).
		return func(n *nodemodel.Node) bool {
			got, present := n.Fields[attr]
			return present && got != nil
		}, ""
	}
	return nil, "unknown proposition"
}

// strictEqual is ECMAScript's === over model values: texts, numbers and
// booleans by value, null equal to null, and an object or a list equal to
// nothing (=== compares them by identity, and no stored value is the node's
// own object).
func strictEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	}
	fa, aNum := number(a)
	fb, bNum := number(b)
	return aNum && bNum && fa == fb
}

// number reads a model number.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}
