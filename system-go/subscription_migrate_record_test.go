package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-sdk/model"
)

// migrate_record (plan sections 2.7 and 6): the fleet record a legacy record
// becomes, under the same id, staged and never proposed.

const graphRootC = "33333333-3333-4333-8333-333333333333"

// migrationCatalogue answers the catalogue page migrate_record reads with
// the given roots, each labelled "Tokyo <n> line-<n>".
func migrationCatalogue(t *testing.T, host *casKVHost, roots ...string) {
	t.Helper()
	page := model.LineCatalogueResponse{CatalogueVersion: fleetTestVersion, Rows: []model.LineCatalogueRow{}}
	for i, root := range roots {
		row := fleetTestRow(i+1, "vless")
		row.LineUUID = root
		row.Label = "Tokyo " + string(rune('1'+i)) + " line-" + string(rune('1'+i))
		page.Rows = append(page.Rows, row)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	host.rpc[vpnCoreLinesService+"/catalogue"] = raw
}

func graphRecord(id string, roots ...string) subscriptionRecord {
	return subscriptionRecord{
		ID: id, Name: id, DisplayName: "Graph " + id, Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "vu_7",
		EntryRoots: roots, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64), Target: "ClashMeta", Tags: []string{"legacy"},
		Process: []json.RawMessage{json.RawMessage(`{"type":"Flag Operator","args":{}}`)},
	}
}

func migrateVia(t *testing.T, rt *runtime, id string) (migrateRecordReply, response) {
	t.Helper()
	res := callSubscription(t, rt, "migrate_record", map[string]any{"subscription_id": id})
	var reply migrateRecordReply
	if res.OK && !isRefusedResponse(res) {
		if err := json.Unmarshal(res.Result, &reply); err != nil {
			t.Fatal(err)
		}
	}
	return reply, res
}

// TestMigrateRecordGraphPinsRootsInOrder: a graph record becomes a fleet
// record whose first step keeps line_uuid in the committed roots in their
// order, ahead of the carried chain; the report reads one catalogue page
// under that selector and warns on a root the catalogue lacks.
func TestMigrateRecordGraphPinsRootsInOrder(t *testing.T) {
	rt, host := newCASRuntime(t)
	if err := rt.saveSubscription(graphRecord("g", graphRootC, graphRootA, graphRootB)); err != nil {
		t.Fatal(err)
	}
	migrationCatalogue(t, host, graphRootA, graphRootB)
	reply, res := migrateVia(t, rt, "g")
	if !res.OK || reply.StagedRevision == "" {
		t.Fatalf("migrate_record = %+v %s", reply, res.Error)
	}
	doc, _ := rt.loadStaged("g")
	steps := processSteps(doc.Record)
	if len(steps) != 2 || !strings.Contains(string(steps[1]), "Flag Operator") {
		t.Fatalf("migrated chain = %s", steps)
	}
	var pin struct {
		Type string                         `json:"type"`
		Args operators.StructuredFilterArgs `json:"args"`
	}
	if err := json.Unmarshal(steps[0], &pin); err != nil || pin.Type != operators.StructuredFilterType || len(pin.Args.Predicates) != 1 {
		t.Fatalf("pin step = %s %v", steps[0], err)
	}
	if got := pin.Args.Predicates[0]; got.Field != "line_uuid" || got.Op != "in" || strings.Join(got.Values, ",") != strings.Join([]string{graphRootC, graphRootA, graphRootB}, ",") {
		t.Fatalf("pin predicate = %+v, want line_uuid in the roots in committed order", got)
	}
	if reply.Selection.Count != 2 || strings.Join(reply.Selection.LineUUIDs, ",") != graphRootA+","+graphRootB || reply.Selection.CatalogueVersion != fleetTestVersion {
		t.Fatalf("selection = %+v", reply.Selection)
	}
	if strings.Join(reply.Warnings, "|") != "root "+graphRootC+" is not in the catalogue" {
		t.Fatalf("warnings = %v", reply.Warnings)
	}
	if len(host.rpcCalls) != 1 || host.rpcCalls[0] != vpnCoreLinesService+"/catalogue" {
		t.Fatalf("rpc calls = %v, want one catalogue page", host.rpcCalls)
	}
	if plan, err := operators.Compile("", steps); err != nil || operators.LeadingRun(plan) != 1 {
		t.Fatalf("the pin is not a leading structured run: %v", err)
	}
}

