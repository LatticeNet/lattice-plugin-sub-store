package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// hostDefaultHTTPResponseBytes is what lattice-server gives an http.do or
// http.operator.do response when the calling method's signed budget names no
// http_response_bytes (DefaultInvokeHTTPResponseBytes in
// internal/plugin/invoke_budget.go).
const hostDefaultHTTPResponseBytes = 256 << 10

// httpBudgetHost answers provider fetches the way lattice-server does: it
// reads the response body up to the calling method's signed
// http_response_bytes, or the host default when the budget names none, and
// refuses a larger body with the host's own error (internal/server/
// plugin_host.go). The method under test selects which signed budget applies,
// exactly as the broker binds the invocation's method before any host call.
type httpBudgetHost struct {
	*kvHostCaller
	method   string
	provider []byte
	served   int
}

func (h *httpBudgetHost) call(method string, params any) (json.RawMessage, error) {
	switch method {
	case latticeplugin.HostMethodRPCCall:
		return json.Marshal(map[string]any{"links": []string{scriptNodeHome}})
	case latticeplugin.HostMethodHTTPOperatorDo:
		return json.Marshal(map[string]any{"status_code": 200})
	case latticeplugin.HostMethodHTTPDo:
		limit := ackedRuntimeBudgets()[pluginID+"/subscription/"+h.method].HTTPResponseBytes
		if limit <= 0 {
			limit = hostDefaultHTTPResponseBytes
		}
		if len(h.provider) > limit {
			return nil, fmt.Errorf("plugin http response exceeds the method's %d byte budget", limit)
		}
		h.served++
		return json.Marshal(map[string]any{
			"status_code": 200,
			"body_base64": base64.StdEncoding.EncodeToString(h.provider),
		})
	default:
		return h.kvHostCaller.call(method, params)
	}
}

// largeProviderBody is a real subscription past the host default and well
// inside maxProviderResponseBytes: a large node list of the kind a provider
// with a few thousand lines, or a ruleset-heavy template, actually serves.
func largeProviderBody(t *testing.T) []byte {
	t.Helper()
	body := []byte(strings.Join(perfgen.URIs(1600), "\n"))
	if len(body) <= hostDefaultHTTPResponseBytes || len(body) >= maxProviderResponseBytes {
		t.Fatalf("provider body is %d bytes; it must sit between the host default %d and maxProviderResponseBytes %d",
			len(body), hostDefaultHTTPResponseBytes, maxProviderResponseBytes)
	}
	return body
}

// Every method that can reach a provider body through the guarded fetch
// (fetchRecordContent, and fetchSubscription over it) must be signed for the
// body that fetch itself accepts. Before this was pinned, only fetch carried
// http_response_bytes: a provider that refreshed fine failed the public share
// of a file whose template is that provider, the live render of a collection
// over it, the console's preview and preview_draft, the row check (probe) and
// publish, all at the host's 256 KiB default.
func TestProviderFetchingMethodsAreSignedForAProviderBody(t *testing.T) {
	provider := largeProviderBody(t)
	records := []subscriptionRecord{
		{ID: "big", Name: "big", Source: subscriptionSourceRemote, URL: "https://provider.example/big"},
		{ID: "big-coll", Name: "big-coll", Kind: kindCollection, Members: []string{"big"}},
		// A plain file served from a provider URL, the shape of a hosted
		// ruleset: its render returns the fetched document as it is.
		{ID: "big-template", Name: "big-template", Kind: kindFile, FileType: fileTypePlain, Source: subscriptionSourceRemote, URL: "https://provider.example/template"},
	}
	scenarios := []struct {
		name    string
		method  string
		payload map[string]any
	}{
		{name: "fetch a provider", method: "fetch", payload: map[string]any{"subscription_id": "big"}},
		{name: "check a provider from its row", method: "probe", payload: map[string]any{"subscription_id": "big"}},
		// The share path: a file template is fetched on every render, and a
		// collection with no snapshot yet renders its members live.
		{name: "render a file whose template is a provider", method: "render", payload: map[string]any{"subscription_id": "big-template", "format": "plain"}},
		{name: "render a collection over a provider", method: "render", payload: map[string]any{"subscription_id": "big-coll", "format": "plain"}},
		{name: "preview a provider", method: "preview", payload: map[string]any{"subscription_id": "big"}},
		{name: "preview a draft that names a provider", method: "preview_draft", payload: map[string]any{"source": subscriptionSourceRemote, "url": "https://provider.example/draft"}},
		{name: "publish a collection over a provider", method: "publish", payload: map[string]any{"subscription_id": "big-coll", "destination": "https://operator.example/hook", "format": "plain"}},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			host := &httpBudgetHost{kvHostCaller: newKVHostCaller(), provider: provider}
			rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
			for _, rec := range records {
				if err := rt.saveSubscription(rec); err != nil {
					t.Fatalf("seed %s: %v", rec.ID, err)
				}
			}
			host.method = scenario.method
			res := callSubscription(t, rt, scenario.method, scenario.payload)
			if !res.OK {
				t.Fatalf("%s of a %d byte provider body failed: %s", scenario.method, len(provider), res.Error)
			}
			if scenario.method == "probe" {
				// probe answers ok:false in a successful response when the
				// source cannot be read, so its own verdict is the assertion.
				// bytes counts the snapshot fetch would store, which carries
				// the whole body.
				var out subscriptionProbeResult
				decodeResult(t, res, &out)
				if !out.OK || out.Bytes < len(provider) {
					t.Fatalf("probe of a %d byte provider body = %+v", len(provider), out)
				}
			}
			if host.served == 0 {
				t.Fatalf("%s never reached the provider; the scenario does not exercise the fetch path", scenario.method)
			}
			// The signed number is the plugin's own cap, so the manifest
			// cannot promise less than the code accepts or more than it keeps.
			if got := ackedRuntimeBudgets()[pluginID+"/subscription/"+scenario.method].HTTPResponseBytes; got != maxProviderResponseBytes {
				t.Errorf("%s is signed for %d byte HTTP responses; the provider fetch accepts %d", scenario.method, got, maxProviderResponseBytes)
			}
		})
	}
}
