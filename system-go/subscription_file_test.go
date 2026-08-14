package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A file is a document whose node list is filled in from a subscription. The
// separation is the point: one hand-tuned config, nodes that follow whatever
// the named subscription currently resolves to.

const configTemplate = `mixed-port: 7890
mode: rule
proxies:
  - name: placeholder
    type: http
    server: 127.0.0.1
    port: 1
proxy-groups:
  - name: PROXY
    type: select
    proxies: [placeholder]
rules:
  - MATCH,PROXY
`

func TestFileConfigInjectsNodesAndKeepsTheRest(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t)
	seedSub(t, rt, "nodes", nil, realNodeA)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "clash", Kind: kindFile, Name: "Clash",
		Content: configTemplate, NodeSource: "nodes",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := rt.renderSubscription("clash", "", "", "", nil)
	if err != nil {
		t.Fatalf("render file: %v", err)
	}
	// The operator's own configuration has to survive untouched — that is the
	// whole reason a file exists rather than serving a plain node list.
	for _, keep := range []string{"mixed-port", "mode: rule", "proxy-groups", "MATCH,PROXY"} {
		if !strings.Contains(out.Content, keep) {
			t.Fatalf("the merge dropped %q from the template:\n%s", keep, out.Content)
		}
	}
	// Mihomo refuses to start on a group naming a proxy that is not there, so a
	// stale reference to the template's example node is a broken config, not
	// cosmetic leftover.
	if strings.Contains(out.Content, "placeholder") {
		t.Fatalf("the template's placeholder proxy survived the merge:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "node-a") {
		t.Fatalf("the node source was not injected:\n%s", out.Content)
	}
	if out.ContentType != "text/yaml; charset=utf-8" {
		t.Fatalf("a config was served as %q", out.ContentType)
	}

	var parsed struct {
		Proxies []struct {
			Name string `json:"name"`
		} `json:"proxies"`
		ProxyGroups []struct {
			Name    string   `json:"name"`
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
	}
	loadYAML(t, rt, out.Content, &parsed)
	if len(parsed.Proxies) != 1 || parsed.Proxies[0].Name != "node-a" {
		t.Fatalf("proxies is %+v, want just node-a", parsed.Proxies)
	}
	if len(parsed.ProxyGroups) != 1 {
		t.Fatalf("the template's group was lost: %+v", parsed.ProxyGroups)
	}
	// An emptied group selects nothing, which is the same failure by another
	// route. It has to end up holding the nodes that replaced the example.
	if got := parsed.ProxyGroups[0].Proxies; len(got) != 1 || got[0] != "node-a" {
		t.Fatalf("group PROXY selects %v, want the injected nodes", got)
	}
}

// A group that builds its own membership is not empty by mistake. Filling it
// would override the operator's rule with a hardcoded list that stops tracking
// the subscription.
func TestFileLeavesRuleBasedGroupsAlone(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t)
	seedSub(t, rt, "nodes", nil, realNodeA)
	template := `proxies: []
proxy-groups:
  - name: AUTO
    type: url-test
    include-all: true
  - name: JP
    type: select
    filter: "japan"
rules:
  - MATCH,AUTO
`
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "ruled", Kind: kindFile, Name: "Ruled", Content: template, NodeSource: "nodes",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.renderSubscription("ruled", "", "", "", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var parsed struct {
		ProxyGroups []struct {
			Name    string   `json:"name"`
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
	}
	loadYAML(t, rt, out.Content, &parsed)
	for _, group := range parsed.ProxyGroups {
		if len(group.Proxies) != 0 {
			t.Fatalf("group %s was given an explicit list %v, overriding its own rule", group.Name, group.Proxies)
		}
	}
}

// A group naming another group is a chain the operator built on purpose.
func TestFileKeepsGroupsThatReferenceOtherGroups(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t)
	seedSub(t, rt, "nodes", nil, realNodeA)
	template := `proxies: []
proxy-groups:
  - name: AUTO
    type: url-test
    include-all: true
  - name: FINAL
    type: select
    proxies: [AUTO, DIRECT, placeholder]
rules:
  - MATCH,FINAL
`
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "chain", Kind: kindFile, Name: "Chain", Content: template, NodeSource: "nodes",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.renderSubscription("chain", "", "", "", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var parsed struct {
		ProxyGroups []struct {
			Name    string   `json:"name"`
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
	}
	loadYAML(t, rt, out.Content, &parsed)
	var final []string
	for _, group := range parsed.ProxyGroups {
		if group.Name == "FINAL" {
			final = group.Proxies
		}
	}
	want := []string{"AUTO", "DIRECT"}
	if len(final) != len(want) {
		t.Fatalf("FINAL selects %v, want %v", final, want)
	}
	for i := range want {
		if final[i] != want[i] {
			t.Fatalf("FINAL selects %v, want %v", final, want)
		}
	}
}

func TestPreviewOfAFileWithNodeSourceFailsClosed(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t)
	seedSub(t, rt, "nodes", nil, realNodeA)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "clash", Kind: kindFile, Name: "Clash",
		Content: configTemplate, NodeSource: "nodes",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	res := callSubscription(t, rt, "preview", map[string]any{"subscription_id": "clash"})
	if res.OK || res.Error != "file preview does not expose node-source content" || len(res.Result) != 0 {
		t.Fatalf("node-source file preview did not fail closed: %+v", res)
	}
}

func TestPreviewOfSelfContainedFileReturnsDocument(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "rules", Kind: kindFile, Name: "Rules", Content: "rules:\n  - MATCH,DIRECT\n"}); err != nil {
		t.Fatal(err)
	}
	res := callSubscription(t, rt, "preview", map[string]any{"subscription_id": "rules"})
	var out previewResult
	decodeResult(t, res, &out)
	if !strings.Contains(out.Document, "MATCH,DIRECT") || len(out.Nodes) != 0 {
		t.Fatalf("self-contained preview=%+v", out)
	}
}

