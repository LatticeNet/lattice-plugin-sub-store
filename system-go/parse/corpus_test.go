package parse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// conformanceDir is the harness data vendored at the plugin repository root
// (S1 plan section 5.1), or the directory $LATTICE_SUBSTORE_CONFORMANCE
// names. The data is part of the repository, so a missing copy fails the
// test instead of skipping it.
func conformanceDir(t testing.TB) string {
	t.Helper()
	d := os.Getenv("LATTICE_SUBSTORE_CONFORMANCE")
	if d == "" {
		d = filepath.Join("..", "..", "conformance")
	}
	for _, sub := range []string{"corpus/inputs", "goldens/parse", "allowlist/divergences.yaml"} {
		if _, err := os.Stat(filepath.Join(d, sub)); err != nil {
			t.Fatalf("conformance data at %s lacks %s: %v", d, sub, err)
		}
	}
	return d
}

// corpusCase is one corpus input: a .txt file under corpus/inputs/<family>/
// or corpus/fleet/, whose id is the file name without .txt.
type corpusCase struct {
	id, input string
}

func corpusCases(t testing.TB, root string) []corpusCase {
	t.Helper()
	var cases []corpusCase
	seen := map[string]string{}
	for _, dir := range []string{filepath.Join(root, "corpus", "inputs"), filepath.Join(root, "corpus", "fleet")} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".txt") {
				return err
			}
			id := strings.TrimSuffix(d.Name(), ".txt")
			if prev, dup := seen[id]; dup {
				return fmt.Errorf("duplicate case id %s: %s and %s", id, prev, path)
			}
			seen[id] = path
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			cases = append(cases, corpusCase{id: id, input: string(raw)})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].id < cases[j].id })
	return cases
}

// divergence is one entry of allowlist/divergences.yaml, read as the
// harness's oracle/lib/allowlist.mjs reads it.
type divergence struct {
	ID        string   `yaml:"id"`
	Stage     string   `yaml:"stage"`
	Pending   string   `yaml:"pending"`
	Cases     []string `yaml:"cases"`
	Normalise []struct {
		StripKeys   string `yaml:"strip_keys"`
		DropEntries *struct {
			Field  string `yaml:"field"`
			Equals []any  `yaml:"equals"`
		} `yaml:"drop_entries"`
	} `yaml:"normalise"`
}

func loadDivergences(t testing.TB, root string) []divergence {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "allowlist", "divergences.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Divergences []divergence `yaml:"divergences"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Divergences) == 0 {
		t.Fatal("allowlist has no entries")
	}
	return doc.Divergences
}

// applicableParse is applicable(entries, 'parse', null, caseId): entries of
// the parse stage that are not pending and whose case prefixes match.
func applicableParse(entries []divergence, caseID string) []divergence {
	var out []divergence
	for _, e := range entries {
		if e.Pending != "" || e.Stage != "parse" {
			continue
		}
		match := len(e.Cases) == 0
		for _, p := range e.Cases {
			if p == "*" || strings.HasPrefix(caseID, p) {
				match = true
			}
		}
		if match {
			out = append(out, e)
		}
	}
	return out
}

// normaliseValue applies the entries' steps in order, as allowlist.mjs's
// normalise does.
func normaliseValue(t testing.TB, v any, entries []divergence) any {
	for _, e := range entries {
		for _, step := range e.Normalise {
			var re *regexp.Regexp
			if step.StripKeys != "" {
				re = regexp.MustCompile(step.StripKeys)
			}
			v = applyStep(v, re, step.DropEntries)
		}
	}
	return v
}

func applyStep(v any, re *regexp.Regexp, drop *struct {
	Field  string `yaml:"field"`
	Equals []any  `yaml:"equals"`
}) any {
	switch x := v.(type) {
	case []any:
		out := []any{}
		for _, e := range x {
			if m, ok := e.(map[string]any); ok && drop != nil {
				dropped := false
				for _, want := range drop.Equals {
					if got, ok := m[drop.Field]; ok && reflect.DeepEqual(got, want) {
						dropped = true
					}
				}
				if dropped {
					continue
				}
			}
			out = append(out, applyStep(e, re, drop))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if re != nil && re.MatchString(k) {
				continue
			}
			out[k] = applyStep(e, re, drop)
		}
		return out
	}
	return v
}

// firstDiff returns the first path where two decoded JSON values differ,
// keys visited in sorted order, as check.mjs reports it.
func firstDiff(want, got any, path string) string {
	if reflect.DeepEqual(want, got) {
		return ""
	}
	wl, wok := want.([]any)
	gl, gok := got.([]any)
	if wok && gok {
		for i := 0; i < len(wl) || i < len(gl); i++ {
			p := fmt.Sprintf("%s[%d]", path, i)
			if i >= len(wl) || i >= len(gl) {
				return p + ": length differs"
			}
			if d := firstDiff(wl[i], gl[i], p); d != "" {
				return d
			}
		}
	}
	wm, wok := want.(map[string]any)
	gm, gok := got.(map[string]any)
	if wok && gok {
		keys := map[string]bool{}
		for k := range wm {
			keys[k] = true
		}
		for k := range gm {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			wv, win := wm[k]
			gv, gin := gm[k]
			if !win || !gin {
				return fmt.Sprintf("%s.%s: golden %s, got %s", path, k, clipJSON(wv, win), clipJSON(gv, gin))
			}
			if d := firstDiff(wv, gv, path+"."+k); d != "" {
				return d
			}
		}
	}
	return fmt.Sprintf("%s: golden %s, got %s", path, clipJSON(want, true), clipJSON(got, true))
}

