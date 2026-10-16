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
        localStorage: typeof globalThis.localStorage,
        std: typeof globalThis.std,
        os: typeof globalThis.os,
        bjson: typeof globalThis.bjson
      }
    });
  }
};
`)

	result, err := engine.convertIsolated(subStoreConversionRequest{
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

// statefulTestCore counts parse calls in a global, so a test can tell
// whether two calls shared a runtime.
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

// isolatedCount is how many calls the engine's isolated path has served.
func (engine *subStoreEngine) isolatedCount() int {
	return int(engine.isolatedServes.Load())
}

// runScriptsOnOneRuntime evaluates call scripts in turn on one isolated
// runtime and returns each one's stringified JSON answer. Each script is a
// complete call script, as subStoreConversionScript and previewScript build
// them. The parity tests ask the bundle dozens of questions about scriptless
// chains; S1 answered them back to back on the warm runtime, and this keeps
// that shape for the tests, at one core load, now that every production call
// gets a runtime of its own (S2 plan section 1.4). No production path
// batches.
func runScriptsOnOneRuntime(t testing.TB, engine *subStoreEngine, stage string, scripts []string) []string {
	t.Helper()
	var b strings.Builder
	b.WriteString("(async function () {\n  const answers = [];\n")
	for _, script := range scripts {
		b.WriteString("  answers.push(await (")
		b.WriteString(script)
		b.WriteString("));\n")
	}
	b.WriteString("  return JSON.stringify(answers);\n})()")
	raw, err := engine.runIsolatedScript(stage, "lattice-test-batch.js", b.String())
	if err != nil {
		t.Fatalf("%s: %v", stage, err)
	}
	var answers []string
	if err := json.Unmarshal([]byte(raw), &answers); err != nil || len(answers) != len(scripts) {
		t.Fatalf("%s: %d answers for %d scripts (%v)", stage, len(answers), len(scripts), err)
	}
	return answers
}

// Every call gets a runtime of its own, scriptless or not: from S2 there is
// no persistent runtime for a call to inherit state from or leave state in
// (S2 plan section 1.4). The stateful core counts its parses in a global, so
// a runtime two calls shared would answer 2 on the second.
func TestSubStoreEngineGivesEveryCallAFreshRuntime(t *testing.T) {
	engine := newSubStoreEngine(statefulTestCore)
	scriptOp := json.RawMessage(`{"type":"Script Operator","args":{"content":"function operator(p){ return p; }"}}`)
	for i, req := range []subStoreConversionRequest{
		{Raw: "ss://one", Target: "Clash"},
		{Raw: "ss://two", Target: "Clash"},
		{Raw: "ss://three", Target: "Clash", Operators: []json.RawMessage{scriptOp}},
		{Raw: "ss://four", Target: "Clash"},
	} {
		out, err := engine.convertIsolated(req)
		if err != nil {
			t.Fatal(err)
		}
		if out.Output != "1" {
			t.Fatalf("call %d saw %q parses in its runtime, want a fresh count of 1", i+1, out.Output)
		}
	}
	if got := engine.isolatedCount(); got != 4 {
		t.Fatalf("isolated calls = %d, want 4", got)
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

	_, err := engine.convertIsolated(subStoreConversionRequest{Raw: secretRaw, Target: "Clash"})
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
	_, err := newSubStoreEngine(`globalThis.SubStoreProxyUtils = {};`).convertIsolated(subStoreConversionRequest{
		Raw:    "ss://one",
		Target: "Clash",
	})
	if err == nil || !strings.Contains(err.Error(), "error_sha256=") {
		t.Fatalf("invalid core contract error: %v", err)
	}

	_, err = newSubStoreEngine("").convertIsolated(subStoreConversionRequest{
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
	result, err := newTestEmbeddedSubStoreEngine().convertIsolated(subStoreConversionRequest{
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
	result, err := newTestEmbeddedSubStoreEngine().convertIsolated(subStoreConversionRequest{
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
	result, err := newTestEmbeddedSubStoreEngine().convertIsolated(subStoreConversionRequest{
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
	result, err := newSubStoreEngine(string(core)).convertIsolated(subStoreConversionRequest{
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
// ui/src/client.ts. All of them are asked in one isolated call: each call is
// a fresh runtime, and fourteen would pay fourteen core loads for one
// question.
func TestEmbeddedSubStoreCoreProducesEveryCuratedTarget(t *testing.T) {
	targets := []string{
		"Clash", "ClashMeta", "sing-box", "Surge", "Loon", "Stash", "QX",
		"Shadowrocket", "Egern", "Surfboard", "SurgeMac", "URI", "V2Ray", "JSON",
	}
	encoded, err := json.Marshal(targets)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := newTestEmbeddedSubStoreEngine().runIsolatedScript("targets", "lattice-test-targets.js", `(function () {
  const root = globalThis.SubStoreProxyUtils;
  const core = root && root.ProxyUtils ? root.ProxyUtils : root;
  const proxies = core.parse("ss://YWVzLTEyOC1nY206c2VjcmV0@example.com:8388#Node");
  const out = {};
  for (const target of `+string(encoded)+`) {
    try {
      const document = core.produce(proxies, target, "external", {});
      out[target] = typeof document === "string" ? document.length : -1;
    } catch (err) {
      out[target] = -2;
    }
  }
  return JSON.stringify(out);
})()`)
	if err != nil {
		t.Fatal(err)
	}
	var sizes map[string]int
	if err := json.Unmarshal([]byte(raw), &sizes); err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		switch size := sizes[target]; {
		case size == -2:
			t.Errorf("target %s: produce threw", target)
		case size <= 0:
			t.Errorf("target %s produced no output (%d)", target, size)
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

	withOpts, err := engine.convertIsolated(subStoreConversionRequest{
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

	without, err := engine.convertIsolated(subStoreConversionRequest{Raw: "x", Target: "Stash"})
	if err != nil {
		t.Fatalf("convert without options: %v", err)
	}
	if !strings.Contains(without.Output, `"opts":{}`) {
		t.Fatalf("produce must receive an empty object when no options are set: %s", without.Output)
	}
}