func TestPreviewOfRemoteFileFailsBeforeHostFetch(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("remote-secret-canary")
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "remote-file", Kind: kindFile, Name: "Remote", Source: subscriptionSourceRemote,
		URL: "https://provider.invalid/file", Content: "local fallback",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	host.calls = 0
	res := callSubscription(t, rt, "preview", map[string]any{"subscription_id": "remote-file"})
	if res.OK || res.Error != "file preview requires a self-contained local document" {
		t.Fatalf("remote preview did not fail closed: %+v", res)
	}
	if host.calls != 0 {
		t.Fatalf("remote preview made %d host calls", host.calls)
	}
	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "remote-secret-canary") {
		t.Fatal("remote preview leaked fetched content")
	}
}

// A config with no node source is a document maintained entirely by hand —
// rules, a fragment. Serving it unchanged is correct, not an error.
func TestFileWithoutANodeSourceServesItsTemplate(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "rules", Kind: kindFile, Name: "Rules", Content: "rules:\n  - MATCH,DIRECT\n",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.renderSubscription("rules", "", "", "", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out.Content, "MATCH,DIRECT") {
		t.Fatalf("the template was not served: %q", out.Content)
	}
}

func TestPlainFileIsServedAsText(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "notes", Kind: kindFile, Name: "Notes",
		FileType: fileTypePlain, Content: "just some text",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.renderSubscription("notes", "", "", "", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out.Content != "just some text" {
		t.Fatalf("plain content was altered: %q", out.Content)
	}
	if out.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("plain text was served as %q", out.ContentType)
	}
}

// A plain file's chain runs over the response, not over a node list. This is
// the only operator shape that path accepts, and the UI offers nothing else —
// so it has to actually work.
func TestPlainFileRunsItsScriptOverTheDocument(t *testing.T) {
	rt, _ := newKVRuntime(t)
	// The response path runs `transformFunction(res)`. A proxy operator is
	// skipped there by design, which is why a plain file's palette offers only
	// this one.
	script := `function transformFunction(res) { res.body = res.body.replace("DIRECT", "PROXY"); return res; }`
	step, err := json.Marshal(map[string]any{
		"type": "Response Transformer",
		"args": map[string]any{"content": script, "mode": "script"},
	})
	if err != nil {
		t.Fatalf("marshal step: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "rules", Kind: kindFile, Name: "Rules", FileType: fileTypePlain,
		Content: "DOMAIN-SUFFIX,example.invalid,DIRECT\n",
		Process: []json.RawMessage{step},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := rt.renderSubscription("rules", "", "", "", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out.Content, "PROXY") || strings.Contains(out.Content, "DIRECT") {
		t.Fatalf("the script did not reach the document: %q", out.Content)
	}
}

// Two files naming each other would render forever, the same reason a
// collection cannot contain a collection.
func TestFileCannotSourceAnotherFile(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "a", Kind: kindFile, Name: "A", Content: configTemplate,
	}); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "b", Kind: kindFile, Name: "B", Content: configTemplate, NodeSource: "a",
	}); err != nil {
		t.Fatalf("save b: %v", err)
	}
	if _, err := rt.renderSubscription("b", "", "", "", nil); err == nil {
		t.Fatal("a file was allowed to source another file")
	}
}

func TestFileCannotSourceItself(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "self", Kind: kindFile, Name: "Self", Content: configTemplate, NodeSource: "self",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.renderSubscription("self", "", "", "", nil); err == nil {
		t.Fatal("a file was allowed to source itself")
	}
}

