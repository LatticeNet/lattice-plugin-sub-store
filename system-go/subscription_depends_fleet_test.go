package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// dependsFixture is a store with a fleet record read by two collections (by
// id and by tag), a legacy record read by a collection that a file reads,
// a provider record nobody reads, and an archived legacy record.
func dependsFixture(t *testing.T) *runtime {
	t.Helper()
	rt, _ := newFleetRuntime(t, nil)
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp", Tags: []string{"asia"}})
	for _, rec := range []subscriptionRecord{
		{ID: "legacy", Name: "legacy", Source: subscriptionSourceVPNCore, Tags: []string{"old"}},
		{ID: "provider", Name: "provider", Source: subscriptionSourceRemote, URL: "https://provider.invalid/sub"},
		{ID: "by-id", Name: "by-id", Kind: kindCollection, Members: []string{"jp", "provider"}},
		{ID: "by-tag", Name: "by-tag", Kind: kindCollection, MemberTags: []string{"asia"}},
		{ID: "old-coll", Name: "old-coll", Kind: kindCollection, MemberTags: []string{"old"}},
		{ID: "old-file", Name: "old-file", Kind: kindFile, Content: "proxies: []", NodeSource: "old-coll"},
		{ID: "retired", Name: "retired", Source: subscriptionSourceVPNCore},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatalf("save %s: %v", rec.ID, err)
		}
	}
	if err := rt.deleteSubscription("retired"); err != nil {
		t.Fatal(err)
	}
	return rt
}

func dependsCall(t *testing.T, rt *runtime, payload map[string]any) dependsOnReply {
	t.Helper()
	raw, _ := json.Marshal(payload)
	res := rt.handleSubscriptionCall(callPayload{Method: "depends_on", Payload: raw})
	if !res.OK {
		t.Fatal(res.Error)
	}
	var reply dependsOnReply
	if err := json.Unmarshal(res.Result, &reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

func recordIDs(reply dependsOnReply) string {
	ids := make([]string, 0, len(reply.Records))
	for _, rec := range reply.Records {
		ids = append(ids, rec.ID)
	}
	return strings.Join(ids, ",")
}

// TestDependsOnNamesFleetRecordsAndTheirReaders pins the fleet_bound
// closure: the fleet record and every collection that gathers it, by id or
// by tag, with the edges that make them fleet-bound and each row's live
// flag.
func TestDependsOnNamesFleetRecordsAndTheirReaders(t *testing.T) {
	reply := dependsCall(t, dependsFixture(t), map[string]any{})
	if got := strings.Join(reply.FleetBound, ","); got != "by-id,by-tag,jp" {
		t.Fatalf("fleet_bound = %s", got)
	}
	edges := fmt.Sprint(reply.Edges)
	for _, want := range []string{"{by-id jp member}", "{by-tag jp member_tag}", "{old-coll legacy member_tag}", "{old-file old-coll node_source}"} {
		if !strings.Contains(edges, want) {
			t.Errorf("edges %s lack %s", edges, want)
		}
	}
	for _, rec := range reply.Records {
		wantBound := rec.ID == "jp" || rec.ID == "by-id" || rec.ID == "by-tag"
		if rec.FleetBound != wantBound {
			t.Errorf("%s fleet_bound = %v", rec.ID, rec.FleetBound)
		}
		if rec.ID == "jp" && (rec.Kind != kindSub || rec.Source != subscriptionSourceFleet || rec.Revision == "") {
			t.Errorf("jp row = %+v", rec)
		}
	}
}

// TestOwnerCredentialsClosesOverReaders pins the owner_credentials closure:
// a legacy record, a collection gathering it, and a file over that
// collection all still serve owner credentials.
func TestOwnerCredentialsClosesOverReaders(t *testing.T) {
	reply := dependsCall(t, dependsFixture(t), map[string]any{})
	if got := strings.Join(reply.OwnerCredentials, ","); got != "legacy,old-coll,old-file" {
		t.Fatalf("owner_credentials = %s", got)
	}
}

// TestDependsOnKeepsLegacyRecordsInExpirySet pins the store-wide records:
// the union of both closures, so a legacy record with no reader stays in
// the set core expires on a fleet write, while a provider record nobody
// fleet-bound reads and an archived record stay out.
func TestDependsOnKeepsLegacyRecordsInExpirySet(t *testing.T) {
	reply := dependsCall(t, dependsFixture(t), map[string]any{})
	if got := recordIDs(reply); got != "by-id,by-tag,jp,legacy,old-coll,old-file" {
		t.Fatalf("records = %s", got)
	}
	again := dependsCall(t, dependsFixture(t), map[string]any{"include_staged": true})
	if again.Version != reply.Version {
		t.Fatal("with nothing staged the union answer differs from the live one")
	}
}

// TestDependsOnPerRecordClosure pins the per-record form: the record and
// every record that reads it, transitively, with the edges among them.
func TestDependsOnPerRecordClosure(t *testing.T) {
	rt := dependsFixture(t)
	reply := dependsCall(t, rt, map[string]any{"subscription_id": "legacy", "include_staged": true})
	if got := recordIDs(reply); got != "legacy,old-coll,old-file" {
		t.Fatalf("records = %s", got)
	}
	if got := fmt.Sprint(reply.Edges); got != "[{old-coll legacy member_tag} {old-file old-coll node_source}]" {
		t.Fatalf("edges = %s", got)
	}
	reply = dependsCall(t, rt, map[string]any{"subscription_id": "provider"})
	if got := recordIDs(reply); got != "by-id,provider" {
		t.Fatalf("a provider record's closure = %s", got)
	}
	raw, _ := json.Marshal(map[string]any{"subscription_id": "missing"})
	if res := rt.handleSubscriptionCall(callPayload{Method: "depends_on", Payload: raw}); res.OK {
		t.Fatal("depends_on answered for a record that does not exist")
	}
}
