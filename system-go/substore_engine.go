package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/fastschema/qjs"
)

const (
	subStoreQuickJSMemoryLimit = 128 << 20
	subStoreQuickJSStackLimit  = 8 << 20
	subStoreQuickJSGCThreshold = 32 << 20
)

// Timeout bounds one script's wall clock inside the engine. It went from 10s
// to 25s when scripts gained a network: a chain that resolves domains over
// DoH or downloads a ruleset spends most of its budget waiting on someone
// else's server, and 10s made an ordinary fetch-and-filter script fail on a
// slow provider rather than on its own logic. The declared per-method
// invocation timeout (30s for the script-capable methods) still bounds the
// whole call, and the request budget bounds how many waits a script can
// stack up.
var defaultSubStoreEngineLimits = subStoreEngineLimits{
	Timeout:      25 * time.Second,
	MemoryLimit:  subStoreQuickJSMemoryLimit,
	MaxStackSize: subStoreQuickJSStackLimit,
	GCThreshold:  subStoreQuickJSGCThreshold,
}

//go:embed lib/substore-core.js
var embeddedSubStoreCoreJS string

// The embedded core's bytecode is identical for every engine in the process,
// so it is compiled once and shared; a failed compile leaves the slot empty
// so a later call can retry, rather than disabling seeding for the process's
// lifetime. Engines carrying a custom core (tests) compile their own.
var (
	embeddedCoreBytecodeMu sync.Mutex
	embeddedCoreBytecode   []byte
)

// subStoreEngine owns the embedded Sub-Store core and the one way to run it:
// a fresh QuickJS runtime per call, discarded with the call
// (runIsolatedScript). From S2 every node chain the Go operators can run, and
// every target with a Go producer, never reaches it (engine_dispatch.go); what
// remains is the whole-chain fallback (script steps, Resolve Domain, the Tier
// 2 targets), response transformers, script files and config merges, each an
// isolate per call.
//
// Nothing boots at process start. The first call compiles the core to
// bytecode once on a throwaway runtime (ensureCoreBytecode) and every later
// runtime is seeded from that bytecode, which the original measurement put at
// about 4x faster than re-parsing the 1.27 MB source. The persistent warm
// runtime S1 kept for scriptless chains is gone (S2 plan section 1.4): it held
// about 44 MiB per idle worker for chains the native dispatcher now answers in
// Go.
//
// Every runtime is bounded in process by the one mechanism that measurably
// interrupts QuickJS-on-wazero: it is context-bound (CloseOnContextDone), and
// cancelling the context closes the module mid-call. MaxExecutionTime does
// nothing in the pinned qjs build (no interrupt handler is installed), and an
// asynchronous Close does not stop a hot loop. The isolated path cancels on a
// per-call deadline, so a catastrophic regex over provider node names, or a
// provider document too large to parse in time, costs one call its budget and
// not the worker (TestEngineBoundsCatastrophicRegexPerCall,
// TestEngineBoundsHugeYAMLPerCall). The library panic a closed module raises
// is recovered and redacted.
//
// Every runtime is also sealed before anything is evaluated in it
// (newSubStoreQuickJSRuntime): no filesystem, no native-module globals.
type subStoreEngine struct {
	coreJS string
	limits subStoreEngineLimits

	// mu guards coreBytecode. A v2 worker handles one invocation at a time,
	// so it is uncontended.
	mu           sync.Mutex
	coreBytecode []byte
	// Counters, for tests and diagnostics: the calls the isolated path
	// served, the compiles of the core to bytecode, and the runtimes loaded
	// from the core's source because no bytecode was at hand.
	isolatedServes  atomic.Int64
	coreCompiles    atomic.Int64
	coreSourceLoads atomic.Int64

	// scriptHTTP is the network arm for the invocation currently in flight,
	// or nil when none is. A v2 worker handles one invocation at a time, so
	// this is a single slot rather than a map; it is a pointer swap so a
	// script's host function can read it without locking.
	scriptHTTP atomic.Pointer[scriptHTTPGateway]
}

// attachScriptHTTP gives the engine's JavaScript the invocation's network for
// as long as the returned release function has not been called. Detaching is
// what makes an expired host lease unreachable: a script that outlives its
// invocation finds no network rather than a stale client.
func (engine *subStoreEngine) attachScriptHTTP(gateway *scriptHTTPGateway) func() {
	if engine == nil {
		return func() {}
	}
	engine.scriptHTTP.Store(gateway)
	return func() { engine.scriptHTTP.Store(nil) }
}

