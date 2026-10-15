package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LatticeNet/lattice-sdk/model"
)

// The write methods' S2 rules (plan sections 1.2, 2.6, 2.7 and 3): staging on
// every origin, the guards every origin runs, the structured refusal, the
// half-done promotion no write may land on, and probe's read-only roll-over.

// hasEntry reports whether id has a live-list entry, on a store that may
// have no index yet.
func hasEntry(t *testing.T, rt *runtime, id string) bool {
	t.Helper()
	idx, err := rt.loadIndex()
	if err != nil {
		t.Fatal(err)
	}
	return idx != nil && idx.position(id) >= 0
}

// halfDonePromotion stages next over its id and runs the promotion until its
// index put has landed, then kills the invocation at the record put: the
// index names the staged revision while record-v2 is at the old revision (or
// missing, for a never-live record) and staged-v2 still holds the document.
// It answers the promoted revision.
func halfDonePromotion(t *testing.T, rt *runtime, host *casKVHost, next subscriptionRecord) string {
	t.Helper()
	res, err := rt.storeWriteRecord(writeRequest{Record: next}, originImport)
	if err != nil || !res.Staged {
		t.Fatalf("stage %s: %+v %v", next.ID, res, err)
	}
	idx, err := rt.writableIndex()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := rt.loadStaged(next.ID)
	if err != nil || doc == nil {
		t.Fatalf("staged %s: %v %v", next.ID, doc, err)
	}
	host.failPut = func(key string) bool { return key == recordKey(next.ID) }
	if err := rt.storePromote(idx, doc); err == nil {
		t.Fatal("the killed promotion reported success")
	}
	host.failPut = nil
	entry, _ := entryOf(t, rt, next.ID)
	if entry.Revision != res.StagedRevision || entry.hasStaged() {
		t.Fatalf("after the index put the entry is %+v, want revision %s with nothing staged", entry, res.StagedRevision)
	}
	return res.StagedRevision
}

// localNode is one parseable node a local record can carry.
const localNode = "vless://11111111-1111-4111-8111-111111111111@a.example:443?security=reality&sni=a.com&fp=chrome&pbk=x#HK-01"

// fleetToLocal is fleet record id switched to a local record: fleet-bound
// before the write, so it stages, and a provider record once promoted.
func fleetToLocal(id string) subscriptionRecord {
	return subscriptionRecord{ID: id, Name: id, Source: subscriptionSourceLocal, Content: localNode}
}

func probeStructuredStep(field string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "Structured Filter", "args": map[string]any{
		"mode": "keep", "predicates": []map[string]any{{"field": field, "op": "true"}},
	}})
	return raw
}

// TestSaveFleetValidatesPredicateFields: a fleet record's predicates are
// read against the catalogue when one reads a row block, an unknown field is
// refused by the strict compile as a structured refusal naming the step, a
// plugin-only field and a pushable one both save, and a record with no such
// predicate makes no catalogue call.
func TestSaveFleetValidatesPredicateFields(t *testing.T) {
	rt, host := newCASRuntime(t)
	page, _ := json.Marshal(model.LineCatalogueResponse{CatalogueVersion: fleetTestVersion, Rows: []model.LineCatalogueRow{}, SelectorFields: model.LineCatalogueSelectorFields()})
	host.rpc[fleetCatalogueService+"/"+fleetCatalogueMethod] = page

	plain := fleetRecord("plain")
	plain.Process = []json.RawMessage{
		json.RawMessage(`{"type":"Structured Filter","args":{"predicates":[{"field":"country","op":"in","values":["JP"]}]}}`),
		json.RawMessage(`{"type":"Structured Filter","args":{"predicates":[{"field":"city","op":"in","values":["Tokyo"]}]}}`),
	}
	reply, res := saveViaMethod(t, rt, plain, "")
	if !res.OK || !reply.Staged {
		t.Fatalf("a fleet record over a pushable and a plugin-only field = %+v %s", reply, res.Error)
	}
	if len(host.rpcCalls) != 0 {
		t.Fatalf("a record with no predicate over a row block read the catalogue: %v", host.rpcCalls)
	}

	unknown := fleetRecord("unknown")
	unknown.Process = []json.RawMessage{json.RawMessage(`{"type":"Structured Filter","args":{"predicates":[{"field":"favourite_colour","op":"in","values":["https://leak.invalid/x"]}]}}`)}
	_, res = saveViaMethod(t, rt, unknown, "")
	r, refused := refusalOf(res)
	if !refused || r.Code != refusedStructuredArgs || !strings.Contains(r.Message, "step 1") || strings.Contains(r.Message, "://") || strings.Contains(r.Message, "favourite_colour") {
		t.Fatalf("an unknown predicate field = %+v %s", r, res.Error)
	}
	if hasEntry(t, rt, "unknown") {
		t.Fatal("a refused save left an entry")
	}

	probing := fleetRecord("probing")
	probing.Process = []json.RawMessage{probeStructuredStep("probe_udp_ok")}
	if reply, res := saveViaMethod(t, rt, probing, ""); !res.OK || !reply.Staged {
		t.Fatalf("a probe predicate on a core that fills probes = %+v %s", reply, res.Error)
	}
	if len(host.rpcCalls) != 1 || host.rpcCalls[0] != fleetCatalogueService+"/"+fleetCatalogueMethod {
		t.Fatalf("the probe predicate's check made %v, want one catalogue page", host.rpcCalls)
	}
}

