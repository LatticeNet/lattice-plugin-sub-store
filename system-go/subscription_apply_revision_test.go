package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// apply_revision (plan section 2.7): the claim first, then the promotion with
// the index first or the delete with the archive first, every refusal an
// error core reads from its own call.

// applyRequest is core's apply_revision payload for a promotion.
func applyRequest(id, revision, expected string) map[string]any {
	return map[string]any{"subscription_id": id, "revision": revision, "expected_revision": expected, "approval_id": "ap-1", "plan_sha256": strings.Repeat("ab", 32)}
}

// stageEdit stages rec over its id through save's write path and answers the
// staged revision and the live one it was staged over.
func stageEdit(t *testing.T, rt *runtime, rec subscriptionRecord, origin writeOrigin) (staged, base string) {
	t.Helper()
	res, err := rt.storeWriteRecord(writeRequest{Record: rec}, origin)
	if err != nil || !res.Staged {
		t.Fatalf("stage %s: %+v %v", rec.ID, res, err)
	}
	return res.StagedRevision, res.BaseRevision
}

// putStagedRaw writes a staged document and the entry's staged facts the
// way an older binary could have left them, bypassing every write guard.
func putStagedRaw(t *testing.T, rt *runtime, rec subscriptionRecord) string {
	t.Helper()
	rec = withRevision(rec)
	idx, err := rt.writableIndex()
	if err != nil {
		t.Fatal(err)
	}
	pos := idx.position(rec.ID)
	base := ""
	now := time.Now().UTC().Format(time.RFC3339)
	if pos >= 0 {
		base = idx.Records[pos].Revision
		idx.Records[pos].setStaged(rec, now)
	} else {
		idx.Records = append(idx.Records, stagedEntryFor(rec, now))
	}
	raw, err := encodeStaged(stagedDocument{Record: rec, BaseRevision: base, StagedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.kvPut(stagedKey(rec.ID), raw); err != nil {
		t.Fatal(err)
	}
	indexRaw, err := encodeIndex(idx, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.putIndexRetrying(idx, indexRaw, false, nil); err != nil {
		t.Fatal(err)
	}
	return rec.Revision
}

// TestApplyRevisionClaimsBeforeWriting: the first host call is the claim,
// carrying core's request exactly as it arrived, and nothing is read or
// written before it answers.
func TestApplyRevisionClaimsBeforeWriting(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	edit := fleetRecord("f")
	edit.DisplayName = "edited"
	staged, base := stageEdit(t, rt, edit, originSave)
	var first string
	var claimed json.RawMessage
	wrapped := &orderedHost{inner: host, onCall: func(method string, params any) {
		if first == "" {
			first = method
			encoded, _ := json.Marshal(params)
			var p struct {
				Request json.RawMessage `json:"request"`
			}
			_ = json.Unmarshal(encoded, &p)
			claimed = p.Request
		}
	}}
	rt.host = wrapped
	payload := applyRequest("f", staged, base)
	raw, _ := json.Marshal(payload)
	res := rt.handleSubscriptionCall(callPayload{Method: "apply_revision", Payload: raw})
	if !res.OK {
		t.Fatalf("apply_revision: %s", res.Error)
	}
	if first != "rpc.call" || host.rpcCalls[0] != subStorePlansService+"/claim_apply" {
		t.Fatalf("the first host call was %q (%v), want the claim", first, host.rpcCalls)
	}
	if string(claimed) != string(raw) {
		t.Fatalf("the claim carried %s, want core's request %s verbatim", claimed, raw)
	}
}

// orderedHost observes every call before passing it on.
type orderedHost struct {
	inner  hostCaller
	onCall func(method string, params any)
}

func (h *orderedHost) call(method string, params any) (json.RawMessage, error) {
	h.onCall(method, params)
	return h.inner.call(method, params)
}

// TestApplyRevisionRefusesWithoutGrant: a claim core does not grant, or one
// that answers granted false, writes nothing.
func TestApplyRevisionRefusesWithoutGrant(t *testing.T) {
	for name, deny := range map[string]func(h *casKVHost){
		"claim refused":  func(h *casKVHost) { h.denyClaim = true },
		"granted false":  func(h *casKVHost) { h.rpc[subStorePlansService+"/claim_apply"] = json.RawMessage(`{"granted":false}`) },
		"no granted key": func(h *casKVHost) { h.rpc[subStorePlansService+"/claim_apply"] = json.RawMessage(`{}`) },
	} {
		t.Run(name, func(t *testing.T) {
			rt, host := newCASRuntime(t)
			seedFleetLive(t, rt, fleetRecord("f"))
			edit := fleetRecord("f")
			edit.DisplayName = "edited"
			staged, base := stageEdit(t, rt, edit, originSave)
			deny(host)
			puts, deletes := host.puts, len(host.deletes)
			res := callSubscription(t, rt, "apply_revision", applyRequest("f", staged, base))
			if res.OK || !strings.HasPrefix(res.Error, applyNotGrantedCode) {
				t.Fatalf("apply without a grant = %+v", res)
			}
			if host.puts != puts || len(host.deletes) != deletes {
				t.Fatal("apply without a grant wrote")
			}
		})
	}
}

// TestApplyRevisionRefusesExpectedRevisionMismatch: the live revision must be
// the one core expects, and the staged document must have been staged over
// it; a staged id that is not the entry's is revision_unknown.
func TestApplyRevisionRefusesExpectedRevisionMismatch(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	edit := fleetRecord("f")
	edit.DisplayName = "edited"
	staged, base := stageEdit(t, rt, edit, originSave)
	puts := host.puts
	if res := callSubscription(t, rt, "apply_revision", applyRequest("f", staged, "rev-other")); res.OK || !strings.HasPrefix(res.Error, expectedRevisionMismatchCode) {
		t.Fatalf("a wrong expected revision = %+v", res)
	}
	if res := callSubscription(t, rt, "apply_revision", applyRequest("f", "rev-gone", base)); res.OK || !strings.HasPrefix(res.Error, revisionUnknownCode) {
		t.Fatalf("an unknown staged revision = %+v", res)
	}
	if host.puts != puts {
		t.Fatal("a refused apply wrote")
	}
}

// TestApplyRevisionPromotesAndClearsStaged: the index names the staged
// revision with its staged facts cleared and its flags recomputed, record-v2
// holds the promoted record, and the staged key is gone.
func TestApplyRevisionPromotesAndClearsStaged(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f", "hk"))
	edit := fleetRecord("f", "jp")
	staged, base := stageEdit(t, rt, edit, originSave)
	var reply map[string]string
	decodeResult(t, callSubscription(t, rt, "apply_revision", applyRequest("f", staged, base)), &reply)
	if reply["subscription_id"] != "f" || reply["revision"] != staged {
		t.Fatalf("reply = %v", reply)
	}
	entry, _ := entryOf(t, rt, "f")
	if entry.Revision != staged || entry.hasStaged() || strings.Join(entry.Tags, ",") != "jp" || !entry.Flags.FleetBound {
		t.Fatalf("promoted entry = %+v", entry)
	}
	if rec, err := rt.getSubscription("f"); err != nil || rec.Revision != staged {
		t.Fatalf("record-v2 = %+v %v", rec, err)
	}
	if _, found := host.values[stagedKey("f")]; found {
		t.Fatal("the staged key survived the promotion")
	}
	if res := callSubscription(t, rt, "apply_revision", applyRequest("f", staged, base)); res.OK || !strings.HasPrefix(res.Error, revisionUnknownCode) {
		t.Fatalf("a second apply of the same grant = %+v", res)
	}
}

// TestApplyRevisionDeleteAction: a delete plan archives the fleet-bound
// record, moves its entry to Archived and removes its record and staged keys.
func TestApplyRevisionDeleteAction(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	edit := fleetRecord("f")
	edit.DisplayName = "pending"
	stageEdit(t, rt, edit, originSave)
	live, _ := entryOf(t, rt, "f")
	req := map[string]any{"subscription_id": "f", "expected_revision": live.Revision, "approval_id": "ap-2", "plan_sha256": "x", "action": "delete"}
	var reply map[string]string
	decodeResult(t, callSubscription(t, rt, "apply_revision", req), &reply)
	if reply["revision"] != live.Revision {
		t.Fatalf("reply = %v", reply)
	}
	idx, _ := rt.loadIndex()
	if idx.position("f") >= 0 || idx.archivedPosition("f") < 0 || idx.Archived[idx.archivedPosition("f")].hasStaged() {
		t.Fatalf("index after the delete: live %d archived %d", idx.position("f"), idx.archivedPosition("f"))
	}
	for _, key := range []string{recordKey("f"), stagedKey("f")} {
		if _, found := host.values[key]; found {
			t.Fatalf("%s survived the delete", key)
		}
	}
	var archived archivedRecord
	if err := json.Unmarshal(host.values[archiveKey("f")], &archived); err != nil || archived.ArchivedAt == "" || archived.DisplayName != "" {
		t.Fatalf("archive = %+v %v", archived, err)
	}
	if res := callSubscription(t, rt, "apply_revision", map[string]any{"subscription_id": "f", "expected_revision": live.Revision, "approval_id": "ap-3", "plan_sha256": "x", "action": "remove"}); res.OK {
		t.Fatal("an unknown action was accepted")
	}
}

// TestApplyRevisionDeleteArchivesFirst: an index conflict that exhausts the
// retry leaves the archive copy, the live record and the index untouched,
// and the next delete overwrites the copy.
func TestApplyRevisionDeleteArchivesFirst(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	live, _ := entryOf(t, rt, "f")
	host.beforePut = conflictingIndexWrites(t, rt, host, 2)
	req := map[string]any{"subscription_id": "f", "expected_revision": live.Revision, "approval_id": "ap-2", "plan_sha256": "x", "action": "delete"}
	res := callSubscription(t, rt, "apply_revision", req)
	if res.OK || !strings.Contains(res.Error, kvConflictCode) {
		t.Fatalf("delete under two conflicts = %+v", res)
	}
	host.beforePut = nil
	if _, found := host.values[archiveKey("f")]; !found {
		t.Fatal("the archive copy was not written first")
	}
	if entry, ok := entryOf(t, rt, "f"); !ok || entry.Revision != live.Revision {
		t.Fatalf("the live entry moved: %+v", entry)
	}
	if _, found := host.values[recordKey("f")]; !found {
		t.Fatal("the live record went")
	}
	if res := callSubscription(t, rt, "apply_revision", req); !res.OK {
		t.Fatalf("the next delete: %s", res.Error)
	}
}

// conflictingIndexWrites answers a beforePut hook that, for the next n index
// puts, lets another record's write land first: a provider record is saved
// under its own id, which moves the index digest the put carries.
func conflictingIndexWrites(t *testing.T, rt *runtime, host *casKVHost, n int) func(string) {
	t.Helper()
	left := n
	busy := false
	return func(key string) {
		if key != storeIndexKey || left == 0 || busy {
			return
		}
		left--
		busy = true
		defer func() { busy = false }()
		value := host.values[storeIndexKey]
		var doc map[string]any
		if json.Unmarshal(value, &doc) != nil {
			t.Fatal("the index does not decode")
		}
		// Another writer's index write: the same document with one more
		// key, so the stored digest moves and the entry under test does not.
		doc["concurrent_writes"] = n - left
		moved, _ := json.Marshal(doc)
		host.values[storeIndexKey] = moved
	}
}

// TestApplyRevisionRetriesIndexOnConflict: one concurrent index write makes
// the promotion re-read the index and re-apply its own delta, keeping the
// other writer's change.
func TestApplyRevisionRetriesIndexOnConflict(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	if err := rt.saveSubscription(subscriptionRecord{ID: "r", Name: "r", Source: subscriptionSourceRemote, URL: "https://provider.invalid/r"}); err != nil {
		t.Fatal(err)
	}
	edit := fleetRecord("f")
	edit.DisplayName = "edited"
	staged, base := stageEdit(t, rt, edit, originSave)
	once := false
	host.beforePut = func(key string) {
		if key != storeIndexKey || once {
			return
		}
		once = true
		// noteFetchOutcome of another record lands between the promotion's
		// index read and its put.
		rt2 := &runtime{host: host.kvHostCaller}
		rt2.noteFetchOutcome("r", fetchOutcome{at: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)})
	}
	if res := callSubscription(t, rt, "apply_revision", applyRequest("f", staged, base)); !res.OK {
		t.Fatalf("apply under one conflict: %s", res.Error)
	}
	if host.conflicts != 1 {
		t.Fatalf("conflicts = %d, want 1", host.conflicts)
	}
	if entry, _ := entryOf(t, rt, "f"); entry.Revision != staged {
		t.Fatalf("the promotion did not land: %+v", entry)
	}
	if entry, _ := entryOf(t, rt, "r"); entry.LastFetchAt == "" {
		t.Fatal("the retry reverted the other record's bookkeeping")
	}
}

// TestApplyRevisionIndexConflictExhaustionLeavesStoreUnchanged: with the
// index put first, a second conflict writes nothing at all: the reply is
// kv_conflict, record-v2 and the staged document stand, and the index still
// names the live revision with its staged facts.
func TestApplyRevisionIndexConflictExhaustionLeavesStoreUnchanged(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	edit := fleetRecord("f")
	edit.DisplayName = "edited"
	staged, base := stageEdit(t, rt, edit, originSave)
	record := append([]byte(nil), host.values[recordKey("f")]...)
	host.beforePut = conflictingIndexWrites(t, rt, host, 2)
	res := callSubscription(t, rt, "apply_revision", applyRequest("f", staged, base))
	if res.OK || !strings.HasPrefix(res.Error, kvConflictCode) {
		t.Fatalf("apply under two conflicts = %+v", res)
	}
	host.beforePut = nil
	if string(host.values[recordKey("f")]) != string(record) {
		t.Fatal("record-v2 moved under an exhausted promotion")
	}
	entry, _ := entryOf(t, rt, "f")
	if entry.Revision != base || entry.StagedRevision != staged {
		t.Fatalf("the index moved: %+v", entry)
	}
	if doc, _ := rt.loadStaged("f"); doc == nil || doc.Record.Revision != staged {
		t.Fatal("the staged document went")
	}
	if res := callSubscription(t, rt, "apply_revision", applyRequest("f", staged, base)); !res.OK {
		t.Fatalf("proposing again: %s", res.Error)
	}
}

// TestApplyRevisionKillAfterIndexPutRollsForward: a kill between the index
// put and the record put leaves a half-done promotion. The render half: render
// serves the staged document, reports its revision as live_revision and writes
// nothing. The fetch half: the next fetch completes the promotion with two
// writes beyond the usual fetch.
func TestApplyRevisionKillAfterIndexPutRollsForward(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	host.failPut = func(key string) bool { return key == recordKey("f") }
	staged, base := stageEdit(t, rt, fleetToLocal("f"), originSave)
	res := callSubscription(t, rt, "apply_revision", applyRequest("f", staged, base))
	if res.OK {
		t.Fatal("the killed apply reported success")
	}
	host.failPut = nil
	if entry, _ := entryOf(t, rt, "f"); entry.Revision != staged {
		t.Fatalf("the index put did not land before the kill: %+v", entry)
	}

	t.Run("render", func(t *testing.T) {
		puts, deletes := host.puts, len(host.deletes)
		out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "f", "format": "plain", "target": "URI"})
		if !res.OK {
			t.Fatalf("render over the half-done promotion: %s", res.Error)
		}
		if out.LiveRevision != staged || !strings.Contains(out.Content, "HK-01") {
			t.Fatalf("render served live %q content %q, want the staged local record at %s", out.LiveRevision, out.Content, staged)
		}
		if host.puts != puts || len(host.deletes) != deletes {
			t.Fatal("render wrote during a half-done promotion")
		}
		if doc, _ := rt.loadStaged("f"); doc == nil {
			t.Fatal("render completed the promotion")
		}
	})

	t.Run("fetch", func(t *testing.T) {
		puts, deletes := host.puts, len(host.deletes)
		if res := fetchViaMethod(t, rt, "f"); !res.OK {
			t.Fatalf("fetch over the half-done promotion: %s", res.Error)
		}
		// The usual fetch writes the index once; the roll-forward adds the
		// record put and the staged key's deletion.
		if host.puts-puts != 2 || len(host.deletes)-deletes != 1 {
			t.Fatalf("fetch wrote %d puts and %d deletes, want 2 and 1", host.puts-puts, len(host.deletes)-deletes)
		}
		if rec, err := rt.getSubscription("f"); err != nil || rec.Revision != staged || rec.Source != subscriptionSourceLocal {
			t.Fatalf("record-v2 after the roll-forward = %+v %v", rec, err)
		}
		if doc, _ := rt.loadStaged("f"); doc != nil {
			t.Fatal("the staged document survived the roll-forward")
		}
		if entry, _ := entryOf(t, rt, "f"); entry.Revision != staged || entry.LastFetchAt == "" {
			t.Fatalf("entry after the roll-forward = %+v", entry)
		}
	})
}

