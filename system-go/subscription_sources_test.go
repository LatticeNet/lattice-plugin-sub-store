package main

import (
	"strings"
	"testing"
)

// Three named sources replace "whichever field happens to be set". Naming them
// matters because a record that carries both a stale URL and fresh pasted
// content used to resolve to the URL, which is not what the operator chose.

func TestLocalSourcePrefersPastedContentOverAStaleURL(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "m", Name: "Manual", Source: subscriptionSourceLocal,
		Content: "vless://pasted",
		// Left behind by an earlier edit: the operator switched to pasting.
		URL: "https://stale.invalid/sub",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rec, _ := rt.getSubscription("m")
	got, err := rt.resolveSubContent(rec)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "vless://pasted" {
		t.Fatalf("a manual subscription resolved to something else: %q", got)
	}
}

// Refreshing a manual subscription is a no-op, not a failure: the operator
// should not have to learn which button is meaningless for which source.
func TestRefreshingAManualSubscriptionReturnsItsContent(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "m", Name: "Manual", Source: subscriptionSourceLocal, Content: "vless://pasted",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.fetchSubscription("m")
	if err != nil {
		t.Fatalf("refresh of a manual subscription failed: %v", err)
	}
	if out.Raw != "vless://pasted" {
		t.Fatalf("refresh returned %q", out.Raw)
	}
}

func TestRemoteSourceWithoutALinkSaysSo(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "p", Name: "Provider", Source: subscriptionSourceRemote,
		// No URL, but content left over from a previous choice.
		Content: "vless://stale",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rec, _ := rt.getSubscription("p")
	_, err := rt.resolveSubContent(rec)
	if err == nil {
		t.Fatal("a provider subscription with no link silently served stale pasted content")
	}
	if !strings.Contains(err.Error(), "provider URL") {
		t.Fatalf("error must name what is missing, got %v", err)
	}
}

// Records written before the source was named must keep resolving the way they
// always did: whichever field is populated.
func TestUnnamedSourceStillResolvesURLThenContent(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "legacy", Name: "Legacy", Content: "vless://pasted",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rec, _ := rt.getSubscription("legacy")
	got, err := rt.resolveSubContent(rec)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "vless://pasted" {
		t.Fatalf("legacy record resolved to %q", got)
	}
}