// TestSaveRefusesProbePredicateWhileUnavailable: while the catalogue lists
// probe as unavailable, a fleet record with an enabled predicate over the
// probe block is refused, naming the step and the field; a disabled one, and
// a non-fleet record, are not checked.
func TestSaveRefusesProbePredicateWhileUnavailable(t *testing.T) {
	rt, host := newCASRuntime(t)
	page, _ := json.Marshal(model.LineCatalogueResponse{CatalogueVersion: fleetTestVersion, Rows: []model.LineCatalogueRow{}, Unavailable: []string{model.LineCatalogueUnavailableProbe}})
	host.rpc[fleetCatalogueService+"/"+fleetCatalogueMethod] = page
	for _, field := range []string{"probe_udp_ok", "probe_vantage", "probe_cold_p50_ms", "probe_passed_hours"} {
		rec := fleetRecord("p-" + strings.ReplaceAll(field, "_", "-"))
		step := probeStructuredStep(field)
		switch field {
		case "probe_vantage":
			step = json.RawMessage(`{"type":"Structured Filter","args":{"predicates":[{"field":"probe_vantage","op":"in","values":["control-plane"]}]}}`)
		case "probe_cold_p50_ms", "probe_passed_hours":
			step = json.RawMessage(`{"type":"Structured Filter","args":{"predicates":[{"field":"` + field + `","op":"lte","value":24}]}}`)
		}
		rec.Process = []json.RawMessage{json.RawMessage(`{"type":"Structured Filter","args":{"predicates":[{"field":"country","op":"in","values":["JP"]}]}}`), step}
		_, res := saveViaMethod(t, rt, rec, "")
		r, refused := refusalOf(res)
		if !refused || r.Code != refusedProbeUnavailable || !strings.Contains(r.Message, "step 2") || !strings.Contains(r.Message, field) {
			t.Fatalf("%s while probe is unavailable = %+v %s", field, r, res.Error)
		}
		if hasEntry(t, rt, rec.ID) {
			t.Fatalf("a refused %s save left an entry", field)
		}
	}
	disabled := fleetRecord("disabled")
	disabled.Process = []json.RawMessage{json.RawMessage(`{"type":"Structured Filter","disabled":true,"args":{"predicates":[{"field":"probe_udp_ok","op":"true"}]}}`)}
	calls := len(host.rpcCalls)
	if reply, res := saveViaMethod(t, rt, disabled, ""); !res.OK || !reply.Staged || len(host.rpcCalls) != calls {
		t.Fatalf("a disabled probe predicate = %+v %s (catalogue calls %d -> %d)", reply, res.Error, calls, len(host.rpcCalls))
	}
}

