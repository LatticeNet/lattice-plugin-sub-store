package main

import (
	"bytes"
	"encoding/json"
	"testing"
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
		subscriptionRecord{ID: "provider", Source: subscriptionSourceLocal, Content: "trojan://provider-pw@198.51.100.7:443#provider\nvless://0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d@198.51.100.8:443?security=tls&sni=p.example#provider-vless"},
		subscriptionRecord{ID: "coll", Kind: kindCollection, Members: []string{"sub", "provider"}, Process: []json.RawMessage{sortDesc}},
	)
	for _, id := range []string{"sub", "scripted", "coll"} {
		snap, err := rt.fetchSubscription(id)
		if err != nil {
			t.Fatalf("fetch %s: %v", id, err)
		}
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
		}
	}
}
