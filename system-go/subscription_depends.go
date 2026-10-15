package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// depends_on v2 (S2 plan sections 1.2 and 2.7). It answers two closures over
// the index, so core can expire only the shares a fleet write reaches and
// compute the records a plan covers:
//
//   - fleet_bound: every record whose transitive sources include a fleet
//     record, seeded from the fleet subs and closed over the collections
//     that gather them (files cannot be fleet-bound in S2);
//   - owner_credentials: every record that still serves line owner
//     credentials, seeded from the legacy vpn-core and vpn-core-graph subs
//     and closed over every reader (collections, then files).
//
// The closures run over the union of live and staged index state when the
// request sets include_staged: an entry with a staged revision contributes
// its staged source, members, member tags and tags beside its live ones, so
// an edge only a staged revision creates is visible to the plan that would
// promote it. Without include_staged they run over live state, which is
// what the index's own flags hold.
//
// Request {subscription_id?, include_staged?}. Reply {records, fleet_bound,
// owner_credentials, edges, version}. With no subscription_id the lists are
// store-wide and records names the union of both closures, which is the set
// the fleet-write expiry reads, so a legacy graph record stays in it across
// a restart; with one, records holds that record and every record that reads
// it, transitively. Each record row carries its live revision, kind and
// source, its staged facts, and its live flags. An edge {from, to, via}
// says the record from reads the record to, by member, member_tag or
// node_source. Everything it needs is in the index: one host call, no record
// read. The version is a digest of the answer itself, so it moves only when
// the answer does and not with every refresh's bookkeeping. Core's v1
// reader reads records[].id, which v2 keeps.

// dependsOnRecord is one record row of the reply.
type dependsOnRecord struct {
	ID               string `json:"id"`
	Revision         string `json:"revision"`
	Kind             string `json:"kind,omitempty"`
	Source           string `json:"source,omitempty"`
	StagedRevision   string `json:"staged_revision,omitempty"`
	StagedSource     string `json:"staged_source,omitempty"`
	StagedMigrated   bool   `json:"staged_migrated,omitempty"`
	FleetBound       bool   `json:"fleet_bound,omitempty"`
	OwnerCredentials bool   `json:"owner_credentials,omitempty"`
}

// Edge kinds of the reply.
const (
	dependsViaMember     = "member"
	dependsViaMemberTag  = "member_tag"
	dependsViaNodeSource = "node_source"
)

// dependsEdge says the record From reads the record To.
type dependsEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Via  string `json:"via"`
}

type dependsOnRequest struct {
	SubscriptionID string `json:"subscription_id,omitempty"`
	IncludeStaged  bool   `json:"include_staged,omitempty"`
}

type dependsOnReply struct {
	Records          []dependsOnRecord `json:"records"`
	FleetBound       []string          `json:"fleet_bound"`
	OwnerCredentials []string          `json:"owner_credentials"`
	Edges            []dependsEdge     `json:"edges"`
	Version          string            `json:"version"`
}

