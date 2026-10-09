package producers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
	"gopkg.in/yaml.v3"
)

// conformanceDir is the vendored copy of the conformance harness at the
// plugin repository root (tools/conformance-sync.sh writes it).
const conformanceDir = "../../conformance"

// harnessTargets are the five native targets by harness id, with the
// platform name the harness's check.mjs calls each producer by
// (oracle/lib/targets.mjs), the golden file extension, the canonical form the
// checker parses the output with, and whether the checker compares bytes. For
// URI and V2Ray it does, and the allowlist never applies to them, so equal
// bytes is the checker's whole verdict.
var harnessTargets = []struct {
	id, platform, ext, form string
	bytes                   bool
}{
	{"uri", "URI", "txt", "uri", true},
	{"v2ray", "V2Ray", "txt", "v2ray", true},
	{"json", "JSON", "json", "json", false},
	{"singbox", "sing-box", "json", "json", false},
	{"clashmeta", "ClashMeta", "yaml", "yaml", false},
}

// produceCase is one corpus case check.mjs produces from: its golden nodes
// and the options of its meta.json.
type produceCase struct {
	id      string
	options Options
	nodes   json.RawMessage
}

// produceCases lists the corpus cases whose parse golden is a node list, the
// ones check.mjs produces for, sorted by id as check.mjs sorts them.
func produceCases(t *testing.T) []produceCase {
	t.Helper()
	var cases []produceCase
	for _, sub := range []string{"inputs", "fleet"} {
		root := filepath.Join(conformanceDir, "corpus", sub)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".meta.json") {
				return err
			}
			var meta struct {
				ID      string  `json:"id"`
				Options Options `json:"options"`
			}
			if err := json.Unmarshal(readFile(t, path), &meta); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			nodes := readFile(t, filepath.Join(conformanceDir, "goldens", "parse", meta.ID+".json"))
			if bytes.HasPrefix(bytes.TrimSpace(nodes), []byte("[")) {
				cases = append(cases, produceCase{id: meta.ID, options: meta.Options, nodes: nodes})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(cases) == 0 {
		t.Fatalf("no corpus cases under %s", conformanceDir)
	}
	slices.SortFunc(cases, func(a, b produceCase) int { return strings.Compare(a.id, b.id) })
	return cases
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeNodes(t *testing.T, raw json.RawMessage) []*nodemodel.Node {
	t.Helper()
	var nodes []*nodemodel.Node
	if err := json.Unmarshal(raw, &nodes); err != nil {
		t.Fatal(err)
	}
	return nodes
}

func produce(t *testing.T, target string, nodes []*nodemodel.Node, opts Options) (string, Result) {
	t.Helper()
	p, ok := Lookup(target)
	if !ok {
		t.Fatalf("Lookup(%q) found no producer", target)
	}
	var buf bytes.Buffer
	res, err := p.Produce(&buf, nodes, target, opts)
	if err != nil {
		t.Fatalf("Produce(%q): %v", target, err)
	}
	return buf.String(), res
}

// TestProduceCorpusMatchesGoldens produces every corpus case from its golden
// nodes with its options, as check.mjs does, and compares the output with the
// produce golden by the checker's rule for the target.
func TestProduceCorpusMatchesGoldens(t *testing.T) {
	cases := produceCases(t)
	for _, target := range harnessTargets {
		t.Run(target.id, func(t *testing.T) {
			if _, native := Lookup(target.platform); !native {
				t.Fatalf("%s has no native producer", target.platform)
			}
			dir := filepath.Join(conformanceDir, "goldens", "produce", target.id)
			judged, passed := 0, 0
			for _, c := range cases {
				canonFile := filepath.Join(dir, c.id+".canon.json")
				if _, err := os.Stat(canonFile); err != nil {
					continue
				}
				judged++
				got, _ := produce(t, target.platform, decodeNodes(t, c.nodes), c.options)
				if target.bytes {
					golden := string(readFile(t, filepath.Join(dir, c.id+"."+target.ext)))
					if got != golden {
						t.Errorf("%s: output differs from the golden at byte %d\n golden:    %q\n candidate: %q", c.id, firstDifference(golden, got), clip(golden), clip(got))
						continue
					}
				} else if path, golden, candidate, same := sameStructure(t, readFile(t, canonFile), target.form, got); !same {
					t.Errorf("%s: output differs from the golden at %s\n golden:    %s\n candidate: %s\n output:    %q", c.id, path, golden, candidate, clip(got))
					continue
				}
				passed++
			}
			if judged == 0 {
				t.Fatalf("no produce goldens under %s", dir)
			}
			t.Logf("%s: %d/%d cases match", target.id, passed, judged)
		})
	}
}

func firstDifference(a, b string) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}

// sameStructure compares a produced document with its golden's canonical
// form by the vendored checker's rule (oracle/lib/canonical.mjs and firstDiff
// in oracle/check.mjs): the output is parsed back, json as JSON.parse reads it
// and yaml as a YAML 1.2 document, and the two values are compared with object
// key order ignored and list order kept. Output that does not parse is
// {unparsed: <text>}, which only equal text matches. When the two differ it
// returns the first differing path and both values there.
func sameStructure(t *testing.T, canon []byte, form, output string) (path, golden, candidate string, same bool) {
	t.Helper()
	var want any
	if err := json.Unmarshal(canon, &want); err != nil {
		t.Fatal(err)
	}
	return firstDiff(want, parseCanonical(t, form, output), "$")
}

func parseCanonical(t *testing.T, form, output string) any {
	t.Helper()
	unparsed := map[string]any{"unparsed": output}
	var v any
	switch form {
	case "json":
		if err := json.Unmarshal([]byte(output), &v); err != nil {
			return unparsed
		}
		return v
	case "yaml":
		if err := yaml.Unmarshal([]byte(output), &v); err != nil {
			return unparsed
		}
		j, ok := jsonValue(v)
		if !ok {
			return unparsed
		}
		return j
	}
	t.Fatalf("no canonical form %q", form)
	return nil
}

// jsonValue turns a decoded YAML value into what JSON.parse(JSON.stringify(v))
// gives in the checker: numbers become float64, not-a-number and the
// infinities become null. A value JSON cannot carry reports false.
func jsonValue(v any) (any, bool) {
	switch x := v.(type) {
	case nil, bool, string:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, true
		}
		return x, true
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			var ok bool
			if out[i], ok = jsonValue(e); !ok {
				return nil, false
			}
		}
		return out, true
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			var ok bool
			if out[k], ok = jsonValue(e); !ok {
				return nil, false
			}
		}
		return out, true
	}
	return nil, false
}

