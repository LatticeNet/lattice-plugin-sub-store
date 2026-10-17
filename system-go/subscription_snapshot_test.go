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
	snap, err := rt.fetchSubscription("coll")
	if err != nil {
		t.Fatalf("fetch snapshot: %v", err)
	}
	if _, ok := decodeSnapshotEnvelope(snap.Raw); !ok {
		t.Fatalf("fetch did not write a version 2 envelope: %.120s", snap.Raw)
	}
	// The envelope as fetch stored it, which is what the core hands render.
	fromSnapshot, err := rt.renderCollection(rec, subscriptionTarget(rec, ""), nil, snap.Raw, legacyWithheld)
	if err != nil {
		t.Fatalf("render from snapshot: %v", err)
	}
	live, err := rt.renderCollection(rec, subscriptionTarget(rec, ""), nil, "", legacyWithheld)
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
	snap, err := rt.fetchSubscription("scripty")
	if err != nil {
		t.Fatalf("fetch snapshot: %v", err)
	}
	text := snapshotText(snap.Raw)
	if !strings.Contains(text, `"source_id":"coll"`) {
		t.Fatalf("the envelope did not read back as the members object: %.120s", text)
	}
	fromSnapshot, _, err := rt.renderFile(rec, "", nil, text)
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

// A snapshot this plugin cannot read is an error, never a reason to resolve
// the members live (S2 plan section 1.2): core hands back only snapshots the
// plugin wrote, and the live path would read a legacy member's export for
// whoever called render with a broken raw. A snapshot that was never written
// (an empty raw) still renders live, as previews do.
func TestRenderRefusesUndecodableSnapshot(t *testing.T) {
	rt, _ := newCountingRuntime(t)
	seedBudgetStore(t, rt)

	rec, err := rt.getSubscription("coll")
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.renderCollection(rec, subscriptionTarget(rec, ""), nil, "this is not the envelope", legacyWithheld)
	if err == nil || !strings.HasPrefix(err.Error(), codeSnapshotMalformed) {
		t.Fatalf("an undecodable snapshot: err %v", err)
	}
	out, err := rt.renderCollection(rec, subscriptionTarget(rec, ""), nil, "", legacyWithheld)
	if err != nil {
		t.Fatalf("an empty snapshot no longer renders live: %v", err)
	}
	if !strings.Contains(out, "vless://") && !strings.Contains(out, "HK-01") {
		t.Fatalf("live render produced no nodes: %q", out[:min(120, len(out))])
	}
}
