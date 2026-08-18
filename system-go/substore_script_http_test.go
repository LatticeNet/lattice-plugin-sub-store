package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// scriptHTTPHost answers http.do with canned bytes and records what it was
// asked for, so a test can assert on the request a script actually produced.
type scriptHTTPHost struct {
	mu       sync.Mutex
	requests []map[string]any
	status   int
	body     []byte
	header   map[string]string
	failWith error
}

func (h *scriptHTTPHost) call(method string, params any) (json.RawMessage, error) {
	if method != "http.do" {
		return nil, fmt.Errorf("unexpected host method %q", method)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var seen map[string]any
	if err := json.Unmarshal(encoded, &seen); err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.requests = append(h.requests, seen)
	failure := h.failWith
	h.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	status := h.status
	if status == 0 {
		status = 200
	}
	return json.Marshal(map[string]any{
		"status_code": status,
		"header":      h.header,
		"body_base64": base64.StdEncoding.EncodeToString(h.body),
	})
}

func (h *scriptHTTPHost) seen() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.requests...)
}

// scriptFetchOperator builds a Script Operator that fetches and writes what it
// got into the node names, which is the cheapest way to read a script's view
// of the response out of a real conversion.
func scriptFetchOperator(body string) []json.RawMessage {
	script := `async function operator(proxies) {
                const resp = await $substore.http.get({ url: "https://rules.example/list", timeout: 5000 });
                return proxies.map((proxy) => ({ ...proxy, name: proxy.name + "|" + resp.statusCode + "|" + ` + body + ` }));
        }`
	encoded, _ := json.Marshal(map[string]any{
		"type": "Script Operator",
		"args": map[string]any{"mode": "script", "content": script},
	})
	return []json.RawMessage{encoded}
}

const scriptHTTPNode = "ss://YWVzLTEyOC1nY206c2VjcmV0@keep.example.com:8388#Keep"

func TestScriptHTTPReachesTheHostAndReturnsToTheScript(t *testing.T) {
	host := &scriptHTTPHost{body: []byte("DOMAIN,example.com,PROXY"), header: map[string]string{"Content-Type": "text/plain"}}
	engine := newTestEmbeddedSubStoreEngine()
	defer engine.attachScriptHTTP(newScriptHTTPGateway(host))()

	result, err := engine.convert(subStoreConversionRequest{
		Raw:       scriptHTTPNode,
		Target:    "Clash",
		Operators: scriptFetchOperator(`resp.body.split(",")[2]`),
	})
	if err != nil {
		t.Fatalf("convert with a fetching script: %v", err)
	}
	if !strings.Contains(result.Output, "Keep|200|PROXY") {
		t.Fatalf("script did not see the response: %s", result.Output)
	}

	requests := host.seen()
	if len(requests) != 1 {
		t.Fatalf("expected exactly one host request, got %d", len(requests))
	}
	if requests[0]["method"] != "GET" || requests[0]["url"] != "https://rules.example/list" {
		t.Fatalf("unexpected host request: %+v", requests[0])
	}
}