// firstDiff is check.mjs's firstDiff over decoded JSON values.
func firstDiff(a, b any, path string) (string, string, string, bool) {
	if reflect.DeepEqual(a, b) {
		return "", "", "", true
	}
	switch x := a.(type) {
	case []any:
		if y, ok := b.([]any); ok {
			for i := 0; i < max(len(x), len(y)); i++ {
				p := path + "[" + strconv.Itoa(i) + "]"
				if i >= len(x) || i >= len(y) {
					return p, showAt(x, i), showAt(y, i), false
				}
				if p, g, c, same := firstDiff(x[i], y[i], p); !same {
					return p, g, c, false
				}
			}
		}
	case map[string]any:
		if y, ok := b.(map[string]any); ok {
			keys := slices.Sorted(maps.Keys(x))
			for k := range y {
				if _, ok := x[k]; !ok {
					keys = append(keys, k)
				}
			}
			slices.Sort(keys)
			for _, k := range keys {
				p := path + "." + k
				xv, inX := x[k]
				yv, inY := y[k]
				if !inX || !inY {
					return p, showPresent(xv, inX), showPresent(yv, inY), false
				}
				if p, g, c, same := firstDiff(xv, yv, p); !same {
					return p, g, c, false
				}
			}
		}
	}
	return path, show(a), show(b), false
}

func showAt(l []any, i int) string {
	if i < len(l) {
		return show(l[i])
	}
	return "<missing>"
}

func showPresent(v any, present bool) string {
	if !present {
		return "<missing>"
	}
	return show(v)
}

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return clip(string(b))
}

