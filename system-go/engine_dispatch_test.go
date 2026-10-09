package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/parse"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/producers"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// dispatchRuntime is a store-backed runtime on an engine of its own, booted
// warm, so a test reads that engine's path counters without another test's
// calls in them, and a call that should not reach the warm runtime has one
// ready to reach.
func dispatchRuntime(t *testing.T) *runtime {
	t.Helper()
	engine := testEngineWithHeadroom()
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	return &runtime{host: newKVHostCaller(), engine: engine}
}

// savedRecord stores rec and reads it back with its revision.
func savedRecord(t *testing.T, rt *runtime, rec subscriptionRecord) subscriptionRecord {
	t.Helper()
	if rec.Name == "" {
		rec.Name = rec.ID
	}
	if err := rt.saveSubscription(rec); err != nil {
		t.Fatalf("save %s: %v", rec.ID, err)
	}
	stored, err := rt.getSubscription(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

const (
	stepRegexFilter  = `{"type":"Regex Filter","args":{"regex":["node"],"keep":true}}`
	stepRename       = `{"type":"Regex Rename Operator","args":[{"expr":"^node-","now":"edge-"}]}`
	stepSort         = `{"type":"Sort Operator","args":"asc"}`
	stepFlag         = `{"type":"Flag Operator","args":{"mode":"add"}}`
	stepResolve      = `{"type":"Resolve Domain Operator","args":{"provider":"Google","type":"IPv4","cache":false}}`
	stepScript       = `{"type":"Script Operator","args":{"content":"function operator(proxies){ return proxies.map(p => ({...p, name: p.name + '-tagged'})); }"}}`
	stepScriptFilter = `{"type":"Script Filter","args":{"content":"function filter(proxies){ return proxies.map(() => true); }"}}`
	stepLookahead    = `{"type":"Regex Filter","args":{"regex":["^(?!.*hy2).*$"],"keep":true}}`
	stepBadShape     = `{"type":"Regex Sort Operator","args":{"expressions":["^node"],"order":"random"}}`
	stepResponse     = `{"type":"Response Transformer","args":{"content":"function transform(r){ return r; }"}}`
)

// fallbackTargets are the targets convert allows that have no Go producer in
// S1: the bundle answers them.
func fallbackTargets() []string {
	var out []string
	for target := range subscriptionConvertTargets {
		if !producers.Native(target) {
			out = append(out, target)
		}
	}
	sort.Strings(out)
	return out
}

var nativeTargets = []string{"URI", "V2Ray", "JSON", "sing-box", "ClashMeta"}

func TestNativeRouteRequiresNativeTargetAndChain(t *testing.T) {
	disabledScript := strings.Replace(stepScript, `"type":"Script Operator"`, `"type":"Script Operator","disabled":true`, 1)
	cases := []struct {
		name   string
		chain  []json.RawMessage
		target string
		want   bool
	}{
		{"no chain, native target", nil, "sing-box", true},
		{"no chain, fallback target", nil, "Loon", false},
		{"native chain, native target", steps(stepRegexFilter, stepRename), "ClashMeta", true},
		{"native chain, a native target's alias", steps(stepRegexFilter), "clash.meta", true},
		{"native chain, fallback target", steps(stepRegexFilter), "Surge", false},
		{"native chain, legacy Clash", steps(stepRegexFilter), "Clash", false},
		{"a Resolve Domain step", steps(stepRegexFilter, stepResolve), "sing-box", false},
		{"a Script Operator", steps(stepScript), "URI", false},
		{"a Script Filter", steps(stepRegexFilter, stepScriptFilter), "URI", false},
		{"a pattern RE2 refuses", steps(stepLookahead), "URI", false},
		{"arguments only the bundle reads", steps(stepBadShape), "URI", false},
		{"a disabled script step", steps(stepRegexFilter, disabledScript), "URI", true},
		{"a Response Transformer", steps(stepRegexFilter, stepResponse), "JSON", true},
	}
	host := &scriptHTTPHost{
		body:   dnsAnswer("keep.example.com", [4]byte{1, 2, 3, 4}),
		header: map[string]string{"Content-Type": "application/dns-message"},
	}
	rt := dispatchRuntime(t)
	defer rt.engine.attachScriptHTTP(newScriptHTTPGateway(host))()
	document := shareFleetFixture(3)
	for _, c := range cases {
		var plan *operators.Plan
		if c.chain != nil {
			var err error
			if plan, err = operators.Compile("route", c.chain); err != nil {
				t.Fatalf("%s: compile: %v", c.name, err)
			}
		}
		if got := nativeRoute(plan, c.target); got != c.want {
			t.Fatalf("%s: nativeRoute = %v, want %v", c.name, got, c.want)
		}
		warm, isolated := rt.engine.pathCounts()
		_, served, err := rt.convertNodes(nodeConvertRequest{Parts: []string{document}, Target: c.target, Plan: plan})
		if err != nil {
			t.Fatalf("%s: convert: %v", c.name, err)
		}
		warmAfter, isolatedAfter := rt.engine.pathCounts()
		want, wantIsolated := servedIsolated, isolated+1
		if c.want {
			want, wantIsolated = servedNative, isolated
		}
		if served != want || warmAfter != warm || isolatedAfter != wantIsolated {
			t.Fatalf("%s: served %s with warm %d->%d and isolated %d->%d, want %s on %d isolated calls and no warm one",
				c.name, served, warm, warmAfter, isolated, isolatedAfter, want, wantIsolated)
		}
	}
	// A collection member whose chain ran on the bundle sends the whole
	// collection there, however native its own chain and target are.
	warm, isolated := rt.engine.pathCounts()
	_, served, err := rt.convertNodes(nodeConvertRequest{Parts: []string{document}, Target: "sing-box", MemberFallback: true})
	if warmAfter, isolatedAfter := rt.engine.pathCounts(); err != nil || served != servedIsolated || warmAfter != warm || isolatedAfter != isolated+1 {
		t.Fatalf("member fallback: served %s err %v, warm %d->%d isolated %d->%d", served, err, warm, warmAfter, isolated, isolatedAfter)
	}
}

// The acceptance of slices.md:61: every target without a Go producer is
// answered by the bundle on its isolated path, even though the warm runtime
// is up, and the five native targets touch neither runtime.
func TestFallbackTargetsRenderOnIsolatedPath(t *testing.T) {
	rt := dispatchRuntime(t)
	rec := savedRecord(t, rt, subscriptionRecord{ID: "fleet", Content: shareFleetFixture(3)})
	plan, err := rt.chainPlan(rec)
	if err != nil {
		t.Fatal(err)
	}
	targets := fallbackTargets()
	if want := []string{"Clash", "Egern", "Loon", "QX", "Shadowrocket", "Stash", "Surfboard", "Surge", "SurgeMac"}; !slices.Equal(targets, want) {
		t.Fatalf("fallback targets = %v, want the nine of %v", targets, want)
	}
	for _, target := range append(targets, nativeTargets...) {
		native := producers.Native(target)
		warm, isolated := rt.engine.pathCounts()
		out, served, err := rt.convertNodes(nodeConvertRequest{Parts: []string{rec.Content}, Target: target, Plan: plan})
		if err != nil || strings.TrimSpace(out.Output) == "" {
			t.Fatalf("%s: convert err=%v output=%q", target, err, head(out.Output, 80))
		}
		warmAfter, isolatedAfter := rt.engine.pathCounts()
		switch {
		case native && (served != servedNative || isolatedAfter != isolated || warmAfter != warm):
			t.Fatalf("%s: served %s, warm %d->%d isolated %d->%d; a native target touches neither runtime", target, served, warm, warmAfter, isolated, isolatedAfter)
		case !native && (served != servedIsolated || isolatedAfter != isolated+1 || warmAfter != warm):
			t.Fatalf("%s: served %s, warm %d->%d isolated %d->%d; want one isolated call and no warm one", target, served, warm, warmAfter, isolated, isolatedAfter)
		}
		rendered, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: rec.ID, Target: target, Format: "plain"})
		if err != nil || rendered.Target != target {
			t.Fatalf("%s: render target=%q err=%v", target, rendered.Target, err)
		}
		wantIsolated := isolatedAfter + 1
		if native {
			wantIsolated = isolatedAfter
		}
		if w, i := rt.engine.pathCounts(); w != warm || i != wantIsolated {
			t.Fatalf("%s: render moved warm %d->%d isolated %d->%d, want isolated %d", target, warmAfter, w, isolatedAfter, i, wantIsolated)
		}
	}
}