// withholdScriptHTTP replaces the invocation's script network with a
// refusal that sends nothing and counts every request, until the returned
// restore puts back what was attached before (S2-SR-2). A refusal already in
// place is kept, so a nested caller counts on the same one and its restore
// does nothing.
func (engine *subStoreEngine) withholdScriptHTTP() (*scriptHTTPGateway, func()) {
	if current := engine.scriptHTTP.Load(); current != nil && current.withheld {
		return current, func() {}
	}
	refusal := &scriptHTTPGateway{withheld: true}
	previous := engine.scriptHTTP.Swap(refusal)
	return refusal, func() { engine.scriptHTTP.CompareAndSwap(refusal, previous) }
}

// installScriptHTTPBinding exposes the single host function the environment
// shim calls. It is the only I/O primitive any JavaScript in this process can
// reach, and it holds no client of its own: it borrows the invocation's.
func (engine *subStoreEngine) installScriptHTTPBinding(qctx *qjs.Context) {
	qctx.SetFunc("__lattice_host_http", func(this *qjs.This) (*qjs.Value, error) {
		args := this.Args()
		if len(args) == 0 {
			return nil, fmt.Errorf("script http: no request")
		}
		gateway := engine.scriptHTTP.Load()
		if gateway == nil {
			return nil, fmt.Errorf("script http: the network is not available for this call")
		}
		answer, err := gateway.do(args[0].String())
		if err != nil {
			return nil, err
		}
		return this.Context().NewString(answer), nil
	})
}

type subStoreEngineLimits struct {
	Timeout      time.Duration
	MemoryLimit  int
	MaxStackSize int
	GCThreshold  int
}

type subStoreConversionRequest struct {
	Raw string `json:"raw"`
	// RawParts, when set, replaces Raw as the node source: each part is parsed
	// on its own and the node lists are concatenated, which is how a
	// combination merges members that arrive in different encodings. Joining
	// them as text first lost whole members, because the engine recognises a
	// base64 list or a YAML document only when it is the entire input.
	RawParts []string `json:"raw_parts,omitempty"`
	// Nodes, when set, replaces Raw and RawParts: pre-parsed node-model
	// objects handed straight to the producer (produceNodes).
	Nodes     []json.RawMessage `json:"nodes,omitempty"`
	Target    string            `json:"target"`
	Operators []json.RawMessage `json:"operators,omitempty"`
	// Options is the produce() opts object — Sub-Store's own flag names, e.g.
	// "include-unsupported-proxy". Bounded to booleans so a caller cannot
	// smuggle structures into the engine script; unknown names are simply
	// ignored by the core, exactly as upstream ignores them.
	Options map[string]bool `json:"options,omitempty"`
	// Explain asks the engine to also work out which nodes this target dropped.
	// The console sets it so a near-empty document can say why; the path that
	// serves subscriptions to clients does not, and pays nothing for it.
	Explain bool `json:"explain,omitempty"`
	// CarrierCheck asks the engine to report the nodes the chain kept that a
	// URI output could not write. A combination member with its own steps is
	// handed on as URI links, and the URI producer has no form for several
	// protocols (HTTP, Snell, SSH and others), so without this those nodes
	// would leave the member without a word. Only meaningful with target URI.
	CarrierCheck bool `json:"carrier_check,omitempty"`
}

type subStoreResponseTransformRequest struct {
	Response  json.RawMessage   `json:"response"`
	Target    string            `json:"target,omitempty"`
	Operators []json.RawMessage `json:"operators,omitempty"`
}

