package normalise

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// conformanceRoot finds the conformance data laid out as the harness root
// (corpus/, goldens/): the directory $LATTICE_SUBSTORE_CONFORMANCE names, the
// copy vendored at the plugin repository root (conformance/, S1 plan section
// 5.1), or a lattice-substore-conformance checkout beside a directory above
// the working directory, as on the Lattice desk. Without any of them the
// calling test skips; CI runs it once the vendored copy lands.
func conformanceRoot(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("LATTICE_SUBSTORE_CONFORMANCE"); d != "" {
		if !isConformanceRoot(d) {
			t.Fatalf("LATTICE_SUBSTORE_CONFORMANCE=%s has no corpus/inputs and goldens/parse", d)
		}
		return d
	}
	if d := filepath.Join("..", "..", "conformance"); isConformanceRoot(d) {
		return d
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for dir := wd; ; {
		if d := filepath.Join(dir, "lattice-substore-conformance"); isConformanceRoot(d) {
			return d
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("conformance data not found; set LATTICE_SUBSTORE_CONFORMANCE to a harness checkout")
	return ""
}

func isConformanceRoot(d string) bool {
	for _, sub := range []string{"corpus/inputs", "goldens/parse"} {
		if st, err := os.Stat(filepath.Join(d, sub)); err != nil || !st.IsDir() {
			return false
		}
	}
	return true
}

// normaliserOnlyCases are the corpus cases whose parse needs nothing but the
// normaliser: Clash documents whose proxies are plain objects of the 29
// accepted types, using none of the Clash object mappings of parser.md
// section 5 (servername, server-cert-fingerprint, fingerprint, dialer-proxy,
// benchmark-url, benchmark-timeout) and only VMess ciphers that its security
// normalisation keeps. The YAML is decoded with gopkg.in/yaml.v3, which is
// close enough to the parser's YAML for these inputs; the parser lane owns
// the exact grammar and the full corpus comparison.
var normaliserOnlyCases = []string{
	"clash-anytls", "clash-direct", "clash-doc-anchors", "clash-doc-crlf-comments",
	"clash-doc-full-config", "clash-doc-groups-only", "clash-doc-include-unsupported-option",
	"clash-easytier", "clash-gost-relay", "clash-h2-connect", "clash-http", "clash-hysteria",
	"clash-hysteria2", "clash-juicity", "clash-masque", "clash-masque-surge", "clash-mieru",
	"clash-naive", "clash-openvpn", "clash-quirk-coercion", "clash-quirk-hop-interval-range",
	"clash-quirk-legacy-ws-keys", "clash-quirk-shadow-tls-opts", "clash-quirk-sni-empty-and-off",
	"clash-quirk-sni-from-host", "clash-quirk-unicode-names", "clash-shadowquic", "clash-snell",
	"clash-socks5", "clash-ss-aead", "clash-ss-obfs", "clash-ss-restls", "clash-ss-shadow-tls",
	"clash-ss-udp-over-tcp-smux", "clash-ss-v2ray-plugin", "clash-ssh", "clash-ssr", "clash-sudoku",
	"clash-tailscale", "clash-trojan-grpc-reality", "clash-trojan-grpc-tls", "clash-trojan-h2-reality",
	"clash-trojan-h2-tls", "clash-trojan-http-reality", "clash-trojan-http-tls",
	"clash-trojan-httpupgrade-reality", "clash-trojan-httpupgrade-tls", "clash-trojan-tcp-reality",
	"clash-trojan-tcp-tls", "clash-trojan-ws-reality", "clash-trojan-ws-tls",
	"clash-trojan-xhttp-reality", "clash-trojan-xhttp-tls", "clash-trusttunnel", "clash-tuic",
	"clash-vless-grpc-none", "clash-vless-h2-none", "clash-vless-http-none",
	"clash-vless-httpupgrade-none", "clash-vless-tcp-none", "clash-vless-ws-none",
	"clash-vless-xhttp-none", "clash-vmess-grpc-none", "clash-vmess-h2-none", "clash-vmess-http-none",
	"clash-vmess-httpupgrade-none", "clash-vmess-tcp-none", "clash-vmess-ws-none",
	"clash-vmess-xhttp-none", "clash-wireguard", "clash-wireguard-amnezia", "clash-zerotier",
}

// TestNormaliseMatchesParseGoldens runs H1, N1 to N35, H3, the bounds, D1 and
// D2 over each case's proxies and compares the result with the parse golden
// under the allowlist entries this lane lands (underscore keys stripped on
// both sides for clash- cases; external and exec-shaped entries dropped from
// the golden).
func TestNormaliseMatchesParseGoldens(t *testing.T) {
	root := conformanceRoot(t)
	for _, id := range normaliserOnlyCases {
		t.Run(id, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, "corpus", "inputs", "clash", id+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Proxies []any `yaml:"proxies"`
			}
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("yaml: %v", err)
			}
			got, failed := normaliseObjects(t, doc.Proxies)
			goldenRaw, err := os.ReadFile(filepath.Join(root, "goldens", "parse", id+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var golden any
			if err := json.Unmarshal(goldenRaw, &golden); err != nil {
				t.Fatal(err)
			}
			if m, ok := golden.(map[string]any); ok && m["error"] == true {
				if !failed {
					t.Fatal("golden is a whole-document failure; the normaliser returned nodes")
				}
				return
			}
			if failed {
				t.Fatal("the normaliser failed the document; the golden has nodes")
			}
			golden = allowlistParse(golden)
			gotValue := allowlistParse(got)
			if !reflect.DeepEqual(gotValue, golden) {
				g, _ := json.MarshalIndent(gotValue, "", " ")
				w, _ := json.MarshalIndent(golden, "", " ")
				t.Errorf("normalised nodes differ from the golden\n got  %s\n want %s", g, w)
			}
		})
	}
}

// normaliseObjects is the object half of the parse pipeline for one
// document: H1, the node stage, then the document rules. It returns the
// nodes as decoded JSON, and whether N33 failed the document.
func normaliseObjects(t *testing.T, objects []any) ([]any, bool) {
	t.Helper()
	var nodes []*nodemodel.Node
	for i, o := range objects {
		fields, ok := jsonValue(o).(map[string]any)
		if !ok {
			t.Fatalf("proxy %d is not an object", i)
		}
		n := &nodemodel.Node{Fields: fields}
		nodemodel.StripFromInput(n)
		drop, _, err := Node(n, false)
		if err != nil {
			return nil, true
		}
		if !drop {
			nodes = append(nodes, n)
		}
	}
	out := []any{}
	for _, n := range Document(nodes) {
		encoded, err := n.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(encoded, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out, false
}

// jsonValue turns a yaml.v3 value into the model's JSON value types.
func jsonValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = jsonValue(e)
		}
		return m
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[fmt.Sprint(k)] = jsonValue(e)
		}
		return m
	case []any:
		l := make([]any, len(x))
		for i, e := range x {
			l[i] = jsonValue(e)
		}
		return l
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	}
	return v
}

