package producers

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// linkBreaks are the characters no field may carry into a URI or V2Ray link
// (uri.md and v2ray.md, "Line safety"): LF and CRLF end the link, and CR,
// NEL, U+2028, U+2029, VT, FF, tab, NUL and DEL are control or separator
// characters a client's line reader may also split on. Each value leads into
// the start of a share link, which is what a feed would append.
var linkBreaks = map[string]string{
	"LF": "\nss://YWVzLTEyOC1nY206cA@192.0.2.9:1#x", "CR": "\rx", "CRLF": "\r\nx", "NEL": "\u0085x",
	"LS": " x", "PS": " x", "VT": "\vx", "FF": "\fx", "tab": "\tx", "NUL": "\x00x", "DEL": "\x7fx",
}

// URI-form nodes of every scheme, each written in full by the URI producer.
func uriLineSafetyBases() map[string]map[string]any {
	return map[string]map[string]any{
		"socks5": {"type": "socks5", "name": "s", "server": "192.0.2.1", "port": float64(1080), "username": "u", "password": "p", "udp": true},
		"trojan": {"type": "trojan", "name": "t", "server": "h.example.com", "port": float64(443), "password": "p", "sni": "s.example.com", "udp": true,
			"network": "ws", "ws-opts": map[string]any{"path": "/ws", "headers": map[string]any{"Host": "w.example.com"}}},
		"vless": {"type": "vless", "name": "v", "server": "h.example.com", "port": float64(443), "uuid": "11111111-1111-4111-8111-111111111111",
			"tls": true, "servername": "s.example.com", "network": "tcp", "udp": true},
		"vmess-query": {"type": "vmess", "name": "q", "server": "h.example.com", "port": float64(443), "uuid": "22222222-2222-4222-8222-222222222222",
			"alterId": float64(0), "cipher": "auto", "tls": true, "servername": "s.example.com", "network": "tcp", "udp": true,
			"reality-opts": map[string]any{"public-key": "AAAA", "short-id": "ab"}},
		"vmess-json": {"type": "vmess", "name": "j", "server": "h.example.com", "port": float64(443), "uuid": "33333333-3333-4333-8333-333333333333",
			"alterId": float64(0), "cipher": "auto", "tls": true, "servername": "s.example.com", "network": "tcp", "udp": true},
		"ss": {"type": "ss", "name": "s", "server": "192.0.2.2", "port": float64(8388), "cipher": "aes-128-gcm", "password": "p", "udp": true},
		"hysteria": {"type": "hysteria", "name": "h", "server": "h.example.com", "port": float64(443), "auth-str": "a", "obfs": "o",
			"ports": "443,8443", "sni": "s.example.com", "up": "10", "down": "20", "protocol": "udp", "udp": true},
		"hysteria2": {"type": "hysteria2", "name": "h2", "server": "h.example.com", "port": float64(443), "password": "p", "ports": "443,8443", "udp": true},
		"anytls":    {"type": "anytls", "name": "a", "server": "h.example.com", "port": float64(443), "password": "p", "sni": "s.example.com", "udp": true},
		"tuic": {"type": "tuic", "name": "tu", "server": "h.example.com", "port": float64(443), "uuid": "44444444-4444-4444-8444-444444444444",
			"password": "p", "alpn": []any{"h3"}, "congestion-controller": "bbr", "udp-relay-mode": "native", "udp": true},
		"wireguard": {"type": "wireguard", "name": "w", "server": "192.0.2.3", "port": float64(51820), "private-key": "k", "public-key": "pk",
			"ip": "10.0.0.2", "udp": true},
	}
}

// walkedKey is the field name a row adds when it names no existing field:
// the generic walks of hysteria, tuic, anytls and wireguard write every
// unknown field as a parameter, its name raw.
const walkedKey = "+walked-key"

// uriLineSafetyRows are the fields the URI link writes raw (uri.md: the
// socks5 name, authorities, the trojan password, the vless and vmess
// query-form uuid, hysteria's auth, obfs, ports, peer and rates, the
// hysteria2 mport, tuic's congestion control, and the field names of the
// generic walks), which a line break must reject the node through.
var uriLineSafetyRows = []struct{ base, field string }{
	{"socks5", "name"}, {"socks5", "server"}, {"socks5", "port"},
	{"trojan", "password"}, {"trojan", "server"}, {"trojan", "port"},
	{"vless", "uuid"}, {"vless", "server"},
	{"vmess-query", "uuid"}, {"vmess-query", "server"},
	{"ss", "server"}, {"ss", "port"},
	{"hysteria", "auth-str"}, {"hysteria", "obfs"}, {"hysteria", "ports"}, {"hysteria", "sni"}, {"hysteria", "up"}, {"hysteria", "down"},
	{"hysteria", "server"}, {"hysteria", walkedKey},
	{"hysteria2", "ports"}, {"hysteria2", "server"},
	{"anytls", "password"}, {"anytls", "server"}, {"anytls", walkedKey},
	{"tuic", "congestion-controller"}, {"tuic", "server"}, {"tuic", walkedKey},
	{"wireguard", "server"}, {"wireguard", walkedKey},
}