type subStoreConversionResult struct {
	Target          string `json:"target"`
	SourceNodeCount int    `json:"source_node_count"`
	NodeCount       int    `json:"node_count"`
	Output          string `json:"output"`
	OutputBytes     int    `json:"output_bytes"`
	// UnsupportedNodeCount and UnsupportedProtocols describe nodes the target
	// client cannot carry, which its producer drops on the way to the document.
	// Without them a record made entirely of VLESS and Hysteria2 renders for
	// Clash as the nine bytes "proxies:" and nothing explains why.
	//
	// Only filled when the request asked to explain. Each producer owns its own
	// support rules, and they are not declared anywhere a caller can read, so
	// the only honest way to learn them is to ask the producer: a node the
	// producer drops leaves the document exactly as an empty list would. That
	// costs extra produce calls, which the serve path has no reason to pay.
	UnsupportedNodeCount int      `json:"unsupported_node_count"`
	UnsupportedProtocols []string `json:"unsupported_protocols,omitempty"`
	// ZeroNodes is true when the document carries no node for this client:
	// the chain left nothing, or the producer's output equals what it writes
	// for an empty list. Producers emit a non-empty skeleton for zero nodes
	// ("proxies:\n", an empty sing-box object), so an empty-body check alone
	// cannot see this, and a client that receives such a document deletes
	// every node it had. Computed on every call; the producer's empty document
	// is produced only when the output is short enough to be it
	// (subStoreMaxEmptyDocumentBytes).
	ZeroNodes bool `json:"zero_nodes"`
	// CarrierLostNodeCount and CarrierLostProtocols are the nodes the chain
	// kept that the URI output does not carry, when the request asked for a
	// carrier check. Nodes the core rejects for every client (a VLESS Reality
	// node without a public key, for one) are not counted: no client would
	// have received them anyway.
	CarrierLostNodeCount int      `json:"carrier_lost_node_count"`
	CarrierLostProtocols []string `json:"carrier_lost_protocols,omitempty"`
	// nodes are the native path's nodes after the chain, so a collection
	// member that ran natively reaches the collection without a URI round
	// trip. Never set by the bundle and never encoded.
	nodes []*nodemodel.Node
}

type subStoreResponseTransformResult struct {
	Target    string         `json:"target,omitempty"`
	Status    int            `json:"status"`
	Headers   map[string]any `json:"headers"`
	Body      string         `json:"body"`
	BodyBytes int            `json:"body_bytes"`
}

type subStoreCoreConversionResult struct {
	SourceNodeCount      int      `json:"source_node_count"`
	NodeCount            int      `json:"node_count"`
	UnsupportedNodeCount int      `json:"unsupported_node_count"`
	UnsupportedProtocols []string `json:"unsupported_protocols"`
	ZeroNodes            bool     `json:"zero_nodes"`
	CarrierLostNodeCount int      `json:"carrier_lost_node_count"`
	CarrierLostProtocols []string `json:"carrier_lost_protocols"`
	Output               string   `json:"output"`
}

type subStoreCoreResponseTransformResult struct {
	Status  int            `json:"status"`
	Headers map[string]any `json:"headers"`
	Body    string         `json:"body"`
}

func newSubStoreEngine(coreJS string) *subStoreEngine {
	return &subStoreEngine{coreJS: coreJS, limits: defaultSubStoreEngineLimits}
}

func newEmbeddedSubStoreEngine() *subStoreEngine {
	return newSubStoreEngine(embeddedSubStoreCoreJS)
}

// convertIsolated runs one conversion on the bundle's isolated path: the
// whole-chain fallback of the native dispatcher (engine_dispatch.go).
func (engine *subStoreEngine) convertIsolated(req subStoreConversionRequest) (result subStoreConversionResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = redactSubStoreEnginePanic(recovered)
		}
	}()

	if strings.TrimSpace(engine.coreJS) == "" {
		return subStoreConversionResult{}, fmt.Errorf("Sub-Store core bundle is empty")
	}
	if strings.TrimSpace(req.Target) == "" {
		return subStoreConversionResult{}, fmt.Errorf("target is required")
	}

	script, err := subStoreConversionScript(req)
	if err != nil {
		return subStoreConversionResult{}, err
	}
	rawResult, err := engine.runIsolatedScript("convert", "lattice-substore-convert.js", script)
	if err != nil {
		return subStoreConversionResult{}, err
	}
	var coreResult subStoreCoreConversionResult
	if err := json.Unmarshal([]byte(rawResult), &coreResult); err != nil {
		return subStoreConversionResult{}, fmt.Errorf("decode Sub-Store conversion result: %w", err)
	}
	return subStoreConversionResult{
		Target:          req.Target,
		SourceNodeCount: coreResult.SourceNodeCount,
		NodeCount:       coreResult.NodeCount,
		Output:          coreResult.Output,
		OutputBytes:     len([]byte(coreResult.Output)),

		UnsupportedNodeCount: coreResult.UnsupportedNodeCount,
		UnsupportedProtocols: coreResult.UnsupportedProtocols,
		ZeroNodes:            coreResult.ZeroNodes,
		CarrierLostNodeCount: coreResult.CarrierLostNodeCount,
		CarrierLostProtocols: coreResult.CarrierLostProtocols,
	}, nil
}

// produceNodes hands pre-parsed node-model objects to the bundle's producer
// on the isolated path, with no chain. It exists for convert with nodes,
// which answers every allowed target while the five Tier 2 targets have no
// Go producer. It runs the conversion script with its parse step replaced,
// so the zero-node rule is the one every other conversion applies.
func (engine *subStoreEngine) produceNodes(nodes []json.RawMessage, target string, options map[string]bool) (subStoreConversionResult, error) {
	if nodes == nil {
		nodes = []json.RawMessage{}
	}
	return engine.convertIsolated(subStoreConversionRequest{Nodes: nodes, Target: target, Options: options})
}

