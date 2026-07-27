// Command lattice-plugin-sub-store is the official LatticeNet Sub-Store companion
// system plugin: it imports node connection info from the vpn-core plugin into
// the operator's existing Sub-Store backend (a managed local subscription) and
// reports reachability while preserving all native Sub-Store features.
//
// It implements the Lattice system-plugin stdio contract:
//   - runner -> plugin stdin: {"action":"call","payload":{...}} then EOF
//   - plugin -> runner: {"host_call":{...}} for brokered rpc/http/kv calls
//   - runner -> plugin fd 3: {"host_response":{...}}
//   - plugin -> runner: {"ok":true,"result":...}
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

const (
	pluginID             = "latticenet.sub-store"
	pluginName           = "Sub-Store companion"
	pluginVersion        = "0.4.0-alpha.1"
	defaultSubStoreName  = "lattice-vpn-core"
	pipelineRecordsKey   = "engine-pipelines-v1"
	maxExportLinks       = 10_000
	maxExportBytes       = 1 << 20
	maxLinkBytes         = 4 << 10
	maxErrorExcerpt      = 4 << 10
	maxPipelineRecords   = 256
	maxPipelineOperators = 64
	maxPipelineDocBytes  = 1 << 20
	maxPipelineRawBytes  = 1 << 20
)

// Private/loopback Sub-Store endpoints require the explicit system-only
// operator-target primitive. Ordinary http:egress remains unable to reach them.
// secret:read/secret:write back the opt-in encrypted endpoint vault (design-15
// §7): the server resolves secret:// references from this plugin's own
// namespace, so a saved endpoint never re-crosses the browser.
var capabilities = []string{"rpc:call", "http:operator-target", "secret:read", "secret:write"}

type request = latticeplugin.Request
type callPayload = latticeplugin.CallPayload
type response = latticeplugin.Response

type subStoreRequest struct {
	BaseURL string `json:"base_url"`
	SubName string `json:"sub_name"`
	UserID  string `json:"user_id"`
	// Autosync is only honored by save_endpoint: it stores the design-15 §7
	// server-side auto-sync flag alongside the endpoint in the encrypted vault.
	Autosync *bool `json:"autosync,omitempty"`
}

type endpointSecretDocument struct {
	Version  int    `json:"version"`
	BaseURL  string `json:"base_url"`
	Autosync bool   `json:"autosync"`
}

