package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// kvHostCaller is an in-memory KV the subscription store can be exercised
// against. The scripted fakeHostCaller in main_test.go replays a fixed list of
// responses, which cannot model read-after-write.
type kvHostCaller struct {
	values  map[string][]byte
	puts    int
	deletes []string
}

func newKVHostCaller() *kvHostCaller {
	return &kvHostCaller{values: map[string][]byte{}}
}

func (k *kvHostCaller) call(method string, params any) (json.RawMessage, error) {
	encoded, _ := json.Marshal(params)
	var p struct {
		Key         string `json:"key"`
		ValueBase64 string `json:"value_base64"`
	}
	_ = json.Unmarshal(encoded, &p)

	switch method {
	case latticeplugin.HostMethodKVPut:
		raw, err := base64.StdEncoding.DecodeString(p.ValueBase64)
		if err != nil {
			return nil, err
		}
		k.values[p.Key] = raw
		k.puts++
		return json.RawMessage(`{"ok":true}`), nil
	case latticeplugin.HostMethodKVGet:
		value, ok := k.values[p.Key]
		if !ok {
			return json.RawMessage(`{"ok":false}`), nil
		}
		return json.RawMessage(`{"ok":true,"value_base64":"` + base64.StdEncoding.EncodeToString(value) + `"}`), nil
	case latticeplugin.HostMethodKVDelete:
		delete(k.values, p.Key)
		k.deletes = append(k.deletes, p.Key)
		return json.RawMessage(`{"ok":true}`), nil
	default:
		return nil, nil
	}
}

func newKVRuntime(t *testing.T) (*runtime, *kvHostCaller) {
	t.Helper()
	host := newKVHostCaller()
	return &runtime{host: host, engine: testEngineWithHeadroom()}, host
}

// testEngineWithHeadroom is the embedded engine with a timeout wide enough to
// survive race instrumentation.
//
// The production timeout is 10s and wazero under -race exceeds it on this
// machine, so a test that kept it would be measuring the instrumented runtime
// against a production limit and failing for a reason unrelated to what it
// asserts. Every other limit stays at its production value, so a test still
// cannot pass by exceeding a bound production would enforce.
func testEngineWithHeadroom() *subStoreEngine {
	engine := newEmbeddedSubStoreEngine()
	engine.limits.Timeout = 2 * time.Minute
	return engine
}

var (
	sharedWarmEngineOnce sync.Once
	sharedWarmEngine     *subStoreEngine
	sharedWarmEngineErr  error
)

// sharedWarmTestEngine is one prewarmed engine for tests whose engine work is
// incidental to what they assert. A fresh engine pays a cold boot per test,
// about 3 s here and about 12 s under -race, and the system suite already runs
// close to CI's timeout under -race. Parse and produce keep nothing between
// calls, and anything with user JavaScript runs on its own isolated runtime,
// so the tests sharing it cannot see each other. A test that inspects or
// instruments the warm runtime builds its own engine.
func sharedWarmTestEngine(t *testing.T) *subStoreEngine {
	t.Helper()
	sharedWarmEngineOnce.Do(func() {
		sharedWarmEngine = testEngineWithHeadroom()
		sharedWarmEngineErr = sharedWarmEngine.prewarm()
	})
	if sharedWarmEngineErr != nil {
		t.Fatalf("prewarm shared test engine: %v", sharedWarmEngineErr)
	}
	return sharedWarmEngine
}

// newWarmKVRuntime is newKVRuntime on the shared prewarmed engine.
func newWarmKVRuntime(t *testing.T) (*runtime, *kvHostCaller) {
	t.Helper()
	host := newKVHostCaller()
	return &runtime{host: host, engine: sharedWarmTestEngine(t)}, host
}

func TestSubscriptionRecordRoundTrip(t *testing.T) {
	rt, _ := newKVRuntime(t)

	if err := rt.saveSubscription(subscriptionRecord{
		ID: "s1", Name: "provider", URL: "https://example.invalid/sub", UA: "Surge", Target: "Surge",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := rt.getSubscription("s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "provider" || got.URL != "https://example.invalid/sub" || got.Target != "Surge" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.SchemaVersion != recordSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got.SchemaVersion, recordSchemaVersion)
	}

	if list := listedEntries(t, rt); len(list) != 1 {
		t.Fatalf("list = %v", list)
	}
}

func TestSubscriptionSaveReplacesRatherThanDuplicates(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", Name: "first"}); err != nil {
		t.Fatalf("save first: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", Name: "second"}); err != nil {
		t.Fatalf("save second: %v", err)
	}
	list := listedEntries(t, rt)
	if len(list) != 1 {
		t.Fatalf("save duplicated the record: %d entries", len(list))
	}
	if list[0].Name != "second" {
		t.Fatalf("replace did not take: %+v", list[0])
	}
}

// The host accepts a KV value of any size and KV rides the full-rewrite state
// path, so this plugin bounds itself rather than waiting for the host fix.
func TestSubscriptionInlineContentIsCapped(t *testing.T) {
	rt, _ := newKVRuntime(t)
	err := rt.saveSubscription(subscriptionRecord{
		ID:      "big",
		Name:    "big",
		Content: strings.Repeat("x", maxSubscriptionInlineBytes+1),
	})
	if err == nil {
		t.Fatal("oversized inline content was accepted")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error must name the limit, got %v", err)
	}
}

func TestSubscriptionSaveRequiresAnID(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{Name: "no id"}); err == nil {
		t.Fatal("a record without an id was accepted")
	}
}

func TestSubscriptionDelete(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", Name: "one"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := rt.deleteSubscription("s1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := rt.getSubscription("s1"); err == nil {
		t.Fatal("record still present after delete")
	}
	if err := rt.deleteSubscription("s1"); err == nil {
		t.Fatal("deleting a missing record reported success")
	}
}

// The store keeps the operator's manual order: a new record goes last, an
// edit keeps its place, and reorder is the only thing that moves a row.
func TestSubscriptionRecordsKeepManualOrder(t *testing.T) {
	rt, _ := newKVRuntime(t)
	for _, id := range []string{"c", "a", "b"} {
		if err := rt.saveSubscription(subscriptionRecord{ID: id, Name: id}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Name: "c renamed"}); err != nil {
		t.Fatalf("edit c: %v", err)
	}
	order := func() string {
		var ids []string
		for i, entry := range listedEntries(t, rt) {
			if entry.Order != i {
				t.Fatalf("entry %s has order %d at position %d", entry.ID, entry.Order, i)
			}
			ids = append(ids, entry.ID)
		}
		return strings.Join(ids, ",")
	}
	if got := order(); got != "c,a,b" {
		t.Fatalf("order = %s, want the order records were created in (c,a,b)", got)
	}
	if res := callSubscription(t, rt, "reorder", map[string]any{"ids": []string{"b", "c", "a"}}); !res.OK {
		t.Fatalf("reorder: %s", res.Error)
	}
	if got := order(); got != "b,c,a" {
		t.Fatalf("order after reorder = %s, want b,c,a", got)
	}
	for name, ids := range map[string][]string{
		"missing one": {"b", "c"},
		"duplicate":   {"b", "b", "a"},
		"unknown":     {"b", "c", "zzz"},
	} {
		if res := callSubscription(t, rt, "reorder", map[string]any{"ids": ids}); res.OK {
			t.Fatalf("reorder %s was accepted", name)
		}
	}
	if got := order(); got != "b,c,a" {
		t.Fatalf("a refused reorder moved rows: %s", got)
	}
}
