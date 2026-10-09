package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// depends_on names every record whose transitive sources include the fleet,
// so a committed vpn-core write expires only the shares that carry fleet
// nodes rather than every share of the plugin (lattice-server
// share_fleet_depends.go). The record itself when its source is vpn-core or
// vpn-core-graph, a collection with such a member (explicit or by tag), and a
// file whose node source is one of those.
//
// Everything it needs is in the index, so it costs one host call and never
// reads a record. Request {}; reply {"records":[{"id","revision"}],"version"}.
// Core reads the ids; revision and version are this plugin's own, and the
// version is a digest of the answer itself, so it moves only when the answer
// does and not with every refresh's bookkeeping.

type dependsOnRecord struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

func (rt *runtime) dependsOn(payload json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(payload)) > 0 {
		var req struct{}
		if err := decodeStrictVPNCoreGraphJSON(payload, &req); err != nil {
			return nil, fmt.Errorf("invalid depends_on payload: %w", err)
		}
	}
	listing, err := rt.storeListing()
	if err != nil {
		return nil, err
	}
	records := fleetDependents(listing.Records)
	answer, err := json.Marshal(records)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"records": records, "version": digestOf(string(answer))})
}

// fleetDependents resolves the transitive closure over live entries: subs
// first, then collections over them, then files over either. A collection
// cannot contain a collection and a file cannot source a file, so three
// passes are the whole closure.
func fleetDependents(entries []indexEntry) []dependsOnRecord {
	dependent := map[string]bool{}
	for _, entry := range entries {
		if entry.Kind == kindSub && isVPNCoreSource(entry.Source) {
			dependent[entry.ID] = true
		}
	}
	for _, entry := range entries {
		if entry.Kind != kindCollection {
			continue
		}
		if collectionReadsFleet(entry, entries, dependent) {
			dependent[entry.ID] = true
		}
	}
	for _, entry := range entries {
		if entry.Kind == kindFile && dependent[strings.TrimSpace(entry.NodeSource)] {
			dependent[entry.ID] = true
		}
	}
	out := make([]dependsOnRecord, 0, len(dependent))
	for _, entry := range entries {
		if dependent[entry.ID] {
			out = append(out, dependsOnRecord{ID: entry.ID, Revision: entry.Revision})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func collectionReadsFleet(collection indexEntry, entries []indexEntry, fleetSubs map[string]bool) bool {
	for _, id := range collection.Members {
		if fleetSubs[id] {
			return true
		}
	}
	if len(collection.MemberTags) == 0 {
		return false
	}
	wanted := make(map[string]bool, len(collection.MemberTags))
	for _, tag := range collection.MemberTags {
		wanted[strings.TrimSpace(tag)] = true
	}
	for _, candidate := range entries {
		if candidate.Kind != kindSub || !fleetSubs[candidate.ID] {
			continue
		}
		for _, tag := range candidate.Tags {
			if wanted[strings.TrimSpace(tag)] {
				return true
			}
		}
	}
	return false
}
