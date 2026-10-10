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
	ID      string
	Stage   string
	Pending string
	Cases   []string
	Steps   []normaliseStep
}

// normaliseStep is one normalisation of an entry. Exactly one kind is set.
type normaliseStep struct {
	stripKeys  *regexp.Regexp  // strip_keys: delete matching keys at any depth
	dropField  string          // drop_entries: delete objects whose field
	dropEquals []any           // equals one of the values
	dropKeys   map[string]bool // drop_entries_with_keys, lower-cased
	// without_line_breaking_nodes changes no value: at produce stage it
	// lets a failing case match its line-safe golden, so a parse comparison
	// never applies it (the harness refuses it at parse stage).
	lineSafe bool
}

// loadDivergences reads the allowlist and fails on a normalisation kind
// this reader does not implement, so a schema change in the harness cannot
// be ignored silently.
func loadDivergences(t testing.TB, root string) []divergence {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "allowlist", "divergences.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Divergences []struct {
			ID        string           `yaml:"id"`
			Stage     string           `yaml:"stage"`
			Pending   string           `yaml:"pending"`
			Cases     []string         `yaml:"cases"`
			Normalise []map[string]any `yaml:"normalise"`
		} `yaml:"divergences"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Divergences) == 0 {
		t.Fatal("allowlist has no entries")
	}
	var out []divergence
	for _, e := range doc.Divergences {
		d := divergence{ID: e.ID, Stage: e.Stage, Pending: e.Pending, Cases: e.Cases}
		for _, step := range e.Normalise {
			if len(step) != 1 {
				t.Fatalf("allowlist %s: a normalise step has %d kinds, want one", e.ID, len(step))
			}
			var ns normaliseStep
			for kind, v := range step {
				switch kind {
				case "strip_keys":
					pattern, _ := v.(string)
					ns.stripKeys = regexp.MustCompile(pattern)
				case "drop_entries":
					m, _ := v.(map[string]any)
					ns.dropField, _ = m["field"].(string)
					ns.dropEquals, _ = m["equals"].([]any)
					if ns.dropField == "" || len(ns.dropEquals) == 0 {
						t.Fatalf("allowlist %s: drop_entries needs field and equals", e.ID)
					}
				case "drop_entries_with_keys":
					keys, _ := v.([]any)
					ns.dropKeys = map[string]bool{}
					for _, k := range keys {
						s, _ := k.(string)
						ns.dropKeys[strings.ToLower(s)] = true
					}
					if len(ns.dropKeys) == 0 {
						t.Fatalf("allowlist %s: drop_entries_with_keys needs keys", e.ID)
					}
				case "without_line_breaking_nodes":
					if v != true || e.Stage != "produce" {
						t.Fatalf("allowlist %s: without_line_breaking_nodes takes true and applies only at produce stage", e.ID)
					}
					ns.lineSafe = true
				default:
					t.Fatalf("allowlist %s: normalise kind %q is not implemented by this test", e.ID, kind)
				}
			}
			d.Steps = append(d.Steps, ns)
		}
		out = append(out, d)
	}
	return out
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
func normaliseValue(v any, entries []divergence) any {
	for _, e := range entries {
		for _, step := range e.Steps {
			v = applyStep(v, step, "")
		}
	}
	return v
}

func applyStep(v any, step normaliseStep, parentKey string) any {
	switch x := v.(type) {
	case []any:
		out := []any{}
		for _, e := range x {
			if m, ok := e.(map[string]any); ok && dropEntry(m, step) {
				continue
			}
			if pair, ok := e.([]any); ok && step.stripKeys != nil && parentKey == "query" && len(pair) > 0 {
				if k, isString := pair[0].(string); isString && step.stripKeys.MatchString(k) {
					continue
				}
			}
			out = append(out, applyStep(e, step, ""))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if step.stripKeys != nil && step.stripKeys.MatchString(k) {
				continue
			}
			out[k] = applyStep(e, step, k)
		}
		return out
	}
	return v
}

func dropEntry(m map[string]any, step normaliseStep) bool {
	if step.dropField != "" {
		if got, ok := m[step.dropField]; ok {
			for _, want := range step.dropEquals {
				if reflect.DeepEqual(got, want) {
					return true
				}
			}
		}
	}
	for k := range m {
		if step.dropKeys[strings.ToLower(k)] {
			return true
		}
	}
	return false
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
	pass    bool
	allowed []string // allowlist entries the pass needed
	diff    string
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
	nodes, _, perr := Document(c.input, Options{})
	var r caseResult

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
		ng, nc := normaliseValue(golden, applicable), normaliseValue(got, applicable)
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
// Every grammar of parser.md has landed, so every case must match, either
// exactly or under the allowlist entries that apply to it.
func TestParseCorpusMatchesGoldens(t *testing.T) {
	root := conformanceDir(t)
	cases := corpusCases(t, root)
	entries := loadDivergences(t, root)
	if len(cases) == 0 {
		t.Fatal("no corpus cases")
	}
	passed, failed, allowed := 0, 0, map[string]int{}
	for _, c := range cases {
		r := runCorpusCase(t, root, c, entries)
		t.Run(c.id, func(t *testing.T) {
			if !r.pass {
				failed++
				t.Errorf("parse differs from the golden: %s", r.diff)
				return
			}
			passed++
			for _, id := range r.allowed {
				allowed[id]++
			}
		})
	}
	t.Logf("%d cases: %d passed, %d failed; cases that matched only under the allowlist, by entry applied: %v", len(cases), passed, failed, allowed)
}

// fatalRecorder stands in for a test so loadDivergences's refusal can be
// observed without failing the caller.
type fatalRecorder struct {
	testing.TB
	fatal string
}

func (r *fatalRecorder) Helper() {}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	if r.fatal == "" {
		r.fatal = fmt.Sprintf(format, args...)
	}
}

// TestAllowlistReaderFollowsTheHarnessSchema holds the Go reader to
// allowlist.mjs: the vendored entries apply as the harness applies them,
// key presence matched without regard to case, and a normalisation kind
// the reader does not implement stops the test instead of being skipped.
func TestAllowlistReaderFollowsTheHarnessSchema(t *testing.T) {
	entries := loadDivergences(t, conformanceDir(t))
	nodes := []any{
		map[string]any{"type": "ss", "name": "keep", "_x": 1.0, "o": map[string]any{"_y": 2.0}},
		map[string]any{"type": "external", "name": "drop by type"},
		map[string]any{"type": "tuic", "name": "drop by key", "EXEC": "x"},
		map[string]any{"type": "ssr", "name": "drop by key", "local_address": "x"},
	}
	clash := normaliseValue(nodes, applicableParse(entries, "clash-example"))
	want := []any{map[string]any{"type": "ss", "name": "keep", "o": map[string]any{}}}
	if !reflect.DeepEqual(clash, want) {
		t.Errorf("clash- case normalised to %#v, want %#v", clash, want)
	}
	other := normaliseValue(nodes, applicableParse(entries, "tuic-example"))
	want = []any{map[string]any{"type": "ss", "name": "keep", "_x": 1.0, "o": map[string]any{"_y": 2.0}}}
	if !reflect.DeepEqual(other, want) {
		t.Errorf("tuic- case normalised to %#v, want %#v", other, want)
	}

	// line-safety is a produce-stage entry: it loads, and no parse
	// comparison ever applies it.
	for _, id := range []string{"clash-socks5-name-newline", "clash-ssh"} {
		for _, e := range applicableParse(entries, id) {
			if e.ID == "line-safety" {
				t.Errorf("%s: the parse comparison applies line-safety", id)
			}
		}
	}

	for _, tc := range []struct{ yaml, want string }{
		{"divergences:\n  - id: x\n    stage: parse\n    normalise:\n      - rename_keys: a\n", "rename_keys"},
		{"divergences:\n  - id: x\n    stage: parse\n    normalise:\n      - without_line_breaking_nodes: true\n", "only at produce stage"},
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "allowlist"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "allowlist", "divergences.yaml"), []byte(tc.yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		r := &fatalRecorder{TB: t}
		loadDivergences(r, dir)
		if !strings.Contains(r.fatal, tc.want) {
			t.Errorf("allowlist %q gave %q, want the test stopped with %q", tc.yaml, r.fatal, tc.want)
		}
	}
}
