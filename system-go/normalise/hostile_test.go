package normalise

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// TestHostileStripExternalLine covers H3 on the node shapes the specification
// names: the node a Surge external line yields (surge/surge-hostile-external,
// run here in both modes because the oracle protocol has no opt-in field),
// exec-style keys on object input (clash/clash-hostile-exec-key), and a key a
// TUIC link copies from its query (tuic/tuic-hostile-exec-query).
func TestHostileStripExternalLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"surge external line", `{"name":"ext","type":"external","exec":"/usr/bin/ssh","args":["-D","1080"],"local-port":1080,"addresses":["192.0.2.1"]}`},
		{"type in another letter case", `{"name":"ext","type":"External","server":"a.example","port":1}`},
		{"exec key on shadowsocks", `{"name":"ss","type":"ss","server":"a.example","port":1,"cipher":"aes-128-gcm","password":"p","exec":"/bin/sh"}`},
		{"local-address on ssr", `{"name":"ssr","type":"ssr","server":"a.example","port":1,"cipher":"aes-128-cfb","password":"p","protocol":"origin","obfs":"plain","local-address":"127.0.0.1"}`},
		{"query key copied by the TUIC parser, upper case", `{"name":"t","type":"tuic","server":"a.example","port":1,"uuid":"u","password":"p","EXEC":"/bin/sh"}`},
		{"local_address", `{"name":"h","type":"http","server":"a.example","port":1,"local_address":"127.0.0.1"}`},
		{"Local-Port", `{"name":"h","type":"http","server":"a.example","port":1,"Local-Port":1080}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			drop, reason, err := Node(decodeNode(t, c.in), false)
			if err != nil || !drop || reason != ReasonExecShaped {
				t.Fatalf("remote input: Node = %v, %q, %v; want dropped as %s", drop, reason, err, ReasonExecShaped)
			}
			if strings.Contains(reason, "/") || strings.Contains(reason, "127.0.0.1") {
				t.Errorf("reason %q carries node content", reason)
			}
			drop, reason, err = Node(decodeNode(t, c.in), true)
			if err != nil || drop {
				t.Fatalf("local record with the opt-in: Node = %v, %q, %v; want kept", drop, reason, err)
			}
		})
	}

	ordinary := decodeNode(t, `{"name":"ok","type":"ss","server":"a.example","port":1,"cipher":"aes-128-gcm","password":"p","plugin-opts":{"exec":"nested keys are not top-level"}}`)
	if drop, reason, err := Node(ordinary, false); err != nil || drop {
		t.Fatalf("a nested exec key dropped the node: %v, %q, %v", drop, reason, err)
	}

	report := nodemodel.StripFromInput(decodeNode(t, cases[0].in))
	if !report.ExecShaped {
		t.Error("StripFromInput did not report the external node as exec-shaped")
	}
}

// TestHostileStripClashCA covers H1 and H2 for the ca entry
// (clash/clash-quirk-ca-fields): a _ca on object input is removed, and a _ca
// is never read as a file, even when it names a readable PEM file.
func TestHostileStripClashCA(t *testing.T) {
	pem := "-----BEGIN CERTIFICATE-----\n" + testCertBody + "\n-----END CERTIFICATE-----\n"
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, []byte(pem), 0o600); err != nil {
		t.Fatal(err)
	}
	in := `{"name":"t","type":"trojan","server":"a.example","port":443,"password":"p","sni":"s.example","_ca":` + jsonString(path) + `}`

	fromObject := decodeNode(t, in)
	report := nodemodel.StripFromInput(fromObject)
	if report.UnderscoreKeys != 1 || report.ExecShaped {
		t.Fatalf("StripFromInput = %+v, want one underscore key and not exec-shaped", report)
	}
	expectNodeNormalised(t, fromObject, `{"name":"t","type":"trojan","server":"a.example","port":443,"password":"p","sni":"s.example","network":"tcp","tls":true,"udp":true}`)

	// A _ca that reached the normaliser without H1 is still never read.
	unstripped := decodeNode(t, in)
	expectNodeNormalised(t, unstripped, `{"name":"t","type":"trojan","server":"a.example","port":443,"password":"p","sni":"s.example","network":"tcp","tls":true,"udp":true,"_ca":`+jsonString(path)+`}`)

	// ca-str still gives the fingerprint beside a _ca.
	withText := decodeNode(t, `{"name":"t","type":"trojan","server":"a.example","port":443,"password":"p","sni":"s.example","_ca":"/nonexistent","ca-str":`+jsonString(pem)+`}`)
	nodemodel.StripFromInput(withText)
	expectNodeNormalised(t, withText, `{"name":"t","type":"trojan","server":"a.example","port":443,"password":"p","sni":"s.example","ca-str":`+jsonString(pem)+`,"tls-fingerprint":"`+testCertFingerprint+`","network":"tcp","tls":true,"udp":true}`)
}

// TestHostileStripRemoteSubName: a _subName in remote input is stripped at
// every depth, while one the engine sets after parsing (the collection path
// names the member a node came from) survives normalisation, the bounds and
// the wire form. Registered annotations are never stripped by the normaliser.
func TestHostileStripRemoteSubName(t *testing.T) {
	n := decodeNode(t, `{"name":"n","type":"vless","server":"a.example","port":443,"uuid":"`+testUUID+`","tls":true,"sni":"s.example",`+
		`"_subName":"provider-set","reality-opts":{"public-key":"pk","_subName":"nested"},"peers":[{"_subName":"in a list"}]}`)
	report := nodemodel.StripFromInput(n)
	if report.UnderscoreKeys != 3 {
		t.Fatalf("stripped %d underscore keys, want 3", report.UnderscoreKeys)
	}
	encoded, err := n.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "_subName") {
		t.Fatalf("remote _subName survived H1: %s", encoded)
	}

	n.Fields["_subName"] = "member-a"
	n.Fields["reality-opts"].(map[string]any)["_spider-x"] = "/spx"
	expectNodeNormalised(t, n, `{"name":"n","type":"vless","server":"a.example","port":443,"uuid":"`+testUUID+`","tls":true,"sni":"s.example",`+
		`"_subName":"member-a","reality-opts":{"public-key":"pk","_spider-x":"/spx"},"peers":[{}],"network":"tcp","udp":true}`)
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
