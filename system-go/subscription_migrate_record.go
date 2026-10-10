package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// migrate_record builds the fleet record a legacy vpn-core or vpn-core-graph
// record becomes, under the same id, and stages it (plan sections 2.7 and 6).
// It never proposes: the UI collects share identities and proposes one plan
// over the record and its co-migrating siblings.
//
// For vpn-core-graph the selection pins the committed roots in order: one
// Structured Filter step keeping line_uuid in the roots, prepended to the
// chain, so the catalogue's composed root template stands in for compose.
// For vpn-core no structured step is added: the selection is every line the
// share's identity is bound to, which is what the export gave each identity.
// Target, options, chain, name, tags and display fields carry over; the
// legacy fields move to MigratedFrom.
//
// Node names do not carry over and the method does not claim they do: the
// fleet render names a node by the catalogue row's label, while the export
// named lines by proxy profile or by the fragment the box reported, so a
// carried-over name-based step may select differently. For a graph record the
// method reads one catalogue page under the pinned roots and reports, per
// name-based step, how many lines it matches under the fleet labels, with
// zero as a warning; for an export record the count depends on the identity
// and belongs to bind.preview, where the UI runs the same check.

const (
	structuredFilterType = "Structured Filter"
	// maxMigrateReportRoots is how many roots one catalogue selector may
	// pin (model.MaxLineCatalogueSelectorValues); a record with more is
	// checked over its first ones and says so.
	maxMigrateReportRoots = model.MaxLineCatalogueSelectorValues
)

type migrateRecordRequest struct {
	SubscriptionID string `json:"subscription_id"`
	IfRevision     string `json:"if_revision,omitempty"`
}

type migrateRecordSelection struct {
	CatalogueVersion string   `json:"catalogue_version,omitempty"`
	Count            int      `json:"count"`
	LineUUIDs        []string `json:"line_uuids,omitempty"`
	// EveryBoundLine is an export record's selection: every line the share's
	// identity is bound to; bind.preview counts it per identity.
	EveryBoundLine bool `json:"every_bound_line,omitempty"`
}

type migrateRecordDependents struct {
	Collections []string `json:"collections"`
	CoMigrate   []string `json:"co_migrate"`
}

type migrateRecordReply struct {
	SubscriptionID string                  `json:"subscription_id"`
	LiveRevision   string                  `json:"live_revision"`
	StagedRevision string                  `json:"staged_revision"`
	Selection      migrateRecordSelection  `json:"selection"`
	Legacy         legacyOrigin            `json:"legacy"`
	Dependents     migrateRecordDependents `json:"dependents"`
	Warnings       []string                `json:"warnings"`
}

// migrateRecordCall serves migrate_record. A refusal is the structured reply.
func (rt *runtime) migrateRecordCall(payload json.RawMessage) response {
	var req migrateRecordRequest
	if err := decodeStrictVPNCoreGraphJSON(payload, &req); err != nil || strings.TrimSpace(req.SubscriptionID) == "" {
		return latticeplugin.ErrorResponse(errors.New("invalid migrate_record payload: subscription_id is required"))
	}
	reply, conflict, err := rt.migrateRecord(req)
	if err != nil {
		return mutationResponse(err)
	}
	if conflict != nil {
		return conflictResponse(req.SubscriptionID, conflict.reason, conflict.current, conflict.revision)
	}
	return latticeplugin.RawResultResponse(mustJSON(reply), "")
}

