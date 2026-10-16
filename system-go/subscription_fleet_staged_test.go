package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// structuredKeep is a keep-mode Structured Filter step with one "in"
// predicate.
func structuredKeep(field string, values ...string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "Structured Filter", "args": map[string]any{
		"predicates": []map[string]any{{"field": field, "op": "in", "values": values}},
	}})
	return raw
}

// renameStep is a Regex Rename step from one label prefix to another.
func renameStep(from, to string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "Regex Rename Operator", "args": []map[string]string{{"expr": "^" + from, "now": to}}})
	return raw
}

// stageRecord writes rec through storeWriteRecord as save does and requires
// it to be staged, returning the staged revision.
func stageRecord(t *testing.T, rt *runtime, rec subscriptionRecord) string {
	t.Helper()
	if rec.Name == "" {
		rec.Name = rec.ID
	}
	res, err := rt.storeWriteRecord(writeRequest{Record: rec}, originSave)
	if err != nil || res.Refused != nil || !res.Staged {
		t.Fatalf("stage %s: %+v refused %+v err %v", rec.ID, res, res.Refused, err)
	}
	return res.StagedRevision
}

// mixedProtocolRows is lines 1 to 4 on trojan and 5 to 8 on vless.
func mixedProtocolRows() []model.LineCatalogueRow {
	rows := fleetTestRows(4, "trojan")
	for i := 5; i <= 8; i++ {
		rows = append(rows, fleetTestRow(i, "vless"))
	}
	return rows
}

// planLines is the line of every fleet node of a plan, in plan order.
func planLines(plan model.SelectionPlan) []string {
	var out []string
	for _, node := range plan.Nodes {
		if node.LineUUID != "" {
			out = append(out, node.LineUUID)
		}
	}
	return out
}

// planNames is the name of every node of a plan, in plan order.
func planNames(t *testing.T, plan model.SelectionPlan) []string {
	t.Helper()
	var out []string
	for _, node := range plan.Nodes {
		var fields map[string]any
		if err := json.Unmarshal(node.Node, &fields); err != nil {
			t.Fatal(err)
		}
		name, _ := fields["name"].(string)
		out = append(out, name)
	}
	return out
}

// freshFleetPlans gives a test its own plan cache, so a count of plan
// computations is not satisfied by a plan an earlier test cached under the
// same key, and restores the worker's cache after.
func freshFleetPlans(t *testing.T) {
	t.Helper()
	saved := fleetPlans
	fleetPlans = newFleetPlanCache(fleetPlanCacheBytes)
	t.Cleanup(func() { fleetPlans = saved })
}

// TestStagedFleetRevisionRendersLiveOverLegacyEnvelope pins the migration
// case: core previews a staged fleet revision over the record's live
// snapshot, which is still the legacy envelope. The render reads the
// catalogue under the staged revision's selection, never reads the
// snapshot's text as content, and no export link reaches the reply.
func TestStagedFleetRevisionRendersLiveOverLegacyEnvelope(t *testing.T) {
	rt, host := newFleetRuntime(t, fleetTestRows(3, "vless"))
	if err := rt.saveSubscription(subscriptionRecord{ID: "legacy", Name: "legacy", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}); err != nil {
		t.Fatal(err)
	}
	live, _ := rt.getSubscription("legacy")
	staged := stageRecord(t, rt, subscriptionRecord{ID: "legacy", Source: subscriptionSourceFleet})
	const link = "vless://11111111-2222-4333-8444-555555555555@owner.example:443?security=reality#owner"
	// Not valid JSON: a decode of the snapshot as nodes would fail loudly.
	snapshot := `{"version":2,"kind":"sub","raw":"` + link + `"`
	for name, raw := range map[string]string{"legacy envelope": snapshot, "no snapshot": ""} {
		out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "legacy", "raw": raw, "target": "sing-box", "revision": staged})
		if !res.OK {
			t.Fatalf("%s: %s", name, res.Error)
		}
		if out.LiveRevision != live.Revision {
			t.Fatalf("%s: live revision %q, want %q", name, out.LiveRevision, live.Revision)
		}
		plan := decodeRenderPlan(t, out.Plan)
		if len(planLines(plan)) != 3 || plan.Selection == nil || plan.Selection.CatalogueVersion != fleetTestVersion {
			t.Fatalf("%s: plan lines %v selection %+v", name, planLines(plan), plan.Selection)
		}
		encoded, _ := json.Marshal(out)
		if strings.Contains(string(encoded), "owner.example") || strings.Contains(string(encoded), "11111111") {
			t.Fatalf("%s: the reply carries the export link", name)
		}
	}
	if host.other != 0 {
		t.Fatalf("the staged render made %d rpc calls beside the catalogue", host.other)
	}
	// The live revision of a legacy record is still withheld.
	if _, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "legacy", "target": "sing-box"}); res.OK || !strings.HasPrefix(res.Error, codeOwnerCredentialsWithheld) {
		t.Fatalf("live legacy render: ok=%v error=%q", res.OK, res.Error)
	}
}

