package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
)

// The index is what list reads in one kv.get: one entry per record with
// everything the management view and depends_on need, in manual order, plus
// the fetch bookkeeping (plan section 3.4: it lives here so list reads one
// key, and every index write re-reads the index in the same invocation and
// applies only its own delta).

const (
	// maxIndexRemarkBytes is how much of a remark the index carries; get
	// returns the whole remark.
	maxIndexRemarkBytes = 160
	// storeOrderManual is the only order list reports.
	storeOrderManual = "manual"
)

// indexDocument is the value under storeIndexKey.
type indexDocument struct {
	Version   int             `json:"version"`
	Records   []indexEntry    `json:"records"` // in manual order, Order dense from 0
	Archived  []indexEntry    `json:"archived,omitempty"`
	Migration *migrationState `json:"migration,omitempty"`
	// digest is the stored index's kvDigest as this invocation read it, the
	// if_match every write of this document sends; empty for an index that
	// did not exist, which makes the write a create (store_kv.go).
	digest string
}

type indexEntry struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Name        string     `json:"name"`
	DisplayName string     `json:"display_name,omitempty"`
	Remark      string     `json:"remark,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	Source      string     `json:"source,omitempty"`
	HasURL      bool       `json:"has_url"`
	HasInline   bool       `json:"has_inline_content"`
	Members     []string   `json:"members,omitempty"`
	MemberTags  []string   `json:"member_tags,omitempty"`
	Target      string     `json:"target,omitempty"`
	FileType    string     `json:"file_type,omitempty"`
	NodeSource  string     `json:"node_source,omitempty"`
	StepCount   int        `json:"step_count"`
	StepsOff    int        `json:"disabled_step_count"`
	Imported    bool       `json:"imported"`
	Revision    string     `json:"revision"`
	ContentHash string     `json:"content_hash"`
	Order       int        `json:"order"`
	Flags       indexFlags `json:"flags,omitzero"`
	// Fetch bookkeeping, written by fetch.
	LastFetchAt string `json:"last_fetch_at,omitempty"`
	LastFetchOK *bool  `json:"last_fetch_ok,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	Userinfo    string `json:"userinfo,omitempty"`
	NodesIn     *int   `json:"nodes_in,omitempty"`
	NodesOut    *int   `json:"nodes_out,omitempty"`
	ArchivedAt  string `json:"archived_at,omitempty"`
	// StagedRevision is the revision of the record held in staged-v2-<id>,
	// waiting for a plan; empty when nothing is staged (plan section 2.2).
	StagedRevision string `json:"staged_revision,omitempty"`
	StagedAt       string `json:"staged_at,omitempty"`
	// StagedSource, StagedMembers, StagedMemberTags, StagedTags and
	// StagedMigrated are the staged record's facts the closures and core's
	// publish read without opening staged-v2-<id>: its source, the edges it
	// would create, and whether migrate_record wrote it (MigratedFrom set).
	// StagedKind is the staged record's kind, which a staged edit may change.
	StagedKind       string   `json:"staged_kind,omitempty"`
	StagedSource     string   `json:"staged_source,omitempty"`
	StagedMembers    []string `json:"staged_members,omitempty"`
	StagedMemberTags []string `json:"staged_member_tags,omitempty"`
	StagedTags       []string `json:"staged_tags,omitempty"`
	StagedMigrated   bool     `json:"staged_migrated,omitempty"`
	// StagedRestoredAt is the archived_at of the archive a staged restore
	// came from; discard_staged returns the entry to Archived with it, and
	// the promotion deletes archive-v2-<id>.
	StagedRestoredAt string `json:"staged_restored_at,omitempty"`
	// SelectionVersion is the catalogue version the last fetch wrote.
	SelectionVersion string `json:"selection_version,omitempty"`
	// No legacy {vpn_identity, entry_roots} block: an entry is derived only
	// when its record is written, so no write could backfill it for records
	// not written since 0.18, and a graph record's roots (up to 2048) do not
	// belong in an index bounded at 384 KiB. Core's readers open
	// record-v2-<id> for a legacy-source entry instead.
}

