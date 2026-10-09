package parse

import (
	"encoding/base64"
	"testing"
)

func TestPreprocessBase64KnownSchemeAndFallback(t *testing.T) {
	links := "vmess://abc\nvless://u@h:1#n\n"
	std := base64.StdEncoding.EncodeToString([]byte(links))
	out, kind := Preprocess(std)
	if kind != KindBase64 || out != links {
		t.Fatalf("known-scheme document gave %s %q", kind, out)
	}
	// "vless://" holds "ss:/" at a three-byte boundary, so a VLESS list is
	// caught by the known-scheme row; an AnyTLS list carries no marker.
	if !containsAny(base64.StdEncoding.EncodeToString([]byte("vless://u@h:1")), knownSchemeMarkers) {
		t.Error("a VLESS link lost its ss:/ marker")
	}
	anytls := "anytls://p@h:1?a=1&b=~~~#n"
	url := base64.RawURLEncoding.EncodeToString([]byte(anytls))
	if out, kind := Preprocess(url); kind != KindBase64Fallback || out != anytls {
		t.Fatalf("URL-safe fallback document gave %s %q", kind, out)
	}
	// A known marker over prose: the raw text comes back with a warning, and
	// the fallback row does not run.
	prose := "dm1lc3MgaXMgYSB3b3Jk" // "vmess is a word"
	raw, kind, warnings, err := preprocess(prose)
	if err != nil || kind != KindBase64 || raw != prose || len(warnings) != 1 || warnings[0].Line != 0 {
		t.Fatalf("known marker over prose gave %s %q %v %v", kind, raw, warnings, err)
	}
}

func TestStartsLikeProtocolLine(t *testing.T) {
	for in, want := range map[string]bool{
		"vless://x":         true,
		"prose\nss://y":     true,
		"a\r\nb = c":        true,
		"name =  value":     true,
		"key\n= value":      true, // white space before "=" may span a line
		"x\u2028trojan://p": true,
		"://x":              false,
		"vless://":          false,
		"vless:// x":        false,
		"just words here":   false,
		" vless://x":        false,
		"a.b://c":           false,
		"tuic=\u3000ok":     true,
		"":                  false,
	} {
		if got := startsLikeProtocolLine(in); got != want {
			t.Errorf("startsLikeProtocolLine(%q) = %v, want %v", in, got, want)
		}
	}
}
