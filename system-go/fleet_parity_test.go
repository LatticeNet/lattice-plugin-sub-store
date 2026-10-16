package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// TestSnapshotRenderParityFleet is the snapshot-render parity gate for fleet
// records (S2 plan section 8): a fleet record rendered on the serve path
// from the envelope its fetch wrote equals its live render, which reads the
// catalogue under the record's own selection, once every placeholder is
// normalised to LATTICE-SYN-<line_uuid>-<field>-*, since every plan
// computation draws fresh ones. It holds for a fleet sub with a native
// chain, one whose chain runs on the bundle's process, and a collection of
// a fleet member and a provider member, on three targets. The live render
// is the render method itself, asked with member_revisions and no
// snapshot, which is the staged path core takes when it previews a plan.
//
// "targeted" names its nodes after the target its script operator is handed,
// so its plan differs per target: the second target's serve-path render
// reads the same source version the first one cached under, and only the
// target in the plan cache's key keeps it from being served the first
// target's plan.
func TestSnapshotRenderParityFleet(t *testing.T) {
	freshFleetPlans(t)
	rt, host := newFleetRuntime(t, sweepRows())
	host.applySelector = true
	rename := renameStep("tokyo", "osaka")
	sortDesc := json.RawMessage(`{"type":"Sort Operator","args":"desc"}`)
	seedRecords(t, rt,
		subscriptionRecord{ID: "sub", Source: subscriptionSourceFleet,
			Process: []json.RawMessage{structuredKeep("protocol", "vless", "trojan", "hysteria2", "tuic", "vmess", "socks", "anytls"), rename, sortDesc}},
		subscriptionRecord{ID: "scripted", Source: subscriptionSourceFleet,
			Process: []json.RawMessage{scriptStep(t, `function operator(proxies) { return proxies.map(function (p) { p.name = "[s] " + p.name; return p; }); }`)}},
		subscriptionRecord{ID: "targeted", Source: subscriptionSourceFleet,
			Process: []json.RawMessage{scriptStep(t, `function operator(proxies, targetPlatform) { return proxies.map(function (p) { p.name = "[" + targetPlatform + "] " + p.name; return p; }); }`)}},
		subscriptionRecord{ID: "provider", Source: subscriptionSourceLocal, Content: "trojan://provider-pw@198.51.100.7:443#provider\nvless://0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d@198.51.100.8:443?security=tls&sni=p.example#provider-vless"},
		subscriptionRecord{ID: "coll", Kind: kindCollection, Members: []string{"sub", "provider"}, Process: []json.RawMessage{sortDesc}},
	)
	for _, id := range []string{"sub", "scripted", "targeted", "coll"} {
		snap, err := rt.fetchSubscription(id)
		if err != nil {
			t.Fatalf("fetch %s: %v", id, err)
		}
		plans := map[string]string{}
		for _, target := range []string{"sing-box", "ClashMeta", "URI"} {
			fromSnapshot, res := renderFleetCall(t, rt, map[string]any{"subscription_id": id, "raw": snap.Raw, "target": target})
			if !res.OK {
				t.Fatalf("%s %s: serve-path render: %s", id, target, res.Error)
			}
			reads := host.reads
			live, res := renderFleetCall(t, rt, map[string]any{"subscription_id": id, "target": target, "member_revisions": map[string]string{}})
			if !res.OK {
				t.Fatalf("%s %s: live render: %s", id, target, res.Error)
			}
			if host.reads == reads {
				t.Fatalf("%s %s: the live render read no catalogue page", id, target)
			}
			plan := decodeRenderPlan(t, fromSnapshot.Plan)
			if len(plan.Nodes) == 0 {
				t.Fatalf("%s %s: the plan is empty", id, target)
			}
			a, b := normalisePlaceholders(fromSnapshot.Plan), normalisePlaceholders(live.Plan)
			if !bytes.Equal(a, b) {
				t.Fatalf("%s %s: the snapshot render differs from the live render\nsnapshot: %.600s\nlive:     %.600s", id, target, a, b)
			}
			if bytes.Equal(fromSnapshot.Plan, live.Plan) {
				t.Fatalf("%s %s: the two renders share placeholders, so one plan was reused", id, target)
			}
			plans[target] = string(a)
			if id == "targeted" && !bytes.Contains(a, []byte("["+target+"]")) {
				t.Fatalf("targeted %s: the plan's names do not carry the target: %.300s", target, a)
			}
		}
		if id == "targeted" && (plans["sing-box"] == plans["ClashMeta"] || plans["ClashMeta"] == plans["URI"]) {
			t.Fatal("targeted: two targets rendered the same plan")
		}
	}
}

// TestFleetFetchReplyIsACoreSnapshot holds the fetch reply to the snapshot
// core stores from it: the reply's raw, userinfo, source_version and
// source_manifest, in a schema 2 SubscriptionSnapshot, must pass the SDK's
// strict decode, for a fleet sub, a fleet collection and a provider record.
// A fleet envelope's digest sent as source_version without a manifest
// failed that decode ("requires paired provenance fields") on every share
// of a fleet record, which only the end-to-end test over the real plugin
// showed; the digest stays inside raw, where the plugin's plan cache reads
// it.
func TestFleetFetchReplyIsACoreSnapshot(t *testing.T) {
	rt, _ := newFleetRuntime(t, fleetTestRows(4, "vless"))
	seedRecords(t, rt,
		subscriptionRecord{ID: "sub", Source: subscriptionSourceFleet},
		subscriptionRecord{ID: "provider", Source: subscriptionSourceLocal, Content: "trojan://provider-pw@198.51.100.7:443#provider"},
		subscriptionRecord{ID: "coll", Kind: kindCollection, Members: []string{"sub", "provider"}},
	)
	for _, id := range []string{"sub", "coll", "provider"} {
		res := callSubscription(t, rt, "fetch", map[string]any{"subscription_id": id})
		if !res.OK {
			t.Fatalf("fetch %s: %s", id, res.Error)
		}
		var reply struct {
			Raw            string          `json:"raw"`
			Userinfo       string          `json:"userinfo"`
			SourceVersion  string          `json:"source_version"`
			SourceManifest json.RawMessage `json:"source_manifest"`
		}
		if err := json.Unmarshal(res.Result, &reply); err != nil {
			t.Fatal(err)
		}
		stored := map[string]any{"schema_version": model.SubscriptionSnapshotSchemaVersion, "plugin_id": pluginID, "subscription_id": id,
			"raw": reply.Raw, "userinfo": reply.Userinfo, "fetched_at": "2026-10-16T00:00:00Z", "last_attempt_at": "2026-10-16T00:00:00Z", "stale": false}
		if reply.SourceVersion != "" || len(reply.SourceManifest) > 0 {
			stored["source_version"], stored["source_manifest"] = reply.SourceVersion, reply.SourceManifest
		}
		var snapshot model.SubscriptionSnapshot
		if err := json.Unmarshal(mustJSON(stored), &snapshot); err != nil {
			t.Fatalf("%s: core cannot store the fetch reply as a snapshot: %v", id, err)
		}
		if env, ok := decodeSnapshotEnvelope(reply.Raw); id != "provider" && (!ok || !strings.HasPrefix(env.SourceVersion, "lfe1-")) {
			t.Fatalf("%s: the envelope inside raw lost its digest: %+v", id, env.SourceVersion)
		}
	}
}
