package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func seedForBackup(t *testing.T, rt *runtime) {
	t.Helper()
	for _, rec := range []subscriptionRecord{
		{ID: "b", Name: "second", URL: "https://b.invalid/sub"},
		{ID: "a", Name: "first", Content: "vless://one", Operators: []json.RawMessage{json.RawMessage(`{"type":"Flag Operator","args":{}}`)}},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatalf("seed %s: %v", rec.ID, err)
		}
	}
	if err := rt.saveSettings(pluginSettings{DefaultTarget: "Surge", DefaultUA: "Lattice/1.0"}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
}

// An export that depended on map order would diff against itself and be useless
// for comparing a backup against what is live.
func TestBackupExportIsStableAndRoundTrips(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedForBackup(t, rt)

	first, err := rt.exportBackup()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := rt.importBackup(first); err != nil {
		t.Fatalf("import: %v", err)
	}
	second, err := rt.exportBackup()
	if err != nil {
		t.Fatalf("re-export: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("round trip was not stable:\nfirst:  %s\nsecond: %s", first, second)
	}
	if !bytes.Contains(first, []byte("Surge")) {
		t.Fatal("settings were not included in the backup")
	}
}

// A restore that quietly removed newer work would be worse than no restore.
func TestBackupImportIsAdditiveAndReportsReplacements(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedForBackup(t, rt)
	backup, err := rt.exportBackup()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "newer", Name: "created after the backup"}); err != nil {
		t.Fatalf("seed newer: %v", err)
	}

	out, err := rt.importBackup(backup)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := rt.getSubscription("newer"); err != nil {
		t.Fatalf("a record absent from the backup was deleted by the restore: %v", err)
	}
	if len(out.Replaced) != 2 {
		t.Fatalf("replaced = %v, want both seeded records reported", out.Replaced)
	}
	if len(out.Imported) != 2 {
		t.Fatalf("imported = %v", out.Imported)
	}
}

func TestBackupImportRejectsAnUnknownFormat(t *testing.T) {
	rt, _ := newKVRuntime(t)
	for _, body := range []string{
		`{"format":"something.else","records":[]}`,
		`{"records":[]}`,
		`not json`,
	} {
		if _, err := rt.importBackup([]byte(body)); err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
}

func TestBackupImportReportsUnusableRecords(t *testing.T) {
	rt, _ := newKVRuntime(t)
	body := `{"format":"` + subscriptionBackupFormat + `","records":[
	  {"id":"ok","name":"fine"},
	  {"name":"no id"},
	  {"id":"bad-op","operators":[{"type":"Nonexistent Operator"}]},
	  {"id":"bad-graph","source":"vpn-core-graph","vpn_identity":"identity","entry_roots":["11111111-1111-4111-8111-111111111111"]}
	]}`
	out, err := rt.importBackup([]byte(body))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(out.Imported) != 1 {
		t.Fatalf("imported %v, want just the usable one", out.Imported)
	}
	if len(out.Skipped) != 3 {
		t.Fatalf("skipped = %+v, want three entries", out.Skipped)
	}
	if _, ok := out.Skipped["bad-op"]; !ok {
		t.Fatalf("the unknown-operator record was not named: %+v", out.Skipped)
	}
	if _, ok := out.Skipped["bad-graph"]; !ok {
		t.Fatalf("the graph without canonical options authority was not named: %+v", out.Skipped)
	}
}

func TestSettingsRoundTripAndBounds(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSettings(pluginSettings{DefaultTarget: "Clash", DefaultUA: "UA/1"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := rt.loadSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.DefaultTarget != "Clash" || got.DefaultUA != "UA/1" || got.SchemaVersion != 1 {
		t.Fatalf("round trip: %+v", got)
	}
	if err := rt.saveSettings(pluginSettings{DefaultTarget: strings.Repeat("x", 65)}); err == nil {
		t.Fatal("an over-long target was accepted")
	}
	if err := rt.saveSettings(pluginSettings{DefaultUA: "bad\nua"}); err == nil {
		t.Fatal("a control character was accepted into the user agent")
	}
}

// Absent settings must load as defaults rather than as an error, so a fresh
// install is not a failure state.
func TestSettingsDefaultWhenAbsent(t *testing.T) {
	rt, _ := newKVRuntime(t)
	got, err := rt.loadSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.DefaultTarget != "" || got.SchemaVersion != 1 {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}
