package main

import (
	"strings"
	"testing"
)

// The migration read `/api/subs` alone, so an operator with four subscriptions,
// two combinations and fifteen files got four records and no warning. These use
// the shapes a real Sub-Store returns — including a file whose content is a
// placeholder and whose generator sits in the chain, which is how every file in
// the deployment this was measured against is written.

const upstreamSubsBody = `{"status":"success","data":[
  {"name":"home-nodes","source":"local","content":"vless://11111111-1111-1111-1111-111111111111@a.example:443#HK-01","tag":["home"]},
  {"name":"office-nodes","source":"local","content":"vless://22222222-2222-2222-2222-222222222222@b.example:443#JP-01","tag":["office"]}
]}`

const upstreamCollectionsBody = `{"status":"success","data":[
  {"name":"all-nodes","displayName":"All","remark":"everything","tag":["home","office"],
   "subscriptions":["home-nodes","office-nodes"],"subscriptionTags":["home"],
   "ignoreFailedRemoteSub":true,
   "process":[{"type":"Quick Setting Operator","args":{"udp":"ENABLED"}}]}
]}`

// content is upstream's placeholder; the program lives in the chain.
const upstreamFilesBody = `{"status":"success","data":[
  {"name":"phone-config","displayName":"Phone","remark":"phone","tag":["phone"],
   "source":"local","sourceType":"collection","sourceName":"all-nodes",
   "type":"mihomoProfile","content":"// The content of the file","download":false,
   "ignoreFailedRemoteFile":false,
   "process":[{"type":"Script Operator","args":{"mode":"script","content":"$content = ProxyUtils.yaml.safeDump({mode: 'rule'});"}}]},
  {"name":"extra-rules","displayName":"Rules","tag":[],
   "source":"local","type":"file","content":"DOMAIN-SUFFIX,example.invalid,DIRECT\n",
   "process":[]}
]}`

func upstreamHost(t *testing.T) (*runtime, *httpKVHost) {
	t.Helper()
	host := &httpKVHost{
		kvHostCaller: newKVHostCaller(),
		status:       200,
		byPath: map[string][]byte{
			"/api/subs":        []byte(upstreamSubsBody),
			"/api/collections": []byte(upstreamCollectionsBody),
			"/api/wholeFiles":  []byte(upstreamFilesBody),
		},
	}
	return &runtime{host: host, engine: testEngineWithHeadroom()}, host
}

func TestMigrationImportsAllThreeKinds(t *testing.T) {
	rt, _ := upstreamHost(t)
	report, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(report.Skipped) != 0 {
		t.Fatalf("records were skipped: %+v", report.Skipped)
	}
	if report.Total != 5 {
		t.Fatalf("total is %d, want 2 subs + 1 combination + 2 files", report.Total)
	}
	if len(report.Imported) != 5 {
		t.Fatalf("imported %v, want all five", report.Imported)
	}

	// A combination has to point at the records this run created, not at names
	// that mean nothing here.
	col, err := rt.getSubscription("imported-col-all-nodes")
	if err != nil {
		t.Fatalf("get combination: %v", err)
	}
	if recordKind(col) != kindCollection {
		t.Fatalf("the combination came across as %q", recordKind(col))
	}
	want := []string{"imported-home-nodes", "imported-office-nodes"}
	if len(col.Members) != len(want) {
		t.Fatalf("members are %v, want %v", col.Members, want)
	}
	for i := range want {
		if col.Members[i] != want[i] {
			t.Fatalf("members are %v, want %v", col.Members, want)
		}
	}
	if len(col.MemberTags) != 1 || col.MemberTags[0] != "home" {
		t.Fatalf("member tags are %v", col.MemberTags)
	}
	// ignoreFailedRemoteSub is upstream's spelling of the failure mode, and
	// getting it backwards would turn "keep serving" into "refuse".
	if col.FailureMode != failureModeSkip {
		t.Fatalf("failure mode is %q, want %q", col.FailureMode, failureModeSkip)
	}
	if col.DisplayName != "All" || col.Remark != "everything" {
		t.Fatalf("display name or remark lost: %+v", col)
	}
}