// TestApplyRevisionRaceWithRollingForwardFetch: a refresh that completes the
// promotion between the apply's index put and its record put is benign: the
// apply writes the same record and tolerates the missing staged key.
func TestApplyRevisionRaceWithRollingForwardFetch(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	staged, base := stageEdit(t, rt, fleetToLocal("f"), originSave)
	raced := false
	host.beforePut = func(key string) {
		if key != recordKey("f") || raced {
			return
		}
		raced = true
		other := &runtime{host: host}
		idx, err := other.loadIndex()
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := other.storeLoadRecord(idx, "f", false)
		if err != nil || loaded.State != recordPromotionPending {
			t.Fatalf("the racing fetch saw state %d: %v", loaded.State, err)
		}
		if err := other.storeCompletePromotion(loaded); err != nil {
			t.Fatal(err)
		}
	}
	if res := callSubscription(t, rt, "apply_revision", applyRequest("f", staged, base)); !res.OK {
		t.Fatalf("apply racing a roll-forward: %s", res.Error)
	}
	host.beforePut = nil
	if !raced {
		t.Fatal("the race did not happen")
	}
	loaded, err := rt.storeLoadRecord(mustIndex(t, rt), "f", true)
	if err != nil || loaded.State != recordConsistent || loaded.LiveRevision != staged || loaded.Staged != nil {
		t.Fatalf("after the race: state %d revision %s staged %v %v", loaded.State, loaded.LiveRevision, loaded.Staged, err)
	}
}