// breakField returns a copy of base with brk in field, or, for walkedKey, a
// new field whose name holds it.
func breakField(base map[string]any, field, brk string) map[string]any {
	bad := deepCopy(base).(map[string]any)
	if field == walkedKey {
		bad["x"+brk] = "1"
		return bad
	}
	setPath(bad, field, "v"+brk)
	return bad
}

// decodeV2Ray returns the text a V2Ray document encodes.
func decodeV2Ray(t *testing.T, doc string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(doc)
	if err != nil {
		t.Fatalf("V2Ray document %q is not Base64: %v", doc, err)
	}
	return string(b)
}

// TestURILineBreakingFieldRejectsTheNode pins uri.md's and v2ray.md's line
// safety rule: a node whose link would carry a line-breaking character from
// any field it writes raw is rejected as unsupported. Placed between two
// nodes that are written, it leaves each document exactly as those two alone
// produce it, so no field adds a link of its own, and the V2Ray text keeps no
// empty line in its place.
func TestURILineBreakingFieldRejectsTheNode(t *testing.T) {
	bases := uriLineSafetyBases()
	for _, row := range uriLineSafetyRows {
		for label, brk := range linkBreaks {
			for _, target := range []string{"URI", "V2Ray"} {
				t.Run(target+"/"+row.base+"."+row.field+"/"+label, func(t *testing.T) {
					good := func() *nodemodel.Node { return node(deepCopy(bases[row.base]).(map[string]any)) }
					bad := breakField(bases[row.base], row.field, brk)
					want, wantRes := produce(t, target, []*nodemodel.Node{good(), good()}, nil)
					out, res := produce(t, target, []*nodemodel.Node{good(), node(bad), good()}, nil)
					if wantRes.Entries != 2 {
						t.Fatalf("the clean node yields %d entries, want 2: %q", wantRes.Entries, want)
					}
					if res.Entries != 2 || !slices.Equal(reasons(res.Dropped), []string{"1:" + ReasonUnsupported}) {
						t.Fatalf("entries %d, dropped %v, output %q; want the node with %s in %s rejected", res.Entries, res.Dropped, out, label, row.field)
					}
					if out != want {
						t.Errorf("output %q, want the two written nodes alone, %q", out, want)
					}
				})
			}
		}
	}
}

// TestURIKeepsLineBreakTheLinkEncodes pins that the check runs on the
// finished link, not on the node's text: a line break in a field the link
// component-encodes, Base64-encodes or escapes inside the vmess JSON body
// never reaches the link, so the node is written, as upstream writes it.
func TestURIKeepsLineBreakTheLinkEncodes(t *testing.T) {
	bases := uriLineSafetyBases()
	for _, row := range []struct{ base, field string }{
		{"socks5", "username"}, {"socks5", "password"},
		{"trojan", "name"}, {"trojan", "sni"}, {"trojan", "ws-opts.path"}, {"trojan", "ws-opts.headers.Host"},
		{"vless", "name"}, {"vless", "servername"},
		{"vmess-json", "uuid"}, {"vmess-json", "name"}, {"vmess-json", "server"},
		{"ss", "password"}, {"ss", "name"},
		{"hysteria2", "password"}, {"tuic", "uuid"}, {"tuic", "password"}, {"wireguard", "private-key"},
	} {
		for label, brk := range linkBreaks {
			for _, target := range []string{"URI", "V2Ray"} {
				bad := breakField(bases[row.base], row.field, brk)
				out, res := produce(t, target, []*nodemodel.Node{node(bad)}, nil)
				text := out
				if target == "V2Ray" {
					text = decodeV2Ray(t, out)
				}
				if res.Entries != 1 || len(res.Dropped) != 0 || !lineSafe(text) {
					t.Errorf("%s/%s.%s/%s: entries %d, dropped %v, text %q; want the node written with the line break encoded", target, row.base, row.field, label, res.Entries, res.Dropped, text)
				}
			}
		}
	}
}

