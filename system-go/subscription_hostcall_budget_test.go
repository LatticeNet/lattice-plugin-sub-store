package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// graphBudgetHost counts every broker round trip, including KV, exactly as the
// production runner does. An optional limit makes the next call fail, mirroring
// the runner's host_calls boundary rather than merely comparing a static table.
type graphBudgetHost struct {
	*kvHostCaller
	t             *testing.T
	total         int
	limit         int
	compose       json.RawMessage
	options       json.RawMessage
	publishedBody string
}

func (host *graphBudgetHost) call(method string, params any) (json.RawMessage, error) {
	host.total++
	if host.limit > 0 && host.total > host.limit {
		return nil, fmt.Errorf("plugin exceeded host-call limit %d", host.limit)
	}
	switch method {
	case latticeplugin.HostMethodRPCCall:
		encoded, _ := json.Marshal(params)
		var call struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(encoded, &call)
		if call.Method == "graph_options" {
			return append(json.RawMessage(nil), host.options...), nil
		}
		return append(json.RawMessage(nil), host.compose...), nil
	case latticeplugin.HostMethodHTTPOperatorDo:
		encoded, _ := json.Marshal(params)
		var request struct {
			Body string `json:"body"`
		}
		_ = json.Unmarshal(encoded, &request)
		host.publishedBody = request.Body
		return json.RawMessage(`{"status_code":200}`), nil
	case latticeplugin.HostMethodHTTPDo:
		return json.RawMessage(`{"status_code":200,"body_base64":"` + base64.StdEncoding.EncodeToString([]byte("unused")) + `"}`), nil
	default:
		return host.kvHostCaller.call(method, params)
	}
}

func newGraphBudgetRuntime(t *testing.T) (*runtime, *graphBudgetHost) {
	t.Helper()
	host := &graphBudgetHost{t: t, kvHostCaller: newKVHostCaller(), compose: canonicalGraphResponseForIdentity(t, "identity-a", []string{graphRootA}), options: canonicalGraphOptionsResponse(t)}
	return &runtime{host: host, engine: testEngineWithHeadroom()}, host
}

