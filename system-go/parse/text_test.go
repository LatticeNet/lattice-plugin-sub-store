package parse

import (
	"math"
	"testing"
)

func TestTrimECMAScriptRemovesWhatTrimRemoves(t *testing.T) {
	for in, want := range map[string]string{
		"\ufeff vless://a \u2028":   "vless://a",
		"\u00a0\u3000x\u2029\t\v\f": "x",
		"\u200bx":                   "\u200bx", // zero-width space is not white space
		"":                          "",
	} {
		if got := TrimECMAScript(in); got != want {
			t.Errorf("TrimECMAScript(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPercentDecodeStrict(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"a%20b+c", "a b+c", true},
		{"%E9%A6%99", "香", true},
		{"%e9%a6%99", "香", true},
		{"no-escape", "no-escape", true},
		{"%2", "", false},
		{"%zz", "", false},
		{"%", "", false},
		{"%C0%AF", "", false},       // overlong
		{"%ED%A0%80", "", false},    // surrogate
		{"%E4%B8", "", false},       // truncated
		{"%E4%B8x", "", false},      // continuation is not an escape
		{"%F5%80%80%80", "", false}, // above U+10FFFF
		{"%80", "", false},          // lone continuation byte
		{"%25eth0", "%eth0", true},
	} {
		got, ok := PercentDecodeStrict(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("PercentDecodeStrict(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	if got := PercentDecodeLenient("100%"); got != "100%" {
		t.Errorf("PercentDecodeLenient kept %q, want the input", got)
	}
	if got := PercentDecodeLenient("a%2Fb"); got != "a/b" {
		t.Errorf("PercentDecodeLenient(a%%2Fb) = %q", got)
	}
}

func TestBase64ValidityTest(t *testing.T) {
	for in, want := range map[string]bool{
		"":           true,
		"YWJj":       true,
		"YW Jj\n":    true,
		"YWI=":       true,
		"YQ==":       true,
		"YQ===":      false,
		"YQ= =":      true, // white space goes first, then two "="
		"a+b/":       true,
		"a-b_":       true,
		"a+b_":       false, // mixes the alphabets
		"a=b":        false,
		"vless://x":  false,
		"YWJj\u00a0": true,
		"YWJj\u200b": false,
	} {
		if got := Base64Valid(in); got != want {
			t.Errorf("Base64Valid(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBase64DecodeLenient(t *testing.T) {
	for in, want := range map[string]string{
		"dm1lc3M6Ly8=":    "vmess://",
		"dm1lc3M6Ly8":     "vmess://",
		"dm1lc3M6 Ly8=\n": "vmess://",
		"YWJj-_8":         "abc\ufffd\ufffd", // -_ read as +/, bytes FB FF are ill-formed
		"YWJjZ":           "abc",             // a final single character is ignored
		"YW=Jj":           "abc",             // "=" inside is deleted, not an end
		"YR":              "a",               // non-zero trailing bits ignored
		"":                "",
		"8J+YgA==":        "😀",
		"4oKs":            "€",
		"wA==":            "\ufffd", // C0 is never valid
		"7aCA":            "\ufffd\ufffd\ufffd",
		"8JCA":            "\ufffd", // F0 90 80 is a truncated sequence: one U+FFFD
	} {
		if got := Base64DecodeLenient(in); got != want {
			t.Errorf("Base64DecodeLenient(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContainsTrueOr1(t *testing.T) {
	for in, want := range map[string]bool{
		"1": true, "10": true, "TRUE": true, "untrue": true, "tRuE": true,
		"0": false, "false": false, "yes": false, "": false, "tru": false,
	} {
		if got := ContainsTrueOr1(in); got != want {
			t.Errorf("ContainsTrueOr1(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLeadingAndDigitsOnlyIntegers(t *testing.T) {
	for in, want := range map[string]float64{
		"12abc": 12, "  -3": -3, "+7x": 7, "\u00a09": 9, "0443": 443,
	} {
		if got := LeadingInteger(in); got != want {
			t.Errorf("LeadingInteger(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"abc", "", "-", " +x", "\u200b1"} {
		if got := LeadingInteger(in); !math.IsNaN(got) {
			t.Errorf("LeadingInteger(%q) = %v, want not-a-number", in, got)
		}
	}
	if got := LeadingInteger("-0"); got != 0 || !math.Signbit(got) {
		t.Errorf("LeadingInteger(-0) = %v, want negative zero", got)
	}
	if v, ok := DigitsOnlyInteger("0088"); !ok || v != 88 {
		t.Errorf("DigitsOnlyInteger(0088) = %v, %v", v, ok)
	}
	for _, in := range []string{"", "1a", "-1", " 1", "+1"} {
		if _, ok := DigitsOnlyInteger(in); ok {
			t.Errorf("DigitsOnlyInteger(%q) accepted", in)
		}
	}
	if _, ok := safeDigits("9007199254740992"); ok {
		t.Error("safeDigits accepted 2^53")
	}
	if v, ok := safeDigits("9007199254740991"); !ok || v != maxSafeInteger {
		t.Errorf("safeDigits(2^53-1) = %v, %v", v, ok)
	}
}

func TestEncodeURIComponentKeepsTheUnreservedMarks(t *testing.T) {
	if got, want := encodeURIComponent("obfs-local;obfs=http;obfs-host=a.b!~*'()é"), "obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Da.b!~*'()%C3%A9"; got != want {
		t.Errorf("encodeURIComponent = %q, want %q", got, want)
	}
}

func TestJSStringAndTruthiness(t *testing.T) {
	for _, c := range []struct {
		v       any
		present bool
		want    string
		truthy  bool
	}{
		{nil, false, "undefined", false},
		{nil, true, "null", false},
		{"", true, "", false},
		{"0", true, "0", true},
		{0.0, true, "0", false},
		{math.NaN(), true, "NaN", false},
		{1e21, true, "1e+21", true},
		{[]any{"a", nil, 1.0}, true, "a,,1", true},
		{map[string]any{}, true, "[object Object]", true},
		{false, true, "false", false},
	} {
		if got := jsString(c.v, c.present); got != c.want {
			t.Errorf("jsString(%#v) = %q, want %q", c.v, got, c.want)
		}
		if got := truthy(c.v, c.present); got != c.truthy {
			t.Errorf("truthy(%#v) = %v, want %v", c.v, got, c.truthy)
		}
	}
}
