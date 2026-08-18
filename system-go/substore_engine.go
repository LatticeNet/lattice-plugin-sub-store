package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
// so a later boot can retry, rather than disabling seeding for the process's
// lifetime. Engines carrying a custom core (tests) compile their own.
var (
	embeddedCoreBytecodeMu sync.Mutex
	embeddedCoreBytecode   []byte
)

// warmBootRetryCooldown spaces background boot attempts after a failure so a
// deterministic boot fault costs one attempt per window, not one per call.
const warmBootRetryCooldown = 30 * time.Second

// subStoreEngine owns the embedded Sub-Store core and two ways to run it.
//
// The warm path keeps one QuickJS runtime alive with the core already
// evaluated, so a call pays only its own script: the ~13.5 s the production
// box spent re-booting the engine per invocation (wasm compile + 1.27 MB core
// eval) happens at most once per worker process. Only chains free of user
// JavaScript run there — every generated call script is an IIFE, so repeated
// evaluation cannot collide in the shared global scope, and nothing user-
// written can poison state a later record would inherit.
//
// The isolated path builds a fresh runtime per call, seeded from core
// bytecode compiled once at warm-up (measured ~4x faster than re-parsing the
// source). Anything that executes user JavaScript — script operators and
// filters, response transformers, script files — runs there and the runtime
// is discarded with the call.
//
// Interruption is asymmetric by measurement, not by choice: a hot loop inside
// QuickJS-on-wazero ignores MaxExecutionTime and an asynchronous Close, so
// the warm path carries no in-process deadline — the runner's invocation
// budget bounds it by killing the worker, and the pool replaces the worker in
// the background. The isolated path keeps the context-bound runtime
// (CloseOnContextDone), whose deadline provably interrupts by closing the
// module mid-call; the resulting library panic is recovered and redacted.
type subStoreEngine struct {
	coreJS string
	limits subStoreEngineLimits

	// mu serializes warm-runtime use and guards the warm state. A v2 worker
	// handles one invocation at a time, so contention is the boot goroutine
	// only; the isolated path never takes this lock.
	mu         sync.Mutex
	warm       *qjs.Runtime
	warmCtx    *qjs.Context
	warmCancel context.CancelFunc
	warming    bool
	// lastBootFail memoizes a failed boot so calls do not retrigger one every
	// time inside the cooldown.
	lastBootFail time.Time
	coreBytecode []byte
	// Path counters, for tests and diagnostics: how many calls each path
	// actually served. warmServes is guarded by mu (the warm path holds it
	// anyway); isolatedServes is atomic so the isolated path stays lock-free.
	warmServes     int
	isolatedServes atomic.Int64

	// scriptHTTP is the network arm for the invocation currently in flight,
	// or nil when none is. A v2 worker handles one invocation at a time, so
	// this is a single slot rather than a map; it is a pointer swap so the
	// isolated path (which never takes mu) can read it without locking.
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
	Raw       string            `json:"raw"`
	Target    string            `json:"target"`
	Operators []json.RawMessage `json:"operators,omitempty"`
	// Options is the produce() opts object — Sub-Store's own flag names, e.g.
	// "include-unsupported-proxy". Bounded to booleans so a caller cannot
	// smuggle structures into the engine script; unknown names are simply
	// ignored by the core, exactly as upstream ignores them.
	Options map[string]bool `json:"options,omitempty"`
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
}

type subStoreResponseTransformResult struct {
	Target    string         `json:"target,omitempty"`
	Status    int            `json:"status"`
	Headers   map[string]any `json:"headers"`
	Body      string         `json:"body"`
	BodyBytes int            `json:"body_bytes"`
}

