// Package operators is the native operator chain: the fourteen non-script
// operators of the upstream inventory (upstream.md section 5) over nodemodel
// nodes, and the compiler that classifies every stored step as native,
// fallback, response or disabled (S1 plan section 2.3).
//
// The semantics are the pinned bundle's (2.36.22), observed through
// engine.convert over synthetic nodes, quirks included (Remove Duplicate
// Filter keeps every node, a null keep means keep for the Region and Type
// filters and drop for the Regex Filter). Three differences are decided:
// patterns are RE2 (design-28.md:170); the Conditional Filter's EXISTS tests
// presence (design-28.md:168; the bundle answers true for every node); and
// Sort and Regex Sort keep the input order of nodes the sort cannot tell
// apart (the bundle's comparator never answers "equal" for them, so
// QuickJS's unstable sort decides). TestOperatorsMatchBundleOnSyntheticNodes
// in package main holds the rest to the bundle.
//
// Nothing in this package imports package main, the SDK or the script engine.
package operators

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// Kind classifies a compiled step.
type Kind uint8

const (
	KindNative   Kind = iota // runs in Go
	KindFallback             // Resolve Domain, Script Operator, Script Filter: bundle; also a native operator whose pattern RE2 refuses or whose arguments only the bundle reads
	KindResponse             // Response Transformer: response stage, bundle until S3
	KindDisabled
)

// Diagnostic codes.
const (
	// CodeRegexIncompatible is a pattern RE2 refuses. The step falls back to
	// the bundle, the index flags the record, and a save that introduces one
	// is refused.
	CodeRegexIncompatible = "regex_incompatible"
	// CodeArgumentShape is arguments the native operator does not read the
	// way the bundle does (a bare string where the bundle wants a list, an
	// unknown sort order). The step falls back to the bundle, which answers
	// for it exactly as it did before the native chain existed.
	CodeArgumentShape = "argument_shape"
	// CodeInertUntilFiles marks Add Proxies From Subscription, which acts
	// only on a file pipeline that this plugin does not run yet; the bundle
	// leaves the node list alone too.
	CodeInertUntilFiles = "inert_until_files"
)

// Compiled is one step after compilation. Regexes are compiled once here.
type Compiled struct {
	Step Step
	Kind Kind
	Run  func(nodes []*nodemodel.Node, ctx *Context) []*nodemodel.Node // nil unless KindNative
	Diag []Diagnostic                                                  // regex_incompatible with its rewrite offer, unknown argument shapes
}

// Diagnostic is one finding about one step.
type Diagnostic struct {
	Step    int    // 1-based
	Code    string // "regex_incompatible", "argument_shape"
	Pattern string // the operator's own text, safe to show the operator
	Rewrite string // non-empty when the keep-mode negative-lookahead idiom was recognised
	Message string
}

// Plan is a record revision's compiled chain. It is built once per
// revision and cached in-process keyed by the record's Revision.
type Plan struct {
	Revision string
	Steps    []Compiled
}

// Context is what a step sees beside the nodes.
type Context struct {
	Target string // the exact caller target string
	Raw    string // the source text, for parity with process(..., raw)
}

// Native reports whether every enabled node-stage step runs in Go. Response
// Transformer steps do not count: they run on the response stage after the
// document exists (subscription_render.go:360-399) and are compiled as
// KindResponse. A chain whose only non-native step is a transformer is
// therefore native for the node stage and the transformer runs on the
// isolated bundle as today.
func (p *Plan) Native() bool {
	for _, s := range p.Steps {
		if s.Kind == KindFallback {
			return false
		}
	}
	return true
}

// HasFallback reports whether an enabled step is of a type only the bundle
// runs: Resolve Domain or a script step. It is the index's
// has_fallback_step. A native operator that falls back for a pattern or its
// arguments makes the plan not Native without counting here; the pattern
// case is Incompatible's.
func (p *Plan) HasFallback() bool {
	for _, s := range p.Steps {
		if s.Kind == KindFallback && fallbackOperators[s.Step.Type] {
			return true
		}
	}
	return false
}

// ResponseSteps are the Response Transformer steps, in order.
func (p *Plan) ResponseSteps() []Step {
	var out []Step
	for _, s := range p.Steps {
		if s.Kind == KindResponse {
			out = append(out, s.Step)
		}
	}
	return out
}

// Incompatible is every regex_incompatible diagnostic, in step order.
func (p *Plan) Incompatible() []Diagnostic {
	var out []Diagnostic
	for _, s := range p.Steps {
		for _, d := range s.Diag {
			if d.Code == CodeRegexIncompatible {
				out = append(out, d)
			}
		}
	}
	return out
}

