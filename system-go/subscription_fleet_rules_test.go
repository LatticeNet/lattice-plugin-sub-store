package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// The record rules lane 1 owns (applyFleetWriteRules, S2 plan sections 1.2,
// 1.4 and 2.1), driven through the save method, which reaches them through
// lane 4's storeWriteRecord and answers a refusal as the structured reply.

// rulesStore is a fleet catalogue store for the rule tests.
func rulesStore(t *testing.T) *runtime {
	t.Helper()
	rt, _ := newFleetRuntime(t, fleetTestRows(2, "trojan"))
	return rt
}

// seedRecords writes records live with S1's storeSave semantics (store_seed_test.go),
// as a backup restore leaves them.
func seedRecords(t *testing.T, rt *runtime, recs ...subscriptionRecord) {
	t.Helper()
	for _, rec := range recs {
		if rec.Name == "" {
			rec.Name = rec.ID
		}
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatalf("seed %s: %v", rec.ID, err)
		}
	}
}

// requireRefused saves rec through the save method and requires the
// structured refusal code.
func requireRefused(t *testing.T, rt *runtime, rec subscriptionRecord, code string) storeRefusal {
	t.Helper()
	if rec.Name == "" {
		rec.Name = rec.ID
	}
	_, res := saveViaMethod(t, rt, rec, "")
	refusal, ok := refusalOf(res)
	if !ok || refusal.Code != code {
		t.Fatalf("save %s = %+v (refusal %+v), want %s", rec.ID, res, refusal, code)
	}
	if strings.Contains(refusal.Message, "://") {
		t.Fatalf("the refusal message quotes a URL: %q", refusal.Message)
	}
	return refusal
}

// requireSaved saves rec through the save method and requires it landed,
// live or staged.
func requireSaved(t *testing.T, rt *runtime, rec subscriptionRecord) saveReply {
	t.Helper()
	if rec.Name == "" {
		rec.Name = rec.ID
	}
	reply, res := saveViaMethod(t, rt, rec, "")
	if _, refused := refusalOf(res); !res.OK || refused || !reply.Saved {
		t.Fatalf("save %s = %+v", rec.ID, res)
	}
	return reply
}

// TestSaveRefusesFleetToLegacySource pins that a record that is fleet-bound
// live, or whose live source is fleet, never takes a legacy source: its
// identity shares would receive the owner-credential export.
func TestSaveRefusesFleetToLegacySource(t *testing.T) {
	rt := rulesStore(t)
	seedRecords(t, rt, subscriptionRecord{ID: "f", Source: subscriptionSourceFleet})
	refusal := requireRefused(t, rt, subscriptionRecord{ID: "f", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}, codeFleetToLegacyRefused)
	if len(refusal.IDs) != 1 || refusal.IDs[0] != "f" {
		t.Fatalf("ids %v", refusal.IDs)
	}
	// The graph source through the rule itself: save revalidates a graph
	// selection against core's options before the rules run.
	graph := subscriptionRecord{ID: "f", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}
	for _, live := range []fleetLiveFacts{{Revision: "r", Source: subscriptionSourceFleet}, {Revision: "r", Source: subscriptionSourceLocal, FleetBound: true}} {
		for _, origin := range []fleetWriteOrigin{fleetWriteSave, fleetWriteImport, fleetWriteRestore} {
			if _, err := applyFleetWriteRules(graph, live, nil, origin); err == nil || !strings.HasPrefix(err.Error(), codeFleetToLegacyRefused) {
				t.Fatalf("graph source over %+v at %s: %v", live, origin, err)
			}
		}
	}
	if f, _ := rt.getSubscription("f"); f.Source != subscriptionSourceFleet {
		t.Fatalf("the refused save changed the record: %+v", f)
	}
}