// TestRenderStagedSelectorFetchesLive pins which rows a staged fleet render
// reads: a staged revision whose leading structured run differs from the
// snapshot's reads the catalogue live under its own pushed selector; one
// that changes only a later step reuses the snapshot's rows and reads
// nothing.
func TestRenderStagedSelectorFetchesLive(t *testing.T) {
	rt, host := newFleetRuntime(t, mixedProtocolRows())
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp", Process: []json.RawMessage{structuredKeep("protocol", "trojan")}})
	fetched, err := rt.fetchSubscription("jp")
	if err != nil {
		t.Fatal(err)
	}
	widened := stageRecord(t, rt, subscriptionRecord{ID: "jp", Source: subscriptionSourceFleet, Process: []json.RawMessage{structuredKeep("protocol", "trojan", "vless")}})
	reads := host.calls
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "jp", "raw": fetched.Raw, "target": "sing-box", "revision": widened})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if host.calls == reads {
		t.Fatal("a staged revision with another leading run did not read the catalogue")
	}
	last := host.requests[len(host.requests)-1]
	if last.Selector == nil || len(last.Selector.Protocols) != 2 {
		t.Fatalf("the live read pushed %+v, want the staged revision's two protocols", last.Selector)
	}
	if lines := planLines(decodeRenderPlan(t, out.Plan)); len(lines) != 8 {
		t.Fatalf("widened plan lines = %d, want 8", len(lines))
	}

	renamed := stageRecord(t, rt, subscriptionRecord{ID: "jp", Source: subscriptionSourceFleet, Process: []json.RawMessage{structuredKeep("protocol", "trojan"), renameStep("tokyo", "osaka")}})
	reads = host.calls
	out, res = renderFleetCall(t, rt, map[string]any{"subscription_id": "jp", "raw": fetched.Raw, "target": "sing-box", "revision": renamed})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if host.calls != reads {
		t.Fatalf("a staged revision with the same leading run read %d catalogue pages", host.calls-reads)
	}
	plan := decodeRenderPlan(t, out.Plan)
	if len(planLines(plan)) != 4 {
		t.Fatalf("renamed plan lines = %d, want the snapshot's 4", len(planLines(plan)))
	}
	for _, name := range planNames(t, plan) {
		if !strings.HasPrefix(name, "osaka") {
			t.Fatalf("the staged chain did not run: node %q", name)
		}
	}
}