// TestMigrateRecordExportSelectsEverything: an export record adds no
// structured step (every line the share's identity is bound to) and reads no
// catalogue page.
func TestMigrateRecordExportSelectsEverything(t *testing.T) {
	rt, host := newCASRuntime(t)
	legacy := subscriptionRecord{ID: "e", Name: "e", Source: subscriptionSourceVPNCore, VPNIdentity: "proxy-user-1",
		Process: []json.RawMessage{json.RawMessage(`{"type":"Regex Filter","args":{"regex":["HK"]}}`)}}
	if err := rt.saveSubscription(legacy); err != nil {
		t.Fatal(err)
	}
	reply, res := migrateVia(t, rt, "e")
	if !res.OK || !reply.Selection.EveryBoundLine || reply.Selection.Count != 0 || len(reply.Selection.LineUUIDs) != 0 {
		t.Fatalf("export migration = %+v %s", reply, res.Error)
	}
	doc, _ := rt.loadStaged("e")
	if steps := processSteps(doc.Record); len(steps) != 1 || !strings.Contains(string(steps[0]), "Regex Filter") {
		t.Fatalf("migrated chain = %s", steps)
	}
	if len(host.rpcCalls) != 0 {
		t.Fatalf("an export migration read %v", host.rpcCalls)
	}
}

// TestMigrateRecordKeepsIdAndClearsLegacyFields: same id, fleet source, the
// legacy fields moved to MigratedFrom and cleared, everything else carried;
// the live record stays legacy until a plan promotes the staged one.
func TestMigrateRecordKeepsIdAndClearsLegacyFields(t *testing.T) {
	rt, host := newCASRuntime(t)
	legacy := graphRecord("g", graphRootA)
	if err := rt.saveSubscription(legacy); err != nil {
		t.Fatal(err)
	}
	live, _ := entryOf(t, rt, "g")
	migrationCatalogue(t, host, graphRootA)
	reply, res := migrateVia(t, rt, "g")
	if !res.OK || reply.LiveRevision != live.Revision || reply.SubscriptionID != "g" {
		t.Fatalf("reply = %+v %s", reply, res.Error)
	}
	if reply.Legacy.Source != subscriptionSourceVPNCoreGraph || reply.Legacy.VPNIdentity != "vu_7" || strings.Join(reply.Legacy.EntryRoots, ",") != graphRootA {
		t.Fatalf("legacy block = %+v", reply.Legacy)
	}
	doc, _ := rt.loadStaged("g")
	rec := doc.Record
	if rec.ID != "g" || rec.Source != subscriptionSourceFleet || rec.VPNIdentity != "" || len(rec.EntryRoots) != 0 || rec.GraphOptionsVersion != "" || rec.URL != "" || rec.Content != "" {
		t.Fatalf("staged record kept a legacy field: %+v", rec)
	}
	if rec.Name != "g" || rec.DisplayName != "Graph g" || rec.Target != "ClashMeta" || strings.Join(rec.Tags, ",") != "legacy" {
		t.Fatalf("staged record lost a carried field: %+v", rec)
	}
	if rec.MigratedFrom == nil || rec.MigratedFrom.Source != subscriptionSourceVPNCoreGraph || rec.MigratedFrom.VPNIdentity != "vu_7" || rec.MigratedFrom.MigratedAt == "" {
		t.Fatalf("migrated_from = %+v", rec.MigratedFrom)
	}
	stored, err := rt.getSubscription("g")
	if err != nil || stored.Source != subscriptionSourceVPNCoreGraph || stored.Revision != live.Revision {
		t.Fatalf("the live record moved: %+v %v", stored, err)
	}
}

