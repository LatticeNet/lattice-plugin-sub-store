package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// The fleet source (design 28, S2 plan section 1.2). A fleet record selects
// lines from core's line catalogue with its leading run of Structured Filter
// steps. Fetch reads the catalogue under the part of that run core can
// evaluate (the pushed selector), evaluates the rest of the run in Go,
// projects each kept row to fleetRow and stores the rows in the snapshot
// envelope. Render builds one node per row with placeholder credentials,
// runs the record's chain and returns a selection plan; core binds each share
// identity's credentials into it. No fleet credential ever reaches the
// plugin.

const (
	// fleetCatalogueService and fleetCatalogueMethod are core's catalogue,
	// read over rpc.call (srv/server_vpncore.go).
	fleetCatalogueService = "latticenet.vpn-core/lines"
	fleetCatalogueMethod  = "catalogue"
	// fleetCataloguePageRows is the page size every fleet read asks for.
	fleetCataloguePageRows = model.MaxLineCataloguePageRows
	// fleetCatalogueMaxPagesPerPass bounds one pass over the catalogue: ten
	// pages of a thousand rows read a 10000-line catalogue whole.
	fleetCatalogueMaxPagesPerPass = 10
	// fleetCatalogueMaxPasses is the first pass and the one restart a
	// catalogue version move allows (the reader contract,
	// srv/line_catalogue.go:1182-1190). A second move fails the read.
	fleetCatalogueMaxPasses = 2
	// fleetCatalogueVersionPrefix starts every catalogue version core writes
	// (srv/line_catalogue.go:67-69); core refuses a fleet snapshot whose
	// version does not carry it.
	fleetCatalogueVersionPrefix = "lcv1-"
	// fleetEnvelopeDigestPrefix starts a fleet envelope's source_version.
	fleetEnvelopeDigestPrefix = "lfe1-"
	// fleetStepHashPrefix starts the digest of a leading structured run.
	fleetStepHashPrefix = "lsh1-"
)

// Refusal and error codes of the fleet path. Each leads its message, so core
// and the UI can match it.
const (
	codeFleetEnvelopeMismatch         = "fleet_envelope_mismatch"
	codeFleetSelectorMismatch         = "fleet_selector_mismatch"
	codeFleetSnapshotMissing          = "fleet_snapshot_missing"
	codeSnapshotMalformed             = "snapshot_malformed"
	codeSnapshotPredatesS2            = "snapshot_predates_s2"
	codeRevisionUnknown               = "revision_unknown"
	codeStoreInconsistent             = "store_inconsistent"
	codeOwnerCredentialsWithheld      = "owner_credentials_withheld"
	codeFleetResponseChainUnavailable = "fleet_response_chain_unavailable"
	codeFleetPublishUnavailable       = "fleet_publish_unavailable"
	codePlanTooLarge                  = "plan_too_large"
	codeCatalogueUnavailable          = "catalogue_unavailable"
)

// fleetNow is the clock the fetch-time predicates read. Tests replace it.
var fleetNow = func() time.Time { return time.Now().UTC() }

// fleetCatalogueRead is one complete read of the catalogue under a selector.
type fleetCatalogueRead struct {
	CatalogueVersion string
	SelectorFields   []string
	Unavailable      []string
	// Rows are the rows keep admitted, projected, in catalogue order.
	Rows []fleetRow
	// Scanned is how many rows the final pass read.
	Scanned int
	// Pages counts the catalogue calls made, restarts included.
	Pages     int
	Restarted bool
}

// fleetRowKeep decides, for one validated row of a read, whether the fetch
// keeps it. It evaluates the part of the leading structured run core did not;
// nil keeps every row.
type fleetRowKeep func(row fleetRow, now time.Time) (bool, error)

