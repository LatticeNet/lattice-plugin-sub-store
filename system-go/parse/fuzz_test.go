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

var fuzzSchemes = []string{"vless://", "vmess://", "anytls://", "ss://", "trojan://", ""}

func FuzzParseURI(f *testing.F) {
	fuzzSeeds(f, "vless", "vmess")
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
		_, _ = parseVLESS(rest, nil)
		_, _ = parseVMess(rest, nil)
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
