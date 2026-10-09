package operators

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// triState reads a Quick Setting switch: "ENABLED" sets the field true,
// "DISABLED" sets it false, and anything else (absent, "DEFAULT", a
// boolean from a record the old editor wrote) leaves the node alone, as the
// bundle reads it (ui/src/commonSettings.ts).
func triState(args map[string]any, key string) (value, set bool) {
	switch args[key] {
	case "ENABLED":
		return true, true
	case "DISABLED":
		return false, true
	}
	return false, false
}

// quickSettingTypes are the node types a type-scoped switch applies to.
var quickSettingTypes = map[string]map[string]bool{
	"vmess aead": {"vmess": true},
	"reuse":      {"snell": true, "anytls": true, "trusttunnel": true},
	"ecn":        {"tuic": true, "hysteria2": true},
}

// blockQUICValues are the values block-quic is written with; any other value
// writes nothing.
var blockQUICValues = map[string]bool{"auto": true, "on": true, "off": true}

// compileQuickSetting forces switches on every node (upstream.md 5.1):
// udp, tfo (as tfo and fast-open), scert (skip-cert-verify), vmess aead
// (aead, VMess only), reuse (snell, anytls, trusttunnel), ecn (tuic,
// hysteria2), block-quic (auto, on or off), ip-version (any text but
// "DEFAULT"), and useless "ENABLED", which runs the Useless Filter and drops
// ports outside 1 to 65535.
func compileQuickSetting(c *stepCompiler, raw json.RawMessage) stepFunc {
	args, ok := argsObject(c, raw)
	if !ok {
		return nil
	}
	type assignment struct {
		keys  []string
		value any
		types map[string]bool // nil: every node
	}
	var set []assignment
	for _, sw := range []struct {
		arg  string
		keys []string
	}{
		{"udp", []string{"udp"}},
		{"tfo", []string{"tfo", "fast-open"}},
		{"scert", []string{"skip-cert-verify"}},
		{"vmess aead", []string{"aead"}},
		{"reuse", []string{"reuse"}},
		{"ecn", []string{"ecn"}},
	} {
		if v, ok := triState(args, sw.arg); ok {
			set = append(set, assignment{keys: sw.keys, value: v, types: quickSettingTypes[sw.arg]})
		}
	}
	if v, ok := args["block-quic"].(string); ok && blockQUICValues[v] {
		set = append(set, assignment{keys: []string{"block-quic"}, value: v})
	}
	if v, present := args["ip-version"]; present && truthy(v) && v != "DEFAULT" {
		set = append(set, assignment{keys: []string{"ip-version"}, value: v})
	}
	dropUseless := args["useless"] == "ENABLED"
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		if dropUseless {
			nodes = filterNodes(nodes, func(n *nodemodel.Node) bool { return !useless(n) && validPort(n) })
		}
		for _, n := range nodes {
			t := n.Type()
			for _, a := range set {
				if a.types != nil && !a.types[t] {
					continue
				}
				for _, k := range a.keys {
					// A copy per node: no two nodes share a value a later
					// step could change in one of them.
					n.Fields[k] = nodemodel.CloneValue(a.value)
				}
			}
		}
		return nodes
	}
}

// compileFlag adds the country flag in front of each name, or with mode
// "remove" strips flags (upstream.md 5.8). tw decides the Taiwan flag: by
// default it becomes the China flag, "ws" the Samoa flag, "tw" keeps it.
func compileFlag(c *stepCompiler, raw json.RawMessage) stepFunc {
	args, ok := argsObject(c, raw)
	if !ok {
		return nil
	}
	if args["mode"] == "remove" {
		return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
			for _, n := range nodes {
				n.SetName(removeFlags(n.Name()))
			}
			return nodes
		}
	}
	taiwan := "🇨🇳"
	switch args["tw"] {
	case "ws":
		taiwan = "🇼🇸"
	case "tw":
		taiwan = "🇹🇼"
	}
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		for _, n := range nodes {
			name := n.Name()
			flag := nameFlag(name)
			if flag == "🇹🇼" {
				flag = taiwan
			}
			n.SetName(flag + " " + removeFlags(name))
		}
		return nodes
	}
}

// rename is one compiled Regex Rename rule.
type rename struct {
	re  *regexp.Regexp
	now string
}

