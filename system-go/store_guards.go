package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The guards storeWriteRecord runs for every origin (plan sections 1.2, 1.4,
// 2.1 and 2.2), and the staging decision. Each reads the state the plan fixes
// for it; store_graph.go says which state is which.

// writeContext is what the staging decision and the guards read for one
// written record. The three graphs hold the other records only (and, in the
// union, the written record's own live facts, since a staged edit removes no
// edge until it is promoted); the written record joins them through facts.
type writeContext struct {
	origin    writeOrigin
	rec       subscriptionRecord
	facts     recordFacts
	entry     indexEntry
	hasEntry  bool
	live      *storeGraph
	union     *storeGraph
	effective *storeGraph
}

// liveEntry reports whether the written id has a live revision.
func (c writeContext) liveEntry() bool { return c.hasEntry && c.entry.hasLiveRevision() }

// fleetBoundAfter reports whether the written record would be fleet-bound:
// a fleet source, or a collection gathering a fleet record over the union of
// the other records' live and staged state. A file cannot be fleet-bound in
// S2.
func (c writeContext) fleetBoundAfter() bool {
	if c.facts.Kind == kindFile {
		return false
	}
	return c.union.with(c.facts).closure(seedFleet, false)[c.facts.ID]
}

// stage is the staging decision of plan section 3: fleet-bound before the
// write (the entry's live flag) or after it.
func (c writeContext) stage() bool {
	return (c.liveEntry() && c.entry.Flags.FleetBound) || c.fleetBoundAfter()
}

// refusal runs the guards in order and answers the first that refuses.
func (c writeContext) refusal(stage bool) *storeRefusal {
	if stage {
		if r := scriptLinkRefusal(c.rec); r != nil {
			return r
		}
	}
	if r := c.fleetToLegacyRefusal(); r != nil {
		return r
	}
	if r := c.legacySourceRetiredRefusal(); r != nil {
		return r
	}
	if r := c.mixingRefusal(); r != nil {
		return r
	}
	return c.fileRefusal()
}

// scriptLinkRefusal refuses a fleet-bound record carrying an enabled Script
// Operator or Script Filter in link mode: the program is fetched at run time,
// so an approved plan would preview one program and the serve path would run
// whatever the host serves next (plan section 1.4). Inline scripts are in the
// record, and the revision hashes them.
func scriptLinkRefusal(rec subscriptionRecord) *storeRefusal {
	for index, raw := range processSteps(rec) {
		if linkModeScriptStep(raw) {
			return &storeRefusal{
				Code: refusedScriptLink, IDs: []string{rec.ID},
				Message: fmt.Sprintf("subscription %q is fleet-bound and step %d fetches its script from a link; link-mode scripts on fleet-bound records arrive in S3, so paste the script inline", rec.ID, index+1),
			}
		}
	}
	return nil
}

// linkModeScriptStep reports whether a step is an enabled link-mode script.
func linkModeScriptStep(raw json.RawMessage) bool {
	var step struct {
		Type     string `json:"type"`
		Disabled bool   `json:"disabled"`
		Args     struct {
			Mode string `json:"mode"`
		} `json:"args"`
	}
	if json.Unmarshal(raw, &step) != nil || step.Disabled {
		return false
	}
	if step.Type != "Script Operator" && step.Type != "Script Filter" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(step.Args.Mode), "link")
}

// fleetToLegacyRefusal: a record that is fleet-bound live (its live flag, or
// a live fleet source) refuses a legacy source, and a collection membership
// that gathers a legacy record, resolved over the other records' effective
// state (plan section 2.1).
func (c writeContext) fleetToLegacyRefusal() *storeRefusal {
	if !c.liveEntry() || !(c.entry.Flags.FleetBound || isFleetSource(c.entry.Source)) {
		return nil
	}
	if isVPNCoreSource(c.rec.Source) {
		return &storeRefusal{
			Code: refusedFleetToLegacy, IDs: []string{c.rec.ID},
			Message: fmt.Sprintf("subscription %q is fleet-bound and cannot take the legacy source %q, which serves line owner credentials", c.rec.ID, c.rec.Source),
		}
	}
	if c.facts.Kind == kindCollection {
		if legacy := sourcesGathered(c.effective.with(c.facts), c.facts, seedLegacy); len(legacy) > 0 {
			return &storeRefusal{
				Code: refusedFleetToLegacy, IDs: append([]string{c.rec.ID}, legacy...),
				Message: fmt.Sprintf("collection %q is fleet-bound and cannot gather the legacy records %s; migrate them to fleet first", c.rec.ID, strings.Join(legacy, ", ")),
			}
		}
	}
	return nil
}

// legacySourceRetiredRefusal: save creates no new owner-credential record,
// neither a new one nor a provider, fleet or never-live record switched to
// the export. Import, migrate and restore still write legacy records,
// because a backup must restore what it holds.
func (c writeContext) legacySourceRetiredRefusal() *storeRefusal {
	if c.origin != originSave || !isVPNCoreSource(c.rec.Source) {
		return nil
	}
	if c.liveEntry() && isVPNCoreSource(c.entry.Source) {
		return nil
	}
	return &storeRefusal{
		Code: refusedLegacySourceRetired, IDs: []string{c.rec.ID},
		Message: fmt.Sprintf("subscription %q cannot take the legacy source %q: new records select fleet lines instead", c.rec.ID, c.rec.Source),
	}
}

