package operators

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/LatticeNet/lattice-sdk/model"
)

// The two Lattice-only step types (design 28, "One operator upstream cannot
// have"). They read the fleet block a fleet node carries (nodemodel's
// LatticeFields, filled from the catalogue row) and nothing else, so no
// upstream operator and no script can change what they see. The bundle does
// not know them: a chain holding one never runs them there, and arguments
// this package cannot read are refused at compile instead of falling back
// (CodeStructuredArgs).
const (
	StructuredFilterType = "Structured Filter"
	StructuredSortType   = "Structured Sort Operator"
)

// CodeStructuredArgs leads the refusal of Structured Filter or Structured
// Sort arguments this package cannot read. A native operator with arguments
// it cannot read falls back to the bundle; a Lattice-only step cannot,
// because the bundle would skip it and report success.
const CodeStructuredArgs = "structured_args_invalid"

// Predicate operators.
const (
	OpIn      = "in"
	OpNotIn   = "not_in"
	OpLTE     = "lte"
	OpGTE     = "gte"
	OpTrue    = "true"
	OpFalse   = "false"
	OpPresent = "present"
	OpAbsent  = "absent"
)

// Step modes, match rules and geo choices.
const (
	ModeKeep = "keep"
	ModeDrop = "drop"
	MatchAll = "all"
	MatchAny = "any"
	GeoExit  = "exit"
	GeoEntry = "entry"
)

// Bounds of one Structured Filter step. A predicate list names at most
// model.MaxSubscriptionRecordNodes values, which is what a migrated graph
// record's line_uuid pin needs.
const (
	maxStructuredPredicates = 64
	maxPredicateValues      = model.MaxSubscriptionRecordNodes
	maxColdP50MS            = 600_000
)

// StructuredFilterArgs is the wire shape of "Structured Filter".
type StructuredFilterArgs struct {
	Mode       string      `json:"mode"`          // "keep" (default) or "drop"
	Match      string      `json:"match"`         // "all" (default) or "any"
	Geo        string      `json:"geo,omitempty"` // "exit" (default) or "entry": which geo the geo fields read for chained lines
	Predicates []Predicate `json:"predicates"`
}

// Predicate is one condition of a Structured Filter step.
type Predicate struct {
	Field  string   `json:"field"`
	Op     string   `json:"op"`               // "in", "not_in", "lte", "gte", "true", "false", "present", "absent"
	Values []string `json:"values,omitempty"` // in, not_in
	Value  *float64 `json:"value,omitempty"`  // lte, gte
}

// fieldKind is the shape of the value a predicate field reads.
type fieldKind uint8

const (
	kindText   fieldKind = iota // one text: in, not_in, present, absent
	kindList                    // a list of texts: in (any), not_in (none), present, absent
	kindNumber                  // lte, gte, present, absent
	kindBool                    // true, false
)

var kindOps = map[fieldKind][]string{
	kindText:   {OpIn, OpNotIn, OpPresent, OpAbsent},
	kindList:   {OpIn, OpNotIn, OpPresent, OpAbsent},
	kindNumber: {OpLTE, OpGTE, OpPresent, OpAbsent},
	kindBool:   {OpTrue, OpFalse},
}

// fieldSpec is one predicate field: the shape it reads, how text compares,
// which block it reads, and the SDK selector field it pushes to.
type fieldSpec struct {
	kind fieldKind
	// fold compares text with Unicode case folding (strings.EqualFold);
	// otherwise text compares exactly.
	fold bool
	// geo reads the step's effective geo (exit by default, entry on the
	// toggle); unknown for a relay whose chain is unresolved.
	geo bool
	// probe reads the probe block, null until design 27's P2 writes it.
	probe bool
	// selector is the model.LineCatalogueSelector field (JSON name) an "in"
	// predicate (or, for the two clock fields, an "lte" predicate) pushes to;
	// empty when the plugin evaluates the field alone.
	selector string
	// min and max bound a number predicate's value.
	min, max float64
}

