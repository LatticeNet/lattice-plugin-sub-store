package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The QuickJS wasm build this engine pins initialises the native modules
// qjs:std, qjs:os and qjs:bjson for every context, publishes the first two
// (and bjson) on globalThis, and mounts a host directory as the guest's "/".
// These tests ask, from every place JavaScript runs in this engine, whether
// any of that is reachable: the globals by name, the modules through dynamic
// import, a file under the guest root (read, list or write), and the process
// environment. They only observe; nothing here goes further than a typeof, a
// file open and an environment lookup.

const (
	sandboxSentinelName     = "lattice-sandbox-sentinel.txt"
	sandboxSentinelJSONName = "lattice-sandbox-sentinel.json"
	sandboxSentinelSecret   = "lattice-sandbox-sentinel-7f3a"
	sandboxStdWriteName     = "lattice-sandbox-written-std.txt"
	sandboxOSWriteName      = "lattice-sandbox-written-os.txt"
	sandboxEnvName          = "LATTICE_SUBSTORE_SANDBOX_PROBE"
)

// sandboxProbeJS defines latticeSandboxProbe, which reports what the code
// that calls it can reach. It is pasted verbatim into a call script, a user
// script operator and a test core, so the same questions are asked from each.
var sandboxProbeJS = strings.NewReplacer(
	"__SENTINEL__", mustJSONString(sandboxSentinelName),
	"__SENTINEL_JSON__", mustJSONString(sandboxSentinelJSONName),
	"__STD_WRITE__", mustJSONString(sandboxStdWriteName),
	"__OS_WRITE__", mustJSONString(sandboxOSWriteName),
	"__ENV__", mustJSONString(sandboxEnvName),
).Replace(`
async function latticeSandboxProbe() {
  const result = {
    types: { std: typeof std, os: typeof os, bjson: typeof bjson },
    reached: [],
    reads: [],
    writes: [],
    listing: [],
    env: []
  };
  const routes = [];
  if (globalThis.std && typeof globalThis.std === "object") routes.push(["global std", "std", globalThis.std]);
  if (globalThis.os && typeof globalThis.os === "object") routes.push(["global os", "os", globalThis.os]);
  for (const name of ["std", "os"]) {
    try {
      const mod = await import("qjs:" + name);
      if (mod) routes.push(["import qjs:" + name, name, mod]);
    } catch (err) {}
  }
  const sentinel = __SENTINEL__;
  const paths = ["/" + sentinel, sentinel, "./" + sentinel];
  for (const [route, kind, mod] of routes) {
    result.reached.push(route);
    if (kind === "std") {
      for (const path of paths) {
        try {
          const text = mod.loadFile(path);
          if (text !== null && text !== undefined) result.reads.push(route + " loadFile " + path + ": " + text);
        } catch (err) {}
        try {
          const file = mod.open(path, "r");
          if (file) {
            result.reads.push(route + " open " + path + ": " + file.readAsString());
            file.close();
          }
        } catch (err) {}
      }
      try {
        const file = mod.open("/" + __STD_WRITE__, "w");
        if (file) {
          file.puts("x");
          file.close();
          result.writes.push(route + " open w");
        }
      } catch (err) {}
      try {
        const value = mod.getenv(__ENV__);
        if (value !== null && value !== undefined) result.env.push(route + " getenv: " + value);
      } catch (err) {}
      try {
        const all = mod.getenviron();
        for (const key of Object.keys(all || {})) result.env.push(route + " environ: " + key);
      } catch (err) {}
    } else {
      try {
        const listed = mod.readdir("/");
        if (Array.isArray(listed) && listed[1] === 0 && Array.isArray(listed[0])) {
          for (const name of listed[0]) {
            if (name !== "." && name !== "..") result.listing.push(route + ": " + name);
          }
        }
      } catch (err) {}
      for (const path of paths) {
        try {
          const fd = mod.open(path, mod.O_RDONLY);
          if (typeof fd === "number" && fd >= 0) {
            const buffer = new ArrayBuffer(256);
            const n = mod.read(fd, buffer, 0, 256);
            mod.close(fd);
            result.reads.push(route + " open " + path + ": " + String.fromCharCode.apply(null, new Uint8Array(buffer, 0, Math.max(n, 0))));
          }
        } catch (err) {}
      }
      try {
        const fd = mod.open("/" + __OS_WRITE__, mod.O_WRONLY | mod.O_CREAT | mod.O_TRUNC, 0o644);
        if (typeof fd === "number" && fd >= 0) {
          mod.write(fd, new Uint8Array([120]).buffer, 0, 1);
          mod.close(fd);
          result.writes.push(route + " open w");
        }
      } catch (err) {}
    }
  }
  try {
    const mod = await import("/" + __SENTINEL_JSON__);
    result.reads.push("import json: " + JSON.stringify(mod && mod.default));
  } catch (err) {}
  return result;
}
`)

