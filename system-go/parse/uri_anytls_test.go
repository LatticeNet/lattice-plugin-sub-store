package parse

import "testing"

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
		{ // a key the VLESS pass always creates is never copied, even when
			// that pass left it without a value
			"anytls://pw@h:443?tls=x&sni2=y#n",
			map[string]any{"tls": nil, "sni2": "y"},
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
