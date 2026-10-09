package parse

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestYAMLCoreSchemaScalars(t *testing.T) {
	v, err := decodeYAML(`
null1: ~
null2: null
null3:
bool1: True
bool2: FALSE
str1: yes
str2: off
int1: 0088
int2: -17
oct: 0o17
hex: 0x1F
float1: 1.5e3
float2: .5
inf: -.inf
big: 12345678901234567890
quoted: '0088'
str3: !!str 0123
str4: !<str> 0123
str5: !foo 123
str6: 0o8
`)
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	want := map[string]any{
		"null1": nil, "null2": nil, "null3": nil, "bool1": true, "bool2": false,
		"str1": "yes", "str2": "off", "int1": 88.0, "int2": -17.0, "oct": 15.0, "hex": 31.0,
		"float1": 1500.0, "float2": 0.5, "inf": math.Inf(-1), "big": 12345678901234567890.0,
		"quoted": "0088", "str3": "0123", "str4": "0123", "str5": "123", "str6": "0o8",
	}
	for k, w := range want {
		if got := m[k]; !reflect.DeepEqual(got, w) {
			t.Errorf("%s = %#v, want %#v", k, got, w)
		}
	}
}

func TestYAMLDuplicateKeysAndDocuments(t *testing.T) {
	for _, bad := range []string{
		"a: 1\na: 2\n",
		"{name: a, name: b, type: ss}",
		"1: x\n01: y\n", // both keys are the number 1
		"a: 1\n---\nb: 2\n",
	} {
		if v, err := decodeYAML(bad); err == nil {
			t.Errorf("decodeYAML(%q) = %#v, want an error", bad, v)
		}
	}
	// The number 1 and the text "1" are different keys; the later one wins
	// the shared property name.
	v, err := decodeYAML("1: x\n'1': y\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := v.(map[string]any)["1"]; got != "y" {
		t.Errorf(`"1" = %#v, want the later value`, got)
	}
	if v, err := decodeYAML(""); err != nil || v != nil {
		t.Errorf("empty document = %#v, %v; want null", v, err)
	}
}

func TestYAMLMergeKeys(t *testing.T) {
	v, err := decodeYAML(`
base: &b {cipher: aes, port: 1, udp: true}
other: &o {port: 2, tfo: true}
p1: {<<: *b, port: 9}
p2: {port: 9, <<: *b}
p3: {<<: [*o, *b]}
`)
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	for name, want := range map[string]map[string]any{
		"p1": {"cipher": "aes", "port": 9.0, "udp": true},
		"p2": {"cipher": "aes", "port": 9.0, "udp": true},
		"p3": {"cipher": "aes", "port": 2.0, "udp": true, "tfo": true},
	} {
		if got := m[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
	if _, err := decodeYAML("a: &a [1]\nb: {<<: *a}\n"); err == nil {
		t.Error("a list merge source was accepted")
	}
}

// TestYAMLAliasInsideItsOwnAnchorIsAnError covers values that would contain
// themselves, found by FuzzPreprocessClashYAML: yaml.v3 keeps such a node
// tree, and resolving it naively never ends.
func TestYAMLAliasInsideItsOwnAnchorIsAnError(t *testing.T) {
	for _, src := range []string{
		"a: &a [*a]\n",
		"x: &c {<<: *c}\n",
		"x-common: &common\n proxies:\n  - <<: *common\n    name: n\n",
		"a: &a {b: {c: [1, *a]}}\n",
	} {
		if v, err := decodeYAML(src); !errors.Is(err, errYAMLRecursiveAlias) {
			t.Errorf("decodeYAML(%q) = %#v, %v; want errYAMLRecursiveAlias", src, v, err)
		}
	}
	// An alias after its anchor is complete is fine, at any depth.
	if _, err := decodeYAML("a: &a {b: 1}\nc: {d: [*a]}\n"); err != nil {
		t.Errorf("alias after its anchor: %v", err)
	}
}

// TestYAMLAliasCountLimit pins the limit of 100: an anchor's definition
// counts once, so a scalar anchor allows 99 aliases, and an anchor holding
// aliases weighs as much as the heaviest of them.
func TestYAMLAliasCountLimit(t *testing.T) {
	aliases := func(n int) string {
		var b strings.Builder
		b.WriteString("a: &x v\nl:\n")
		for i := 0; i < n; i++ {
			b.WriteString("  - *x\n")
		}
		return b.String()
	}
	if _, err := decodeYAML(aliases(99)); err != nil {
		t.Fatalf("99 aliases: %v", err)
	}
	if _, err := decodeYAML(aliases(100)); err == nil {
		t.Fatal("100 aliases of one anchor were accepted")
	}
	// Ten uses of a ten-alias anchor weigh 11 x 10.
	var b strings.Builder
	b.WriteString("a: &x v\nb: &y [*x, *x, *x, *x, *x, *x, *x, *x, *x]\nc: [")
	for i := 0; i < 10; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("*y")
	}
	b.WriteString("]\n")
	if _, err := decodeYAML(b.String()); err == nil {
		t.Fatal("nested aliases past the limit were accepted")
	}
}

func TestYAMLMergeBudget(t *testing.T) {
	var b strings.Builder
	b.WriteString("base: &b {")
	for i := 0; i < 2000; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("k")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString(jsNumber(float64(i)))
		b.WriteString(": 1")
	}
	b.WriteString("}\nl:\n")
	for i := 0; i < maxYAMLMergedEntries/2000+1; i++ {
		b.WriteString("  - {<<: *b}\n")
	}
	if _, err := decodeYAML(b.String()); err != errYAMLMergeBudget {
		t.Fatalf("merging past the budget gave %v, want the budget error", err)
	}
}