// readFleetCatalogue reads every page of the catalogue under sel with
// fleetCataloguePageRows per page, validating every row once with the SDK's
// Validate, keeping the rows keep admits and projecting them. A page whose
// catalogue_version differs from the first page's restarts the read without
// a cursor, once. maxRows bounds the kept rows (zero: unbounded); past it
// the read stops and the error says how many it found.
func (rt *runtime) readFleetCatalogue(sel *model.LineCatalogueSelector, keep fleetRowKeep, maxRows int) (fleetCatalogueRead, error) {
	if sel != nil {
		if err := sel.Validate(); err != nil {
			return fleetCatalogueRead{}, fmt.Errorf("fleet selector: %w", err)
		}
	}
	now := fleetNow()
	var read fleetCatalogueRead
	for pass := 0; pass < fleetCatalogueMaxPasses; pass++ {
		read.Rows, read.Scanned, read.CatalogueVersion = read.Rows[:0], 0, ""
		seen := map[string]bool{}
		cursor := ""
		moved := false
		for page := 0; ; page++ {
			if page >= fleetCatalogueMaxPagesPerPass {
				return fleetCatalogueRead{}, fmt.Errorf("%s: the catalogue holds more than %d lines under this selection; narrow the selection", codeCatalogueUnavailable, fleetCatalogueMaxPagesPerPass*fleetCataloguePageRows)
			}
			got, err := rt.fetchCataloguePage(sel, cursor)
			read.Pages++
			if err != nil {
				return fleetCatalogueRead{}, err
			}
			if read.CatalogueVersion == "" {
				read.CatalogueVersion = got.CatalogueVersion
				read.SelectorFields = got.SelectorFields
				read.Unavailable = got.Unavailable
			} else if got.CatalogueVersion != read.CatalogueVersion {
				moved = true
				break
			}
			for i := range got.Rows {
				row := &got.Rows[i]
				if seen[row.LineUUID] {
					return fleetCatalogueRead{}, fmt.Errorf("%s: the catalogue listed line %s twice in one read", codeCatalogueUnavailable, row.LineUUID)
				}
				seen[row.LineUUID] = true
				read.Scanned++
				projected := projectFleetRow(*row)
				if keep != nil {
					ok, err := keep(projected, now)
					if err != nil {
						return fleetCatalogueRead{}, err
					}
					if !ok {
						continue
					}
				}
				read.Rows = append(read.Rows, projected)
				if maxRows > 0 && len(read.Rows) > maxRows {
					return fleetCatalogueRead{}, fleetSelectionTooLarge(len(read.Rows), maxRows)
				}
			}
			if got.Cursor == "" {
				break
			}
			cursor = got.Cursor
		}
		if !moved {
			if read.Rows == nil {
				read.Rows = []fleetRow{}
			}
			return read, nil
		}
		read.Restarted = true
	}
	return fleetCatalogueRead{}, fmt.Errorf("%s: the catalogue moved twice during one read; the next refresh reads it again", codeCatalogueUnavailable)
}

// fleetSelectionTooLarge refuses a selection past the plan node bound.
func fleetSelectionTooLarge(found, limit int) error {
	return fmt.Errorf("%s: the selection holds more than %d lines (at least %d); one record carries at most %d; narrow the selection or split the record", snapshotTooLargeCode, limit, found, limit)
}

// fetchCataloguePage reads one catalogue page over rpc.call and validates
// it: the SDK's page Validate (version, cursor, row count, every row by the
// row's own Validate, duplicates, the page byte bound), core's version
// prefix, and a label without control characters. Errors never quote a row:
// a row is credential-free by contract, but a page that fails validation is
// exactly the page whose content is not trusted.
func (rt *runtime) fetchCataloguePage(sel *model.LineCatalogueSelector, cursor string) (model.LineCatalogueResponse, error) {
	request := model.LineCatalogueRequest{Selector: sel, Cursor: cursor, Limit: fleetCataloguePageRows}
	raw, err := rt.callHost(latticeplugin.HostMethodRPCCall, map[string]any{
		"service": fleetCatalogueService,
		"method":  fleetCatalogueMethod,
		"request": request,
	})
	if err != nil {
		return model.LineCatalogueResponse{}, fmt.Errorf("%s: the line catalogue could not be read", codeCatalogueUnavailable)
	}
	var page model.LineCatalogueResponse
	if err := json.Unmarshal(raw, &page); err != nil {
		return model.LineCatalogueResponse{}, fmt.Errorf("%s: the line catalogue page does not decode", codeCatalogueUnavailable)
	}
	if err := validateCataloguePage(page); err != nil {
		return model.LineCatalogueResponse{}, fmt.Errorf("%s: %w", codeCatalogueUnavailable, err)
	}
	return page, nil
}

