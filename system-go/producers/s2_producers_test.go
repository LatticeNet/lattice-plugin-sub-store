package producers

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
)

// The rules of the four S2 producers that the corpus cannot reach or that
// their specifications single out.

func wireGuardNode(name string) *nodemodel.Node {
	return node(map[string]any{"type": "wireguard", "name": name, "server": "192.0.2.40", "port": float64(51820),
		"ip": "10.0.0.2", "private-key": "k", "public-key": "p", "udp": true, "_subName": "fleet"})
}

// TestSurgeModuleHeaderOnlyForUpperCaseTarget pins surge.md's target rule:
// the module header is written only when the target string starts with an
// upper-case Surge, only when every node that reached the producer is a
// mihomo WireGuard node, and it names the first node's source subscription.
// Without include-unsupported-proxy the WireGuard nodes are rejected, so the
// document is the header alone and holds no entry; with it each node is a
// template after the header.
func TestSurgeModuleHeaderOnlyForUpperCaseTarget(t *testing.T) {
	const header = "#!name=fleet\n#!desc=\n#!category=\n"
	wg := []*nodemodel.Node{wireGuardNode("w1"), wireGuardNode("w2")}

	out, res := produce(t, "Surge", wg, nil)
	if out != header || res.Entries != 0 || !slices.Equal(reasons(res.Dropped), []string{"0:" + ReasonUnsupported, "1:" + ReasonUnsupported}) {
		t.Errorf("Surge of two WireGuard nodes: output %q, entries %d, dropped %v; want the header alone and both rejected", out, res.Entries, res.Dropped)
	}
	out, res = produce(t, "surge", wg, nil)
	if out != "" || res.Entries != 0 {
		t.Errorf("surge of two WireGuard nodes: output %q, entries %d; want the empty document and no header", out, res.Entries)
	}

	include := Options{"include-unsupported-proxy": true}
	out, res = produce(t, "Surge", wg, include)
	if !strings.HasPrefix(out, header+"# > WireGuard Proxy w1\n") || res.Entries != 2 || strings.Count(out, "[WireGuard ") != 2 {
		t.Errorf("Surge of two WireGuard nodes with the option: entries %d, output %q; want the header and two templates", res.Entries, out)
	}
	out, _ = produce(t, "surge", wg, include)
	if strings.Contains(out, "#!name=") || !strings.HasPrefix(out, "# > WireGuard Proxy w1\n") {
		t.Errorf("surge with the option: output %q; want the templates without a header", out)
	}

	// One node of another type, or a wireguard-surge line, and no header.
	mixed := append(slices.Clone(wg), node(socks5("s1")))
	if out, _ := produce(t, "Surge", mixed, include); strings.Contains(out, "#!name=") {
		t.Errorf("Surge of WireGuard and SOCKS5: output %q; want no header", out)
	}
	surgeLine := node(map[string]any{"type": "wireguard-surge", "name": "ws", "section-name": "s", "udp": true, "_subName": "fleet"})
	if out, _ := produce(t, "Surge", []*nodemodel.Node{surgeLine}, nil); out != "ws=wireguard,section-name=s" {
		t.Errorf("Surge of a wireguard-surge line: output %q; want the line without a header", out)
	}
	// A node the support map drops never reached the producer, so it does
	// not stop the header.
	dropped := node(with(socks5("s2"), map[string]any{"supported": map[string]any{"Surge": false}}))
	if out, _ := produce(t, "Surge", append([]*nodemodel.Node{dropped}, wg...), nil); out != header {
		t.Errorf("Surge with a support-map drop first: output %q; want %q", out, header)
	}
	// No source subscription name is the text undefined, as the oracle
	// writes it.
	bare := wireGuardNode("w3")
	delete(bare.Fields, "_subName")
	if out, _ := produce(t, "Surge", []*nodemodel.Node{bare}, nil); out != "#!name=undefined\n#!desc=\n#!category=\n" {
		t.Errorf("Surge of a WireGuard node without _subName: output %q", out)
	}
}

