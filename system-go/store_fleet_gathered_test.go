package main

import (
	"maps"
	"slices"
	"testing"
)

// S2-SR-1: a write that changes what a fleet-bound collection gathers stages
// for a plan, even when the written record is a provider record that is not
// fleet-bound itself. Core serves a fleet-bound collection's provider nodes
// to every identity holder unvalidated, so before this a substore:admin
// caller could edit a member, or save a record carrying a member tag, and
// the next render served the new nodes with no plan.

// gatheredStore seeds a store with:
//
//   - f, a fleet sub tagged hk, live;
//   - r, a remote sub, live, listed explicitly by c;
//   - r4, a remote sub tagged hk, live before tc existed;
//   - plain, a remote sub nothing gathers;
//   - c, a collection listing f and r, fleet-bound live;
//   - tc, a collection gathering the tag hk, fleet-bound live.
func gatheredStore(t *testing.T) *runtime {
	t.Helper()
	rt, _ := newCASRuntime(t)
	for _, rec := range []subscriptionRecord{
		{ID: "r", Name: "r", Source: subscriptionSourceRemote, URL: "https://provider.invalid/r"},
		{ID: "r4", Name: "r4", Source: subscriptionSourceRemote, URL: "https://provider.invalid/r4", Tags: []string{"hk"}},
		{ID: "plain", Name: "plain", Source: subscriptionSourceRemote, URL: "https://provider.invalid/plain"},
	} {
		res, err := rt.storeWriteRecord(writeRequest{Record: rec}, originSave)
		if err != nil || !res.landed() || res.Staged {
			t.Fatalf("seed %s: %+v %v", rec.ID, res, err)
		}
	}
	seedFleetLive(t, rt, fleetRecord("f", "hk"))
	seedFleetLive(t, rt, subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f", "r"}})
	seedFleetLive(t, rt, subscriptionRecord{ID: "tc", Name: "tc", Kind: kindCollection, MemberTags: []string{"hk"}})
	for _, id := range []string{"c", "tc"} {
		if entry, ok := entryOf(t, rt, id); !ok || !entry.Flags.FleetBound || entry.StagedRevision != "" {
			t.Fatalf("%s is not fleet-bound live: %+v", id, entry)
		}
	}
	return rt
}

// requireStaged asserts a save answered staged and left the live revision,
// if any, where it was.
func requireStaged(t *testing.T, rt *runtime, reply saveReply, res response, id, liveBefore string) {
	t.Helper()
	if !res.OK || !reply.Saved || !reply.Staged || reply.StagedRevision == "" {
		t.Fatalf("%s: the write was not staged: %+v %s %s", id, reply, res.Error, refusalText(res))
	}
	entry, ok := entryOf(t, rt, id)
	if !ok || entry.Revision != liveBefore || entry.StagedRevision != reply.StagedRevision {
		t.Fatalf("%s: the live revision moved before any plan: %+v (live was %q)", id, entry, liveBefore)
	}
}

// TestFleetGatheredMemberEditStages is the reviewer's first probe: an edit of
// a remote record a fleet-bound collection lists explicitly.
func TestFleetGatheredMemberEditStages(t *testing.T) {
	rt := gatheredStore(t)
	live, _ := entryOf(t, rt, "r")
	edit := subscriptionRecord{ID: "r", Name: "r", Source: subscriptionSourceRemote, URL: "https://attacker.invalid/r"}
	reply, res := saveViaMethod(t, rt, edit, live.Revision)
	requireStaged(t, rt, reply, res, "r", live.Revision)
	stored, err := rt.getSubscription("r")
	if err != nil || stored.URL != "https://provider.invalid/r" {
		t.Fatalf("the live member moved before any plan: %+v %v", stored, err)
	}
	doc, err := rt.loadStaged("r")
	if err != nil || doc == nil || doc.Record.URL != "https://attacker.invalid/r" || doc.BaseRevision != live.Revision {
		t.Fatalf("staged member = %+v %v", doc, err)
	}
	// The staged member is what a plan promotes.
	promoteStaged(t, rt, "r")
	if stored, err := rt.getSubscription("r"); err != nil || stored.URL != "https://attacker.invalid/r" {
		t.Fatalf("the approved member is not live: %+v %v", stored, err)
	}
}

// TestFleetGatheredTagJoinStages is the reviewer's second probe: a new
// record carrying a member tag of a fleet-bound collection.
func TestFleetGatheredTagJoinStages(t *testing.T) {
	rt := gatheredStore(t)
	joined := subscriptionRecord{ID: "r2", Name: "r2", Source: subscriptionSourceRemote, URL: "https://attacker.invalid/r2", Tags: []string{"hk"}}
	reply, res := saveViaMethod(t, rt, joined, "")
	requireStaged(t, rt, reply, res, "r2", "")
	if _, err := rt.getSubscription("r2"); err == nil {
		t.Fatal("the joining record is live before any plan")
	}
}

// TestFleetGatheredTagAddedToAnExistingRecordStages: a live record nothing
// gathered takes a member tag.
func TestFleetGatheredTagAddedToAnExistingRecordStages(t *testing.T) {
	rt := gatheredStore(t)
	live, _ := entryOf(t, rt, "plain")
	tagged := subscriptionRecord{ID: "plain", Name: "plain", Source: subscriptionSourceRemote, URL: "https://provider.invalid/plain", Tags: []string{"hk"}}
	reply, res := saveViaMethod(t, rt, tagged, live.Revision)
	requireStaged(t, rt, reply, res, "plain", live.Revision)
}

// TestFleetGatheredTagRemovedStages: a member leaves a fleet-bound collection
// by dropping its tag, which removes nodes identity holders receive.
func TestFleetGatheredTagRemovedStages(t *testing.T) {
	rt := gatheredStore(t)
	live, _ := entryOf(t, rt, "r4")
	untagged := subscriptionRecord{ID: "r4", Name: "r4", Source: subscriptionSourceRemote, URL: "https://provider.invalid/r4"}
	reply, res := saveViaMethod(t, rt, untagged, live.Revision)
	requireStaged(t, rt, reply, res, "r4", live.Revision)
}

// TestFleetGatheredRecordSavedUnderAListedIDStages: membership follows ids,
// so a rename lands as a new record under the id a collection lists. A
// fleet-bound collection that lists an id with no record yet gathers
// whatever is saved under it next.
func TestFleetGatheredRecordSavedUnderAListedIDStages(t *testing.T) {
	rt := gatheredStore(t)
	seedFleetLive(t, rt, subscriptionRecord{ID: "c2", Name: "c2", Kind: kindCollection, Members: []string{"f", "renamed"}})
	if entry, _ := entryOf(t, rt, "c2"); !entry.Flags.FleetBound {
		t.Fatalf("c2 is not fleet-bound: %+v", entry)
	}
	renamed := subscriptionRecord{ID: "renamed", Name: "renamed", Source: subscriptionSourceRemote, URL: "https://attacker.invalid/renamed"}
	reply, res := saveViaMethod(t, rt, renamed, "")
	requireStaged(t, rt, reply, res, "renamed", "")
}

// TestFleetGatheredRecordThatStopsBeingASubStages: a member a fleet-bound
// collection lists becomes a collection itself, which the collection can no
// longer gather.
func TestFleetGatheredRecordThatStopsBeingASubStages(t *testing.T) {
	rt := gatheredStore(t)
	live, _ := entryOf(t, rt, "r")
	reshaped := subscriptionRecord{ID: "r", Name: "r", Kind: kindCollection, Members: []string{"plain"}}
	reply, res := saveViaMethod(t, rt, reshaped, live.Revision)
	requireStaged(t, rt, reply, res, "r", live.Revision)
}

// TestFleetGatheredDeleteRequiresPlan: deleting a record a fleet-bound
// collection gathers, explicitly or by tag, is refused like deleting a
// fleet-bound record, naming the collections, and leaves it live.
func TestFleetGatheredDeleteRequiresPlan(t *testing.T) {
	rt := gatheredStore(t)
	for id, want := range map[string][]string{"r": {"c"}, "r4": {"tc"}} {
		res := callSubscription(t, rt, "delete", map[string]any{"subscription_id": id})
		refused, ok := refusalOf(res)
		if !ok || refused.Code != refusedDeleteRequiresPlan || !slices.Equal(refused.Collections, want) {
			t.Fatalf("delete %s = %+v %s, want %s naming %v", id, refused, res.Error, refusedDeleteRequiresPlan, want)
		}
		if entry, ok := entryOf(t, rt, id); !ok || !entry.hasLiveRevision() {
			t.Fatalf("%s left the live list: %+v", id, entry)
		}
	}
	// A record nothing fleet-bound gathers still deletes directly.
	if res := callSubscription(t, rt, "delete", map[string]any{"subscription_id": "plain"}); !res.OK || isRefusedResponse(res) {
		t.Fatalf("delete plain = %s %s", res.Error, refusalText(res))
	}
}

// TestFleetGatheredRestoreStages: restoring an archived record a fleet-bound
// collection gathers brings nodes back to identity holders, so it stages.
func TestFleetGatheredRestoreStages(t *testing.T) {
	rt, _ := newCASRuntime(t)
	back := subscriptionRecord{ID: "back", Name: "back", Source: subscriptionSourceRemote, URL: "https://provider.invalid/back", Tags: []string{"hk"}}
	if res, err := rt.storeWriteRecord(writeRequest{Record: back}, originSave); err != nil || !res.landed() || res.Staged {
		t.Fatalf("seed back: %+v %v", res, err)
	}
	if err := rt.deleteSubscription("back"); err != nil {
		t.Fatal(err)
	}
	seedFleetLive(t, rt, fleetRecord("f", "hk"))
	seedFleetLive(t, rt, subscriptionRecord{ID: "tc", Name: "tc", Kind: kindCollection, MemberTags: []string{"hk"}})
	res := callSubscription(t, rt, "restore", map[string]any{"subscription_id": "back"})
	var reply struct {
		Restored bool `json:"restored"`
		Staged   bool `json:"staged"`
	}
	decodeResult(t, res, &reply)
	if !reply.Restored || !reply.Staged {
		t.Fatalf("restore = %s, want staged", res.Result)
	}
	if entry, ok := entryOf(t, rt, "back"); !ok || entry.hasLiveRevision() || entry.StagedRevision == "" {
		t.Fatalf("the restored record went live before any plan: %+v", entry)
	}
}

// TestFleetGatheredImportStages: a batch write (import, migrate) of a record
// a fleet-bound collection gathers stages it, as save does.
func TestFleetGatheredImportStages(t *testing.T) {
	rt := gatheredStore(t)
	results, err := rt.storeWriteRecords([]writeRequest{
		{Record: subscriptionRecord{ID: "r", Name: "r", Source: subscriptionSourceRemote, URL: "https://attacker.invalid/r"}},
		{Record: subscriptionRecord{ID: "imported", Name: "imported", Source: subscriptionSourceRemote, URL: "https://attacker.invalid/i", Tags: []string{"hk"}}},
		{Record: subscriptionRecord{ID: "other", Name: "other", Source: subscriptionSourceRemote, URL: "https://provider.invalid/other"}},
	}, originImport)
	if err != nil {
		t.Fatal(err)
	}
	staged := map[string]bool{}
	for _, res := range results {
		if !res.landed() {
			t.Fatalf("%s did not land: %+v", res.ID, res)
		}
		staged[res.ID] = res.Staged
	}
	if want := map[string]bool{"r": true, "imported": true, "other": false}; !maps.Equal(staged, want) {
		t.Fatalf("staged = %v, want %v", staged, want)
	}
}

// TestFleetGatheredLeavesOtherProviderWritesLive: the rule reads fleet-bound
// collections only. A record no collection gathers, and a member of a
// collection that gathers no fleet record, still write live.
func TestFleetGatheredLeavesOtherProviderWritesLive(t *testing.T) {
	rt := gatheredStore(t)
	seedFleetLive(t, rt, subscriptionRecord{ID: "providers", Name: "providers", Kind: kindCollection, Members: []string{"plain"}, MemberTags: []string{"eu"}})
	if entry, _ := entryOf(t, rt, "providers"); entry.Flags.FleetBound || entry.StagedRevision != "" {
		t.Fatalf("a provider-only collection is fleet-bound or staged: %+v", entry)
	}
	live, _ := entryOf(t, rt, "plain")
	edit := subscriptionRecord{ID: "plain", Name: "plain", Source: subscriptionSourceRemote, URL: "https://provider.invalid/plain2"}
	reply, res := saveViaMethod(t, rt, edit, live.Revision)
	if !res.OK || !reply.Saved || reply.Staged {
		t.Fatalf("a member of a provider-only collection staged: %+v %s %s", reply, res.Error, refusalText(res))
	}
	joined := subscriptionRecord{ID: "eu1", Name: "eu1", Source: subscriptionSourceRemote, URL: "https://provider.invalid/eu1", Tags: []string{"eu"}}
	if reply, res := saveViaMethod(t, rt, joined, ""); !res.OK || !reply.Saved || reply.Staged {
		t.Fatalf("a tag join of a provider-only collection staged: %+v %s %s", reply, res.Error, refusalText(res))
	}
}