// validateCataloguePage is the page-level check of fetchCataloguePage.
func validateCataloguePage(page model.LineCatalogueResponse) error {
	if err := page.Validate(); err != nil {
		return err
	}
	if !strings.HasPrefix(page.CatalogueVersion, fleetCatalogueVersionPrefix) {
		return errors.New("catalogue page version does not carry core's prefix")
	}
	// A label becomes a node name, which line-oriented clients read whole.
	for i := range page.Rows {
		if strings.ContainsFunc(page.Rows[i].Label, unicode.IsControl) {
			return fmt.Errorf("catalogue row %d has an invalid label", i)
		}
	}
	return nil
}

// projectFleetRow is the row as a fleet envelope stores it: every field
// render and the predicates read, nothing else (fleetRow). Slices and
// pointers are shared with the decoded page, which is discarded after the
// read.
func projectFleetRow(row model.LineCatalogueRow) fleetRow {
	out := fleetRow{
		LineUUID: row.LineUUID, LineHashID: row.LineHashID, NodeID: row.NodeID,
		NodeName: row.NodeName, Name: row.Name, Label: row.Label,
		NodeTags: row.NodeTags, GroupIDs: row.GroupIDs, Geo: row.Geo, Machine: row.Machine,
		DDNSNames: row.DDNSNames, Protocol: row.Protocol, Transport: row.Transport, Security: row.Security,
		PublicHost: row.PublicHost, ProviderEdge: row.ProviderEdge, Addresses: row.Addresses,
		Managed: row.Managed, Overlay: row.Overlay, OverlayStatus: row.OverlayStatus,
		Status: row.Status, ServiceState: row.ServiceState, Chain: row.Chain, Probe: row.Probe,
	}
	if t := row.Template; t != nil {
		out.Template = &fleetTemplate{Protocol: t.Protocol, Host: t.Host, Port: t.Port, Params: t.Params}
	}
	return out
}

// fleetLattice is a row's Lattice block: every tagged field core carries and
// every row fact a predicate or Structured Sort reads.
func fleetLattice(row fleetRow) *nodemodel.LatticeFields {
	l := &nodemodel.LatticeFields{
		LineUUID: row.LineUUID, LineHashID: row.LineHashID, NodeID: row.NodeID,
		Geo: row.Geo, Tags: row.NodeTags, Groups: row.GroupIDs, Probe: row.Probe, Addresses: row.Addresses,
		NodeName: row.NodeName, Machine: row.Machine, DDNSNames: row.DDNSNames,
		Protocol: row.Protocol, Transport: row.Transport, Security: row.Security,
		Status: row.Status, ServiceState: row.ServiceState, OverlayStatus: row.OverlayStatus,
		Managed: row.Managed, Overlay: row.Overlay, ProviderEdge: row.ProviderEdge, PublicHost: row.PublicHost,
	}
	chain := row.Chain
	l.Chain = &chain
	if row.Template != nil {
		l.TemplateHost = row.Template.Host
	}
	return l
}

// fleetSelection is how a fleet record's leading Structured Filter run
// splits between core and the fetch.
type fleetSelection struct {
	// Pushed is the selector core evaluates; nil selects every line.
	Pushed *model.LineCatalogueSelector
	// Leading is how many leading structured steps the fetch evaluates in
	// all, pushed or in Go; StepHash is the digest of those steps as stored.
	Leading  int
	StepHash string
	// Keep evaluates the leading steps core did not; nil keeps every row.
	Keep fleetRowKeep
}

// record is the selection as the envelope records it.
func (s fleetSelection) record() *fleetSelectorRecord {
	return &fleetSelectorRecord{Pushed: s.Pushed, Leading: s.Leading, StepHash: s.StepHash}
}

