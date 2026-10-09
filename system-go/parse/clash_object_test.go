package parse

import "testing"

func TestClashObjectMappings(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // servername moves to sni on vmess and vless only
			`{"type":"vless","server":"h","servername":"s.example.com"}`,
			map[string]any{"sni": "s.example.com", "servername": nil},
		},
		{
			`{"type":"trojan","server":"h","servername":"s.example.com"}`,
			map[string]any{"sni": nil, "servername": "s.example.com"},
		},
		{ // fingerprint wins over server-cert-fingerprint; the originals stay
			`{"type":"ss","server-cert-fingerprint":"A","fingerprint":"B","dialer-proxy":"d","benchmark-url":"u","benchmark-timeout":5}`,
			map[string]any{
				"tls-fingerprint": "B", "server-cert-fingerprint": "A", "fingerprint": "B",
				"underlying-proxy": "d", "dialer-proxy": "d", "test-url": "u", "test-timeout": 5.0,
			},
		},
		{ // falsy values are not copied
			`{"type":"ss","fingerprint":"","dialer-proxy":0}`,
			map[string]any{"tls-fingerprint": nil, "underlying-proxy": nil},
		},
		{ // the VMess cipher is normalised; an absent cipher is left to N17
			`{"type":"vmess","cipher":" CHACHA20-IETF-POLY1305 "}`,
			map[string]any{"cipher": "chacha20-poly1305"},
		},
		{
			`{"type":"vmess"}`,
			map[string]any{"cipher": nil},
		},
		{ // H1: underscore keys go at any depth, inside lists too
			`{"type":"vless","_a":1,"reality-opts":{"_spider-x":"/","public-key":"K"},"l":[{"_b":2,"c":3}]}`,
			map[string]any{"_a": nil, "reality-opts": map[string]any{"public-key": "K"}, "l": []any{map[string]any{"c": 3.0}}},
		},
		{ // a flow mapping that is not JSON5 is read as YAML
			"{name: a, type: ss, server: h.example.com, port: 0088, password: !!str 0123}",
			map[string]any{"type": "ss", "port": 88.0, "password": "0123"},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
	for _, line := range []string{
		`{"type":"SS","server":"h","port":1}`,
		`{"type":"external","exec":"x"}`,
		`{"type":"wireguard-surge"}`,
		`{"type":true}`,
		"{name: a, name: b, type: ss}",
	} {
		if f := parserFields(t, line); f != nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}
