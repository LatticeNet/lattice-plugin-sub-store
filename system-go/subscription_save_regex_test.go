package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func steps(raw ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(raw))
	for i, r := range raw {
		out[i] = json.RawMessage(r)
	}
	return out
}

const lookaheadFilter = `{"type":"Regex Filter","args":{"regex":["^(?!.*(HK|TW)).*$"],"keep":true}}`

// A save that brings in a pattern RE2 refuses is refused before anything is
// written, with the regex_incompatible code leading the message (the editor
// reads it there), the step and pattern named, and the rewrite offered for
// the keep-mode negative-lookahead idiom. The rewritten chain saves.
func TestSaveRefusesLookaheadWithRewrite(t *testing.T) {
	rt, host := newKVRuntime(t)
	res := callSubscription(t, rt, "save", map[string]any{"subscription": map[string]any{
		"id": "s1", "name": "provider", "content": "vless://example",
		"process": steps(`{"type":"Sort Operator","args":"asc"}`, lookaheadFilter),
	}})
	if res.OK {
		t.Fatalf("a lookahead pattern was saved: %s", res.Result)
	}
	for _, part := range []string{`process step 2`, `"^(?!.*(HK|TW)).*$"`, `rewrite offered: a drop-mode Regex Filter on "HK|TW"`} {
		if !strings.HasPrefix(res.Error, "regex_incompatible: ") || !strings.Contains(res.Error, part) {
			t.Fatalf("refusal %q lacks %q", res.Error, part)
		}
	}
	if len(host.values) != 0 {
		t.Fatalf("a refused save wrote %d keys", len(host.values))
	}

	// A lookbehind has no rewrite to offer; the refusal still names it.
	res = callSubscription(t, rt, "save", map[string]any{"subscription": map[string]any{
		"id": "s1", "name": "provider", "content": "vless://example",
		"process": steps(`{"type":"Regex Delete Operator","args":["(?<=HK)\\d+"]}`),
	}})
	if res.OK || !strings.HasPrefix(res.Error, "regex_incompatible: ") || strings.Contains(res.Error, "rewrite offered") || !strings.Contains(res.Error, `(?<=HK)\\d+`) {
		t.Fatalf("lookbehind save = ok %v, %q", res.OK, res.Error)
	}

	// The rewrite keeps the same nodes and saves.
	var saved saveResult
	decodeResult(t, callSubscription(t, rt, "save", map[string]any{"subscription": map[string]any{
		"id": "s1", "name": "provider", "content": "vless://example",
		"process": steps(`{"type":"Sort Operator","args":"asc"}`, `{"type":"Regex Filter","args":{"regex":["HK|TW"],"keep":false}}`),
	}}), &saved)
	if !saved.Saved {
		t.Fatalf("the rewritten chain was not saved: %+v", saved)
	}
	if flags := indexEntryOf(t, rt, "s1").Flags; flags != (indexFlags{}) {
		t.Fatalf("the rewritten chain is flagged: %+v", flags)
	}

	// An edit that adds the pattern to a stored record is refused and leaves
	// the record as it was.
	before := getRecord(t, rt, "s1")
	edit := before
	edit.Process = append(append([]json.RawMessage{}, before.Process...), json.RawMessage(lookaheadFilter))
	res = callSubscription(t, rt, "save", map[string]any{"subscription": edit, "if_revision": before.Revision})
	if res.OK || !strings.HasPrefix(res.Error, "regex_incompatible: ") || !strings.Contains(res.Error, "process step 3") {
		t.Fatalf("adding a lookahead step = ok %v, %q", res.OK, res.Error)
	}
	if after := getRecord(t, rt, "s1"); after.Revision != before.Revision {
		t.Fatal("a refused edit changed the stored record")
	}
}

// A record the migration flagged keeps saving when the edit leaves its
// patterns alone, however the editor re-serialises the chain, and the flag
// stays in the index; an edit that changes the pattern to another one RE2
// refuses, or adds a second, is refused; disabling the step clears the flag.
func TestSaveOfUnrelatedFieldKeepsFlaggedRecord(t *testing.T) {
	host := newKVHostCaller()
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	seedLegacyStore(t, host, []subscriptionRecord{
		{ID: "flagged", Name: "provider", Content: "vless://example", Process: steps(`{"type":"Sort Operator","args":"asc"}`, lookaheadFilter)},
	})
	if reply := migrateStoreUntilDone(t, rt); !reply.Done || strings.Join(reply.RegexIncompatible, ",") != "flagged" {
		t.Fatalf("migration = %+v", reply)
	}
	if !indexEntryOf(t, rt, "flagged").Flags.RegexIncompatible {
		t.Fatal("the migration did not flag the record")
	}

	save := func(rec subscriptionRecord, revision string) response {
		return callSubscription(t, rt, "save", map[string]any{"subscription": rec, "if_revision": revision})
	}

	// An unrelated field.
	rec := getRecord(t, rt, "flagged")
	rec.Name = "renamed provider"
	var saved saveResult
	decodeResult(t, save(rec, rec.Revision), &saved)
	if !saved.Saved || saved.Subscription.Name != "renamed provider" {
		t.Fatalf("the unrelated edit was not saved: %+v", saved)
	}
	if !indexEntryOf(t, rt, "flagged").Flags.RegexIncompatible {
		t.Fatal("the unrelated edit dropped the flag")
	}

	// The same chain written in another key order, and moved behind a new
	// compatible step.
	rec = getRecord(t, rt, "flagged")
	rec.Process = steps(`{"type":"Useless Filter"}`, `{"args":{"keep":true,"regex":["^(?!.*(HK|TW)).*$"]},"type":"Regex Filter"}`, `{"type":"Sort Operator","args":"asc"}`)
	decodeResult(t, save(rec, rec.Revision), &saved)
	if !saved.Saved || !indexEntryOf(t, rt, "flagged").Flags.RegexIncompatible {
		t.Fatalf("re-serialising the flagged chain was refused or unflagged: %+v", saved)
	}

	rec = getRecord(t, rt, "flagged")
	changed := rec
	changed.Process = steps(`{"type":"Regex Filter","args":{"regex":["^(?!.*(JP)).*$"],"keep":true}}`)
	if res := save(changed, rec.Revision); res.OK || !strings.HasPrefix(res.Error, "regex_incompatible: ") || !strings.Contains(res.Error, `drop-mode Regex Filter on "JP"`) {
		t.Fatalf("changing the pattern = ok %v, %q", res.OK, res.Error)
	}
	doubled := rec
	doubled.Process = append(append([]json.RawMessage{}, rec.Process...), json.RawMessage(lookaheadFilter))
	if res := save(doubled, rec.Revision); res.OK || !strings.HasPrefix(res.Error, "regex_incompatible: ") {
		t.Fatalf("adding a second lookahead step = ok %v, %q", res.OK, res.Error)
	}
	if after := getRecord(t, rt, "flagged"); after.Revision != rec.Revision {
		t.Fatal("a refused edit changed the stored record")
	}

	disabled := rec
	disabled.Process = steps(`{"type":"Regex Filter","disabled":true,"args":{"regex":["^(?!.*(HK|TW)).*$"],"keep":true}}`)
	decodeResult(t, save(disabled, rec.Revision), &saved)
	if !saved.Saved || indexEntryOf(t, rt, "flagged").Flags.RegexIncompatible {
		t.Fatalf("disabling the step did not clear the flag: %+v", indexEntryOf(t, rt, "flagged").Flags)
	}
}
