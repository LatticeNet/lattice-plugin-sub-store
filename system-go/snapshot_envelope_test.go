package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// A version 2 envelope survives its own encoding and reads back as the text
// the render paths take; everything a runtime before it stored is read as
// the version 1 text it is, including a provider body that happens to be a
// JSON document with a version field.
func TestEnvelopeV2RoundTripAndV1Detection(t *testing.T) {
	text := textEnvelope(kindSub, "ss://YWVzLTEyOC1nY206cHc@192.0.2.10:8388#one", "sv1:abc")
	members := membersEnvelope(kindFile, []fileScriptMember{{SubName: "home", Raw: "vless://a"}, {SubName: "work", Raw: "vless://b"}})
	members.SourceID, members.SourceName, members.SourceKind = "coll", "Coll", kindCollection
	for name, env := range map[string]snapshotEnvelope{"text": text, "members": members} {
		encoded, err := encodeSnapshotEnvelope(env)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		decoded, ok := decodeSnapshotEnvelope(encoded)
		if !ok || !reflect.DeepEqual(decoded, env) {
			t.Fatalf("%s did not round trip: ok=%v %+v", name, ok, decoded)
		}
	}
	encodedText, _ := encodeSnapshotEnvelope(text)
	if got := snapshotText(encodedText); got != text.Raw {
		t.Fatalf("text envelope read back as %q", got)
	}
	if text.RawSHA256 != "" && len(text.RawSHA256) != 64 {
		t.Fatalf("raw_sha256 = %q, want the full hex digest", text.RawSHA256)
	}
	encodedMembers, _ := encodeSnapshotEnvelope(members)
	var snap snapshotArtifacts
	if err := json.Unmarshal([]byte(snapshotText(encodedMembers)), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.SourceID != "coll" || snap.SourceName != "Coll" || snap.SourceKind != kindCollection || len(snap.Members) != 2 || snap.Members[1].Raw != "vless://b" {
		t.Fatalf("members envelope read back as %+v", snap)
	}
	for name, v1 := range map[string]string{
		"uri list":            "vless://a@b:443#x\nvless://c@d:443#y",
		"base64 list":         "dmxlc3M6Ly9hQGI6NDQzI3g=",
		"v1 members object":   `{"members":[{"sub_name":"home","raw":"vless://a"}]}`,
		"json with version 2": `{"version":2,"servers":[{"server":"a"}]}`,
		"unknown kind":        `{"version":2,"kind":"catalogue","raw":"x"}`,
		"sing-box config":     `{"outbounds":[{"type":"vless"}]}`,
		"clash yaml":          "proxies:\n  - name: a\n",
	} {
		if _, ok := decodeSnapshotEnvelope(v1); ok {
			t.Errorf("%s was read as a version 2 envelope", name)
		}
		if got := snapshotText(v1); got != v1 {
			t.Errorf("%s changed on the way to render: %q", name, got)
		}
	}
}

// The core skips a render when raw is byte for byte what it holds, so two
// refreshes of unchanged content must produce the same raw, and a change in
// content must produce a different one.
func TestEnvelopeHasNoVolatileFields(t *testing.T) {
	rt, host := newCountingRuntime(t)
	seedBudgetStore(t, rt)
	for _, id := range []string{"local-a", "remote-a", "coll", "scripty"} {
		first, err := rt.fetchSubscription(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		second, err := rt.fetchSubscription(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if first.Raw != second.Raw {
			t.Fatalf("%s: two fetches of unchanged content differ:\n%s\n%s", id, first.Raw, second.Raw)
		}
		if _, ok := decodeSnapshotEnvelope(first.Raw); !ok {
			t.Fatalf("%s: fetch did not write a version 2 envelope", id)
		}
	}
	before, _ := rt.fetchSubscription("remote-a")
	host.remoteBody = scriptNodeHome + "\n" + "ss://YWVzLTEyOC1nY206cHc@192.0.2.99:8388#new"
	after, err := rt.fetchSubscription("remote-a")
	if err != nil {
		t.Fatal(err)
	}
	if before.Raw == after.Raw {
		t.Fatal("changed provider content produced the same snapshot")
	}
}

// An envelope past the core's raw bound is refused in the plugin with a
// stated reason, not handed to a server that refuses it with a bare size.
func TestEnvelopeOverTheRawBoundIsRefused(t *testing.T) {
	// Quotes escape to two bytes each, so a provider body under the bound
	// goes over it once enveloped.
	body := strings.Repeat(`"`, model.MaxSubscriptionRawBytes/2+1)
	if len(body) >= model.MaxSubscriptionRawBytes {
		t.Fatal("the fixture must fit the bound before the envelope")
	}
	if _, err := encodeSnapshotEnvelope(textEnvelope(kindSub, body, "")); err == nil || !strings.HasPrefix(err.Error(), snapshotTooLargeCode+":") {
		t.Fatalf("an oversize envelope = %v, want the %s refusal", err, snapshotTooLargeCode)
	}
	if _, err := encodeSnapshotEnvelope(textEnvelope(kindSub, strings.Repeat("a", 1<<20), "")); err != nil {
		t.Fatalf("a 1 MiB snapshot was refused: %v", err)
	}
}