// fleetSelectionFor splits a record's leading Structured Filter run between
// core and the fetch: operators.Pushdown names the selector core evaluates
// and how many leading steps it covers, and the steps the fetch evaluated
// are hashed as stored so render can tell whether a revision's run still
// answers the snapshot's rows. Fields not in advertised are never pushed.
//
// yagni: until Predicate.Evaluate lands (lane 2, S2 plan section 2.4) the
// fetch evaluates only the pushed steps, so Leading is the covered count and
// the leading steps core cannot evaluate run at render with the rest of the
// chain, over a wider row set. When Evaluate lands, Leading becomes
// operators.LeadingRun and Keep evaluates the steps between.
func fleetSelectionFor(rec subscriptionRecord, advertised []string) (fleetSelection, error) {
	plan, err := (&runtime{}).chainPlan(rec)
	if err != nil {
		return fleetSelection{}, err
	}
	pushed, covered := operators.Pushdown(plan, advertised)
	steps := processSteps(rec)
	if covered > len(steps) {
		return fleetSelection{}, fmt.Errorf("the pushed run covers %d of %d steps", covered, len(steps))
	}
	return fleetSelection{Pushed: pushed, Leading: covered, StepHash: fleetStepHash(steps[:covered])}, nil
}

// fleetStepHash is the digest of a leading structured run as stored.
func fleetStepHash(run []json.RawMessage) string {
	if run == nil {
		run = []json.RawMessage{}
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		// Stored steps are valid JSON; a run that is not hashes as nothing,
		// which never equals a real run's digest.
		return ""
	}
	sum := sha256.Sum256(encoded)
	return fleetStepHashPrefix + hex.EncodeToString(sum[:])
}

