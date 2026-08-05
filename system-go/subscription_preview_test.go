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

	out, err := rt.previewSubscription(previewFixture, nil, "URI")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if out.SourceNodeCount != 2 || out.NodeCount != 2 {
		t.Fatalf("counts = %d/%d, want 2/2", out.SourceNodeCount, out.NodeCount)
	}

	// A preview answers "did my filter keep the right nodes". It must not double
	// as a credential dump, so the reduction happens inside the engine and the
	// fields that identify a server never cross the process boundary.
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"example.com", "example.net", "443", "pbk", "sni",
	} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("preview leaked %q: %s", leak, encoded)
		}
	}
	names := []string{out.Nodes[0].Name, out.Nodes[1].Name}
	if names[0] != "node-a" || names[1] != "node-b" {
		t.Fatalf("names = %v, want node-a/node-b", names)
	}
}

// A preview is how an operator sees what a filter does before saving it, so the
// node count after the pipeline is the answer it exists to give.
func TestPreviewAppliesTheOperatorPipeline(t *testing.T) {
	rt, _ := newKVRuntime(t)
	ops := []json.RawMessage{json.RawMessage(`{"type":"Regex Filter","args":{"keywords":["node-a"]}}`)}

	out, err := rt.previewSubscription(previewFixture, ops, "URI")
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
	if _, err := rt.previewSubscription(previewFixture, ops, "URI"); err == nil {
		t.Fatal("an unknown operator was previewed as if it worked")
	}
}

func TestPreviewNeedsContent(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if _, err := rt.previewSubscription("   ", nil, "URI"); err == nil {
		t.Fatal("preview accepted empty content")
	}
}
