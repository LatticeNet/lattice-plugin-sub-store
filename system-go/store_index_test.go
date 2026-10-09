package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// heavyLegacyRecords is n records with every list field near its realistic
// worst: long multi-byte names, a remark past the index's cut, tags, a
// chain, a provider URL, and refresh bookkeeping with a capped error and a
// full quota header.
func heavyLegacyRecords(n int) []subscriptionRecord {
	steps := []json.RawMessage{
		json.RawMessage(`{"type":"Regex Filter","args":{"regex":["HK|TW|JP"],"keep":true}}`),
		json.RawMessage(`{"type":"Regex Rename Operator","args":[{"expr":"^(.*)$","now":"$1"}]}`),
		json.RawMessage(`{"type":"Sort Operator","args":"asc","disabled":true}`),
		json.RawMessage(`{"type":"Flag Operator","args":{"mode":"add"}}`),
	}
	records := make([]subscriptionRecord, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("provider-subscription-%03d", i)
		rec := subscriptionRecord{
			ID: id, Name: strings.Repeat("订阅", 10) + id, DisplayName: strings.Repeat("显示", 10) + id,
			Remark: strings.Repeat("备注 ", 100), Tags: []string{"home-network", "paid-provider", "asia-pacific", "fallback-group"},
			Source: subscriptionSourceRemote, URL: "https://provider.example/api/v1/client/subscribe?token=" + strings.Repeat("t", 32),
			Target: "ClashMeta", Process: steps,
			LastFetchAt: "2026-10-01T00:00:00Z", LastError: strings.Repeat("e", maxFetchErrorBytes) + "…",
			Userinfo: "upload=123456789012; download=987654321098; total=1099511627776; expire=1893456000",
		}
		if i%10 == 0 {
			rec = subscriptionRecord{ID: id, Name: rec.Name, Kind: kindCollection, Tags: rec.Tags, Process: steps,
				Members:    []string{"provider-subscription-001", "provider-subscription-002", "provider-subscription-003", "provider-subscription-004"},
				MemberTags: []string{"home-network", "paid-provider"}}
		}
		records = append(records, rec)
	}
	return records
}

// The risk the plan names: at 300 records the index approaches list's
// budget. Migrated from a legacy store at realistic worst, the index stays
// under the bound a save is refused at, and list's whole answer under the
// wave's list stdout budget.
func TestIndexAt300RecordsFitsListBudget(t *testing.T) {
	host := newKVHostCaller()
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	seedLegacyStore(t, host, heavyLegacyRecords(300))
	for reply := (migrateStoreReply{}); !reply.Verified; {
		reply = migrateStoreCall(t, rt, map[string]any{})
	}
	index := len(host.values[storeIndexKey])
	res := callSubscription(t, rt, "list", map[string]any{})
	if !res.OK {
		t.Fatal(res.Error)
	}
	// The runner counts the whole invoke_result frame against stdout.
	frame := len(mustJSON(res)) + 256
	budget := waveRuntimeBudgets()[pluginID+"/subscription/list"].StdoutBytes
	t.Logf("300 heavy records: index %d bytes (bound %d), list frame %d bytes (budget %d)", index, maxIndexBytes, frame, budget)
	if index > maxIndexBytes {
		t.Fatalf("the index is %d bytes at 300 records, past the %d a save is refused at", index, maxIndexBytes)
	}
	if frame > budget {
		t.Fatalf("list answers %d bytes at 300 records, over its %d stdout budget", frame, budget)
	}
}

// A save that would take the index past its bound is refused before it
// writes anything, so it leaves no orphan record behind.
func TestSaveRefusesAnIndexPastItsBound(t *testing.T) {
	host := newKVHostCaller()
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	idx := indexDocument{Version: storeVersionSplit}
	for i := 0; len(mustJSON(idx)) < maxIndexBytes-512; i++ {
		idx.Records = append(idx.Records, indexEntry{ID: fmt.Sprintf("bulk-%03d", i), Kind: kindSub, Name: "bulk", LastError: strings.Repeat("e", 2048)})
	}
	host.values[storeIndexKey] = mustJSON(idx)
	err := rt.saveSubscription(subscriptionRecord{ID: "straw", Name: strings.Repeat("n", 1024), Remark: strings.Repeat("r", 160)})
	if err == nil || !strings.Contains(err.Error(), "the subscription index would be") {
		t.Fatalf("a save past the index bound = %v", err)
	}
	if _, ok := host.values[recordKey("straw")]; ok {
		t.Fatal("the refused save left its record behind")
	}
}

// Record ids were never constrained, and the server refuses keys with a
// slash, a backslash or a control character. Those bytes are escaped so
// every id has a key, two ids never share one, and an id too long for a key
// is refused by name.
func TestStoreKeysEscapeIDsTheServerWouldRefuse(t *testing.T) {
	host := newKVHostCaller()
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	ids := []string{"a/b", "a%2Fb", `back\slash`, "tab\there", "plain-id"}
	keys := map[string]string{}
	for _, id := range ids {
		key := recordKey(id)
		if strings.ContainsAny(key, `/\`) || strings.ContainsFunc(key, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			t.Fatalf("key %q for %q would be refused by the server", key, id)
		}
		if other, taken := keys[key]; taken {
			t.Fatalf("%q and %q share key %q", id, other, key)
		}
		keys[key] = id
		if err := rt.saveSubscription(subscriptionRecord{ID: id, Name: id, Content: "x"}); err != nil {
			t.Fatalf("save %q: %v", id, err)
		}
		got, err := rt.getSubscription(id)
		if err != nil || got.ID != id {
			t.Fatalf("get %q = %+v, %v", id, got, err)
		}
	}
	if recordKey("plain-id") != storeRecordPrefix+"plain-id" {
		t.Fatalf("an ordinary id was escaped: %q", recordKey("plain-id"))
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: strings.Repeat("x", 250), Name: "long"}); err == nil || !strings.Contains(err.Error(), "too long to store") {
		t.Fatalf("an id past the key bound = %v", err)
	}
}
