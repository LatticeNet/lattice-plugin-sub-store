package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
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
	if result.Target != "sing-box" || result.NodeCount != 2 || result.OutputBytes != len([]byte(result.Output)) {
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
