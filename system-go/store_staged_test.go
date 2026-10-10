package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// ── Shared test helpers (other lanes build on these) ────────────────────────

// isRefusedResponse reports whether res is a mutating method's structured
// refusal: a successful call answering {saved: false, refused}.
func isRefusedResponse(res response) bool {
	_, ok := refusalOf(res)
	return ok
}

// refusalOf decodes a structured refusal.
func refusalOf(res response) (storeRefusal, bool) {
	if !res.OK {
		return storeRefusal{}, false
	}
	var reply struct {
		Saved   *bool         `json:"saved"`
		Refused *storeRefusal `json:"refused"`
	}
	if json.Unmarshal(res.Result, &reply) != nil || reply.Refused == nil || reply.Saved == nil || *reply.Saved {
		return storeRefusal{}, false
	}
	return *reply.Refused, true
}

// refusalText is a structured refusal as "code: message", the shape the
// error channel carried before S2, or "" when res is no refusal.
func refusalText(res response) string {
	r, ok := refusalOf(res)
	if !ok {
		return ""
	}
	return r.Code + ": " + r.Message
}

// casKVHost is the in-memory KV with the compare-and-swap a124's host gives
// kv.put (plan section 2.5): if_match, when present, must equal the stored
// value's sha256, and an empty if_match writes only a key that does not
// exist; kv.get answers the digest. beforePut, when set, runs before every
// kv.put, which is how a test models another invocation writing in between.
type casKVHost struct {
	*kvHostCaller
	total     int
	conflicts int
	ifMatched int
	beforePut func(key string)
	// rpc answers rpc.call by "service/method"; claim_apply answers
	// {"granted": true} unless denyClaim is set.
	rpc       map[string]json.RawMessage
	rpcCalls  []string
	denyClaim bool
}

func newCASHost() *casKVHost {
	return &casKVHost{kvHostCaller: newKVHostCaller(), rpc: map[string]json.RawMessage{}}
}

func (h *casKVHost) call(method string, params any) (json.RawMessage, error) {
	h.total++
	encoded, _ := json.Marshal(params)
	switch method {
	case latticeplugin.HostMethodKVGet:
		var p struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(encoded, &p)
		value, ok := h.values[p.Key]
		if !ok {
			return json.RawMessage(`{"ok":false}`), nil
		}
		return json.Marshal(map[string]any{"ok": true, "value_base64": base64.StdEncoding.EncodeToString(value), "sha256": kvDigest(value)})
	case latticeplugin.HostMethodKVPut:
		var p struct {
			Key     string  `json:"key"`
			IfMatch *string `json:"if_match"`
		}
		_ = json.Unmarshal(encoded, &p)
		if h.beforePut != nil {
			h.beforePut(p.Key)
		}
		if p.IfMatch != nil {
			h.ifMatched++
			stored, exists := h.values[p.Key]
			if (*p.IfMatch == "" && exists) || (*p.IfMatch != "" && (!exists || kvDigest(stored) != *p.IfMatch)) {
				h.conflicts++
				return nil, fmt.Errorf("kv_conflict: %s moved", p.Key)
			}
		}
		return h.kvHostCaller.call(method, params)
	case latticeplugin.HostMethodRPCCall:
		var p struct {
			Service string `json:"service"`
			Method  string `json:"method"`
		}
		_ = json.Unmarshal(encoded, &p)
		target := p.Service + "/" + p.Method
		h.rpcCalls = append(h.rpcCalls, target)
		if target == "latticenet.sub-store/plans/claim_apply" && !h.denyClaim {
			if answer, ok := h.rpc[target]; ok {
				return answer, nil
			}
			return json.RawMessage(`{"granted":true}`), nil
		}
		if answer, ok := h.rpc[target]; ok {
			return answer, nil
		}
		return nil, fmt.Errorf("no canned answer for %s", target)
	default:
		return h.kvHostCaller.call(method, params)
	}
}

// newCASRuntime is a runtime over a casKVHost on the shared warm engine.
func newCASRuntime(t *testing.T) (*runtime, *casKVHost) {
	t.Helper()
	host := newCASHost()
	return &runtime{host: host, engine: sharedWarmTestEngine(t)}, host
}