// migrateRecord is the method. Host calls: the index, the record, for a graph
// record one catalogue page under the pinned roots and one more when the
// selection spans two, the staged put, the index put (two more on a
// kv_conflict).
func (rt *runtime) migrateRecord(req migrateRecordRequest) (migrateRecordReply, *saveConflict, error) {
	idx, err := rt.writableIndex()
	if err != nil {
		return migrateRecordReply{}, nil, err
	}
	id := req.SubscriptionID
	loaded, err := rt.storeLoadRecord(idx, id, false)
	if err != nil {
		return migrateRecordReply{}, nil, err
	}
	if r, refused := loaded.writeRefusal(); refused {
		return migrateRecordReply{}, nil, refusalErr(r)
	}
	if !loaded.HasLive || !loaded.HasEntry {
		return migrateRecordReply{}, nil, fmt.Errorf("subscription %q has no live record to migrate", id)
	}
	if req.IfRevision != "" && req.IfRevision != loaded.LiveRevision {
		return migrateRecordReply{}, &saveConflict{reason: "stale", current: withBookkeeping(loaded.Live, loaded.Entry), revision: loaded.LiveRevision}, nil
	}
	legacy := loaded.Live
	if recordKind(legacy) != kindSub || !isVPNCoreSource(legacy.Source) {
		return migrateRecordReply{}, nil, fmt.Errorf("subscription %q is not a legacy vpn-core or vpn-core-graph record", id)
	}
	fleet, origin := migratedFleetRecord(legacy, time.Now().UTC())
	union := newStoreGraph(idx.Records, graphUnion).without(id)
	if own, ok := liveFactsOf(loaded.Entry); ok {
		union.facts = append(union.facts, own)
	}
	full := union.with(factsOfRecord(fleet))
	dependents := migrateDependents(full, id)
	// No sequence makes a migration valid while a file reads the record or
	// a collection over it: a fleet-bound file cannot exist before S3, and
	// refusing here, before the flow collects share identities, is cheaper
	// than propose refusing after it.
	readers := map[string]bool{id: true}
	for _, collection := range dependents.Collections {
		readers[collection] = true
	}
	if files := filesOver(full, readers); len(files) > 0 {
		return migrateRecordReply{}, nil, refusalErr(storeRefusal{
			Code: refusedFleetFileUnavailable, IDs: []string{id}, Files: files, Collections: dependents.Collections,
			Message: fmt.Sprintf("subscription %q feeds the files %s; fleet-bound files arrive in S3, so it cannot migrate yet", id, strings.Join(files, ", ")),
		})
	}
	reply := migrateRecordReply{SubscriptionID: id, LiveRevision: loaded.LiveRevision, Legacy: origin, Dependents: dependents, Warnings: []string{}}
	if legacy.Source == subscriptionSourceVPNCoreGraph {
		selection, warnings, err := rt.migrateGraphReport(legacy.EntryRoots, processSteps(legacy))
		if err != nil {
			return migrateRecordReply{}, nil, err
		}
		reply.Selection, reply.Warnings = selection, append(reply.Warnings, warnings...)
	} else {
		reply.Selection = migrateRecordSelection{EveryBoundLine: true}
	}
	results, err := rt.storeWriteRecordsIn(idx, []writeRequest{{Record: fleet}}, originMigrateRecord, true)
	if err != nil {
		return migrateRecordReply{}, nil, err
	}
	res := results[0]
	switch {
	case res.Refused != nil:
		return migrateRecordReply{}, nil, refusalErr(*res.Refused)
	case res.Skipped != "":
		return migrateRecordReply{}, nil, fmt.Errorf("subscription %q: the fleet record it becomes is invalid: %s", id, res.Skipped)
	case !res.Staged:
		return migrateRecordReply{}, nil, fmt.Errorf("subscription %q: the fleet record it becomes was not staged", id)
	}
	reply.StagedRevision = res.StagedRevision
	return reply, nil, nil
}

// migratedFleetRecord is the fleet record a legacy record becomes.
func migratedFleetRecord(legacy subscriptionRecord, now time.Time) (subscriptionRecord, legacyOrigin) {
	origin := legacyOrigin{
		Source: legacy.Source, VPNIdentity: legacy.VPNIdentity,
		EntryRoots:          append([]string(nil), legacy.EntryRoots...),
		GraphOptionsVersion: legacy.GraphOptionsVersion,
		MigratedAt:          now.Format(time.RFC3339),
	}
	fleet := legacy
	fleet.Source = subscriptionSourceFleet
	fleet.URL, fleet.Content, fleet.UA, fleet.VPNIdentity, fleet.GraphOptionsVersion = "", "", "", "", ""
	fleet.EntryRoots = nil
	fleet.Revision = ""
	fleet.LastFetchAt, fleet.LastFetchOK, fleet.LastError, fleet.Userinfo = "", false, "", ""
	saved := origin
	fleet.MigratedFrom = &saved
	steps := processSteps(legacy)
	if legacy.Source == subscriptionSourceVPNCoreGraph && len(legacy.EntryRoots) > 0 {
		pin := mustJSON(map[string]any{
			"type": structuredFilterType,
			"args": map[string]any{
				"mode": "keep", "match": "all",
				"predicates": []map[string]any{{"field": "line_uuid", "op": "in", "values": legacy.EntryRoots}},
			},
		})
		steps = append([]json.RawMessage{pin}, steps...)
	}
	fleet.Process, fleet.Operators = steps, nil
	return fleet, origin
}

