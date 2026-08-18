package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

// The core refuses an empty body as well. Both refusals exist deliberately: a
// proxy client that receives an empty but successful subscription deletes every
// node it had, so the failure is worth stopping twice rather than trusting either
// layer alone.
func TestRenderRefusesToProduceEmptyContent(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "empty", Name: "empty", Content: "   "}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "empty", Format: "base64", UAClass: "surge"}); err == nil {
		t.Fatal("render returned success for a subscription with no content")
	}
}

func TestRenderUnknownSubscriptionIsAnError(t *testing.T) {
	rt, _ := newKVRuntime(t)
	if _, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "missing", Format: "base64", UAClass: "surge"}); err == nil {
		t.Fatal("unknown subscription rendered successfully")
	}
}

// An explicit target on the record must win over the client classification: an
// operator who chose a target is not overridden by a header.
func TestSubscriptionTargetPrefersTheRecord(t *testing.T) {
	if got := subscriptionTarget(subscriptionRecord{Target: "Clash"}, "surge"); got != "Clash" {
		t.Fatalf("record target ignored: %q", got)
	}
	if got := subscriptionTarget(subscriptionRecord{}, "surge"); got != "Surge" {
		t.Fatalf("ua class target = %q, want Surge", got)
	}
	if got := subscriptionTarget(subscriptionRecord{}, "other"); got != "URI" {
		t.Fatalf("unclassified client target = %q, want URI", got)
	}
	if got := subscriptionTarget(subscriptionRecord{Target: "  "}, "loon"); got != "Loon" {
		t.Fatalf("blank record target must fall through, got %q", got)
	}
}

func TestEncodeSubscriptionOutput(t *testing.T) {
	const output = "vless://example"

	body, ct, err := encodeSubscriptionOutput(output, "base64")
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil || string(decoded) != output {
		t.Fatalf("base64 round trip failed: %q %v", body, err)
	}
	if ct != "text/plain; charset=utf-8" {
		t.Fatalf("base64 content type = %q", ct)
	}

	// An absent format must behave as the default rather than as an error: the
	// core sends the share's default, which may itself be empty.
	if _, _, err := encodeSubscriptionOutput(output, ""); err != nil {
		t.Fatalf("empty format rejected: %v", err)
	}

	body, _, err = encodeSubscriptionOutput(output, "plain")
	if err != nil || body != output {
		t.Fatalf("plain: %q %v", body, err)
	}

	_, ct, err = encodeSubscriptionOutput(`{"outbounds":[]}`, "sing-box")
	if err != nil {
		t.Fatalf("sing-box: %v", err)
	}
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("sing-box content type = %q", ct)
	}

	if _, _, err := encodeSubscriptionOutput(output, "nonsense"); err == nil {
		t.Fatal("an unknown format was accepted")
	}
}

// The Sub-Store URL parity contract: an explicit target names the client and
// outranks everything — the record's own pin included. Below it the existing
// order holds (pin, then UA class, then URI).
func TestResolveRenderTargetPriority(t *testing.T) {
	pinned := subscriptionRecord{Target: "Clash"}
	free := subscriptionRecord{}
	cases := []struct {
		name     string
		rec      subscriptionRecord
		explicit string
		uaClass  string
		want     string
	}{
		{"explicit beats the record pin", pinned, "Stash", "surge", "Stash"},
		{"explicit beats the UA class", free, "sing-box", "surge", "sing-box"},
		{"pin beats the UA class", pinned, "", "surge", "Clash"},
		{"UA class fills the gap", free, "", "loon", "Loon"},
		{"URI is the last resort", free, "", "", "URI"},
		{"whitespace explicit does not count", pinned, "   ", "surge", "Clash"},
	}
	for _, tc := range cases {
		if got := resolveRenderTarget(tc.rec, tc.explicit, tc.uaClass); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
