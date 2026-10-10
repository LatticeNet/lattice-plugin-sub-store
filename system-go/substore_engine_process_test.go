package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func scriptStep(t *testing.T, program string) json.RawMessage {
	t.Helper()
	return step(t, map[string]any{"type": "Script Operator", "args": map[string]any{"mode": "script", "content": program}})
}

// TestFleetScriptStepRunsOnBundleProcess pins S2 plan section 1.4: a fleet
// record with a Script Operator renders through engine.processNodes, the
// script sees and changes the nodes, and each node's line still comes from
// the placeholder its credential carries, with the row's Lattice block
// attached again whatever the script wrote under "_lattice". A script that
// moves a node's server keeps the node's line, so core rejects the node as
// plan_rejected:server rather than binding it to a foreign host.
func TestFleetScriptStepRunsOnBundleProcess(t *testing.T) {
	program := `function operator(proxies) {
  return proxies.map(function (p, i) {
    p.name = p.name + " [scripted]";
    if (p._lattice) { p._lattice.node_id = "forged"; p._lattice.line_uuid = "00000000-0000-4000-8000-000000000000"; }
    if (i === 0) { p.server = "foreign.example"; }
    return p;
  });
}`
	rt, rec, snapshot := fetchedFleetRecord(t, fleetTestRows(3, "trojan"), subscriptionRecord{ID: "scripted", Process: []json.RawMessage{scriptStep(t, program)}})
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": rec.ID, "raw": snapshot, "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	plan := decodeRenderPlan(t, out.Plan)
	if len(plan.Nodes) != 3 {
		t.Fatalf("nodes = %d", len(plan.Nodes))
	}
	for i, node := range plan.Nodes {
		var fields map[string]any
		if err := json.Unmarshal(node.Node, &fields); err != nil {
			t.Fatal(err)
		}
		if node.LineUUID != fleetTestLineUUID(i+1) {
			t.Fatalf("node %d line = %q", i, node.LineUUID)
		}
		if !strings.HasSuffix(fields["name"].(string), " [scripted]") {
			t.Fatalf("node %d name = %v: the script did not run", i, fields["name"])
		}
		if fields["node_id"] == "forged" || fields["_lattice"] != nil {
			t.Fatalf("node %d carries what the script wrote under _lattice: %s", i, node.Node)
		}
	}
	var first map[string]any
	_ = json.Unmarshal(plan.Nodes[0].Node, &first)
	if first["server"] != "foreign.example" || plan.Nodes[0].LineUUID == "" {
		t.Fatalf("a script-moved node lost its line or its server: %v", first)
	}
}

// TestInventedNodeInProviderCollectionIsNoLine pins provenance through a
// collection chain that runs on the bundle: a node the script invents is
// neither a line nor a provider node (core excludes it with no_line), a
// renamed provider node stays a provider node, and a provider node handed a
// fleet node's placeholder is read as that line, which core then checks
// against the line's server and rejects.
func TestInventedNodeInProviderCollectionIsNoLine(t *testing.T) {
	program := `function operator(proxies) {
  const fleet = proxies.find(function (p) { return p._lattice && p._lattice.line_uuid; });
  const providers = proxies.filter(function (p) { return p._lattice && p._lattice.provenance; });
  providers[0].name = providers[0].name + "-renamed";
  providers[1].password = fleet.password;
  proxies.push({ type: "trojan", name: "invented", server: "evil.example", port: 443, password: "nope" });
  return proxies;
}`
	rt, _ := fleetCollectionFixture(t, subscriptionRecord{ID: "scripted", Name: "scripted", Members: []string{"hk", "local"},
		Process: []json.RawMessage{scriptStep(t, program)}})
	fetched, err := rt.fetchSubscription("scripted")
	if err != nil {
		t.Fatal(err)
	}
	out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "scripted", "raw": fetched.Raw, "target": "sing-box"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	plan := decodeRenderPlan(t, out.Plan)
	// The parser names a provider node from its fragment and port.
	find := func(prefix string) int {
		for i, node := range plan.Nodes {
			var fields map[string]any
			_ = json.Unmarshal(node.Node, &fields)
			if name, _ := fields["name"].(string); strings.HasPrefix(name, prefix) {
				return i
			}
		}
		t.Fatalf("no node named %s*", prefix)
		return -1
	}
	invented, renamed, copied := plan.Nodes[find("invented")], plan.Nodes[find("provider-one")], plan.Nodes[find("provider-two")]
	if invented.LineUUID != "" || invented.Provider {
		t.Fatalf("an invented node = %+v", invented)
	}
	if !renamed.Provider || renamed.LineUUID != "" {
		t.Fatalf("a renamed provider node = %+v", renamed)
	}
	if copied.Provider || copied.LineUUID != fleetTestLineUUID(1) {
		t.Fatalf("a provider node with a copied placeholder = %+v", copied)
	}
}

