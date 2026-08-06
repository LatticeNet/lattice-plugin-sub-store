package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The subscription interface shipped with `list` but no way to create, read
// with content, edit or delete a record: the store functions existed but only
// migration and backup restore could reach them. These cover the methods that
// close that gap.

func callSubscription(t *testing.T, rt *runtime, method string, payload any) response {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return rt.handleSubscriptionCall(callPayload{Method: method, Payload: raw})
}

func decodeResult[T any](t *testing.T, res response, out *T) {
	t.Helper()
	if !res.OK {
		t.Fatalf("call failed: %s", res.Error)
	}
	if err := json.Unmarshal(res.Result, out); err != nil {
		t.Fatalf("decode result %s: %v", string(res.Result), err)
	}
}

func TestSubscriptionSaveThenGetRoundTrip(t *testing.T) {
	rt, _ := newKVRuntime(t)

	res := callSubscription(t, rt, "save", map[string]any{
		"subscription": map[string]any{
			"id": "s1", "name": "provider", "url": "https://example.invalid/sub",
			"ua": "Surge", "target": "Surge", "content": "vless://example",
		},
	})
	var saved struct {
		Subscription subscriptionRecord `json:"subscription"`
		Saved        bool               `json:"saved"`
	}
	decodeResult(t, res, &saved)
	if !saved.Saved || saved.Subscription.ID != "s1" {
		t.Fatalf("save did not report the stored record: %+v", saved)
	}

	// `list` must not carry content; `get` must.
	var listed struct {
		Subscriptions []map[string]any `json:"subscriptions"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	if len(listed.Subscriptions) != 1 {
		t.Fatalf("list = %d entries, want 1", len(listed.Subscriptions))
	}
	if _, leaked := listed.Subscriptions[0]["content"]; leaked {
		t.Fatal("list leaked inline content; it is a management view, not a payload dump")
	}

	var got struct {
		Subscription subscriptionRecord `json:"subscription"`
	}
	decodeResult(t, callSubscription(t, rt, "get", map[string]any{"subscription_id": "s1"}), &got)
	if got.Subscription.Content != "vless://example" || got.Subscription.Target != "Surge" {
		t.Fatalf("get lost the editable fields: %+v", got.Subscription)
	}
}

func TestSubscriptionSaveMethodRequiresAnID(t *testing.T) {
	rt, _ := newKVRuntime(t)
	res := callSubscription(t, rt, "save", map[string]any{
		"subscription": map[string]any{"name": "no id"},
	})
	if res.OK {
		t.Fatal("a record without an id was accepted")
	}
}

// Origin marks a record as having come from a migration. A caller must not be
// able to claim it, or an operator reading the list cannot trust what it says
// about where a subscription came from.
func TestSubscriptionSaveCannotForgeOrigin(t *testing.T) {
	rt, _ := newKVRuntime(t)

	res := callSubscription(t, rt, "save", map[string]any{
		"subscription": map[string]any{
			"id": "s1", "name": "hand made",
			"origin": map[string]any{"source": "https://elsewhere.invalid", "kind": "claimed"},
		},
	})
	var saved struct {
		Subscription subscriptionRecord `json:"subscription"`
	}
	decodeResult(t, res, &saved)
	if saved.Subscription.Origin != nil {
		t.Fatalf("a caller forged a migration origin: %+v", saved.Subscription.Origin)
	}

	var listed struct {
		Subscriptions []struct {
			Imported bool `json:"imported"`
		} `json:"subscriptions"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	if listed.Subscriptions[0].Imported {
		t.Fatal("list reported a hand-made record as imported")
	}
}

// Editing a migrated record must not silently erase where it came from.
func TestSubscriptionSavePreservesOriginOnEdit(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "m1", Name: "migrated", Origin: &migratedOrigin{Source: "https://upstream.invalid", Kind: "sub"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := callSubscription(t, rt, "save", map[string]any{
		"subscription": map[string]any{"id": "m1", "name": "renamed"},
	})
	var saved struct {
		Subscription subscriptionRecord `json:"subscription"`
	}
	decodeResult(t, res, &saved)
	if saved.Subscription.Name != "renamed" {
		t.Fatalf("edit did not take: %+v", saved.Subscription)
	}
	if saved.Subscription.Origin == nil || saved.Subscription.Origin.Source != "https://upstream.invalid" {
		t.Fatalf("editing a migrated record erased its origin: %+v", saved.Subscription.Origin)
	}
}

func TestSubscriptionSaveRejectsUnknownOperator(t *testing.T) {
	rt, _ := newKVRuntime(t)
	res := callSubscription(t, rt, "save", map[string]any{
		"subscription": map[string]any{
			"id": "s1", "name": "typo",
			"operators": []map[string]any{{"type": "Definitely Not An Operator"}},
		},
	})
	if res.OK {
		t.Fatal("an unknown operator type was accepted; upstream ignores these silently and this is the guard against that")
	}
}

func TestSubscriptionDeleteRemovesTheRecord(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", Name: "one"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var deleted struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	decodeResult(t, callSubscription(t, rt, "delete", map[string]any{"subscription_id": "s1"}), &deleted)
	if !deleted.Deleted || deleted.ID != "s1" {
		t.Fatalf("delete did not report the removal: %+v", deleted)
	}

	if res := callSubscription(t, rt, "get", map[string]any{"subscription_id": "s1"}); res.OK {
		t.Fatal("record still readable after delete")
	}
	if res := callSubscription(t, rt, "delete", map[string]any{"subscription_id": "s1"}); res.OK {
		t.Fatal("deleting a missing record reported success")
	}
}

func TestSubscriptionGetAndDeleteRequireAnID(t *testing.T) {
	rt, _ := newKVRuntime(t)
	for _, method := range []string{"get", "delete"} {
		res := callSubscription(t, rt, method, map[string]any{})
		if res.OK {
			t.Fatalf("%s accepted an empty subscription_id", method)
		}
		if !strings.Contains(res.Error, "subscription_id is required") {
			t.Fatalf("%s error must name the missing field, got %q", method, res.Error)
		}
	}
}