// TestRenderServesStagedDuringHalfDonePromotion pins the render half of a
// promotion killed after its index put: the entry names the staged
// revision while record-v2 is still the old one, and render serves the
// document being promoted, reports its revision as live, and refuses the
// old revision by name.
func TestRenderServesStagedDuringHalfDonePromotion(t *testing.T) {
	rt, _ := newFleetRuntime(t, fleetTestRows(3, "trojan"))
	before := saveFleetRecord(t, rt, subscriptionRecord{ID: "jp"})
	fetched, err := rt.fetchSubscription("jp")
	if err != nil {
		t.Fatal(err)
	}
	staged := stageRecord(t, rt, subscriptionRecord{ID: "jp", Source: subscriptionSourceFleet, Process: []json.RawMessage{renameStep("tokyo", "osaka")}})
	// The index half of the promotion, and nothing else.
	idx, err := rt.writableIndex()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := rt.loadStaged("jp")
	if err != nil || doc == nil {
		t.Fatalf("staged document: %v", err)
	}
	pos := idx.position("jp")
	entry := indexEntryFor(doc.Record)
	entry.carryBookkeeping(idx.Records[pos])
	entry.clearStaged()
	idx.Records[pos] = entry
	if err := rt.putIndex(idx, true); err != nil {
		t.Fatal(err)
	}
	if rec, _, _ := rt.loadRecord("jp"); rec.Revision != before.Revision {
		t.Fatalf("record-v2 moved to %s; the fixture is not a half-done promotion", rec.Revision)
	}

	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "jp", "raw": fetched.Raw, "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if out.LiveRevision != staged {
		t.Fatalf("live revision = %q, want the promoted %q", out.LiveRevision, staged)
	}
	for _, name := range planNames(t, decodeRenderPlan(t, out.Plan)) {
		if !strings.HasPrefix(name, "osaka") {
			t.Fatalf("render served the old revision: node %q", name)
		}
	}
	if _, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "jp", "raw": fetched.Raw, "target": "sing-box", "revision": before.Revision}); res.OK || !strings.HasPrefix(res.Error, codeRevisionUnknown) {
		t.Fatalf("the replaced revision by name: ok=%v error=%q", res.OK, res.Error)
	}
	// Render never completes the promotion: that is fetch's.
	stored, err := decodeRecordDoc("jp", rt.host.(*fleetCatalogueHost).values[recordKey("jp")])
	if err != nil || withRevision(stored).Revision != before.Revision {
		t.Fatalf("render wrote record-v2 (%v)", err)
	}
}

// TestRenderRefusesStoreInconsistent pins that an entry naming a revision no
// document holds refuses store_inconsistent instead of rendering anything.
func TestRenderRefusesStoreInconsistent(t *testing.T) {
	rt, host := newFleetRuntime(t, fleetTestRows(2, "trojan"))
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp"})
	delete(host.values, recordKey("jp"))
	if _, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "jp", "raw": "", "target": "sing-box"}); res.OK || !strings.HasPrefix(res.Error, codeStoreInconsistent) {
		t.Fatalf("ok=%v error=%q", res.OK, res.Error)
	}
}

// mixedCollectionWithPendingMigration is collection "c" over fleet "f" and
// legacy "l", live (seeded, as no write method can reach it), with l's
// migration to a fleet record staged.
func mixedCollectionWithPendingMigration(t *testing.T) (*runtime, *fleetCatalogueHost, string) {
	t.Helper()
	rt, host := newFleetRuntime(t, fleetTestRows(3, "trojan"))
	saveFleetRecord(t, rt, subscriptionRecord{ID: "f"})
	if err := rt.saveSubscription(subscriptionRecord{ID: "l", Name: "l", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f", "l"}}); err != nil {
		t.Fatal(err)
	}
	return rt, host, stageRecord(t, rt, subscriptionRecord{ID: "l", Source: subscriptionSourceFleet})
}

// TestRenderRefusesMixedCollectionFromIndexFlags pins both halves of the
// mixing rule at render. On the serve path a collection whose entry carries
// fleet_bound and owner_credentials refuses fleet_mixed_owner_credentials
// before any block is read. A staged render skips that flag check and runs
// the mixing guard over the resolved members' sources, so the "to" side of
// the rest of a migration renders a plan while the live entry still
// carries both flags.
func TestRenderRefusesMixedCollectionFromIndexFlags(t *testing.T) {
	rt, host, stagedL := mixedCollectionWithPendingMigration(t)
	entry, _ := entryOf(t, rt, "c")
	if !entry.Flags.FleetBound || !entry.Flags.OwnerCredentials {
		t.Fatalf("flags = %+v, want both", entry.Flags)
	}
	_, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": `{"version":2,"kind":"collection"}`, "target": "sing-box"})
	if res.OK || !strings.HasPrefix(res.Error, codeFleetMixedOwnerCredentials) {
		t.Fatalf("serve: ok=%v error=%q", res.OK, res.Error)
	}
	c, _ := rt.getSubscription("c")
	f, _ := rt.getSubscription("f")
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "target": "sing-box", "revision": c.Revision,
		"member_revisions": map[string]string{"f": f.Revision, "l": stagedL}})
	if !res.OK {
		t.Fatalf("staged: %s", res.Error)
	}
	if lines := planLines(decodeRenderPlan(t, out.Plan)); len(lines) != 6 {
		t.Fatalf("staged plan lines = %d, want both members' 3", len(lines))
	}
	if host.other != 0 {
		t.Fatalf("the staged render made %d non-catalogue rpc calls (an export read?)", host.other)
	}
	// Naming the legacy member at its live revision is still mixed.
	l, _ := rt.getSubscription("l")
	_, res = renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "target": "sing-box", "revision": c.Revision,
		"member_revisions": map[string]string{"l": l.Revision}})
	if res.OK || !strings.HasPrefix(res.Error, codeFleetMixedOwnerCredentials) {
		t.Fatalf("staged over the legacy member: ok=%v error=%q", res.OK, res.Error)
	}
}