type indexFlags struct {
	RegexIncompatible bool `json:"regex_incompatible,omitempty"`
	HasFallbackStep   bool `json:"has_fallback_step,omitempty"`
	// OwnerCredentials marks a legacy vpn-core or vpn-core-graph record and
	// every collection or file that reads one: it still serves line owner
	// credentials (design-28.md:230). Live state only.
	OwnerCredentials bool `json:"owner_credentials,omitempty"`
	// FleetBound marks a record whose transitive live sources include a fleet
	// record. Live state only: what a staged revision would make fleet-bound
	// is a staged fact, read from the Staged* fields, never folded into this
	// flag. Files cannot be fleet-bound in S2.
	FleetBound bool `json:"fleet_bound,omitempty"`
}

// hasLiveRevision reports whether the entry names a live record. An entry
// built by stagedEntryFor (a staged new record, a staged import of a new id,
// a staged restore) has none.
func (entry indexEntry) hasLiveRevision() bool { return entry.Revision != "" }

// hasStaged reports whether a staged revision waits for a plan.
func (entry indexEntry) hasStaged() bool { return entry.StagedRevision != "" }

// stagedEntryFor is the entry of a record that has no live revision: its id,
// kind, names and order, and its staged facts, and no live fact at all (no
// source, tags, members, node source, flags or fetch bookkeeping), so every
// live-state reader sees nothing in it (plan section 2.2).
func stagedEntryFor(rec subscriptionRecord, stagedAt string) indexEntry {
	entry := indexEntry{ID: rec.ID, Kind: recordKind(rec), Name: rec.Name, DisplayName: rec.DisplayName}
	entry.setStaged(rec, stagedAt)
	return entry
}

// setStaged records rec as the entry's staged revision.
func (entry *indexEntry) setStaged(rec subscriptionRecord, stagedAt string) {
	entry.StagedRevision = subscriptionRevision(rec)
	entry.StagedAt = stagedAt
	entry.StagedKind = recordKind(rec)
	entry.StagedSource = rec.Source
	entry.StagedMembers = rec.Members
	entry.StagedMemberTags = rec.MemberTags
	entry.StagedTags = rec.Tags
	entry.StagedMigrated = rec.MigratedFrom != nil
}

// clearStaged forgets the staged revision's facts.
func (entry *indexEntry) clearStaged() {
	entry.StagedRevision, entry.StagedAt, entry.StagedKind, entry.StagedSource = "", "", "", ""
	entry.StagedMembers, entry.StagedMemberTags, entry.StagedTags = nil, nil, nil
	entry.StagedMigrated, entry.StagedRestoredAt = false, ""
}

// migrationState is migrate_store's progress, kept in the index so a chunk
// that dies resumes where the last one landed.
type migrationState struct {
	Source   string `json:"source"`
	Done     bool   `json:"done"`
	Cursor   string `json:"cursor,omitempty"`
	Verified string `json:"verified_at,omitempty"`
	Legacy   string `json:"legacy_kept,omitempty"`
	// LegacyPrograms are the legacy program keys still to delete, by record
	// id. They are deleted only after the verify, so a migration that stops
	// short never loses a program the legacy document still points at.
	LegacyPrograms []string `json:"legacy_programs,omitempty"`
}

// verified reports whether writes may apply to this index: a store created
// split, or one whose migration verified.
func (idx *indexDocument) verified() bool {
	return idx.Migration == nil || idx.Migration.Verified != ""
}

func (idx *indexDocument) position(id string) int {
	for i := range idx.Records {
		if idx.Records[i].ID == id {
			return i
		}
	}
	return -1
}

func (idx *indexDocument) archivedPosition(id string) int {
	for i := range idx.Archived {
		if idx.Archived[i].ID == id {
			return i
		}
	}
	return -1
}

// renumber makes Order the slice position again after a mutation.
func (idx *indexDocument) renumber() {
	for i := range idx.Records {
		idx.Records[i].Order = i
	}
	for i := range idx.Archived {
		idx.Archived[i].Order = i
	}
}

