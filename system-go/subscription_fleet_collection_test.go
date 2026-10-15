package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// fleetCollectionFixture is a store with two fleet members that overlap on
// one line, a local provider member, and the collection over all three.
// The catalogue answers the first member's read with lines 1 to 3 and the
// second's with lines 3 to 5.
func fleetCollectionFixture(t *testing.T, collection subscriptionRecord) (*runtime, *fleetCatalogueHost) {
	t.Helper()
	all := append(fleetTestRows(2, "trojan"), fleetTestRow(3, "vless"), fleetTestRow(4, "vless"), fleetTestRow(5, "vless"))
	rt, host := newFleetRuntime(t, nil)
	host.readSets = [][]model.LineCatalogueRow{all[0:3], all[2:5]}
	saveFleetRecord(t, rt, subscriptionRecord{ID: "hk", Tags: []string{"asia"}})
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp", Tags: []string{"asia"}})
	if err := rt.saveSubscription(subscriptionRecord{ID: "local", Name: "local", Source: subscriptionSourceLocal,
		Content: "trojan://pw1@198.51.100.7:443#provider-one\ntrojan://pw2@198.51.100.8:443#provider-two"}); err != nil {
		t.Fatal(err)
	}
	if collection.ID == "" {
		collection = subscriptionRecord{ID: "all", Name: "all", Members: []string{"hk", "jp", "local"}}
	}
	collection.Kind = kindCollection
	if err := rt.saveSubscription(collection); err != nil {
		t.Fatal(err)
	}
	return rt, host
}

// TestFleetCollectionUnionBlock pins a collection's snapshot with fleet
// members: every block names its member (id, source, revision), a fleet
// block carries its steps, catalogue version, selector and rows and no
// text, and the top level carries the derived "lcv1-" version and one union
// row per selected line, the overlapping line once, so core's unchanged
// selection reader reads it as it reads a sub's.
func TestFleetCollectionUnionBlock(t *testing.T) {
	rt, _ := fleetCollectionFixture(t, subscriptionRecord{})
	out, err := rt.fetchSubscription("all")
	if err != nil {
		t.Fatal(err)
	}
	env, ok := decodeSnapshotEnvelope(out.Raw)
	if !ok || env.Kind != kindCollection || len(env.Members) != 3 {
		t.Fatalf("envelope = %+v", env)
	}
	hk, _ := rt.getSubscription("hk")
	if b := env.Members[0]; b.ID != "hk" || b.Source != subscriptionSourceFleet || b.Revision != hk.Revision || b.Raw != "" || b.CatalogueVersion != fleetTestVersion || b.Selector == nil || len(b.Rows) == 0 {
		t.Fatalf("fleet block = %+v", b)
	}
	if b := env.Members[2]; b.ID != "local" || b.Source != subscriptionSourceLocal || b.Revision == "" || b.Raw == "" || b.Rows != nil || b.CatalogueVersion != "" {
		t.Fatalf("provider block = %+v", b)
	}
	if !strings.HasPrefix(env.CatalogueVersion, fleetCatalogueVersionPrefix) || env.CatalogueVersion == fleetTestVersion {
		t.Fatalf("collection version = %q", env.CatalogueVersion)
	}
	var union []fleetUnionRow
	if err := json.Unmarshal(env.Rows, &union); err != nil {
		t.Fatal(err)
	}
	want := []fleetUnionRow{{fleetTestLineUUID(1), 0}, {fleetTestLineUUID(2), 0}, {fleetTestLineUUID(3), 0}, {fleetTestLineUUID(4), 1}, {fleetTestLineUUID(5), 1}}
	if fmt.Sprint(union) != fmt.Sprint(want) {
		t.Fatalf("union rows = %v, want %v", union, want)
	}
	if !strings.HasPrefix(out.SourceVersion, fleetEnvelopeDigestPrefix) || out.SourceVersion != env.SourceVersion {
		t.Fatalf("source_version = %q", out.SourceVersion)
	}
}