func clip(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

func node(fields map[string]any) *nodemodel.Node { return &nodemodel.Node{Fields: fields} }

func socks5(name string) map[string]any {
	return map[string]any{"type": "socks5", "name": name, "server": "192.0.2.1", "port": float64(1080), "udp": true}
}

func with(fields map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+len(extra))
	for k, v := range fields {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func reasons(d []Dropped) []string {
	out := make([]string, len(d))
	for i, x := range d {
		out[i] = strconv.Itoa(x.Index) + ":" + x.Reason
	}
	return out
}

// TestSupportMapKeyedByExactTarget pins that the per-node supported map is
// keyed by the exact target string the caller used: supported.URI = false
// drops the node for URI and keeps it for uri, and so on for every name each
// specification lists.
func TestSupportMapKeyedByExactTarget(t *testing.T) {
	for _, tc := range []struct {
		key     string
		drops   []string
		ignores []string
	}{
		{"URI", []string{"URI"}, []string{"uri"}},
		{"uri", []string{"uri"}, []string{"URI"}},
		{"V2Ray", []string{"V2Ray"}, []string{"v2ray", "v2"}},
		{"v2", []string{"v2"}, []string{"V2Ray", "v2ray"}},
		{"JSON", []string{"JSON"}, []string{"json"}},
		{"sing-box", []string{"sing-box"}, []string{"singbox"}},
		{"ClashMeta", []string{"ClashMeta"}, []string{"clashmeta", "meta", "clash.meta", "Clash.Meta", "mihomo", "Mihomo"}},
		{"mihomo", []string{"mihomo"}, []string{"ClashMeta", "Mihomo"}},
	} {
		n := node(with(socks5("s1"), map[string]any{"supported": map[string]any{tc.key: false}}))
		for _, target := range tc.drops {
			p, _ := Lookup(target)
			out, res := produce(t, target, []*nodemodel.Node{n}, nil)
			if out != string(p.EmptyDocument(nil)) || res.Entries != 0 || !slices.Equal(reasons(res.Dropped), []string{"0:" + ReasonSupportMap}) {
				t.Errorf("supported.%s=false for %s: output %q, entries %d, dropped %v; want the node dropped by the support map", tc.key, target, out, res.Entries, res.Dropped)
			}
		}
		for _, target := range tc.ignores {
			out, res := produce(t, target, []*nodemodel.Node{n}, nil)
			if out == "" || res.Entries != 1 || len(res.Dropped) != 0 {
				t.Errorf("supported.%s=false for %s: output %q, entries %d, dropped %v; want the node produced", tc.key, target, out, res.Entries, res.Dropped)
			}
		}
	}
	// A true entry, or any value other than false, never drops.
	for _, v := range []any{true, "false", float64(0), nil} {
		n := node(with(socks5("s1"), map[string]any{"supported": map[string]any{"URI": v}}))
		if _, res := produce(t, "URI", []*nodemodel.Node{n}, nil); res.Entries != 1 {
			t.Errorf("supported.URI=%#v dropped the node: %v", v, res.Dropped)
		}
	}
}

// TestLookupAcceptsExactNamesOnly pins the target strings the native
// producers answer: each specification's names and the harness ids, exactly
// as written, and nothing for the nine bundle targets.
func TestLookupAcceptsExactNamesOnly(t *testing.T) {
	for target, id := range map[string]string{
		"URI": "uri", "uri": "uri", "V2Ray": "v2ray", "v2ray": "v2ray", "v2": "v2ray",
		"JSON": "json", "json": "json", "sing-box": "singbox", "singbox": "singbox",
		"ClashMeta": "clashmeta", "clashmeta": "clashmeta", "meta": "clashmeta", "clash.meta": "clashmeta",
		"Clash.Meta": "clashmeta", "mihomo": "clashmeta", "Mihomo": "clashmeta",
	} {
		p, ok := Lookup(target)
		if !ok || p.ID() != id {
			t.Errorf("Lookup(%q) = %v, %v; want the %s producer", target, p, ok, id)
		}
	}
	for _, target := range []string{
		"Uri", "V2RAY", "v2rayN", "", "Json", "SING-BOX", "Sing-Box", "sing_box", "Meta", "MIHOMO", "clashMeta", "ClashMETA",
		"Stash", "Surge", "SurgeMac", "Loon", "Shadowrocket", "QX", "Egern", "Surfboard", "Clash", "clash",
	} {
		if Native(target) {
			t.Errorf("Native(%q) = true; want the bundle to answer it", target)
		}
	}
}

// TestZeroNodeCountsEntriesNotText pins the zero-node rule: a document is
// empty when the producer wrote no entry, whatever its text. V2Ray gives
// nodes without a URI form an empty line, so two of them encode to "Cg==",
// which is not the empty document and still holds no link. sing-box and
// ClashMeta write a skeleton when every node is dropped, which is not the
// empty string either, and sing-box counts entries, so a chained node counts
// twice.
func TestZeroNodeCountsEntriesNotText(t *testing.T) {
	httpNode := func(name string) *nodemodel.Node {
		return node(map[string]any{"type": "http", "name": name, "server": "192.0.2.2", "port": float64(8080), "udp": false})
	}
	unsupported := []*nodemodel.Node{httpNode("h1"), httpNode("h2")}

	out, res := produce(t, "V2Ray", unsupported, nil)
	if out != "Cg==" || res.Entries != 0 {
		t.Errorf("V2Ray of two http nodes: output %q, entries %d; want Cg== and 0 entries", out, res.Entries)
	}
	if want := []string{"0:" + ReasonUnsupported, "1:" + ReasonUnsupported}; !slices.Equal(reasons(res.Dropped), want) {
		t.Errorf("V2Ray of two http nodes dropped %v, want %v", reasons(res.Dropped), want)
	}
	if p, _ := Lookup("V2Ray"); out == string(p.EmptyDocument(nil)) {
		t.Errorf("V2Ray: Cg== must not equal the empty document")
	}

	out, res = produce(t, "V2Ray", []*nodemodel.Node{httpNode("h1"), node(socks5("s1")), httpNode("h2")}, nil)
	decoded, err := base64.StdEncoding.DecodeString(out)
	if err != nil {
		t.Fatal(err)
	}
	if res.Entries != 1 || strings.Count(string(decoded), "\n") != 2 || !strings.Contains(string(decoded), "socks://") {
		t.Errorf("V2Ray of http, socks5, http: decoded %q, entries %d; want one link between two empty lines", decoded, res.Entries)
	}

	out, res = produce(t, "URI", unsupported, nil)
	if out != "" || res.Entries != 0 {
		t.Errorf("URI of two http nodes: output %q, entries %d; want the empty string and 0 entries", out, res.Entries)
	}

	// Skeletons: every node dropped leaves the empty document, never "".
	direct := func(name string) *nodemodel.Node {
		return node(map[string]any{"type": "direct", "name": name, "udp": true})
	}
	juicity := func(name string) *nodemodel.Node {
		return node(map[string]any{"type": "juicity", "name": name, "server": "192.0.2.9", "port": float64(443), "uuid": "u", "password": "p", "udp": true})
	}
	for _, tc := range []struct {
		target string
		nodes  []*nodemodel.Node
		opts   Options
		want   string
	}{
		{"sing-box", []*nodemodel.Node{direct("d1"), direct("d2")}, nil, "{\n  \"outbounds\": [],\n  \"endpoints\": []\n}"},
		{"ClashMeta", []*nodemodel.Node{juicity("j1"), juicity("j2")}, nil, "proxies:\n"},
		{"ClashMeta", []*nodemodel.Node{juicity("j1"), juicity("j2")}, Options{"prettyYaml": true}, "proxies: []\n"},
		{"JSON", []*nodemodel.Node{node(with(socks5("s"), map[string]any{"supported": map[string]any{"JSON": false}}))}, nil, "[]"},
	} {
		out, res := produce(t, tc.target, tc.nodes, tc.opts)
		p, _ := Lookup(tc.target)
		if out != tc.want || out != string(p.EmptyDocument(tc.opts)) || res.Entries != 0 || len(res.Dropped) != len(tc.nodes) {
			t.Errorf("%s %v of dropped nodes: output %q, entries %d, dropped %v; want the skeleton %q and 0 entries", tc.target, tc.opts, out, res.Entries, res.Dropped, tc.want)
		}
	}

	// A shadow-tls Shadowsocks node is two sing-box entries.
	chained := node(map[string]any{"type": "ss", "name": "c", "server": "192.0.2.10", "port": float64(443), "cipher": "aes-128-gcm", "password": "p", "udp": true,
		"plugin": "shadow-tls", "plugin-opts": map[string]any{"host": "h.example.com", "password": "q", "version": float64(3)}})
	if out, res := produce(t, "sing-box", []*nodemodel.Node{chained}, nil); res.Entries != 2 || !strings.Contains(out, `"c_shadowtls"`) {
		t.Errorf("sing-box of one shadow-tls node: entries %d, output %q; want the outbound and its helper", res.Entries, out)
	}
}

// TestIncludeUnsupportedProxy pins what include-unsupported-proxy changes:
// it bypasses the support map, root headers and Shadowsocks over TLS, and
// nothing else. Broken VLESS Reality and xhttp stream-one are dropped either
// way, and a type without a URI form still has none.
func TestIncludeUnsupportedProxy(t *testing.T) {
	nodes := []*nodemodel.Node{
		node(with(socks5("map"), map[string]any{"supported": map[string]any{"URI": false, "V2Ray": false}})),
		node(map[string]any{"type": "http", "name": "hdr", "server": "192.0.2.3", "port": float64(80), "udp": false, "headers": map[string]any{"X-A": "b"}}),
		node(map[string]any{"type": "ss", "name": "sstls", "server": "192.0.2.4", "port": float64(443), "cipher": "aes-128-gcm", "password": "pw", "tls": true, "udp": true}),
		node(map[string]any{"type": "vless", "name": "broken", "server": "192.0.2.5", "port": float64(443), "uuid": "u", "network": "tcp", "tls": true, "udp": true, "reality-opts": map[string]any{"short-id": "ab"}}),
		node(map[string]any{"type": "vless", "name": "one", "server": "192.0.2.6", "port": float64(443), "uuid": "u", "network": "xhttp", "tls": true, "udp": true,
			"xhttp-opts": map[string]any{"mode": "stream-one", "path": "/x", "download-settings": map[string]any{"server": "192.0.2.7"}}}),
	}

	out, res := produce(t, "URI", nodes, nil)
	want := []string{"0:" + ReasonSupportMap, "1:" + ReasonRootHeaders, "2:" + ReasonSSTLS, "3:" + ReasonBrokenReality, "4:" + ReasonXHTTPStreamOne}
	if out != "" || res.Entries != 0 || !slices.Equal(reasons(res.Dropped), want) {
		t.Errorf("without the option: output %q, entries %d, dropped %v; want nothing and dropped %v", out, res.Entries, reasons(res.Dropped), want)
	}

	opts := Options{"include-unsupported-proxy": true}
	out, res = produce(t, "URI", nodes, opts)
	wantLines := []string{
		"socks://Og%3D%3D@192.0.2.1:1080#map",
		"ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pw")) + "@192.0.2.4:443?udp=1&sni=192.0.2.4&security=tls#sstls",
	}
	want = []string{"1:" + ReasonUnsupported, "3:" + ReasonBrokenReality, "4:" + ReasonXHTTPStreamOne}
	if out != strings.Join(wantLines, "\n") || res.Entries != 2 || !slices.Equal(reasons(res.Dropped), want) {
		t.Errorf("with the option: output %q, entries %d, dropped %v; want %q and dropped %v", out, res.Entries, reasons(res.Dropped), strings.Join(wantLines, "\n"), want)
	}

	// On V2Ray the http node now reaches the producer and leaves an empty
	// line where the root-header step left nothing.
	out, _ = produce(t, "V2Ray", nodes, opts)
	if want := base64.StdEncoding.EncodeToString([]byte(wantLines[0] + "\n\n" + wantLines[1])); out != want {
		t.Errorf("V2Ray with the option: %q, want %q", out, want)
	}
	out, _ = produce(t, "V2Ray", nodes, nil)
	if out != "" {
		t.Errorf("V2Ray without the option: %q, want the empty string", out)
	}

	// A string option is truthy, as JavaScript reads it; an empty one is not.
	if _, res := produce(t, "URI", nodes, Options{"include-unsupported-proxy": "yes"}); res.Entries != 2 {
		t.Errorf(`include-unsupported-proxy "yes": %d entries, want 2`, res.Entries)
	}
	if _, res := produce(t, "URI", nodes, Options{"include-unsupported-proxy": ""}); res.Entries != 0 {
		t.Errorf(`include-unsupported-proxy "": %d entries, want 0`, res.Entries)
	}

	// ClashMeta: the option also skips the whole admission filter (juicity
	// is A9), and broken Reality still goes.
	meta := []*nodemodel.Node{
		node(map[string]any{"type": "juicity", "name": "j", "server": "192.0.2.11", "port": float64(443), "uuid": "u", "password": "p", "udp": true}),
		nodes[2], nodes[3],
	}
	_, res = produce(t, "ClashMeta", meta, nil)
	if want := []string{"0:" + ReasonUnsupported, "1:" + ReasonSSTLS, "2:" + ReasonBrokenReality}; res.Entries != 0 || !slices.Equal(reasons(res.Dropped), want) {
		t.Errorf("ClashMeta without the option: entries %d, dropped %v; want %v", res.Entries, reasons(res.Dropped), want)
	}
	out, res = produce(t, "ClashMeta", meta, opts)
	if want := []string{"2:" + ReasonBrokenReality}; res.Entries != 2 || !slices.Equal(reasons(res.Dropped), want) || !strings.Contains(out, `"type":"juicity"`) {
		t.Errorf("ClashMeta with the option: entries %d, dropped %v, output %q; want juicity and ss kept, %v", res.Entries, reasons(res.Dropped), out, want)
	}

	// sing-box: the option lifts F8, so ssr converts.
	ssr := []*nodemodel.Node{node(map[string]any{"type": "ssr", "name": "r", "server": "192.0.2.12", "port": float64(443), "cipher": "aes-128-cfb",
		"password": "p", "obfs": "plain", "protocol": "origin", "udp": true})}
	if _, res := produce(t, "sing-box", ssr, nil); res.Entries != 0 || !slices.Equal(reasons(res.Dropped), []string{"0:" + ReasonUnsupported}) {
		t.Errorf("sing-box ssr without the option: entries %d, dropped %v; want it dropped as unsupported", res.Entries, reasons(res.Dropped))
	}
	if out, res := produce(t, "sing-box", ssr, opts); res.Entries != 1 || !strings.Contains(out, `"type": "shadowsocksr"`) {
		t.Errorf("sing-box ssr with the option: entries %d, output %q; want a shadowsocksr outbound", res.Entries, out)
	}
}