// A chain with a Resolve Domain step renders whole on the isolated path: the
// native Regex Rename before it runs there too, in order, and the warm
// runtime, which could run both, is not used.
func TestResolveDomainChainRendersWholeOnIsolatedPath(t *testing.T) {
	host := &scriptHTTPHost{
		body:   dnsAnswer("keep.example.com", [4]byte{1, 2, 3, 4}),
		header: map[string]string{"Content-Type": "application/dns-message"},
	}
	rt := dispatchRuntime(t)
	defer rt.engine.attachScriptHTTP(newScriptHTTPGateway(host))()
	rename := `{"type":"Regex Rename Operator","args":[{"expr":"^Keep$","now":"Renamed"}]}`
	savedRecord(t, rt, subscriptionRecord{ID: "resolve", Content: scriptHTTPNode, Process: steps(rename, stepResolve)})
	warm, isolated := rt.engine.pathCounts()
	out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "resolve", Target: "sing-box", Format: "plain"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out.Content, `"1.2.3.4"`) || !strings.Contains(out.Content, `"Renamed"`) {
		t.Fatalf("the chain did not run whole: %s", head(out.Content, 300))
	}
	if w, i := rt.engine.pathCounts(); w != warm || i != isolated+1 {
		t.Fatalf("warm %d->%d isolated %d->%d, want one isolated call", warm, w, isolated, i)
	}
	if len(host.seen()) == 0 {
		t.Fatal("Resolve Domain never asked the resolver")
	}
}

