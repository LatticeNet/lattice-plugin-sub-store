package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The embedded bundle is AGPL-3.0 upstream code inside an MIT plugin, so the
// notice must name exactly what ships: the pinned upstream commit, version
// and the SHA-256 of the file in lib/. A re-pin that forgets the notice fails
// here.
func TestThirdPartyNoticeNamesTheShippedBundle(t *testing.T) {
	notice, err := os.ReadFile("../THIRD_PARTY_NOTICES.md")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../tools/substore-core/pin.json")
	if err != nil {
		t.Fatal(err)
	}
	var pin struct {
		Commit         string `json:"commit"`
		BackendVersion string `json:"backend_version"`
		OutputSHA256   string `json:"output_sha256"`
	}
	if err := json.Unmarshal(raw, &pin); err != nil {
		t.Fatal(err)
	}
	bundle, err := os.ReadFile("lib/substore-core.js")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bundle)
	shipped := hex.EncodeToString(sum[:])
	if shipped != pin.OutputSHA256 {
		t.Fatalf("lib/substore-core.js is %s but pin.json records %s", shipped, pin.OutputSHA256)
	}
	for what, want := range map[string]string{"licence": "AGPL-3.0", "commit": pin.Commit, "version": pin.BackendVersion, "sha256": shipped} {
		if want == "" || !strings.Contains(string(notice), want) {
			t.Fatalf("THIRD_PARTY_NOTICES.md does not name the bundle's %s %q", what, want)
		}
	}
}