func mustIndex(t *testing.T, rt *runtime) *indexDocument {
	t.Helper()
	idx, err := rt.loadIndex()
	if err != nil || idx == nil {
		t.Fatalf("index: %v", err)
	}
	return idx
}

// TestApplyRevisionRefusesFleetToLegacySource: a staged document an older
// binary wrote that takes a legacy source over a fleet-bound record is
// refused at the promotion.
func TestApplyRevisionRefusesFleetToLegacySource(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	live, _ := entryOf(t, rt, "f")
	staged := putStagedRaw(t, rt, subscriptionRecord{ID: "f", Name: "f", Source: subscriptionSourceVPNCore})
	puts := host.puts
	res := callSubscription(t, rt, "apply_revision", applyRequest("f", staged, live.Revision))
	if res.OK || !strings.HasPrefix(res.Error, refusedFleetToLegacy) {
		t.Fatalf("a fleet record promoted to the export = %+v", res)
	}
	if host.puts != puts {
		t.Fatal("the refused promotion wrote")
	}
}

// TestApplyRevisionRefusesFleetBoundCollectionGatheringLegacyMember: a
// fleet-bound collection re-staged to gather a live legacy record is refused,
// even with no fleet member left (the membership half).
func TestApplyRevisionRefusesFleetBoundCollectionGatheringLegacyMember(t *testing.T) {
	rt, _ := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	if err := rt.saveSubscription(subscriptionRecord{ID: "l", Name: "l", Source: subscriptionSourceVPNCore}); err != nil {
		t.Fatal(err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "p", Name: "p", Source: subscriptionSourceRemote, URL: "https://provider.invalid/p"}); err != nil {
		t.Fatal(err)
	}
	seedFleetLive(t, rt, subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f"}})
	live, _ := entryOf(t, rt, "c")
	staged := putStagedRaw(t, rt, subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"l", "p"}})
	res := callSubscription(t, rt, "apply_revision", applyRequest("c", staged, live.Revision))
	if res.OK || !strings.HasPrefix(res.Error, refusedFleetToLegacy) {
		t.Fatalf("a fleet-bound collection promoted to gather a legacy record = %+v", res)
	}
	// The same refusal at save, from the live flag.
	_, saved := saveViaMethod(t, rt, subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"l", "p"}}, "")
	if r, refused := refusalOf(saved); !refused || r.Code != refusedFleetToLegacy {
		t.Fatalf("save of the same collection = %+v %s", r, saved.Error)
	}
}

