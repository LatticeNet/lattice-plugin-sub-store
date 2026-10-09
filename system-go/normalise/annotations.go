package normalise

// Annotations is the closed registry of parser annotation keys (normaliser.md
// section 6): underscore keys the URI and line parsers create from recognised
// syntax. They are part of the parsed model, of the parse golden and of the
// JSON scripts see, and producers read some of them. Location is the options
// key that holds the annotation, or "" at the top level of the node. SetBy
// names the parsers that create it.
//
// The list is closed: a parser annotation not in it is a specification bug,
// and TestAnnotationRegistryIsClosed holds every underscore key in the
// non-object parse goldens to it. Underscore keys that arrive inside Clash or
// mihomo objects are not annotations; H1 removes them before the normaliser
// runs.
var Annotations = map[string]struct{ Location, SetBy string }{
	"_grpc-type":             {"grpc-opts", "SS, VMess, VLESS, Trojan URIs"},
	"_grpc-authority":        {"grpc-opts", "SS, VMess, VLESS, Trojan URIs"},
	"_kcp-type":              {"kcp-opts", "VMess v2rayN"},
	"_kcp-host":              {"kcp-opts", "VMess v2rayN"},
	"_kcp-path":              {"kcp-opts", "VMess v2rayN"},
	"_quic-type":             {"quic-opts", "VMess v2rayN"},
	"_quic-host":             {"quic-opts", "VMess v2rayN"},
	"_quic-path":             {"quic-opts", "VMess v2rayN"},
	"_v2ray-http-upgrade-ed": {"ws-opts", "SS, VMess, VLESS, Trojan URIs"},
	"_spider-x":              {"reality-opts", "SS, VLESS, Trojan URIs"},
	"_mode":                  {"", "SS, Trojan (Reality), VLESS (non-xhttp)"},
	"_extra":                 {"", "SS, Trojan (Reality), VLESS (non-xhttp, or invalid xhttp extra)"},
	"_extra_unsupported":     {"", "VLESS xhttp"},
	"_echConfigList":         {"", "VLESS URI"},
	"_dns":                   {"ech-opts", "VLESS, Hysteria2, xhttp download settings"},
	"_force-query":           {"ech-opts", "VLESS, Hysteria2, xhttp download settings"},
	"_sockopt":               {"ech-opts", "VLESS, Hysteria2, xhttp download settings"},
	"_vcn":                   {"", "VLESS, Trojan URIs"},
	"_h2":                    {"", "VLESS URI"},
	"_pqv":                   {"", "VLESS URI"},
	"_finalmask":             {"", "VLESS URI"},
	"_obfs":                  {"", "Hysteria URI"},
	"_qx_obfs_http":          {"", "Quantumult X"},
	"_ssr_python_uot":        {"", "Quantumult X"},
	"_loon_tls_profile":      {"", "Loon"},
}

// IsAnnotation reports whether key, held by the options object named
// location ("" for the node's top level), is a registered parser annotation.
func IsAnnotation(location, key string) bool {
	a, ok := Annotations[key]
	return ok && a.Location == location
}
