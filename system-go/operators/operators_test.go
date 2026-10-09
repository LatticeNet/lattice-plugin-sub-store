package operators

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// node is a trojan node with the given name and extra fields.
func node(t *testing.T, name string, extra ...any) *nodemodel.Node {
	t.Helper()
	n := &nodemodel.Node{Fields: map[string]any{"name": name, "type": "trojan", "server": "192.0.2.1", "port": float64(443), "password": "p"}}
	for i := 0; i+1 < len(extra); i += 2 {
		n.Fields[extra[i].(string)] = extra[i+1]
	}
	return n
}

func named(t *testing.T, names ...string) []*nodemodel.Node {
	t.Helper()
	out := make([]*nodemodel.Node, len(names))
	for i, name := range names {
		out[i] = node(t, name)
	}
	return out
}

func namesOf(ns []*nodemodel.Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Name()
	}
	return out
}

// compileOne compiles a one-step chain and fails unless it is native.
func compileOne(t *testing.T, step string) *Plan {
	t.Helper()
	plan, err := Compile("r", []json.RawMessage{json.RawMessage(step)})
	if err != nil {
		t.Fatalf("compile %s: %v", step, err)
	}
	if !plan.Native() {
		t.Fatalf("%s did not compile natively: %+v", step, plan.Steps[0].Diag)
	}
	return plan
}

func runOne(t *testing.T, step string, ns []*nodemodel.Node) []*nodemodel.Node {
	t.Helper()
	return compileOne(t, step).Run(ns, &Context{Target: "sing-box"})
}

func wantNames(t *testing.T, got []*nodemodel.Node, want ...string) {
	t.Helper()
	if g := namesOf(got); !slices.Equal(g, want) {
		t.Fatalf("names = %q, want %q", g, want)
	}
}

// A leading (?i) makes one pattern case-insensitive; without it a pattern
// matches the letter case it was written in, as the bundle matches.
func TestRegexFilterCaseInsensitive(t *testing.T) {
	in := func() []*nodemodel.Node { return named(t, "HK a", "hk b", "Hk c", "JP d") }
	wantNames(t, runOne(t, `{"type":"Regex Filter","args":{"regex":["(?i)hk"]}}`, in()), "HK a", "hk b", "Hk c")
	wantNames(t, runOne(t, `{"type":"Regex Filter","args":{"regex":["hk"]}}`, in()), "hk b")
	wantNames(t, runOne(t, `{"type":"Regex Filter","args":{"regex":["(?i)HK"],"keep":false}}`, in()), "JP d")
	// The prefix is per pattern: the second pattern stays case-sensitive.
	wantNames(t, runOne(t, `{"type":"Regex Filter","args":{"regex":["(?i)jp","hk"]}}`, in()), "hk b", "JP d")
}

// Regex Rename's replacement reads ECMAScript's $ forms: numbered groups
// ($1, and $10 as group 1 then "0" when there is no tenth group), named
// groups, $& and $$, the text around the match; "\1" is literal text, and
// every match is replaced.
func TestRegexRenameBackreferences(t *testing.T) {
	rename := func(expr, now string, name string) string {
		step, _ := json.Marshal(map[string]any{"type": "Regex Rename Operator", "args": []any{map[string]any{"expr": expr, "now": now}}})
		return runOne(t, string(step), named(t, name))[0].Name()
	}
	cases := []struct{ expr, now, name, want string }{
		{`(\w+) (\d+)`, "$2-$1", "HK 01", "01-HK"},
		{`(\w+) (\d+)`, `\2-\1`, "HK 01", `\2-\1`},
		{`(\w+) (\d+)`, "$$ $& [$`] [$'] $3 $10 $1x $01", "HK 01 hk", "$ HK 01 [] [ hk] $3 HK0 HKx HK hk"},
		{`(?P<cc>[A-Z]{2})`, "<$<cc>>", "HK-JP", "<HK>-<JP>"},
		{`([A-Z])`, "$<x>", "a B", "a $<x>"},
		{`(a)|(b)`, "[$2]", "ab", "[][b]"},
		{`HK`, "Hong Kong", " HK 01 HK ", "Hong Kong 01 Hong Kong"},
		{`(?i)hk`, "X", "HK hk", "X X"},
	}
	for _, c := range cases {
		if got := rename(c.expr, c.now, c.name); got != c.want {
			t.Errorf("rename %q with %q over %q = %q, want %q", c.expr, c.now, c.name, got, c.want)
		}
	}
	// An absent "now" is the text String(undefined), as the bundle writes it.
	got := runOne(t, `{"type":"Regex Rename Operator","args":[{"expr":"HK"}]}`, named(t, "HK 01"))
	wantNames(t, got, "undefined 01")
	// Rules apply in order, each over the previous one's output.
	got = runOne(t, `{"type":"Regex Rename Operator","args":[{"expr":"0","now":"O"},{"expr":"O","now":"Zero"}]}`, named(t, "HK 01"))
	wantNames(t, got, "HK Zero1")
}

