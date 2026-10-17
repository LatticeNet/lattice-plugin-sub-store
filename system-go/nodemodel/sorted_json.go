package nodemodel

import (
	"encoding/json"
	"sort"
	"strconv"
)

// appendSorted appends the JSON value raw as appendValue writes the value
// json.Unmarshal decodes from it (object keys sorted at every depth, numbers
// as AppendNumber writes them, strings as AppendJSONString quotes them), but
// straight from raw, without building the maps and interfaces in between.
// The fleet block of every plan node takes this path (json.Marshal of the
// block, then this), which is the same bytes MarshalPlanNode always wrote.
// ok is false for anything but the compact documents json.Marshal writes
// (whitespace, a duplicate key, a nesting deeper than 32, a malformed
// value), and the caller decodes and writes as before;
// TestAppendSortedIsAppendValueOfUnmarshal and its fuzz test hold the two
// to the same bytes.
func appendSorted(dst, raw []byte) ([]byte, bool) {
	w := sortedWriter{data: raw}
	out, ok := w.value(dst, 0)
	if !ok || w.pos != len(raw) {
		return dst, false
	}
	return out, true
}

type sortedWriter struct {
	data []byte
	pos  int
}

// member is one object member: its decoded key and the span of its value.
type member struct {
	key        string
	start, end int
}

func (w *sortedWriter) value(dst []byte, depth int) ([]byte, bool) {
	if depth > 32 || w.pos >= len(w.data) {
		return dst, false
	}
	switch c := w.data[w.pos]; {
	case c == '{':
		return w.object(dst, depth)
	case c == '[':
		return w.array(dst, depth)
	case c == '"':
		s, ok := w.str()
		if !ok {
			return dst, false
		}
		return AppendJSONString(dst, s), true
	case c == 't':
		return append(dst, "true"...), w.literal("true")
	case c == 'f':
		return append(dst, "false"...), w.literal("false")
	case c == 'n':
		return append(dst, "null"...), w.literal("null")
	case c == '-' || (c >= '0' && c <= '9'):
		start := w.pos
		for w.pos < len(w.data) {
			c := w.data[w.pos]
			if (c < '0' || c > '9') && c != '-' && c != '+' && c != '.' && c != 'e' && c != 'E' {
				break
			}
			w.pos++
		}
		text := w.data[start:w.pos]
		if !jsonNumber(text) {
			return dst, false
		}
		f, err := strconv.ParseFloat(string(text), 64)
		if err != nil {
			return dst, false
		}
		return AppendNumber(dst, f), true
	}
	return dst, false
}

func (w *sortedWriter) literal(word string) bool {
	if len(w.data)-w.pos < len(word) || string(w.data[w.pos:w.pos+len(word)]) != word {
		return false
	}
	w.pos += len(word)
	return true
}

// str decodes the string at w.pos as json.Unmarshal does: as it is when it
// holds no escape, through json.Unmarshal otherwise.
func (w *sortedWriter) str() (string, bool) {
	start := w.pos
	w.pos++
	escaped := false
	for w.pos < len(w.data) {
		switch c := w.data[w.pos]; {
		case c == '"':
			w.pos++
			if !escaped {
				return string(w.data[start+1 : w.pos-1]), true
			}
			var s string
			if json.Unmarshal(w.data[start:w.pos], &s) != nil {
				return "", false
			}
			return s, true
		case c == '\\':
			escaped = true
			w.pos += 2
		case c < 0x20 || c >= 0x80:
			// A control byte is malformed; a byte outside ASCII may be
			// invalid UTF-8, which json.Unmarshal replaces.
			escaped = true
			w.pos++
		default:
			w.pos++
		}
	}
	return "", false
}

func (w *sortedWriter) array(dst []byte, depth int) ([]byte, bool) {
	w.pos++
	dst = append(dst, '[')
	if w.pos < len(w.data) && w.data[w.pos] == ']' {
		w.pos++
		return append(dst, ']'), true
	}
	for first := true; ; first = false {
		if !first {
			dst = append(dst, ',')
		}
		var ok bool
		if dst, ok = w.value(dst, depth+1); !ok || w.pos >= len(w.data) {
			return dst, false
		}
		switch w.data[w.pos] {
		case ',':
			w.pos++
		case ']':
			w.pos++
			return append(dst, ']'), true
		default:
			return dst, false
		}
	}
}