func (engine *subStoreEngine) transformResponse(req subStoreResponseTransformRequest) (result subStoreResponseTransformResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = redactSubStoreEnginePanic(recovered)
		}
	}()

	script, err := subStoreResponseTransformScript(req)
	if err != nil {
		return subStoreResponseTransformResult{}, err
	}
	rawResult, err := engine.runIsolatedScript("process response", "lattice-substore-process-response.js", script)
	if err != nil {
		return subStoreResponseTransformResult{}, err
	}
	var coreResult subStoreCoreResponseTransformResult
	if err := json.Unmarshal([]byte(rawResult), &coreResult); err != nil {
		return subStoreResponseTransformResult{}, fmt.Errorf("decode Sub-Store response transform result: %w", err)
	}
	if coreResult.Headers == nil {
		coreResult.Headers = map[string]any{}
	}
	return subStoreResponseTransformResult{
		Target:    req.Target,
		Status:    coreResult.Status,
		Headers:   coreResult.Headers,
		Body:      coreResult.Body,
		BodyBytes: len([]byte(coreResult.Body)),
	}, nil
}

// subStoreGuestRoot is the host path every runtime mounts as the guest's "/".
//
// The pinned qjs always mounts one directory, read-write, at the guest root
// (Option.CWD, defaulting to the process working directory) and offers no way
// to mount nothing or to mount read-only. In a worker that default is the
// generation directory holding the plugin's own artifact, and quickjs-libc's
// std and os modules gave any script open, read, write and readdir on it; it
// was also a store that outlived the call.
//
// This path does not exist and nothing can create it. wazero opens a mounted
// root lazily and, while that open fails with ENOENT, answers every guest
// path operation with EBADF before touching the host, so the guest cannot
// make its own root; the host cannot either, because procfs refuses new
// entries at its top level (and on macOS, where tests run, /proc is absent
// from a read-only system volume). A root that fails any other way (ENOTDIR
// below a device file, EACCES on a locked directory) makes wazero panic
// inside the guest's call instead, which is why the path is a missing entry
// rather than an unusable one. Nothing the engine runs reads a file: the core
// and every call script arrive as strings or bytecode.
const subStoreGuestRoot = "/proc/lattice-substore-no-filesystem"

// subStoreSandboxSealShim removes the native-module globals the qjs build
// installs at context creation (qjs:std, qjs:os and qjs:bjson as globalThis.std,
// os and bjson). Neither the core nor any call script uses them; the timer
// functions the same init copies from os (setTimeout and friends) stay, since
// the core schedules with setTimeout. The modules themselves remain
// registered, so a dynamic import can still name them, but with no guest
// filesystem and an empty WASI environment (qjs passes none) they reach no
// file and no variable. Deleting must leave each name undefined, or the
// runtime is refused rather than run unsealed.
const subStoreSandboxSealShim = `
(function () {
  for (const name of ["std", "os", "bjson"]) {
    delete globalThis[name];
    if (typeof globalThis[name] !== "undefined") {
      throw new Error("cannot remove the " + name + " global");
    }
  }
})();
`

// newSubStoreQuickJSRuntime creates every QuickJS runtime the engine uses,
// for a call or for the core's compile, and seals it before the caller evaluates anything in it:
// the guest root is subStoreGuestRoot and the native-module globals are gone,
// so neither the core's top level nor any call or user script ever sees them.
// The runtime is bound to ctx; cancelling it closes the module mid-call.
func newSubStoreQuickJSRuntime(ctx context.Context, limits subStoreEngineLimits) (*qjs.Runtime, error) {
	if err := checkSubStoreGuestRootAbsent(subStoreGuestRoot); err != nil {
		return nil, err
	}
	rt, err := qjs.New(qjs.Option{
		CWD:                subStoreGuestRoot,
		Context:            ctx,
		CloseOnContextDone: true,
		MemoryLimit:        limits.MemoryLimit,
		MaxStackSize:       limits.MaxStackSize,
		GCThreshold:        limits.GCThreshold,
		Stdout:             io.Discard,
		Stderr:             io.Discard,
	})
	if err != nil {
		return nil, err
	}
	if err := evalQuickJSStep(rt.Context(), "lattice-sandbox-seal.js", subStoreSandboxSealShim); err != nil {
		closeQuickJSRuntime(rt)
		return nil, fmt.Errorf("seal Sub-Store JS runtime: %w", err)
	}
	return rt, nil
}

