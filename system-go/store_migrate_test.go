package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// legacyFixture is a legacy store of n records in the shapes a real one
// holds: plain subs, tagged subs carrying refresh bookkeeping, collections and
// script files whose programs live under their own keys.
func legacyFixture(n int) []subscriptionRecord {
	records := make([]subscriptionRecord, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("rec-%03d", i)
		rec := subscriptionRecord{ID: id, Name: "record " + id, Source: subscriptionSourceLocal, Content: "ss://YWVzLTEyOC1nY206cHc@192.0.2.10:8388#" + id}
		switch {
		case i%30 == 7:
			rec = subscriptionRecord{ID: id, Name: "script " + id, Kind: kindFile, FileType: fileTypeScript, Content: fmt.Sprintf(`$content = "program %d";`, i)}
		case i%25 == 3:
			rec = subscriptionRecord{ID: id, Name: "coll " + id, Kind: kindCollection, Members: []string{"rec-000"}, MemberTags: []string{"home"}}
		case i%4 == 1:
			rec.Tags = []string{"home"}
			rec.LastFetchAt, rec.LastFetchOK, rec.Userinfo = "2026-10-01T00:00:00Z", true, "upload=1; download=2; total=3"
		}
		records = append(records, rec)
	}
	return records
}

func migrateStoreCall(t *testing.T, rt *runtime, payload map[string]any) migrateStoreReply {
	t.Helper()
	var reply migrateStoreReply
	decodeResult(t, callSubscription(t, rt, "migrate_store", payload), &reply)
	return reply
}

