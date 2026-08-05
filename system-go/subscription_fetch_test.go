package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// httpKVHost answers KV from memory and HTTP from a scripted reply, so a fetch
// can be exercised end to end without a real host.
type httpKVHost struct {
	*kvHostCaller
	status  int
	header  map[string]string
	body    []byte
	httpErr error
	ua      string
}

func (h *httpKVHost) call(method string, params any) (json.RawMessage, error) {
	if method != latticeplugin.HostMethodHTTPDo && method != latticeplugin.HostMethodHTTPOperatorDo {
		return h.kvHostCaller.call(method, params)
	}
	encoded, _ := json.Marshal(params)
	var p struct {
		Header map[string]string `json:"header"`
	}
	_ = json.Unmarshal(encoded, &p)
	h.ua = p.Header["User-Agent"]
	if h.httpErr != nil {
		return nil, h.httpErr
	}
	reply := map[string]any{"status_code": h.status}
	if h.header != nil {
		reply["header"] = h.header
	}
	if h.body != nil {
		reply["body_base64"] = base64.StdEncoding.EncodeToString(h.body)
	}
	raw, _ := json.Marshal(reply)
	return raw, nil
}

func newFetchRuntime(t *testing.T) (*runtime, *httpKVHost) {
	t.Helper()
	host := &httpKVHost{kvHostCaller: newKVHostCaller(), status: 200}
	return &runtime{host: host}, host
}

func TestFetchReturnsBodyAndTrafficHeader(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("vless://one\nvless://two")
	host.header = map[string]string{"Subscription-Userinfo": "upload=1; download=2; total=3"}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", Name: "p", URL: "https://provider.invalid/sub", UA: "Surge/2000"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := rt.fetchSubscription("s1")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if out.Raw != string(host.body) {
		t.Fatalf("raw = %q", out.Raw)
	}
	if out.Userinfo != "upload=1; download=2; total=3" {
		t.Fatalf("userinfo = %q", out.Userinfo)
	}
	if host.ua != "Surge/2000" {
		t.Fatalf("the record's user agent was not sent: %q", host.ua)
	}
}

// Providers are inconsistent about header casing, and this value is what a
// client shows as its remaining quota.
func TestFetchFindsTrafficHeaderRegardlessOfCase(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("vless://one")
	host.header = map[string]string{"subscription-userinfo": "upload=5"}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.fetchSubscription("s1")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if out.Userinfo != "upload=5" {
		t.Fatalf("userinfo = %q", out.Userinfo)
	}
}

// An empty body is a failed fetch, not a subscription with no nodes. Treating it
// as success would overwrite a good snapshot with nothing and then serve nothing.
func TestFetchTreatsAnEmptyBodyAsFailure(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = nil
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.fetchSubscription("s1"); err == nil {
		t.Fatal("an empty provider body was accepted as success")
	}
}

func TestFetchRejectsNonSuccessStatus(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.status = 403
	host.body = []byte("denied")
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.fetchSubscription("s1"); err == nil {
		t.Fatal("a 403 was accepted as success")
	}
}

func TestFetchRejectsNonHTTPSchemes(t *testing.T) {
	rt, _ := newFetchRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: "file:///etc/passwd"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.fetchSubscription("s1"); err == nil {
		t.Fatal("a file:// URL was fetched")
	}
}

// A provider URL is frequently a bearer credential in path form, and this error
// travels into the core's audit log and onto the operator's screen.
func TestFetchErrorsRedactTheProviderURL(t *testing.T) {
	rt, host := newFetchRuntime(t)
	const secret = "https://provider.invalid/secret-token-path/sub"
	host.httpErr = errors.New("dial " + secret + ": connection refused")
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", URL: secret}); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := rt.fetchSubscription("s1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "secret-token-path") || strings.Contains(err.Error(), "://") {
		t.Fatalf("the provider URL leaked into the error: %v", err)
	}
}

func TestFetchRequiresAURL(t *testing.T) {
	rt, _ := newFetchRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", Content: "vless://inline"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.fetchSubscription("s1"); err == nil {
		t.Fatal("a record with no URL was fetched")
	}
}
