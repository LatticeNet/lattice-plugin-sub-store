package main

import (
	"os"
	"strings"
	"testing"
)

// These run a generator shaped like the ones operators actually deploy, against
// a store shaped the way theirs is. The surface is what matters: a script
// reaches for produceArtifact, $content, $options and $arguments, and the
// embedded core provides none of them on its own, so a script written upstream
// could not run here at all until this plugin built them.
//
// LATTICE_SUBSTORE_GENERATOR points the same tests at a real generator on disk.
// Real ones carry an operator's DNS servers, node names and credentials, so one
// is never committed here; the fixture reproduces the surface without them.
//
// A real generator names its collection and subscriptions as constants, so it
// only runs against a store using those names — pointing the override at one
// without also renaming the seeds below fails with produceArtifact refusing an
// undeclared source, which is the guard working rather than a regression.

const scriptNodeHome = "vless://11111111-1111-1111-1111-111111111111@a.example:443?security=reality&sni=a.com&fp=chrome&pbk=x#HK-01"
const scriptNodeOffice = "vless://22222222-2222-2222-2222-222222222222@b.example:443?security=reality&sni=b.com&fp=chrome&pbk=y#JP-01"

// seedGeneratorStore builds the shape the real script expects: a collection
// named all-nodes over two subs whose names are in its SUB_PREFIX_MAP.
func seedGeneratorStore(t *testing.T, rt *runtime) {
	t.Helper()
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "home-nodes", Name: "home-nodes", Content: scriptNodeHome,
	}); err != nil {
		t.Fatalf("seed home-nodes: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "office-nodes", Name: "office-nodes", Content: scriptNodeOffice,
	}); err != nil {
		t.Fatalf("seed office-nodes: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "all-nodes", Kind: kindCollection, Name: "all-nodes",
		Members: []string{"home-nodes", "office-nodes"},
	}); err != nil {
		t.Fatalf("seed collection: %v", err)
	}
}

func realGeneratorScript(t *testing.T) string {
	t.Helper()
	path := "testdata/generator-fixture.js"
	if override := os.Getenv("LATTICE_SUBSTORE_GENERATOR"); override != "" {
		path = override
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generator: %v", err)
	}
	return string(body)
}

func TestGeneratorScriptProducesAConfig(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedGeneratorStore(t, rt)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "cd-self", Kind: kindFile, Name: "cd-self", FileType: fileTypeScript,
		NodeSource: "all-nodes", Content: realGeneratorScript(t),
	}); err != nil {
		t.Fatalf("save file: %v", err)
	}

	out, err := rt.renderSubscription("cd-self", "", "", "", nil)
	if err != nil {
		t.Fatalf("render script file: %v", err)
	}

	// The script filters on _subName and prefixes by it. Both prefixes appearing
	// is the proof that provenance survived: merging the members into one blob
	// before parsing would have dropped the tag, the filter would have kept
	// nothing, and the script would have thrown rather than quietly serving an
	// empty config.
	for _, want := range []string{"[home]-HK-01", "[office]-JP-01"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("the generator did not see per-member provenance; %q missing from:\n%s",
				want, head(out.Content, 1200))
		}
	}
	// A configuration, not a node list.
	for _, want := range []string{"proxies:", "proxy-groups:", "rules:", "dns:"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("output is missing %q:\n%s", want, head(out.Content, 1200))
		}
	}
	// The script sets its own response headers, and the content type it chose
	// must beat the one guessed from the file kind.
	if out.ContentType != "text/yaml; charset=utf-8" {
		t.Fatalf("the script's content-type was not honoured: %q", out.ContentType)
	}
	if out.Headers["profile-update-interval"] != "24" {
		t.Fatalf("the script's headers did not come through: %+v", out.Headers)
	}
}

