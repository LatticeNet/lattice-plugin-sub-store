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
