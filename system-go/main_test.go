package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type recordedHostCall struct {
	method string
	params map[string]any
}

type fakeHostCaller struct {
	calls     []recordedHostCall
	responses []json.RawMessage
	errors    []error
}

func (f *fakeHostCaller) call(method string, params any) (json.RawMessage, error) {
	encoded, _ := json.Marshal(params)
	decoded := map[string]any{}
	_ = json.Unmarshal(encoded, &decoded)
	f.calls = append(f.calls, recordedHostCall{method: method, params: decoded})
	index := len(f.calls) - 1
	if index < len(f.errors) && f.errors[index] != nil {
		return nil, f.errors[index]
	}
	if index < len(f.responses) {
		return f.responses[index], nil
	}
	return nil, fmt.Errorf("unexpected host call %s", method)
}

func TestValidateBaseURLAcceptsHTTPSWithSecretPath(t *testing.T) {
	got, err := validateBaseURL(" https://sub.example.com/secret-path/ ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://sub.example.com/secret-path" {
		t.Fatalf("base URL = %q, want normalized secret path URL", got)
	}
}

func TestValidateBaseURLAcceptsLoopbackHTTPWithSecretPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "http://localhost/secret/", want: "http://localhost/secret"},
		{in: "http://127.0.0.1:3000/secret", want: "http://127.0.0.1:3000/secret"},
		{in: "http://127.7.8.9/secret", want: "http://127.7.8.9/secret"},
		{in: "http://[::1]:3000/secret", want: "http://[::1]:3000/secret"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := validateBaseURL(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("base URL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateBaseURLRejectsUnsafeInputs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "required"},
		{name: "relative", in: "sub-store.local/secret", want: "absolute"},
		{name: "scheme", in: "file:///tmp/sub-store/secret", want: "http or https"},
		{name: "credentials", in: "https://user:pass@sub.example.com/secret", want: "credentials"},
		{name: "missing secret path", in: "https://sub.example.com", want: "secret path"},
		{name: "slash-only secret path", in: "https://sub.example.com////", want: "secret path"},
		{name: "query", in: "https://sub.example.com/secret?token=abc", want: "query or fragment"},
		{name: "fragment", in: "https://sub.example.com/secret#frag", want: "query or fragment"},
		{name: "encoded control", in: "https://sub.example.com/secret%0aheader", want: "control"},
		{name: "dot segment", in: "https://sub.example.com/./secret", want: "dot segments"},
		{name: "traversal", in: "https://sub.example.com/../secret", want: "dot segments"},
		{name: "encoded traversal", in: "https://sub.example.com/%2e%2e/secret", want: "dot segments"},
		{name: "bad port", in: "https://sub.example.com:99999/secret", want: "invalid"},
		{name: "remote http host", in: "http://sub.example.com/secret", want: "https"},
		{name: "private lan http", in: "http://10.0.0.5/secret", want: "loopback"},
		{name: "unspecified http", in: "http://0.0.0.0:3000/secret", want: "loopback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateBaseURL(tc.in)
			if err == nil {
				t.Fatalf("validateBaseURL(%q) = %q, want error containing %q", tc.in, got, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tc.want)
			}
		})
	}
}

func TestNormalizeSubNameRejectsPathSyntax(t *testing.T) {
	for _, value := range []string{"../other", "/absolute", "space name", "name/slash", ".hidden", "_private", "-temporary"} {
		if got, err := normalizeSubName(value); err == nil {
			t.Fatalf("normalizeSubName(%q) = %q, want error", value, got)
		}
	}
	if got, err := normalizeSubName("managed.v2_1"); err != nil || got != "managed.v2_1" {
		t.Fatalf("valid sub name: got=%q err=%v", got, err)
	}
}

func TestSubStoreStatusRejectsInvalidURLBeforeHostCall(t *testing.T) {
	host := &fakeHostCaller{}
	rt := &runtime{host: host}

	out := string(rt.status(subStoreRequest{BaseURL: "https://sub.example.com"}))

	if !strings.Contains(out, `"reachable":false`) {
		t.Fatalf("status response = %s, want unreachable", out)
	}
	if !strings.Contains(out, "secret path") {
		t.Fatalf("status response = %s, want secret path validation error", out)
	}
	if len(host.calls) != 0 {
		t.Fatalf("invalid URL reached host: %+v", host.calls)
	}
}

