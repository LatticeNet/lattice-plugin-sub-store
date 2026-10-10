package main

import (
	"sort"
	"strings"
)

// The record graph the index holds (plan section 2.2). Every edge a record
// can have is in its entry: a collection's explicit members and member tags,
// and a file's node source. A collection cannot contain a collection and a
// file cannot source a file, so subs, then collections, then files is the
// whole closure.
//
// Three states are read, and which check reads which is fixed by the plan:
//
//   - live: the entries' live fields only. The two index flags, the serve
//     path, fleet_delete_requires_plan and the trigger of
//     fleet_to_legacy_refused read it, so a staged migration never changes
//     the live behaviour of another write.
//   - union: every record's live and staged facts together. The staging
//     decision, the write-time fleet_file_unavailable guard and core's
//     closures read it, so an edge only a staged revision creates is seen.
//   - effective: each record's staged facts when it has a staged revision,
//     its live ones otherwise, never both. The mixing guard at save, import,
//     migrate and restore reads it, so a legacy member with a pending
//     migration does not count as legacy and fleet at once.

// recordFacts is one record's sources and edges at one state.
type recordFacts struct {
	ID         string
	Kind       string
	Source     string
	NodeSource string
	Tags       []string
	Members    []string
	MemberTags []string
}

// graphState names which facts a storeGraph holds.
type graphState int

const (
	graphLive graphState = iota
	graphUnion
	graphEffective
)

// liveFactsOf is an entry's live facts; false for an entry with no live
// revision, which contributes nothing live.
func liveFactsOf(entry indexEntry) (recordFacts, bool) {
	if !entry.hasLiveRevision() {
		return recordFacts{}, false
	}
	return recordFacts{
		ID: entry.ID, Kind: entry.Kind, Source: entry.Source, NodeSource: entry.NodeSource,
		Tags: entry.Tags, Members: entry.Members, MemberTags: entry.MemberTags,
	}, true
}

// stagedFactsOf is an entry's staged facts; false when nothing is staged.
// Files are never staged (a fleet-bound file cannot exist in S2), so a staged
// revision is a sub or a collection.
func stagedFactsOf(entry indexEntry) (recordFacts, bool) {
	if !entry.hasStaged() {
		return recordFacts{}, false
	}
	kind := entry.StagedKind
	if kind == "" {
		kind = kindSub
		if len(entry.StagedMembers) > 0 || len(entry.StagedMemberTags) > 0 {
			kind = kindCollection
		}
	}
	return recordFacts{
		ID: entry.ID, Kind: kind, Source: entry.StagedSource,
		Tags: entry.StagedTags, Members: entry.StagedMembers, MemberTags: entry.StagedMemberTags,
	}, true
}

// factsOfRecord is a record document's facts.
func factsOfRecord(rec subscriptionRecord) recordFacts {
	return recordFacts{
		ID: rec.ID, Kind: recordKind(rec), Source: rec.Source, NodeSource: strings.TrimSpace(rec.NodeSource),
		Tags: rec.Tags, Members: rec.Members, MemberTags: rec.MemberTags,
	}
}

// storeGraph is the record graph at one state. In the union state a record
// may contribute two facts, its live and its staged ones.
type storeGraph struct {
	facts []recordFacts
}

// newStoreGraph builds the graph of entries at state.
func newStoreGraph(entries []indexEntry, state graphState) *storeGraph {
	g := &storeGraph{facts: make([]recordFacts, 0, len(entries))}
	for _, entry := range entries {
		live, hasLive := liveFactsOf(entry)
		staged, hasStaged := stagedFactsOf(entry)
		switch state {
		case graphLive:
			if hasLive {
				g.facts = append(g.facts, live)
			}
		case graphUnion:
			if hasLive {
				g.facts = append(g.facts, live)
			}
			if hasStaged {
				g.facts = append(g.facts, staged)
			}
		case graphEffective:
			if hasStaged {
				g.facts = append(g.facts, staged)
			} else if hasLive {
				g.facts = append(g.facts, live)
			}
		}
	}
	return g
}

// with returns a copy of the graph in which the record f.ID has exactly the
// facts f: the written record's own state, whatever state the rest is in.
func (g *storeGraph) with(f recordFacts) *storeGraph {
	out := &storeGraph{facts: make([]recordFacts, 0, len(g.facts)+1)}
	for _, other := range g.facts {
		if other.ID != f.ID {
			out.facts = append(out.facts, other)
		}
	}
	out.facts = append(out.facts, f)
	return out
}

