package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// replyFrameBytes is the size of the response frame that carries reply,
// rounded up the way hostCallFrameOverhead rounds a host_call frame.
func replyFrameBytes(t *testing.T, reply any) int {
	t.Helper()
	raw, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	return len(raw) + hostCallFrameOverhead
}

// Core counts every frame a plugin writes against the method's signed
// stdout_bytes, host calls included, and kills the call past it. A migration
// of script files with large programs must therefore split its chunks by
// bytes: each call stays inside the budget, and the store still verifies.
func TestMigrateStoreBoundsEachChunkByFrameBytes(t *testing.T) {
	host := &budgetCountingHost{kvHostCaller: newKVHostCaller()}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	program := `$content = "` + strings.Repeat("x", 1500<<10) + `";`
	var records []subscriptionRecord
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("big-%d", i)
		records = append(records, subscriptionRecord{ID: id, Name: "script " + id, Kind: kindFile, FileType: fileTypeScript, Content: program})
	}
	records = append(records, legacyFixture(20)...)
	seedLegacyStore(t, host.kvHostCaller, records)
	budget := ackedRuntimeBudgets()[pluginID+"/subscription/migrate_store"]

	migrated, calls := 0, 0
	var reply migrateStoreReply
	for calls < 20 {
		calls++
		host.total, host.frames = 0, 0
		reply = migrateStoreCall(t, rt, map[string]any{})
		out := host.frames + replyFrameBytes(t, reply)
		if out > budget.StdoutBytes || host.total > budget.HostCalls {
			t.Fatalf("call %d wrote %d bytes in %d host calls, over %d bytes and %d calls", calls, out, host.total, budget.StdoutBytes, budget.HostCalls)
		}
		migrated += reply.Migrated
		if reply.Verified {
			break
		}
	}
	if !reply.Verified || reply.StoreVersion != storeVersionSplit || migrated != len(records) {
		t.Fatalf("after %d calls: %+v, %d of %d records migrated", calls, reply, migrated, len(records))
	}
	// Six records of about 2 MiB of frames each cannot share one call.
	if calls < 4 {
		t.Fatalf("the migration took %d calls; a chunk ignored its byte bound", calls)
	}
	for _, rec := range records {
		got, err := rt.getSubscription(rec.ID)
		if err != nil || (isScriptFile(rec) && got.Content != rec.Content) {
			t.Fatalf("%s after the migration: %v", rec.ID, err)
		}
	}
}

// Every write method's signed stdout_bytes covers its largest frames, taken
// from the store's own bounds rather than from a fixture, so a budget cannot
// pass the tests and still be too small for a full store.
func TestWriteBudgetsCoverTheirLargestFrames(t *testing.T) {
	budgets := ackedRuntimeBudgets()
	index := kvPutFrameBytes(storeIndexKey, make([]byte, maxIndexBytes))
	record := kvPutFrameBytes(recordKey("x"), make([]byte, maxRecordDocBytes))
	legacy := kvPutFrameBytes(subscriptionRecordsKey, make([]byte, maxSubscriptionDocBytes+maxLegacyDocSlackBytes))
	small := hostCallFrameOverhead + 512
	reply := 64 << 10
	// get, save and restore answer with the record itself.
	withRecord := maxRecordDocBytes + reply
	for method, need := range map[string]int{
		"get":     3*small + withRecord,
		"save":    record + index + 4*small + withRecord,
		"delete":  record + index + 3*small + reply,
		"restore": record + index + 3*small + withRecord,
		"purge":   index + 2*small + reply,
		"reorder": index + small + reply,
		// The larger of a chunk (its record frames, which stop before
		// migrateChunkFrameBytes unless one record is larger, the index and
		// the reads) and the verify call (the index twice and the legacy
		// document).
		"migrate_store": max(max(migrateChunkFrameBytes, record)+index+140*small, 2*index+legacy+8*small) + reply,
	} {
		got := budgets[pluginID+"/subscription/"+method].StdoutBytes
		if got < need {
			t.Errorf("%s signs %d stdout bytes; its largest frames need %d", method, got, need)
		}
	}
}