func seedGraphBudgetStore(t *testing.T, rt *runtime) {
	t.Helper()
	members := make([]string, 0, maxCollectionMembers)
	for i := 0; i < maxCollectionMembers; i++ {
		id := fmt.Sprintf("graph-%02d", i)
		members = append(members, id)
		if err := rt.saveSubscription(subscriptionRecord{ID: id, Name: id, Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "all-graphs", Name: "all-graphs", Kind: kindCollection, Members: members}); err != nil {
		t.Fatal(err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "script-graphs", Name: "script-graphs", Kind: kindFile, FileType: fileTypeScript, NodeSource: "all-graphs", Content: `$content = "ok";`}); err != nil {
		t.Fatal(err)
	}
}

func TestGraphHostCallBudgetsMatchProductionReachablePaths(t *testing.T) {
	const (
		graphOptionsCalls = 1
		// fetch: record read, one compose, and the refresh bookkeeping's own
		// read and write — the outcome must land on the row either way.
		graphFetchCalls = 4
		// save: the options reload that validates the selection, then the
		// single-document flow — one load, one write.
		graphSaveCalls       = 3
		graphPreviewCalls    = 2
		graphRenderCalls     = 2
		graphPublishCalls    = 3
		maxGraphRenderCalls  = 68
		maxGraphPreviewCalls = 2
		maxGraphPublishCalls = 69
	)

	tests := []struct {
		name   string
		method string
		body   map[string]any
		want   int
		seed   bool
		wantOK bool
	}{
		{name: "options", method: "graph_options", body: map[string]any{}, want: graphOptionsCalls, wantOK: true},
		{name: "fetch", method: "fetch", body: map[string]any{"subscription_id": "graph-00"}, want: graphFetchCalls, seed: true, wantOK: true},
		{name: "save", method: "save", body: map[string]any{"subscription": map[string]any{"id": "new-graph", "name": "new-graph", "source": subscriptionSourceVPNCoreGraph, "vpn_identity": "identity-a", "entry_roots": []string{graphRootA}, "graph_options_version": "ov1:" + repeatHex("a")}}, want: graphSaveCalls, seed: true, wantOK: true},
		{name: "direct preview", method: "preview", body: map[string]any{"subscription_id": "graph-00"}, want: graphPreviewCalls, seed: true, wantOK: true},
		{name: "direct render", method: "render", body: map[string]any{"subscription_id": "graph-00", "format": "plain"}, want: graphRenderCalls, seed: true, wantOK: true},
		{name: "direct publish", method: "publish", body: map[string]any{"subscription_id": "graph-00", "destination": "https://destination.invalid/graph", "format": "plain"}, want: graphPublishCalls, seed: true, wantOK: true},
		{name: "64 graph collection render", method: "render", body: map[string]any{"subscription_id": "all-graphs", "format": "plain"}, want: 66, seed: true, wantOK: true},
		{name: "node-source file preview fails before composition", method: "preview", body: map[string]any{"subscription_id": "script-graphs"}, want: maxGraphPreviewCalls, seed: true},
		{name: "script over 64 graph collection render", method: "render", body: map[string]any{"subscription_id": "script-graphs", "format": "plain"}, want: maxGraphRenderCalls, seed: true, wantOK: true},
		{name: "script over 64 graph collection publish", method: "publish", body: map[string]any{"subscription_id": "script-graphs", "destination": "https://destination.invalid/graph", "format": "plain"}, want: maxGraphPublishCalls, seed: true, wantOK: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt, host := newGraphBudgetRuntime(t)
			if test.seed {
				seedGraphBudgetStore(t, rt)
			}
			host.total = 0
			response := callSubscription(t, rt, test.method, test.body)
			if response.OK != test.wantOK || host.total != test.want {
				t.Fatalf("response=%+v calls=%d want=%d", response, host.total, test.want)
			}
			budget := ackedRuntimeBudgets()[pluginID+"/subscription/"+test.method]
			if host.total > budget.HostCalls {
				t.Fatalf("production path needs %d calls, signed budget is %d", host.total, budget.HostCalls)
			}
		})
	}

	for _, method := range []string{"preview", "render", "publish"} {
		t.Run(method+" rejects one extra call", func(t *testing.T) {
			rt, host := newGraphBudgetRuntime(t)
			seedGraphBudgetStore(t, rt)
			budget := ackedRuntimeBudgets()[pluginID+"/subscription/"+method].HostCalls
			// Model one additional broker call introduced before the measured path.
			// The real path itself exactly consumes the signed cap.
			host.total = 1
			host.limit = budget
			body := map[string]any{"subscription_id": "script-graphs", "format": "plain"}
			if method == "preview" {
				body["subscription_id"] = "graph-00"
			}
			if method == "publish" {
				body["destination"] = "https://destination.invalid/graph"
			}
			if response := callSubscription(t, rt, method, body); response.OK {
				t.Fatalf("%s unexpectedly accepted host call %d beyond cap %d", method, budget+1, budget)
			}
		})
	}
}

func repeatHex(value string) string {
	result := ""
	for len(result) < 64 {
		result += value
	}
	return result[:64]
}

// The runner kills an invocation the moment it makes one more host call than
// the signed manifest allows, and the operator sees a bare 502. The budgets
// were originally set by estimating call shapes, and production proved the
// estimates wrong in several places at once — 2026-08-11: duplicating a
// script-built file 502'd, because `get` reads the records document AND the
// program key against a budget of 1, and three sibling methods were under by
// the same kind of miscount. Estimating is how that happened, so this file
// measures instead: each method runs against a seeded store through the real
// dispatch, its host round trips are counted, and the count is pinned to an
// exact expectation that must also fit the acked budget table (the same
// numbers the signed manifest is pinned to). A code change that adds a host
// call fails here until the budget is consciously re-acked.