// predicateFields is every field a predicate may name. The row field each
// reads is in Evaluate. The case rules of the pushable fields are the core's
// (srv/line_catalogue.go:198-250, now model.LineCatalogueSelectorMatches):
// countries, regions, protocols, transports and service states fold;
// tags, groups and chain roles compare exactly.
var predicateFields = map[string]fieldSpec{
	"line_uuid":          {kind: kindText, selector: "line_uuids"},
	"country":            {kind: kindText, fold: true, geo: true, selector: "countries"},
	"region":             {kind: kindText, fold: true, geo: true, selector: "regions"},
	"city":               {kind: kindText, fold: true, geo: true},
	"asn":                {kind: kindText, geo: true},
	"as_org":             {kind: kindText, fold: true, geo: true},
	"geo_provider":       {kind: kindText, fold: true, geo: true},
	"node_tag":           {kind: kindList, selector: "node_tags"},
	"group":              {kind: kindList, selector: "group_ids"},
	"machine_vendor":     {kind: kindText, fold: true},
	"renewal_days":       {kind: kindNumber, selector: "renewal_within_days", min: -model.MaxLineCatalogueRenewalDays, max: model.MaxLineCatalogueRenewalDays},
	"protocol":           {kind: kindText, fold: true, selector: "protocols"},
	"transport":          {kind: kindText, fold: true, selector: "transports"},
	"security":           {kind: kindText, fold: true},
	"managed":            {kind: kindBool},
	"overlay":            {kind: kindBool},
	"overlay_status":     {kind: kindText, fold: true},
	"status":             {kind: kindText, fold: true},
	"service_state":      {kind: kindText, fold: true, selector: "service_states"},
	"ddns_present":       {kind: kindBool},
	"chain_role":         {kind: kindText, selector: "chain_roles"},
	"chain_root":         {kind: kindBool},
	"path_state":         {kind: kindText},
	"chain_unresolved":   {kind: kindBool},
	"probe_passed_hours": {kind: kindNumber, probe: true, selector: "probe_passed_within_hours", min: 0, max: model.MaxLineCatalogueProbeHours},
	"probe_vantage":      {kind: kindText, probe: true},
	"probe_cold_p50_ms":  {kind: kindNumber, probe: true, min: 0, max: maxColdP50MS},
	"probe_udp_ok":       {kind: kindBool, probe: true},
}

// PredicateField describes one field a Structured Filter predicate may name,
// for the save-time checks and the editor.
type PredicateField struct {
	Name string   `json:"name"`
	Ops  []string `json:"ops"`
	// Selector is the SDK selector field a predicate over this field may be
	// pushed to; empty when the plugin evaluates it alone.
	Selector string `json:"selector,omitempty"`
	// Probe marks a field that reads the probe block. A save refuses a
	// predicate over it while the catalogue lists "probe" as unavailable.
	Probe bool `json:"probe,omitempty"`
	// Geo marks a field that reads the effective geo, so the step's geo
	// toggle applies to it.
	Geo bool `json:"geo,omitempty"`
}

