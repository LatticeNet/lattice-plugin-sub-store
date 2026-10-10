package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	// Target is the client this document was produced for, after the explicit
	// target, a format that names a client, the record's pin and the UA class
	// were weighed. The core can label the response from it instead of
	// guessing from the request. Empty for a file, which has no target.
	Target string `json:"target,omitempty"`
	// ZeroNodes is reported only to a caller that asked to explain: the
	// document carries no node for this client, and the serve path refuses it
	// with zeroNodesForTargetCode. The console still receives the document so
	// it can say why.
	ZeroNodes bool `json:"zero_nodes,omitempty"`
}

// Stable codes the core can match in an error text to pick an audit reason.
// The plugin's error channel is a string, so the code leads the message.
const (
	// zeroNodesForTargetCode: the rendered document carries no node for the
	// client that would receive it.
	zeroNodesForTargetCode = "zero_nodes_for_target"
	// providerNoNodesCode: a refresh read a source that yielded no nodes (an
	// error page, a login wall, a document that is not a subscription). The
	// fetch fails, so the core keeps its last good snapshot.
	providerNoNodesCode = "provider_no_nodes"
	// memberChainDropsNodesCode: a combination member's own steps kept nodes
	// that cannot be handed on as URI links, which is how a member's processed
	// nodes reach the combination. The member fails, and the combination's
	// failure mode decides what happens next.
	memberChainDropsNodesCode = "member_chain_drops_nodes"
)

func zeroNodesForTargetError(label, target string) error {
	return fmt.Errorf("%s: %s has no node the %s client can carry; refusing to serve a document that would make the client delete its nodes", zeroNodesForTargetCode, label, target)
}

func providerNoNodesError(label string) error {
	return fmt.Errorf("%s: %s yielded no nodes; it is not treated as a subscription, so the last good snapshot stays", providerNoNodesCode, label)
}

