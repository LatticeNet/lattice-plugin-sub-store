package normalise

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

const testUUID = "5840f636-1f16-4327-8520-2c6d190b4fb9"

// The body of the certificate in the corpus case clash/clash-quirk-ca-fields
// and the fingerprint its parse golden carries.
const (
	testCertBody        = "l9ZYEiYe1wv6hn8k5WKxty2+kbdvqLUqICQL+xHPaPbXI4ewI1LdVultsGlFWUU1"
	testCertFingerprint = "82:80:74:3B:0D:B9:A0:48:92:C9:08:C2:3B:CB:07:FF:43:8B:5E:8B:E7:D8:9F:86:B8:91:5E:21:1C:82:D1:64"
)

func decodeNode(t *testing.T, in string) *nodemodel.Node {
	t.Helper()
	n := &nodemodel.Node{}
	if err := n.UnmarshalJSON([]byte(in)); err != nil {
		t.Fatalf("decode %s: %v", in, err)
	}
	return n
}

// sameJSON compares two JSON texts structurally.
func sameJSON(t *testing.T, got, want []byte) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("decode got %s: %v", got, err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("decode want %s: %v", want, err)
	}
	return reflect.DeepEqual(g, w)
}

// expectNormalised runs the whole node stage over in and compares the result
// with want.
func expectNormalised(t *testing.T, in, want string) {
	t.Helper()
	n := decodeNode(t, in)
	expectNodeNormalised(t, n, want)
}

func expectNodeNormalised(t *testing.T, n *nodemodel.Node, want string) {
	t.Helper()
	drop, reason, err := Node(n, false)
	if err != nil || drop {
		t.Fatalf("Node: drop=%v reason=%q err=%v", drop, reason, err)
	}
	got, err := n.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !sameJSON(t, got, []byte(want)) {
		t.Errorf("normalised\n got  %s\n want %s", got, want)
	}
}

type ruleCase struct {
	name string
	in   string
	want string
}