// TestSaveRefusesNewLegacyRecord pins that save creates no owner-credential
// record from S2.
func TestSaveRefusesNewLegacyRecord(t *testing.T) {
	rt := rulesStore(t)
	requireRefused(t, rt, subscriptionRecord{ID: "brand-new", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}, codeLegacySourceRetired)
	if _, err := rt.getSubscription("brand-new"); err == nil {
		t.Fatal("the refused record exists")
	}
	// A live legacy record keeps its source on an edit.
	seedRecords(t, rt, subscriptionRecord{ID: "kept", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"})
	requireSaved(t, rt, subscriptionRecord{ID: "kept", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a", Remark: "edited"})
}

// TestSaveRefusesProviderToLegacySource pins that a provider record cannot
// be switched to the owner-credential export.
func TestSaveRefusesProviderToLegacySource(t *testing.T) {
	rt := rulesStore(t)
	seedRecords(t, rt,
		subscriptionRecord{ID: "remote", Source: subscriptionSourceRemote, URL: "https://provider.example/sub"},
		subscriptionRecord{ID: "local", Source: subscriptionSourceLocal, Content: "trojan://pw@198.51.100.7:443#one"},
	)
	requireRefused(t, rt, subscriptionRecord{ID: "remote", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}, codeLegacySourceRetired)
	requireRefused(t, rt, subscriptionRecord{ID: "local", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}, codeLegacySourceRetired)
	graph := subscriptionRecord{ID: "local", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}
	if _, err := applyFleetWriteRules(graph, fleetLiveFacts{Revision: "r", Source: subscriptionSourceLocal}, nil, fleetWriteSave); err == nil || !strings.HasPrefix(err.Error(), codeLegacySourceRetired) {
		t.Fatalf("local to graph: %v", err)
	}
	// import, migrate and restore still write legacy records: a backup must
	// restore what it holds.
	for _, origin := range []fleetWriteOrigin{fleetWriteImport, fleetWriteMigrate, fleetWriteRestore} {
		if _, err := applyFleetWriteRules(graph, fleetLiveFacts{}, nil, origin); err != nil {
			t.Fatalf("%s refused a legacy record: %v", origin, err)
		}
	}
}

// TestSaveRefusesLegacySourceOnNeverLiveRecord pins that a record staged and
// never promoted has no live legacy revision to keep, so it cannot take a
// legacy source either.
func TestSaveRefusesLegacySourceOnNeverLiveRecord(t *testing.T) {
	rt := rulesStore(t)
	reply := requireSaved(t, rt, subscriptionRecord{ID: "fresh", Source: subscriptionSourceFleet})
	if !reply.Staged {
		t.Fatal("a new fleet record did not stage; the fixture needs a never-live entry")
	}
	if entry, ok := entryOf(t, rt, "fresh"); !ok || entry.Revision != "" {
		t.Fatalf("entry = %+v", entry)
	}
	requireRefused(t, rt, subscriptionRecord{ID: "fresh", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}, codeLegacySourceRetired)
}

// TestSaveRefusesFleetBoundCollectionGatheringLegacyMember pins the
// membership half of fleet_to_legacy_refused: a fleet-bound collection
// re-saved over a legacy and a provider record mixes nothing, and would
// otherwise preview as "every line removed" and then serve the export to
// its identity-less shares.
func TestSaveRefusesFleetBoundCollectionGatheringLegacyMember(t *testing.T) {
	rt := rulesStore(t)
	seedRecords(t, rt,
		subscriptionRecord{ID: "f", Source: subscriptionSourceFleet},
		subscriptionRecord{ID: "l", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a", Tags: []string{"old"}},
		subscriptionRecord{ID: "p", Source: subscriptionSourceLocal, Content: "trojan://pw@198.51.100.7:443#one"},
		subscriptionRecord{ID: "c", Kind: kindCollection, Members: []string{"f"}},
	)
	refusal := requireRefused(t, rt, subscriptionRecord{ID: "c", Kind: kindCollection, Members: []string{"l", "p"}}, codeFleetToLegacyRefused)
	if len(refusal.Collections) != 1 || refusal.Collections[0] != "c" || len(refusal.IDs) != 1 || refusal.IDs[0] != "l" {
		t.Fatalf("refusal = %+v", refusal)
	}
	// By tag too.
	requireRefused(t, rt, subscriptionRecord{ID: "c", Kind: kindCollection, MemberTags: []string{"old"}}, codeFleetToLegacyRefused)
}

// TestSaveRefusesTagEditThatMixesCollection pins the mixing guard at save
// from all three sides: a collection whose members would mix, a legacy
// record whose new tags a fleet-bound collection gathers, and a record
// switched to fleet that a collection with a legacy member gathers.
func TestSaveRefusesTagEditThatMixesCollection(t *testing.T) {
	t.Run("collection", func(t *testing.T) {
		rt := rulesStore(t)
		seedRecords(t, rt,
			subscriptionRecord{ID: "f", Source: subscriptionSourceFleet, Tags: []string{"asia"}},
			subscriptionRecord{ID: "l", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a", Tags: []string{"legacy"}},
		)
		refusal := requireRefused(t, rt, subscriptionRecord{ID: "c", Kind: kindCollection, MemberTags: []string{"asia", "legacy"}}, codeFleetMixedOwnerCredentials)
		if strings.Join(refusal.IDs, ",") != "f,l" || refusal.Collections[0] != "c" {
			t.Fatalf("refusal = %+v", refusal)
		}
	})
	t.Run("legacy record tagged into a fleet-bound collection", func(t *testing.T) {
		rt := rulesStore(t)
		seedRecords(t, rt,
			subscriptionRecord{ID: "f", Source: subscriptionSourceFleet, Tags: []string{"asia"}},
			subscriptionRecord{ID: "l", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"},
			subscriptionRecord{ID: "c", Kind: kindCollection, MemberTags: []string{"asia"}},
		)
		requireRefused(t, rt, subscriptionRecord{ID: "l", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a", Tags: []string{"asia"}}, codeFleetMixedOwnerCredentials)
	})
	t.Run("record switched to fleet under a collection with a legacy member", func(t *testing.T) {
		rt := rulesStore(t)
		seedRecords(t, rt,
			subscriptionRecord{ID: "l", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a", Tags: []string{"asia"}},
			subscriptionRecord{ID: "p", Source: subscriptionSourceLocal, Content: "trojan://pw@198.51.100.7:443#one", Tags: []string{"asia"}},
			subscriptionRecord{ID: "c", Kind: kindCollection, MemberTags: []string{"asia"}},
		)
		requireRefused(t, rt, subscriptionRecord{ID: "p", Source: subscriptionSourceFleet, Tags: []string{"asia"}}, codeFleetMixedOwnerCredentials)
	})
}

// TestFleetRecordRefusesLinkModeScriptStepRenderAndMemberBlock pins the render and member-block
// halves of fleet_script_link_unavailable (lane 4 pins save and import): a
// stored chain with a link-mode script step, on a fleet record or in a
// collection's fleet member block, refuses at render, because a serve-path
// render has the script network and would run whatever the link serves next.
func TestFleetRecordRefusesLinkModeScriptStepRenderAndMemberBlock(t *testing.T) {
	freshFleetPlans(t)
	link, _ := json.Marshal(map[string]any{"type": "Script Operator", "args": map[string]string{"mode": "link", "content": "https://scripts.example/op.js"}})
	rt, _, snapshot := fetchedFleetRecord(t, fleetTestRows(2, "trojan"), subscriptionRecord{ID: "jp"})
	// A record staged by an older binary: seeded past the save rule.
	seedRecords(t, rt, subscriptionRecord{ID: "jp", Source: subscriptionSourceFleet, Process: []json.RawMessage{link}})
	_, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "jp", "raw": snapshot, "target": "sing-box"})
	if res.OK || !strings.HasPrefix(res.Error, codeFleetScriptLinkUnavailable) {
		t.Fatalf("render: ok=%v error=%q", res.OK, res.Error)
	}

	crt, _ := fleetCollectionFixture(t, subscriptionRecord{})
	fetched, err := crt.fetchSubscription("all")
	if err != nil {
		t.Fatal(err)
	}
	env, _ := decodeSnapshotEnvelope(fetched.Raw)
	env.Members[0].Steps = []json.RawMessage{link}
	tampered, _ := json.Marshal(env)
	_, res = renderFleetCall(t, crt, map[string]any{"subscription_id": "all", "raw": string(tampered), "target": "sing-box"})
	if res.OK || !strings.HasPrefix(res.Error, codeFleetScriptLinkUnavailable) {
		t.Fatalf("member block: ok=%v error=%q", res.OK, res.Error)
	}
	// The save rule itself, for the record and for a collection's member.
	if _, err := applyFleetWriteRules(subscriptionRecord{ID: "x", Source: subscriptionSourceFleet, Process: []json.RawMessage{link}}, fleetLiveFacts{}, nil, fleetWriteSave); err == nil || !strings.HasPrefix(err.Error(), codeFleetScriptLinkUnavailable) {
		t.Fatalf("save rule: %v", err)
	}
}

// externalDocument is a Clash document with one ordinary node and one
// exec-shaped node, which H3 drops unless the record opts in.
const externalDocument = `proxies:
  - {name: plain, type: trojan, server: 198.51.100.7, port: 443, password: pw}
  - {name: runs-a-program, type: ss, server: 198.51.100.8, port: 8388, cipher: aes-128-gcm, password: pw, exec: /usr/bin/ssh}
`

// TestLocalExternalOptIn pins the H3 opt-in field: kept only on a local
// record, covered by the revision, and wired to the native parse, so a
// local record that opts in keeps an exec-shaped node its fetch would
// otherwise drop.
func TestLocalExternalOptIn(t *testing.T) {
	for _, rec := range []subscriptionRecord{
		{ID: "r", Source: subscriptionSourceRemote, URL: "https://provider.example/sub", External: true},
		{ID: "f", Source: subscriptionSourceFleet, External: true},
		{ID: "c", Kind: kindCollection, Members: []string{"r"}, External: true},
	} {
		normalized, err := normalizeSubscriptionForStore(rec)
		if err != nil || normalized.External {
			t.Fatalf("%s kept external: %+v %v", rec.ID, normalized.External, err)
		}
	}
	off, _ := normalizeSubscriptionForStore(subscriptionRecord{ID: "l", Source: subscriptionSourceLocal, Content: externalDocument})
	on, _ := normalizeSubscriptionForStore(subscriptionRecord{ID: "l", Source: subscriptionSourceLocal, Content: externalDocument, External: true})
	if !on.External || on.Revision == off.Revision {
		t.Fatalf("the revision does not cover external: %s %s", on.Revision, off.Revision)
	}

	rt := rulesStore(t)
	for _, c := range []struct {
		external bool
		want     int
	}{{false, 1}, {true, 2}} {
		seedRecords(t, rt, subscriptionRecord{ID: "l", Source: subscriptionSourceLocal, Content: externalDocument, External: c.external})
		out, err := rt.fetchSubscription("l")
		if err != nil {
			t.Fatal(err)
		}
		if out.nodesIn == nil || *out.nodesIn != c.want {
			t.Fatalf("external %v: nodes in %v, want %d", c.external, out.nodesIn, c.want)
		}
	}
}

// TestFleetNeverEmitsExternal pins that a fleet record never carries the
// opt-in and its nodes never come out exec-shaped: rows become nodes through
// parse.Line with the opt-in off, so a template parameter cannot smuggle an
// exec key into a plan.
func TestFleetNeverEmitsExternal(t *testing.T) {
	rt := rulesStore(t)
	seedRecords(t, rt, subscriptionRecord{ID: "f", Source: subscriptionSourceFleet, External: true})
	if f, _ := rt.getSubscription("f"); f.External {
		t.Fatal("a fleet record kept external")
	}
	rows := fleetTestRows(2, "vless")
	rows[1].Template.Params["exec"] = "/usr/bin/ssh"
	_, plan := buildTestPlan(t, subscriptionRecord{ID: "f", Source: subscriptionSourceFleet}, projectRows(rows))
	for _, node := range plan.Nodes {
		n := &nodemodel.Node{}
		if err := n.UnmarshalPlanNode(node.Node); err != nil {
			t.Fatal(err)
		}
		if nodemodel.IsExecShaped(n) {
			t.Fatalf("an exec-shaped fleet node: %s", node.Node)
		}
	}
}