// TestFleetRecordRefusesLinkModeScriptStepOnWrite is the save and import
// halves of the plan's TestFleetRecordRefusesLinkModeScriptStep, whose render
// and member-block halves lane 1 pins under that name: a link-mode Script
// Operator or Script Filter on a fleet-bound record is refused at
// storeWriteRecord for every origin, naming the step and never the link; an
// inline script is unaffected.
func TestFleetRecordRefusesLinkModeScriptStepOnWrite(t *testing.T) {
	link := json.RawMessage(`{"type":"Script Operator","args":{"mode":"link","content":"https://scripts.invalid/x.js"}}`)
	inline := json.RawMessage(`{"type":"Script Filter","args":{"mode":"script","content":"function filter(p){return p.map(()=>true)}"}}`)
	t.Run("save", func(t *testing.T) {
		rt, _ := newCASRuntime(t)
		rec := fleetRecord("linked")
		rec.Process = []json.RawMessage{link}
		_, res := saveViaMethod(t, rt, rec, "")
		r, refused := refusalOf(res)
		if !refused || r.Code != refusedScriptLink || strings.Contains(r.Message, "://") {
			t.Fatalf("a link-mode step on a fleet record = %+v %s", r, res.Error)
		}
		rec.Process = []json.RawMessage{inline}
		if reply, res := saveViaMethod(t, rt, rec, ""); !res.OK || !reply.Staged {
			t.Fatalf("an inline script on a fleet record = %+v %s", reply, res.Error)
		}
		// A collection that gathers a fleet record is fleet-bound too.
		coll := subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"linked"}, Process: []json.RawMessage{link}}
		if r, refused := refusalOf(callSubscription(t, rt, "save", map[string]any{"subscription": coll})); !refused || r.Code != refusedScriptLink {
			t.Fatalf("a link-mode step on a fleet-bound collection = %+v", r)
		}
	})
	t.Run("import", func(t *testing.T) {
		rt, _ := newCASRuntime(t)
		backup, _ := json.Marshal(map[string]any{"format": subscriptionBackupFormat, "records": []subscriptionRecord{
			{ID: "linked", Name: "linked", Source: subscriptionSourceFleet, Process: []json.RawMessage{link}},
			{ID: "plain", Name: "plain", Source: subscriptionSourceRemote, URL: "https://provider.invalid/p", Process: []json.RawMessage{link}},
		}})
		var out importOutcome
		decodeResult(t, callSubscription(t, rt, "import", map[string]any{"backup": string(backup)}), &out)
		if r, ok := out.Refused["linked"]; !ok || r.Code != refusedScriptLink || strings.Contains(r.Message, "://") {
			t.Fatalf("import of a fleet record with a link-mode step = %+v", out)
		}
		if strings.Join(out.Imported, ",") != "plain" {
			t.Fatalf("a provider record with a link-mode step was not imported live: %+v", out)
		}
	})
}

// TestProbeReportsWithheldLegacyRecord: probe never holds the refresh's
// authority, so a legacy record's probe meets the fetcher guard and reports
// owner_credentials_withheld, not source_unavailable, with no rpc.call.
func TestProbeReportsWithheldLegacyRecord(t *testing.T) {
	rt, host := newCASRuntime(t)
	for _, rec := range []subscriptionRecord{
		{ID: "export", Name: "export", Source: subscriptionSourceVPNCore},
		{ID: "graph", Name: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "vu_1", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)},
		{ID: "coll", Name: "coll", Kind: kindCollection, Members: []string{"export"}},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"export", "graph", "coll"} {
		var result subscriptionProbeResult
		decodeResult(t, callSubscription(t, rt, "probe", map[string]any{"subscription_id": id}), &result)
		if result.OK || result.ErrorCode != codeOwnerCredentialsWithheld || result.Bytes != 0 {
			t.Fatalf("probe of %s = %+v, want owner_credentials_withheld", id, result)
		}
	}
	if len(host.rpcCalls) != 0 {
		t.Fatalf("a withheld probe made rpc calls: %v", host.rpcCalls)
	}
}

// TestProbeNeverCompletesPromotion: during a half-done promotion probe reads
// the staged document, as render does, and writes nothing; only fetch rolls
// the promotion forward.
func TestProbeNeverCompletesPromotion(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	before, _ := entryOf(t, rt, "f")
	promoted := halfDonePromotion(t, rt, host, fleetToLocal("f"))
	puts, deletes := host.puts, len(host.deletes)
	var result subscriptionProbeResult
	decodeResult(t, callSubscription(t, rt, "probe", map[string]any{"subscription_id": "f"}), &result)
	// record-v2 is still the fleet record, whose fetch would need the
	// catalogue this host does not serve: an answer is the staged local
	// record's.
	if !result.OK || result.Bytes == 0 {
		t.Fatalf("probe during a half-done promotion = %+v, want the staged local record served", result)
	}
	if host.puts != puts || len(host.deletes) != deletes {
		t.Fatalf("probe wrote: puts %d -> %d, deletes %d -> %d", puts, host.puts, deletes, len(host.deletes))
	}
	if doc, err := rt.loadStaged("f"); err != nil || doc == nil || doc.Record.Revision != promoted {
		t.Fatalf("probe disturbed the roll-forward document: %+v %v", doc, err)
	}
	value, _, _ := rt.kvGet(recordKey("f"))
	if rec, err := decodeRecordDoc("f", value); err != nil || withRevision(rec).Revision != before.Revision {
		t.Fatalf("probe moved record-v2: %v", err)
	}
	if res := fetchViaMethod(t, rt, "f"); !res.OK {
		t.Fatalf("fetch did not complete the promotion: %s", res.Error)
	}
	if doc, _ := rt.loadStaged("f"); doc != nil {
		t.Fatal("fetch left the staged document behind")
	}
}

