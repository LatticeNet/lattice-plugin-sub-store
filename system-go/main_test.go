package main

import (
	"strings"
	"testing"
)

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

func TestSubStoreStatusRejectsInvalidURLBeforeHostCall(t *testing.T) {
	rt := &runtime{}

	out := string(rt.status(subStoreRequest{BaseURL: "https://sub.example.com"}))

	if !strings.Contains(out, `"reachable":false`) {
		t.Fatalf("status response = %s, want unreachable", out)
	}
	if !strings.Contains(out, "secret path") {
		t.Fatalf("status response = %s, want secret path validation error", out)
	}
}
