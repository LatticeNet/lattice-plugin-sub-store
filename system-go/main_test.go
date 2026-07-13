package main

import (
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
	resp := rt.handleCall(call)
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

func TestSubStorePlanRedactsSecretValues(t *testing.T) {
	plan := renderPlan(json.RawMessage(`{"base_url":"https://sub.example/very-secret","user_id":"user-1","sub_name":"managed"}`))
	if strings.Contains(plan, "very-secret") || !strings.Contains(plan, "base_url = <redacted>") {
		t.Fatalf("plan leaked secret: %s", plan)
	}
}