// fleetAndProviderCollection is collection "c" over fleet "f" (lines 1 to
// 4, keep trojan) and remote provider "p", fetched.
func fleetAndProviderCollection(t *testing.T) (*runtime, *fleetCatalogueHost, string) {
	t.Helper()
	rt, host := newFleetRuntime(t, mixedProtocolRows())
	host.httpBody = "trojan://pw1@198.51.100.7:443#provider-one\ntrojan://pw2@198.51.100.8:443#provider-two"
	saveFleetRecord(t, rt, subscriptionRecord{ID: "f", Process: []json.RawMessage{structuredKeep("protocol", "trojan")}})
	if err := rt.saveSubscription(subscriptionRecord{ID: "p", Name: "p", Source: subscriptionSourceRemote, URL: "https://provider.example/sub", UA: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f", "p"}}); err != nil {
		t.Fatal(err)
	}
	fetched, err := rt.fetchSubscription("c")
	if err != nil {
		t.Fatal(err)
	}
	return rt, host, fetched.Raw
}

// providerNodes is the encoding of every provider node of a plan.
func providerNodes(plan model.SelectionPlan) []string {
	var out []string
	for _, node := range plan.Nodes {
		if node.Provider {
			out = append(out, string(node.Node))
		}
	}
	return out
}

// TestRenderMemberRevisionsResolveStagedMembers pins how a plan over a sub
// previews the collections it feeds: the collection rendered at its own
// live revision with member_revisions naming the sub's staged revision
// renders that member from staged-v2, here a widened selection read live.
func TestRenderMemberRevisionsResolveStagedMembers(t *testing.T) {
	rt, _, snapshot := fleetAndProviderCollection(t)
	c, _ := rt.getSubscription("c")
	from, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": snapshot, "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if lines := planLines(decodeRenderPlan(t, from.Plan)); len(lines) != 4 {
		t.Fatalf("from lines = %d, want 4", len(lines))
	}
	widened := stageRecord(t, rt, subscriptionRecord{ID: "f", Source: subscriptionSourceFleet, Process: []json.RawMessage{structuredKeep("protocol", "trojan", "vless")}})
	to, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": snapshot, "target": "sing-box", "revision": c.Revision,
		"member_revisions": map[string]string{"f": widened}})
	if !res.OK {
		t.Fatal(res.Error)
	}
	plan := decodeRenderPlan(t, to.Plan)
	if lines := planLines(plan); len(lines) != 8 || len(plan.Selection.LineUUIDs) != 8 {
		t.Fatalf("to lines = %d, selection %d, want the widened 8", len(lines), len(plan.Selection.LineUUIDs))
	}
	if to.LiveRevision != c.Revision {
		t.Fatalf("live revision = %q", to.LiveRevision)
	}
}

