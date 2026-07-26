package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/fastschema/qjs"
)

const consoleShim = `
var console = {
  log: function(){},
  info: function(){},
  warn: function(){},
  error: function(){},
  debug: function(){}
};
`

type report struct {
	BundlePath       string         `json:"bundle_path"`
	BundleBytes      int            `json:"bundle_bytes"`
	BundleSHA256Hint string         `json:"bundle_sha256_hint,omitempty"`
	Iterations       int            `json:"iterations"`
	Engines          []engineReport `json:"engines"`
}

type engineReport struct {
	Name          string          `json:"name"`
	FeatureLevel  string          `json:"feature_level"`
	ModulesLoaded []string        `json:"modules_loaded"`
	ShimsRequired []string        `json:"shims_required"`
	Load          measurement     `json:"load"`
	Features      []featureResult `json:"features"`
	Conversions   []caseResult    `json:"conversions"`
}

type featureResult struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type caseResult struct {
	Name             string        `json:"name"`
	Target           string        `json:"target"`
	InputNodes       int           `json:"input_nodes"`
	Iterations       int           `json:"iterations"`
	OK               bool          `json:"ok"`
	Error            string        `json:"error,omitempty"`
	NodeCount        int           `json:"node_count,omitempty"`
	OutputBytes      int           `json:"output_bytes,omitempty"`
	MeanMillis       float64       `json:"mean_millis,omitempty"`
	MinMillis        float64       `json:"min_millis,omitempty"`
	MaxMillis        float64       `json:"max_millis,omitempty"`
	MeanTotalAllocKB float64       `json:"mean_total_alloc_kb,omitempty"`
	MaxHeapAllocKB   uint64        `json:"max_heap_alloc_kb,omitempty"`
	Runs             []measurement `json:"runs,omitempty"`
}

type measurement struct {
	OK               bool    `json:"ok"`
	Error            string  `json:"error,omitempty"`
	Millis           float64 `json:"millis,omitempty"`
	TotalAllocKB     uint64  `json:"total_alloc_kb,omitempty"`
	HeapAllocAfterKB uint64  `json:"heap_alloc_after_kb,omitempty"`
}

type conversionOutput struct {
	NodeCount   int    `json:"node_count"`
	OutputBytes int    `json:"output_bytes"`
	Head        string `json:"head,omitempty"`
}

type sampleCase struct {
	Name       string
	Target     string
	InputNodes int
	Raw        string
}

type engineProbe struct {
	name string
	load func(string) (engineInstance, measurement)
}

type engineInstance interface {
	run(script string) (string, error)
	close()
}

func main() {
	bundlePath := flag.String("bundle", "", "path to an IIFE bundle exposing global SubStoreProxyUtils")
	iterations := flag.Int("iterations", 5, "cold conversion iterations for small and medium cases")
	jsonOut := flag.Bool("json", false, "emit JSON")
	flag.Parse()

	if *bundlePath == "" {
		fail("missing -bundle")
	}
	bundle, err := os.ReadFile(*bundlePath)
	if err != nil {
		fail("read bundle: %v", err)
	}

	cases := []sampleCase{
		{Name: "small-uri-2", Target: "Clash", InputNodes: 2, Raw: sampleLinks(2)},
		{Name: "medium-uri-200", Target: "sing-box", InputNodes: 200, Raw: sampleLinks(200)},
		{Name: "cap-check-uri-5000", Target: "sing-box", InputNodes: 5000, Raw: sampleLinks(5000)},
	}
	probes := []engineProbe{
		{name: "goja", load: loadGoja},
		{name: "quickjs-wazero", load: loadQJS},
	}

	out := report{
		BundlePath:  *bundlePath,
		BundleBytes: len(bundle),
		Iterations:  *iterations,
	}
	for _, probe := range probes {
		out.Engines = append(out.Engines, runEngine(probe, string(bundle), cases, *iterations))
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fail("encode report: %v", err)
		}
		return
	}
	for _, engine := range out.Engines {
		fmt.Printf("%s: load ok=%v %.1fms feature=%s shims=%s\n",
			engine.Name,
			engine.Load.OK,
			engine.Load.Millis,
			engine.FeatureLevel,
			strings.Join(engine.ShimsRequired, ", "),
		)
		for _, conv := range engine.Conversions {
			if !conv.OK {
				fmt.Printf("  %s/%s failed: %s\n", conv.Name, conv.Target, conv.Error)
				continue
			}
			fmt.Printf("  %s/%s nodes=%d bytes=%d mean=%.1fms alloc=%.1fKiB\n",
				conv.Name,
				conv.Target,
				conv.NodeCount,
				conv.OutputBytes,
				conv.MeanMillis,
				conv.MeanTotalAllocKB,
			)
		}
	}
}