// TestApplyRevisionResolvesGatheredMembersLive: a collection C' = [F, L] can
// be saved and staged while L's migration L' is pending (save resolves the
// other records at their staged state), but apply_revision resolves them
// live: C' is refused until L' is promoted, whether live C is provider-only
// or already fleet-bound, and then promotes.
func TestApplyRevisionResolvesGatheredMembersLive(t *testing.T) {
	for name, liveMembers := range map[string][]string{
		"live C provider-only": {"p"},
		"live C fleet-bound":   {"f"},
	} {
		t.Run(name, func(t *testing.T) {
			rt, _ := newCASRuntime(t)
			seedFleetLive(t, rt, fleetRecord("f"))
			for _, rec := range []subscriptionRecord{
				{ID: "l", Name: "l", Source: subscriptionSourceVPNCore},
				{ID: "p", Name: "p", Source: subscriptionSourceRemote, URL: "https://provider.invalid/p"},
			} {
				if err := rt.saveSubscription(rec); err != nil {
					t.Fatal(err)
				}
			}
			seedFleetLive(t, rt, subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: liveMembers})
			var migrated migrateRecordReply
			decodeResult(t, callSubscription(t, rt, "migrate_record", map[string]any{"subscription_id": "l"}), &migrated)
			if migrated.StagedRevision == "" {
				t.Fatalf("migrate_record = %+v", migrated)
			}
			cLive, _ := entryOf(t, rt, "c")
			lLive, _ := entryOf(t, rt, "l")
			reply, res := saveViaMethod(t, rt, subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f", "l"}}, "")
			if !res.OK || !reply.Staged {
				t.Fatalf("C' = [F, L] with L' pending = %+v %s", reply, res.Error)
			}
			res = callSubscription(t, rt, "apply_revision", applyRequest("c", reply.StagedRevision, cLive.Revision))
			if res.OK || !strings.HasPrefix(res.Error, refusedFleetToLegacy) || !strings.Contains(res.Error, "legacy records l;") {
				t.Fatalf("C' promoted before L' = %+v", res)
			}
			if res := callSubscription(t, rt, "apply_revision", applyRequest("l", migrated.StagedRevision, lLive.Revision)); !res.OK {
				t.Fatalf("L' promotion: %s", res.Error)
			}
			if res := callSubscription(t, rt, "apply_revision", applyRequest("c", reply.StagedRevision, cLive.Revision)); !res.OK {
				t.Fatalf("C' after L': %s", res.Error)
			}
			if entry, _ := entryOf(t, rt, "c"); !entry.Flags.FleetBound || entry.Flags.OwnerCredentials {
				t.Fatalf("live C after both promotions = %+v", entry.Flags)
			}
		})
	}
}