// checkSubStoreGuestRootAbsent refuses to build a runtime when the guest root
// exists on the host. The seal rests on that path being a missing entry; a
// host where something did create it (procfs not mounted, a bind mount) would
// hand every script a real directory again, so the engine fails closed
// instead of running unsealed.
func checkSubStoreGuestRootAbsent(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("Sub-Store JS sandbox root %s exists on this host; refusing to run scripts", path)
	}
	return fmt.Errorf("Sub-Store JS sandbox root %s cannot be checked: %w", path, err)
}

// closeQuickJSRuntime closes a runtime whose module may already be broken.
// qjs panics on close failures by design, and a cleanup panic or hang must
// stay confined here rather than kill the worker.
func closeQuickJSRuntime(rt *qjs.Runtime) {
	defer func() { _ = recover() }()
	rt.Close()
}

// runIsolatedScript executes on a fresh runtime that dies with the call. It
// is the only place any JavaScript runs. Its deadline is the call's context:
// cancelling the context-bound runtime closes the module mid-call. The
// core's one-time compile happens before that deadline starts, on a runtime
// of its own, so the first call of a worker is not judged on it.
func (engine *subStoreEngine) runIsolatedScript(stage, file, script string) (string, error) {
	if strings.TrimSpace(engine.coreJS) == "" {
		return "", fmt.Errorf("Sub-Store core bundle is empty")
	}

	engine.isolatedServes.Add(1)
	bytecode := engine.ensureCoreBytecode()

	limits := engine.limits.withDefaults()
	ctx, cancel := context.WithTimeout(context.Background(), limits.Timeout)
	defer cancel()

	rt, err := newSubStoreQuickJSRuntime(ctx, limits)
	if err != nil {
		return "", fmt.Errorf("create Sub-Store JS runtime: %w", err)
	}
	defer rt.Close()

	qctx := rt.Context()
	if err := evalQuickJSStep(qctx, "lattice-console-shim.js", subStoreConsoleShim); err != nil {
		return "", fmt.Errorf("install Sub-Store console shim: %w", err)
	}
	engine.installScriptHTTPBinding(qctx)
	if err := evalQuickJSStep(qctx, "lattice-script-env.js", subStoreScriptEnvShim); err != nil {
		return "", fmt.Errorf("install Sub-Store script environment: %w", err)
	}
	if err := engine.loadCoreInto(qctx, bytecode); err != nil {
		return "", err
	}
	value, err := qctx.Eval(file, qjs.Code(script))
	if err != nil {
		return "", redactSubStoreJSError(stage, err)
	}
	return finishQuickJSValue(stage, value)
}

// ensureCoreBytecode returns the core's bytecode, compiling it on the first
// call that needs it and caching it under mu; nothing compiles at process
// start, so an idle worker holds no runtime at all. The embedded core's copy
// is shared process-wide. A failed compile caches nothing, so the call loads
// the source and a later call tries the compile again.
func (engine *subStoreEngine) ensureCoreBytecode() []byte {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.coreBytecode) > 0 {
		return engine.coreBytecode
	}
	if engine.coreJS == embeddedSubStoreCoreJS {
		embeddedCoreBytecodeMu.Lock()
		if len(embeddedCoreBytecode) == 0 {
			embeddedCoreBytecode = engine.compileCore()
		}
		engine.coreBytecode = embeddedCoreBytecode
		embeddedCoreBytecodeMu.Unlock()
	} else {
		engine.coreBytecode = engine.compileCore()
	}
	return engine.coreBytecode
}

// compileCore compiles the core to bytecode on a throwaway runtime, sealed
// like every other and bounded by the engine's timeout, and closes it: the
// bytecode is a copy, independent of the runtime that produced it. Compiling
// evaluates nothing, so no shim is installed. It answers nil on any failure,
// a library panic included.
func (engine *subStoreEngine) compileCore() (bytecode []byte) {
	defer func() {
		if recovered := recover(); recovered != nil {
			bytecode = nil
		}
	}()
	engine.coreCompiles.Add(1)
	limits := engine.limits.withDefaults()
	ctx, cancel := context.WithTimeout(context.Background(), limits.Timeout)
	defer cancel()
	rt, err := newSubStoreQuickJSRuntime(ctx, limits)
	if err != nil {
		return nil
	}
	defer closeQuickJSRuntime(rt)
	compiled, err := rt.Context().Compile("substore-core.js", qjs.Code(engine.coreJS))
	if err != nil {
		return nil
	}
	return compiled
}

