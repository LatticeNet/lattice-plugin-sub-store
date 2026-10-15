package operators

import (
	"math"

	"github.com/LatticeNet/lattice-sdk/model"
)

// LeadingRun is the length of the plan's leading structured run: the
// enabled Structured Filter steps at its head, counting disabled steps of any
// type between them (a disabled step does nothing, so it does not end the
// run) and not counting disabled steps after the last one. A fleet fetch
// evaluates exactly these steps over the catalogue rows, the first `covered`
// through the selector Pushdown returns and the rest in Go, and records the
// count beside the digest of these steps' stored JSON so render can tell
// whether a revision's run still answers the snapshot's rows. Structured
// predicates read only Lattice fields, which no step writes, so the run
// gives the same set over rows at fetch as over nodes at render (S2 plan
// section 2.4).
func LeadingRun(p *Plan) int {
	if p == nil {
		return 0
	}
	run := 0
	for i, s := range p.Steps {
		switch {
		case s.Kind == KindDisabled:
			continue
		case s.Kind == KindNative && s.Step.Type == StructuredFilterType:
			run = i + 1
			continue
		}
		break
	}
	return run
}

// Pushdown splits a plan's leading run of Structured Filter steps into the
// selector core evaluates and the number of leading steps the selector
// covers. A step joins the selector only when: mode keep, match all, no
// entry-geo predicate, every predicate is "in" over a pushable field (or lte
// over renewal_days or probe_passed_hours with a whole value inside the
// selector's bounds) and no pushed field repeats one already pushed (two
// "any" lists over node_tags do not intersect). The first step that fails
// stops the run; it and everything after run in the plugin. Fields not in
// advertised are never pushed, and a step whose values the selector's own
// Validate refuses (a value with surrounding space, a list past
// model.MaxLineCatalogueSelectorValues) is not pushed either, because the
// core would refuse the whole request. covered is 0 and sel nil when no step
// is pushed; disabled steps before a pushed step are covered.
func Pushdown(p *Plan, advertised []string) (sel *model.LineCatalogueSelector, covered int) {
	if p == nil || len(advertised) == 0 {
		return nil, 0
	}
	can := make(map[string]bool, len(advertised))
	for _, f := range advertised {
		can[f] = true
	}
	var acc model.LineCatalogueSelector
	used := map[string]bool{}
	for i, s := range p.Steps {
		if s.Kind == KindDisabled {
			continue
		}
		if s.Kind != KindNative || s.Step.Type != StructuredFilterType {
			break
		}
		args, err := ParseStructuredFilter(s.Step.Args)
		if err != nil {
			break
		}
		next, fields, ok := pushStep(acc, used, args, can)
		if !ok {
			break
		}
		acc = next
		for _, f := range fields {
			used[f] = true
		}
		covered = i + 1
	}
	if covered == 0 {
		return nil, 0
	}
	return &acc, covered
}

// pushStep adds one step to a copy of sel. ok is false when any predicate
// of the step cannot be pushed; sel itself is never changed, because every
// field the step sets is one sel does not yet set.
func pushStep(sel model.LineCatalogueSelector, used map[string]bool, args StructuredFilterArgs, can map[string]bool) (model.LineCatalogueSelector, []string, bool) {
	if args.Mode != ModeKeep || args.Match != MatchAll {
		return sel, nil, false
	}
	var fields []string
	mine := map[string]bool{}
	for _, p := range args.Predicates {
		spec := predicateFields[p.Field]
		f := spec.selector
		switch {
		case f == "", !can[f], used[f], mine[f]:
			return sel, nil, false
		case spec.geo && args.Geo == GeoEntry:
			return sel, nil, false
		}
		switch {
		case p.Op == OpIn:
			setSelectorList(&sel, f, p.Values)
		case p.Op == OpLTE && f == "renewal_within_days":
			n, ok := wholeValue(p.Value)
			if !ok {
				return sel, nil, false
			}
			sel.RenewalWithinDays = &n
		case p.Op == OpLTE && f == "probe_passed_within_hours":
			n, ok := wholeValue(p.Value)
			if !ok {
				return sel, nil, false
			}
			sel.ProbePassedWithinHours = &n
		default:
			return sel, nil, false
		}
		mine[f] = true
		fields = append(fields, f)
	}
	if sel.Validate() != nil {
		return sel, nil, false
	}
	return sel, fields, true
}

// setSelectorList sets one list field of the selector. line_uuids keeps the
// first occurrence of each value in order, which is the order the plugin's
// own evaluation keeps; the selector refuses a duplicate.
func setSelectorList(sel *model.LineCatalogueSelector, field string, values []string) {
	list := append([]string(nil), values...)
	switch field {
	case "line_uuids":
		sel.LineUUIDs = firstOccurrences(list)
	case "countries":
		sel.Countries = list
	case "regions":
		sel.Regions = list
	case "protocols":
		sel.Protocols = list
	case "transports":
		sel.Transports = list
	case "node_tags":
		sel.NodeTags = list
	case "group_ids":
		sel.GroupIDs = list
	case "service_states":
		sel.ServiceStates = list
	case "chain_roles":
		sel.ChainRoles = list
	}
}

// firstOccurrences drops every repeat of a value, keeping the order of first
// occurrences.
func firstOccurrences(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := values[:0]
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// wholeValue is a number predicate's value as an int when it is whole. The
// selector's window fields are whole days and whole hours; a fractional
// window runs in the plugin.
func wholeValue(v *float64) (int, bool) {
	if v == nil || *v != math.Trunc(*v) || math.Abs(*v) > 1<<31 {
		return 0, false
	}
	return int(*v), true
}

// StructuredAfterLeadingRun is the 1-based index of the first enabled
// Lattice-only step after the plan's leading structured run, 0 when there is
// none. A plan that is not Native (a script or Resolve Domain step in it)
// runs on the bundle as a whole, and the bundle skips a step type it does
// not know without a word. The leading run is evaluated over the rows before
// the chain runs, so only a step this reports would be lost there; a fleet
// render that hands a non-native chain to the bundle refuses such a plan
// rather than serve it without the step.
func StructuredAfterLeadingRun(p *Plan) int {
	if p == nil {
		return 0
	}
	for i := LeadingRun(p); i < len(p.Steps); i++ {
		if s := p.Steps[i]; s.Kind == KindNative && fleetOperators[s.Step.Type] {
			return i + 1
		}
	}
	return 0
}
