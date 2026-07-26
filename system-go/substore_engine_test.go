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

func TestSubStoreEngineUsesFreshRuntimePerConversion(t *testing.T) {
	engine := newSubStoreEngine(`
globalThis.SubStoreProxyUtils = {
  parse() {
    globalThis.__calls = (globalThis.__calls || 0) + 1;
    return [{ name: "node" }];
  },
  produce() {
    return String(globalThis.__calls);
  }
};
`)

	first, err := engine.convert(subStoreConversionRequest{Raw: "ss://one", Target: "Clash"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.convert(subStoreConversionRequest{Raw: "ss://two", Target: "Clash"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Output != "1" || second.Output != "1" {
		t.Fatalf("runtime state leaked across conversions: first=%q second=%q", first.Output, second.Output)
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

func TestSubStoreEngineConvertCallDoesNotUseHost(t *testing.T) {
	host := &fakeHostCaller{}
	engine := newTestEmbeddedSubStoreEngine()
	rt := &runtime{host: host, engine: &engine}
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

	resp := rt.handleCall(payload)
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

func newTestEmbeddedSubStoreEngine() subStoreEngine {
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