// TestWalksWriteAddedSNILast pins the field order of the parameter walks
// for the one field a step adds that a walk writes: sni from disable-sni
// comes after every field the node had, so hysteria writes peer last and
// wireguard writes sni just before address (the two checked lines of
// uri.md, "Input"; no corpus case has such a node). tuic is exempt from the
// step and gets no sni at all.
func TestWalksWriteAddedSNILast(t *testing.T) {
	for _, tc := range []struct {
		fields map[string]any
		want   string
	}{
		{
			map[string]any{"type": "hysteria", "name": "h", "server": "192.0.2.10", "port": float64(443), "auth-str": "a",
				"disable-sni": true, "down": float64(20), "up": float64(10), "udp": true},
			"hysteria://192.0.2.10:443?auth=a&disable_sni=true&downmbps=20&udp=true&upmbps=10&peer=192.0.2.10#h",
		},
		{
			map[string]any{"type": "wireguard", "name": "w", "server": "192.0.2.12", "port": float64(51820), "private-key": "k",
				"public-key": "pk", "disable-sni": true, "udp": true, "ip": "10.0.0.2"},
			"wireguard://k@192.0.2.12:51820/?disable-sni=true&publickey=pk&udp=1&sni=192.0.2.12&address=10.0.0.2%2F32#w",
		},
		{
			map[string]any{"type": "tuic", "name": "t", "server": "192.0.2.13", "port": float64(443), "uuid": "u", "password": "p",
				"disable-sni": true, "udp": true, "alpn": []any{"h3"}, "congestion-controller": "cubic", "udp-relay-mode": "native"},
			"tuic://u:p@192.0.2.13:443?alpn=h3&congestion_control=cubic&disable_sni=1&udp=true&udp_relay_mode=native#t",
		},
	} {
		if out, _ := produce(t, "URI", []*nodemodel.Node{node(tc.fields)}, nil); out != tc.want {
			t.Errorf("%s:\n got  %s\n want %s", tc.fields["type"], out, tc.want)
		}
	}
}