// skip moves past the value at w.pos, checking only enough to find its
// end; the value is written later from its span, which checks it fully.
func (w *sortedWriter) skip(depth int) bool {
	if depth > 32 || w.pos >= len(w.data) {
		return false
	}
	switch c := w.data[w.pos]; {
	case c == '"':
		w.pos++
		for w.pos < len(w.data) {
			switch w.data[w.pos] {
			case '"':
				w.pos++
				return true
			case '\\':
				w.pos += 2
			default:
				w.pos++
			}
		}
		return false
	case c == '{' || c == '[':
		closer := byte('}')
		if c == '[' {
			closer = ']'
		}
		w.pos++
		if w.pos < len(w.data) && w.data[w.pos] == closer {
			w.pos++
			return true
		}
		for {
			if c == '{' {
				if w.pos >= len(w.data) || w.data[w.pos] != '"' || !w.skip(depth+1) || w.pos >= len(w.data) || w.data[w.pos] != ':' {
					return false
				}
				w.pos++
			}
			if !w.skip(depth+1) || w.pos >= len(w.data) {
				return false
			}
			switch w.data[w.pos] {
			case ',':
				w.pos++
			case closer:
				w.pos++
				return true
			default:
				return false
			}
		}
	default:
		for w.pos < len(w.data) {
			switch w.data[w.pos] {
			case ',', '}', ']':
				return true
			}
			w.pos++
		}
		return true
	}
}

func (w *sortedWriter) object(dst []byte, depth int) ([]byte, bool) {
	w.pos++
	if w.pos < len(w.data) && w.data[w.pos] == '}' {
		w.pos++
		return append(dst, '{', '}'), true
	}
	var buf [16]member
	members := buf[:0]
	for {
		if w.pos >= len(w.data) || w.data[w.pos] != '"' {
			return dst, false
		}
		key, ok := w.str()
		if !ok || w.pos >= len(w.data) || w.data[w.pos] != ':' {
			return dst, false
		}
		w.pos++
		start := w.pos
		if !w.skip(depth + 1) {
			return dst, false
		}
		members = append(members, member{key: key, start: start, end: w.pos})
		if w.pos >= len(w.data) {
			return dst, false
		}
		if w.data[w.pos] == '}' {
			w.pos++
			break
		}
		if w.data[w.pos] != ',' {
			return dst, false
		}
		w.pos++
	}
	sort.Slice(members, func(i, j int) bool { return members[i].key < members[j].key })
	for i := 1; i < len(members); i++ {
		if members[i].key == members[i-1].key {
			return dst, false
		}
	}
	dst = append(dst, '{')
	for i, m := range members {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(AppendJSONString(dst, m.key), ':')
		sub := sortedWriter{data: w.data[:m.end], pos: m.start}
		var ok bool
		if dst, ok = sub.value(dst, depth+1); !ok || sub.pos != m.end {
			return dst, false
		}
	}
	return append(dst, '}'), true
}

// blockMember is one top-level member of the fleet block's encoding.
type blockMember struct {
	key   string
	value []byte
}

// blockMembers splits the fleet block's encoding into its top-level members
// for MarshalPlanNode, which writes each value with appendSorted; ok is
// false for anything but a compact object with distinct keys, and the caller
// decodes and writes as before.
func blockMembers(raw []byte) ([]blockMember, bool) {
	if len(raw) < 2 || raw[0] != '{' {
		return nil, false
	}
	w := sortedWriter{data: raw, pos: 1}
	var out []blockMember
	if raw[1] == '}' {
		return out, len(raw) == 2
	}
	for {
		if w.pos >= len(raw) || raw[w.pos] != '"' {
			return nil, false
		}
		key, ok := w.str()
		if !ok || w.pos >= len(raw) || raw[w.pos] != ':' {
			return nil, false
		}
		w.pos++
		start := w.pos
		if !w.skip(1) || w.pos >= len(raw) {
			return nil, false
		}
		for _, m := range out {
			if m.key == key {
				return nil, false
			}
		}
		out = append(out, blockMember{key: key, value: raw[start:w.pos]})
		switch raw[w.pos] {
		case '}':
			return out, w.pos+1 == len(raw)
		case ',':
			w.pos++
		default:
			return nil, false
		}
	}
}

// jsonNumber reports whether b is -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?.
func jsonNumber(b []byte) bool {
	i := 0
	if i < len(b) && b[i] == '-' {
		i++
	}
	switch {
	case i < len(b) && b[i] == '0':
		i++
	case i < len(b) && b[i] >= '1' && b[i] <= '9':
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
	default:
		return false
	}
	if i < len(b) && b[i] == '.' {
		i++
		start := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			i++
		}
		start := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(b)
}
