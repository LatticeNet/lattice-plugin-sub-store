package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// maxProviderResponseBytes bounds what one provider fetch may return. A provider
// that streams without end would otherwise be able to exhaust this process and
// then the snapshot the core stores.
const maxProviderResponseBytes = 8 << 20

// defaultProviderUA is sent when a subscription names no user agent. Providers
// commonly vary their output by client, so an absent UA is a real choice rather
// than a missing value - this one asks for the plain URI list.
const defaultProviderUA = "Lattice/1.0"

type fetchResult struct {
	Raw            string          `json:"raw"`
	Userinfo       string          `json:"userinfo,omitempty"`
	SourceVersion  string          `json:"source_version,omitempty"`
	SourceManifest json.RawMessage `json:"source_manifest,omitempty"`
}

func isVPNCoreSource(source string) bool {
	return source == subscriptionSourceVPNCore || source == subscriptionSourceVPNCoreGraph
}

// fetchSubscription retrieves the provider's current content.
//
// It runs under guarded egress rather than the operator-target primitive: a
// provider URL is an ordinary public address, so it belongs behind the broker's
// SSRF checks rather than behind the escape hatch reserved for the operator's own
// private endpoints.
func (rt *runtime) fetchSubscription(subscriptionID string) (fetchResult, error) {
	rec, err := rt.getSubscription(subscriptionID)
	if err != nil {
		return fetchResult{}, err
	}

	// A vpn-core subscription has no provider to reach: its content is the
	// current node export, read over rpc:call. It is handled before the URL
	// checks because there is no URL involved and none should be required.
	if rec.Source == subscriptionSourceVPNCore {
		links, err := rt.fetchExport(subStoreRequest{UserID: rec.VPNIdentity})
		if err != nil {
			return fetchResult{}, err
		}
		if len(links) == 0 {
			// Serving nothing would reach a client as "you have no nodes" and
			// wipe its configuration, so an empty export is an error here
			// rather than empty content passed downstream.
			return fetchResult{}, fmt.Errorf("subscription %q: vpn-core returned no nodes", subscriptionID)
		}
		return fetchResult{Raw: strings.Join(links, "\n")}, nil
	}
	if rec.Source == subscriptionSourceVPNCoreGraph {
		composed, err := rt.fetchVPNCoreGraph(rec)
		if err != nil {
			return fetchResult{}, err
		}
		return fetchResult{
			Raw:            composed.Raw,
			SourceVersion:  composed.SourceVersion,
			SourceManifest: append(json.RawMessage(nil), composed.SourceManifest...),
		}, nil
	}

	// A manual subscription has nothing to fetch; its content is what was
	// pasted. Returning it here means "refresh" is harmless rather than an
	// error the operator has to learn to ignore.
	if rec.Source == subscriptionSourceLocal {
		if strings.TrimSpace(rec.Content) == "" {
			return fetchResult{}, fmt.Errorf("subscription %q has no pasted content", subscriptionID)
		}
		return fetchResult{Raw: rec.Content}, nil
	}

	target := strings.TrimSpace(rec.URL)
	if target == "" {
		return fetchResult{}, fmt.Errorf("subscription %q has no URL to fetch", subscriptionID)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return fetchResult{}, fmt.Errorf("subscription %q has an unparseable URL", subscriptionID)
	}
	// The scheme is checked here as well as by the broker. A provider URL that is
	// not http(s) is a configuration mistake worth naming at its source, rather
	// than a broker rejection the operator has to trace back.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fetchResult{}, fmt.Errorf("subscription %q URL must be http or https", subscriptionID)
	}

	ua := strings.TrimSpace(rec.UA)
	if ua == "" {
		ua = defaultProviderUA
	}
	raw, err := rt.callHost(latticeplugin.HostMethodHTTPDo, map[string]any{
		"method": "GET",
		"url":    target,
		"header": map[string]string{"User-Agent": ua},
	})
	if err != nil {
		return fetchResult{}, redactProviderError(subscriptionID, err)
	}
	var out struct {
		StatusCode int               `json:"status_code"`
		Header     map[string]string `json:"header,omitempty"`
		BodyBase64 string            `json:"body_base64,omitempty"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fetchResult{}, fmt.Errorf("decode provider response: %w", err)
	}
	if out.StatusCode < 200 || out.StatusCode >= 300 {
		return fetchResult{}, fmt.Errorf("subscription %q provider returned status %d", subscriptionID, out.StatusCode)
	}
	body, err := base64.StdEncoding.DecodeString(out.BodyBase64)
	if err != nil {
		return fetchResult{}, fmt.Errorf("decode provider body: %w", err)
	}
	if len(body) == 0 {
		// An empty body is a failed fetch, not a subscription with no nodes.
		// Returning it as success would overwrite a good snapshot with nothing.
		return fetchResult{}, fmt.Errorf("subscription %q provider returned an empty body", subscriptionID)
	}
	if len(body) > maxProviderResponseBytes {
		return fetchResult{}, fmt.Errorf("subscription %q provider returned %d bytes, limit %d", subscriptionID, len(body), maxProviderResponseBytes)
	}

	return fetchResult{Raw: string(body), Userinfo: userinfoHeader(out.Header)}, nil
}

// userinfoHeader finds the provider's traffic figures. Header names are matched
// case-insensitively because providers are inconsistent about the casing and the
// value is what a client displays as its remaining quota.
func userinfoHeader(header map[string]string) string {
	for name, value := range header {
		if strings.EqualFold(name, "subscription-userinfo") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// redactProviderError keeps a failing URL out of the error text. A provider URL
// is frequently a bearer credential in path form, and this error travels into
// the core's audit log and the operator's screen.
func redactProviderError(subscriptionID string, err error) error {
	return fmt.Errorf("subscription %q provider request failed: %s", subscriptionID, redactURLs(err.Error()))
}

func redactURLs(text string) string {
	var out []string
	for _, field := range strings.Fields(text) {
		if strings.Contains(field, "://") {
			out = append(out, "<redacted-url>")
			continue
		}
		out = append(out, field)
	}
	return strings.Join(out, " ")
}