func TestSubStoreStatusUsesOnlyOperatorTargetHTTPAndRedactsHostErrors(t *testing.T) {
	host := &fakeHostCaller{responses: []json.RawMessage{json.RawMessage(`{"status_code":204}`)}}
	rt := &runtime{host: host}
	out := string(rt.status(subStoreRequest{BaseURL: "http://127.0.0.1:3000/secret"}))
	if !strings.Contains(out, `"reachable":true`) || len(host.calls) != 1 || host.calls[0].method != "http.operator.do" {
		t.Fatalf("status=%s calls=%+v", out, host.calls)
	}

	secret := "https://10.0.0.5/very-secret-path"
	failing := &fakeHostCaller{errors: []error{errors.New("dial " + secret + ": refused")}}
	out = string((&runtime{host: failing}).status(subStoreRequest{BaseURL: secret}))
	if strings.Contains(out, "very-secret-path") || !strings.Contains(out, `"reachable":false`) {
		t.Fatalf("status leaked endpoint secret: %s", out)
	}
}

func TestSubStoreImportCallsDeclaredRPCThenOperatorTargetUpsert(t *testing.T) {
	host := &fakeHostCaller{responses: []json.RawMessage{
		json.RawMessage(`{"links":["vless://one","ss://two"]}`),
		json.RawMessage(`{"status_code":204}`),
	}}
	rt := &runtime{host: host}
	result, err := rt.importNodes(subStoreRequest{
		BaseURL: "https://10.0.0.5/secret", SubName: "managed", UserID: "user-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"pushed":2`) || len(host.calls) != 2 {
		t.Fatalf("result=%s calls=%+v", result, host.calls)
	}
	if host.calls[0].method != "rpc.call" || host.calls[1].method != "http.operator.do" {
		t.Fatalf("unexpected call order: %+v", host.calls)
	}
	if got := host.calls[1].params["method"]; got != "PATCH" {
		t.Fatalf("upsert method=%v", got)
	}
	if body, _ := host.calls[1].params["body"].(string); !strings.Contains(body, "vless://one\\nss://two") {
		t.Fatalf("upsert body=%q", body)
	}
}

func TestSubStoreImportFallsBackToCreateOnlyForMissingUpdate(t *testing.T) {
	host := &fakeHostCaller{responses: []json.RawMessage{
		json.RawMessage(`{"links":[]}`),
		json.RawMessage(`{"status_code":404}`),
		json.RawMessage(`{"status_code":201}`),
	}}
	if _, err := (&runtime{host: host}).importNodes(subStoreRequest{BaseURL: "https://10.0.0.5/secret"}); err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 3 || host.calls[2].params["method"] != "POST" {
		t.Fatalf("missing update did not create: %+v", host.calls)
	}

	failing := &fakeHostCaller{
		responses: []json.RawMessage{json.RawMessage(`{"links":[]}`)},
		errors:    []error{nil, errors.New("dial https://10.0.0.5/secret: refused")},
	}
	_, err := (&runtime{host: failing}).importNodes(subStoreRequest{BaseURL: "https://10.0.0.5/secret"})
	if err == nil || len(failing.calls) != 2 || strings.Contains(err.Error(), "/secret") {
		t.Fatalf("network failure fallback/leak: calls=%+v err=%v", failing.calls, err)
	}
}

func TestSubStoreRejectsUndeclaredAliasAndOversizedExports(t *testing.T) {
	rt := &runtime{host: &fakeHostCaller{}}
	call, _ := json.Marshal(callPayload{Service: pluginID + "/import", Method: "run", Payload: json.RawMessage(`{}`)})
	resp := rt.handle(request{Action: "call", Payload: call})
	if resp.OK || !strings.Contains(resp.Error, "unsupported method") {
		t.Fatalf("undeclared run alias accepted: %+v", resp)
	}

	links := make([]string, maxExportLinks+1)
	for i := range links {
		links[i] = "ss://x"
	}
	raw, _ := json.Marshal(map[string]any{"links": links})
	host := &fakeHostCaller{responses: []json.RawMessage{raw}}
	_, err := (&runtime{host: host}).importNodes(subStoreRequest{BaseURL: "https://10.0.0.5/secret"})
	if err == nil || !strings.Contains(err.Error(), "too many links") || len(host.calls) != 1 {
		t.Fatalf("oversized export not stopped before HTTP: calls=%d err=%v", len(host.calls), err)
	}
}

// ── embedded engine pipeline records (KV, no raw subscription bodies) ─────────

func TestPipelineRecordsSaveUsesScopedKVDocument(t *testing.T) {
	host := &fakeHostCaller{responses: []json.RawMessage{json.RawMessage(`{"ok":false}`), json.RawMessage(`{}`)}}
	rt := &runtime{host: host}
	payload := mustJSON(callPayload{
		Service: pluginID + "/engine",
		Method:  "save_pipeline",
		Payload: mustJSON(pipelineRecord{
			ID:     "daily",
			Name:   "Daily export",
			Target: "Clash",
			Operators: []json.RawMessage{json.RawMessage(`{
				"type": "Script Filter",
				"args": {"mode": "script", "content": "function filter(proxies) { return proxies.map(() => true); }"}
			}`)},
		}),
	})

	resp := rt.handle(request{Action: "call", Payload: payload})
	if !resp.OK {
		t.Fatalf("save pipeline failed: %+v", resp)
	}
	if len(host.calls) != 2 || host.calls[0].method != "kv.get" || host.calls[1].method != "kv.put" {
		t.Fatalf("host calls: %+v", host.calls)
	}
	if host.calls[0].params["key"] != pipelineRecordsKey || host.calls[1].params["key"] != pipelineRecordsKey {
		t.Fatalf("kv keys: %+v", host.calls)
	}
	doc := decodePipelineDocumentFromKVPut(t, host.calls[1])
	if doc.Version != 1 || len(doc.Records) != 1 || doc.Records[0].ID != "daily" ||
		doc.Records[0].Name != "Daily export" || doc.Records[0].Target != "Clash" {
		t.Fatalf("stored document: %+v", doc)
	}
	if len(doc.Records[0].Operators) != 1 || !strings.Contains(string(doc.Records[0].Operators[0]), "Script Filter") {
		t.Fatalf("stored operators: %+v", doc.Records[0].Operators)
	}
}

func TestPipelineRecordsListOmitsOperatorBodies(t *testing.T) {
	doc := pipelineRecordsDocument{Version: 1, Records: []pipelineRecord{{
		ID:     "daily",
		Name:   "Daily export",
		Target: "Clash",
		Operators: []json.RawMessage{json.RawMessage(`{
			"type": "Script Operator",
			"args": {"mode": "script", "content": "secret-token-in-script"}
		}`)},
	}}}
	host := &fakeHostCaller{responses: []json.RawMessage{kvDocumentResponse(t, doc)}}
	rt := &runtime{host: host}
	payload := mustJSON(callPayload{Service: pluginID + "/engine", Method: "list_pipelines"})

	resp := rt.handle(request{Action: "call", Payload: payload})
	if !resp.OK {
		t.Fatalf("list pipelines failed: %+v", resp)
	}
	if strings.Contains(string(resp.Result), "secret-token-in-script") {
		t.Fatalf("list leaked operator body: %s", resp.Result)
	}
	var got struct {
		Count   int `json:"count"`
		Records []struct {
			ID            string `json:"id"`
			OperatorCount int    `json:"operator_count"`
		} `json:"records"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 1 || len(got.Records) != 1 || got.Records[0].ID != "daily" || got.Records[0].OperatorCount != 1 {
		t.Fatalf("list result: %+v", got)
	}
}

func TestPipelineRecordsDeleteRewritesDocument(t *testing.T) {
	doc := pipelineRecordsDocument{Version: 1, Records: []pipelineRecord{
		{ID: "daily", Name: "Daily", Target: "Clash"},
		{ID: "weekly", Name: "Weekly", Target: "sing-box"},
	}}
	host := &fakeHostCaller{responses: []json.RawMessage{kvDocumentResponse(t, doc), json.RawMessage(`{}`)}}
	rt := &runtime{host: host}
	payload := mustJSON(callPayload{
		Service: pluginID + "/engine",
		Method:  "delete_pipeline",
		Payload: mustJSON(pipelineRecordRef{ID: "daily"}),
	})

	resp := rt.handle(request{Action: "call", Payload: payload})
	if !resp.OK {
		t.Fatalf("delete pipeline failed: %+v", resp)
	}
	if len(host.calls) != 2 || host.calls[0].method != "kv.get" || host.calls[1].method != "kv.put" {
		t.Fatalf("host calls: %+v", host.calls)
	}
	next := decodePipelineDocumentFromKVPut(t, host.calls[1])
	if len(next.Records) != 1 || next.Records[0].ID != "weekly" {
		t.Fatalf("rewritten document: %+v", next)
	}
}

func TestPipelineRecordsRunSavedPipelineWithoutStoringRaw(t *testing.T) {
	doc := pipelineRecordsDocument{Version: 1, Records: []pipelineRecord{{
		ID:     "daily",
		Name:   "Daily",
		Target: "Clash",
		Operators: []json.RawMessage{json.RawMessage(`{
			"type": "Script Filter",
			"args": {"mode": "script", "content": "return $server.name.includes('Keep');"}
		}`)},
	}}}
	host := &fakeHostCaller{responses: []json.RawMessage{kvDocumentResponse(t, doc)}}
	engine := newTestEmbeddedSubStoreEngine()
	rt := &runtime{host: host, engine: &engine}
	payload := mustJSON(callPayload{
		Service: pluginID + "/engine",
		Method:  "run_pipeline",
		Payload: mustJSON(pipelineRunRequest{
			ID: "daily",
			Raw: strings.Join([]string{
				"ss://YWVzLTEyOC1nY206c2VjcmV0@keep.example.com:8388#Keep",
				"ss://YWVzLTEyOC1nY206c2VjcmV0@drop.example.com:8388#Drop",
			}, "\n"),
		}),
	})

	resp := rt.handle(request{Action: "call", Payload: payload})
	if !resp.OK {
		t.Fatalf("run pipeline failed: %+v", resp)
	}
	if len(host.calls) != 1 || host.calls[0].method != "kv.get" {
		t.Fatalf("host calls: %+v", host.calls)
	}
	var got struct {
		PipelineID string                   `json:"pipeline_id"`
		Conversion subStoreConversionResult `json:"conversion"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatal(err)
	}
	if got.PipelineID != "daily" || got.Conversion.SourceNodeCount != 2 ||
		got.Conversion.NodeCount != 1 || !strings.Contains(got.Conversion.Output, "Keep") ||
		strings.Contains(got.Conversion.Output, "Drop") {
		t.Fatalf("run pipeline result: %+v", got)
	}
}

func TestPipelineRecordsRunMissingPipelineRedactsRaw(t *testing.T) {
	host := &fakeHostCaller{responses: []json.RawMessage{kvDocumentResponse(t, pipelineRecordsDocument{Version: 1})}}
	rt := &runtime{host: host}
	secretRaw := "ss://password@secret-node.example:443#Secret"
	payload := mustJSON(callPayload{
		Service: pluginID + "/engine",
		Method:  "run_pipeline",
		Payload: mustJSON(pipelineRunRequest{ID: "missing", Raw: secretRaw}),
	})

	resp := rt.handle(request{Action: "call", Payload: payload})
	if resp.OK || !strings.Contains(resp.Error, "not found") || strings.Contains(resp.Error, secretRaw) {
		t.Fatalf("missing pipeline error: %+v", resp)
	}
	if len(host.calls) != 1 || host.calls[0].method != "kv.get" {
		t.Fatalf("host calls: %+v", host.calls)
	}
}

func TestPipelineRecordsRunRejectsMissingRawBeforeHostCall(t *testing.T) {
	host := &fakeHostCaller{}
	rt := &runtime{host: host}
	payload := mustJSON(callPayload{
		Service: pluginID + "/engine",
		Method:  "run_pipeline",
		Payload: mustJSON(pipelineRunRequest{ID: "daily", Raw: " \n\t "}),
	})

	resp := rt.handle(request{Action: "call", Payload: payload})
	if resp.OK || !strings.Contains(resp.Error, "raw subscription is required") {
		t.Fatalf("missing raw accepted: %+v", resp)
	}
	if len(host.calls) != 0 {
		t.Fatalf("missing raw reached host: %+v", host.calls)
	}
}

func TestPipelineRecordsRunRejectsOversizedRawBeforeHostCall(t *testing.T) {
	host := &fakeHostCaller{}
	rt := &runtime{host: host}
	raw := "ss://" + strings.Repeat("a", maxPipelineRawBytes+1)
	payload := mustJSON(callPayload{
		Service: pluginID + "/engine",
		Method:  "run_pipeline",
		Payload: mustJSON(pipelineRunRequest{ID: "daily", Raw: raw}),
	})

	resp := rt.handle(request{Action: "call", Payload: payload})
	if resp.OK || !strings.Contains(resp.Error, "raw subscription exceeds") || strings.Contains(resp.Error, raw) {
		t.Fatalf("oversized raw error: %+v", resp)
	}
	if len(host.calls) != 0 {
		t.Fatalf("oversized raw reached host: %+v", host.calls)
	}
}

func TestPipelineRecordValidationStopsBeforeHostCall(t *testing.T) {
	host := &fakeHostCaller{}
	rt := &runtime{host: host}
	payload := mustJSON(callPayload{
		Service: pluginID + "/engine",
		Method:  "save_pipeline",
		Payload: mustJSON(pipelineRecord{ID: "../bad", Target: "Clash"}),
	})

	resp := rt.handle(request{Action: "call", Payload: payload})
	if resp.OK || !strings.Contains(resp.Error, "pipeline id") {
		t.Fatalf("invalid pipeline accepted: %+v", resp)
	}
	if len(host.calls) != 0 {
		t.Fatalf("invalid pipeline reached host: %+v", host.calls)
	}
}

func kvDocumentResponse(t *testing.T, doc pipelineRecordsDocument) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(fmt.Sprintf(`{"ok":true,"value_base64":%q}`, base64.StdEncoding.EncodeToString(raw)))
}

