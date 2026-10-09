package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
)

// The native operators answer what the embedded bundle answers. Both run
// over the same synthetic nodes: the bundle parses the document and runs the
// chain through engine.convert to its JSON producer; the native chain runs
// over the bundle's own parse of the same document (engine.convert with no
// chain, JSON producer), so the comparison sees the operators and nothing
// else. Node lists are compared as JSON values.

// parityDocument is perfgen's Reality links, some renamed or re-ported so
// every filter has something to keep and something to drop, plus a few
// nodes of other types.
func parityDocument(n int) string {
	lines := perfgen.URIs(n)
	for i := range lines {
		switch {
		case i%17 == 5:
			lines[i] = renameURI(lines[i], fmt.Sprintf("剩余流量：%d GB", i))
		case i%19 == 7:
			lines[i] = renameURI(lines[i], fmt.Sprintf("TW 台湾 %04d", i+1))
		case i%23 == 11:
			lines[i] = renameURI(lines[i], fmt.Sprintf("🇺🇸 Los Angeles %04d", i+1))
		}
	}
	lines = append(lines,
		"trojan://pw@192.0.2.10:443?sni=t.example.com#JP trojan 9001",
		"ss://YWVzLTEyOC1nY206cGFzcw==@192.0.2.11:8388#SG ss 9002",
		"vmess://"+base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"KR vmess 9003","add":"192.0.2.12","port":"443","id":"0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35","aid":"0","net":"ws","type":"none","host":"v.example.com","path":"/ws","tls":"tls"}`)),
		"hysteria2://pw@192.0.2.13:443?sni=h.example.com#DE hy2 9004",
	)
	return strings.Join(lines, "\n")
}

func renameURI(link, name string) string {
	return link[:strings.LastIndexByte(link, '#')+1] + strings.ReplaceAll(name, " ", "%20")
}

// bundleNodes runs one conversion on the embedded bundle and returns the
// JSON producer's node list.
func bundleNodes(t *testing.T, engine *subStoreEngine, raw string, chain []json.RawMessage) []any {
	t.Helper()
	out, err := engine.convert(subStoreConversionRequest{Raw: raw, Target: "JSON", Operators: chain})
	if err != nil {
		t.Fatalf("bundle convert: %v", err)
	}
	var nodes []any
	if err := json.Unmarshal([]byte(out.Output), &nodes); err != nil {
		t.Fatalf("bundle output: %v", err)
	}
	return nodes
}

// nativeNodes runs a chain natively over nodes decoded from base, the
// bundle's own parse.
func nativeNodes(t *testing.T, base []any, chain []json.RawMessage) []any {
	t.Helper()
	plan, err := operators.Compile("parity", chain)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !plan.Native() {
		t.Fatalf("chain is not native: %+v", plan.Steps)
	}
	nodes := make([]*nodemodel.Node, len(base))
	for i, b := range base {
		raw, _ := json.Marshal(b)
		nodes[i] = &nodemodel.Node{}
		if err := nodes[i].UnmarshalJSON(raw); err != nil {
			t.Fatal(err)
		}
	}
	nodes = plan.Run(nodes, &operators.Context{Target: "JSON"})
	raw, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func nodeNames(nodes []any) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i], _ = n.(map[string]any)["name"].(string)
	}
	return out
}

func sameNodes(a, b []any) bool {
	return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b))
}

// asMultiset is the list in a canonical order, for comparisons that do not
// hold the order.
func asMultiset(nodes []any) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		b, _ := json.Marshal(n)
		out[i] = string(b)
	}
	sort.Strings(out)
	return out
}

func chain(steps ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(steps))
	for i, s := range steps {
		out[i] = json.RawMessage(s)
	}
	return out
}

func TestOperatorsMatchBundleOnSyntheticNodes(t *testing.T) {
	engine := sharedWarmTestEngine(t)
	raw := parityDocument(120)
	base := bundleNodes(t, engine, raw, nil)
	if len(base) != 124 {
		t.Fatalf("the bundle parsed %d nodes, want 124", len(base))
	}

	// One case per operator, several where its arguments choose a different
	// path, and the perf gate's chain. Each holds both engines to the same
	// node list.
	exact := []struct {
		name  string
		chain []json.RawMessage
	}{
		{"quick setting", chain(`{"type":"Quick Setting Operator","args":{"udp":"DISABLED","tfo":"ENABLED","scert":"ENABLED","vmess aead":"ENABLED","reuse":"ENABLED","ecn":"ENABLED","block-quic":"on","ip-version":"ipv4-prefer","useless":"ENABLED"}}`)},
		{"useless filter", chain(`{"type":"Useless Filter"}`)},
		{"region filter keep", chain(`{"type":"Region Filter","args":{"value":["HK","TW","SG","JP","UK","US","DE","KR"],"keep":true}}`)},
		{"region filter drop", chain(`{"type":"Region Filter","args":{"value":["JP","US"],"keep":false}}`)},
		{"type filter keep", chain(`{"type":"Type Filter","args":{"value":["trojan","ss","vmess"]}}`)},
		{"type filter drop", chain(`{"type":"Type Filter","args":{"value":["vless"],"keep":false}}`)},
		{"regex filter keep", chain(`{"type":"Regex Filter","args":{"regex":["(?i)^hk","JP"],"keep":true}}`)},
		{"regex filter drop", chain(`{"type":"Regex Filter","args":{"regex":["\\d{3}[13579]$"],"keep":false}}`)},
		{"conditional filter", chain(`{"type":"Conditional Filter","args":{"rule":{"operator":"AND","child":[{"proposition":"EQUALS","attr":"type","value":"vless"},{"operator":"NOT","child":{"proposition":"IN","attr":"port","value":[443]}}]}}}`)},
		{"conditional filter or", chain(`{"type":"Conditional Filter","args":{"rule":{"operator":"OR","child":[{"proposition":"CONTAINS","attr":"server","value":"example.net"},{"proposition":"EQUALS","attr":"network","value":"grpc"},{"proposition":"IN","attr":"type","value":"trojan hysteria2"}]}}}`)},
		{"remove duplicate filter", chain(`{"type":"Remove Duplicate Filter"}`)},
		{"flag add", chain(`{"type":"Flag Operator","args":{"mode":"add"}}`)},
		{"flag add samoa", chain(`{"type":"Flag Operator","args":{"mode":"add","tw":"ws"}}`)},
		{"flag add then remove", chain(`{"type":"Flag Operator","args":{"mode":"add","tw":"tw"}}`, `{"type":"Flag Operator","args":{"mode":"remove"}}`)},
		{"sort asc", chain(`{"type":"Sort Operator","args":"asc"}`)},
		{"sort desc", chain(`{"type":"Sort Operator","args":"desc"}`)},
		{"regex rename", chain(`{"type":"Regex Rename Operator","args":[{"expr":"^(\\w+) (\\w+) (\\d+)$","now":"$3 $1 $2"},{"expr":"(?i)hk","now":"Hong Kong"}]}`)},
		{"regex delete", chain(`{"type":"Regex Delete Operator","args":["\\s\\d+$","^\\s+"]}`)},
		{"handle duplicate rename", chain(`{"type":"Regex Rename Operator","args":[{"expr":"^(\\w+) .*$","now":"$1"}]}`, `{"type":"Handle Duplicate Operator","args":{}}`)},
		{"handle duplicate glyphs", chain(`{"type":"Handle Duplicate Operator","args":{"action":"rename","field":["port"],"position":"front","link":"_","template":"⓪ ① ② ③ ④ ⑤ ⑥ ⑦ ⑧ ⑨"}}`)},
		{"handle duplicate delete", chain(`{"type":"Handle Duplicate Operator","args":{"action":"delete","field":["port","network"]}}`)},
		{"add proxies from subscription", chain(`{"type":"Add Proxies From Subscription Operator","args":{"sourceType":"subscription","sourceName":"other","position":"replace"}}`)},
		{"perf gate chain", chain(`{"type":"Regex Filter","args":{"regex":["(?i)^(hk|jp|sg|us|tw)"],"keep":true}}`, `{"type":"Regex Rename Operator","args":[{"expr":"^(\\w+) (\\w+) (\\d+)$","now":"$1 $3"}]}`, `{"type":"Sort Operator","args":"asc"}`, `{"type":"Flag Operator","args":{"mode":"add"}}`)},
	}
	for _, c := range exact {
		want := bundleNodes(t, engine, raw, c.chain)
		got := nativeNodes(t, base, c.chain)
		if !sameNodes(got, want) {
			t.Errorf("%s: native and bundle differ\nnative %q\nbundle %q", c.name, nodeNames(got), nodeNames(want))
			continue
		}
		t.Logf("%-30s match: %d of %d nodes", c.name, len(got), len(base))
	}

	// Known differences from the pinned bundle (2.36.22), each held to what
	// it is.

	// Sort random: the order is random in both; the nodes are the same.
	random := chain(`{"type":"Sort Operator","args":"random"}`)
	if got, want := nativeNodes(t, base, random), bundleNodes(t, engine, raw, random); !slices.Equal(asMultiset(got), asMultiset(want)) {
		t.Errorf("sort random: native and bundle hold different nodes")
	}

	// Regex Sort: the bundle's comparator answers "after" for two nodes one
	// expression matches, never "equal", so QuickJS's unstable sort leaves
	// such a group in an order of its own; native keeps the group in input
	// order. The groups, their order and the unmatched tail agree.
	regexSort := chain(`{"type":"Regex Sort Operator","args":{"expressions":["^HK","^JP","^(SG|US)"],"order":"desc"}}`)
	got, want := nativeNodes(t, base, regexSort), bundleNodes(t, engine, raw, regexSort)
	group := func(name string) int {
		for i, p := range []string{"^HK", "^JP", "^(SG|US)"} {
			if regexp.MustCompile(p).MatchString(name) {
				return i
			}
		}
		return 3
	}
	gn, wn := nodeNames(got), nodeNames(want)
	if !slices.Equal(asMultiset(got), asMultiset(want)) || len(gn) != len(wn) {
		t.Fatalf("regex sort: native and bundle hold different nodes")
	}
	for i := range gn {
		if group(gn[i]) != group(wn[i]) || group(gn[i]) == 3 && gn[i] != wn[i] {
			t.Fatalf("regex sort: position %d native %q bundle %q", i, gn[i], wn[i])
		}
	}

	// Conditional Filter EXISTS: upstream's test is true for every node
	// (upstream.md 5.6) and the bundle keeps them all; native keeps the
	// nodes carrying the field (design-28.md:168).
	exists := chain(`{"type":"Conditional Filter","args":{"rule":{"proposition":"EXISTS","attr":"flow"}}}`)
	if bundleKept := bundleNodes(t, engine, raw, exists); len(bundleKept) != len(base) {
		t.Errorf("the bundle's EXISTS kept %d of %d nodes; the known defect keeps all, so the allowance is stale", len(bundleKept), len(base))
	}
	var withFlow []any
	for _, n := range base {
		if v, ok := n.(map[string]any)["flow"]; ok && v != nil {
			withFlow = append(withFlow, n)
		}
	}
	if got := nativeNodes(t, base, exists); len(withFlow) == 0 || len(withFlow) == len(base) || !sameNodes(got, withFlow) {
		t.Errorf("native EXISTS kept %d nodes, want the %d carrying flow", len(got), len(withFlow))
	}
}

// The flag tables answer what the bundle answers for every keyword they
// carry, each in another letter case, beside other text and beside a flag
// already in the name, and for pairs of keywords, where the order of the
// tables decides.
func TestFlagOperatorMatchesBundleOnKeywordVocabulary(t *testing.T) {
	engine := sharedWarmTestEngine(t)
	keywords := operators.FlagKeywords()
	var names []string
	for i, k := range keywords {
		switch i % 4 {
		case 0:
			names = append(names, strings.ToUpper(k))
		case 1:
			names = append(names, "1"+k+" 02")
		case 2:
			names = append(names, "🇯🇵 x"+k)
		default:
			names = append(names, k+"x")
		}
		names = append(names, k+" | "+keywords[(i*37+11)%len(keywords)])
	}
	nodes := make([]map[string]any, len(names))
	for i, name := range names {
		nodes[i] = map[string]any{"name": name, "type": "trojan", "server": fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255), "port": 443, "password": "p"}
	}
	doc, err := json.Marshal(map[string]any{"proxies": nodes})
	if err != nil {
		t.Fatal(err)
	}
	flag := chain(`{"type":"Flag Operator","args":{"mode":"add","tw":"tw"}}`)
	raw := string(doc)
	base := bundleNodes(t, engine, raw, nil)
	if len(base) != len(names) {
		t.Fatalf("the bundle parsed %d of %d names", len(base), len(names))
	}
	got, want := nodeNames(nativeNodes(t, base, flag)), nodeNames(bundleNodes(t, engine, raw, flag))
	mismatches := 0
	for i := range want {
		if got[i] != want[i] {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("%q: native %q, bundle %q", names[i], got[i], want[i])
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("%d of %d names differ", mismatches, len(names))
	}
	t.Logf("%d keywords, %d names agree", len(keywords), len(names))
}

// The native compiler and the store validate one vocabulary.
func TestNativeVocabularyMatchesProcessVocabulary(t *testing.T) {
	var store []string
	for name := range processVocabulary() {
		store = append(store, name)
	}
	sort.Strings(store)
	if native := operators.Vocabulary(); !slices.Equal(native, store) {
		t.Fatalf("operators.Vocabulary() = %q\nprocessVocabulary() = %q", native, store)
	}
}
