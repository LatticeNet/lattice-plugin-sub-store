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
func (rt *runtime) renderSubscription(subscriptionID, format, uaClass, raw string) (renderResult, error) {
	rec, err := rt.getSubscription(subscriptionID)
	if err != nil {
		return renderResult{}, err
	}

	// The core hands back the snapshot it holds for this subscription. Inline
	// content is the fallback for a record that has no remote source at all.
	source := raw
	if strings.TrimSpace(source) == "" {
		source = rec.Content
	}
	if strings.TrimSpace(source) == "" {
		// Remote fetch arrives with sub-project 2. Until then a record without
		// inline content has nothing to render, and saying so is better than
		// serving an empty subscription.
		return renderResult{}, fmt.Errorf("subscription %q has no content to render", subscriptionID)
	}

	if err := validateOperators(rec.Operators); err != nil {
		return renderResult{}, fmt.Errorf("subscription %q: %w", subscriptionID, err)
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
	case "fetch":
		var req struct {
			SubscriptionID string `json:"subscription_id"`
		}
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid fetch payload: %w", err))
			}
		}
		out, err := rt.fetchSubscription(req.SubscriptionID)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		body, err := json.Marshal(out)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "publish":
		body, err := rt.handlePublishCall(call.Payload)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "export":
		body, err := rt.exportBackup()
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(mustJSON(map[string]any{"backup": string(body)}), "")
	case "import":
		var req struct {
			Backup string `json:"backup"`
		}
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid import payload: %w", err))
			}
		}
		out, err := rt.importBackup([]byte(req.Backup))
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		body, err := json.Marshal(out)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "get_settings":
		settings, err := rt.loadSettings()
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(mustJSON(settings), "")
	case "save_settings":
		var req pluginSettings
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid settings payload: %w", err))
			}
		}
		if err := rt.saveSettings(req); err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		saved, err := rt.loadSettings()
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(mustJSON(saved), "")
	case "migrate":
		var req subStoreRequest
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid migrate payload: %w", err))
			}
		}
		report, err := rt.migrateFromSubStore(req)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		body, err := json.Marshal(report)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "list":
		records, err := rt.listSubscriptions()
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		// The list is a management view, so it reports what a subscription IS
		// without its stored content: a definition list must not double as a
		// dump of every provider payload.
		type view struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			HasURL    bool   `json:"has_url"`
			HasInline bool   `json:"has_inline_content"`
			Target    string `json:"target,omitempty"`
			Operators int    `json:"operator_count"`
			Imported  bool   `json:"imported"`
		}
		views := make([]view, 0, len(records))
		for _, rec := range records {
			views = append(views, view{
				ID: rec.ID, Name: rec.Name,
				HasURL: strings.TrimSpace(rec.URL) != "", HasInline: strings.TrimSpace(rec.Content) != "",
				Target: rec.Target, Operators: len(rec.Operators), Imported: rec.Origin != nil,
			})
		}
		body, err := json.Marshal(map[string]any{"subscriptions": views})
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "get":
		// `list` deliberately omits content and operators so a management view
		// cannot double as a dump of every provider payload. Editing one record
		// still needs them, so `get` returns the whole thing for exactly one id.
		var req struct {
			SubscriptionID string `json:"subscription_id"`
		}
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid get payload: %w", err))
			}
		}
		if strings.TrimSpace(req.SubscriptionID) == "" {
			return latticeplugin.ErrorResponse(fmt.Errorf("subscription_id is required"))
		}
		rec, err := rt.getSubscription(req.SubscriptionID)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		body, err := json.Marshal(map[string]any{"subscription": rec})
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "save":
		// Until this existed a subscription could only enter the store by
		// migrating from a standalone Sub-Store or restoring a backup: the
		// record type, its validation and its storage were all reachable, but
		// nothing let an operator create or edit one.
		var req struct {
			Subscription subscriptionRecord `json:"subscription"`
		}
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid save payload: %w", err))
			}
		}
		rec := req.Subscription
		if strings.TrimSpace(rec.ID) == "" {
			return latticeplugin.ErrorResponse(fmt.Errorf("subscription id is required"))
		}
		// Origin records where a record came from during migration. A caller
		// must not be able to forge it, so it is preserved from the stored
		// record rather than taken from the request.
		if existing, err := rt.getSubscription(rec.ID); err == nil {
			rec.Origin = existing.Origin
		} else {
			rec.Origin = nil
		}
		if err := rt.saveSubscription(rec); err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		saved, err := rt.getSubscription(rec.ID)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		body, err := json.Marshal(map[string]any{"subscription": saved, "saved": true})
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "delete":
		var req struct {
			SubscriptionID string `json:"subscription_id"`
		}
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid delete payload: %w", err))
			}
		}
		if strings.TrimSpace(req.SubscriptionID) == "" {
			return latticeplugin.ErrorResponse(fmt.Errorf("subscription_id is required"))
		}
		if err := rt.deleteSubscription(req.SubscriptionID); err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		// Deleting the definition does not retract anything already published:
		// the share lives in the core, and removing it is a separate decision
		// the operator makes there.
		body, err := json.Marshal(map[string]any{"id": req.SubscriptionID, "deleted": true})
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "operators":
		body, err := json.Marshal(map[string]any{"operators": operatorCatalogInfo()})
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "preview":
		var req struct {
			SubscriptionID string            `json:"subscription_id"`
			Raw            string            `json:"raw"`
			Target         string            `json:"target"`
			Operators      []json.RawMessage `json:"operators"`
		}
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid preview payload: %w", err))
			}
		}
		raw := req.Raw
		operators := req.Operators
		target := req.Target
		if strings.TrimSpace(raw) == "" && strings.TrimSpace(req.SubscriptionID) != "" {
			rec, err := rt.getSubscription(req.SubscriptionID)
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			raw = rec.Content
			if operators == nil {
				operators = rec.Operators
			}
			if strings.TrimSpace(target) == "" {
				target = rec.Target
			}
		}
		out, err := rt.previewSubscription(raw, operators, target)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		body, err := json.Marshal(out)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "render":
		var req struct {
			SubscriptionID string `json:"subscription_id"`
			Format         string `json:"format"`
			UAClass        string `json:"ua_class"`
			Raw            string `json:"raw"`
		}
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid render payload: %w", err))
			}
		}
		out, err := rt.renderSubscription(req.SubscriptionID, req.Format, req.UAClass, req.Raw)
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