// budgetCountingHost is the in-memory KV plus canned network answers, counting
// every round trip the way the runner's host_calls budget does: one per call,
// whatever the method.
type budgetCountingHost struct {
	*kvHostCaller
	total       int
	exportLinks []string
	remoteBody  string
}

func (c *budgetCountingHost) call(method string, params any) (json.RawMessage, error) {
	c.total++
	switch method {
	case latticeplugin.HostMethodRPCCall:
		links := c.exportLinks
		if links == nil {
			links = []string{scriptNodeHome}
		}
		return json.Marshal(map[string]any{"links": links})
	case latticeplugin.HostMethodHTTPDo, latticeplugin.HostMethodHTTPOperatorDo:
		body := c.remoteBody
		if body == "" {
			body = scriptNodeHome
		}
		return json.Marshal(map[string]any{
			"status_code": 200,
			"body_base64": base64.StdEncoding.EncodeToString([]byte(body)),
		})
	default:
		return c.kvHostCaller.call(method, params)
	}
}

func newCountingRuntime(t *testing.T) (*runtime, *budgetCountingHost) {
	t.Helper()
	host := &budgetCountingHost{kvHostCaller: newKVHostCaller()}
	return &runtime{host: host, engine: testEngineWithHeadroom()}, host
}

// Seed records shared by the scenarios: one of each source shape, a collection
// over two remote subs, and a script-built file drawing its nodes from that
// collection — the same shape as the operator's real store, which is the one
// that found the undercounts.
func seedBudgetStore(t *testing.T, rt *runtime) {
	t.Helper()
	records := []subscriptionRecord{
		{ID: "local-a", Name: "local-a", Source: subscriptionSourceLocal, Content: scriptNodeHome},
		{ID: "remote-a", Name: "remote-a", Source: subscriptionSourceRemote, URL: "https://provider.example/a"},
		{ID: "remote-b", Name: "remote-b", Source: subscriptionSourceRemote, URL: "https://provider.example/b"},
		{ID: "vpn-a", Name: "vpn-a", Source: subscriptionSourceVPNCore},
		{ID: "coll", Name: "coll", Kind: kindCollection, Members: []string{"remote-a", "remote-b"}},
		{
			ID: "scripty", Name: "scripty", Kind: kindFile, FileType: fileTypeScript,
			NodeSource: "coll", Content: `$content = "ok";`,
		},
	}
	for _, rec := range records {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatalf("seed %s: %v", rec.ID, err)
		}
	}
}

