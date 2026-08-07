package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadYAML reads a document with the same parser a merged config passed through
// on its way out, and hands it back as JSON so a test can assert on structure.
//
// Going through the engine rather than a Go YAML library is the point: it proves
// the output is readable by the parser that actually matters, and it keeps the
// plugin's dependency set unchanged.
func loadYAML(t *testing.T, rt *runtime, doc string, into any) {
	t.Helper()
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}
	script := fmt.Sprintf(`(function() {
  const root = globalThis.SubStoreProxyUtils;
  const core = root && root.ProxyUtils ? root.ProxyUtils : root;
  return JSON.stringify(core.yaml.safeLoad(%s));
})()`, encoded)
	out, err := rt.subStoreEngine().runCoreScript("yaml load", "yaml-load.js", script)
	if err != nil {
		t.Fatalf("the output is not readable YAML: %v\n%s", err, doc)
	}
	if err := json.Unmarshal([]byte(out), into); err != nil {
		t.Fatalf("decode parsed document: %v", err)
	}
}

// showSubStoreEngineErrors makes engine failures carry their original text for
// the duration of one test.
//
// Use it while diagnosing, and when a test asserts on what a failure actually
// says. Callers must not be parallel: the flag is process-wide.
func showSubStoreEngineErrors(t *testing.T) {
	t.Helper()
	previous := substoreEngineRawErrors
	substoreEngineRawErrors = true
	t.Cleanup(func() { substoreEngineRawErrors = previous })
}

// The escape hatch is only safe because nothing outside the tests can open it.
// A single assignment in shipped code would turn redaction into a default-off
// protection without anyone noticing.
func TestOnlyTestsCanUnredactEngineErrors(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			// The declaration is the one place shipped code names it.
			if strings.HasPrefix(trimmed, "var substoreEngineRawErrors") {
				continue
			}
			if strings.Contains(trimmed, "substoreEngineRawErrors =") || strings.Contains(trimmed, "substoreEngineRawErrors=") {
				t.Fatalf("%s:%d assigns substoreEngineRawErrors; redaction must only be lifted from tests", name, i+1)
			}
		}
	}
}

func TestEngineErrorsAreRedactedByDefault(t *testing.T) {
	if substoreEngineRawErrors {
		t.Fatal("the package starts with engine error redaction disabled")
	}
}
