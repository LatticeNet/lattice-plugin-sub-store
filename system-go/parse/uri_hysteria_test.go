package parse

import (
	"math"
	"testing"
)

func TestHysteria2RowsOutsideTheCorpus(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // no port: 443 and the default name
			"hy2://pw@h.example.com",
			map[string]any{"port": 443.0, "name": "Hysteria2 h.example.com:443", "ports": nil, "skip-cert-verify": false, "tfo": false},
		},
		{ // a list takes its first entry, the lower bound of a range
			"hysteria2://pw@h:443,8443-9000/?sni=s#n",
			map[string]any{"port": 443.0, "ports": "443,8443-9000"},
		},
		{
			"hysteria2://pw@h:20000-30000,443#n",
			map[string]any{"port": 20000.0, "ports": "20000-30000,443"},
		},
		{ // mport replaces the authority list; hop_interval is the fallback;
			// a keepalive that is not digits is dropped.
			"hysteria2://pw@h:1-2?mport=5000-6000&hop_interval=10&keepalive=1s&upmbps=x#n",
			map[string]any{"ports": "5000-6000", "hop-interval": "10", "keepalive": nil, "up": "x"},
		},
		{ // a bare obfs item is the text undefined under Q1
			"hysteria2://pw@h:1?obfs&insecure=true&fastopen=TRUE#n",
			map[string]any{"obfs": "undefined", "skip-cert-verify": true, "tfo": true},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
	if f := parserFields(t, "hysteria2://pw@h:1;2#n"); f == nil || !math.IsNaN(f["port"].(float64)) {
		t.Errorf("a list with ';' gave %#v, want a not-a-number port", f)
	}
	for _, line := range []string{"hysteria2://h:443#n", "hysteria2://p%zz@h:443#n", "hysteria2://pw@h:443#%zz", "hy2://pw@h:1?sni=%zz"} {
		if f := parserFields(t, line); f != nil {
			t.Errorf("%s: accepted as %#v", line, f)
		}
	}
}

func TestHysteriaRowsOutsideTheCorpus(t *testing.T) {
	for _, c := range []struct {
		line string
		want map[string]any
	}{
		{ // an "@" stays in the server; no port gives 443
			"hysteria://u@h.example.com",
			map[string]any{"server": "u@h.example.com", "port": 443.0, "name": "Hysteria u@h.example.com:443", "protocol": "udp"},
		},
		{ // only the first "_" changes; copied keys keep the first value,
			// handled keys the last.
			"hysteria://h:1?a_b_c=1&x=1&x=2&auth=a&auth=b&protocol=faketcp#n",
			map[string]any{"a-b_c": "1", "x": "1", "auth-str": "b", "protocol": "faketcp"},
		},
		{ // a copied sni wins over peer; an empty alpn is absent; an empty obfs is
			// the empty annotation.
			"hysteria://h:1?peer=p&sni=s&alpn=&obfs=#n",
			map[string]any{"sni": "s", "alpn": nil, "_obfs": ""},
		},
		{
			"hysteria://h:1?peer=p&fast-open=1#n",
			map[string]any{"sni": "p", "fast-open": nil, "tfo": nil},
		},
	} {
		expectFields(t, c.line, parserFields(t, c.line), c.want)
	}
}

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
