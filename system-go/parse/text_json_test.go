package parse

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeJSONReadsAsJSONParse(t *testing.T) {
	v, err := decodeJSON(` {"a":1,"a":2,"big":1e400,"s":"é"} `)
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["a"] != 2.0 {
		t.Errorf("duplicate key kept %v, want the last value", m["a"])
	}
	if !math.IsInf(m["big"].(float64), 1) {
		t.Errorf("1e400 read as %v, want +Inf as JSON.parse reads it", m["big"])
	}
	if m["s"] != "é" {
		t.Errorf("escape read as %q", m["s"])
	}
	for _, bad := range []string{"", "{} x", "{'a':1}", "[1,]", "{\"a\":1}{}"} {
		if _, err := decodeJSON(bad); err == nil {
			t.Errorf("decodeJSON(%q) accepted", bad)
		}
	}
}

func TestDecodeJSON5(t *testing.T) {
	for _, c := range []struct {
		in   string
		want any
	}{
		{`{name: 'a', type: "ss", port: 443,}`, map[string]any{"name": "a", "type": "ss", "port": 443.0}},
		{`// comment
		{ /* block */ a: [1, 2,], $b_1: null, }`, map[string]any{"a": []any{1.0, 2.0}, "$b_1": nil}},
		{`{a: 0x1F, b: +5, c: .5, d: 5., e: -Infinity, f: 1e3, g: 5.e1}`, map[string]any{"a": 31.0, "b": 5.0, "c": 0.5, "d": 5.0, "e": math.Inf(-1), "f": 1000.0, "g": 50.0}},
		{`{a: 1, a: 2}`, map[string]any{"a": 2.0}},
		{`'\x41\u0042\'\"\0\v'`, "AB'\"\x00\v"},
		{`"\ud83d\ude00"`, "\U0001F600"},
		{`"\ud83dx"`, "\uFFFDx"}, // a lone surrogate becomes U+FFFD
		{"'line\\\ncontinued'", "linecontinued"},
		{`"\q"`, "q"},
		{`{ключ: 1, \u0061b: 2}`, map[string]any{"ключ": 1.0, "ab": 2.0}},
		{"\ufeff[true, false]\u2028", []any{true, false}},
		{`{"name":"a","type":"vless","port":443}`, map[string]any{"name": "a", "type": "vless", "port": 443.0}},
	} {
		got, err := decodeJSON5(c.in)
		if err != nil {
			t.Errorf("decodeJSON5(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("decodeJSON5(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
	nan, err := decodeJSON5("NaN")
	if err != nil || !math.IsNaN(nan.(float64)) {
		t.Errorf("decodeJSON5(NaN) = %v, %v", nan, err)
	}
	for _, bad := range []string{
		"01", "{a 1}", "{a: 1", "[1 2]", "'unterminated", "\"a\nb\"", `"\1"`, `"\01"`,
		"/* open", "{1a: 2}", "- type: ss", "type: ss", "{name: a, type: ss, x}", "+", ".", "0x",
		"{a: 1} trailing",
	} {
		if v, err := decodeJSON5(bad); err == nil {
			t.Errorf("decodeJSON5(%q) = %#v, want an error", bad, v)
		}
	}
}

func TestDecodeJSON5BoundsNesting(t *testing.T) {
	deep := strings.Repeat("[", maxStructuredDepth+2) + strings.Repeat("]", maxStructuredDepth+2)
	if _, err := decodeJSON5(deep); err == nil {
		t.Fatal("decodeJSON5 accepted nesting past the bound")
	}
	ok := strings.Repeat("[", 100) + strings.Repeat("]", 100)
	if _, err := decodeJSON5(ok); err != nil {
		t.Fatalf("decodeJSON5 refused 100 levels: %v", err)
	}
}