// TestProduceLeavesCallerNodesUntouched pins that the steps, the URI
// preparation and the ClashMeta transforms work on copies: filling a name, a
// port and sni, removing fields, and writing inside ws-opts, plugin-opts,
// h2-opts, grpc-opts and the xhttp download settings never reach the
// caller's node.
func TestProduceLeavesCallerNodesUntouched(t *testing.T) {
	nodes := []*nodemodel.Node{
		node(map[string]any{"type": "hysteria2", "name": " ", "server": "192.0.2.8", "ports": "20000-20010/30000", "password": "p",
			"disable-sni": true, "tls": true, "udp": true, "id": "x", "resolved": nil}),
		node(map[string]any{"type": "vmess", "name": "ws", "server": "192.0.2.20", "port": float64(443), "uuid": "u", "cipher": "auto", "alterId": float64(0),
			"network": "ws", "tls": true, "udp": true, "ws-opts": map[string]any{"path": "/ws?ed=2048", "headers": map[string]any{"Host": "h.example.com"}}}),
		node(map[string]any{"type": "ss", "name": "plugin", "server": "192.0.2.21", "port": float64(443), "cipher": "aes-128-gcm", "password": "p",
			"skip-cert-verify": true, "udp": true, "plugin": "v2ray-plugin",
			"plugin-opts": map[string]any{"mode": "websocket", "tls": true, "mux": "true", "host": "w.example.com"}}),
		node(map[string]any{"type": "vless", "name": "h2", "server": "192.0.2.22", "port": float64(443), "uuid": "u", "network": "h2", "tls": true, "udp": true,
			"h2-opts": map[string]any{"path": []any{"/a", "/b"}, "headers": map[string]any{"Host": "h2.example.com"}}}),
		node(map[string]any{"type": "vless", "name": "grpc", "server": "192.0.2.23", "port": float64(443), "uuid": "u", "network": "grpc", "tls": true, "udp": true,
			"grpc-opts": map[string]any{"grpc-service-name": "s", "_grpc-type": "gun"}}),
		node(map[string]any{"type": "vless", "name": "xhttp", "server": "192.0.2.24", "port": float64(443), "uuid": "u", "network": "xhttp", "tls": true, "udp": true,
			"reality-opts": map[string]any{"public-key": "k"},
			"xhttp-opts":   map[string]any{"mode": "packet-up", "download-settings": map[string]any{"server": "192.0.2.25", "tls": true}}}),
		node(map[string]any{"type": "snell", "name": "snell", "server": "192.0.2.26", "port": float64(443), "psk": "k", "version": float64(4), "udp": true,
			"plugin": "shadow-tls", "plugin-opts": map[string]any{"host": "s.example.com", "password": "q", "version": float64(3)}}),
		node(map[string]any{"type": "trojan", "name": "st", "server": "192.0.2.27", "port": float64(443), "password": "p", "network": "tcp", "tls": true, "udp": true,
			"plugin": "shadow-tls", "plugin-opts": map[string]any{"host": "t.example.com", "password": "q", "version": float64(3), "alpn": []any{"h2"}}}),
	}
	before := make([][]byte, len(nodes))
	for i, n := range nodes {
		var err error
		if before[i], err = n.MarshalJSON(); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range harnessTargets {
		for _, opts := range []Options{nil, {"include-unsupported-proxy": true, "prettyYaml": true}} {
			produce(t, target.platform, nodes, opts)
		}
	}
	ClashMetaInternal(nodes, nil)
	for i, n := range nodes {
		after, err := n.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before[i], after) {
			t.Errorf("producing changed the caller's node:\n before %s\n after  %s", before[i], after)
		}
	}
}

