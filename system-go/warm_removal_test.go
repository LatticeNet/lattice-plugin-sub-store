package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
)

// The warm runtime's removal (S2 plan sections 1.4 and 7, lane 3): no
// persistent QuickJS runtime, no process-start boot, no engine service, and
// a core compiled to bytecode on the first call that needs it.

// warmRuntimeSymbols are the identifiers the warm runtime was built from. A
// source file that declares or names one of them has brought it back.
var warmRuntimeSymbols = []string{"bootWarm", "runWarm", "warmCtx", "prewarm", "markWarmDeadLocked", "servedWarm"}

// workerBinaryVar names a built plugin binary a test may read instead of
// building one, as the memory CI job's TestWorkerVmRSS does.
const workerBinaryVar = "LATTICE_WORKER_BINARY"

// TestNoWarmRuntimeSymbol is the acceptance test "the warm runtime no longer
// exists in the binary" (S2 plan section 8): go/parser over every Go file of
// the module, tests included, finds none of the warm runtime's identifiers,
// and the release-built binary's symbol table, read with go tool nm, carries
// none of them in package main.
func TestNoWarmRuntimeSymbol(t *testing.T) {
	banned := map[string]bool{}
	for _, name := range warmRuntimeSymbols {
		banned[name] = true
	}
	fset := token.NewFileSet()
	files := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		files++
		ast.Inspect(file, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok && banned[ident.Name] {
				t.Errorf("%s names the warm runtime's %s", fset.Position(ident.Pos()), ident.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 100 {
		t.Fatalf("parsed %d Go files; the walk did not cover the module", files)
	}

	symbols := mainPackageSymbols(t)
	if !slices.Contains(symbols, "main.(*subStoreEngine).runIsolatedScript") {
		t.Fatalf("the symbol table read %d main symbols and not the isolated path; nm did not read the plugin", len(symbols))
	}
	for _, symbol := range symbols {
		for _, part := range strings.Split(symbol, ".") {
			if banned[part] {
				t.Errorf("the binary carries %s", symbol)
			}
		}
	}
}

// mainPackageSymbols lists the package main symbols of the plugin binary as
// the release builds it (CGO off, -trimpath), or of the binary
// LATTICE_WORKER_BINARY names.
func mainPackageSymbols(t *testing.T) []string {
	t.Helper()
	gocmd, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command to build and read the binary with")
	}
	bin := os.Getenv(workerBinaryVar)
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "plugin")
		build := exec.Command(gocmd, "build", "-trimpath", "-buildvcs=false", "-o", bin, ".")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build the plugin: %v\n%s", err, out)
		}
	}
	out, err := exec.Command(gocmd, "tool", "nm", bin).Output()
	if err != nil {
		t.Fatalf("go tool nm %s: %v", bin, err)
	}
	var symbols []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if name := fields[len(fields)-1]; strings.HasPrefix(name, "main.") {
			symbols = append(symbols, name)
		}
	}
	return symbols
}

