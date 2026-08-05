package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// renderResult is what the core receives. It carries bytes and a content type and
// nothing else: the plugin does not choose a status code, set a header, or see
// the share's token.
type renderResult struct {
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
}

// uaClassTargets maps the core's bounded client classification onto the engine's
// client target. It is used only when a subscription does not name its own
// target, so an operator who has chosen one is never overridden by a header.
var uaClassTargets = map[string]string{
	"surge":        "Surge",
	"loon":         "Loon",
	"quantumultx":  "QX",
	"stash":        "Stash",
	"shadowrocket": "Shadowrocket",
	"clash":        "Clash",
	"singbox":      "sing-box",
	"egern":        "Egern",
}

// subscriptionTarget picks the engine target for one render. An explicit target
// on the record wins; otherwise the client class decides; a client the core could
// not classify falls back to the widely accepted URI list.
func subscriptionTarget(rec subscriptionRecord, uaClass string) string {
	if t := strings.TrimSpace(rec.Target); t != "" {
		return t
	}
	if t, ok := uaClassTargets[uaClass]; ok {
		return t
	}
	return "URI"
}

// renderSubscription produces the body the core will serve.
//
// It refuses to return empty content. The core refuses an empty body too, and
// both refusals exist deliberately: a proxy client that receives an empty but
// successful subscription deletes every node it had, so the failure is worth
// stopping twice rather than relying on either layer alone.
func (rt *runtime) renderSubscription(subscriptionID, format, uaClass string) (renderResult, error) {
	rec, err := rt.getSubscription(subscriptionID)
	if err != nil {
		return renderResult{}, err
	}

	source := rec.Content
	if strings.TrimSpace(source) == "" {
		// Remote fetch arrives with sub-project 2. Until then a record without
		// inline content has nothing to render, and saying so is better than
		// serving an empty subscription.
		return renderResult{}, fmt.Errorf("subscription %q has no content to render", subscriptionID)
	}

	converted, err := rt.subStoreEngine().convert(subStoreConversionRequest{
		Raw:       source,
		Target:    subscriptionTarget(rec, uaClass),
		Operators: rec.Operators,
	})
	if err != nil {
		return renderResult{}, err
	}
	if strings.TrimSpace(converted.Output) == "" {
		return renderResult{}, fmt.Errorf("subscription %q converted to empty content", subscriptionID)
	}

	body, contentType, err := encodeSubscriptionOutput(converted.Output, format)
	if err != nil {
		return renderResult{}, err
	}
	return renderResult{Content: body, ContentType: contentType}, nil
}

// encodeSubscriptionOutput applies the core's transport format to the engine's
// output. Target decides what the config says; format decides how it is carried.
func encodeSubscriptionOutput(output, format string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "base64":
		return base64.StdEncoding.EncodeToString([]byte(output)), "text/plain; charset=utf-8", nil
	case "plain":
		return output, "text/plain; charset=utf-8", nil
	case "sing-box", "singbox":
		return output, "application/json; charset=utf-8", nil
	default:
		return "", "", fmt.Errorf("unsupported subscription format %q", format)
	}
}

// handleSubscriptionCall serves the interface the core calls to build a public
// subscription body. It is deliberately tiny: the only method is a read, and the
// only thing it returns is content plus a content type.
func (rt *runtime) handleSubscriptionCall(call callPayload) response {
	switch call.Method {
	case "render":
		var req struct {
			SubscriptionID string `json:"subscription_id"`
			Format         string `json:"format"`
			UAClass        string `json:"ua_class"`
		}
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid render payload: %w", err))
			}
		}
		out, err := rt.renderSubscription(req.SubscriptionID, req.Format, req.UAClass)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		body, err := json.Marshal(out)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	default:
		return latticeplugin.ErrorResponse(fmt.Errorf("unsupported method %q", call.Method))
	}
}
