package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestImportCAlone(t *testing.T) {
	rt, host := newKVRuntime(t)
	for _, tag := range []string{"A", "B", "C"} {
		data, _ := os.ReadFile("/tmp/lattice-probe/import_" + tag + ".json")
		out, err := rt.importBackup(data)
		t.Logf("%s: imported=%d skipped=%v puts=%d err=%v", tag, len(out.Imported), out.Skipped, host.puts, err)
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
	}
	doc, _ := rt.loadSubscriptionRecords()
	raw, _ := json.Marshal(doc)
	t.Logf("final doc bytes: %d", len(raw))
}
