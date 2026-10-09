package parse

import (
	"encoding/base64"
	"testing"
)

func TestSSRowsOutsideTheCorpus(t *testing.T) {
	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // no fragment: the default name; no query: TLS absent, UDP on.
			"ss://" + b64("aes-128-gcm:pw") + "@h.example.com:8388",
			map[string]any{"name": "SS h.example.com:8388", "tls": nil, "udp": true, "skip-cert-verify": false},
		},
		{ // the port is the first run of digits after the last ":".
			"ss://aes-128-gcm:pw@[2001:db8::1]:8388x/?udp=OFF#n",
			map[string]any{"server": "[2001:db8::1]", "port": 8388.0, "udp": false},
		},
		{ // legacy form: a v2ray-plugin item holds a Base64 JSON object.
			"ss://" + b64("aes-128-gcm:pw@h:1") + "?v2ray-plugin=" + b64(`{"mode":"websocket","host":"w.example.com"}`) + "#n",
			map[string]any{"plugin": "v2ray-plugin", "plugin-opts": map[string]any{"mode": "websocket", "host": "w.example.com"}},
		},
		{ // legacy form: plugin items are found in the decoded text too.
			"ss://" + b64("aes-128-gcm:pw@h:1/?plugin=obfs-local;obfs=tls") + "#n",
			map[string]any{"server": "h", "port": 1.0, "plugin": "obfs", "plugin-opts": map[string]any{"mode": "tls"}},
		},
		{ // "\=" in a plugin value is "="; v2ray-plugin defaults and value forms.
			"ss://aes-128-gcm:pw@h:1/?plugin=v2ray-plugin%3Bhost%3Da%5C%3Db%3Btls%3D1%3Bmux%3Dx%3Bskip-cert-verify%3Dtrue#n",
			map[string]any{"plugin-opts": map[string]any{"mode": "websocket", "host": "a=b", "tls": "1", "skip-cert-verify": true}},
		},
		{ // a bare tls item is the boolean true, and so is an empty value.
			"ss://aes-128-gcm:pw@h:1/?plugin=v2ray-plugin%3Btls%3Bsni%3D#n",
			map[string]any{"plugin-opts": map[string]any{"mode": "websocket", "tls": true, "sni": true, "skip-cert-verify": false}},
		},
		{ // Reality with a type: the top-level mode and extra annotations.
			"ss://aes-128-gcm:pw@h:1?type=tcp&security=reality&sid=ab&mode=m&extra=x#n",
			map[string]any{"reality-opts": map[string]any{"short-id": "ab"}, "_mode": "m", "_extra": "x", "tls": true},
		},
		{ // gRPC options from the query.
			"ss://aes-128-gcm:pw@h:1?type=grpc&serviceName=s&mode=multi&authority=a#n",
			map[string]any{"network": "grpc", "grpc-opts": map[string]any{"grpc-service-name": "s", "_grpc-type": "multi", "_grpc-authority": "a"}},
		},
		{ // uot and tfo are prefix matches: "uot=0" does not count.
			"ss://aes-128-gcm:pw@h:1?uot=0&tfo=true#n",
			map[string]any{"udp-over-tcp": nil, "tfo": true},
		},
		{ // a bare security item reads as the text undefined, which is TLS.
			"ss://aes-128-gcm:pw@h:1?security&allowInsecure#n",
			map[string]any{"tls": true, "skip-cert-verify": true},
		},
		{ // shadow-tls and gost replace the server and port.
			"ss://aes-128-gcm:pw@h:1?shadow-tls=" + b64(`{"version":"2","host":"s.example.com","password":" ","address":"a.example.com","port":"443x"}`) + "#n",
			map[string]any{"plugin": "shadow-tls", "plugin-opts": map[string]any{"host": "s.example.com", "version": 2.0}, "server": "a.example.com", "port": 443.0},
		},
		{
			"ss://aes-128-gcm:pw@h:1?gost=" + b64(`{"route":"WS","path":"/p"}`) + "#n",
			map[string]any{"plugin": "gost-plugin", "plugin-opts": map[string]any{"mode": "websocket", "path": "/p"}, "server": "h"},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
	for _, line := range []string{
		"ss://aes-128-gcm:pw@h:1?alpn=%25zz#n",             // second decode of alpn fails
		"ss://aes-128-gcm:pw@h:1?type=ws&host=%25zz#n",     // second decode of host fails
		"ss://aes-128-gcm:pw@h:1/?plugin=obfs-local%zz#n",  // plugin strict decode
		"ss://aes-128-gcm:pw@h:1?shadow-tls=bm90IGpzb24#n", // not JSON
		"ss://aes-128-gcm:pw@h#n",                          // no port
		"ss://" + b64("pw@h:1") + "#n",                     // no cipher
	} {
		if f := parserFields(t, line); f != nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}

func TestHasFlagItem(t *testing.T) {
	for _, c := range []struct {
		query string
		want  bool
	}{
		{"uot=1", true}, {"uot=10", true}, {"a=1&UOT=True", true}, {"uot=0", false},
		{"uot=", false}, {"x=uot", false}, {"uot=0&uot=1", true},
	} {
		if got := hasFlagItem(c.query, "uot="); got != c.want {
			t.Errorf("hasFlagItem(%q) = %v, want %v", c.query, got, c.want)
		}
	}
}