// TestMigrateRecordStages: the staged facts land on the entry (staged_migrated
// among them) over the live revision, and the live flags are unchanged.
func TestMigrateRecordStages(t *testing.T) {
	rt, _ := newCASRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "e", Name: "e", Source: subscriptionSourceVPNCore, Tags: []string{"old"}}); err != nil {
		t.Fatal(err)
	}
	live, _ := entryOf(t, rt, "e")
	reply, res := migrateVia(t, rt, "e")
	if !res.OK {
		t.Fatal(res.Error)
	}
	entry, _ := entryOf(t, rt, "e")
	doc, _ := rt.loadStaged("e")
	if entry.Revision != live.Revision || entry.StagedRevision != reply.StagedRevision || entry.StagedSource != subscriptionSourceFleet || !entry.StagedMigrated || doc.BaseRevision != live.Revision {
		t.Fatalf("entry %+v staged %+v", entry, doc)
	}
	if !entry.Flags.OwnerCredentials || entry.Flags.FleetBound {
		t.Fatalf("the live flags read staged facts: %+v", entry.Flags)
	}
	// Migrating again replaces the staged revision with the same content.
	again, res := migrateVia(t, rt, "e")
	if !res.OK || again.StagedRevision == "" {
		t.Fatalf("a second migrate_record = %+v %s", again, res.Error)
	}
}

// TestMigrateRecordListsDependents: the collections that read the record,
// explicitly or by tag, are listed; they are what the plan will cover.
func TestMigrateRecordListsDependents(t *testing.T) {
	rt, _ := newCASRuntime(t)
	for _, rec := range []subscriptionRecord{
		{ID: "e", Name: "e", Source: subscriptionSourceVPNCore, Tags: []string{"old"}},
		{ID: "p", Name: "p", Source: subscriptionSourceRemote, URL: "https://provider.invalid/p"},
		{ID: "by-id", Name: "by-id", Kind: kindCollection, Members: []string{"e", "p"}},
		{ID: "by-tag", Name: "by-tag", Kind: kindCollection, MemberTags: []string{"old"}},
		{ID: "unrelated", Name: "unrelated", Kind: kindCollection, Members: []string{"p"}},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatal(err)
		}
	}
	reply, res := migrateVia(t, rt, "e")
	if !res.OK || strings.Join(reply.Dependents.Collections, ",") != "by-id,by-tag" || len(reply.Dependents.CoMigrate) != 0 {
		t.Fatalf("dependents = %+v %s", reply.Dependents, res.Error)
	}
}

// TestMigrateRecordListsCoMigrateSiblings: a collection with two legacy
// members has no order in which one can go first, so the first member's
// migration stages (the mixing guard reads live state here, with the record
// at its live state, so the collection is not mixed by this write) and names
// the other as co_migrate.
func TestMigrateRecordListsCoMigrateSiblings(t *testing.T) {
	rt, _ := newCASRuntime(t)
	for _, rec := range []subscriptionRecord{
		{ID: "l1", Name: "l1", Source: subscriptionSourceVPNCore},
		{ID: "l2", Name: "l2", Source: subscriptionSourceVPNCore, Tags: []string{"old"}},
		{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"l1"}, MemberTags: []string{"old"}},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatal(err)
		}
	}
	reply, res := migrateVia(t, rt, "l1")
	if !res.OK || reply.StagedRevision == "" || strings.Join(reply.Dependents.Collections, ",") != "c" || strings.Join(reply.Dependents.CoMigrate, ",") != "l2" {
		t.Fatalf("first member = %+v %s %s", reply, res.Error, refusalText(res))
	}
	// The second member migrates too, with the first one's migration
	// pending: over the union its co-member would already count as fleet.
	reply, res = migrateVia(t, rt, "l2")
	if !res.OK || reply.StagedRevision == "" || strings.Join(reply.Dependents.CoMigrate, ",") != "l1" {
		t.Fatalf("second member = %+v %s %s", reply, res.Error, refusalText(res))
	}
}