// without returns a copy of the graph with no facts for id.
func (g *storeGraph) without(id string) *storeGraph {
	out := &storeGraph{facts: make([]recordFacts, 0, len(g.facts))}
	for _, other := range g.facts {
		if other.ID != id {
			out.facts = append(out.facts, other)
		}
	}
	return out
}

// closure is every record whose transitive sources include a sub that seed
// accepts: the seeded subs, the collections that gather one (an explicit
// member or a member tag a seeded sub carries), and, with intoFiles, the
// files whose node source is any of those.
func (g *storeGraph) closure(seed func(recordFacts) bool, intoFiles bool) map[string]bool {
	set := map[string]bool{}
	for _, f := range g.facts {
		if f.Kind == kindSub && seed(f) {
			set[f.ID] = true
		}
	}
	subs := make(map[string]bool, len(set))
	for id := range set {
		subs[id] = true
	}
	for _, f := range g.facts {
		if f.Kind == kindCollection && g.gathersAny(f, subs) {
			set[f.ID] = true
		}
	}
	if intoFiles {
		for _, f := range g.facts {
			if f.Kind == kindFile && f.NodeSource != "" && set[f.NodeSource] {
				set[f.ID] = true
			}
		}
	}
	return set
}

// gathersAny reports whether a collection gathers any sub in subs: by an
// explicit member, or by a member tag one of those subs carries.
func (g *storeGraph) gathersAny(collection recordFacts, subs map[string]bool) bool {
	for _, id := range collection.Members {
		if subs[id] {
			return true
		}
	}
	if len(collection.MemberTags) == 0 {
		return false
	}
	wanted := tagSet(collection.MemberTags)
	for _, candidate := range g.facts {
		if candidate.Kind == kindSub && subs[candidate.ID] && carriesAny(candidate.Tags, wanted) {
			return true
		}
	}
	return false
}

// gathered is the ids a collection gathers: its explicit members, then the
// subs that carry one of its member tags, sorted, each once. It is
// collectionMembers' rule over the graph instead of the store.
func (g *storeGraph) gathered(collection recordFacts) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range collection.Members {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(collection.MemberTags) == 0 {
		return out
	}
	wanted := tagSet(collection.MemberTags)
	var tagged []string
	for _, candidate := range g.facts {
		if candidate.Kind == kindSub && !seen[candidate.ID] && carriesAny(candidate.Tags, wanted) {
			seen[candidate.ID] = true
			tagged = append(tagged, candidate.ID)
		}
	}
	sort.Strings(tagged)
	return append(out, tagged...)
}

// factsFor is every fact the graph holds for id (two in the union state when
// the record has a live and a staged revision).
func (g *storeGraph) factsFor(id string) []recordFacts {
	var out []recordFacts
	for _, f := range g.facts {
		if f.ID == id {
			out = append(out, f)
		}
	}
	return out
}

// collections is every collection fact in the graph.
func (g *storeGraph) collections() []recordFacts {
	var out []recordFacts
	for _, f := range g.facts {
		if f.Kind == kindCollection {
			out = append(out, f)
		}
	}
	return out
}

// files is every file fact in the graph.
func (g *storeGraph) files() []recordFacts {
	var out []recordFacts
	for _, f := range g.facts {
		if f.Kind == kindFile {
			out = append(out, f)
		}
	}
	return out
}

func tagSet(tags []string) map[string]bool {
	set := make(map[string]bool, len(tags))
	for _, tag := range tags {
		set[strings.TrimSpace(tag)] = true
	}
	return set
}

func carriesAny(tags []string, wanted map[string]bool) bool {
	for _, tag := range tags {
		if wanted[strings.TrimSpace(tag)] {
			return true
		}
	}
	return false
}

// seedFleet accepts a fleet sub; seedLegacy a vpn-core or vpn-core-graph one.
func seedFleet(f recordFacts) bool  { return isFleetSource(f.Source) }
func seedLegacy(f recordFacts) bool { return isVPNCoreSource(f.Source) }

// recomputeFlags sets the two closure flags on every live entry from the live
// graph (plan section 2.2). It runs on every index write, inside encodeIndex,
// so list never recomputes them. An entry with no live revision carries no
// live flag.
func (idx *indexDocument) recomputeFlags() {
	live := newStoreGraph(idx.Records, graphLive)
	fleet := live.closure(seedFleet, false)
	owner := live.closure(seedLegacy, true)
	for i := range idx.Records {
		entry := &idx.Records[i]
		entry.Flags.FleetBound = entry.hasLiveRevision() && fleet[entry.ID]
		entry.Flags.OwnerCredentials = entry.hasLiveRevision() && owner[entry.ID]
	}
}
