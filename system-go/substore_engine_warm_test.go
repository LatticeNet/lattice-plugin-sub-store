package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const warmTestURI = "vless://11111111-1111-1111-1111-111111111111@a.example:443?security=reality&sni=a.com&fp=chrome&pbk=x#HK-01"

// statefulTestCore counts parse calls in a global so tests can tell which
// runtime an evaluation landed on.
const statefulTestCore = `
globalThis.SubStoreProxyUtils = {
  parse() {
    globalThis.__calls = (globalThis.__calls || 0) + 1;
    return [{ name: "node" }];
  },
  process(proxies) { return proxies; },
  produce() {
    return String(globalThis.__calls);
  }
};
`

func (engine *subStoreEngine) pathCounts() (warm, isolated int) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.warmServes, int(engine.isolatedServes.Load())
}

// Scriptless calls answer from the persistent runtime, repeatedly: the second
// evaluation of a generated IIFE must not collide with the first in the
// shared global scope.
func TestWarmEngineServesScriptlessCallsFromPersistentRuntime(t *testing.T) {
	engine := newTestEmbeddedSubStoreEngine()
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	var outputs []string
	for i := 0; i < 3; i++ {
		result, err := engine.convert(subStoreConversionRequest{Raw: warmTestURI, Target: "URI"})
		if err != nil {
			t.Fatalf("warm convert #%d: %v", i+1, err)
		}
		outputs = append(outputs, result.Output)
	}
	if outputs[0] == "" || outputs[0] != outputs[1] || outputs[1] != outputs[2] {
		t.Fatalf("warm outputs drifted: %q", outputs)
	}
	warm, isolated := engine.pathCounts()
	if warm != 3 || isolated != 0 {
		t.Fatalf("path counts warm=%d isolated=%d, want 3/0", warm, isolated)
	}
}

// The warm and isolated paths must be indistinguishable in their answers —
// the isolated runtime is seeded from the bytecode the warm-up compiled, and
// both must produce byte-identical output for the same conversion script.
func TestWarmEngineParityWithIsolatedPath(t *testing.T) {
	engine := newTestEmbeddedSubStoreEngine()
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	script, err := subStoreConversionScript(subStoreConversionRequest{Raw: warmTestURI, Target: "URI"})
	if err != nil {
		t.Fatal(err)
	}
	warmOut, err := engine.runCoreScript("convert", "parity.js", script)
	if err != nil {
		t.Fatalf("warm run: %v", err)
	}
	isolatedOut, err := engine.runIsolatedScript("convert", "parity.js", script)
	if err != nil {
		t.Fatalf("isolated run: %v", err)
	}
	if warmOut != isolatedOut {
		t.Fatalf("parity broken:\nwarm=%s\nisolated=%s", warmOut, isolatedOut)
	}
	engine.mu.Lock()
	hasBytecode := len(engine.coreBytecode) > 0
	engine.mu.Unlock()
	if !hasBytecode {
		t.Fatal("prewarm did not compile core bytecode")
	}
}

// Chains carrying user JavaScript never touch the warm runtime.
func TestWarmEngineIsolatesUserScriptChains(t *testing.T) {
	engine := newTestEmbeddedSubStoreEngine()
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	script := `{"type":"Script Operator","args":{"content":"function operator(proxies){ return proxies.map(p => ({...p, name: p.name + '-tagged'})); }"}}`
	result, err := engine.convert(subStoreConversionRequest{
		Raw:       warmTestURI,
		Target:    "URI",
		Operators: []json.RawMessage{json.RawMessage(script)},
	})
	if err != nil {
		t.Fatalf("script convert: %v", err)
	}
	if !strings.Contains(result.Output, "-tagged") {
		t.Fatalf("script operator did not run: %q", result.Output)
	}
	warm, isolated := engine.pathCounts()
	if warm != 0 || isolated != 1 {
		t.Fatalf("path counts warm=%d isolated=%d, want 0/1", warm, isolated)
	}
}

// A script exception on the warm path is the call's answer, not the engine's
// death: the next call still answers warm.
func TestWarmEngineSurvivesScriptExceptions(t *testing.T) {
	engine := newTestEmbeddedSubStoreEngine()
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	if _, err := engine.runCoreScript("test", "boom.js", `(function(){ throw new Error("boom") })()`); err == nil {
		t.Fatal("expected the thrown error to surface")
	}
	result, err := engine.convert(subStoreConversionRequest{Raw: warmTestURI, Target: "URI"})
	if err != nil {
		t.Fatalf("convert after exception: %v", err)
	}
	if result.NodeCount != 1 {
		t.Fatalf("node count=%d, want 1", result.NodeCount)
	}
	warm, _ := engine.pathCounts()
	if warm != 2 {
		t.Fatalf("warm serves=%d, want 2 (exception call and recovery call)", warm)
	}
}

