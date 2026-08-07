package main

import (
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

	if _, err := rt.renderSubscription("c", "plain", "", ""); err == nil {
		t.Fatal("strict mode served a collection with a failed member")
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

	out, err := rt.renderSubscription("c", "plain", "", "")
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

	_, err := rt.renderSubscription("c", "plain", "", "")
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
