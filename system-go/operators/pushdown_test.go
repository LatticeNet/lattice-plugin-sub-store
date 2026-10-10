package operators

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// allSelectorFields is what a core that evaluates every selector field
// advertises.
var allSelectorFields = model.LineCatalogueSelectorFields()

// structuredPlan builds a plan for the pushdown tests: the S1 steps through
// the compiler, the structured steps by hand with their arguments checked.
func structuredPlan(t *testing.T, steps ...string) *Plan {
	t.Helper()
	plan := &Plan{Revision: "r"}
	for i, s := range steps {
		step, err := DecodeStep(json.RawMessage(s))
		if err != nil {
			t.Fatal(err)
		}
		if step.Type == StructuredFilterType || step.Type == StructuredSortType {
			kind := KindNative
			if step.Disabled {
				kind = KindDisabled
			} else if _, err := ParseStructuredFilter(step.Args); step.Type == StructuredFilterType && err != nil {
				t.Fatalf("step %d: %v", i+1, err)
			}
			plan.Steps = append(plan.Steps, Compiled{Step: step, Kind: kind})
			continue
		}
		compiled, err := compileStep(i+1, step)
		if err != nil {
			t.Fatal(err)
		}
		plan.Steps = append(plan.Steps, compiled)
	}
	return plan
}

func intp(n int) *int { return &n }

func TestPushdownPushesKeepAllInPredicates(t *testing.T) {
	plan := structuredPlan(t,
		`{"type":"Structured Filter","args":{"predicates":[{"field":"country","op":"in","values":["HK","jp"]},{"field":"protocol","op":"in","values":["vless"]}]}}`,
		`{"type":"Structured Filter","args":{"mode":"keep","match":"all","predicates":[{"field":"node_tag","op":"in","values":["edge"]},{"field":"renewal_days","op":"lte","value":30},{"field":"probe_passed_hours","op":"lte","value":24}]}}`,
		`{"type":"Structured Filter","args":{"predicates":[{"field":"line_uuid","op":"in","values":["0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35","1b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35","0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35"]},{"field":"chain_role","op":"in","values":["single","exit"]},{"field":"group","op":"in","values":["g1"]},{"field":"service_state","op":"in","values":["running"]},{"field":"region","op":"in","values":["Kowloon"]},{"field":"transport","op":"in","values":["tcp"]}]}}`,
		`{"type":"Sort Operator","args":"asc"}`,
	)
	sel, covered := Pushdown(plan, allSelectorFields)
	if covered != 3 {
		t.Fatalf("covered = %d, want 3", covered)
	}
	want := &model.LineCatalogueSelector{
		LineUUIDs:              []string{"0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35", "1b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35"},
		Countries:              []string{"HK", "jp"},
		Regions:                []string{"Kowloon"},
		Protocols:              []string{"vless"},
		Transports:             []string{"tcp"},
		NodeTags:               []string{"edge"},
		GroupIDs:               []string{"g1"},
		ServiceStates:          []string{"running"},
		ChainRoles:             []string{"single", "exit"},
		RenewalWithinDays:      intp(30),
		ProbePassedWithinHours: intp(24),
	}
	if !reflect.DeepEqual(sel, want) {
		t.Fatalf("selector = %+v\nwant %+v", sel, want)
	}
	if err := sel.Validate(); err != nil {
		t.Fatalf("pushed selector does not validate: %v", err)
	}
	if run := LeadingRun(plan); run != 3 {
		t.Fatalf("LeadingRun = %d, want 3", run)
	}
}

