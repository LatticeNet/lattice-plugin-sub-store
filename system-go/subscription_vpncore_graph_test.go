package main

import (
	"encoding/json"
	"reflect"
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
	response json.RawMessage
	calls    []map[string]any
}

func (h *vpnCoreGraphHost) call(method string, params any) (json.RawMessage, error) {
	if method != latticeplugin.HostMethodRPCCall {
		return h.kvHostCaller.call(method, params)
	}
	encoded, _ := json.Marshal(params)
	var call map[string]any
	_ = json.Unmarshal(encoded, &call)
	h.calls = append(h.calls, call)
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
		manifest.Entries = append(manifest.Entries, model.SubscriptionSourceManifestEntry{
			Root: root,
			Endpoint: model.SubscriptionSourceManifestEndpoint{LineUUID: root, NodeID: "node", Label: "entry", Host: "entry.example.com", Port: 443,
				SNI: "entry.example.com", Fingerprint: "chrome", ALPN: []string{}, PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ShortID: "0123456789abcdef", Flow: "xtls-rprx-vision"},
			Path:     []model.SubscriptionSourceManifestEdge{},
			Terminal: model.SubscriptionSourceManifestTerminal{LineUUID: root, Generation: uint64(i + 1), ObservationRevision: uint64(i + 1), Status: "converged"},
		})
		entries = append(entries, "vless://"+root+"@entry.example.com:443?security=reality#entry")
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
	rt, host := newVPNCoreGraphRuntime(t, canonicalGraphResponse(t, roots))
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
			if err == nil || result.Raw != "" {
				t.Fatalf("hostile response produced partial success: result=%+v err=%v", result, err)
			}
			if len(host.calls) != 1 {
				t.Fatalf("host call count = %d", len(host.calls))
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
