package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// mixedMembers seeds three members in three encodings: a URI list with four
// nodes, a base64 list with three, and a Clash YAML document with two.
func mixedMembers(t *testing.T, rt *runtime) {
	t.Helper()
	uriA := shareFleetFixtureNoSS(3)                                                                                    // 3 vless + 1 hy2
	b64B := base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(shareFleetFixtureNoSS(2), "node-", "b-node-"))) // 2 vless + 1 hy2
	yamlC := "proxies:\n  - {name: c-node-1, type: ss, server: 192.0.2.10, port: 8388, cipher: aes-128-gcm, password: pw}\n  - {name: c-node-2, type: ss, server: 192.0.2.11, port: 8388, cipher: aes-128-gcm, password: pw}\n"
	seedSub(t, rt, "a", nil, uriA)
	seedSub(t, rt, "b", nil, b64B)
	seedSub(t, rt, "c", nil, yamlC)
}

func shareFleetFixtureNoSS(n int) string {
	full := shareFleetFixture(n)
	var keep []string
	for _, line := range strings.Split(full, "\n") {
		if strings.HasPrefix(line, "ss://") || strings.TrimSpace(line) == "" {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n") + "\n"
}

func countURINodes(body string) int {
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.Contains(line, "://") {
			n++
		}
	}
	return n
}

// Members were joined as text and parsed once, and the engine's base64 and
// YAML handling only fires when a whole document is in that encoding. Mixing a
// URI list with a base64 list or a Clash document therefore lost whole members
// and served the rest as complete: a+b served 4 of 7, a+c 4 of 6, b+c failed,
// a+b+c 4 of 9. Each member is now parsed on its own.
func TestCollectionMixedEncodingsKeepEveryMember(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	mixedMembers(t, rt)
	cases := []struct {
		members []string
		want    int
	}{
		{[]string{"a"}, 4},
		{[]string{"b"}, 3},
		{[]string{"c"}, 2},
		{[]string{"a", "b"}, 7},
		{[]string{"a", "c"}, 6},
		{[]string{"b", "c"}, 5},
		{[]string{"a", "b", "c"}, 9},
	}
	for _, tc := range cases {
		id := "col-" + strings.Join(tc.members, "")
		if err := rt.saveSubscription(subscriptionRecord{ID: id, Kind: kindCollection, Name: id, Members: tc.members}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
		live, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: id, Format: "plain", Target: "URI"})
		if err != nil {
			t.Fatalf("%s live render: %v", id, err)
		}
		if got := countURINodes(live.Content); got != tc.want {
			t.Fatalf("%s live served %d nodes, want %d", id, got, tc.want)
		}
		// The refresh path stores per-member text and the serve path renders
		// from it; both must agree.
		snap, err := rt.fetchSubscription(id)
		if err != nil {
			t.Fatalf("%s refresh: %v", id, err)
		}
		served, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: id, Format: "plain", Target: "URI", Raw: snap.Raw})
		if err != nil {
			t.Fatalf("%s snapshot render: %v", id, err)
		}
		if got := countURINodes(served.Content); got != tc.want {
			t.Fatalf("%s snapshot served %d nodes, want %d", id, got, tc.want)
		}
	}
}

// A snapshot the core stored before this change holds each member's provider
// body as it arrived. It must render every member too, without waiting for the
// next refresh.
func TestCollectionRendersAPreUpgradeSnapshotWithMixedMembers(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	mixedMembers(t, rt)
	if err := rt.saveSubscription(subscriptionRecord{ID: "abc", Kind: kindCollection, Name: "abc", Members: []string{"a", "b", "c"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	var members []fileScriptMember
	for _, id := range []string{"a", "b", "c"} {
		rec, err := rt.getSubscription(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		members = append(members, fileScriptMember{SubName: id, Raw: rec.Content})
	}
	envelope, err := json.Marshal(snapshotArtifacts{Members: members})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "abc", Format: "plain", Target: "URI", Raw: string(envelope)})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := countURINodes(out.Content); got != 9 {
		t.Fatalf("pre-upgrade snapshot served %d nodes, want 9", got)
	}
}

// Members are not round-tripped through the URI producer to merge them. That
// producer has no form for HTTP, Snell or SSH nodes, so converting every member
// to URI would drop nodes a same-format combination serves today.
func TestCollectionKeepsMembersWhoseNodesHaveNoURIForm(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	seedSub(t, rt, "surge-a", nil, "snell-a = snell, 192.0.2.20, 443, psk=one, version=4\n")
	seedSub(t, rt, "surge-b", nil, "http-b = http, 192.0.2.21, 8080, user, pass\n")
	if err := rt.saveSubscription(subscriptionRecord{ID: "c", Kind: kindCollection, Name: "c", Members: []string{"surge-a", "surge-b"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "c", Format: "plain", Target: "Surge"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out.Content, "snell-a") || !strings.Contains(out.Content, "http-b") {
		t.Fatalf("a member was lost on the way through the merge: %q", out.Content)
	}
}
