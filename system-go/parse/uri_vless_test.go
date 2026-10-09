package parse

import (
	"encoding/base64"
	"reflect"
	"testing"
)

// parserFields runs parser selection on one line and returns the parser's
// output before the normaliser, or nil when no parser accepts the line.
func parserFields(t *testing.T, line string) map[string]any {
	t.Helper()
	f, _, ok := newLineState(nil).parse(line)
	if !ok {
		return nil
	}
	return f
}

// expectFields checks the listed fields; a nil want means the key must be
// absent.
func expectFields(t *testing.T, line string, f map[string]any, want map[string]any) {
	t.Helper()
	if f == nil {
		t.Errorf("%s: rejected", line)
		return
	}
	for k, w := range want {
		got, ok := f[k]
		if w == nil {
			if ok {
				t.Errorf("%s: %s = %#v, want absent", line, k, got)
			}
			continue
		}
		if !ok || !reflect.DeepEqual(got, w) {
			t.Errorf("%s: %s = %#v (present %v), want %#v", line, k, got, ok, w)
		}
	}
}

const uuid4 = "00000000-0000-4000-8000-000000000000"

func TestVLESSShape(t *testing.T) {
	for _, c := range []struct {
		rest, host, port, query, fragment string
		hasFragment, ok                   bool
	}{
		{"u@h.example.com:443?a=1#n", "h.example.com", "443", "a=1", "n", true, true},
		{"u@[2001:db8::1]:8443/?a=1", "[2001:db8::1]", "8443", "a=1", "", false, true},
		{"u@2001:db8::1:443", "2001:db8::1", "443", "", "", false, true},
		{"u@h:443#a#b", "h", "443", "", "a#b", true, true},
		{"u@h:443?q#f?x", "h", "443", "q", "f?x", true, true},
		{"u@h:443/x", "", "", "", "", false, false},
		{"u@h:x", "", "", "", "", false, false},
		{"u@h", "", "", "", "", false, false},
		{"uh:443", "", "", "", "", false, false},
		{"u@h:443?a\r=1", "", "", "", "", false, false},
		{"u@@h:1", "@h", "1", "", "", false, true},
		// The host is the shortest that works, so ":digits" later in the
		// fragment or the query stays there.
		{"u@h:443#name:8080", "h", "443", "", "name:8080", true, true},
		{"u@h:443?path=%2Fa:80", "h", "443", "path=%2Fa:80", "", false, true},
	} {
		s, ok := matchVLESSShape(c.rest)
		if ok != c.ok {
			t.Errorf("%q: ok = %v, want %v", c.rest, ok, c.ok)
			continue
		}
		if ok && (s.host != c.host || s.port != c.port || s.query != c.query || s.fragment != c.fragment || s.hasFragment != c.hasFragment) {
			t.Errorf("%q: got %+v", c.rest, s)
		}
	}
}

