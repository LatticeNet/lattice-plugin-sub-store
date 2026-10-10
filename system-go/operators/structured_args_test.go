package operators

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseStructuredFilterFillsDefaults(t *testing.T) {
	args, err := ParseStructuredFilter(json.RawMessage(`{"predicates":[{"field":"country","op":"in","values":["HK"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if args.Mode != ModeKeep || args.Match != MatchAll || args.Geo != GeoExit {
		t.Fatalf("defaults = %q %q %q, want keep all exit", args.Mode, args.Match, args.Geo)
	}
}

func TestParseStructuredFilterRefusesWhatItCannotRead(t *testing.T) {
	cases := map[string]struct{ args, want string }{
		"absent":              {``, "arguments are required"},
		"null":                {`null`, "arguments are required"},
		"not an object":       {`["HK"]`, "arguments"},
		"unknown key":         {`{"predicates":[{"field":"country","op":"in","values":["HK"]}],"keep":true}`, "unknown field"},
		"unknown predicate":   {`{"predicates":[{"field":"country","op":"in","values":["HK"],"negate":true}]}`, "unknown field"},
		"trailing data":       {`{"predicates":[{"field":"country","op":"in","values":["HK"]}]} {}`, "trailing"},
		"mode":                {`{"mode":"invert","predicates":[{"field":"country","op":"in","values":["HK"]}]}`, "mode"},
		"match":               {`{"match":"none","predicates":[{"field":"country","op":"in","values":["HK"]}]}`, "match"},
		"geo":                 {`{"geo":"both","predicates":[{"field":"country","op":"in","values":["HK"]}]}`, "geo"},
		"no predicates":       {`{"predicates":[]}`, "at least one predicate"},
		"unknown field":       {`{"predicates":[{"field":"usage","op":"lte","value":1}]}`, "not a structured field"},
		"op for the kind":     {`{"predicates":[{"field":"country","op":"lte","value":1}]}`, "takes in, not_in"},
		"bool op on text":     {`{"predicates":[{"field":"managed","op":"in","values":["x"]}]}`, "takes true, false"},
		"in without values":   {`{"predicates":[{"field":"country","op":"in"}]}`, "at least one value"},
		"in with value":       {`{"predicates":[{"field":"country","op":"in","values":["HK"],"value":1}]}`, "not value"},
		"empty value":         {`{"predicates":[{"field":"country","op":"in","values":[""]}]}`, "empty value"},
		"uppercase line uuid": {`{"predicates":[{"field":"line_uuid","op":"in","values":["0B3CD6A9-4A5D-4A37-8C47-62F1A2D93C35"]}]}`, "lowercase UUIDv4"},
		"asn text":            {`{"predicates":[{"field":"asn","op":"in","values":["AS4134"]}]}`, "decimal AS number"},
		"chain role":          {`{"predicates":[{"field":"chain_role","op":"in","values":["hub"]}]}`, "chain_role"},
		"path state":          {`{"predicates":[{"field":"path_state","op":"in","values":["stale"]}]}`, "path_state"},
		"lte without value":   {`{"predicates":[{"field":"renewal_days","op":"lte"}]}`, "needs a value"},
		"lte with values":     {`{"predicates":[{"field":"renewal_days","op":"lte","values":["3"]}]}`, "not values"},
		"window past bound":   {`{"predicates":[{"field":"probe_passed_hours","op":"lte","value":9000}]}`, "from 0 to 8760"},
		"negative hours":      {`{"predicates":[{"field":"probe_passed_hours","op":"gte","value":-1}]}`, "from 0 to 8760"},
		"present with value":  {`{"predicates":[{"field":"city","op":"present","value":1}]}`, "takes no value"},
		"true with values":    {`{"predicates":[{"field":"managed","op":"true","values":["x"]}]}`, "takes no value"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseStructuredFilter(json.RawMessage(tc.args))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
	t.Run("too many predicates", func(t *testing.T) {
		preds := make([]string, maxStructuredPredicates+1)
		for i := range preds {
			preds[i] = `{"field":"country","op":"in","values":["HK"]}`
		}
		_, err := ParseStructuredFilter(json.RawMessage(`{"predicates":[` + strings.Join(preds, ",") + `]}`))
		if err == nil || !strings.Contains(err.Error(), "at most") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestParseStructuredFilterAcceptsEveryFieldAndOp(t *testing.T) {
	for _, f := range PredicateFields() {
		for _, op := range f.Ops {
			p := map[string]any{"field": f.Name, "op": op}
			switch op {
			case OpIn, OpNotIn:
				v := "x"
				switch f.Name {
				case "line_uuid":
					v = "0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35"
				case "asn":
					v = "4134"
				case "chain_role":
					v = "relay"
				case "path_state":
					v = "drifted"
				}
				p["values"] = []string{v}
			case OpLTE, OpGTE:
				p["value"] = 1
			}
			raw, _ := json.Marshal(map[string]any{"predicates": []any{p}})
			if _, err := ParseStructuredFilter(raw); err != nil {
				t.Errorf("%s %s: %v", f.Name, op, err)
			}
		}
	}
	if len(PredicateFields()) != 28 {
		t.Fatalf("%d predicate fields, want the 28 of S2 plan section 2.4", len(PredicateFields()))
	}
	if _, ok := LookupPredicateField("usage"); ok {
		t.Fatal("usage is not a predicate field: the record names no identity")
	}
	if f, _ := LookupPredicateField("probe_vantage"); !f.Probe {
		t.Fatal("probe_vantage is not marked as reading the probe block")
	}
}

func TestParseStructuredSort(t *testing.T) {
	args, err := ParseStructuredSort(json.RawMessage(`{"key":"probe_cold_p50_ms"}`))
	if err != nil || args.Order != OrderAsc {
		t.Fatalf("args = %+v, err = %v; want asc", args, err)
	}
	for name, tc := range map[string]struct{ args, want string }{
		"absent":      {``, "required"},
		"unknown key": {`{"key":"usage"}`, "key must be one of"},
		"order":       {`{"key":"country","order":"random"}`, "order"},
		"extra":       {`{"key":"country","keep":true}`, "unknown field"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseStructuredSort(json.RawMessage(tc.args)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