type autoSyncStatusDocument struct {
	State         string `json:"state"`
	AttemptedAt   string `json:"attempted_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	Error         string `json:"error,omitempty"`
}

type pipelineRecordsDocument struct {
	Version int              `json:"version"`
	Records []pipelineRecord `json:"records"`
}

type pipelineRecord struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Target    string            `json:"target"`
	Operators []json.RawMessage `json:"operators,omitempty"`
}

type pipelineRecordRef struct {
	ID string `json:"id"`
}

type pipelineRunRequest struct {
	ID  string `json:"id"`
	Raw string `json:"raw"`
}

type pipelineRecordListItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Target        string `json:"target"`
	OperatorCount int    `json:"operator_count"`
}

func main() {
	rt := &runtime{}
	_ = latticeplugin.Serve(context.Background(), latticeplugin.HandlerFunc(
		func(ctx context.Context, req latticeplugin.Request, host *latticeplugin.HostClient) latticeplugin.Response {
			rt.host = sdkHostCaller{ctx: ctx, client: host}
			return rt.handle(req)
		},
	))
}

type runtime struct {
	host   hostCaller
	engine *subStoreEngine
}

type hostCaller interface {
	call(method string, params any) (json.RawMessage, error)
}

type sdkHostCaller struct {
	ctx    context.Context
	client *latticeplugin.HostClient
}

func (host sdkHostCaller) call(method string, params any) (json.RawMessage, error) {
	return host.client.Call(host.ctx, method, params)
}

func (rt *runtime) handle(req request) response {
	switch req.Action {
	case latticeplugin.ActionDescribe:
		body, _ := json.Marshal(map[string]any{
			"id":           pluginID,
			"name":         pluginName,
			"version":      pluginVersion,
			"capabilities": capabilities,
			"manages": []string{
				"import vpn-core nodes into a managed Sub-Store subscription",
				"idempotent upsert without replacing the operator's whole subs array",
				"Sub-Store backend reachability checks",
			},
			"calls":  "latticenet.vpn-core/nodes export (inter-plugin RPC)",
			"engine": "embedded Sub-Store ProxyUtils on QuickJS/wazero; remote I/O only through host capabilities",
		})
		return latticeplugin.RawResultResponse(body, "sub-store companion capability surface")
	case latticeplugin.ActionHealth:
		return latticeplugin.MessageResponse("sub-store companion healthy")
	case latticeplugin.ActionPlan:
		return latticeplugin.PlanResponse(renderPlan(req.Payload), "sub-store import dry-run plan")
	case latticeplugin.ActionCall:
		return rt.handleCall(req)
	default:
		return latticeplugin.ErrorResponse(fmt.Errorf("unsupported action %q", req.Action))
	}
}

func (rt *runtime) handleCall(req request) response {
	call, err := req.CallPayload()
	if err != nil {
		return latticeplugin.ErrorResponse(fmt.Errorf("invalid call payload: %w", err))
	}

	switch call.Service {
	case pluginID + "/import":
		return rt.handleImportCall(call)
	case pluginID + "/engine":
		return rt.handleEngineCall(call)
	default:
		return latticeplugin.ErrorResponse(fmt.Errorf("unsupported service %q", call.Service))
	}
}

func (rt *runtime) handleImportCall(call callPayload) response {
	var req subStoreRequest
	if len(call.Payload) > 0 {
		if err := json.Unmarshal(call.Payload, &req); err != nil {
			return latticeplugin.ErrorResponse(fmt.Errorf("invalid sub-store payload: %w", err))
		}
	}
	switch call.Method {
	case "status":
		result := rt.status(req)
		return latticeplugin.RawResultResponse(result, "")
	case "preview":
		result, err := rt.preview(req)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(result, "sub-store import preview")
	case "import":
		result, err := rt.importNodes(req)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(result, "sub-store import complete")
	case "save_endpoint":
		result, err := rt.saveEndpoint(req)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(result, "sub-store endpoint saved (encrypted)")
	case "clear_endpoint":
		result, err := rt.clearEndpoint()
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(result, "sub-store endpoint cleared")
	case "endpoint_status":
		result, err := rt.endpointStatus()
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(result, "")
	default:
		return latticeplugin.ErrorResponse(fmt.Errorf("unsupported method %q", call.Method))
	}
}

func (rt *runtime) handleEngineCall(call callPayload) response {
	switch call.Method {
	case "convert":
		var req subStoreConversionRequest
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid sub-store engine payload: %w", err))
			}
		}
		result, err := rt.subStoreEngine().convert(req)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(mustJSON(result), "sub-store conversion complete")
	case "transform_response":
		var req subStoreResponseTransformRequest
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid sub-store response transform payload: %w", err))
			}
		}
		result, err := rt.subStoreEngine().transformResponse(req)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(mustJSON(result), "sub-store response transform complete")
	case "save_pipeline":
		var req pipelineRecord
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid sub-store pipeline payload: %w", err))
			}
		}
		result, err := rt.savePipelineRecord(req)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(result, "sub-store pipeline saved")
	case "get_pipeline":
		var req pipelineRecordRef
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid sub-store pipeline payload: %w", err))
			}
		}
		result, err := rt.getPipelineRecord(req)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(result, "")
	case "list_pipelines":
		result, err := rt.listPipelineRecords()
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(result, "")
	case "delete_pipeline":
		var req pipelineRecordRef
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid sub-store pipeline payload: %w", err))
			}
		}
		result, err := rt.deletePipelineRecord(req)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(result, "sub-store pipeline deleted")
	case "run_pipeline":
		var req pipelineRunRequest
		if len(call.Payload) > 0 {
			if err := json.Unmarshal(call.Payload, &req); err != nil {
				return latticeplugin.ErrorResponse(fmt.Errorf("invalid sub-store pipeline payload: %w", err))
			}
		}
		result, err := rt.runPipelineRecord(req)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		return latticeplugin.RawResultResponse(result, "sub-store pipeline conversion complete")
	default:
		return latticeplugin.ErrorResponse(fmt.Errorf("unsupported method %q", call.Method))
	}
}

func (rt *runtime) subStoreEngine() subStoreEngine {
	if rt != nil && rt.engine != nil {
		return *rt.engine
	}
	return newEmbeddedSubStoreEngine()
}

func (rt *runtime) status(req subStoreRequest) json.RawMessage {
	base, err := validateBaseURL(req.BaseURL)
	if err != nil {
		return mustJSON(map[string]any{"reachable": false, "sub_name": defaultSubStoreName, "error": err.Error()})
	}
	subName, err := normalizeSubName(req.SubName)
	if err != nil {
		return mustJSON(map[string]any{"reachable": false, "sub_name": defaultSubStoreName, "error": err.Error()})
	}
	status, _, err := rt.httpDo("GET", base+"/api/utils/env", nil)
	reachable := err == nil && status >= 200 && status < 500
	out := map[string]any{"reachable": reachable, "sub_name": subName}
	if err != nil {
		out["error"] = "Sub-Store endpoint is unreachable or denied by host policy"
	}
	return mustJSON(out)
}

// fetchExport pulls the vpn-core node links, enforcing the export bounds before
// any of it reaches the Sub-Store backend or a preview diff.
func (rt *runtime) fetchExport(req subStoreRequest) ([]string, error) {
	rpcReq := map[string]string{}
	if userID := strings.TrimSpace(req.UserID); userID != "" {
		if len(userID) > 128 || hasControl(userID) {
			return nil, fmt.Errorf("user_id must be printable and at most 128 characters")
		}
		rpcReq["user_id"] = userID
	}
	raw, err := rt.callHost(latticeplugin.HostMethodRPCCall, map[string]any{
		"service": "latticenet.vpn-core/nodes",
		"method":  "export",
		"request": rpcReq,
	})
	if err != nil {
		return nil, fmt.Errorf("export vpn-core nodes failed")
	}
	if len(raw) > maxExportBytes {
		return nil, fmt.Errorf("vpn-core export exceeds %d bytes", maxExportBytes)
	}
	var exp struct {
		Links []string `json:"links"`
	}
	if err := json.Unmarshal(raw, &exp); err != nil {
		return nil, fmt.Errorf("decode vpn-core export: %w", err)
	}
	if len(exp.Links) > maxExportLinks {
		return nil, fmt.Errorf("vpn-core export has too many links (max %d)", maxExportLinks)
	}
	totalBytes := 0
	for _, link := range exp.Links {
		if len(link) > maxLinkBytes {
			return nil, fmt.Errorf("vpn-core export link exceeds %d bytes", maxLinkBytes)
		}
		totalBytes += len(link)
		if totalBytes > maxExportBytes {
			return nil, fmt.Errorf("vpn-core export content exceeds %d bytes", maxExportBytes)
		}
	}
	return exp.Links, nil
}

func (rt *runtime) importNodes(req subStoreRequest) (json.RawMessage, error) {
	base, err := validateBaseURL(req.BaseURL)
	if err != nil {
		return nil, err
	}
	subName, err := normalizeSubName(req.SubName)
	if err != nil {
		return nil, err
	}
	links, err := rt.fetchExport(req)
	if err != nil {
		return nil, err
	}

	sub := map[string]any{
		"name":        subName,
		"source":      "local",
		"displayName": "Lattice vpn-core",
		"content":     strings.Join(links, "\n"),
		"tag":         []string{"lattice", "vpn-core"},
	}
	body, _ := json.Marshal(sub)
	status, respBody, err := rt.httpDo("PATCH", base+"/api/sub/"+url.PathEscape(subName), body)
	if err != nil {
		return nil, fmt.Errorf("Sub-Store update failed")
	}
	if status == 404 || status == 405 {
		status, respBody, err = rt.httpDo("POST", base+"/api/subs", body)
		if err != nil {
			return nil, fmt.Errorf("Sub-Store create failed")
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("Sub-Store create returned status %d: %s", status, bodyExcerpt(respBody))
		}
	} else if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Sub-Store update returned status %d: %s", status, bodyExcerpt(respBody))
	}
	return mustJSON(map[string]any{"ok": true, "sub_name": subName, "pushed": len(links)}), nil
}

// previewReports what an import WOULD change without writing anything: it
// pulls the same vpn-core export, reads the remote managed sub, and diffs link
// sets. Labels are best-effort display names (URL fragment, else host); the
// full links never leave this method.
func (rt *runtime) preview(req subStoreRequest) (json.RawMessage, error) {
	base, err := validateBaseURL(req.BaseURL)
	if err != nil {
		return nil, err
	}
	subName, err := normalizeSubName(req.SubName)
	if err != nil {
		return nil, err
	}
	links, err := rt.fetchExport(req)
	if err != nil {
		return nil, err
	}
	current := []string{}
	exists := false
	status, body, err := rt.httpDo("GET", base+"/api/sub/"+url.PathEscape(subName), nil)
	switch {
	case err != nil:
		return nil, fmt.Errorf("Sub-Store read failed")
	case status == 404:
		exists = false
	case status >= 200 && status < 300:
		exists = true
		var sub struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(body, &sub); err != nil {
			return nil, fmt.Errorf("decode remote sub: %w", err)
		}
		for _, line := range strings.Split(sub.Content, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				current = append(current, trimmed)
			}
		}
	default:
		return nil, fmt.Errorf("Sub-Store read returned status %d: %s", status, bodyExcerpt(body))
	}
	added, removed, unchanged := diffLinks(links, current)
	addedLabels := make([]string, 0, len(added))
	for _, link := range added {
		addedLabels = append(addedLabels, linkLabel(link))
	}
	removedLabels := make([]string, 0, len(removed))
	for _, link := range removed {
		removedLabels = append(removedLabels, linkLabel(link))
	}
	return mustJSON(map[string]any{
		"sub_name": subName, "exists": exists,
		"added": addedLabels, "removed": removedLabels,
		"added_count": len(added), "removed_count": len(removed), "unchanged_count": unchanged,
		"total_after": len(links),
	}), nil
}

// diffLinks compares the new export against the remote content by exact link
// string, returning (added, removed, unchanged-count) with input order kept.
func diffLinks(next, current []string) ([]string, []string, int) {
	currentSet := map[string]bool{}
	for _, link := range current {
		currentSet[link] = true
	}
	nextSet := map[string]bool{}
	added := []string{}
	unchanged := 0
	for _, link := range next {
		nextSet[link] = true
		if currentSet[link] {
			unchanged++
		} else {
			added = append(added, link)
		}
	}
	removed := []string{}
	for _, link := range current {
		if !nextSet[link] {
			removed = append(removed, link)
		}
	}
	return added, removed, unchanged
}

// linkLabel returns only the parsed host. Fragments, userinfo, paths, and raw
// fallback text may contain credentials and must never reach a read-scoped
// preview response.
func linkLabel(link string) string {
	if parsed, err := url.Parse(link); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return "unnamed link"
}

// ── encrypted endpoint vault (design-15 §7) ──────────────────────────────────

func (rt *runtime) saveEndpoint(req subStoreRequest) (json.RawMessage, error) {
	base, err := validateBaseURL(req.BaseURL)
	if err != nil {
		return nil, err
	}
	doc := endpointSecretDocument{Version: 1, BaseURL: base, Autosync: req.Autosync != nil && *req.Autosync}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode endpoint settings: %w", err)
	}
	// One secret write commits the endpoint and auto-sync flag atomically. Older
	// plain endpoint values remain readable through endpointSettings.
	if err := rt.secretPut("endpoint", string(raw)); err != nil {
		return nil, fmt.Errorf("save endpoint: %s", oneLine(err.Error()))
	}
	return mustJSON(map[string]any{"ok": true, "autosync": doc.Autosync}), nil
}

func (rt *runtime) clearEndpoint() (json.RawMessage, error) {
	for _, key := range []string{"endpoint", "autosync"} {
		if _, err := rt.callHost(latticeplugin.HostMethodSecretDelete, map[string]any{"key": key}); err != nil {
			return nil, fmt.Errorf("clear %s: %s", key, oneLine(err.Error()))
		}
	}
	return mustJSON(map[string]any{"ok": true}), nil
}

// endpointStatus reports what the vault holds WITHOUT exposing the endpoint:
// the hint is scheme://host only — the path carries the Sub-Store secret token.
func (rt *runtime) endpointStatus() (json.RawMessage, error) {
	endpoint, autosync, found, err := rt.endpointSettings()
	if err != nil {
		return nil, fmt.Errorf("read endpoint settings: %s", oneLine(err.Error()))
	}
	out := map[string]any{"has_saved_endpoint": found, "autosync": autosync}
	if found {
		out["endpoint_hint"] = endpointHint(endpoint)
	}
	statusValue, statusFound, err := rt.secretGet("autosync_status")
	if err != nil {
		return nil, fmt.Errorf("read auto-sync status: %s", oneLine(err.Error()))
	}
	if statusFound {
		var status autoSyncStatusDocument
		if err := json.Unmarshal([]byte(statusValue), &status); err != nil {
			return nil, fmt.Errorf("saved auto-sync status is invalid")
		}
		out["autosync_status"] = status
	}
	return mustJSON(out), nil
}

// ── embedded engine pipeline records (KV, no raw subscription bodies) ─────────

func (rt *runtime) savePipelineRecord(record pipelineRecord) (json.RawMessage, error) {
	normalized, err := normalizePipelineRecord(record)
	if err != nil {
		return nil, err
	}
	doc, err := rt.loadPipelineRecords()
	if err != nil {
		return nil, fmt.Errorf("read pipeline records: %s", oneLine(err.Error()))
	}
	found := false
	for index, existing := range doc.Records {
		if existing.ID == normalized.ID {
			doc.Records[index] = normalized
			found = true
			break
		}
	}
	if !found {
		if len(doc.Records) >= maxPipelineRecords {
			return nil, fmt.Errorf("too many pipeline records: max %d", maxPipelineRecords)
		}
		doc.Records = append(doc.Records, normalized)
	}
	sortPipelineRecords(doc.Records)
	if err := rt.storePipelineRecords(doc); err != nil {
		return nil, fmt.Errorf("save pipeline records: %s", oneLine(err.Error()))
	}
	return mustJSON(map[string]any{"id": normalized.ID, "created": !found, "count": len(doc.Records)}), nil
}

func (rt *runtime) getPipelineRecord(ref pipelineRecordRef) (json.RawMessage, error) {
	id, err := normalizePipelineRecordID(ref.ID)
	if err != nil {
		return nil, err
	}
	doc, err := rt.loadPipelineRecords()
	if err != nil {
		return nil, fmt.Errorf("read pipeline records: %s", oneLine(err.Error()))
	}
	for _, record := range doc.Records {
		if record.ID == id {
			return mustJSON(map[string]any{"found": true, "record": record}), nil
		}
	}
	return mustJSON(map[string]any{"found": false, "id": id}), nil
}

func (rt *runtime) listPipelineRecords() (json.RawMessage, error) {
	doc, err := rt.loadPipelineRecords()
	if err != nil {
		return nil, fmt.Errorf("read pipeline records: %s", oneLine(err.Error()))
	}
	items := make([]pipelineRecordListItem, 0, len(doc.Records))
	for _, record := range doc.Records {
		items = append(items, pipelineRecordListItem{
			ID:            record.ID,
			Name:          record.Name,
			Target:        record.Target,
			OperatorCount: len(record.Operators),
		})
	}
	return mustJSON(map[string]any{"records": items, "count": len(items)}), nil
}

func (rt *runtime) deletePipelineRecord(ref pipelineRecordRef) (json.RawMessage, error) {
	id, err := normalizePipelineRecordID(ref.ID)
	if err != nil {
		return nil, err
	}
	doc, err := rt.loadPipelineRecords()
	if err != nil {
		return nil, fmt.Errorf("read pipeline records: %s", oneLine(err.Error()))
	}
	next := doc.Records[:0]
	found := false
	for _, record := range doc.Records {
		if record.ID == id {
			found = true
			continue
		}
		next = append(next, record)
	}
	if !found {
		return mustJSON(map[string]any{"id": id, "deleted": false, "count": len(doc.Records)}), nil
	}
	doc.Records = next
	if err := rt.storePipelineRecords(doc); err != nil {
		return nil, fmt.Errorf("save pipeline records: %s", oneLine(err.Error()))
	}
	return mustJSON(map[string]any{"id": id, "deleted": true, "count": len(doc.Records)}), nil
}

func (rt *runtime) runPipelineRecord(req pipelineRunRequest) (json.RawMessage, error) {
	id, err := normalizePipelineRecordID(req.ID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Raw) == "" {
		return nil, fmt.Errorf("raw subscription is required")
	}
	if len([]byte(req.Raw)) > maxPipelineRawBytes {
		return nil, fmt.Errorf("raw subscription exceeds %d bytes", maxPipelineRawBytes)
	}
	doc, err := rt.loadPipelineRecords()
	if err != nil {
		return nil, fmt.Errorf("read pipeline records: %s", oneLine(err.Error()))
	}
	for _, record := range doc.Records {
		if record.ID != id {
			continue
		}
		result, err := rt.subStoreEngine().convert(subStoreConversionRequest{
			Raw:       req.Raw,
			Target:    record.Target,
			Operators: record.Operators,
		})
		if err != nil {
			return nil, err
		}
		return mustJSON(map[string]any{"pipeline_id": id, "conversion": result}), nil
	}
	return nil, fmt.Errorf("pipeline %q was not found", id)
}

func (rt *runtime) loadPipelineRecords() (pipelineRecordsDocument, error) {
	value, found, err := rt.kvGet(pipelineRecordsKey)
	if err != nil || !found {
		return pipelineRecordsDocument{Version: 1}, err
	}
	if len(value) > maxPipelineDocBytes {
		return pipelineRecordsDocument{}, fmt.Errorf("pipeline records exceed %d bytes", maxPipelineDocBytes)
	}
	var doc pipelineRecordsDocument
	if err := json.Unmarshal(value, &doc); err != nil {
		return pipelineRecordsDocument{}, fmt.Errorf("decode pipeline records: %w", err)
	}
	if doc.Version != 1 {
		return pipelineRecordsDocument{}, fmt.Errorf("unsupported pipeline records version %d", doc.Version)
	}
	if len(doc.Records) > maxPipelineRecords {
		return pipelineRecordsDocument{}, fmt.Errorf("pipeline records exceed max %d", maxPipelineRecords)
	}
	for index, record := range doc.Records {
		normalized, err := normalizePipelineRecord(record)
		if err != nil {
			return pipelineRecordsDocument{}, fmt.Errorf("pipeline record %d is invalid: %w", index, err)
		}
		doc.Records[index] = normalized
	}
	sortPipelineRecords(doc.Records)
	return doc, nil
}

func (rt *runtime) storePipelineRecords(doc pipelineRecordsDocument) error {
	doc.Version = 1
	sortPipelineRecords(doc.Records)
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encode pipeline records: %w", err)
	}
	if len(raw) > maxPipelineDocBytes {
		return fmt.Errorf("pipeline records exceed %d bytes", maxPipelineDocBytes)
	}
	return rt.kvPut(pipelineRecordsKey, raw)
}

func normalizePipelineRecord(record pipelineRecord) (pipelineRecord, error) {
	id, err := normalizePipelineRecordID(record.ID)
	if err != nil {
		return pipelineRecord{}, err
	}
	name := strings.TrimSpace(record.Name)
	if name == "" {
		name = id
	}
	if len(name) > 128 || hasControl(name) {
		return pipelineRecord{}, fmt.Errorf("pipeline name must be printable and at most 128 characters")
	}
	target := strings.TrimSpace(record.Target)
	if target == "" {
		return pipelineRecord{}, fmt.Errorf("target is required")
	}
	if len(target) > 64 || hasControl(target) {
		return pipelineRecord{}, fmt.Errorf("target must be printable and at most 64 characters")
	}
	operators, err := normalizePipelineOperators(record.Operators)
	if err != nil {
		return pipelineRecord{}, err
	}
	return pipelineRecord{ID: id, Name: name, Target: target, Operators: operators}, nil
}

func normalizePipelineRecordID(value string) (string, error) {
	id := strings.TrimSpace(value)
	if id == "" {
		return "", fmt.Errorf("pipeline id is required")
	}
	if len(id) > 128 || hasControl(id) {
		return "", fmt.Errorf("pipeline id must be printable and at most 128 characters")
	}
	for index, char := range id {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-') ||
			(index == 0 && (char == '.' || char == '_' || char == '-')) {
			return "", fmt.Errorf("pipeline id must start with an alphanumeric character and contain only letters, numbers, dot, underscore, or hyphen")
		}
	}
	return id, nil
}

func normalizePipelineOperators(operators []json.RawMessage) ([]json.RawMessage, error) {
	if len(operators) > maxPipelineOperators {
		return nil, fmt.Errorf("too many pipeline operators: max %d", maxPipelineOperators)
	}
	out := make([]json.RawMessage, 0, len(operators))
	for index, operator := range operators {
		var object map[string]any
		if err := json.Unmarshal(operator, &object); err != nil {
			return nil, fmt.Errorf("operator %d must be a JSON object: %w", index, err)
		}
		operatorType, ok := object["type"].(string)
		if !ok || strings.TrimSpace(operatorType) == "" {
			return nil, fmt.Errorf("operator %d type is required", index)
		}
		encoded, err := json.Marshal(object)
		if err != nil {
			return nil, fmt.Errorf("encode operator %d: %w", index, err)
		}
		out = append(out, encoded)
	}
	return out, nil
}

func sortPipelineRecords(records []pipelineRecord) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
}

// endpointSettings reads the versioned one-secret document and falls back to
// the pre-v1 plain endpoint plus separate autosync flag for rolling upgrades.
func (rt *runtime) endpointSettings() (string, bool, bool, error) {
	value, found, err := rt.secretGet("endpoint")
	if err != nil || !found {
		return "", false, found, err
	}
	var doc endpointSecretDocument
	if json.Unmarshal([]byte(value), &doc) == nil && doc.Version == 1 {
		base, err := validateBaseURL(doc.BaseURL)
		if err != nil {
			return "", false, false, fmt.Errorf("saved endpoint document is invalid: %w", err)
		}
		return base, doc.Autosync, true, nil
	}
	base, err := validateBaseURL(value)
	if err != nil {
		return "", false, false, fmt.Errorf("saved endpoint is invalid: %w", err)
	}
	legacyFlag, legacyFound, err := rt.secretGet("autosync")
	if err != nil {
		return "", false, false, err
	}
	return base, legacyFound && legacyFlag == "1", true, nil
}

func (rt *runtime) secretPut(key, value string) error {
	_, err := rt.callHost(latticeplugin.HostMethodSecretPut, map[string]any{
		"key":          key,
		"value_base64": base64.StdEncoding.EncodeToString([]byte(value)),
	})
	return err
}

func (rt *runtime) secretGet(key string) (string, bool, error) {
	raw, err := rt.callHost(latticeplugin.HostMethodSecretGet, map[string]any{"key": key})
	if err != nil {
		return "", false, err
	}
	var out struct {
		OK          bool   `json:"ok"`
		ValueBase64 string `json:"value_base64"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", false, err
	}
	if !out.OK {
		return "", false, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(out.ValueBase64)
	if err != nil {
		return "", false, err
	}
	return string(decoded), true, nil
}