// allowlistParse applies the parse-stage normalisations of the three entries
// lane 1 lands in allowlist/divergences.yaml: strip_keys ^_ at any depth
// (underscore, with ca folded into it) and drop_entries type external plus
// nodes carrying an H3 key (external). Applied to both sides, as check.mjs
// applies an entry.
func allowlistParse(v any) any {
	switch x := v.(type) {
	case []any:
		out := []any{}
		for _, e := range x {
			if m, ok := e.(map[string]any); ok && execShapedJSON(m) {
				continue
			}
			out = append(out, allowlistParse(e))
		}
		return out
	case map[string]any:
		m := map[string]any{}
		for k, e := range x {
			if !strings.HasPrefix(k, "_") {
				m[k] = allowlistParse(e)
			}
		}
		return m
	}
	return v
}

func execShapedJSON(m map[string]any) bool {
	return nodemodel.IsExecShaped(&nodemodel.Node{Fields: m})
}

// annotationLocations walks a parse golden and records every underscore key
// with the key of the object that holds it ("" for a node's top level).
func annotationLocations(v any, parent string, into map[string]bool) {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			annotationLocations(e, parent, into)
		}
	case map[string]any:
		for k, e := range x {
			if strings.HasPrefix(k, "_") {
				into[parent+"|"+k] = true
			}
			annotationLocations(e, k, into)
		}
	}
}

// TestAnnotationRegistryIsClosed holds the registry to normaliser.md section
// 6 and holds every underscore key in the parse goldens of cases that are not
// object input to the registry, at its registered location.
func TestAnnotationRegistryIsClosed(t *testing.T) {
	want := map[string]string{
		"_grpc-type": "grpc-opts", "_grpc-authority": "grpc-opts",
		"_kcp-type": "kcp-opts", "_kcp-host": "kcp-opts", "_kcp-path": "kcp-opts",
		"_quic-type": "quic-opts", "_quic-host": "quic-opts", "_quic-path": "quic-opts",
		"_v2ray-http-upgrade-ed": "ws-opts", "_spider-x": "reality-opts",
		"_mode": "", "_extra": "", "_extra_unsupported": "", "_echConfigList": "",
		"_dns": "ech-opts", "_force-query": "ech-opts", "_sockopt": "ech-opts",
		"_vcn": "", "_h2": "", "_pqv": "", "_finalmask": "", "_obfs": "",
		"_qx_obfs_http": "", "_ssr_python_uot": "", "_loon_tls_profile": "",
	}
	if len(Annotations) != len(want) {
		t.Errorf("registry has %d annotations, the specification %d", len(Annotations), len(want))
	}
	for k, loc := range want {
		a, ok := Annotations[k]
		if !ok {
			t.Errorf("registry misses %s", k)
			continue
		}
		if a.Location != loc || a.SetBy == "" {
			t.Errorf("%s registered at %q (set by %q), want %q", k, a.Location, a.SetBy, loc)
		}
		if !IsAnnotation(loc, k) || IsAnnotation(loc+"x", k) {
			t.Errorf("IsAnnotation disagrees with the registry for %s", k)
		}
	}
	for k := range Annotations {
		if _, ok := want[k]; !ok {
			t.Errorf("registry carries %s, which the specification does not list", k)
		}
	}

	root := conformanceRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "goldens", "parse"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	checked := 0
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		// Object input keeps underscore keys in upstream's output; H1 strips
		// them and the underscore allowlist entry covers the difference.
		if strings.HasPrefix(id, "clash-") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, "goldens", "parse", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		found := map[string]bool{}
		annotationLocations(v, "", found)
		for loc := range found {
			parent, key, _ := strings.Cut(loc, "|")
			if !IsAnnotation(parent, key) {
				t.Errorf("%s: %s under %q is not a registered annotation", id, key, parent)
			}
			seen[loc] = true
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no parse goldens checked")
	}
	var locs []string
	for l := range seen {
		locs = append(locs, l)
	}
	sort.Strings(locs)
	t.Logf("%d goldens checked; annotations seen: %s", checked, strings.Join(locs, " "))
}