// EXISTS keeps the nodes that carry the field (and not as null). Upstream's
// EXISTS is true for every node; design 28 fixes it, and the parity test in
// package main pins the bundle's answer as a known difference.
func TestConditionalFilterExists(t *testing.T) {
	in := func() []*nodemodel.Node {
		return []*nodemodel.Node{node(t, "with", "sni", "a.example"), node(t, "without"), node(t, "null", "sni", nil), node(t, "empty", "sni", "")}
	}
	wantNames(t, runOne(t, `{"type":"Conditional Filter","args":{"rule":{"proposition":"EXISTS","attr":"sni"}}}`, in()), "with", "empty")
	wantNames(t, runOne(t, `{"type":"Conditional Filter","args":{"rule":{"operator":"NOT","child":{"proposition":"EXISTS","attr":"sni"}}}}`, in()), "without", "null")
	rule := `{"type":"Conditional Filter","args":{"rule":{"operator":"AND","child":[{"proposition":"EXISTS","attr":"sni"},{"proposition":"CONTAINS","attr":"sni","value":"example"}]}}}`
	wantNames(t, runOne(t, rule, in()), "with")
}

// The other propositions and operators, as the bundle answers them.
func TestConditionalFilterPropositions(t *testing.T) {
	in := func() []*nodemodel.Node {
		return []*nodemodel.Node{
			node(t, "a", "alpn", []any{"h2"}),
			node(t, "b", "type", "vmess", "port", float64(8443)),
			node(t, "c", "server", "x.example", "udp", false),
		}
	}
	for _, c := range []struct {
		rule string
		want []string
	}{
		{`{"proposition":"EQUALS","attr":"type","value":"vmess"}`, []string{"b"}},
		{`{"proposition":"EQUALS","attr":"port","value":"443"}`, nil},
		{`{"proposition":"EQUALS","attr":"udp","value":false}`, []string{"c"}},
		{`{"proposition":"EQUALS","attr":"nothing"}`, []string{"a", "b", "c"}},
		{`{"proposition":"IN","attr":"type","value":["vmess","x"]}`, []string{"b"}},
		{`{"proposition":"IN","attr":"port","value":[443]}`, []string{"a", "c"}},
		{`{"proposition":"IN","attr":"type","value":"vmess-x"}`, []string{"b"}},
		{`{"proposition":"CONTAINS","attr":"server","value":"example"}`, []string{"c"}},
		{`{"proposition":"CONTAINS","attr":"alpn","value":"h2"}`, nil},
		{`{"proposition":"CONTAINS","attr":"port","value":"44"}`, nil},
		{`{"operator":"OR","child":[{"proposition":"EQUALS","attr":"name","value":"a"},{"proposition":"EQUALS","attr":"name","value":"c"}]}`, []string{"a", "c"}},
		{`{"operator":"AND","child":[]}`, []string{"a", "b", "c"}},
		{`{"operator":"OR","child":[]}`, nil},
	} {
		got := runOne(t, `{"type":"Conditional Filter","args":{"rule":`+c.rule+`}}`, in())
		if g := namesOf(got); !slices.Equal(g, c.want) && !(len(g) == 0 && len(c.want) == 0) {
			t.Errorf("rule %s kept %q, want %q", c.rule, g, c.want)
		}
	}
	// A tree the bundle refuses falls back to it rather than guessing.
	for _, rule := range []string{`["type=vless"]`, `{"proposition":"equals","attr":"type"}`, `{"operator":"NOT","child":[]}`, `{"proposition":"IN","attr":"type","value":null}`, `null`} {
		plan, err := Compile("r", []json.RawMessage{json.RawMessage(`{"type":"Conditional Filter","args":{"rule":` + rule + `}}`)})
		if err != nil || plan.Native() || plan.Steps[0].Diag[0].Code != CodeArgumentShape {
			t.Errorf("rule %s: native=%v err=%v", rule, plan != nil && plan.Native(), err)
		}
	}
}