// TestMigrateRecordRefusesFileDependents: a file whose node source is the
// record makes the migration refuse, naming the file, before anything is
// staged.
func TestMigrateRecordRefusesFileDependents(t *testing.T) {
	rt, host := newCASRuntime(t)
	for _, rec := range []subscriptionRecord{
		{ID: "e", Name: "e", Source: subscriptionSourceVPNCore},
		{ID: "cfg", Name: "cfg", Kind: kindFile, NodeSource: "e", Content: "proxies: []"},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatal(err)
		}
	}
	puts := host.puts
	_, res := migrateVia(t, rt, "e")
	r, refused := refusalOf(res)
	if !refused || r.Code != refusedFleetFileUnavailable || strings.Join(r.Files, ",") != "cfg" {
		t.Fatalf("migrate_record with a file dependent = %+v %s", r, res.Error)
	}
	if host.puts != puts {
		t.Fatal("the refused migration wrote")
	}
}

// TestMigrateRecordRefusesFileOverDependentCollection: a file over a
// collection that reads the record refuses too, naming the file and the
// collection; migrate_record checks these files itself, because the
// collections are left out of the live-state rules call.
func TestMigrateRecordRefusesFileOverDependentCollection(t *testing.T) {
	rt, _ := newCASRuntime(t)
	for _, rec := range []subscriptionRecord{
		{ID: "e", Name: "e", Source: subscriptionSourceVPNCore, Tags: []string{"old"}},
		{ID: "c", Name: "c", Kind: kindCollection, MemberTags: []string{"old"}},
		{ID: "cfg", Name: "cfg", Kind: kindFile, NodeSource: "c", Content: "proxies: []"},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatal(err)
		}
	}
	_, res := migrateVia(t, rt, "e")
	r, refused := refusalOf(res)
	if !refused || r.Code != refusedFleetFileUnavailable || strings.Join(r.Files, ",") != "cfg" || strings.Join(r.Collections, ",") != "c" {
		t.Fatalf("migrate_record under a file over a collection = %+v %s", r, res.Error)
	}
	if entry, _ := entryOf(t, rt, "e"); entry.hasStaged() {
		t.Fatal("the refused migration staged")
	}
}

// TestMigrateRecordWarnsOnNameStepsMatchingNothing: per carried-over
// name-based step, the method counts the lines it matches under the fleet
// labels and warns on zero, numbering steps as the migrated record carries
// them; a step that matches something, and a disabled one, do not warn.
func TestMigrateRecordWarnsOnNameStepsMatchingNothing(t *testing.T) {
	rt, host := newCASRuntime(t)
	legacy := graphRecord("g", graphRootA, graphRootB)
	legacy.Process = []json.RawMessage{
		json.RawMessage(`{"type":"Regex Filter","args":{"regex":["^US "]}}`),
		json.RawMessage(`{"type":"Regex Rename Operator","args":[{"expr":"line-","now":"L"}]}`),
		json.RawMessage(`{"type":"Regex Delete Operator","disabled":true,"args":["^nothing"]}`),
		json.RawMessage(`{"type":"Regex Delete Operator","args":["^nomatch"]}`),
	}
	if err := rt.saveSubscription(legacy); err != nil {
		t.Fatal(err)
	}
	migrationCatalogue(t, host, graphRootA, graphRootB)
	reply, res := migrateVia(t, rt, "g")
	if !res.OK {
		t.Fatal(res.Error)
	}
	want := []string{
		"step 2 (Regex Filter) matches 0 of 2 lines under the fleet labels",
		"step 5 (Regex Delete Operator) matches 0 of 2 lines under the fleet labels",
	}
	if strings.Join(reply.Warnings, "|") != strings.Join(want, "|") {
		t.Fatalf("warnings = %q, want %q", reply.Warnings, want)
	}
}