func TestFileNeedsATemplate(t *testing.T) {
	rt, _ := newKVRuntime(t)
	err := rt.saveSubscription(subscriptionRecord{ID: "empty", Kind: kindFile, Name: "Empty"})
	if err == nil {
		t.Fatal("a file with no template was accepted")
	}
	if !strings.Contains(err.Error(), "template") {
		t.Fatalf("error must name what is missing, got %v", err)
	}
}

// A file's fields belong to a file. Keeping membership or a client target on it
// would leave two answers to what the record is.
func TestFileDropsFieldsBelongingToOtherKinds(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "f", Kind: kindFile, Name: "F", Content: configTemplate,
		Members: []string{"x"}, Target: "Clash", VPNIdentity: "someone",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := rt.getSubscription("f")
	if len(got.Members) != 0 || got.Target != "" || got.VPNIdentity != "" {
		t.Fatalf("a file kept fields from other kinds: %+v", got)
	}
}

// A collection gathers node sources. A file is a document, not one of them.
func TestCollectionRefusesAFileAsAMember(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "f", Kind: kindFile, Name: "F", Content: configTemplate,
	}); err != nil {
		t.Fatalf("save file: %v", err)
	}
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "c", Kind: kindCollection, Name: "C", Members: []string{"f"},
	}); err != nil {
		t.Fatalf("save collection: %v", err)
	}
	rec, _ := rt.getSubscription("c")
	if _, err := rt.collectionMembers(rec); err == nil {
		t.Fatal("a collection accepted a file as a member")
	}
}

// A malformed template must fail with something an operator can act on rather
// than producing a document that looks fine and is not.
func TestFileRefusesATemplateThatIsNotAMapping(t *testing.T) {
	rt, _ := newVPNCoreRuntime(t)
	seedSub(t, rt, "nodes", nil, realNodeA)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "bad", Kind: kindFile, Name: "Bad",
		Content: "- just\n- a\n- list\n", NodeSource: "nodes",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.renderSubscription("bad", "", "", "", nil); err == nil {
		t.Fatal("a YAML sequence was accepted as a configuration template")
	}
}

// A file marked for download arrives as a file. The flag has been on the record
// since files existed and nothing read it.
func TestADownloadFileAsksToBeSaved(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "rules", Kind: kindFile, Name: "Rules", DisplayName: "My Rules",
		FileType: fileTypePlain, Content: "DOMAIN,example.invalid,DIRECT\n", Download: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.renderSubscription("rules", "", "", "", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := out.Headers["content-disposition"]; got != `attachment; filename="My Rules"` {
		t.Fatalf("content-disposition is %q", got)
	}
}

func TestAFileNotMarkedForDownloadAsksForNothing(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{
		ID: "rules", Kind: kindFile, Name: "Rules", FileType: fileTypePlain, Content: "x",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.renderSubscription("rules", "", "", "", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, set := out.Headers["content-disposition"]; set {
		t.Fatalf("an ordinary file asked to be downloaded: %+v", out.Headers)
	}
}

// A quoted filename containing a quote is a header-injection shape, and a
// slash would let a name escape the directory a client saves into.
func TestADownloadFilenameCannotBreakOutOfItsHeader(t *testing.T) {
	got := downloadFilename(subscriptionRecord{
		ID: "x", DisplayName: "evil\"; drop=1; a=\"/../../etc/passwd",
	})
	if strings.ContainsAny(got, "\"\\/") {
		t.Fatalf("the filename kept a character that breaks the header: %q", got)
	}
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("the filename kept a newline: %q", got)
	}
}

// A configuration saved without an extension is one a client will not know what
// to do with.
func TestADownloadedConfigGetsAnExtension(t *testing.T) {
	got := downloadFilename(subscriptionRecord{ID: "c", Name: "phone", FileType: fileTypeConfig})
	if got != "phone.yaml" {
		t.Fatalf("filename is %q", got)
	}
	// One the operator already named keeps their extension.
	kept := downloadFilename(subscriptionRecord{ID: "c", Name: "phone.yml", FileType: fileTypeConfig})
	if kept != "phone.yml" {
		t.Fatalf("an explicit extension was changed to %q", kept)
	}
}

func TestADownloadFilenameFallsBackRatherThanBeingEmpty(t *testing.T) {
	if got := downloadFilename(subscriptionRecord{ID: "only-an-id"}); got != "only-an-id.yaml" {
		t.Fatalf("filename is %q", got)
	}
	if got := downloadFilename(subscriptionRecord{ID: "", DisplayName: "///"}); got == "" {
		t.Fatal("a name of only illegal characters produced an empty filename")
	}
}