// TestEngineServiceIsGone pins decision 5 of the S2 plan (section 12): the
// uncalled engine service leaves with its seven methods and its pipeline
// store. The manifest declares no latticenet.sub-store/engine interface, the
// handler refuses every one of the seven, and no shipped file names the
// engine-pipelines-v1 key.
func TestEngineServiceIsGone(t *testing.T) {
	service := pluginID + "/engine"
	for _, iface := range loadManifestInterfaces(t) {
		if iface.Service == service {
			t.Fatalf("the manifest still declares %s with %d methods", service, len(iface.Methods))
		}
	}
	manifest, err := os.ReadFile("../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), `"`+service+`"`) {
		t.Fatalf("the manifest still names %s", service)
	}

	rt := &runtime{host: denyHostCalls{}, engine: testEngineWithHeadroom()}
	for _, method := range []string{"convert", "transform_response", "save_pipeline", "get_pipeline", "list_pipelines", "delete_pipeline", "run_pipeline"} {
		payload, err := json.Marshal(callPayload{Service: service, Method: method, Payload: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		res := rt.handle(request{Action: "call", Payload: payload})
		if res.OK || !strings.Contains(res.Error, "unsupported service") {
			t.Errorf("%s.%s: ok=%v error=%q, want the unsupported service refusal", service, method, res.OK, res.Error)
		}
	}
	if n := rt.engine.isolatedCount(); n != 0 {
		t.Fatalf("a refused engine call reached the bundle %d times", n)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "engine-pipelines-v1") {
			t.Errorf("%s names the engine-pipelines-v1 key", name)
		}
	}
}

// TestIsolatedPathSeedsFromLazyBytecode pins the replacement for the warm
// boot (S2 plan section 1.4): nothing compiles when an engine is made, the
// first call compiles the core to bytecode once, and every later call seeds
// its runtime from that bytecode rather than re-parsing the source. The
// embedded core's bytecode is shared by every engine in the process.
func TestIsolatedPathSeedsFromLazyBytecode(t *testing.T) {
	engine := newSubStoreEngine(statefulTestCore)
	engine.mu.Lock()
	compiled := len(engine.coreBytecode)
	engine.mu.Unlock()
	if compiled != 0 || engine.coreCompiles.Load() != 0 {
		t.Fatalf("a new engine holds %d bytes of bytecode after %d compiles; nothing may compile before a call", compiled, engine.coreCompiles.Load())
	}
	for i := 1; i <= 3; i++ {
		out, err := engine.convertIsolated(subStoreConversionRequest{Raw: "ss://x", Target: "Clash"})
		if err != nil || out.Output != "1" {
			t.Fatalf("call %d: output %q, err %v", i, out.Output, err)
		}
		if compiles, loads := engine.coreCompiles.Load(), engine.coreSourceLoads.Load(); compiles != 1 || loads != 0 {
			t.Fatalf("after call %d: %d compiles and %d source loads, want one compile and every runtime seeded from it", i, compiles, loads)
		}
	}
	engine.mu.Lock()
	compiled = len(engine.coreBytecode)
	engine.mu.Unlock()
	if compiled == 0 {
		t.Fatal("the engine kept no bytecode after its first call")
	}

	// The embedded core compiles once per process: once any engine holds its
	// bytecode, a new engine's first call compiles nothing.
	if len(newTestEmbeddedSubStoreEngine().ensureCoreBytecode()) == 0 {
		t.Fatal("the embedded core did not compile")
	}
	second := newTestEmbeddedSubStoreEngine()
	if out, err := second.convertIsolated(subStoreConversionRequest{Raw: sandboxTestURI, Target: "URI"}); err != nil || out.NodeCount != 1 {
		t.Fatalf("embedded convert: nodes=%d err=%v", out.NodeCount, err)
	}
	if compiles, loads := second.coreCompiles.Load(), second.coreSourceLoads.Load(); compiles != 0 || loads != 0 {
		t.Fatalf("a second embedded engine made %d compiles and %d source loads, want it seeded from the process's bytecode", compiles, loads)
	}

	// A core that does not compile caches nothing: the call loads the
	// source, fails on it, and the next call tries the compile again.
	broken := newSubStoreEngine("globalThis.SubStoreProxyUtils = {")
	for i := 1; i <= 2; i++ {
		if _, err := broken.convertIsolated(subStoreConversionRequest{Raw: "x", Target: "Clash"}); err == nil {
			t.Fatalf("call %d on a core that does not parse succeeded", i)
		}
	}
	if compiles, loads := broken.coreCompiles.Load(), broken.coreSourceLoads.Load(); compiles != 2 || loads != 2 {
		t.Fatalf("a core that does not compile: %d compiles and %d source loads, want a retry per call", compiles, loads)
	}
}

// BenchmarkIsolatedFirstCall is a worker's first bundle call: the core's
// compile to bytecode and the call itself. Each iteration gets an engine
// whose core text differs from the embedded one by a trailing newline, so
// the process-wide copy of the embedded bytecode does not answer it.
func BenchmarkIsolatedFirstCall(b *testing.B) {
	core := embeddedSubStoreCoreJS + "\n"
	req := subStoreConversionRequest{Raw: sandboxTestURI, Target: "URI"}
	for b.Loop() {
		engine := newSubStoreEngine(core)
		engine.limits = defaultSubStoreEngineLimits
		if _, err := engine.convertIsolated(req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkIsolatedSeededCall is every later bundle call: a fresh runtime
// seeded from the cached bytecode.
func BenchmarkIsolatedSeededCall(b *testing.B) {
	engine := newEmbeddedSubStoreEngine()
	if len(engine.ensureCoreBytecode()) == 0 {
		b.Fatal("the core did not compile")
	}
	req := subStoreConversionRequest{Raw: strings.Join(perfgen.URIs(64), "\n"), Target: "URI"}
	for b.Loop() {
		if _, err := engine.convertIsolated(req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMergeConfigIsolated is a config-file render's merge, which moved
// from the warm runtime to the isolated path in S2 (plan section 5): a small
// Mihomo template and 64 nodes, on a seeded engine.
func BenchmarkMergeConfigIsolated(b *testing.B) {
	engine := newEmbeddedSubStoreEngine()
	if len(engine.ensureCoreBytecode()) == 0 {
		b.Fatal("the core did not compile")
	}
	req := subStoreConfigMergeRequest{
		Template: "mixed-port: 7890\nmode: rule\nproxy-groups:\n  - name: auto\n    type: url-test\n    proxies: []\nrules:\n  - MATCH,auto\n",
		Raw:      strings.Join(perfgen.URIs(64), "\n"),
	}
	for b.Loop() {
		out, err := engine.mergeConfig(req)
		if err != nil {
			b.Fatal(err)
		}
		if out.NodeCount == 0 {
			b.Fatal("the merge injected no node")
		}
	}
}