// fleetRecord is a fleet sub with a leading Structured Filter step.
func fleetRecord(id string, tags ...string) subscriptionRecord {
	return subscriptionRecord{ID: id, Name: id, Source: subscriptionSourceFleet, Tags: tags}
}

// seedFleetLive puts a fleet-bound record live the way production does: the
// write stages it, and the promotion apply_revision runs makes it live.
func seedFleetLive(t *testing.T, rt *runtime, rec subscriptionRecord) {
	t.Helper()
	res, err := rt.storeWriteRecord(writeRequest{Record: rec}, originImport)
	if err != nil || !res.landed() {
		t.Fatalf("stage %s: %+v %v", rec.ID, res, err)
	}
	if !res.Staged {
		return
	}
	promoteStaged(t, rt, rec.ID)
}

// promoteStaged promotes id's staged revision through storePromote.
func promoteStaged(t *testing.T, rt *runtime, id string) {
	t.Helper()
	idx, err := rt.writableIndex()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := rt.loadStaged(id)
	if err != nil || doc == nil {
		t.Fatalf("staged %s: %v %v", id, doc, err)
	}
	if err := rt.storePromote(idx, doc); err != nil {
		t.Fatalf("promote %s: %v", id, err)
	}
}

// saveReply is save's reply with the S2 fields.
type saveReply struct {
	Saved          bool               `json:"saved"`
	Staged         bool               `json:"staged"`
	StagedRevision string             `json:"staged_revision"`
	BaseRevision   string             `json:"base_revision"`
	Subscription   subscriptionRecord `json:"subscription"`
}

func saveViaMethod(t *testing.T, rt *runtime, rec subscriptionRecord, ifRevision string) (saveReply, response) {
	t.Helper()
	payload := map[string]any{"subscription": rec}
	if ifRevision != "" {
		payload["if_revision"] = ifRevision
	}
	res := callSubscription(t, rt, "save", payload)
	var reply saveReply
	if res.OK {
		_ = json.Unmarshal(res.Result, &reply)
	}
	return reply, res
}

func entryOf(t *testing.T, rt *runtime, id string) (indexEntry, bool) {
	t.Helper()
	idx, err := rt.loadIndex()
	if err != nil || idx == nil {
		t.Fatalf("index: %v", err)
	}
	if pos := idx.position(id); pos >= 0 {
		return idx.Records[pos], true
	}
	return indexEntry{}, false
}

// ── Flags ───────────────────────────────────────────────────────────────────