// Handle Duplicate numbers each repeated key from 1, zero-padded to the
// width of the largest count across all keys, every digit written with the
// template's glyph.
func TestHandleDuplicateDigitTemplate(t *testing.T) {
	got := runOne(t, `{"type":"Handle Duplicate Operator","args":{"action":"rename","link":"_","template":"⓪ ① ② ③ ④ ⑤ ⑥ ⑦ ⑧ ⑨"}}`, named(t, "A", "B", "A", "A", "B", "C"))
	wantNames(t, got, "A_①", "B_①", "A_②", "A_③", "B_②", "C")

	in := named(t, "B", "B")
	for i := 0; i < 12; i++ {
		in = append(in, node(t, "A"))
	}
	got = runOne(t, `{"type":"Handle Duplicate Operator","args":{}}`, in)
	if g := namesOf(got); g[0] != "B-01" || g[1] != "B-02" || g[2] != "A-01" || g[13] != "A-12" {
		t.Fatalf("padding: %q", g)
	}
	// A template with fewer glyphs than digits writes "undefined" for the
	// missing glyph, front puts the counter first.
	got = runOne(t, `{"type":"Handle Duplicate Operator","args":{"template":"a b c","position":"front"}}`, named(t, "A", "A", "A"))
	wantNames(t, got, "b-A", "c-A", "undefined-A")
	// delete keeps the first of each key; the key is the field list joined
	// with "_", an absent field reading as "-" and null as empty text.
	ns := []*nodemodel.Node{node(t, "n1", "aa", nil), node(t, "n2"), node(t, "n3", "aa", "null"), node(t, "n4", "aa", ""), node(t, "n5", "aa", "-")}
	wantNames(t, runOne(t, `{"type":"Handle Duplicate Operator","args":{"action":"delete","field":["aa"]}}`, ns), "n1", "n2", "n3")
	ns = []*nodemodel.Node{node(t, "n1", "aa", "x_", "bb", "y"), node(t, "n2", "aa", "x", "bb", "_y"), node(t, "n3", "aa", float64(443)), node(t, "n4", "aa", "443")}
	wantNames(t, runOne(t, `{"type":"Handle Duplicate Operator","args":{"action":"delete","field":["aa","bb"]}}`, ns), "n1", "n3")
	ns = []*nodemodel.Node{node(t, "n1", "reality-opts", map[string]any{"public-key": "k"}), node(t, "n2", "reality-opts", map[string]any{"public-key": "k"}), node(t, "n3")}
	wantNames(t, runOne(t, `{"type":"Handle Duplicate Operator","args":{"action":"delete","field":["reality-opts.public-key"]}}`, ns), "n1", "n3")
	wantNames(t, runOne(t, `{"type":"Handle Duplicate Operator","args":{"action":"skip"}}`, named(t, "A", "A")), "A", "A")
}

// The Taiwan flag becomes the China flag by default, the Samoa flag with
// "ws", and stays with "tw"; remove strips every flag.
func TestFlagTaiwanChoice(t *testing.T) {
	in := func() []*nodemodel.Node { return named(t, "TW 01", "台湾 02", "🇹🇼 node", "HK 03") }
	wantNames(t, runOne(t, `{"type":"Flag Operator","args":{"mode":"add"}}`, in()), "🇨🇳 TW 01", "🇨🇳 台湾 02", "🇨🇳 node", "🇭🇰 HK 03")
	wantNames(t, runOne(t, `{"type":"Flag Operator","args":{"mode":"add","tw":"ws"}}`, in()), "🇼🇸 TW 01", "🇼🇸 台湾 02", "🇼🇸 node", "🇭🇰 HK 03")
	wantNames(t, runOne(t, `{"type":"Flag Operator","args":{"tw":"tw"}}`, in()), "🇹🇼 TW 01", "🇹🇼 台湾 02", "🇹🇼 node", "🇭🇰 HK 03")
	wantNames(t, runOne(t, `{"type":"Flag Operator","args":{"mode":"remove"}}`, named(t, "🇹🇼 node", "x 🇺🇸 y 🇯🇵 z", "🏴‍☠️ a", "🏳️‍🌈 b", "🏁 c")), "node", "x  y  z", "a", "b", "🏁 c")
}

