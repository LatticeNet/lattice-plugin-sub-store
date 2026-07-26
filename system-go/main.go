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
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	pluginID            = "latticenet.sub-store"
	pluginName          = "Sub-Store companion"
	pluginVersion       = "0.3.2-alpha.4"
	defaultSubStoreName = "lattice-vpn-core"
	maxExportLinks      = 10_000
	maxExportBytes      = 1 << 20
	maxLinkBytes        = 4 << 10
	maxErrorExcerpt     = 4 << 10
)

// Private/loopback Sub-Store endpoints require the explicit system-only
// operator-target primitive. Ordinary http:egress remains unable to reach them.
// secret:read/secret:write back the opt-in encrypted endpoint vault (design-15
// §7): the server resolves secret:// references from this plugin's own
// namespace, so a saved endpoint never re-crosses the browser.
var capabilities = []string{"rpc:call", "http:operator-target", "secret:read", "secret:write"}

type request struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

type callPayload struct {
	Service string          `json:"service"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

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

type response struct {
	OK      bool            `json:"ok"`
	Plan    string          `json:"plan,omitempty"`
	Message string          `json:"message,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type hostCallEnvelope struct {
	HostCall hostCall `json:"host_call"`
}

type hostCall struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type hostResponseEnvelope struct {
	HostResponse hostResponse `json:"host_response"`
}

type hostResponse struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	respScanner, closeResponses := hostResponseScanner()
	defer closeResponses()
	rt := &runtime{host: &stdioHostCaller{responses: respScanner, output: os.Stdout}}
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			write(response{OK: false, Error: "invalid request: " + err.Error()})
			continue
		}
		write(rt.handle(req))
	}
}

type runtime struct {
	host hostCaller
}

type hostCaller interface {
	call(method string, params any) (json.RawMessage, error)
}

type stdioHostCaller struct {
	responses *bufio.Scanner
	nextID    int
	output    io.Writer
}

func (rt *runtime) handle(req request) response {
	switch req.Action {
	case "describe":
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
			"engine": "plugin artifact via brokered rpc.call + http.operator.do",
		})
		return response{OK: true, Result: body, Message: "sub-store companion capability surface"}
	case "health":
		return response{OK: true, Message: "sub-store companion healthy"}
	case "plan":
		return response{OK: true, Plan: renderPlan(req.Payload), Message: "sub-store import dry-run plan"}
	case "call":
		return rt.handleCall(req.Payload)
	default:
		return response{OK: false, Error: fmt.Sprintf("unsupported action %q", req.Action)}
	}
}

func (rt *runtime) handleCall(payload json.RawMessage) response {
	var call callPayload
	if err := json.Unmarshal(payload, &call); err != nil {
		return response{OK: false, Error: "invalid call payload: " + err.Error()}
	}
	if call.Service != pluginID+"/import" {
		return response{OK: false, Error: fmt.Sprintf("unsupported service %q", call.Service)}
	}
	var req subStoreRequest
	if len(call.Payload) > 0 {
		if err := json.Unmarshal(call.Payload, &req); err != nil {
			return response{OK: false, Error: "invalid sub-store payload: " + err.Error()}
		}
	}
	switch call.Method {
	case "status":
		result := rt.status(req)
		return response{OK: true, Result: result}
	case "preview":
		result, err := rt.preview(req)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Result: result, Message: "sub-store import preview"}
	case "import":
		result, err := rt.importNodes(req)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Result: result, Message: "sub-store import complete"}
	case "save_endpoint":
		result, err := rt.saveEndpoint(req)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Result: result, Message: "sub-store endpoint saved (encrypted)"}
	case "clear_endpoint":
		result, err := rt.clearEndpoint()
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Result: result, Message: "sub-store endpoint cleared"}
	case "endpoint_status":
		result, err := rt.endpointStatus()
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Result: result}
	default:
		return response{OK: false, Error: fmt.Sprintf("unsupported method %q", call.Method)}
	}
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
	raw, err := rt.callHost("rpc.call", map[string]any{
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
		if _, err := rt.callHost("secret.delete", map[string]any{"key": key}); err != nil {
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
	_, err := rt.callHost("secret.put", map[string]any{
		"key":          key,
		"value_base64": base64.StdEncoding.EncodeToString([]byte(value)),
	})
	return err
}

func (rt *runtime) secretGet(key string) (string, bool, error) {
	raw, err := rt.callHost("secret.get", map[string]any{"key": key})
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
	raw, err := rt.callHost("http.operator.do", params)
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

func (host *stdioHostCaller) call(method string, params any) (json.RawMessage, error) {
	if host == nil || host.responses == nil || host.output == nil {
		return nil, fmt.Errorf("host response fd unavailable")
	}
	host.nextID++
	id := fmt.Sprintf("h%d", host.nextID)
	if err := json.NewEncoder(host.output).Encode(hostCallEnvelope{
		HostCall: hostCall{ID: id, Method: method, Params: params},
	}); err != nil {
		return nil, fmt.Errorf("write host_call: %w", err)
	}
	if !host.responses.Scan() {
		if err := host.responses.Err(); err != nil {
			return nil, fmt.Errorf("read host_response: %w", err)
		}
		return nil, fmt.Errorf("read host_response: eof")
	}
	var env hostResponseEnvelope
	if err := json.Unmarshal(host.responses.Bytes(), &env); err != nil {
		return nil, fmt.Errorf("decode host_response: %w", err)
	}
	if env.HostResponse.ID != id {
		return nil, fmt.Errorf("host_response id mismatch: got %q want %q", env.HostResponse.ID, id)
	}
	if !env.HostResponse.OK {
		if env.HostResponse.Error == "" {
			env.HostResponse.Error = "host call failed"
		}
		return nil, fmt.Errorf("%s: %s", method, env.HostResponse.Error)
	}
	return env.HostResponse.Result, nil
}

func hostResponseScanner() (*bufio.Scanner, func()) {
	fd := 3
	if raw := strings.TrimSpace(os.Getenv("LATTICE_HOST_RESPONSE_FD")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 3 {
			return nil, func() {}
		}
		fd = parsed
	}
	file := os.NewFile(uintptr(fd), "lattice-host-response")
	if file == nil {
		return nil, func() {}
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return scanner, func() { _ = file.Close() }
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

func write(resp response) { _ = json.NewEncoder(os.Stdout).Encode(resp) }
