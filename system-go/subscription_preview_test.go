package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const previewFixture = "vless://11111111-1111-1111-1111-111111111111@example.com:443?security=reality&sni=a.com&fp=chrome&pbk=x#node-a\n" +
	"vless://22222222-2222-2222-2222-222222222222@example.net:443?security=reality&sni=b.com&fp=chrome&pbk=y#node-b"

func TestPreviewReportsNodesWithoutCredentials(t *testing.T) {
	rt, _ := newKVRuntime(t)

	out, err := rt.previewSubscription(previewFixture, nil, "URI", false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if out.SourceNodeCount != 2 || out.NodeCount != 2 {
		t.Fatalf("counts = %d/%d, want 2/2", out.SourceNodeCount, out.NodeCount)
	}

	// A preview answers "did my filter keep the right nodes, and are they the
	// shape I expect" — so endpoint and transport flags cross the boundary (the
	// operator asked for upstream's level of detail, 2026-08-11), while anything
	// that would make the preview a credential dump still does not: the
	// reduction happens inside the engine, and uuid, keys and SNI never leave it.
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"pbk", "sni", "a.com", "b.com",
	} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("preview leaked %q: %s", leak, encoded)
		}
	}
	first := out.Nodes[0]
	if first.Name != "node-a" || out.Nodes[1].Name != "node-b" {
		t.Fatalf("names = %q/%q, want node-a/node-b", first.Name, out.Nodes[1].Name)
	}
	if first.Type != "vless" || first.Server != "example.com" || first.Port != "443" {
		t.Fatalf("endpoint summary = %s %s:%s, want vless example.com:443", first.Type, first.Server, first.Port)
	}
	if first.Security != "reality" {
		t.Fatalf("security = %q, want reality", first.Security)
	}
}