// fleetEnvelopeDigest is a fleet envelope's source_version: "lfe1-" plus the
// sha256 of the canonical encoding of catalogue_version, selector and rows,
// or, for a collection, of every member block in order, whole, provider
// blocks included. Core keys its revalidation and body cache on it, so it
// moves whenever any selected row or any member's content moves.
func fleetEnvelopeDigest(env snapshotEnvelope) (string, error) {
	var canonical []byte
	var err error
	if env.Kind == kindCollection {
		canonical, err = json.Marshal(env.Members)
	} else {
		canonical, err = json.Marshal(struct {
			CatalogueVersion string               `json:"catalogue_version"`
			Selector         *fleetSelectorRecord `json:"selector"`
			Rows             json.RawMessage      `json:"rows"`
		}{env.CatalogueVersion, env.Selector, env.Rows})
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return fleetEnvelopeDigestPrefix + hex.EncodeToString(sum[:]), nil
}

// encodeFleetRows is rows as an envelope's rows field: the literal [] for
// none, never null.
func encodeFleetRows(rows []fleetRow) (json.RawMessage, error) {
	if rows == nil {
		rows = []fleetRow{}
	}
	return json.Marshal(rows)
}

// fleetSubEnvelope is a fleet sub's envelope for one read.
func fleetSubEnvelope(read fleetCatalogueRead, sel fleetSelection) (snapshotEnvelope, error) {
	rows, err := encodeFleetRows(read.Rows)
	if err != nil {
		return snapshotEnvelope{}, err
	}
	env := snapshotEnvelope{
		Version: snapshotEnvelopeVersion, Kind: kindSub, SourceKind: subscriptionSourceFleet,
		CatalogueVersion: read.CatalogueVersion, Rows: rows, Selector: sel.record(),
	}
	if env.SourceVersion, err = fleetEnvelopeDigest(env); err != nil {
		return snapshotEnvelope{}, err
	}
	return env, nil
}

// encodeFleetEnvelope is encodeSnapshotEnvelope for a fleet sub: rows are
// never dropped for size, so an envelope past the core's bound is refused
// with snapshot_too_large and a figure the operator can act on, computed
// from the measured average row size of this fetch.
func encodeFleetEnvelope(env snapshotEnvelope, rowCount int) (string, error) {
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	if len(raw) <= model.MaxSubscriptionRawBytes {
		return string(raw), nil
	}
	overhead := len(raw) - len(env.Rows)
	perRow := len(env.Rows) / max(rowCount, 1)
	fit := (model.MaxSubscriptionRawBytes - overhead) / max(perRow, 1)
	return "", fmt.Errorf("%s: the selection holds %d lines; this core keeps at most %d MiB of snapshot, about %d lines of this fleet; narrow the selection or split the record", snapshotTooLargeCode, rowCount, model.MaxSubscriptionRawBytes>>20, max(fit, 0))
}

// fetchFleetSub is fetch for a fleet sub: read the catalogue under the
// pushed selector, keep the rows the rest of the leading run admits, and
// store them. A selection that matches no line is a valid fetch: the
// envelope carries rows as [] and render builds a plan with no nodes, which
// core answers with the decoy (empty_plan).
//
// nodes_in is the row count; nodes_out is the count after the whole chain
// when it runs in Go, built over placeholder credentials that are never
// stored.
func (rt *runtime) fetchFleetSub(rec subscriptionRecord) (fetchResult, error) {
	sel, read, err := rt.readFleetSelection(rec)
	if err != nil {
		return fetchResult{}, fmt.Errorf("subscription %q: %w", rec.ID, err)
	}
	env, err := fleetSubEnvelope(read, sel)
	if err != nil {
		return fetchResult{}, err
	}
	raw, err := encodeFleetEnvelope(env, len(read.Rows))
	if err != nil {
		return fetchResult{}, fmt.Errorf("subscription %q: %w", rec.ID, err)
	}
	out := fetchResult{Raw: raw, SourceVersion: env.SourceVersion}
	count := len(read.Rows)
	out.nodesIn = &count
	if after, ok := fleetNodesOut(rec, read.Rows); ok {
		out.nodesOut = &after
	}
	return out, nil
}

// readFleetSelection reads the catalogue under a record's selection. The
// selector is first computed against every field the SDK defines; when the
// first page advertises fewer and the pushed selector names one it does not,
// the selection is computed again against what core advertised and read
// again, so a narrower core still answers rather than refusing the field.
func (rt *runtime) readFleetSelection(rec subscriptionRecord) (fleetSelection, fleetCatalogueRead, error) {
	sel, err := fleetSelectionFor(rec, model.LineCatalogueSelectorFields())
	if err != nil {
		return fleetSelection{}, fleetCatalogueRead{}, err
	}
	read, err := rt.readFleetCatalogue(sel.Pushed, sel.Keep, model.MaxSubscriptionRecordNodes)
	if err != nil {
		return fleetSelection{}, fleetCatalogueRead{}, err
	}
	if sel.Pushed == nil || len(sel.Pushed.UnsupportedFields(read.SelectorFields)) == 0 {
		return sel, read, nil
	}
	if sel, err = fleetSelectionFor(rec, read.SelectorFields); err != nil {
		return fleetSelection{}, fleetCatalogueRead{}, err
	}
	pages := read.Pages
	read, err = rt.readFleetCatalogue(sel.Pushed, sel.Keep, model.MaxSubscriptionRecordNodes)
	read.Pages += pages
	return sel, read, err
}

// ownerCredentialsWithheldError is the answer of every path that would read
// a legacy vpn-core or vpn-core-graph record's export outside core's
// refresh: the export carries line owner credentials, or an identity's, and
// only the fetch core issues may read it (S2 plan section 1.2).
func ownerCredentialsWithheldError(id string) error {
	return fmt.Errorf("%s: subscription %q serves line owner credentials, which only core's refresh may read; migrate it to a fleet record to render it here", codeOwnerCredentialsWithheld, id)
}

// factsOfEntry is a live index entry as the fleet rules read it.
func factsOfEntry(entry indexEntry) fleetRecordFacts {
	return fleetRecordFacts{
		ID: entry.ID, Kind: entry.Kind, Source: entry.Source, Tags: entry.Tags,
		Members: entry.Members, MemberTags: entry.MemberTags, NodeSource: entry.NodeSource,
	}
}

// fleetBoundLive reports whether a record is fleet-bound over the live
// store: a fleet sub, a collection that gathers one, or a file whose node
// source is either. A sub costs no host call; a collection or a file reads
// the listing once.
//
// yagni: lane 4's index carries the live fleet_bound flag (S2 plan section
// 2.2), computed at every index write; when it lands this reads the
// record's entry instead of recomputing the closure here.
func (rt *runtime) fleetBoundLive(rec subscriptionRecord) (bool, error) {
	self := factsOf(rec)
	if self.Kind == kindSub {
		return self.Source == subscriptionSourceFleet, nil
	}
	listing, err := rt.storeListing()
	if err != nil {
		return false, err
	}
	universe := make([]fleetRecordFacts, 0, len(listing.Records)+1)
	byID := map[string]fleetRecordFacts{}
	for _, entry := range listing.Records {
		if entry.ID == rec.ID {
			continue
		}
		facts := factsOfEntry(entry)
		universe = append(universe, facts)
		byID[facts.ID] = facts
	}
	universe = append(universe, self)
	if self.Kind == kindFile {
		source, ok := byID[strings.TrimSpace(self.NodeSource)]
		return ok && fleetBoundUnder(source, universe), nil
	}
	return fleetBoundUnder(self, universe), nil
}

// refuseMixedMembers refuses a collection whose members are a legacy and a
// fleet record together, naming both sides (fleet_mixed_owner_credentials).
// Core serves a collection's provider nodes to every identity unvalidated,
// so a legacy member beside a fleet member would hand line owner
// credentials, or another identity's, to the collection's identity holders.
func refuseMixedMembers(collectionID string, members []subscriptionRecord) error {
	var fleet, legacy []string
	for _, member := range members {
		switch {
		case member.Source == subscriptionSourceFleet:
			fleet = append(fleet, member.ID)
		case isVPNCoreSource(member.Source):
			legacy = append(legacy, member.ID)
		}
	}
	if len(fleet) > 0 && len(legacy) > 0 {
		return mixedRefusal(collectionID, fleet, legacy)
	}
	return nil
}

// fetchFleetMemberBlock reads a fleet member's selection for its collection's
// snapshot: the catalogue under the member's own pushed selector, its own
// leading run, its rows projected. The member's chain is not run.
func (rt *runtime) fetchFleetMemberBlock(member subscriptionRecord) (*fleetMemberBlock, error) {
	sel, read, err := rt.readFleetSelection(member)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", memberLabel(member), err)
	}
	rows, err := encodeFleetRows(read.Rows)
	if err != nil {
		return nil, err
	}
	return &fleetMemberBlock{catalogueVersion: read.CatalogueVersion, selector: sel.record(), rows: rows, count: len(read.Rows)}, nil
}