// After a warm death, calls never wait: the next scriptless call answers via
// the isolated path while a background boot restores the warm runtime.
func TestWarmEngineRebootsAfterDeathWithoutBlockingCalls(t *testing.T) {
	engine := newTestEmbeddedSubStoreEngine()
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	engine.mu.Lock()
	engine.markWarmDeadLocked()
	engine.mu.Unlock()
	result, err := engine.convert(subStoreConversionRequest{Raw: warmTestURI, Target: "URI"})
	if err != nil {
		t.Fatalf("convert after death: %v", err)
	}
	if result.NodeCount != 1 {
		t.Fatalf("node count=%d, want 1", result.NodeCount)
	}
	if _, isolated := engine.pathCounts(); isolated != 1 {
		t.Fatalf("post-death call did not take the isolated path (isolated=%d)", isolated)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		engine.mu.Lock()
		alive := engine.warm != nil
		engine.mu.Unlock()
		if alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background reboot never restored the warm runtime")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := engine.convert(subStoreConversionRequest{Raw: warmTestURI, Target: "URI"}); err != nil {
		t.Fatalf("convert after reboot: %v", err)
	}
	warm, _ := engine.pathCounts()
	if warm != 1 {
		t.Fatalf("rebooted warm runtime did not serve (warm=%d)", warm)
	}
}

// A wedged warm eval is interrupted at limits.Timeout by the watchdog: the
// call gets an answer, the runtime retires, and the engine keeps serving.
func TestWarmEngineWatchdogInterruptsWedgedCall(t *testing.T) {
	engine := newSubStoreEngine(statefulTestCore)
	engine.limits.Timeout = 2 * time.Second
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	start := time.Now()
	_, err := engine.runCoreScript("test", "wedge.js", `(function(){ for(;;){} })()`)
	if err == nil {
		t.Fatal("wedged call returned no error")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("watchdog took %s to interrupt", elapsed)
	}
	if _, err := engine.convert(subStoreConversionRequest{Raw: "ss://after", Target: "Clash"}); err != nil {
		t.Fatalf("engine did not survive the wedged call: %v", err)
	}
}

// Every user-JavaScript surface routes isolated, not just convert: response
// transformers and config merges must never reach the warm runtime.
func TestWarmEngineRoutesEveryUserScriptSurfaceIsolated(t *testing.T) {
	engine := newSubStoreEngine(statefulTestCore + `
globalThis.SubStoreProxyUtils.processResponse = function(response){ return { status: 200, headers: {}, body: "ok" }; };
`)
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	if _, err := engine.transformResponse(subStoreResponseTransformRequest{
		Response: json.RawMessage(`{"status":200,"headers":{},"body":"x"}`),
		Operators: []json.RawMessage{
			json.RawMessage(`{"type":"Response Transformer","args":{"content":"function transform(r){return r}"}}`),
		},
	}); err != nil {
		t.Fatalf("transformResponse: %v", err)
	}
	warm, isolated := engine.pathCounts()
	if warm != 0 || isolated != 1 {
		t.Fatalf("transformResponse path counts warm=%d isolated=%d, want 0/1", warm, isolated)
	}

	previewEngine := newSubStoreEngine(statefulTestCore)
	if err := previewEngine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	rt := &runtime{engine: previewEngine}
	scriptOp := json.RawMessage(`{"type":"Script Operator","args":{"content":"function operator(p){ return p; }"}}`)
	if _, err := rt.previewSubscription("ss://x", []json.RawMessage{scriptOp}, "URI", false); err != nil {
		t.Fatalf("script preview: %v", err)
	}
	if warm, isolated := previewEngine.pathCounts(); warm != 0 || isolated != 1 {
		t.Fatalf("script preview path counts warm=%d isolated=%d, want 0/1", warm, isolated)
	}
	if _, err := rt.previewSubscription("ss://y", nil, "URI", false); err != nil {
		t.Fatalf("scriptless preview: %v", err)
	}
	if warm, _ := previewEngine.pathCounts(); warm != 1 {
		t.Fatalf("scriptless preview did not answer warm (warm=%d)", warm)
	}
}

// A failed boot is memoized: within the cooldown the engine does not spawn
// another background attempt from the call path.
func TestWarmEngineMemoizesBootFailure(t *testing.T) {
	engine := newSubStoreEngine("throw new Error('broken core');")
	if err := engine.prewarm(); err == nil {
		t.Fatal("expected the broken core to fail the boot")
	}
	engine.mu.Lock()
	failedAt := engine.lastBootFail
	engine.startWarmBootLocked()
	spawned := engine.warming
	engine.mu.Unlock()
	if failedAt.IsZero() {
		t.Fatal("boot failure was not memoized")
	}
	if spawned {
		t.Fatal("call path spawned a boot inside the failure cooldown")
	}
}
