package parse

import (
	"errors"
	"strings"
	"testing"
)

func TestPreprocessClashWritesOneJSONLinePerProxy(t *testing.T) {
	doc := `port: 7890
proxies:
  - {name: a, type: vless, server: s.example.com, port: 443, short-id: 0088}
  - name: b
    type: trojan
    server: t.example.com
    port: 443
    reality-opts: {short-id: '', public-key: k}
  - just a string
  - ~
proxy-groups:
  - {name: g, type: select, proxies: [a, b]}
`
	out, kind := Preprocess(doc)
	if kind != KindClash {
		t.Fatalf("kind %s", kind)
	}
	want := strings.Join([]string{
		`{"name":"a","port":443,"server":"s.example.com","short-id":"0088","type":"vless"}`,
		`{"name":"b","port":443,"reality-opts":{"public-key":"k","short-id":""},"server":"t.example.com","type":"trojan"}`,
		`"just a string"`,
		`null`,
	}, "\n")
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestPreprocessClashEdges(t *testing.T) {
	if out, kind := Preprocess("proxy-groups:\n  - {name: g, type: select}\nproxies: none\n"); kind != KindClash || out != "" {
		t.Errorf("groups-only document gave %s %q", kind, out)
	}
	// A YAML error makes the row fail; the raw text is split into lines.
	dup := "proxies:\n  - {name: a, name: b, type: ss}\n"
	if out, kind := Preprocess(dup); kind != KindPlain || out != dup {
		t.Errorf("duplicate-key document gave %s %q", kind, out)
	}
	if _, kind := Preprocess("proxies_list: [1]\n"); kind == KindClash {
		t.Error("a document without a proxies list matched")
	}
}

func TestQuoteShortIDs(t *testing.T) {
	in := "a:\n  short-id: 0088 # keep\n  b: {short-id: 12ab, x: 1}\n  short-id:\n  short-id: 'q'\n  short-id: null\n  short-id: \"d\"\r\n"
	want := "a:\n  short-id: \"0088\" # keep\n  b: {short-id: \"12ab\", x: 1}\n  short-id: \"\"\n  short-id: 'q'\n  short-id: null\n  short-id: \"d\"\r\n"
	if got := quoteShortIDs(in); got != want {
		t.Errorf("quoteShortIDs:\n got %q\nwant %q", got, want)
	}
}

func TestPreprocessClashBoundsAliasExpansion(t *testing.T) {
	big := strings.Repeat("x", 200<<10)
	var b strings.Builder
	b.WriteString("blob: &b " + big + "\nproxies:\n")
	for i := 0; i < 99; i++ {
		b.WriteString("  - {name: n, type: ss, password: *b}\n")
	}
	if _, _, err := Document(b.String(), Options{}); !errors.Is(err, ErrExpansionTooLarge) {
		t.Fatalf("Document = %v, want ErrExpansionTooLarge", err)
	}
}
