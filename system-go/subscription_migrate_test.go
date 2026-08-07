package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// migrationHost serves a subscription list and nothing else, which is what a
// source with no combinations or files looks like. Answering the same body for
// every endpoint would import the list three times over.
func migrationHost(t *testing.T, listBody string, status int) (*runtime, *httpKVHost) {
	t.Helper()
	host := &httpKVHost{
		kvHostCaller: newKVHostCaller(),
		status:       status,
		body:         []byte(listBody),
		byPath:       map[string][]byte{"/api/subs": []byte(listBody)},
	}
	return &runtime{host: host}, host
}

const twoSubs = `{"status":"success","data":[
  {"name":"provider-a","url":"https://a.invalid/sub","ua":"Surge","process":[{"type":"Flag Operator","args":{}}],"mystery":"keep me"},
  {"name":"provider-b","url":"https://b.invalid/sub"}
]}`

// The operator's requirement is that migrating loses nothing. The honest way to
// promise that is to keep the original object, not to map it carefully.
func TestMigrationPreservesTheSourceRecordVerbatim(t *testing.T) {
	rt, _ := migrationHost(t, twoSubs, 200)

	report, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(report.Imported) != 2 {
		t.Fatalf("imported %v, want 2 records", report.Imported)
	}

	rec, err := rt.getSubscription("imported-provider-a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.Origin == nil {
		t.Fatal("the source record was not preserved")
	}
	if !strings.Contains(string(rec.Origin.Raw), "keep me") {
		t.Fatalf("a field the mapping does not know about was dropped: %s", rec.Origin.Raw)
	}
	if rec.URL != "https://a.invalid/sub" || rec.UA != "Surge" {
		t.Fatalf("mapped fields wrong: %+v", rec)
	}
}

// The operator will run this more than once - once to see what happens, once for
// real - so a second run must not double everything.
func TestMigrationIsIdempotent(t *testing.T) {
	rt, _ := migrationHost(t, twoSubs, 200)

	if _, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"}); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if _, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"}); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	list, err := rt.listSubscriptions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("a second run duplicated records: %d entries", len(list))
	}
}

// Migration is additive. A record the operator created locally must survive an
// import, or the tool cannot be run safely twice.
func TestMigrationDeletesNothingLocal(t *testing.T) {
	rt, _ := migrationHost(t, twoSubs, 200)
	if err := rt.saveSubscription(subscriptionRecord{ID: "local-one", Name: "local", Content: "vless://local"}); err != nil {
		t.Fatalf("seed local: %v", err)
	}

	if _, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := rt.getSubscription("local-one"); err != nil {
		t.Fatalf("a local record was lost by the import: %v", err)
	}
}

// A skipped record must be named. Silently dropping one is what would make the
// operator afraid to switch the old instance off.
func TestMigrationReportsWhatItCouldNotImport(t *testing.T) {
	const mixed = `{"status":"success","data":[
	  {"name":"good","url":"https://a.invalid/sub"},
	  {"name":"bad-operator","url":"https://b.invalid/sub","process":[{"type":"Nonexistent Operator"}]},
	  {"url":"https://c.invalid/sub"}
	]}`
	rt, _ := migrationHost(t, mixed, 200)

	report, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(report.Imported) != 1 {
		t.Fatalf("imported %v, want just the good one", report.Imported)
	}
	if report.Total != 3 {
		t.Fatalf("total = %d, want 3", report.Total)
	}
	if _, ok := report.Skipped["bad-operator"]; !ok {
		t.Fatalf("the unknown-operator record was not reported: %+v", report.Skipped)
	}
	if len(report.Skipped) != 2 {
		t.Fatalf("skipped = %+v, want two entries", report.Skipped)
	}
}

func TestMigrationRejectsANonSuccessStatus(t *testing.T) {
	rt, _ := migrationHost(t, `{}`, 500)
	if _, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"}); err == nil {
		t.Fatal("a 500 from the source was accepted")
	}
}

func TestMigratedRecordIDIsStableAndSafe(t *testing.T) {
	cases := map[string]string{
		"Provider A":   "imported-provider-a",
		"provider a":   "imported-provider-a",
		"../../escape": "imported-escape",
		"!!!":          "imported-unnamed",
		"":             "imported-unnamed",
	}
	for in, want := range cases {
		if got := migratedRecordID(in); got != want {
			t.Fatalf("migratedRecordID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMigrationErrorsRedactTheSourceURL(t *testing.T) {
	host := &httpKVHost{kvHostCaller: newKVHostCaller(), httpErr: errJSON("dial https://source.invalid/secret-path: refused")}
	rt := &runtime{host: host}
	_, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret-path"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "secret-path") {
		t.Fatalf("the source URL leaked: %v", err)
	}
}

func errJSON(msg string) error { return &jsonErr{msg} }

type jsonErr struct{ msg string }

func (e *jsonErr) Error() string { return e.msg }

var _ = json.Marshal
