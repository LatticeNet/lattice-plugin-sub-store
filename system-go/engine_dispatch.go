package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/parse"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/producers"
)

// The native dispatcher (S1 plan sections 1.2 and 1.3). Every node-list
// conversion the render, collection, preview and convert paths make goes
// through convertNodes, which answers in Go when the target has a native
// producer and every enabled step of the chain compiles native, and
// otherwise runs the whole chain on the bundle's isolated path: never half
// native and half bundle (slices.md:54, decision.md:67). It sits above
// subStoreEngine, so engine.convert keeps its signature and its warm routing
// for the legacy engine service and for the engine's own tests.

// servedBy says which engine answered a conversion. Tests assert on it; the
// serve path ignores it.
type servedBy string

const (
	servedNative   servedBy = "native"
	servedIsolated servedBy = "isolated" // the bundle on runIsolatedScript
	servedWarm     servedBy = "warm"     // legacy engine service only
)

// nodeConvertRequest is one conversion as the render, collection, preview and
// convert paths ask for it.
//
// Parts are raw texts, each parsed on its own (RawParts semantics). Nodes are
// node-model nodes, decoded where the JSON entered (a convert request, a
// snapshot envelope) or produced by a member's native chain. Both may be set:
// the native path reads Nodes when there are any and the bundle reads Parts,
// so a snapshot that carries its parsed nodes beside its provider text
// serves either route without parsing twice. The bundle reads Nodes only
// when there are no Parts (convert with nodes, engine.produceNodes).
type nodeConvertRequest struct {
	Parts  []string
	Nodes  []*nodemodel.Node
	Target string // the exact caller string, keys the support map
	Plan   *operators.Plan
	// MemberFallback says a collection member's chain ran on the bundle, so
	// the collection renders whole on the bundle too (plan section 1.3).
	MemberFallback bool
	Options        map[string]bool
	Explain        bool
	CarrierCheck   bool
}

// nativeRoute reports whether a plan and a target run in Go. The whole chain
// and the target must be native; otherwise the bundle runs the whole chain on
// the isolated path (slices.md:54, decision.md:67).
func nativeRoute(plan *operators.Plan, target string) bool {
	return (plan == nil || plan.Native()) && producers.Native(target)
}

// convertNodes is the one entry every node-list conversion goes through. It
// returns the same fields subStoreConversionResult carries for the bundle, so
// finishNodeRender and memberNodes keep their semantics.
func (rt *runtime) convertNodes(req nodeConvertRequest) (subStoreConversionResult, servedBy, error) {
	if strings.TrimSpace(req.Target) == "" {
		return subStoreConversionResult{}, "", errors.New("target is required")
	}
	if !req.MemberFallback && nativeRoute(req.Plan, req.Target) {
		out, err := convertNative(req)
		return out, servedNative, err
	}
	engine := rt.subStoreEngine()
	if len(req.Parts) == 0 && len(req.Nodes) > 0 {
		encoded := make([]json.RawMessage, len(req.Nodes))
		for i, n := range req.Nodes {
			raw, err := n.MarshalJSON()
			if err != nil {
				return subStoreConversionResult{}, "", fmt.Errorf("encode node %d: %w", i, err)
			}
			encoded[i] = raw
		}
		out, err := engine.produceNodes(encoded, req.Target, req.Options)
		return out, servedIsolated, err
	}
	conversion := subStoreConversionRequest{
		Target:       req.Target,
		Operators:    bundleOperators(req.Plan),
		Options:      req.Options,
		Explain:      req.Explain,
		CarrierCheck: req.CarrierCheck,
	}
	if len(req.Parts) == 1 {
		conversion.Raw = req.Parts[0]
	} else {
		conversion.RawParts = req.Parts
	}
	out, err := engine.convertIsolated(conversion)
	return out, servedIsolated, err
}

// bundleOperators is the chain the bundle runs: every step the plan does not
// mark disabled, in order and as stored. Response Transformers are included,
// as they always were: the bundle's process skips them on the node stage.
func bundleOperators(plan *operators.Plan) []json.RawMessage {
	if plan == nil {
		return nil
	}
	out := make([]json.RawMessage, 0, len(plan.Steps))
	for _, s := range plan.Steps {
		if s.Kind != operators.KindDisabled {
			out = append(out, s.Step.Raw)
		}
	}
	return out
}

// parseParts parses each text on its own and concatenates the nodes, as the
// bundle's conversion script does with RawParts. The external opt-in (H3) has
// no record field before S2, so it is off.
func parseParts(parts []string) ([]*nodemodel.Node, error) {
	var nodes []*nodemodel.Node
	for _, part := range parts {
		parsed, _, err := parse.Document(part, parse.Options{})
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, parsed...)
	}
	return nodes, nil
}

