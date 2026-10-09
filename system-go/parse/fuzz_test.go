package parse

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// fuzzSeeds adds a few corpus inputs of each family to a fuzzer, so the
// mutations start from real shapes.
func fuzzSeeds(f *testing.F, families ...string) {
	root := filepath.Join("..", "..", "conformance", "corpus", "inputs")
	for _, fam := range families {
		matches, _ := filepath.Glob(filepath.Join(root, fam, "*.txt"))
		for i, m := range matches {
			if i >= 12 {
				break
			}
			if raw, err := os.ReadFile(m); err == nil {
				f.Add(string(raw))
			}
		}
	}
}

// checkNodes holds every node a parse returns to the model's invariants: it
// writes as JSON and it is inside the bounds the normaliser enforces.
func checkNodes(t *testing.T, nodes []*nodemodel.Node) {
	t.Helper()
	for i, n := range nodes {
		if _, err := n.MarshalJSON(); err != nil {
			t.Fatalf("node %d does not write: %v", i, err)
		}
		if err := nodemodel.Bounds(n); err != nil {
			t.Fatalf("node %d escaped the bounds: %v", i, err)
		}
	}
}

func FuzzParseDocument(f *testing.F) {
	fuzzSeeds(f, "vless", "vmess", "base64", "clash", "ssd", "surge", "mixed", "html")
	f.Add("proxies:\n  - &a {name: a, type: vless, short-id: 0088}\n  - *a\n")
	f.Add("[Proxy]\na = ss, h, 1\n[Rule]\n")
	f.Fuzz(func(t *testing.T, text string) {
		nodes, warnings, err := Document(text, Options{})
		if err != nil {
			if !errors.Is(err, normalise.ErrCertificateNotPEM) && !errors.Is(err, ErrExpansionTooLarge) && !errors.Is(err, ErrDocumentTooLarge) {
				t.Fatalf("unexpected error %v", err)
			}
			return
		}
		checkNodes(t, nodes)
		pre, _ := Preprocess(text)
		lines := strings.Count(pre, "\n") + 1
		for _, w := range warnings {
			if w.Line < 0 || w.Line > lines {
				t.Fatalf("warning on line %d of %d", w.Line, lines)
			}
		}
	})
}

var fuzzSchemes = []string{
	"vless://", "vmess://", "anytls://", "ss://", "ssr://", "trojan://", "socks://", "socks5://",
	"https://", "tuic://", "wg://", "hysteria://", "hy2://", "",
}

func FuzzParseURI(f *testing.F) {
	fuzzSeeds(f, "vless", "vmess", "ss", "ssr", "trojan", "tuic", "wireguard", "hysteria", "hysteria2", "anytls", "socks-http")
	f.Add(uuid4 + "@h:443?type=xhttp&extra=%7B%22downloadSettings%22%3A%7B%22security%22%3A%22reality%22%7D%7D#n")
	f.Add(base64.StdEncoding.EncodeToString([]byte(`{"add":"h","port":"1","id":"u","net":"httpupgrade","path":"/p?ed=1"}`)))
	f.Fuzz(func(t *testing.T, rest string) {
		for _, scheme := range fuzzSchemes {
			line := strings.TrimPrefix(rest, scheme)
			line = scheme + line
			n, ok, err := Line(line, Options{})
			if err != nil && !errors.Is(err, normalise.ErrCertificateNotPEM) {
				t.Fatalf("Line(%q): %v", line, err)
			}
			if ok {
				checkNodes(t, []*nodemodel.Node{n})
			}
		}
		// The parsers themselves, before parser selection and the
		// normaliser, never panic on any text after the scheme.
		for _, p := range []func(string, *lineState) (map[string]any, error){
			parseVLESS, parseVMess, parseAnyTLS, parseSS, parseSSR, parseTrojan, parseSocks,
			parseTUIC, parseWireGuard, parseHysteria, parseHysteria2,
		} {
			_, _ = p(rest, nil)
		}
		_, _ = parseProxyURI(rest, "http", true)
	})
}

func FuzzPreprocessClashYAML(f *testing.F) {
	fuzzSeeds(f, "clash")
	f.Add("proxies:\n  - {name: a, type: ss, short-id: 0088 # c\n}\n")
	f.Add("a: &a [1]\nproxies: [*a, *a, {<<: {x: 1}}]\n")
	f.Fuzz(func(t *testing.T, text string) {
		out, ok, _, err := preprocessClash(text)
		if err != nil {
			if !errors.Is(err, ErrExpansionTooLarge) {
				t.Fatalf("unexpected error %v", err)
			}
			return
		}
		if !ok {
			return
		}
		if len(out) > MaxExpandedBytes {
			t.Fatalf("output of %d bytes passed the bound", len(out))
		}
		if out == "" {
			return
		}
		for _, line := range strings.Split(out, "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatalf("line %q is not JSON", line)
			}
			if _, err := decodeJSON5(line); err != nil {
				t.Fatalf("line %q does not read back as JSON5: %v", line, err)
			}
		}
	})
}

