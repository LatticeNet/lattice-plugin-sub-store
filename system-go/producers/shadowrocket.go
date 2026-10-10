package producers

import (
	"bytes"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// The Shadowrocket producer (specs/producers/shadowrocket.md): the proxies:
// document of proxies.go under a short deny list. Shadowsocks over TLS is
// kept (presteps.go), ShadowTLS is restored into shadow-tls-opts and into
// snell's obfs-opts, tfo is copied rather than moved, and WireGuard addresses
// carry their prefix.

type shadowrocketProducer struct{}

func (shadowrocketProducer) ID() string { return "shadowrocket" }

// EmptyDocument is "proxies:" and a newline, or "proxies: []" and a newline
// in the pretty form (shadowrocket.md, "Empty document").
func (shadowrocketProducer) EmptyDocument(opts Options) []byte { return emptyProxies(opts) }

func (shadowrocketProducer) Produce(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options) (Result, error) {
	return produceProxies(dst, nodes, target, opts, proxyRules{id: "shadowrocket", admits: shadowrocketAdmits, transform: shadowrocketTransform})
}

// shadowrocketDenied is the admission filter's deny list of types.
var shadowrocketDenied = map[string]bool{
	"tailscale": true, "sudoku": true, "naive": true, "openvpn": true, "gost-relay": true,
	"shadowquic": true, "zerotier": true, "masque-surge": true, "easytier": true,
}

// shadowrocketV2rayModes are the v2ray-plugin modes Shadowrocket reads.
var shadowrocketV2rayModes = map[string]bool{"websocket": true, "quic": true, "http2": true, "mkcp": true, "grpc": true}

// shadowrocketAdmits is Shadowrocket's admission filter (shadowrocket.md,
// "Admission filter"): false drops the node. A node with network xhttp is
// kept, and upstream only warns about it.
func shadowrocketAdmits(f map[string]any) bool {
	typ, _ := f["type"].(string)
	if shadowrocketDenied[typ] {
		return false
	}
	switch typ {
	case "ss":
		if f["plugin"] == "v2ray-plugin" && !shadowrocketV2rayModes[v2rayPluginMode(f)] {
			return false
		}
	case "snell":
		// Only the number 1 to 6: a missing version and the text "3" both
		// drop the node.
		v, ok := f["version"].(float64)
		if !ok || v != float64(int64(v)) || v < 1 || v > 6 {
			return false
		}
		if f["plugin"] == "shadow-tls" {
			oo, _ := obj(f, "obfs-opts")
			for _, k := range []string{"mode", "host", "path"} {
				if _, ok := oo[k]; ok {
					return false
				}
			}
		}
	}
	return true
}

// shadowrocketTransform applies Shadowrocket's field rules to one admitted
// node, the ShadowTLS restore first.
func shadowrocketTransform(p *prepared) {
	f := p.node.Fields
	typ, _ := f["type"].(string)
	stream := typ == "vmess" || typ == "vless"

	switch typ {
	case "vmess", "vless", "trojan", "anytls":
		if po, ok := shadowTLSPlugin(f); ok {
			delete(f, "plugin")
			delete(f, "plugin-opts")
			restoreShadowTLSNonNull(p.put, po, "sni", stream)
		}
	}
	if typ == "vless" && f["network"] == "xhttp" {
		xo, _ := obj(f, "xhttp-opts")
		ds, _ := obj(xo, "download-settings")
		if po, ok := shadowTLSPlugin(ds); ok {
			ds = own(own(f, "xhttp-opts"), "download-settings")
			delete(ds, "plugin")
			delete(ds, "plugin-opts")
			restoreShadowTLSNonNull(func(k string, v any) { ds[k] = v }, po, "servername", true)
		}
	}

	switch typ {
	case "vmess":
		vmessAEAD(p)
		moveKey(p, "sni", "servername")
		p.put("cipher", vmessSecurity(f))
	case "vless":
		moveKey(p, "sni", "servername")
	case "ss":
		if ssOverTLS(f) {
			if v, ok := f["sni"]; ok {
				p.put("servername", v)
			}
		}
	case "tuic":
		alpnList(f)
		copyKey(p, "tfo", "fast-open")
		tuicVersion(p)
	case "hysteria":
		copyKey(p, "auth_str", "auth-str")
		alpnList(f)
		copyKey(p, "tfo", "fast-open")
	case "hysteria2":
		alpnList(f)
		copyKey(p, "tfo", "fast-open")
	case "wireguard":
		mirrorKey(p, "keepalive", "persistent-keepalive")
		mirrorKey(p, "preshared-key", "pre-shared-key")
		prefixedAddress(f, "ip", "ip-cidr", normalise.IsIPv4Literal, 32)
		prefixedAddress(f, "ipv6", "ipv6-cidr", normalise.IsIPv6Literal, 128)
	case "snell":
		if v, ok := f["version"]; ok && jsLessThan(v, 3) {
			delete(f, "udp")
		}
		if po, ok := shadowTLSPlugin(f); ok {
			oo := map[string]any{"mode": "shadow-tls"}
			for _, k := range []string{"host", "password", "version"} {
				if v, ok := po[k]; ok {
					oo[k] = v
				}
			}
			if set(po, "alpn") {
				oo["alpn"] = po["alpn"]
			}
			delete(f, "plugin")
			delete(f, "plugin-opts")
			p.put("obfs-opts", oo)
		}
	case "anytls":
		if v, ok := f["reuse"]; ok && !truthy(v) {
			delete(f, "reuse")
			p.put("disable-reuse", true)
		}
	}
	streamTransports(p, typ)
	pluginSkipCertVerify(f)
	switch typ {
	case "trojan", "tuic", "hysteria", "hysteria2", "juicity", "anytls", "trusttunnel", "naive":
		delete(f, "tls")
	}
	renameSet(p, "tls-fingerprint", "fingerprint")
	renameSet(p, "underlying-proxy", "dialer-proxy")
	dropNonBooleanTLS(f)
	dropPipelineFields(f)
	dropAnnotations(f)
	dropGRPCAnnotations(f)
}

// restoreShadowTLSNonNull is the Shadowrocket ShadowTLS restore with po, the
// shadow-tls plugin options, writing through put: shadow-tls-opts from the
// password and version, the host into hostKey and the alpn when they are not
// null, and tls on when setTLS and the block is enabled.
func restoreShadowTLSNonNull(put func(string, any), po map[string]any, hostKey string, setTLS bool) {
	opts := map[string]any{}
	for _, k := range []string{"password", "version"} {
		if v, ok := po[k]; ok {
			opts[k] = v
		}
	}
	put("shadow-tls-opts", opts)
	if v := po["host"]; v != nil {
		put(hostKey, v)
	}
	if v := po["alpn"]; v != nil {
		put("alpn", v)
	}
	if setTLS && shadowTLSEnabled(po) {
		put("tls", true)
	}
}
