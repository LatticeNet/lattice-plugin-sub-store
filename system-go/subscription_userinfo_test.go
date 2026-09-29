package main

import (
	"encoding/json"
	"testing"
)

func usageValue(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestParseProviderUsage(t *testing.T) {
	type want struct{ upload, download, total, expire any }
	cases := []struct {
		name string
		raw  string
		want want
	}{
		{
			name: "the usual shape",
			raw:  "upload=3221225472; download=25769803776; total=536870912000; expire=1893456000",
			want: want{int64(3221225472), int64(25769803776), int64(536870912000), int64(1893456000)},
		},
		{
			name: "no spaces, keys out of order",
			raw:  "total=100;expire=1893456000;download=20;upload=10",
			want: want{int64(10), int64(20), int64(100), int64(1893456000)},
		},
		{
			name: "spaces around every separator and mixed case keys",
			raw:  "  Upload = 1 ;DOWNLOAD= 2 ;  Total =3 ; Expire=1893456000 ",
			want: want{int64(1), int64(2), int64(3), int64(1893456000)},
		},
		{
			name: "commas instead of semicolons, a trailing separator",
			raw:  "upload=1, download=2, total=3,",
			want: want{int64(1), int64(2), int64(3), nil},
		},
		{
			name: "missing fields stay missing",
			raw:  "total=107374182400",
			want: want{nil, nil, int64(107374182400), nil},
		},
		{
			name: "floats and exponent form are truncated",
			raw:  "upload=1.5; download=2.9E3; total=1.073741824e10",
			want: want{int64(1), int64(2900), int64(10737418240), nil},
		},
		{
			name: "quoted values",
			raw:  `upload="5"; total='10'`,
			want: want{int64(5), nil, int64(10), nil},
		},
		{
			name: "garbage values drop only their own field",
			raw:  "upload=abc; download=-5; total=NaN; expire=Inf; upload=7",
			want: want{int64(7), nil, nil, nil},
		},
		{
			name: "the first valid value for a key wins",
			raw:  "total=10; total=20",
			want: want{nil, nil, int64(10), nil},
		},
		{
			name: "expire of zero means never and is left out",
			raw:  "total=10; expire=0",
			want: want{nil, nil, int64(10), nil},
		},
		{
			name: "expire in milliseconds is scaled to seconds",
			raw:  "expire=1893456000000",
			want: want{nil, nil, nil, int64(1893456000)},
		},
		{
			name: "unknown keys and pairs without a value are ignored",
			raw:  "plan=pro; total; =5; upload=1",
			want: want{int64(1), nil, nil, nil},
		},
		{
			name: "a value past int64 is refused, not wrapped",
			raw:  "total=99999999999999999999; download=1e30",
			want: want{nil, nil, nil, nil},
		},
		{
			name: "empty header",
			raw:  "",
			want: want{nil, nil, nil, nil},
		},
		{
			name: "not a userinfo header at all",
			raw:  "<html>502 Bad Gateway</html>",
			want: want{nil, nil, nil, nil},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseProviderUsage(tc.raw)
			seen := want{usageValue(got.Upload), usageValue(got.Download), usageValue(got.Total), usageValue(got.Expire)}
			if seen != tc.want {
				t.Fatalf("parseProviderUsage(%q) = %+v, want %+v", tc.raw, seen, tc.want)
			}
		})
	}
}

// The list carries the parsed figures next to the verbatim header, so the
// overview can draw traffic and expiry without a `get` per row, and a record
// the provider never described carries none of them.
func TestListCarriesParsedProviderUsage(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("vless://one")
	host.header = map[string]string{"subscription-userinfo": "upload=10; download=20; total=100; expire=1893456000"}
	if err := rt.saveSubscription(subscriptionRecord{ID: "provider", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "pasted", Content: "vless://inline"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if res := fetchViaMethod(t, rt, "provider"); !res.OK {
		t.Fatalf("fetch failed: %s", res.Error)
	}

	var listed struct {
		Subscriptions []map[string]json.RawMessage `json:"subscriptions"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	byID := map[string]map[string]json.RawMessage{}
	for _, entry := range listed.Subscriptions {
		var id string
		_ = json.Unmarshal(entry["id"], &id)
		byID[id] = entry
	}
	provider := byID["provider"]
	for key, want := range map[string]string{"upload": "10", "download": "20", "total": "100", "expire": "1893456000"} {
		if got := string(provider[key]); got != want {
			t.Fatalf("list %s = %q, want %s (row %v)", key, got, want, provider)
		}
	}
	if _, ok := provider["userinfo"]; !ok {
		t.Fatalf("the verbatim header left the row: %v", provider)
	}
	for _, key := range []string{"upload", "download", "total", "expire"} {
		if _, present := byID["pasted"][key]; present {
			t.Fatalf("a record with no provider header claims %s: %v", key, byID["pasted"])
		}
	}
}

// A header that parses to nothing adds no keys: an empty object would be
// read as "the provider reported zero".
func TestListOmitsUsageThatDoesNotParse(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("vless://one")
	host.header = map[string]string{"Subscription-Userinfo": "plan=pro"}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if res := fetchViaMethod(t, rt, "s1"); !res.OK {
		t.Fatalf("fetch failed: %s", res.Error)
	}
	var listed struct {
		Subscriptions []map[string]json.RawMessage `json:"subscriptions"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	row := listed.Subscriptions[0]
	for _, key := range []string{"upload", "download", "total", "expire"} {
		if _, present := row[key]; present {
			t.Fatalf("an unparseable header produced %s: %v", key, row)
		}
	}
}
