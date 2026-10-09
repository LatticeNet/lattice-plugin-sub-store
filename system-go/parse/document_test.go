package parse

import (
	"errors"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// withParsers swaps the parser table for one test.
func withParsers(t *testing.T, table []lineParser) {
	t.Helper()
	saved := parsers
	parsers = table
	t.Cleanup(func() { parsers = saved })
}

// fixedParser accepts lines with the prefix and returns a copy of fields
// with the line's text as the name.
func fixedParser(label, prefix string, fields map[string]any) lineParser {
	return lineParser{
		label: label,
		test:  func(line string, _ *lineState) bool { return strings.HasPrefix(line, prefix) },
		parse: func(line string, _ *lineState) (map[string]any, error) {
			f := map[string]any{"name": line}
			for k, v := range fields {
				f[k] = v
			}
			return f, nil
		},
	}
}

func TestParserSelectionTriesTheLastParserFirst(t *testing.T) {
	ss := map[string]any{"type": "ss", "server": "a.example.com", "port": 1.0, "cipher": "none"}
	trojan := map[string]any{"type": "trojan", "server": "a.example.com", "port": 1.0}
	withParsers(t, []lineParser{
		fixedParser("first", "x", ss),
		fixedParser("second", "", trojan), // accepts every line
	})
	nodes, _, err := Document("y.one\nx.two\nx.three", Options{})
	if err != nil {
		t.Fatal(err)
	}
	// "x two" would be taken by the first row in the ordered search, but the
	// second row produced the previous node and accepts it too.
	for i, n := range nodes {
		if n.Type() != "trojan" {
			t.Errorf("node %d (%s) has type %s, want trojan from the sticky parser", i, n.Name(), n.Type())
		}
	}
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(nodes))
	}
}

func TestParserSelectionFallsBackToTheOrderedSearch(t *testing.T) {
	failing := lineParser{
		label: "failing",
		test:  func(string, *lineState) bool { return true },
		parse: func(string, *lineState) (map[string]any, error) { return nil, errReject },
	}
	withParsers(t, []lineParser{
		failing,
		fixedParser("a", "a", map[string]any{"type": "ss", "server": "h", "port": 1.0}),
		fixedParser("b", "b", map[string]any{"type": "trojan", "server": "h", "port": 1.0}),
	})
	nodes, warnings, err := Document("a.1\nb.2\nc.3\na.4", Options{}) // "." keeps the text from reading as Base64
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, n := range nodes {
		types = append(types, n.Type())
	}
	if got := strings.Join(types, ","); got != "ss,trojan,ss" {
		t.Errorf("types %s, want ss,trojan,ss", got)
	}
	if len(warnings) != 1 || warnings[0].Line != 3 {
		t.Errorf("warnings %+v, want one for line 3", warnings)
	}
}

func TestDocumentBounds(t *testing.T) {
	if _, _, err := Document(strings.Repeat("a", MaxDocumentBytes+1), Options{}); !errors.Is(err, ErrDocumentTooLarge) {
		t.Errorf("a document above the raw bound gave %v", err)
	}
	if out, kind := Preprocess(strings.Repeat("a", MaxDocumentBytes+1)); out != "" || kind != KindPlain {
		t.Errorf("Preprocess above the raw bound gave %s %d bytes", kind, len(out))
	}
	long := "vless://" + uuid4 + "@h.example.com:443?x=" + strings.Repeat("a", nodemodel.MaxLineBytes) + "#n"
	good := "vless://" + uuid4 + "@h.example.com:443#ok"
	nodes, warnings, err := Document(long+"\n"+good, Options{})
	if err != nil || len(nodes) != 1 || nodes[0].Name() != "ok" {
		t.Fatalf("long line next to a good one gave %d nodes, %v", len(nodes), err)
	}
	if len(warnings) != 1 || warnings[0].Line != 1 || !strings.Contains(warnings[0].Message, "exceeds") {
		t.Errorf("warnings %+v, want the line bound on line 1", warnings)
	}
	if _, ok, _ := Line(long, Options{}); ok {
		t.Error("Line accepted a line above the bound")
	}
}

func TestDocumentRepairsInvalidUTF8(t *testing.T) {
	nodes, _, err := Document("vless://"+uuid4+"@h.example.com:443?remarks=a\xffb", Options{})
	if err != nil || len(nodes) != 1 {
		t.Fatalf("got %d nodes, %v", len(nodes), err)
	}
	if got := nodes[0].Name(); got != "a\ufffdb" {
		t.Errorf("name %q, want the WHATWG replacement", got)
	}
}

