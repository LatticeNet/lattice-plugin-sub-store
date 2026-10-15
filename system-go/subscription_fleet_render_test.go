package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// decodeRenderPlan reads an encoded fleet plan with the SDK's strict
// decoder, which refuses an unknown field, a duplicate key and a plan that
// fails Validate, so a plan core would refuse fails the test.
func decodeRenderPlan(t *testing.T, encoded []byte) model.SelectionPlan {
	t.Helper()
	plan, err := model.DecodeSelectionPlan(encoded)
	if err != nil {
		t.Fatalf("the SDK refuses the plan: %v", err)
	}
	return plan
}

func projectRows(rows []model.LineCatalogueRow) []fleetRow {
	out := make([]fleetRow, len(rows))
	for i, row := range rows {
		out[i] = projectFleetRow(row)
	}
	return out
}

func buildTestPlan(t *testing.T, rec subscriptionRecord, rows []fleetRow) (fleetPlanOutput, model.SelectionPlan) {
	t.Helper()
	rec.Source = subscriptionSourceFleet
	normalised, err := normalizeSubscriptionForStore(rec)
	if err != nil {
		t.Fatal(err)
	}
	rt := &runtime{engine: testEngineWithHeadroom()}
	out, err := rt.buildFleetPlan(fleetPlanInput{Label: "subscription " + quoteLabel(rec.ID), Record: normalised, CatalogueVersion: fleetTestVersion, Rows: rows, Target: "sing-box"})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	return out, decodeRenderPlan(t, out.Encoded)
}

// wantNodeTypes is the node type the URI parsers give each protocol.
var wantNodeTypes = map[string]string{
	"vless": "vless", "trojan": "trojan", "hysteria2": "hysteria2", "tuic": "tuic",
	"vmess": "vmess", "socks": "socks5", "anytls": "anytls",
}