// TestWriteMethodsRefusePromotionPending: after a kill between the index put
// and the record put of an approved promotion, save, delete, migrate_record
// and discard_staged write nothing on top of the half-done state (the never-
// live case, with no record-v2 at all, included), and the staged document the
// roll-forward needs survives every one of them.
func TestWriteMethodsRefusePromotionPending(t *testing.T) {
	t.Run("a live record promoted to a provider record", func(t *testing.T) {
		rt, host := newCASRuntime(t)
		seedFleetLive(t, rt, fleetRecord("f"))
		promoted := halfDonePromotion(t, rt, host, fleetToLocal("f"))
		edit := fleetToLocal("f")
		edit.DisplayName = "edited"
		_, res := saveViaMethod(t, rt, edit, "")
		if r, refused := refusalOf(res); !refused || r.Code != refusedPromotionPending || r.StagedRevision != promoted {
			t.Fatalf("save = %+v %s", r, res.Error)
		}
		res = callSubscription(t, rt, "delete", map[string]any{"subscription_id": "f"})
		if r, refused := refusalOf(res); !refused || r.Code != refusedPromotionPending {
			t.Fatalf("delete = %+v %s", r, res.Error)
		}
		for _, revision := range []string{"", promoted} {
			res = callSubscription(t, rt, "discard_staged", map[string]any{"subscription_id": "f", "staged_revision": revision})
			if r, refused := refusalOf(res); !refused || r.Code != refusedStagedMismatch {
				t.Fatalf("discard_staged %q = %+v %s", revision, r, res.Error)
			}
		}
		if doc, err := rt.loadStaged("f"); err != nil || doc == nil || doc.Record.Revision != promoted {
			t.Fatalf("a refused write disturbed the roll-forward document: %+v %v", doc, err)
		}
	})
	t.Run("a legacy record whose migration was promoted", func(t *testing.T) {
		rt, host := newCASRuntime(t)
		legacy := subscriptionRecord{ID: "l", Name: "l", Source: subscriptionSourceVPNCore}
		if err := rt.saveSubscription(legacy); err != nil {
			t.Fatal(err)
		}
		fleet, _ := migratedFleetRecord(legacy, time.Now())
		promoted := halfDonePromotion(t, rt, host, fleet)
		res := callSubscription(t, rt, "migrate_record", map[string]any{"subscription_id": "l"})
		if r, refused := refusalOf(res); !refused || r.Code != refusedPromotionPending || r.StagedRevision != promoted {
			t.Fatalf("migrate_record = %+v %s", r, res.Error)
		}
	})
	t.Run("a never-live record with no record-v2", func(t *testing.T) {
		rt, host := newCASRuntime(t)
		promoted := halfDonePromotion(t, rt, host, fleetRecord("n"))
		if _, found, _ := rt.kvGet(recordKey("n")); found {
			t.Fatal("the never-live case has a record-v2")
		}
		edit := fleetRecord("n")
		edit.DisplayName = "edited"
		_, res := saveViaMethod(t, rt, edit, "")
		if r, refused := refusalOf(res); !refused || r.Code != refusedPromotionPending {
			t.Fatalf("save of a never-live half-done promotion = %+v %s", r, res.Error)
		}
		if doc, _ := rt.loadStaged("n"); doc == nil || doc.Record.Revision != promoted {
			t.Fatal("the never-live roll-forward document is gone")
		}
	})
}

