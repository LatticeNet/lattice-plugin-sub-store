package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

const (
	graphRootA       = "11111111-1111-4111-8111-111111111111"
	graphRootB       = "22222222-2222-4222-8222-222222222222"
	graphCredentialA = "aaaaaaaa-aaaa-5aaa-8aaa-aaaaaaaaaaaa"
	graphCredentialB = "bbbbbbbb-bbbb-1bbb-8bbb-bbbbbbbbbbbb"
)

func graphCredential(root string) string {
	if root == graphRootA {
		return graphCredentialA
	}
	if root == graphRootB {
		return graphCredentialB
	}
	return "cccccccc-cccc-2ccc-8ccc-cccccccccccc"
}

type vpnCoreGraphHost struct {
	*kvHostCaller
	response        json.RawMessage
	optionsResponse json.RawMessage
	graphResponses  []json.RawMessage
	legacyLinks     []string
	calls           []map[string]any
	published       []string
}

func (h *vpnCoreGraphHost) call(method string, params any) (json.RawMessage, error) {
	if method == latticeplugin.HostMethodHTTPOperatorDo {
		encoded, _ := json.Marshal(params)
		var request struct {
			Body string `json:"body"`
		}
		_ = json.Unmarshal(encoded, &request)
		h.published = append(h.published, request.Body)
		return json.RawMessage(`{"status_code":200}`), nil
	}
	if method != latticeplugin.HostMethodRPCCall {
		return h.kvHostCaller.call(method, params)
	}
	encoded, _ := json.Marshal(params)
	var call map[string]any
	_ = json.Unmarshal(encoded, &call)
	h.calls = append(h.calls, call)
	if call["service"] == "latticenet.vpn-core/nodes" {
		return json.Marshal(map[string]any{"links": h.legacyLinks})
	}
	if call["method"] == "graph_options" {
		return append(json.RawMessage(nil), h.optionsResponse...), nil
	}
	if len(h.graphResponses) > 0 {
		response := h.graphResponses[0]
		h.graphResponses = h.graphResponses[1:]
		return append(json.RawMessage(nil), response...), nil
	}
	return append(json.RawMessage(nil), h.response...), nil
}