// renameName applies each rule in order: every match replaced, the result
// trimmed (upstream.md 5.11).
func renameName(name string, rules []rename) string {
	for _, r := range rules {
		name = trimES(replaceAll(r.re, name, r.now))
	}
	return name
}

func renameStep(rules []rename) stepFunc {
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		for _, n := range nodes {
			n.SetName(renameName(n.Name(), rules))
		}
		return nodes
	}
}

// compileRegexRename rewrites names. Arguments: the bare list
// [{"expr", "now"}]; "now" is a replacement with ECMAScript's $ forms
// ($1, $&, $<name>, $$ and the rest); an absent "now" is the text
// "undefined", as String.prototype.replace reads it.
func compileRegexRename(c *stepCompiler, raw json.RawMessage) stepFunc {
	v, _, err := decodeArgs(raw)
	if err != nil {
		return c.shape("arguments are not JSON: %v", err)
	}
	list, ok := v.([]any)
	if !ok {
		return c.shape("arguments must be a list of {expr, now}")
	}
	rules := make([]rename, 0, len(list))
	for _, e := range list {
		pair, ok := e.(map[string]any)
		if !ok {
			return c.shape("each rename is an object {expr, now}")
		}
		expr, ok := pair["expr"].(string)
		if !ok {
			return c.shape("each rename names its pattern in expr")
		}
		now := "undefined"
		if v, present := pair["now"]; present {
			if _, isObject := v.(map[string]any); isObject {
				return c.shape("now must be a text")
			}
			now = text(v)
		}
		rules = append(rules, rename{re: c.compilePattern(expr), now: now})
	}
	if c.fallback() {
		return nil
	}
	return renameStep(rules)
}

// compileRegexDelete strips matches out of names: a rename to nothing per
// pattern (upstream.md 5.12). Arguments: the bare list of patterns.
func compileRegexDelete(c *stepCompiler, raw json.RawMessage) stepFunc {
	v, _, err := decodeArgs(raw)
	if err != nil {
		return c.shape("arguments are not JSON: %v", err)
	}
	patterns, ok := stringList(v)
	if !ok {
		return c.shape("arguments must be a list of patterns")
	}
	rules := make([]rename, 0, len(patterns))
	for _, p := range patterns {
		rules = append(rules, rename{re: c.compilePattern(p)})
	}
	if c.fallback() {
		return nil
	}
	return renameStep(rules)
}

