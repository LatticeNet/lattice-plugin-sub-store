// Package nodemodel is the parsed proxy model the native engine passes from
// the parsers through the normaliser and the operators to the producers.
//
// A Node is the JSON object upstream's parse produces, held as a map. The
// parse goldens compare deep equality over well over a hundred distinct
// top-level keys, with absent, empty and null all distinct, and Clash objects
// pass unknown keys through untouched, so the map is the model and the typed
// accessors below are views over it (design 28's struct family is recorded as
// a deliberate departure in the S1 plan, section 7 decision 1).
//
// Nothing in this package imports package main, the SDK or the script engine.
package nodemodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Node is one parsed and normalised proxy. Fields holds exactly the JSON
// object upstream's parse produces: string, float64 (or int64 after
// normalisation), bool, nil (JSON null), []any and map[string]any values.
// Script holds the underscore fields scripts set (S3); producers never write
// them. Lattice holds the fleet fields (S2) and is nil in S1.
type Node struct {
	Fields  map[string]any
	Script  map[string]any
	Lattice *LatticeFields
}

// latticeKey is where Lattice travels on the wire. It is reserved: a value
// under this key in Fields is never written, so the wire form cannot carry
// two fleet blocks.
const latticeKey = "_lattice"

// yagni: every accessor reads Fields on each call. A typed cache of the hot
// fields (name, type, server, port) is the upgrade path if the S1 perf gate
// (BenchmarkPipelineReality4096SingBox) shows map lookups dominate; until then
// the map stays the single source of truth and there is nothing to keep in
// sync.

// Name is the node's name, or "" when it is absent or not text.
func (n *Node) Name() string {
	s, _ := n.String("name")
	return s
}

// SetName writes the name.
func (n *Node) SetName(name string) { n.Set(name, "name") }

// Type is the node's type, or "" when it is absent or not text.
func (n *Node) Type() string {
	s, _ := n.String("type")
	return s
}

// Server is the node's server, or "" when it is absent or not text.
func (n *Node) Server() string {
	s, _ := n.String("server")
	return s
}

// Port is the node's port when it is an integral number. Not-a-number (the
// value the normaliser stores for an empty port text), a fraction, text and
// absence all give ok=false.
func (n *Node) Port() (int, bool) {
	v, ok := n.Get("port")
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x != math.Trunc(x) {
			return 0, false
		}
		return int(x), true
	case int64:
		return int(x), true
	case int:
		return x, true
	}
	return 0, false
}

// Bool reads a top-level boolean. ok is false when the key is absent or holds
// another type.
func (n *Node) Bool(key string) (value, ok bool) {
	v, present := n.Get(key)
	if !present {
		return false, false
	}
	b, isBool := v.(bool)
	return b, isBool
}

// String reads a top-level text. ok is false when the key is absent or holds
// another type.
func (n *Node) String(key string) (value string, ok bool) {
	v, present := n.Get(key)
	if !present {
		return "", false
	}
	s, isString := v.(string)
	return s, isString
}

// Get reads a value by path, for example Get("reality-opts", "public-key").
// Every element but the last must name an object. An empty path reads
// nothing.
func (n *Node) Get(path ...string) (any, bool) {
	if n == nil || len(path) == 0 {
		return nil, false
	}
	m := n.Fields
	for _, key := range path[:len(path)-1] {
		next, ok := m[key].(map[string]any)
		if !ok {
			return nil, false
		}
		m = next
	}
	v, ok := m[path[len(path)-1]]
	return v, ok
}

// Set writes a value by path. Missing objects along the path are created, and
// an element that holds something other than an object is replaced by one.
// An empty path writes nothing.
func (n *Node) Set(value any, path ...string) {
	if len(path) == 0 {
		return
	}
	if n.Fields == nil {
		n.Fields = map[string]any{}
	}
	m := n.Fields
	for _, key := range path[:len(path)-1] {
		next, ok := m[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[key] = next
		}
		m = next
	}
	m[path[len(path)-1]] = value
}

