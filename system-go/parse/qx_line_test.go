package parse

import (
	"reflect"
	"testing"
)

func TestQXALPNDecoding(t *testing.T) {
	for _, c := range []struct {
		value string
		want  []any
		ok    bool
	}{
		{"02:68:32:08:68:74:74:70:2f:31:2e:31", []any{"h2", "http/1.1"}, true},
		{"026832", []any{"h2"}, true},
		{"0268", nil, false}, // the name runs past the end
		{"02683", nil, false},
		{"h2", nil, false},
		{"", nil, false},
	} {
		got, ok := decodeQXALPN(c.value)
		if ok != c.ok || (ok && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("decodeQXALPN(%q) = %v, %v; want %v, %v", c.value, got, ok, c.want, c.ok)
		}
	}
}

func TestQXRowsOutsideTheCorpus(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // a bracketed or bare IPv6 host; the port after the last ":"
			"http=[2001:db8::1]:8080, tag=a",
			map[string]any{"server": "[2001:db8::1]", "port": 8080.0},
		},
		{
			"http= 2001:db8::1:80 , tag=a",
			map[string]any{"server": "2001:db8::1", "port": 80.0},
		},
		{ // a port outside 0 to 65535 is absent
			"socks5=h:70000",
			map[string]any{"port": nil, "server": "h"},
		},
		{ // a password may hold "=" and commas
			"trojan=h:443, password=a=b, c, tag=t",
			map[string]any{"password": "a=b, c", "name": "t"},
		},
		{ // tls-verification with other text names the certificate
			"trojan=h:443, password=p, tls-verification= x.example.com ",
			map[string]any{"name-cert-verify": "x.example.com", "skip-cert-verify": nil},
		},
		{ // an HTTP obfs spelling is kept as written and selects HTTP
			"vmess=h:80, password=u, obfs=vemss-http, obfs-host=o, obfs-uri=/p",
			map[string]any{"_qx_obfs_http": "vemss-http", "network": "http", "http-opts": map[string]any{"path": "/p", "headers": map[string]any{"Host": "o"}}},
		},
		{ // over-tls on Shadowsocks without an obfs host
			"shadowsocks=h:443, method=aes-128-gcm, password=p, obfs=over-tls",
			map[string]any{"tls": true, "sni": nil, "plugin": nil},
		},
		{ // ssr-protocol alone makes ShadowsocksR, without an obfs
			"shadowsocks=h:1, method=aes-128-cfb, password=p, ssr-protocol=auth_chain_b",
			map[string]any{"type": "ssr", "protocol": "auth_chain_b", "obfs": nil, "obfs-param": nil},
		},
		{ // VLESS carries cipher none; over-tls keeps an sni already set
			"vless=h:443, password=u, tls-host=s, obfs=over-tls, obfs-host=o",
			map[string]any{"cipher": "none", "sni": "s", "tls": true},
		},
		{ // a non-boolean udp-over-tcp on trojan is an unknown option
			"trojan=h:443, password=p, udp-over-tcp=1",
			map[string]any{"udp-over-tcp": nil},
		},
		{ // anytls is TLS
			"anytls=h:443, password=p",
			map[string]any{"tls": true},
		},
	} {
		f, err := parseQX(c.line, nil)
		if err != nil {
			t.Errorf("%s: rejected", c.line)
			continue
		}
		expectFields(t, c.line, f, c.want)
	}
	for _, line := range []string{
		"http=h, tag=a",     // no ":" before the first comma
		"http=h:80x, tag=a", // junk after the port
		"shadowsocks=h:1, udp-over-tcp=sp.v3",
		"trojan=h:443, server_check_url=http://x/?a=1",
		"trojan=h:443, tag=a=b",
		"trojan=h:443, obfs=wsx",
	} {
		if f, err := parseQX(line, nil); err == nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}