// fleetCollectionVersion is a collection's derived catalogue version: "lcv1-"
// plus the sha256 of its fleet members' versions in member order, so core's
// unchanged selection reader reads a collection envelope as it reads a sub's.
func fleetCollectionVersion(versions []string) string {
	sum := sha256.Sum256([]byte(strings.Join(versions, "\n")))
	return fleetCatalogueVersionPrefix + hex.EncodeToString(sum[:])
}

// finishFleetCollectionEnvelope writes a collection envelope's top-level
// fleet block when any member is a fleet member: the derived catalogue
// version, one union row per selected line (the first member that selected
// it), and the envelope digest as the source version. It reports whether the
// collection has a fleet member.
func finishFleetCollectionEnvelope(env *snapshotEnvelope) (bool, error) {
	var versions []string
	union := []fleetUnionRow{}
	seen := map[string]bool{}
	for i, member := range env.Members {
		if member.Source != subscriptionSourceFleet {
			continue
		}
		versions = append(versions, member.CatalogueVersion)
		var rows []struct {
			LineUUID string `json:"line_uuid"`
		}
		if err := json.Unmarshal(member.Rows, &rows); err != nil {
			return false, err
		}
		for _, row := range rows {
			if !seen[row.LineUUID] {
				seen[row.LineUUID] = true
				union = append(union, fleetUnionRow{LineUUID: row.LineUUID, Member: i})
			}
		}
	}
	if versions == nil {
		return false, nil
	}
	rows, err := json.Marshal(union)
	if err != nil {
		return false, err
	}
	env.CatalogueVersion, env.Rows = fleetCollectionVersion(versions), rows
	env.SourceVersion, err = fleetEnvelopeDigest(*env)
	return true, err
}

// fleetCollectionTooLarge restates an envelope refusal for a collection with
// fleet members, naming the collection and the member that holds the most
// lines, so the operator knows which selection to narrow.
func fleetCollectionTooLarge(collectionID string, members []fileScriptMember, cause error) error {
	total, largest, largestID := 0, -1, ""
	for _, member := range members {
		if member.block.fleet == nil {
			continue
		}
		total += member.block.fleet.count
		if member.block.fleet.count > largest {
			largest, largestID = member.block.fleet.count, member.block.id
		}
	}
	if largestID == "" {
		return cause
	}
	return fmt.Errorf("%s: collection %q holds %d fleet lines and its snapshot is over the %d MiB the core keeps; member %q holds the most, %d; narrow its selection or split the collection", snapshotTooLargeCode, collectionID, total, model.MaxSubscriptionRawBytes>>20, largestID, largest)
}
