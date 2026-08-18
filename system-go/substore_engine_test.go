package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fastschema/qjs"
)

func TestSubStoreEngineConvertsWithQuickJSCoreFixture(t *testing.T) {
	engine := newSubStoreEngine(`
globalThis.SubStoreProxyUtils = {
  parse(raw) {
    return raw.split(/\n+/).map((line) => line.trim()).filter(Boolean)
      .map((line, index) => ({ name: "node-" + (index + 1), raw: line }));
  },
  produce(proxies, target, env) {
    return JSON.stringify({
      target,
      env,
      names: proxies.map((proxy) => proxy.name),
      globals: {
        process: typeof globalThis.process,
        require: typeof globalThis.require,
        fetch: typeof globalThis.fetch,
        WebSocket: typeof globalThis.WebSocket,
        XMLHttpRequest: typeof globalThis.XMLHttpRequest,
        Deno: typeof globalThis.Deno,
        Bun: typeof globalThis.Bun,
        document: typeof globalThis.document,
        localStorage: typeof globalThis.localStorage
      }
    });
  }
};
`)

	result, err := engine.convert(subStoreConversionRequest{
		Raw:    "ss://one\n\nvless://two",
		Target: "sing-box",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Target != "sing-box" || result.SourceNodeCount != 2 ||
		result.NodeCount != 2 || result.OutputBytes != len([]byte(result.Output)) {
		t.Fatalf("conversion metadata: %+v", result)
	}

	var output struct {
		Target  string   `json:"target"`
		Env     string   `json:"env"`
		Names   []string `json:"names"`
		Globals map[string]string
	}
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatal(err)
	}
	if output.Target != "sing-box" || output.Env != "external" {
		t.Fatalf("target/env: %+v", output)
	}
	if len(output.Names) != 2 || output.Names[0] != "node-1" || output.Names[1] != "node-2" {
		t.Fatalf("names: %v", output.Names)
	}
	for name, kind := range output.Globals {
		if kind != "undefined" {
			t.Fatalf("host global %s is %s, want undefined", name, kind)
		}
	}
}

// The engine's isolation contract is per-path, not per-call. Scriptless
// conversions deliberately share the warm runtime — that reuse is what
// removed the ~13.5 s per-invocation boot — so core-held state persists
// across them. Anything carrying user JavaScript still gets a fresh runtime
// every time, so nothing user-written can read or poison state a later call
// would inherit.
func TestSubStoreEngineIsolatesUserScriptRunsNotWarmOnes(t *testing.T) {
	engine := newSubStoreEngine(statefulTestCore)
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	first, err := engine.convert(subStoreConversionRequest{Raw: "ss://one", Target: "Clash"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.convert(subStoreConversionRequest{Raw: "ss://two", Target: "Clash"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Output != "1" || second.Output != "2" {
		t.Fatalf("warm conversions did not share the runtime: first=%q second=%q", first.Output, second.Output)
	}

	// A scripting chain must see a fresh runtime with none of that state.
	scriptOp := json.RawMessage(`{"type":"Script Operator","args":{"content":"function operator(p){ return p; }"}}`)
	isolated, err := engine.convert(subStoreConversionRequest{Raw: "ss://three", Target: "Clash", Operators: []json.RawMessage{scriptOp}})
	if err != nil {
		t.Fatal(err)
	}
	if isolated.Output != "1" {
		t.Fatalf("user-script run inherited warm state: output=%q, want fresh count 1", isolated.Output)
	}
	// And it must not have poisoned the warm runtime either.
	third, err := engine.convert(subStoreConversionRequest{Raw: "ss://four", Target: "Clash"})
	if err != nil {
		t.Fatal(err)
	}
	if third.Output != "3" {
		t.Fatalf("warm runtime state disturbed by isolated run: output=%q, want 3", third.Output)
	}
}

func TestSubStoreEngineRedactsConversionErrorInput(t *testing.T) {
	secretRaw := "ss://password@secret-node.example:443"
	engine := newSubStoreEngine(`
globalThis.SubStoreProxyUtils = {
  parse(raw) {
    throw new Error("bad subscription " + raw);
  },
  produce() {
    return "";
  }
};
`)

	_, err := engine.convert(subStoreConversionRequest{Raw: secretRaw, Target: "Clash"})
	if err == nil {
		t.Fatal("expected conversion error")
	}
	if strings.Contains(err.Error(), secretRaw) || !strings.Contains(err.Error(), "error_sha256=") {
		t.Fatalf("conversion error was not redacted: %v", err)
	}

	panicErr := redactSubStoreEnginePanic("panic near " + secretRaw)
	if strings.Contains(panicErr.Error(), secretRaw) || !strings.Contains(panicErr.Error(), "panic_sha256=") {
		t.Fatalf("panic error was not redacted: %v", panicErr)
	}
}

func TestSubStoreEngineRejectsInvalidCoreContract(t *testing.T) {
	_, err := newSubStoreEngine(`globalThis.SubStoreProxyUtils = {};`).convert(subStoreConversionRequest{
		Raw:    "ss://one",
		Target: "Clash",
	})
	if err == nil || !strings.Contains(err.Error(), "error_sha256=") {
		t.Fatalf("invalid core contract error: %v", err)
	}

	_, err = newSubStoreEngine("").convert(subStoreConversionRequest{
		Raw:    "ss://one",
		Target: "Clash",
	})
	if err == nil || !strings.Contains(err.Error(), "core bundle is empty") {
		t.Fatalf("empty core error: %v", err)
	}
}

func TestEmbeddedSubStoreCoreMatchesPinnedMetadata(t *testing.T) {
	raw, err := os.ReadFile("../tools/substore-core/pin.json")
	if err != nil {
		t.Fatal(err)
	}
	var pin struct {
		OutputBytes  int    `json:"output_bytes"`
		OutputSHA256 string `json:"output_sha256"`
	}
	if err := json.Unmarshal(raw, &pin); err != nil {
		t.Fatal(err)
	}
	if len([]byte(embeddedSubStoreCoreJS)) != pin.OutputBytes {
		t.Fatalf("embedded core bytes = %d, want %d", len([]byte(embeddedSubStoreCoreJS)), pin.OutputBytes)
	}
	sum := sha256.Sum256([]byte(embeddedSubStoreCoreJS))
	if got := fmt.Sprintf("%x", sum[:]); got != pin.OutputSHA256 {
		t.Fatalf("embedded core sha256 = %s, want %s", got, pin.OutputSHA256)
	}
}

func TestEmbeddedSubStoreCoreExposesWidenedProxyUtils(t *testing.T) {
	rt, err := qjs.New(qjs.Option{Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx := rt.Context()
	if err := evalQuickJSStep(ctx, "lattice-console-shim.js", subStoreConsoleShim); err != nil {
		t.Fatal(err)
	}
	if err := evalQuickJSStep(ctx, "substore-core.js", embeddedSubStoreCoreJS); err != nil {
		t.Fatal(err)
	}
	value, err := ctx.Eval("substore-core-capabilities.js", qjs.Code(`(function() {
  const root = globalThis.SubStoreProxyUtils;
  const core = root && root.ProxyUtils ? root.ProxyUtils : root;
  return JSON.stringify({
    parse: typeof core.parse,
    process: typeof core.process,
    processResponse: typeof core.processResponse,
    produce: typeof core.produce
  });
})()`))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Free()

	var got map[string]string
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatal(err)
	}
	for name, kind := range got {
		if kind != "function" {
			t.Fatalf("ProxyUtils.%s is %s, want function", name, kind)
		}
	}
}

func TestEmbeddedSubStoreCoreConvertsRepresentativeSubscription(t *testing.T) {
	result, err := newTestEmbeddedSubStoreEngine().convert(subStoreConversionRequest{
		Raw:    "ss://YWVzLTEyOC1nY206c2VjcmV0@example.com:8388#Node",
		Target: "Clash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NodeCount != 1 || result.OutputBytes == 0 || !strings.Contains(result.Output, "Node") {
		t.Fatalf("embedded core conversion: %+v", result)
	}
}

func TestEmbeddedSubStoreCoreAppliesScriptPipeline(t *testing.T) {
	operators := []json.RawMessage{
		json.RawMessage(`{
			"type": "Script Filter",
			"args": {
				"mode": "script",
				"content": "function filter(proxies) { return proxies.map((proxy) => proxy.name.includes('Keep')); }"
			}
		}`),
		json.RawMessage(`{
			"type": "Script Operator",
			"args": {
				"mode": "script",
				"content": "function operator(proxies) { return proxies.map((proxy) => ({ ...proxy, name: proxy.name + '-OK' })); }"
			}
		}`),
	}
	result, err := newTestEmbeddedSubStoreEngine().convert(subStoreConversionRequest{
		Raw: strings.Join([]string{
			"ss://YWVzLTEyOC1nY206c2VjcmV0@keep.example.com:8388#Keep",
			"ss://YWVzLTEyOC1nY206c2VjcmV0@drop.example.com:8388#Drop",
		}, "\n"),
		Target:    "Clash",
		Operators: operators,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceNodeCount != 2 || result.NodeCount != 1 ||
		!strings.Contains(result.Output, "Keep-OK") || strings.Contains(result.Output, "Drop") {
		t.Fatalf("script pipeline result: %+v", result)
	}
}

func TestEmbeddedSubStoreCoreAppliesNodeShortcutPipeline(t *testing.T) {
	operators := []json.RawMessage{
		json.RawMessage(`{
			"type": "Script Filter",
			"args": {
				"mode": "script",
				"content": "return $server.name.includes('Keep');"
			}
		}`),
		json.RawMessage(`{
			"type": "Script Operator",
			"args": {
				"mode": "script",
				"content": "$server.name = $server.name + '-NODE';"
			}
		}`),
	}
	result, err := newTestEmbeddedSubStoreEngine().convert(subStoreConversionRequest{
		Raw: strings.Join([]string{
			"ss://YWVzLTEyOC1nY206c2VjcmV0@keep.example.com:8388#Keep",
			"ss://YWVzLTEyOC1nY206c2VjcmV0@drop.example.com:8388#Drop",
		}, "\n"),
		Target:    "Clash",
		Operators: operators,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceNodeCount != 2 || result.NodeCount != 1 ||
		!strings.Contains(result.Output, "Keep-NODE") || strings.Contains(result.Output, "Drop") {
		t.Fatalf("node shortcut pipeline result: %+v", result)
	}
}

func TestEmbeddedSubStoreCoreAppliesResponseTransformerPipeline(t *testing.T) {
	operators := []json.RawMessage{
		json.RawMessage(`{
			"type": "Script Operator",
			"args": {
				"mode": "script",
				"content": "function operator() { throw new Error('proxy operators must be skipped for responses'); }"
			}
		}`),
		json.RawMessage(`{
			"type": "Response Transformer",
			"args": {
				"mode": "script",
				"content": "function transformFunction(res, context) { context.process = { type: 'disable', customNames: ['branch-b'] }; res.body += 'A'; res.headers['x-stage'] = 'a'; return res; }"
			}
		}`),
		json.RawMessage(`{
			"type": "Response Transformer",
			"customName": "branch-b",
			"args": {
				"mode": "script",
				"content": "function transformFunction(res) { res.body += 'B'; return res; }"
			}
		}`),
		json.RawMessage(`{
			"type": "Response Transformer",
			"customName": "branch-c",
			"args": {
				"mode": "script",
				"content": "function transformFunction(res) { res.body += 'C'; res.headers['x-finished'] = 'yes'; return res; }"
			}
		}`),
	}
	result, err := newTestEmbeddedSubStoreEngine().transformResponse(subStoreResponseTransformRequest{
		Response:  json.RawMessage(`{"status":206,"headers":{"x-source":"fixture"},"body":""}`),
		Target:    "Clash",
		Operators: operators,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Target != "Clash" || result.Status != 206 || result.Body != "AC" ||
		result.BodyBytes != len([]byte(result.Body)) {
		t.Fatalf("response transform result: %+v", result)
	}
	if result.Headers["x-source"] != "fixture" || result.Headers["x-stage"] != "a" ||
		result.Headers["x-finished"] != "yes" {
		t.Fatalf("response transform headers: %+v", result.Headers)
	}
}

func TestSubStoreEngineConvertCallDoesNotUseHost(t *testing.T) {
	host := &fakeHostCaller{}
	engine := newTestEmbeddedSubStoreEngine()
	rt := &runtime{host: host, engine: engine}
	payload, err := json.Marshal(callPayload{
		Service: pluginID + "/engine",
		Method:  "convert",
		Payload: mustJSON(subStoreConversionRequest{
			Raw:    "ss://YWVzLTEyOC1nY206c2VjcmV0@example.com:8388#Node",
			Target: "Clash",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := rt.handle(request{Action: "call", Payload: payload})
	if !resp.OK {
		t.Fatalf("engine convert call failed: %+v", resp)
	}
	if len(host.calls) != 0 {
		t.Fatalf("engine conversion reached host: %+v", host.calls)
	}
	var result subStoreConversionResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.NodeCount != 1 || result.OutputBytes == 0 || !strings.Contains(result.Output, "Node") {
		t.Fatalf("engine conversion result: %+v", result)
	}
}

func TestSubStoreEngineResponseTransformCallDoesNotUseHost(t *testing.T) {
	host := &fakeHostCaller{}
	engine := newTestEmbeddedSubStoreEngine()
	rt := &runtime{host: host, engine: engine}
	payload, err := json.Marshal(callPayload{
		Service: pluginID + "/engine",
		Method:  "transform_response",
		Payload: mustJSON(subStoreResponseTransformRequest{
			Response: json.RawMessage(`{"body":"seed"}`),
			Operators: []json.RawMessage{json.RawMessage(`{
				"type": "Response Transformer",
				"args": {
					"mode": "script",
					"content": "function transformFunction(res) { res.body += '-done'; return res; }"
				}
			}`)},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := rt.handle(request{Action: "call", Payload: payload})
	if !resp.OK {
		t.Fatalf("engine response transform call failed: %+v", resp)
	}
	if len(host.calls) != 0 {
		t.Fatalf("engine response transform reached host: %+v", host.calls)
	}
	var result subStoreResponseTransformResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != 200 || result.Body != "seed-done" || result.BodyBytes != len([]byte("seed-done")) {
		t.Fatalf("engine response transform result: %+v", result)
	}
}

func newTestEmbeddedSubStoreEngine() *subStoreEngine {
	engine := newEmbeddedSubStoreEngine()
	engine.limits.Timeout = 30 * time.Second
	return engine
}

func TestSubStoreEngineConvertsPinnedCoreWhenProvided(t *testing.T) {
	corePath := os.Getenv("LATTICE_SUBSTORE_CORE_JS")
	if corePath == "" {
		t.Skip("set LATTICE_SUBSTORE_CORE_JS to a built ProxyUtils IIFE bundle")
	}
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := newSubStoreEngine(string(core)).convert(subStoreConversionRequest{
		Raw:    "ss://YWVzLTEyOC1nY206c2VjcmV0@example.com:8388#Node",
		Target: "Clash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NodeCount != 1 || result.OutputBytes == 0 || !strings.Contains(result.Output, "Node") {
		t.Fatalf("pinned core conversion: %+v", result)
	}
}

// Every target the UI curates must stay producible by the pinned core: a pin
// bump that drops or renames a producer must break the build here rather than
// surface as a runtime conversion error. The list mirrors CONVERT_TARGETS in
// ui/src/client.ts.
func TestEmbeddedSubStoreCoreProducesEveryCuratedTarget(t *testing.T) {
	engine := newTestEmbeddedSubStoreEngine()
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	targets := []string{
		"Clash", "ClashMeta", "sing-box", "Surge", "Loon", "Stash", "QX",
		"Shadowrocket", "Egern", "Surfboard", "SurgeMac", "URI", "V2Ray",
	}
	for _, target := range targets {
		result, err := engine.convert(subStoreConversionRequest{
			Raw:    "ss://YWVzLTEyOC1nY206c2VjcmV0@example.com:8388#Node",
			Target: target,
		})
		if err != nil {
			t.Errorf("target %s: %v", target, err)
			continue
		}
		if result.OutputBytes == 0 {
			t.Errorf("target %s produced no output", target)
		}
	}
}

// TestSubStoreEngineThreadsProduceOptions pins the parity contract that
// produce() receives the caller's option flags as its fourth argument —
// Sub-Store's own mechanism for include-unsupported-proxy and friends. A
// conversion without options must still call produce with an empty object,
// not undefined, so the core's `opts?.flag` reads stay well-defined.
func TestSubStoreEngineThreadsProduceOptions(t *testing.T) {
	engine := newSubStoreEngine(`
globalThis.SubStoreProxyUtils = {
  parse(raw) { return [{ name: "n1" }]; },
  produce(proxies, target, env, opts) {
    return JSON.stringify({ target, opts: opts || null });
  },
};`)

	withOpts, err := engine.convert(subStoreConversionRequest{
		Raw:    "x",
		Target: "Stash",
		Options: map[string]bool{
			"include-unsupported-proxy": true,
		},
	})
	if err != nil {
		t.Fatalf("convert with options: %v", err)
	}
	if !strings.Contains(withOpts.Output, `"include-unsupported-proxy":true`) {
		t.Fatalf("produce did not receive the option flags: %s", withOpts.Output)
	}

	without, err := engine.convert(subStoreConversionRequest{Raw: "x", Target: "Stash"})
	if err != nil {
		t.Fatalf("convert without options: %v", err)
	}
	if !strings.Contains(without.Output, `"opts":{}`) {
		t.Fatalf("produce must receive an empty object when no options are set: %s", without.Output)
	}
}