// A chain with a script step renders whole on the isolated path: the native
// Regex Filter before the script runs there too.
func TestScriptStepChainRendersWholeOnIsolatedPath(t *testing.T) {
	rt := dispatchRuntime(t)
	filter := `{"type":"Regex Filter","args":{"regex":["^node-00[0-2]$"],"keep":true}}`
	savedRecord(t, rt, subscriptionRecord{ID: "scripted", Content: shareFleetFixture(6), Process: steps(filter, stepScript)})
	warm, isolated := rt.engine.pathCounts()
	out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "scripted", Target: "ClashMeta", Format: "plain", Explain: true})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out.NodeCount != 3 || strings.Count(out.Content, "-tagged") != 3 || strings.Contains(out.Content, "node-003") || strings.Contains(out.Content, "ss-1") {
		t.Fatalf("the filter and the script did not both run: nodes=%d %s", out.NodeCount, head(out.Content, 300))
	}
	if w, i := rt.engine.pathCounts(); w != warm || i != isolated+1 {
		t.Fatalf("warm %d->%d isolated %d->%d, want one isolated call", warm, w, isolated, i)
	}
}

// A record's chain is compiled once per revision and reused by every render
// of it; an edit is a new revision and compiles again.
func TestPlanCacheCompilesEachRevisionOnce(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	compiles := func() int {
		nativePlans.mu.Lock()
		defer nativePlans.mu.Unlock()
		return nativePlans.compiles
	}
	// The id makes the revision one no other test has compiled.
	id := fmt.Sprintf("plan-cache-%d", compiles())
	savedRecord(t, rt, subscriptionRecord{ID: id, Content: shareFleetFixture(3), Process: steps(stepRename, stepSort)})
	before := compiles()
	var outputs []string
	for i := 0; i < 3; i++ {
		out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: id, Target: "sing-box", Format: "plain"})
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, out.Content)
	}
	if got := compiles() - before; got != 1 {
		t.Fatalf("three renders of one revision compiled %d times, want 1", got)
	}
	if outputs[0] != outputs[1] || outputs[1] != outputs[2] {
		t.Fatal("a cached plan rendered differently on reuse")
	}
	savedRecord(t, rt, subscriptionRecord{ID: id, Content: shareFleetFixture(3), Process: steps(stepRename, stepSort, stepFlag)})
	if _, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: id, Target: "sing-box", Format: "plain"}); err != nil {
		t.Fatal(err)
	}
	if got := compiles() - before; got != 2 {
		t.Fatalf("an edited chain compiled %d times in all, want 2", got)
	}

	// The cache is bounded: the oldest revision leaves first.
	cache := newPlanCache()
	for i := 0; i <= planCacheEntries; i++ {
		if _, err := cache.plan(fmt.Sprintf("rev-%d", i), steps(stepSort)); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.plans) != planCacheEntries || cache.plans["rev-0"] != nil || cache.plans[fmt.Sprintf("rev-%d", planCacheEntries)] == nil {
		t.Fatalf("cache holds %d plans, rev-0 kept=%v", len(cache.plans), cache.plans["rev-0"] != nil)
	}
	// A chain that belongs to no stored record is compiled and not kept.
	if _, err := cache.plan("", steps(stepSort)); err != nil || len(cache.plans) != planCacheEntries || cache.plans[""] != nil {
		t.Fatalf("an anonymous chain was cached: %d plans, err %v", len(cache.plans), err)
	}
}