// TestStagedCollectionRenderReusesProviderBlocks pins that a staged
// collection render reads a provider member from its block, never live:
// core folds every provider entry under one plan line and compares it byte
// for byte at the decision, so a refetch would show a change that never
// happened.
func TestStagedCollectionRenderReusesProviderBlocks(t *testing.T) {
	rt, host, snapshot := fleetAndProviderCollection(t)
	c, _ := rt.getSubscription("c")
	from, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": snapshot, "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	widened := stageRecord(t, rt, subscriptionRecord{ID: "f", Source: subscriptionSourceFleet, Process: []json.RawMessage{structuredKeep("protocol", "trojan", "vless")}})
	fetches := host.httpCalls
	host.httpBody = "trojan://changed@198.51.100.9:443#moved-since"
	to, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": snapshot, "target": "sing-box", "revision": c.Revision,
		"member_revisions": map[string]string{"f": widened}})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if host.httpCalls != fetches {
		t.Fatalf("the staged render fetched the provider %d times", host.httpCalls-fetches)
	}
	a, b := providerNodes(decodeRenderPlan(t, from.Plan)), providerNodes(decodeRenderPlan(t, to.Plan))
	if len(a) != 2 || strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Fatalf("provider nodes from %v to %v", a, b)
	}
}

// TestStagedCollectionRenderFetchesMemberSwitchedFromFleetToProvider pins
// the source-equality rule: a fleet member re-staged as a provider has no
// block of its source, so it is resolved live like an added provider member
// and its content shows as provider nodes, never as the fleet block's
// rows or an empty provider.
func TestStagedCollectionRenderFetchesMemberSwitchedFromFleetToProvider(t *testing.T) {
	for _, source := range []string{subscriptionSourceRemote, subscriptionSourceLocal} {
		t.Run(source, func(t *testing.T) {
			rt, host := newFleetRuntime(t, fleetTestRows(3, "trojan"))
			saveFleetRecord(t, rt, subscriptionRecord{ID: "f"})
			saveFleetRecord(t, rt, subscriptionRecord{ID: "g"})
			if err := rt.saveSubscription(subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f", "g"}}); err != nil {
				t.Fatal(err)
			}
			fetched, err := rt.fetchSubscription("c")
			if err != nil {
				t.Fatal(err)
			}
			const pasted = "trojan://pasted@198.51.100.20:443#pasted-one"
			switched := subscriptionRecord{ID: "g", Source: source}
			if source == subscriptionSourceRemote {
				switched.URL, switched.UA = "https://provider.example/g", "test"
				host.httpBody = pasted
			} else {
				switched.Content = pasted
			}
			stagedG := stageRecord(t, rt, switched)
			c, _ := rt.getSubscription("c")
			out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": fetched.Raw, "target": "sing-box", "revision": c.Revision,
				"member_revisions": map[string]string{"g": stagedG}})
			if !res.OK {
				t.Fatal(res.Error)
			}
			plan := decodeRenderPlan(t, out.Plan)
			provider := providerNodes(plan)
			if len(provider) != 1 || !strings.Contains(provider[0], "pasted-one") {
				t.Fatalf("provider nodes = %v, want the switched member's pasted node", provider)
			}
			if lines := planLines(plan); len(lines) != 3 {
				t.Fatalf("fleet lines = %d, want only f's 3", len(lines))
			}
		})
	}
}

// TestRenderStagedCollectionAddsExplicitMember pins the membership
// recompute: a staged collection revision that adds an explicit fleet
// member with no block in the snapshot reads that member's lines live and
// carries them in the plan.
func TestRenderStagedCollectionAddsExplicitMember(t *testing.T) {
	rt, host := newFleetRuntime(t, nil)
	host.readSets = [][]model.LineCatalogueRow{fleetTestRows(2, "trojan"), {fleetTestRow(7, "vless"), fleetTestRow(8, "vless")}}
	saveFleetRecord(t, rt, subscriptionRecord{ID: "f1"})
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f1"}}); err != nil {
		t.Fatal(err)
	}
	fetched, err := rt.fetchSubscription("c")
	if err != nil {
		t.Fatal(err)
	}
	saveFleetRecord(t, rt, subscriptionRecord{ID: "f2"})
	staged := stageRecord(t, rt, subscriptionRecord{ID: "c", Kind: kindCollection, Members: []string{"f1", "f2"}})
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": fetched.Raw, "target": "sing-box", "revision": staged})
	if !res.OK {
		t.Fatal(res.Error)
	}
	lines := planLines(decodeRenderPlan(t, out.Plan))
	if len(lines) != 4 || lines[2] != fleetTestLineUUID(7) || lines[3] != fleetTestLineUUID(8) {
		t.Fatalf("plan lines = %v, want f1's 2 then the added f2's 2", lines)
	}
}

