package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func step(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal step: %v", err)
	}
	return raw
}

func seedSub(t *testing.T, rt *runtime, id string, tags []string, links string) {
	t.Helper()
	if err := rt.saveSubscription(subscriptionRecord{
		ID: id, Name: id, Tags: tags, Content: links,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestCollectionRequiresMembersOrTags(t *testing.T) {
	rt, _ := newKVRuntime(t)
	err := rt.saveSubscription(subscriptionRecord{ID: "empty", Kind: kindCollection, Name: "Empty"})
	if err == nil {
		t.Fatal("a collection with nothing to gather was accepted")
	}
	if !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("error must say what is missing, got %v", err)
	}
}

// A collection is defined by what it gathers, so source fields on it are
// meaningless. Storing them would leave two answers to "where does this get its
// content" in one record.
func TestCollectionDropsSourceFields(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedSub(t, rt, "a", nil, "vless://a")
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c", Kind: kindCollection, Name: "C", Members: []string{"a"},
		URL: "https://example.invalid", Content: "stray", Source: subscriptionSourceVPNCore,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := rt.getSubscription("c")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.URL != "" || got.Content != "" || got.Source != "" {
		t.Fatalf("collection kept source fields: %+v", got)
	}
}

// The reverse: a sub is not a collection, so membership on it is meaningless.
func TestSubDropsMembershipFields(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "s", Name: "S", Content: "vless://a",
		Members: []string{"x"}, MemberTags: []string{"y"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := rt.getSubscription("s")
	if len(got.Members) != 0 || len(got.MemberTags) != 0 {
		t.Fatalf("sub kept membership fields: %+v", got)
	}
}

func TestCollectionMembersResolveByIDThenTag(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedSub(t, rt, "b", []string{"home"}, "vless://b")
	seedSub(t, rt, "a", []string{"home"}, "vless://a")
	seedSub(t, rt, "z", []string{"away"}, "vless://z")

	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c", Kind: kindCollection, Name: "C",
		Members: []string{"z"}, MemberTags: []string{"home"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rec, _ := rt.getSubscription("c")
	members, err := rt.collectionMembers(rec)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	var ids []string
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	// Explicit members keep the order given; tag matches follow, sorted, so the
	// result does not depend on storage order.
	if strings.Join(ids, ",") != "z,a,b" {
		t.Fatalf("member order = %v, want z,a,b", ids)
	}
}

func TestCollectionMemberListedTwiceAppearsOnce(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedSub(t, rt, "a", []string{"home"}, "vless://a")
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c", Kind: kindCollection, Name: "C",
		Members: []string{"a", "a"}, MemberTags: []string{"home"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rec, _ := rt.getSubscription("c")
	members, err := rt.collectionMembers(rec)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("duplicate member produced %d entries", len(members))
	}
}

// Dropping a missing member would shrink the served subscription, which reaches
// a client as "those nodes were withdrawn" — a lie the client acts on.
func TestCollectionMissingMemberIsAnError(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedSub(t, rt, "a", nil, "vless://a")
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c", Kind: kindCollection, Name: "C", Members: []string{"a", "gone"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rec, _ := rt.getSubscription("c")
	if _, err := rt.collectionMembers(rec); err == nil {
		t.Fatal("a missing member was silently skipped")
	}
}

// Two collections referencing each other would render forever.
func TestCollectionCannotContainACollection(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedSub(t, rt, "a", nil, "vless://a")
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "inner", Kind: kindCollection, Name: "Inner", Members: []string{"a"},
	}); err != nil {
		t.Fatalf("save inner: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "outer", Kind: kindCollection, Name: "Outer", Members: []string{"inner"},
	}); err != nil {
		t.Fatalf("save outer: %v", err)
	}
	rec, _ := rt.getSubscription("outer")
	if _, err := rt.collectionMembers(rec); err == nil {
		t.Fatal("a collection was accepted as a member of another collection")
	}
}

// A disabled step is kept so its arguments survive, but must not run. Deleting a
// step to try the chain without it is what the flag exists to avoid.
func TestDisabledStepsAreStoredButNotRun(t *testing.T) {
	rt, _ := newKVRuntime(t)
	enabled := step(t, map[string]any{"type": "Useless Filter"})
	disabled := step(t, map[string]any{"type": "Flag Operator", "disabled": true, "customName": "flags off"})
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "s", Name: "S", Content: "vless://a",
		Process: []json.RawMessage{enabled, disabled},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, _ := rt.getSubscription("s")
	if len(processSteps(got)) != 2 {
		t.Fatalf("a disabled step was dropped from storage: %d kept", len(processSteps(got)))
	}
	live, err := enabledOperators(got)
	if err != nil {
		t.Fatalf("enabledOperators: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("disabled step reached the engine: %d operators", len(live))
	}
}

// An unknown type is refused even when disabled: it would run the moment
// someone flipped the switch, and that is not when anyone is watching.
func TestDisabledStepStillValidatesItsType(t *testing.T) {
	rt, _ := newKVRuntime(t)
	bad := step(t, map[string]any{"type": "Definitely Not An Operator", "disabled": true})
	err := rt.saveSubscription(subscriptionRecord{
		ID: "s", Name: "S", Content: "vless://a", Process: []json.RawMessage{bad},
	})
	if err == nil {
		t.Fatal("an unknown operator was accepted because it was disabled")
	}
}

// The chain used to be called `operators`. A stored record still spelling it
// that way must keep working, and must come back normalised.
func TestLegacyOperatorsFieldIsReadAndNormalised(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "s", Name: "S", Content: "vless://a",
		Operators: []json.RawMessage{step(t, map[string]any{"type": "Useless Filter"})},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := rt.getSubscription("s")
	if len(got.Process) != 1 {
		t.Fatalf("legacy operators were not normalised into process: %+v", got)
	}
	if len(got.Operators) != 0 {
		t.Fatal("both spellings were stored; exactly one must be authoritative")
	}
	if len(processSteps(got)) != 1 {
		t.Fatal("the chain is not readable after normalisation")
	}
}

func TestCollectionMergesMembersAndConverts(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t)
	seedSub(t, rt, "a", nil, "vless://11111111-1111-1111-1111-111111111111@a.example:443?security=reality&sni=a.com&fp=chrome&pbk=x#node-a")
	seedSub(t, rt, "b", nil, "vless://22222222-2222-2222-2222-222222222222@b.example:443?security=reality&sni=b.com&fp=chrome&pbk=y#node-b")
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c", Kind: kindCollection, Name: "C", Members: []string{"a", "b"}, Target: "URI",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := rt.renderSubscription("c", "plain", "", "", nil)
	if err != nil {
		t.Fatalf("render collection: %v", err)
	}
	if !strings.Contains(out.Content, "node-a") || !strings.Contains(out.Content, "node-b") {
		t.Fatalf("collection did not merge both members: %q", out.Content)
	}
}
