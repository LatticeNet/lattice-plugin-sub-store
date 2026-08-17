package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
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

func TestParseRuntimeV2EnvironmentStrict(t *testing.T) {
	valid := map[string]string{"LATTICE_RUNTIME_PROTOCOL": latticeplugin.RuntimeProtocolStdioJSONV2, "LATTICE_RUNTIME_GENERATION": "42"}
	getenv := func(key string) string { return valid[key] }
	if got, err := parseRuntimeV2Environment(getenv); err != nil || got != 42 {
		t.Fatalf("generation=%d error=%v", got, err)
	}
	for name, values := range map[string]map[string]string{
		"nil environment":  nil,
		"wrong protocol":   {"LATTICE_RUNTIME_PROTOCOL": "stdio-json-v1", "LATTICE_RUNTIME_GENERATION": "42"},
		"empty generation": {"LATTICE_RUNTIME_PROTOCOL": latticeplugin.RuntimeProtocolStdioJSONV2},
		"zero generation":  {"LATTICE_RUNTIME_PROTOCOL": latticeplugin.RuntimeProtocolStdioJSONV2, "LATTICE_RUNTIME_GENERATION": "0"},
		"leading zero":     {"LATTICE_RUNTIME_PROTOCOL": latticeplugin.RuntimeProtocolStdioJSONV2, "LATTICE_RUNTIME_GENERATION": "042"},
		"signed":           {"LATTICE_RUNTIME_PROTOCOL": latticeplugin.RuntimeProtocolStdioJSONV2, "LATTICE_RUNTIME_GENERATION": "+42"},
		"overflow":         {"LATTICE_RUNTIME_PROTOCOL": latticeplugin.RuntimeProtocolStdioJSONV2, "LATTICE_RUNTIME_GENERATION": "18446744073709551616"},
	} {
		t.Run(name, func(t *testing.T) {
			var lookup func(string) string
			if values != nil {
				lookup = func(key string) string { return values[key] }
			}
			if _, err := parseRuntimeV2Environment(lookup); err == nil {
				t.Fatal("hostile runtime environment accepted")
			}
		})
	}
}

func TestInvocationHandlerServesTwoCorrelatedV2CallsWithoutHostLeakage(t *testing.T) {
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	hostReader, hostWriter := io.Pipe()
	host := latticeplugin.NewHostClient(latticeplugin.HostClientOptions{Output: outWriter, Responses: hostReader})
	rt := latticeplugin.NewRuntime(latticeplugin.RuntimeOptions{In: inReader, Out: outWriter, Host: host})
	engine := newTestEmbeddedSubStoreEngine()
	base := &runtime{engine: engine}
	production := invocationHandler(base)
	var capturedMu sync.Mutex
	var captured []*latticeplugin.HostClient
	handler := latticeplugin.HandlerFunc(func(ctx context.Context, req latticeplugin.Request, facade *latticeplugin.HostClient) latticeplugin.Response {
		capturedMu.Lock()
		captured = append(captured, facade)
		capturedMu.Unlock()
		return production.HandlePluginRequest(ctx, req, facade)
	})
	done := make(chan error, 1)
	go func() { done <- rt.ServeV2(context.Background(), handler, 7) }()

	type wireFrame struct {
		Protocol     int                    `json:"protocol"`
		Kind         string                 `json:"kind"`
		Generation   uint64                 `json:"generation"`
		InvocationID string                 `json:"invocation_id"`
		HostCallID   string                 `json:"host_call_id"`
		Response     latticeplugin.Response `json:"response"`
	}
	scanner := bufio.NewScanner(outReader)
	scanner.Buffer(make([]byte, 64<<10), latticeplugin.DefaultMaxHostResponseFrameBytes+1)
	readFrame := func() wireFrame {
		t.Helper()
		if !scanner.Scan() {
			t.Fatalf("read v2 frame: %v", scanner.Err())
		}
		var frame wireFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			t.Fatalf("decode v2 frame: %v: %s", err, scanner.Bytes())
		}
		return frame
	}
	assertFrame := func(frame wireFrame, kind, invocation string) {
		t.Helper()
		if frame.Protocol != 2 || frame.Kind != kind || frame.Generation != 7 || frame.InvocationID != invocation {
			t.Fatalf("frame=%+v want kind=%s invocation=%s", frame, kind, invocation)
		}
	}
	assertFrame(readFrame(), "runtime_ready", "runtime")

	requestPayload := mustJSON(callPayload{Service: pluginID + "/engine", Method: "list_pipelines"})
	for invocation := 1; invocation <= 2; invocation++ {
		invocationID := fmt.Sprintf("%d", invocation)
		invoke := map[string]any{"protocol": 2, "kind": "invoke", "generation": 7, "invocation_id": invocationID, "request": request{Action: latticeplugin.ActionCall, Payload: requestPayload}}
		if err := json.NewEncoder(inWriter).Encode(invoke); err != nil {
			t.Fatal(err)
		}
		hostCall := readFrame()
		assertFrame(hostCall, "host_call", invocationID)
		wantHostCallID := fmt.Sprintf("h%d", invocation)
		if hostCall.HostCallID != wantHostCallID {
			t.Fatalf("host_call_id=%q want=%q", hostCall.HostCallID, wantHostCallID)
		}
		hostResult := kvDocumentResponse(t, pipelineRecordsDocument{Version: 1})
		hostResponse := map[string]any{
			"protocol": 2, "kind": "host_response", "generation": 7, "invocation_id": invocationID, "host_call_id": wantHostCallID,
			"host_response": map[string]any{"id": wantHostCallID, "ok": true, "result": json.RawMessage(hostResult)},
		}
		if err := json.NewEncoder(hostWriter).Encode(hostResponse); err != nil {
			t.Fatal(err)
		}
		result := readFrame()
		assertFrame(result, "invoke_result", invocationID)
		if !result.Response.OK {
			t.Fatalf("invoke result=%+v", result.Response)
		}
		assertFrame(readFrame(), "stderr_complete", invocationID)
		assertFrame(readFrame(), "invoke_ready", invocationID)

		capturedMu.Lock()
		facade := captured[invocation-1]
		capturedMu.Unlock()
		if _, _, err := facade.KVGet(context.Background(), "late"); !errors.Is(err, latticeplugin.ErrHostClientExpired) {
			t.Fatalf("invocation %s retained callable host facade: %v", invocationID, err)
		}
		if base.host != nil {
			t.Fatal("base runtime retained invocation host facade")
		}
	}
	if err := inWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = hostWriter.Close()
	_ = outReader.Close()
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
	rt := &runtime{host: host, engine: engine}
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
