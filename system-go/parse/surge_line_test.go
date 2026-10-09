package parse

import (
	"reflect"
	"testing"
)

func TestSurgeOptionMatching(t *testing.T) {
	const head = "a = ss, h.example.com, 8388, encrypt-method=aes-128-gcm, password=p"
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // a boolean is exactly true or false in lower case, else the option is unknown
			head + ", udp-relay=True, tfo=true",
			map[string]any{"udp": nil, "tfo": true},
		},
		{ // keys are case-sensitive
			head + ", Password=x",
			map[string]any{"password": "p"},
		},
		{ // enumeration words are tried in the list's order
			"a = ss, h, 1, encrypt-method=chacha20-poly1305, password=p",
			map[string]any{"cipher": "chacha20-poly1305"},
		},
		{
			"a = ss, h, 1, encrypt-method=rc4, password=p",
			map[string]any{"cipher": "rc4"},
		},
		{ // white space around "=" and ","; a text value keeps trailing white
			// space, which defeats quote stripping
			"a = ss , h , 1 , password =  \"p\" , udp-relay = true",
			map[string]any{"password": "\"p\" ", "udp": true, "server": "h"},
		},
		{ // a text value may hold "="
			"a = ss, h, 1, password=YWJj==",
			map[string]any{"password": "YWJj=="},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
	for _, line := range []string{
		head + ", flag",           // an option without "="
		head + ", tfo=falsey",     // a token with characters after it
		head + ",",                // an empty option
		head + ", =x",             // an empty key
		head + ", udp-relay=a=b",  // no token, and "=" in the value
		head + ", encrypt-method", // a known key without "="
	} {
		if f, err := parseSurge(line, nil); err == nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
	if _, err := parseSurge("a,b = ss, h, 1", nil); err == nil {
		t.Error("a name holding a comma was accepted")
	}
}

func TestSurgeCredentials(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // the positional user is trimmed and stripped, the password stripped
			"a = socks5, h, 1080, \" u \" , 'p'",
			map[string]any{"username": " u ", "password": "p"},
		},
		{ // a keyword pair after a positional pair overrides it
			"a = http, h, 80, u1, p1, username=u2, password=p2",
			map[string]any{"username": "u2", "password": "p2"},
		},
		{ // a positional user holding "=" is no user
			"a = https, h, 443, x=1, y",
			nil,
		},
		{ // the keyword pair must come first among the options
			"a = ssh, h, 22, idle-timeout=30, username=u, password=p",
			map[string]any{"username": nil, "password": nil, "idle-timeout": 30.0},
		},
	} {
		f, err := parseSurge(c.line, nil)
		if c.want == nil {
			if err == nil {
				t.Errorf("%s: accepted as %#v", c.line, f)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: rejected", c.line)
			continue
		}
		expectFields(t, c.line, f, c.want)
	}
}

func TestSurgeHeaderLists(t *testing.T) {
	for _, c := range []struct {
		value string
		sep   byte
		want  map[string]any
		end   int // index after the list, -1 for the whole value
	}{
		{"X-A: 1;X-B: 2", ';', map[string]any{"X-A": "1", "X-B": "2"}, -1},
		{`"X-A: 1;X-B: 2", next=1`, ';', map[string]any{"X-A": "1", "X-B": "2"}, 15},
		// A quoted value keeps the separator; a quoted name is a name.
		{`X-A: "a;X-C: b";X-B: 2`, ';', map[string]any{"X-A": "a;X-C: b", "X-B": "2"}, -1},
		{`"X-A": 1;'X-B' : '2'`, ';', map[string]any{"X-A": "1", "X-B": "2"}, -1},
		// Pairs without ":" and pairs with an empty name are dropped; an empty
		// name never starts a pair after a separator.
		{"foo;X-A: 1", ';', map[string]any{"X-A": "1"}, -1},
		{": 0;foo;X-A: 1;: 2", ';', map[string]any{"X-A": "1;: 2"}, -1},
		// A separator splits only when a header name and ":" follow it.
		{"X-A: a;b;X-B: c", ';', map[string]any{"X-A": "a;b", "X-B": "c"}, -1},
		// "|" is a token character, so a run of separators reads as one name.
		{"a|b|c: 1", '|', map[string]any{"c": "1"}, -1},
		{"Host:x, tfo=true", '|', map[string]any{"Host": "x"}, 6},
	} {
		ls := newLineScan(c.value)
		got, end, ok := headerList(ls, 0, c.sep)
		want := c.end
		if want < 0 {
			want = len(c.value)
		}
		if !ok || !reflect.DeepEqual(got, c.want) || end != want {
			t.Errorf("headerList(%q) = %v, %d, %v; want %v, %d", c.value, got, end, ok, c.want, want)
		}
	}
	if _, _, ok := headerList(newLineScan(`""`), 0, ';'); ok {
		t.Error("an empty list was read")
	}
}