// A preview is how an operator sees what a filter does before saving it, so the
// node count after the pipeline is the answer it exists to give.
func TestPreviewAppliesTheOperatorPipeline(t *testing.T) {
	rt, _ := newKVRuntime(t)
	ops := []json.RawMessage{json.RawMessage(`{"type":"Regex Filter","args":{"regex":["node-a"],"keep":true}}`)}

	out, err := rt.previewSubscription(previewFixture, ops, "URI", false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	// Exactly the matching node, not merely "fewer than before". This test used
	// to assert only that the count fell, and passed while the filter kept
	// nothing at all: its args named a key the operator does not read, so every
	// run reduced two nodes to zero and the assertion was satisfied.
	if out.SourceNodeCount != 2 {
		t.Fatalf("source count = %d, want 2", out.SourceNodeCount)
	}
	if out.NodeCount != 1 || out.Nodes[0].Name != "node-a" {
		t.Fatalf("kept %d node(s) %v, want just node-a", out.NodeCount, out.Nodes)
	}
}

func TestPreviewRejectsAnUnknownOperatorBeforeRunning(t *testing.T) {
	rt, _ := newKVRuntime(t)
	ops := []json.RawMessage{json.RawMessage(`{"type":"Not An Operator","args":{}}`)}
	if _, err := rt.previewSubscription(previewFixture, ops, "URI", false); err == nil {
		t.Fatal("an unknown operator was previewed as if it worked")
	}
}

func TestPreviewNeedsContent(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if _, err := rt.previewSubscription("   ", nil, "URI", false); err == nil {
		t.Fatal("preview accepted empty content")
	}
}

// A count says a filter bit; the list says which nodes it bit. Tuning a filter
// means reading the names it removed, which is the half a preview of the
// survivors alone cannot show.
func TestPreviewNamesTheNodesTheChainRemoved(t *testing.T) {
	rt, _ := newKVRuntime(t)
	ops := []json.RawMessage{json.RawMessage(`{"type":"Regex Filter","args":{"regex":["node-a"],"keep":true}}`)}

	out, err := rt.previewSubscription(previewFixture, ops, "URI", false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if out.DroppedCount != 1 || len(out.Dropped) != 1 {
		t.Fatalf("dropped = %d (%d listed), want 1 (1 listed)", out.DroppedCount, len(out.Dropped))
	}
	if out.Dropped[0].Name != "node-b" {
		t.Fatalf("dropped node = %q, want node-b", out.Dropped[0].Name)
	}
	// The removed node is a summary like any other, so it carries its endpoint
	// and none of its secrets.
	if out.Dropped[0].Server != "example.net" || out.Dropped[0].Type != "vless" {
		t.Fatalf("dropped summary = %s %s, want vless example.net", out.Dropped[0].Type, out.Dropped[0].Server)
	}
	encoded, err := json.Marshal(out.Dropped)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "22222222-2222-2222-2222-222222222222") {
		t.Fatalf("the removed node carried its credential out: %s", encoded)
	}
}

// A rename is the one chain edit invisible in the result alone: the new name
// looks like it was always the name. Pairing by endpoint recovers the old one.
func TestPreviewReportsWhatTheChainRenamed(t *testing.T) {
	rt, _ := newKVRuntime(t)
	ops := []json.RawMessage{
		json.RawMessage(`{"type":"Regex Rename Operator","args":[{"expr":"node-","now":"edge-"}]}`),
	}

	out, err := rt.previewSubscription(previewFixture, ops, "URI", false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if out.NodeCount != 2 || out.DroppedCount != 0 {
		t.Fatalf("a rename changed the population: %d kept, %d dropped", out.NodeCount, out.DroppedCount)
	}
	if out.Nodes[0].Name != "edge-a" {
		t.Fatalf("name = %q, want edge-a", out.Nodes[0].Name)
	}
	if out.Nodes[0].Was != "node-a" || out.Nodes[1].Was != "node-b" {
		t.Fatalf("previous names = %q/%q, want node-a/node-b", out.Nodes[0].Was, out.Nodes[1].Was)
	}
}

// Untouched nodes must not be dressed up as renames, or every preview reads as
// though the chain rewrote the whole subscription.
func TestPreviewLeavesUnchangedNamesUnannotated(t *testing.T) {
	rt, _ := newKVRuntime(t)
	out, err := rt.previewSubscription(previewFixture, nil, "URI", false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, node := range out.Nodes {
		if node.Was != "" {
			t.Fatalf("node %q claims it was renamed from %q, and no operator ran", node.Name, node.Was)
		}
	}
}

// Two nodes can share an endpoint and differ only by name. The pairing claims
// exact name matches first for this case: pairing the untouched one against its
// renamed sibling would invent a rename and hide a real one.
func TestPreviewPairsSharedEndpointsByNameFirst(t *testing.T) {
	rt, _ := newKVRuntime(t)
	shared := "vless://11111111-1111-1111-1111-111111111111@example.com:443?security=reality&sni=a.com&fp=chrome&pbk=x#keep-me\n" +
		"vless://11111111-1111-1111-1111-111111111111@example.com:443?security=reality&sni=a.com&fp=chrome&pbk=x#rename-me"
	ops := []json.RawMessage{
		json.RawMessage(`{"type":"Regex Rename Operator","args":[{"expr":"rename-me","now":"renamed"}]}`),
	}

	out, err := rt.previewSubscription(shared, ops, "URI", false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if out.DroppedCount != 0 {
		t.Fatalf("dropped %d nodes, want 0", out.DroppedCount)
	}
	byName := map[string]nodeSummary{}
	for _, node := range out.Nodes {
		byName[node.Name] = node
	}
	if got, ok := byName["keep-me"]; !ok || got.Was != "" {
		t.Fatalf("the untouched node was reported as renamed from %q", got.Was)
	}
	if got, ok := byName["renamed"]; !ok || got.Was != "rename-me" {
		t.Fatalf("renamed node was = %q, want rename-me", got.Was)
	}
}
