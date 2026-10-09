package main

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
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

	body, ct, err := encodeSubscriptionOutput(output, "base64", "URI")
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
	if body, _, err := encodeSubscriptionOutput(output, "", "URI"); err != nil || body == output {
		t.Fatalf("empty format must be the client-native default (base64 for URI): %q %v", body, err)
	}

	body, _, err = encodeSubscriptionOutput(output, "plain", "URI")
	if err != nil || body != output {
		t.Fatalf("plain: %q %v", body, err)
	}

	// The envelope is the URI list's alone: a document any other client reads
	// is carried as its producer wrote it, whatever the format says.
	const yaml = "proxies:\n  - {name: a, type: ss}\n"
	body, ct, err = encodeSubscriptionOutput(yaml, "base64", "ClashMeta")
	if err != nil || body != yaml || !strings.Contains(ct, "yaml") {
		t.Fatalf("base64 + ClashMeta must stay YAML: %q %q %v", body, ct, err)
	}

	_, ct, err = encodeSubscriptionOutput(`{"outbounds":[]}`, "sing-box", "sing-box")
	if err != nil {
		t.Fatalf("sing-box: %v", err)
	}
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("sing-box content type = %q", ct)
	}

	if _, _, err := encodeSubscriptionOutput(output, "nonsense", "URI"); err == nil {
		t.Fatal("an unknown format was accepted")
	}
}

// The Sub-Store URL parity contract: an explicit target names the client and
// outranks everything, the record's own pin included. Below it the order is
// pin, then the core's target for the agent (ua_target), then the plugin's
// own UA class table, then URI.
func TestResolveRenderTargetPriority(t *testing.T) {
	pinned := subscriptionRecord{Target: "Clash"}
	free := subscriptionRecord{}
	cases := []struct {
		name          string
		rec           subscriptionRecord
		explicit      string
		defaultTarget string
		uaTarget      string
		uaClass       string
		want          string
	}{
		{"explicit beats the record pin", pinned, "Stash", "", "", "surge", "Stash"},
		{"explicit beats the UA class", free, "sing-box", "", "", "surge", "sing-box"},
		{"explicit beats ua_target", free, "sing-box", "", "ClashMeta", "clash", "sing-box"},
		{"explicit beats the default target", free, "sing-box", "QX", "", "", "sing-box"},
		{"pin beats the UA class", pinned, "", "", "", "surge", "Clash"},
		{"pin beats ua_target", pinned, "", "", "ClashMeta", "clash", "Clash"},
		{"pin beats the default target", pinned, "", "QX", "", "", "Clash"},
		{"default target beats ua_target", free, "", "QX", "ClashMeta", "clash", "QX"},
		{"default target beats the UA class", free, "", "QX", "", "loon", "QX"},
		{"whitespace default target does not count", free, "", "  ", "", "loon", "Loon"},
		{"ua_target beats the UA class", free, "", "", "ClashMeta", "clash", "ClashMeta"},
		{"UA class fills the gap", free, "", "", "", "loon", "Loon"},
		{"whitespace ua_target does not count", free, "", "", "  ", "loon", "Loon"},
		{"URI is the last resort", free, "", "", "", "", "URI"},
		{"whitespace explicit does not count", pinned, "   ", "", "", "surge", "Clash"},
	}
	for _, tc := range cases {
		if got := resolveRenderTarget(tc.rec, tc.explicit, tc.defaultTarget, tc.uaTarget, tc.uaClass); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

// The core sends a mihomo client ua_class "clash", for the plugins built
// before the clashmeta class, and the target it resolved in ua_target. The
// render decodes ua_target and serves ClashMeta: without it the class alone
// picks legacy Clash, which carries no VLESS, and the client would get a
// document with nothing in it. A record's pin and an explicit target still
// outrank it.
func TestRenderHonoursUATarget(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	link := perfgen.URIs(1)[0]
	for _, rec := range []map[string]any{
		{"id": "free", "name": "free", "content": link},
		{"id": "pinned", "name": "pinned", "content": link, "target": "sing-box"},
	} {
		decodeResult(t, callSubscription(t, rt, "save", map[string]any{"subscription": rec}), &struct{}{})
	}
	render := func(payload map[string]any) (renderResult, response) {
		res := callSubscription(t, rt, "render", payload)
		var out renderResult
		if res.OK {
			decodeResult(t, res, &out)
		}
		return out, res
	}

	out, res := render(map[string]any{"subscription_id": "free", "format": "plain", "ua_class": "clash", "ua_target": "ClashMeta"})
	if !res.OK || out.Target != "ClashMeta" || !strings.Contains(out.Content, `"type":"vless"`) {
		t.Fatalf("with ua_target: ok %v err %q target %q content %q", res.OK, res.Error, out.Target, out.Content)
	}
	// Without it the class picks legacy Clash, which drops the VLESS node and
	// is refused as a document with no node for the client.
	if _, res := render(map[string]any{"subscription_id": "free", "format": "plain", "ua_class": "clash"}); res.OK || !strings.Contains(res.Error, zeroNodesForTargetCode) {
		t.Fatalf("without ua_target: ok %v err %q", res.OK, res.Error)
	}
	if out, res := render(map[string]any{"subscription_id": "pinned", "format": "plain", "ua_class": "clash", "ua_target": "ClashMeta"}); !res.OK || out.Target != "sing-box" {
		t.Fatalf("pinned record: ok %v err %q target %q", res.OK, res.Error, out.Target)
	}
	if out, res := render(map[string]any{"subscription_id": "free", "format": "plain", "ua_class": "clash", "ua_target": "ClashMeta", "target": "URI"}); !res.OK || out.Target != "URI" {
		t.Fatalf("explicit target: ok %v err %q target %q", res.OK, res.Error, out.Target)
	}
}