// TestDeleteRefusesFleetBoundRecord: delete refuses a fleet record and a
// collection that gathers one, naming plans.propose; a provider record
// deletes as today, its staged key with it.
func TestDeleteRefusesFleetBoundRecord(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f", "hk"))
	seedFleetLive(t, rt, subscriptionRecord{ID: "hk-coll", Name: "hk-coll", Kind: kindCollection, MemberTags: []string{"hk"}})
	for _, id := range []string{"f", "hk-coll"} {
		res := callSubscription(t, rt, "delete", map[string]any{"subscription_id": id})
		r, refused := refusalOf(res)
		if !refused || r.Code != refusedDeleteRequiresPlan || !strings.Contains(r.Message, "plans.propose") {
			t.Fatalf("delete of %s = %+v %s", id, r, res.Error)
		}
		if _, ok := entryOf(t, rt, id); !ok {
			t.Fatalf("a refused delete removed %s", id)
		}
	}
	// A provider record with a staged switch to fleet deletes live, and its
	// staged key goes with it.
	if err := rt.saveSubscription(subscriptionRecord{ID: "r", Name: "r", Source: subscriptionSourceRemote, URL: "https://provider.invalid/r"}); err != nil {
		t.Fatal(err)
	}
	if reply, res := saveViaMethod(t, rt, fleetRecord("r"), ""); !res.OK || !reply.Staged {
		t.Fatalf("remote to fleet = %+v %s", reply, res.Error)
	}
	if res := callSubscription(t, rt, "delete", map[string]any{"subscription_id": "r"}); !res.OK || isRefusedResponse(res) {
		t.Fatalf("delete of a provider record = %+v", res)
	}
	if _, found := host.values[stagedKey("r")]; found {
		t.Fatal("the deleted record's staged key survived")
	}
	if _, found := host.values[archiveKey("r")]; !found {
		t.Fatal("delete did not archive")
	}
}

// archiveViaApply deletes a fleet-bound record the way a delete plan does.
func archiveViaApply(t *testing.T, rt *runtime, id string) {
	t.Helper()
	entry, _ := entryOf(t, rt, id)
	res := callSubscription(t, rt, "apply_revision", map[string]any{"subscription_id": id, "expected_revision": entry.Revision, "approval_id": "ap-del", "plan_sha256": "x", "action": "delete"})
	if !res.OK {
		t.Fatalf("delete plan of %s: %s", id, res.Error)
	}
}

// TestRestoreStagesFleetBoundRecord: restoring an archived fleet record
// stages it with an empty base revision; the entry is a stagedEntryFor entry
// in the live list that remembers when it was archived, archive-v2 stays,
// and the promotion with an empty expected revision makes it live and
// deletes the archive. A provider record restores live.
func TestRestoreStagesFleetBoundRecord(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f", "hk"))
	archiveViaApply(t, rt, "f")
	res := callSubscription(t, rt, "restore", map[string]any{"subscription_id": "f"})
	var reply struct {
		Restored       bool   `json:"restored"`
		Staged         bool   `json:"staged"`
		StagedRevision string `json:"staged_revision"`
	}
	decodeResult(t, res, &reply)
	if !reply.Restored || !reply.Staged || reply.StagedRevision == "" {
		t.Fatalf("restore of a fleet record = %+v", reply)
	}
	entry, ok := entryOf(t, rt, "f")
	if !ok || entry.Revision != "" || entry.StagedRevision != reply.StagedRevision || entry.StagedRestoredAt == "" || entry.Source != "" || len(entry.Tags) != 0 {
		t.Fatalf("staged restore entry = %+v", entry)
	}
	doc, err := rt.loadStaged("f")
	if err != nil || doc == nil || doc.BaseRevision != "" {
		t.Fatalf("staged restore document = %+v %v", doc, err)
	}
	if _, found := host.values[archiveKey("f")]; !found {
		t.Fatal("the archive went before the promotion")
	}
	apply := callSubscription(t, rt, "apply_revision", map[string]any{"subscription_id": "f", "revision": reply.StagedRevision, "expected_revision": "", "approval_id": "ap-1", "plan_sha256": "x"})
	if !apply.OK {
		t.Fatalf("promotion of the restore: %s", apply.Error)
	}
	if entry, _ := entryOf(t, rt, "f"); entry.Revision != reply.StagedRevision || entry.StagedRestoredAt != "" || !entry.Flags.FleetBound {
		t.Fatalf("promoted restore entry = %+v", entry)
	}
	if _, found := host.values[archiveKey("f")]; found {
		t.Fatal("the promotion of a restore left the archive")
	}

	if err := rt.saveSubscription(subscriptionRecord{ID: "p", Name: "p", Source: subscriptionSourceRemote, URL: "https://provider.invalid/p"}); err != nil {
		t.Fatal(err)
	}
	if res := callSubscription(t, rt, "delete", map[string]any{"subscription_id": "p"}); !res.OK || isRefusedResponse(res) {
		t.Fatalf("delete p: %+v", res)
	}
	decodeResult(t, callSubscription(t, rt, "restore", map[string]any{"subscription_id": "p"}), &reply)
	if reply.Staged {
		t.Fatal("a provider record restored staged")
	}
}