// TestFleetCollectionRendersFromMemberBlocks pins the node-wise render: the
// fleet members' lines come back as fleet nodes with their placeholders and
// Lattice fields (no URI round trip, so line_uuid survives), the provider
// member's nodes as provider nodes, and the selection is the union of the
// fleet blocks' lines.
func TestFleetCollectionRendersFromMemberBlocks(t *testing.T) {
	rt, _ := fleetCollectionFixture(t, subscriptionRecord{})
	fetched, err := rt.fetchSubscription("all")
	if err != nil {
		t.Fatal(err)
	}
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "all", "raw": fetched.Raw, "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	plan := decodeRenderPlan(t, out.Plan)
	fleet, provider := map[string]int{}, 0
	for _, node := range plan.Nodes {
		switch {
		case node.Provider:
			provider++
			if node.LineUUID != "" || len(node.Placeholders) != 0 {
				t.Fatalf("a provider node carries a line: %+v", node)
			}
		case node.LineUUID != "":
			fleet[node.LineUUID]++
			var fields map[string]any
			_ = json.Unmarshal(node.Node, &fields)
			if fields["node_id"] == nil {
				t.Errorf("fleet node %s lost its Lattice fields", node.LineUUID)
			}
		default:
			t.Fatalf("a node with neither a line nor provenance: %s", node.Node)
		}
	}
	if provider != 2 || len(fleet) != 5 || fleet[fleetTestLineUUID(3)] != 2 {
		t.Fatalf("provider nodes %d, fleet lines %v", provider, fleet)
	}
	if plan.Selection == nil || len(plan.Selection.LineUUIDs) != 5 || !strings.HasPrefix(plan.Selection.CatalogueVersion, fleetCatalogueVersionPrefix) {
		t.Fatalf("selection = %+v", plan.Selection)
	}
	env, _ := decodeSnapshotEnvelope(fetched.Raw)
	if plan.Selection.CatalogueVersion != env.CatalogueVersion {
		t.Fatalf("plan version %q, snapshot version %q", plan.Selection.CatalogueVersion, env.CatalogueVersion)
	}
}

// TestFleetCollectionServePathNeverReadsLive pins that the serve path reads
// no member record and no catalogue page: the blocks carry everything.
func TestFleetCollectionServePathNeverReadsLive(t *testing.T) {
	rt, host := fleetCollectionFixture(t, subscriptionRecord{})
	fetched, err := rt.fetchSubscription("all")
	if err != nil {
		t.Fatal(err)
	}
	pages := host.calls
	counting := &countingHost{inner: host}
	rt.host = counting
	if _, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "all", "raw": fetched.Raw, "target": "ClashMeta"}); !res.OK {
		t.Fatal(res.Error)
	}
	if host.calls != pages {
		t.Fatalf("the serve path read %d catalogue pages", host.calls-pages)
	}
	for _, key := range counting.keys {
		if key == recordKey("hk") || key == recordKey("jp") || key == recordKey("local") {
			t.Fatalf("the serve path read the member record %s", key)
		}
	}
}

// countingHost records the KV keys a call sequence reads.
type countingHost struct {
	inner hostCaller
	keys  []string
}

func (h *countingHost) call(method string, params any) (json.RawMessage, error) {
	encoded, _ := json.Marshal(params)
	var p struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(encoded, &p)
	if p.Key != "" {
		h.keys = append(h.keys, p.Key)
	}
	return h.inner.call(method, params)
}

// TestFleetBoundCollectionRefusesLegacyShapedEnvelope pins that a
// fleet-bound collection never reads a block as provider content when the
// block does not say what it is: an S1 snapshot's blocks carry only a name
// and text, and serving one could hand a legacy member's export links to
// identity holders as provider nodes.
func TestFleetBoundCollectionRefusesLegacyShapedEnvelope(t *testing.T) {
	rt, _ := fleetCollectionFixture(t, subscriptionRecord{})
	const link = "vless://11111111-2222-4333-8444-555555555555@owner.example:443#owner"
	s1 := snapshotEnvelope{Version: snapshotEnvelopeVersion, Kind: kindCollection, Members: []envelopeMember{{SubName: "hk", Raw: link}}}
	raw, _ := json.Marshal(s1)
	for name, snapshot := range map[string]string{"S1 blocks": string(raw), "not an envelope": "x", "a sub envelope": `{"version":2,"kind":"sub","raw":"` + link + `"}`} {
		out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "all", "raw": snapshot, "target": "sing-box"})
		if res.OK || !strings.HasPrefix(res.Error, codeFleetEnvelopeMismatch) || len(out.Plan) != 0 || strings.Contains(res.Error, "owner.example") {
			t.Fatalf("%s: ok=%v error=%q", name, res.OK, res.Error)
		}
	}
	_, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "all", "target": "sing-box"})
	if res.OK || !strings.HasPrefix(res.Error, codeFleetSnapshotMissing) {
		t.Fatalf("no snapshot: ok=%v error=%q", res.OK, res.Error)
	}
}

// TestFleetCollectionRefusesLegacyMember pins the mixing refusal at fetch
// and at render: a collection gathering a fleet record and a legacy record
// refuses fleet_mixed_owner_credentials, naming both, before any member is
// read.
func TestFleetCollectionRefusesLegacyMember(t *testing.T) {
	rt, host := fleetCollectionFixture(t, subscriptionRecord{ID: "mixed", Name: "mixed", Members: []string{"hk", "legacy"}})
	if err := rt.saveSubscription(subscriptionRecord{ID: "legacy", Name: "legacy", Source: subscriptionSourceVPNCore}); err != nil {
		t.Fatal(err)
	}
	_, err := rt.fetchSubscription("mixed")
	if err == nil || !strings.HasPrefix(err.Error(), codeFleetMixedOwnerCredentials) || !strings.Contains(err.Error(), "hk") || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("fetch: err %v", err)
	}
	if host.calls != 0 || host.other != 0 {
		t.Fatalf("a refused collection read %d pages and %d other rpc calls", host.calls, host.other)
	}
	_, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "mixed", "raw": `{"version":2,"kind":"collection"}`, "target": "sing-box"})
	if res.OK || !strings.HasPrefix(res.Error, codeFleetMixedOwnerCredentials) {
		t.Fatalf("render: ok=%v error=%q", res.OK, res.Error)
	}
}