func canonicalGraphOptionsResponse(t *testing.T) json.RawMessage {
	t.Helper()
	response := vpnCoreGraphOptionsResponse{
		SchemaVersion:  1,
		OK:             true,
		OptionsVersion: "ov1:" + strings.Repeat("a", 64),
		Identities: []vpnCoreGraphIdentityOption{
			{ID: "identity-a", Label: "Primary", Status: "eligible", Selectable: true},
			{ID: "identity-b", Label: "Secondary", Status: "eligible", Selectable: true},
		},
		Roots: []vpnCoreGraphRootOption{
			{LineUUID: graphRootA, Label: "Source A", SourceNode: "node-a", Source: "managed", TargetLabel: "Target B", Status: "converged", PathSummary: "Source A → Target B (1 hop)", EligibleIdentityIDs: []string{"identity-a"}, Selectable: true},
			{LineUUID: graphRootB, Label: "Source B", SourceNode: "node-b", Source: "managed", Status: "converged", PathSummary: "Source B", EligibleIdentityIDs: []string{"identity-a", "identity-b"}, Selectable: true},
		},
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func canonicalGraphResponse(t *testing.T, roots []string) json.RawMessage {
	return canonicalGraphResponseForIdentity(t, "identity", roots)
}

func canonicalGraphResponseForIdentity(t *testing.T, identityID string, roots []string) json.RawMessage {
	t.Helper()
	manifest := model.SubscriptionSourceManifestV1{
		Schema: model.SubscriptionSourceManifestSchemaV1, Renderer: model.SubscriptionSourceRendererV1,
		Identity:   model.SubscriptionSourceManifestIdentity{ID: identityID, Generation: 3},
		EntryRoots: append([]string(nil), roots...), Entries: make([]model.SubscriptionSourceManifestEntry, 0, len(roots)),
	}
	entries := make([]string, 0, len(roots))
	for i, root := range roots {
		label := fmt.Sprintf("entry-%d", i+1)
		manifest.Entries = append(manifest.Entries, model.SubscriptionSourceManifestEntry{
			Root: root,
			Endpoint: model.SubscriptionSourceManifestEndpoint{LineUUID: root, NodeID: "node", Label: label, Host: "entry.example.com", Port: 443,
				SNI: "entry.example.com", Fingerprint: "chrome", ALPN: []string{}, PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ShortID: "0123456789abcdef", Flow: "xtls-rprx-vision"},
			Path:     []model.SubscriptionSourceManifestEdge{},
			Terminal: model.SubscriptionSourceManifestTerminal{LineUUID: root, Generation: uint64(i + 1), ObservationRevision: uint64(i + 1), Status: "converged"},
		})
		values := url.Values{"type": {"tcp"}, "encryption": {"none"}, "security": {"reality"}, "flow": {"xtls-rprx-vision"},
			"pbk": {manifest.Entries[i].Endpoint.PublicKey}, "sid": {manifest.Entries[i].Endpoint.ShortID}, "sni": {manifest.Entries[i].Endpoint.SNI}, "fp": {manifest.Entries[i].Endpoint.Fingerprint}}
		entries = append(entries, "vless://"+graphCredential(root)+"@"+net.JoinHostPort(manifest.Entries[i].Endpoint.Host, strconv.Itoa(manifest.Entries[i].Endpoint.Port))+"?"+values.Encode()+"#"+url.PathEscape(label))
	}
	manifestRaw, sourceVersion, err := model.CanonicalSubscriptionSourceManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(vpnCoreGraphComposeResponse{SchemaVersion: 1, OK: true, SourceVersion: sourceVersion,
		SourceManifest: manifestRaw, Entries: entries, Raw: strings.Join(entries, "\n")})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newVPNCoreGraphRuntime(t *testing.T, response json.RawMessage) (*runtime, *vpnCoreGraphHost) {
	t.Helper()
	host := &vpnCoreGraphHost{kvHostCaller: newKVHostCaller(), response: response}
	return &runtime{host: host, engine: testEngineWithHeadroom()}, host
}

func TestVPNCoreGraphComposesOrderedRootsWithOneHostCall(t *testing.T) {
	roots := []string{graphRootB, graphRootA}
	composeResponse := canonicalGraphResponse(t, roots)
	rt, host := newVPNCoreGraphRuntime(t, composeResponse)
	if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: roots, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	result, err := rt.fetchSubscription("graph")
	if err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 1 || host.calls[0]["service"] != vpnCoreGraphService || host.calls[0]["method"] != "compose" {
		t.Fatalf("host calls = %+v", host.calls)
	}
	request, ok := host.calls[0]["request"].(map[string]any)
	if !ok || request["schema_version"] != float64(1) || request["identity_id"] != "identity" {
		t.Fatalf("request = %+v", request)
	}
	gotRoots, _ := request["entry_roots"].([]any)
	if !reflect.DeepEqual(gotRoots, []any{graphRootB, graphRootA}) {
		t.Fatalf("root order = %+v", gotRoots)
	}
	if !strings.HasPrefix(result.Raw, "vless://"+graphCredentialB) || !strings.Contains(result.Raw, "\nvless://"+graphCredentialA) {
		t.Fatalf("raw order = %q", result.Raw)
	}
	if strings.Contains(result.Raw, "vless://"+graphRootA+"@") || strings.Contains(result.Raw, "vless://"+graphRootB+"@") {
		t.Fatalf("entry root was reused as a credential: %q", result.Raw)
	}
	var expected vpnCoreGraphComposeResponse
	if err := json.Unmarshal(composeResponse, &expected); err != nil {
		t.Fatal(err)
	}
	if result.SourceVersion != expected.SourceVersion || string(result.SourceManifest) != string(expected.SourceManifest) {
		t.Fatalf("fetch provenance = version %q manifest %s, want version %q manifest %s", result.SourceVersion, result.SourceManifest, expected.SourceVersion, expected.SourceManifest)
	}

	response := rt.handleSubscriptionCall(callPayload{Method: "fetch", Payload: mustJSON(map[string]string{"subscription_id": "graph"})})
	if !response.OK {
		t.Fatalf("fetch RPC failed: %+v", response)
	}
	var wire fetchResult
	if err := json.Unmarshal(response.Result, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.SourceVersion != expected.SourceVersion || string(wire.SourceManifest) != string(expected.SourceManifest) || wire.Raw != expected.Raw {
		t.Fatalf("fetch RPC dropped graph authority: %+v", wire)
	}
}

func TestVPNCoreGraphProbeNeverReturnsCredentialBearingAuthority(t *testing.T) {
	composeResponse := canonicalGraphResponse(t, []string{graphRootA})
	rt, _ := newVPNCoreGraphRuntime(t, composeResponse)
	if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	response := rt.handleSubscriptionCall(callPayload{Method: "probe", Payload: mustJSON(map[string]string{"subscription_id": "graph"})})
	if !response.OK {
		t.Fatalf("probe RPC failed: %+v", response)
	}
	var probe subscriptionProbeResult
	if err := json.Unmarshal(response.Result, &probe); err != nil {
		t.Fatal(err)
	}
	if !probe.OK || probe.Bytes == 0 || probe.SourceVersion == "" || probe.Stale {
		t.Fatalf("probe=%+v", probe)
	}
	for _, canary := range []string{graphCredentialA, "vless://", "source_manifest", "raw", "public_key"} {
		if strings.Contains(string(response.Result), canary) {
			t.Fatalf("browser-safe probe leaked %q: %s", canary, response.Result)
		}
	}

	failed := rt.handleSubscriptionCall(callPayload{Method: "probe", Payload: json.RawMessage(`{"subscription_id":"missing"}`)})
	if !failed.OK || strings.Contains(string(failed.Result), "vless://") || strings.Contains(string(failed.Result), graphCredentialA) {
		t.Fatalf("probe failure exposed diagnostics: %+v", failed)
	}
	var failure subscriptionProbeResult
	if err := json.Unmarshal(failed.Result, &failure); err != nil || failure.OK || failure.ErrorCode != "source_unavailable" || failure.Bytes != 0 || failure.SourceVersion != "" {
		t.Fatalf("failure=%+v err=%v", failure, err)
	}
}

func TestVPNCoreGraphOptionsUseOneStrictSecretFreeHostCall(t *testing.T) {
	rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponse(t, []string{graphRootA}))
	host.optionsResponse = canonicalGraphOptionsResponse(t)
	response := rt.handleSubscriptionCall(callPayload{Method: "graph_options", Payload: json.RawMessage(`{}`)})
	if !response.OK {
		t.Fatalf("graph_options failed: %+v", response)
	}
	if len(host.calls) != 1 || host.calls[0]["service"] != vpnCoreGraphService || host.calls[0]["method"] != "graph_options" {
		t.Fatalf("host calls=%+v", host.calls)
	}
	request, ok := host.calls[0]["request"].(map[string]any)
	if !ok || len(request) != 0 {
		t.Fatalf("request=%+v", host.calls[0]["request"])
	}
	var options vpnCoreGraphOptionsResponse
	if err := json.Unmarshal(response.Result, &options); err != nil {
		t.Fatal(err)
	}
	if len(options.Identities) != 2 || len(options.Roots) != 2 || !options.Identities[0].Selectable || !options.Roots[0].Selectable || !reflect.DeepEqual(options.Roots[1].EligibleIdentityIDs, []string{"identity-a", "identity-b"}) {
		t.Fatalf("options=%+v", options)
	}
	for _, canary := range []string{"vless://", "PRIVATE KEY", "lat$", "33333333-3333-4333-8333-333333333333", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if strings.Contains(string(response.Result), canary) {
			t.Fatalf("graph options leaked %q: %s", canary, response.Result)
		}
	}

	options.Identities[0].Label = "mutated"
	options.Roots[0].Label = "mutated"
	second := rt.handleSubscriptionCall(callPayload{Method: "graph_options", Payload: json.RawMessage(`{}`)})
	if !second.OK || strings.Contains(string(second.Result), "mutated") {
		t.Fatalf("graph options aliased caller mutation: %+v", second)
	}
}

func TestVPNCoreGraphDraftPreviewComposesWithoutPersistence(t *testing.T) {
	roots := []string{graphRootB, graphRootA}
	rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponseForIdentity(t, "identity-a", roots))
	host.optionsResponse = canonicalGraphOptionsResponse(t)
	selection := vpnCoreGraphSelection{SchemaVersion: 1, OptionsVersion: "ov1:" + strings.Repeat("a", 64), IdentityID: "identity-a", EntryRoots: roots}
	response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]any{"graph_selection": selection, "target": "URI"})})
	if !response.OK {
		t.Fatalf("draft preview failed: %+v", response)
	}
	if len(host.calls) != 2 || host.calls[0]["method"] != "graph_options" || host.calls[1]["method"] != "compose" {
		t.Fatalf("host calls=%+v, want one options then one compose", host.calls)
	}
	request, _ := host.calls[1]["request"].(map[string]any)
	if !reflect.DeepEqual(request["entry_roots"], []any{graphRootB, graphRootA}) {
		t.Fatalf("compose roots=%+v", request["entry_roots"])
	}
	if _, err := rt.getSubscription("preview"); err == nil {
		t.Fatal("draft preview persisted a subscription")
	}
	var summary previewResult
	if err := json.Unmarshal(response.Result, &summary); err != nil {
		t.Fatal(err)
	}
	var composed vpnCoreGraphComposeResponse
	if err := json.Unmarshal(canonicalGraphResponseForIdentity(t, "identity-a", roots), &composed); err != nil {
		t.Fatal(err)
	}
	if summary.SourceVersion != composed.SourceVersion || summary.Stale {
		t.Fatalf("preview authority=%+v want version=%s fresh", summary, composed.SourceVersion)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(response.Result, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"source_node_count", "node_count", "source_version", "stale"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("preview wire omitted %q: %s", key, response.Result)
		}
	}
	if _, legacy := wire["count"]; legacy {
		t.Fatalf("preview wire exposed ambiguous count: %s", response.Result)
	}
}

