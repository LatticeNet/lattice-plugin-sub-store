package parse

import (
	"encoding/base64"
	"math"
	"testing"
)

func vmessLink(payload string) string {
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(payload))
}

func TestVMessQuantumultForm(t *testing.T) {
	line := vmessLink(`n = vmess, h.example.com, 443, , "`+uuid4+`", udp-relay=false, fast-open=true, tls-verification=false, over-tls=true`) + "#%20Frag"
	expectFields(t, "quantumult", parserFields(t, line), map[string]any{
		"name": " Frag", "server": "h.example.com", "port": "443", "cipher": "auto", "uuid": uuid4,
		"tls": false, "udp": "false", "tfo": "true", "skip-cert-verify": false, "network": nil,
	})
	for _, payload := range []string{
		`n = vmess, h, 443, auto, ` + uuid4,                   // uuid not quoted
		`n = vmess, h, 443, auto, "` + uuid4 + `", obfs=http`, // any obfs
		`n = vmess, h, 443`,                                   // too few parts
	} {
		if f := parserFields(t, vmessLink(payload)); f != nil {
			t.Errorf("%q accepted as %#v", payload, f)
		}
	}
}

func TestVMessV2rayNForm(t *testing.T) {
	for _, c := range []struct {
		payload string
		want    map[string]any
	}{
		{`{"add":"h","port":"8443x","id":"u","net":"tcp","type":"http","host":"a.example.com, b.example.com"}`,
			map[string]any{"network": "http", "port": 8443.0, "http-opts": map[string]any{"path": "/", "headers": map[string]any{"Host": "a.example.com"}}}},
		{`{"add":"h","port":1,"id":"u","obfs":"websocket","net":"grpc"}`,
			map[string]any{"network": "ws", "ws-opts": map[string]any{"path": "/", "headers": map[string]any{}}}},
		{`{"add":"h","port":1,"id":"u","net":"http","host":"a.example.com"}`,
			map[string]any{"network": "h2", "h2-opts": map[string]any{"path": "/", "host": []any{"a.example.com"}}}},
		{`{"add":"h","port":1,"id":"u","net":"kcp"}`,
			map[string]any{"network": "kcp", "kcp-opts": map[string]any{}}},
		{`{"add":"h","port":1,"id":"u","net":"grpc","authority":"a"}`,
			map[string]any{"network": nil, "grpc-opts": nil}},
		{`{"add":"h","port":1,"id":"u","net":"xhttp","path":"/p"}`,
			map[string]any{"network": nil}},
		{`{"add":"h","port":1,"id":"u","net":"httpupgrade","path":"/u","ed":"2048"}`,
			map[string]any{"network": "ws", "ws-opts": map[string]any{"path": "/u", "headers": map[string]any{}, "v2ray-http-upgrade": true,
				"v2ray-http-upgrade-fast-open": true, "_v2ray-http-upgrade-ed": "2048"}}},
		{`{"add":"h","port":1,"id":"u","net":"ws","host":"{\"Host\":\"j.example.com\"}"}`,
			map[string]any{"ws-opts": map[string]any{"path": "/", "headers": map[string]any{"Host": "j.example.com"}}}},
		{`{"add":"h","port":1,"id":"u","tls":"tls","sni":"","peer":"p.example.com","verify_cert":"false","allowInsecure":1}`,
			map[string]any{"tls": true, "sni": "p.example.com", "skip-cert-verify": true}}, // "false" is truthy, then allowInsecure applies
		{`{"add":"h","port":1,"id":"u","verify_cert":false,"allowInsecure":0}`,
			map[string]any{"skip-cert-verify": true}},
		{`{"add":"h","port":1,"id":"u","sni":"s.example.com","scy":"AES-128-GCM ","aid":"","alterId":"7"}`,
			map[string]any{"sni": nil, "cipher": "aes-128-gcm", "alterId": 7.0}},
		{`{"add":"h","id":"u","remarks":null,"remark":"R"}`,
			map[string]any{"name": "R"}},
	} {
		expectFields(t, c.payload, parserFields(t, vmessLink(c.payload)), c.want)
	}
	f := parserFields(t, vmessLink(`{"add":"h","id":"u"}`))
	if p, _ := f["port"].(float64); !math.IsNaN(p) || f["name"] != "VMess h:NaN" {
		t.Errorf("a v2rayN payload without a port gave port %v, name %v", f["port"], f["name"])
	}
	// A JSON value that is not an object reads every property as undefined.
	expectFields(t, "number payload", parserFields(t, vmessLink(`123`)), map[string]any{"server": nil, "name": "VMess undefined:NaN", "cipher": "auto"})
	for _, payload := range []string{`null`, `{"add":"h","alpn":["h2"]}`, `{"add":"h","alpn":5}`} {
		if f := parserFields(t, vmessLink(payload)); f != nil {
			t.Errorf("%s accepted as %#v", payload, f)
		}
	}
}

func TestVMessShadowrocketForm(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	line := "vmess://" + enc("auto:"+uuid4+"@[2001:db8::1]:443") + "/?remarks=R&tls=true&obfs=websocket&obfsParam=o.example.com&path=%2Fw&alpn=h2"
	expectFields(t, "shadowrocket", parserFields(t, line), map[string]any{
		"server": "[2001:db8::1]", "port": 443.0, "uuid": uuid4, "name": "R", "tls": false, "alpn": []any{"h2"},
		"ws-opts": map[string]any{"path": "/w", "headers": map[string]any{"Host": "o.example.com"}},
	})
	for _, l := range []string{
		"vmess://" + enc("auto:"+uuid4+"@h:443"),                  // no query
		"vmess://" + enc("auto:"+uuid4+"@h:443") + "?alpn=h2,h3",  // list-valued alpn
		"vmess://" + enc("auto"+uuid4+"h443") + "?remarks=R",      // decoded text does not match
		"vmess://" + enc("auto:"+uuid4+"@h:44x") + "?remarks=R",   // port is not digits
		"vmess://" + enc("auto:"+uuid4+"@h:443") + "?remarks=%zz", // strict decode
	} {
		if f := parserFields(t, l); f != nil {
			t.Errorf("%s accepted as %#v", l, f)
		}
	}
}