// TestDiscardStagedReturnsRestoreToArchive: discarding a staged restore puts
// the entry back in Archived with its archive time, and the archive key stays.
func TestDiscardStagedReturnsRestoreToArchive(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f"))
	archiveViaApply(t, rt, "f")
	idx, _ := rt.loadIndex()
	archivedAt := idx.Archived[idx.archivedPosition("f")].ArchivedAt
	var reply struct {
		StagedRevision string `json:"staged_revision"`
	}
	decodeResult(t, callSubscription(t, rt, "restore", map[string]any{"subscription_id": "f"}), &reply)
	res := callSubscription(t, rt, "discard_staged", map[string]any{"subscription_id": "f", "staged_revision": reply.StagedRevision})
	if !res.OK || isRefusedResponse(res) {
		t.Fatalf("discard of a staged restore = %+v", res)
	}
	idx, _ = rt.loadIndex()
	if idx.position("f") >= 0 || idx.archivedPosition("f") < 0 || idx.Archived[idx.archivedPosition("f")].ArchivedAt != archivedAt {
		t.Fatalf("after the discard: live %d archived %d", idx.position("f"), idx.archivedPosition("f"))
	}
	if _, found := host.values[archiveKey("f")]; !found {
		t.Fatal("the discard deleted the archive")
	}
	if _, found := host.values[stagedKey("f")]; found {
		t.Fatal("the discard left the staged key")
	}
}

// importBackupVia imports records through the import method.
func importBackupVia(t *testing.T, rt *runtime, records ...subscriptionRecord) importOutcome {
	t.Helper()
	backup, err := json.Marshal(map[string]any{"format": subscriptionBackupFormat, "records": records})
	if err != nil {
		t.Fatal(err)
	}
	var out importOutcome
	decodeResult(t, callSubscription(t, rt, "import", map[string]any{"backup": string(backup)}), &out)
	return out
}

// TestImportStagesEveryFleetRecord: import stages every fleet-bound record it
// carries with an empty base revision when the id has no live revision, and
// reports each staged revision; provider records land live.
func TestImportStagesEveryFleetRecord(t *testing.T) {
	rt, _ := newCASRuntime(t)
	out := importBackupVia(t, rt,
		subscriptionRecord{ID: "f1", Name: "f1", Source: subscriptionSourceFleet, Tags: []string{"hk"}},
		subscriptionRecord{ID: "f2", Name: "f2", Source: subscriptionSourceFleet},
		subscriptionRecord{ID: "hk-coll", Name: "hk-coll", Kind: kindCollection, MemberTags: []string{"hk"}},
		subscriptionRecord{ID: "p", Name: "p", Source: subscriptionSourceRemote, URL: "https://provider.invalid/p"},
	)
	if len(out.Staged) != 3 || out.Staged["f1"] == "" || out.Staged["f2"] == "" || out.Staged["hk-coll"] == "" {
		t.Fatalf("staged = %v, want f1, f2 and the collection gathering f1", out.Staged)
	}
	for _, id := range []string{"f1", "f2", "hk-coll"} {
		entry, _ := entryOf(t, rt, id)
		doc, _ := rt.loadStaged(id)
		if entry.Revision != "" || entry.StagedRevision != out.Staged[id] || doc == nil || doc.BaseRevision != "" {
			t.Fatalf("%s: entry %+v staged %+v", id, entry, doc)
		}
		if _, found, _ := rt.kvGet(recordKey(id)); found {
			t.Fatalf("%s was written live", id)
		}
	}
	if entry, _ := entryOf(t, rt, "p"); entry.Revision == "" || entry.hasStaged() {
		t.Fatalf("the provider record = %+v", entry)
	}
}

// TestImportNeverReplacesLiveFleetRecord: a backup carrying a live fleet
// record's id stages its content over the live revision and never writes it.
func TestImportNeverReplacesLiveFleetRecord(t *testing.T) {
	rt, _ := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f", "hk"))
	live, _ := entryOf(t, rt, "f")
	incoming := fleetRecord("f", "jp")
	out := importBackupVia(t, rt, incoming)
	if out.Staged["f"] == "" || strings.Join(out.Replaced, ",") != "f" {
		t.Fatalf("import over a live fleet record = %+v", out)
	}
	entry, _ := entryOf(t, rt, "f")
	doc, _ := rt.loadStaged("f")
	if entry.Revision != live.Revision || strings.Join(entry.Tags, ",") != "hk" || doc == nil || doc.BaseRevision != live.Revision || strings.Join(doc.Record.Tags, ",") != "jp" {
		t.Fatalf("entry %+v staged %+v", entry, doc)
	}
	stored, err := rt.getSubscription("f")
	if err != nil || strings.Join(stored.Tags, ",") != "hk" {
		t.Fatalf("the live record moved: %+v %v", stored, err)
	}
}