// TestFleetRenderReturnsPlanWithPlaceholdersPerProtocol pins S2 plan
// section 2.3's placeholders: one per credential field of the template
// protocol, in the field core compares, standing for the row's line; the
// selection lists every selected line; shadowsocks, which has no bind shape,
// is dropped and reported.
func TestFleetRenderReturnsPlanWithPlaceholdersPerProtocol(t *testing.T) {
	protocols := []string{"vless", "trojan", "hysteria2", "tuic", "vmess", "socks", "anytls", "shadowsocks"}
	var rows []fleetRow
	for i, protocol := range protocols {
		rows = append(rows, projectFleetRow(fleetTestRow(i+1, protocol)))
	}
	out, plan := buildTestPlan(t, subscriptionRecord{ID: "mixed", Name: "mixed"}, rows)
	if plan.Kind != model.SelectionPlanKindNodes || len(plan.Nodes) != 7 || out.NodeCount != 7 {
		t.Fatalf("kind %q, %d nodes", plan.Kind, len(plan.Nodes))
	}
	if got := out.Dropped; len(got) != 1 || got[0] != (fleetDrop{LineUUID: fleetTestLineUUID(8), Protocol: "shadowsocks", Reason: fleetDropNoBindShape}) {
		t.Fatalf("dropped = %v", out.Dropped)
	}
	if plan.Selection == nil || plan.Selection.CatalogueVersion != fleetTestVersion || len(plan.Selection.LineUUIDs) != 8 {
		t.Fatalf("selection = %+v", plan.Selection)
	}
	for i, node := range plan.Nodes {
		protocol := protocols[i]
		if node.LineUUID != fleetTestLineUUID(i+1) || node.Provider {
			t.Fatalf("node %d names line %q", i, node.LineUUID)
		}
		var fields map[string]any
		if err := json.Unmarshal(node.Node, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["type"] != wantNodeTypes[protocol] {
			t.Errorf("%s node type = %v", protocol, fields["type"])
		}
		want := fleetCredentialFields[protocol]
		got := make([]string, 0, len(node.Placeholders))
		for field, placeholder := range node.Placeholders {
			got = append(got, field)
			line, placed, err := model.ParsePlanPlaceholder(placeholder)
			if err != nil || line != node.LineUUID || placed != field {
				t.Errorf("%s placeholder for %s = %q (%v)", protocol, field, placeholder, err)
			}
			if fields[field] != placeholder {
				t.Errorf("%s node field %s = %v, want its placeholder", protocol, field, fields[field])
			}
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s placeholders %v, want %v", protocol, got, want)
		}
		if _, ok := fields["_lattice"]; ok {
			t.Errorf("%s node carries _lattice", protocol)
		}
		if fields["node_id"] != fmt.Sprintf("node-%d", i+1) || fields["line_hash_id"] != fmt.Sprintf("lh-%d", i+1) {
			t.Errorf("%s node Lattice fields: node_id %v, line_hash_id %v", protocol, fields["node_id"], fields["line_hash_id"])
		}
	}
}

// TestFleetRenderEmitsNoFlowForVless pins that the plugin never writes a
// vless flow: core fills the identity's own.
func TestFleetRenderEmitsNoFlowForVless(t *testing.T) {
	_, plan := buildTestPlan(t, subscriptionRecord{ID: "v", Name: "v"}, projectRows(fleetTestRows(3, "vless")))
	for i, node := range plan.Nodes {
		var fields map[string]any
		if err := json.Unmarshal(node.Node, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["flow"]; ok {
			t.Errorf("vless node %d carries a flow: %s", i, node.Node)
		}
	}
}

// TestFleetNodeNameIsTheRowLabel pins that a fleet node's name is the label
// core ships in the row, used as given and never derived from node_name and
// name (core's own formula falls through the line's name, tag and hash id,
// and the row carries no tag).
func TestFleetNodeNameIsTheRowLabel(t *testing.T) {
	rows := projectRows(fleetTestRows(2, "trojan"))
	rows[0].Label = "Osaka edge (line tag)"
	rows[1].Name = ""
	rows[1].Label = "tokyo-2 lh-2"
	_, plan := buildTestPlan(t, subscriptionRecord{ID: "n", Name: "n"}, rows)
	for i, node := range plan.Nodes {
		var fields map[string]any
		if err := json.Unmarshal(node.Node, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["name"] != rows[i].Label {
			t.Errorf("node %d name = %v, want the row label %q", i, fields["name"], rows[i].Label)
		}
	}
}

// TestPlanLineIdentityComesFromPlaceholder pins line recovery: a node whose
// credential field still holds a placeholder minted for this plan is that
// line whatever a script wrote into its other fields, and a node whose
// credential holds no known placeholder carries no line and no Lattice
// block, so core excludes it with no_line.
func TestPlanLineIdentityComesFromPlaceholder(t *testing.T) {
	rows := projectRows(fleetTestRows(2, "vless"))
	table := newFleetPlaceholders(len(rows))
	nodes, _, err := fleetRowNodes(rows, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	// A script swaps the two nodes' Lattice-looking fields and names, and
	// invents a third node with a placeholder-shaped credential of its own.
	nodes[0].Set("00000000-0000-4000-8000-000000000999", "line_uuid")
	nodes[0].Set("node-2", "node_id")
	invented := nodes[1].Clone()
	invented.Set("LATTICE-SYN-"+fleetTestLineUUID(2)+"-uuid-0123456789abcdef0123456789abcdef", "uuid")
	invented.Lattice = nil
	nodes = append(nodes, invented)

	planNodes, err := fleetPlanNodes(nodes, table)
	if err != nil {
		t.Fatal(err)
	}
	if planNodes[0].LineUUID != fleetTestLineUUID(1) || planNodes[1].LineUUID != fleetTestLineUUID(2) {
		t.Fatalf("lines = %q, %q", planNodes[0].LineUUID, planNodes[1].LineUUID)
	}
	var first map[string]any
	if err := json.Unmarshal(planNodes[0].Node, &first); err != nil {
		t.Fatal(err)
	}
	if first["node_id"] != "node-1" || first["line_uuid"] != nil {
		t.Errorf("a script's Lattice-looking fields reached the plan: node_id %v, line_uuid %v", first["node_id"], first["line_uuid"])
	}
	if planNodes[2].LineUUID != "" || len(planNodes[2].Placeholders) != 0 {
		t.Fatalf("an invented node was given a line: %+v", planNodes[2])
	}
	var third map[string]any
	if err := json.Unmarshal(planNodes[2].Node, &third); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"node_id", "geo", "addresses", "line_hash_id"} {
		if _, ok := third[key]; ok {
			t.Errorf("a node no placeholder names carries %q", key)
		}
	}
}

// TestDDNSDialSubstitutesVerifiedNamesOnly pins the DDNS opt-in: server
// moves to a verified name, never to an unverified one, never without the
// opt-in, and sni, host and path are never touched.
func TestDDNSDialSubstitutesVerifiedNamesOnly(t *testing.T) {
	rows := projectRows(fleetTestRows(3, "trojan"))
	rows[0].DDNSNames = []model.LineCatalogueDDNSName{{Name: "jp1-stale.ddns.example"}, {Name: "jp1.ddns.example", Verified: true}}
	rows[1].DDNSNames = []model.LineCatalogueDDNSName{{Name: "jp2.ddns.example"}}
	servers := func(plan model.SelectionPlan) []map[string]any {
		var out []map[string]any
		for _, node := range plan.Nodes {
			var fields map[string]any
			if err := json.Unmarshal(node.Node, &fields); err != nil {
				t.Fatal(err)
			}
			out = append(out, fields)
		}
		return out
	}
	_, plain := buildTestPlan(t, subscriptionRecord{ID: "d", Name: "d"}, rows)
	if got := servers(plain)[0]["server"]; got != "jp1.example" {
		t.Fatalf("without the opt-in server = %v", got)
	}
	_, dial := buildTestPlan(t, subscriptionRecord{ID: "d", Name: "d", Fleet: &fleetOptions{DDNSDial: true}}, rows)
	got := servers(dial)
	if got[0]["server"] != "jp1.ddns.example" || got[1]["server"] != "jp2.example" || got[2]["server"] != "jp3.example" {
		t.Fatalf("servers = %v, %v, %v", got[0]["server"], got[1]["server"], got[2]["server"])
	}
	if got[0]["sni"] != "t.example" || !strings.Contains(fmt.Sprint(got[0]["ws-opts"]), "t.example") {
		t.Fatalf("DDNS substitution touched sni or the host header: %v", got[0])
	}
	if dial.Policy == nil || !dial.Policy.DDNSDial {
		t.Fatalf("policy = %+v", dial.Policy)
	}
}

// TestDDNSDialKeepsNATLinesOnEdge pins the NAT rule: a line whose template
// host is the provider edge keeps the edge unless a verified name's target
// is that edge, so a node's own A-record name never replaces it.
func TestDDNSDialKeepsNATLinesOnEdge(t *testing.T) {
	rows := projectRows(fleetTestRows(1, "vless"))
	rows[0].ProviderEdge = "edge.provider.example"
	rows[0].Template.Host = "edge.provider.example"
	rows[0].DDNSNames = []model.LineCatalogueDDNSName{{Name: "node1.ddns.example", Verified: true}}
	_, plan := buildTestPlan(t, subscriptionRecord{ID: "nat", Name: "nat", Fleet: &fleetOptions{DDNSDial: true}}, rows)
	var fields map[string]any
	if err := json.Unmarshal(plan.Nodes[0].Node, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["server"] != "edge.provider.example" {
		t.Fatalf("a NAT line dials %v", fields["server"])
	}

	// A verified CNAME whose target is the edge may stand in for it; one
	// pointing elsewhere may not.
	rows[0].DDNSNames = []model.LineCatalogueDDNSName{
		{Name: "elsewhere.ddns.example", Verified: true, Target: "other-edge.example"},
		{Name: "edge-alias.ddns.example", Verified: true, Target: "edge.provider.example"},
	}
	_, plan = buildTestPlan(t, subscriptionRecord{ID: "nat", Name: "nat", Fleet: &fleetOptions{DDNSDial: true}}, rows)
	if err := json.Unmarshal(plan.Nodes[0].Node, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["server"] != "edge-alias.ddns.example" {
		t.Fatalf("a NAT line with an edge-target name dials %v", fields["server"])
	}
}

// TestFleetRenderRefusesResponseTransformerUntilS3 pins that a fleet record
// with a Response Transformer refuses at render: the body write would run
// over a document core binds after render.
func TestFleetRenderRefusesResponseTransformerUntilS3(t *testing.T) {
	rec, err := normalizeSubscriptionForStore(subscriptionRecord{ID: "rt", Name: "rt", Source: subscriptionSourceFleet,
		Process: []json.RawMessage{json.RawMessage(`{"type":"Response Transformer","args":{"content":"function transform(r){return r}"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	rt := &runtime{engine: testEngineWithHeadroom()}
	_, err = rt.buildFleetPlan(fleetPlanInput{Label: "subscription \"rt\"", Record: rec, CatalogueVersion: fleetTestVersion, Rows: projectRows(fleetTestRows(1, "vless")), Target: "sing-box"})
	if err == nil || !strings.HasPrefix(err.Error(), codeFleetResponseChainUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

// TestFleetRecordRefusesLinkModeScriptStepAtRender pins the render half of
// fleet_script_link_unavailable: a stored chain with a link-mode script step
// (a record staged by an older binary) is refused rather than run.
func TestFleetRecordRefusesLinkModeScriptStepAtRender(t *testing.T) {
	rec, err := normalizeSubscriptionForStore(subscriptionRecord{ID: "ln", Name: "ln", Source: subscriptionSourceFleet,
		Process: []json.RawMessage{json.RawMessage(`{"type":"Script Operator","args":{"mode":"link","content":"https://scripts.invalid/x.js"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	rt := &runtime{engine: testEngineWithHeadroom()}
	_, err = rt.buildFleetPlan(fleetPlanInput{Label: "subscription \"ln\"", Record: rec, CatalogueVersion: fleetTestVersion, Rows: projectRows(fleetTestRows(1, "vless")), Target: "sing-box"})
	if err == nil || !strings.HasPrefix(err.Error(), codeFleetScriptLinkUnavailable) || strings.Contains(err.Error(), "://") {
		t.Fatalf("err = %v", err)
	}
}

// TestRenderRefusesPlanOverCap pins plan_too_large at the render cap, which
// sits below the SDK's own plan bound by the reply overhead.
func TestRenderRefusesPlanOverCap(t *testing.T) {
	node := model.SelectionPlanNode{Node: json.RawMessage(`{"name":"` + strings.Repeat("x", 4000) + `","type":"vless"}`)}
	plan := model.SelectionPlan{Kind: model.SelectionPlanKindNodes}
	for len(plan.Nodes) < 1500 {
		plan.Nodes = append(plan.Nodes, node)
	}
	if _, err := encodeFleetPlan(plan); err == nil || !strings.HasPrefix(err.Error(), codePlanTooLarge) {
		t.Fatalf("a %d-node plan of 4 KB names: err %v", len(plan.Nodes), err)
	}
	plan.Nodes = plan.Nodes[:10]
	if _, err := encodeFleetPlan(plan); err != nil {
		t.Fatalf("a small plan: %v", err)
	}
	if model.MaxRenderPlanBytes >= model.MaxSelectionPlanBytes {
		t.Fatal("the render cap does not leave room for the reply")
	}
}

// TestPolicyOfFleetOptions pins the bind policy a record's options ask for:
// none by default, the probe threshold defaulting to 3, usage only when
// enabled.
func TestPolicyOfFleetOptions(t *testing.T) {
	if policyOf(nil) != nil || policyOf(&fleetOptions{}) != nil {
		t.Fatal("no options asked for a policy")
	}
	p := policyOf(&fleetOptions{ProbeExclusion: &probeExclusion{Enabled: true}, UsageExclusion: &usageExclusion{Enabled: false, MaxBytesPerLine: 10}})
	if p == nil || p.Probe == nil || p.Probe.ConsecutiveFailures != defaultProbeConsecutiveFailures || p.Usage != nil {
		t.Fatalf("policy = %+v", p)
	}
	raw, _ := json.Marshal(policyOf(&fleetOptions{DDNSDial: true, UsageExclusion: &usageExclusion{Enabled: true, MaxBytesPerLine: 1 << 30}}))
	if string(raw) != `{"usage":{"max_bytes_per_line":1073741824},"ddns_dial":true}` {
		t.Fatalf("policy wire form = %s", raw)
	}
}

// TestPlanCacheServesTwoIdentitiesFromOnePlan pins the serve-path cache: two
// renders of one envelope and revision for two identities compute one plan.
func TestPlanCacheServesTwoIdentitiesFromOnePlan(t *testing.T) {
	cache := newFleetPlanCache(1 << 20)
	key := newFleetPlanKey("lfe1-a", "rev-1", nil, "sing-box", "", map[string]bool{"include-unsupported-proxy": true})
	compute := func() (fleetPlanOutput, error) {
		return fleetPlanOutput{Encoded: json.RawMessage(`{"kind":"nodes","nodes":[]}`)}, nil
	}
	for identity := 0; identity < 2; identity++ {
		if _, err := cache.get(key, compute); err != nil {
			t.Fatal(err)
		}
	}
	if cache.computeCount() != 1 {
		t.Fatalf("computes = %d, want 1", cache.computeCount())
	}
}

// TestPlanCacheMissesWhenRowsMoveUnderSameCatalogueVersion pins that the key
// is the envelope digest: rows that moved under one catalogue version (a
// clock-dependent leading step) change the digest and miss.
func TestPlanCacheMissesWhenRowsMoveUnderSameCatalogueVersion(t *testing.T) {
	rows := projectRows(fleetTestRows(3, "vless"))
	sel := fleetSelection{StepHash: fleetStepHash(nil)}
	before, err := fleetSubEnvelope(fleetCatalogueRead{CatalogueVersion: fleetTestVersion, Rows: rows}, sel)
	if err != nil {
		t.Fatal(err)
	}
	after, err := fleetSubEnvelope(fleetCatalogueRead{CatalogueVersion: fleetTestVersion, Rows: rows[:2]}, sel)
	if err != nil {
		t.Fatal(err)
	}
	if before.SourceVersion == after.SourceVersion {
		t.Fatal("the envelope digest did not move with the rows")
	}
	cache := newFleetPlanCache(1 << 20)
	compute := func() (fleetPlanOutput, error) { return fleetPlanOutput{Encoded: json.RawMessage(`{}`)}, nil }
	for _, env := range []snapshotEnvelope{before, after} {
		if _, err := cache.get(newFleetPlanKey(env.SourceVersion, "rev-1", nil, "sing-box", "", nil), compute); err != nil {
			t.Fatal(err)
		}
	}
	if cache.computeCount() != 2 {
		t.Fatalf("computes = %d, want 2", cache.computeCount())
	}
}

// TestFleetPlanCacheEvictsByBytes pins the LRU bound.
func TestFleetPlanCacheEvictsByBytes(t *testing.T) {
	cache := newFleetPlanCache(100)
	plan := func(n int) func() (fleetPlanOutput, error) {
		return func() (fleetPlanOutput, error) { return fleetPlanOutput{Encoded: make(json.RawMessage, n)}, nil }
	}
	for i := 0; i < 5; i++ {
		if _, err := cache.get(newFleetPlanKey(fmt.Sprint(i), "", nil, "", "", nil), plan(40)); err != nil {
			t.Fatal(err)
		}
	}
	if cache.bytes > 100 || len(cache.entries) != 2 {
		t.Fatalf("bytes %d, entries %d", cache.bytes, len(cache.entries))
	}
	if _, err := cache.get(newFleetPlanKey("huge", "", nil, "", "", nil), plan(500)); err != nil || cache.bytes > 100 {
		t.Fatalf("a plan past the bound was kept: bytes %d (%v)", cache.bytes, err)
	}
}

// renderFleetCall drives the render method as core calls it.
func renderFleetCall(t *testing.T, rt *runtime, payload map[string]any) (renderReply, response) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	res := rt.handleSubscriptionCall(callPayload{Method: "render", Payload: raw})
	var out renderReply
	if res.OK {
		if err := json.Unmarshal(res.Result, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, res
}

// fetchedFleetRecord saves a fleet record over the test catalogue and
// fetches it, returning the snapshot core would hold.
func fetchedFleetRecord(t *testing.T, rows []model.LineCatalogueRow, rec subscriptionRecord) (*runtime, subscriptionRecord, string) {
	t.Helper()
	rt, _ := newFleetRuntime(t, rows)
	saved := saveFleetRecord(t, rt, rec)
	out, err := rt.fetchSubscription(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rt, saved, out.Raw
}

// TestRenderHonoursRevisionAndReportsLiveRevision pins render's S2 request
// and reply: no revision or the live one serves the plan with the live
// revision beside it, another revision is revision_unknown, and a provider
// record's render reports its live revision too.
func TestRenderHonoursRevisionAndReportsLiveRevision(t *testing.T) {
	rt, rec, snapshot := fetchedFleetRecord(t, fleetTestRows(4, "vless"), subscriptionRecord{ID: "jp"})
	for _, revision := range []string{"", rec.Revision} {
		out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "jp", "raw": snapshot, "target": "sing-box", "revision": revision})
		if !res.OK {
			t.Fatalf("revision %q: %s", revision, res.Error)
		}
		if out.LiveRevision != rec.Revision || out.Content != "" || len(out.Plan) == 0 || out.Target != "sing-box" {
			t.Fatalf("revision %q: live %q, content %q, plan %d bytes", revision, out.LiveRevision, out.Content, len(out.Plan))
		}
		plan := decodeRenderPlan(t, out.Plan)
		if len(plan.Nodes) != 4 || plan.Selection == nil || len(plan.Selection.LineUUIDs) != 4 {
			t.Fatalf("plan = %d nodes, selection %+v", len(plan.Nodes), plan.Selection)
		}
	}
	_, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "jp", "raw": snapshot, "revision": "not-a-revision"})
	if res.OK || !strings.HasPrefix(res.Error, codeRevisionUnknown) {
		t.Fatalf("an unknown revision: ok=%v error=%q", res.OK, res.Error)
	}

	if err := rt.saveSubscription(subscriptionRecord{ID: "p", Name: "p", Source: subscriptionSourceLocal, Content: "trojan://pw@192.0.2.10:443#one"}); err != nil {
		t.Fatal(err)
	}
	provider, _ := rt.getSubscription("p")
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "p", "target": "URI", "format": "plain"})
	if !res.OK || out.LiveRevision != provider.Revision || out.Content == "" || len(out.Plan) != 0 {
		t.Fatalf("provider render: ok=%v live %q plan %d error %q", res.OK, out.LiveRevision, len(out.Plan), res.Error)
	}
}

// TestLiveFleetRevisionRefusesLegacyEnvelope pins the serve path's envelope
// rule: a live fleet revision handed a legacy envelope with export links in
// raw refuses fleet_envelope_mismatch, returns no plan and never decodes the
// links.
func TestLiveFleetRevisionRefusesLegacyEnvelope(t *testing.T) {
	rt, _, _ := fetchedFleetRecord(t, fleetTestRows(2, "vless"), subscriptionRecord{ID: "migrated"})
	const link = "vless://11111111-2222-4333-8444-555555555555@owner.example:443?security=reality#owner"
	legacy, err := encodeSnapshotEnvelope(textEnvelope(kindSub, link, ""))
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{"legacy envelope": legacy, "not json": "x", "provider text": link} {
		out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "migrated", "raw": raw, "target": "sing-box"})
		if res.OK || !strings.HasPrefix(res.Error, codeFleetEnvelopeMismatch) || len(out.Plan) != 0 {
			t.Fatalf("%s: ok=%v error=%q", name, res.OK, res.Error)
		}
		if strings.Contains(res.Error, "owner.example") || strings.Contains(res.Error, "11111111") {
			t.Fatalf("%s: the refusal quotes the snapshot: %q", name, res.Error)
		}
	}
	_, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "migrated", "target": "sing-box"})
	if res.OK || !strings.HasPrefix(res.Error, codeFleetSnapshotMissing) {
		t.Fatalf("no snapshot: ok=%v error=%q", res.OK, res.Error)
	}
}

// TestRenderServePathRefusesSelectorMismatch pins that rows selected by
// another leading run are never served: the envelope's step hash must equal
// the live revision's leading run.
func TestRenderServePathRefusesSelectorMismatch(t *testing.T) {
	rt, _, snapshot := fetchedFleetRecord(t, fleetTestRows(2, "trojan"), subscriptionRecord{ID: "jp"})
	env, ok := decodeSnapshotEnvelope(snapshot)
	if !ok {
		t.Fatal("snapshot is not an envelope")
	}
	env.Selector.StepHash = fleetStepHash([]json.RawMessage{json.RawMessage(`{"type":"Structured Filter","args":{"predicates":[]}}`)})
	env.Selector.Leading = 0
	tampered, _ := json.Marshal(env)
	_, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "jp", "raw": string(tampered), "target": "sing-box"})
	if res.OK || !strings.HasPrefix(res.Error, codeFleetSelectorMismatch) {
		t.Fatalf("a foreign step hash: ok=%v error=%q", res.OK, res.Error)
	}
	env.Selector = nil
	tampered, _ = json.Marshal(env)
	_, res = renderFleetCall(t, rt, map[string]any{"subscription_id": "jp", "raw": string(tampered), "target": "sing-box"})
	if res.OK || !strings.HasPrefix(res.Error, codeFleetSelectorMismatch) {
		t.Fatalf("no selector record: ok=%v error=%q", res.OK, res.Error)
	}
}

// TestRenderServesTwoIdentitiesFromOneCachedPlan pins the serve path's use
// of the plan cache: core renders once per identity miss, and two renders of
// one snapshot and revision compute one plan on one worker.
func TestRenderServesTwoIdentitiesFromOneCachedPlan(t *testing.T) {
	rt, _, snapshot := fetchedFleetRecord(t, fleetTestRows(3, "hysteria2"), subscriptionRecord{ID: "cached"})
	before := fleetPlans.computeCount()
	var plans []string
	for identity := 0; identity < 2; identity++ {
		out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "cached", "raw": snapshot, "target": "ClashMeta"})
		if !res.OK {
			t.Fatal(res.Error)
		}
		plans = append(plans, string(out.Plan))
	}
	if got := fleetPlans.computeCount() - before; got != 1 || plans[0] != plans[1] {
		t.Fatalf("computes = %d, plans equal %v", got, plans[0] == plans[1])
	}
}

// TestLegacyRecordRenderWithoutRawIsWithheld pins the record half of the
// withholding rule: a legacy record rendered with no snapshot answers
// owner_credentials_withheld and makes no rpc call, where render used to
// read the export.
func TestLegacyRecordRenderWithoutRawIsWithheld(t *testing.T) {
	rt, host := newFleetRuntime(t, nil)
	if err := rt.saveSubscription(subscriptionRecord{ID: "legacy", Name: "legacy", Source: subscriptionSourceVPNCore}); err != nil {
		t.Fatal(err)
	}
	_, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "legacy", "target": "URI"})
	if res.OK || !strings.HasPrefix(res.Error, codeOwnerCredentialsWithheld) {
		t.Fatalf("a legacy record without raw: ok=%v error=%q", res.OK, res.Error)
	}
	if host.calls != 0 || host.other != 0 {
		t.Fatalf("rpc calls = %d catalogue, %d other", host.calls, host.other)
	}
}