func mustJSONString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

type sandboxProbeResult struct {
	Types   map[string]string `json:"types"`
	Reached []string          `json:"reached"`
	Reads   []string          `json:"reads"`
	Writes  []string          `json:"writes"`
	Listing []string          `json:"listing"`
	Env     []string          `json:"env"`
}

// sandboxProbeHost prepares what the probe looks for: sentinel files in the
// directory the engine would mount as "/" (the process working directory
// when a runtime is created, unless the engine chooses otherwise) and an
// environment variable in the process. It returns that directory.
func sandboxProbeHost(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sandboxSentinelName), []byte(sandboxSentinelSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sandboxSentinelJSONName), []byte(`{"secret":"`+sandboxSentinelSecret+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv(sandboxEnvName, sandboxSentinelSecret)
	return dir
}

// assertSandboxSealed reports every way the probe got through, rather than
// stopping at the first, so one run records the whole surface.
func assertSandboxSealed(t *testing.T, where, hostDir string, result sandboxProbeResult) {
	t.Helper()
	t.Logf("%s: types=%v reached=%v", where, result.Types, result.Reached)
	for _, name := range []string{"std", "os", "bjson"} {
		if got := result.Types[name]; got != "undefined" {
			t.Errorf("%s: typeof %s = %q, want \"undefined\"", where, name, got)
		}
	}
	for _, read := range result.Reads {
		t.Errorf("%s: read a file under the guest root: %s", where, read)
	}
	for _, write := range result.Writes {
		t.Errorf("%s: wrote a file under the guest root: %s", where, write)
	}
	for _, name := range result.Listing {
		t.Errorf("%s: listed the guest root: %s", where, name)
	}
	for _, value := range result.Env {
		t.Errorf("%s: read the environment: %s", where, value)
	}
	for _, name := range []string{sandboxStdWriteName, sandboxOSWriteName} {
		path := filepath.Join(hostDir, name)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s: %s appeared on the host", where, name)
			_ = os.Remove(path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: stat %s: %v", where, name, err)
		}
	}
}

func decodeSandboxProbe(t *testing.T, where, raw string) sandboxProbeResult {
	t.Helper()
	var result sandboxProbeResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("%s: decode probe result: %v\n%s", where, err, raw)
	}
	if len(result.Types) != 3 {
		t.Fatalf("%s: the probe did not run: %s", where, raw)
	}
	return result
}

// The guest root is safe only while it names nothing on the host. If it ever
// resolved, every runtime would mount whatever is there.
func TestSandboxGuestRootDoesNotExist(t *testing.T) {
	if _, err := os.Stat(subStoreGuestRoot); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("guest root %s: stat err=%v, want it absent", subStoreGuestRoot, err)
	}
}

