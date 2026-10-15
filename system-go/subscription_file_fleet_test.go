package main

import (
	"strings"
	"testing"
)

// TestFileRefusesFleetNodeSourceUntilS3 pins fleet_file_unavailable at
// render and at fetch: a config file and a script file whose node source is
// a fleet record, or a collection that gathers one, are refused before any
// node is read, because a file render carries no plan and its clients would
// receive placeholder credentials. A file over a provider record still
// renders.
func TestFileRefusesFleetNodeSourceUntilS3(t *testing.T) {
	rt, host := newFleetRuntime(t, fleetTestRows(2, "vless"))
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp", Tags: []string{"asia"}})
	for _, rec := range []subscriptionRecord{
		{ID: "local", Name: "local", Source: subscriptionSourceLocal, Content: "trojan://pw@192.0.2.10:443#one"},
		{ID: "asia", Name: "asia", Kind: kindCollection, MemberTags: []string{"asia"}},
		{ID: "cfg-fleet", Name: "cfg-fleet", Kind: kindFile, Content: "proxies: []\n", NodeSource: "jp"},
		{ID: "cfg-collection", Name: "cfg-collection", Kind: kindFile, Content: "proxies: []\n", NodeSource: "asia"},
		{ID: "script-fleet", Name: "script-fleet", Kind: kindFile, FileType: fileTypeScript, Content: "$content = 'x'", NodeSource: "jp"},
		{ID: "cfg-local", Name: "cfg-local", Kind: kindFile, Content: "proxies: []\n", NodeSource: "local"},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatalf("save %s: %v", rec.ID, err)
		}
	}
	for _, id := range []string{"cfg-fleet", "cfg-collection", "script-fleet"} {
		_, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: id})
		if err == nil || !strings.HasPrefix(err.Error(), codeFleetFileUnavailable) {
			t.Fatalf("render %s: err %v", id, err)
		}
		if _, err := rt.fetchSubscription(id); err == nil || !strings.HasPrefix(err.Error(), codeFleetFileUnavailable) {
			t.Fatalf("fetch %s: err %v", id, err)
		}
	}
	if host.calls != 0 {
		t.Fatalf("a refused file read %d catalogue pages", host.calls)
	}
	if _, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "cfg-local"}); err != nil {
		t.Fatalf("a file over a provider record: %v", err)
	}
}

// TestScriptFileRefusesUnreadableSnapshot pins that a script file handed a
// snapshot it cannot read answers snapshot_malformed instead of resolving
// its node source live.
func TestScriptFileRefusesUnreadableSnapshot(t *testing.T) {
	rt, _ := newFleetRuntime(t, nil)
	for _, rec := range []subscriptionRecord{
		{ID: "local", Name: "local", Source: subscriptionSourceLocal, Content: "trojan://pw@192.0.2.10:443#one"},
		{ID: "script", Name: "script", Kind: kindFile, FileType: fileTypeScript, Content: "$content = 'x'", NodeSource: "local"},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatal(err)
		}
	}
	_, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "script", Raw: "x"})
	if err == nil || !strings.HasPrefix(err.Error(), codeSnapshotMalformed) {
		t.Fatalf("an unreadable snapshot: err %v", err)
	}
}