func TestVPNCoreGraphDraftPreviewOverridesStoredOrderWithoutMutation(t *testing.T) {
	storedRoots := []string{graphRootA, graphRootB}
	previewRoots := []string{graphRootB, graphRootA}
	rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponseForIdentity(t, "identity-a", previewRoots))
	host.optionsResponse = canonicalGraphOptionsResponse(t)
	stored := subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a", EntryRoots: storedRoots,
		GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64), Target: "URI"}
	if err := rt.saveSubscription(stored); err != nil {
		t.Fatal(err)
	}
	selection := vpnCoreGraphSelection{SchemaVersion: 1, OptionsVersion: stored.GraphOptionsVersion, IdentityID: stored.VPNIdentity, EntryRoots: previewRoots}
	response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]any{"subscription_id": "graph", "graph_selection": selection})})
	if !response.OK || len(host.calls) != 2 {
		t.Fatalf("draft preview=%+v calls=%+v", response, host.calls)
	}
	after, err := rt.getSubscription("graph")
	if err != nil || !reflect.DeepEqual(after.EntryRoots, storedRoots) {
		t.Fatalf("preview mutated stored order: %+v err=%v", after.EntryRoots, err)
	}
}

func TestVPNCoreGraphDraftPreviewSaveAndPublishPreserveOneOrder(t *testing.T) {
	roots := []string{graphRootB, graphRootA}
	compose := canonicalGraphResponseForIdentity(t, "identity-a", roots)
	rt, host := newVPNCoreGraphRuntime(t, compose)
	host.optionsResponse = canonicalGraphOptionsResponse(t)
	host.graphResponses = []json.RawMessage{compose, compose}
	selection := vpnCoreGraphSelection{SchemaVersion: 1, OptionsVersion: "ov1:" + strings.Repeat("a", 64), IdentityID: "identity-a", EntryRoots: roots}
	preview := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]any{"graph_selection": selection, "target": "URI"})})
	if !preview.OK {
		t.Fatalf("preview=%+v", preview)
	}
	record := selection.record("graph")
	record.Name = "Graph"
	record.Target = "URI"
	saved := rt.handleSubscriptionCall(callPayload{Method: "save", Payload: mustJSON(map[string]any{"subscription": record})})
	if !saved.OK {
		t.Fatalf("save=%+v", saved)
	}
	if _, err := rt.publishSubscription("graph", "https://destination.invalid/graph", "PUT", "plain"); err != nil {
		t.Fatal(err)
	}
	stored, err := rt.getSubscription("graph")
	if err != nil || !reflect.DeepEqual(stored.EntryRoots, roots) {
		t.Fatalf("stored roots=%+v err=%v", stored.EntryRoots, err)
	}
	if len(host.published) != 1 || !strings.Contains(host.published[0], graphCredentialB) || strings.Index(host.published[0], graphCredentialB) >= strings.Index(host.published[0], graphCredentialA) {
		t.Fatalf("published order=%q", host.published)
	}
	for _, call := range host.calls {
		if call["method"] != "compose" {
			continue
		}
		request, _ := call["request"].(map[string]any)
		if !reflect.DeepEqual(request["entry_roots"], []any{graphRootB, graphRootA}) {
			t.Fatalf("compose order=%+v", request["entry_roots"])
		}
	}
}

func TestVPNCoreGraphDraftPreviewRejectsStaleOrCrossIdentityBeforeCompose(t *testing.T) {
	rootOnlyB := "55555555-5555-4555-8555-555555555555"
	var options vpnCoreGraphOptionsResponse
	if err := json.Unmarshal(canonicalGraphOptionsResponse(t), &options); err != nil {
		t.Fatal(err)
	}
	options.Roots = append(options.Roots, vpnCoreGraphRootOption{LineUUID: rootOnlyB, Label: "B only", SourceNode: "node-b", Source: "managed", Status: "converged", PathSummary: "B only", EligibleIdentityIDs: []string{"identity-b"}, Selectable: true})
	optionsRaw, _ := json.Marshal(options)

	for name, selection := range map[string]vpnCoreGraphSelection{
		"stale options":  {SchemaVersion: 1, OptionsVersion: "ov1:" + strings.Repeat("b", 64), IdentityID: "identity-a", EntryRoots: []string{graphRootA}},
		"cross identity": {SchemaVersion: 1, OptionsVersion: options.OptionsVersion, IdentityID: "identity-a", EntryRoots: []string{rootOnlyB}},
	} {
		t.Run(name, func(t *testing.T) {
			rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponseForIdentity(t, "identity-a", selection.EntryRoots))
			host.optionsResponse = optionsRaw
			response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]any{"graph_selection": selection})})
			if response.OK || len(host.calls) != 1 || host.calls[0]["method"] != "graph_options" {
				t.Fatalf("selection escaped validation: response=%+v calls=%+v", response, host.calls)
			}
			if _, err := rt.getSubscription("preview"); err == nil {
				t.Fatal("rejected draft preview persisted state")
			}
		})
	}
}

func TestVPNCoreGraphDraftPreviewStrictlyRejectsHostileSelection(t *testing.T) {
	valid := `{"schema_version":1,"options_version":"ov1:` + strings.Repeat("a", 64) + `","identity_id":"identity-a","entry_roots":["` + graphRootA + `"]}`
	for name, selection := range map[string]string{
		"null":                `null`,
		"unknown":             strings.TrimSuffix(valid, `}`) + `,"unknown":true}`,
		"duplicate":           strings.Replace(valid, `"identity_id":"identity-a"`, `"identity_id":"identity-a","identity_id":"identity-b"`, 1),
		"trailing":            valid + ` {}`,
		"uppercase authority": strings.Replace(valid, strings.Repeat("a", 64), strings.Repeat("A", 64), 1),
	} {
		t.Run(name, func(t *testing.T) {
			rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponseForIdentity(t, "identity-a", []string{graphRootA}))
			host.optionsResponse = canonicalGraphOptionsResponse(t)
			payload := json.RawMessage(`{"graph_selection":` + selection + `}`)
			response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: payload})
			if response.OK || len(host.calls) != 0 {
				t.Fatalf("hostile selection escaped strict decode: response=%+v calls=%+v", response, host.calls)
			}
		})
	}

	rt, host := newVPNCoreGraphRuntime(t, nil)
	if err := rt.saveSubscription(subscriptionRecord{ID: "legacy", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}); err != nil {
		t.Fatal(err)
	}
	response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: json.RawMessage(`{"subscription_id":"legacy","graph_selection":` + valid + `}`)})
	if response.OK || len(host.calls) != 0 {
		t.Fatalf("non-graph preview accepted graph selection: response=%+v calls=%+v", response, host.calls)
	}
}

