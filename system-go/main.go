// Command lattice-plugin-sub-store is the official LatticeNet subscription
// plugin: it stores subscription definitions, renders them through the embedded
// Sub-Store engine, and answers the core when a public share is fetched.
//
// It no longer pushes anything to an external Sub-Store. That direction existed
// while Lattice integrated with a standalone instance; now that Lattice serves
// subscriptions itself, an outbound push was the opposite of the point and sat
// in the UI next to a migration that pulled the other way.
//
// It implements the persistent Lattice stdio-json-v2 lifecycle: one runtime
// advertises its generation, then serves correlated invocations and host calls
// over the framed stdout protocol. Host responses are invocation-scoped through
// the configured response channel; runtime-ready and invoke-ready delimit the
// lifecycle instead of the legacy one-shot stdin/EOF exchange.
package main

import (
	"context"
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

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

const (
	pluginID        = "latticenet.sub-store"
	pluginName      = "Sub-Store companion"
	pluginVersion   = "0.18.0-alpha.1"
	maxExportLinks  = 10_000
	maxExportBytes  = 1 << 20
	maxLinkBytes    = 4 << 10
	maxErrorExcerpt = 4 << 10
	// maxPipelineOperators bounds the steps of a stored process chain
	// (validateProcessAgainst). The name is older than the record store: the
	// engine service's pipelines, removed in S2, held the first chains.
	maxPipelineOperators = 64
)

// Reported by the describe action. It must agree with the signed manifest:
// this list is what the plugin claims, the manifest is what the host grants,
// and a disagreement means one of them is lying to whoever reads it.
//
// http:egress fetches a remote provider under the broker's SSRF checks;
// http:operator-target reaches an address the operator explicitly designated,
// for migration and publishing. secret:read/secret:write were dropped with the
// endpoint vault, which existed only to hold an external Sub-Store URL.
var capabilities = []string{
	"rpc:call", "http:egress", "http:operator-target",
	"kv:read", "kv:write", "subscription:serve",
}

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

// hostFrameCap is how big one frame on the invocation channel may get here.
// The SDK's request side defaults to 1 MiB (DefaultMaxRequestBytes), and that
// was fine until the record store grew: one kv.get of the store document
// base64's to 1.4 times its size, so past about 770 KiB of store every call
// answered "bufio.Scanner: token too long" and the plugin died mid-invocation
// (2026-08-11, production). The core now sends a render request of up to
// model.MaxSubscriptionRequestBytes (8 MiB, a 4 MiB snapshot envelope JSON
// escaped) and a convert request of up to model.MaxConvertRequestBytes
// (6 MiB), so the cap is the larger of those plus the frame envelope.
const hostFrameCap = 12 << 20

func main() {
	if err := servePluginV2(context.Background(), os.Stdin, os.Stdout, os.Getenv); err != nil {
		os.Exit(1)
	}
}

func servePluginV2(ctx context.Context, in io.Reader, out io.Writer, getenv func(string) string) error {
	generation, err := parseRuntimeV2Environment(getenv)
	if err != nil {
		return err
	}
	// Nothing boots here: the engine compiles the core on the first call
	// that reaches the bundle (ensureCoreBytecode), so an idle worker holds
	// no QuickJS runtime.
	base := &runtime{engine: newEmbeddedSubStoreEngine()}
	rt := latticeplugin.NewRuntime(pluginRuntimeOptions(in, out))
	defer rt.Close()
	return rt.ServeV2(ctx, invocationHandler(base), generation)
}

// pluginRuntimeOptions is the transport production serves with. A test that
// drives the framed transport swaps the host for a pipe and keeps the rest,
// the frame cap included.
func pluginRuntimeOptions(in io.Reader, out io.Writer) latticeplugin.RuntimeOptions {
	return latticeplugin.RuntimeOptions{
		In:              in,
		Out:             out,
		OpenHostFromEnv: true,
		MaxRequestBytes: hostFrameCap,
	}
}

func parseRuntimeV2Environment(getenv func(string) string) (uint64, error) {
	if getenv == nil || getenv("LATTICE_RUNTIME_PROTOCOL") != latticeplugin.RuntimeProtocolStdioJSONV2 {
		return 0, fmt.Errorf("LATTICE_RUNTIME_PROTOCOL must be %s", latticeplugin.RuntimeProtocolStdioJSONV2)
	}
	raw := getenv("LATTICE_RUNTIME_GENERATION")
	generation, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || generation == 0 || strconv.FormatUint(generation, 10) != raw {
		return 0, fmt.Errorf("LATTICE_RUNTIME_GENERATION must be a canonical positive integer")
	}
	return generation, nil
}

func invocationHandler(base *runtime) latticeplugin.Handler {
	return latticeplugin.HandlerFunc(func(ctx context.Context, req latticeplugin.Request, host *latticeplugin.HostClient) latticeplugin.Response {
		return serveInvocation(base.engine, sdkHostCaller{ctx: ctx, client: host}, req)
	})
}

// serveInvocation is one invocation's whole contract, in one place so a test
// drives the same grant decision production does rather than a copy of it.
//
// Scripts get the network for exactly this invocation, and only when the method
// being invoked is one the manifest declares broadly enough to cover it (see
// script_network_policy.go). Attaching unconditionally was the older rule; it
// meant a method declared substore:read could drive arbitrary outbound
// requests, because the grant sat on the invocation rather than on the method
// whose scope is the only bound this process has. Everything still runs its
// chain, just without a network on the read-scoped paths. The release runs even
// when a handler panics.
func serveInvocation(engine *subStoreEngine, host hostCaller, req request) response {
	invocation := &runtime{engine: engine, host: host}
	var gateway *scriptHTTPGateway
	if scriptNetworkAllowed(req) {
		gateway = newScriptHTTPGateway(host)
	}
	release := engine.attachScriptHTTP(gateway)
	defer release()
	return invocation.handle(req)
}

type runtime struct {
	host   hostCaller
	engine *subStoreEngine
	// legacy caches the legacy document for one invocation, so a store that
	// has not migrated pays its read once rather than once per record.
	legacy legacyCache
	// settings caches the settings document for one invocation, so a
	// collection of remote members reads the default agent once.
	settings settingsCache
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
	case pluginID + "/subscription":
		return rt.handleSubscriptionCall(call)
	default:
		return latticeplugin.ErrorResponse(fmt.Errorf("unsupported service %q", call.Service))
	}
}

func (rt *runtime) subStoreEngine() *subStoreEngine {
	if rt != nil && rt.engine != nil {
		return rt.engine
	}
	return newEmbeddedSubStoreEngine()
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
		// base64, not a JSON string: the host_call frame counts against the
		// method's stdout_bytes, and JSON escaping can grow a body six times
		// where base64 grows it by a third.
		params["body_base64"] = base64.StdEncoding.EncodeToString(body)
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
