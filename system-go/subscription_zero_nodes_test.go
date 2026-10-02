package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const providerErrorPage = "<html><body>429 Too Many Requests</body></html>"

// A client that receives an empty but successful subscription deletes every
// node it had. The old guard only refused an empty BODY, and producers emit a
// non-empty skeleton for zero nodes ("proxies:\n", an empty sing-box object),
// so a provider's error page served 200 to Clash, Stash and sing-box clients.
// Every target must refuse it, with a stable code the core can map to its
// audit reason.
func TestRenderRefusesZeroNodeDocuments(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	seedSub(t, rt, "garbage", nil, providerErrorPage)
	for _, target := range []string{"URI", "ClashMeta", "Clash", "Stash", "sing-box", "Surge", "JSON"} {
		out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "garbage", Format: "base64", Target: target})
		if err == nil {
			t.Fatalf("%s served %d bytes for a provider error page: %q", target, len(out.Content), head(out.Content, 60))
		}
		if !strings.Contains(err.Error(), zeroNodesForTargetCode) {
			t.Fatalf("%s refusal does not carry %s: %v", target, zeroNodesForTargetCode, err)
		}
	}
}

// Legacy Clash carries neither VLESS nor Hysteria2, so this fleet renders for
// it as "proxies:\n". That document must be refused rather than served.
func TestRenderRefusesALegacyClashDocumentWithNoNodes(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	vlessAndHy2Only := "vless://0cd403ef-ce9b-4c7c-b3e8-472397e616f7@a.example:34099?security=reality&type=tcp&sni=x.example&fp=chrome&flow=xtls-rprx-vision&pbk=n6g3w4_4iiLhntHLX3DFRwuOWmu28PhaLcLj9D3jIw8#one\n" +
		"hysteria2://d9362106-8c97-46e2-ac27-7f9a8e785d95@b.example:13434?insecure=1&sni=b.example#two\n"
	seedSub(t, rt, "fleet", nil, vlessAndHy2Only)
	_, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "fleet", Format: "base64", UAClass: "clash"})
	if err == nil || !strings.Contains(err.Error(), zeroNodesForTargetCode) {
		t.Fatalf("legacy Clash with no carriable nodes must be refused, got %v", err)
	}

	// The console asks for an explanation and still gets the document, so it
	// can say why the link would be refused.
	explained, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "fleet", Format: "plain", UAClass: "clash", Explain: true})
	if err != nil {
		t.Fatalf("explained render: %v", err)
	}
	if !explained.ZeroNodes || explained.DroppedNodeCount != 2 {
		t.Fatalf("explained render zero=%v dropped=%d, want true and 2", explained.ZeroNodes, explained.DroppedNodeCount)
	}
}

// A combination whose members together carry nothing for this client is
// refused on the same rule.
func TestRenderRefusesAZeroNodeCollection(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	seedSub(t, rt, "a", nil, providerErrorPage)
	seedSub(t, rt, "b", nil, "hysteria2://d9362106-8c97-46e2-ac27-7f9a8e785d95@b.example:13434?insecure=1&sni=b.example#two\n")
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Kind: kindCollection, Name: "c", Members: []string{"b"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "c", Format: "base64", Target: "Clash"})
	if err == nil || !strings.Contains(err.Error(), zeroNodesForTargetCode) {
		t.Fatalf("a collection with nothing Clash can carry must be refused, got %v", err)
	}
}