func TestSurgePortHopping(t *testing.T) {
	for _, c := range []struct {
		line, rest, ports string
		ok                bool
	}{
		{"a = tuic, h, 1, port-hopping = '1000,2000;3000-4000', sni=s", "a = tuic, h, 1, sni=s", "1000,2000,3000-4000", true},
		{"a = masque, h, 1, port-hopping=1-2", "a = masque, h, 1", "1-2", true},
		{"a = hysteria2, h, 1, port-hopping-interval=30", "a = hysteria2, h, 1, port-hopping-interval=30", "", false},
		{`a = tuic, h, 1, port-hopping="1-2`, `a = tuic, h, 1, port-hopping="1-2`, "", false},
		{"a = tuic, h, 1, port-hopping=1-, sni=s", "a = tuic, h, 1, port-hopping=1-, sni=s", "", false},
	} {
		rest, ports, ok := liftPortHopping(c.line)
		if rest != c.rest || ports != c.ports || ok != c.ok {
			t.Errorf("liftPortHopping(%q) = %q, %q, %v", c.line, rest, ports, ok)
		}
	}
	// On other types port-hopping is an unknown option.
	if f, err := parseSurge("a = ss, h, 1, password=p, port-hopping=1000-2000", nil); err != nil || f["ports"] != nil {
		t.Errorf("ss with a range: %#v, %v", f, err)
	}
	if f, err := parseSurge("a = ss, h, 1, password=p, port-hopping=1000,2000", nil); err == nil {
		t.Errorf("ss with a list was accepted as %#v", f)
	}
}

func TestSurgeTypesOutsideTheCorpus(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // snell keeps a mode only from its list, after trim-strip
			"a = snell, h, 1, psk=k, mode=\"unshaped\"",
			map[string]any{"mode": "unshaped"},
		},
		{
			"a = snell, h, 1, psk=k, mode=other",
			map[string]any{"mode": nil},
		},
		{ // without vmess-aead the alterId is 1 (quirk); tls is a vmess option
			"a = vmess, h, 1, username=u , tls=true",
			map[string]any{"alterId": 1.0, "aead": nil, "cipher": "auto", "uuid": "u ", "tls": true},
		},
		{ // tls is not an ss option, so it is ignored
			"a = ss, h, 1, password=p, tls=true",
			map[string]any{"tls": nil},
		},
		{ // direct and wireguard have no server or port
			"a = direct",
			map[string]any{"type": "direct", "server": nil, "port": nil},
		},
		{ // h3=false sets nothing; trust-tunnel takes its credentials in any order
			"a = trust-tunnel, h, 443, password=p, username='u', h3=false",
			map[string]any{"network": nil, "username": "u", "password": "p", "tls": true},
		},
		{ // Shadow-TLS defaults to version 2 and takes the alpn
			"a = trojan, h, 443, password=p, shadow-tls-password=s, alpn=h2",
			map[string]any{"plugin": "shadow-tls", "plugin-opts": map[string]any{"password": "s", "version": 2.0, "alpn": []any{"h2"}}, "alpn": nil},
		},
		{ // sni "off" in quotes still disables SNI
			"a = trojan, h, 443, password=p, sni=\"off\"",
			map[string]any{"disable-sni": true, "sni": nil},
		},
		{ // WebSocket: the Host header loses one more pair of double quotes
			"a = trojan, h, 443, password=p, ws=true, ws-headers=Host:'\"x\"'",
			map[string]any{"network": "ws", "ws-opts": map[string]any{"headers": map[string]any{"Host": "x"}}},
		},
		{ // an alpn in quotes may hold commas; empty names are dropped
			"a = ss, h, 1, password=p, alpn=' h2, ,http/1.1 '",
			map[string]any{"alpn": []any{"h2", "http/1.1"}},
		},
		{ // max-streams in matching quotes only
			"a = h2-connect, h, 443, max-streams='4'",
			map[string]any{"max-streams": 4.0},
		},
		{
			"a = h2-connect, h, 443, max-streams=\"4'",
			map[string]any{"max-streams": nil},
		},
		{ // gecko-password names its obfs
			"a = hysteria2, h, 443, password=p, gecko-password='g'",
			map[string]any{"obfs": "gecko", "obfs-password": "g"},
		},
	} {
		f, err := parseSurge(c.line, nil)
		if err != nil {
			t.Errorf("%s: rejected", c.line)
			continue
		}
		expectFields(t, c.line, f, c.want)
	}
	if _, err := parseSurge("a = ss, h, 1, encrypt-method=aes-128-gcm-x", nil); err == nil {
		t.Error("a cipher with characters after a listed one was accepted")
	}
	if _, err := parseSurge("a = sss, h, 1", nil); err == nil {
		t.Error("an unknown type keyword was accepted")
	}
}

func TestSurgeExternal(t *testing.T) {
	f := parserFields(t, `x = external, exec = "/bin/x, y", args = "a", args=b, addresses = [::1], addresses = 10.0.0.300, local-port = "1080", exec=/bin/z`)
	expectFields(t, "external", f, map[string]any{
		"type": "external", "name": "x", "exec": "/bin/x, y", "args": []any{"a", "b"},
		"addresses": []any{"::1"}, "local-port": "1080",
	})
	if _, err := parseSurgeExternal("x = externals, exec=a", nil); err == nil {
		t.Error("a longer keyword was accepted as external")
	}
}