func decodePipelineDocumentFromKVPut(t *testing.T, call recordedHostCall) pipelineRecordsDocument {
	t.Helper()
	value, ok := call.params["value_base64"].(string)
	if !ok {
		t.Fatalf("kv.put missing value_base64: %+v", call.params)
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	var doc pipelineRecordsDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestSubStorePlanRedactsSecretValues(t *testing.T) {
	plan := renderPlan(json.RawMessage(`{"base_url":"https://sub.example/very-secret","user_id":"user-1","sub_name":"managed"}`))
	if strings.Contains(plan, "very-secret") || !strings.Contains(plan, "base_url = <redacted>") {
		t.Fatalf("plan leaked secret: %s", plan)
	}
}

// ── design-15 §7: preview / endpoint vault ───────────────────────────────────

func TestDiffLinks(t *testing.T) {
	next := []string{"a", "b", "c"}
	current := []string{"b", "d"}
	added, removed, unchanged := diffLinks(next, current)
	if len(added) != 2 || added[0] != "a" || added[1] != "c" {
		t.Fatalf("added: %v", added)
	}
	if len(removed) != 1 || removed[0] != "d" {
		t.Fatalf("removed: %v", removed)
	}
	if unchanged != 1 {
		t.Fatalf("unchanged: %d", unchanged)
	}
}

func TestLinkLabel(t *testing.T) {
	if got := linkLabel("vless://uuid@1.2.3.4:443?security=reality#secret-label"); got != "1.2.3.4:443" {
		t.Fatalf("host label: %q", got)
	}
	if got := linkLabel("trojan://pw@example.com:8443"); got != "example.com:8443" {
		t.Fatalf("host label: %q", got)
	}
	if got := linkLabel("not-a-url-secret-token"); got != "unnamed link" {
		t.Fatalf("raw fallback leaked: %q", got)
	}
}

func TestPreviewDiffsAgainstRemote(t *testing.T) {
	host := &fakeHostCaller{
		responses: []json.RawMessage{
			json.RawMessage(`{"links":["vless://a@node-a.example:443#n1","vless://b@node-b.example:443#n2","vless://c@node-c.example:443#n3"]}`),
			json.RawMessage(`{"status_code":200,"body_base64":"eyJjb250ZW50Ijoidmxlc3M6Ly9iQG5vZGUtYi5leGFtcGxlOjQ0MyNuMlxudmxlc3M6Ly9kQG5vZGUtZC5leGFtcGxlOjQ0MyNuNCJ9"}`),
		},
		errors: []error{nil, nil},
	}
	rt := &runtime{host: host}
	out, err := rt.preview(subStoreRequest{BaseURL: "https://sub.example.com/secret"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Exists         bool     `json:"exists"`
		Added          []string `json:"added"`
		Removed        []string `json:"removed"`
		UnchangedCount int      `json:"unchanged_count"`
		TotalAfter     int      `json:"total_after"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Exists || got.UnchangedCount != 1 || got.TotalAfter != 3 {
		t.Fatalf("preview: %+v", got)
	}
	if len(got.Added) != 2 || got.Added[0] != "node-a.example:443" || got.Added[1] != "node-c.example:443" {
		t.Fatalf("added labels: %v", got.Added)
	}
	if len(got.Removed) != 1 || got.Removed[0] != "node-d.example:443" {
		t.Fatalf("removed labels: %v", got.Removed)
	}
	// The preview must issue exactly one read (GET) and no writes.
	for _, call := range host.calls {
		if call.method == "http.operator.do" && call.params["method"] != "GET" {
			t.Fatalf("preview wrote to the backend: %v", call.params)
		}
	}
}

func TestPreviewMissingSubReportsNotExists(t *testing.T) {
	host := &fakeHostCaller{
		responses: []json.RawMessage{
			json.RawMessage(`{"links":["vless://a#n1"]}`),
			json.RawMessage(`{"status_code":404}`),
		},
		errors: []error{nil, nil},
	}
	rt := &runtime{host: host}
	out, err := rt.preview(subStoreRequest{BaseURL: "https://sub.example.com/secret"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Exists     bool `json:"exists"`
		AddedCount int  `json:"added_count"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Exists || got.AddedCount != 1 {
		t.Fatalf("missing sub: %+v", got)
	}
}

func TestSaveEndpointWritesEncryptedVault(t *testing.T) {
	host := &fakeHostCaller{
		responses: []json.RawMessage{json.RawMessage(`{}`)},
		errors:    []error{nil},
	}
	rt := &runtime{host: host}
	autosync := true
	out, err := rt.saveEndpoint(subStoreRequest{BaseURL: " https://sub.example.com/secret/ ", Autosync: &autosync})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK       bool `json:"ok"`
		Autosync bool `json:"autosync"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || !got.Autosync {
		t.Fatalf("save: %+v", got)
	}
	if len(host.calls) != 1 || host.calls[0].method != "secret.put" {
		t.Fatalf("calls: %+v", host.calls)
	}
	if host.calls[0].params["key"] != "endpoint" {
		t.Fatalf("keys: %+v", host.calls)
	}
	// Endpoint and auto-sync are one versioned, atomically-written document.
	value, _ := base64.StdEncoding.DecodeString(host.calls[0].params["value_base64"].(string))
	var doc endpointSecretDocument
	if err := json.Unmarshal(value, &doc); err != nil {
		t.Fatalf("stored document: %v (%q)", err, value)
	}
	if doc.Version != 1 || doc.BaseURL != "https://sub.example.com/secret" || !doc.Autosync {
		t.Fatalf("stored document: %+v", doc)
	}
}

func TestSaveEndpointFailureCannotPartiallyEnableAutosync(t *testing.T) {
	host := &fakeHostCaller{errors: []error{errors.New("vault unavailable")}}
	autosync := true
	_, err := (&runtime{host: host}).saveEndpoint(subStoreRequest{
		BaseURL: "https://sub.example.com/secret", Autosync: &autosync,
	})
	if err == nil || len(host.calls) != 1 || host.calls[0].params["key"] != "endpoint" {
		t.Fatalf("atomic save failure: err=%v calls=%+v", err, host.calls)
	}
}

func TestClearEndpointDeletesBothKeys(t *testing.T) {
	host := &fakeHostCaller{
		responses: []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{}`)},
		errors:    []error{nil, nil},
	}
	rt := &runtime{host: host}
	if _, err := rt.clearEndpoint(); err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 2 || host.calls[0].method != "secret.delete" || host.calls[0].params["key"] != "endpoint" ||
		host.calls[1].params["key"] != "autosync" {
		t.Fatalf("calls: %+v", host.calls)
	}
}

func TestEndpointStatusHintsWithoutLeakingPath(t *testing.T) {
	host := &fakeHostCaller{
		responses: []json.RawMessage{
			json.RawMessage(`{"ok":true,"value_base64":"aHR0cHM6Ly9zdWIuZXhhbXBsZS5jb20vc2VjcmV0LXRva2Vu"}`),
			json.RawMessage(`{"ok":true,"value_base64":"MQ=="}`),
			json.RawMessage(`{"ok":false}`),
		},
		errors: []error{nil, nil, nil},
	}
	rt := &runtime{host: host}
	out, err := rt.endpointStatus()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		HasSaved     bool   `json:"has_saved_endpoint"`
		Autosync     bool   `json:"autosync"`
		EndpointHint string `json:"endpoint_hint"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !got.HasSaved || !got.Autosync {
		t.Fatalf("status: %+v", got)
	}
	if got.EndpointHint != "https://sub.example.com" || strings.Contains(got.EndpointHint, "secret") {
		t.Fatalf("hint leaks path: %q", got.EndpointHint)
	}
}

func TestEndpointStatusReadsAtomicDocumentAndSyncHealth(t *testing.T) {
	endpointDoc, _ := json.Marshal(endpointSecretDocument{
		Version: 1, BaseURL: "https://sub.example.com/secret-token", Autosync: true,
	})
	syncDoc, _ := json.Marshal(autoSyncStatusDocument{
		State: "error", AttemptedAt: "2026-07-22T12:00:00Z", Error: "import failed",
	})
	host := &fakeHostCaller{responses: []json.RawMessage{
		json.RawMessage(fmt.Sprintf(`{"ok":true,"value_base64":%q}`, base64.StdEncoding.EncodeToString(endpointDoc))),
		json.RawMessage(fmt.Sprintf(`{"ok":true,"value_base64":%q}`, base64.StdEncoding.EncodeToString(syncDoc))),
	}}
	out, err := (&runtime{host: host}).endpointStatus()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		HasSaved   bool                   `json:"has_saved_endpoint"`
		Autosync   bool                   `json:"autosync"`
		Endpoint   string                 `json:"endpoint_hint"`
		SyncStatus autoSyncStatusDocument `json:"autosync_status"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !got.HasSaved || !got.Autosync || got.Endpoint != "https://sub.example.com" ||
		got.SyncStatus.State != "error" || got.SyncStatus.Error != "import failed" {
		t.Fatalf("status: %+v", got)
	}
	if strings.Contains(string(out), "secret-token") {
		t.Fatalf("status leaked endpoint path: %s", out)
	}
}

func TestImportErrorSurfacesBackendBody(t *testing.T) {
	host := &fakeHostCaller{
		responses: []json.RawMessage{
			json.RawMessage(`{"links":[]}`),
			json.RawMessage(`{"status_code":500,"body_base64":"eyJlcnJvciI6InBhc3N3b3JkPXN1cGVyLXNlY3JldCB2bGVzczovL3V1aWRAbm9kZSJ9"}`),
		},
		errors: []error{nil, nil},
	}
	rt := &runtime{host: host}
	_, err := rt.importNodes(subStoreRequest{BaseURL: "https://sub.example.com/secret"})
	if err == nil || !strings.Contains(err.Error(), "response body redacted") ||
		strings.Contains(err.Error(), "super-secret") || strings.Contains(err.Error(), "vless://") {
		t.Fatalf("backend body was not safely redacted: %v", err)
	}
}

func TestEndpointStatusSurfacesVaultFailure(t *testing.T) {
	rt := &runtime{host: &fakeHostCaller{errors: []error{errors.New("vault unavailable")}}}
	if _, err := rt.endpointStatus(); err == nil || !strings.Contains(err.Error(), "vault unavailable") {
		t.Fatalf("vault failure hidden: %v", err)
	}
}