// Run executes the native steps in order over nodes, in place. Steps of
// every other kind are skipped: a plan that is not Native runs on the bundle
// as a whole, never half here and half there.
func (p *Plan) Run(nodes []*nodemodel.Node, ctx *Context) []*nodemodel.Node {
	if ctx == nil {
		ctx = &Context{}
	}
	for _, s := range p.Steps {
		if s.Kind == KindNative {
			nodes = s.Run(nodes, ctx)
		}
	}
	return nodes
}

// Compile decodes, validates against the vocabulary and compiles. An
// unknown type is an error (operators.go:97-120 semantics). A pattern RE2
// rejects does not fail Compile: the step becomes KindFallback with a
// regex_incompatible diagnostic, so a stored record keeps rendering on the
// bundle (design-28.md:170).
func Compile(revision string, steps []json.RawMessage) (*Plan, error) {
	plan := &Plan{Revision: revision, Steps: make([]Compiled, 0, len(steps))}
	for i, raw := range steps {
		step, err := DecodeStep(raw)
		if err != nil {
			return nil, fmt.Errorf("process step %d: %w", i+1, err)
		}
		compiled, err := compileStep(i+1, step)
		if err != nil {
			return nil, err
		}
		plan.Steps = append(plan.Steps, compiled)
	}
	return plan, nil
}

// CompileStrict is the save-time variant: a regex_incompatible diagnostic
// is an error carrying the rewrite offer, so the editor can refuse and
// propose (decision.md:65). The error is an *IncompatibleError.
func CompileStrict(steps []json.RawMessage) (*Plan, error) {
	plan, err := Compile("", steps)
	if err != nil {
		return nil, err
	}
	if diags := plan.Incompatible(); len(diags) > 0 {
		return nil, &IncompatibleError{Diagnostics: diags}
	}
	return plan, nil
}

// IncompatibleError is CompileStrict's refusal. Its message leads with the
// regex_incompatible code, which is how a runtime method's refusal carries
// a stable code to the UI (ui/src/client.ts errorCodeOf), then names each
// step and pattern and, where one exists, the rewrite.
type IncompatibleError struct {
	Diagnostics []Diagnostic
}

func (e *IncompatibleError) Error() string {
	parts := make([]string, 0, len(e.Diagnostics))
	for _, d := range e.Diagnostics {
		part := fmt.Sprintf("process step %d: %s", d.Step, d.Message)
		if d.Rewrite != "" {
			part += fmt.Sprintf("; rewrite offered: a drop-mode Regex Filter on %q keeps the same nodes", d.Rewrite)
		}
		parts = append(parts, part)
	}
	return CodeRegexIncompatible + ": " + strings.Join(parts, "; ")
}

// AsIncompatible unwraps a CompileStrict refusal.
func AsIncompatible(err error) (*IncompatibleError, bool) {
	var target *IncompatibleError
	ok := errors.As(err, &target)
	return target, ok
}

// compileStep classifies and compiles one decoded step.
func compileStep(index int, step Step) (Compiled, error) {
	if strings.TrimSpace(step.Type) == "" {
		return Compiled{}, fmt.Errorf("process step %d has no type", index)
	}
	compile, native := nativeOperators[step.Type]
	switch {
	case !native && !fallbackOperators[step.Type] && !responseOperators[step.Type]:
		return Compiled{}, fmt.Errorf("process step %d: unknown operator %q", index, step.Type)
	case step.Disabled:
		return Compiled{Step: step, Kind: KindDisabled}, nil
	case responseOperators[step.Type]:
		return Compiled{Step: step, Kind: KindResponse}, nil
	case fallbackOperators[step.Type]:
		return Compiled{Step: step, Kind: KindFallback}, nil
	}
	c := &stepCompiler{index: index}
	run := compile(c, step.Args)
	out := Compiled{Step: step, Kind: KindNative, Run: run, Diag: c.diags}
	if run == nil || c.fallback() {
		out.Kind, out.Run = KindFallback, nil
	}
	return out, nil
}

// stepFunc is a native step's body.
type stepFunc = func(nodes []*nodemodel.Node, ctx *Context) []*nodemodel.Node

// stepCompiler collects one step's diagnostics while its operator compiles.
type stepCompiler struct {
	index int
	diags []Diagnostic
}

// shape records arguments the native operator does not read, and returns
// nil so a compile function can end with `return c.shape(...)`.
func (c *stepCompiler) shape(format string, args ...any) stepFunc {
	c.diags = append(c.diags, Diagnostic{Step: c.index, Code: CodeArgumentShape, Message: fmt.Sprintf(format, args...)})
	return nil
}

// fallback reports whether a diagnostic sends the step to the bundle.
func (c *stepCompiler) fallback() bool {
	for _, d := range c.diags {
		if d.Code == CodeRegexIncompatible || d.Code == CodeArgumentShape {
			return true
		}
	}
	return false
}