// TestRenderMemberRevisionDroppedTagRemovesMember pins that a member whose
// named staged revision no longer carries the tag the collection gathers
// leaves the collection's plan.
func TestRenderMemberRevisionDroppedTagRemovesMember(t *testing.T) {
	rt, host := newFleetRuntime(t, nil)
	host.readSets = [][]model.LineCatalogueRow{fleetTestRows(2, "trojan"), {fleetTestRow(7, "vless")}}
	saveFleetRecord(t, rt, subscriptionRecord{ID: "f1", Tags: []string{"asia"}})
	saveFleetRecord(t, rt, subscriptionRecord{ID: "f2", Tags: []string{"asia"}})
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, MemberTags: []string{"asia"}}); err != nil {
		t.Fatal(err)
	}
	fetched, err := rt.fetchSubscription("c")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := rt.getSubscription("c")
	untagged := stageRecord(t, rt, subscriptionRecord{ID: "f2", Source: subscriptionSourceFleet})
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": fetched.Raw, "target": "sing-box", "revision": c.Revision,
		"member_revisions": map[string]string{"f2": untagged}})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if lines := planLines(decodeRenderPlan(t, out.Plan)); len(lines) != 2 || lines[0] != fleetTestLineUUID(1) {
		t.Fatalf("plan lines = %v, want only f1's", lines)
	}
	// "" names a deleted member, which contributes nothing either.
	out, res = renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": fetched.Raw, "target": "sing-box", "revision": c.Revision,
		"member_revisions": map[string]string{"f1": ""}})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if lines := planLines(decodeRenderPlan(t, out.Plan)); len(lines) != 1 || lines[0] != fleetTestLineUUID(7) {
		t.Fatalf("plan lines = %v, want only f2's", lines)
	}
}

// TestStagedCollectionRefusesS1ShapedBlocks pins that a staged render never
// reads a block that names no member or source as provider content.
func TestStagedCollectionRefusesS1ShapedBlocks(t *testing.T) {
	rt, _, _ := fleetAndProviderCollection(t)
	c, _ := rt.getSubscription("c")
	f, _ := rt.getSubscription("f")
	s1 := snapshotEnvelope{Version: snapshotEnvelopeVersion, Kind: kindCollection, Members: []envelopeMember{{SubName: "p", Raw: "trojan://x@198.51.100.1:443#s1"}}}
	raw, _ := json.Marshal(s1)
	_, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": string(raw), "target": "sing-box", "revision": c.Revision,
		"member_revisions": map[string]string{"f": f.Revision}})
	if res.OK || !strings.HasPrefix(res.Error, codeSnapshotPredatesS2) {
		t.Fatalf("ok=%v error=%q", res.OK, res.Error)
	}
}

// TestPlanCacheBypassedForStagedRenders pins that a staged render neither
// reads nor writes the plan cache: a dependent collection's "to" render has
// the same snapshot, record revision and member list as its "from" render,
// so a shared key would hand the "from" plan back as the "to" plan.
func TestPlanCacheBypassedForStagedRenders(t *testing.T) {
	freshFleetPlans(t)
	rt, _, snapshot := fleetAndProviderCollection(t)
	c, _ := rt.getSubscription("c")
	widened := stageRecord(t, rt, subscriptionRecord{ID: "f", Source: subscriptionSourceFleet, Process: []json.RawMessage{structuredKeep("protocol", "trojan", "vless")}})
	before := fleetPlans.computeCount()
	from, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": snapshot, "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	to, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": snapshot, "target": "sing-box", "revision": c.Revision,
		"member_revisions": map[string]string{"f": widened}})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if got := fleetPlans.computeCount() - before; got != 2 || len(planLines(decodeRenderPlan(t, from.Plan))) == len(planLines(decodeRenderPlan(t, to.Plan))) {
		t.Fatalf("computes = %d, from and to plans alike", got)
	}
	// Again: the serve path hits, the staged render computes again.
	renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": snapshot, "target": "sing-box"})
	renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": snapshot, "target": "sing-box", "revision": c.Revision,
		"member_revisions": map[string]string{"f": widened}})
	if got := fleetPlans.computeCount() - before; got != 3 {
		t.Fatalf("computes after a repeat = %d, want 3 (one hit, one staged compute)", got)
	}
}

