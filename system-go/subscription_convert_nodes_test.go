package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
)

// convert accepts the SDK ConvertRequest's nodes: a typed-node plan's nodes
// after the core bound them. For a native target they are produced in Go,
// byte for byte what the same nodes sent as URIs produce, and no runtime is
// touched; for the nine other targets the bundle produces them on its
// isolated path (engine.produceNodes).
func TestConvertAcceptsNodes(t *testing.T) {
	engine := testEngineWithHeadroom()
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	rt := &runtime{host: denyHostCalls{}, engine: engine}
	// perfgen.Nodes(n) is exactly what parsing perfgen.URIs(n) gives.
	nodes, uris := perfgen.Nodes(3), perfgen.URIs(3)
	for _, target := range nativeTargets {
		fromNodes := decodeConvert(t, callConvert(t, rt, map[string]any{"nodes": nodes, "target": target, "format": "plain"}))
		fromURIs := decodeConvert(t, callConvert(t, rt, map[string]any{"uris": uris, "target": target, "format": "plain"}))
		if fromNodes.Content != fromURIs.Content || fromNodes.NodeCount != 3 || fromNodes.Target != target {
			t.Fatalf("%s: nodes converted to %d nodes and\n%s\nURIs to\n%s", target, fromNodes.NodeCount, head(fromNodes.Content, 300), head(fromURIs.Content, 300))
		}
	}
	if warm, isolated := engine.pathCounts(); warm != 0 || isolated != 0 {
		t.Fatalf("native converts reached the bundle: warm=%d isolated=%d", warm, isolated)
	}

	stash := decodeConvert(t, callConvert(t, rt, map[string]any{"nodes": nodes, "target": "Stash"}))
	if stash.NodeCount != 3 || !strings.Contains(stash.Content, "reality-opts") {
		t.Fatalf("Stash from nodes: %d nodes\n%s", stash.NodeCount, head(stash.Content, 300))
	}
	if warm, isolated := engine.pathCounts(); warm != 0 || isolated != 1 {
		t.Fatalf("a fallback target's nodes: warm=%d isolated=%d, want one isolated call", warm, isolated)
	}

	// The SDK's bounds hold: every node is a JSON object, and nodes do not
	// mix with another input.
	for name, payload := range map[string]map[string]any{
		"a node that is not an object": {"nodes": []any{"vless://x"}, "target": "URI"},
		"nodes beside uris":            {"nodes": nodes, "uris": uris, "target": "URI"},
	} {
		if res := callConvert(t, rt, payload); res.OK || !strings.Contains(res.Error, "invalid convert payload") {
			t.Fatalf("%s: ok=%v error=%q", name, res.OK, res.Error)
		}
	}
}

// A response chain is refused with a stated reason until S3 runs it in
// convert's isolate, before any engine work, and the reason never quotes the
// input. A document plan is substituted since S2
// (TestConvertDocumentSubstitutionPerScalar).
func TestConvertRefusesResponseChainWithReason(t *testing.T) {
	engine := testEngineWithHeadroom()
	rt := &runtime{host: denyHostCalls{}, engine: engine}
	const secret = "convert-secret-sentinel"
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"response chain", map[string]any{
			"uris":           perfgen.URIs(1),
			"target":         "URI",
			"response_chain": []map[string]any{{"name": "rewrite", "source": "function transform(r){ return '" + secret + "'; }", "enabled": true}},
		}, "response chain"},
	}
	for _, c := range cases {
		res := callConvert(t, rt, c.payload)
		if res.OK {
			t.Fatalf("%s: convert accepted it", c.name)
		}
		if !strings.HasPrefix(res.Error, convertUnsupportedCode+": ") || !strings.Contains(res.Error, c.want) {
			t.Fatalf("%s: error %q, want %s naming the %s", c.name, res.Error, convertUnsupportedCode, c.want)
		}
		if strings.Contains(res.Error, secret) {
			t.Fatalf("%s: the refusal quotes the input: %q", c.name, res.Error)
		}
	}
	if warm, isolated := engine.pathCounts(); warm != 0 || isolated != 0 {
		t.Fatalf("a refused convert reached the bundle: warm=%d isolated=%d", warm, isolated)
	}
	// The request is still the SDK's, decoded strictly: an unknown field is
	// refused before the reasons above are reached.
	raw, _ := json.Marshal(map[string]any{"uris": perfgen.URIs(1), "target": "URI", "plan": map[string]any{}})
	if res := rt.handleSubscriptionCall(callPayload{Method: "convert", Payload: raw}); res.OK || !strings.Contains(res.Error, "unknown field") {
		t.Fatalf("an unknown field: ok=%v error=%q", res.OK, res.Error)
	}
}