func runEngine(probe engineProbe, bundle string, cases []sampleCase, iterations int) engineReport {
	engine := engineReport{
		Name:          probe.name,
		ModulesLoaded: []string{},
		ShimsRequired: []string{"console"},
	}

	featureInst, featureLoad := probe.load("")
	if featureInst != nil {
		engine.Features = runFeatures(featureInst, probe.name)
		engine.FeatureLevel = featureLevel(engine.Features)
		featureInst.close()
	} else {
		engine.Features = []featureResult{{
			Name:  "feature probe engine load",
			OK:    false,
			Error: featureLoad.Error,
		}}
		engine.FeatureLevel = "feature probe failed"
	}

	inst, loadMeasure := probe.load(bundle)
	engine.Load = loadMeasure
	if inst != nil {
		engine.ModulesLoaded = []string{"ProxyUtils.parse", "ProxyUtils.produce"}
		defer inst.close()
	} else {
		if engine.FeatureLevel == "" {
			engine.FeatureLevel = "bundle load failed"
		}
		return engine
	}

	for _, sample := range cases {
		caseIterations := iterations
		if sample.InputNodes > 1000 {
			caseIterations = 1
		}
		engine.Conversions = append(engine.Conversions, runCase(probe, bundle, sample, caseIterations))
	}
	return engine
}

func runCase(probe engineProbe, bundle string, sample sampleCase, iterations int) caseResult {
	result := caseResult{
		Name:       sample.Name,
		Target:     sample.Target,
		InputNodes: sample.InputNodes,
		Iterations: iterations,
	}
	var totalMillis float64
	var totalAlloc uint64
	var output conversionOutput
	for i := 0; i < iterations; i++ {
		inst, loadMeasure := probe.load(bundle)
		if inst == nil {
			result.Error = loadMeasure.Error
			return result
		}
		script := conversionScript(sample.Raw, sample.Target)
		value, runMeasure := measure(func() (string, error) {
			return inst.run(script)
		})
		inst.close()
		runMeasure.Millis += loadMeasure.Millis
		runMeasure.TotalAllocKB += loadMeasure.TotalAllocKB
		if !runMeasure.OK {
			result.Error = runMeasure.Error
			result.Runs = append(result.Runs, runMeasure)
			return result
		}
		var parsed conversionOutput
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			runMeasure.OK = false
			runMeasure.Error = "decode conversion output: " + err.Error()
			result.Error = runMeasure.Error
			result.Runs = append(result.Runs, runMeasure)
			return result
		}
		output = parsed
		totalMillis += runMeasure.Millis
		totalAlloc += runMeasure.TotalAllocKB
		if result.MinMillis == 0 || runMeasure.Millis < result.MinMillis {
			result.MinMillis = runMeasure.Millis
		}
		if runMeasure.Millis > result.MaxMillis {
			result.MaxMillis = runMeasure.Millis
		}
		if runMeasure.HeapAllocAfterKB > result.MaxHeapAllocKB {
			result.MaxHeapAllocKB = runMeasure.HeapAllocAfterKB
		}
		result.Runs = append(result.Runs, runMeasure)
	}
	result.OK = true
	result.NodeCount = output.NodeCount
	result.OutputBytes = output.OutputBytes
	result.MeanMillis = totalMillis / float64(iterations)
	result.MeanTotalAllocKB = float64(totalAlloc) / float64(iterations)
	return result
}

func loadGoja(bundle string) (engineInstance, measurement) {
	var inst *gojaInstance
	_, m := measure(func() (string, error) {
		vm := goja.New()
		if _, err := vm.RunString(consoleShim); err != nil {
			return "", err
		}
		if bundle != "" {
			if _, err := vm.RunString(bundle); err != nil {
				return "", err
			}
		}
		inst = &gojaInstance{vm: vm}
		return "", nil
	})
	if inst == nil {
		return nil, m
	}
	return inst, m
}

type gojaInstance struct {
	vm *goja.Runtime
}

