package parse

import (
	"encoding/base64"
	"testing"
)

func TestSSRParameters(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	link := func(params string) string {
		return "ssr://" + enc("h.example.com:8388:auth_aes128_md5:aes-128-cfb:http_simple:"+enc("pw")+"/?"+params)
	}
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // protocolparam is the fallback; white space is removed; (null) and
			// empty values are ignored.
			link("protocolparam=" + enc("1 2\t3") + "&obfsparam=(null)&remarks="),
			map[string]any{"protocol-param": "123", "obfs-param": nil, "name": "h.example.com"},
		},
		{ // values are trimmed before use, and a single item is ignored
			link("remarks= " + enc("R") + " &group=g"),
			map[string]any{"name": "R"},
		},
		{
			link("remarks=" + enc("R")),
			map[string]any{"name": "h.example.com"},
		},
		{ // a blank decoded parameter is not set
			link("obfsparam=" + enc("   ") + "&group=g"),
			map[string]any{"obfs-param": nil, "protocol": "auth_aes128_md5", "cipher": "aes-128-cfb", "obfs": "http_simple", "password": "pw"},
		},
		{ // the auth_ marker ends the host and port when there is no :origin
			link("a=1&b=2"),
			map[string]any{"server": "h.example.com", "port": "8388"},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
}
