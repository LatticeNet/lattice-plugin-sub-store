package parse

import "testing"

func TestLoonRowsOutsideTheCorpus(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // the type keyword in any case
			`a = TROJAN, h, 443, "p", over-tls=true`,
			map[string]any{"type": "trojan", "password": "p", "tls": true},
		},
		{ // VMess security normalisation
			`a = vmess, h, 1, CHACHA20-IETF-POLY1305 , "u"`,
			map[string]any{"cipher": "chacha20-poly1305", "uuid": "u", "alterId": 0.0},
		},
		{
			`a = vmess, h, 1, NONE, "u", alterId=2`,
			map[string]any{"cipher": "none", "alterId": 2.0},
		},
		{ // http with only a quoted password
			`a = http, h, 80, "p", fast-open=true`,
			map[string]any{"password": "p", "username": nil, "tfo": true},
		},
		{ // server-dns quoted, or unquoted up to the next ", key="
			`a = trojan, h, 443, "p", server-dns = "1.1.1.1, 8.8.8.8"`,
			map[string]any{"server-dns": []any{"1.1.1.1", "8.8.8.8"}},
		},
		{
			`a = trojan, h, 443, "p", server-dns=1.1.1.1, 8.8.8.8 ,, udp=true`,
			map[string]any{"server-dns": []any{"1.1.1.1", "8.8.8.8"}, "udp": true},
		},
		{ // an unquoted alpn is an unknown option; a quoted one is a list
			`a = trojan, h, 443, "p", alpn=h2`,
			map[string]any{"alpn": nil},
		},
		{
			`a = trojan, h, 443, "p", alpn="h2, ,http/1.1"`,
			map[string]any{"alpn": []any{"h2", "http/1.1"}},
		},
		{ // block-quic is written on or off; tls-profile maps the fingerprint
			`a = vless, h, 443, "u", block-quic=false, tls-profile="chrome147"`,
			map[string]any{"block-quic": "off", "_loon_tls_profile": "chrome147", "client-fingerprint": "chrome"},
		},
		{
			`a = vless, h, 443, "u", tls-profile=other`,
			map[string]any{"_loon_tls_profile": "other", "client-fingerprint": nil},
		},
		{ // Shadow-TLS without a version keeps it absent (unlike Surge)
			`a = shadowsocks, h, 443, aes-128-gcm, "p", shadow-tls-password='s', shadow-tls-sni=x`,
			map[string]any{"plugin": "shadow-tls", "plugin-opts": map[string]any{"password": "s", "host": "x"}},
		},
		{ // the obfs word of ssr is copied to obfs
			`a = shadowsocksr, h, 1, aes-128-cfb, "p", obfs=tls1.2_ticket_fastauth, protocol=origin`,
			map[string]any{"obfs": "tls1.2_ticket_fastauth", "protocol": "origin"},
		},
		{ // tls-name is not an http option
			`a = http, h, 80, u, "p", tls-name=x, fast-open=true`,
			map[string]any{"sni": nil, "username": " u"},
		},
		{ // Reality keys build reality-opts
			`a = vless, h, 443, "u", public-key="K", short-id=ab`,
			map[string]any{"reality-opts": map[string]any{"public-key": "K", "short-id": "ab"}},
		},
	} {
		f, err := parseLoon(c.line, nil)
		if err != nil {
			t.Errorf("%s: rejected", c.line)
			continue
		}
		expectFields(t, c.line, f, c.want)
	}
	for _, line := range []string{
		`a = shadowsocks, h, 1, aes-128-gcm, "p", http, obfs-host=x`, // an obfs host holding "=" is no host
		`a = vmess, h, 1, auto, ""`,                                  // an empty UUID
		`a = trojan, h, 443, p`,                                      // an unquoted password
		`a = shadowsocks, h, 443, aes-128-gcm, "p", shadow-tls-password=s, shadow-tls-version=1`,
		`a = shadowsocks, h, 1, aes-128-gcmx, "p"`,
	} {
		if f, err := parseLoon(line, nil); err == nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}

func TestLoonWireGuardKeys(t *testing.T) {
	line := `w = wireguard, server-dns = 9.9.9.9, dns = 1.1.1.1, private-key = "K", peers = [{endpoint ="h.example.com:51820", allowed-ips = 10.0.0.0/8, reserved = "[1,2,3]"}]`
	f, err := parseLoonWireGuard(line, nil)
	if err != nil {
		t.Fatalf("rejected: %v", err)
	}
	expectFields(t, "wireguard", f, map[string]any{
		"server-dns": []any{"9.9.9.9"}, "dns": []any{" 1.1.1.1"}, "remote-dns-resolve": true,
		"private-key": ` "K"`, "server": "h.example.com", "port": 51820.0,
		"allowed-ips": nil, "reserved": []any{1.0, 2.0, 3.0},
	})
	if _, err := parseLoonWireGuard("w = wireguard, private-key = K", nil); err == nil {
		t.Error("a line without a peers block was accepted")
	}
}
