package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A member that cannot be fetched is a choice, not a rule. Strict protects a
// client from silently losing nodes; skipping keeps a large collection serving
// when one provider is down. Both are legitimate, so both are tested.

const realNodeA = "vless://11111111-1111-1111-1111-111111111111@a.example:443?security=reality&sni=a.com&fp=chrome&pbk=x#node-a"

func TestCollectionStrictModeFailsWhenAMemberFails(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t)
	seedSub(t, rt, "good", nil, realNodeA)
	// A vpn-core member with an export that returns nothing: the host in this
	// runtime answers with an empty list.
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "bad", Name: "Bad", Source: subscriptionSourceVPNCore,
	}); err != nil {
		t.Fatalf("seed bad: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c", Kind: kindCollection, Name: "C",
		Members: []string{"good", "bad"}, Target: "URI",
	}); err != nil {
		t.Fatalf("save collection: %v", err)
	}

	if _, err := rt.renderSubscription("c", "plain", "", "", nil); err == nil {
		t.Fatal("strict mode served a collection with a failed member")
	}
}

func TestCollectionNeverSkipsFailedGraphMembers(t *testing.T) {
	failure := json.RawMessage(`{"schema_version":1,"ok":false,"error":{"code":"unavailable","message":"unavailable"}}`)
	for name, setup := range map[string]func(*runtime, *vpnCoreGraphHost){
		"mixed legacy and graph": func(rt *runtime, host *vpnCoreGraphHost) {
			host.legacyLinks = []string{realNodeA}
			if err := rt.saveSubscription(subscriptionRecord{ID: "legacy", Source: subscriptionSourceVPNCore}); err != nil {
				t.Fatal(err)
			}
			if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
				t.Fatal(err)
			}
		},
		"two graph members": func(rt *runtime, host *vpnCoreGraphHost) {
			host.graphResponses = []json.RawMessage{canonicalGraphResponse(t, []string{graphRootA}), failure}
			for _, pair := range []struct{ id, root string }{{"graph-a", graphRootA}, {"graph-b", graphRootB}} {
				if err := rt.saveSubscription(subscriptionRecord{ID: pair.id, Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{pair.root}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
					t.Fatal(err)
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			host := &vpnCoreGraphHost{kvHostCaller: newKVHostCaller(), response: failure}
			rt := &runtime{host: host, engine: testEngineWithHeadroom()}
			setup(rt, host)
			members := []string{"legacy", "graph"}
			if name == "two graph members" {
				members = []string{"graph-a", "graph-b"}
			}
			if err := rt.saveSubscription(subscriptionRecord{ID: "collection", Kind: kindCollection, Members: members, Target: "URI", FailureMode: failureModeSkip}); err != nil {
				t.Fatal(err)
			}
			if output, err := rt.renderSubscription("collection", "plain", "", "", nil); err == nil || output.Content != "" {
				t.Fatalf("failed graph member produced partial collection: output=%+v err=%v", output, err)
			}
		})
	}
}

func TestScriptArtifactCollectionNeverSkipsFailedGraphMembers(t *testing.T) {
	failure := json.RawMessage(`{"schema_version":1,"ok":false,"error":{"code":"unavailable","message":"unavailable"}}`)
	host := &vpnCoreGraphHost{kvHostCaller: newKVHostCaller(), response: failure}
	rt := &runtime{host: host, engine: testEngineWithHeadroom()}
	seedSub(t, rt, "good", nil, realNodeA)
	if err := rt.saveSubscription(subscriptionRecord{ID: "graph", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "identity", EntryRoots: []string{graphRootA}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "collection", Kind: kindCollection, Members: []string{"good", "graph"}, FailureMode: failureModeSkip}); err != nil {
		t.Fatal(err)
	}
	if artifacts, err := rt.resolveScriptArtifacts(subscriptionRecord{ID: "file", Kind: kindFile, NodeSource: "collection"}); err == nil || len(artifacts) != 0 {
		t.Fatalf("failed graph member produced partial script artifacts: artifacts=%+v err=%v", artifacts, err)
	}
}

func TestCollectionSkipModeServesTheRest(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t)
	seedSub(t, rt, "good", nil, realNodeA)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "bad", Name: "Bad", Source: subscriptionSourceVPNCore,
	}); err != nil {
		t.Fatalf("seed bad: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c", Kind: kindCollection, Name: "C",
		Members: []string{"good", "bad"}, Target: "URI", FailureMode: failureModeSkip,
	}); err != nil {
		t.Fatalf("save collection: %v", err)
	}

	out, err := rt.renderSubscription("c", "plain", "", "", nil)
	if err != nil {
		t.Fatalf("skip mode failed anyway: %v", err)
	}
	if !strings.Contains(out.Content, "node-a") {
		t.Fatalf("skip mode dropped the healthy member too: %q", out.Content)
	}
}

// Skipping every member is not "serve what is left" — it is an empty
// subscription, and that must never leave as a success.
func TestCollectionSkipModeStillRefusesWhenEveryMemberFails(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t)
	for _, id := range []string{"bad1", "bad2"} {
		if err := rt.saveSubscription(subscriptionRecord{
			ID: id, Name: id, Source: subscriptionSourceVPNCore,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c", Kind: kindCollection, Name: "C",
		Members: []string{"bad1", "bad2"}, Target: "URI", FailureMode: failureModeSkip,
	}); err != nil {
		t.Fatalf("save collection: %v", err)
	}

	_, err := rt.renderSubscription("c", "plain", "", "", nil)
	if err == nil {
		t.Fatal("a collection whose every member failed was served as a success")
	}
	if !strings.Contains(err.Error(), "every member failed") {
		t.Fatalf("error must say what happened, got %v", err)
	}
}

// Strict is what an operator gets without choosing, because the failure it
// prevents is the destructive one.
func TestCollectionDefaultsToStrict(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedSub(t, rt, "a", nil, realNodeA)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c", Kind: kindCollection, Name: "C", Members: []string{"a"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := rt.getSubscription("c")
	if got.FailureMode == failureModeSkip {
		t.Fatal("a collection defaulted to skipping failures")
	}
}