func (rt *runtime) dependsOn(payload json.RawMessage) (json.RawMessage, error) {
	var req dependsOnRequest
	if len(bytes.TrimSpace(payload)) > 0 {
		if err := decodeStrictVPNCoreGraphJSON(payload, &req); err != nil {
			return nil, fmt.Errorf("invalid depends_on payload: %w", err)
		}
	}
	listing, err := rt.storeListing()
	if err != nil {
		return nil, err
	}
	reply, err := dependsOnAnswer(listing.Records, req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(reply)
}

// dependsOnAnswer computes the reply over the live index entries.
func dependsOnAnswer(entries []indexEntry, req dependsOnRequest) (dependsOnReply, error) {
	union := fleetDependents(entries, req.IncludeStaged)
	live := union
	if req.IncludeStaged {
		live = fleetDependents(entries, false)
	}
	byID := make(map[string]indexEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	var selected map[string]bool
	if id := strings.TrimSpace(req.SubscriptionID); id != "" {
		if _, ok := byID[id]; !ok {
			return dependsOnReply{}, fmt.Errorf("subscription %q was not found", id)
		}
		selected = readersOf(id, union.Edges)
	} else {
		selected = map[string]bool{}
		for id := range union.FleetBound {
			selected[id] = true
		}
		for id := range union.OwnerCredentials {
			selected[id] = true
		}
	}
	reply := dependsOnReply{Records: []dependsOnRecord{}, FleetBound: []string{}, OwnerCredentials: []string{}, Edges: []dependsEdge{}}
	for _, entry := range entries {
		if !selected[entry.ID] {
			continue
		}
		staged := stagedFactsOf(entry)
		reply.Records = append(reply.Records, dependsOnRecord{
			ID: entry.ID, Revision: entry.Revision, Kind: entry.Kind, Source: entry.Source,
			StagedRevision: staged.revision, StagedSource: staged.source, StagedMigrated: staged.migrated,
			FleetBound: live.FleetBound[entry.ID], OwnerCredentials: live.OwnerCredentials[entry.ID],
		})
		if union.FleetBound[entry.ID] {
			reply.FleetBound = append(reply.FleetBound, entry.ID)
		}
		if union.OwnerCredentials[entry.ID] {
			reply.OwnerCredentials = append(reply.OwnerCredentials, entry.ID)
		}
	}
	for _, edge := range union.Edges {
		if selected[edge.From] && selected[edge.To] {
			reply.Edges = append(reply.Edges, edge)
		}
	}
	sort.Slice(reply.Records, func(i, j int) bool { return reply.Records[i].ID < reply.Records[j].ID })
	sort.Strings(reply.FleetBound)
	sort.Strings(reply.OwnerCredentials)
	answer, err := json.Marshal(struct {
		Records          []dependsOnRecord `json:"records"`
		FleetBound       []string          `json:"fleet_bound"`
		OwnerCredentials []string          `json:"owner_credentials"`
		Edges            []dependsEdge     `json:"edges"`
	}{reply.Records, reply.FleetBound, reply.OwnerCredentials, reply.Edges})
	if err != nil {
		return dependsOnReply{}, err
	}
	reply.Version = digestOf(string(answer))
	return reply, nil
}

// readersOf is a record and every record that reads it, transitively.
func readersOf(id string, edges []dependsEdge) map[string]bool {
	out := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, edge := range edges {
			if out[edge.To] && !out[edge.From] {
				out[edge.From] = true
				changed = true
			}
		}
	}
	return out
}

// stagedFacts are an entry's staged facts: what a staged revision would make
// the record, read without opening its staged document.
type stagedFacts struct {
	revision   string
	source     string
	members    []string
	memberTags []string
	tags       []string
	migrated   bool
}

// stagedFactsOf reads an entry's staged facts.
//
// yagni: the index fields that hold them (StagedRevision, StagedSource,
// StagedMembers, StagedMemberTags, StagedTags, StagedMigrated) are lane 4's
// and have not landed, so no entry has a staged revision yet and the union
// equals live state. When they land this returns them and every caller,
// depends_on included, sees staged edges with no other change.
func stagedFactsOf(entry indexEntry) stagedFacts {
	_ = entry
	return stagedFacts{}
}

// fleetClosure is the two closures over a set of index entries and the
// edges they were computed over.
type fleetClosure struct {
	FleetBound       map[string]bool
	OwnerCredentials map[string]bool
	Edges            []dependsEdge
}

// fleetDependents computes both closures over the entries, live or over the
// union of live and staged state. A collection cannot contain a collection
// and a file cannot source a file, so subs, then collections, then files
// are the whole closure. The index's own flags call it with includeStaged
// false at every index write; depends_on with include_staged true.
func fleetDependents(entries []indexEntry, includeStaged bool) fleetClosure {
	type facts struct {
		entry      indexEntry
		sources    []string
		tags       []string
		members    []string
		memberTags []string
	}
	all := make([]facts, 0, len(entries))
	for _, entry := range entries {
		f := facts{entry: entry, sources: []string{entry.Source}, tags: entry.Tags, members: entry.Members, memberTags: entry.MemberTags}
		if staged := stagedFactsOf(entry); includeStaged && staged.revision != "" {
			f.sources = append(f.sources, staged.source)
			f.tags = append(append([]string(nil), f.tags...), staged.tags...)
			f.members = append(append([]string(nil), f.members...), staged.members...)
			f.memberTags = append(append([]string(nil), f.memberTags...), staged.memberTags...)
		}
		all = append(all, f)
	}
	out := fleetClosure{FleetBound: map[string]bool{}, OwnerCredentials: map[string]bool{}}
	seenEdge := map[dependsEdge]bool{}
	addEdge := func(edge dependsEdge) {
		if !seenEdge[edge] {
			seenEdge[edge] = true
			out.Edges = append(out.Edges, edge)
		}
	}
	for _, f := range all {
		if f.entry.Kind != kindSub {
			continue
		}
		for _, source := range f.sources {
			switch {
			case source == subscriptionSourceFleet:
				out.FleetBound[f.entry.ID] = true
			case isVPNCoreSource(source):
				out.OwnerCredentials[f.entry.ID] = true
			}
		}
	}
	for _, c := range all {
		if c.entry.Kind != kindCollection {
			continue
		}
		explicit := make(map[string]bool, len(c.members))
		for _, id := range c.members {
			explicit[id] = true
		}
		for _, s := range all {
			if s.entry.Kind != kindSub {
				continue
			}
			via := ""
			switch {
			case explicit[s.entry.ID]:
				via = dependsViaMember
			case tagsIntersect(c.memberTags, s.tags):
				via = dependsViaMemberTag
			default:
				continue
			}
			addEdge(dependsEdge{From: c.entry.ID, To: s.entry.ID, Via: via})
			if out.FleetBound[s.entry.ID] {
				out.FleetBound[c.entry.ID] = true
			}
			if out.OwnerCredentials[s.entry.ID] {
				out.OwnerCredentials[c.entry.ID] = true
			}
		}
	}
	for _, f := range all {
		source := strings.TrimSpace(f.entry.NodeSource)
		if f.entry.Kind != kindFile || source == "" {
			continue
		}
		addEdge(dependsEdge{From: f.entry.ID, To: source, Via: dependsViaNodeSource})
		if out.OwnerCredentials[source] {
			out.OwnerCredentials[f.entry.ID] = true
		}
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		a, b := out.Edges[i], out.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Via < b.Via
	})
	return out
}
