package parse

import (
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// The WireGuard URI parser of parser.md section 4.13.

func parseWireGuard(rest string, _ *lineState) (map[string]any, error) {
	p := splitURI(rest, true, "0123456789")
	privateKey := "undefined" // the text of a missing user info (quirk)
	if p.hasUserInfo {
		var ok bool
		if privateKey, ok = PercentDecodeStrict(p.userInfo); !ok {
			return nil, errReject
		}
	}
	port := 51820.0
	if p.port != "" {
		port = numberValue(p.port)
	}
	f := map[string]any{
		"type":        "wireguard",
		"server":      p.host,
		"port":        port,
		"private-key": privateKey,
		"udp":         true,
	}
	if p.hasFragment {
		name, ok := PercentDecodeStrict(p.fragment)
		if !ok {
			return nil, errReject
		}
		f["name"] = name
	} else {
		f["name"] = "WireGuard " + p.host + ":" + jsNumber(port)
	}
	q, ok := splitQuery(p.query, q6)
	if !ok {
		return nil, errReject
	}
	for _, it := range q {
		v := jsString(it.Value, true)
		lower := strings.ToLower(it.Key)
		switch {
		case it.Key == "reserved":
			var reserved []any
			for _, piece := range strings.Split(v, ",") {
				if n := LeadingInteger(TrimECMAScript(piece)); n == n {
					reserved = append(reserved, n)
				}
			}
			if len(reserved) == 3 {
				f["reserved"] = reserved
			}
		case it.Key == "address" || it.Key == "ip":
			wireGuardAddresses(f, v)
		case it.Key == "mtu":
			if n := LeadingInteger(v); n == n {
				f["mtu"] = n
			}
		case strings.Contains(lower, "publickey"):
			f["public-key"] = v
		case strings.Contains(lower, "privatekey"):
			f["private-key"] = v
		case it.Key == "udp":
			f["udp"] = ContainsTrueOr1(v)
		case it.Key == "flag":
		default:
			copyUnhandled(f, it)
		}
	}
	return f, nil
}

// wireGuardAddresses reads address[/cidr] pieces, brackets optional: an IPv4
// literal sets ip (and ip-cidr for 0 to 32), an IPv6 literal sets ipv6 (and
// ipv6-cidr for 0 to 128); a later piece of the same family overwrites, and
// other pieces are ignored.
func wireGuardAddresses(f map[string]any, v string) {
	for _, piece := range strings.Split(v, ",") {
		addr, cidr, hasCIDR := strings.Cut(TrimECMAScript(piece), "/")
		if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") && len(addr) >= 2 {
			addr = addr[1 : len(addr)-1]
		}
		bits, validCIDR := 0.0, false
		if hasCIDR && digitsOnly(cidr) {
			bits, validCIDR = numberValue(cidr), true
		}
		switch {
		case normalise.IsIPv4Literal(addr):
			f["ip"] = addr
			if validCIDR && bits <= 32 {
				f["ip-cidr"] = bits
			}
		case normalise.IsIPv6Literal(addr):
			f["ipv6"] = addr
			if validCIDR && bits <= 128 {
				f["ipv6-cidr"] = bits
			}
		}
	}
}