// Delete removes the value at path. A path through something other than an
// object removes nothing.
func (n *Node) Delete(path ...string) {
	if n == nil || len(path) == 0 {
		return
	}
	m := n.Fields
	for _, key := range path[:len(path)-1] {
		next, ok := m[key].(map[string]any)
		if !ok {
			return
		}
		m = next
	}
	delete(m, path[len(path)-1])
}

// Clone is a deep copy. Operators mutate nodes in place and clone only here.
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	out := &Node{}
	if n.Fields != nil {
		out.Fields = cloneValue(n.Fields).(map[string]any)
	}
	if n.Script != nil {
		out.Script = cloneValue(n.Script).(map[string]any)
	}
	if n.Lattice != nil {
		l := *n.Lattice
		out.Lattice = &l
	}
	return out
}

// CloneValue deep-copies one model value: objects and lists are copied, every
// other value is returned as it is.
func CloneValue(v any) any { return cloneValue(v) }

func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = cloneValue(e)
		}
		return m
	case []any:
		l := make([]any, len(x))
		for i, e := range x {
			l[i] = cloneValue(e)
		}
		return l
	}
	return v
}

// MarshalJSON writes Fields with keys sorted at every depth, then Lattice
// under "_lattice" when present (in its sorted position, so the whole object
// stays sorted). Script is never written. Numbers are written as ECMAScript's
// Number::toString writes them (integers below 1e21 without exponent, -0 as
// 0); not-a-number and the infinities are written as null, as JSON.stringify
// does. Text is escaped as JSON.stringify escapes it, so "<", ">", "&",
// U+2028 and U+2029 stay literal; an invalid UTF-8 byte becomes U+FFFD.
func (n *Node) MarshalJSON() ([]byte, error) {
	if n == nil {
		return []byte("null"), nil
	}
	fields := n.Fields
	if n.Lattice != nil || hasKey(fields, latticeKey) {
		fields = make(map[string]any, len(n.Fields)+1)
		for k, v := range n.Fields {
			if k != latticeKey {
				fields[k] = v
			}
		}
		if n.Lattice != nil {
			lattice, err := latticeValue(n.Lattice)
			if err != nil {
				return nil, err
			}
			if lattice != nil {
				fields[latticeKey] = lattice
			}
		}
	}
	if fields == nil {
		fields = map[string]any{}
	}
	return appendValue(nil, fields, 0)
}

// UnmarshalJSON sets Fields from a JSON object; "_lattice" moves to Lattice.
// Numbers decode as float64, which is how ECMAScript reads them (so a
// twenty-digit number keeps the value of the nearest double). Script is left
// as it is: the wire form never carries it.
func (n *Node) UnmarshalJSON(data []byte) error {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("nodemodel: a node must be a JSON object")
	}
	n.Lattice = nil
	if raw, ok := fields[latticeKey]; ok {
		delete(fields, latticeKey)
		if raw != nil {
			if _, isObject := raw.(map[string]any); !isObject {
				return errors.New("nodemodel: _lattice must be a JSON object")
			}
			encoded, err := json.Marshal(raw)
			if err != nil {
				return err
			}
			var l LatticeFields
			if err := json.Unmarshal(encoded, &l); err != nil {
				return fmt.Errorf("nodemodel: _lattice: %w", err)
			}
			n.Lattice = &l
		}
	}
	n.Fields = fields
	return nil
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// latticeValue turns the fleet block into a model object, so it is written by
// the same sorted writer and new S2 fields need no writer change. An empty
// block gives nil and is not written.
func latticeValue(l *LatticeFields) (map[string]any, error) {
	encoded, err := json.Marshal(l)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(encoded, &m); err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, nil
	}
	return m, nil
}

// maxWriteDepth stops a cyclic value (a map that contains itself) from
// recursing forever. Bounds refuses nodes far shallower than this.
const maxWriteDepth = 1000