// TestApplyRevisionPromotesFirstRecordOfMultiRecordMigration: apply_revision
// runs no mixing guard, so the first promotion of a two-record migration
// succeeds and leaves the collection mixed (both flags live) until the second.
func TestApplyRevisionPromotesFirstRecordOfMultiRecordMigration(t *testing.T) {
	rt, _ := newCASRuntime(t)
	for _, rec := range []subscriptionRecord{
		{ID: "l1", Name: "l1", Source: subscriptionSourceVPNCore},
		{ID: "l2", Name: "l2", Source: subscriptionSourceVPNCore},
		{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"l1", "l2"}},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatal(err)
		}
	}
	revisions := map[string][2]string{}
	for _, id := range []string{"l1", "l2"} {
		live, _ := entryOf(t, rt, id)
		var migrated migrateRecordReply
		decodeResult(t, callSubscription(t, rt, "migrate_record", map[string]any{"subscription_id": id}), &migrated)
		if migrated.StagedRevision == "" {
			t.Fatalf("migrate_record %s = %+v", id, migrated)
		}
		revisions[id] = [2]string{migrated.StagedRevision, live.Revision}
	}
	if res := callSubscription(t, rt, "apply_revision", applyRequest("l1", revisions["l1"][0], revisions["l1"][1])); !res.OK {
		t.Fatalf("the first promotion: %s", res.Error)
	}
	if entry, _ := entryOf(t, rt, "c"); !entry.Flags.FleetBound || !entry.Flags.OwnerCredentials {
		t.Fatalf("between the promotions the collection is %+v, want both flags", entry.Flags)
	}
	if res := callSubscription(t, rt, "apply_revision", applyRequest("l2", revisions["l2"][0], revisions["l2"][1])); !res.OK {
		t.Fatalf("the second promotion: %s", res.Error)
	}
	if entry, _ := entryOf(t, rt, "c"); !entry.Flags.FleetBound || entry.Flags.OwnerCredentials {
		t.Fatalf("after both promotions the collection is %+v", entry.Flags)
	}
}

// TestApplyRevisionRefusesPromotionThatBindsAFile: a staged fleet record
// whose promotion would make a file's node source fleet-bound (here through a
// collection that gathers it by tag) is refused, naming the file.
func TestApplyRevisionRefusesPromotionThatBindsAFile(t *testing.T) {
	rt, host := newCASRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "hk-coll", Name: "hk-coll", Kind: kindCollection, MemberTags: []string{"hk"}}); err != nil {
		t.Fatal(err)
	}
	staged, _ := stageEdit(t, rt, fleetRecord("f", "hk"), originSave)
	// An older binary let a file over the collection in after the staging.
	if err := rt.saveSubscription(subscriptionRecord{ID: "cfg", Name: "cfg", Kind: kindFile, NodeSource: "hk-coll", Content: "proxies: []"}); err != nil {
		t.Fatal(err)
	}
	puts := host.puts
	res := callSubscription(t, rt, "apply_revision", applyRequest("f", staged, ""))
	if res.OK || !strings.HasPrefix(res.Error, refusedFleetFileUnavailable) || !strings.Contains(res.Error, "cfg") {
		t.Fatalf("a promotion that binds a file = %+v", res)
	}
	if host.puts != puts {
		t.Fatal("the refused promotion wrote")
	}
}