// indexEntryFor derives a record's entry. Bookkeeping is not derived: the
// caller carries it over from the entry being replaced or the legacy record.
func indexEntryFor(rec subscriptionRecord) indexEntry {
	steps := processSteps(rec)
	off := 0
	for _, raw := range steps {
		if meta, err := decodeStep(raw); err == nil && meta.Disabled {
			off++
		}
	}
	return indexEntry{
		ID: rec.ID, Kind: recordKind(rec), Name: rec.Name,
		DisplayName: rec.DisplayName, Remark: truncateUTF8(rec.Remark, maxIndexRemarkBytes), Tags: rec.Tags,
		Source: rec.Source, HasURL: strings.TrimSpace(rec.URL) != "",
		// A script file's program is not inline content in the sense the view
		// means: the legacy store never reported it as such, and the editor
		// asks "is there pasted node text".
		HasInline: !isScriptFile(rec) && strings.TrimSpace(rec.Content) != "",
		Members:   rec.Members, MemberTags: rec.MemberTags,
		Target: rec.Target, FileType: rec.FileType, NodeSource: rec.NodeSource,
		StepCount: len(steps), StepsOff: off, Imported: rec.Origin != nil,
		Revision: subscriptionRevision(rec), ContentHash: contentHashOf(rec),
		Flags: chainFlags(steps),
	}
}

// contentHashOf fingerprints what a record serves from: the program's digest
// for a script file, the inline content otherwise, nothing when it has none.
func contentHashOf(rec subscriptionRecord) string {
	if isScriptFile(rec) {
		return rec.ScriptDigest
	}
	if rec.Content == "" {
		return ""
	}
	return digestOf(rec.Content)
}

// legacyEntry is a legacy record's entry, bookkeeping included.
func legacyEntry(rec subscriptionRecord) indexEntry {
	entry := indexEntryFor(rec)
	entry.LastFetchAt, entry.LastError, entry.Userinfo = rec.LastFetchAt, rec.LastError, rec.Userinfo
	if rec.LastFetchAt != "" {
		ok := rec.LastFetchOK
		entry.LastFetchOK = &ok
	}
	return entry
}

// carryBookkeeping copies what fetch wrote, and the staged revision's facts,
// onto a freshly derived entry. The staged facts travel with it so a live
// write or a rebuild never orphans staged-v2-<id> or stales a pending plan;
// a promotion clears them explicitly after carrying.
func (entry *indexEntry) carryBookkeeping(from indexEntry) {
	entry.LastFetchAt, entry.LastFetchOK, entry.LastError = from.LastFetchAt, from.LastFetchOK, from.LastError
	entry.Userinfo, entry.NodesIn, entry.NodesOut = from.Userinfo, from.NodesIn, from.NodesOut
	entry.SelectionVersion = from.SelectionVersion
	entry.StagedRevision, entry.StagedAt, entry.StagedKind, entry.StagedSource = from.StagedRevision, from.StagedAt, from.StagedKind, from.StagedSource
	entry.StagedMembers, entry.StagedMemberTags, entry.StagedTags = from.StagedMembers, from.StagedMemberTags, from.StagedTags
	entry.StagedMigrated, entry.StagedRestoredAt = from.StagedMigrated, from.StagedRestoredAt
}

// withBookkeeping hands a record back with its entry's bookkeeping, for the
// replies that always carried it (save, export).
func withBookkeeping(rec subscriptionRecord, entry indexEntry) subscriptionRecord {
	rec.LastFetchAt, rec.LastError, rec.Userinfo = entry.LastFetchAt, entry.LastError, entry.Userinfo
	rec.LastFetchOK = entry.LastFetchOK != nil && *entry.LastFetchOK
	return rec
}

// truncateUTF8 cuts text to at most limit bytes on a rune boundary.
func truncateUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// chainFlags computes an entry's flags from its chain: has_fallback_step
// when an enabled step only the bundle runs (Resolve Domain, a script step),
// regex_incompatible when an enabled step carries a pattern RE2 refuses.
// Such a record keeps rendering on the bundle until its chain is edited. A
// chain that does not compile at all has no flags: render refuses it with
// its own reason.
func chainFlags(steps []json.RawMessage) indexFlags {
	plan, err := operators.Compile("", steps)
	if err != nil {
		return indexFlags{}
	}
	return indexFlags{RegexIncompatible: len(plan.Incompatible()) > 0, HasFallbackStep: plan.HasFallback()}
}

// storeListing is what list, depends_on, export and tag resolution read.
type storeListing struct {
	Records  []indexEntry
	Archived []indexEntry
	// Version is 1 while the legacy document holds records the migration has
	// not verified, 2 otherwise.
	Version int
}

