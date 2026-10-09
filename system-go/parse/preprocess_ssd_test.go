package parse

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func ssdDocument(body string) string {
	return "ssd://" + base64.StdEncoding.EncodeToString([]byte(body))
}

func TestPreprocessSSDInheritsFromThePreviousServer(t *testing.T) {
	doc := ssdDocument(`{"port":8388,"encryption":"aes-128-gcm","password":"top","plugin":"ignored","servers":[
		{"server":"a.example.com","remarks":"A"},
		{"server":"b.example.com","port":9000,"encryption":"chacha20-ietf-poly1305","plugin":"obfs-local","plugin_options":"obfs=http;obfs-host=h"},
		{"server":"c.example.com"}]}`)
	out, kind := Preprocess(doc)
	if kind != KindSSD {
		t.Fatalf("kind %s", kind)
	}
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	want := strings.Join([]string{
		"ss://" + b64("aes-128-gcm:top") + "@a.example.com:8388#A",
		"ss://" + b64("chacha20-ietf-poly1305:top") + "@b.example.com:9000/?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dh#1",
		"ss://" + b64("chacha20-ietf-poly1305:top") + "@c.example.com:9000#2",
	}, "\n")
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestPreprocessSSDFailsOverToTheNextRow(t *testing.T) {
	for _, body := range []string{`not json`, `{"servers":[null]}`, `{"servers":{}}`, `null`} {
		if _, kind := Preprocess(ssdDocument(body)); kind == KindSSD {
			t.Errorf("SSD body %q was accepted", body)
		}
	}
}

func TestPreprocessSSDBoundsItsExpansion(t *testing.T) {
	// Every server inherits a 1 MiB password, so twenty servers expand past
	// the bound from a document under the raw bound.
	body := `{"port":1,"encryption":"aes-128-gcm","password":"` + strings.Repeat("p", 1<<20) + `","servers":[` +
		strings.TrimSuffix(strings.Repeat(`{"server":"h"},`, 20), ",") + `]}`
	doc := ssdDocument(body)
	if len(doc) > MaxDocumentBytes {
		t.Fatalf("test document is %d bytes, above the raw bound", len(doc))
	}
	if _, _, err := Document(doc, Options{}); !errors.Is(err, ErrExpansionTooLarge) {
		t.Fatalf("Document = %v, want ErrExpansionTooLarge", err)
	}
}
