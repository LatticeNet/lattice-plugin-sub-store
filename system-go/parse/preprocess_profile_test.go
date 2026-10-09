package parse

import "testing"

func TestPreprocessProfileExtractsTheProxySection(t *testing.T) {
	for _, c := range []struct {
		name, in, want string
		kind           Kind
	}{
		{
			name: "proxy section ends at the next header",
			in:   "[General]\nx = 1\n[Proxy]\na = ss, h, 1\n[Proxy Group]\ng = select, a\n",
			want: "\na = ss, h, 1\n", kind: KindProfile,
		},
		{
			name: "a lower-case header ends it too",
			in:   "[Proxy]\na = ss, h, 1\n[proxy group]\nb = ss, h, 2\n",
			want: "\na = ss, h, 1\n", kind: KindProfile,
		},
		{
			name: "another section ending in proxy] wins when it comes first",
			in:   "[Remote Proxy]\nr = ss, h, 1\n[Proxy]\na = ss, h, 2\n[Rule]\n",
			want: "\nr = ss, h, 1\n", kind: KindProfile,
		},
		{
			name: "the last section is kept whole",
			in:   "[General]\nx = 1\n[Proxy]\na = ss, h, 1\n",
			want: "[General]\nx = 1\n[Proxy]\na = ss, h, 1\n", kind: KindProfile,
		},
		{
			name: "a Quantumult X profile is kept whole",
			in:   "[server_local]\nshadowsocks=h:1, tag=a\n[filter_remote]\nvmess=h:2, tag=b\n",
			want: "[server_local]\nshadowsocks=h:1, tag=a\n[filter_remote]\nvmess=h:2, tag=b\n", kind: KindProfile,
		},
		{
			name: "server_local before proxy] keeps the document",
			in:   "[SERVER_LOCAL]\n[Proxy]\na = ss, h, 1\n[Rule]\n",
			want: "[SERVER_LOCAL]\n[Proxy]\na = ss, h, 1\n[Rule]\n", kind: KindProfile,
		},
		{
			name: "a header with CRLF ends the section",
			in:   "[Proxy]\r\na = ss, h, 1\r\n[Rule]\r\n",
			want: "\r\na = ss, h, 1\r\n", kind: KindProfile,
		},
	} {
		out, kind := Preprocess(c.in)
		if kind != c.kind || out != c.want {
			t.Errorf("%s: got %s %q, want %s %q", c.name, kind, out, c.kind, c.want)
		}
	}
}
