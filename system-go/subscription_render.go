package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// renderResult is what the core receives. It carries bytes and a content type and
// nothing else: the plugin does not choose a status code, set a header, or see
// the share's token.
type renderResult struct {
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
	// DroppedNodeCount and DroppedProtocols name what this client could not
	// carry. Only filled when the caller asked to explain, which the console
	// does and the serve path does not: a client that receives an empty
	// document deserves a reason, and finding one costs extra produce calls.
	NodeCount        int      `json:"node_count,omitempty"`
	DroppedNodeCount int      `json:"dropped_node_count,omitempty"`
	DroppedProtocols []string `json:"dropped_protocols,omitempty"`
	// Headers a script file asked for through `$options._res.headers`. The core
	// decides which of them it is willing to send; the plugin only reports what
	// the document said it wanted.
	Headers map[string]string `json:"headers,omitempty"`
}

// subscriptionProbeResult is the browser-safe refresh view. Provider bytes,
// manifests, URIs, and credentials remain confined to the internal fetch
// method used by the server's subscription:serve path.
type subscriptionProbeResult struct {
	SubscriptionID string `json:"subscription_id"`
	Bytes          int    `json:"bytes"`
	SourceVersion  string `json:"source_version,omitempty"`
	Stale          bool   `json:"stale"`
	OK             bool   `json:"ok"`
	ErrorCode      string `json:"error_code,omitempty"`
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

// storedPreviewOperators validates the whole stored chain before a graph host
// call, then returns only the enabled node-stage operators a preview can run.
// Response transformers remain part of the canonical process, but they operate
// on a rendered document rather than the node list a preview summarizes.
func storedPreviewOperators(rec subscriptionRecord) ([]json.RawMessage, error) {
	steps := processSteps(rec)
	if err := validateProcess(steps); err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(steps))
	for _, raw := range steps {
		meta, _ := decodeStep(raw)
		if meta.Disabled || responseOperators[meta.Type] {
			continue
		}
		out = append(out, raw)
	}
	return out, nil
}

// subscriptionRenderRequest carries one render's full contract. Target and
// Options are the Sub-Store URL-parity additions: ?target= names the client
// explicitly and produce flags ride under upstream's own names.
type subscriptionRenderRequest struct {
	SubscriptionID string
	Format         string
	UAClass        string
	Target         string
	Options        map[string]bool
	Raw            string
	Query          map[string]string
	// Explain asks for the diagnosis alongside the document.
	Explain bool
}

// resolveRenderTarget picks the client for one render. Priority is explicit
// caller target, then the record's pin, then the UA classification, then the
// universally accepted URI list — an operator or URL that names a client is
// never overridden by a header.
func resolveRenderTarget(rec subscriptionRecord, explicit, uaClass string) string {
	if t := strings.TrimSpace(explicit); t != "" {
		return t
	}
	return subscriptionTarget(rec, uaClass)
}