// loadCoreInto seeds a fresh context with the core, from bytecode when the
// compile produced it and from source otherwise.
func (engine *subStoreEngine) loadCoreInto(qctx *qjs.Context, bytecode []byte) error {
	if len(bytecode) > 0 {
		value, err := qctx.Eval("substore-core.js", qjs.Bytecode(bytecode))
		if value != nil {
			value.Free()
		}
		if err == nil {
			return nil
		}
		// Bytecode from a healthy compile should never fail to load; if it
		// does, the source is still the truth.
	}
	engine.coreSourceLoads.Add(1)
	if err := evalQuickJSStep(qctx, "substore-core.js", engine.coreJS); err != nil {
		return fmt.Errorf("load Sub-Store core: %w", err)
	}
	return nil
}

// finishQuickJSValue settles a completion value into the stringified-JSON
// contract every engine call shares.
func finishQuickJSValue(stage string, value *qjs.Value) (string, error) {
	var err error
	if value.IsPromise() {
		promise := value
		value, err = promise.Await()
		promise.Free()
		if err != nil {
			return "", redactSubStoreJSError(stage, err)
		}
	}
	defer value.Free()
	if !value.IsString() {
		return "", fmt.Errorf("Sub-Store %s returned %s, want stringified JSON", stage, value.Type())
	}
	return value.String(), nil
}

func (limits subStoreEngineLimits) withDefaults() subStoreEngineLimits {
	defaults := defaultSubStoreEngineLimits
	if limits.Timeout <= 0 {
		limits.Timeout = defaults.Timeout
	}
	if limits.MemoryLimit <= 0 {
		limits.MemoryLimit = defaults.MemoryLimit
	}
	if limits.MaxStackSize <= 0 {
		limits.MaxStackSize = defaults.MaxStackSize
	}
	if limits.GCThreshold <= 0 {
		limits.GCThreshold = defaults.GCThreshold
	}
	return limits
}

func evalQuickJSStep(ctx *qjs.Context, file, code string) error {
	value, err := ctx.Eval(file, qjs.Code(code))
	if value != nil {
		value.Free()
	}
	return err
}

// subStoreMaxEmptyDocumentBytes bounds what a producer writes for an empty
// node list. The conversion script compares an output with the producer's
// empty document only when the output is no longer than this, so the common
// serve path does not pay for a second produce. A pin that grew an empty
// document past it would let a zero-node document through, which is why
// TestEmptyDocumentsFitTheZeroNodeBound checks every target against it.
const subStoreMaxEmptyDocumentBytes = 4096

