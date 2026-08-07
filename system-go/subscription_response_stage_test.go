package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// One chain, two stages.
//
// The engine walks a record's chain twice: `process` over the nodes, skipping
// response transformers, and `processResponse` over the finished body, running
// only those. This plugin used to treat the two vocabularies as alternatives and
// validate a chain against one of them, which refused a legitimate chain and
// left subscriptions unable to rewrite what they serve — even though upstream
// offers the step in exactly that palette.

func responseTransformerStep(t *testing.T, script string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type": "Response Transformer",
		"args": map[string]any{"mode": "script", "content": script},
	})
	if err != nil {
		t.Fatalf("marshal step: %v", err)
	}
	return raw
}

func TestSubscriptionResponseChainRewritesWhatIsServed(t *testing.T) {
	rt, _ := newKVRuntime(t)
	step := responseTransformerStep(t,
		`function transformFunction(res) { res.body = res.body + "\n# reviewed"; return res; }`)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "s1", Name: "S1", Content: realNodeA, Target: "URI",
		Process: []json.RawMessage{step},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := rt.renderSubscription("s1", "plain", "", "", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out.Content, "# reviewed") {
		t.Fatalf("the response chain did not run:\n%s", out.Content)
	}
	// The nodes still have to be there — the response stage runs after the node
	// stage, it does not replace it.
	if !strings.Contains(out.Content, "node-a") {
		t.Fatalf("the node stage was lost:\n%s", out.Content)
	}
}

// A collection serves a merged body, and the same chain applies to it.
func TestCollectionResponseChainRewritesWhatIsServed(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedSub(t, rt, "m1", nil, realNodeA)
	step := responseTransformerStep(t,
		`function transformFunction(res) { res.body = "# header\n" + res.body; return res; }`)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c1", Kind: kindCollection, Name: "C1", Members: []string{"m1"},
		Process: []json.RawMessage{step},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.renderSubscription("c1", "plain", "", "", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.HasPrefix(out.Content, "# header") {
		t.Fatalf("the collection's response chain did not run:\n%s", out.Content)
	}
}

// The two kinds coexist in one chain: the node stage skips the transformer, the
// response stage skips the node operator, and both effects land.
func TestOneChainCarriesBothKindsOfStep(t *testing.T) {
	rt, _ := newKVRuntime(t)
	// The engine hands this constructor the array directly and destructures
	// {expr, now} out of each entry: `function lI(e) { … for (const {expr, now}
	// of e) … }`. Wrapping it in {value: …} makes `e` an object, and iterating
	// an object throws.
	rename, err := json.Marshal(map[string]any{
		"type": "Regex Rename Operator",
		"args": []any{map[string]any{"expr": "^node", "now": "renamed"}},
	})
	if err != nil {
		t.Fatalf("marshal rename: %v", err)
	}
	step := responseTransformerStep(t,
		`function transformFunction(res) { res.body = res.body + "\n# done"; return res; }`)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "both", Name: "Both", Content: realNodeA, Target: "URI",
		Process: []json.RawMessage{json.RawMessage(rename), step},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.renderSubscription("both", "plain", "", "", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out.Content, "renamed-a") {
		t.Fatalf("the node stage did not run:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "# done") {
		t.Fatalf("the response stage did not run:\n%s", out.Content)
	}
}

// A chain with nothing for the response stage must not start the engine a
// second time. Serving is the hot path and a spare VM boot per request is the
// cost the subscription cache exists to avoid.
func TestNoResponseStepMeansNoSecondEngineRun(t *testing.T) {
	rt, _ := newKVRuntime(t)
	rec := subscriptionRecord{ID: "plain", Name: "Plain", Content: realNodeA}
	body, headers, err := rt.applyResponseChain(rec, "unchanged", "text/plain")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if body != "unchanged" {
		t.Fatalf("the body was rewritten by an empty chain: %q", body)
	}
	if headers != nil {
		t.Fatalf("an empty chain produced headers: %+v", headers)
	}
}

// A response transformer that empties the body is the same hazard as an empty
// render: a client that receives an empty success deletes every node it had.
func TestAResponseChainThatEmptiesTheBodyIsRefused(t *testing.T) {
	rt, _ := newKVRuntime(t)
	step := responseTransformerStep(t,
		`function transformFunction(res) { res.body = ""; return res; }`)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "empty", Name: "Empty", Content: realNodeA, Target: "URI",
		Process: []json.RawMessage{step},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.renderSubscription("empty", "plain", "", "", nil); err == nil {
		t.Fatal("a chain that emptied the body was served")
	}
}

// Both vocabularies belong to one chain, so storing either must be accepted.
func TestSavingAcceptsBothVocabularies(t *testing.T) {
	rt, _ := newKVRuntime(t)
	step := responseTransformerStep(t, `function transformFunction(res) { return res; }`)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "s", Name: "S", Content: realNodeA,
		Process: []json.RawMessage{step},
	}); err != nil {
		t.Fatalf("a subscription was refused a response transformer: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "f", Kind: kindFile, Name: "F", FileType: fileTypePlain, Content: "text",
		Process: []json.RawMessage{step},
	}); err != nil {
		t.Fatalf("a file was refused a response transformer: %v", err)
	}
}
