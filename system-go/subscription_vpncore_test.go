package main

import (
	"encoding/json"
	"strings"
	"testing"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// A deployment whose nodes live in vpn-core could not serve them natively at
// all: the export was reachable, but only by the outbound push to an external
// Sub-Store. These cover the source that closes that.

// vpnCoreHost answers the vpn-core export over rpc:call and records what was
// asked for, so a test can assert the identity filter actually travels.
type vpnCoreHost struct {
	*kvHostCaller
	links    []string
	rpcCalls []map[string]any
	failWith error
}

func newVPNCoreHost(links ...string) *vpnCoreHost {
	return &vpnCoreHost{kvHostCaller: newKVHostCaller(), links: links}
}

func (h *vpnCoreHost) call(method string, params any) (json.RawMessage, error) {
	if method == latticeplugin.HostMethodRPCCall {
		encoded, _ := json.Marshal(params)
		var p map[string]any
		_ = json.Unmarshal(encoded, &p)
		h.rpcCalls = append(h.rpcCalls, p)
		if h.failWith != nil {
			return nil, h.failWith
		}
		return json.Marshal(map[string]any{"links": h.links})
	}
	return h.kvHostCaller.call(method, params)
}

func newVPNCoreRuntime(t *testing.T, links ...string) (*runtime, *vpnCoreHost) {
	t.Helper()
	host := newVPNCoreHost(links...)
	return &runtime{host: host, engine: testEngineWithHeadroom()}, host
}

func TestVPNCoreSubscriptionFetchesTheExport(t *testing.T) {
	rt, host := newVPNCoreRuntime(t, "vless://one", "vless://two")
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "fleet", Name: "Fleet", Source: subscriptionSourceVPNCore,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := rt.fetchSubscription("fleet")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if out.Raw != "vless://one\nvless://two" {
		t.Fatalf("fetch did not return the export: %q", out.Raw)
	}
	if len(host.rpcCalls) != 1 {
		t.Fatalf("expected exactly one rpc call, got %d", len(host.rpcCalls))
	}
	if got := host.rpcCalls[0]["service"]; got != "latticenet.vpn-core/nodes" {
		t.Fatalf("wrong service: %v", got)
	}
	if got := host.rpcCalls[0]["method"]; got != "export" {
		t.Fatalf("wrong method: %v", got)
	}
}

// A vpn-core record has no URL, and requiring one would make the source
// unusable — that requirement is what the old code path enforced.
func TestVPNCoreSubscriptionNeedsNoURL(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t, "vless://one")
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "fleet", Source: subscriptionSourceVPNCore,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.fetchSubscription("fleet"); err != nil {
		t.Fatalf("a vpn-core record was asked for a URL: %v", err)
	}
}

func TestVPNCoreIdentityFilterReachesTheExport(t *testing.T) {
	rt, host := newVPNCoreRuntime(t, "vless://one")
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "one-user", Source: subscriptionSourceVPNCore, VPNIdentity: "user-42",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.fetchSubscription("one-user"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	request, _ := host.rpcCalls[0]["request"].(map[string]any)
	if request["user_id"] != "user-42" {
		t.Fatalf("identity filter did not reach the export: %+v", request)
	}
}

func TestVPNCoreOmitsIdentityWhenUnset(t *testing.T) {
	rt, host := newVPNCoreRuntime(t, "vless://one")
	if err := rt.saveSubscription(subscriptionRecord{ID: "all", Source: subscriptionSourceVPNCore}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.fetchSubscription("all"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	request, _ := host.rpcCalls[0]["request"].(map[string]any)
	if _, present := request["user_id"]; present {
		t.Fatalf("an empty identity was sent as a filter: %+v", request)
	}
}

// Serving nothing reaches a client as "you have no nodes" and wipes its
// configuration. An empty export must fail here rather than travel onward.
func TestVPNCoreEmptyExportIsAnError(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "empty", Source: subscriptionSourceVPNCore}); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := rt.fetchSubscription("empty")
	if err == nil {
		t.Fatal("an empty vpn-core export was accepted")
	}
	if !strings.Contains(err.Error(), "no nodes") {
		t.Fatalf("error must name the cause, got %v", err)
	}
}

// The core holds the snapshot, but on the very first request it has none. A
// vpn-core record has no inline content to fall back to, so render must reach
// the export itself or the subscription fails until something warms it.
func TestVPNCoreRendersOnFirstRequestWithNoSnapshot(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t, "vless://11111111-1111-1111-1111-111111111111@example.com:443?security=reality&sni=a.com&fp=chrome&pbk=x#node-a")
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "fleet", Name: "Fleet", Source: subscriptionSourceVPNCore, Target: "URI",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "fleet", Format: "", UAClass: ""})
	if err != nil {
		t.Fatalf("render with no snapshot failed: %v", err)
	}
	if strings.TrimSpace(out.Content) == "" {
		t.Fatal("render produced nothing; an empty body is never servable")
	}
}

func TestVPNCoreFetchFailureIsReported(t *testing.T) {
	rt, host := newVPNCoreRuntime(t, "vless://one")
	host.failWith = errAsHostFailure()
	if err := rt.saveSubscription(subscriptionRecord{ID: "fleet", Source: subscriptionSourceVPNCore}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.fetchSubscription("fleet"); err == nil {
		t.Fatal("a failing export was reported as success")
	}
}

func errAsHostFailure() error { return &hostFailure{} }

type hostFailure struct{}

func (*hostFailure) Error() string { return "vpn-core unreachable" }

func TestPreviewResolvesAnUnsavedFleetDraft(t *testing.T) {
	// A draft that reads the fleet has no pasted content and no stored record —
	// the preview must resolve the live export, not report "no content".
	rt, host := newVPNCoreRuntime(t,
		"vless://11111111-1111-1111-1111-111111111111@example.com:443?security=reality&sni=a.com&fp=chrome&pbk=x#node-a",
		"vless://22222222-2222-2222-2222-222222222222@example.net:443?security=reality&sni=b.com&fp=chrome&pbk=y#node-b")
	raw, err := json.Marshal(map[string]any{"source": "vpn-core"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res := rt.handleSubscriptionCall(callPayload{Method: "preview", Payload: raw})
	if !res.OK {
		t.Fatalf("preview of an unsaved fleet draft failed: %s", res.Error)
	}
	var out previewResult
	if err := json.Unmarshal(res.Result, &out); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if out.NodeCount != 2 {
		t.Fatalf("expected the export's 2 nodes, got %d", out.NodeCount)
	}
	if len(host.rpcCalls) != 1 {
		t.Fatalf("expected exactly one export rpc, got %d", len(host.rpcCalls))
	}
}
