package operators

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Arguments the bundle refuses or reads in a way the native operator does
// not are left to the bundle: the step falls back with argument_shape.
func TestUnreadArgumentsFallBack(t *testing.T) {
	for _, step := range []string{
		`{"type":"Regex Filter","args":{"regex":"HK"}}`,
		`{"type":"Regex Delete Operator","args":"HK"}`,
		`{"type":"Regex Delete Operator","args":{"value":["HK"]}}`,
		`{"type":"Regex Rename Operator","args":{"value":[{"expr":"a","now":"b"}]}}`,
		`{"type":"Sort Operator"}`,
		`{"type":"Sort Operator","args":"xyz"}`,
		`{"type":"Regex Sort Operator","args":{"expressions":["a"],"order":"random"}}`,
		`{"type":"Handle Duplicate Operator","args":{"field":"server"}}`,
		`{"type":"Handle Duplicate Operator","args":{"field":["a[0]"]}}`,
		`{"type":"Type Filter","args":{}}`,
		`{"type":"Region Filter","args":{"value":"HK"}}`,
		`{"type":"Quick Setting Operator","args":["udp"]}`,
	} {
		plan, err := Compile("r", []json.RawMessage{json.RawMessage(step)})
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		s := plan.Steps[0]
		if s.Kind != KindFallback || s.Run != nil || len(s.Diag) == 0 || s.Diag[0].Code != CodeArgumentShape {
			t.Errorf("%s: kind %d diag %+v", step, s.Kind, s.Diag)
		}
	}
}

// Each kind of step is classified, and the plan's questions answer from the
// classification.
func TestCompileClassifiesSteps(t *testing.T) {
	steps := []json.RawMessage{
		json.RawMessage(`{"type":"Flag Operator","args":{"mode":"add"}}`),
		json.RawMessage(`{"type":"Sort Operator","args":"asc","disabled":true}`),
		json.RawMessage(`{"type":"Response Transformer","args":{"mode":"script","content":"x"}}`),
	}
	plan, err := Compile("rev-1", steps)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Revision != "rev-1" || plan.Steps[0].Kind != KindNative || plan.Steps[1].Kind != KindDisabled || plan.Steps[2].Kind != KindResponse {
		t.Fatalf("kinds = %d %d %d", plan.Steps[0].Kind, plan.Steps[1].Kind, plan.Steps[2].Kind)
	}
	if !plan.Native() || plan.HasFallback() || len(plan.ResponseSteps()) != 1 || plan.ResponseSteps()[0].Type != "Response Transformer" {
		t.Fatalf("native=%v fallback=%v response=%+v", plan.Native(), plan.HasFallback(), plan.ResponseSteps())
	}
	if string(plan.Steps[0].Step.Raw) != string(steps[0]) || plan.Steps[1].Step.CustomName != "" || !plan.Steps[1].Step.Disabled {
		t.Fatalf("step decode: %+v", plan.Steps[:2])
	}
	// The disabled sort does not run.
	wantNames(t, plan.Run(named(t, "b HK", "a JP"), nil), "🇭🇰 b HK", "🇯🇵 a JP")

	for _, typ := range []string{"Resolve Domain Operator", "Script Operator", "Script Filter"} {
		plan, err := Compile("", []json.RawMessage{json.RawMessage(fmt.Sprintf(`{"type":%q,"args":{}}`, typ))})
		if err != nil || plan.Native() || !plan.HasFallback() || plan.Steps[0].Kind != KindFallback {
			t.Errorf("%s: native=%v fallback=%v err=%v", typ, plan.Native(), plan.HasFallback(), err)
		}
	}
	for _, bad := range []string{`{"type":"Nope"}`, `{"args":{}}`, `[1]`, `{"type":"Flag Operator","disabled":"yes"}`} {
		if _, err := Compile("", []json.RawMessage{json.RawMessage(bad)}); err == nil {
			t.Errorf("%s compiled", bad)
		}
	}
}