// TestMigrateStagesEveryFleetRecord: migrating from a standalone Sub-Store
// writes its records live, except one whose id is a live fleet record's, which
// stages over it, reported in the migration's staged map.
func TestMigrateStagesEveryFleetRecord(t *testing.T) {
	rt, host := migrationHost(t, twoSubs, 200)
	seedFleetLive(t, rt, fleetRecord("imported-provider-a"))
	live, _ := entryOf(t, rt, "imported-provider-a")
	report, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Staged["imported-provider-a"] == "" || len(report.Staged) != 1 {
		t.Fatalf("migration staged %v", report.Staged)
	}
	if entry, _ := entryOf(t, rt, "imported-provider-a"); entry.Revision != live.Revision || entry.Source != subscriptionSourceFleet {
		t.Fatalf("the live fleet record moved: %+v", entry)
	}
	if entry, _ := entryOf(t, rt, "imported-provider-b"); entry.Revision == "" || entry.hasStaged() {
		t.Fatalf("the provider record = %+v", entry)
	}
	_ = host
}

// TestImportStillWritesLegacyRecords: a backup restores what it holds, so
// import writes legacy records live (save alone retires the sources).
func TestImportStillWritesLegacyRecords(t *testing.T) {
	rt, _ := newCASRuntime(t)
	out := importBackupVia(t, rt,
		subscriptionRecord{ID: "export", Name: "export", Source: subscriptionSourceVPNCore},
		subscriptionRecord{ID: "graph", Name: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "vu_1", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)},
	)
	if strings.Join(out.Imported, ",") != "export,graph" || len(out.Staged) != 0 || len(out.Refused) != 0 {
		t.Fatalf("import of legacy records = %+v", out)
	}
	for _, id := range []string{"export", "graph"} {
		if entry, _ := entryOf(t, rt, id); entry.Revision == "" || !entry.Flags.OwnerCredentials {
			t.Fatalf("%s = %+v", id, entry)
		}
	}
	_, res := saveViaMethod(t, rt, subscriptionRecord{ID: "new-export", Name: "new-export", Source: subscriptionSourceVPNCore}, "")
	if r, refused := refusalOf(res); !refused || r.Code != refusedLegacySourceRetired {
		t.Fatalf("save of a new legacy record = %+v %s", r, res.Error)
	}
}

// TestImportRefusesLegacyRecordIntoFleetCollection: a legacy record whose tag
// a fleet-bound collection gathers is refused per record and reported, and
// the rest of the backup imports.
func TestImportRefusesLegacyRecordIntoFleetCollection(t *testing.T) {
	rt, _ := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f", "hk"))
	seedFleetLive(t, rt, subscriptionRecord{ID: "hk-coll", Name: "hk-coll", Kind: kindCollection, MemberTags: []string{"hk"}})
	out := importBackupVia(t, rt,
		subscriptionRecord{ID: "legacy", Name: "legacy", Source: subscriptionSourceVPNCore, Tags: []string{"hk"}},
		subscriptionRecord{ID: "other", Name: "other", Source: subscriptionSourceVPNCore, Tags: []string{"jp"}},
	)
	r, ok := out.Refused["legacy"]
	if !ok || r.Code != refusedMixedOwnerCreds || !strings.Contains(strings.Join(r.Collections, ","), "hk-coll") {
		t.Fatalf("import of a legacy record a fleet collection gathers = %+v", out)
	}
	if _, ok := entryOf(t, rt, "legacy"); ok {
		t.Fatal("the refused legacy record was written")
	}
	if strings.Join(out.Imported, ",") != "other" {
		t.Fatalf("imported %v, want other", out.Imported)
	}
}

