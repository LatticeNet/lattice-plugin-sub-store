package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// Script HTTP: the network arm of the embedded Sub-Store core.
//
// Sub-Store's scripting model assumes a network. Its own operators need one —
// Resolve Domain speaks DoH, produceArtifact downloads remote subscriptions —
// and user scripts routinely fetch a ruleset or a provider's quota endpoint.
// Our QuickJS sandbox has no I/O of its own, so every one of those calls has
// been failing: with no client installed the core's HTTP worker is undefined,
// which surfaces either as "cannot read property 'finally' of undefined" or,
// worse, as a promise that resolves to undefined and blows up one line later.
//
// The fix keeps the sandbox exactly as tight as it was. JavaScript gets no
// socket: it gets one host function that hands a request to the plugin's Go
// side, which forwards it over the SAME guarded host call the provider fetch
// already uses (http.do). Every guard the server enforces there — capability
// check, SSRF/private-address denial re-run on each redirect, DNS-rebinding
// defense at dial time, response cap, per-call audit — applies unchanged and
// without this code being able to opt out of any of it.
//
// What is new is frequency and timing: a script can now ask for many fetches
// inside one invocation. That is what the budget below bounds.

const (
	// scriptHTTPMaxCalls bounds fetches per invocation. Upstream has no such
	// limit; we do, because a subscription render is reachable from an
	// unauthenticated share URL and a script is operator-written but not
	// operator-reviewed on every request.
	scriptHTTPMaxCalls = 8
	// scriptHTTPMaxTotalBytes bounds what one invocation may pull in total.
	// The server caps each individual response far below this.
	scriptHTTPMaxTotalBytes = 8 << 20
	// Request timeout clamp. Upstream's default is 8s; a script may ask for
	// less, and asking for more than the ceiling is silently clamped rather
	// than refused, because the invocation deadline is the real bound.
	scriptHTTPDefaultTimeout = 8 * time.Second
	scriptHTTPMaxTimeout     = 15 * time.Second
	// scriptHTTPMaxDiagnostics caps retained per-call diagnostics so a loop
	// cannot grow memory through the record it leaves behind.
	scriptHTTPMaxDiagnostics = 32
	// Request headers are the script's, and the host forwards them verbatim.
	// That is the same trust the provider fetch already has, but a script is a
	// broader authorship surface than a provider URL, so the shape is bounded
	// here: a request cannot become a smuggling channel through header count
	// or size, and the failure says which limit was hit.
	scriptHTTPMaxHeaders        = 24
	scriptHTTPMaxHeaderBytes    = 8 << 10
	scriptHTTPMaxRequestBodyLen = 256 << 10
)

// scriptHTTPRequest is what the JavaScript shim hands to Go. Field names are
// the shim's own; nothing here crosses a signed boundary.
type scriptHTTPRequest struct {
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
	TimeoutMS  int               `json:"timeout_ms,omitempty"`
	Binary     bool              `json:"binary,omitempty"`
}

// scriptHTTPResponse goes back as JSON. A text-safe body travels as a string
// so the shim needs no UTF-8 decoder; anything else (or an explicit binary
// request, which is how DoH asks) travels as base64 and the shim turns it
// into a Uint8Array.
type scriptHTTPResponse struct {
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
}

// scriptHTTPCall is one line of evidence for the operator: what a script
// actually reached, and what it got. Operations stay visible — a script that
// quietly phones home is exactly what an operator needs to be able to see.
type scriptHTTPCall struct {
	Method   string `json:"method"`
	Host     string `json:"host"`
	Status   int    `json:"status"`
	Bytes    int    `json:"bytes"`
	Duration int64  `json:"duration_ms"`
	Error    string `json:"error,omitempty"`
}

// scriptHTTPGateway serves one invocation. It is created per invocation and
// dropped with it, so its budget cannot leak into the next call and a stale
// host client can never be used: when the invocation ends the engine detaches
// the gateway, and a later script call finds no network rather than an
// expired lease.
type scriptHTTPGateway struct {
	host hostCaller

	mu       sync.Mutex
	calls    int
	bytes    int
	recorded []scriptHTTPCall
}

func newScriptHTTPGateway(host hostCaller) *scriptHTTPGateway {
	if host == nil {
		return nil
	}
	return &scriptHTTPGateway{host: host}
}