// A pattern RE2 refuses does not fail Compile: the step falls back with a
// regex_incompatible diagnostic, the rewrite is offered for the keep-mode
// negative-lookahead idiom, and CompileStrict refuses with both.
func TestCompileFlagsLookaheadAndOffersRewrite(t *testing.T) {
	steps := []json.RawMessage{
		json.RawMessage(`{"type":"Sort Operator","args":"asc"}`),
		json.RawMessage(`{"type":"Regex Filter","args":{"regex":["^(?!.*(HK|TW)).*$"],"keep":true}}`),
		json.RawMessage(`{"type":"Regex Delete Operator","args":["(?<=x)y"]}`),
		json.RawMessage(`{"type":"Regex Rename Operator","args":[{"expr":"(a)\\1","now":"b"}]}`),
		json.RawMessage(`{"type":"Regex Sort Operator","args":{"expressions":["(?=a)"]}}`),
		json.RawMessage(`{"type":"Regex Filter","args":{"regex":["^(?!.*JP).*$","US"]}}`),
		json.RawMessage(`{"type":"Regex Filter","args":{"regex":["^(?!.*JP).*$"],"keep":false}}`),
		json.RawMessage(`{"type":"Regex Filter","disabled":true,"args":{"regex":["(?!x)"]}}`),
	}
	plan, err := Compile("r", steps)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Native() || plan.HasFallback() {
		t.Fatalf("native=%v has_fallback=%v, want a non-native plan with no fallback-type step", plan.Native(), plan.HasFallback())
	}
	inc := plan.Incompatible()
	type got struct {
		step             int
		pattern, rewrite string
	}
	var all []got
	for _, d := range inc {
		if d.Code != CodeRegexIncompatible || !strings.Contains(d.Message, strconv.Quote(d.Pattern)) {
			t.Errorf("diagnostic %+v", d)
		}
		all = append(all, got{d.Step, d.Pattern, d.Rewrite})
	}
	want := []got{
		{2, "^(?!.*(HK|TW)).*$", "HK|TW"},
		{3, "(?<=x)y", ""},
		{4, `(a)\1`, ""},
		{5, "(?=a)", ""},
		{6, "^(?!.*JP).*$", ""}, // beside another pattern: no rewrite
		{7, "^(?!.*JP).*$", ""}, // drop mode: no rewrite
	}
	if !slices.Equal(all, want) {
		t.Fatalf("diagnostics = %+v\nwant %+v", all, want)
	}
	for i, s := range plan.Steps {
		wantKind := KindFallback
		switch i {
		case 0:
			wantKind = KindNative
		case 7:
			wantKind = KindDisabled
		}
		if s.Kind != wantKind {
			t.Errorf("step %d kind %d, want %d", i+1, s.Kind, wantKind)
		}
	}

	if _, err := CompileStrict(steps[:1]); err != nil {
		t.Fatalf("a compatible chain was refused: %v", err)
	}
	_, err = CompileStrict(steps[:2])
	refusal, ok := AsIncompatible(err)
	if !ok || len(refusal.Diagnostics) != 1 {
		t.Fatalf("CompileStrict = %v", err)
	}
	msg := err.Error()
	for _, part := range []string{"regex_incompatible: ", "process step 2", `"^(?!.*(HK|TW)).*$"`, `drop-mode Regex Filter on "HK|TW"`} {
		if !strings.HasPrefix(msg, "regex_incompatible: ") || !strings.Contains(msg, part) {
			t.Fatalf("refusal %q lacks %q", msg, part)
		}
	}
}

// Every step runs in place: a native chain mutates the nodes it is handed,
// in order, without cloning them.
func TestRunMutatesInPlaceInOrder(t *testing.T) {
	steps := []json.RawMessage{
		json.RawMessage(`{"type":"Regex Filter","args":{"regex":["^(HK|JP)"]}}`),
		json.RawMessage(`{"type":"Regex Rename Operator","args":[{"expr":"^(\\w+) (\\w+)$","now":"$2 $1"}]}`),
		json.RawMessage(`{"type":"Sort Operator","args":"asc"}`),
		json.RawMessage(`{"type":"Flag Operator","args":{}}`),
	}
	plan, err := Compile("", steps)
	if err != nil || !plan.Native() {
		t.Fatalf("compile: %v", err)
	}
	in := named(t, "JP b", "US a", "HK c")
	keep := in[0]
	got := plan.Run(in, nil)
	wantNames(t, got, "🇯🇵 b JP", "🇭🇰 c HK")
	if keep.Name() != "🇯🇵 b JP" {
		t.Fatalf("the input node was copied, not renamed in place: %q", keep.Name())
	}
}

// The operators are imported by the conformance runner and the dispatcher
// and must stay free of package main, the SDK and the script engine (S1 plan
// section 1.1): the standard library, nodemodel and normalise only.
func TestOperatorsImportOnlyTheStandardLibraryAndTheModel(t *testing.T) {
	allowed := []string{
		"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel",
		"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if standard := !strings.Contains(strings.SplitN(path, "/", 2)[0], "."); standard || slices.Contains(allowed, path) {
				continue
			}
			t.Errorf("%s imports %s", file, path)
		}
	}
	if checked == 0 {
		t.Fatal("no source files found")
	}
}
