package main

import (
	"os"
	"testing"
)

// The 2026-08-11 production import of the operator's standalone Sub-Store
// (4 subscriptions, 2 combinations, 16 script files) died mid-flight with a
// 502: per-record saves cost a document read+write each plus one key per
// script program, and the signed host_calls budget for `import` is 3. The
// batch path must persist the whole wave in one document write plus one key
// per program — this test pins the round-trip count so the budget keeps
// fitting. Fixture: the actual converted production data.
func TestImportBatchAmortisesHostCalls(t *testing.T) {
	rt, host := newKVRuntime(t)
	total := 0
	for _, tag := range []string{"A", "B", "C"} {
		data, err := os.ReadFile("/tmp/lattice-probe/import_" + tag + ".json")
		if err != nil {
			t.Skipf("fixture %s not present on this machine", tag)
		}
		out, err := rt.importBackup(data)
		if err != nil {
			t.Fatalf("importBackup %s: %v", tag, err)
		}
		if len(out.Skipped) != 0 {
			t.Fatalf("importBackup %s skipped %v", tag, out.Skipped)
		}
		total += len(out.Imported)
	}
	if total != 22 {
		t.Fatalf("expected 22 records imported, got %d", total)
	}
	// 16 script programs + 3 document writes (one per import call).
	if host.puts > 20 {
		t.Fatalf("host KV puts = %d, want <= 20 (per-record saves would be 38+)", host.puts)
	}
	doc, err := rt.loadSubscriptionRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Records) != 22 {
		t.Fatalf("stored %d records, want 22", len(doc.Records))
	}
	// A script file's program must be readable back through its own key.
	rec, err := rt.getSubscription("imported-file-for-cdcd-self-use")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Content) < 1000 {
		t.Fatalf("script content did not round-trip through its own key (%d bytes)", len(rec.Content))
	}
}