// replaceAll is String.prototype.replace with a global pattern: every
// match replaced, the replacement read with GetSubstitution's $ forms. Go's
// own Expand differs ($1x names a group "1x" there, and $& and $$ are not
// forms), so the substitution is written out here.
func replaceAll(re *regexp.Regexp, s, repl string) string {
	matches := re.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}
	simple := !strings.Contains(repl, "$")
	var b strings.Builder
	last := 0
	for _, m := range matches {
		b.WriteString(s[last:m[0]])
		if simple {
			b.WriteString(repl)
		} else {
			substitute(&b, re, s, m, repl)
		}
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// substitute writes repl for one match m of re in s, as ECMAScript's
// GetSubstitution does: $$ is $, $& the match, $` the text before it, $' the
// text after it, $n and $nn a capture (two digits when that names a group,
// else one; a group that took no part is empty text), $<name> a named
// capture when the pattern names any, and every other $ is itself.
func substitute(b *strings.Builder, re *regexp.Regexp, s string, m []int, repl string) {
	groups := len(m)/2 - 1
	named := false
	for _, name := range re.SubexpNames() {
		if name != "" {
			named = true
			break
		}
	}
	capture := func(i int) {
		if m[2*i] >= 0 {
			b.WriteString(s[m[2*i]:m[2*i+1]])
		}
	}
	for i := 0; i < len(repl); i++ {
		ch := repl[i]
		if ch != '$' || i+1 == len(repl) {
			b.WriteByte(ch)
			continue
		}
		next := repl[i+1]
		switch {
		case next == '$':
			b.WriteByte('$')
			i++
		case next == '&':
			b.WriteString(s[m[0]:m[1]])
			i++
		case next == '`':
			b.WriteString(s[:m[0]])
			i++
		case next == '\'':
			b.WriteString(s[m[1]:])
			i++
		case next >= '0' && next <= '9':
			one := int(next - '0')
			if i+2 < len(repl) && repl[i+2] >= '0' && repl[i+2] <= '9' {
				if two := one*10 + int(repl[i+2]-'0'); two >= 1 && two <= groups {
					capture(two)
					i += 2
					continue
				}
			}
			if one >= 1 && one <= groups {
				capture(one)
				i++
				continue
			}
			b.WriteByte('$')
		case next == '<' && named:
			end := strings.IndexByte(repl[i+2:], '>')
			if end < 0 {
				b.WriteByte('$')
				continue
			}
			if idx := re.SubexpIndex(repl[i+2 : i+2+end]); idx > 0 {
				capture(idx)
			}
			i += 2 + end
		default:
			b.WriteByte('$')
		}
	}
}

// compileHandleDuplicate deals with nodes that share a key (upstream.md
// 5.13). Arguments: {action, template, link, position, field}. The key is
// each field path read with a default of "-", joined with "_" as
// Array.prototype.join writes values (null becomes empty text). action
// "delete" keeps the first node of each key; "rename" (the default)
// numbers every node of a repeated key from 1, zero-padded to the width of
// the largest count, each digit written with the template's glyph, joined
// to the name with link (default "-") at the back or, with position
// "front", in front; any other action leaves the list alone.
func compileHandleDuplicate(c *stepCompiler, raw json.RawMessage) stepFunc {
	args, ok := argsObject(c, raw)
	if !ok {
		return nil
	}
	fields := []string{"name"}
	if v, present := args["field"]; present {
		if fields, ok = stringList(v); !ok {
			return c.shape("field must be a list of field paths")
		}
	}
	paths := make([][]string, len(fields))
	for i, f := range fields {
		if paths[i], ok = fieldPath(f); !ok {
			return c.shape("field path %q is not one this operator reads", f)
		}
	}
	keyOf := func(n *nodemodel.Node) string {
		parts := make([]string, len(paths))
		for i, p := range paths {
			// lodash reads a path that is itself a key of the object as that
			// key before splitting it at the dots.
			v, ok := n.Fields[fields[i]]
			if !ok && len(p) > 1 {
				v, ok = n.Get(p...)
			}
			switch {
			case !ok:
				parts[i] = "-"
			case v != nil:
				parts[i] = text(v)
			}
		}
		return strings.Join(parts, "_")
	}
	action := "rename"
	if v, present := args["action"]; present {
		s, isText := v.(string)
		if !isText {
			return c.shape("action must be a text")
		}
		action = s
	}
	switch action {
	case "delete":
		return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
			seen := make(map[string]bool, len(nodes))
			return filterNodes(nodes, func(n *nodemodel.Node) bool {
				k := keyOf(n)
				if seen[k] {
					return false
				}
				seen[k] = true
				return true
			})
		}
	case "rename":
	default:
		return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node { return nodes }
	}
	template := strings.Split("0 1 2 3 4 5 6 7 8 9", " ")
	if v, present := args["template"]; present {
		s, isText := v.(string)
		if !isText {
			return c.shape("template must be a text")
		}
		template = strings.Split(s, " ")
	}
	link := "-"
	if v, present := args["link"]; present {
		if _, isObject := v.(map[string]any); isObject {
			return c.shape("link must be a text")
		}
		link = text(v)
	}
	front := args["position"] == "front"
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		keys := make([]string, len(nodes))
		counts := map[string]int{}
		widest := 0
		for i, n := range nodes {
			keys[i] = keyOf(n)
			counts[keys[i]]++
			widest = max(widest, counts[keys[i]])
		}
		width := len(strconv.Itoa(widest))
		seen := map[string]int{}
		for i, n := range nodes {
			if counts[keys[i]] < 2 {
				continue
			}
			seen[keys[i]]++
			digits := strconv.Itoa(seen[keys[i]])
			digits = strings.Repeat("0", width-len(digits)) + digits
			var counter strings.Builder
			for _, d := range digits {
				if idx := int(d - '0'); idx < len(template) {
					counter.WriteString(template[idx])
				} else {
					counter.WriteString("undefined")
				}
			}
			if front {
				n.SetName(counter.String() + link + n.Name())
			} else {
				n.SetName(n.Name() + link + counter.String())
			}
		}
		return nodes
	}
}

// fieldPath reads a lodash property path in its dotted form ("a.b"). A
// path with brackets is not read here (ok=false) and the step falls back.
func fieldPath(path string) ([]string, bool) {
	if strings.ContainsAny(path, "[]") {
		return nil, false
	}
	return strings.Split(path, "."), true
}
