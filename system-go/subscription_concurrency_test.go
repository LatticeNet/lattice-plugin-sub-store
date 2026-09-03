package main

import (
	"encoding/json"
	"testing"
	"time"
)

// Lost-update protection on the save path.
//
// A save used to be a blind full-record overwrite. Two operators editing one
// record, or one operator editing a record a refresh or a restore had already
// moved, and the loser's work disappeared with nothing on screen to say it had
// happened. `if_revision` makes the write conditional.
//
// The two properties that matter pull against each other, so both are pinned
// here: a real content change must be caught, and a background write that
// changes nothing the operator can see must NOT be, or the conflict dialog
// becomes noise an operator learns to click through.

type saveResult struct {
	Subscription subscriptionRecord `json:"subscription"`
	Saved        bool               `json:"saved"`
	Conflict     *struct {
		ID           string             `json:"id"`
		Reason       string             `json:"reason"`
		Revision     string             `json:"revision"`
		Subscription subscriptionRecord `json:"subscription"`
	} `json:"conflict"`
}

func saveRecord(t *testing.T, rt *runtime, payload map[string]any) saveResult {
	t.Helper()
	res := callSubscription(t, rt, "save", payload)
	var out saveResult
	decodeResult(t, res, &out)
	return out
}

func getRecord(t *testing.T, rt *runtime, id string) subscriptionRecord {
	t.Helper()
	res := callSubscription(t, rt, "get", map[string]any{"subscription_id": id})
	var out struct {
		Subscription subscriptionRecord `json:"subscription"`
	}
	decodeResult(t, res, &out)
	return out.Subscription
}

func seed(t *testing.T, rt *runtime, id, name string) subscriptionRecord {
	t.Helper()
	saved := saveRecord(t, rt, map[string]any{
		"subscription": map[string]any{"id": id, "name": name, "url": "https://example.invalid/sub"},
	})
	if !saved.Saved {
		t.Fatalf("seed save was refused")
	}
	return saved.Subscription
}

func TestSaveReturnsARevisionAndGetAgrees(t *testing.T) {
	rt, _ := newKVRuntime(t)
	saved := seed(t, rt, "s1", "provider")

	if saved.Revision == "" {
		t.Fatal("save returned no revision, so a caller has nothing to send back")
	}
	if got := getRecord(t, rt, "s1"); got.Revision != saved.Revision {
		t.Fatalf("get and save disagree on the revision: %q vs %q", got.Revision, saved.Revision)
	}
}

func TestConditionalSaveAcceptsTheCurrentRevision(t *testing.T) {
	rt, _ := newKVRuntime(t)
	first := seed(t, rt, "s1", "provider")

	out := saveRecord(t, rt, map[string]any{
		"if_revision":  first.Revision,
		"subscription": map[string]any{"id": "s1", "name": "renamed", "url": "https://example.invalid/sub"},
	})
	if !out.Saved || out.Conflict != nil {
		t.Fatalf("a save carrying the current revision was refused: %+v", out.Conflict)
	}
	if out.Subscription.Revision == first.Revision {
		t.Fatal("the revision did not move after a content change")
	}
	if got := getRecord(t, rt, "s1"); got.Name != "renamed" {
		t.Fatalf("the accepted save did not land: name is %q", got.Name)
	}
}

func TestConditionalSaveRefusesAStaleRevision(t *testing.T) {
	rt, _ := newKVRuntime(t)
	first := seed(t, rt, "s1", "provider")

	// Someone else saves in between, the way a second operator would.
	if out := saveRecord(t, rt, map[string]any{
		"subscription": map[string]any{"id": "s1", "name": "theirs", "url": "https://example.invalid/sub"},
	}); !out.Saved {
		t.Fatal("the intervening save was refused")
	}

	out := saveRecord(t, rt, map[string]any{
		"if_revision":  first.Revision,
		"subscription": map[string]any{"id": "s1", "name": "mine", "url": "https://example.invalid/sub"},
	})
	if out.Saved {
		t.Fatal("a stale save was accepted, so the intervening edit was lost")
	}
	if out.Conflict == nil || out.Conflict.Reason != "stale" {
		t.Fatalf("expected a stale conflict, got %+v", out.Conflict)
	}
	// The caller needs the current record to say what changed: it is the only
	// party holding the copy that was read.
	if out.Conflict.Subscription.Name != "theirs" {
		t.Fatalf("the conflict did not carry the current record: %+v", out.Conflict.Subscription)
	}
	if out.Conflict.Revision == "" || out.Conflict.Revision != out.Conflict.Subscription.Revision {
		t.Fatalf("the conflict's revision is missing or disagrees with its record")
	}
	// And the refusal must be total: nothing of the losing write landed.
	if got := getRecord(t, rt, "s1"); got.Name != "theirs" {
		t.Fatalf("a refused save still wrote something: name is %q", got.Name)
	}
}