// TestFleetMemberIsNotSkippable pins that a failed fleet member fails the
// collection even under skip-failed.
func TestFleetMemberIsNotSkippable(t *testing.T) {
	rt, host := fleetCollectionFixture(t, subscriptionRecord{ID: "skip", Name: "skip", Members: []string{"hk", "local"}, FailureMode: failureModeSkip})
	host.fail = errors.New("catalogue down")
	if _, err := rt.fetchSubscription("skip"); err == nil || !strings.Contains(err.Error(), codeCatalogueUnavailable) {
		t.Fatalf("a failed fleet member under skip-failed: err %v", err)
	}
	if !collectionMemberFailureIsSkippable(subscriptionRecord{FailureMode: failureModeSkip}, subscriptionRecord{Source: subscriptionSourceRemote}) {
		t.Fatal("a remote member stopped being skippable")
	}
}

// TestFleetCollectionWithEmptyFleetMemberServesOtherMembers pins that a
// fleet member whose selection matched nothing writes a block with rows []
// and the collection keeps serving its other members.
func TestFleetCollectionWithEmptyFleetMemberServesOtherMembers(t *testing.T) {
	rt, host := fleetCollectionFixture(t, subscriptionRecord{})
	host.readSets = [][]model.LineCatalogueRow{{}, fleetTestRows(2, "vless")}
	fetched, err := rt.fetchSubscription("all")
	if err != nil {
		t.Fatalf("a zero-row fleet member failed the collection: %v", err)
	}
	env, _ := decodeSnapshotEnvelope(fetched.Raw)
	if string(env.Members[0].Rows) != "[]" {
		t.Fatalf("the empty member's rows = %s", env.Members[0].Rows)
	}
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "all", "raw": fetched.Raw, "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	plan := decodeRenderPlan(t, out.Plan)
	if len(plan.Nodes) != 4 {
		t.Fatalf("nodes = %d, want the other fleet member's 2 and the provider's 2", len(plan.Nodes))
	}
}

// TestCollectionSourceVersionFollowsProviderRaw pins that a fleet
// collection's source_version moves when only a provider member's content
// moves, so core never extends a cached body over old provider nodes.
func TestCollectionSourceVersionFollowsProviderRaw(t *testing.T) {
	rt, _ := fleetCollectionFixture(t, subscriptionRecord{})
	host := rt.host.(*fleetCatalogueHost)
	host.readSets = [][]model.LineCatalogueRow{fleetTestRows(2, "vless")}
	before, err := rt.fetchSubscription("all")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "local", Name: "local", Source: subscriptionSourceLocal, Content: "trojan://pw9@198.51.100.9:443#changed"}); err != nil {
		t.Fatal(err)
	}
	after, err := rt.fetchSubscription("all")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := decodeSnapshotEnvelope(before.Raw)
	a, _ := decodeSnapshotEnvelope(after.Raw)
	if a.CatalogueVersion != b.CatalogueVersion {
		t.Fatal("the fleet version moved with provider content")
	}
	if after.SourceVersion == before.SourceVersion {
		t.Fatal("source_version did not move with the provider member's content")
	}
}

// TestCollectionPlanCarriesItsOwnPolicy pins that a collection's plan
// carries the collection's own fleet options and never a member's.
func TestCollectionPlanCarriesItsOwnPolicy(t *testing.T) {
	rt, _ := fleetCollectionFixture(t, subscriptionRecord{ID: "own", Name: "own", Members: []string{"hk", "jp"},
		Fleet: &fleetOptions{UsageExclusion: &usageExclusion{Enabled: true, MaxBytesPerLine: 5 << 30}}})
	saveFleetRecord(t, rt, subscriptionRecord{ID: "hk", Tags: []string{"asia"}, Fleet: &fleetOptions{ProbeExclusion: &probeExclusion{Enabled: true, ConsecutiveFailures: 2}}})
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp", Tags: []string{"asia"}, Fleet: &fleetOptions{DDNSDial: true}})
	fetched, err := rt.fetchSubscription("own")
	if err != nil {
		t.Fatal(err)
	}
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "own", "raw": fetched.Raw, "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	plan := decodeRenderPlan(t, out.Plan)
	if plan.Policy == nil || plan.Policy.Usage == nil || plan.Policy.Usage.MaxBytesPerLine != 5<<30 || plan.Policy.Probe != nil || plan.Policy.DDNSDial {
		t.Fatalf("policy = %+v", plan.Policy)
	}
}