// storeListing reads the store's entries: the index on a split store (one
// call), the legacy document on one that has not migrated (the index miss
// and the document), and both while a migration is in progress.
func (rt *runtime) storeListing() (storeListing, error) {
	idx, err := rt.loadIndex()
	if err != nil {
		return storeListing{}, err
	}
	if idx != nil && idx.verified() {
		return storeListing{Records: idx.Records, Archived: idx.Archived, Version: storeVersionSplit}, nil
	}
	legacy, err := rt.legacyDocument()
	if err != nil {
		return storeListing{}, err
	}
	out := storeListing{Version: storeVersionSplit}
	seen := map[string]bool{}
	if idx != nil {
		out.Records = append(out.Records, idx.Records...)
		for _, entry := range idx.Records {
			seen[entry.ID] = true
		}
	}
	if legacy != nil {
		for _, rec := range legacy.Records {
			if seen[rec.ID] {
				continue
			}
			entry := legacyEntry(rec)
			entry.Order = len(out.Records)
			out.Records = append(out.Records, entry)
		}
		if len(legacy.Records) > 0 {
			out.Version = storeVersionLegacy
		}
	}
	return out, nil
}

// listView is one row of list: today's management view plus revision,
// order, flags and the node counts. It reports what a record IS without its
// stored content: a definition list must not double as a dump of every
// provider payload.
type listView struct {
	ID          string      `json:"id"`
	Kind        string      `json:"kind"`
	Name        string      `json:"name"`
	DisplayName string      `json:"display_name,omitempty"`
	Remark      string      `json:"remark,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Source      string      `json:"source,omitempty"`
	HasURL      bool        `json:"has_url"`
	HasInline   bool        `json:"has_inline_content"`
	Members     []string    `json:"members,omitempty"`
	MemberTags  []string    `json:"member_tags,omitempty"`
	Target      string      `json:"target,omitempty"`
	FileType    string      `json:"file_type,omitempty"`
	NodeSource  string      `json:"node_source,omitempty"`
	Steps       int         `json:"step_count"`
	StepsOff    int         `json:"disabled_step_count"`
	Imported    bool        `json:"imported"`
	Revision    string      `json:"revision,omitempty"`
	Order       int         `json:"order"`
	Flags       *indexFlags `json:"flags,omitempty"`
	NodesIn     *int        `json:"nodes_in,omitempty"`
	NodesOut    *int        `json:"nodes_out,omitempty"`
	// Fetch bookkeeping, emitted only once the record has been fetched at
	// all: before that there is no status to report, and emitting zero
	// values would read as "refresh failed" rather than "never fetched".
	// LastFetchOK is a pointer so a real false survives encoding.
	LastFetchAt string `json:"last_fetch_at,omitempty"`
	LastFetchOK *bool  `json:"last_fetch_ok,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	Userinfo    string `json:"userinfo,omitempty"`
	// The same header, parsed: bytes used up and down, the provider's
	// total, and the expiry in unix seconds. Each is present only when
	// the provider sent it and it parsed, so the overview and the sources
	// table can draw traffic and expiry without a `get` per row.
	providerUsage
	// UserinfoParsed is true on every row that carries the header, and
	// says the figures above are this runtime's whole answer: a field
	// missing beside it was refused (negative, past int64, not a
	// number), and the UI must not parse the header again and bring it
	// back. A runtime without it predates the parse, and the UI falls
	// back to reading the header itself.
	UserinfoParsed bool   `json:"userinfo_parsed,omitempty"`
	ArchivedAt     string `json:"archived_at,omitempty"`
	// The staged revision and its facts (plan section 2.2): a record with a
	// staged revision shows both revisions, and a staged new record shows an
	// empty revision beside its staged one.
	StagedRevision   string   `json:"staged_revision,omitempty"`
	StagedAt         string   `json:"staged_at,omitempty"`
	StagedKind       string   `json:"staged_kind,omitempty"`
	StagedSource     string   `json:"staged_source,omitempty"`
	StagedMembers    []string `json:"staged_members,omitempty"`
	StagedMemberTags []string `json:"staged_member_tags,omitempty"`
	StagedTags       []string `json:"staged_tags,omitempty"`
	StagedMigrated   bool     `json:"staged_migrated,omitempty"`
	StagedRestoredAt string   `json:"staged_restored_at,omitempty"`
	SelectionVersion string   `json:"selection_version,omitempty"`
}

