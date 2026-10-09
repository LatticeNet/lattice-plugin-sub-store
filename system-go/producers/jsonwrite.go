package producers

import (
	"fmt"
	"slices"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// object is a JSON object whose keys are written in the order they were
// first set, as an ECMAScript object literal built key by key is. Producers
// use it wherever a specification fixes a key order (the vmess JSON form, the
// VLESS xhttp extra).
type object struct {
	keys []string
	vals map[string]any
}

// set writes key. A new key is appended; an existing key keeps its position
// and takes the new value.
func (o *object) set(key string, v any) {
	if o.vals == nil {
		o.vals = map[string]any{}
	}
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

func (o *object) get(key string) (any, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// del removes key; the keys after it keep their order.
func (o *object) del(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
}

func (o *object) len() int { return len(o.keys) }

// appendJSON appends v as ECMAScript's JSON.stringify(v) writes it, compact:
// text escaped as nodemodel.AppendJSONString escapes it (so "<", ">", "&",
// U+2028 and U+2029 stay literal, unlike encoding/json), numbers as
// Number::toString writes them, *object keys in their order and model objects
// in ECMAScript property order (propertyOrder).
func appendJSON(dst []byte, v any) ([]byte, error) {
	return appendJSONDepth(dst, v, "", "", 0)
}

// appendJSONIndent appends v as JSON.stringify(v, null, 2) writes it: every
// member and element on its own line, two spaces deeper than its container,
// "key": value with one space, and [] and {} for empty containers. indent is
// the indentation of the line v starts on, which the lines after the first
// carry too.
func appendJSONIndent(dst []byte, v any, indent string) ([]byte, error) {
	return appendJSONDepth(dst, v, "  ", indent, 0)
}

// maxJSONDepth stops a cyclic value from recursing forever. Bounds refuses
// nodes far shallower than this.
const maxJSONDepth = 1000

// appendJSONDepth writes v compact when gap is empty and indented by gap per
// level otherwise, as JSON.stringify's space argument does.
func appendJSONDepth(dst []byte, v any, gap, indent string, depth int) ([]byte, error) {
	if depth > maxJSONDepth {
		return nil, fmt.Errorf("producers: value nested too deeply to write")
	}
	switch x := v.(type) {
	case nil:
		return append(dst, "null"...), nil
	case bool:
		return strconv.AppendBool(dst, x), nil
	case string:
		return nodemodel.AppendJSONString(dst, x), nil
	case float64:
		return nodemodel.AppendNumber(dst, x), nil
	case int64:
		return strconv.AppendInt(dst, x, 10), nil
	case int:
		return strconv.AppendInt(dst, int64(x), 10), nil
	case []any:
		if len(x) == 0 {
			return append(dst, "[]"...), nil
		}
		inner := indent + gap
		dst = append(dst, '[')
		for i, e := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = newline(dst, gap, inner)
			var err error
			if dst, err = appendJSONDepth(dst, e, gap, inner, depth+1); err != nil {
				return nil, err
			}
		}
		return append(newline(dst, gap, indent), ']'), nil
	case map[string]any:
		return appendMembers(dst, propertyOrder(x), func(k string) any { return x[k] }, gap, indent, depth)
	case *object:
		return appendMembers(dst, x.keys, func(k string) any { return x.vals[k] }, gap, indent, depth)
	}
	return nil, fmt.Errorf("producers: value of type %T is not part of the model", v)
}

func appendMembers(dst []byte, keys []string, value func(string) any, gap, indent string, depth int) ([]byte, error) {
	if len(keys) == 0 {
		return append(dst, "{}"...), nil
	}
	inner := indent + gap
	dst = append(dst, '{')
	for i, k := range keys {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = newline(dst, gap, inner)
		dst = nodemodel.AppendJSONString(dst, k)
		dst = append(dst, ':')
		if gap != "" {
			dst = append(dst, ' ')
		}
		var err error
		if dst, err = appendJSONDepth(dst, value(k), gap, inner, depth+1); err != nil {
			return nil, err
		}
	}
	return append(newline(dst, gap, indent), '}'), nil
}

// newline starts a line at indent in the indented form; the compact form has
// no line breaks.
func newline(dst []byte, gap, indent string) []byte {
	if gap == "" {
		return dst
	}
	return append(append(dst, '\n'), indent...)
}

// jsonText is appendJSON into a string.
func jsonText(v any) (string, error) {
	b, err := appendJSON(nil, v)
	return string(b), err
}

// propertyOrder returns m's keys in the order an ECMAScript object holds the
// keys of a key-sorted input (specs/producers/uri.md, "Input"): keys that are
// array indices first, in numeric order, then the rest in ascending order of
// their UTF-16 code units. The harness feeds every producer key-sorted nodes,
// and the native model commits to this order whatever order its parser built
// a node in, which a Go map does not keep anyway.
func propertyOrder(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, comparePropertyKeys)
	return keys
}

// keyOrder is the order upstream holds a node's keys in after the steps and
// transforms that created added (prepared.added): the keys the producer
// received in property order, then the created ones still present, in the
// order they were last created. The parameter walks visit fields in this
// order (uri.md, "Input"), and ClashMeta and JSON write it.
func keyOrder(f map[string]any, added []string) []string {
	if len(added) == 0 {
		return propertyOrder(f)
	}
	created := make(map[string]bool, len(added))
	for _, k := range added {
		created[k] = true
	}
	keys := make([]string, 0, len(f))
	for k := range f {
		if !created[k] {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, comparePropertyKeys)
	tail := make([]string, 0, len(added))
	seen := make(map[string]bool, len(added))
	for i := len(added) - 1; i >= 0; i-- {
		k := added[i]
		if _, present := f[k]; present && !seen[k] {
			seen[k] = true
			tail = append(tail, k)
		}
	}
	slices.Reverse(tail)
	return append(keys, tail...)
}

func comparePropertyKeys(a, b string) int {
	ia, aIndex := arrayIndex(a)
	ib, bIndex := arrayIndex(b)
	switch {
	case aIndex && bIndex:
		return compareUint(ia, ib)
	case aIndex:
		return -1
	case bIndex:
		return 1
	}
	return compareUTF16(a, b)
}

func compareUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// arrayIndex reports whether k is an ECMAScript array index: the canonical
// decimal text of an integer below 2^32 - 1.
func arrayIndex(k string) (uint64, bool) {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return 0, false
		}
		n = n*10 + uint64(k[i]-'0')
	}
	return n, n < 1<<32-1
}

// compareUTF16 orders text as ECMAScript's default sort does, by UTF-16 code
// units. It equals byte order except where a character above U+FFFF meets
// one between U+E000 and U+FFFF.
func compareUTF16(a, b string) int {
	for a != "" && b != "" {
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if ra != rb {
			ua, ub := firstUnit(ra), firstUnit(rb)
			if ua != ub {
				return compareUint(uint64(ua), uint64(ub))
			}
			// Same high surrogate: the low surrogates decide.
			return compareUint(uint64(ra), uint64(rb))
		}
		a, b = a[sa:], b[sb:]
	}
	return compareUint(uint64(len(a)), uint64(len(b)))
}

func firstUnit(r rune) rune {
	if r >= 0x10000 {
		hi, _ := utf16.EncodeRune(r)
		return hi
	}
	return r
}