type subStoreCoreConversionResult struct {
	SourceNodeCount int    `json:"source_node_count"`
	NodeCount       int    `json:"node_count"`
	Output          string `json:"output"`
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

func (engine *subStoreEngine) convert(req subStoreConversionRequest) (result subStoreConversionResult, err error) {
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
	run := engine.runCoreScript
	if containsScriptingOperator(req.Operators) {
		// User JavaScript never touches the warm runtime.
		run = engine.runIsolatedScript
	}
	rawResult, err := run("convert", "lattice-substore-convert.js", script)
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
	}, nil
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
	// A response transformer is user JavaScript by definition; it never
	// touches the warm runtime.
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

// prewarm boots the warm runtime synchronously. Production launches it from
// a background goroutine at process start (the boot recovers its own library
// panics); tests call it directly and rely on the warm runtime being ready on
// return.
func (engine *subStoreEngine) prewarm() error {
	return engine.bootWarm()
}

// startWarmBootLocked spawns at most one background boot, spaced by a
// cooldown after failures. Calls never wait for a boot: while one is pending
// they take the isolated path, which the compiled bytecode keeps cheap.
func (engine *subStoreEngine) startWarmBootLocked() {
	if engine.warming || engine.warm != nil {
		return
	}
	if !engine.lastBootFail.IsZero() && time.Since(engine.lastBootFail) < warmBootRetryCooldown {
		return
	}
	engine.warming = true
	go func() { _ = engine.bootWarm() }()
}

// bootWarm builds a warm runtime outside the engine lock and installs it
// under the lock. The runtime is context-bound (CloseOnContextDone) so the
// per-call watchdog can interrupt a wedged eval by cancellation — the one
// interruption mechanism that measurably works on QuickJS-on-wazero. The qjs
// library panics on internal faults by design, so the whole boot recovers.
func (engine *subStoreEngine) bootWarm() (err error) {
	engine.mu.Lock()
	engine.warming = true
	engine.mu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = redactSubStoreEnginePanic(recovered)
		}
		engine.mu.Lock()
		engine.warming = false
		if err != nil {
			engine.lastBootFail = time.Now()
		}
		engine.mu.Unlock()
	}()
	if strings.TrimSpace(engine.coreJS) == "" {
		return fmt.Errorf("Sub-Store core bundle is empty")
	}
	limits := engine.limits.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	rt, newErr := qjs.New(qjs.Option{
		Context:            ctx,
		CloseOnContextDone: true,
		MemoryLimit:        limits.MemoryLimit,
		MaxStackSize:       limits.MaxStackSize,
		GCThreshold:        limits.GCThreshold,
		Stdout:             io.Discard,
		Stderr:             io.Discard,
	})
	if newErr != nil {
		cancel()
		return fmt.Errorf("create warm Sub-Store JS runtime: %w", newErr)
	}
	qctx := rt.Context()
	if shimErr := evalQuickJSStep(qctx, "lattice-console-shim.js", subStoreConsoleShim); shimErr != nil {
		cancel()
		closeQuickJSRuntime(rt)
		return fmt.Errorf("install Sub-Store console shim: %w", shimErr)
	}
	// Before the core, always: the core decides which client it is running
	// inside while it loads. The warm path needs this too — Resolve Domain
	// speaks DoH and carries no user JavaScript, so it runs here.
	engine.installScriptHTTPBinding(qctx)
	if shimErr := evalQuickJSStep(qctx, "lattice-script-env.js", subStoreScriptEnvShim); shimErr != nil {
		cancel()
		closeQuickJSRuntime(rt)
		return fmt.Errorf("install Sub-Store script environment: %w", shimErr)
	}
	// Compile first, evaluate the bytecode: one parse serves both the warm
	// boot and every later isolated seed.
	bytecode := engine.ensureCoreBytecode(qctx)
	if len(bytecode) > 0 {
		value, coreErr := qctx.Eval("substore-core.js", qjs.Bytecode(bytecode))
		if value != nil {
			value.Free()
		}
		if coreErr != nil {
			cancel()
			closeQuickJSRuntime(rt)
			return fmt.Errorf("load Sub-Store core: %w", coreErr)
		}
	} else if coreErr := evalQuickJSStep(qctx, "substore-core.js", engine.coreJS); coreErr != nil {
		cancel()
		closeQuickJSRuntime(rt)
		return fmt.Errorf("load Sub-Store core: %w", coreErr)
	}
	engine.mu.Lock()
	if engine.warm != nil {
		// A concurrent boot won; keep the installed one.
		engine.mu.Unlock()
		cancel()
		go closeQuickJSRuntime(rt)
		return nil
	}
	engine.warm, engine.warmCtx, engine.warmCancel = rt, qctx, cancel
	engine.lastBootFail = time.Time{}
	engine.mu.Unlock()
	return nil
}

// ensureCoreBytecode returns the core's bytecode, compiling it on the given
// context when no cached copy exists. The embedded core's copy is shared
// process-wide; a failed compile leaves the cache empty for a later retry.
func (engine *subStoreEngine) ensureCoreBytecode(qctx *qjs.Context) []byte {
	engine.mu.Lock()
	cached := engine.coreBytecode
	engine.mu.Unlock()
	if len(cached) > 0 {
		return cached
	}
	if engine.coreJS == embeddedSubStoreCoreJS {
		embeddedCoreBytecodeMu.Lock()
		if len(embeddedCoreBytecode) == 0 {
			if bytecode, err := qctx.Compile("substore-core.js", qjs.Code(engine.coreJS)); err == nil {
				embeddedCoreBytecode = bytecode
			}
		}
		cached = embeddedCoreBytecode
		embeddedCoreBytecodeMu.Unlock()
	} else if bytecode, err := qctx.Compile("substore-core.js", qjs.Code(engine.coreJS)); err == nil {
		cached = bytecode
	}
	if len(cached) > 0 {
		engine.mu.Lock()
		engine.coreBytecode = cached
		engine.mu.Unlock()
	}
	return cached
}

// closeQuickJSRuntime closes a runtime whose module may already be broken.
// qjs panics on close failures by design, and a cleanup panic or hang must
// stay confined here rather than kill the worker.
func closeQuickJSRuntime(rt *qjs.Runtime) {
	defer func() { _ = recover() }()
	rt.Close()
}