// TestPlanCacheMissesOnMemberRevision pins that a collection's plan key
// carries its members' revisions as the snapshot's blocks name them: two
// snapshots that differ only in a member revision compute two plans, so a
// worker never serves a cached collection plan across an approved change to
// a member's non-structured step.
func TestPlanCacheMissesOnMemberRevision(t *testing.T) {
	freshFleetPlans(t)
	rt, host := newFleetRuntime(t, nil)
	host.readSets = [][]model.LineCatalogueRow{fleetTestRows(2, "trojan")}
	saveFleetRecord(t, rt, subscriptionRecord{ID: "f"})
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f"}}); err != nil {
		t.Fatal(err)
	}
	fetched, err := rt.fetchSubscription("c")
	if err != nil {
		t.Fatal(err)
	}
	env, _ := decodeSnapshotEnvelope(fetched.Raw)
	before := fleetPlans.computeCount()
	if _, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": fetched.Raw, "target": "sing-box"}); !res.OK {
		t.Fatal(res.Error)
	}
	// The same source_version (the envelope's copy core never reads) with
	// another member revision and a rename in the member's steps.
	env.Members[0].Revision = "r-next"
	env.Members[0].Steps = []json.RawMessage{renameStep("tokyo", "osaka")}
	moved, _ := json.Marshal(env)
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": string(moved), "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if got := fleetPlans.computeCount() - before; got != 2 {
		t.Fatalf("computes = %d, want 2", got)
	}
	for _, name := range planNames(t, decodeRenderPlan(t, out.Plan)) {
		if !strings.HasPrefix(name, "osaka") {
			t.Fatalf("the cached plan was served across a member revision: %q", name)
		}
	}
}

// TestCollectionSnapshotFetchedBeforeApplyIsDiscarded pins the plugin's half
// of the apply rule: a collection snapshot names every member at the
// revision it was read at, so after a member's promotion the next fetch
// writes another source_version inside raw (core keys its body cache on the
// hash of raw, which moves with it) and the old
// snapshot's plan is never reused for it, while core's generation bump
// discards a refresh that was in flight.
func TestCollectionSnapshotFetchedBeforeApplyIsDiscarded(t *testing.T) {
	rt, host := newFleetRuntime(t, nil)
	host.readSets = [][]model.LineCatalogueRow{fleetTestRows(2, "trojan")}
	saveFleetRecord(t, rt, subscriptionRecord{ID: "f"})
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f"}}); err != nil {
		t.Fatal(err)
	}
	before, err := rt.fetchSubscription("c")
	if err != nil {
		t.Fatal(err)
	}
	stageRecord(t, rt, subscriptionRecord{ID: "f", Source: subscriptionSourceFleet, Process: []json.RawMessage{renameStep("tokyo", "osaka")}})
	promoteStaged(t, rt, "f")
	f, _ := rt.getSubscription("f")
	after, err := rt.fetchSubscription("c")
	if err != nil {
		t.Fatal(err)
	}
	if after.SourceVersion == before.SourceVersion {
		t.Fatal("the collection's source_version did not move with its member's promotion")
	}
	env, _ := decodeSnapshotEnvelope(after.Raw)
	if env.Members[0].Revision != f.Revision {
		t.Fatalf("block revision %q, want the promoted %q", env.Members[0].Revision, f.Revision)
	}
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": after.Raw, "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	for _, name := range planNames(t, decodeRenderPlan(t, out.Plan)) {
		if !strings.HasPrefix(name, "osaka") {
			t.Fatalf("the render served the member's old chain: %q", name)
		}
	}
}
