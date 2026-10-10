package main

import (
	"strings"
	"testing"
)

type dependsReply struct {
	Records []dependsOnRecord `json:"records"`
	Version string            `json:"version"`
}

func dependsOnIDs(reply dependsReply) string {
	ids := make([]string, 0, len(reply.Records))
	for _, rec := range reply.Records {
		ids = append(ids, rec.ID)
	}
	return strings.Join(ids, ",")
}

// depends_on names every live record whose transitive sources include the
// fleet, from the index alone: the fleet subs, a collection with a fleet
// member named or tagged, and a file whose node source is either. A record
// that reads no fleet node, and an archived one, are left out.
func TestDependsOnNamesTransitiveFleetReaders(t *testing.T) {
	host := &budgetCountingHost{kvHostCaller: newKVHostCaller()}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	graph := subscriptionRecord{ID: "graph", Name: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64), Tags: []string{"fleet"}}
	for _, rec := range []subscriptionRecord{
		{ID: "fleet", Name: "fleet", Source: subscriptionSourceVPNCore},
		graph,
		{ID: "provider", Name: "provider", Source: subscriptionSourceRemote, URL: "https://provider.invalid/sub", Tags: []string{"other"}},
		{ID: "pasted", Name: "pasted", Source: subscriptionSourceLocal, Content: scriptNodeHome},
		{ID: "by-member", Name: "by-member", Kind: kindCollection, Members: []string{"provider", "fleet"}},
		{ID: "by-tag", Name: "by-tag", Kind: kindCollection, MemberTags: []string{"fleet"}},
		{ID: "no-fleet", Name: "no-fleet", Kind: kindCollection, Members: []string{"provider", "pasted"}, MemberTags: []string{"other"}},
		{ID: "file-over-collection", Name: "f1", Kind: kindFile, Content: "proxies: []", NodeSource: "by-member"},
		{ID: "file-over-provider", Name: "f2", Kind: kindFile, Content: "proxies: []", NodeSource: "provider"},
		{ID: "retired", Name: "retired", Source: subscriptionSourceVPNCore},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatalf("save %s: %v", rec.ID, err)
		}
	}
	if err := rt.deleteSubscription("retired"); err != nil {
		t.Fatal(err)
	}

	host.total = 0
	var reply dependsReply
	decodeResult(t, callSubscription(t, rt, "depends_on", map[string]any{}), &reply)
	if host.total != 1 {
		t.Fatalf("depends_on made %d host calls, want the index read alone", host.total)
	}
	if got := dependsOnIDs(reply); got != "by-member,by-tag,file-over-collection,fleet,graph" {
		t.Fatalf("depends_on = %s", got)
	}
	for _, rec := range reply.Records {
		if rec.Revision != indexEntryOf(t, rt, rec.ID).Revision {
			t.Fatalf("%s revision %q is not its index revision", rec.ID, rec.Revision)
		}
	}
	if reply.Version == "" {
		t.Fatal("depends_on carries no version")
	}

	// A refresh moves bookkeeping, not the answer, so the version holds.
	rt.noteFetchOutcome("fleet", fetchOutcome{at: fixedTime()})
	var again dependsReply
	decodeResult(t, callSubscription(t, rt, "depends_on", map[string]any{}), &again)
	if again.Version != reply.Version {
		t.Fatal("a refresh moved the depends_on version")
	}
	// A record joining the closure moves it.
	if err := rt.saveSubscription(subscriptionRecord{ID: "file-over-fleet", Name: "f3", Kind: kindFile, Content: "proxies: []", NodeSource: "fleet"}); err != nil {
		t.Fatal(err)
	}
	decodeResult(t, callSubscription(t, rt, "depends_on", map[string]any{}), &again)
	if again.Version == reply.Version || !strings.Contains(dependsOnIDs(again), "file-over-fleet") {
		t.Fatalf("a new fleet reader did not move the answer: %+v", again)
	}
	if res := callSubscription(t, rt, "depends_on", map[string]any{"unexpected": true}); res.OK {
		t.Fatal("depends_on accepted an unknown field")
	}
}