// migrateDependents lists the collections that read id (they are what the
// plan will cover) and their other legacy members, which the same plan must
// promote, because there is no order that migrates them one at a time.
func migrateDependents(g *storeGraph, id string) migrateRecordDependents {
	out := migrateRecordDependents{Collections: []string{}, CoMigrate: []string{}}
	for _, collection := range g.collections() {
		members := g.gathered(collection)
		if !contains(members, id) {
			continue
		}
		out.Collections = appendUnique(out.Collections, collection.ID)
		for _, member := range sourcesGathered(g, collection, seedLegacy) {
			if member != id {
				out.CoMigrate = appendUnique(out.CoMigrate, member)
			}
		}
	}
	sort.Strings(out.Collections)
	sort.Strings(out.CoMigrate)
	return out
}

// catalogueLabelPage is the part of a catalogue page migrate_record reads.
type catalogueLabelPage struct {
	CatalogueVersion string `json:"catalogue_version"`
	Rows             []struct {
		LineUUID string `json:"line_uuid"`
		Label    string `json:"label"`
	} `json:"rows"`
	Cursor string `json:"cursor"`
}

// migrateGraphReport reads the catalogue under the pinned roots and reports
// the selection, the roots the catalogue lacks and the name-based steps that
// match no line under the fleet labels. One page, and one more when the
// selection spans two; a page whose version differs from the first means the
// catalogue moved under the read, and the report keeps the first page.
func (rt *runtime) migrateGraphReport(roots []string, steps []json.RawMessage) (migrateRecordSelection, []string, error) {
	var warnings []string
	checked := roots
	if len(checked) > maxMigrateReportRoots {
		checked = checked[:maxMigrateReportRoots]
		warnings = append(warnings, fmt.Sprintf("only the first %d of %d roots were checked against the catalogue", maxMigrateReportRoots, len(roots)))
	}
	request := model.LineCatalogueRequest{Selector: &model.LineCatalogueSelector{LineUUIDs: checked}, Limit: min(len(checked), model.MaxLineCataloguePageRows)}
	page, err := rt.cataloguePage(request)
	if err != nil {
		return migrateRecordSelection{}, nil, err
	}
	rows := page.Rows
	if page.Cursor != "" {
		request.Cursor = page.Cursor
		next, err := rt.cataloguePage(request)
		if err != nil {
			return migrateRecordSelection{}, nil, err
		}
		if next.CatalogueVersion == page.CatalogueVersion {
			rows = append(rows, next.Rows...)
		} else {
			warnings = append(warnings, "the catalogue moved while it was read; the count is from its first page")
		}
	}
	present := make(map[string]string, len(rows))
	for _, row := range rows {
		present[row.LineUUID] = row.Label
	}
	selection := migrateRecordSelection{CatalogueVersion: page.CatalogueVersion}
	labels := make([]string, 0, len(rows))
	for _, root := range checked {
		label, ok := present[root]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("root %s is not in the catalogue", root))
			continue
		}
		selection.LineUUIDs = append(selection.LineUUIDs, root)
		labels = append(labels, label)
	}
	selection.Count = len(selection.LineUUIDs)
	warnings = append(warnings, nameStepWarnings(steps, labels)...)
	return selection, warnings, nil
}

