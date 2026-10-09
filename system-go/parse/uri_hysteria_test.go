package parse

import "testing"

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
