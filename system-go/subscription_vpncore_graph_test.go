package main

import (
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
	graphRootA = "11111111-1111-4111-8111-111111111111"
	graphRootB = "22222222-2222-4222-8222-222222222222"
)

type vpnCoreGraphHost struct {
	*kvHostCaller
	response       json.RawMessage
	graphResponses []json.RawMessage
	legacyLinks    []string
	calls          []map[string]any
	published      []string
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
	if len(h.graphResponses) > 0 {
		response := h.graphResponses[0]
		h.graphResponses = h.graphResponses[1:]
		return append(json.RawMessage(nil), response...), nil
	}
	return append(json.RawMessage(nil), h.response...), nil
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
		entries = append(entries, "vless://"+root+"@"+net.JoinHostPort(manifest.Entries[i].Endpoint.Host, strconv.Itoa(manifest.Entries[i].Endpoint.Port))+"?"+values.Encode()+"#"+url.PathEscape(label))
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
	if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: roots}); err != nil {
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
	if !strings.HasPrefix(result.Raw, "vless://"+graphRootB) || !strings.Contains(result.Raw, "\nvless://"+graphRootA) {
		t.Fatalf("raw order = %q", result.Raw)
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
	if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: roots, Target: "URI"}); err != nil {
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
	rootBIndex, rootAIndex := strings.Index(rendered.Content, graphRootB), strings.Index(rendered.Content, graphRootA)
	if !strings.Contains(rendered.Content, graphRootB) || rootBIndex >= rootAIndex {
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

func TestVPNCoreGraphPreviewRejectsCallerRawAsAuthority(t *testing.T) {
	rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponse(t, []string{graphRootA}))
	if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, Target: "URI"}); err != nil {
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
		"missing identity":    {ID: "graph", Source: subscriptionSourceVPNCoreGraph, EntryRoots: []string{graphRootA}},
		"identity whitespace": {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: " identity", EntryRoots: []string{graphRootA}},
		"missing roots":       {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity"},
		"duplicate roots":     {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA, graphRootA}},
		"uppercase root":      {ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaA"}},
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
			if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}}); err != nil {
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
			if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}}); err != nil {
				t.Fatal(err)
			}
			result, err := rt.fetchSubscription("graph")
			if err == nil || result.Raw != "" || result.SourceVersion != "" || len(result.SourceManifest) != 0 {
				t.Fatalf("noncanonical entry produced partial success: %+v err=%v", result, err)
			}
		})
	}
}

func TestVPNCoreGraphRejectsManifestIdentityAndRootOrderMismatch(t *testing.T) {
	for name, responseRoots := range map[string][]string{
		"root order": {graphRootA, graphRootB},
		"root count": {graphRootB},
	} {
		t.Run(name, func(t *testing.T) {
			rt, _ := newVPNCoreGraphRuntime(t, canonicalGraphResponse(t, responseRoots))
			if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootB, graphRootA}}); err != nil {
				t.Fatal(err)
			}
			if _, err := rt.fetchSubscription("graph"); err == nil {
				t.Fatal("manifest mismatch accepted")
			}
		})
	}
	t.Run("identity", func(t *testing.T) {
		rt, _ := newVPNCoreGraphRuntime(t, canonicalGraphResponseForIdentity(t, "other-identity", []string{graphRootA}))
		if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}}); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.fetchSubscription("graph"); err == nil {
			t.Fatal("manifest identity mismatch accepted")
		}
	})
}