func TestPushdownStopsAtFirstUnpushableStep(t *testing.T) {
	pushable := `{"type":"Structured Filter","args":{"predicates":[{"field":"country","op":"in","values":["HK"]}]}}`
	later := `{"type":"Structured Filter","args":{"predicates":[{"field":"protocol","op":"in","values":["vless"]}]}}`
	cases := []struct {
		name    string
		blocker string
	}{
		{"drop mode", `{"type":"Structured Filter","args":{"mode":"drop","predicates":[{"field":"transport","op":"in","values":["ws"]}]}}`},
		{"match any", `{"type":"Structured Filter","args":{"match":"any","predicates":[{"field":"transport","op":"in","values":["ws"]}]}}`},
		{"entry geo", `{"type":"Structured Filter","args":{"geo":"entry","predicates":[{"field":"region","op":"in","values":["Kowloon"]}]}}`},
		{"plugin-only field", `{"type":"Structured Filter","args":{"predicates":[{"field":"transport","op":"in","values":["ws"]},{"field":"city","op":"in","values":["Hong Kong"]}]}}`},
		{"not_in", `{"type":"Structured Filter","args":{"predicates":[{"field":"transport","op":"not_in","values":["ws"]}]}}`},
		{"gte", `{"type":"Structured Filter","args":{"predicates":[{"field":"renewal_days","op":"gte","value":3}]}}`},
		{"fractional window", `{"type":"Structured Filter","args":{"predicates":[{"field":"renewal_days","op":"lte","value":2.5}]}}`},
		{"negative window", `{"type":"Structured Filter","args":{"predicates":[{"field":"renewal_days","op":"lte","value":-1}]}}`},
		{"zero-hour window", `{"type":"Structured Filter","args":{"predicates":[{"field":"probe_passed_hours","op":"lte","value":0}]}}`},
		{"value the selector refuses", `{"type":"Structured Filter","args":{"predicates":[{"field":"transport","op":"in","values":[" ws"]}]}}`},
		{"structured sort", `{"type":"Structured Sort Operator","args":{"key":"country"}}`},
		{"S1 operator", `{"type":"Regex Filter","args":{"regex":["HK"],"keep":true}}`},
		{"script step", `{"type":"Script Filter","args":{"mode":"script","content":"function filter(p){return p.map(()=>true)}"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := structuredPlan(t, pushable, tc.blocker, later)
			sel, covered := Pushdown(plan, allSelectorFields)
			if covered != 1 {
				t.Fatalf("covered = %d, want 1 (the run stops at the blocker)", covered)
			}
			want := &model.LineCatalogueSelector{Countries: []string{"HK"}}
			if !reflect.DeepEqual(sel, want) {
				t.Fatalf("selector = %+v, want %+v", sel, want)
			}
		})
	}
	t.Run("blocker first", func(t *testing.T) {
		plan := structuredPlan(t, `{"type":"Structured Filter","args":{"mode":"drop","predicates":[{"field":"country","op":"in","values":["HK"]}]}}`, pushable)
		if sel, covered := Pushdown(plan, allSelectorFields); sel != nil || covered != 0 {
			t.Fatalf("Pushdown = %+v, %d; want nil, 0", sel, covered)
		}
		if run := LeadingRun(plan); run != 2 {
			t.Fatalf("LeadingRun = %d, want 2: an unpushable Structured Filter still belongs to the run", run)
		}
	})
	t.Run("disabled steps", func(t *testing.T) {
		plan := structuredPlan(t,
			`{"type":"Regex Filter","disabled":true,"args":{"regex":["HK"]}}`,
			pushable,
			`{"type":"Structured Filter","disabled":true,"args":{"mode":"drop","predicates":[{"field":"country","op":"in","values":["HK"]}]}}`,
			later,
			`{"type":"Script Filter","disabled":true,"args":{"mode":"script","content":"x"}}`,
			`{"type":"Sort Operator","args":"asc"}`,
		)
		sel, covered := Pushdown(plan, allSelectorFields)
		if covered != 4 {
			t.Fatalf("covered = %d, want 4: disabled steps do not end the run", covered)
		}
		want := &model.LineCatalogueSelector{Countries: []string{"HK"}, Protocols: []string{"vless"}}
		if !reflect.DeepEqual(sel, want) {
			t.Fatalf("selector = %+v, want %+v", sel, want)
		}
		if run := LeadingRun(plan); run != 4 {
			t.Fatalf("LeadingRun = %d, want 4: a trailing disabled step is not part of the run", run)
		}
	})
	t.Run("nil plan and empty chain", func(t *testing.T) {
		if sel, covered := Pushdown(nil, allSelectorFields); sel != nil || covered != 0 {
			t.Fatalf("Pushdown(nil) = %+v, %d", sel, covered)
		}
		if sel, covered := Pushdown(structuredPlan(t), allSelectorFields); sel != nil || covered != 0 {
			t.Fatalf("Pushdown(empty) = %+v, %d", sel, covered)
		}
		if LeadingRun(nil) != 0 {
			t.Fatal("LeadingRun(nil) != 0")
		}
	})
}

func TestPushdownNeverRepeatsAField(t *testing.T) {
	cases := []struct {
		name  string
		steps []string
		want  *model.LineCatalogueSelector
		cov   int
	}{
		{
			name: "two node_tags lists in two steps",
			steps: []string{
				`{"type":"Structured Filter","args":{"predicates":[{"field":"node_tag","op":"in","values":["hk"]}]}}`,
				`{"type":"Structured Filter","args":{"predicates":[{"field":"node_tag","op":"in","values":["edge"]}]}}`,
			},
			want: &model.LineCatalogueSelector{NodeTags: []string{"hk"}},
			cov:  1,
		},
		{
			name: "two country predicates in one step",
			steps: []string{
				`{"type":"Structured Filter","args":{"predicates":[{"field":"protocol","op":"in","values":["vless"]}]}}`,
				`{"type":"Structured Filter","args":{"predicates":[{"field":"country","op":"in","values":["HK","JP"]},{"field":"country","op":"in","values":["JP"]}]}}`,
			},
			want: &model.LineCatalogueSelector{Protocols: []string{"vless"}},
			cov:  1,
		},
		{
			name: "a second renewal window",
			steps: []string{
				`{"type":"Structured Filter","args":{"predicates":[{"field":"renewal_days","op":"lte","value":30}]}}`,
				`{"type":"Structured Filter","args":{"predicates":[{"field":"renewal_days","op":"lte","value":10}]}}`,
			},
			want: &model.LineCatalogueSelector{RenewalWithinDays: intp(30)},
			cov:  1,
		},
		{
			name: "a second line list",
			steps: []string{
				`{"type":"Structured Filter","args":{"predicates":[{"field":"line_uuid","op":"in","values":["0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35"]}]}}`,
				`{"type":"Structured Filter","args":{"predicates":[{"field":"line_uuid","op":"in","values":["0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35"]}]}}`,
			},
			want: &model.LineCatalogueSelector{LineUUIDs: []string{"0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35"}},
			cov:  1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, covered := Pushdown(structuredPlan(t, tc.steps...), allSelectorFields)
			if covered != tc.cov || !reflect.DeepEqual(sel, tc.want) {
				t.Fatalf("Pushdown = %+v, %d; want %+v, %d", sel, covered, tc.want, tc.cov)
			}
			for _, f := range sel.Fields() {
				n := 0
				for _, g := range sel.Fields() {
					if g == f {
						n++
					}
				}
				if n != 1 {
					t.Fatalf("field %s set %d times", f, n)
				}
			}
		})
	}
}

func TestPushdownRespectsAdvertisedFields(t *testing.T) {
	steps := []string{
		`{"type":"Structured Filter","args":{"predicates":[{"field":"country","op":"in","values":["HK"]}]}}`,
		`{"type":"Structured Filter","args":{"predicates":[{"field":"probe_passed_hours","op":"lte","value":6}]}}`,
		`{"type":"Structured Filter","args":{"predicates":[{"field":"protocol","op":"in","values":["vless"]}]}}`,
	}
	plan := structuredPlan(t, steps...)
	t.Run("an older core without the probe window", func(t *testing.T) {
		var older []string
		for _, f := range allSelectorFields {
			if f != "probe_passed_within_hours" {
				older = append(older, f)
			}
		}
		sel, covered := Pushdown(plan, older)
		if covered != 1 || !reflect.DeepEqual(sel, &model.LineCatalogueSelector{Countries: []string{"HK"}}) {
			t.Fatalf("Pushdown = %+v, %d; want countries only, 1", sel, covered)
		}
		if unsupported := sel.UnsupportedFields(older); len(unsupported) != 0 {
			t.Fatalf("pushed fields the core does not advertise: %v", unsupported)
		}
	})
	t.Run("a core advertising nothing", func(t *testing.T) {
		if sel, covered := Pushdown(plan, nil); sel != nil || covered != 0 {
			t.Fatalf("Pushdown = %+v, %d; want nil, 0", sel, covered)
		}
	})
	t.Run("a core without countries", func(t *testing.T) {
		if sel, covered := Pushdown(plan, []string{"protocols", "probe_passed_within_hours"}); sel != nil || covered != 0 {
			t.Fatalf("Pushdown = %+v, %d; want nil, 0: the first step is not pushable there", sel, covered)
		}
	})
	t.Run("every field", func(t *testing.T) {
		sel, covered := Pushdown(plan, allSelectorFields)
		want := &model.LineCatalogueSelector{Countries: []string{"HK"}, Protocols: []string{"vless"}, ProbePassedWithinHours: intp(6)}
		if covered != 3 || !reflect.DeepEqual(sel, want) {
			t.Fatalf("Pushdown = %+v, %d; want %+v, 3", sel, covered, want)
		}
	})
}

func TestPushdownCoversEveryPushableField(t *testing.T) {
	pushable := map[string]bool{}
	for _, f := range PredicateFields() {
		if f.Selector != "" {
			pushable[f.Selector] = true
		}
	}
	for _, f := range allSelectorFields {
		if !pushable[f] {
			t.Errorf("selector field %s has no predicate field that pushes to it", f)
		}
		delete(pushable, f)
	}
	for f := range pushable {
		t.Errorf("predicate field pushes to %s, which the selector does not define", f)
	}
}
