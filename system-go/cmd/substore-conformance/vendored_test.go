package main

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// conformanceDir is the vendored copy of the conformance harness at the
// plugin repository root (tools/conformance-sync.sh writes it).
const conformanceDir = "../../../conformance"

// vendoredPaths are the harness paths tools/conformance-sync.sh copies. Keep
// the two lists in step. specs/ is not here and never may be: the harness's
// specifications are written from reading upstream Sub-Store.
var vendoredPaths = []string{
	".gitattributes",
	"corpus",
	"goldens",
	"allowlist",
	"oracle/check.mjs",
	"oracle/lib",
	"oracle/upstream.json",
	"oracle/package.json",
	"oracle/package-lock.json",
}

// pluginOwned are the files under conformance/ that do not come from the
// harness: the commit the copy came from, and the plugin's own numbers.
var pluginOwned = []string{"HARNESS_COMMIT", "conformance.json"}

// The markers the harness's oracle/test/upstream-free.test.mjs scans for: the
// licence header of upstream's LICENSE, its backend package name, the "@/"
// module alias its source files import with, the source path comments an
// unminified bundle of it carries, and the names of its source tree, its
// entries, the oracle bundle and the bundle this plugin embeds.
var upstreamContentMarkers = []struct {
	what string
	re   *regexp.Regexp
}{
	{"upstream's AGPL licence header", regexp.MustCompile(`GNU AFFERO GENERAL PUBLIC LICENSE`)},
	{"upstream's backend package name", regexp.MustCompile(`"name"\s*:\s*"sub-store"`)},
	{"upstream's @/ module alias", regexp.MustCompile(`(?:\bfrom|\bimport|\brequire\()\s*['"]@/`)},
	{"a bundle of upstream's source", regexp.MustCompile(`(?m)^// src/(?:core|utils|products|vendor)/`)},
}

var upstreamPathMarkers = []struct {
	what string
	re   *regexp.Regexp
}{
	{"upstream's source tree", regexp.MustCompile(`(?:^|/)backend/src/`)},
	{"upstream's ProxyUtils entry or the oracle bundle", regexp.MustCompile(`(?:^|/)proxy-utils\.(?:esm\.js|cjs)$`)},
	{"upstream's server entries", regexp.MustCompile(`(?:^|/)sub-store-[01]\.js$`)},
	{"the plugin's embedded bundle", regexp.MustCompile(`(?:^|/)substore-core\.js$`)},
}

// vendoredViolations reports every file that is outside the paths the sync
// copies, or that looks like upstream source or a bundle of it.
func vendoredViolations(rel string, text []byte) []string {
	var out []string
	if !isVendoredPath(rel) && !slices.Contains(pluginOwned, rel) {
		out = append(out, rel+": not a path tools/conformance-sync.sh copies")
	}
	for _, m := range upstreamPathMarkers {
		if m.re.MatchString(rel) {
			out = append(out, rel+": path looks like "+m.what)
		}
	}
	for _, m := range upstreamContentMarkers {
		if m.re.Match(text) {
			out = append(out, rel+": contains "+m.what)
		}
	}
	return out
}