func TestVPNCoreGraphOptionsRejectHostilePayloadsAndResponsesWithoutPartialOutput(t *testing.T) {
	for _, payload := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{"x":1}`), json.RawMessage(`{"x":1,"x":2}`), json.RawMessage(`{} {}`)} {
		rt, host := newVPNCoreGraphRuntime(t, nil)
		host.optionsResponse = canonicalGraphOptionsResponse(t)
		response := rt.handleSubscriptionCall(callPayload{Method: "graph_options", Payload: payload})
		if response.OK || len(host.calls) != 0 {
			t.Fatalf("hostile payload=%s response=%+v calls=%+v", payload, response, host.calls)
		}
	}

	valid := canonicalGraphOptionsResponse(t)
	var decoded vpnCoreGraphOptionsResponse
	if err := json.Unmarshal(valid, &decoded); err != nil {
		t.Fatal(err)
	}
	mutate := func(fn func(*vpnCoreGraphOptionsResponse)) json.RawMessage {
		copyResponse := decoded.clone()
		fn(&copyResponse)
		raw, _ := json.Marshal(copyResponse)
		return raw
	}
	for name, hostile := range map[string]json.RawMessage{
		"duplicate":   append([]byte(`{"schema_version":1,`), valid[1:]...),
		"unknown":     append(valid[:len(valid)-1], []byte(`,"unknown":true}`)...),
		"trailing":    append(append([]byte(nil), valid...), []byte(` {}`)...),
		"bad version": mutate(func(r *vpnCoreGraphOptionsResponse) { r.OptionsVersion = "ov1:" + strings.Repeat("z", 64) }),
		"uppercase version": mutate(func(r *vpnCoreGraphOptionsResponse) {
			r.OptionsVersion = "ov1:" + strings.Repeat("A", 64)
		}),
		"unsorted identities": mutate(func(r *vpnCoreGraphOptionsResponse) {
			r.Identities[0], r.Identities[1] = r.Identities[1], r.Identities[0]
		}),
		"duplicate roots":   mutate(func(r *vpnCoreGraphOptionsResponse) { r.Roots[1].LineUUID = r.Roots[0].LineUUID }),
		"selectable reason": mutate(func(r *vpnCoreGraphOptionsResponse) { r.Roots[0].Reason = "looks_bad" }),
		"secret label":      mutate(func(r *vpnCoreGraphOptionsResponse) { r.Identities[0].Label = "vless://credential" }),
	} {
		t.Run(name, func(t *testing.T) {
			rt, host := newVPNCoreGraphRuntime(t, nil)
			host.optionsResponse = hostile
			response := rt.handleSubscriptionCall(callPayload{Method: "graph_options", Payload: json.RawMessage(`{}`)})
			if response.OK || len(response.Result) != 0 || len(host.calls) != 1 {
				t.Fatalf("hostile response returned partial success: %+v", response)
			}
		})
	}
}

func TestVPNCoreGraphPreviewRedactsCredentialsBeforeScriptOperators(t *testing.T) {
	selection := vpnCoreGraphSelection{SchemaVersion: 1, OptionsVersion: "ov1:" + strings.Repeat("a", 64), IdentityID: "identity-a", EntryRoots: []string{graphRootA}}
	composed := canonicalGraphResponseForIdentity(t, selection.IdentityID, selection.EntryRoots)
	syntheticCredential := "00000000-0000-4000-8000-000000000001"
	for name, test := range map[string]struct {
		program string
		want    string
	}{
		"direct":        {program: "$server.name = $server.uuid;", want: syntheticCredential},
		"reverse":       {program: "$server.name = $server.uuid.split('').reverse().join('');", want: reverseString(syntheticCredential)},
		"base64":        {program: "$server.name = Buffer.from($server.uuid).toString('base64');", want: base64.StdEncoding.EncodeToString([]byte(syntheticCredential))},
		"prefix suffix": {program: "$server.name = 'prefix-' + $server.uuid + '-suffix';", want: "prefix-" + syntheticCredential + "-suffix"},
	} {
		t.Run(name, func(t *testing.T) {
			rt, host := newVPNCoreGraphRuntime(t, composed)
			host.optionsResponse = canonicalGraphOptionsResponse(t)
			operators := []json.RawMessage{mustJSON(map[string]any{"type": "Script Operator", "args": map[string]string{"mode": "script", "content": test.program}})}
			response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]any{
				"graph_selection": selection,
				"operators":       operators,
				"target":          "URI",
			})})
			if len(host.calls) != 2 {
				t.Fatalf("host calls=%+v", host.calls)
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			reversed := reverseString(graphCredentialA)
			for _, canary := range []string{graphCredentialA, reversed, base64.StdEncoding.EncodeToString([]byte(graphCredentialA)), "vless://" + graphCredentialA} {
				if strings.Contains(string(encoded), canary) {
					t.Fatalf("credential transform %q leaked %q: %s", name, canary, encoded)
				}
			}
			if !response.OK {
				t.Fatalf("redacted preview failed: %+v", response)
			}
			var summary previewResult
			if err := json.Unmarshal(response.Result, &summary); err != nil {
				t.Fatal(err)
			}
			if summary.SourceNodeCount != 1 || summary.NodeCount != 1 || summary.SourceVersion == "" || summary.Stale {
				t.Fatalf("preview authority/count drifted: %+v", summary)
			}
			if summary.Nodes[0].Name != test.want {
				t.Fatalf("script %q did not execute against the synthetic credential: name=%q want=%q", name, summary.Nodes[0].Name, test.want)
			}
		})
	}
}

func TestVPNCoreGraphPreviewScriptFailureCannotExposeCredentialTransforms(t *testing.T) {
	selection := vpnCoreGraphSelection{SchemaVersion: 1, OptionsVersion: "ov1:" + strings.Repeat("a", 64), IdentityID: "identity-a", EntryRoots: []string{graphRootA}}
	rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponseForIdentity(t, selection.IdentityID, selection.EntryRoots))
	host.optionsResponse = canonicalGraphOptionsResponse(t)
	operators := []json.RawMessage{mustJSON(map[string]any{"type": "Script Operator", "args": map[string]string{
		"mode": "script", "content": "$server.name = Buffer.from($server.uuid).toString('base64'); throw new Error('failed-' + $server.name);",
	}})}
	response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]any{
		"graph_selection": selection,
		"operators":       operators,
		"target":          "URI",
	})})
	if response.OK || len(host.calls) != 2 {
		t.Fatalf("hostile failure did not fail safely: response=%+v calls=%+v", response, host.calls)
	}
	if !strings.Contains(response.Error, "error_sha256=") {
		t.Fatalf("script failure lacked the engine execution sentinel: %+v", response)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{
		graphCredentialA,
		reverseString(graphCredentialA),
		base64.StdEncoding.EncodeToString([]byte(graphCredentialA)),
		"vless://" + graphCredentialA,
	} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("preview error leaked credential transform %q: %s", canary, encoded)
		}
	}
}

func TestLegacyVPNCorePreviewRejectsScriptingBeforeCredentialFetch(t *testing.T) {
	credential := "dddddddd-dddd-5ddd-8ddd-dddddddddddd"
	link := "vless://" + credential + "@legacy.example.com:443?security=reality&sni=legacy.example.com&fp=chrome&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA#legacy"
	for name, operator := range map[string]json.RawMessage{
		"direct operator":  mustJSON(map[string]any{"type": "Script Operator", "args": map[string]string{"mode": "script", "content": "$server.name = $server.uuid;"}}),
		"reverse operator": mustJSON(map[string]any{"type": "Script Operator", "args": map[string]string{"mode": "script", "content": "$server.name = $server.uuid.split('').reverse().join('');"}}),
		"base64 operator":  mustJSON(map[string]any{"type": "Script Operator", "args": map[string]string{"mode": "script", "content": "$server.name = Buffer.from($server.uuid).toString('base64');"}}),
		"failing operator": mustJSON(map[string]any{"type": "Script Operator", "args": map[string]string{"mode": "script", "content": "throw new Error($server.uuid);"}}),
		"script filter":    mustJSON(map[string]any{"type": "Script Filter", "args": map[string]string{"mode": "script", "content": "return $server.uuid === '" + credential + "';"}}),
	} {
		t.Run(name, func(t *testing.T) {
			rt, host := newVPNCoreGraphRuntime(t, nil)
			host.legacyLinks = []string{link}
			if err := rt.saveSubscription(subscriptionRecord{ID: "legacy", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}); err != nil {
				t.Fatal(err)
			}
			response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]any{
				"subscription_id": "legacy",
				"operators":       []json.RawMessage{operator},
			})})
			if response.OK || len(host.calls) != 0 || response.Error != "legacy vpn-core preview does not allow scripting operators" {
				t.Fatalf("legacy scripting preview escaped before fetch: response=%+v calls=%+v", response, host.calls)
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			for _, canary := range []string{credential, reverseString(credential), base64.StdEncoding.EncodeToString([]byte(credential)), link} {
				if strings.Contains(string(encoded), canary) {
					t.Fatalf("legacy preview refusal leaked %q: %s", canary, encoded)
				}
			}
		})
	}
}

func TestLegacyVPNCorePreviewAllowsNonScriptingBuiltIns(t *testing.T) {
	rt, host := newVPNCoreGraphRuntime(t, nil)
	host.legacyLinks = []string{"vless://dddddddd-dddd-5ddd-8ddd-dddddddddddd@legacy.example.com:443?security=reality&sni=legacy.example.com&fp=chrome&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA#legacy"}
	if err := rt.saveSubscription(subscriptionRecord{ID: "legacy", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}); err != nil {
		t.Fatal(err)
	}
	response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]any{
		"subscription_id": "legacy",
		"operators":       []json.RawMessage{json.RawMessage(`{"type":"Regex Filter","args":{"keywords":["legacy"]}}`)},
	})})
	if !response.OK || len(host.calls) != 1 || host.calls[0]["method"] != "export" {
		t.Fatalf("non-scripting legacy preview failed: response=%+v calls=%+v", response, host.calls)
	}
}

func TestFilePreviewRejectsCredentialBearingNodeSourcesBeforeResolution(t *testing.T) {
	credential := graphCredentialA
	legacyLink := "vless://" + credential + "@legacy.example.com:443?security=reality&sni=legacy.example.com&fp=chrome&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA#legacy"
	for _, test := range []struct {
		name     string
		source   subscriptionRecord
		fileType string
	}{
		{name: "legacy vpn-core", source: subscriptionRecord{ID: "source", Source: subscriptionSourceVPNCore, VPNIdentity: "identity-a"}},
		{name: "vpn-core graph", source: subscriptionRecord{ID: "source", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}},
		{name: "script file over graph", source: subscriptionRecord{ID: "source", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}, fileType: fileTypeScript},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponseForIdentity(t, "identity-a", []string{graphRootA}))
			host.legacyLinks = []string{legacyLink}
			if err := rt.saveSubscription(test.source); err != nil {
				t.Fatal(err)
			}
			file := subscriptionRecord{ID: "file", Kind: kindFile, FileType: test.fileType, NodeSource: test.source.ID, Content: "rules:\n  - MATCH,DIRECT\n"}
			if err := rt.saveSubscription(file); err != nil {
				t.Fatal(err)
			}
			response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]string{"subscription_id": file.ID})})
			if response.OK || response.Error != "file preview does not expose node-source content" || len(response.Result) != 0 || len(host.calls) != 0 {
				t.Fatalf("credential-bearing file preview escaped: response=%+v calls=%+v", response, host.calls)
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			for _, canary := range []string{credential, legacyLink, "vless://", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
				if strings.Contains(string(encoded), canary) {
					t.Fatalf("file preview refusal leaked %q: %s", canary, encoded)
				}
			}
		})
	}
}

func reverseString(value string) string {
	runes := []rune(value)
	for left, right := 0, len(runes)-1; left < right; left, right = left+1, right-1 {
		runes[left], runes[right] = runes[right], runes[left]
	}
	return string(runes)
}

func TestVPNCoreGraphOptionsResponseUsesFourMiBProtocolBound(t *testing.T) {
	large := vpnCoreGraphOptionsResponse{SchemaVersion: 1, OK: true, OptionsVersion: "ov1:" + strings.Repeat("a", 64), Roots: []vpnCoreGraphRootOption{{LineUUID: graphRootA, Label: "Source A", SourceNode: "node-a", Source: "managed", Status: "converged", PathSummary: "Source A", EligibleIdentityIDs: []string{"identity-00000"}, Selectable: true}}}
	for i := 0; i < 7000; i++ {
		large.Identities = append(large.Identities, vpnCoreGraphIdentityOption{ID: fmt.Sprintf("identity-%05d", i), Label: fmt.Sprintf("identity-%05d-%s", i, strings.Repeat("x", 96)), Status: "eligible", Selectable: true})
	}
	largeRaw, err := json.Marshal(large)
	if err != nil {
		t.Fatal(err)
	}
	if len(largeRaw) <= 1<<20 || len(largeRaw) >= model.MaxSubscriptionResponseBytes {
		t.Fatalf("large legal projection size=%d", len(largeRaw))
	}
	if err := validateVPNCoreGraphOptions(large); err != nil {
		t.Fatalf("large legal projection validation: %v", err)
	}
	var decodedLarge vpnCoreGraphOptionsResponse
	if err := decodeStrictVPNCoreGraphJSON(largeRaw, &decodedLarge); err != nil {
		t.Fatalf("large legal projection strict decode: %v", err)
	}
	if err := validateVPNCoreGraphOptions(decodedLarge); err != nil {
		t.Fatalf("large decoded projection validation: %v", err)
	}
	rt, host := newVPNCoreGraphRuntime(t, nil)
	host.optionsResponse = largeRaw
	if response := rt.handleSubscriptionCall(callPayload{Method: "graph_options", Payload: json.RawMessage(`{}`)}); !response.OK {
		t.Fatalf("legal >1MiB projection rejected: %+v", response)
	}

	rt, host = newVPNCoreGraphRuntime(t, nil)
	host.optionsResponse = json.RawMessage(strings.Repeat(" ", model.MaxSubscriptionResponseBytes+1))
	response := rt.handleSubscriptionCall(callPayload{Method: "graph_options", Payload: json.RawMessage(`{}`)})
	if response.OK || len(host.calls) != 1 {
		t.Fatalf("4MiB+1 options response escaped bound: response=%+v calls=%+v", response, host.calls)
	}
}

func TestVPNCoreGraphSaveRevalidatesOptionsAndPersistsExactOrderAcrossRestart(t *testing.T) {
	rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponse(t, []string{graphRootB, graphRootA}))
	host.optionsResponse = canonicalGraphOptionsResponse(t)
	record := subscriptionRecord{ID: "graph", Name: "Graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a",
		EntryRoots: []string{graphRootB, graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}
	response := rt.handleSubscriptionCall(callPayload{Method: "save", Payload: mustJSON(map[string]any{"subscription": record})})
	if !response.OK || len(host.calls) != 1 || host.calls[0]["method"] != "graph_options" {
		t.Fatalf("save=%+v calls=%+v", response, host.calls)
	}

	restarted := &runtime{host: host, engine: testEngineWithHeadroom()}
	stored, err := restarted.getSubscription("graph")
	if err != nil {
		t.Fatal(err)
	}
	if stored.VPNIdentity != record.VPNIdentity || stored.GraphOptionsVersion != record.GraphOptionsVersion || !reflect.DeepEqual(stored.EntryRoots, record.EntryRoots) {
		t.Fatalf("restarted graph record=%+v want=%+v", stored, record)
	}
	stored.EntryRoots[0] = graphRootA
	again, err := restarted.getSubscription("graph")
	if err != nil || !reflect.DeepEqual(again.EntryRoots, record.EntryRoots) {
		t.Fatalf("stored graph record aliases caller: %+v err=%v", again, err)
	}
}

func TestVPNCoreGraphSaveRejectsStaleOrIneligibleSelectionWithoutPersistence(t *testing.T) {
	for name, mutate := range map[string]func(*subscriptionRecord){
		"stale version":    func(record *subscriptionRecord) { record.GraphOptionsVersion = "ov1:" + strings.Repeat("b", 64) },
		"unknown identity": func(record *subscriptionRecord) { record.VPNIdentity = "identity-z" },
		"ineligible root": func(record *subscriptionRecord) {
			record.EntryRoots = []string{"55555555-5555-4555-8555-555555555555"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			rt, host := newVPNCoreGraphRuntime(t, nil)
			host.optionsResponse = canonicalGraphOptionsResponse(t)
			record := subscriptionRecord{ID: "graph", Name: "Graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a",
				EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}
			mutate(&record)
			response := rt.handleSubscriptionCall(callPayload{Method: "save", Payload: mustJSON(map[string]any{"subscription": record})})
			if response.OK || len(host.calls) != 1 {
				t.Fatalf("invalid selection saved: response=%+v calls=%+v", response, host.calls)
			}
			if _, err := rt.getSubscription("graph"); err == nil {
				t.Fatal("invalid selection was persisted")
			}
		})
	}
}

func TestLegacyVPNCoreFetchDoesNotInventGraphProvenance(t *testing.T) {
	host := newVPNCoreHost("vless://legacy")
	rt := &runtime{host: host, engine: testEngineWithHeadroom()}
	if err := rt.saveSubscription(subscriptionRecord{ID: "legacy", Source: subscriptionSourceVPNCore, VPNIdentity: "identity"}); err != nil {
		t.Fatal(err)
	}
	response := rt.handleSubscriptionCall(callPayload{Method: "fetch", Payload: mustJSON(map[string]string{"subscription_id": "legacy"})})
	if !response.OK {
		t.Fatalf("legacy fetch failed: %+v", response)
	}
	if strings.Contains(string(response.Result), "source_version") || strings.Contains(string(response.Result), "source_manifest") {
		t.Fatalf("legacy response invented graph provenance: %s", response.Result)
	}
}

func TestVPNCoreGraphColdPreviewAndPublishUseExactOrderedComposition(t *testing.T) {
	roots := []string{graphRootB, graphRootA}
	composeResponse := canonicalGraphResponse(t, roots)
	var expected vpnCoreGraphComposeResponse
	if err := json.Unmarshal(composeResponse, &expected); err != nil {
		t.Fatal(err)
	}
	rt, host := newVPNCoreGraphRuntime(t, composeResponse)
	if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: roots, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64), Target: "URI"}); err != nil {
		t.Fatal(err)
	}
	rendered, err := rt.renderSubscription("graph", "plain", "", "", nil)
	if err != nil {
		t.Fatalf("cold render failed: %v", err)
	}

	preview := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]string{"subscription_id": "graph"})})
	if !preview.OK {
		t.Fatalf("cold preview failed: %+v", preview)
	}
	var summary previewResult
	if err := json.Unmarshal(preview.Result, &summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Nodes) != 2 || summary.Nodes[0].Name != "entry-1" || summary.Nodes[1].Name != "entry-2" {
		t.Fatalf("preview order = %+v", summary.Nodes)
	}
	if _, err := rt.publishSubscription("graph", "https://destination.invalid/graph", "PUT", "plain"); err != nil {
		t.Fatal(err)
	}
	if len(host.published) != 1 || host.published[0] != rendered.Content {
		t.Fatalf("published bytes = %q, want exact cold-render bytes %q", host.published, rendered.Content)
	}
	rootBIndex, rootAIndex := strings.Index(rendered.Content, graphCredentialB), strings.Index(rendered.Content, graphCredentialA)
	if !strings.Contains(rendered.Content, graphCredentialB) || rootBIndex >= rootAIndex {
		t.Fatalf("rendered root order changed: %q", rendered.Content)
	}
	if len(host.calls) != 3 {
		t.Fatalf("compose calls = %d, want one per cold operation", len(host.calls))
	}
	for _, call := range host.calls {
		request, _ := call["request"].(map[string]any)
		if !reflect.DeepEqual(request["entry_roots"], []any{graphRootB, graphRootA}) {
			t.Fatalf("compose root order = %+v", request["entry_roots"])
		}
	}
}

func TestVPNCoreGraphStoredPreviewUsesEnabledCanonicalProcess(t *testing.T) {
	roots := []string{graphRootA, graphRootB}
	composeResponse := canonicalGraphResponseForIdentity(t, "identity-a", roots)
	rt, host := newVPNCoreGraphRuntime(t, composeResponse)
	host.optionsResponse = canonicalGraphOptionsResponse(t)
	enabled := step(t, map[string]any{
		"type": "Regex Filter",
		"args": map[string]any{"regex": []string{"entry-1"}, "keep": true},
	})
	disabled := step(t, map[string]any{
		"type":     "Regex Filter",
		"disabled": true,
		"args":     map[string]any{"regex": []string{"does-not-match"}, "keep": true},
	})
	legacy := step(t, map[string]any{
		"type": "Regex Filter",
		"args": map[string]any{"regex": []string{"entry-2"}, "keep": true},
	})
	record := subscriptionRecord{
		ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a", EntryRoots: roots,
		GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64), Target: "URI",
		Process: []json.RawMessage{enabled, disabled}, Operators: []json.RawMessage{legacy},
	}
	if err := rt.saveSubscription(record); err != nil {
		t.Fatal(err)
	}
	stored, err := rt.getSubscription("graph")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.Process, record.Process) || len(stored.Operators) != 0 {
		t.Fatalf("stored process was not canonical: process=%s operators=%s", stored.Process, stored.Operators)
	}

	selection := vpnCoreGraphSelection{
		SchemaVersion: 1, OptionsVersion: record.GraphOptionsVersion, IdentityID: record.VPNIdentity, EntryRoots: roots,
	}
	for _, test := range []struct {
		name    string
		payload map[string]any
	}{
		{name: "subscription id", payload: map[string]any{"subscription_id": "graph"}},
		{name: "selected record", payload: map[string]any{"subscription_id": "graph", "graph_selection": selection}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(test.payload)})
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			for _, canary := range []string{graphCredentialA, graphCredentialB, "vless://", "PRIVATE KEY"} {
				if strings.Contains(string(encoded), canary) {
					t.Fatalf("stored preview leaked %q: %s", canary, encoded)
				}
			}
			if !response.OK {
				t.Fatalf("stored preview failed: %+v", response)
			}
			var summary previewResult
			if err := json.Unmarshal(response.Result, &summary); err != nil {
				t.Fatal(err)
			}
			if summary.SourceNodeCount != 2 || summary.NodeCount != 1 || len(summary.Nodes) != 1 || summary.Nodes[0].Name != "entry-1" {
				t.Fatalf("stored process did not filter the authoritative composition: %+v", summary)
			}
			if summary.SourceVersion == "" || summary.Stale {
				t.Fatalf("stored preview lost composition authority: %+v", summary)
			}
		})
	}
	if len(host.calls) != 3 || host.calls[0]["method"] != "compose" || host.calls[1]["method"] != "graph_options" || host.calls[2]["method"] != "compose" {
		t.Fatalf("stored preview host calls=%+v", host.calls)
	}
}

func TestVPNCoreGraphStoredPreviewPropagatesCanonicalProcessErrors(t *testing.T) {
	record := subscriptionRecord{
		ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity-a", EntryRoots: []string{graphRootA},
		GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64), Process: []json.RawMessage{json.RawMessage(`42`)},
	}
	selection := vpnCoreGraphSelection{
		SchemaVersion: 1, OptionsVersion: record.GraphOptionsVersion, IdentityID: record.VPNIdentity, EntryRoots: record.EntryRoots,
	}
	for _, test := range []struct {
		name    string
		payload map[string]any
	}{
		{name: "subscription id", payload: map[string]any{"subscription_id": "graph"}},
		{name: "selected record", payload: map[string]any{"subscription_id": "graph", "graph_selection": selection}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponseForIdentity(t, record.VPNIdentity, record.EntryRoots))
			host.optionsResponse = canonicalGraphOptionsResponse(t)
			if err := rt.saveSubscriptionRecords(subscriptionRecordsDocument{Version: 1, Records: []subscriptionRecord{record}}); err != nil {
				t.Fatal(err)
			}
			response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(test.payload)})
			if response.OK || !strings.Contains(response.Error, "process step 1") || len(host.calls) != 0 {
				t.Fatalf("stored process error was not bounded before composition: response=%+v calls=%+v", response, host.calls)
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			for _, canary := range []string{graphCredentialA, "vless://", "PRIVATE KEY"} {
				if strings.Contains(string(encoded), canary) {
					t.Fatalf("stored process error leaked %q: %s", canary, encoded)
				}
			}
		})
	}
}

func TestVPNCoreGraphPreviewRejectsCallerRawAsAuthority(t *testing.T) {
	rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponse(t, []string{graphRootA}))
	if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64), Target: "URI"}); err != nil {
		t.Fatal(err)
	}
	response := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: mustJSON(map[string]string{
		"subscription_id": "graph", "raw": "vless://99999999-9999-4999-8999-999999999999@hostile.example:443#hostile",
	})})
	if !response.OK || len(host.calls) != 1 {
		t.Fatalf("authoritative preview = %+v calls=%d", response, len(host.calls))
	}
	var preview previewResult
	if err := json.Unmarshal(response.Result, &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Nodes) != 1 || preview.Nodes[0].Name != "entry-1" {
		t.Fatalf("preview used caller bytes: %+v", preview.Nodes)
	}
}

func TestVPNCoreGraphConfigurationIsCanonicalAndHasNoFallback(t *testing.T) {
	for name, record := range map[string]subscriptionRecord{
		"missing identity":    {ID: "graph", Source: subscriptionSourceVPNCoreGraph, EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)},
		"identity whitespace": {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: " identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)},
		"missing roots":       {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)},
		"duplicate roots":     {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA, graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)},
		"uppercase root":      {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaA"}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)},
		"missing authority":   {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}},
		"uppercase authority": {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("A", 64)},
		"short authority":     {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:abc"},
	} {
		t.Run(name, func(t *testing.T) {
			rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponse(t, []string{graphRootA}))
			if err := rt.saveSubscription(record); err == nil {
				t.Fatal("invalid graph record saved")
			}
			if len(host.calls) != 0 {
				t.Fatalf("invalid configuration reached host: %+v", host.calls)
			}
		})
	}
}

func TestFileSaveClearsEveryGraphAuthorityField(t *testing.T) {
	rt, _ := newVPNCoreGraphRuntime(t, nil)
	record := subscriptionRecord{ID: "file", Name: "file", Kind: kindFile, FileType: fileTypePlain, Content: "rules", VPNIdentity: "identity-a", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}
	if err := rt.saveSubscription(record); err != nil {
		t.Fatal(err)
	}
	stored, err := rt.getSubscription("file")
	if err != nil {
		t.Fatal(err)
	}
	if stored.VPNIdentity != "" || len(stored.EntryRoots) != 0 || stored.GraphOptionsVersion != "" {
		t.Fatalf("file retained graph authority: %+v", stored)
	}
}

func TestVPNCoreGraphRejectsHostileOrPartialResponses(t *testing.T) {
	valid := canonicalGraphResponse(t, []string{graphRootA})
	var decoded vpnCoreGraphComposeResponse
	if err := json.Unmarshal(valid, &decoded); err != nil {
		t.Fatal(err)
	}
	mutate := func(fn func(*vpnCoreGraphComposeResponse)) json.RawMessage {
		copyResponse := decoded
		copyResponse.SourceManifest = append(json.RawMessage(nil), decoded.SourceManifest...)
		copyResponse.Entries = append([]string(nil), decoded.Entries...)
		fn(&copyResponse)
		raw, _ := json.Marshal(copyResponse)
		return raw
	}
	cases := map[string]json.RawMessage{
		"duplicate":        append([]byte(`{"ok":true,`), valid[1:]...),
		"unknown":          append(valid[:len(valid)-1], []byte(`,"unknown":true}`)...),
		"trailing":         append(append([]byte(nil), valid...), []byte(` {}`)...),
		"null":             json.RawMessage(`null`),
		"null manifest":    mutate(func(r *vpnCoreGraphComposeResponse) { r.SourceManifest = nil }),
		"nested duplicate": json.RawMessage(`{"schema_version":1,"ok":false,"error":{"code":"a","code":"b","message":"failed"}}`),
		"nested unknown":   json.RawMessage(`{"schema_version":1,"ok":false,"error":{"code":"failed","message":"failed","unknown":true}}`),
		"manifest duplicate": json.RawMessage(strings.Replace(string(valid), `"source_manifest":{"schema":`,
			`"source_manifest":{"schema":"duplicate","schema":`, 1)),
		"manifest unknown": json.RawMessage(strings.Replace(string(valid), `"source_manifest":{`,
			`"source_manifest":{"unknown":true,`, 1)),
		"partial failure": mutate(func(r *vpnCoreGraphComposeResponse) {
			r.OK = false
			r.Error = &vpnCoreGraphComposeError{Code: "failed", Message: "failed"}
		}),
		"source hash mismatch": mutate(func(r *vpnCoreGraphComposeResponse) { r.SourceVersion = "sv1:" + strings.Repeat("0", 64) }),
		"raw mismatch":         mutate(func(r *vpnCoreGraphComposeResponse) { r.Raw = "vless://partial" }),
		"entry count mismatch": mutate(func(r *vpnCoreGraphComposeResponse) {
			r.Entries = append(r.Entries, r.Entries[0])
			r.Raw = strings.Join(r.Entries, "\n")
		}),
		"entry oversize": mutate(func(r *vpnCoreGraphComposeResponse) {
			r.Entries[0] = strings.Repeat("x", model.MaxSubscriptionURIBytes+1)
			r.Raw = r.Entries[0]
		}),
		"response oversize": json.RawMessage(strings.Repeat(" ", model.MaxSubscriptionResponseBytes+1)),
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			rt, host := newVPNCoreGraphRuntime(t, response)
			if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
				t.Fatal(err)
			}
			result, err := rt.fetchSubscription("graph")
			if err == nil || result.Raw != "" || result.SourceVersion != "" || len(result.SourceManifest) != 0 {
				t.Fatalf("hostile response produced partial success: result=%+v err=%v", result, err)
			}
			if len(host.calls) != 1 {
				t.Fatalf("host call count = %d", len(host.calls))
			}
		})
	}
}

func TestVPNCoreGraphRejectsEntriesOutsideCanonicalManifestBinding(t *testing.T) {
	valid := canonicalGraphResponse(t, []string{graphRootA})
	var decoded vpnCoreGraphComposeResponse
	if err := json.Unmarshal(valid, &decoded); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(string) string{
		"credential equals entry root": func(s string) string {
			return strings.Replace(s, graphCredentialA+"@", graphRootA+"@", 1)
		},
		"scheme":      func(s string) string { return strings.Replace(s, "vless://", "vmess://", 1) },
		"newline":     func(s string) string { return s + "\nvless://" + graphRootB + "@entry.example.com:443" },
		"control":     func(s string) string { return strings.Replace(s, "#entry-1", "#entry%01", 1) },
		"host":        func(s string) string { return strings.Replace(s, "entry.example.com", "other.example.com", 1) },
		"port":        func(s string) string { return strings.Replace(s, ":443?", ":444?", 1) },
		"sni":         func(s string) string { return strings.Replace(s, "sni=entry.example.com", "sni=other.example.com", 1) },
		"fingerprint": func(s string) string { return strings.Replace(s, "fp=chrome", "fp=firefox", 1) },
		"public key": func(s string) string {
			return strings.Replace(s, "pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "pbk=BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", 1)
		},
		"short id":        func(s string) string { return strings.Replace(s, "sid=0123456789abcdef", "sid=abcdef", 1) },
		"flow":            func(s string) string { return strings.Replace(s, "flow=xtls-rprx-vision", "flow=other", 1) },
		"label":           func(s string) string { return strings.Replace(s, "#entry-1", "#other", 1) },
		"duplicate query": func(s string) string { return strings.Replace(s, "?", "?security=reality&", 1) },
		"unknown query":   func(s string) string { return strings.Replace(s, "?", "?unknown=1&", 1) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			response := decoded
			response.Entries = []string{mutate(decoded.Entries[0])}
			response.Raw = response.Entries[0]
			raw, _ := json.Marshal(response)
			rt, _ := newVPNCoreGraphRuntime(t, raw)
			if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
				t.Fatal(err)
			}
			result, err := rt.fetchSubscription("graph")
			if err == nil || result.Raw != "" || result.SourceVersion != "" || len(result.SourceManifest) != 0 {
				t.Fatalf("noncanonical entry produced partial success: %+v err=%v", result, err)
			}
		})
	}
}

func TestVPNCoreGraphAcceptsCanonicalNonV4CredentialUUID(t *testing.T) {
	response := canonicalGraphResponse(t, []string{graphRootA})
	rt, _ := newVPNCoreGraphRuntime(t, response)
	if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	result, err := rt.fetchSubscription("graph")
	if err != nil || !strings.Contains(result.Raw, graphCredentialA) || strings.Contains(result.Raw, "vless://"+graphRootA+"@") {
		t.Fatalf("canonical non-v4 credential rejected: result=%+v err=%v", result, err)
	}
}

func TestVPNCoreGraphRejectsManifestIdentityAndRootOrderMismatch(t *testing.T) {
	for name, responseRoots := range map[string][]string{
		"root order": {graphRootA, graphRootB},
		"root count": {graphRootB},
	} {
		t.Run(name, func(t *testing.T) {
			rt, _ := newVPNCoreGraphRuntime(t, canonicalGraphResponse(t, responseRoots))
			if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootB, graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
				t.Fatal(err)
			}
			if _, err := rt.fetchSubscription("graph"); err == nil {
				t.Fatal("manifest mismatch accepted")
			}
		})
	}
	t.Run("identity", func(t *testing.T) {
		rt, _ := newVPNCoreGraphRuntime(t, canonicalGraphResponseForIdentity(t, "other-identity", []string{graphRootA}))
		if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.fetchSubscription("graph"); err == nil {
			t.Fatal("manifest identity mismatch accepted")
		}
	})
}