// TestQXRejectsOverlongALPN pins the tls-alpn rule the normaliser's ALPN
// bound keeps out of the corpus (quantumultx.md, "TLS group"): a protocol of
// 255 bytes is written with the length ff, one of 256 bytes rejects the
// node, and the raw tls-alpn the Quantumult X parser keeps wins over alpn.
func TestQXRejectsOverlongALPN(t *testing.T) {
	trojan := func(name string, extra map[string]any) *nodemodel.Node {
		return node(with(map[string]any{"type": "trojan", "name": name, "server": "192.0.2.50", "port": float64(443),
			"password": "p", "network": "tcp", "tls": true, "udp": true}, extra))
	}
	long := strings.Repeat("a", 255)
	out, res := produce(t, "QX", []*nodemodel.Node{
		trojan("ok", map[string]any{"alpn": []any{"h2", long}}),
		trojan("over", map[string]any{"alpn": []any{"h2", long + "a"}}),
		trojan("raw", map[string]any{"alpn": []any{"h2", long + "a"}, "tls-alpn": "0268320a"}),
		trojan("empty", map[string]any{"alpn": " , "}),
	}, nil)
	lines := strings.Split(out, "\n")
	if res.Entries != 3 || !slices.Equal(reasons(res.Dropped), []string{"1:" + ReasonUnsupported}) || len(lines) != 3 {
		t.Fatalf("output %q, entries %d, dropped %v; want three lines and the overlong node rejected", out, res.Entries, res.Dropped)
	}
	if want := ",tls-alpn=026832ff" + strings.Repeat("61", 255) + ","; !strings.Contains(lines[0], want) {
		t.Errorf("255-byte protocol: line %q; want %q", lines[0], want)
	}
	if !strings.Contains(lines[1], ",tls-alpn=0268320a,") {
		t.Errorf("raw tls-alpn: line %q; want it written verbatim", lines[1])
	}
	if strings.Contains(lines[2], "tls-alpn") {
		t.Errorf("alpn without a protocol: line %q; want no tls-alpn", lines[2])
	}
}

// TestS2TargetsStayOnTheBundle pins that registering the four S2 producers
// does not move their targets: Lookup finds each, so the conformance runner
// and these tests judge them, and Native stays false until routing adds the
// harness id (producer.go, routed).
func TestS2TargetsStayOnTheBundle(t *testing.T) {
	for target, id := range map[string]string{
		"Stash": "stash", "stash": "stash", "Shadowrocket": "shadowrocket", "ShadowRocket": "shadowrocket",
		"shadowrocket": "shadowrocket", "Surge": "surge", "surge": "surge", "QX": "quantumultx", "qx": "quantumultx",
		"QuantumultX": "quantumultx",
	} {
		p, ok := Lookup(target)
		if !ok || p.ID() != id {
			t.Errorf("Lookup(%q) = %v, %v; want the %s producer", target, p, ok, id)
		}
		if Native(target) {
			t.Errorf("Native(%q) = true; the S2 producers stay on the bundle until routing switches them on", target)
		}
	}
}

// fleetMix decodes perfgen.FleetMix's n nodes.
func fleetMix(tb testing.TB, n int) []*nodemodel.Node {
	tb.Helper()
	raw, err := perfgen.FleetMix(conformanceDir, n)
	if err != nil {
		tb.Fatal(err)
	}
	nodes := make([]*nodemodel.Node, len(raw))
	for i, r := range raw {
		nodes[i] = &nodemodel.Node{}
		if err := json.Unmarshal(r, nodes[i]); err != nil {
			tb.Fatal(err)
		}
	}
	return nodes
}