// TestClashMetaInternalKeepsWhatT20Removes pins the internal mode the
// sing-box producer runs: every node passes (no admission filter), the
// transforms run, and T20 does not, so null values and underscore keys that
// sing-box reads stay on the node while the text form removes them.
func TestClashMetaInternalKeepsWhatT20Removes(t *testing.T) {
	in := []*nodemodel.Node{
		node(map[string]any{"type": "juicity", "name": "j", "server": "192.0.2.30", "port": float64(443), "udp": true}),
		node(map[string]any{"type": "trojan", "name": "t", "server": "192.0.2.31", "port": float64(443), "password": "p", "network": "tcp", "tls": true,
			"udp": true, "_dns_server": "d", "client-fingerprint": nil, "tls-fingerprint": "ab", "id": "7"}),
	}
	out := ClashMetaInternal(in, nil)
	if len(out) != 2 || out[0].Fields["type"] != "juicity" {
		t.Fatalf("ClashMetaInternal dropped or reordered nodes: %v", out)
	}
	f := out[1].Fields
	if _, ok := f["client-fingerprint"]; !ok || f["_dns_server"] != "d" {
		t.Errorf("internal mode removed what T20 removes: %v", f)
	}
	if _, ok := f["tls"]; ok || f["fingerprint"] != "ab" || f["id"] != nil {
		t.Errorf("internal mode skipped T15, T16 or T19: %v", f)
	}
	text, _ := produce(t, "ClashMeta", in[1:], nil)
	if strings.Contains(text, "_dns_server") || strings.Contains(text, "client-fingerprint") {
		t.Errorf("the text form kept what T20 removes: %s", text)
	}
}