func (engine *subStoreEngine) markWarmDeadLocked() {
	if engine.warm == nil {
		return
	}
	warm, cancel := engine.warm, engine.warmCancel
	engine.warm, engine.warmCtx, engine.warmCancel = nil, nil, nil
	if cancel != nil {
		cancel()
	}
	// Close on a separate goroutine: after a mid-eval failure the module may
	// be wedged, and a dead runtime must never block the invocation that
	// discovered it.
	go closeQuickJSRuntime(warm)
}

// runCoreScript runs a call that contains no user JavaScript. It answers from
// the warm runtime when one is ready and otherwise takes the isolated path
// immediately — a call never waits for a boot.
func (engine *subStoreEngine) runCoreScript(stage, file, script string) (string, error) {
	out, err, served := engine.runWarm(stage, file, script)
	if served {
		return out, err
	}
	return engine.runIsolatedScript(stage, file, script)
}

// runWarm executes on the persistent runtime when it is ready. served=false
// means no warm runtime exists yet (a background boot was requested) and the
// caller should take the isolated path. A per-call watchdog cancels the
// runtime's context at limits.Timeout, restoring the in-process deadline the
// per-call runtimes used to provide: the cancellation closes the module, the
// in-flight eval panics, the recover below answers the call and retires the
// runtime, and the next call re-warms in the background. Any eval failure is
// the call's answer — retrying user input against a fresh engine would just
// fail slower.
func (engine *subStoreEngine) runWarm(stage, file, script string) (out string, err error, served bool) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.warm == nil {
		engine.startWarmBootLocked()
		return "", nil, false
	}
	limits := engine.limits.withDefaults()
	watchdog := time.AfterFunc(limits.Timeout, engine.warmCancel)
	defer func() {
		fired := !watchdog.Stop()
		if recovered := recover(); recovered != nil {
			engine.markWarmDeadLocked()
			out, err, served = "", redactSubStoreEnginePanic(recovered), true
			return
		}
		if fired {
			// The deadline fired even though this call completed: the
			// runtime's context is canceled for good, so retire it before it
			// fails a later call.
			engine.markWarmDeadLocked()
		}
	}()
	engine.warmServes++
	value, evalErr := engine.warmCtx.Eval(file, qjs.Code(script))
	if evalErr != nil {
		return "", redactSubStoreJSError(stage, evalErr), true
	}
	out, err = finishQuickJSValue(stage, value)
	if err != nil {
		// A malformed completion is the script's fault, not the runtime's;
		// the warm engine stays up.
		return "", err, true
	}
	return out, nil, true
}

// runIsolatedScript executes on a fresh runtime that dies with the call. This
// is the only place user JavaScript ever runs, and the only path with an
// in-process deadline: the context-bound runtime is the one interruption
// mechanism that provably works.
func (engine *subStoreEngine) runIsolatedScript(stage, file, script string) (string, error) {
	if strings.TrimSpace(engine.coreJS) == "" {
		return "", fmt.Errorf("Sub-Store core bundle is empty")
	}

	engine.isolatedServes.Add(1)

	limits := engine.limits.withDefaults()
	ctx, cancel := context.WithTimeout(context.Background(), limits.Timeout)
	defer cancel()

	rt, err := qjs.New(qjs.Option{
		Context:            ctx,
		CloseOnContextDone: true,
		MemoryLimit:        limits.MemoryLimit,
		MaxStackSize:       limits.MaxStackSize,
		GCThreshold:        limits.GCThreshold,
		Stdout:             io.Discard,
		Stderr:             io.Discard,
	})
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
	if err := engine.loadCoreInto(qctx); err != nil {
		return "", err
	}
	value, err := qctx.Eval(file, qjs.Code(script))
	if err != nil {
		return "", redactSubStoreJSError(stage, err)
	}
	return finishQuickJSValue(stage, value)
}

// loadCoreInto seeds a fresh context with the core, from bytecode when the
// warm-up produced it and from source otherwise.
func (engine *subStoreEngine) loadCoreInto(qctx *qjs.Context) error {
	engine.mu.Lock()
	bytecode := engine.coreBytecode
	engine.mu.Unlock()
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

func subStoreConversionScript(req subStoreConversionRequest) (string, error) {
	raw, err := json.Marshal(req.Raw)
	if err != nil {
		return "", fmt.Errorf("encode raw subscription: %w", err)
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
  const target = %s;
  const operators = %s || [];
  const produceOptions = %s || {};
  const root = globalThis.SubStoreProxyUtils;
  const core = root && root.ProxyUtils ? root.ProxyUtils : root;
  if (!core || typeof core.parse !== "function" || typeof core.produce !== "function") {
    throw new Error("Sub-Store core must expose parse(raw) and produce(proxies, target, env)");
  }
  let proxies = core.parse(raw);
  if (!Array.isArray(proxies)) {
    throw new Error("Sub-Store parse(raw) must return an array");
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
  return JSON.stringify({ source_node_count: sourceNodeCount, node_count: proxies.length, output });
})()`, prefix, raw, target, operators, options, processBlock), nil
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