// TestFleetBoundCollectionWithFallbackChainRendersNodeWise pins that a
// fleet-bound collection renders node-wise whatever runs on the bundle: a
// provider member's own script ran at fetch and its block carries the
// output, so it never runs again at render, and a collection-level script
// runs once over every node, fleet and provider.
func TestFleetBoundCollectionWithFallbackChainRendersNodeWise(t *testing.T) {
	memberScript := scriptStep(t, `function operator(proxies) { return proxies.map(function (p) { p.name = p.name + "-m"; return p; }); }`)
	collectionScript := scriptStep(t, `function operator(proxies) { return proxies.map(function (p) { p.name = p.name + "-c"; return p; }); }`)
	for name, setup := range map[string]struct{ member, collection []json.RawMessage }{
		"provider member chain":   {member: []json.RawMessage{memberScript}},
		"collection chain":        {collection: []json.RawMessage{collectionScript}},
		"both chains on a bundle": {member: []json.RawMessage{memberScript}, collection: []json.RawMessage{collectionScript}},
	} {
		t.Run(name, func(t *testing.T) {
			rt, _ := fleetCollectionFixture(t, subscriptionRecord{ID: "c", Name: "c", Members: []string{"hk", "local"}, Process: setup.collection})
			if err := rt.saveSubscription(subscriptionRecord{ID: "local", Name: "local", Source: subscriptionSourceLocal, Process: setup.member,
				Content: "trojan://pw1@198.51.100.7:443#provider-one\ntrojan://pw2@198.51.100.8:443#provider-two"}); err != nil {
				t.Fatal(err)
			}
			fetched, err := rt.fetchSubscription("c")
			if err != nil {
				t.Fatal(err)
			}
			out, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "c", "raw": fetched.Raw, "target": "sing-box"})
			if !res.OK {
				t.Fatal(res.Error)
			}
			plan := decodeRenderPlan(t, out.Plan)
			providers := 0
			for _, node := range plan.Nodes {
				var fields map[string]any
				_ = json.Unmarshal(node.Node, &fields)
				name := fields["name"].(string)
				if strings.Count(name, "-m") > 1 || strings.Count(name, "-c") > 1 {
					t.Fatalf("a chain ran twice: %q", name)
				}
				if setup.collection != nil && !strings.HasSuffix(name, "-c") {
					t.Fatalf("the collection chain skipped %q", name)
				}
				if node.Provider {
					providers++
					if setup.member != nil && !strings.Contains(name, "-m") {
						t.Fatalf("the provider member's chain output was lost: %q", name)
					}
				} else if node.LineUUID == "" {
					t.Fatalf("a node lost its line: %q", name)
				} else if strings.Contains(name, "-m") {
					t.Fatalf("a provider member's chain ran over fleet node %q", name)
				}
			}
			if providers != 2 {
				t.Fatalf("provider nodes = %d", providers)
			}
		})
	}
}