// A refresh of a record whose chain runs in Go counts the nodes after it, so
// the record table's "nodes out" column has a value without a render; a chain
// on the bundle leaves it empty rather than stale.
func TestFetchRecordsNodesOutForANativeChain(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	filter := `{"type":"Regex Filter","args":{"regex":["^node-"],"keep":true}}`
	// shareFleetFixture(4): four VLESS nodes named node-, two Hysteria2 and
	// two ss.
	savedRecord(t, rt, subscriptionRecord{ID: "out", Source: subscriptionSourceLocal, Content: shareFleetFixture(4), Process: steps(filter)})
	if res := callSubscription(t, rt, "fetch", map[string]any{"subscription_id": "out"}); !res.OK {
		t.Fatalf("fetch: %s", res.Error)
	}
	row := func() listView {
		t.Helper()
		res := callSubscription(t, rt, "list", map[string]any{})
		var out struct {
			Subscriptions []listView `json:"subscriptions"`
		}
		if err := json.Unmarshal(res.Result, &out); err != nil {
			t.Fatal(err)
		}
		for _, view := range out.Subscriptions {
			if view.ID == "out" {
				return view
			}
		}
		t.Fatal("record not listed")
		return listView{}
	}
	view := row()
	if view.NodesIn == nil || *view.NodesIn != 8 || view.NodesOut == nil || *view.NodesOut != 4 {
		t.Fatalf("nodes in/out = %v/%v, want 8/4", view.NodesIn, view.NodesOut)
	}
	savedRecord(t, rt, subscriptionRecord{ID: "out", Source: subscriptionSourceLocal, Content: shareFleetFixture(4), Process: steps(filter, stepScript)})
	if res := callSubscription(t, rt, "fetch", map[string]any{"subscription_id": "out"}); !res.OK {
		t.Fatalf("fetch: %s", res.Error)
	}
	if view := row(); view.NodesIn == nil || *view.NodesIn != 8 || view.NodesOut != nil {
		t.Fatalf("with a script step, nodes in/out = %v/%v, want 8/none", view.NodesIn, view.NodesOut)
	}
}

