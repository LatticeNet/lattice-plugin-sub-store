package main

import (
	"strings"
	"testing"
)

// A record made of this fleet's own nodes renders for Clash as the nine bytes
// "proxies:" and nothing else, because Clash carries neither VLESS nor
// Hysteria2. That is correct, and for a long time it was also indistinguishable
// from a broken render. The engine now says what the client refused.
//
// The support rules live inside each producer and are declared nowhere a caller
// can read, so this asks the producer rather than keeping a table that would
// drift from the pinned core.

const fleetShapedNodes = "vless://0cd403ef-ce9b-4c7c-b3e8-472397e616f7@a.example:34099?security=reality&type=tcp&sni=x.example&fp=chrome&flow=xtls-rprx-vision&pbk=n6g3w4_4iiLhntHLX3DFRwuOWmu28PhaLcLj9D3jIw8#one\n" +
	"hysteria2://d9362106-8c97-46e2-ac27-7f9a8e785d95@b.example:13434?insecure=1&sni=b.example#two\n" +
	"ss://YWVzLTEyOC1nY206dGVzdA@c.example:8388#three"

func TestEngineNamesWhatTheClientRefused(t *testing.T) {
	engine := newEmbeddedSubStoreEngine()
	if err := engine.prewarm(); err != nil {
		t.Fatal(err)
	}

	clash, err := engine.convert(subStoreConversionRequest{Raw: fleetShapedNodes, Target: "Clash", Explain: true})
	if err != nil {
		t.Fatal(err)
	}
	if clash.NodeCount != 3 {
		t.Fatalf("the chain produced %d nodes, want 3", clash.NodeCount)
	}
	if clash.UnsupportedNodeCount != 2 {
		t.Fatalf("Clash refused %d nodes, want the vless and the hysteria2", clash.UnsupportedNodeCount)
	}
	if got := strings.Join(clash.UnsupportedProtocols, ","); got != "hysteria2,vless" {
		t.Fatalf("refused protocols = %q, want hysteria2,vless", got)
	}
	// The document really is nearly empty, which is the whole reason this
	// diagnosis has to exist.
	if !strings.Contains(clash.Output, "proxies:") || strings.Contains(clash.Output, "vless") {
		t.Fatalf("Clash document should hold only the shadowsocks node: %q", clash.Output)
	}

	meta, err := engine.convert(subStoreConversionRequest{Raw: fleetShapedNodes, Target: "ClashMeta", Explain: true})
	if err != nil {
		t.Fatal(err)
	}
	if meta.UnsupportedNodeCount != 0 || len(meta.UnsupportedProtocols) != 0 {
		t.Fatalf("mihomo carries all three: %d %v", meta.UnsupportedNodeCount, meta.UnsupportedProtocols)
	}
}

// Asking the client to take them anyway leaves nothing to explain.
func TestIncludeUnsupportedLeavesNothingRefused(t *testing.T) {
	engine := newEmbeddedSubStoreEngine()
	if err := engine.prewarm(); err != nil {
		t.Fatal(err)
	}
	out, err := engine.convert(subStoreConversionRequest{
		Raw: fleetShapedNodes, Target: "Clash", Explain: true,
		Options: map[string]bool{"include-unsupported-proxy": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.UnsupportedNodeCount != 0 || len(out.UnsupportedProtocols) != 0 {
		t.Fatalf("with the flag on nothing is refused: %d %v", out.UnsupportedNodeCount, out.UnsupportedProtocols)
	}
	if !strings.Contains(out.Output, "vless") {
		t.Fatalf("the flag should put the vless node in the document: %q", out.Output)
	}
}

// The diagnosis costs extra produce calls, so the path that answers a client
// must not pay for it. A caller that did not ask gets exactly what it got
// before, and no counting happens.
func TestServePathAsksForNoDiagnosis(t *testing.T) {
	engine := newEmbeddedSubStoreEngine()
	if err := engine.prewarm(); err != nil {
		t.Fatal(err)
	}
	quiet, err := engine.convert(subStoreConversionRequest{Raw: fleetShapedNodes, Target: "Clash"})
	if err != nil {
		t.Fatal(err)
	}
	if quiet.UnsupportedNodeCount != 0 || len(quiet.UnsupportedProtocols) != 0 {
		t.Fatalf("an unasked-for diagnosis was computed: %d %v", quiet.UnsupportedNodeCount, quiet.UnsupportedProtocols)
	}
	loud, err := engine.convert(subStoreConversionRequest{Raw: fleetShapedNodes, Target: "Clash", Explain: true})
	if err != nil {
		t.Fatal(err)
	}
	// The document itself must not depend on whether anyone asked why.
	if quiet.Output != loud.Output {
		t.Fatalf("explaining changed the document:\n%q\n%q", quiet.Output, loud.Output)
	}
}