func FuzzPreprocessBase64(f *testing.F) {
	fuzzSeeds(f, "base64")
	f.Add("dm1lc3M6Ly8=")
	f.Add("YW55dGxzOi8vcEBoOjE")
	// Encoded, these hold the sixth-bit values 62 and 63, so the round trip
	// below meets "-" and "_" as well as "+" and "/".
	f.Add("~~>~~?\xfb\xff\xfe")
	f.Fuzz(func(t *testing.T, text string) {
		for _, p := range []func(string) (string, bool, string, error){preprocessBase64Known, preprocessBase64Fallback} {
			out, ok, warning, err := p(text)
			if err != nil {
				t.Fatalf("Base64 preprocessor failed: %v", err)
			}
			if !ok {
				continue
			}
			if !Base64Valid(text) {
				t.Fatalf("matched %q, which fails the validity test", text)
			}
			switch {
			case warning != "":
				if out != text {
					t.Fatalf("a warned decode changed the text")
				}
			case !startsLikeProtocolLine(out) || out != Base64DecodeLenient(text):
				t.Fatalf("decoded %q to %q without a protocol line", text, out)
			}
		}
		// Encoding any text and decoding it leniently gives the text back,
		// with ill-formed UTF-8 replaced as the WHATWG decoder does.
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawURLEncoding} {
			if got := Base64DecodeLenient(enc.EncodeToString([]byte(text))); got != wellFormed(text) {
				t.Fatalf("round trip of %q gave %q", text, got)
			}
		}
	})
}

// fuzzLine holds one line to the invariants every parse keeps: Line never
// fails except on N33, and an accepted node writes as JSON inside the
// bounds.
func fuzzLine(t *testing.T, line string) {
	t.Helper()
	n, ok, err := Line(line, Options{})
	if err != nil && !errors.Is(err, normalise.ErrCertificateNotPEM) {
		t.Fatalf("Line(%q): %v", line, err)
	}
	if ok {
		checkNodes(t, []*nodemodel.Node{n})
	}
}

// fuzzLines splits a fuzz input into trimmed lines, as the document does.
func fuzzLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if l = TrimECMAScript(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// checkPort fails when a grammar wrote a port outside 0 to 65535 or one
// that is not an integer.
func checkPort(t *testing.T, line string, f map[string]any) {
	t.Helper()
	p, ok := f["port"]
	if !ok {
		return
	}
	v, isNumber := p.(float64)
	if !isNumber || v < 0 || v > 65535 || v != float64(int(v)) {
		t.Fatalf("%q: port %#v", line, p)
	}
}

func FuzzSurgeLine(f *testing.F) {
	fuzzSeeds(f, "surge", "mixed")
	f.Add(`a = https, h, 443, u, "p", headers="X-A: 1;X-B: \"q;r\"", sni=off`)
	f.Add("a = tuic-v5, h, 1, port-hopping='1-2;3', alpn=\"h3, h2\", uuid=u")
	f.Add("a = vmess, h, 1, username=u, ws=true, ws-headers=Host:\"a\"|X:'b'")
	f.Add(`x = external, exec="/bin/x", args=a, addresses=[::1], local-port=1`)
	f.Add("a = ss, h, 99999, encrypt-method=aes-128-gcm, password=p")
	types := map[any]bool{"external": true}
	for _, st := range surgeTypes {
		types[st.nodeType] = true
	}
	f.Fuzz(func(t *testing.T, text string) {
		for _, line := range fuzzLines(text) {
			fuzzLine(t, line)
			if fields, err := parseSurge(line, nil); err == nil {
				name, _, _ := lineName(line)
				if fields["name"] != name || !types[fields["type"]] {
					t.Fatalf("%q: name %#v, type %#v", line, fields["name"], fields["type"])
				}
				checkPort(t, line, fields)
			}
			if fields, err := parseSurgeExternal(line, nil); err == nil && fields["type"] != "external" {
				t.Fatalf("%q: external parser gave type %#v", line, fields["type"])
			}
		}
	})
}

func FuzzLoonLine(f *testing.F) {
	fuzzSeeds(f, "loon", "mixed")
	f.Add(`a = shadowsocks, h, 1, aes-128-gcm, "p,q", tls, o.example.com, udp-over-tcp=false`)
	f.Add(`a = hysteria2, h, 1, "p", server-ports=" 1 - 2 , 3", server-dns=1.1.1.1, 8.8.8.8`)
	f.Add(`w = wireguard, interface-ip = 10.0.0.2, peers = [{endpoint = "h:1", reserved = [1,2,3]}]`)
	types := map[any]bool{}
	for _, lt := range loonTypes {
		types[lt.nodeType] = true
	}
	f.Fuzz(func(t *testing.T, text string) {
		for _, line := range fuzzLines(text) {
			fuzzLine(t, line)
			if fields, err := parseLoon(line, nil); err == nil {
				if !types[fields["type"]] {
					t.Fatalf("%q: type %#v", line, fields["type"])
				}
				// A Loon port outside the range rejects the line, so an
				// accepted line always has one.
				if _, ok := fields["port"]; !ok {
					t.Fatalf("%q: accepted without a port", line)
				}
				checkPort(t, line, fields)
			}
			if fields, err := parseLoonWireGuard(line, nil); err == nil {
				if peers, ok := fields["peers"].([]any); !ok || len(peers) != 1 {
					t.Fatalf("%q: peers %#v", line, fields["peers"])
				}
			}
		}
	})
}

func FuzzQXLine(f *testing.F) {
	fuzzSeeds(f, "qx", "mixed")
	f.Add("trojan=h:443, password=a,b=c, d, tls-alpn=02:68:32, tag=t")
	f.Add("shadowsocks=[::1]:1, method=aes-128-cfb, password=p, obfs=http_simple, obfs-host=o")
	f.Add("http= h.example.com :80, tag=a")
	types := map[any]bool{"ssr": true}
	for _, qt := range qxTypes {
		types[qt.nodeType] = true
	}
	f.Fuzz(func(t *testing.T, text string) {
		for _, line := range fuzzLines(text) {
			fuzzLine(t, line)
			if fields, err := parseQX(line, nil); err == nil {
				server, _ := fields["server"].(string)
				if !types[fields["type"]] || server != TrimECMAScript(server) {
					t.Fatalf("%q: type %#v, server %q", line, fields["type"], server)
				}
				checkPort(t, line, fields)
			}
		}
	})
}
