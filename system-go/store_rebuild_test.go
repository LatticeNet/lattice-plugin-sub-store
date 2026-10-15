package main

import (
	"strings"
	"testing"
	"time"
)

// migrate_store's S2 additions (plan sections 2.2 and 2.7): the rebuild
// reads staged-v2 wherever the record document alone would mislead, and
// delete_legacy removes subscriptions-v1 once nothing needs it.

// TestRebuildIndexKeepsStagedFields: a rebuild carries every staged fact and
// selection_version, and settles the five states a record document alone
// would misread: a staged new record, a never-live half-done promotion, a
// half-done promotion with record-v2 at the old revision, a staged restore,
// and a record-v2 ahead of its entry with nothing staged at the entry's
// revision.
func TestRebuildIndexKeepsStagedFields(t *testing.T) {
	rt, host := newCASRuntime(t)

	// carried: a live fleet record with a staged edit and a selection version.
	seedFleetLive(t, rt, fleetRecord("carried", "hk"))
	rt.noteFetchOutcome("carried", fetchOutcome{at: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), selectionVersion: fleetTestVersion})
	edit := fleetRecord("carried", "jp")
	stagedCarried, _ := stageEdit(t, rt, edit, originSave)

	// new: a staged new record, no record-v2.
	stagedNew, _ := stageEdit(t, rt, fleetRecord("new"), originSave)

	// never-live: a promotion of a new record killed after its index put.
	neverLive := halfDonePromotion(t, rt, host, fleetRecord("never"))

	// half-done: a live record's promotion killed after its index put.
	seedFleetLive(t, rt, fleetRecord("half"))
	halfDone := halfDonePromotion(t, rt, host, fleetToLocal("half"))

	// restored: an archived fleet record restored into staged-v2.
	seedFleetLive(t, rt, fleetRecord("restored"))
	archiveViaApply(t, rt, "restored")
	var restored struct {
		StagedRevision string `json:"staged_revision"`
	}
	decodeResult(t, callSubscription(t, rt, "restore", map[string]any{"subscription_id": "restored"}), &restored)

	// ahead: record-v2 written past its entry, nothing staged.
	if err := rt.saveSubscription(subscriptionRecord{ID: "ahead", Name: "ahead", Source: subscriptionSourceRemote, URL: "https://provider.invalid/old"}); err != nil {
		t.Fatal(err)
	}
	ahead, err := normalizeSubscriptionForStore(subscriptionRecord{ID: "ahead", Name: "ahead", Source: subscriptionSourceRemote, URL: "https://provider.invalid/new"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeRecord(ahead)
	if err != nil {
		t.Fatal(err)
	}
	host.values[recordKey("ahead")] = raw
	ahead, _ = rt.getSubscription("ahead")

	before, _ := entryOf(t, rt, "carried")
	reply := migrateStoreCall(t, rt, map[string]any{"rebuild": true})
	if !reply.Done || reply.Rebuilt == 0 {
		t.Fatalf("rebuild = %+v", reply)
	}

	carried, _ := entryOf(t, rt, "carried")
	if carried.StagedRevision != stagedCarried || carried.StagedSource != before.StagedSource || strings.Join(carried.StagedTags, ",") != "jp" || carried.SelectionVersion != fleetTestVersion || carried.LastFetchAt == "" {
		t.Fatalf("carried fields after the rebuild = %+v", carried)
	}
	if entry, ok := entryOf(t, rt, "new"); !ok || entry.Revision != "" || entry.StagedRevision != stagedNew {
		t.Fatalf("the staged new record = %+v (kept %v)", entry, ok)
	}
	if entry, ok := entryOf(t, rt, "never"); !ok || entry.Revision != neverLive || entry.Source != subscriptionSourceFleet || !entry.Flags.FleetBound {
		t.Fatalf("the never-live half-done promotion = %+v (kept %v)", entry, ok)
	}
	if doc, _ := rt.loadStaged("never"); doc == nil {
		t.Fatal("the rebuild orphaned the never-live roll-forward document")
	}
	if entry, _ := entryOf(t, rt, "half"); entry.Revision != halfDone || entry.Source != subscriptionSourceLocal {
		t.Fatalf("the half-done promotion was reverted: %+v", entry)
	}
	if entry, ok := entryOf(t, rt, "restored"); !ok || entry.Revision != "" || entry.StagedRevision != restored.StagedRevision || entry.StagedRestoredAt == "" {
		t.Fatalf("the staged restore = %+v (kept %v)", entry, ok)
	}
	if idx, _ := rt.loadIndex(); idx.archivedPosition("restored") >= 0 {
		t.Fatal("the rebuild moved the staged restore back to Archived")
	}
	if entry, _ := entryOf(t, rt, "ahead"); entry.Revision != ahead.Revision {
		t.Fatalf("record-v2 ahead of its entry: entry %s, record %s", entry.Revision, ahead.Revision)
	}
	// The roll-forwards still complete after the rebuild.
	for _, id := range []string{"never", "half"} {
		if res := fetchViaMethod(t, rt, id); !res.OK && !strings.Contains(res.Error, codeCatalogueUnavailable) {
			t.Fatalf("fetch of %s after the rebuild: %s", id, res.Error)
		}
		if doc, _ := rt.loadStaged(id); doc != nil {
			t.Fatalf("%s's roll-forward did not complete after the rebuild", id)
		}
	}
}

// TestMigrateStoreDeleteLegacy: delete_legacy refuses until the split store
// has verified and its legacy programs are gone, then deletes
// subscriptions-v1 and records it, and every record stays readable.
func TestMigrateStoreDeleteLegacy(t *testing.T) {
	rt, host := newCASRuntime(t)
	records := legacyFixture(40)
	seedLegacyStore(t, host.kvHostCaller, records)
	res := callSubscription(t, rt, "migrate_store", map[string]any{"delete_legacy": true})
	if res.OK {
		t.Fatalf("delete_legacy on an unmigrated store = %s", res.Result)
	}
	if res := callSubscription(t, rt, "migrate_store", map[string]any{"delete_legacy": true, "rebuild": true}); res.OK {
		t.Fatal("rebuild and delete_legacy together were accepted")
	}
	// One chunk past the verify leaves programs to delete.
	for call := 0; call < 32; call++ {
		reply := migrateStoreCall(t, rt, map[string]any{"chunk": 2})
		if reply.Verified {
			if reply.LegacyPrograms > 0 {
				if res := callSubscription(t, rt, "migrate_store", map[string]any{"delete_legacy": true}); res.OK {
					t.Fatal("delete_legacy ran with legacy programs pending")
				}
			}
			break
		}
	}
	migrateStoreUntilDone(t, rt)
	if _, found := host.values[subscriptionRecordsKey]; !found {
		t.Fatal("the migration deleted the legacy document on its own")
	}
	reply := migrateStoreCall(t, rt, map[string]any{"delete_legacy": true})
	if !reply.DeletedLegacy || !reply.Done {
		t.Fatalf("delete_legacy = %+v", reply)
	}
	if _, found := host.values[subscriptionRecordsKey]; found {
		t.Fatal("subscriptions-v1 survived delete_legacy")
	}
	if idx, _ := rt.loadIndex(); idx.Migration != nil && idx.Migration.Legacy != "" {
		t.Fatalf("the index still names the legacy document: %+v", idx.Migration)
	}
	for _, rec := range records {
		if _, err := rt.getSubscription(rec.ID); err != nil {
			t.Fatalf("%s after delete_legacy: %v", rec.ID, err)
		}
	}
	// Running it again is harmless.
	if reply := migrateStoreCall(t, rt, map[string]any{"delete_legacy": true}); !reply.DeletedLegacy {
		t.Fatalf("a second delete_legacy = %+v", reply)
	}
}