// TestV2RayRejectedNodeLeavesNoLine pins the V2Ray half (v2ray.md, "Output
// shape"): nodes without a URI form keep their empty lines, and a node
// rejected for line safety contributes nothing, so the document equals the
// one produced without it.
func TestV2RayRejectedNodeLeavesNoLine(t *testing.T) {
	httpNode := func() *nodemodel.Node {
		return node(map[string]any{"type": "http", "name": "h", "server": "192.0.2.4", "port": float64(8080)})
	}
	tuicToken := func() *nodemodel.Node {
		return node(map[string]any{"type": "tuic", "name": "t4", "server": "192.0.2.5", "port": float64(443), "token": "tk",
			"alpn": []any{"h3"}, "congestion-controller": "cubic", "udp-relay-mode": "native"})
	}
	for label, brk := range linkBreaks {
		withBad := []*nodemodel.Node{node(socks5("a")), httpNode(), node(socks5("b" + brk)), tuicToken(), node(socks5("c"))}
		without := []*nodemodel.Node{node(socks5("a")), httpNode(), tuicToken(), node(socks5("c"))}
		out, res := produce(t, "V2Ray", withBad, nil)
		want, _ := produce(t, "V2Ray", without, nil)
		if out != want {
			t.Errorf("%s: V2Ray text %q, want %q, the document without the rejected node", label, decodeV2Ray(t, out), decodeV2Ray(t, want))
		}
		if lines := strings.Split(decodeV2Ray(t, out), "\n"); len(lines) != 4 || lines[1] != "" || lines[2] != "" {
			t.Errorf("%s: V2Ray lines %q, want a link, two empty lines and a link", label, lines)
		}
		if res.Entries != 2 || !slices.Equal(reasons(res.Dropped), []string{"1:" + ReasonUnsupported, "2:" + ReasonUnsupported, "3:" + ReasonUnsupported}) {
			t.Errorf("%s: entries %d, dropped %v; want two links and three unsupported nodes", label, res.Entries, res.Dropped)
		}
	}
}

// stringLeaves lists the dotted paths of every text value in a node, nested
// mappings included (lists are left alone, which setPath cannot address).
func stringLeaves(m map[string]any, prefix string, out *[]string) {
	for k, v := range m {
		if strings.Contains(k, ".") {
			continue
		}
		switch x := v.(type) {
		case string:
			*out = append(*out, prefix+k)
		case map[string]any:
			stringLeaves(x, prefix+k+".", out)
		}
	}
}

// TestURINeverWritesALineBreakFromAField is the review probe in small: every
// text field of the first three nodes of every corpus case, and one added
// field name, is given each line break in turn, and the node, produced
// alone, must leave a URI document and a V2Ray text without a line-breaking
// character: one link, or nothing.
func TestURINeverWritesALineBreakFromAField(t *testing.T) {
	breaks := []string{"\n", "\r", " ", "\u0085"}
	mutated := 0
	for _, c := range produceCases(t) {
		nodes := decodeNodes(t, c.nodes)
		for i := 0; i < len(nodes) && i < 3; i++ {
			var leaves []string
			stringLeaves(nodes[i].Fields, "", &leaves)
			sort.Strings(leaves)
			leaves = append(leaves, walkedKey)
			for _, leaf := range leaves {
				for _, brk := range breaks {
					bad := breakField(nodes[i].Fields, leaf, brk)
					in := []*nodemodel.Node{node(bad)}
					out, res := produce(t, "URI", in, c.options)
					if !lineSafe(out) || (out == "") != (res.Entries == 0) {
						t.Errorf("%s node %d, %q in %s: URI document %q, %d entries; want one link without a line break, or nothing", c.id, i, brk, leaf, out, res.Entries)
					}
					v2, _ := produce(t, "V2Ray", in, c.options)
					if text := decodeV2Ray(t, v2); !lineSafe(text) {
						t.Errorf("%s node %d, %q in %s: V2Ray text %q holds a line break from a field", c.id, i, brk, leaf, text)
					}
					mutated++
				}
			}
		}
	}
	t.Logf("%d mutated nodes produced for URI and V2Ray", mutated)
}

// TestURILineSafeCasesMatchTheLineSafeGolden holds the corpus to the rule
// the checker only permits: for every case with a line-safe bytes golden on
// URI or V2Ray, the native output must equal those bytes, never the strict
// golden that carries upstream's split link. Without it a producer that
// writes the injection would still pass the corpus, because the strict
// golden is upstream's own output.
func TestURILineSafeCasesMatchTheLineSafeGolden(t *testing.T) {
	judged := 0
	for _, target := range harnessTargets {
		if !target.bytes {
			continue
		}
		dir := filepath.Join(conformanceDir, "goldens", "produce", target.id)
		for _, c := range produceCases(t) {
			want, err := os.ReadFile(filepath.Join(dir, c.id+lineSafeBytesInfix+target.ext))
			if err != nil {
				continue
			}
			if !comparesWithoutLineBreaks(t, target.id, c.id) {
				t.Errorf("%s %s: a line-safe golden exists for a case line-safety does not name", target.id, c.id)
			}
			judged++
			got, _ := produce(t, target.platform, c.produceInput(decodeNodes(t, c.nodes)), c.options)
			if got != string(want) {
				t.Errorf("%s %s: output %q, want the line-safe golden %q", target.id, c.id, clip(got), clip(string(want)))
			}
		}
	}
	if judged != 20 {
		t.Errorf("%d line-safe bytes goldens judged, want 20 (ten cases on URI and on V2Ray)", judged)
	}
}
