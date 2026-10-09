package producers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
)

// conformanceDir is the vendored copy of the conformance harness at the
// plugin repository root (tools/conformance-sync.sh writes it).
const conformanceDir = "../../conformance"

// harnessTargets are the five native targets by harness id, with the
// platform name the harness's check.mjs calls each producer by
// (oracle/lib/targets.mjs), the golden file extension, and whether the
// checker compares bytes. For URI and V2Ray it does, and the allowlist never
// applies to them, so equal bytes is the checker's whole verdict.
var harnessTargets = []struct {
	id, platform, ext string
	bytes             bool
}{
	{"uri", "URI", "txt", true},
	{"v2ray", "V2Ray", "txt", true},
	{"json", "JSON", "json", false},
	{"singbox", "sing-box", "json", false},
	{"clashmeta", "ClashMeta", "yaml", false},
}

// pendingTargets have no native producer yet. Each is reported as skipped
// with its reason, and a target that gains a producer fails here until it
// leaves this list, so nothing is skipped silently.
var pendingTargets = map[string]string{
	"json":      "the JSON producer lands in lane 4 step B",
	"singbox":   "the sing-box producer lands in lane 4 step B",
	"clashmeta": "the ClashMeta producer lands in lane 4 step B",
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
			_, native := Lookup(target.platform)
			if reason, pending := pendingTargets[target.id]; pending {
				if native {
					t.Fatalf("%s has a native producer: remove it from pendingTargets", target.id)
				}
				t.Skipf("pending: %s", reason)
			}
			if !native {
				t.Fatalf("%s has no native producer and is not pending", target.platform)
			}
			if !target.bytes {
				t.Fatalf("%s: no structural comparison is implemented yet", target.id)
			}
			dir := filepath.Join(conformanceDir, "goldens", "produce", target.id)
			judged, passed := 0, 0
			for _, c := range cases {
				if _, err := os.Stat(filepath.Join(dir, c.id+".canon.json")); err != nil {
					continue
				}
				judged++
				golden := string(readFile(t, filepath.Join(dir, c.id+"."+target.ext)))
				got, _ := produce(t, target.platform, decodeNodes(t, c.nodes), c.options)
				if got != golden {
					t.Errorf("%s: output differs from the golden at byte %d\n golden:    %q\n candidate: %q", c.id, firstDifference(golden, got), clip(golden), clip(got))
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
// drops the node for URI and keeps it for uri.
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
	} {
		n := node(with(socks5("s1"), map[string]any{"supported": map[string]any{tc.key: false}}))
		for _, target := range tc.drops {
			out, res := produce(t, target, []*nodemodel.Node{n}, nil)
			if out != "" || res.Entries != 0 || !slices.Equal(reasons(res.Dropped), []string{"0:" + ReasonSupportMap}) {
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
	for target, id := range map[string]string{"URI": "uri", "uri": "uri", "V2Ray": "v2ray", "v2ray": "v2ray", "v2": "v2ray"} {
		p, ok := Lookup(target)
		if !ok || p.ID() != id {
			t.Errorf("Lookup(%q) = %v, %v; want the %s producer", target, p, ok, id)
		}
	}
	for _, target := range []string{"Uri", "V2RAY", "v2rayN", "", "Stash", "Surge", "SurgeMac", "Loon", "Shadowrocket", "QX", "Egern", "Surfboard", "Clash"} {
		if Native(target) {
			t.Errorf("Native(%q) = true; want the bundle to answer it", target)
		}
	}
}

// TestZeroNodeCountsEntriesNotText pins the zero-node rule: a document is
// empty when the producer wrote no entry, whatever its text. V2Ray gives
// nodes without a URI form an empty line, so two of them encode to "Cg==",
// which is not the empty document and still holds no link.
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

// TestProduceLeavesCallerNodesUntouched pins that the steps and the URI
// preparation work on copies: filling a name, a port and sni, and removing
// fields, never reaches the caller's node.
func TestProduceLeavesCallerNodesUntouched(t *testing.T) {
	fields := map[string]any{"type": "hysteria2", "name": " ", "server": "192.0.2.8", "ports": "20000-20010/30000", "password": "p",
		"disable-sni": true, "tls": true, "udp": true, "id": "x", "resolved": nil}
	n := node(fields)
	before, err := n.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"URI", "V2Ray"} {
		produce(t, target, []*nodemodel.Node{n}, nil)
	}
	after, err := n.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("producing changed the caller's node:\n before %s\n after  %s", before, after)
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
				b.Skipf("pending: %s", pendingTargets[target.id])
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