// TestPrettyYAMLShortIDRewrite pins the pretty form's textual short-id
// rewrite (clashmeta.md, "Output shape"): a plain short id is quoted, a
// quoted one, null and an empty value keep or gain their quotes, and the
// rewrite also reaches a name that contains "short-id:", which leaves text
// that is not valid YAML. The default form has no rewrite, so a numeric short
// id (reachable only through scripts) stays a number there.
func TestPrettyYAMLShortIDRewrite(t *testing.T) {
	for in, want := range map[string]string{
		"short-id: 49afd48f\n":           "short-id: \"49afd48f\"\n",
		"short-id: \"08\"\n":             "short-id: \"08\"\n",
		"short-id: '08'\n":               "short-id: '08'\n",
		"short-id: null\n":               "short-id: null\n",
		"short-id:\n":                    "short-id: \"\"\n",
		"short-id:    \n":                "short-id: \"\"\n",
		"short-id: \"\n":                 "short-id: \"\"\"\n",
		"{short-id: ab, x: 1}":           "{short-id: \"ab\", x: 1}",
		"short-id: ab # c\nshort-id: cd": "short-id: \"ab\"# c\nshort-id: \"cd\"",
		"name: \"short-id: x\"\n":        "name: \"short-id: \"x\"\"\n",
		"proxies: []\n":                  "proxies: []\n",
	} {
		if got := string(rewriteShortID([]byte(in))); got != want {
			t.Errorf("rewriteShortID(%q) = %q, want %q", in, got, want)
		}
	}

	reality := func(name string, shortID any) *nodemodel.Node {
		return node(map[string]any{"type": "vless", "name": name, "server": "192.0.2.40", "port": float64(443), "uuid": "u", "network": "tcp",
			"tls": true, "udp": true, "client-fingerprint": "chrome", "reality-opts": map[string]any{"public-key": "k", "short-id": shortID}})
	}
	for _, key := range []string{"prettyYaml", "pretty-yaml"} {
		out, res := produce(t, "ClashMeta", []*nodemodel.Node{reality("n", float64(1234)), reality("short-id: x", "ab")}, Options{key: true})
		if res.Entries != 2 || !strings.Contains(out, "\n      short-id: \"1234\"\n") || !strings.Contains(out, "\n      short-id: \"ab\"\n") ||
			!strings.Contains(out, "\n    name: \"short-id: \"x\"\"\n") || !strings.HasPrefix(out, "proxies:\n  - ") {
			t.Errorf("%s: pretty form\n%s\nwant both short ids quoted and the name corrupted as upstream corrupts it", key, out)
		}
		if err := yaml.Unmarshal([]byte(out), new(any)); err == nil {
			t.Errorf("%s: the corrupted name still reads as YAML:\n%s", key, out)
		}
	}
	out, _ := produce(t, "ClashMeta", []*nodemodel.Node{reality("n", float64(1234))}, nil)
	if !strings.Contains(out, `"short-id":1234`) {
		t.Errorf("default form: %q; want the numeric short id left a number", out)
	}
}