func (i *gojaInstance) run(script string) (string, error) {
	value, err := i.vm.RunString(script)
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

func (i *gojaInstance) close() {}

func loadQJS(bundle string) (engineInstance, measurement) {
	var inst *qjsInstance
	_, m := measure(func() (string, error) {
		rt, err := qjs.New()
		if err != nil {
			return "", err
		}
		ctx := rt.Context()
		if value, err := ctx.Eval("console-shim.js", qjs.Code(consoleShim)); err != nil {
			rt.Close()
			return "", err
		} else if value != nil {
			value.Free()
		}
		if bundle != "" {
			if value, err := ctx.Eval("substore-proxy-utils.iife.js", qjs.Code(bundle)); err != nil {
				rt.Close()
				return "", err
			} else if value != nil {
				value.Free()
			}
		}
		inst = &qjsInstance{rt: rt, ctx: ctx}
		return "", nil
	})
	if inst == nil {
		return nil, m
	}
	return inst, m
}

type qjsInstance struct {
	rt  *qjs.Runtime
	ctx *qjs.Context
}

func (i *qjsInstance) run(script string) (string, error) {
	value, err := i.ctx.Eval("conversion.js", qjs.Code(script))
	if err != nil {
		return "", err
	}
	defer value.Free()
	return value.String(), nil
}

func (i *qjsInstance) close() {
	i.rt.Close()
}

func runFeatures(inst engineInstance, engine string) []featureResult {
	tests := []struct {
		name   string
		script string
	}{
		{name: "ES5.1 strict/json", script: `(function(){ "use strict"; return JSON.stringify([1,2,3].map(function(x){ return x + 1; })); })()`},
		{name: "ES2015 let/const/arrow/class/spread/map", script: `(() => { const f = (...xs) => xs.reduce((a, b) => a + b, 0); class C2015 { constructor(x){ this.x = x; } } return JSON.stringify({sum:f(1,2,3), x:new C2015(7).x, m:new Map([["a",1]]).get("a")}); })()`},
		{name: "ES2018 async function syntax", script: `(() => { return (async function(){ return await Promise.resolve(42); }); })(); "ok"`},
		{name: "ES2020 optional chaining/nullish", script: `(() => { const v = ({a:{b:1}}).a?.b ?? 2; return String(v); })()`},
		{name: "ES2020 BigInt syntax", script: `(() => String(1n + 2n))()`},
		{name: "ES2022 class fields syntax", script: `(() => { class C2022 { x = 1; } return String(new C2022().x); })()`},
	}
	results := make([]featureResult, 0, len(tests)+1)
	for _, test := range tests {
		_, err := inst.run(test.script)
		results = append(results, featureResult{Name: test.name, OK: err == nil, Error: errString(err)})
	}
	if engine == "quickjs-wazero" {
		if qi, ok := inst.(*qjsInstance); ok {
			value, err := qi.ctx.Eval("module-probe.mjs", qjs.Code(`export const value = 1;`), qjs.TypeModule())
			if value != nil {
				value.Free()
			}
			results = append(results, featureResult{Name: "ES module syntax", OK: err == nil, Error: errString(err)})
		}
	} else {
		_, err := inst.run(`export const value = 1;`)
		results = append(results, featureResult{Name: "ES module syntax", OK: err == nil, Error: errString(err)})
	}
	return results
}

func featureLevel(results []featureResult) string {
	highest := "below ES5.1"
	order := []struct {
		probe string
		level string
	}{
		{"ES5.1", "ES5.1"},
		{"ES2015", "ES2015"},
		{"ES2018", "ES2018 syntax"},
		{"ES2020 optional", "ES2020 syntax"},
		{"ES2020 BigInt", "ES2020 BigInt"},
		{"ES2022", "ES2022 class fields"},
		{"ES module", "ES module syntax"},
	}
	ok := map[string]bool{}
	for _, result := range results {
		ok[result.Name] = result.OK
	}
	for _, item := range order {
		for name, passed := range ok {
			if passed && strings.Contains(name, item.probe) {
				highest = item.level
			}
		}
	}
	return highest
}

func conversionScript(raw string, target string) string {
	rawJSON, _ := json.Marshal(raw)
	targetJSON, _ := json.Marshal(target)
	return fmt.Sprintf(`(function(){
  const raw = %s;
  const target = %s;
  const proxies = SubStoreProxyUtils.parse(raw);
  const output = SubStoreProxyUtils.produce(proxies, target, "external");
  return JSON.stringify({
    node_count: proxies.length,
    output_bytes: output.length,
    head: output.slice(0, 120)
  });
})()`, rawJSON, targetJSON)
}

func sampleLinks(nodes int) string {
	if nodes < 1 {
		return ""
	}
	lines := make([]string, 0, nodes)
	for i := 0; i < nodes; i++ {
		if i%2 == 0 {
			lines = append(lines, fmt.Sprintf("ss://YWVzLTI1Ni1nY206cGFzcw@example-%04d.com:8388#ss-%04d", i, i))
			continue
		}
		lines = append(lines, fmt.Sprintf("trojan://password@example-%04d.org:443?peer=sni-%04d.example.org&sni=sni-%04d.example.org#trojan-%04d", i, i, i, i))
	}
	return strings.Join(lines, "\n")
}

func measure(fn func() (string, error)) (value string, result measurement) {
	goruntime.GC()
	var before goruntime.MemStats
	goruntime.ReadMemStats(&before)
	start := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			var after goruntime.MemStats
			goruntime.ReadMemStats(&after)
			result = measurement{
				OK:               false,
				Error:            fmt.Sprintf("panic: %v", recovered),
				Millis:           float64(time.Since(start).Microseconds()) / 1000,
				TotalAllocKB:     (after.TotalAlloc - before.TotalAlloc) / 1024,
				HeapAllocAfterKB: after.HeapAlloc / 1024,
			}
		}
	}()
	value, err := fn()
	elapsed := time.Since(start)
	var after goruntime.MemStats
	goruntime.ReadMemStats(&after)
	return value, measurement{
		OK:               err == nil,
		Error:            errString(err),
		Millis:           float64(elapsed.Microseconds()) / 1000,
		TotalAllocKB:     (after.TotalAlloc - before.TotalAlloc) / 1024,
		HeapAllocAfterKB: after.HeapAlloc / 1024,
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return msg
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