// TestImportRefusesFileOverFleetSource: a file whose node source is or would
// be fleet-bound cannot exist before S3, so import refuses it per record.
func TestImportRefusesFileOverFleetSource(t *testing.T) {
	rt, _ := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("f", "hk"))
	out := importBackupVia(t, rt,
		subscriptionRecord{ID: "cfg", Name: "cfg", Kind: kindFile, NodeSource: "f", Content: "proxies: []"},
		subscriptionRecord{ID: "coll", Name: "coll", Kind: kindCollection, MemberTags: []string{"hk"}},
		subscriptionRecord{ID: "cfg2", Name: "cfg2", Kind: kindFile, NodeSource: "coll", Content: "proxies: []"},
	)
	for _, id := range []string{"cfg", "cfg2"} {
		if r, ok := out.Refused[id]; !ok || r.Code != refusedFleetFileUnavailable || strings.Join(r.Files, ",") != id {
			t.Fatalf("import of file %s over a fleet-bound source = %+v", id, out)
		}
	}
	// Records written together see each other: the collection would make the
	// node source of a file in the same backup fleet-bound, so it is refused
	// too, naming the file, rather than staged for a file that cannot exist.
	if r, ok := out.Refused["coll"]; !ok || r.Code != refusedFleetFileUnavailable || strings.Join(r.Files, ",") != "cfg2" {
		t.Fatalf("the collection a backup's file reads = %+v", out)
	}
	if len(out.Imported) != 0 || len(out.Staged) != 0 {
		t.Fatalf("a refused record landed: %+v", out)
	}
	out = importBackupVia(t, rt, subscriptionRecord{ID: "coll", Name: "coll", Kind: kindCollection, MemberTags: []string{"hk"}})
	if out.Staged["coll"] == "" {
		t.Fatalf("the collection alone was not staged: %+v", out)
	}
}

// TestNoteFetchOutcomeDropsWriteWhenRevisionMoved: fetch bookkeeping that
// meets a kv_conflict re-reads once and drops its write when the entry's
// Revision or StagedRevision moved, so it never reverts a save or a staging.
func TestNoteFetchOutcomeDropsWriteWhenRevisionMoved(t *testing.T) {
	at := time.Date(2026, 10, 12, 1, 2, 3, 0, time.UTC)
	t.Run("a save moved the revision", func(t *testing.T) {
		rt, _ := newCASRuntime(t)
		if err := rt.saveSubscription(subscriptionRecord{ID: "r", Name: "r", Source: subscriptionSourceRemote, URL: "https://provider.invalid/a"}); err != nil {
			t.Fatal(err)
		}
		read, _ := rt.loadIndex()
		reply, res := saveViaMethod(t, rt, subscriptionRecord{ID: "r", Name: "r", Source: subscriptionSourceRemote, URL: "https://provider.invalid/b"}, "")
		if !res.OK || reply.Staged {
			t.Fatal(res.Error)
		}
		saved, _ := entryOf(t, rt, "r")
		rt.noteFetchOutcome("r", fetchOutcome{at: at, index: read, indexRead: true})
		entry, _ := entryOf(t, rt, "r")
		if entry.Revision != saved.Revision || entry.LastFetchAt != "" {
			t.Fatalf("the bookkeeping reverted the save or landed anyway: %+v", entry)
		}
		if stored, err := rt.getSubscription("r"); err != nil || stored.URL != "https://provider.invalid/b" {
			t.Fatalf("the saved record = %+v %v", stored, err)
		}
	})
	t.Run("a staging moved the staged revision", func(t *testing.T) {
		rt, _ := newCASRuntime(t)
		seedFleetLive(t, rt, fleetRecord("f"))
		read, _ := rt.loadIndex()
		edit := fleetRecord("f")
		edit.DisplayName = "staged"
		reply, res := saveViaMethod(t, rt, edit, "")
		if !res.OK || !reply.Staged {
			t.Fatal(res.Error)
		}
		rt.noteFetchOutcome("f", fetchOutcome{at: at, index: read, indexRead: true})
		entry, _ := entryOf(t, rt, "f")
		if entry.StagedRevision != reply.StagedRevision || entry.LastFetchAt != "" {
			t.Fatalf("the bookkeeping dropped the staged revision or landed anyway: %+v", entry)
		}
	})
	t.Run("another record's write only conflicts", func(t *testing.T) {
		rt, _ := newCASRuntime(t)
		for _, id := range []string{"r", "s"} {
			if err := rt.saveSubscription(subscriptionRecord{ID: id, Name: id, Source: subscriptionSourceRemote, URL: "https://provider.invalid/" + id}); err != nil {
				t.Fatal(err)
			}
		}
		read, _ := rt.loadIndex()
		if _, res := saveViaMethod(t, rt, subscriptionRecord{ID: "s", Name: "s2", Source: subscriptionSourceRemote, URL: "https://provider.invalid/s"}, ""); !res.OK {
			t.Fatal(res.Error)
		}
		rt.noteFetchOutcome("r", fetchOutcome{at: at, index: read, indexRead: true})
		if entry, _ := entryOf(t, rt, "r"); entry.LastFetchAt != at.Format(time.RFC3339) {
			t.Fatalf("the retried bookkeeping did not land: %+v", entry)
		}
		if entry, _ := entryOf(t, rt, "s"); entry.Name != "s2" {
			t.Fatalf("the retried bookkeeping reverted another record: %+v", entry)
		}
	})
}