func (rt *runtime) kvPut(key string, value []byte) error {
	_, err := rt.callHost(latticeplugin.HostMethodKVPut, map[string]any{
		"key":          key,
		"value_base64": base64.StdEncoding.EncodeToString(value),
	})
	return err
}

func (rt *runtime) kvGet(key string) ([]byte, bool, error) {
	raw, err := rt.callHost(latticeplugin.HostMethodKVGet, map[string]any{"key": key})
	if err != nil {
		return nil, false, err
	}
	var out struct {
		OK          bool   `json:"ok"`
		Value       string `json:"value,omitempty"`
		ValueBase64 string `json:"value_base64,omitempty"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false, err
	}
	if !out.OK {
		return nil, false, nil
	}
	if out.ValueBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(out.ValueBase64)
		if err != nil {
			return nil, false, err
		}
		return decoded, true, nil
	}
	return []byte(out.Value), true, nil
}

// endpointHint renders scheme://host of a validated endpoint — never the path,
// which carries the Sub-Store API token.
func endpointHint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "(saved endpoint)"
	}
	return parsed.Scheme + "://" + parsed.Host
}

// ── shared helpers ────────────────────────────────────────────────────────────

func (rt *runtime) httpDo(method, target string, body []byte) (int, []byte, error) {
	params := map[string]any{
		"method": method,
		"url":    target,
	}
	if body != nil {
		params["header"] = map[string]string{"Content-Type": "application/json"}
		params["body"] = string(body)
	}
	raw, err := rt.callHost(latticeplugin.HostMethodHTTPOperatorDo, params)
	if err != nil {
		return 0, nil, err
	}
	var out struct {
		StatusCode int    `json:"status_code"`
		BodyBase64 string `json:"body_base64,omitempty"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, nil, fmt.Errorf("decode http response: %w", err)
	}
	var respBody []byte
	if out.BodyBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(out.BodyBase64)
		if err != nil {
			return 0, nil, fmt.Errorf("decode http response body: %w", err)
		}
		respBody = decoded
	}
	return out.StatusCode, respBody, nil
}

