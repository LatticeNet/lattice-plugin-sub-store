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
	ops := []json.RawMessage{json.RawMessage(`{"type":"Regex Filter","args":{"keywords":["node-a"]}}`)}

	out, err := rt.previewSubscription(previewFixture, ops, "URI", false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if out.SourceNodeCount != 2 {
		t.Fatalf("source count = %d, want 2", out.SourceNodeCount)
	}
	if out.NodeCount >= out.SourceNodeCount {
		t.Fatalf("the filter had no effect: %d of %d kept", out.NodeCount, out.SourceNodeCount)
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
