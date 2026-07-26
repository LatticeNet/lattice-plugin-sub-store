package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fastschema/qjs"
)

const (
	subStoreQuickJSMemoryLimit = 128 << 20
	subStoreQuickJSStackLimit  = 8 << 20
	subStoreQuickJSGCThreshold = 32 << 20
)

var defaultSubStoreEngineLimits = subStoreEngineLimits{
	Timeout:      10 * time.Second,
	MemoryLimit:  subStoreQuickJSMemoryLimit,
	MaxStackSize: subStoreQuickJSStackLimit,
	GCThreshold:  subStoreQuickJSGCThreshold,
}

type subStoreEngine struct {
	coreJS string
	limits subStoreEngineLimits
}

type subStoreEngineLimits struct {
	Timeout      time.Duration
	MemoryLimit  int
	MaxStackSize int
	GCThreshold  int
}

type subStoreConversionRequest struct {
	Raw    string
	Target string
}

type subStoreConversionResult struct {
	Target      string `json:"target"`
	NodeCount   int    `json:"node_count"`
	Output      string `json:"output"`
	OutputBytes int    `json:"output_bytes"`
}

type subStoreCoreConversionResult struct {
	NodeCount int    `json:"node_count"`
	Output    string `json:"output"`
}

func newSubStoreEngine(coreJS string) subStoreEngine {
	return subStoreEngine{coreJS: coreJS, limits: defaultSubStoreEngineLimits}
}

func (engine subStoreEngine) convert(req subStoreConversionRequest) (result subStoreConversionResult, err error) {
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
		return subStoreConversionResult{}, fmt.Errorf("create Sub-Store JS runtime: %w", err)
	}
	defer rt.Close()

	qctx := rt.Context()
	if err := evalQuickJSStep(qctx, "lattice-console-shim.js", subStoreConsoleShim); err != nil {
		return subStoreConversionResult{}, fmt.Errorf("install Sub-Store console shim: %w", err)
	}
	if err := evalQuickJSStep(qctx, "substore-core.js", engine.coreJS); err != nil {
		return subStoreConversionResult{}, fmt.Errorf("load Sub-Store core: %w", err)
	}

	script, err := subStoreConversionScript(req)
	if err != nil {
		return subStoreConversionResult{}, err
	}
	value, err := qctx.Eval("lattice-substore-convert.js", qjs.Code(script))
	if err != nil {
		return subStoreConversionResult{}, redactSubStoreJSError("convert", err)
	}
	defer value.Free()
	if !value.IsString() {
		return subStoreConversionResult{}, fmt.Errorf("Sub-Store conversion returned %s, want stringified JSON", value.Type())
	}

	var coreResult subStoreCoreConversionResult
	if err := json.Unmarshal([]byte(value.String()), &coreResult); err != nil {
		return subStoreConversionResult{}, fmt.Errorf("decode Sub-Store conversion result: %w", err)
	}
	return subStoreConversionResult{
		Target:      req.Target,
		NodeCount:   coreResult.NodeCount,
		Output:      coreResult.Output,
		OutputBytes: len([]byte(coreResult.Output)),
	}, nil
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
	return fmt.Sprintf(`(function() {
  const raw = %s;
  const target = %s;
  const core = globalThis.SubStoreProxyUtils;
  if (!core || typeof core.parse !== "function" || typeof core.produce !== "function") {
    throw new Error("Sub-Store core must expose parse(raw) and produce(proxies, target, env)");
  }
  const proxies = core.parse(raw);
  if (!Array.isArray(proxies)) {
    throw new Error("Sub-Store parse(raw) must return an array");
  }
  const output = core.produce(proxies, target, "external");
  if (typeof output !== "string") {
    throw new Error("Sub-Store produce(proxies, target, env) must return a string");
  }
  return JSON.stringify({ node_count: proxies.length, output });
})()`, raw, target), nil
}

func redactSubStoreJSError(stage string, err error) error {
	sum := sha256.Sum256([]byte(err.Error()))
	return fmt.Errorf("Sub-Store JS %s failed (error_sha256=%x)", stage, sum[:8])
}

func redactSubStoreEnginePanic(recovered any) error {
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