func TestIndexFlagsOwnerCredentialsAndFleetBound(t *testing.T) {
	rt, _ := newCASRuntime(t)
	for _, rec := range []subscriptionRecord{
		{ID: "legacy", Name: "legacy", Source: subscriptionSourceVPNCore, Tags: []string{"old"}},
		{ID: "remote", Name: "remote", Source: subscriptionSourceRemote, URL: "https://provider.invalid/a", Tags: []string{"hk"}},
		{ID: "old-coll", Name: "old-coll", Kind: kindCollection, MemberTags: []string{"old"}},
		{ID: "old-file", Name: "old-file", Kind: kindFile, NodeSource: "old-coll", Content: "proxies: []"},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatalf("seed %s: %v", rec.ID, err)
		}
	}
	seedFleetLive(t, rt, fleetRecord("fleet", "hk"))
	seedFleetLive(t, rt, subscriptionRecord{ID: "hk-coll", Name: "hk-coll", Kind: kindCollection, MemberTags: []string{"hk"}})
	want := map[string]indexFlags{
		"legacy":   {OwnerCredentials: true},
		"old-coll": {OwnerCredentials: true},
		"old-file": {OwnerCredentials: true},
		"remote":   {},
		"fleet":    {FleetBound: true},
		"hk-coll":  {FleetBound: true},
	}
	for id, flags := range want {
		entry, ok := entryOf(t, rt, id)
		if !ok {
			t.Fatalf("%s has no entry", id)
		}
		got := entry.Flags
		got.RegexIncompatible, got.HasFallbackStep = false, false
		if got != flags {
			t.Errorf("%s flags = %+v, want %+v", id, got, flags)
		}
	}
	// list exposes both flags.
	var listed struct {
		Subscriptions []listView `json:"subscriptions"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	seen := 0
	for _, view := range listed.Subscriptions {
		if view.Flags != nil && (view.Flags.FleetBound || view.Flags.OwnerCredentials) {
			seen++
		}
	}
	if seen != 5 {
		t.Fatalf("list shows %d flagged rows, want 5", seen)
	}
}

// ── Staging ─────────────────────────────────────────────────────────────────

func TestSaveStagesFleetBoundRecords(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("fleet", "hk"))
	live, _ := entryOf(t, rt, "fleet")
	edit := fleetRecord("fleet", "hk")
	edit.DisplayName = "Fleet HK"
	reply, res := saveViaMethod(t, rt, edit, live.Revision)
	if !res.OK || !reply.Saved || !reply.Staged || reply.StagedRevision == "" || reply.BaseRevision != live.Revision {
		t.Fatalf("a fleet edit was not staged: %+v %s", reply, res.Error)
	}
	entry, _ := entryOf(t, rt, "fleet")
	if entry.Revision != live.Revision || entry.StagedRevision != reply.StagedRevision || entry.StagedSource != subscriptionSourceFleet {
		t.Fatalf("index after staging = %+v", entry)
	}
	stored, err := rt.getSubscription("fleet")
	if err != nil || stored.DisplayName != "" {
		t.Fatalf("the live record moved before any plan: %+v %v", stored, err)
	}
	doc, err := rt.loadStaged("fleet")
	if err != nil || doc == nil || doc.Record.DisplayName != "Fleet HK" || doc.BaseRevision != live.Revision {
		t.Fatalf("staged document = %+v %v", doc, err)
	}
	if host.ifMatched == 0 {
		t.Fatal("no index write carried if_match")
	}
}

func TestSaveWritesProviderRecordsLive(t *testing.T) {
	rt, _ := newCASRuntime(t)
	reply, res := saveViaMethod(t, rt, subscriptionRecord{ID: "remote", Name: "remote", Source: subscriptionSourceRemote, URL: "https://provider.invalid/a"}, "")
	if !res.OK || !reply.Saved || reply.Staged {
		t.Fatalf("a provider save = %+v %s", reply, res.Error)
	}
	entry, _ := entryOf(t, rt, "remote")
	if entry.Revision == "" || entry.StagedRevision != "" {
		t.Fatalf("provider entry = %+v", entry)
	}
	if _, err := rt.loadStaged("remote"); err != nil {
		t.Fatal(err)
	}
}

func TestNewFleetRecordStages(t *testing.T) {
	cases := map[string]func(t *testing.T, rt *runtime){
		"no reader": func(t *testing.T, rt *runtime) {},
		// A share core kept after a purge names the id; the plugin cannot see
		// it, so the brand-new record stages all the same.
		"a reused id with a surviving identity share": func(t *testing.T, rt *runtime) {
			if err := rt.saveSubscription(subscriptionRecord{ID: "new", Name: "old", Source: subscriptionSourceRemote, URL: "https://provider.invalid/x"}); err != nil {
				t.Fatal(err)
			}
			if err := rt.deleteSubscription("new"); err != nil {
				t.Fatal(err)
			}
			if err := rt.purgeSubscription("new"); err != nil {
				t.Fatal(err)
			}
		},
		"identity shares on a tag-gathering collection": func(t *testing.T, rt *runtime) {
			seedFleetLive(t, rt, fleetRecord("other", "hk"))
			seedFleetLive(t, rt, subscriptionRecord{ID: "hk-coll", Name: "hk-coll", Kind: kindCollection, MemberTags: []string{"hk"}})
		},
		"a provider-only collection": func(t *testing.T, rt *runtime) {
			for _, rec := range []subscriptionRecord{
				{ID: "p", Name: "p", Source: subscriptionSourceRemote, URL: "https://provider.invalid/p", Tags: []string{"hk"}},
				{ID: "hk-coll", Name: "hk-coll", Kind: kindCollection, MemberTags: []string{"hk"}},
			} {
				if err := rt.saveSubscription(rec); err != nil {
					t.Fatal(err)
				}
			}
		},
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			rt, _ := newCASRuntime(t)
			seed(t, rt)
			reply, res := saveViaMethod(t, rt, fleetRecord("new", "hk"), "")
			if !res.OK || !reply.Staged || reply.BaseRevision != "" {
				t.Fatalf("a new fleet record = %+v %s", reply, res.Error)
			}
			entry, ok := entryOf(t, rt, "new")
			if !ok || entry.Revision != "" || entry.StagedRevision != reply.StagedRevision {
				t.Fatalf("new record entry = %+v", entry)
			}
		})
	}
	t.Run("a collection a file reads", func(t *testing.T) {
		rt, _ := newCASRuntime(t)
		for _, rec := range []subscriptionRecord{
			{ID: "p", Name: "p", Source: subscriptionSourceRemote, URL: "https://provider.invalid/p", Tags: []string{"hk"}},
			{ID: "hk-coll", Name: "hk-coll", Kind: kindCollection, MemberTags: []string{"hk"}},
			{ID: "cfg", Name: "cfg", Kind: kindFile, NodeSource: "hk-coll", Content: "proxies: []"},
		} {
			if err := rt.saveSubscription(rec); err != nil {
				t.Fatal(err)
			}
		}
		_, res := saveViaMethod(t, rt, fleetRecord("new", "hk"), "")
		r, refused := refusalOf(res)
		if !refused || r.Code != refusedFleetFileUnavailable || strings.Join(r.Files, ",") != "cfg" {
			t.Fatalf("a fleet record a file would read = %+v %s", r, res.Error)
		}
		if _, ok := entryOf(t, rt, "new"); ok {
			t.Fatal("a refused write left an entry")
		}
	})
	t.Run("a collection with a legacy member", func(t *testing.T) {
		rt, _ := newCASRuntime(t)
		for _, rec := range []subscriptionRecord{
			{ID: "legacy", Name: "legacy", Source: subscriptionSourceVPNCore, Tags: []string{"hk"}},
			{ID: "hk-coll", Name: "hk-coll", Kind: kindCollection, MemberTags: []string{"hk"}},
		} {
			if err := rt.saveSubscription(rec); err != nil {
				t.Fatal(err)
			}
		}
		_, res := saveViaMethod(t, rt, fleetRecord("new", "hk"), "")
		r, refused := refusalOf(res)
		if !refused || r.Code != refusedMixedOwnerCreds || strings.Join(r.Collections, ",") != "hk-coll" {
			t.Fatalf("a fleet record a legacy collection would gather = %+v %s", r, res.Error)
		}
	})
}

func TestStagedOnlyEntryCarriesNoLiveFacts(t *testing.T) {
	rt, _ := newCASRuntime(t)
	for _, rec := range []subscriptionRecord{
		{ID: "p", Name: "p", Source: subscriptionSourceRemote, URL: "https://provider.invalid/p", Tags: []string{"hk"}},
		{ID: "hk-coll", Name: "hk-coll", Kind: kindCollection, MemberTags: []string{"hk"}},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := entryOf(t, rt, "hk-coll")
	if _, res := saveViaMethod(t, rt, fleetRecord("staged", "hk"), ""); !res.OK {
		t.Fatal(res.Error)
	}
	entry, _ := entryOf(t, rt, "staged")
	if entry.Source != "" || len(entry.Tags) != 0 || entry.Flags != (indexFlags{}) || entry.Revision != "" || entry.LastFetchAt != "" {
		t.Fatalf("a staged-only entry carries live facts: %+v", entry)
	}
	if entry.StagedSource != subscriptionSourceFleet || strings.Join(entry.StagedTags, ",") != "hk" {
		t.Fatalf("a staged-only entry lost its staged facts: %+v", entry)
	}
	after, _ := entryOf(t, rt, "hk-coll")
	if after.Flags != before.Flags || after.Flags.FleetBound {
		t.Fatalf("a staged record changed a live collection's flags: %+v -> %+v", before.Flags, after.Flags)
	}
	// The live collection still writes live: staging is decided from the
	// live flag and the written record's own reach, and the collection's own
	// reach over the union does include the staged fleet record, so a
	// collection edit stages from here on.
	edit := subscriptionRecord{ID: "hk-coll", Name: "hk-coll", DisplayName: "renamed", Kind: kindCollection, MemberTags: []string{"hk"}}
	reply, res := saveViaMethod(t, rt, edit, before.Revision)
	if !res.OK || !reply.Staged {
		t.Fatalf("a collection that would gather a staged fleet record = %+v %s", reply, res.Error)
	}
}

func TestSecondSaveReplacesStagedIdNeverRewrites(t *testing.T) {
	rt, _ := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("fleet"))
	live, _ := entryOf(t, rt, "fleet")
	first := fleetRecord("fleet")
	first.DisplayName = "one"
	r1, _ := saveViaMethod(t, rt, first, live.Revision)
	second := fleetRecord("fleet")
	second.DisplayName = "two"
	r2, res := saveViaMethod(t, rt, second, r1.StagedRevision)
	if !res.OK || !r2.Staged || r2.StagedRevision == r1.StagedRevision || r2.BaseRevision != live.Revision {
		t.Fatalf("second save = %+v / %+v %s", r1, r2, res.Error)
	}
	doc, _ := rt.loadStaged("fleet")
	if doc.Record.Revision != r2.StagedRevision || doc.Record.DisplayName != "two" {
		t.Fatalf("staged document = %+v", doc.Record)
	}
	// Back at the live content: nothing is left to stage.
	back := fleetRecord("fleet")
	var reply struct {
		Saved     bool   `json:"saved"`
		Staged    bool   `json:"staged"`
		Discarded string `json:"discarded_staged"`
	}
	decodeResult(t, callSubscription(t, rt, "save", map[string]any{"subscription": back, "if_revision": r2.StagedRevision}), &reply)
	if !reply.Saved || reply.Staged || reply.Discarded != r2.StagedRevision {
		t.Fatalf("saving the live content back = %+v", reply)
	}
	if entry, _ := entryOf(t, rt, "fleet"); entry.StagedRevision != "" {
		t.Fatalf("the staged revision outlived a save back to live: %+v", entry)
	}
	if doc, _ := rt.loadStaged("fleet"); doc != nil {
		t.Fatal("the staged key outlived a save back to live")
	}
}

func TestSaveKeepsMigratedFrom(t *testing.T) {
	rt, _ := newCASRuntime(t)
	migrated := fleetRecord("m")
	migrated.MigratedFrom = &legacyOrigin{Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a", MigratedAt: "2026-10-10T00:00:00Z"}
	res, err := rt.storeWriteRecord(writeRequest{Record: migrated}, originMigrateRecord)
	if err != nil || !res.Staged {
		t.Fatalf("stage migrated: %+v %v", res, err)
	}
	edit := fleetRecord("m")
	edit.DisplayName = "edited"
	// A caller cannot forge or drop provenance.
	edit.MigratedFrom = nil
	reply, resp := saveViaMethod(t, rt, edit, res.StagedRevision)
	if !resp.OK || !reply.Staged {
		t.Fatalf("edit of the staged migrated record = %+v %s", reply, resp.Error)
	}
	entry, _ := entryOf(t, rt, "m")
	doc, _ := rt.loadStaged("m")
	if !entry.StagedMigrated || doc.Record.MigratedFrom == nil || doc.Record.MigratedFrom.VPNIdentity != "identity-a" {
		t.Fatalf("MigratedFrom lost on edit: entry %+v doc %+v", entry, doc.Record.MigratedFrom)
	}
}

func TestSaveOfLiveLegacyContentIgnoresStagedMigration(t *testing.T) {
	rt, _ := newCASRuntime(t)
	legacy := subscriptionRecord{ID: "l", Name: "l", Source: subscriptionSourceVPNCore}
	if err := rt.saveSubscription(legacy); err != nil {
		t.Fatal(err)
	}
	liveEntry, _ := entryOf(t, rt, "l")
	migrated := fleetRecord("l")
	migrated.MigratedFrom = &legacyOrigin{Source: subscriptionSourceVPNCore, MigratedAt: "2026-10-10T00:00:00Z"}
	staged, err := rt.storeWriteRecord(writeRequest{Record: migrated}, originMigrateRecord)
	if err != nil || !staged.Staged {
		t.Fatalf("stage migration: %+v %v", staged, err)
	}
	edit := legacy
	edit.DisplayName = "still legacy"
	reply, res := saveViaMethod(t, rt, edit, liveEntry.Revision)
	if !res.OK || !reply.Saved || reply.Staged {
		t.Fatalf("a live edit of the legacy record = %+v %s", reply, res.Error)
	}
	entry, _ := entryOf(t, rt, "l")
	if entry.StagedRevision != staged.StagedRevision || !entry.StagedMigrated || !entry.Flags.OwnerCredentials {
		t.Fatalf("the live edit disturbed the staged migration: %+v", entry)
	}
}

// ── discard_staged ──────────────────────────────────────────────────────────

func TestDiscardStagedClearsIndexAndDeletesKey(t *testing.T) {
	rt, _ := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("fleet"))
	live, _ := entryOf(t, rt, "fleet")
	edit := fleetRecord("fleet")
	edit.DisplayName = "x"
	reply, _ := saveViaMethod(t, rt, edit, live.Revision)
	var out struct {
		Discarded string `json:"discarded"`
	}
	decodeResult(t, callSubscription(t, rt, "discard_staged", map[string]any{"subscription_id": "fleet", "staged_revision": reply.StagedRevision}), &out)
	if out.Discarded != reply.StagedRevision {
		t.Fatalf("discard reply = %+v", out)
	}
	entry, _ := entryOf(t, rt, "fleet")
	if entry.hasStaged() || entry.Revision != live.Revision {
		t.Fatalf("entry after discard = %+v", entry)
	}
	if doc, _ := rt.loadStaged("fleet"); doc != nil {
		t.Fatal("the staged key survived discard")
	}
}

func TestDiscardStagedRefusesRevisionMismatch(t *testing.T) {
	rt, _ := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("fleet"))
	live, _ := entryOf(t, rt, "fleet")
	edit := fleetRecord("fleet")
	edit.DisplayName = "x"
	reply, _ := saveViaMethod(t, rt, edit, live.Revision)
	res := callSubscription(t, rt, "discard_staged", map[string]any{"subscription_id": "fleet", "staged_revision": "deadbeef"})
	r, refused := refusalOf(res)
	if !refused || r.Code != refusedStagedMismatch || r.StagedRevision != reply.StagedRevision {
		t.Fatalf("a mismatched discard = %+v %s", r, res.Error)
	}
	if doc, _ := rt.loadStaged("fleet"); doc == nil {
		t.Fatal("a refused discard deleted the staged key")
	}
}

func TestDiscardStagedRefusesWhenNothingStaged(t *testing.T) {
	rt, host := newCASRuntime(t)
	seedFleetLive(t, rt, fleetRecord("fleet"))
	deletes := len(host.deletes)
	for _, revision := range []string{"", "anything"} {
		res := callSubscription(t, rt, "discard_staged", map[string]any{"subscription_id": "fleet", "staged_revision": revision})
		if r, refused := refusalOf(res); !refused || r.Code != refusedStagedMismatch {
			t.Fatalf("discard with nothing staged (%q) = %+v %s", revision, r, res.Error)
		}
	}
	if len(host.deletes) != deletes {
		t.Fatal("a refused discard deleted a key")
	}
}

func TestDiscardStagedRemovesNeverLiveEntry(t *testing.T) {
	rt, _ := newCASRuntime(t)
	reply, _ := saveViaMethod(t, rt, fleetRecord("new", "hk"), "")
	decodeResult(t, callSubscription(t, rt, "discard_staged", map[string]any{"subscription_id": "new", "staged_revision": reply.StagedRevision}), &struct{}{})
	if _, ok := entryOf(t, rt, "new"); ok {
		t.Fatal("a discarded never-live record left a ghost entry")
	}
}

// ── Compare-and-swap ────────────────────────────────────────────────────────

func TestIndexWriteRetriesOnceOnConflict(t *testing.T) {
	rt, host := newCASRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "a", Name: "a", Source: subscriptionSourceRemote, URL: "https://provider.invalid/a"}); err != nil {
		t.Fatal(err)
	}
	// Another invocation writes the index once between this save's read and
	// its write: the save re-reads, re-applies its delta and lands.
	moved := false
	host.beforePut = func(key string) {
		if key == storeIndexKey && !moved {
			moved = true
			idx, _ := rt.loadIndex()
			idx.Records[0].LastError = "a concurrent fetch"
			raw, _ := encodeIndex(idx, false)
			host.values[storeIndexKey] = raw
		}
	}
	reply, res := saveViaMethod(t, rt, subscriptionRecord{ID: "b", Name: "b", Source: subscriptionSourceRemote, URL: "https://provider.invalid/b"}, "")
	if !res.OK || !reply.Saved || host.conflicts != 1 {
		t.Fatalf("save across one concurrent write = %+v %s conflicts=%d", reply, res.Error, host.conflicts)
	}
	a, _ := entryOf(t, rt, "a")
	if _, ok := entryOf(t, rt, "b"); !ok || a.LastError != "a concurrent fetch" {
		t.Fatalf("the retry lost a delta: a=%+v", a)
	}
}

func TestLiveSaveIndexConflictExhaustionLeavesRecordAuthoritative(t *testing.T) {
	rt, host := newCASRuntime(t)
	rec := subscriptionRecord{ID: "a", Name: "a", Source: subscriptionSourceRemote, URL: "https://provider.invalid/a"}
	if err := rt.saveSubscription(rec); err != nil {
		t.Fatal(err)
	}
	before, _ := entryOf(t, rt, "a")
	host.beforePut = func(key string) {
		if key == storeIndexKey {
			idx, _ := rt.loadIndex()
			idx.Records[0].LastError += "x"
			raw, _ := encodeIndex(idx, false)
			host.values[storeIndexKey] = raw
		}
	}
	edit := rec
	edit.DisplayName = "new content"
	_, res := saveViaMethod(t, rt, edit, before.Revision)
	if r, refused := refusalOf(res); !refused || r.Code != refusedKVConflict {
		t.Fatalf("an exhausted retry = %+v %s", r, res.Error)
	}
	host.beforePut = nil
	// The record put landed before the index put was refused twice: the
	// record is authoritative and every reader serves it.
	idx, _ := rt.loadIndex()
	loaded, err := rt.storeLoadRecord(idx, "a", false)
	if err != nil || loaded.State != recordAhead || loaded.Live.DisplayName != "new content" || loaded.LiveRevision == before.Revision {
		t.Fatalf("after exhaustion: state %d live %+v err %v", loaded.State, loaded.Live, err)
	}
	// A later save writes over it and re-derives the entry.
	again := edit
	again.DisplayName = "third"
	reply, res := saveViaMethod(t, rt, again, loaded.LiveRevision)
	if !res.OK || !reply.Saved {
		t.Fatalf("a save over the record-ahead state = %+v %s", reply, res.Error)
	}
	entry, _ := entryOf(t, rt, "a")
	if entry.Revision != reply.Subscription.Revision {
		t.Fatalf("the entry was not re-derived: %+v", entry)
	}
	// A discard with nothing staged is refused as on a consistent store.
	if r, refused := refusalOf(callSubscription(t, rt, "discard_staged", map[string]any{"subscription_id": "a", "staged_revision": "x"})); !refused || r.Code != refusedStagedMismatch {
		t.Fatalf("discard over a repaired record = %+v", r)
	}
}