// ruleCases holds, for each node rule of normaliser.md section 2, inputs that
// exercise it and the full normalised node, so each case also pins how the
// rule meets the rules around it.
var ruleCases = map[string][]ruleCase{
	"N1": {
		{"lower-case key wins and inner keys are lower-cased",
			`{"name":"n","type":"socks5","server":"a.example","port":1080,"WS-OPTS":{"Path":"/up"},"ws-opts":{"Path":"/low","Headers":{"X-Up":"1"}},"Grpc-Opts":{"Grpc-Service-Name":"svc"},"Ws-Path":"kept"}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1080,"udp":true,"ws-opts":{"path":"/low","headers":{"X-Up":"1"}},"grpc-opts":{"grpc-service-name":"svc"},"Ws-Path":"kept"}`},
		{"a list value is moved but not entered",
			`{"name":"n","type":"socks5","server":"a.example","port":1080,"H2-OPTS":[{"Host":"x"}]}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1080,"udp":true,"h2-opts":[{"Host":"x"}]}`},
	},
	"N2": {
		{"Loon profile with ML-KEM marks Reality",
			`{"name":"n","type":"vless","server":"a.example","port":443,"uuid":"` + testUUID + `","reality-opts":{"public-key":"pk"},"_loon_tls_profile":" chrome147 "}`,
			`{"name":"n","type":"vless","server":"a.example","port":443,"uuid":"` + testUUID + `","reality-opts":{"public-key":"pk","support-x25519mlkem768":true},"_loon_tls_profile":" chrome147 ","network":"tcp","udp":true}`},
		{"other profiles do not",
			`{"name":"n","type":"vless","server":"a.example","port":443,"uuid":"` + testUUID + `","reality-opts":{"public-key":"pk"},"_loon_tls_profile":"chrome"}`,
			`{"name":"n","type":"vless","server":"a.example","port":443,"uuid":"` + testUUID + `","reality-opts":{"public-key":"pk"},"_loon_tls_profile":"chrome","network":"tcp","udp":true}`},
		{"an existing key is left alone",
			`{"name":"n","type":"vless","server":"a.example","port":443,"uuid":"` + testUUID + `","reality-opts":{"public-key":"pk","support-x25519mlkem768":false},"_loon_tls_profile":"safari-ios-26"}`,
			`{"name":"n","type":"vless","server":"a.example","port":443,"uuid":"` + testUUID + `","reality-opts":{"public-key":"pk","support-x25519mlkem768":false},"_loon_tls_profile":"safari-ios-26","network":"tcp","udp":true}`},
	},
	"N3": {
		{"FALSE", `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":"FALSE"}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":false}`},
		{"zero", `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":0}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":false}`},
		{"off", `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":"Off"}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":false}`},
		{"null", `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":null}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"absent", `{"name":"n","type":"socks5","server":"a.example","port":1}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"yes", `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":"yes"}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"untrimmed false", `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":" false"}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":true}`},
	},
	"N4": {
		{"cipher lower-cased",
			`{"name":"n","type":"ss","server":"a.example","port":1,"cipher":"AES-128-GCM","password":"p"}`,
			`{"name":"n","type":"ss","server":"a.example","port":1,"cipher":"aes-128-gcm","password":"p","udp":true}`},
	},
	"N5": {
		{"double rounding of a twenty-digit number",
			`{"name":"n","type":"ss","server":"a.example","port":1,"cipher":"aes-128-gcm","password":12345678901234567890}`,
			`{"name":"n","type":"ss","server":"a.example","port":1,"cipher":"aes-128-gcm","password":"12345678901234567168","udp":true}`},
		{"safe integer",
			`{"name":"n","type":"ss","server":"a.example","port":1,"cipher":"aes-128-gcm","password":485528}`,
			`{"name":"n","type":"ss","server":"a.example","port":1,"cipher":"aes-128-gcm","password":"485528","udp":true}`},
	},
	"N6": {
		{"digits", `{"name":"n","type":"socks5","server":"a.example","port":1,"hop-interval":"30","hop-interval-max":99}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"hop-interval":30,"udp":true}`},
		{"range", `{"name":"n","type":"socks5","server":"a.example","port":1,"hop-interval":" 15 - 30 "}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"hop-interval":15,"hop-interval-max":30,"udp":true}`},
		{"descending range", `{"name":"n","type":"socks5","server":"a.example","port":1,"hop-interval":"30-10","hop-interval-max":99}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"text", `{"name":"n","type":"socks5","server":"a.example","port":1,"hop-interval":"abc"}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"zero", `{"name":"n","type":"socks5","server":"a.example","port":1,"hop-interval":0}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"max alone", `{"name":"n","type":"socks5","server":"a.example","port":1,"hop-interval-max":99}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"hop-interval-max":99,"udp":true}`},
	},
	"N7": {
		{"none after N4 lower-cases it",
			`{"name":"n","type":"ss","server":"a.example","port":1,"cipher":"NONE"}`,
			`{"name":"n","type":"ss","server":"a.example","port":1,"cipher":"none","password":"","udp":true}`},
	},
	"N8": {
		{"interface replaces interface-name",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"interface":"eth0","interface-name":"old"}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"interface-name":"eth0","udp":true}`},
	},
	"N9": {
		{"leading zero", `{"name":"n","type":"socks5","server":"a.example","port":"0443"}`, `{"name":"n","type":"socks5","server":"a.example","port":443,"udp":true}`},
		{"out of range text", `{"name":"n","type":"socks5","server":"a.example","port":"65536"}`, `{"name":"n","type":"socks5","server":"a.example","port":"65536","udp":true}`},
		{"empty is not-a-number", `{"name":"n","type":"socks5","server":"a.example","port":""}`, `{"name":"n","type":"socks5","server":"a.example","port":null,"udp":true}`},
		{"letters", `{"name":"n","type":"socks5","server":"a.example","port":"abc"}`, `{"name":"n","type":"socks5","server":"a.example","port":"abc","udp":true}`},
		{"out of range number", `{"name":"n","type":"socks5","server":"a.example","port":70000}`, `{"name":"n","type":"socks5","server":"a.example","port":70000,"udp":true}`},
		{"five digits", `{"name":"n","type":"socks5","server":"a.example","port":"65535"}`, `{"name":"n","type":"socks5","server":"a.example","port":65535,"udp":true}`},
	},
	"N10": {
		{"trimmed, brackets removed",
			`{"name":"n","type":"socks5","server":" [2001:db8::1] ","port":1}`,
			`{"name":"n","type":"socks5","server":"2001:db8::1","port":1,"udp":true}`},
		{"number becomes text",
			`{"name":"n","type":"socks5","server":123,"port":1}`,
			`{"name":"n","type":"socks5","server":"123","port":1,"udp":true}`},
	},
	"N11": {
		{"VMess shadow-tls-opts become the plugin, with N13's ALPN move",
			`{"name":"n","type":"vmess","server":"a.example","port":443,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"sni":"s.example","alpn":["h2"],"shadow-tls-opts":{"password":"stp","version":3,"host":"ignored"}}`,
			`{"name":"n","type":"vmess","server":"a.example","port":443,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"sni":"s.example","network":"tcp","udp":true,"plugin":"shadow-tls","plugin-opts":{"host":"s.example","password":"stp","version":3,"alpn":["h2"]}}`},
		{"Shadowsocks keeps shadow-tls-opts",
			`{"name":"n","type":"ss","server":"a.example","port":443,"cipher":"aes-128-gcm","password":"p","shadow-tls-opts":{"host":"h","password":"stp","version":3}}`,
			`{"name":"n","type":"ss","server":"a.example","port":443,"cipher":"aes-128-gcm","password":"p","shadow-tls-opts":{"host":"h","password":"stp","version":3},"udp":true}`},
	},
	"N12": {
		{"Snell obfs shadow-tls becomes the plugin",
			`{"name":"n","type":"snell","server":"a.example","port":1,"psk":"k","obfs-opts":{"mode":"shadow-tls","host":"h.example","password":"stp","version":2,"alpn":"h2"}}`,
			`{"name":"n","type":"snell","server":"a.example","port":1,"psk":"k","udp":true,"plugin":"shadow-tls","plugin-opts":{"host":"h.example","password":"stp","version":2,"alpn":"h2"}}`},
		{"a truthy plugin blocks it",
			`{"name":"n","type":"snell","server":"a.example","port":1,"plugin":"obfs","obfs-opts":{"mode":"shadow-tls"}}`,
			`{"name":"n","type":"snell","server":"a.example","port":1,"plugin":"obfs","obfs-opts":{"mode":"shadow-tls"},"udp":true}`},
	},
	"N13": {
		{"alpn moves into plugin-opts",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"plugin":"shadow-tls","plugin-opts":{"host":"h"},"alpn":"h2"}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"plugin":"shadow-tls","plugin-opts":{"host":"h","alpn":"h2"},"udp":true}`},
		{"existing plugin alpn wins, top-level removed anyway",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"plugin":"shadow-tls","plugin-opts":{"alpn":"h3"},"alpn":"h2"}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"plugin":"shadow-tls","plugin-opts":{"alpn":"h3"},"udp":true}`},
	},
	"N14": {
		{"download settings Shadow-TLS",
			`{"name":"n","type":"vless","server":"a.example","port":443,"uuid":"` + testUUID + `","network":"xhttp","xhttp-opts":{"path":"/x","download-settings":{"server":"d.example","servername":"dn.example","alpn":["h2"],"shadow-tls-opts":{"password":"stp","version":3}}}}`,
			`{"name":"n","type":"vless","server":"a.example","port":443,"uuid":"` + testUUID + `","network":"xhttp","udp":true,"xhttp-opts":{"path":"/x","download-settings":{"server":"d.example","servername":"dn.example","plugin":"shadow-tls","plugin-opts":{"host":"dn.example","password":"stp","version":3,"alpn":["h2"]}}}}`},
	},
	"N15": {
		{"legacy keys build ws-opts and N24 then reads the Host",
			`{"name":"n","type":"vmess","server":"a.example","port":443,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"tls":true,"network":"ws","ws-path":"/p","ws-headers":{"Host":"h.example"}}`,
			`{"name":"n","type":"vmess","server":"a.example","port":443,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"tls":true,"network":"ws","udp":true,"sni":"h.example","ws-opts":{"path":"/p","headers":{"Host":"h.example"}}}`},
		{"present ws-opts is kept and legacy keys go",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"sni":"s","network":"ws","ws-opts":{"path":"/w"},"ws-path":"/p"}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"sni":"s","network":"ws","ws-opts":{"path":"/w"},"udp":true}`},
	},
	"N16": {
		{"text trimmed with a slash", `{"name":"n","type":"socks5","server":"a.example","port":1,"network":"grpc","grpc-opts":{"path":" p "}}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"network":"grpc","grpc-opts":{"path":"/p"},"udp":true}`},
		{"number", `{"name":"n","type":"socks5","server":"a.example","port":1,"network":"grpc","grpc-opts":{"path":123}}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"network":"grpc","grpc-opts":{"path":"/123"},"udp":true}`},
		{"empty", `{"name":"n","type":"socks5","server":"a.example","port":1,"network":"grpc","grpc-opts":{"path":""}}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"network":"grpc","grpc-opts":{"path":"/"},"udp":true}`},
		{"list", `{"name":"n","type":"socks5","server":"a.example","port":1,"network":"grpc","grpc-opts":{"path":["a","",5,true,null]}}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"network":"grpc","grpc-opts":{"path":["/a","/","/5",true,null]},"udp":true}`},
		{"object left alone", `{"name":"n","type":"socks5","server":"a.example","port":1,"network":"grpc","grpc-opts":{"path":{"x":1}}}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"network":"grpc","grpc-opts":{"path":{"x":1}},"udp":true}`},
	},
	"N17": {
		{"VMess defaults",
			`{"name":"n","type":"vmess","server":"a.example","port":1,"uuid":"` + testUUID + `","alterId":""}`,
			`{"name":"n","type":"vmess","server":"a.example","port":1,"uuid":"` + testUUID + `","alterId":0,"cipher":"none","network":"tcp","udp":true}`},
		{"Trojan network",
			`{"name":"n","type":"trojan","server":"a.example","port":1,"password":"p","sni":"s"}`,
			`{"name":"n","type":"trojan","server":"a.example","port":1,"password":"p","sni":"s","network":"tcp","tls":true,"udp":true}`},
	},
	"N18": {
		{"xudp wins", `{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","xudp":true,"packet-addr":true}`, `{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","xudp":true,"packet-addr":true,"packet-encoding":"xudp","network":"tcp","udp":true}`},
		{"packet-addr", `{"name":"n","type":"vmess","server":"a.example","port":1,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"packet-addr":true}`, `{"name":"n","type":"vmess","server":"a.example","port":1,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"packet-addr":true,"packet-encoding":"packetaddr","network":"tcp","udp":true}`},
		{"explicit kept", `{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","xudp":true,"packet-encoding":"none"}`, `{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","xudp":true,"packet-encoding":"none","network":"tcp","udp":true}`},
		{"null replaced", `{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","xudp":1,"packet-encoding":null}`, `{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","xudp":1,"packet-encoding":"xudp","network":"tcp","udp":true}`},
	},
	"N19": {
		{"Juicity forced TLS", `{"name":"n","type":"juicity","server":"192.0.2.1","port":1}`, `{"name":"n","type":"juicity","server":"192.0.2.1","port":1,"tls":true,"udp":true}`},
		{"masque-surge is not forced", `{"name":"n","type":"masque-surge","server":"192.0.2.1","port":1}`, `{"name":"n","type":"masque-surge","server":"192.0.2.1","port":1,"udp":true}`},
	},
	"N20": {
		{"host becomes Host",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"sni":"s","network":"ws","ws-opts":{"path":"/","headers":{"host":"h.example"}}}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"sni":"s","network":"ws","ws-opts":{"path":"/","headers":{"Host":"h.example"}},"udp":true}`},
		{"a truthy Host keeps both",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"sni":"s","network":"ws","ws-opts":{"path":"/","headers":{"host":"x","Host":"y"}}}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"sni":"s","network":"ws","ws-opts":{"path":"/","headers":{"host":"x","Host":"y"}},"udp":true}`},
	},
	"N21": {
		{"headers host moves, list path takes its first element, N24 reads the host",
			`{"name":"n","type":"vmess","server":"a.example","port":443,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"tls":true,"network":"h2","h2-opts":{"headers":{"host":"h.example","X-A":"1"},"path":["/a","/b"]}}`,
			`{"name":"n","type":"vmess","server":"a.example","port":443,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"tls":true,"network":"h2","udp":true,"sni":"h.example","h2-opts":{"host":["h.example"],"headers":{"X-A":"1"},"path":"/a"}}`},
		{"h2-opts.host wins and an emptied headers is dropped",
			`{"name":"n","type":"vmess","server":"a.example","port":443,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"tls":true,"sni":"s","network":"h2","h2-opts":{"host":"x.example","headers":{"Host":"y.example"},"path":"/"}}`,
			`{"name":"n","type":"vmess","server":"a.example","port":443,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"tls":true,"sni":"s","network":"h2","udp":true,"h2-opts":{"host":["x.example"],"path":"/"}}`},
	},
	"N22": {
		{"plain WebSocket pins the server as Host",
			`{"name":"n","type":"vless","server":"a.example","port":80,"uuid":"` + testUUID + `","network":"ws"}`,
			`{"name":"n","type":"vless","server":"a.example","port":80,"uuid":"` + testUUID + `","network":"ws","udp":true,"ws-opts":{"headers":{"Host":"a.example"},"path":"/"}}`},
		{"VMess on HTTP pins a list",
			`{"name":"n","type":"vmess","server":"a.example","port":80,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"network":"http"}`,
			`{"name":"n","type":"vmess","server":"a.example","port":80,"uuid":"` + testUUID + `","cipher":"auto","alterId":0,"network":"http","udp":true,"http-opts":{"headers":{"Host":["a.example"]},"path":["/"]}}`},
		{"mapped full form is not an IP literal, so it is pinned",
			`{"name":"n","type":"vless","server":"[0:0:0:0:0:ffff:192.0.2.1]","port":80,"uuid":"` + testUUID + `","network":"ws","ws-opts":{"path":"/w"}}`,
			`{"name":"n","type":"vless","server":"0:0:0:0:0:ffff:192.0.2.1","port":80,"uuid":"` + testUUID + `","network":"ws","udp":true,"ws-opts":{"headers":{"Host":"0:0:0:0:0:ffff:192.0.2.1"},"path":"/w"}}`},
		{"compressed mapped form is an IP literal, so it is not",
			`{"name":"n","type":"vless","server":"[::ffff:192.0.2.1]","port":80,"uuid":"` + testUUID + `","network":"ws","ws-opts":{"path":"/w"}}`,
			`{"name":"n","type":"vless","server":"::ffff:192.0.2.1","port":80,"uuid":"` + testUUID + `","network":"ws","udp":true,"ws-opts":{"path":"/w"}}`},
	},
	"N23": {
		{"Host and path become lists",
			`{"name":"n","type":"vless","server":"a.example","port":80,"uuid":"` + testUUID + `","network":"http","http-opts":{"headers":{"Host":"h.example"},"path":"/p"}}`,
			`{"name":"n","type":"vless","server":"a.example","port":80,"uuid":"` + testUUID + `","network":"http","udp":true,"http-opts":{"headers":{"Host":["h.example"]},"path":["/p"]}}`},
	},
	"N24": {
		{"from the transport host list",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"network":"ws","ws-opts":{"path":"/","headers":{"Host":["h.example","i.example"]}}}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"network":"ws","ws-opts":{"path":"/","headers":{"Host":["h.example","i.example"]}},"sni":"h.example","udp":true}`},
		{"from the server", `{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"sni":"a.example","udp":true}`},
		{"upper-case mapped form is not an IP literal", `{"name":"n","type":"socks5","server":"[::FFFF:192.0.2.1]","port":1,"tls":true}`, `{"name":"n","type":"socks5","server":"::FFFF:192.0.2.1","port":1,"tls":true,"sni":"::FFFF:192.0.2.1","udp":true}`},
		{"upper-case zoned form is not an IP literal", `{"name":"n","type":"socks5","server":"[FE80::1%25eth0]","port":1,"tls":true}`, `{"name":"n","type":"socks5","server":"FE80::1%25eth0","port":1,"tls":true,"sni":"FE80::1%25eth0","udp":true}`},
		{"compressed mapped form is", `{"name":"n","type":"socks5","server":"[::ffff:192.0.2.1]","port":1,"tls":true}`, `{"name":"n","type":"socks5","server":"::ffff:192.0.2.1","port":1,"tls":true,"udp":true}`},
		{"empty sni stays", `{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"sni":""}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"tls":true,"sni":"","disable-sni":true,"udp":true}`},
	},
	"N25": {
		{"slash becomes comma", `{"name":"n","type":"socks5","server":"a.example","port":1,"ports":"1000-2000/3000"}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"ports":"1000-2000,3000","udp":true}`},
		{"empty removed", `{"name":"n","type":"socks5","server":"a.example","port":1,"ports":""}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"number becomes text", `{"name":"n","type":"socks5","server":"a.example","port":1,"ports":443}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"ports":"443","udp":true}`},
	},
	"N26": {
		{"obfs word is the salamander password",
			`{"name":"n","type":"hysteria2","server":"192.0.2.1","port":1,"password":"p","obfs":"word"}`,
			`{"name":"n","type":"hysteria2","server":"192.0.2.1","port":1,"password":"p","obfs":"salamander","obfs-password":"word","tls":true,"udp":true}`},
	},
	"N27": {
		{"obfs_password alias",
			`{"name":"n","type":"hysteria2","server":"192.0.2.1","port":1,"password":"p","obfs":"salamander","obfs_password":"pw"}`,
			`{"name":"n","type":"hysteria2","server":"192.0.2.1","port":1,"password":"p","obfs":"salamander","obfs-password":"pw","tls":true,"udp":true}`},
		{"N26 runs first, so the alias stays next to a non-salamander obfs",
			`{"name":"n","type":"hysteria2","server":"192.0.2.1","port":1,"password":"p","obfs":"word","obfs_password":"pw"}`,
			`{"name":"n","type":"hysteria2","server":"192.0.2.1","port":1,"password":"p","obfs":"salamander","obfs-password":"word","obfs_password":"pw","tls":true,"udp":true}`},
	},
	"N28": {
		{"null flow and empty option objects",
			`{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","flow":"null","reality-opts":{},"grpc-opts":{}}`,
			`{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","network":"tcp","udp":true}`},
		{"empty flow kept with Reality",
			`{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","flow":"","reality-opts":{"public-key":"pk"}}`,
			`{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","flow":"","reality-opts":{"public-key":"pk"},"network":"tcp","udp":true}`},
		{"empty flow removed without Reality",
			`{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","flow":""}`,
			`{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","network":"tcp","udp":true}`},
		{"HTTP without path",
			`{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","tls":true,"sni":"s","network":"http","http-opts":{"headers":{"Host":["h"]}}}`,
			`{"name":"n","type":"vless","server":"a.example","port":1,"uuid":"` + testUUID + `","tls":true,"sni":"s","network":"http","http-opts":{"headers":{"Host":["h"]},"path":["/"]},"udp":true}`},
	},
	"N29": {
		{"Buffer object", `{"name":{"type":"Buffer","data":[104,105]},"type":"socks5","server":"a.example","port":1}`, `{"name":"hi","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"byte list", `{"name":[104,105,256,-191,"x"],"type":"socks5","server":"a.example","port":1}`, `{"name":"hi\u0000A\u0000","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"ill-formed bytes", `{"name":[255,224,128,65],"type":"socks5","server":"a.example","port":1}`, `{"name":"���A","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"true", `{"name":true,"type":"socks5","server":"a.example","port":1}`, `{"name":"socks5 a.example:1","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"fraction", `{"name":1.5,"type":"socks5","server":"a.example","port":1}`, `{"name":"socks5 a.example:1","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"number", `{"name":7,"type":"socks5","server":"a.example","port":1}`, `{"name":"7","type":"socks5","server":"a.example","port":1,"udp":true}`},
		{"absent with absent port", `{"type":"direct","server":"a.example"}`, `{"name":"direct a.example:undefined","type":"direct","server":"a.example","udp":true}`},
	},
	"N30": {
		{"text path on HTTP for Trojan is replaced (quirk)",
			`{"name":"n","type":"trojan","server":"a.example","port":1,"password":"p","sni":"s","network":"http","http-opts":{"path":"/p"}}`,
			`{"name":"n","type":"trojan","server":"a.example","port":1,"password":"p","sni":"s","network":"http","http-opts":{"path":["/"]},"tls":true,"udp":true}`},
		{"masque keeps a missing ws path",
			`{"name":"n","type":"masque","server":"192.0.2.1","port":1,"network":"ws"}`,
			`{"name":"n","type":"masque","server":"192.0.2.1","port":1,"network":"ws","tls":true,"udp":true}`},
		{"WebSocket path created",
			`{"name":"n","type":"trojan","server":"a.example","port":1,"password":"p","sni":"s","network":"ws"}`,
			`{"name":"n","type":"trojan","server":"a.example","port":1,"password":"p","sni":"s","network":"ws","ws-opts":{"path":"/"},"tls":true,"udp":true}`},
		{"HTTP list with no truthy element",
			`{"name":"n","type":"trojan","server":"a.example","port":1,"password":"p","sni":"s","network":"http","http-opts":{"path":[null,false]}}`,
			`{"name":"n","type":"trojan","server":"a.example","port":1,"password":"p","sni":"s","network":"http","http-opts":{"path":["/"]},"tls":true,"udp":true}`},
	},
	"N31": {
		{"disable-reuse writes reuse false",
			`{"name":"n","type":"anytls","server":"192.0.2.1","port":1,"password":"p","disable-reuse":true}`,
			`{"name":"n","type":"anytls","server":"192.0.2.1","port":1,"password":"p","disable-reuse":true,"reuse":false,"tls":true,"udp":true}`},
	},
	"N32": {
		{"off", `{"name":"n","type":"socks5","server":"a.example","port":1,"sni":"off"}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"sni":"off","disable-sni":true,"udp":true}`},
		{"empty", `{"name":"n","type":"socks5","server":"a.example","port":1,"sni":""}`, `{"name":"n","type":"socks5","server":"a.example","port":1,"sni":"","disable-sni":true,"udp":true}`},
	},
	"N33": {
		{"single certificate",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"ca-str":"-----BEGIN CERTIFICATE-----\n` + testCertBody + `\n-----END CERTIFICATE-----"}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"ca-str":"-----BEGIN CERTIFICATE-----\n` + testCertBody + `\n-----END CERTIFICATE-----","tls-fingerprint":"` + testCertFingerprint + `","udp":true}`},
		{"chain keeps the last certificate",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"ca-str":"-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\n` + testCertBody + `\n-----END CERTIFICATE-----\n"}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"ca-str":"-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\n` + testCertBody + `\n-----END CERTIFICATE-----\n","tls-fingerprint":"` + testCertFingerprint + `","udp":true}`},
		{"ca_str moves and a given fingerprint wins",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"ca_str":"not pem at all","tls-fingerprint":"AA:BB"}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"ca-str":"not pem at all","tls-fingerprint":"AA:BB","udp":true}`},
		{"ca_str stays next to a truthy ca-str",
			`{"name":"n","type":"socks5","server":"a.example","port":1,"ca-str":"-----BEGIN CERTIFICATE-----\n` + testCertBody + `\n-----END CERTIFICATE-----","ca_str":"other"}`,
			`{"name":"n","type":"socks5","server":"a.example","port":1,"ca-str":"-----BEGIN CERTIFICATE-----\n` + testCertBody + `\n-----END CERTIFICATE-----","ca_str":"other","tls-fingerprint":"` + testCertFingerprint + `","udp":true}`},
	},
	"N34": {
		{"empty alpn", `{"name":"n","type":"tuic","server":"192.0.2.1","port":1,"uuid":"u","password":"p","alpn":""}`, `{"name":"n","type":"tuic","server":"192.0.2.1","port":1,"uuid":"u","password":"p","alpn":["h3"],"congestion-controller":"cubic","udp-relay-mode":"native","tls":true,"udp":true}`},
		{"text alpn", `{"name":"n","type":"tuic","server":"192.0.2.1","port":1,"uuid":"u","password":"p","alpn":"h3","congestion-controller":"bbr"}`, `{"name":"n","type":"tuic","server":"192.0.2.1","port":1,"uuid":"u","password":"p","alpn":["h3"],"congestion-controller":"bbr","udp-relay-mode":"native","tls":true,"udp":true}`},
		{"list kept", `{"name":"n","type":"tuic","server":"192.0.2.1","port":1,"uuid":"u","password":"p","alpn":["h3","h2"],"udp-relay-mode":"quic"}`, `{"name":"n","type":"tuic","server":"192.0.2.1","port":1,"uuid":"u","password":"p","alpn":["h3","h2"],"congestion-controller":"cubic","udp-relay-mode":"quic","tls":true,"udp":true}`},
	},
	"N35": {
		{"first peer read even without addresses (quirk)",
			`{"name":"n","type":"wireguard","server":"a.example","port":1,"private-key":"k","ip":"","ip-cidr":24,"peers":[{"server":"p.example"},{"ip":"10.0.0.2","ipv6":"fd00::2"}]}`,
			`{"name":"n","type":"wireguard","server":"a.example","port":1,"private-key":"k","peers":[{"server":"p.example"},{"ip":"10.0.0.2","ipv6":"fd00::2"}],"udp":true}`},
		{"suffix used when the CIDR key is out of range",
			`{"name":"n","type":"wireguard","server":"a.example","port":1,"private-key":"k","ip":" 10.0.0.2/24 ","ip-cidr":40,"ipv6":"[fd00::1]/64"}`,
			`{"name":"n","type":"wireguard","server":"a.example","port":1,"private-key":"k","ip":"10.0.0.2","ip-cidr":24,"ipv6":"fd00::1","ipv6-cidr":64,"udp":true}`},
		{"digits CIDR wins, default otherwise",
			`{"name":"n","type":"wireguard","server":"a.example","port":1,"private-key":"k","ip":"10.0.0.2/24","ip-cidr":"16","ipv6":"fd00::1/200"}`,
			`{"name":"n","type":"wireguard","server":"a.example","port":1,"private-key":"k","ip":"10.0.0.2","ip-cidr":16,"ipv6":"fd00::1","ipv6-cidr":128,"udp":true}`},
		{"not an address, or empty",
			`{"name":"n","type":"wireguard","server":"a.example","port":1,"private-key":"k","ip":"not-ip","ip-cidr":24,"ipv6":" ","ipv6-cidr":64}`,
			`{"name":"n","type":"wireguard","server":"a.example","port":1,"private-key":"k","ip":"not-ip","ip-cidr":24,"ipv6":" ","udp":true}`},
	},
}

// TestNormaliseRulesInOrder holds the rule table to the specification's order
// and runs one subtest per rule.
func TestNormaliseRulesInOrder(t *testing.T) {
	if len(nodeRules) != 35 {
		t.Fatalf("%d node rules, want 35", len(nodeRules))
	}
	for i, r := range nodeRules {
		if want := fmt.Sprintf("N%d", i+1); r.id != want {
			t.Fatalf("rule %d is %s, want %s", i, r.id, want)
		}
	}
	for _, r := range nodeRules {
		cases := ruleCases[r.id]
		t.Run(r.id, func(t *testing.T) {
			if len(cases) == 0 {
				t.Fatalf("no case exercises %s", r.id)
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) { expectNormalised(t, c.in, c.want) })
			}
		})
	}
	if len(ruleCases) != len(nodeRules) {
		t.Errorf("%d rule case groups for %d rules", len(ruleCases), len(nodeRules))
	}
}

// Values JSON cannot carry, which the model can.
func TestNormaliseNotANumberValues(t *testing.T) {
	n := &nodemodel.Node{Fields: map[string]any{
		"name": "n", "type": "vmess", "server": "a.example", "port": float64(1), "uuid": testUUID,
		"cipher": "auto", "alterId": math.NaN(), "udp": math.NaN(),
	}}
	// N17 treats not-a-number as not truthy; N3 treats it as true because it
	// is not 0.
	expectNodeNormalised(t, n, `{"name":"n","type":"vmess","server":"a.example","port":1,"uuid":"`+testUUID+`","cipher":"auto","alterId":0,"network":"tcp","udp":true}`)
}

func TestNormaliseCertificateNotPEMFailsTheDocument(t *testing.T) {
	n := decodeNode(t, `{"name":"n","type":"socks5","server":"a.example","port":1,"ca-str":"MIIB"}`)
	drop, reason, err := Node(n, false)
	if !errors.Is(err, ErrCertificateNotPEM) || !drop || reason != "N33" {
		t.Fatalf("Node = %v, %q, %v; want a dropped node and ErrCertificateNotPEM from N33", drop, reason, err)
	}
}

// TestDocumentRules covers D1 and D2.
func TestDocumentRules(t *testing.T) {
	in := []string{
		`{"name":"a","type":"hysteria2","server":"a.example","port":1,"obfs":"salamander"}`,
		`{"name":"b","type":"hysteria2","server":"a.example","port":1,"obfs":"salamander","obfs-password":"pw"}`,
		`{"name":"c","type":"vless","server":"a.example","port":1,"uuid":"RHOvcranpksM"}`,
		`{"name":"d","type":"vmess","server":"a.example","port":1,"uuid":"` + strings.ToUpper(testUUID) + `"}`,
		`{"name":"e","type":"vmess","server":"a.example","port":1,"uuid":12}`,
	}
	nodes := make([]*nodemodel.Node, len(in))
	for i, s := range in {
		nodes[i] = decodeNode(t, s)
	}
	out, notes := DocumentNotes(nodes)
	var names []string
	for _, n := range out {
		names = append(names, n.Name())
	}
	if got := strings.Join(names, ","); got != "b,c,d,e" {
		t.Errorf("kept %s, want b,c,d,e (D1 removes a, D2 keeps c and e)", got)
	}
	want := []Note{{Index: 0, Rule: "D1"}, {Index: 2, Rule: "D2"}, {Index: 4, Rule: "D2"}}
	if len(notes) != len(want) {
		t.Fatalf("notes %+v, want %+v", notes, want)
	}
	for i, n := range notes {
		if n.Index != want[i].Index || n.Rule != want[i].Rule {
			t.Errorf("note %d = %+v, want index %d rule %s", i, n, want[i].Index, want[i].Rule)
		}
		if strings.Contains(n.Message, "RHOvcranpksM") || strings.Contains(n.Message, "salamander ") {
			t.Errorf("note %d carries node content: %q", i, n.Message)
		}
	}
	if len(Document(nodes)) != 4 {
		t.Error("Document disagrees with DocumentNotes")
	}
}
