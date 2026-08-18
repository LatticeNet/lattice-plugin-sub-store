package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The serve path's steady state: fetch resolves a record's variable content
// into the core's snapshot, render consumes it. The two ends are pinned to
// each other here — a snapshot render must produce exactly what the live
// render produces, or a client would get different bytes depending on which
// side of a refresh it polled.

func TestFetchCollectionSnapshotCarriesChainedMembers(t *testing.T) {
	rt, _ := newCountingRuntime(t)
	seedBudgetStore(t, rt)

	out, err := rt.fetchSubscription("coll")
	if err != nil {
		t.Fatalf("fetch collection: %v", err)
	}
	var snap snapshotArtifacts
	if err := json.Unmarshal([]byte(out.Raw), &snap); err != nil {
		t.Fatalf("snapshot is not the artifacts envelope: %v", err)
	}
	if len(snap.Members) != 2 {
		t.Fatalf("snapshot members = %d, want one per collection member (2)", len(snap.Members))
	}
	for _, member := range snap.Members {
		if member.SubName == "" || !strings.Contains(member.Raw, "vless://") {
			t.Fatalf("member lost its name or nodes: %+v", member)
		}
	}
}

func TestFetchScriptFileSnapshotKeepsSourceIdentity(t *testing.T) {
	rt, _ := newCountingRuntime(t)
	seedBudgetStore(t, rt)

	out, err := rt.fetchSubscription("scripty")
	if err != nil {
		t.Fatalf("fetch script file: %v", err)
	}
	var snap snapshotArtifacts
	if err := json.Unmarshal([]byte(out.Raw), &snap); err != nil {
		t.Fatalf("snapshot is not the artifacts envelope: %v", err)
	}
	// A ported script names its source the way upstream did; the envelope must
	// carry both the id and the display name or produceArtifact lookups break
	// only for snapshot renders — the worst kind of divergence.
	if snap.SourceID != "coll" || snap.SourceName != "coll" || snap.SourceKind != kindCollection {
		t.Fatalf("source identity = %q/%q/%q, want coll/coll/collection", snap.SourceID, snap.SourceName, snap.SourceKind)
	}
	if len(snap.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(snap.Members))
	}
}

func TestCollectionRenderFromSnapshotMatchesLive(t *testing.T) {
	rt, _ := newCountingRuntime(t)
	seedBudgetStore(t, rt)

	rec, err := rt.getSubscription("coll")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := rt.fetchCollectionSnapshot(rec)
	if err != nil {
		t.Fatalf("fetch snapshot: %v", err)
	}
	fromSnapshot, err := rt.renderCollection(rec, subscriptionTarget(rec, ""), nil, snap.Raw)
	if err != nil {
		t.Fatalf("render from snapshot: %v", err)
	}
	live, err := rt.renderCollection(rec, subscriptionTarget(rec, ""), nil, "")
	if err != nil {
		t.Fatalf("render live: %v", err)
	}
	if fromSnapshot != live {
		t.Fatalf("snapshot render diverged from live:\nsnapshot: %q\nlive: %q", fromSnapshot, live)
	}
}

func TestScriptFileRenderFromSnapshotMatchesLive(t *testing.T) {
	rt, _ := newCountingRuntime(t)
	seedBudgetStore(t, rt)

	rec, err := rt.getSubscription("scripty")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := rt.fetchFileSnapshot(rec)
	if err != nil {
		t.Fatalf("fetch snapshot: %v", err)
	}
	fromSnapshot, _, err := rt.renderFile(rec, "", nil, snap.Raw)
	if err != nil {
		t.Fatalf("render from snapshot: %v", err)
	}
	live, _, err := rt.renderFile(rec, "", nil, "")
	if err != nil {
		t.Fatalf("render live: %v", err)
	}
	if fromSnapshot != live {
		t.Fatalf("snapshot render diverged from live:\nsnapshot: %q\nlive: %q", fromSnapshot, live)
	}
}

// A snapshot from an older plugin, or one that was never written, must not
// fail the serve: the live path takes over.
func TestRenderFallsBackWhenSnapshotUndecodable(t *testing.T) {
	rt, _ := newCountingRuntime(t)
	seedBudgetStore(t, rt)

	rec, err := rt.getSubscription("coll")
	if err != nil {
		t.Fatal(err)
	}
	out, err := rt.renderCollection(rec, subscriptionTarget(rec, ""), nil, "this is not the envelope")
	if err != nil {
		t.Fatalf("undecodable snapshot failed the render: %v", err)
	}
	if !strings.Contains(out, "vless://") && !strings.Contains(out, "HK-01") {
		t.Fatalf("fallback render produced no nodes: %q", out[:min(120, len(out))])
	}
}
