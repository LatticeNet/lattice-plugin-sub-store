package parse

import "testing"

func TestTrojanDefaultPort(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"pw@h:443", "pw@h:443"},
		{"pw@h?sni=x#n", "pw@h:443?sni=x#n"},
		{"pw@h/?sni=x", "pw@h:443/?sni=x"},
		{"pw@h:443/?sni=x", "pw@h:443/?sni=x"},
		// Without a query the fragment is part of the authority (quirk).
		{"pw@h:8443#n", "pw@h:8443#n:443"},
		{"pw@[::1]?a=1", "pw@[::1]:443?a=1"},
	} {
		if got := trojanDefaultPort(c.in); got != c.want {
			t.Errorf("trojanDefaultPort(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTrojanRowsOutsideTheCorpus(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // the default name keeps the brackets and the port as written
			"trojan://pw@[2001:db8::1]:0443?sni=s",
			map[string]any{"name": "[2001:db8::1]:0443", "server": "[2001:db8::1]", "port": 443.0},
		},
		{ // a fragment that fails to decode keeps the default name
			"trojan://pw@h:443?a=1#%zz",
			map[string]any{"name": "h:443"},
		},
		{ // values are decoded leniently, and an absent item stays absent
			"trojan://p%zz@h:443?sni=%zz&udp=x#n",
			map[string]any{"password": "p%zz", "sni": "%zz", "udp": false, "tfo": nil, "skip-cert-verify": nil},
		},
		{ // the legacy ws flag by the truthy test
			"trojan://pw@h:443?ws=true&wspath=%2Fw#n",
			map[string]any{"network": "ws", "ws-opts": map[string]any{"path": "/w"}},
		},
		{ // the host is not decoded a second time
			"trojan://pw@h:443?type=ws&host=a%2525b#n",
			map[string]any{"ws-opts": map[string]any{"headers": map[string]any{"Host": "a%25b"}}},
		},
		{ // no security value is read, so tls stays unset
			"trojan://pw@h:443?security=tls#n",
			map[string]any{"tls": nil},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
	for _, line := range []string{
		"trojan://pw@a:b:443?sni=s#n",         // a host with ":" that is no IPv6 literal
		"trojan://pw@[FE80::1%25x]:443?a=1#n", // not a literal under parser.md 1.5
		"trojan://pw@h:65536?a=1#n",           // port out of range
		"trojan://@h:443?a=1#n",               // empty password
		"trojan://pw@h:443?vcn#n",             // a bare vcn
		"trojan://pw@h:443x?a=1#n",            // junk after the port
	} {
		if f := parserFields(t, line); f != nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}
