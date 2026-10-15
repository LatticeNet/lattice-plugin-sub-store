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
		reply.Records = append(reply.Records, dependsOnRecord{
			ID: entry.ID, Revision: entry.Revision, Kind: entry.Kind, Source: entry.Source,
			StagedRevision: entry.StagedRevision, StagedSource: entry.StagedSource, StagedMigrated: entry.StagedMigrated,
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

// fleetClosure is the two closures over a set of index entries and the
// edges they were computed over.
type fleetClosure struct {
	FleetBound       map[string]bool
	OwnerCredentials map[string]bool
	Edges            []dependsEdge
}

// fleetDependents computes both closures over the entries, live or over the
// union of live and staged state, on lane 4's record graph (store_graph.go):
// fleet_bound seeds from the fleet subs and closes over collections,
// owner_credentials seeds from the legacy subs and closes over collections
// and files. The edges are every reader edge of the same graph. An entry
// with no live revision contributes only its staged facts, and only to the
// union.
func fleetDependents(entries []indexEntry, includeStaged bool) fleetClosure {
	state := graphLive
	if includeStaged {
		state = graphUnion
	}
	g := newStoreGraph(entries, state)
	out := fleetClosure{
		FleetBound:       g.closure(seedFleet, false),
		OwnerCredentials: g.closure(seedLegacy, true),
	}
	seenEdge := map[dependsEdge]bool{}
	addEdge := func(edge dependsEdge) {
		if !seenEdge[edge] {
			seenEdge[edge] = true
			out.Edges = append(out.Edges, edge)
		}
	}
	for _, c := range g.collections() {
		explicit := make(map[string]bool, len(c.Members))
		for _, id := range c.Members {
			explicit[id] = true
		}
		for _, id := range g.gathered(c) {
			via := dependsViaMemberTag
			if explicit[id] {
				via = dependsViaMember
			}
			addEdge(dependsEdge{From: c.ID, To: id, Via: via})
		}
	}
	for _, f := range g.files() {
		if f.NodeSource != "" {
			addEdge(dependsEdge{From: f.ID, To: f.NodeSource, Via: dependsViaNodeSource})
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