func clipJSON(v any, present bool) string {
	if !present {
		return "<missing>"
	}
	b, _ := json.Marshal(v)
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}
	return string(b)
}

// caseResult is the outcome of one corpus case.
type caseResult struct {
	pass          bool
	allowed       []string // allowlist entries the pass needed
	diff          string
	unimplemented []string // grammars the case reached that have not landed
}

// runCorpusCase parses one case and compares it with its parse golden:
// key-sorted deep equality first, then again under the applicable allowlist
// normalisations, as check.mjs compares.
func runCorpusCase(t testing.TB, root string, c corpusCase, entries []divergence) caseResult {
	t.Helper()
	goldenRaw, err := os.ReadFile(filepath.Join(root, "goldens", "parse", c.id+".json"))
	if err != nil {
		t.Fatalf("%s: no parse golden: %v", c.id, err)
	}
	var golden any
	if err := json.Unmarshal(goldenRaw, &golden); err != nil {
		t.Fatalf("%s: golden: %v", c.id, err)
	}
	labels := map[string]bool{}
	nodes, _, perr := document(c.input, Options{}, func(label string) { labels[label] = true })
	var r caseResult
	for l := range labels {
		r.unimplemented = append(r.unimplemented, l)
	}
	sort.Strings(r.unimplemented)

	if m, ok := golden.(map[string]any); ok && m["error"] == true {
		r.pass = perr != nil
		if !r.pass {
			r.diff = "$: golden is a whole-document failure, got nodes"
		}
		return r
	}
	if perr != nil {
		r.diff = "$: golden has nodes, got error " + perr.Error()
		return r
	}
	got := []any{}
	for _, n := range nodes {
		encoded, err := n.MarshalJSON()
		if err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		var v any
		if err := json.Unmarshal(encoded, &v); err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		got = append(got, v)
	}
	if reflect.DeepEqual(golden, got) {
		r.pass = true
		return r
	}
	applicable := applicableParse(entries, c.id)
	if len(applicable) > 0 {
		ng, nc := normaliseValue(t, golden, applicable), normaliseValue(t, got, applicable)
		if reflect.DeepEqual(ng, nc) {
			r.pass = true
			for _, e := range applicable {
				r.allowed = append(r.allowed, e.ID)
			}
			return r
		}
		r.diff = firstDiff(ng, nc, "$")
		return r
	}
	r.diff = firstDiff(golden, got, "$")
	return r
}

// TestParseCorpusMatchesGoldens runs every vendored corpus case through
// Document and compares the nodes with goldens/parse, reporting each case.
// A case that reaches a grammar which has not landed yet is listed as
// pending under that grammar, never skipped silently; once every grammar
// has landed nothing can be pending and every case must pass.
func TestParseCorpusMatchesGoldens(t *testing.T) {
	root := conformanceDir(t)
	cases := corpusCases(t, root)
	entries := loadDivergences(t, root)
	if len(cases) == 0 {
		t.Fatal("no corpus cases")
	}
	pending := map[string][]string{}
	passed, failed, allowed := 0, 0, map[string]int{}
	for _, c := range cases {
		r := runCorpusCase(t, root, c, entries)
		t.Run(c.id, func(t *testing.T) {
			switch {
			case len(r.unimplemented) > 0:
				// A case that reaches a grammar which has not landed is pending
				// even when it matches: its golden may hold no nodes only
				// because the grammar rejects the line today.
				for _, l := range r.unimplemented {
					pending[l] = append(pending[l], c.id)
				}
				state := "differs: " + r.diff
				if r.pass {
					state = "matches so far"
				}
				t.Skipf("pending on %s, which has not landed; %s", strings.Join(r.unimplemented, ", "), state)
			case r.pass:
				passed++
				for _, id := range r.allowed {
					allowed[id]++
				}
			default:
				failed++
				t.Errorf("parse differs from the golden: %s", r.diff)
			}
		})
	}
	labels := make([]string, 0, len(pending))
	total := 0
	for l, ids := range pending {
		labels = append(labels, l)
		total += len(ids)
	}
	sort.Strings(labels)
	t.Logf("%d cases: %d passed, %d failed, %d pending; allowlist entries used: %v", len(cases), passed, failed, len(cases)-passed-failed, allowed)
	for _, l := range labels {
		t.Logf("pending on %s (%d): %s", l, len(pending[l]), strings.Join(pending[l], " "))
	}
}
