package operators

import (
	"encoding/json"
	"math/rand/v2"
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// compareNames orders two names as ECMAScript's < orders strings: by UTF-16
// code unit, which differs from code point order where a character above
// U+FFFF meets one from U+E000 to U+FFFF.
func compareNames(a, b string) int {
	for a != "" && b != "" {
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if ra != rb {
			ua, ub := firstUnit(ra), firstUnit(rb)
			if ua != ub {
				if ua < ub {
					return -1
				}
				return 1
			}
			// Same high surrogate: the low surrogates order as the code
			// points do.
			if ra < rb {
				return -1
			}
			return 1
		}
		a, b = a[na:], b[nb:]
	}
	switch {
	case a == b:
		return 0
	case a == "":
		return -1
	}
	return 1
}

// firstUnit is a rune's first UTF-16 code unit.
func firstUnit(r rune) rune {
	if r >= 0x10000 {
		return 0xd800 + (r-0x10000)>>10
	}
	return r
}

// sortByName orders nodes by name, ascending or descending. Nodes with
// equal names keep their input order. The bundle's comparator never answers
// "equal" for them, so there their order is whatever QuickJS's unstable sort
// leaves; here it is the input order.
func sortByName(nodes []*nodemodel.Node, desc bool) {
	slices.SortStableFunc(nodes, func(a, b *nodemodel.Node) int {
		c := compareNames(a.Name(), b.Name())
		if desc {
			return -c
		}
		return c
	})
}

// compileSort orders the node list (upstream.md 5.9). Arguments: the bare
// text "asc", "desc" or "random"; the bundle refuses anything else, so
// anything else falls back to it.
func compileSort(c *stepCompiler, raw json.RawMessage) stepFunc {
	v, _, err := decodeArgs(raw)
	if err != nil {
		return c.shape("arguments are not JSON: %v", err)
	}
	switch v {
	case "asc", "desc":
		desc := v == "desc"
		return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
			sortByName(nodes, desc)
			return nodes
		}
	case "random":
		return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
			rand.Shuffle(len(nodes), func(i, j int) { nodes[i], nodes[j] = nodes[j], nodes[i] })
			return nodes
		}
	}
	return c.shape(`the order must be "asc", "desc" or "random"`)
}

// compileRegexSort puts nodes matching earlier patterns first, each group in
// input order, and the rest after them, ordered by name ("asc", the
// default, or "desc") or kept in input order ("original") (upstream.md
// 5.10). Arguments: the bare list of patterns, or {"expressions": [...],
// "order"}.
func compileRegexSort(c *stepCompiler, raw json.RawMessage) stepFunc {
	v, _, err := decodeArgs(raw)
	if err != nil {
		return c.shape("arguments are not JSON: %v", err)
	}
	order := "asc"
	list := v
	if obj, ok := v.(map[string]any); ok {
		list = obj["expressions"]
		if list == nil {
			list = []any{}
		}
		if o, present := obj["order"]; present {
			s, isText := o.(string)
			if !isText {
				return c.shape(`order must be "asc", "desc" or "original"`)
			}
			order = s
		}
	}
	switch order {
	case "asc", "desc", "original":
	default:
		return c.shape(`order must be "asc", "desc" or "original"`)
	}
	patterns, ok := stringList(list)
	if !ok {
		return c.shape("expressions must be a list of patterns")
	}
	res := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		res = append(res, c.compilePattern(p))
	}
	if c.fallback() {
		return nil
	}
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		rank := make(map[*nodemodel.Node]int, len(nodes))
		for _, n := range nodes {
			name := n.Name()
			rank[n] = len(res)
			for i, re := range res {
				if re.MatchString(name) {
					rank[n] = i
					break
				}
			}
		}
		slices.SortStableFunc(nodes, func(a, b *nodemodel.Node) int {
			ra, rb := rank[a], rank[b]
			if ra != rb {
				return ra - rb
			}
			if ra < len(res) {
				return 0
			}
			switch order {
			case "asc":
				return compareNames(a.Name(), b.Name())
			case "desc":
				return compareNames(b.Name(), a.Name())
			}
			return 0
		})
		return nodes
	}
}