// convertNative is rule 1 of plan section 1.2: parse (or take the nodes),
// run the chain, produce. The warm runtime is not touched.
func convertNative(req nodeConvertRequest) (subStoreConversionResult, error) {
	producer, _ := producers.Lookup(req.Target)
	nodes := req.Nodes
	if len(nodes) == 0 {
		parsed, err := parseParts(req.Parts)
		if err != nil {
			return subStoreConversionResult{}, err
		}
		nodes = parsed
	}
	out := subStoreConversionResult{Target: req.Target, SourceNodeCount: len(nodes)}
	if req.Plan != nil {
		nodes = req.Plan.Run(nodes, &operators.Context{Target: req.Target, Raw: strings.Join(req.Parts, "\n")})
	}
	out.NodeCount = len(nodes)
	opts := make(producers.Options, len(req.Options))
	for key, value := range req.Options {
		opts[key] = value
	}
	var document bytes.Buffer
	produced, err := producer.Produce(&document, nodes, req.Target, opts)
	if err != nil {
		return subStoreConversionResult{}, err
	}
	out.Output = document.String()
	out.OutputBytes = document.Len()
	out.ZeroNodes = produced.Entries == 0
	out.nodes = nodes
	// The producer's own drop report: every node that yielded no entry,
	// whatever the reason. The bundle can only infer drops by producing
	// each node alone, and counts none when include-unsupported-proxy is on;
	// here the report is exact, so a node the option cannot rescue (a VLESS
	// Reality block without a public key) is still named.
	if req.Explain {
		out.UnsupportedNodeCount, out.UnsupportedProtocols = droppedSummary(produced.Dropped, nil)
	}
	if req.CarrierCheck {
		// Nodes the core rejects for every client are not carrier losses:
		// the bundle sets them aside by asking its JSON producer, which keeps
		// everything but a broken Reality block and an xhttp stream-one with
		// download settings.
		out.CarrierLostNodeCount, out.CarrierLostProtocols = droppedSummary(produced.Dropped, map[string]bool{
			producers.ReasonBrokenReality:  true,
			producers.ReasonXHTTPStreamOne: true,
		})
	}
	return out, nil
}

// droppedSummary counts the dropped nodes whose reason is not skipped and
// names their protocols, sorted, "unknown" for a node without a type.
func droppedSummary(dropped []producers.Dropped, skip map[string]bool) (int, []string) {
	count := 0
	seen := map[string]bool{}
	var protocols []string
	for _, d := range dropped {
		if skip[d.Reason] {
			continue
		}
		count++
		protocol := d.Type
		if protocol == "" {
			protocol = "unknown"
		}
		if !seen[protocol] {
			seen[protocol] = true
			protocols = append(protocols, protocol)
		}
	}
	sort.Strings(protocols)
	return count, protocols
}

// planCacheEntries bounds the compiled chains kept in process: twice the
// record cap, so every record of a full store and the revision before its
// last edit fit.
const planCacheEntries = 512

// planCache holds compiled chains by record revision (plan section 2.3). A
// revision fingerprints the record's whole operator-editable content, its
// chain included, and every record the store hands out carries a recomputed
// one (withRevision), so a hit is always the chain the record holds. A plan
// is read-only once compiled: its steps keep compiled patterns and their
// arguments and no state between runs, so one plan serves every invocation
// of the worker.
type planCache struct {
	mu    sync.Mutex
	plans map[string]*operators.Plan
	order []string // insertion order, oldest first
	// compiles counts the compilations the cache made, for tests.
	compiles int
}

func newPlanCache() *planCache {
	return &planCache{plans: map[string]*operators.Plan{}}
}

// nativePlans is the worker's plan cache. It lives as long as the process,
// like the warm runtime, and holds nothing secret: steps and patterns only.
var nativePlans = newPlanCache()

// plan returns the compiled chain for revision, compiling it on a miss. An
// empty revision is a chain that belongs to no stored record (a preview of
// caller-supplied steps) and is compiled without being kept.
func (c *planCache) plan(revision string, steps []json.RawMessage) (*operators.Plan, error) {
	if revision == "" {
		return operators.Compile("", steps)
	}
	c.mu.Lock()
	cached := c.plans[revision]
	c.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	compiled, err := operators.Compile(revision, steps)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.compiles++
	if cached := c.plans[revision]; cached != nil {
		return cached, nil
	}
	if len(c.order) >= planCacheEntries {
		delete(c.plans, c.order[0])
		c.order = c.order[1:]
	}
	c.plans[revision] = compiled
	c.order = append(c.order, revision)
	return compiled, nil
}

// chainPlan is a record's compiled chain, from the per-revision cache. A
// record without steps has no plan: nil, which nativeRoute reads as no
// chain.
func (rt *runtime) chainPlan(rec subscriptionRecord) (*operators.Plan, error) {
	steps := processSteps(rec)
	if len(steps) == 0 {
		return nil, nil
	}
	return nativePlans.plan(rec.Revision, steps)
}

// planIsNative is nativeRoute without the target: whether a chain runs in Go
// wherever its nodes go. Fetch decides on it before any target is known.
func planIsNative(plan *operators.Plan) bool {
	return plan == nil || plan.Native()
}

// enabledSteps counts the steps the bundle would receive: every step not
// disabled, Response Transformers included.
func enabledSteps(plan *operators.Plan) int {
	return len(bundleOperators(plan))
}

// encodeNodes is nodes as node-model JSON objects, for a snapshot envelope.
func encodeNodes(nodes []*nodemodel.Node) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, len(nodes))
	for i, n := range nodes {
		raw, err := n.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("encode node %d: %w", i, err)
		}
		out[i] = raw
	}
	return out, nil
}

// decodeNodes reads node-model JSON objects. ok is false when any of them
// does not decode, and the caller parses the raw text instead.
func decodeNodes(raws []json.RawMessage) ([]*nodemodel.Node, bool) {
	out := make([]*nodemodel.Node, len(raws))
	for i, raw := range raws {
		out[i] = &nodemodel.Node{}
		if err := out[i].UnmarshalJSON(raw); err != nil {
			return nil, false
		}
	}
	return out, true
}