func TestHostCallCountsStayWithinAckedBudgets(t *testing.T) {
	scenarios := []struct {
		name    string
		method  string
		payload map[string]any
		// want is the exact number of host round trips the method should make.
		// Lower is a free win — update the pin and take it. Higher must be
		// justified in the acked budget table before it can merge.
		want int
	}{
		// The management reads.
		{name: "list", method: "list", payload: map[string]any{}, want: 1},
		{name: "get a plain sub", method: "get", payload: map[string]any{"subscription_id": "local-a"}, want: 1},
		// The records document plus the program's own key. Production died here
		// with the budget at 1.
		{name: "get a script file", method: "get", payload: map[string]any{"subscription_id": "scripty"}, want: 2},
		// Save loads the document once, keeps provenance from the in-memory copy,
		// writes the program key for a script, and writes the document once. No
		// read-back: the response is built from what was just written.
		{name: "save a new plain sub", method: "save", payload: map[string]any{"subscription": map[string]any{
			"id": "new-plain", "name": "new-plain", "content": scriptNodeHome,
		}}, want: 2},
		{name: "save a new script file", method: "save", payload: map[string]any{"subscription": map[string]any{
			"id": "new-script", "name": "new-script", "kind": kindFile, "file_type": fileTypeScript,
			"content": `$content = "new";`,
		}}, want: 3},
		{name: "re-save an existing script file", method: "save", payload: map[string]any{"subscription": map[string]any{
			"id": "scripty", "name": "scripty", "kind": kindFile, "file_type": fileTypeScript,
			"node_source": "coll", "content": `$content = "ok";`,
		}}, want: 3},
		// Turning a script file into a plain sub clears the orphaned program key
		// after the document write: load, write, clear.
		{name: "save over a script file with a plain sub", method: "save", payload: map[string]any{"subscription": map[string]any{
			"id": "scripty", "name": "scripty", "content": scriptNodeHome,
		}}, want: 3},
		// Delete reads and writes the document; only a script file pays the
		// third call to clear its program key.
		{name: "delete a plain sub", method: "delete", payload: map[string]any{"subscription_id": "local-a"}, want: 2},
		{name: "delete a script file", method: "delete", payload: map[string]any{"subscription_id": "scripty"}, want: 3},
		// Fetch: read the record, one network read, then the refresh bookkeeping
		// is its own document read and write.
		{name: "fetch a remote sub", method: "fetch", payload: map[string]any{"subscription_id": "remote-a"}, want: 4},
		{name: "fetch a vpn-core sub", method: "fetch", payload: map[string]any{"subscription_id": "vpn-a"}, want: 4},
		// A collection's refresh resolves every member: record, member list, one
		// fetch per remote member, bookkeeping.
		{name: "fetch a collection of remote subs", method: "fetch", payload: map[string]any{"subscription_id": "coll"}, want: 6},
		// A script file's refresh resolves its node source the same way, plus the
		// program key on the initial read.
		{name: "fetch a script file over a remote collection", method: "fetch", payload: map[string]any{"subscription_id": "scripty"}, want: 8},
		// Renders. A plain local sub is one read; the engine runs in-process.
		{name: "render a plain local sub", method: "render", payload: map[string]any{"subscription_id": "local-a", "format": "plain"}, want: 1},
		// A collection render reads the record, lists its members, then pays one
		// provider fetch per remote member.
		{name: "render a collection of remote subs", method: "render", payload: map[string]any{"subscription_id": "coll", "format": "plain"}, want: 4},
		// The operator's real shape: a script file over a collection of two
		// remote subs. Document + program, the source record, the collection's
		// member list, then one provider fetch per member.
		{name: "render a script file over a remote collection", method: "render", payload: map[string]any{"subscription_id": "scripty", "format": "plain"}, want: 6},
		// With the refresh path's snapshot in hand, the same renders pay no
		// network at all: the document read (plus the program key) is the whole
		// cost. This pair is the serve path's steady state.
		{name: "render a script file from its snapshot", method: "render", payload: map[string]any{
			"subscription_id": "scripty", "format": "plain",
			"raw": `{"source_id":"coll","source_name":"coll","source_kind":"collection","members":[{"sub_name":"remote-a","raw":"` + "vless://11111111-1111-1111-1111-111111111111@a.example:443?security=reality&sni=a.com&fp=chrome&pbk=x#HK-01" + `"}]}`,
		}, want: 2},
		{name: "render a collection from its snapshot", method: "render", payload: map[string]any{
			"subscription_id": "coll", "format": "plain",
			"raw": `{"members":[{"sub_name":"remote-a","raw":"` + "vless://11111111-1111-1111-1111-111111111111@a.example:443?security=reality&sni=a.com&fp=chrome&pbk=x#HK-01" + `"}]}`,
		}, want: 1},
		// A combination preview renders its members: record, member list, one
		// fetch per remote member.
		{name: "preview a combination of remote subs", method: "preview", payload: map[string]any{"subscription_id": "coll"}, want: 4},
		{name: "preview a saved local sub", method: "preview", payload: map[string]any{"subscription_id": "local-a"}, want: 1},
		// Export reads the document and settings, then reattaches every script
		// program — a backup without them is not a backup.
		{name: "export", method: "export", payload: map[string]any{}, want: 3},
		// Publish renders (one read for a local sub) and sends once.
		{name: "publish a local sub", method: "publish", payload: map[string]any{
			"subscription_id": "local-a", "destination": "https://operator.example/hook",
		}, want: 2},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			rt, host := newCountingRuntime(t)
			seedBudgetStore(t, rt)
			host.total = 0

			res := callSubscription(t, rt, scenario.method, scenario.payload)
			if !res.OK {
				t.Fatalf("%s failed: %s", scenario.method, res.Error)
			}
			if host.total != scenario.want {
				t.Errorf("%s made %d host calls, pinned at %d", scenario.method, host.total, scenario.want)
			}
			budget, ok := ackedRuntimeBudgets()[pluginID+"/subscription/"+scenario.method]
			if !ok {
				t.Fatalf("%s has no acked budget entry", scenario.method)
			}
			if host.total > budget.HostCalls {
				t.Errorf("%s made %d host calls, over the acked budget of %d — this 502s in production", scenario.method, host.total, budget.HostCalls)
			}
		})
	}
}