// mixingRefusal is fleet_mixed_owner_credentials from all three sides: a
// collection whose membership would mix a legacy and a fleet record, a legacy
// record that a fleet-bound collection would gather, and a fleet record that
// a collection with a legacy member would gather. Core serves provider-side
// nodes unvalidated to every identity, so a legacy member beside a fleet one
// would hand owner credentials to identity holders.
//
// At save, import, migrate and restore the other records count at their
// effective state, so a legacy member with a pending migration is not legacy
// and fleet at once. At migrate_record the guard reads live state only, with
// the written record at its live state too: that write exists to turn a
// legacy member into a fleet one, and the co_migrate list covers its
// collections instead.
func (c writeContext) mixingRefusal() *storeRefusal {
	g, facts := c.effective, c.facts
	if c.origin == originMigrateRecord {
		g = c.live
		live, ok := liveFactsOf(c.entry)
		if !c.hasEntry || !ok {
			return nil
		}
		facts = live
	}
	full := g.with(facts)
	if facts.Kind == kindCollection {
		legacy := sourcesGathered(full, facts, seedLegacy)
		fleet := sourcesGathered(full, facts, seedFleet)
		if len(legacy) > 0 && len(fleet) > 0 {
			return mixedRefusal(facts.ID, nil, legacy, fleet)
		}
		return nil
	}
	if facts.Kind != kindSub || !(seedLegacy(facts) || seedFleet(facts)) {
		return nil
	}
	var collections []string
	for _, collection := range full.collections() {
		if !contains(full.gathered(collection), facts.ID) {
			continue
		}
		legacy := sourcesGathered(full, collection, seedLegacy)
		fleet := sourcesGathered(full, collection, seedFleet)
		if len(legacy) > 0 && len(fleet) > 0 {
			collections = appendUnique(collections, collection.ID)
		}
	}
	if len(collections) == 0 {
		return nil
	}
	return mixedRefusal(facts.ID, collections, nil, nil)
}

func mixedRefusal(id string, collections, legacy, fleet []string) *storeRefusal {
	r := &storeRefusal{Code: refusedMixedOwnerCreds, IDs: append([]string{id}, append(legacy, fleet...)...), Collections: collections}
	if len(collections) > 0 {
		r.Message = fmt.Sprintf("subscription %q would put a legacy record and a fleet record in the collections %s; migrate the legacy members to fleet together first", id, strings.Join(collections, ", "))
	} else {
		r.Message = fmt.Sprintf("collection %q would gather the legacy records %s beside the fleet records %s; migrate the legacy members to fleet together first", id, strings.Join(legacy, ", "), strings.Join(fleet, ", "))
	}
	return r
}

// fileRefusal is fleet_file_unavailable at write time, over the union: a file
// whose node source is or would be fleet-bound. A fleet-bound file would hand
// clients configs whose uuid fields are placeholders, served unbound because
// a file render carries no plan, so such files do not exist until S3. Only
// files the write itself binds are named: the written file over a fleet-bound
// source, or a file over a record the write makes fleet-bound (the record
// itself, or a collection that gathers it).
func (c writeContext) fileRefusal() *storeRefusal {
	full := c.union.with(c.facts)
	after := full.closure(seedFleet, false)
	if c.facts.Kind == kindFile {
		if c.facts.NodeSource != "" && after[c.facts.NodeSource] {
			return &storeRefusal{
				Code: refusedFleetFileUnavailable, IDs: []string{c.rec.ID, c.facts.NodeSource}, Files: []string{c.rec.ID},
				Message: fmt.Sprintf("file %q reads %q, which is fleet-bound; fleet-bound files arrive in S3", c.rec.ID, c.facts.NodeSource),
			}
		}
		return nil
	}
	before := c.union.closure(seedFleet, false)
	bound := map[string]bool{}
	if after[c.facts.ID] {
		bound[c.facts.ID] = true
	}
	for _, collection := range full.collections() {
		if after[collection.ID] && !before[collection.ID] && contains(full.gathered(collection), c.facts.ID) {
			bound[collection.ID] = true
		}
	}
	files := filesOver(full, bound)
	if len(files) == 0 {
		return nil
	}
	return &storeRefusal{
		Code: refusedFleetFileUnavailable, IDs: []string{c.rec.ID}, Files: files,
		Message: fmt.Sprintf("subscription %q would make the node source of the files %s fleet-bound; fleet-bound files arrive in S3", c.rec.ID, strings.Join(files, ", ")),
	}
}

// filesOver is every file whose node source is in sources, sorted.
func filesOver(g *storeGraph, sources map[string]bool) []string {
	var files []string
	for _, file := range g.files() {
		if file.NodeSource != "" && sources[file.NodeSource] {
			files = appendUnique(files, file.ID)
		}
	}
	sort.Strings(files)
	return files
}

// sourcesGathered is the ids of the subs a collection gathers whose source
// seed accepts, at the graph's state.
func sourcesGathered(g *storeGraph, collection recordFacts, seed func(recordFacts) bool) []string {
	var out []string
	for _, id := range g.gathered(collection) {
		for _, f := range g.factsFor(id) {
			if f.Kind == kindSub && seed(f) {
				out = appendUnique(out, id)
				break
			}
		}
	}
	return out
}

func contains(list []string, id string) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}

func appendUnique(list []string, id string) []string {
	if contains(list, id) {
		return list
	}
	return append(list, id)
}
