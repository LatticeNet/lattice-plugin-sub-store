package main

import (
	"strings"
	"testing"
)

func publishRuntime(t *testing.T, status int) (*runtime, *httpKVHost) {
	t.Helper()
	host := &httpKVHost{kvHostCaller: newKVHostCaller(), status: status, body: []byte(`{"ok":true}`)}
	rt := &runtime{host: host, engine: testEngineWithHeadroom()}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", Name: "one", Content: previewFixture}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return rt, host
}

func TestPublishSendsRenderedContent(t *testing.T) {
	rt, _ := publishRuntime(t, 200)
	out, err := rt.publishSubscription("s1", "https://dest.invalid/put", "PUT", "plain")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if out.Bytes == 0 || out.StatusCode != 200 {
		t.Fatalf("result = %+v", out)
	}
}

// Publishing an empty config would overwrite a good destination with nothing,
// which is the same destructive shape the serve path refuses.
func TestPublishRefusesEmptyContent(t *testing.T) {
	rt, _ := publishRuntime(t, 200)
	if err := rt.saveSubscription(subscriptionRecord{ID: "empty", Name: "empty", Content: "  "}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := rt.publishSubscription("empty", "https://dest.invalid/put", "PUT", "plain"); err == nil {
		t.Fatal("an empty subscription was published")
	}
}

// Allowing GET or DELETE would turn publishing into a general-purpose request
// proxy that happens to hold an operator target.
func TestPublishRefusesNonWriteMethods(t *testing.T) {
	rt, _ := publishRuntime(t, 200)
	for _, method := range []string{"GET", "DELETE", "HEAD", "nonsense"} {
		if _, err := rt.publishSubscription("s1", "https://dest.invalid/x", method, "plain"); err == nil {
			t.Fatalf("method %q was accepted", method)
		}
	}
}

func TestPublishDefaultsToPut(t *testing.T) {
	rt, _ := publishRuntime(t, 201)
	out, err := rt.publishSubscription("s1", "https://dest.invalid/put", "", "plain")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if out.StatusCode != 201 {
		t.Fatalf("status = %d", out.StatusCode)
	}
}

func TestPublishRejectsNonSuccessStatus(t *testing.T) {
	rt, _ := publishRuntime(t, 422)
	if _, err := rt.publishSubscription("s1", "https://dest.invalid/put", "PUT", "plain"); err == nil {
		t.Fatal("a 422 was treated as a successful publish")
	}
}

func TestPublishNeedsADestination(t *testing.T) {
	rt, _ := publishRuntime(t, 200)
	if _, err := rt.publishSubscription("s1", "   ", "PUT", "plain"); err == nil {
		t.Fatal("publish accepted an empty destination")
	}
}

// A destination is frequently a credential in URL form, so it must not survive
// into an error that reaches the audit log.
func TestPublishErrorsRedactTheDestination(t *testing.T) {
	host := &httpKVHost{kvHostCaller: newKVHostCaller(), httpErr: errJSON("dial https://dest.invalid/secret-token: refused")}
	rt := &runtime{host: host, engine: testEngineWithHeadroom()}
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", Content: previewFixture}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := rt.publishSubscription("s1", "https://dest.invalid/secret-token", "PUT", "plain")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("the destination leaked: %v", err)
	}
}
