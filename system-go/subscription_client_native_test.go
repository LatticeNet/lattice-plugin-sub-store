package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// shareFleetFixture is shaped like this fleet: VLESS Reality and Hysteria2
// links, plus Shadowsocks so that every client target has at least one node it
// can carry. n VLESS links, a Hysteria2 link for every third, and two
// Shadowsocks links.
func shareFleetFixture(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "vless://%08d-1111-4222-8333-444455556666@203.0.113.%d:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.example.com&fp=chrome&pbk=AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdefg&sid=%02x&type=tcp#node-%03d\n", i, i%250+1, i%256, i)
		if i%3 == 0 {
			fmt.Fprintf(&b, "hysteria2://pass%03d@198.51.100.%d:8443?sni=h2.example.com&insecure=0#hy2-%03d\n", i, i%250+1, i)
		}
	}
	b.WriteString("ss://YWVzLTEyOC1nY206cHc@192.0.2.10:8388#ss-1\n")
	b.WriteString("ss://YWVzLTEyOC1nY206cHc@192.0.2.11:8388#ss-2\n")
	return b.String()
}

// What the core sends for a share left at "automatic" is format "base64"
// (normalizeProxySubscriptionFormat("") in lattice-server), and the server
// never sends an empty format; both must mean "the client's own format". The
// base64 envelope belongs to exactly one target, the URI list, because that is
// the only document whose clients expect one. Wrapping YAML, JSON or a Surge
// configuration in base64 hands a client a document it cannot read: every
// per-client link the console handed out was broken this way unless the
// operator had picked "plain".
func TestRenderServesEachClientItsNativeEncoding(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "s", Name: "s", Content: shareFleetFixture(4)}); err != nil {
		t.Fatalf("save: %v", err)
	}
	yamlTargets := map[string]bool{"ClashMeta": true, "Clash": true, "Stash": true}
	jsonTargets := map[string]bool{"sing-box": true, "JSON": true}
	targets := []string{"URI", "V2Ray", "Shadowrocket", "QX", "Surge", "SurgeMac", "Loon", "Surfboard", "Stash", "ClashMeta", "Clash", "Egern", "sing-box", "JSON"}
	for _, target := range targets {
		plain, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "s", Format: "plain", Target: target})
		if err != nil {
			t.Fatalf("%s plain: %v", target, err)
		}
		for _, format := range []string{"base64", ""} {
			native, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "s", Format: format, Target: target})
			if err != nil {
				t.Fatalf("%s format %q: %v", target, format, err)
			}
			if native.Target != target {
				t.Fatalf("%s format %q: reply names target %q", target, format, native.Target)
			}
			if target == "URI" {
				decoded, err := base64.StdEncoding.DecodeString(native.Content)
				if err != nil {
					t.Fatalf("URI format %q must stay the classic base64 list: %v", format, err)
				}
				if string(decoded) != plain.Content {
					t.Fatalf("URI format %q decodes to %q, want the plain list", format, head(string(decoded), 80))
				}
				continue
			}
			if native.Content != plain.Content {
				t.Fatalf("%s format %q is wrapped: got %q, want the producer's own %q", target, format, head(native.Content, 60), head(plain.Content, 60))
			}
		}
		switch {
		case yamlTargets[target]:
			if !strings.HasPrefix(strings.TrimSpace(plain.Content), "proxies:") {
				t.Fatalf("%s did not produce a YAML proxy list: %q", target, head(plain.Content, 60))
			}
			if !strings.Contains(plain.ContentType, "yaml") {
				t.Fatalf("%s content type = %q, want YAML", target, plain.ContentType)
			}
		case jsonTargets[target]:
			var doc any
			if err := json.Unmarshal([]byte(plain.Content), &doc); err != nil {
				t.Fatalf("%s did not produce JSON: %v (%q)", target, err, head(plain.Content, 60))
			}
			if !strings.Contains(plain.ContentType, "json") {
				t.Fatalf("%s content type = %q, want JSON", target, plain.ContentType)
			}
		case target == "V2Ray":
			// The V2Ray producer already emits the base64 list; one decode must
			// reach the URIs, not a second envelope.
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(plain.Content))
			if err != nil || !strings.Contains(string(decoded), "://") {
				t.Fatalf("V2Ray must be one base64 envelope around URIs: %v %q", err, head(string(decoded), 60))
			}
		}
	}
}

// ?format=clash and ?format=clash-meta pass the server's validation, so a
// plugin share must answer them with the Clash Meta document rather than the
// render failure (and the 404 decoy) it answered before. ?format=sing-box names
// a client the same way. An explicit ?target= still wins.
func TestRenderFormatThatNamesAClientPicksThatTarget(t *testing.T) {
	rt, _ := newWarmKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "s", Name: "s", Content: shareFleetFixture(2)}); err != nil {
		t.Fatalf("save: %v", err)
	}
	cases := []struct {
		format, target, want string
	}{
		{"clash", "", "ClashMeta"},
		{"clash-meta", "", "ClashMeta"},
		{"sing-box", "", "sing-box"},
		{"clash-meta", "Stash", "Stash"},
	}
	for _, tc := range cases {
		out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "s", Format: tc.format, Target: tc.target, UAClass: "surge"})
		if err != nil {
			t.Fatalf("format %q target %q: %v", tc.format, tc.target, err)
		}
		if out.Target != tc.want {
			t.Fatalf("format %q target %q rendered %q, want %q", tc.format, tc.target, out.Target, tc.want)
		}
		if tc.want == "ClashMeta" && !strings.Contains(out.Content, `"type":"vless"`) {
			t.Fatalf("format %q lost the VLESS nodes: %q", tc.format, head(out.Content, 120))
		}
	}
}

// Clash Verge Rev, FlClash and anything else built on mihomo understand VLESS
// and Hysteria2. The core classifies them as "clashmeta" and the plugin must
// render ClashMeta for that class; legacy "clash" stays Clash.
func TestUAClassClashMetaRendersClashMeta(t *testing.T) {
	if got := subscriptionTarget(subscriptionRecord{}, "clashmeta"); got != "ClashMeta" {
		t.Fatalf("class clashmeta resolved to %q, want ClashMeta", got)
	}
	if got := subscriptionTarget(subscriptionRecord{}, "clash"); got != "Clash" {
		t.Fatalf("class clash resolved to %q, want Clash", got)
	}
	rt, _ := newWarmKVRuntime(t)
	if err := rt.saveSubscription(subscriptionRecord{ID: "s", Name: "s", Content: shareFleetFixture(3)}); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: "s", Format: "base64", UAClass: "clashmeta"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out.Content, `"type":"vless"`) || !strings.Contains(out.Content, `"type":"hysteria2"`) {
		t.Fatalf("a mihomo client lost the fleet's nodes: %q", head(out.Content, 160))
	}
}
