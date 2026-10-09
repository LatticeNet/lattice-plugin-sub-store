package parse

import "testing"

func TestWireGuardAddresses(t *testing.T) {
	for _, c := range []struct {
		value string
		want  map[string]any
	}{
		{"10.0.0.2/24, [fd00::2]/64", map[string]any{"ip": "10.0.0.2", "ip-cidr": 24.0, "ipv6": "fd00::2", "ipv6-cidr": 64.0}},
		{"10.0.0.2/33,fd00::2/129", map[string]any{"ip": "10.0.0.2", "ip-cidr": nil, "ipv6": "fd00::2", "ipv6-cidr": nil}},
		// A later piece of the same family overwrites; others are ignored.
		{"10.0.0.2/32,10.0.0.3,01.2.3.4,h.example.com", map[string]any{"ip": "10.0.0.3", "ip-cidr": 32.0, "ipv6": nil}},
		{"::ffff:1.2.3.4/128", map[string]any{"ipv6": "::ffff:1.2.3.4", "ipv6-cidr": 128.0}},
	} {
		f := map[string]any{}
		wireGuardAddresses(f, c.value)
		expectFields(t, c.value, f, c.want)
	}
}

func TestWireGuardRowsOutsideTheCorpus(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // no port gives 51820; reserved needs exactly three integers
			"wg://K@h.example.com?reserved=1,2&mtu=x&flag=JP",
			map[string]any{"port": 51820.0, "name": "WireGuard h.example.com:51820", "reserved": nil, "mtu": nil, "flag": nil},
		},
		{
			"wireguard://K@h:1?reserved=%201%20,2x,3&mtu=1420.5#n",
			map[string]any{"reserved": []any{1.0, 2.0, 3.0}, "mtu": 1420.0},
		},
		{ // keys containing publickey or privatekey in any case; private_key
			// becomes private-key, which is already there.
			"wireguard://K@h:1?PeerPublicKey=P&MyPrivateKey=Q&private_key=R&udp=0#n",
			map[string]any{"public-key": "P", "private-key": "Q", "udp": false},
		},
		{
			"wireguard://K@h:1?private_key=R&dns=1.1.1.1#n",
			map[string]any{"private-key": "K", "dns": "1.1.1.1"},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
}

func TestAnyTLSOverlay(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // no fragment: the AnyTLS default name; tcp without Reality drops
			// network and the copied security.
			"anytls://pw@h.example.com:443?security=tls&type=tcp&tls=x",
			map[string]any{"name": "AnyTLS h.example.com:443", "network": nil, "security": nil, "uuid": nil, "tls": true},
		},
		{ // other networks keep both; every "_" in a key changes
			"anytls://pw@h:443?type=ws&security=tls&idle_session_timeout=30#n",
			map[string]any{"network": "ws", "security": "tls", "idle-session-timeout": "30"},
		},
		{ // insecure and udp by contains-true-or-1
			"anytls://pw@h:443?insecure=TRUE&udp=0#n",
			map[string]any{"skip-cert-verify": true, "udp": false},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
	for _, line := range []string{"anytls://pw@h?sni=s#n", "anytls://a%25b@h:443#n", "anytls://pw@h:443?a=%zz#n"} {
		if f := parserFields(t, line); f != nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}