func TestDocumentNormaliserOutcomes(t *testing.T) {
	withParsers(t, []lineParser{
		fixedParser("exec", "exec", map[string]any{"type": "ss", "server": "h", "port": 1.0, "exec": "/bin/x"}),
		fixedParser("ca", "ca", map[string]any{"type": "ss", "server": "h", "port": 1.0, "ca-str": "no pem here"}),
		fixedParser("vmess", "vm", map[string]any{"type": "vmess", "server": "h", "port": 1.0, "uuid": "not-a-uuid"}),
	})
	nodes, warnings, err := Document("exec.secret-1\nvm.secret-2", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Type() != "vmess" {
		t.Fatalf("got %d nodes, want the vmess node kept", len(nodes))
	}
	if len(warnings) != 2 || warnings[0].Line != 1 || warnings[1].Line != 2 ||
		!strings.Contains(warnings[0].Message, normalise.ReasonExecShaped) || !strings.HasPrefix(warnings[1].Message, "D2") {
		t.Errorf("warnings %+v, want H3 on line 1 and D2 on line 2", warnings)
	}
	for _, w := range warnings {
		if strings.Contains(w.Message, "secret") {
			t.Errorf("warning %q carries the line's text", w.Message)
		}
	}
	nodes, _, err = Document("exec.x", Options{AllowExternal: true})
	if err != nil || len(nodes) != 1 {
		t.Errorf("the external opt-in kept %d nodes, %v", len(nodes), err)
	}
	if _, _, err := Document("vm.a\nca.b", Options{}); !errors.Is(err, normalise.ErrCertificateNotPEM) {
		t.Errorf("an N33 failure gave %v, want the whole document refused", err)
	}
	if _, _, err := Line("ca b", Options{}); !errors.Is(err, normalise.ErrCertificateNotPEM) {
		t.Errorf("Line with an N33 failure gave %v", err)
	}
}

func TestLineParsesOneLine(t *testing.T) {
	n, ok, err := Line("\ufeff vless://"+uuid4+"@[2001:db8::1]:443?security=tls#n ", Options{})
	if err != nil || !ok {
		t.Fatalf("Line: %v %v", ok, err)
	}
	if n.Server() != "2001:db8::1" {
		t.Errorf("server %q, want the brackets removed by N10", n.Server())
	}
	if sni, _ := n.String("sni"); sni != "" {
		t.Errorf("sni %q pinned to an IP literal", sni)
	}
	for _, line := range []string{"", "not a proxy", "vless://broken"} {
		if _, ok, err := Line(line, Options{}); ok || err != nil {
			t.Errorf("Line(%q) = %v, %v", line, ok, err)
		}
	}
}

func TestParserTableFollowsTheSpecificationOrder(t *testing.T) {
	if len(parsers) != 47 {
		t.Fatalf("%d parser rows, want the 47 of parser.md 3.1", len(parsers))
	}
	for _, c := range []struct {
		line string
		row  int // 1-based row of parser.md 3.1 whose test passes first
	}{
		{"socks5+tls://h:1", 1}, {"https://h", 1}, {"socks://x", 2}, {"ss://x", 3}, {"ssr://x", 4},
		{"vmess://x", 5}, {"vless://x", 6}, {"tuic://x", 7}, {"wg://x", 8}, {"hy://x", 9},
		{"hy2://x", 10}, {"trojan://x", 11}, {"anytls://x", 12},
		{`{"type":"ss","name":"a"}`, 13}, {"{name: a, type: ss}", 13}, {"type: direct", 13},
		{"d = direct", 14}, {"d = directx, a", 14}, {"a = anytls, h, 1", 15}, {"a = trust-tunnel, h, 1", 16},
		{"a = masque, h, 1", 17}, {"a = h2-connect, h, 1", 18}, {"a = ssh, h, 1", 19}, {"a = ss, h, 1", 20},
		{"a = vmess, h, 1, username=u", 21}, {"a = trojan, h, 1", 22}, {"a = http, h, 1", 23},
		{"a = snell, h, 1", 24}, {"a = tuic, h, 1", 25}, {"a = wireguard, section-name=x", 26},
		{"a = hysteria2, h, 1", 27}, {"a = socks5, h, 1", 28}, {"a = external, exec=x", 29},
		{"a = Shadowsocks, h, 1", 30}, {"a = ShadowsocksR, h, 1", 31}, {"a = VMess, h, 1", 32},
		{"a = VLESS, h, 1", 33}, {"a = Hysteria2, h, 1", 34}, {"a = Trojan, h, 1", 35},
		{"a = AnyTLS, h, 1", 36}, {"a = http, h, 1, over-tls=true", 37}, {"a = socks5, h, 1, FAST-OPEN = 1", 38},
		{"a = WireGuard, h", 39},
		{"shadowsocks = h:1, tag=a", 40}, {"shadowsocks=h:1, ssr-protocol=x", 41}, {"vmess = h:1", 42},
		{"vless=h:1", 43}, {"anytls=h:1", 44}, {"trojan =h:1", 45}, {"http= h:1", 46}, {"socks5=h:1", 47},
	} {
		st := newLineState()
		row := 0
		for i := range parsers {
			if parsers[i].test(c.line, st) {
				row = i + 1
				break
			}
		}
		if row != c.row {
			t.Errorf("%q: first passing test is row %d, want %d", c.line, row, c.row)
		}
	}
}