// BenchmarkProduce4096FleetMix times each native producer over 4096 nodes of
// the fleet's protocol mix (perfgen.FleetMix), the input every target has
// something to write from; BenchmarkProduce4096's VLESS Reality nodes leave
// Surge nothing. New since the S1 baseline, it is recorded and held to no
// bound until a baseline names it.
func BenchmarkProduce4096FleetMix(b *testing.B) {
	nodes := fleetMix(b, 4096)
	for _, target := range harnessTargets {
		b.Run(target.id, func(b *testing.B) {
			p, ok := Lookup(target.platform)
			if !ok {
				b.Fatalf("%s has no native producer", target.platform)
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

// lineBreaks are the characters a field must never carry into a Surge or
// Quantumult X line (surge.md and quantumultx.md, "Line safety"): LF opens a
// section of its own, and CR, NEL, U+2028, U+2029, VT, FF and tab are
// control or separator characters an Apple client's text APIs may also read
// as line ends.
var lineBreaks = map[string]string{
	"LF": "\n[Script]\nx=y", "CR": "\r[MITM]", "CRLF": "\r\n[Rule]", "NEL": "\u0085[Rule]",
	"LS": "\u2028[Rule]", "PS": "\u2029[Rule]", "VT": "\v[Rule]", "FF": "\f[Rule]", "tab": "\t[Rule]",
	"NUL": "\x00[Rule]",
}

// setPath sets one field of fields, a dotted path walking nested maps.
func setPath(fields map[string]any, path string, v any) {
	keys := strings.Split(path, ".")
	m := fields
	for _, k := range keys[:len(keys)-1] {
		m = m[k].(map[string]any)
	}
	m[keys[len(keys)-1]] = v
}

// deepCopy copies a node's fields, nested maps and lists included.
func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = deepCopy(e)
		}
		return out
	}
	return v
}

// TestLineBreakingFieldRejectsTheNode pins surge.md's and quantumultx.md's
// line-safety rule: a node whose entry would carry a line-breaking character
// from any field it writes is rejected as unsupported. Placed between two
// nodes that are written, it leaves the document exactly as those two alone
// produce it, so no field opens a line of its own. The WireGuard template is
// checked line by line, because its own line breaks are the producer's.
func TestLineBreakingFieldRejectsTheNode(t *testing.T) {
	trojan := map[string]any{"type": "trojan", "name": "t", "server": "h.example.com", "port": float64(443),
		"password": "p", "tls": true, "sni": "s.example.com", "udp": true, "network": "ws",
		"ws-opts": map[string]any{"path": "/ws", "headers": map[string]any{"Host": "w.example.com"}}}
	http := map[string]any{"type": "http", "name": "h", "server": "192.0.2.60", "port": float64(8080),
		"username": "u", "password": "p", "udp": false}
	httpHeaders := with(http, map[string]any{"headers": map[string]any{"X-A": "1"}})
	ss := map[string]any{"type": "ss", "name": "s", "server": "192.0.2.61", "port": float64(8388),
		"cipher": "aes-128-gcm", "password": "p", "udp": true, "plugin": "obfs",
		"plugin-opts": map[string]any{"mode": "http", "host": "o.example.com"}}
	wg := wireGuardNode("w").Fields
	wg["dns"] = []any{"192.0.2.53"}
	wg["section-name"] = "sec"
	include := Options{"include-unsupported-proxy": true}

	for _, tc := range []struct {
		target string
		base   map[string]any
		field  string
		opts   Options
	}{
		{"Surge", trojan, "name", nil},
		{"Surge", trojan, "server", nil},
		{"Surge", trojan, "password", nil},
		{"Surge", trojan, "sni", nil},
		{"Surge", trojan, "ws-opts.path", nil},
		{"Surge", trojan, "ws-opts.headers.Host", nil},
		{"Surge", http, "username", nil},
		{"Surge", httpHeaders, "headers.X-A", nil},
		{"Surge", ss, "plugin-opts.host", nil},
		{"Surge", wg, "name", include},
		{"Surge", wg, "section-name", include},
		{"Surge", wg, "private-key", include},
		{"Surge", wg, "dns", include},
		{"QX", trojan, "name", nil},
		{"QX", trojan, "server", nil},
		{"QX", trojan, "password", nil},
		{"QX", trojan, "sni", nil},
		{"QX", trojan, "ws-opts.path", nil},
		{"QX", trojan, "ws-opts.headers.Host", nil},
		{"QX", ss, "plugin-opts.host", nil},
		{"QX", http, "username", nil},
	} {
		for label, brk := range lineBreaks {
			t.Run(tc.target+"/"+tc.base["type"].(string)+"."+tc.field+"/"+label, func(t *testing.T) {
				good := func() *nodemodel.Node { return node(deepCopy(tc.base).(map[string]any)) }
				bad := deepCopy(tc.base).(map[string]any)
				value := "v" + brk
				if tc.field == "dns" {
					setPath(bad, tc.field, []any{value})
				} else {
					setPath(bad, tc.field, value)
				}
				want, _ := produce(t, tc.target, []*nodemodel.Node{good(), good()}, tc.opts)
				out, res := produce(t, tc.target, []*nodemodel.Node{good(), node(bad), good()}, tc.opts)
				if res.Entries != 2 || !slices.Equal(reasons(res.Dropped), []string{"1:" + ReasonUnsupported}) {
					t.Fatalf("entries %d, dropped %v, output %q; want the node with %s in %s rejected", res.Entries, res.Dropped, out, label, tc.field)
				}
				if out != want {
					t.Errorf("output %q, want the two written nodes alone, %q", out, want)
				}
			})
		}
	}
}

// TestSurgeModuleHeaderDropsLineBreaks pins that the module header, which
// belongs to no node, writes the subscription name with its line-breaking
// characters removed instead of rejecting anything (surge.md, "Line
// safety").
func TestSurgeModuleHeaderDropsLineBreaks(t *testing.T) {
	for label, brk := range lineBreaks {
		w := wireGuardNode("w1")
		w.Fields["_subName"] = "fleet" + brk
		out, _ := produce(t, "Surge", []*nodemodel.Node{w}, nil)
		want := "#!name=fleet" + stripLineBreaking(brk) + "\n#!desc=\n#!category=\n"
		if out != want {
			t.Errorf("%s: header %q, want %q", label, out, want)
		}
	}
}

// TestStructuredS2TargetsEscapeLineBreaks pins the other half of the
// line-injection check: Stash and Shadowrocket write every value through the
// JSON writer, which escapes every C0 control character, so the node is kept
// and no field opens a line of the YAML document. NEL, U+2028 and U+2029 stay
// raw, as JSON.stringify leaves them, inside a double-quoted scalar that only
// an unescaped quote can end.
func TestStructuredS2TargetsEscapeLineBreaks(t *testing.T) {
	for label, brk := range lineBreaks {
		n := node(map[string]any{"type": "trojan", "name": "a" + brk, "server": "h.example.com", "port": float64(443),
			"password": "p\"" + brk, "tls": true, "sni": "s" + brk, "udp": true})
		for _, target := range []string{"Stash", "Shadowrocket"} {
			out, res := produce(t, target, []*nodemodel.Node{n}, nil)
			lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			if res.Entries != 1 || len(lines) != 2 || lines[0] != "proxies:" || !strings.HasPrefix(lines[1], "  - {") {
				t.Errorf("%s/%s: entries %d, output %q; want one flow entry", target, label, res.Entries, out)
			}
			for _, r := range out {
				if r != '\n' && r < 0x20 {
					t.Errorf("%s/%s: output carries %U unescaped: %q", target, label, r, out)
				}
			}
		}
	}
}

// TestShadowrocketV2rayPluginModes pins the whole v2ray-plugin mode list of
// shadowrocket.md's admission filter, which the corpus reaches only for
// websocket and quic: the five modes are kept, trimmed and in any letter
// case, and any other mode drops the node.
func TestShadowrocketV2rayPluginModes(t *testing.T) {
	ss := func(mode string) *nodemodel.Node {
		return node(map[string]any{"type": "ss", "name": "m", "server": "192.0.2.70", "port": float64(8388),
			"cipher": "aes-128-gcm", "password": "p", "udp": true, "plugin": "v2ray-plugin",
			"plugin-opts": map[string]any{"mode": mode, "host": "v.example.com"}})
	}
	for _, mode := range []string{"websocket", "quic", "http2", "mkcp", "grpc", " GRPC "} {
		if _, res := produce(t, "Shadowrocket", []*nodemodel.Node{ss(mode)}, nil); res.Entries != 1 {
			t.Errorf("mode %q: entries %d, dropped %v; want the node kept", mode, res.Entries, res.Dropped)
		}
	}
	for _, mode := range []string{"tls", "h2", ""} {
		if _, res := produce(t, "Shadowrocket", []*nodemodel.Node{ss(mode)}, nil); res.Entries != 0 {
			t.Errorf("mode %q: entries %d; want the node dropped", mode, res.Entries)
		}
	}
}
