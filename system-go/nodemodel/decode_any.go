package nodemodel

import (
	"encoding/json"
	"strconv"

	"github.com/LatticeNet/lattice-sdk/strictjson"
)

// decodeObject is json.Unmarshal of raw into a map[string]any, without
// encoding/json's reflection: convert decodes every bound node this way, a
// thousand of them per call. Values decode as json.Unmarshal decodes them
// into an interface: strings through strictjson.String (the same bytes as
// json.Unmarshal), numbers through strconv.ParseFloat, true, false, null,
// objects (a repeated key keeps its last value) and arrays. ok is false for
// anything else (a malformed document, a number float64 cannot hold, a
// nesting deeper than maxDecodeDepth), and the caller runs json.Unmarshal,
// which decides and words the error. TestDecodeObjectIsJSONUnmarshal and its
// fuzz test hold the two to the same values.
func decodeObject(raw []byte) (map[string]any, bool) {
	d := anyDecoder{data: raw}
	d.space()
	if d.pos >= len(d.data) || d.data[d.pos] != '{' {
		return nil, false
	}
	v, ok := d.value(0)
	if !ok {
		return nil, false
	}
	d.space()
	if d.pos != len(d.data) {
		return nil, false
	}
	return v.(map[string]any), true
}

// maxDecodeDepth bounds decodeObject's nesting; a deeper node takes
// json.Unmarshal.
const maxDecodeDepth = 64

type anyDecoder struct {
	data []byte
	pos  int
}

func (d *anyDecoder) space() {
	for d.pos < len(d.data) {
		switch d.data[d.pos] {
		case ' ', '\t', '\n', '\r':
			d.pos++
		default:
			return
		}
	}
}

func (d *anyDecoder) value(depth int) (any, bool) {
	if depth > maxDecodeDepth || d.pos >= len(d.data) {
		return nil, false
	}
	switch c := d.data[d.pos]; {
	case c == '{':
		return d.object(depth)
	case c == '[':
		return d.array(depth)
	case c == '"':
		return d.str()
	case c == 't':
		return true, d.literal("true")
	case c == 'f':
		return false, d.literal("false")
	case c == 'n':
		return nil, d.literal("null")
	case c == '-' || (c >= '0' && c <= '9'):
		start := d.pos
		for d.pos < len(d.data) {
			c := d.data[d.pos]
			if (c < '0' || c > '9') && c != '-' && c != '+' && c != '.' && c != 'e' && c != 'E' {
				break
			}
			d.pos++
		}
		text := d.data[start:d.pos]
		if !jsonNumber(text) {
			return nil, false
		}
		f, err := strconv.ParseFloat(string(text), 64)
		if err != nil {
			return nil, false
		}
		return f, true
	}
	return nil, false
}

func (d *anyDecoder) literal(word string) bool {
	if len(d.data)-d.pos < len(word) || string(d.data[d.pos:d.pos+len(word)]) != word {
		return false
	}
	d.pos += len(word)
	return true
}

// str decodes the string literal at d.pos: its end is the first quote not
// escaped, and strictjson.String decodes it.
func (d *anyDecoder) str() (any, bool) {
	start := d.pos
	d.pos++
	for d.pos < len(d.data) {
		switch d.data[d.pos] {
		case '"':
			d.pos++
			return strictjson.String(d.data[start:d.pos])
		case '\\':
			d.pos += 2
		default:
			d.pos++
		}
	}
	return nil, false
}

func (d *anyDecoder) array(depth int) (any, bool) {
	d.pos++
	d.space()
	out := []any{}
	if d.pos < len(d.data) && d.data[d.pos] == ']' {
		d.pos++
		return out, true
	}
	for {
		v, ok := d.value(depth + 1)
		if !ok {
			return nil, false
		}
		out = append(out, v)
		d.space()
		if d.pos >= len(d.data) {
			return nil, false
		}
		switch d.data[d.pos] {
		case ',':
			d.pos++
			d.space()
		case ']':
			d.pos++
			return out, true
		default:
			return nil, false
		}
	}
}

func (d *anyDecoder) object(depth int) (any, bool) {
	d.pos++
	d.space()
	out := map[string]any{}
	if d.pos < len(d.data) && d.data[d.pos] == '}' {
		d.pos++
		return out, true
	}
	for {
		if d.pos >= len(d.data) || d.data[d.pos] != '"' {
			return nil, false
		}
		k, ok := d.str()
		if !ok {
			return nil, false
		}
		d.space()
		if d.pos >= len(d.data) || d.data[d.pos] != ':' {
			return nil, false
		}
		d.pos++
		d.space()
		v, ok := d.value(depth + 1)
		if !ok {
			return nil, false
		}
		out[k.(string)] = v
		d.space()
		if d.pos >= len(d.data) {
			return nil, false
		}
		switch d.data[d.pos] {
		case ',':
			d.pos++
			d.space()
		case '}':
			d.pos++
			return out, true
		default:
			return nil, false
		}
	}
}

// unmarshalObject is decodeObject with json.Unmarshal for what it declines.
func unmarshalObject(data []byte) (map[string]any, error) {
	if m, ok := decodeObject(data); ok {
		return m, nil
	}
	var m map[string]any
	err := json.Unmarshal(data, &m)
	return m, err
}
