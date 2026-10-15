package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// previewCallOf drives preview or preview_draft as the gateway would.
func previewCallOf(t *testing.T, rt *runtime, method string, payload map[string]any) (previewResult, response) {
	t.Helper()
	res := callSubscription(t, rt, method, payload)
	var out previewResult
	if res.OK {
		if err := json.Unmarshal(res.Result, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, res
}

// TestPreviewFleetRecordRendersOverPlaceholders pins a saved fleet record's
// preview: read from the catalogue live, the plan's nodes as summaries (no
// credential field), and the target's document over placeholder
// credentials, marked so.
func TestPreviewFleetRecordRendersOverPlaceholders(t *testing.T) {
	rt, host := newFleetRuntime(t, fleetTestRows(3, "vless"))
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp", Process: []json.RawMessage{renameStep("tokyo", "osaka")}})
	out, res := previewCallOf(t, rt, "preview", map[string]any{"subscription_id": "jp", "target": "ClashMeta"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if host.calls == 0 {
		t.Fatal("the preview read no catalogue page")
	}
	if !out.PlaceholderCredentials || out.SourceNodeCount != 3 || out.NodeCount != 3 || len(out.Nodes) != 3 {
		t.Fatalf("preview = %+v", out)
	}
	for _, node := range out.Nodes {
		if !strings.HasPrefix(node.Name, "osaka") || node.Type != "vless" || node.Server == "" {
			t.Fatalf("node summary = %+v", node)
		}
	}
	if !strings.Contains(out.Document, "LATTICE-SYN-") || !strings.Contains(out.Document, "proxies:") {
		t.Fatalf("document = %.300s", out.Document)
	}
	if strings.Contains(out.Document, "line_uuid") || strings.Contains(out.Document, "node_id") || strings.Contains(out.Document, "192.0.2.") {
		t.Fatal("the document carries a Lattice field")
	}
	if out.SourceVersion != fleetTestVersion {
		t.Fatalf("source_version = %q", out.SourceVersion)
	}

	// A fleet-bound collection previews over its live members the same way.
	if err := rt.saveSubscription(subscriptionRecord{ID: "local", Name: "local", Source: subscriptionSourceLocal, Content: "trojan://pw@198.51.100.7:443#provider"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"jp", "local"}}); err != nil {
		t.Fatal(err)
	}
	out, res = previewCallOf(t, rt, "preview", map[string]any{"subscription_id": "c", "target": "URI"})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if !out.PlaceholderCredentials || out.NodeCount != 4 || !strings.Contains(out.Document, "LATTICE-SYN-") || !strings.Contains(out.Document, "provider") {
		t.Fatalf("collection preview = %+v", out)
	}
}

// TestPreviewDraftFleetResolvesLive pins an unsaved fleet draft's preview:
// the draft's leading Structured Filter run selects from the catalogue
// live, and preview (read-scoped) still refuses the draft fields.
func TestPreviewDraftFleetResolvesLive(t *testing.T) {
	rt, host := newFleetRuntime(t, mixedProtocolRows())
	host.applySelector = true
	payload := map[string]any{"source": subscriptionSourceFleet, "operators": []json.RawMessage{structuredKeep("protocol", "trojan")}, "target": "sing-box"}
	out, res := previewCallOf(t, rt, "preview_draft", payload)
	if !res.OK {
		t.Fatal(res.Error)
	}
	if out.NodeCount != 4 || out.SourceNodeCount != 4 || !out.PlaceholderCredentials || !strings.Contains(out.Document, "LATTICE-SYN-") {
		t.Fatalf("draft preview = %+v", out)
	}
	if last := host.requests[len(host.requests)-1]; last.Selector == nil || len(last.Selector.Protocols) != 1 {
		t.Fatalf("the draft pushed %+v", last.Selector)
	}
	if _, res := previewCallOf(t, rt, "preview", payload); res.OK || !strings.Contains(res.Error, "preview_draft") {
		t.Fatalf("preview with draft fields: ok=%v error=%q", res.OK, res.Error)
	}
}

// TestLegacyFilePreviewDocumentWithheld pins that no preview reads a legacy
// record's export: the record itself, a collection holding it, a graph
// draft and a legacy draft answer owner_credentials_withheld; a file over
// either is refused before anything is read (the S1 rule for every file
// with a node source, stricter than the code-specific answer and costing no
// index read); and no rpc call leaves the plugin.
func TestLegacyFilePreviewDocumentWithheld(t *testing.T) {
	rt, host := legacyWithheldStore(t)
	for _, id := range []string{"legacy", "mixed"} {
		if _, res := previewCallOf(t, rt, "preview", map[string]any{"subscription_id": id}); res.OK || !strings.HasPrefix(res.Error, codeOwnerCredentialsWithheld) {
			t.Fatalf("preview %s: ok=%v error=%q", id, res.OK, res.Error)
		}
	}
	for _, id := range []string{"file-legacy", "file-mixed", "script-legacy"} {
		out, res := previewCallOf(t, rt, "preview", map[string]any{"subscription_id": id})
		if res.OK || res.Error != "file preview does not expose node-source content" || out.Document != "" {
			t.Fatalf("preview %s: ok=%v error=%q", id, res.OK, res.Error)
		}
	}
	selection := vpnCoreGraphSelection{SchemaVersion: 1, OptionsVersion: "ov1:" + strings.Repeat("a", 64), IdentityID: "identity-a", EntryRoots: []string{graphRootA}}
	if _, res := previewCallOf(t, rt, "preview", map[string]any{"graph_selection": selection}); res.OK || !strings.HasPrefix(res.Error, codeOwnerCredentialsWithheld) {
		t.Fatalf("graph draft preview: ok=%v error=%q", res.OK, res.Error)
	}
	if _, res := previewCallOf(t, rt, "preview_draft", map[string]any{"source": subscriptionSourceVPNCore, "vpn_identity": "identity-a"}); res.OK || !strings.HasPrefix(res.Error, codeOwnerCredentialsWithheld) {
		t.Fatalf("legacy draft preview: ok=%v error=%q", res.OK, res.Error)
	}
	if len(host.rpcCalls) != 0 {
		t.Fatalf("a withheld preview made %d rpc calls", len(host.rpcCalls))
	}
}
