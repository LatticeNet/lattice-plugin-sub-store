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
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	pluginID            = "latticenet.sub-store"
	pluginName          = "Sub-Store companion"
	pluginVersion       = "0.3.0"
	defaultSubStoreName = "lattice-vpn-core"
)

// capabilities are the broker primitives this companion uses: rpc:call to pull
// nodes from vpn-core, http:egress to push to Sub-Store, and KV for future saved
// backend presets.
var capabilities = []string{"rpc:call", "http:egress", "kv:read", "kv:write"}

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
	rt := &runtime{responses: respScanner}
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
	responses *bufio.Scanner
	nextID    int
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
			"engine": "plugin artifact via brokered rpc.call + http.do",
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
	case "import", "run":
		result, err := rt.importNodes(req)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Result: result, Message: "sub-store import complete"}
	default:
		return response{OK: false, Error: fmt.Sprintf("unsupported method %q", call.Method)}
	}
}

func (rt *runtime) status(req subStoreRequest) json.RawMessage {
	base := normalizeBaseURL(req.BaseURL)
	if base == "" {
		return mustJSON(map[string]any{"reachable": false, "sub_name": defaultSubStoreName, "error": "base_url is required"})
	}
	status, err := rt.httpDo("GET", base+"/api/utils/env", nil)
	reachable := err == nil && status >= 200 && status < 500
	out := map[string]any{"reachable": reachable, "sub_name": defaultSubStoreName}
	if err != nil {
		out["error"] = err.Error()
	}
	return mustJSON(out)
}

func (rt *runtime) importNodes(req subStoreRequest) (json.RawMessage, error) {
	base := normalizeBaseURL(req.BaseURL)
	if base == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	subName := strings.TrimSpace(req.SubName)
	if subName == "" {
		subName = defaultSubStoreName
	}

	rpcReq := map[string]string{}
	if userID := strings.TrimSpace(req.UserID); userID != "" {
		rpcReq["user_id"] = userID
	}
	raw, err := rt.hostCall("rpc.call", map[string]any{
		"service": "latticenet.vpn-core/nodes",
		"method":  "export",
		"request": rpcReq,
	})
	if err != nil {
		return nil, fmt.Errorf("export vpn-core nodes: %w", err)
	}
	var exp struct {
		Links []string `json:"links"`
	}
	if err := json.Unmarshal(raw, &exp); err != nil {
		return nil, fmt.Errorf("decode vpn-core export: %w", err)
	}

	sub := map[string]any{
		"name":        subName,
		"source":      "local",
		"displayName": "Lattice vpn-core",
		"content":     strings.Join(exp.Links, "\n"),
		"tag":         []string{"lattice", "vpn-core"},
	}
	body, _ := json.Marshal(sub)
	status, err := rt.httpDo("PATCH", base+"/api/sub/"+url.PathEscape(subName), body)
	if err != nil || status < 200 || status >= 300 {
		status, err = rt.httpDo("POST", base+"/api/subs", body)
		if err != nil {
			return nil, fmt.Errorf("sub-store create: %w", err)
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("sub-store create returned status %d", status)
		}
	}
	return mustJSON(map[string]any{"ok": true, "sub_name": subName, "pushed": len(exp.Links)}), nil
}

func (rt *runtime) httpDo(method, target string, body []byte) (int, error) {
	params := map[string]any{
		"method": method,
		"url":    target,
	}
	if body != nil {
		params["header"] = map[string]string{"Content-Type": "application/json"}
		params["body"] = string(body)
	}
	raw, err := rt.hostCall("http.do", params)
	if err != nil {
		return 0, err
	}
	var out struct {
		StatusCode int `json:"status_code"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("decode http response: %w", err)
	}
	return out.StatusCode, nil
}

func (rt *runtime) hostCall(method string, params any) (json.RawMessage, error) {
	if rt.responses == nil {
		return nil, fmt.Errorf("host response fd unavailable")
	}
	rt.nextID++
	id := fmt.Sprintf("h%d", rt.nextID)
	if err := json.NewEncoder(os.Stdout).Encode(hostCallEnvelope{
		HostCall: hostCall{ID: id, Method: method, Params: params},
	}); err != nil {
		return nil, fmt.Errorf("write host_call: %w", err)
	}
	if !rt.responses.Scan() {
		if err := rt.responses.Err(); err != nil {
			return nil, fmt.Errorf("read host_response: %w", err)
		}
		return nil, fmt.Errorf("read host_response: eof")
	}
	var env hostResponseEnvelope
	if err := json.Unmarshal(rt.responses.Bytes(), &env); err != nil {
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
		lines = append(lines, fmt.Sprintf("# %s = %v", k, values[k]))
	}
	lines = append(lines, "# import: rpc pull from vpn-core -> upsert managed sub in Sub-Store.")
	return strings.Join(lines, "\n")
}

func normalizeBaseURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func write(resp response) { _ = json.NewEncoder(os.Stdout).Encode(resp) }
