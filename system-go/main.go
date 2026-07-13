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
	pluginVersion       = "0.3.2-alpha.2"
	defaultSubStoreName = "lattice-vpn-core"
	maxExportLinks      = 10_000
	maxExportBytes      = 1 << 20
	maxLinkBytes        = 4 << 10
)

// Private/loopback Sub-Store endpoints require the explicit system-only
// operator-target primitive. Ordinary http:egress remains unable to reach them.
var capabilities = []string{"rpc:call", "http:operator-target"}

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
	case "import":
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
	base, err := validateBaseURL(req.BaseURL)
	if err != nil {
		return mustJSON(map[string]any{"reachable": false, "sub_name": defaultSubStoreName, "error": err.Error()})
	}
	subName, err := normalizeSubName(req.SubName)
	if err != nil {
		return mustJSON(map[string]any{"reachable": false, "sub_name": defaultSubStoreName, "error": err.Error()})
	}
	status, err := rt.httpDo("GET", base+"/api/utils/env", nil)
	reachable := err == nil && status >= 200 && status < 500
	out := map[string]any{"reachable": reachable, "sub_name": subName}
	if err != nil {
		out["error"] = "Sub-Store endpoint is unreachable or denied by host policy"
	}
	return mustJSON(out)
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

	sub := map[string]any{
		"name":        subName,
		"source":      "local",
		"displayName": "Lattice vpn-core",
		"content":     strings.Join(exp.Links, "\n"),
		"tag":         []string{"lattice", "vpn-core"},
	}
	body, _ := json.Marshal(sub)
	status, err := rt.httpDo("PATCH", base+"/api/sub/"+url.PathEscape(subName), body)
	if err != nil {
		return nil, fmt.Errorf("Sub-Store update failed")
	}
	if status == 404 || status == 405 {
		status, err = rt.httpDo("POST", base+"/api/subs", body)
		if err != nil {
			return nil, fmt.Errorf("Sub-Store create failed")
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("Sub-Store create returned status %d", status)
		}
	} else if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Sub-Store update returned status %d", status)
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
	raw, err := rt.callHost("http.operator.do", params)
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
