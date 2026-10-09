package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
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
}

type indexFlags struct {
	RegexIncompatible bool `json:"regex_incompatible,omitempty"`
	HasFallbackStep   bool `json:"has_fallback_step,omitempty"`
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

// carryBookkeeping copies what fetch wrote onto a freshly derived entry.
func (entry *indexEntry) carryBookkeeping(from indexEntry) {
	entry.LastFetchAt, entry.LastFetchOK, entry.LastError = from.LastFetchAt, from.LastFetchOK, from.LastError
	entry.Userinfo, entry.NodesIn, entry.NodesOut = from.Userinfo, from.NodesIn, from.NodesOut
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

// fallbackStepTypes are the steps the bundle has to run (plan section 2.3,
// KindFallback): Resolve Domain needs the network and the two script steps
// run operator JavaScript.
var fallbackStepTypes = map[string]bool{
	"Resolve Domain Operator": true,
	"Script Operator":         true,
	"Script Filter":           true,
}

// chainFlags computes an entry's flags from its chain.
//
// This is the seam the operators lane fills: once system-go/operators exists,
// the body becomes operators.Compile and its Plan.HasFallback and
// Plan.Incompatible. Until then a pattern is incompatible when Go's regexp,
// which is RE2, refuses it (lookaround and backreferences are the cases that
// matter), and that is the same set operators.Compile flags as
// regex_incompatible.
func chainFlags(steps []json.RawMessage) indexFlags {
	var flags indexFlags
	for _, raw := range steps {
		var step struct {
			Type     string          `json:"type"`
			Disabled bool            `json:"disabled"`
			Args     json.RawMessage `json:"args"`
		}
		if json.Unmarshal(raw, &step) != nil || step.Disabled {
			continue
		}
		if fallbackStepTypes[step.Type] {
			flags.HasFallbackStep = true
		}
		for _, pattern := range stepPatterns(step.Type, step.Args) {
			if _, err := regexp.Compile(pattern); err != nil {
				flags.RegexIncompatible = true
			}
		}
	}
	return flags
}

// stepPatterns returns the operator-written regular expressions in one
// step's arguments, in the wire shapes ui/src/operatorSchema.ts writes:
// Regex Filter {"regex": [...]}, Regex Delete and Regex Sort a bare list,
// Regex Rename a bare list of {"expr", "now"}. A legacy {"value": ...}
// wrapper and a single string are read too, as the UI reads them back.
func stepPatterns(stepType string, args json.RawMessage) []string {
	var value any
	if len(args) == 0 || json.Unmarshal(args, &value) != nil {
		return nil
	}
	unwrap := func(v any, key string) any {
		if object, ok := v.(map[string]any); ok {
			return object[key]
		}
		return v
	}
	switch stepType {
	case "Regex Filter":
		return stringsIn(unwrap(value, "regex"), "")
	case "Regex Delete Operator", "Regex Sort Operator":
		return stringsIn(unwrap(value, "value"), "")
	case "Regex Rename Operator":
		return stringsIn(unwrap(value, "value"), "expr")
	default:
		return nil
	}
}

// stringsIn collects non-empty strings from a string, a list of strings, or
// (with key set) a list of objects carrying key.
func stringsIn(value any, key string) []string {
	var out []string
	add := func(v any) {
		if key != "" {
			if object, ok := v.(map[string]any); ok {
				v = object[key]
			}
		}
		if text, ok := v.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	if list, ok := value.([]any); ok {
		for _, item := range list {
			add(item)
		}
		return out
	}
	add(value)
	return out
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

// reorderSubscriptions rewrites the manual order: index read, index write.
// ids must name every live record exactly once, so a reorder sent from a
// stale list is refused rather than silently dropping or duplicating a row.
func (rt *runtime) reorderSubscriptions(ids []string) error {
	idx, err := rt.writableIndex()
	if err != nil {
		return err
	}
	if len(ids) != len(idx.Records) {
		return fmt.Errorf("reorder names %d records, the store holds %d; list again and resend every id once", len(ids), len(idx.Records))
	}
	byID := make(map[string]indexEntry, len(idx.Records))
	for _, entry := range idx.Records {
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
	idx.Records = ordered
	idx.renumber()
	return rt.putIndex(idx, false)
}