func TestConditionalSaveRefusesARecordDeletedUnderneath(t *testing.T) {
	rt, _ := newKVRuntime(t)
	first := seed(t, rt, "s1", "provider")

	if res := callSubscription(t, rt, "delete", map[string]any{"subscription_id": "s1"}); !res.OK {
		t.Fatalf("delete failed: %s", res.Error)
	}

	out := saveRecord(t, rt, map[string]any{
		"if_revision":  first.Revision,
		"subscription": map[string]any{"id": "s1", "name": "mine", "url": "https://example.invalid/sub"},
	})
	if out.Saved {
		t.Fatal("saving over a deleted record silently recreated it")
	}
	if out.Conflict == nil || out.Conflict.Reason != "deleted" {
		t.Fatalf("expected a deleted conflict, got %+v", out.Conflict)
	}
}

// The property that keeps the dialog credible. The fetch path writes
// LastFetchAt, LastFetchOK, LastError and Userinfo on a schedule nobody
// triggers, and those fields are preserved across an edit anyway. If they moved
// the revision, an operator who opened a record and typed for a minute would be
// told their edit conflicted with something they cannot see or act on.
func TestABackgroundFetchDoesNotInvalidateAnOpenEdit(t *testing.T) {
	rt, _ := newKVRuntime(t)
	first := seed(t, rt, "s1", "provider")

	rt.noteFetchOutcome("s1", time.Now(), "upload=1; download=2", nil)

	after := getRecord(t, rt, "s1")
	if after.LastFetchAt == "" {
		t.Fatal("the fetch bookkeeping was not written, so this proves nothing")
	}
	if after.Revision != first.Revision {
		t.Fatalf("a background fetch moved the revision: %q became %q", first.Revision, after.Revision)
	}

	out := saveRecord(t, rt, map[string]any{
		"if_revision":  first.Revision,
		"subscription": map[string]any{"id": "s1", "name": "renamed", "url": "https://example.invalid/sub"},
	})
	if !out.Saved {
		t.Fatalf("an edit was refused because of a refresh that changed nothing editable: %+v", out.Conflict)
	}
	// And the bookkeeping still survives the edit, as it did before.
	if got := getRecord(t, rt, "s1"); got.LastFetchAt == "" {
		t.Fatal("the save dropped the fetch bookkeeping")
	}
}

// Import, migrate and backup restore write records they never read, and an
// older UI does not send the field at all. None of them may start failing.
func TestASaveWithoutARevisionIsUnconditional(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seed(t, rt, "s1", "provider")

	out := saveRecord(t, rt, map[string]any{
		"subscription": map[string]any{"id": "s1", "name": "overwritten", "url": "https://example.invalid/sub"},
	})
	if !out.Saved {
		t.Fatalf("a save with no if_revision was refused: %+v", out.Conflict)
	}
	if got := getRecord(t, rt, "s1"); got.Name != "overwritten" {
		t.Fatalf("the unconditional save did not land: name is %q", got.Name)
	}
}