func viewOf(entry indexEntry) listView {
	view := listView{
		ID: entry.ID, Kind: entry.Kind, Name: entry.Name,
		DisplayName: entry.DisplayName, Remark: entry.Remark, Tags: entry.Tags,
		Source: entry.Source, HasURL: entry.HasURL, HasInline: entry.HasInline,
		Members: entry.Members, MemberTags: entry.MemberTags,
		Target: entry.Target, FileType: entry.FileType, NodeSource: entry.NodeSource,
		Steps: entry.StepCount, StepsOff: entry.StepsOff, Imported: entry.Imported,
		Revision: entry.Revision, Order: entry.Order,
		NodesIn: entry.NodesIn, NodesOut: entry.NodesOut, ArchivedAt: entry.ArchivedAt,
		StagedRevision: entry.StagedRevision, StagedAt: entry.StagedAt, StagedKind: entry.StagedKind,
		StagedSource: entry.StagedSource, StagedMembers: entry.StagedMembers,
		StagedMemberTags: entry.StagedMemberTags, StagedTags: entry.StagedTags,
		StagedMigrated: entry.StagedMigrated, StagedRestoredAt: entry.StagedRestoredAt, SelectionVersion: entry.SelectionVersion,
	}
	if entry.Flags != (indexFlags{}) {
		flags := entry.Flags
		view.Flags = &flags
	}
	if entry.LastFetchAt != "" {
		ok := entry.LastFetchOK != nil && *entry.LastFetchOK
		view.LastFetchAt, view.LastFetchOK = entry.LastFetchAt, &ok
		view.LastError, view.Userinfo = entry.LastError, entry.Userinfo
		// Under the same gate as the header: figures from a record that was
		// never fetched would describe a fetch that never happened.
		if entry.Userinfo != "" {
			view.providerUsage = parseProviderUsage(entry.Userinfo)
			view.UserinfoParsed = true
		}
	}
	return view
}

// listSubscriptionsReply is list's whole answer.
func (rt *runtime) listSubscriptionsReply() (json.RawMessage, error) {
	listing, err := rt.storeListing()
	if err != nil {
		return nil, err
	}
	views := make([]listView, 0, len(listing.Records))
	for _, entry := range listing.Records {
		views = append(views, viewOf(entry))
	}
	reply := map[string]any{
		"subscriptions": views,
		"store_version": listing.Version,
		"order":         storeOrderManual,
	}
	if len(listing.Archived) > 0 {
		archived := make([]listView, 0, len(listing.Archived))
		for _, entry := range listing.Archived {
			archived = append(archived, viewOf(entry))
		}
		reply["archived"] = archived
	}
	return json.Marshal(reply)
}

// reorderSubscriptions rewrites the manual order: index read, index write (two
// more on a kv_conflict). ids must name every live record exactly once, so a
// reorder sent from a stale list is refused rather than silently dropping or
// duplicating a row.
func (rt *runtime) reorderSubscriptions(ids []string) error {
	idx, err := rt.writableIndex()
	if err != nil {
		return err
	}
	mutate := func(target *indexDocument) error {
		if len(ids) != len(target.Records) {
			return fmt.Errorf("reorder names %d records, the store holds %d; list again and resend every id once", len(ids), len(target.Records))
		}
		byID := make(map[string]indexEntry, len(target.Records))
		for _, entry := range target.Records {
			byID[entry.ID] = entry
		}
		ordered := make([]indexEntry, 0, len(ids))
		for _, id := range ids {
			entry, ok := byID[id]
			if !ok {
				return fmt.Errorf("reorder names %q, which is not a live record or is named twice", id)
			}
			delete(byID, id)
			ordered = append(ordered, entry)
		}
		target.Records = ordered
		target.renumber()
		return nil
	}
	if err := mutate(idx); err != nil {
		return err
	}
	raw, err := encodeIndex(idx, false)
	if err != nil {
		return err
	}
	return rt.putIndexRetrying(idx, raw, false, mutate)
}
