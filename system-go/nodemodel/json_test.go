package nodemodel

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// TestNodeJSONRoundTripSortsKeys pins the wire form: keys sorted at every
// depth (objects inside lists included), the fleet block under "_lattice" in
// its sorted place, Script never written, and a decode that gives back the
// same model.
func TestNodeJSONRoundTripSortsKeys(t *testing.T) {
	n := &Node{
		Fields: map[string]any{
			"type":         "vless",
			"port":         float64(443),
			"name":         "JP 01",
			"Zeta":         true,
			"empty-object": map[string]any{},
			"empty-list":   []any{},
			"empty-text":   "",
			"null-value":   nil,
			"ws-opts": map[string]any{
				"path":    "/p",
				"headers": map[string]any{"Host": "h.example", "A-Header": "1"},
			},
			"peers": []any{map[string]any{"z": float64(1), "a": []any{map[string]any{"y": "2", "b": "3"}}}},
		},
		Script:  map[string]any{"_flag": "scripts only"},
		Lattice: &LatticeFields{LineUUID: "u-1", NodeID: "n-1"},
	}
	got, err := n.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"Zeta":true,"_lattice":{"line_uuid":"u-1","node_id":"n-1"},"empty-list":[],"empty-object":{},"empty-text":"",` +
		`"name":"JP 01","null-value":null,"peers":[{"a":[{"b":"3","y":"2"}],"z":1}],"port":443,"type":"vless",` +
		`"ws-opts":{"headers":{"A-Header":"1","Host":"h.example"},"path":"/p"}}`
	if string(got) != want {
		t.Fatalf("MarshalJSON\n got  %s\n want %s", got, want)
	}

	var back Node
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Fields, n.Fields) {
		t.Errorf("decoded fields differ\n got  %#v\n want %#v", back.Fields, n.Fields)
	}
	if back.Lattice == nil || *back.Lattice != *n.Lattice {
		t.Errorf("decoded lattice %+v, want %+v", back.Lattice, n.Lattice)
	}
	if back.Script != nil {
		t.Errorf("Script came back from the wire: %v", back.Script)
	}
	again, err := back.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != want {
		t.Errorf("second round trip\n got  %s\n want %s", again, want)
	}

	// Without a fleet block the key is absent, and a stray Fields value under
	// the reserved key is never written.
	n.Lattice = nil
	n.Fields["_lattice"] = "smuggled"
	got, err = n.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "_lattice") {
		t.Errorf("_lattice written without a fleet block: %s", got)
	}
	n.Lattice = &LatticeFields{}
	got, _ = n.MarshalJSON()
	if strings.Contains(string(got), "_lattice") {
		t.Errorf("an empty fleet block was written: %s", got)
	}
}

func TestNodeJSONNumbersAreWrittenAsECMAScriptWritesThem(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{0, "0"},
		{math.Copysign(0, -1), "0"},
		{443, "443"},
		{-7, "-7"},
		{1.5, "1.5"},
		{0.1, "0.1"},
		{1e20, "100000000000000000000"},
		{1e21, "1e+21"},
		{1.2345678901234567e19, "12345678901234567000"},
		{123456789012345680000, "123456789012345680000"},
		{0.000001, "0.000001"},
		{1e-7, "1e-7"},
		{1.5e-10, "1.5e-10"},
		{2.5e22, "2.5e+22"},
		{9007199254740993, "9007199254740992"},
		{math.NaN(), "null"},
		{math.Inf(1), "null"},
		{math.Inf(-1), "null"},
	}
	for _, c := range cases {
		if got := FormatNumber(c.v); got != c.want {
			t.Errorf("FormatNumber(%v) = %s, want %s", c.v, got, c.want)
		}
	}
	got, err := (&Node{Fields: map[string]any{"a": int64(-12), "b": 3}}).MarshalJSON()
	if err != nil || string(got) != `{"a":-12,"b":3}` {
		t.Errorf("integer values = %s, %v", got, err)
	}
}

func TestNodeJSONTextIsEscapedAsJSONStringifyEscapesIt(t *testing.T) {
	n := &Node{Fields: map[string]any{"name": "a\"b\\c\b\f\n\r\t\x01\x1f<>&  é\xff"}}
	got, err := n.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"a\"b\\c\b\f\n\r\t\u0001\u001f<>&` + "  é�" + `"}`
	if string(got) != want {
		t.Errorf("MarshalJSON\n got  %q\n want %q", got, want)
	}
	var back Node
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
}

func TestNodeJSONRefusesWhatIsNotAModelValue(t *testing.T) {
	if _, err := (&Node{Fields: map[string]any{"x": struct{}{}}}).MarshalJSON(); err == nil {
		t.Error("a struct value was written")
	}
	for _, in := range []string{`null`, `[]`, `"x"`, `{"_lattice":"x"}`, `{"_lattice":[1]}`, `{`} {
		var n Node
		if err := json.Unmarshal([]byte(in), &n); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", in)
		}
	}
	var n Node
	if err := json.Unmarshal([]byte(`{"name":"a","_lattice":null}`), &n); err != nil || n.Lattice != nil || len(n.Fields) != 1 {
		t.Errorf("a null fleet block: lattice %v, fields %v, err %v", n.Lattice, n.Fields, err)
	}
	// Numbers decode as ECMAScript reads them: the nearest double.
	if err := json.Unmarshal([]byte(`{"password":12345678901234567890}`), &n); err != nil {
		t.Fatal(err)
	}
	if n.Fields["password"] != float64(12345678901234567168) {
		t.Errorf("password decoded as %v", n.Fields["password"])
	}
}

// Bounds counts the node size without encoding it; the count must equal the
// encoding's length for every kind of value.
func TestJSONSizeMatchesTheEncoding(t *testing.T) {
	values := []any{
		nil, true, false, "", "plain", "a\"b\\c\b\f\n\r\t\x01\x1f", "é \xff\xfe", float64(0), math.Copysign(0, -1),
		1.5, 1e21, 1e-7, math.NaN(), math.Inf(1), int64(-12345), 7, []any{}, map[string]any{},
		[]any{nil, "x", float64(1), []any{map[string]any{"k\n": "v"}}},
		map[string]any{"b": []any{1.25, "q\""}, "a": map[string]any{"\xff": nil}},
	}
	for _, v := range values {
		encoded, err := appendValue(nil, v, 0)
		if err != nil {
			t.Fatal(err)
		}
		size, err := jsonSize(v, 0)
		if err != nil || size != len(encoded) {
			t.Errorf("jsonSize(%#v) = %d, %v; the encoding %s has %d bytes", v, size, err, encoded, len(encoded))
		}
	}
	if _, err := jsonSize(struct{}{}, 0); err == nil {
		t.Error("jsonSize accepted a struct")
	}
}