// The seal holds only while the guest root is missing on the host, so the
// engine checks it before every runtime and refuses a root that exists.
func TestSandboxRefusesAGuestRootThatExists(t *testing.T) {
	present := t.TempDir()
	if err := checkSubStoreGuestRootAbsent(present); err == nil {
		t.Fatalf("checkSubStoreGuestRootAbsent(%s) = nil for an existing directory, want a refusal", present)
	}
	file := filepath.Join(present, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkSubStoreGuestRootAbsent(file); err == nil {
		t.Fatalf("checkSubStoreGuestRootAbsent(%s) = nil for an existing file, want a refusal", file)
	}
	if err := checkSubStoreGuestRootAbsent(filepath.Join(present, "missing")); err != nil {
		t.Fatalf("checkSubStoreGuestRootAbsent(missing) = %v, want nil", err)
	}
}

var sandboxCallScript = `(async function () {
` + sandboxProbeJS + `
  return JSON.stringify(await latticeSandboxProbe());
})()`

// A call script is what the engine itself evaluates on top of the loaded
// core. On the warm runtime that is every scriptless conversion; this probe
// stands in for one.
func TestSandboxCallScriptOnWarmRuntimeReachesNoHostSurface(t *testing.T) {
	hostDir := sandboxProbeHost(t)
	engine := newTestEmbeddedSubStoreEngine()
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	showSubStoreEngineErrors(t)
	raw, err, served := engine.runWarm("probe", "lattice-sandbox-probe.js", sandboxCallScript)
	if !served {
		t.Fatal("the probe did not run on the warm runtime")
	}
	if err != nil {
		t.Fatalf("warm probe: %v", err)
	}
	assertSandboxSealed(t, "warm call script", hostDir, decodeSandboxProbe(t, "warm call script", raw))
}

func TestSandboxCallScriptOnIsolatedRuntimeReachesNoHostSurface(t *testing.T) {
	hostDir := sandboxProbeHost(t)
	engine := newTestEmbeddedSubStoreEngine()
	showSubStoreEngineErrors(t)
	raw, err := engine.runIsolatedScript("probe", "lattice-sandbox-probe.js", sandboxCallScript)
	if err != nil {
		t.Fatalf("isolated probe: %v", err)
	}
	assertSandboxSealed(t, "isolated call script", hostDir, decodeSandboxProbe(t, "isolated call script", raw))
}

// A user script operator on the real embedded core: the place operator-
// supplied JavaScript actually runs.
func TestSandboxUserScriptOperatorReachesNoHostSurface(t *testing.T) {
	hostDir := sandboxProbeHost(t)
	engine := newTestEmbeddedSubStoreEngine()
	showSubStoreEngineErrors(t)
	content := sandboxProbeJS + `
async function operator(proxies) {
  const report = JSON.stringify(await latticeSandboxProbe());
  return proxies.map(function (proxy) { return Object.assign({}, proxy, { name: report }); });
}`
	operator, err := json.Marshal(map[string]any{
		"type": "Script Operator",
		"args": map[string]any{"content": content},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.convert(subStoreConversionRequest{
		Raw:       warmTestURI,
		Target:    "JSON",
		Operators: []json.RawMessage{operator},
	})
	if err != nil {
		t.Fatalf("script operator convert: %v", err)
	}
	if warm, isolated := engine.pathCounts(); warm != 0 || isolated != 1 {
		t.Fatalf("path counts warm=%d isolated=%d, want 0/1", warm, isolated)
	}
	var proxies []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(result.Output), &proxies); err != nil || len(proxies) != 1 {
		t.Fatalf("decode JSON output (%v): %s", err, result.Output)
	}
	assertSandboxSealed(t, "user script operator", hostDir, decodeSandboxProbe(t, "user script operator", proxies[0].Name))
}

// A Script Operator that reaches for the sealed qjs:std module fails inside
// the bundle, and the embedded core swallows an operator's error: the step is
// skipped, its nodes pass through unchanged and the conversion reports
// success. This pins that behaviour, and that the engine stays usable after
// it, until the native chain compiler (design 28 S1) can report a failed step
// instead of hiding it.
func TestSandboxScriptOperatorCallingLoadFileLeavesNodesAndEngineAlive(t *testing.T) {
	engine := newTestEmbeddedSubStoreEngine()
	showSubStoreEngineErrors(t)
	operator, err := json.Marshal(map[string]any{
		"type": "Script Operator",
		"args": map[string]any{"content": `function operator(proxies) {
  const hosts = std.loadFile("/etc/hosts");
  return proxies.map(function (proxy) { return Object.assign({}, proxy, { name: "read:" + String(hosts) }); });
}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.convert(subStoreConversionRequest{Raw: warmTestURI, Target: "JSON", Operators: []json.RawMessage{operator}})
	if err != nil {
		t.Fatalf("script operator convert: %v", err)
	}
	var proxies []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(result.Output), &proxies); err != nil || len(proxies) != 1 {
		t.Fatalf("decode JSON output (%v): %s", err, result.Output)
	}
	if strings.HasPrefix(proxies[0].Name, "read:") {
		t.Fatalf("the operator ran past std.loadFile and renamed the node to %q", proxies[0].Name)
	}
	after, err := engine.convert(subStoreConversionRequest{Raw: warmTestURI, Target: "JSON"})
	if err != nil || after.NodeCount != 1 {
		t.Fatalf("scriptless convert after the failed operator: %+v, %v", after, err)
	}
}

// sandboxBundleCore is a stand-in core. Its top level runs where the real
// bundle's top level runs, so what it sees at load is what the bundle sees;
// its process step runs the full probe from inside the bundle's own closure.
var sandboxBundleCore = `
(function () {
` + sandboxProbeJS + `
  const atLoad = { std: typeof std, os: typeof os, bjson: typeof bjson };
  let probed = null;
  globalThis.SubStoreProxyUtils = {
    parse() { return [{ name: "n", type: "ss" }]; },
    async process(proxies) {
      probed = await latticeSandboxProbe();
      return proxies;
    },
    produce() { return JSON.stringify({ atLoad: atLoad, probe: probed }); }
  };
})();
`

func runSandboxBundleProbe(t *testing.T, where, hostDir string, engine *subStoreEngine, operator string, wantWarm, wantIsolated int) {
	t.Helper()
	result, err := engine.convert(subStoreConversionRequest{
		Raw:       "x",
		Target:    "JSON",
		Operators: []json.RawMessage{json.RawMessage(operator)},
	})
	if err != nil {
		t.Fatalf("%s: convert: %v", where, err)
	}
	if warm, isolated := engine.pathCounts(); warm != wantWarm || isolated != wantIsolated {
		t.Fatalf("%s: path counts warm=%d isolated=%d, want %d/%d", where, warm, isolated, wantWarm, wantIsolated)
	}
	var out struct {
		AtLoad map[string]string `json:"atLoad"`
		Probe  json.RawMessage   `json:"probe"`
	}
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("%s: decode output: %v\n%s", where, err, result.Output)
	}
	for _, name := range []string{"std", "os", "bjson"} {
		if got := out.AtLoad[name]; got != "undefined" {
			t.Errorf("%s: at bundle load typeof %s = %q, want \"undefined\"", where, name, got)
		}
	}
	assertSandboxSealed(t, where, hostDir, decodeSandboxProbe(t, where, string(out.Probe)))
}

func TestSandboxBundleContextOnWarmRuntimeReachesNoHostSurface(t *testing.T) {
	hostDir := sandboxProbeHost(t)
	engine := newSubStoreEngine(sandboxBundleCore)
	engine.limits.Timeout = 30 * time.Second
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	showSubStoreEngineErrors(t)
	runSandboxBundleProbe(t, "warm bundle", hostDir, engine, `{"type":"Sort Operator","args":{}}`, 1, 0)
}

func TestSandboxBundleContextOnIsolatedRuntimeReachesNoHostSurface(t *testing.T) {
	hostDir := sandboxProbeHost(t)
	engine := newSubStoreEngine(sandboxBundleCore)
	engine.limits.Timeout = 30 * time.Second
	showSubStoreEngineErrors(t)
	runSandboxBundleProbe(t, "isolated bundle", hostDir, engine, `{"type":"Script Operator","args":{"content":"function operator(p){ return p; }"}}`, 0, 1)
}

// redosNodeURI names its node so that ^(a+)+$ backtracks catastrophically:
// forty a's and a trailing character the pattern cannot match.
var redosNodeURI = "ss://YWVzLTEyOC1nY206cGFzcw==@a.example:8388#" + strings.Repeat("a", 40) + "!"

// A provider controls node names and an operator's regex runs over them on
// the scriptless path, which is the warm runtime. A catastrophic pattern must
// cost one call its budget, not the worker: the call fails near the deadline
// and the engine answers the next call.
func TestWarmEngineBoundsCatastrophicRegexOnScriptlessPath(t *testing.T) {
	engine := newTestEmbeddedSubStoreEngine()
	engine.limits.Timeout = 2 * time.Second
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	filter := json.RawMessage(`{"type":"Regex Filter","args":{"regex":["^(a+)+$"],"keep":true}}`)
	start := time.Now()
	_, err := engine.convert(subStoreConversionRequest{Raw: redosNodeURI, Target: "URI", Operators: []json.RawMessage{filter}})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a catastrophic regex completed; the test input no longer backtracks")
	}
	if warm, isolated := engine.pathCounts(); warm != 1 || isolated != 0 {
		t.Fatalf("path counts warm=%d isolated=%d, want the regex call on the warm runtime", warm, isolated)
	}
	if elapsed > engine.limits.Timeout+3*time.Second {
		t.Fatalf("the warm call ran %s against a %s budget", elapsed, engine.limits.Timeout)
	}
	// The warm runtime is retired, so the next call boots an isolated one and
	// loads the full core; under -race on a CI runner that alone outlasts the
	// 2 s test budget, which belongs to the call under test.
	engine.limits.Timeout = 30 * time.Second
	after, err := engine.convert(subStoreConversionRequest{Raw: warmTestURI, Target: "URI"})
	if err != nil || after.NodeCount != 1 {
		t.Fatalf("engine did not answer after the bounded call: nodes=%d err=%v", after.NodeCount, err)
	}
}

// largeFlowYAML is an ordinary Clash document, n proxies in flow style. The
// pinned core parses it at roughly 8 ms a node on this engine (250 nodes in
// about 2 s, 2000 in about 17 s, measured 2026-10-08), so a few thousand
// nodes from a provider outlast any budget the scriptless path has.
func largeFlowYAML(n int) string {
	var b strings.Builder
	b.WriteString("proxies:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "  - {name: node-%06d, type: ss, server: s%d.example.com, port: 8388, cipher: aes-128-gcm, password: p%d}\n", i, i, i)
	}
	return b.String()
}

// The same bound holds for a document too large to parse in time: the call
// ends near the deadline, and the engine keeps serving.
func TestWarmEngineBoundsHugeYAMLOnScriptlessPath(t *testing.T) {
	engine := newTestEmbeddedSubStoreEngine()
	engine.limits.Timeout = 2 * time.Second
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	start := time.Now()
	_, err := engine.countNodes(largeFlowYAML(5000))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("5000 flow-style nodes parsed inside the budget; the test input no longer outlasts it")
	}
	if warm, isolated := engine.pathCounts(); warm != 1 || isolated != 0 {
		t.Fatalf("path counts warm=%d isolated=%d, want the call on the warm runtime", warm, isolated)
	}
	if elapsed > engine.limits.Timeout+3*time.Second {
		t.Fatalf("the warm call ran %s against a %s budget", elapsed, engine.limits.Timeout)
	}
	// The warm runtime is retired, so the next call boots an isolated one and
	// loads the full core; under -race on a CI runner that alone outlasts the
	// 2 s test budget, which belongs to the call under test.
	engine.limits.Timeout = 30 * time.Second
	after, err := engine.convert(subStoreConversionRequest{Raw: warmTestURI, Target: "URI"})
	if err != nil || after.NodeCount != 1 {
		t.Fatalf("engine did not answer after the bounded call: nodes=%d err=%v", after.NodeCount, err)
	}
}