// The whole reason the query whitelist exists: this script switches DNS mode on
// a URL parameter, and the parameter is public input.
func TestGeneratorScriptSeesOnlyDeclaredQueryParameters(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedGeneratorStore(t, rt)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "cd-self", Kind: kindFile, Name: "cd-self", FileType: fileTypeScript,
		NodeSource: "all-nodes", Content: realGeneratorScript(t),
		QueryParams: []string{"enhanced-mode"},
	}); err != nil {
		t.Fatalf("save file: %v", err)
	}

	fakeIP, err := rt.renderSubscription("cd-self", "", "", "", nil)
	if err != nil {
		t.Fatalf("render default: %v", err)
	}
	if !strings.Contains(fakeIP.Content, "fake-ip") {
		t.Fatalf("the default enhanced-mode is not fake-ip:\n%s", head(fakeIP.Content, 800))
	}

	redir, err := rt.renderSubscription("cd-self", "", "", "", map[string]string{"enhanced-mode": "redir-host"})
	if err != nil {
		t.Fatalf("render with query: %v", err)
	}
	if !strings.Contains(redir.Content, "redir-host") {
		t.Fatalf("the declared query parameter did not reach the script:\n%s", head(redir.Content, 800))
	}

	// An undeclared parameter is dropped before the script can read it, so the
	// output is the default one.
	blocked, err := rt.renderSubscription("cd-self", "", "", "", map[string]string{"enhanced-mode-x": "redir-host"})
	if err != nil {
		t.Fatalf("render with undeclared query: %v", err)
	}
	if strings.Contains(blocked.Content, "enhanced-mode: redir-host") {
		t.Fatal("an undeclared query parameter reached the script")
	}
}

// Without a declaration there is nothing to resolve, and the script's own call
// has to say so rather than returning an empty list it would silently build a
// nodeless config from.
func TestProduceArtifactRefusesAnUndeclaredSource(t *testing.T) {
	rt, _ := newKVRuntime(t)
	seedGeneratorStore(t, rt)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "wrong", Kind: kindFile, Name: "wrong", FileType: fileTypeScript,
		NodeSource: "home-nodes",
		Content: `const nodes = await produceArtifact({type: "collection", name: "all-nodes", produceType: "internal"});
$content = JSON.stringify(nodes);`,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := rt.renderSubscription("wrong", "", "", "", nil)
	if err == nil {
		t.Fatal("a script resolved a source the file never declared")
	}
}

// The program is the largest thing this plugin stores. Keeping it in the record
// document would re-encode it on every unrelated edit and exhaust the 1 MB cap
// after a dozen files.
func TestScriptIsStoredOutsideTheRecordDocument(t *testing.T) {
	rt, host := newKVRuntime(t)
	script := realGeneratorScript(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "gen", Kind: kindFile, Name: "gen", FileType: fileTypeScript, Content: script,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	stored, ok := host.values[subscriptionRecordsKey]
	if !ok {
		t.Fatal("the record document was never written")
	}
	// The marker is taken from the script rather than hardcoded, so swapping the
	// fixture — or pointing the run at a real generator — cannot turn this into
	// an assertion that passes because it is looking for something absent.
	marker := script[:120]
	if strings.Contains(string(stored), marker) {
		t.Fatalf("the script was stored inside the record document (%d bytes)", len(stored))
	}
	if len(stored) > 4096 {
		t.Fatalf("the record document is %d bytes; a %d-byte script leaked into it", len(stored), len(script))
	}
	if _, ok := host.values[fileScriptKey("gen")]; !ok {
		t.Fatal("the script was not stored under its own key")
	}

	// The split has to be invisible: a caller reading the record back gets its
	// content, the same as any other kind.
	got, err := rt.getSubscription("gen")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != script {
		t.Fatalf("the script did not come back on read: %d bytes vs %d", len(got.Content), len(script))
	}
}

func TestDeletingAScriptFileClearsItsScript(t *testing.T) {
	rt, host := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "gen", Kind: kindFile, Name: "gen", FileType: fileTypeScript,
		Content: `$content = "hello";`,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := rt.deleteSubscription("gen"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if value := host.values[fileScriptKey("gen")]; len(value) != 0 {
		t.Fatalf("the script outlived its file: %d bytes", len(value))
	}
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…"
}