func subStoreConversionScript(req subStoreConversionRequest) (string, error) {
	rawText := req.Raw
	if strings.TrimSpace(rawText) == "" && len(req.RawParts) > 0 {
		// An operator chain still receives the source text it always did (a
		// script operator may read it); parsing below uses the parts.
		rawText = strings.Join(req.RawParts, "\n")
	}
	raw, err := json.Marshal(rawText)
	if err != nil {
		return "", fmt.Errorf("encode raw subscription: %w", err)
	}
	parts := req.RawParts
	if parts == nil {
		parts = []string{}
	}
	rawParts, err := json.Marshal(parts)
	if err != nil {
		return "", fmt.Errorf("encode raw subscription parts: %w", err)
	}
	// null unless the request hands over parsed nodes; json.Marshal of a nil
	// slice is null.
	nodes, err := json.Marshal(req.Nodes)
	if err != nil {
		return "", fmt.Errorf("encode nodes: %w", err)
	}
	target, err := json.Marshal(req.Target)
	if err != nil {
		return "", fmt.Errorf("encode target: %w", err)
	}
	operators, err := json.Marshal(req.Operators)
	if err != nil {
		return "", fmt.Errorf("encode operators: %w", err)
	}
	options, err := json.Marshal(req.Options)
	if err != nil {
		return "", fmt.Errorf("encode produce options: %w", err)
	}
	explain := "false"
	if req.Explain {
		explain = "true"
	}
	carrierCheck := "false"
	if req.CarrierCheck {
		carrierCheck = "true"
	}
	prefix := "(function() {"
	processBlock := ""
	if len(req.Operators) > 0 {
		prefix = "(async function() {"
		processBlock = `
  if (typeof core.process !== "function") {
    throw new Error("Sub-Store core must expose process(proxies, operators)");
  }
  proxies = await core.process(proxies, operators, target, undefined, undefined, raw);
  if (!Array.isArray(proxies)) {
    throw new Error("Sub-Store process(proxies, operators) must return an array");
  }`
	}
	return fmt.Sprintf(`%s
  const raw = %s;
  const rawParts = %s;
  const nodes = %s;
  const target = %s;
  const operators = %s || [];
  const produceOptions = %s || {};
  const explain = %s;
  const carrierCheck = %s;
  const root = globalThis.SubStoreProxyUtils;
  const core = root && root.ProxyUtils ? root.ProxyUtils : root;
  if (!core || typeof core.parse !== "function" || typeof core.produce !== "function") {
    throw new Error("Sub-Store core must expose parse(raw) and produce(proxies, target, env)");
  }
  // Each part is parsed on its own: the engine recognises a base64 list or a
  // YAML document only when it is the whole input, so text-joined members in
  // different encodings lost whole members.
  let proxies = [];
  if (Array.isArray(nodes)) {
    // Pre-parsed nodes (produceNodes) skip the parse.
    for (const proxy of nodes) proxies.push(proxy);
  } else {
    for (const source of (rawParts.length > 0 ? rawParts : [raw])) {
      const parsed = core.parse(source);
      if (!Array.isArray(parsed)) {
        throw new Error("Sub-Store parse(raw) must return an array");
      }
      for (const proxy of parsed) proxies.push(proxy);
    }
  }
  const sourceNodeCount = proxies.length;
  if (!Array.isArray(operators)) {
    throw new Error("Sub-Store operators must be an array");
  }
%s
  const output = core.produce(proxies, target, "external", produceOptions);
  if (typeof output !== "string") {
    throw new Error("Sub-Store produce(proxies, target, env) must return a string");
  }
  // What this producer writes for no nodes at all. A document equal to it
  // carries nothing for this client, however many bytes it has. It is asked
  // for only when the output could be it: every producer's empty document is
  // a short skeleton ("proxies:", an empty sing-box object), and
  // TestEmptyDocumentsFitTheZeroNodeBound holds each target to the bound, so
  // a longer output carries at least one node and the serve path skips the
  // extra produce.
  let empty;
  const emptyDocument = () => {
    if (empty === undefined) {
      try {
        empty = core.produce([], target, "external", produceOptions);
      } catch (err) {
        empty = null;
      }
    }
    return empty;
  };
  const trimmedOutput = output.trim();
  const zeroNodes = proxies.length === 0 ||
    trimmedOutput === "" ||
    (trimmedOutput.length <= %d && typeof emptyDocument() === "string" && trimmedOutput === empty.trim());
  // Which nodes this client could not carry.
  //
  // Every producer keeps its own support rules inside itself and declares them
  // nowhere, so the only reading that cannot go stale is to ask it: a node the
  // producer drops leaves the document byte for byte as an empty list would.
  // The cheap check comes first, and on the common path where nothing was
  // dropped it is the only extra work done.
  const unsupportedTypes = [];
  let unsupportedCount = 0;
  if (explain && !produceOptions["include-unsupported-proxy"] && proxies.length > 0) {
    const permissive = Object.assign({}, produceOptions, { "include-unsupported-proxy": true });
    let everything = output;
    try {
      everything = core.produce(proxies, target, "external", permissive);
    } catch (err) {
      everything = output;
    }
    if (everything !== output) {
      if (emptyDocument() !== null) {
        for (const proxy of proxies) {
          let alone = null;
          try {
            alone = core.produce([proxy], target, "external", produceOptions);
          } catch (err) {
            // A producer that refuses one node outright has dropped it just as
            // surely as one that filters it.
            alone = empty;
          }
          if (alone !== empty) continue;
          unsupportedCount += 1;
          const type = proxy && typeof proxy.type === "string" ? proxy.type : "unknown";
          if (!unsupportedTypes.includes(type)) unsupportedTypes.push(type);
        }
      }
    }
  }
  // Which nodes the chain kept that a URI carrier could not write.
  //
  // The URI producer writes one line per node it can express and drops the
  // rest, so a line count below the node count means something was lost. The
  // nodes the core rejects for every client are set aside first, by asking
  // the JSON producer (which drops nothing itself) what survives the core's
  // own filters; only the remainder that the URI producer cannot write
  // counts as lost. The common path, where nothing was dropped, costs one
  // split of the output.
  const carrierLostTypes = [];
  let carrierLostCount = 0;
  if (carrierCheck && proxies.length > 0) {
    const carried = output.split("\n").filter((line) => line.trim() !== "").length;
    if (carried < proxies.length) {
      let valid = proxies;
      try {
        valid = core.produce(proxies.slice(), "JSON", "internal", { "include-unsupported-proxy": true });
      } catch (err) {
        valid = proxies;
      }
      for (const proxy of Array.isArray(valid) ? valid : proxies) {
        let line = "";
        try {
          line = core.produce([proxy], target, "external", produceOptions);
        } catch (err) {
          line = "";
        }
        if (typeof line === "string" && line.trim() !== "") continue;
        carrierLostCount += 1;
        const type = proxy && typeof proxy.type === "string" ? proxy.type : "unknown";
        if (!carrierLostTypes.includes(type)) carrierLostTypes.push(type);
      }
    }
  }
  return JSON.stringify({
    source_node_count: sourceNodeCount,
    node_count: proxies.length,
    unsupported_node_count: unsupportedCount,
    unsupported_protocols: unsupportedTypes.sort(),
    zero_nodes: zeroNodes,
    carrier_lost_node_count: carrierLostCount,
    carrier_lost_protocols: carrierLostTypes.sort(),
    output,
  });
})()`, prefix, raw, rawParts, nodes, target, operators, options, explain, carrierCheck, processBlock, subStoreMaxEmptyDocumentBytes), nil
}

