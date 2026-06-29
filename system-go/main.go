// Command lattice-plugin-sub-store is the official LatticeNet Sub-Store companion
// system plugin: it imports node connection info from the vpn-core plugin into
// the operator's existing Sub-Store backend (a managed local subscription) and
// reports reachability — preserving all native Sub-Store features.
//
// It implements the Lattice system-plugin stdio contract: newline-delimited JSON
// {action,payload} on stdin, {ok,plan,message,result,error} on stdout. The
// Lattice system runner executes this artifact for the plugin lifecycle. The
// live import (the inter-plugin RPC pull from vpn-core + the HTTP push to
// Sub-Store) runs in lattice-server today; this plugin is the officially
// maintained, signed, registered front for that companion capability.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	pluginID      = "latticenet.sub-store"
	pluginName    = "Sub-Store companion"
	pluginVersion = "0.2.0"
)

// capabilities are the genuinely broker-available primitives this companion uses:
// rpc:call to pull nodes from vpn-core, http:egress to push to Sub-Store.
var capabilities = []string{"rpc:call", "http:egress", "kv:read", "kv:write"}

type request struct {
	Action  string         `json:"action"`
	Payload map[string]any `json:"payload"`
}

type response struct {
	OK      bool            `json:"ok"`
	Plan    string          `json:"plan,omitempty"`
	Message string          `json:"message,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			write(response{OK: false, Error: "invalid request: " + err.Error()})
			continue
		}
		write(handle(req))
	}
}

func handle(req request) response {
	switch req.Action {
	case "describe":
		body, _ := json.Marshal(map[string]any{
			"id":           pluginID,
			"name":         pluginName,
			"version":      pluginVersion,
			"capabilities": capabilities,
			"manages": []string{
				"import vpn-core nodes into a managed Sub-Store subscription",
				"idempotent upsert (never replaces the whole subs array)",
				"Sub-Store backend reachability checks",
			},
			"calls":  "latticenet.vpn-core/nodes export (inter-plugin RPC)",
			"engine": "lattice-server (core); this plugin is the official front",
		})
		return response{OK: true, Result: body, Message: "sub-store companion capability surface"}
	case "health":
		return response{OK: true, Message: "sub-store companion healthy"}
	case "plan":
		return response{OK: true, Plan: renderPlan(req.Payload), Message: "sub-store import dry-run plan"}
	default:
		return response{OK: false, Error: fmt.Sprintf("unsupported action %q", req.Action)}
	}
}

func renderPlan(payload map[string]any) string {
	lines := []string{"# sub-store import plan (dry run — no changes made here)"}
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("# %s = %v", k, payload[k]))
	}
	lines = append(lines, "# import: rpc pull from vpn-core -> upsert managed sub in Sub-Store (internal-only).")
	return strings.Join(lines, "\n")
}

func write(resp response) { _ = json.NewEncoder(os.Stdout).Encode(resp) }