// PredicateFields is every predicate field, sorted by name.
func PredicateFields() []PredicateField {
	out := make([]PredicateField, 0, len(predicateFields))
	for name := range predicateFields {
		f, _ := LookupPredicateField(name)
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LookupPredicateField describes one predicate field.
func LookupPredicateField(name string) (PredicateField, bool) {
	spec, ok := predicateFields[name]
	if !ok {
		return PredicateField{}, false
	}
	return PredicateField{
		Name:     name,
		Ops:      append([]string(nil), kindOps[spec.kind]...),
		Selector: spec.selector,
		Probe:    spec.probe,
		Geo:      spec.geo,
	}, true
}

// lineUUIDPattern is model's lowercase UUIDv4 rule, the only shape a line
// uuid has (sdk/model/subscription_source_manifest.go:100).
var lineUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// asnPattern is a decimal AS number with no sign and no leading zero.
var asnPattern = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

// ParseStructuredFilter decodes and checks a Structured Filter step's
// arguments, filling the defaults (keep, all, exit). Unknown keys are
// refused: the step is this plugin's own, so a key it does not read is a
// mistake, not an upstream extension.
func ParseStructuredFilter(raw json.RawMessage) (StructuredFilterArgs, error) {
	var args StructuredFilterArgs
	if err := decodeStrict(raw, &args); err != nil {
		return StructuredFilterArgs{}, err
	}
	if args.Mode == "" {
		args.Mode = ModeKeep
	}
	if args.Match == "" {
		args.Match = MatchAll
	}
	if args.Geo == "" {
		args.Geo = GeoExit
	}
	switch {
	case args.Mode != ModeKeep && args.Mode != ModeDrop:
		return StructuredFilterArgs{}, fmt.Errorf(`mode must be "keep" or "drop", not %q`, args.Mode)
	case args.Match != MatchAll && args.Match != MatchAny:
		return StructuredFilterArgs{}, fmt.Errorf(`match must be "all" or "any", not %q`, args.Match)
	case args.Geo != GeoExit && args.Geo != GeoEntry:
		return StructuredFilterArgs{}, fmt.Errorf(`geo must be "exit" or "entry", not %q`, args.Geo)
	case len(args.Predicates) == 0:
		return StructuredFilterArgs{}, errors.New("a Structured Filter needs at least one predicate")
	case len(args.Predicates) > maxStructuredPredicates:
		return StructuredFilterArgs{}, fmt.Errorf("a Structured Filter takes at most %d predicates", maxStructuredPredicates)
	}
	for i, p := range args.Predicates {
		if err := p.check(); err != nil {
			return StructuredFilterArgs{}, fmt.Errorf("predicate %d: %w", i+1, err)
		}
	}
	return args, nil
}

// check validates one predicate against its field.
func (p Predicate) check() error {
	spec, ok := predicateFields[p.Field]
	if !ok {
		return fmt.Errorf("%q is not a structured field", p.Field)
	}
	valid := false
	for _, op := range kindOps[spec.kind] {
		valid = valid || op == p.Op
	}
	if !valid {
		return fmt.Errorf("%s takes %s, not %q", p.Field, strings.Join(kindOps[spec.kind], ", "), p.Op)
	}
	switch p.Op {
	case OpIn, OpNotIn:
		if p.Value != nil {
			return fmt.Errorf("%s %s takes values, not value", p.Field, p.Op)
		}
		if len(p.Values) == 0 {
			return fmt.Errorf("%s %s needs at least one value", p.Field, p.Op)
		}
		if len(p.Values) > maxPredicateValues {
			return fmt.Errorf("%s %s takes at most %d values", p.Field, p.Op, maxPredicateValues)
		}
		for _, v := range p.Values {
			switch {
			case v == "":
				return fmt.Errorf("%s %s has an empty value, which matches nothing", p.Field, p.Op)
			case p.Field == "line_uuid" && !lineUUIDPattern.MatchString(v):
				return fmt.Errorf("line_uuid %q is not a lowercase UUIDv4", v)
			case p.Field == "asn" && !asnPattern.MatchString(v):
				return fmt.Errorf("asn %q is not a decimal AS number", v)
			case p.Field == "chain_role" && (model.LineCatalogueChain{Role: v}).Validate() != nil:
				return fmt.Errorf("chain_role %q is not single, entry, relay or exit", v)
			case p.Field == "path_state" && v != model.LinePathConverged && v != model.LinePathDrifted && v != model.LinePathBusy:
				return fmt.Errorf("path_state %q is not converged, drifted or busy", v)
			}
		}
	case OpLTE, OpGTE:
		if len(p.Values) > 0 {
			return fmt.Errorf("%s %s takes value, not values", p.Field, p.Op)
		}
		if p.Value == nil {
			return fmt.Errorf("%s %s needs a value", p.Field, p.Op)
		}
		v := *p.Value
		if math.IsNaN(v) || math.IsInf(v, 0) || v < spec.min || v > spec.max {
			return fmt.Errorf("%s %s takes a value from %g to %g", p.Field, p.Op, spec.min, spec.max)
		}
	default:
		if len(p.Values) > 0 || p.Value != nil {
			return fmt.Errorf("%s %s takes no value", p.Field, p.Op)
		}
	}
	return nil
}

// decodeStrict decodes one JSON object into v, refusing unknown keys,
// trailing data and absent or null arguments.
func decodeStrict(raw json.RawMessage, v any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("arguments are required")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("arguments: %w", err)
	}
	if dec.More() {
		return errors.New("arguments: trailing data after the object")
	}
	return nil
}

// StepArgsError is the compile refusal of a Lattice-only step whose
// arguments this package cannot read. Its message leads with
// CodeStructuredArgs, the way IncompatibleError leads with its code.
type StepArgsError struct {
	Step    int    // 1-based
	Type    string // the step type
	Message string
}

func (e *StepArgsError) Error() string {
	return fmt.Sprintf("%s: process step %d (%s): %s", CodeStructuredArgs, e.Step, e.Type, e.Message)
}

// AsStepArgs unwraps a StepArgsError.
func AsStepArgs(err error) (*StepArgsError, bool) {
	var target *StepArgsError
	ok := errors.As(err, &target)
	return target, ok
}