func appendValue(dst []byte, v any, depth int) ([]byte, error) {
	if depth > maxWriteDepth {
		return nil, errors.New("nodemodel: value nested too deeply to write")
	}
	switch x := v.(type) {
	case nil:
		return append(dst, "null"...), nil
	case bool:
		if x {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case string:
		return AppendJSONString(dst, x), nil
	case float64:
		return AppendNumber(dst, x), nil
	case int64:
		return strconv.AppendInt(dst, x, 10), nil
	case int:
		return strconv.AppendInt(dst, int64(x), 10), nil
	case []any:
		dst = append(dst, '[')
		for i, e := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			if dst, err = appendValue(dst, e, depth+1); err != nil {
				return nil, err
			}
		}
		return append(dst, ']'), nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		dst = append(dst, '{')
		for i, k := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = AppendJSONString(dst, k)
			dst = append(dst, ':')
			var err error
			if dst, err = appendValue(dst, x[k], depth+1); err != nil {
				return nil, err
			}
		}
		return append(dst, '}'), nil
	}
	return nil, fmt.Errorf("nodemodel: value of type %T is not part of the model", v)
}

// AppendNumber appends f as ECMAScript's Number::toString writes it, which is
// what JSON.stringify emits: the shortest digits that read back as f, plain
// notation for exponents from -7 to 20, exponent notation with an explicit
// sign beyond that ("1e+21", "1e-7"), and "0" for negative zero.
// Not-a-number and the infinities are written as null.
func AppendNumber(dst []byte, f float64) []byte {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(dst, "null"...)
	}
	if f == 0 {
		return append(dst, '0')
	}
	if f < 0 {
		dst = append(dst, '-')
		f = -f
	}
	var buf [32]byte
	sci := strconv.AppendFloat(buf[:0], f, 'e', -1, 64) // d[.ddd]e±xx
	e := bytes.IndexByte(sci, 'e')
	exp, _ := strconv.Atoi(string(sci[e+1:]))
	digits := make([]byte, 0, e)
	for _, c := range sci[:e] {
		if c != '.' {
			digits = append(digits, c)
		}
	}
	k := len(digits)
	point := exp + 1 // the value is 0.digits times ten to the point
	switch {
	case k <= point && point <= 21:
		dst = append(dst, digits...)
		for i := k; i < point; i++ {
			dst = append(dst, '0')
		}
	case 0 < point && point <= 21:
		dst = append(dst, digits[:point]...)
		dst = append(dst, '.')
		dst = append(dst, digits[point:]...)
	case -6 < point && point <= 0:
		dst = append(dst, '0', '.')
		for i := point; i < 0; i++ {
			dst = append(dst, '0')
		}
		dst = append(dst, digits...)
	default:
		dst = append(dst, digits[0])
		if k > 1 {
			dst = append(dst, '.')
			dst = append(dst, digits[1:]...)
		}
		dst = append(dst, 'e')
		if point-1 >= 0 {
			dst = append(dst, '+')
		}
		dst = strconv.AppendInt(dst, int64(point-1), 10)
	}
	return dst
}

// FormatNumber is AppendNumber into a new string. It is also ECMAScript's
// String(number), except that not-a-number and the infinities give "null".
func FormatNumber(f float64) string { return string(AppendNumber(nil, f)) }

// AppendJSONString appends s quoted as JSON.stringify quotes it: `"` and `\`
// escaped, \b \f \n \r \t in short form, every other control character as
// \u00xx, everything else literal. An invalid UTF-8 byte is written as U+FFFD.
func AppendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				dst = append(dst, s[start:i]...)
				dst = append(dst, "�"...)
				i++
				start = i
				continue
			}
			i += size
			continue
		}
		if c >= 0x20 && c != '"' && c != '\\' {
			i++
			continue
		}
		dst = append(dst, s[start:i]...)
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			const hex = "0123456789abcdef"
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
		}
		i++
		start = i
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}