// A combination's row eye used to report "no content" — the record has no
// content of its own and the preview path never looked at its members. The
// preview is the merged member render: both canned members appear.
// A script file's preview is refused, not rendered: preview is substore:read,
// and rendering a script file means executing a stored program over fetched
// node-source content — host-capable work that belongs to render and publish.
// The refusal must also be cheap: only the record lookup touches the host
// (document plus the script's program key).
func TestPreviewOfScriptFileIsRefusedWithoutHostWork(t *testing.T) {
	rt, host := newCountingRuntime(t)
	seedBudgetStore(t, rt)
	host.total = 0
	res := callSubscription(t, rt, "preview", map[string]any{"subscription_id": "scripty"})
	if res.OK || res.Error != "file preview does not expose node-source content" {
		t.Fatalf("expected the node-source refusal, got %+v", res)
	}
	if host.total != 2 {
		t.Errorf("refusal made %d host calls, pinned at 2 — it must not resolve the node source", host.total)
	}
}

func TestPreviewCombinationMergesMemberNodes(t *testing.T) {
	rt, _ := newCountingRuntime(t)
	seedBudgetStore(t, rt)

	var out previewResult
	decodeResult(t, callSubscription(t, rt, "preview", map[string]any{"subscription_id": "coll"}), &out)
	if out.NodeCount != 2 {
		t.Fatalf("combination preview = %d nodes, want one per member (2)", out.NodeCount)
	}
	if out.Nodes[0].Server == "" || out.Nodes[0].Port == "" {
		t.Fatalf("preview node lost its endpoint: %+v", out.Nodes[0])
	}
}

// An export that leaves script programs behind is a backup that cannot be
// restored: the records it carries name script files whose content is nowhere
// in the file, and importing it skips every one of them as "needs a template".
// The round trip is the assertion that matters.
func TestExportBackupCarriesScriptPrograms(t *testing.T) {
	rt, _ := newCountingRuntime(t)
	seedBudgetStore(t, rt)

	var exported struct {
		Backup string `json:"backup"`
	}
	decodeResult(t, callSubscription(t, rt, "export", map[string]any{}), &exported)
	if !strings.Contains(exported.Backup, `$content = \"ok\";`) {
		t.Fatal("export dropped the script file's program; the backup cannot be restored")
	}

	fresh, _ := newCountingRuntime(t)
	var outcome importOutcome
	decodeResult(t, callSubscription(t, fresh, "import", map[string]any{"backup": exported.Backup}), &outcome)
	if len(outcome.Skipped) != 0 {
		t.Fatalf("re-importing the export skipped records: %v", outcome.Skipped)
	}
	restored, err := fresh.getSubscription("scripty")
	if err != nil {
		t.Fatalf("get restored script file: %v", err)
	}
	if restored.Content != `$content = "ok";` {
		t.Fatalf("restored script = %q, program lost in the round trip", restored.Content)
	}
}