// A record stored before this field existed has no revision in the document.
// It must still be editable, and still protected, without a migration.
func TestALegacyRecordWithNoStoredRevisionIsStillProtected(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seed(t, rt, "s1", "provider")

	// Strip the stored revision, the way a document written by an older build
	// would look on disk.
	doc, err := rt.loadSubscriptionRecords()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i := range doc.Records {
		doc.Records[i].Revision = ""
	}
	if err := rt.saveSubscriptionRecords(doc); err != nil {
		t.Fatalf("save doc: %v", err)
	}

	// Reading it still yields a revision, computed rather than stored.
	legacy := getRecord(t, rt, "s1")
	if legacy.Revision == "" {
		t.Fatal("a record with no stored revision reported none, so it cannot be edited safely")
	}
	// That revision is accepted.
	if out := saveRecord(t, rt, map[string]any{
		"if_revision":  legacy.Revision,
		"subscription": map[string]any{"id": "s1", "name": "renamed", "url": "https://example.invalid/sub"},
	}); !out.Saved {
		t.Fatalf("a legacy record could not be saved with its computed revision: %+v", out.Conflict)
	}
	// And a wrong one is still refused.
	if out := saveRecord(t, rt, map[string]any{
		"if_revision":  legacy.Revision,
		"subscription": map[string]any{"id": "s1", "name": "again", "url": "https://example.invalid/sub"},
	}); out.Saved {
		t.Fatal("a stale revision was accepted on a legacy record")
	}
}

// A script file's program lives under its own key, so it is not part of the
// record's own bytes. ScriptDigest carries it into the fingerprint; without
// that, two operators editing one program would never conflict.
func TestAScriptProgramChangeMovesTheRevision(t *testing.T) {
	rt, _ := newKVRuntime(t)

	first := saveRecord(t, rt, map[string]any{
		"subscription": map[string]any{
			"id": "f1", "name": "rules", "kind": "file",
			"file_type": "script", "content": "export default async function () { return 1 }",
		},
	})
	if !first.Saved {
		t.Fatal("the script file could not be created")
	}

	second := saveRecord(t, rt, map[string]any{
		"subscription": map[string]any{
			"id": "f1", "name": "rules", "kind": "file",
			"file_type": "script", "content": "export default async function () { return 2 }",
		},
	})
	if !second.Saved {
		t.Fatal("the program edit was refused")
	}
	if second.Subscription.Revision == first.Subscription.Revision {
		t.Fatal("changing only the program left the revision unchanged, so a concurrent program edit would be lost silently")
	}

	// The first operator, still holding the original revision, is now stale.
	out := saveRecord(t, rt, map[string]any{
		"if_revision": first.Subscription.Revision,
		"subscription": map[string]any{
			"id": "f1", "name": "rules", "kind": "file",
			"file_type": "script", "content": "export default async function () { return 3 }",
		},
	})
	if out.Saved {
		t.Fatal("a stale program edit was accepted")
	}
	if out.Conflict == nil || out.Conflict.Reason != "stale" {
		t.Fatalf("expected a stale conflict, got %+v", out.Conflict)
	}
	// The program is deliberately not echoed back: answering with an empty
	// Content would make a diff claim the operator's program had been erased.
	if out.Conflict.Subscription.Content != "" {
		t.Fatal("the conflict echoed a program back; the UI would diff against a value the store did not send")
	}
}

// A record that stops being a script file must not keep the digest of the
// program it used to have, or its revision keeps describing content it no
// longer holds.
func TestLeavingScriptKindClearsTheProgramDigest(t *testing.T) {
	rt, _ := newKVRuntime(t)

	saveRecord(t, rt, map[string]any{
		"subscription": map[string]any{
			"id": "f1", "name": "rules", "kind": "file",
			"file_type": "script", "content": "export default async function () { return 1 }",
		},
	})
	out := saveRecord(t, rt, map[string]any{
		"subscription": map[string]any{
			"id": "f1", "name": "rules", "kind": "file",
			"file_type": "plain", "content": "just text",
		},
	})
	if !out.Saved {
		t.Fatal("changing the file type was refused")
	}
	if out.Subscription.ScriptDigest != "" {
		t.Fatalf("a plain file kept a program digest: %q", out.Subscription.ScriptDigest)
	}
}

// The fingerprint has to be stable across marshal/unmarshal, or a record would
// appear to change every time it was read back.
func TestTheRevisionIsStableAcrossARoundTrip(t *testing.T) {
	rec := subscriptionRecord{
		ID: "s1", Name: "provider", URL: "https://example.invalid/sub",
		Tags: []string{"home", "paid"}, Arguments: map[string]string{"b": "2", "a": "1"},
	}
	before := subscriptionRevision(rec)

	encoded, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded subscriptionRecord
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if after := subscriptionRevision(decoded); after != before {
		t.Fatalf("the revision changed across a round trip: %q became %q", before, after)
	}
}
