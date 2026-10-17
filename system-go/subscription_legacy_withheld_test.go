package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const legacyTestLink = "vless://11111111-2222-4333-8444-555555555555@owner.example:443?security=reality&sni=a.com&fp=chrome&pbk=x#owner"

// legacyWithheldStore is a store with a legacy vpn-core record, a collection
// over it beside a provider record, and files over each.
func legacyWithheldStore(t *testing.T) (*runtime, *vpnCoreHost) {
	t.Helper()
	rt, host := newVPNCoreRuntime(t, legacyTestLink)
	for _, rec := range []subscriptionRecord{
		{ID: "legacy", Name: "legacy", Source: subscriptionSourceVPNCore, Target: "URI"},
		{ID: "local", Name: "local", Source: subscriptionSourceLocal, Content: "trojan://pw@198.51.100.7:443#provider"},
		{ID: "mixed", Name: "mixed", Kind: kindCollection, Members: []string{"legacy", "local"}, Target: "URI"},
		{ID: "file-legacy", Name: "file-legacy", Kind: kindFile, Content: "proxies: []\n", NodeSource: "legacy"},
		{ID: "file-mixed", Name: "file-mixed", Kind: kindFile, Content: "proxies: []\n", NodeSource: "mixed"},
		{ID: "script-legacy", Name: "script-legacy", Kind: kindFile, FileType: fileTypeScript, Content: "$content = 'x'", NodeSource: "legacy"},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatalf("save %s: %v", rec.ID, err)
		}
	}
	return rt, host
}

// TestLegacyRenderWithoutRawIsWithheld pins that no render path reads a
// legacy record's export: the record, a collection holding it, a file over
// either and a script file over it, rendered with no snapshot, answer
// owner_credentials_withheld; publish answers the same; and a raw that is
// not an envelope this plugin wrote, or one whose blocks are all empty, is
// snapshot_malformed rather than a reason to resolve live. No rpc call
// leaves the plugin on any of them.
func TestLegacyRenderWithoutRawIsWithheld(t *testing.T) {
	rt, host := legacyWithheldStore(t)
	for _, id := range []string{"legacy", "mixed", "file-legacy", "file-mixed", "script-legacy"} {
		_, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: id, Target: "URI"})
		if err == nil || !strings.HasPrefix(err.Error(), codeOwnerCredentialsWithheld) {
			t.Fatalf("render %s with no snapshot: err %v", id, err)
		}
	}
	for _, id := range []string{"legacy", "mixed", "file-legacy"} {
		if _, err := rt.publishSubscription(id, "https://destination.invalid/x", "PUT", "plain"); err == nil || !strings.HasPrefix(err.Error(), codeOwnerCredentialsWithheld) {
			t.Fatalf("publish %s: err %v", id, err)
		}
	}
	empty, _ := json.Marshal(snapshotEnvelope{Version: snapshotEnvelopeVersion, Kind: kindCollection, Members: []envelopeMember{{SubName: "legacy"}}})
	for name, raw := range map[string]string{"raw x": "x", "member-less envelope": string(empty)} {
		_, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "mixed", Target: "URI", Raw: raw})
		if err == nil || !strings.HasPrefix(err.Error(), codeSnapshotMalformed) {
			t.Fatalf("collection with %s: err %v", name, err)
		}
	}
	// A staged recompute reached through member_revisions resolves the
	// collection's members live and meets the same guard at its legacy
	// member.
	mixed, _ := rt.getSubscription("mixed")
	local, _ := rt.getSubscription("local")
	res := callSubscription(t, rt, "render", map[string]any{"subscription_id": "mixed", "target": "URI", "revision": mixed.Revision,
		"member_revisions": map[string]string{"local": local.Revision}})
	if res.OK || !strings.HasPrefix(res.Error, codeOwnerCredentialsWithheld) {
		t.Fatalf("staged recompute over a legacy member: ok=%v error=%q", res.OK, res.Error)
	}
	if len(host.rpcCalls) != 0 {
		t.Fatalf("a withheld path made %d rpc calls", len(host.rpcCalls))
	}
}

// TestLegacyFetchersRunOnlyUnderFetch pins where the export may be read: the
// refresh (fetch) of the record, of a collection over it and of a file over
// it reads it; the same paths under any other authority, which is what
// probe uses, refuse with owner_credentials_withheld before any rpc call.
func TestLegacyFetchersRunOnlyUnderFetch(t *testing.T) {
	rt, host := legacyWithheldStore(t)
	for _, id := range []string{"legacy", "mixed", "file-legacy", "file-mixed", "script-legacy"} {
		before := len(host.rpcCalls)
		if _, err := rt.fetchSubscriptionAs(id, legacyWithheld); err == nil || !strings.HasPrefix(err.Error(), codeOwnerCredentialsWithheld) {
			t.Fatalf("a withheld fetch of %s: err %v", id, err)
		}
		if len(host.rpcCalls) != before {
			t.Fatalf("a withheld fetch of %s made an rpc call", id)
		}
		if _, err := rt.fetchSubscription(id); err != nil {
			t.Fatalf("the refresh of %s no longer reads the export: %v", id, err)
		}
		if len(host.rpcCalls) == before {
			t.Fatalf("the refresh of %s made no rpc call", id)
		}
	}
	// A draft preview of a legacy source resolves nothing either.
	if _, err := rt.fetchRecordContent(subscriptionRecord{Source: subscriptionSourceVPNCoreGraph}); err == nil || !strings.HasPrefix(err.Error(), codeOwnerCredentialsWithheld) {
		t.Fatalf("a legacy draft resolved live: err %v", err)
	}
}