func TestVLESSRowsOutsideTheCorpus(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // security=reality alone: Reality with no items gives no reality-opts.
			"vless://" + uuid4 + "@h:443?security=reality&type=tcp#n",
			map[string]any{"tls": true, "reality-opts": nil, "network": "tcp"},
		},
		{ // remarks empty, remark set; the name falls to remark.
			"vless://" + uuid4 + "@h:443?remarks=&remark=R",
			map[string]any{"name": "R"},
		},
		{ // a port with a leading zero names the node with the number.
			"vless://" + uuid4 + "@h:0443",
			map[string]any{"name": "VLESS h:443", "port": 443.0},
		},
		{ // packetEncoding is trimmed and lower-cased; fm that is not an object is text.
			"vless://" + uuid4 + "@h:443?packetEncoding=%20Packet%20&fm=5&pqv=p",
			map[string]any{"packet-encoding": "packetaddr", "_finalmask": "5", "_pqv": "p"},
		},
		{ // sni falls to peer; vcn drops empty names.
			"vless://" + uuid4 + "@h:443?security=tls&peer=p.example.com&vcn=%20,a.example.com,",
			map[string]any{"sni": "p.example.com", "_vcn": []any{"a.example.com"}, "name-cert-verify": "a.example.com"},
		},
		{ // obfsParam that is JSON becomes the whole headers object.
			"vless://" + uuid4 + "@h:443?type=ws&obfsParam=%7B%22Host%22%3A%22o.example.com%22%2C%22X%22%3A%221%22%7D",
			map[string]any{"ws-opts": map[string]any{"headers": map[string]any{"Host": "o.example.com", "X": "1"}}},
		},
		{ // HTTPUpgrade early data from the ed item; eh names the header even so.
			"vless://" + uuid4 + "@h:443?type=httpupgrade&path=%2Fu&ed=2048&eh=X-Ed",
			map[string]any{"network": "ws", "ws-opts": map[string]any{
				"path": "/u", "v2ray-http-upgrade": true, "v2ray-http-upgrade-fast-open": true,
				"_v2ray-http-upgrade-ed": "2048", "early-data-header-name": "X-Ed",
			}},
		},
		{ // eh without early data on WebSocket.
			"vless://" + uuid4 + "@h:443?type=websocket&eh=X-Ed",
			map[string]any{"network": "ws", "ws-opts": map[string]any{"early-data-header-name": "X-Ed"}},
		},
		{ // xhttp: the Host header moves to the host option.
			"vless://" + uuid4 + "@h:443?type=xhttp&host=x.example.com",
			map[string]any{"xhttp-opts": map[string]any{"host": "x.example.com"}},
		},
		{ // h2 without a path gets "/", the host list is split and trimmed, and
			// the emptied headers object stays for N21 to drop.
			"vless://" + uuid4 + "@h:443?type=http&host=a.example.com%2C%20b.example.com%2C",
			map[string]any{"network": "h2", "h2-opts": map[string]any{"host": []any{"a.example.com", "b.example.com"}, "path": "/", "headers": map[string]any{}}},
		},
		{ // gRPC without mode: _grpc-type gun and no top-level _mode.
			"vless://" + uuid4 + "@h:443?type=grpc&serviceName=s&authority=a.example.com",
			map[string]any{"_mode": nil, "grpc-opts": map[string]any{"grpc-service-name": "s", "_grpc-type": "gun", "_grpc-authority": "a.example.com"}},
		},
		{ // kcp: headerType defaults to none, an empty seed is not set, mode is top-level.
			"vless://" + uuid4 + "@h:443?type=kcp&seed=&mode=m",
			map[string]any{"headerType": "none", "seed": nil, "_mode": "m", "kcp-opts": nil},
		},
		{ // extra on a network other than xhttp is kept as text.
			"vless://" + uuid4 + "@h:443?type=ws&extra=%7B%22a%22%3A1%7D",
			map[string]any{"_extra": `{"a":1}`, "_extra_unsupported": nil},
		},
		{ // a non-numeric ed inside the path stays in the path.
			"vless://" + uuid4 + "@h:443?type=ws&path=%2Fp%3Fx%3D1%26ed%3Dabc",
			map[string]any{"ws-opts": map[string]any{"path": "/p?x=1&ed=abc"}},
		},
		{ // early data in the path: ed items go, other items keep their spelling.
			"vless://" + uuid4 + "@h:443?type=ws&path=%2Fp%3Fx%3D%2541%26ed%3D10%26%26ed%3D20",
			map[string]any{"ws-opts": map[string]any{"path": "/p?x=%41", "max-early-data": 10.0, "early-data-header-name": "Sec-WebSocket-Protocol"}},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
}

func TestVLESSRejectsWhatTheShapeAndDecodersRefuse(t *testing.T) {
	for _, line := range []string{
		"vless://%zz@h:443#n",                        // uuid strict decode
		"vless://" + uuid4 + "@h:443#%zz",            // fragment strict decode
		"vless://" + uuid4 + "@h:443?sni=%E4%B8#n",   // query strict decode
		"vless://" + uuid4 + "@h:443?type=ws&ed=abc", // non-numeric ed item
		"vless://" + uuid4 + "@h:443?type=ws&ed=9007199254740992",
		"vless://bm90LWEtdmxlc3MtbGluaw",        // no shape and no "?"
		"vless://" + uuid4 + "@h:443?a=\u20281", // a line terminator inside
	} {
		if f := parserFields(t, line); f != nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}

func TestVLESSShadowrocketForm(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	f := parserFields(t, "vless://"+enc("auto:"+uuid4+"@h.example.com:443")+"?xtls=1&obfs=none&tls=TRUE&path=%2Fs")
	expectFields(t, "shadowrocket tcp", f, map[string]any{
		"uuid": uuid4, "flow": "xtls-rprx-direct", "network": "tcp", "tls": true, "reality-opts": nil,
		"name": "VLESS h.example.com:443",
	})
	// Reality is assumed only when security is absent.
	f = parserFields(t, "vless://"+enc("auto:"+uuid4+"@h:443")+"?security=tls&tls=1&pbk=K")
	expectFields(t, "shadowrocket with security", f, map[string]any{"tls": true, "reality-opts": map[string]any{"public-key": "K"}})
	// Without serviceName, a path on gRPC is the service name.
	f = parserFields(t, "vless://"+enc("auto:"+uuid4+"@h:443")+"?obfs=grpc&path=svc")
	expectFields(t, "shadowrocket grpc", f, map[string]any{
		"network": "grpc", "grpc-opts": map[string]any{"grpc-service-name": "svc", "_grpc-type": "gun"},
	})
}

func TestVMessInTheVLESSShape(t *testing.T) {
	f := parserFields(t, "vmess://"+uuid4+"@h.example.com:443?encryption=chacha20-ietf-poly1305&flow=xtls-rprx-vision&security=tls")
	expectFields(t, "vmess vless shape", f, map[string]any{
		"type": "vmess", "cipher": "chacha20-poly1305", "alterId": 0.0, "flow": nil, "encryption": nil,
		"name": "VMess h.example.com:443", "tls": true,
	})
}