// The acceptance case: a legacy document of 300 records, which loads because
// the reader bounds bytes and not the count, migrates in chunked calls each
// inside migrate_store's budget, verifies, and leaves every record readable
// from its own key and an index that says what the legacy document said.
func TestMigrateStoreOf300RecordsInChunks(t *testing.T) {
	host := &budgetCountingHost{kvHostCaller: newKVHostCaller()}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	records := legacyFixture(300)
	legacy := seedLegacyStore(t, host.kvHostCaller, records)
	budget := waveRuntimeBudgets()[pluginID+"/subscription/migrate_store"].HostCalls

	var reply migrateStoreReply
	for call := 1; call <= 5; call++ {
		host.total = 0
		reply = migrateStoreCall(t, rt, map[string]any{})
		if host.total > budget {
			t.Fatalf("call %d made %d host calls, over migrate_store's %d", call, host.total, budget)
		}
		wantRemaining := max(300-64*call, 0)
		if reply.Remaining != wantRemaining || reply.Migrated != min(64, 300-64*(call-1)) {
			t.Fatalf("call %d = %+v, want %d remaining", call, reply, wantRemaining)
		}
		if call < 5 && (reply.Verified || reply.StoreVersion != storeVersionLegacy) {
			t.Fatalf("call %d opened the store before the last chunk: %+v", call, reply)
		}
	}
	if !reply.Verified || reply.StoreVersion != storeVersionSplit {
		t.Fatalf("the fifth chunk did not verify: %+v", reply)
	}
	scripts := 0
	for _, rec := range records {
		if isScriptFile(rec) {
			scripts++
		}
	}
	if reply.Done || reply.LegacyPrograms != scripts {
		t.Fatalf("after the verify = %+v, want %d legacy programs still to delete", reply, scripts)
	}
	// The store takes writes once it verified, before the cleanup finishes.
	// An edit, since 300 records are past the cap a new record must fit.
	if err := rt.saveSubscription(subscriptionRecord{ID: "rec-000", Name: "record rec-000", Source: subscriptionSourceLocal, Content: "ss://YWVzLTEyOC1nY206cHc@192.0.2.10:8388#rec-000"}); err != nil {
		t.Fatalf("a verified store refused an edit: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "one-more", Name: "one more"}); err == nil || !strings.Contains(err.Error(), "too many subscriptions") {
		t.Fatalf("a new record past the cap: %v", err)
	}
	host.total = 0
	cleanup := migrateStoreCall(t, rt, map[string]any{})
	if !cleanup.Done || cleanup.LegacyPrograms != 0 || host.total > budget {
		t.Fatalf("cleanup = %+v in %d calls", cleanup, host.total)
	}
	for _, rec := range records {
		if !isScriptFile(rec) {
			continue
		}
		if _, ok := host.values[fileScriptKey(rec.ID)]; ok {
			t.Fatalf("legacy program of %s survived the cleanup", rec.ID)
		}
	}
	if len(host.deletes) != scripts {
		t.Fatalf("cleanup issued %d kv.delete calls, want one per script (%d)", len(host.deletes), scripts)
	}

	entries := map[string]indexEntry{}
	for i, entry := range listedEntries(t, rt) {
		if entry.Order != i {
			t.Fatalf("entry %s has order %d at %d", entry.ID, entry.Order, i)
		}
		entries[entry.ID] = entry
	}
	if len(entries) != 300 {
		t.Fatalf("index holds %d records, want the 300 migrated", len(entries))
	}
	for _, stored := range legacy.Records {
		entry, ok := entries[stored.ID]
		if !ok {
			t.Fatalf("%s is missing from the index", stored.ID)
		}
		want := legacyEntry(stored)
		if entry.Revision != want.Revision || entry.ContentHash != want.ContentHash || entry.Name != stored.Name || entry.Kind != recordKind(stored) {
			t.Fatalf("%s index entry %+v differs from the legacy record", stored.ID, entry)
		}
		if entry.LastFetchAt != stored.LastFetchAt || entry.Userinfo != stored.Userinfo {
			t.Fatalf("%s lost its refresh bookkeeping: %+v", stored.ID, entry)
		}
		rt.legacy = legacyCache{}
		got, err := rt.getSubscription(stored.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Revision != stored.Revision {
			t.Fatalf("%s revision moved across the migration: %s became %s", stored.ID, stored.Revision, got.Revision)
		}
		if isScriptFile(stored) && !strings.Contains(got.Content, "program") {
			t.Fatalf("%s lost its program: %q", stored.ID, got.Content)
		}
	}
	var marked subscriptionRecordsDocument
	if err := json.Unmarshal(host.values[subscriptionRecordsKey], &marked); err != nil || marked.MigratedTo != storeIndexKey || len(marked.Records) != 300 {
		t.Fatalf("legacy document after migration: err=%v migrated_to=%q records=%d", err, marked.MigratedTo, len(marked.Records))
	}
}

// A chunk that dies after writing records but before its index write lands
// resumes from the cursor and writes those records again; a record the index
// already carries at its revision is skipped without a write; and a call on
// a finished store writes nothing.
func TestMigrateStoreIsIdempotentAndResumesFromCursor(t *testing.T) {
	host := &failingIndexHost{kvHostCaller: newKVHostCaller()}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	seedLegacyStore(t, host.kvHostCaller, legacyFixture(100))

	first := migrateStoreCall(t, rt, map[string]any{"chunk": 30})
	if first.Migrated != 30 || first.Remaining != 70 {
		t.Fatalf("first chunk = %+v", first)
	}
	host.failIndexPut = true
	if res := callSubscription(t, rt, "migrate_store", map[string]any{"chunk": 30}); res.OK {
		t.Fatal("a chunk whose index write failed reported success")
	}
	host.failIndexPut = false
	var idx indexDocument
	if err := json.Unmarshal(host.values[storeIndexKey], &idx); err != nil || idx.Migration.Cursor != "rec-029" || len(idx.Records) != 30 {
		t.Fatalf("the failed chunk moved the cursor: cursor=%q records=%d err=%v", idx.Migration.Cursor, len(idx.Records), err)
	}
	puts := host.puts
	resumed := migrateStoreCall(t, rt, map[string]any{"chunk": 30})
	if resumed.Migrated != 30 || resumed.Remaining != 40 || host.puts-puts != 31 {
		t.Fatalf("resumed chunk = %+v with %d writes, want rec-030..rec-059 written again plus the index", resumed, host.puts-puts)
	}

	// Rewind the cursor: everything the index already carries is skipped.
	if err := json.Unmarshal(host.values[storeIndexKey], &idx); err != nil {
		t.Fatal(err)
	}
	idx.Migration.Cursor = ""
	host.values[storeIndexKey] = mustJSON(idx)
	puts = host.puts
	rewound := migrateStoreCall(t, rt, map[string]any{"chunk": 30})
	if rewound.Migrated != 30 || rewound.Remaining != 10 || host.puts-puts != 31 {
		t.Fatalf("rewound chunk = %+v with %d writes, want the 60 known records skipped and rec-060..rec-089 written", rewound, host.puts-puts)
	}
	for !rewound.Verified {
		rewound = migrateStoreCall(t, rt, map[string]any{"chunk": 30})
	}
	for !rewound.Done {
		rewound = migrateStoreCall(t, rt, map[string]any{"chunk": 30})
	}
	puts = host.puts
	again := migrateStoreCall(t, rt, map[string]any{})
	if !again.Done || again.Migrated != 0 || host.puts != puts {
		t.Fatalf("a call on a finished store = %+v with %d writes", again, host.puts-puts)
	}
	if got := len(listedEntries(t, rt)); got != 100 {
		t.Fatalf("index holds %d records, want 100", got)
	}
}

// failingIndexHost refuses index writes on demand, the way a chunk dies
// between its record writes and its index write.
type failingIndexHost struct {
	*kvHostCaller
	failIndexPut bool
}

func (h *failingIndexHost) call(method string, params any) (json.RawMessage, error) {
	if h.failIndexPut && method == "kv.put" {
		var p struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(mustJSON(params), &p)
		if p.Key == storeIndexKey {
			return nil, fmt.Errorf("host refused the write")
		}
	}
	return h.kvHostCaller.call(method, params)
}

// The migration compiles every stored chain: a pattern RE2 refuses flags the
// record regex_incompatible (it keeps rendering on the bundle), a step the
// bundle has to run flags has_fallback_step, and a disabled step flags
// nothing because nothing runs it.
func TestMigrateStoreFlagsLookaheadRecords(t *testing.T) {
	host := newKVHostCaller()
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	step := func(raw string) []json.RawMessage { return []json.RawMessage{json.RawMessage(raw)} }
	seedLegacyStore(t, host, []subscriptionRecord{
		{ID: "lookahead", Name: "lookahead", Content: "x", Process: step(`{"type":"Regex Filter","args":{"regex":["^(?!.*(HK|TW)).*$"],"keep":true}}`)},
		{ID: "rename-backref", Name: "rename", Content: "x", Process: step(`{"type":"Regex Rename Operator","args":[{"expr":"(a)\\1","now":"b"}]}`)},
		{ID: "plain-regex", Name: "plain", Content: "x", Process: step(`{"type":"Regex Filter","args":{"regex":["HK|TW"],"keep":false}}`)},
		{ID: "disabled-lookahead", Name: "disabled", Content: "x", Process: step(`{"type":"Regex Delete Operator","disabled":true,"args":["(?<=x)y"]}`)},
		{ID: "scripted", Name: "scripted", Content: "x", Process: step(`{"type":"Script Operator","args":{"mode":"script","content":"$server.name = 'x'"}}`)},
	})
	reply := migrateStoreCall(t, rt, map[string]any{})
	if !reply.Verified {
		t.Fatalf("migration did not verify: %+v", reply)
	}
	if strings.Join(reply.RegexIncompatible, ",") != "lookahead,rename-backref" {
		t.Fatalf("flagged %v, want lookahead,rename-backref", reply.RegexIncompatible)
	}
	want := map[string]indexFlags{
		"lookahead":          {RegexIncompatible: true},
		"rename-backref":     {RegexIncompatible: true},
		"plain-regex":        {},
		"disabled-lookahead": {},
		"scripted":           {HasFallbackStep: true},
	}
	for id, flags := range want {
		if got := indexEntryOf(t, rt, id).Flags; got != flags {
			t.Errorf("%s flags = %+v, want %+v", id, got, flags)
		}
	}
	var listed struct {
		Subscriptions []listView `json:"subscriptions"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	for _, row := range listed.Subscriptions {
		if row.ID == "lookahead" && (row.Flags == nil || !row.Flags.RegexIncompatible) {
			t.Fatalf("list does not show the flag: %+v", row)
		}
		if row.ID == "plain-regex" && row.Flags != nil {
			t.Fatalf("list flagged a compatible chain: %+v", row.Flags)
		}
	}
}

// Once migrated, the legacy document is kept but marked: a record deleted and
// purged afterwards must not come back out of it.
func TestAPurgedRecordDoesNotComeBackFromTheMigratedLegacyDocument(t *testing.T) {
	host := newKVHostCaller()
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	seedLegacyStore(t, host, []subscriptionRecord{{ID: "gone", Name: "gone", Content: "x"}, {ID: "kept", Name: "kept", Content: "y"}})
	if reply := migrateStoreCall(t, rt, map[string]any{}); !reply.Done {
		t.Fatalf("migration = %+v", reply)
	}
	for _, method := range []string{"delete", "purge"} {
		if res := callSubscription(t, rt, method, map[string]any{"subscription_id": "gone"}); !res.OK {
			t.Fatalf("%s: %s", method, res.Error)
		}
	}
	if !strings.Contains(string(host.values[subscriptionRecordsKey]), `"id":"gone"`) {
		t.Fatal("the fixture must keep the record in the legacy document")
	}
	if res := callSubscription(t, rt, "get", map[string]any{"subscription_id": "gone"}); res.OK {
		t.Fatalf("a purged record came back from the legacy document: %s", res.Result)
	}
	if res := callSubscription(t, rt, "get", map[string]any{"subscription_id": "kept"}); !res.OK {
		t.Fatalf("a live record stopped reading: %s", res.Error)
	}
}

// rebuild re-derives entries from their records: an entry that lost a save's
// delta is corrected, one whose record was archived moves to Archived, and
// one with neither a record nor an archive is dropped. Bookkeeping survives.
func TestRebuildRepairsAStaleIndex(t *testing.T) {
	host := newKVHostCaller()
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	for _, id := range []string{"a", "b", "c"} {
		if err := rt.saveSubscription(subscriptionRecord{ID: id, Name: "name " + id, Content: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	rt.noteFetchOutcome("a", fetchOutcome{at: fixedTime(), userinfo: "total=9"})
	if err := rt.deleteSubscription("b"); err != nil {
		t.Fatal(err)
	}
	var idx indexDocument
	if err := json.Unmarshal(host.values[storeIndexKey], &idx); err != nil {
		t.Fatal(err)
	}
	// What a lost delta leaves: a stale name, an archived record still
	// listed live, and an entry with nothing behind it.
	idx.Records[0].Name = "stale"
	archived := idx.Archived[0]
	archived.ArchivedAt = ""
	idx.Archived = nil
	idx.Records = append(idx.Records, archived, indexEntry{ID: "phantom", Kind: kindSub, Name: "phantom"})
	host.values[storeIndexKey] = mustJSON(idx)

	first := migrateStoreCall(t, rt, map[string]any{"rebuild": true, "chunk": 2})
	if first.Rebuilt != 2 || first.Next != "b" || first.Done {
		t.Fatalf("first rebuild chunk = %+v", first)
	}
	second := migrateStoreCall(t, rt, map[string]any{"rebuild": true, "chunk": 2, "after": first.Next})
	if second.Rebuilt != 2 || !second.Done {
		t.Fatalf("second rebuild chunk = %+v", second)
	}
	listing, err := rt.storeListing()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, entry := range listing.Records {
		ids = append(ids, entry.ID)
	}
	if strings.Join(ids, ",") != "a,c" || len(listing.Archived) != 1 || listing.Archived[0].ID != "b" || listing.Archived[0].ArchivedAt == "" {
		t.Fatalf("after rebuild live=%v archived=%+v", ids, listing.Archived)
	}
	if a := listing.Records[0]; a.Name != "name a" || a.Userinfo != "total=9" {
		t.Fatalf("rebuild left a = %+v", a)
	}
}

// lossyIndexHost drops the last entry from one index write, the way a
// concurrent writer holding an older index can.
type lossyIndexHost struct {
	*kvHostCaller
	lose bool
}

func (h *lossyIndexHost) call(method string, params any) (json.RawMessage, error) {
	out, err := h.kvHostCaller.call(method, params)
	if h.lose && method == "kv.put" && err == nil {
		var p struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(mustJSON(params), &p)
		if p.Key == storeIndexKey {
			var idx indexDocument
			if json.Unmarshal(h.values[storeIndexKey], &idx) == nil && len(idx.Records) > 0 {
				idx.Records = idx.Records[:len(idx.Records)-1]
				h.values[storeIndexKey] = mustJSON(idx)
				h.lose = false
			}
		}
	}
	return out, err
}

// The verify holds the index to the legacy document: an index that lost an
// entry is caught, the store stays closed and the legacy document unmarked,
// and the next call writes the lost record again and verifies.
func TestMigrateStoreVerifyCatchesALostEntryAndRecovers(t *testing.T) {
	host := &lossyIndexHost{kvHostCaller: newKVHostCaller()}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	seedLegacyStore(t, host.kvHostCaller, legacyFixture(10))
	host.lose = true
	if res := callSubscription(t, rt, "migrate_store", map[string]any{}); res.OK || !strings.Contains(res.Error, "migrate_store verify") {
		t.Fatalf("a verify over a lossy index = %+v, want it refused", res)
	}
	if strings.Contains(string(host.values[subscriptionRecordsKey]), "migrated_to") {
		t.Fatal("a failed verify marked the legacy document")
	}
	if res := callSubscription(t, rt, "save", map[string]any{"subscription": map[string]any{"id": "x", "name": "x"}}); res.OK {
		t.Fatal("a failed verify opened the store for writes")
	}
	puts := host.puts
	reply := migrateStoreCall(t, rt, map[string]any{})
	if !reply.Verified || reply.Migrated != 1 {
		t.Fatalf("the retry = %+v, want the lost record written again and verified", reply)
	}
	// The lost record, the index, the legacy mark and the verified index.
	if host.puts-puts != 4 {
		t.Fatalf("the retry wrote %d keys, want 4", host.puts-puts)
	}
}
