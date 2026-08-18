package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxPublishBytes bounds what one publish sends. The engine already caps its own
// output; this is the second bound, on what leaves the host.
const maxPublishBytes = 6 << 20

type publishResult struct {
	SubscriptionID string `json:"subscription_id"`
	Bytes          int    `json:"bytes"`
	StatusCode     int    `json:"status_code"`
}

// publishSubscription renders a subscription and sends it to a destination the
// OPERATOR designated.
//
// This is the general form of what upstream calls artifact sync. It is not
// modelled on Gist specifically: the reason Sub-Store needs artifacts at all is
// to obtain a stable public URL, and Lattice already serves one directly, so
// hardcoding one vendor would add a second publishing path that can drift from
// the first without adding a capability. A destination is any operator target,
// which covers a Gist API call, a webhook, or an object store equally.
//
// The destination may be a secret:// reference. The host resolves it, so a
// credential embedded in the URL never enters this process.
func (rt *runtime) publishSubscription(subscriptionID, destination, method, format string) (publishResult, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return publishResult{}, fmt.Errorf("publish needs a destination")
	}
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "", "PUT":
		method = "PUT"
	case "POST":
		method = "POST"
	case "PATCH":
		method = "PATCH"
	default:
		// GET and DELETE are not publishing. Refusing them keeps this method from
		// becoming a general-purpose request proxy with an operator target.
		return publishResult{}, fmt.Errorf("publish method must be PUT, POST or PATCH")
	}

	rendered, err := rt.renderSubscription(subscriptionRenderRequest{SubscriptionID: subscriptionID, Format: format, UAClass: "other"})
	if err != nil {
		return publishResult{}, err
	}
	body := []byte(rendered.Content)
	if len(body) == 0 {
		// Publishing an empty config would overwrite a good destination with
		// nothing, which is the same destructive shape the serve path refuses.
		return publishResult{}, fmt.Errorf("subscription %q produced no content to publish", subscriptionID)
	}
	if len(body) > maxPublishBytes {
		return publishResult{}, fmt.Errorf("subscription %q produced %d bytes, publish limit %d", subscriptionID, len(body), maxPublishBytes)
	}

	status, _, err := rt.httpDo(method, destination, body)
	if err != nil {
		return publishResult{}, fmt.Errorf("publish %q failed: %s", subscriptionID, redactURLs(err.Error()))
	}
	if status < 200 || status >= 300 {
		return publishResult{}, fmt.Errorf("publish %q returned status %d", subscriptionID, status)
	}
	return publishResult{SubscriptionID: subscriptionID, Bytes: len(body), StatusCode: status}, nil
}

func (rt *runtime) handlePublishCall(payload json.RawMessage) (json.RawMessage, error) {
	var req struct {
		SubscriptionID string `json:"subscription_id"`
		Destination    string `json:"destination"`
		Method         string `json:"method"`
		Format         string `json:"format"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("invalid publish payload: %w", err)
		}
	}
	out, err := rt.publishSubscription(req.SubscriptionID, req.Destination, req.Method, req.Format)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}