func memberChainDropsNodesError(label string, lost, kept int, protocols []string) error {
	return fmt.Errorf("%s: %s keeps %d nodes after its own steps, and %d of them (%s) cannot be handed on as URI links; move those steps to the combination or remove them", memberChainDropsNodesCode, label, kept, lost, strings.Join(protocols, ", "))
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
//
// clashmeta is every client built on mihomo (Clash Verge Rev, FlClash, mihomo
// itself, anything announcing clash.meta or meta). The core matches it before
// plain clash, following upstream Sub-Store's user-agent table. It must map to
// ClashMeta: legacy Clash carries neither VLESS nor Hysteria2, which is this
// fleet, so those clients received "proxies:\n" and deleted their nodes.
var uaClassTargets = map[string]string{
	"surge":        "Surge",
	"loon":         "Loon",
	"quantumultx":  "QX",
	"stash":        "Stash",
	"shadowrocket": "Shadowrocket",
	"clashmeta":    "ClashMeta",
	"clash":        "Clash",
	"singbox":      "sing-box",
	"egern":        "Egern",
}

// formatTargets are the core's formats that name a client rather than an
// envelope. lattice-server accepts ?format=clash, clash-meta and sing-box on
// every share (normalizeProxySubscriptionFormat); before this, a plugin share
// refused clash and clash-meta outright and answered the 404 decoy. clash maps
// to ClashMeta as it does for the core's own proxy-user shares, which render
// both through the same Clash Meta writer.
var formatTargets = map[string]string{
	"clash":      "ClashMeta",
	"clash-meta": "ClashMeta",
	"clashmeta":  "ClashMeta",
	"clash.meta": "ClashMeta",
	"sing-box":   "sing-box",
	"singbox":    "sing-box",
}

// formatTarget is the client a format names, or "" for an envelope format.
func formatTarget(format string) string {
	return formatTargets[strings.ToLower(strings.TrimSpace(format))]
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
	// UATarget is the client target the core derived from the agent when the
	// URL named none (lattice-server share_render_plan.go). It ranks below
	// the record's pin and above this plugin's own ua_class table: the core
	// sends ua_class "clash" to a mihomo client for the sake of plugins that
	// predate the clashmeta class, and only ua_target says ClashMeta.
	UATarget string
	Target   string
	Options  map[string]bool
	Raw      string
	Query    map[string]string
	// Explain asks for the diagnosis alongside the document.
	Explain bool
}

// resolveRenderTarget picks the client for one render. Priority is explicit
// caller target, then the record's pin, then the operator's default target
// from Settings (the pin of every record that names none), then the core's
// target for the agent, then the UA classification, then the universally
// accepted URI list. An operator or URL that names a client is never
// overridden by a header.
func resolveRenderTarget(rec subscriptionRecord, explicit, defaultTarget, uaTarget, uaClass string) string {
	if t := strings.TrimSpace(explicit); t != "" {
		return t
	}
	if t := strings.TrimSpace(rec.Target); t != "" {
		return t
	}
	if t := strings.TrimSpace(defaultTarget); t != "" {
		return t
	}
	if t := strings.TrimSpace(uaTarget); t != "" {
		return t
	}
	return subscriptionTarget(rec, uaClass)
}

// renderTarget is resolveRenderTarget for one render request, reading the
// default target from Settings only when neither the request nor the record
// names a client, so a pinned record or an explicit URL costs no host call.
func (rt *runtime) renderTarget(rec subscriptionRecord, req subscriptionRenderRequest) (string, error) {
	explicit, defaultTarget := requestTarget(req), ""
	if strings.TrimSpace(explicit) == "" && strings.TrimSpace(rec.Target) == "" {
		settings, err := rt.invocationSettings()
		if err != nil {
			return "", err
		}
		defaultTarget = settings.DefaultTarget
	}
	return resolveRenderTarget(rec, explicit, defaultTarget, req.UATarget, req.UAClass), nil
}

// requestTarget is the client a render request names explicitly: ?target=
// first, then a format that names a client. Both come from the URL, so both
// outrank the record's pin and the UA class.
func requestTarget(req subscriptionRenderRequest) string {
	if t := strings.TrimSpace(req.Target); t != "" {
		return t
	}
	return formatTarget(req.Format)
}

func quoteLabel(id string) string { return fmt.Sprintf("%q", id) }

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

// encodeSubscriptionOutput carries the engine's output to the client that will
// read it. Target decides what the document says; the format only decides
// whether a URI list travels inside the classic base64 envelope.
//
// Every client gets its own native document. The base64 envelope belongs to
// exactly one target, the URI list, because that is the only document whose
// importers expect one; YAML, JSON and Surge-style configurations are returned
// as the producer wrote them, and V2Ray's producer already emits its base64
// list. "base64" and an empty format both mean this client-native default (the
// core sends "base64" for a share left at automatic); "plain" asks for the bare
// list even for URI. The formats that name a client (clash, clash-meta,
// sing-box) were turned into a target before this point and are carried
// natively. Wrapping every target, as this did before, served YAML and JSON as
// base64 text no client could read.
func encodeSubscriptionOutput(output, format, target string) (string, string, error) {
	contentType := targetContentType(target)
	switch normalized := strings.ToLower(strings.TrimSpace(format)); {
	case normalized == "" || normalized == "base64":
		if target == "URI" {
			return base64.StdEncoding.EncodeToString([]byte(output)), contentType, nil
		}
		return output, contentType, nil
	case normalized == "plain" || formatTarget(normalized) != "":
		return output, contentType, nil
	default:
		return "", "", fmt.Errorf("unsupported subscription format %q", format)
	}
}

// targetContentType names what a target's document is. It mirrors the core's
// own table (subscriptionResponseContentType in lattice-server), so the type the
// plugin reports and the type the client receives never disagree.
func targetContentType(target string) string {
	switch target {
	case "sing-box", "JSON":
		return "application/json; charset=utf-8"
	case "Clash", "ClashMeta", "Stash":
		return "text/yaml; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}

// handleSubscriptionCall routes one call on the subscription service. It is a
// dispatcher and nothing else: every case calls one function, so the method
// bodies live with the lane that owns them (plan section 1.1) and a new
// method is one line here plus its handler.
func (rt *runtime) handleSubscriptionCall(call callPayload) response {
	// The legacy document is cached for one call. Production builds a runtime
	// per invocation anyway; a test that drives several calls through one
	// runtime must not read one call's cache in the next.
	rt.legacy = legacyCache{}
	rt.settings = settingsCache{}
	switch call.Method {
	case "fetch":
		return rt.fetchCall(call.Payload)
	case "probe":
		return rt.probeCall(call.Payload)
	case "publish":
		return rt.publishCall(call.Payload)
	case "export":
		return rt.exportCall()
	case "import":
		return rt.importCall(call.Payload)
	case "get_settings":
		return rt.getSettingsCall()
	case "save_settings":
		return rt.saveSettingsCall(call.Payload)
	case "migrate":
		return rt.migrateCall(call.Payload)
	case "list":
		return rt.listCall()
	case "get":
		return rt.getCall(call.Payload)
	case "save":
		return rt.saveCall(call.Payload)
	case "delete":
		return rt.deleteCall(call.Payload)
	case "restore", "purge":
		return rt.restoreOrPurgeCall(call.Method, call.Payload)
	case "reorder":
		return rt.reorderCall(call.Payload)
	case "migrate_store":
		return rt.migrateStoreCall(call.Payload)
	case "depends_on":
		return rt.dependsOnCall(call.Payload)
	case "apply_revision":
		return rt.applyRevisionCall(call.Payload)
	case "operators":
		return rt.operatorsCall()
	case "graph_options":
		return rt.graphOptionsCall(call.Payload)
	case "preview", "preview_draft":
		return rt.previewCall(call.Method, call.Payload)
	case "convert":
		return rt.convertCall(call.Payload)
	case "render":
		return rt.renderCall(call.Payload)
	default:
		return latticeplugin.ErrorResponse(fmt.Errorf("unsupported method %q", call.Method))
	}
}

// fetchCall is the refresh path: core calls it on a schedule and when a share
// needs a snapshot. Its reply's raw is what core stores as the snapshot.
func (rt *runtime) fetchCall(payload json.RawMessage) response {
	var req struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return latticeplugin.ErrorResponse(fmt.Errorf("invalid fetch payload: %w", err))
		}
	}
	fetchedAt := time.Now().UTC()
	out, err := rt.fetchSubscription(req.SubscriptionID)
	// Bookkeeping whether the fetch worked or not: this method is the
	// refresh path — the core calls it on a schedule and the UI on a click —
	// so it is the one place that knows when the served snapshot last moved.
	rt.noteFetchOutcome(req.SubscriptionID, fetchOutcome{at: fetchedAt, userinfo: out.Userinfo, nodesIn: out.nodesIn, nodesOut: out.nodesOut, err: err})
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	// `raw` is what the core stores as its snapshot and must stay first
	// class; the rest is the management view's answer to "when did this last
	// move, and what did the provider say about quota".
	reply := map[string]any{
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
		reply["source_version"] = out.SourceVersion
	}
	if len(out.SourceManifest) > 0 {
		reply["source_manifest"] = json.RawMessage(out.SourceManifest)
	}
	body, err := json.Marshal(reply)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// probeCall is the console's row check: fetchSubscription without the
// bookkeeping, answering a byte count and never the body.
func (rt *runtime) probeCall(payload json.RawMessage) response {
	var req struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if err := decodeStrictVPNCoreGraphJSON(payload, &req); err != nil || strings.TrimSpace(req.SubscriptionID) == "" {
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
}

// publishCall renders a record and sends it to an operator target.
func (rt *runtime) publishCall(payload json.RawMessage) response {
	body, err := rt.handlePublishCall(payload)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// exportCall answers the backup of everything this plugin owns.
func (rt *runtime) exportCall() response {
	body, err := rt.exportBackup()
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	reply, err := exportReply(body)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(reply, "")
}

// importCall restores records from a backup.
func (rt *runtime) importCall(payload json.RawMessage) response {
	var req struct {
		Backup string `json:"backup"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
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
}

// getSettingsCall answers the settings document.
func (rt *runtime) getSettingsCall() response {
	settings, err := rt.loadSettings()
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(mustJSON(settings), "")
}

// saveSettingsCall writes the settings document and answers it as stored.
func (rt *runtime) saveSettingsCall(payload json.RawMessage) response {
	var req pluginSettings
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
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
}

// migrateCall imports records from a standalone Sub-Store.
func (rt *runtime) migrateCall(payload json.RawMessage) response {
	var req subStoreRequest
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
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
}

// listCall answers the management view of every record.
func (rt *runtime) listCall() response {
	// One key on the split store: the index carries every row and its
	// fetch bookkeeping (store_index.go).
	body, err := rt.listSubscriptionsReply()
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// getCall answers one whole record.
func (rt *runtime) getCall(payload json.RawMessage) response {
	// `list` deliberately omits content and operators so a management view
	// cannot double as a dump of every provider payload. Editing one record
	// still needs them, so `get` returns the whole thing for exactly one id.
	var req struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
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
}

// saveCall creates or edits one record.
func (rt *runtime) saveCall(payload json.RawMessage) response {
	// Until this existed a subscription could only enter the store by
	// migrating from a standalone Sub-Store or restoring a backup: the
	// record type, its validation and its storage were all reachable, but
	// nothing let an operator create or edit one.
	var req struct {
		Subscription subscriptionRecord `json:"subscription"`
		// IfRevision is the revision the caller read before editing. When it
		// is present the save is conditional: the stored record must still
		// carry it, or the write is refused as stale.
		//
		// Optional on purpose. Import, migrate and backup restore write
		// records they never read, and an older UI does not send it; all of
		// them keep working exactly as before. Only the interactive edit
		// path has a "before" to be stale against, and only it sends this.
		IfRevision string `json:"if_revision,omitempty"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
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
	// A caller must not be able to forge provenance: a new record gets
	// none, and an existing one keeps what is stored.
	rec.Origin = nil
	saved, conflict, err := rt.storeSave(rec, req.IfRevision, true)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	if conflict != nil {
		return conflictResponse(rec.ID, conflict.reason, conflict.current, conflict.revision)
	}
	body, err := json.Marshal(map[string]any{"subscription": saved, "saved": true})
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// deleteCall archives one record.
func (rt *runtime) deleteCall(payload json.RawMessage) response {
	var req struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return latticeplugin.ErrorResponse(fmt.Errorf("invalid delete payload: %w", err))
		}
	}
	if strings.TrimSpace(req.SubscriptionID) == "" {
		return latticeplugin.ErrorResponse(fmt.Errorf("subscription_id is required"))
	}
	if err := rt.deleteSubscription(req.SubscriptionID); err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	// Delete archives (store_archive.go): restore brings the record back
	// under the same id until purge. Deleting the definition does not
	// retract anything already published: the share lives in the core,
	// and the UI archives it there through the gateway.
	body, err := json.Marshal(map[string]any{"id": req.SubscriptionID, "deleted": true, "archived": true})
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// restoreOrPurgeCall brings an archived record back, or deletes it for good.
func (rt *runtime) restoreOrPurgeCall(method string, payload json.RawMessage) response {
	var req struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return latticeplugin.ErrorResponse(fmt.Errorf("invalid %s payload: %w", method, err))
		}
	}
	if strings.TrimSpace(req.SubscriptionID) == "" {
		return latticeplugin.ErrorResponse(fmt.Errorf("subscription_id is required"))
	}
	reply := map[string]any{"id": req.SubscriptionID}
	if method == "restore" {
		restored, err := rt.restoreSubscription(req.SubscriptionID)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		reply["restored"], reply["subscription"] = true, restored
	} else {
		if err := rt.purgeSubscription(req.SubscriptionID); err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		reply["purged"] = true
	}
	return latticeplugin.RawResultResponse(mustJSON(reply), "")
}

// reorderCall rewrites the manual order.
func (rt *runtime) reorderCall(payload json.RawMessage) response {
	var req struct {
		IDs []string `json:"ids"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return latticeplugin.ErrorResponse(fmt.Errorf("invalid reorder payload: %w", err))
		}
	}
	if err := rt.reorderSubscriptions(req.IDs); err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(mustJSON(map[string]any{"reordered": true, "count": len(req.IDs)}), "")
}

// migrateStoreCall moves a legacy store onto the split store, chunked.
func (rt *runtime) migrateStoreCall(payload json.RawMessage) response {
	out, err := rt.migrateStore(payload)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(mustJSON(out), "")
}

// dependsOnCall answers which records the fleet feeds.
func (rt *runtime) dependsOnCall(payload json.RawMessage) response {
	body, err := rt.dependsOn(payload)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// operatorsCall answers the operator catalogue.
func (rt *runtime) operatorsCall() response {
	// Both vocabularies, each flagged. A file editing a document needs the
	// response steps; a subscription's chain needs the proxy operators.
	catalog := append(operatorCatalogInfo(), responseOperatorInfo()...)
	body, err := json.Marshal(map[string]any{"operators": catalog})
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// graphOptionsCall relays vpn-core's graph options for the legacy graph editor.
func (rt *runtime) graphOptionsCall(payload json.RawMessage) response {
	if len(bytes.TrimSpace(payload)) == 0 {
		return latticeplugin.ErrorResponse(errors.New("graph_options requires an empty object"))
	}
	var request map[string]json.RawMessage
	if err := decodeStrictVPNCoreGraphJSON(payload, &request); err != nil || request == nil || len(request) != 0 {
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
}

// convertCall is the sealed converter core calls with one identity's plan.
func (rt *runtime) convertCall(payload json.RawMessage) response {
	out, err := rt.convertSubscription(payload)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(mustJSON(out), "")
}

// conflictResponse answers a refused conditional save.
//
// It is a successful call carrying `saved: false`, not a transport error. A
// stale write is a legitimate outcome that the caller has to render and act on,
// with structured data attached (the record as it stands now, and its current
// revision); the error channel carries a string and would force the UI to parse
// prose to find out what happened. `saved` is already the field the UI checks,
// so a caller that does not understand `conflict` still refuses to claim the
// write landed.
func conflictResponse(id, reason string, current subscriptionRecord, revision string) latticeplugin.Response {
	payload := map[string]any{
		"id":     id,
		"reason": reason,
	}
	if revision != "" {
		payload["revision"] = revision
	}
	if current.ID != "" {
		payload["subscription"] = current
	}
	body, err := json.Marshal(map[string]any{"saved": false, "conflict": payload})
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}