func subStoreResponseTransformScript(req subStoreResponseTransformRequest) (string, error) {
	response := json.RawMessage(strings.TrimSpace(string(req.Response)))
	if len(response) == 0 {
		response = json.RawMessage(`{}`)
	}
	var responseObject map[string]any
	if err := json.Unmarshal(response, &responseObject); err != nil {
		return "", fmt.Errorf("response must be a JSON object: %w", err)
	}
	responseJSON, err := json.Marshal(responseObject)
	if err != nil {
		return "", fmt.Errorf("encode response: %w", err)
	}
	target, err := json.Marshal(req.Target)
	if err != nil {
		return "", fmt.Errorf("encode target: %w", err)
	}
	operators, err := json.Marshal(req.Operators)
	if err != nil {
		return "", fmt.Errorf("encode operators: %w", err)
	}
	return fmt.Sprintf(`(async function() {
  const response = %s;
  const target = %s;
  const operators = %s || [];
  const root = globalThis.SubStoreProxyUtils;
  const core = root && root.ProxyUtils ? root.ProxyUtils : root;
  if (!core || typeof core.processResponse !== "function") {
    throw new Error("Sub-Store core must expose processResponse(response, operators)");
  }
  if (!Array.isArray(operators)) {
    throw new Error("Sub-Store operators must be an array");
  }
  const output = await core.processResponse(response, operators, target, undefined, undefined);
  if (!output || typeof output !== "object" || Array.isArray(output)) {
    throw new Error("Sub-Store processResponse(response, operators) must return an object");
  }
  const status = Number(output.status || 200);
  if (!Number.isFinite(status)) {
    throw new Error("Sub-Store response status must be finite");
  }
  const headers = output.headers && typeof output.headers === "object" ? output.headers : {};
  const body = Object.prototype.hasOwnProperty.call(output, "body") ? output.body : "";
  if (typeof body !== "string") {
    throw new Error("Sub-Store response body must be a string");
  }
  return JSON.stringify({ status, headers, body });
})()`, responseJSON, target, operators), nil
}

// substoreEngineRawErrors lets a failure carry its original text.
//
// A JS error can quote the document that produced it — a subscription body, a
// token inside a URL — which is why every caller normally sees a hash instead.
// That protection also makes a failure impossible to diagnose, so tests turn it
// off around a single call. No production path assigns this: the only writer is
// the helper in the test files, which restores it on cleanup.
var substoreEngineRawErrors = false

func redactSubStoreJSError(stage string, err error) error {
	if substoreEngineRawErrors {
		return fmt.Errorf("Sub-Store JS %s failed: %w", stage, err)
	}
	sum := sha256.Sum256([]byte(err.Error()))
	return fmt.Errorf("Sub-Store JS %s failed (error_sha256=%x)", stage, sum[:8])
}

func redactSubStoreEnginePanic(recovered any) error {
	if substoreEngineRawErrors {
		return fmt.Errorf("Sub-Store engine panicked: %v", recovered)
	}
	sum := sha256.Sum256([]byte(fmt.Sprint(recovered)))
	return fmt.Errorf("Sub-Store engine panicked (panic_sha256=%x)", sum[:8])
}

const subStoreConsoleShim = `
globalThis.console = {
  debug: function() {},
  error: function() {},
  info: function() {},
  log: function() {},
  warn: function() {}
};
`
