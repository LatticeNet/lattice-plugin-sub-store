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
		graphOptionsCalls    = 1
		graphFetchCalls      = 2
		graphSaveCalls       = 5
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
