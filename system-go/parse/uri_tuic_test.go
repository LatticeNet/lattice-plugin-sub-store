package parse

import "testing"

func TestTUICRowsOutsideTheCorpus(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // the password is decoded twice; no port gives 443
			"tuic://u:a%2525b@h.example.com",
			map[string]any{"uuid": "u", "password": "a%b", "port": 443.0, "name": "TUIC h.example.com:443"},
		},
		{ // every "_" changes; keys present before the loop are never copied
			"tuic://u:p@h:1?udp_relay_mode=quic&uuid=x&password=y&reduce_rtt=0&disable_sni=TRUE#n",
			map[string]any{"udp-relay-mode": "quic", "uuid": "u", "password": "p", "reduce-rtt": false, "disable-sni": true},
		},
		{
			"tuic://u:p@h:1?congestion_control=bbr&congestion-controller=x&insecure=1#n",
			map[string]any{"congestion-controller": "bbr", "skip-cert-verify": true},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
	for _, line := range []string{"tuic://u:a%25@h:1#n", "tuic://u%zz:p@h:1#n", "tuic://u:p@h:1?a=%zz#n"} {
		if f := parserFields(t, line); f != nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}