// TestSingBoxPluginOptsKeyOrder pins the order plugin_opts walks plugin-opts
// in, with the two checked lines of singbox.md ("ss plugins"): the received
// keys ascending, then skip-cert-verify from the ClashMeta pass's T14, then
// the node-level host and path.
func TestSingBoxPluginOptsKeyOrder(t *testing.T) {
	for _, tc := range []struct {
		fields map[string]any
		want   string
	}{
		{
			map[string]any{"plugin-opts": map[string]any{"mode": "websocket", "host": "w.example.com", "path": "/p", "tls": true, "mux": true}},
			"host=w.example.com;mode=websocket;mux=1;path=/p;tls;skip-cert-verify=true",
		},
		{
			map[string]any{"plugin-opts": map[string]any{"mode": "websocket", "tls": true}, "ws-host": "wh.example.com", "ws-path": "/wp"},
			"mode=websocket;tls;skip-cert-verify=true;host=wh.example.com;path=/wp",
		},
	} {
		n := node(with(map[string]any{"type": "ss", "name": "v", "server": "192.0.2.50", "port": float64(443), "cipher": "aes-128-gcm", "password": "p",
			"udp": true, "plugin": "v2ray-plugin", "skip-cert-verify": true}, tc.fields))
		out, _ := produce(t, "sing-box", []*nodemodel.Node{n}, nil)
		var doc struct {
			Outbounds []map[string]any `json:"outbounds"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Outbounds) != 1 {
			t.Fatalf("sing-box output %q: %v", out, err)
		}
		if got := doc.Outbounds[0]["plugin_opts"]; got != tc.want {
			t.Errorf("plugin_opts = %q, want %q", got, tc.want)
		}
	}
}

// TestProducersImportOnlyModelPackages keeps the package importable by the
// conformance runner (no package main, SDK or script engine) and keeps
// encoding/json out of every file that writes output: Go's encoder escapes
// "<", ">", "&", U+2028 and U+2029, which byte-exact goldens forbid.
func TestProducersImportOnlyModelPackages(t *testing.T) {
	allowed := []string{
		"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel",
		"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise",
		"gopkg.in/yaml.v3",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			standard := !strings.Contains(strings.SplitN(path, "/", 2)[0], ".")
			switch {
			case path == "encoding/json":
				t.Errorf("%s imports encoding/json; write output with jsonwrite.go", file)
			case !standard && !slices.Contains(allowed, path):
				t.Errorf("%s imports %s", file, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no source files found")
	}
}

// BenchmarkProduce4096 times each native producer over the perf gate's 4096
// VLESS Reality nodes. S1 plan section 5.2 records it and holds it to the
// regression rule only.
func BenchmarkProduce4096(b *testing.B) {
	raw := perfgen.Nodes(4096)
	for _, target := range harnessTargets {
		b.Run(target.id, func(b *testing.B) {
			p, ok := Lookup(target.platform)
			if !ok {
				b.Fatalf("%s has no native producer", target.platform)
			}
			nodes := make([]*nodemodel.Node, len(raw))
			for i, r := range raw {
				nodes[i] = &nodemodel.Node{}
				if err := json.Unmarshal(r, nodes[i]); err != nil {
					b.Fatal(err)
				}
			}
			var buf bytes.Buffer
			b.ReportAllocs()
			for b.Loop() {
				buf.Reset()
				if _, err := p.Produce(&buf, nodes, target.platform, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
