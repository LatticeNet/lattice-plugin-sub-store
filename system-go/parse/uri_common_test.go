package parse

import (
	"reflect"
	"testing"
)

func TestSplitQueryVariants(t *testing.T) {
	const raw = "a=b=c&&bare&k=%41&a_b_c=1&a=last&l=x%2Cy"
	for _, c := range []struct {
		v    queryVariant
		want query
	}{
		{q1, query{{"a", "b"}, {"bare", "undefined"}, {"k", "A"}, {"a_b_c", "1"}, {"a", "last"}, {"l", "x,y"}}},
		{q2, query{{"a", "b=c"}, {"bare", ""}, {"k", "A"}, {"a_b_c", "1"}, {"a", "last"}, {"l", "x,y"}}},
		{q3, query{{"a", "b=c"}, {"bare", true}, {"k", "A"}, {"a_b_c", "1"}, {"a", "last"}, {"l", "x,y"}}},
		{q4, query{{"a", "b"}, {"bare", "undefined"}, {"k", "A"}, {"a_b_c", "1"}, {"a", "last"}, {"l", []any{"x", "y"}}}},
		{q5All, query{{"a", "b"}, {"bare", "undefined"}, {"k", "A"}, {"a-b-c", "1"}, {"a", "last"}, {"l", "x,y"}}},
		{q5First, query{{"a", "b"}, {"bare", "undefined"}, {"k", "A"}, {"a-b_c", "1"}, {"a", "last"}, {"l", "x,y"}}},
		{q6, query{{"a", "b=c"}, {"bare", ""}, {"k", "A"}, {"a-b_c", "1"}, {"a", "last"}, {"l", "x,y"}}},
	} {
		got, ok := splitQuery(raw, c.v)
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("variant %d: %#v, %v\nwant %#v", c.v, got, ok, c.want)
		}
		if v, _ := got.get("a"); v != "last" {
			t.Errorf("variant %d: a = %#v, want the last value", c.v, v)
		}
	}
	for _, v := range []queryVariant{q1, q2, q4, q5All, q5First, q6} {
		if _, ok := splitQuery("a=%zz", v); ok {
			t.Errorf("variant %d accepted a bad escape", v)
		}
	}
	if q, ok := splitQuery("a=%zz&k_x=%25", q3); !ok || q[0].Value != "%zz" || q[1].Key != "k_x" || q[1].Value != "%" {
		t.Errorf("q3 is lenient and keeps keys: %#v, %v", q, ok)
	}
	if q, ok := splitQuery("a=1+2", q2); !ok || q[0].Value != "1+2" {
		t.Errorf("a plus sign changed: %#v", q)
	}
}

func TestQueryOrIsECMAScriptOr(t *testing.T) {
	q, _ := splitQuery("sni=&peer=p&e1=&e2=", q2)
	if v, ok := q.or("sni", "peer"); !ok || v != "p" {
		t.Errorf("or(sni, peer) = %#v, %v", v, ok)
	}
	if v, ok := q.or("e1", "e2"); !ok || v != "" {
		t.Errorf("or of two empty values = %#v, %v; want the second", v, ok)
	}
	if _, ok := q.or("e1", "missing"); ok {
		t.Error("or of an empty and an absent value is present")
	}
}

func TestECHOptsMapping(t *testing.T) {
	for in, want := range map[string]map[string]any{
		"":                  nil,
		"  ":                nil,
		"cfg":               {"enable": true, "config": "cfg"},
		"https://d/q":       {"enable": true, "_dns": "https://d/q"},
		"n+https://d/q":     {"enable": true, "query-server-name": "n", "_dns": "https://d/q"},
		" +https://d/q":     nil,
		"a+b+https://d/q":   nil,
		"https://d/q+ ":     nil,
		"cfg+with+plus/sig": {"enable": true, "config": "cfg+with+plus/sig"},
	} {
		got, ok := echOpts(in)
		if (want == nil) == ok || (ok && !reflect.DeepEqual(got, want)) {
			t.Errorf("echOpts(%q) = %#v, %v; want %#v", in, got, ok, want)
		}
	}
}

func TestVMessCipherNormalisation(t *testing.T) {
	for in, want := range map[string]string{
		"": "auto", " AUTO ": "auto", "none": "none", "zero": "zero", "aes-128-gcm": "aes-128-gcm",
		"chacha20-poly1305": "chacha20-poly1305", "Chacha20-IETF-Poly1305": "chacha20-poly1305",
		"aes-128-cfb": "auto", "rc4": "auto",
	} {
		if got := vmessCipher(in); got != want {
			t.Errorf("vmessCipher(%q) = %q, want %q", in, got, want)
		}
	}
}
