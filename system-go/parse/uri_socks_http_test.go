package parse

import (
	"encoding/base64"
	"testing"
)

func TestProxyURIRowsOutsideTheCorpus(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // https without a port: 443, TLS, and the default name uses "http".
			"https://u:p@h.example.com",
			map[string]any{"type": "http", "tls": true, "port": 443.0, "username": "u", "password": "p", "name": "http h.example.com:443"},
		},
		{ // a port followed by "/" and a query: the query is ignored.
			"socks5://h.example.com:1080/?sni=x#n",
			map[string]any{"server": "h.example.com", "port": 1080.0, "tls": false, "sni": nil},
		},
		{ // the user info is strict-decoded, the password running to the
			// first "@" after the first ":".
			"socks5://u%40x:p%3Aw@h:1#n",
			map[string]any{"username": "u@x", "password": "p:w", "server": "h"},
		},
		{ // an empty fragment is the empty name.
			"http://h:8080#",
			map[string]any{"name": ""},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
	for _, line := range []string{
		"socks5://h.example.com#n", // socks5 needs a port
		"socks5://u:p@h.example.com",
		"http://u%zz:p@h:80", // strict decode of the user name
		"http://h:80#%zz",    // strict decode of the fragment
	} {
		if f := parserFields(t, line); f != nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}

func TestSocksURI(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // further pieces of the user info are dropped
			"socks://" + b64("u:p:extra") + "@h:1080?x=1#n",
			map[string]any{"type": "socks5", "username": "u", "password": "p", "server": "h", "port": 1080.0, "tls": nil},
		},
		{ // the user info ends at the last "@"
			"socks://a@" + b64("u:p") + "@h:1080",
			map[string]any{"server": "h", "name": "socks h:1080"},
		},
		{ // no user info at all
			"socks://h.example.com:1080",
			map[string]any{"username": nil, "password": nil, "server": "h.example.com"},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
	for _, line := range []string{"socks://" + b64("u:p") + "@h:", "socks://h:10x", "socks://%zz@h:1"} {
		if f := parserFields(t, line); f != nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}