// Two invocations overlap in production (one worker plus one overflow), and
// the plan cache is the worker's: concurrent lookups of one revision and of
// many get one plan per revision and no data race (run with -race).
func TestPlanCacheIsSafeForConcurrentInvocations(t *testing.T) {
	cache := newPlanCache()
	chain := steps(stepRename, stepSort)
	const workers = 8
	plans := make([][]*operators.Plan, workers)
	done := make(chan int)
	for w := 0; w < workers; w++ {
		go func(w int) {
			for i := 0; i < 64; i++ {
				plan, err := cache.plan(fmt.Sprintf("rev-%d", i%16), chain)
				if err != nil {
					t.Error(err)
				}
				plans[w] = append(plans[w], plan)
			}
			done <- w
		}(w)
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	for w := 1; w < workers; w++ {
		for i := range plans[w] {
			if plans[w][i] != plans[0][i] {
				t.Fatalf("worker %d got a second plan for rev-%d", w, i%16)
			}
		}
	}
	if len(cache.plans) != 16 {
		t.Fatalf("cache holds %d plans, want 16", len(cache.plans))
	}
}

// A native render explains from the producer's own drop report: every node
// that yielded no entry, a support-map refusal and a VLESS Reality block
// without a public key alike, and include-unsupported-proxy rescues only the
// first. A carrier check counts what the URI output loses, leaving out the
// node no client would get anyway.
func TestNativeRenderExplainsWhatTheProducerDropped(t *testing.T) {
	rt := dispatchRuntime(t)
	raws := perfgen.Nodes(4)
	var refused, broken map[string]any
	if err := json.Unmarshal(raws[2], &refused); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raws[3], &broken); err != nil {
		t.Fatal(err)
	}
	refused["supported"] = map[string]any{"sing-box": false, "URI": false}
	delete(broken["reality-opts"].(map[string]any), "public-key")
	raws[2], _ = json.Marshal(refused)
	raws[3], _ = json.Marshal(broken)
	decode := func() []*nodemodel.Node {
		nodes, ok := decodeNodes(raws)
		if !ok {
			t.Fatal("perfgen nodes do not decode")
		}
		return nodes
	}
	for _, c := range []struct {
		name    string
		options map[string]bool
		want    int
	}{
		{"by default", nil, 2},
		{"with include-unsupported-proxy", map[string]bool{"include-unsupported-proxy": true}, 1},
	} {
		out, served, err := rt.convertNodes(nodeConvertRequest{Nodes: decode(), Target: "sing-box", Options: c.options, Explain: true})
		if err != nil || served != servedNative {
			t.Fatalf("%s: served %s err %v", c.name, served, err)
		}
		if out.UnsupportedNodeCount != c.want || !slices.Equal(out.UnsupportedProtocols, []string{"vless"}) || out.NodeCount != 4 {
			t.Fatalf("%s: dropped %d %v of %d nodes, want %d vless", c.name, out.UnsupportedNodeCount, out.UnsupportedProtocols, out.NodeCount, c.want)
		}
	}
	out, _, err := rt.convertNodes(nodeConvertRequest{Nodes: decode(), Target: "URI", CarrierCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.CarrierLostNodeCount != 1 || !slices.Equal(out.CarrierLostProtocols, []string{"vless"}) || out.UnsupportedNodeCount != 0 {
		t.Fatalf("carrier lost %d %v (explained %d), want the one support-map refusal", out.CarrierLostNodeCount, out.CarrierLostProtocols, out.UnsupportedNodeCount)
	}
}

// A collection snapshot whose members carry no nodes renders whole on the
// bundle: a version 1 snapshot and an envelope written before member nodes
// existed hold member texts the bundle produced, and only an envelope whose
// members carry their nodes says every member chain ran in Go.
func TestCollectionSnapshotWithoutMemberNodesRendersOnTheBundle(t *testing.T) {
	rt := dispatchRuntime(t)
	savedRecord(t, rt, subscriptionRecord{ID: "a", Source: subscriptionSourceLocal, Content: shareFleetFixture(2)})
	savedRecord(t, rt, subscriptionRecord{ID: "b", Source: subscriptionSourceLocal, Content: shareFleetFixture(3)})
	savedRecord(t, rt, subscriptionRecord{ID: "c", Kind: kindCollection, Members: []string{"a", "b"}})
	snap, err := rt.fetchSubscription("c")
	if err != nil {
		t.Fatal(err)
	}
	withNodes := snap.Raw
	env, _ := decodeSnapshotEnvelope(withNodes)
	for i := range env.Members {
		env.Members[i].Nodes = nil
	}
	withoutNodes, _ := json.Marshal(env)
	v1 := snapshotText(withNodes)
	for _, c := range []struct {
		name     string
		raw      string
		isolated int
	}{
		{"an envelope whose members carry nodes", withNodes, 0},
		{"an envelope without member nodes", string(withoutNodes), 1},
		{"a version 1 snapshot", v1, 1},
	} {
		warm, isolated := rt.engine.pathCounts()
		out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "c", Target: "sing-box", Format: "plain", Raw: c.raw})
		if err != nil || out.Content == "" {
			t.Fatalf("%s: render err %v", c.name, err)
		}
		if w, i := rt.engine.pathCounts(); w != warm || i != isolated+c.isolated {
			t.Fatalf("%s: warm %d->%d isolated %d->%d, want %d isolated calls", c.name, warm, w, isolated, i, c.isolated)
		}
	}
}

// bodyHost is the in-memory store plus a provider that answers one body.
type bodyHost struct {
	*kvHostCaller
	body string
}

func (h *bodyHost) call(method string, params any) (json.RawMessage, error) {
	if method != latticeplugin.HostMethodHTTPDo {
		return h.kvHostCaller.call(method, params)
	}
	return json.Marshal(map[string]any{"status_code": 200, "body_base64": base64.StdEncoding.EncodeToString([]byte(h.body))})
}

// A provider body past the raw bound the core keeps is refused with the
// envelope's stated reason, snapshot_too_large, as it was before refresh
// counted in Go, and not with the parser's bound, which is the same size.
func TestOversizeProviderBodyIsRefusedAsTooLarge(t *testing.T) {
	body := strings.Repeat(strings.Join(perfgen.URIs(1024), "\n")+"\n", 24)
	if len(body) <= parse.MaxDocumentBytes {
		t.Fatalf("body is %d bytes, not past the bound", len(body))
	}
	rt := &runtime{host: &bodyHost{kvHostCaller: newKVHostCaller(), body: body}, engine: sharedWarmTestEngine(t)}
	savedRecord(t, rt, subscriptionRecord{ID: "big", Source: subscriptionSourceRemote, URL: "https://provider.example/big", UA: "x"})
	if _, err := rt.fetchSubscription("big"); err == nil || !strings.HasPrefix(err.Error(), snapshotTooLargeCode+":") {
		t.Fatalf("fetch of a %d-byte body = %v, want the %s refusal", len(body), err, snapshotTooLargeCode)
	}
}
