package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// The fetch method is the refresh path — the core calls it on a schedule and
// the UI on a click — so it is where the record learns when it last moved and
// how that went. The management list then shows a stale or failing provider
// without anyone opening the editor.

func fetchViaMethod(t *testing.T, rt *runtime, id string) response {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"subscription_id": id})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return rt.handleSubscriptionCall(callPayload{Method: "fetch", Payload: raw})
}

func storedRecord(t *testing.T, rt *runtime, id string) subscriptionRecord {
	t.Helper()
	rec, err := rt.getSubscription(id)
	if err != nil {
		t.Fatalf("get %q: %v", id, err)
	}
	return rec
}

func TestFetchMethodRecordsSuccessOnTheRecord(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("vless://one\nvless://two")
	host.header = map[string]string{"Subscription-Userinfo": "upload=1; download=2; total=3"}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res := fetchViaMethod(t, rt, "s1")
	if !res.OK {
		t.Fatalf("fetch failed: %s", res.Error)
	}
	var out struct {
		Raw            string `json:"raw"`
		Userinfo       string `json:"userinfo"`
		SubscriptionID string `json:"subscription_id"`
		Bytes          int    `json:"bytes"`
		FetchedAt      string `json:"fetched_at"`
	}
	if err := json.Unmarshal(res.Result, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	// The snapshot body stays first class — the core stores `raw` — and the
	// management fields ride alongside it.
	if out.Raw != string(host.body) || out.Bytes != len(host.body) || out.SubscriptionID != "s1" {
		t.Fatalf("unexpected fetch reply: %s", string(res.Result))
	}
	if _, err := time.Parse(time.RFC3339, out.FetchedAt); err != nil {
		t.Fatalf("fetched_at is not RFC3339: %q", out.FetchedAt)
	}

	rec := storedRecord(t, rt, "s1")
	if !rec.LastFetchOK || rec.LastError != "" {
		t.Fatalf("a good fetch recorded a failure: %+v", rec)
	}
	if _, err := time.Parse(time.RFC3339, rec.LastFetchAt); err != nil {
		t.Fatalf("last_fetch_at is not RFC3339: %q", rec.LastFetchAt)
	}
	if rec.Userinfo != "upload=1; download=2; total=3" {
		t.Fatalf("userinfo = %q", rec.Userinfo)
	}
}

func TestFetchMethodRecordsFailureOnTheRecord(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.httpErr = errors.New("dial tcp: connection refused")
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res := fetchViaMethod(t, rt, "s1")
	if res.OK {
		t.Fatal("a transport error was reported as success")
	}
	rec := storedRecord(t, rt, "s1")
	if rec.LastFetchOK {
		t.Fatal("a failed fetch recorded success")
	}
	if _, err := time.Parse(time.RFC3339, rec.LastFetchAt); err != nil {
		t.Fatalf("last_fetch_at is not RFC3339: %q", rec.LastFetchAt)
	}
	if !strings.Contains(rec.LastError, "connection refused") {
		t.Fatalf("last_error = %q", rec.LastError)
	}
	if rec.LastError != strings.TrimSpace(rec.LastError) {
		t.Fatalf("last_error was stored untrimmed: %q", rec.LastError)
	}
}

// The userinfo is the provider's quota figures; a failed refresh must not wipe
// the last known ones — the row shows them next to the failure badge.
func TestFetchFailureKeepsTheLastUserinfo(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("vless://one")
	host.header = map[string]string{"Subscription-Userinfo": "total=3"}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if res := fetchViaMethod(t, rt, "s1"); !res.OK {
		t.Fatalf("first fetch failed: %s", res.Error)
	}

	host.httpErr = errors.New("dial tcp: timeout")
	if res := fetchViaMethod(t, rt, "s1"); res.OK {
		t.Fatal("second fetch unexpectedly succeeded")
	}
	rec := storedRecord(t, rt, "s1")
	if rec.LastFetchOK || rec.Userinfo != "total=3" {
		t.Fatalf("the failure lost the last quota figures: %+v", rec)
	}
}

// A provider can answer with a whole error page, and the records document is
// rewritten in full on every save — the stored reason is capped so it cannot
// tax every unrelated write.
func TestFetchErrorIsCapped(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.httpErr = errors.New(strings.Repeat("x", maxFetchErrorBytes*4))
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	fetchViaMethod(t, rt, "s1")
	rec := storedRecord(t, rt, "s1")
	if len(rec.LastError) > maxFetchErrorBytes+len("…") {
		t.Fatalf("last_error was stored uncapped: %d bytes", len(rec.LastError))
	}
}

// An edit replaces the record wholesale; without preservation every save would
// claim a fetched record was never refreshed.
func TestSavePreservesFetchBookkeeping(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("vless://one")
	host.header = map[string]string{"Subscription-Userinfo": "total=3"}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if res := fetchViaMethod(t, rt, "s1"); !res.OK {
		t.Fatalf("fetch failed: %s", res.Error)
	}

	raw, _ := json.Marshal(map[string]any{
		"subscription": map[string]any{"id": "s1", "name": "renamed", "url": "https://provider.invalid/sub"},
	})
	if res := rt.handleSubscriptionCall(callPayload{Method: "save", Payload: raw}); !res.OK {
		t.Fatalf("save failed: %s", res.Error)
	}
	rec := storedRecord(t, rt, "s1")
	if !rec.LastFetchOK || rec.LastFetchAt == "" || rec.Userinfo != "total=3" {
		t.Fatalf("the edit dropped the fetch bookkeeping: %+v", rec)
	}
}

func TestListSurfacesFetchBookkeeping(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("vless://one")
	host.header = map[string]string{"Subscription-Userinfo": "total=3"}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A second record that is never fetched: its absence of bookkeeping must
	// read as "never fetched", not as a failure.
	if err := rt.saveSubscription(subscriptionRecord{ID: "s2", Content: "vless://inline"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if res := fetchViaMethod(t, rt, "s1"); !res.OK {
		t.Fatalf("fetch failed: %s", res.Error)
	}

	var listed struct {
		Subscriptions []map[string]any `json:"subscriptions"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	if len(listed.Subscriptions) != 2 {
		t.Fatalf("list = %d entries, want 2", len(listed.Subscriptions))
	}
	byID := map[string]map[string]any{}
	for _, entry := range listed.Subscriptions {
		byID[entry["id"].(string)] = entry
	}
	fetched := byID["s1"]
	if fetched["last_fetch_ok"] != true || fetched["userinfo"] != "total=3" || fetched["last_fetch_at"] == nil {
		t.Fatalf("the fetched record's status did not surface: %v", fetched)
	}
	fresh := byID["s2"]
	for _, key := range []string{"last_fetch_at", "last_fetch_ok", "last_error", "userinfo"} {
		if _, present := fresh[key]; present {
			t.Fatalf("a never-fetched record claims a status: %s in %v", key, fresh)
		}
	}
}

// A remote record has no inline content, so a preview that looked only at the
// stored fields would show nothing for exactly the subscriptions a quick
// preview is most useful on. The preview fetches, like render does.
func TestPreviewFetchesARemoteRecord(t *testing.T) {
	host := &httpKVHost{kvHostCaller: newKVHostCaller(), status: 200, body: []byte(previewFixture)}
	rt := &runtime{host: host, engine: testEngineWithHeadroom()}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	var out previewResult
	decodeResult(t, callSubscription(t, rt, "preview", map[string]any{"subscription_id": "s1"}), &out)
	if out.NodeCount != 2 {
		t.Fatalf("preview of a remote record saw %d nodes, want 2", out.NodeCount)
	}
	// A preview fetch is not a refresh: the served snapshot did not move, so
	// the record must not claim one happened.
	rec := storedRecord(t, rt, "s1")
	if rec.LastFetchAt != "" {
		t.Fatalf("a preview was recorded as a refresh: %+v", rec)
	}
}
