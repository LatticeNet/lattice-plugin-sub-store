package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The cases live in testdata so the UI's fallback parser (rowStatus.ts, for a
// runtime older than this parse) is held to the same answers.
func TestParseProviderUsage(t *testing.T) {
	raw, err := os.ReadFile("testdata/userinfo_cases.json")
	if err != nil {
		t.Fatalf("read cases: %v", err)
	}
	var table struct {
		Cases []struct {
			Name string           `json:"name"`
			Raw  string           `json:"raw"`
			Want map[string]int64 `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("decode cases: %v", err)
	}
	if len(table.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, tc := range table.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			got := parseProviderUsage(tc.Raw)
			seen := map[string]int64{}
			for key, value := range map[string]*int64{"upload": got.Upload, "download": got.Download, "total": got.Total, "expire": got.Expire} {
				if value != nil {
					seen[key] = *value
				}
			}
			if !reflect.DeepEqual(seen, tc.Want) && !(len(seen) == 0 && len(tc.Want) == 0) {
				t.Fatalf("parseProviderUsage(%q) = %v, want %v", tc.Raw, seen, tc.Want)
			}
		})
	}
}

// The list carries the parsed figures next to the verbatim header, so the
// overview can draw traffic and expiry without a `get` per row, and a record
// the provider never described carries none of them.
func TestListCarriesParsedProviderUsage(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("ss://YWVzLTEyOC1nY206cHc@192.0.2.10:8388#one")
	host.header = map[string]string{"subscription-userinfo": "upload=10; download=20; total=100; expire=1893456000"}
	if err := rt.saveSubscription(subscriptionRecord{ID: "provider", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "pasted", Content: "ss://YWVzLTEyOC1nY206cHc@192.0.2.12:8388#inline"}); err != nil {
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
	host.body = []byte("ss://YWVzLTEyOC1nY206cHc@192.0.2.10:8388#one")
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
	// The marker still says the runtime parsed it, so the UI does not read
	// the header again and bring back what this parser refused.
	if got := string(row["userinfo_parsed"]); got != "true" {
		t.Fatalf("userinfo_parsed = %q, want true (row %v)", got, row)
	}
}

// The figures sit under the same gate as the header they come from: a record
// that was never fetched reports neither, and no marker, even when the stored
// document carries a header (a restore, or a store written by hand).
func TestListReportsUsageOnlyForAFetchedRecord(t *testing.T) {
	rt, _ := newFetchRuntime(t)
	doc := subscriptionRecordsDocument{Version: 1, Records: []subscriptionRecord{
		{ID: "unfetched", URL: "https://provider.invalid/sub", Userinfo: "upload=1; download=2; total=100; expire=1893456000"},
	}}
	if err := rt.saveSubscriptionRecords(doc); err != nil {
		t.Fatalf("write store: %v", err)
	}
	var listed struct {
		Subscriptions []map[string]json.RawMessage `json:"subscriptions"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	if len(listed.Subscriptions) != 1 {
		t.Fatalf("list = %v, want one row", listed.Subscriptions)
	}
	row := listed.Subscriptions[0]
	for _, key := range []string{"userinfo", "userinfo_parsed", "upload", "download", "total", "expire"} {
		if _, present := row[key]; present {
			t.Fatalf("a record never fetched carries %s: %v", key, row)
		}
	}
}