// renderSubscription produces the body the core will serve.
//
// It refuses to return empty content. The core refuses an empty body too, and
// both refusals exist deliberately: a proxy client that receives an empty but
// successful subscription deletes every node it had, so the failure is worth
// stopping twice rather than relying on either layer alone.
func (rt *runtime) renderSubscription(req subscriptionRenderRequest) (renderResult, error) {
	subscriptionID, format, uaClass, raw, query := req.SubscriptionID, req.Format, req.UAClass, req.Raw, req.Query
	rec, err := rt.getSubscription(subscriptionID)
	if err != nil {
		return renderResult{}, err
	}

	// A file is served as the document it is: no base64 envelope, no client
	// target. The core's format only decides how a NODE LIST is carried, and a
	// configuration is not a node list.
	if recordKind(rec) == kindFile {
		output, headers, err := rt.renderFile(rec, uaClass, query, raw)
		if err != nil {
			return renderResult{}, err
		}
		contentType := fileContentType(rec)
		// A program says what it produced. Its own content-type wins over the
		// type guessed from the file kind, which is the whole reason it can set
		// headers at all.
		for key, value := range headers {
			if strings.EqualFold(key, "content-type") && strings.TrimSpace(value) != "" {
				contentType = value
			}
		}
		// A file marked for download is meant to arrive as a file rather than be
		// rendered in a browser tab. The record has carried this flag since files
		// existed and nothing ever read it; it takes effect once the core applies
		// the headers a render returns (TASK-0025), and until then it is stored
		// and reported rather than silently dropped.
		if rec.Download {
			if headers == nil {
				headers = map[string]string{}
			}
			if _, set := headers["content-disposition"]; !set {
				headers["content-disposition"] = `attachment; filename="` + downloadFilename(rec) + `"`
			}
		}
		return renderResult{Content: output, ContentType: contentType, Headers: headers}, nil
	}

	// A collection has no content of its own — it is defined entirely by the
	// subs it gathers, so the core's snapshot is not an input here.
	if recordKind(rec) == kindCollection {
		output, err := rt.renderCollection(rec, resolveRenderTarget(rec, req.Target, uaClass), req.Options, raw)
		if err != nil {
			return renderResult{}, err
		}
		body, contentType, err := encodeSubscriptionOutput(output, format)
		if err != nil {
			return renderResult{}, err
		}
		body, headers, err := rt.applyResponseChain(rec, body, contentType)
		if err != nil {
			return renderResult{}, err
		}
		return renderResult{Content: body, ContentType: contentType, Headers: headers}, nil
	}

	// The core hands back the snapshot it holds for this subscription. Inline
	// content is the fallback for a record that has no remote source at all.
	source := raw
	if strings.TrimSpace(source) == "" {
		source = rec.Content
	}
	// A vpn-core record carries no inline content, so on the very first request
	// — before the core holds a snapshot — there is nothing to fall back to.
	// Reading the export here means such a subscription serves correctly the
	// first time it is fetched rather than failing until something warms it.
	if strings.TrimSpace(source) == "" && isVPNCoreSource(rec.Source) {
		// fetchRecordContent dispatches on the source: the plain vpn-core
		// export over rpc:call, or the composed graph subscription. Both are
		// resolved from the record itself so an unsaved draft is honest too.
		fetched, err := rt.fetchRecordContent(rec)
		if err != nil {
			return renderResult{}, err
		}
		source = fetched.Raw
	}
	if strings.TrimSpace(source) == "" {
		// Saying so beats serving an empty subscription: a client that receives
		// an empty success deletes every node it had.
		return renderResult{}, fmt.Errorf("subscription %q has no content to render", subscriptionID)
	}

	operators, err := enabledOperators(rec)
	if err != nil {
		return renderResult{}, fmt.Errorf("subscription %q: %w", subscriptionID, err)
	}
	converted, err := rt.subStoreEngine().convert(subStoreConversionRequest{
		Raw:       source,
		Target:    resolveRenderTarget(rec, req.Target, uaClass),
		Operators: operators,
		Options:   req.Options,
		Explain:   req.Explain,
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
	body, headers, err := rt.applyResponseChain(rec, body, contentType)
	if err != nil {
		return renderResult{}, err
	}
	return renderResult{
		Content:     body,
		ContentType: contentType,
		Headers:     headers,

		NodeCount:        explainedNodeCount(req.Explain, converted.NodeCount),
		DroppedNodeCount: converted.UnsupportedNodeCount,
		DroppedProtocols: converted.UnsupportedProtocols,
	}, nil
}

// explainedNodeCount reports the chain's node count only to a caller that asked
// for an explanation. The serve response stays byte for byte what it was.
func explainedNodeCount(explain bool, count int) int {
	if !explain {
		return 0
	}
	return count
}

// applyResponseChain runs the record's chain a second time, over what it is
// about to serve.
//
// One chain, two stages — that is how the engine is built. `process` walks the
// chain over the nodes and skips response transformers outright; `processResponse`
// walks the same chain over the finished body and runs only those. Treating the
// two vocabularies as alternatives, which this plugin did, meant a subscription
// could not rewrite its own output at all even though the operator had a step
// saying it should.
//
// A chain with no response transformer costs nothing: the engine is not started.
func (rt *runtime) applyResponseChain(rec subscriptionRecord, body, contentType string) (string, map[string]string, error) {
	operators, err := enabledOperators(rec)
	if err != nil {
		return "", nil, fmt.Errorf("subscription %q: %w", rec.ID, err)
	}
	staged := make([]json.RawMessage, 0, len(operators))
	for _, raw := range operators {
		if meta, err := decodeStep(raw); err == nil && responseOperators[meta.Type] {
			staged = append(staged, raw)
		}
	}
	if len(staged) == 0 {
		return body, nil, nil
	}
	out, err := rt.subStoreEngine().transformResponse(subStoreResponseTransformRequest{
		Response: mustJSON(map[string]any{
			"status":  200,
			"headers": map[string]any{"content-type": contentType},
			"body":    body,
		}),
		Operators: staged,
	})
	if err != nil {
		return "", nil, fmt.Errorf("subscription %q: %w", rec.ID, err)
	}
	if strings.TrimSpace(out.Body) == "" {
		// The same rule the empty render has: a client that receives an empty
		// success deletes every node it had.
		return "", nil, fmt.Errorf("subscription %q: its response chain produced nothing", rec.ID)
	}
	// The engine's header map is untyped; the wire carries strings. A header
	// whose value is not one is dropped rather than rendered as Go's %v.
	headers := map[string]string{}
	for key, value := range out.Headers {
		if text, ok := value.(string); ok {
			headers[key] = text
		}
	}
	return out.Body, headers, nil
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
		fetchedAt := time.Now().UTC()
		out, err := rt.fetchSubscription(req.SubscriptionID)
		// Bookkeeping whether the fetch worked or not: this method is the
		// refresh path — the core calls it on a schedule and the UI on a click —
		// so it is the one place that knows when the served snapshot last moved.
		rt.noteFetchOutcome(req.SubscriptionID, fetchedAt, out.Userinfo, err)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		// `raw` is what the core stores as its snapshot and must stay first
		// class; the rest is the management view's answer to "when did this last
		// move, and what did the provider say about quota".
		payload := map[string]any{
			"raw":             out.Raw,
			"userinfo":        out.Userinfo,
			"subscription_id": req.SubscriptionID,
			"bytes":           len(out.Raw),
			"fetched_at":      fetchedAt.Format(time.RFC3339),
		}
		// Graph authority travels with the fetch: the core stores the exact
		// composed source version and manifest alongside the snapshot, so a
		// share can say which composition it serves.
		if out.SourceVersion != "" {
			payload["source_version"] = out.SourceVersion
		}
		if len(out.SourceManifest) > 0 {
			payload["source_manifest"] = json.RawMessage(out.SourceManifest)
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "probe":
		var req struct {
			SubscriptionID string `json:"subscription_id"`
		}
		if err := decodeStrictVPNCoreGraphJSON(call.Payload, &req); err != nil || strings.TrimSpace(req.SubscriptionID) == "" {
			return latticeplugin.ErrorResponse(errors.New("invalid probe payload"))
		}
		out, err := rt.fetchSubscription(req.SubscriptionID)
		result := subscriptionProbeResult{SubscriptionID: req.SubscriptionID, Stale: false, OK: err == nil}
		if err != nil {
			result.ErrorCode = "source_unavailable"
		} else {
			result.Bytes = len(out.Raw)
			result.SourceVersion = out.SourceVersion
		}
		return latticeplugin.RawResultResponse(mustJSON(result), "")
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
			ID          string   `json:"id"`
			Kind        string   `json:"kind"`
			Name        string   `json:"name"`
			DisplayName string   `json:"display_name,omitempty"`
			Remark      string   `json:"remark,omitempty"`
			Tags        []string `json:"tags,omitempty"`
			Source      string   `json:"source,omitempty"`
			HasURL      bool     `json:"has_url"`
			HasInline   bool     `json:"has_inline_content"`
			Members     []string `json:"members,omitempty"`
			MemberTags  []string `json:"member_tags,omitempty"`
			Target      string   `json:"target,omitempty"`
			FileType    string   `json:"file_type,omitempty"`
			NodeSource  string   `json:"node_source,omitempty"`
			Steps       int      `json:"step_count"`
			StepsOff    int      `json:"disabled_step_count"`
			Imported    bool     `json:"imported"`
			// Fetch bookkeeping, emitted only once the record has been fetched at
			// all: before that there is no status to report, and emitting zero
			// values would read as "refresh failed" rather than "never fetched".
			// LastFetchOK is a pointer so a real false survives encoding.
			LastFetchAt string `json:"last_fetch_at,omitempty"`
			LastFetchOK *bool  `json:"last_fetch_ok,omitempty"`
			LastError   string `json:"last_error,omitempty"`
			Userinfo    string `json:"userinfo,omitempty"`
		}
		views := make([]view, 0, len(records))
		for _, rec := range records {
			steps := processSteps(rec)
			off := 0
			for _, raw := range steps {
				if meta, err := decodeStep(raw); err == nil && meta.Disabled {
					off++
				}
			}
			entry := view{
				ID: rec.ID, Kind: recordKind(rec), Name: rec.Name,
				DisplayName: rec.DisplayName, Remark: rec.Remark, Tags: rec.Tags,
				Source: rec.Source,
				HasURL: strings.TrimSpace(rec.URL) != "", HasInline: strings.TrimSpace(rec.Content) != "",
				Members: rec.Members, MemberTags: rec.MemberTags,
				Target: rec.Target, FileType: rec.FileType, NodeSource: rec.NodeSource,
				Steps: len(steps), StepsOff: off, Imported: rec.Origin != nil,
			}
			if rec.LastFetchAt != "" {
				ok := rec.LastFetchOK
				entry.LastFetchAt, entry.LastFetchOK = rec.LastFetchAt, &ok
				entry.LastError, entry.Userinfo = rec.LastError, rec.Userinfo
			}
			views = append(views, entry)
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
		// A graph subscription's selection is validated against what vpn-core
		// actually offers before anything is written: a selection that names a
		// line or chain that does not exist must fail the save, not the render.
		if rec.Source == subscriptionSourceVPNCoreGraph {
			options, err := rt.fetchVPNCoreGraphOptions()
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			if err := validateVPNCoreGraphSelection(rec, options); err != nil {
				return latticeplugin.ErrorResponse(err)
			}
		}
		// The document is loaded once and serves both reads this dispatch used
		// to make: provenance and fetch bookkeeping are preserved from the
		// in-memory copy, and the response is built from what was written. The
		// old shape — get, save, get again — billed up to seven host round trips
		// for one save against a budget of three, so every UI save 502'd once
		// the store held a script file.
		doc, err := rt.loadSubscriptionRecords()
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		wasScript := false
		found := false
		for _, existing := range doc.Records {
			if existing.ID != rec.ID {
				continue
			}
			// Origin records where a record came from during migration. A caller
			// must not be able to forge it, so it is preserved from the stored
			// record rather than taken from the request.
			rec.Origin = existing.Origin
			// Fetch bookkeeping belongs to the record's life, not to this edit:
			// a save that zeroed it would tell the operator a fetched record was
			// never refreshed.
			rec.LastFetchAt, rec.LastFetchOK = existing.LastFetchAt, existing.LastFetchOK
			rec.LastError, rec.Userinfo = existing.LastError, existing.Userinfo
			wasScript = isScriptFile(existing)
			found = true
			break
		}
		if !found {
			rec.Origin = nil
			rec.LastFetchAt, rec.LastFetchOK = "", false
			rec.LastError, rec.Userinfo = "", ""
		}
		saved, err := rt.saveSubscriptionInDoc(&doc, rec, wasScript)
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
		// Both vocabularies, each flagged. A file editing a document needs the
		// response steps; a subscription's chain needs the proxy operators.
		catalog := append(operatorCatalogInfo(), responseOperatorInfo()...)
		body, err := json.Marshal(map[string]any{"operators": catalog})
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	case "graph_options":
		if len(bytes.TrimSpace(call.Payload)) == 0 {
			return latticeplugin.ErrorResponse(errors.New("graph_options requires an empty object"))
		}
		var request map[string]json.RawMessage
		if err := decodeStrictVPNCoreGraphJSON(call.Payload, &request); err != nil || request == nil || len(request) != 0 {
			return latticeplugin.ErrorResponse(errors.New("invalid graph_options payload"))
		}
		options, err := rt.fetchVPNCoreGraphOptions()
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		body, err := json.Marshal(options)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(body, "")
	// preview renders what is already saved. preview_draft additionally resolves
	// an UNSAVED draft's source live, from fields the caller supplies.
	//
	// They are two operations, not one with a flag, because the difference is
	// exactly a scope: preview reads a record an admin already configured, while
	// preview_draft lets the caller name the host the control plane will go and
	// talk to. That second thing is what `fetch` is declared substore:admin to
	// do, and it cannot be gated inside a single method because this process
	// never learns who is calling. Splitting them puts the bound in the manifest,
	// which is the only place it is enforced.
	case "preview", "preview_draft":
		var req struct {
			SubscriptionID string            `json:"subscription_id"`
			Raw            string            `json:"raw"`
			Target         string            `json:"target"`
			Operators      []json.RawMessage `json:"operators"`
			GraphSelection json.RawMessage   `json:"graph_selection"`
			// Source fields let an UNSAVED draft say where its nodes come from;
			// without them a fleet- or provider-sourced draft previewed as
			// "no content" while the nodes were right there. preview_draft only.
			Source      string `json:"source,omitempty"`
			URL         string `json:"url,omitempty"`
			UA          string `json:"ua,omitempty"`
			VPNIdentity string `json:"vpn_identity,omitempty"`
		}
		if len(call.Payload) > 0 {
			if len(call.Payload) > model.MaxSubscriptionResponseBytes {
				return latticeplugin.ErrorResponse(errors.New("preview payload exceeds bounds"))
			}
			if err := decodeStrictVPNCoreGraphJSON(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid preview payload: %w", err))
			}
		}
		// Refused rather than ignored. Silently dropping the fields would let a
		// caller believe they had previewed their draft's real source while they
		// were looking at nothing, and a preview that lies about where its nodes
		// came from is worse than one that says it cannot.
		if call.Method == "preview" {
			for name, value := range map[string]string{
				"source": req.Source, "url": req.URL, "ua": req.UA, "vpn_identity": req.VPNIdentity,
			} {
				if strings.TrimSpace(value) != "" {
					return latticeplugin.ErrorResponse(fmt.Errorf(
						"preview does not resolve an unsaved draft: %q names a source the caller chose; use preview_draft, which is declared substore:admin", name))
				}
			}
		}
		// Set where the graph redactor runs: that path substitutes synthetic
		// credentials into the content itself, so the preview does not reduce
		// the node a second time and can let a script read the stand-in.
		credentialsAlreadySynthetic := false
		var graphSelection *vpnCoreGraphSelection
		if len(req.GraphSelection) > 0 {
			if bytes.Equal(bytes.TrimSpace(req.GraphSelection), []byte("null")) {
				return latticeplugin.ErrorResponse(errors.New("graph_selection must be an object"))
			}
			var selection vpnCoreGraphSelection
			if err := decodeStrictVPNCoreGraphJSON(req.GraphSelection, &selection); err != nil {
				return latticeplugin.ErrorResponse(errors.New("invalid graph_selection payload"))
			}
			graphSelection = &selection
		}
		raw := req.Raw
		operators := req.Operators
		target := req.Target
		previewSourceVersion := ""
		var selectedRecord *subscriptionRecord
		if graphSelection != nil {
			if strings.TrimSpace(req.Raw) != "" {
				return latticeplugin.ErrorResponse(errors.New("graph preview does not accept caller raw content"))
			}
			if strings.TrimSpace(req.SubscriptionID) != "" {
				rec, err := rt.getSubscription(req.SubscriptionID)
				if err != nil {
					return latticeplugin.ErrorResponse(err)
				}
				if rec.Source != subscriptionSourceVPNCoreGraph {
					return latticeplugin.ErrorResponse(errors.New("graph selection is only valid for vpn-core-graph records"))
				}
				selectedRecord = &rec
				if operators == nil {
					storedOperators, err := storedPreviewOperators(rec)
					if err != nil {
						return latticeplugin.ErrorResponse(err)
					}
					operators = storedOperators
				}
				if strings.TrimSpace(target) == "" {
					target = rec.Target
				}
			}
			composed, err := rt.previewVPNCoreGraph(*graphSelection)
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			raw, err = redactVPNCoreGraphPreviewEntries(composed.Entries, graphSelection.EntryRoots)
			credentialsAlreadySynthetic = true
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			previewSourceVersion = composed.SourceVersion
		}
		if strings.TrimSpace(req.SubscriptionID) != "" {
			var rec subscriptionRecord
			if selectedRecord != nil {
				rec = *selectedRecord
			} else {
				var err error
				rec, err = rt.getSubscription(req.SubscriptionID)
				if err != nil {
					return latticeplugin.ErrorResponse(err)
				}
			}
			// A file's content is a document, not a node list. Parsing it as
			// nodes would show the example proxies its template ships with and
			// call them the result, which is worse than showing nothing.
			if recordKind(rec) == kindFile {
				return previewFileResponse(rt, rec)
			}
			// A combination has no content of its own either: its nodes are its
			// members', merged with each member's chain and then its own already
			// run. Previewing that merged list is what the row's eye means on a
			// combination — asking previewSubscription to fetch it again would
			// report "no content" for a combination that serves fifty nodes.
			if recordKind(rec) == kindCollection {
				// The merged list is parsed back below, so it renders in URI —
				// the one target that round-trips through a parse — whatever the
				// record's serving target is.
				previewRec := rec
				previewRec.Target = ""
				merged, err := rt.renderCollection(previewRec, "URI", nil, "")
				if err != nil {
					return latticeplugin.ErrorResponse(err)
				}
				out, err := rt.previewSubscription(merged, nil, "URI", false)
				if err != nil {
					return latticeplugin.ErrorResponse(err)
				}
				body, err := json.Marshal(out)
				if err != nil {
					return latticeplugin.ErrorResponse(err)
				}
				return latticeplugin.RawResultResponse(body, "")
			}
			if operators == nil {
				storedOperators, err := storedPreviewOperators(rec)
				if err != nil {
					return latticeplugin.ErrorResponse(err)
				}
				operators = storedOperators
			}
			if strings.TrimSpace(target) == "" {
				target = rec.Target
			}
			if rec.Source == subscriptionSourceVPNCore {
				if err := validateOperators(operators); err != nil {
					return latticeplugin.ErrorResponse(errors.New("legacy vpn-core preview operators are invalid"))
				}
				if containsScriptingOperator(operators) {
					return latticeplugin.ErrorResponse(errors.New("legacy vpn-core preview does not allow scripting operators"))
				}
			}
			if strings.TrimSpace(raw) == "" {
				raw = rec.Content
				// Same reason as render: a vpn-core or provider record has no
				// inline content, and a preview that showed nothing would look
				// like a broken subscription rather than one sourced from
				// somewhere else. The fetch here is a read for the preview only —
				// it is not recorded as a refresh, because the served snapshot
				// does not move.
				if strings.TrimSpace(raw) == "" &&
					(rec.Source == subscriptionSourceVPNCore || strings.TrimSpace(rec.URL) != "") {
					fetched, err := rt.fetchRecordContent(rec)
					if err != nil {
						return latticeplugin.ErrorResponse(err)
					}
					raw = fetched.Raw
				}
			}
			// A stored graph source is authoritative. Caller-supplied preview bytes
			// must never replace the exact composition bound to its identity and
			// ordered roots.
			if rec.Source == subscriptionSourceVPNCoreGraph && graphSelection == nil {
				composed, err := rt.fetchVPNCoreGraph(rec)
				if err != nil {
					return latticeplugin.ErrorResponse(err)
				}
				raw, err = redactVPNCoreGraphPreviewEntries(composed.Entries, rec.EntryRoots)
				credentialsAlreadySynthetic = true
				if err != nil {
					return latticeplugin.ErrorResponse(err)
				}
				previewSourceVersion = composed.SourceVersion
			}
		} else if strings.TrimSpace(raw) == "" && req.Source != "" && req.Source != subscriptionSourceLocal {
			// An unsaved draft carries its source but no content. Resolve the
			// source live — the same guarded path a saved record's refresh
			// takes — so the preview shows the nodes the draft would produce.
			// A preview fetch is a read, not a refresh: nothing is persisted.
			fetched, err := rt.fetchRecordContent(subscriptionRecord{
				Source:      req.Source,
				URL:         req.URL,
				UA:          req.UA,
				VPNIdentity: req.VPNIdentity,
			})
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			raw = fetched.Raw
		}
		out, err := rt.previewSubscription(raw, operators, target, credentialsAlreadySynthetic)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		out.SourceVersion = previewSourceVersion
		out.Stale = false
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
			// Target names the client explicitly — the parity contract with
			// Sub-Store, whose subscription URLs carry ?target=. An explicit
			// target outranks both the record's pin and the UA class: the
			// caller who spells out a client means that client.
			Target string `json:"target"`
			// Options are produce() flags under Sub-Store's own names
			// (include-unsupported-proxy, ...). Booleans only.
			Options map[string]bool `json:"options"`
			Raw     string          `json:"raw"`
			// Only the parameters the core decided to forward reach here, and the
			// record narrows them again to the names it declared.
			Query map[string]string `json:"query"`
			// Explain asks the render to also report what the chosen client
			// dropped. The console sets it; the path that serves a client does
			// not, so serving pays nothing for a diagnosis nobody reads.
			Explain bool `json:"explain"`
		}
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid render payload: %w", err))
			}
		}
		out, err := rt.renderSubscription(subscriptionRenderRequest{
			SubscriptionID: req.SubscriptionID,
			Format:         req.Format,
			UAClass:        req.UAClass,
			Target:         req.Target,
			Options:        req.Options,
			Raw:            req.Raw,
			Query:          req.Query,
			Explain:        req.Explain,
		})
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