// cataloguePage reads one page of latticenet.vpn-core/lines catalogue.
func (rt *runtime) cataloguePage(request model.LineCatalogueRequest) (catalogueLabelPage, error) {
	raw, err := rt.rpcCall(vpnCoreLinesService, "catalogue", request)
	if err != nil {
		return catalogueLabelPage{}, errors.New("vpn-core line catalogue failed")
	}
	var page catalogueLabelPage
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&page); err != nil || page.CatalogueVersion == "" {
		return catalogueLabelPage{}, errors.New("vpn-core line catalogue answered an unreadable page")
	}
	return page, nil
}

// nameStepWarnings reports each enabled name-based step (Regex Filter, Regex
// Rename, Regex Delete, Flag) that matches no line under the fleet labels.
// Sort steps reorder whatever names they get and are not checked.
func nameStepWarnings(steps []json.RawMessage, labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	for _, label := range labels {
		if label == "" {
			return []string{"this core reports no line labels, so the name-based steps could not be checked; bind.preview shows the names"}
		}
	}
	var warnings []string
	for index, raw := range steps {
		meta, err := decodeStep(raw)
		if err != nil || meta.Disabled {
			continue
		}
		matched, checked := nameStepMatches(meta.Type, raw, labels)
		if checked && matched == 0 {
			warnings = append(warnings, fmt.Sprintf("step %d (%s) matches 0 of %d lines under the fleet labels", index+1, meta.Type, len(labels)))
		}
	}
	return warnings
}

// nameStepMatches counts the labels a name-based step matches.
func nameStepMatches(stepType string, raw json.RawMessage, labels []string) (int, bool) {
	switch stepType {
	case "Regex Filter", "Regex Rename Operator", "Regex Delete Operator":
		patterns := stepPatterns(stepType, raw)
		var compiled []*regexp.Regexp
		for _, pattern := range patterns {
			if re, err := regexp.Compile(pattern); err == nil {
				compiled = append(compiled, re)
			}
		}
		if len(compiled) == 0 {
			return 0, false
		}
		matched := 0
		for _, label := range labels {
			for _, re := range compiled {
				if re.MatchString(label) {
					matched++
					break
				}
			}
		}
		return matched, true
	case "Flag Operator":
		plan, err := operators.Compile("", []json.RawMessage{raw})
		if err != nil || !plan.Native() {
			return 0, false
		}
		nodes := make([]*nodemodel.Node, len(labels))
		for i, label := range labels {
			nodes[i] = &nodemodel.Node{Fields: map[string]any{"name": label, "type": "vless", "server": "line.invalid", "port": float64(443)}}
		}
		out := plan.Run(nodes, &operators.Context{Target: "URI"})
		matched := 0
		for i, node := range out {
			if i < len(labels) && node.Name() != labels[i] {
				matched++
			}
		}
		return matched, true
	}
	return 0, false
}

// stepPatterns reads the regular expressions a Regex Filter, Regex Rename or
// Regex Delete step carries, in each of the argument shapes upstream wrote.
func stepPatterns(stepType string, raw json.RawMessage) []string {
	var step struct {
		Args json.RawMessage `json:"args"`
	}
	if json.Unmarshal(raw, &step) != nil || len(step.Args) == 0 {
		return nil
	}
	strs := func(v json.RawMessage) []string {
		var one string
		if json.Unmarshal(v, &one) == nil {
			return []string{one}
		}
		var many []string
		if json.Unmarshal(v, &many) == nil {
			return many
		}
		return nil
	}
	renames := func(v json.RawMessage) []string {
		var pairs []struct {
			Expr string `json:"expr"`
		}
		if json.Unmarshal(v, &pairs) != nil {
			return nil
		}
		var out []string
		for _, pair := range pairs {
			out = append(out, pair.Expr)
		}
		return out
	}
	var object map[string]json.RawMessage
	isObject := json.Unmarshal(step.Args, &object) == nil
	switch stepType {
	case "Regex Filter":
		if isObject {
			return strs(object["regex"])
		}
		return strs(step.Args)
	case "Regex Delete Operator":
		if isObject {
			return strs(object["value"])
		}
		return strs(step.Args)
	case "Regex Rename Operator":
		if isObject {
			return renames(object["value"])
		}
		return renames(step.Args)
	}
	return nil
}
