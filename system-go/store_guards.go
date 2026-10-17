package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The staging decision, and the bridge to the record rules every write runs
// (plan sections 1.2, 1.4, 2.1 and 2.2). The rules themselves are lane 1's
// applyFleetWriteRules (subscription_store.go): fleet_to_legacy_refused,
// legacy_source_retired, fleet_mixed_owner_credentials,
// fleet_file_unavailable and fleet_script_link_unavailable. This file hands
// it the other records' facts at the state the plan fixes for each origin
// and turns its refusal into the structured one a mutating method answers.
// store_graph.go says which state is which.

// writeContext is what the staging decision and the rules read for one
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
// write (the entry's live flag) or after it, or gathered by a fleet-bound
// collection before or after it.
func (c writeContext) stage() bool {
	return (c.liveEntry() && c.entry.Flags.FleetBound) || c.fleetBoundAfter() || c.gatheredByFleet()
}

// gatheredByFleet reports whether a collection that is fleet-bound over the
// union gathers the written record at its live facts or at its new ones
// (S2-SR-1). A write to a record a fleet-bound collection gathers changes
// what that collection's identity holders receive, and core serves its
// provider nodes unvalidated, so the write stages for a plan even though the
// record is not itself fleet-bound: an edit of a member, a tag that joins or
// leaves a member tag, a new or restored record under an id a collection
// already lists, and a record that stops being a sub. Delete reads the same
// rule through fleetGatherers.
func (c writeContext) gatheredByFleet() bool {
	if len(fleetGatherers(c.union.with(c.facts), c.facts.ID)) > 0 {
		return true
	}
	if live, ok := liveFactsOf(c.entry); ok && c.hasEntry {
		return len(fleetGatherers(c.union.with(live), live.ID)) > 0
	}
	return false
}

// fleetGatherers is every collection in g that gathers id, as an explicit
// member or as a sub carrying one of its member tags, and is fleet-bound in
// g, sorted. In the union a collection may hold a live and a staged fact;
// either one gathering id counts.
func fleetGatherers(g *storeGraph, id string) []string {
	bound := g.closure(seedFleet, false)
	var out []string
	for _, collection := range g.collections() {
		if bound[collection.ID] && contains(g.gathered(collection), id) {
			out = appendUnique(out, collection.ID)
		}
	}
	sort.Strings(out)
	return out
}

// rules runs applyFleetWriteRules and answers the record to store (a
// collection that gathers no fleet record loses its Fleet options) or the
// refusal. At save, import, migrate and restore the other records count at
// their effective state. At migrate_record the plan reads live state only,
// with the written record at its live state too, so no collection that
// gathers the record can be mixed by this write: those collections are left
// out of the call, and migrate_record has already refused the files over
// them, the one rule they would still matter for.
func (c writeContext) rules() (subscriptionRecord, *storeRefusal) {
	live := fleetLiveFacts{}
	if c.liveEntry() {
		live = fleetLiveFacts{Revision: c.entry.Revision, Source: c.entry.Source, FleetBound: c.entry.Flags.FleetBound}
	}
	others := c.effective.facts
	origin := fleetWriteOrigin(c.origin)
	if c.origin == originMigrateRecord {
		full := c.live.with(c.facts)
		others = nil
		for _, f := range c.live.facts {
			if f.Kind == kindCollection && contains(full.gathered(f), c.facts.ID) {
				continue
			}
			others = append(others, f)
		}
	}
	converted := make([]fleetRecordFacts, 0, len(others))
	for _, f := range others {
		if f.ID == c.rec.ID {
			continue
		}
		converted = append(converted, fleetRecordFacts{
			ID: f.ID, Kind: f.Kind, Source: f.Source, Tags: f.Tags,
			Members: f.Members, MemberTags: f.MemberTags, NodeSource: f.NodeSource,
		})
	}
	rec, err := applyFleetWriteRules(c.rec, live, converted, origin)
	if err != nil {
		if r, ok := asRefusal(err); ok {
			return c.rec, &r
		}
		r := storeRefusal{Code: refusedFleetToLegacy, IDs: []string{c.rec.ID}, Message: fmt.Sprintf("subscription %q was refused by a record rule", c.rec.ID)}
		return c.rec, &r
	}
	return rec, nil
}

// ruleRefusal maps lane 1's fleetRuleError onto the structured refusal.
func ruleRefusal(err error) (storeRefusal, bool) {
	var rule *fleetRuleError
	if !errors.As(err, &rule) {
		return storeRefusal{}, false
	}
	return storeRefusal{Code: rule.Code, IDs: rule.IDs, Files: rule.Files, Collections: rule.Collections, Message: rule.Message}, true
}

// fileRefusal is fleet_file_unavailable over the union, for apply_revision's
// promotion: a file whose node source the promotion would make fleet-bound
// (the record itself, or a collection that gathers it). Only files the
// promotion binds are named.
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