// The refresh path is where a provider's error page used to replace the last
// good snapshot: any non-empty 2xx body was accepted. A body that yields no
// nodes is now a failed fetch, so the core keeps serving what it had, and the
// record says why. A good fetch afterwards recovers.
func TestFetchRefusesAProviderBodyWithNoNodes(t *testing.T) {
	rt, host := newFetchRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "p", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	host.body = []byte(providerErrorPage)
	res := fetchViaMethod(t, rt, "p")
	if res.OK {
		t.Fatalf("a provider error page was accepted as a snapshot: %s", res.Result)
	}
	if !strings.Contains(res.Error, providerNoNodesCode) {
		t.Fatalf("fetch refusal does not carry %s: %s", providerNoNodesCode, res.Error)
	}
	if rec := storedRecord(t, rt, "p"); rec.LastFetchOK || !strings.Contains(rec.LastError, providerNoNodesCode) {
		t.Fatalf("record bookkeeping ok=%v error=%q, want a failure naming %s", rec.LastFetchOK, rec.LastError, providerNoNodesCode)
	}

	host.body = []byte(shareFleetFixture(2))
	res = fetchViaMethod(t, rt, "p")
	if !res.OK {
		t.Fatalf("a good fetch after a bad one failed: %s", res.Error)
	}
	var payload struct {
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal(res.Result, &payload); err != nil || !strings.Contains(payload.Raw, "vless://") {
		t.Fatalf("recovered fetch payload: %v %q", err, head(payload.Raw, 60))
	}
	if rec := storedRecord(t, rt, "p"); !rec.LastFetchOK {
		t.Fatalf("record still reports the old failure: %q", rec.LastError)
	}
}

// The probe answers the same question for the browser: a provider that answers
// with something that is not a subscription is not available.
func TestProbeReportsAProviderBodyWithNoNodesAsUnavailable(t *testing.T) {
	rt, host := newFetchRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "p", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	host.body = []byte(providerErrorPage)
	raw, _ := json.Marshal(map[string]any{"subscription_id": "p"})
	res := rt.handleSubscriptionCall(callPayload{Method: "probe", Payload: raw})
	var probe subscriptionProbeResult
	if err := json.Unmarshal(res.Result, &probe); err != nil {
		t.Fatalf("decode probe: %v", err)
	}
	if probe.OK || probe.ErrorCode != "source_unavailable" {
		t.Fatalf("probe ok=%v code=%q, want unavailable", probe.OK, probe.ErrorCode)
	}
}

// A combination member whose provider answered with an error page is a failed
// member, so strict mode refuses the refresh instead of storing a snapshot
// that silently lost it.
func TestCollectionRefreshTreatsAMemberWithNoNodesAsFailed(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	seedSub(t, rt, "good", nil, shareFleetFixture(1))
	seedSub(t, rt, "bad", nil, providerErrorPage)
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Kind: kindCollection, Name: "c", Members: []string{"good", "bad"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.fetchSubscription("c"); err == nil || !strings.Contains(err.Error(), providerNoNodesCode) {
		t.Fatalf("strict collection refresh with a garbage member must fail naming %s, got %v", providerNoNodesCode, err)
	}

	// Skip mode is the operator's choice to serve the survivors.
	if err := rt.saveSubscription(subscriptionRecord{ID: "c-skip", Kind: kindCollection, Name: "c-skip", Members: []string{"good", "bad"}, FailureMode: failureModeSkip}); err != nil {
		t.Fatalf("save skip: %v", err)
	}
	snap, err := rt.fetchSubscription("c-skip")
	if err != nil {
		t.Fatalf("skip-mode refresh: %v", err)
	}
	var envelope snapshotArtifacts
	if err := json.Unmarshal([]byte(snap.Raw), &envelope); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(envelope.Members) != 1 || envelope.Members[0].SubName != "good" {
		t.Fatalf("skip mode kept %+v, want only the good member", envelope.Members)
	}
}

// A combination's refresh counts every unchained member in one engine call. On
// a worker whose warm runtime is still booting each call takes the isolated
// path, so one call per member would put a large combination's refresh at risk
// of its time budget.
func TestCollectionRefreshCountsItsMembersInOneEngineCall(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	var members []string
	for _, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		seedSub(t, rt, id, nil, shareFleetFixture(1))
		members = append(members, id)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Kind: kindCollection, Name: "c", Members: members}); err != nil {
		t.Fatalf("save: %v", err)
	}
	warmBefore, isolatedBefore := rt.engine.pathCounts()
	if _, err := rt.fetchSubscription("c"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	warmAfter, isolatedAfter := rt.engine.pathCounts()
	if calls := (warmAfter - warmBefore) + (isolatedAfter - isolatedBefore); calls != 1 {
		t.Fatalf("refreshing five unchained members took %d engine calls, want 1", calls)
	}
}