func (rt *runtime) callHost(method string, params any) (json.RawMessage, error) {
	if rt.host == nil {
		return nil, fmt.Errorf("host response fd unavailable")
	}
	return rt.host.call(method, params)
}

func renderPlan(payload json.RawMessage) string {
	values := map[string]any{}
	_ = json.Unmarshal(payload, &values)
	lines := []string{"# sub-store import plan (dry run - no changes made here)"}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		value := fmt.Sprintf("%v", values[k])
		lower := strings.ToLower(k)
		if strings.Contains(lower, "url") || strings.Contains(lower, "secret") ||
			strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "key") {
			value = "<redacted>"
		}
		lines = append(lines, fmt.Sprintf("# %s = %s", k, value))
	}
	lines = append(lines, "# import: rpc pull from vpn-core -> upsert managed sub in Sub-Store.")
	return strings.Join(lines, "\n")
}

func validateBaseURL(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", fmt.Errorf("base_url is required")
	}
	if len(raw) > 2048 || hasControl(raw) {
		return "", fmt.Errorf("base_url must be printable and at most 2048 characters")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("base_url must be an absolute http(s) URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("base_url must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("base_url must be an absolute http(s) URL")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("base_url must not include credentials")
	}
	if strings.EqualFold(parsed.Scheme, "http") && !isLoopbackHost(parsed.Hostname()) {
		return "", fmt.Errorf("base_url may use http only for localhost or loopback; use https for remote Sub-Store backends")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("base_url must not include query or fragment")
	}
	if hasControl(parsed.Path) {
		return "", fmt.Errorf("base_url path must not contain control characters")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return "", fmt.Errorf("base_url must include the Sub-Store secret path")
	}
	hasSecretPathSegment := false
	for _, segment := range strings.Split(parsed.Path, "/") {
		switch segment {
		case "":
			continue
		case ".", "..":
			return "", fmt.Errorf("base_url path must not contain dot segments")
		default:
			hasSecretPathSegment = true
		}
	}
	if !hasSecretPathSegment {
		return "", fmt.Errorf("base_url must include the Sub-Store secret path")
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("base_url must include a host")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("base_url port is invalid")
		}
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func normalizeSubName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return defaultSubStoreName, nil
	}
	if len(name) > 128 || hasControl(name) {
		return "", fmt.Errorf("sub_name must be printable and at most 128 characters")
	}
	for index, char := range name {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-') ||
			(index == 0 && (char == '.' || char == '_' || char == '-')) {
			return "", fmt.Errorf("sub_name must start with an alphanumeric character and contain only letters, numbers, dot, underscore, or hyphen")
		}
	}
	return name, nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// bodyExcerpt returns bounded evidence without reflecting attacker-controlled
// backend text. Response bodies can contain endpoint tokens, share links, and
// credentials; arbitrary text cannot be reliably content-redacted.
func bodyExcerpt(body []byte) string {
	if len(body) == 0 {
		return "empty response body"
	}
	sum := sha256.Sum256(body)
	text := fmt.Sprintf("response body redacted (bytes=%d sha256=%x)", len(body), sum[:8])
	if len(text) > maxErrorExcerpt {
		return text[:maxErrorExcerpt]
	}
	return text
}

// oneLine flattens text to a single printable line for error messages.
func oneLine(value string) string {
	mapped := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	return strings.Join(strings.Fields(mapped), " ")
}

func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}