// The generator is the file. Importing the record without lifting it out of the
// chain stores the placeholder as the document, and every render serves the
// string "// The content of the file".
func TestMigrationLiftsAFileGeneratorOutOfTheChain(t *testing.T) {
	rt, _ := upstreamHost(t)
	if _, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	file, err := rt.getSubscription("imported-file-phone-config")
	if err != nil {
		t.Fatalf("get file: %v", err)
	}
	if recordKind(file) != kindFile {
		t.Fatalf("the file came across as %q", recordKind(file))
	}
	if file.FileType != fileTypeScript {
		t.Fatalf("file type is %q, want %q", file.FileType, fileTypeScript)
	}
	if !strings.Contains(file.Content, "$content") {
		t.Fatalf("the generator did not become the document: %q", file.Content)
	}
	if strings.Contains(file.Content, "The content of the file") {
		t.Fatal("the placeholder was stored as the document")
	}
	if len(file.Process) != 0 {
		t.Fatalf("the generator was left in the chain as well: %d steps", len(file.Process))
	}
	// The node source names an upstream record, so it has to be rewritten the
	// same way the combination's members were.
	if file.NodeSource != "imported-col-all-nodes" {
		t.Fatalf("node source is %q, want the imported combination", file.NodeSource)
	}
}

// A file with no generator keeps its own content and its declared type.
func TestMigrationKeepsAPlainFileAsWritten(t *testing.T) {
	rt, _ := upstreamHost(t)
	if _, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	file, err := rt.getSubscription("imported-file-extra-rules")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if file.FileType != fileTypePlain {
		t.Fatalf("file type is %q, want %q", file.FileType, fileTypePlain)
	}
	if !strings.Contains(file.Content, "DOMAIN-SUFFIX") {
		t.Fatalf("content lost: %q", file.Content)
	}
}

// `mihomoProfile` is upstream's name for a client configuration. A file with no
// generator and that type has to land as a config, not as plain text, or its
// node source would never be injected.
func TestMigrationMapsMihomoProfileOntoAConfig(t *testing.T) {
	if got := upstreamFileType("mihomoProfile"); got != fileTypeConfig {
		t.Fatalf("mihomoProfile mapped to %q", got)
	}
	if got := upstreamFileType("file"); got != fileTypePlain {
		t.Fatalf("file mapped to %q", got)
	}
}

// A `mode: link` script is a URL, not a program. Treating it as one would store
// "https://…" as JavaScript and fail at render with a syntax error.
func TestMigrationLeavesARemoteScriptInTheChain(t *testing.T) {
	step := []byte(`{"type":"Script Operator","args":{"mode":"link","content":"https://example.invalid/gen.js"}}`)
	if _, ok := scriptStepContent(step); ok {
		t.Fatal("a remote script was treated as an inline program")
	}
}

// A source that serves only subscriptions still imports them, and says what it
// could not offer rather than refusing the whole migration.
func TestMigrationSurvivesASourceWithoutTheOtherEndpoints(t *testing.T) {
	host := &httpKVHost{
		kvHostCaller: newKVHostCaller(),
		status:       200,
		byPath:       map[string][]byte{"/api/subs": []byte(upstreamSubsBody)},
	}
	rt := &runtime{host: host}
	report, err := rt.migrateFromSubStore(subStoreRequest{BaseURL: "https://source.invalid/secret"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(report.Imported) != 2 {
		t.Fatalf("imported %v, want the two subscriptions", report.Imported)
	}
	if len(report.Skipped) != 0 {
		t.Fatalf("a missing endpoint was reported as a skipped record: %+v", report.Skipped)
	}
	if report.Unavailable["collections"] == "" || report.Unavailable["files"] == "" {
		t.Fatalf("the missing endpoints were not reported: %+v", report.Unavailable)
	}
}