// The flag a name stands for: a word anywhere in any case beats a code; a
// code is a whole run of ASCII letters in its own case; a flag already in
// the name answers only when no keyword does.
func TestNameFlag(t *testing.T) {
	for name, want := range map[string]string{
		"HK abcde 0001":  "🇭🇰",
		"hk node":        pirateFlag,
		"HK01":           "🇭🇰",
		"SHK 01":         pirateFlag,
		"US HK":          "🇭🇰",
		"JP-香港":          "🇭🇰",
		"xJapan":         "🇯🇵",
		"Virginia BY":    "🇺🇸",
		"Romania":        "🇴🇲",
		"🇯🇵 US":          "🇺🇸",
		"🇺🇸 node":        "🇺🇸",
		"x 🇺🇸 y 🇯🇵 z":    "🇺🇸",
		"Bandwidth: 1GB": "🏳️‍🌈",
		"CN2 GIA":        pirateFlag,
		"CN2x":           "🇨🇳",
		"HKBN":           "🇭🇰",
		"AFG 1":          "🇦🇫",
	} {
		if got := nameFlag(name); got != want {
			t.Errorf("nameFlag(%q) = %q, want %q", name, got, want)
		}
	}
}

// The Region Filter compares the flag before the Taiwan choice, over the
// eight regions the bundle maps; any other code matches nothing.
func TestRegionFilter(t *testing.T) {
	in := func() []*nodemodel.Node { return named(t, "HK n", "TW n", "🇹🇼 m", "GB n", "UK n", "FR n", "x") }
	wantNames(t, runOne(t, `{"type":"Region Filter","args":{"value":["TW","UK"]}}`, in()), "TW n", "🇹🇼 m", "GB n", "UK n")
	wantNames(t, runOne(t, `{"type":"Region Filter","args":["FR","CN"]}`, in()))
	wantNames(t, runOne(t, `{"type":"Region Filter","args":{"value":["HK"],"keep":false}}`, in()), "TW n", "🇹🇼 m", "GB n", "UK n", "FR n", "x")
	wantNames(t, runOne(t, `{"type":"Region Filter","args":{"value":["HK"],"keep":null}}`, in()), "HK n")
}

func TestTypeFilterAndKeep(t *testing.T) {
	in := func() []*nodemodel.Node {
		return []*nodemodel.Node{node(t, "a"), node(t, "v", "type", "vmess"), node(t, "s", "type", "ss")}
	}
	wantNames(t, runOne(t, `{"type":"Type Filter","args":{"value":["vmess","ss"]}}`, in()), "v", "s")
	wantNames(t, runOne(t, `{"type":"Type Filter","args":["vmess"]}`, in()), "v")
	wantNames(t, runOne(t, `{"type":"Type Filter","args":{"value":["vmess"],"keep":0}}`, in()), "a", "s")
	wantNames(t, runOne(t, `{"type":"Type Filter","args":{"value":["vmess"],"keep":"no"}}`, in()), "v")
	// null keeps for the Region and Type filters and drops for the Regex
	// Filter, as the bundle reads each.
	wantNames(t, runOne(t, `{"type":"Type Filter","args":{"value":["vmess"],"keep":null}}`, in()), "v")
	wantNames(t, runOne(t, `{"type":"Regex Filter","args":{"regex":["^v$"],"keep":null}}`, in()), "a", "s")
}

func TestUselessFilterAndQuickSetting(t *testing.T) {
	in := func() []*nodemodel.Node {
		return []*nodemodel.Node{
			node(t, "ok"), node(t, "剩余流量：10GB"), node(t, "Bandwidth: x"), node(t, "bandwidth"), node(t, "Expire soon"),
			node(t, "pw", "password", "密码"), node(t, "ctl", "password", "a\x7fb"),
			node(t, "host", "network", "ws", "ws-opts", map[string]any{"headers": map[string]any{"Host": "例子.example"}}),
			node(t, "h2", "network", "h2", "h2-opts", map[string]any{"host": []any{"例子.example"}}),
			node(t, "zero", "port", float64(0)), node(t, "big", "port", float64(70000)),
		}
	}
	wantNames(t, runOne(t, `{"type":"Useless Filter"}`, in()), "ok", "bandwidth", "Expire soon", "ctl", "h2", "zero", "big")
	wantNames(t, runOne(t, `{"type":"Quick Setting Operator","args":{"useless":"ENABLED"}}`, in()), "ok", "bandwidth", "Expire soon", "ctl", "h2")

	ns := []*nodemodel.Node{node(t, "t"), node(t, "v", "type", "vmess"), node(t, "s", "type", "snell"), node(t, "q", "type", "tuic")}
	runOne(t, `{"type":"Quick Setting Operator","args":{"udp":"DISABLED","tfo":"ENABLED","scert":true,"vmess aead":"ENABLED","reuse":"ENABLED","ecn":"DISABLED","block-quic":"xyz","ip-version":"ipv4-prefer"}}`, ns)
	for _, n := range ns {
		if n.Fields["udp"] != false || n.Fields["tfo"] != true || n.Fields["fast-open"] != true || n.Fields["ip-version"] != "ipv4-prefer" {
			t.Errorf("%s: %v", n.Name(), n.Fields)
		}
		if _, set := n.Fields["skip-cert-verify"]; set {
			t.Errorf("%s: a boolean switch was applied: %v", n.Name(), n.Fields)
		}
		if _, set := n.Fields["block-quic"]; set {
			t.Errorf("%s: block-quic took a value outside auto, on, off", n.Name())
		}
		_, aead := n.Fields["aead"]
		_, reuse := n.Fields["reuse"]
		_, ecn := n.Fields["ecn"]
		if aead != (n.Type() == "vmess") || reuse != (n.Type() == "snell") || ecn != (n.Type() == "tuic") {
			t.Errorf("%s: type-scoped switches landed on the wrong types: %v", n.Name(), n.Fields)
		}
	}
}

