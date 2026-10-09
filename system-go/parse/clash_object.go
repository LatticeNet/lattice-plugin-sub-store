package parse

import (
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// The Clash and mihomo object parser of parser.md section 5 (parser order
// 13). A line is an object when it parses as JSON5, or failing that as YAML,
// into a value with a truthy type; clashObjectTest has already decoded it.

// clashTypes are the 29 types the parser accepts, case-sensitive.
var clashTypes = map[string]bool{
	"ss": true, "ssr": true, "vmess": true, "vless": true, "trojan": true,
	"anytls": true, "hysteria": true, "hysteria2": true, "tuic": true,
	"wireguard": true, "socks5": true, "http": true, "snell": true,
	"ssh": true, "direct": true, "masque": true, "masque-surge": true,
	"sudoku": true, "juicity": true, "naive": true, "h2-connect": true,
	"trusttunnel": true, "easytier": true, "tailscale": true, "openvpn": true,
	"gost-relay": true, "shadowquic": true, "zerotier": true, "mieru": true,
}

// clashCopies are the keys copied, when truthy, to a second name, in order:
// fingerprint comes after server-cert-fingerprint, so it wins. The original
// keys stay.
var clashCopies = [][2]string{
	{"server-cert-fingerprint", "tls-fingerprint"},
	{"fingerprint", "tls-fingerprint"},
	{"dialer-proxy", "underlying-proxy"},
	{"benchmark-url", "test-url"},
	{"benchmark-timeout", "test-timeout"},
}

func parseClashObject(line string, st *lineState) (map[string]any, error) {
	f, ok := st.objectValue.(map[string]any)
	if !ok || st.objectLine != line {
		return nil, errReject
	}
	// The decoded value belongs to this parse from here on.
	st.objectLine, st.objectValue = "", nil
	t, _ := f["type"].(string)
	if !clashTypes[t] {
		return nil, errReject
	}
	if t == "vmess" || t == "vless" {
		// A falsy servername stays where it is (case clash-quirk-sni-empty-and-off).
		if v, ok := f["servername"]; truthy(v, ok) {
			f["sni"] = v
			delete(f, "servername")
		}
	}
	for _, c := range clashCopies {
		if v, ok := f[c[0]]; truthy(v, ok) {
			f[c[1]] = v
		}
	}
	if t == "vmess" {
		if v, ok := f["cipher"]; ok {
			f["cipher"] = vmessCipher(jsString(v, true))
		}
	}
	// H1: underscore keys from object input go before the normaliser runs.
	nodemodel.StripFromInput(&nodemodel.Node{Fields: f})
	return f, nil
}
