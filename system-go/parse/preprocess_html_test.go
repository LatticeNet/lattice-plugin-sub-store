package parse

import "testing"

func TestPreprocessHTMLNeedsTheExactDoctype(t *testing.T) {
	for in, want := range map[string]Kind{
		"<!DOCTYPE html><html>vless://a@b:1</html>": KindHTML,
		"<!doctype html>\nvless://a@b:1":            KindPlain,
		" <!DOCTYPE html>":                          KindPlain,
	} {
		out, kind := Preprocess(in)
		if kind != want {
			t.Errorf("Preprocess(%q) kind = %s, want %s", in, kind, want)
		}
		if kind == KindHTML && out != "" {
			t.Errorf("an HTML page gave %q, want the empty text", out)
		}
	}
}