// Sort compares names by UTF-16 code unit, as ECMAScript's < does: a
// character above U+FFFF sorts before U+FF21.
func TestSortOrdersByUTF16CodeUnit(t *testing.T) {
	in := func() []*nodemodel.Node { return named(t, "b", "a", "B", "ä", "😀", "Ａ", "10", "9") }
	wantNames(t, runOne(t, `{"type":"Sort Operator","args":"asc"}`, in()), "10", "9", "B", "a", "b", "ä", "😀", "Ａ")
	wantNames(t, runOne(t, `{"type":"Sort Operator","args":"desc"}`, in()), "Ａ", "😀", "ä", "b", "a", "B", "9", "10")
	if got := runOne(t, `{"type":"Sort Operator","args":"random"}`, in()); len(got) != 8 {
		t.Fatalf("random sort lost nodes: %q", namesOf(got))
	}
}

// Regex Sort puts each pattern's matches first in input order and orders
// the rest by the order argument.
func TestRegexSortGroups(t *testing.T) {
	in := func() []*nodemodel.Node {
		return named(t, "c JP 1", "a US 2", "b HK 3", "d JP 4", "e x 5", "a hk 6", "0 z 8")
	}
	wantNames(t, runOne(t, `{"type":"Regex Sort Operator","args":["HK","JP"]}`, in()), "b HK 3", "c JP 1", "d JP 4", "0 z 8", "a US 2", "a hk 6", "e x 5")
	wantNames(t, runOne(t, `{"type":"Regex Sort Operator","args":{"expressions":["HK","JP"],"order":"desc"}}`, in()), "b HK 3", "c JP 1", "d JP 4", "e x 5", "a hk 6", "a US 2", "0 z 8")
	wantNames(t, runOne(t, `{"type":"Regex Sort Operator","args":{"expressions":["(?i)hk"],"order":"original"}}`, in()), "b HK 3", "a hk 6", "c JP 1", "a US 2", "d JP 4", "e x 5", "0 z 8")
	// An object without expressions sorts everything by name.
	wantNames(t, runOne(t, `{"type":"Regex Sort Operator","args":{"value":["HK"]}}`, in()), "0 z 8", "a US 2", "a hk 6", "b HK 3", "c JP 1", "d JP 4", "e x 5")
}

func TestRegexDeleteAndInertSteps(t *testing.T) {
	wantNames(t, runOne(t, `{"type":"Regex Delete Operator","args":["(?i)us"," "]}`, named(t, "HK 01 hk", " JP-02 ", "US 03 US")), "HK01hk", "JP-02", "03")
	in := named(t, "a", "a", "b")
	wantNames(t, runOne(t, `{"type":"Remove Duplicate Filter"}`, in), "a", "a", "b")
	plan := compileOne(t, `{"type":"Add Proxies From Subscription Operator","args":{"sourceType":"subscription","sourceName":"x","position":"replace"}}`)
	if d := plan.Steps[0].Diag; len(d) != 1 || d[0].Code != CodeInertUntilFiles {
		t.Fatalf("add proxies diagnostics = %+v", d)
	}
	wantNames(t, plan.Run(named(t, "a"), nil), "a")
}