// do runs one script-initiated request. The error it returns becomes a thrown
// JavaScript exception, which the shim converts into the callback's err
// argument — the shape every Sub-Store client uses.
func (g *scriptHTTPGateway) do(requestJSON string) (string, error) {
	var req scriptHTTPRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", fmt.Errorf("script http: malformed request")
	}
	target := strings.TrimSpace(req.URL)
	if target == "" {
		return "", fmt.Errorf("script http: no url")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("script http: unparseable url")
	}
	// Checked here as well as by the broker, so a script author reads a
	// sentence about their own URL rather than a host rejection.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("script http: url must be http or https")
	}

	if err := g.reserve(); err != nil {
		return "", err
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = "GET"
	}
	if err := boundScriptHTTPHeaders(req.Headers); err != nil {
		return "", err
	}
	if len(req.Body) > scriptHTTPMaxRequestBodyLen || len(req.BodyBase64) > scriptHTTPMaxRequestBodyLen*2 {
		return "", fmt.Errorf("script http: request body exceeds %d bytes", scriptHTTPMaxRequestBodyLen)
	}
	params := map[string]any{"method": method, "url": target}
	if len(req.Headers) > 0 {
		params["header"] = req.Headers
	}
	switch {
	case req.BodyBase64 != "":
		params["body_base64"] = req.BodyBase64
	case req.Body != "":
		params["body_base64"] = base64.StdEncoding.EncodeToString([]byte(req.Body))
	}

	started := time.Now()
	raw, callErr := g.host.call(latticeplugin.HostMethodHTTPDo, params)
	elapsed := time.Since(started)
	if callErr != nil {
		g.record(scriptHTTPCall{Method: method, Host: parsed.Host, Duration: elapsed.Milliseconds(), Error: "request failed"})
		// The host's message can carry the URL, and a script URL is as likely
		// to be credential-bearing as a provider's.
		return "", fmt.Errorf("script http: %s", redactURLs(callErr.Error()))
	}
	var out struct {
		StatusCode int               `json:"status_code"`
		Header     map[string]string `json:"header,omitempty"`
		BodyBase64 string            `json:"body_base64,omitempty"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		g.record(scriptHTTPCall{Method: method, Host: parsed.Host, Duration: elapsed.Milliseconds(), Error: "malformed response"})
		return "", fmt.Errorf("script http: malformed host response")
	}
	body, decodeErr := base64.StdEncoding.DecodeString(out.BodyBase64)
	if decodeErr != nil {
		g.record(scriptHTTPCall{Method: method, Host: parsed.Host, Status: out.StatusCode, Duration: elapsed.Milliseconds(), Error: "undecodable body"})
		return "", fmt.Errorf("script http: undecodable response body")
	}
	if err := g.account(len(body)); err != nil {
		g.record(scriptHTTPCall{Method: method, Host: parsed.Host, Status: out.StatusCode, Bytes: len(body), Duration: elapsed.Milliseconds(), Error: err.Error()})
		return "", err
	}
	g.record(scriptHTTPCall{Method: method, Host: parsed.Host, Status: out.StatusCode, Bytes: len(body), Duration: elapsed.Milliseconds()})

	// A non-2xx is NOT an error here: Sub-Store scripts routinely branch on
	// the status themselves, and turning it into a throw would break them.
	resp := scriptHTTPResponse{Status: out.StatusCode, Headers: out.Header}
	if req.Binary || !utf8.Valid(body) {
		resp.BodyBase64 = base64.StdEncoding.EncodeToString(body)
	} else {
		resp.Body = string(body)
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("script http: response encoding failed")
	}
	return string(encoded), nil
}

// boundScriptHTTPHeaders keeps a script's headers to a shape the host can be
// expected to forward. It deliberately does not filter WHICH headers may be
// set: upstream scripts legitimately send authorization and user-agent, and a
// denylist would break real subscriptions while a determined author could
// still reach the same endpoint by other means.
func boundScriptHTTPHeaders(headers map[string]string) error {
	if len(headers) > scriptHTTPMaxHeaders {
		return fmt.Errorf("script http: too many request headers (%d, limit %d)", len(headers), scriptHTTPMaxHeaders)
	}
	total := 0
	for name, value := range headers {
		if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
			// Go's client would reject this too; refusing here names the
			// header instead of surfacing a transport error.
			return fmt.Errorf("script http: header %q contains a line break", name)
		}
		total += len(name) + len(value)
	}
	if total > scriptHTTPMaxHeaderBytes {
		return fmt.Errorf("script http: request headers exceed %d bytes", scriptHTTPMaxHeaderBytes)
	}
	return nil
}

// reserve takes one call slot before the request goes out, so a burst of
// concurrent script calls cannot collectively overshoot the budget.
func (g *scriptHTTPGateway) reserve() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.calls >= scriptHTTPMaxCalls {
		return fmt.Errorf("script http: request budget exhausted (%d of %d used in this call)", g.calls, scriptHTTPMaxCalls)
	}
	g.calls++
	return nil
}

func (g *scriptHTTPGateway) account(size int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.bytes += size
	if g.bytes > scriptHTTPMaxTotalBytes {
		return fmt.Errorf("script http: download budget exhausted (%d bytes in this call, limit %d)", g.bytes, scriptHTTPMaxTotalBytes)
	}
	return nil
}

func (g *scriptHTTPGateway) record(call scriptHTTPCall) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.recorded) >= scriptHTTPMaxDiagnostics {
		return
	}
	g.recorded = append(g.recorded, call)
}

// diagnostics returns what this invocation's scripts reached.
func (g *scriptHTTPGateway) diagnostics() []scriptHTTPCall {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]scriptHTTPCall(nil), g.recorded...)
}