func isVendoredPath(rel string) bool {
	for _, p := range vendoredPaths {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// checkerInstall is where `npm ci --prefix conformance/oracle` installs the
// checker's one dependency. It is ignored by git, so it is never part of the
// vendored copy.
const checkerInstall = "oracle/node_modules"

// walkConformance returns every file under conformance/ by its slash path
// relative to that directory, and fails on anything that is not a regular
// file: a symbolic link could point the checker anywhere.
func walkConformance(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(conformanceDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(conformanceDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == checkerInstall {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			t.Errorf("%s: not a regular file", rel)
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HARNESS_COMMIT", "oracle/check.mjs", "allowlist/divergences.yaml"} {
		if !slices.Contains(files, want) {
			t.Fatalf("conformance/%s is missing; run tools/conformance-sync.sh", want)
		}
	}
	return files
}

// The public plugin repository may carry the harness's checker and synthetic
// data, never upstream text. This is the harness's own upstream-free scan run
// over the vendored copy, plus the sync's path allowlist, so neither a hand
// copy nor a widened sync can publish specs/ or upstream source by accident.
func TestVendoredDataCarriesNoUpstreamText(t *testing.T) {
	var violations []string
	for _, rel := range walkConformance(t) {
		text, err := os.ReadFile(filepath.Join(conformanceDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		violations = append(violations, vendoredViolations(rel, text)...)
	}
	if len(violations) > 0 {
		t.Fatalf("conformance/ carries files it must not:\n%s", strings.Join(violations, "\n"))
	}
}

// Each rule of the scan reports what it is for, and clean text, including
// prose that names upstream, reports nothing.
func TestVendoredScanReportsEachMarker(t *testing.T) {
	for _, c := range []struct {
		rel, text string
		hits      int
	}{
		{"specs/parser.md", "", 1},
		{"oracle/run.mjs", "", 1},
		{"corpus/LICENSE", "                    GNU AFFERO GENERAL PUBLIC LICENSE\n", 1},
		{"oracle/package.json", "{\n  \"name\": \"sub-store\",\n  \"version\": \"2.42.3\"\n}\n", 1},
		{"oracle/lib/producer.mjs", "import $ from '@/core/app';\n", 1},
		{"oracle/lib/utils.mjs", "const { x } = require('@/utils');\n", 1},
		{"oracle/lib/vendor.mjs", "var x = 1;\n\n// src/core/proxy-utils/index.js\nvar y = 2;\n", 1},
		{"goldens/backend/src/core/proxy-utils/index.js", "", 1},
		{"oracle/lib/proxy-utils.cjs", "", 1},
		{"corpus/proxy-utils.esm.js", "", 1},
		{"corpus/sub-store-1.js", "", 1},
		{"oracle/lib/substore-core.js", "", 1},
		{"oracle/check.mjs", "// Upstream (package sub-store, AGPL-3.0) imports through its @/ alias from src/core/.\n", 0},
		{"HARNESS_COMMIT", "573977b5106225f95ad103eb5ea89338359863cd\n", 0},
		{"conformance.json", "{}\n", 0},
	} {
		if got := vendoredViolations(c.rel, []byte(c.text)); len(got) != c.hits {
			t.Errorf("%s: %d violations %q, want %d", c.rel, len(got), got, c.hits)
		}
	}
}

// harnessDir is the conformance harness checkout the vendored copy is held
// to: LATTICE_SUBSTORE_CONFORMANCE, or the sibling of this repository on the
// workspace desk.
func harnessDir() string {
	if dir := os.Getenv("LATTICE_SUBSTORE_CONFORMANCE"); dir != "" {
		return dir
	}
	return filepath.Join("..", "..", "..", "..", "lattice-substore-conformance")
}

// The vendored copy must be exactly the harness's files at HARNESS_COMMIT:
// check.mjs first of all, and with it every corpus case, golden, the
// allowlist and the checker's library, so a hand edit cannot drift silently.
// It needs the private harness checkout, so CI, which has none, skips it.
func TestVendoredCheckerMatchesHarnessCommit(t *testing.T) {
	harness := harnessDir()
	if out, err := exec.Command("git", "-C", harness, "rev-parse", "--git-dir").CombinedOutput(); err != nil {
		t.Skipf("no conformance harness checkout at %s (set LATTICE_SUBSTORE_CONFORMANCE): %s", harness, bytes.TrimSpace(out))
	}
	raw, err := os.ReadFile(filepath.Join(conformanceDir, "HARNESS_COMMIT"))
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(string(raw))
	if !regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`).MatchString(commit) {
		t.Fatalf("conformance/HARNESS_COMMIT is %q, want one full commit id", commit)
	}

	args := append([]string{"-C", harness, "ls-tree", "-r", "-z", commit, "--"}, vendoredPaths...)
	listing, err := exec.Command("git", args...).Output()
	if err != nil {
		t.Fatalf("listing %s in %s: %v (fetch the harness)", commit, harness, err)
	}
	want := map[string]string{} // path -> blob id
	for _, entry := range strings.Split(strings.TrimSuffix(string(listing), "\x00"), "\x00") {
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" || fields[0] == "120000" {
			t.Fatalf("unexpected harness tree entry %q", entry)
		}
		want[path] = fields[2]
	}
	if _, ok := want["oracle/check.mjs"]; !ok {
		t.Fatalf("the harness has no oracle/check.mjs at %s", commit)
	}

	var drift []string
	for _, rel := range walkConformance(t) {
		if slices.Contains(pluginOwned, rel) {
			continue
		}
		id, ok := want[rel]
		if !ok {
			drift = append(drift, rel+": not in the harness at "+commit)
			continue
		}
		delete(want, rel)
		b, err := os.ReadFile(filepath.Join(conformanceDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if got := gitBlobID(b, len(id)); got != id {
			drift = append(drift, rel+": differs from the harness at "+commit)
		}
	}
	for rel := range want {
		drift = append(drift, rel+": in the harness at "+commit+" but missing here")
	}
	if len(drift) > 0 {
		slices.Sort(drift)
		if len(drift) > 20 {
			drift = append(drift[:20], fmt.Sprintf("... and %d more", len(drift)-20))
		}
		t.Fatalf("conformance/ does not match the harness; rerun tools/conformance-sync.sh:\n%s", strings.Join(drift, "\n"))
	}
}

// gitBlobID is the object id git gives a file's bytes: SHA-1 for a 40-digit
// id, SHA-256 for a 64-digit one.
func gitBlobID(b []byte, digits int) string {
	var h hash.Hash = sha1.New()
	if digits == 64 {
		h = sha256.New()
	}
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}
