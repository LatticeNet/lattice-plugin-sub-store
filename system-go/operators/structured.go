package operators

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
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
	// ops, when set, narrows the kind's ops for this field.
	ops []string
}

// opsOf is the ops a field takes.
func (s fieldSpec) opsOf() []string {
	if s.ops != nil {
		return s.ops
	}
	return kindOps[s.kind]
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
	"probe_passed_hours": {kind: kindNumber, probe: true, selector: "probe_passed_within_hours", min: 0, max: model.MaxLineCatalogueProbeHours, ops: []string{OpLTE, OpGTE}},
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
		Ops:      append([]string(nil), spec.opsOf()...),
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
	if !slices.Contains(spec.opsOf(), p.Op) {
		return fmt.Errorf("%s takes %s, not %q", p.Field, strings.Join(spec.opsOf(), ", "), p.Op)
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

// Evaluate reports match (true), no match (false) or unknown (ok=false): a
// predicate over a null probe block, a null machine or geo, or a field the
// node has no Lattice block for (a provider node) is unknown. The geo of a
// relay whose Chain.Unresolved is true is unknown too, for country, region,
// city, asn, as_org and geo_provider alike: its outbound resolves to no
// fleet line, so neither its own geo nor an exit geo says where traffic
// leaves (the 16 relays of 2026-10-08 dialled a stale address and would
// otherwise match their hub's country, PROGRAM.md:268-270). A keep-mode step
// treats unknown as not matching; a drop-mode step treats it as not dropped
// (design-28.md:114, :172). model.LineCatalogueSelectorMatches applies the
// same rule, so pushdown and the plugin agree.
//
// geo is the step's geo choice. "exit" (the default) reads the chain's exit
// geo when the line has one and the node's own geo otherwise, never falling
// back field by field; "entry" reads the node's own geo, which an unresolved
// relay still has. Every geo field follows the choice.
//
// Beyond null blocks, an empty text value is unknown to in and not_in (the
// core's "an empty row value never matches", and a line with no country is
// not "outside CN" either), while present and absent read it as absent. A
// list (node tags, groups) is never unknown on a fleet node: an empty list
// has no tag in it. A renewal or cold p50 of zero is the catalogue's
// "unknown" and reads the same way. probe_passed_hours is unknown unless the
// last verdict passed, since a failed probe does not say when the line last
// passed.
func (p Predicate) Evaluate(n *nodemodel.Node, geo string, now time.Time) (match, ok bool) {
	if n == nil || n.Lattice == nil {
		return false, false
	}
	spec, known := predicateFields[p.Field]
	if !known {
		return false, false
	}
	l := n.Lattice
	switch spec.kind {
	case kindText:
		v, ok := textValue(l, p.Field, geo)
		if !ok {
			return false, false
		}
		return p.matchText(v, spec.fold)
	case kindList:
		var have []string
		switch p.Field {
		case "node_tag":
			have = l.Tags
		case "group":
			have = l.Groups
		}
		return p.matchList(have), true
	case kindNumber:
		return p.matchNumber(l, now)
	case kindBool:
		b, ok := boolValue(l, p.Field)
		if !ok {
			return false, false
		}
		return b == (p.Op == OpTrue), true
	}
	return false, false
}

// effectiveGeo is the geo a geo field reads under the step's choice; ok is
// false when it is unknown.
func effectiveGeo(l *nodemodel.LatticeFields, choice string) (*model.NodeGeo, bool) {
	if choice != GeoEntry && l.Chain != nil {
		if l.Chain.Unresolved {
			return nil, false
		}
		if l.Chain.ExitGeo != nil {
			return l.Chain.ExitGeo, true
		}
	}
	return l.Geo, l.Geo != nil
}

// textValue reads a text field; ok is false when its block is null.
func textValue(l *nodemodel.LatticeFields, field, geoChoice string) (string, bool) {
	if spec := predicateFields[field]; spec.geo {
		g, ok := effectiveGeo(l, geoChoice)
		if !ok {
			return "", false
		}
		switch field {
		case "country":
			return g.Country, true
		case "region":
			return g.Region, true
		case "city":
			return g.City, true
		case "asn":
			if g.ASN <= 0 {
				return "", true
			}
			return strconv.Itoa(g.ASN), true
		case "as_org":
			return g.ASOrg, true
		case "geo_provider":
			return g.Provider, true
		}
		return "", false
	}
	switch field {
	case "line_uuid":
		return l.LineUUID, true
	case "machine_vendor":
		if l.Machine == nil {
			return "", false
		}
		return l.Machine.Vendor, true
	case "protocol":
		return l.Protocol, true
	case "transport":
		return l.Transport, true
	case "security":
		return l.Security, true
	case "overlay_status":
		return l.OverlayStatus, true
	case "status":
		return l.Status, true
	case "service_state":
		return l.ServiceState, true
	case "chain_role", "path_state":
		if l.Chain == nil {
			return "", false
		}
		if field == "chain_role" {
			return l.Chain.Role, true
		}
		return l.Chain.PathState, true
	case "probe_vantage":
		if l.Probe == nil {
			return "", false
		}
		return l.Probe.Vantage, true
	}
	return "", false
}

// boolValue reads a bool field; ok is false when its block is null or, for
// probe_udp_ok, when UDP was not tested.
func boolValue(l *nodemodel.LatticeFields, field string) (bool, bool) {
	switch field {
	case "managed":
		return l.Managed, true
	case "overlay":
		return l.Overlay, true
	case "ddns_present":
		for _, d := range l.DDNSNames {
			if d.Verified {
				return true, true
			}
		}
		return false, true
	case "chain_root", "chain_unresolved":
		if l.Chain == nil {
			return false, false
		}
		if field == "chain_root" {
			return l.Chain.Root, true
		}
		return l.Chain.Unresolved, true
	case "probe_udp_ok":
		if l.Probe == nil || l.Probe.UDPOK == nil {
			return false, false
		}
		return *l.Probe.UDPOK, true
	}
	return false, false
}

func (p Predicate) matchText(v string, fold bool) (bool, bool) {
	switch p.Op {
	case OpPresent:
		return v != "", true
	case OpAbsent:
		return v == "", true
	}
	if v == "" {
		return false, false
	}
	in := false
	for _, want := range p.Values {
		if want == v || (fold && strings.EqualFold(want, v)) {
			in = true
			break
		}
	}
	return in == (p.Op == OpIn), true
}

func (p Predicate) matchList(have []string) bool {
	switch p.Op {
	case OpPresent:
		return len(have) > 0
	case OpAbsent:
		return len(have) == 0
	}
	in := false
	for _, want := range p.Values {
		if slices.Contains(have, want) {
			in = true
			break
		}
	}
	return in == (p.Op == OpIn)
}

// matchNumber evaluates the three number fields. The two clock fields read
// the same instants the core's matcher reads: renewal_days lte N is a known
// renewal at or before now plus N days, so a past renewal matches;
// probe_passed_hours lte N is a pass at or after now minus N hours. gte is
// the other side of the same instant, inclusive.
func (p Predicate) matchNumber(l *nodemodel.LatticeFields, now time.Time) (bool, bool) {
	if (p.Op == OpLTE || p.Op == OpGTE) && p.Value == nil {
		return false, false // ParseStructuredFilter refuses this; a hand-built predicate is unknown, not a panic
	}
	switch p.Field {
	case "renewal_days":
		if l.Machine == nil {
			return false, false
		}
		at := l.Machine.NextRenewal
		switch p.Op {
		case OpPresent, OpAbsent:
			return at.IsZero() == (p.Op == OpAbsent), true
		}
		if at.IsZero() {
			return false, false
		}
		edge := now.Add(window(*p.Value, 24*time.Hour))
		if p.Op == OpLTE {
			return !at.After(edge), true
		}
		return !at.Before(edge), true
	case "probe_passed_hours":
		if l.Probe == nil || l.Probe.Verdict != model.LineProbeVerdictPass {
			return false, false
		}
		edge := now.Add(-window(*p.Value, time.Hour))
		if p.Op == OpLTE {
			return !l.Probe.At.Before(edge), true
		}
		return !l.Probe.At.After(edge), true
	case "probe_cold_p50_ms":
		if l.Probe == nil {
			return false, false
		}
		ms := l.Probe.ColdP50MS
		switch p.Op {
		case OpPresent, OpAbsent:
			return (ms <= 0) == (p.Op == OpAbsent), true
		}
		if ms <= 0 {
			return false, false
		}
		if p.Op == OpLTE {
			return float64(ms) <= *p.Value, true
		}
		return float64(ms) >= *p.Value, true
	}
	return false, false
}

// window is n units as a duration. A whole n is multiplied in integers,
// exactly as the core's matcher multiplies its int window, so a pushed
// window and the same window run here name the same instant.
func window(n float64, unit time.Duration) time.Duration {
	if n == math.Trunc(n) {
		return time.Duration(int64(n)) * unit
	}
	return time.Duration(n * float64(unit))
}

// matches is a step's verdict over one node: true only when the
// predicates combine to a known match. Under "all" one false or unknown
// predicate decides; under "any" one known match does. An unknown verdict
// is never true, which is the whole of "a keep step treats unknown as not
// matching and a drop step as not dropped".
func (a StructuredFilterArgs) matches(n *nodemodel.Node, now time.Time) bool {
	for _, p := range a.Predicates {
		m, ok := p.Evaluate(n, a.Geo, now)
		hit := m && ok
		if a.Match == MatchAny && hit {
			return true
		}
		if a.Match == MatchAll && !hit {
			return false
		}
	}
	return a.Match == MatchAll
}

// order is the line list a keep step orders its kept nodes by: the values
// of its first line_uuid "in" predicate, first occurrence winning. nil when
// the step keeps the input order.
func (a StructuredFilterArgs) order() map[string]int {
	if a.Mode != ModeKeep {
		return nil
	}
	for _, p := range a.Predicates {
		if p.Field == "line_uuid" && p.Op == OpIn {
			rank := make(map[string]int, len(p.Values))
			for i, v := range p.Values {
				if _, seen := rank[v]; !seen {
					rank[v] = i
				}
			}
			return rank
		}
	}
	return nil
}

// compileStructuredFilter keeps (mode keep) or drops (mode drop) the nodes
// the step's predicates match. A keep step with a line_uuid "in" predicate
// orders its kept nodes by that list, nodes the list does not name after
// them in input order, which is the order the core answers a selector that
// names lines in (srv/line_catalogue.go:183-189), so a migrated graph
// record keeps its roots in committed order whether the step was pushed or
// not.
func compileStructuredFilter(c *stepCompiler, raw json.RawMessage) stepFunc {
	args, err := ParseStructuredFilter(raw)
	if err != nil {
		return c.structured(err)
	}
	rank := args.order()
	keep := args.Mode == ModeKeep
	return func(nodes []*nodemodel.Node, ctx *Context) []*nodemodel.Node {
		now := ctx.now()
		out := filterNodes(nodes, func(n *nodemodel.Node) bool { return args.matches(n, now) == keep })
		if rank != nil {
			pos := func(n *nodemodel.Node) int {
				if n.Lattice != nil {
					if i, ok := rank[n.Lattice.LineUUID]; ok {
						return i
					}
				}
				return len(rank)
			}
			slices.SortStableFunc(out, func(a, b *nodemodel.Node) int { return pos(a) - pos(b) })
		}
		return out
	}
}

// structured records a Lattice-only step's argument refusal and returns nil,
// so a compile function can end with `return c.structured(err)`. The step
// then fails Compile with a StepArgsError (chain.go), never falling back.
func (c *stepCompiler) structured(err error) stepFunc {
	c.diags = append(c.diags, Diagnostic{Step: c.index, Code: CodeStructuredArgs, Message: err.Error()})
	return nil
}

// fleetArgsError is the Compile error of a Lattice-only step that did not
// compile: its argument diagnostics joined.
func fleetArgsError(c *stepCompiler, step Step) error {
	msgs := make([]string, 0, len(c.diags))
	for _, d := range c.diags {
		msgs = append(msgs, d.Message)
	}
	if len(msgs) == 0 {
		msgs = append(msgs, "the step did not compile")
	}
	return &StepArgsError{Step: c.index, Type: step.Type, Message: strings.Join(msgs, "; ")}
}
