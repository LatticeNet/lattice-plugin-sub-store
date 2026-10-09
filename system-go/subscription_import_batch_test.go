package main

import (
	"os"
	"testing"
)

// The 2026-08-11 production import of the operator's standalone Sub-Store
// (4 subscriptions, 2 combinations, 16 script files) died mid-flight with a
// 502: per-record saves cost a document read and write each plus one key per
// script program, and the signed host_calls budget for `import` was 3. The
// batch path persists the whole wave with one write per record (a script's
// program is in its record) and one index write per import. This test pins
// the write count so the budget keeps fitting. Fixture: the actual converted
// production data.
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
	// 22 record writes + 3 index writes (one per import call).
	if host.puts > 25 {
		t.Fatalf("host KV puts = %d, want <= 25 (per-record saves would be 44+)", host.puts)
	}
	if listed := listedEntries(t, rt); len(listed) != 22 {
		t.Fatalf("stored %d records, want 22", len(listed))
	}
	// A script file's program must be readable back from its record.
	rec, err := rt.getSubscription("imported-file-for-cdcd-self-use")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Content) < 1000 {
		t.Fatalf("script content did not round-trip through its record (%d bytes)", len(rec.Content))
	}
}