func TestScriptHTTPIsUnavailableWithoutAnInvocation(t *testing.T) {
	// No attach. A script that somehow runs outside an invocation must be
	// refused in a sentence it can catch, never handed a stale host lease.
	engine := newTestEmbeddedSubStoreEngine()
	result, err := engine.convert(subStoreConversionRequest{
		Raw:       scriptHTTPNode,
		Target:    "Clash",
		Operators: catchingFetchOperator(),
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(result.Output, "network is not available") {
		t.Fatalf("the script did not receive a usable refusal: %s", result.Output)
	}
}

// catchingFetchOperator reports the failure message back through the node
// name, which is how these tests read a script author's own view of an error.
func catchingFetchOperator() []json.RawMessage {
	script := `async function operator(proxies) {
                let seen = "no-error";
                try {
                        await $substore.http.get({ url: "https://rules.example/list", timeout: 5000 });
                } catch (failure) {
                        seen = String(failure && failure.message ? failure.message : failure);
                }
                return proxies.map((proxy) => ({ ...proxy, name: proxy.name + "|" + seen }));
        }`
	encoded, _ := json.Marshal(map[string]any{
		"type": "Script Operator",
		"args": map[string]any{"mode": "script", "content": script},
	})
	return []json.RawMessage{encoded}
}

func TestScriptHTTPEnforcesItsRequestBudget(t *testing.T) {
	host := &scriptHTTPHost{body: []byte("ok")}
	gateway := newScriptHTTPGateway(host)
	for i := 0; i < scriptHTTPMaxCalls; i++ {
		if _, err := gateway.do(`{"method":"GET","url":"https://rules.example/list"}`); err != nil {
			t.Fatalf("request %d within budget failed: %v", i+1, err)
		}
	}
	_, err := gateway.do(`{"method":"GET","url":"https://rules.example/list"}`)
	if err == nil || !strings.Contains(err.Error(), "budget exhausted") {
		t.Fatalf("the budget must stop the next request, got %v", err)
	}
	if calls := len(host.seen()); calls != scriptHTTPMaxCalls {
		t.Fatalf("budget-exceeding request still reached the host: %d calls", calls)
	}
	if diagnostics := gateway.diagnostics(); len(diagnostics) != scriptHTTPMaxCalls {
		t.Fatalf("every served request must leave a record, got %d", len(diagnostics))
	}
}

func TestScriptHTTPRejectsNonHTTPSchemesBeforeSpendingBudget(t *testing.T) {
	host := &scriptHTTPHost{body: []byte("ok")}
	gateway := newScriptHTTPGateway(host)
	for _, target := range []string{"file:///etc/passwd", "ftp://example.com/x", "not a url at all::"} {
		payload, _ := json.Marshal(map[string]any{"method": "GET", "url": target})
		if _, err := gateway.do(string(payload)); err == nil {
			t.Fatalf("%q must be refused", target)
		}
	}
	if len(host.seen()) != 0 {
		t.Fatal("a refused scheme must never reach the host")
	}
	// Refusals are free: the budget is for requests that actually went out.
	if _, err := gateway.do(`{"method":"GET","url":"https://rules.example/list"}`); err != nil {
		t.Fatalf("a valid request after refusals failed: %v", err)
	}
}

func TestScriptHTTPCarriesBinaryBodiesForResolvers(t *testing.T) {
	// The DoH resolver behind Resolve Domain asks for binary-mode and then
	// does Buffer.from(body): a UTF-8 string would corrupt the packet, so the
	// shim must hand back bytes.
	payload := []byte{0x00, 0x01, 0x80, 0xff, 0xfe}
	host := &scriptHTTPHost{body: payload}
	gateway := newScriptHTTPGateway(host)
	answer, err := gateway.do(`{"method":"GET","url":"https://dns.example/dns-query","binary":true}`)
	if err != nil {
		t.Fatalf("binary request failed: %v", err)
	}
	var resp scriptHTTPResponse
	if err := json.Unmarshal([]byte(answer), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Body != "" || resp.BodyBase64 == "" {
		t.Fatalf("binary responses must travel as base64: %+v", resp)
	}
	decoded, err := base64.StdEncoding.DecodeString(resp.BodyBase64)
	if err != nil || string(decoded) != string(payload) {
		t.Fatalf("binary body did not round-trip: %v %q", err, decoded)
	}

	// Non-UTF-8 bytes take the same route even when the caller did not ask.
	implicit := newScriptHTTPGateway(&scriptHTTPHost{body: []byte{0xff, 0xfe, 0xfd}})
	answer, err = implicit.do(`{"method":"GET","url":"https://dns.example/dns-query"}`)
	if err != nil {
		t.Fatal(err)
	}
	resp = scriptHTTPResponse{}
	if err := json.Unmarshal([]byte(answer), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.BodyBase64 == "" {
		t.Fatalf("undecodable text must fall back to base64: %+v", resp)
	}
}

func TestScriptHTTPKeepsStatusAndRedactsFailures(t *testing.T) {
	// A 404 is the script's business, not an engine error.
	notFound := newScriptHTTPGateway(&scriptHTTPHost{status: 404, body: []byte("missing")})
	answer, err := notFound.do(`{"method":"GET","url":"https://rules.example/gone"}`)
	if err != nil {
		t.Fatalf("a non-2xx must reach the script, not throw: %v", err)
	}
	var resp scriptHTTPResponse
	if err := json.Unmarshal([]byte(answer), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != 404 || resp.Body != "missing" {
		t.Fatalf("status or body lost: %+v", resp)
	}

	// A host failure must not carry the URL back: script URLs are as likely
	// to embed a credential as a provider's.
	failing := newScriptHTTPGateway(&scriptHTTPHost{failWith: fmt.Errorf("http.do: dial https://secret:token@rules.example/x failed")})
	if _, err := failing.do(`{"method":"GET","url":"https://secret:token@rules.example/x"}`); err == nil {
		t.Fatal("host failure must surface")
	} else if strings.Contains(err.Error(), "secret:token") || strings.Contains(err.Error(), "://") {
		t.Fatalf("failure leaked the url: %v", err)
	}
}

// dnsAnswer builds a wire-format DNS response with one A record, so the test
// can answer DoH without a network or a DNS library.
func dnsAnswer(name string, ip [4]byte) []byte {
	packet := []byte{
		0x00, 0x00, // id
		0x81, 0x80, // response, recursion desired + available
		0x00, 0x01, // one question
		0x00, 0x01, // one answer
		0x00, 0x00, 0x00, 0x00,
	}
	for _, label := range strings.Split(name, ".") {
		packet = append(packet, byte(len(label)))
		packet = append(packet, label...)
	}
	packet = append(packet, 0x00, 0x00, 0x01, 0x00, 0x01) // root, A, IN
	packet = append(packet,
		0xc0, 0x0c, // pointer back to the question's name
		0x00, 0x01, 0x00, 0x01, // A, IN
		0x00, 0x00, 0x01, 0x2c, // ttl 300
		0x00, 0x04,
		ip[0], ip[1], ip[2], ip[3],
	)
	return packet
}

// TestResolveDomainOperatorNowResolves covers the capability this work
// actually restores. Resolve Domain is a first-class Sub-Store operator that
// carries no user JavaScript, so it runs on the warm engine — and it speaks
// DoH, which means it has never once worked in this plugin: the core's HTTP
// client had no worker, so every resolve failed. It only passes with the
// environment shim installed on BOTH engine paths.
func TestResolveDomainOperatorNowResolves(t *testing.T) {
	host := &scriptHTTPHost{
		body:   dnsAnswer("keep.example.com", [4]byte{1, 2, 3, 4}),
		header: map[string]string{"Content-Type": "application/dns-message"},
	}
	engine := newTestEmbeddedSubStoreEngine()
	defer engine.attachScriptHTTP(newScriptHTTPGateway(host))()

	operator, _ := json.Marshal(map[string]any{
		"type": "Resolve Domain Operator",
		"args": map[string]any{"provider": "Google", "type": "IPv4", "cache": false},
	})
	result, err := engine.convert(subStoreConversionRequest{
		Raw:       scriptHTTPNode,
		Target:    "Clash",
		Operators: []json.RawMessage{operator},
	})
	if err != nil {
		t.Fatalf("resolve domain conversion: %v", err)
	}
	if !strings.Contains(result.Output, "1.2.3.4") {
		t.Fatalf("the node was not resolved: %s", result.Output)
	}
	requests := host.seen()
	if len(requests) == 0 {
		t.Fatal("resolve domain never reached the network")
	}
	if url, _ := requests[0]["url"].(string); !strings.Contains(url, "dns") {
		t.Fatalf("unexpected resolver request: %+v", requests[0])
	}
}

func TestScriptHTTPBoundsRequestShape(t *testing.T) {
	host := &scriptHTTPHost{body: []byte("ok")}
	gateway := newScriptHTTPGateway(host)

	// A header carrying a line break is a smuggling attempt, and naming the
	// header beats surfacing a transport error from three layers down.
	payload, _ := json.Marshal(map[string]any{
		"method":  "GET",
		"url":     "https://rules.example/list",
		"headers": map[string]string{"X-Bad": "value\r\nHost: elsewhere"},
	})
	if _, err := gateway.do(string(payload)); err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("header injection must be refused, got %v", err)
	}

	many := map[string]string{}
	for i := 0; i < scriptHTTPMaxHeaders+1; i++ {
		many[fmt.Sprintf("X-%d", i)] = "v"
	}
	payload, _ = json.Marshal(map[string]any{"method": "GET", "url": "https://rules.example/list", "headers": many})
	if _, err := gateway.do(string(payload)); err == nil || !strings.Contains(err.Error(), "too many request headers") {
		t.Fatalf("header count must be bounded, got %v", err)
	}

	payload, _ = json.Marshal(map[string]any{
		"method":  "POST",
		"url":     "https://rules.example/list",
		"body":    strings.Repeat("x", scriptHTTPMaxRequestBodyLen+1),
		"headers": map[string]string{"Content-Type": "text/plain"},
	})
	if _, err := gateway.do(string(payload)); err == nil || !strings.Contains(err.Error(), "request body exceeds") {
		t.Fatalf("request body must be bounded, got %v", err)
	}

	if len(host.seen()) != 0 {
		t.Fatal("a refused request must never reach the host")
	}

	// Ordinary headers still pass: upstream scripts legitimately send
	// authorization and a user agent, and a denylist would break them.
	payload, _ = json.Marshal(map[string]any{
		"method":  "POST",
		"url":     "https://rules.example/list",
		"headers": map[string]string{"Authorization": "Bearer token", "User-Agent": "Lattice/1.0"},
		"body":    `{"ok":true}`,
	})
	if _, err := gateway.do(string(payload)); err != nil {
		t.Fatalf("a normal request was refused: %v", err)
	}
	requests := host.seen()
	if len(requests) != 1 {
		t.Fatalf("expected the request to go out, got %d", len(requests))
	}
	header, _ := requests[0]["header"].(map[string]any)
	if header["Authorization"] != "Bearer token" {
		t.Fatalf("headers did not reach the host: %+v", requests[0])
	}
	if requests[0]["body_base64"] == nil {
		t.Fatal("the body did not reach the host")
	}
}
