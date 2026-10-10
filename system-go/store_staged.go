package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// Staging (design 28, S2; plan sections 2.7 and 3). A record that is
// fleet-bound before or after a write is not written live: its new content
// goes to staged-v2-<id> and waits for core's plan, whose apply calls
// apply_revision with a one-time grant, or for plans.publish when core finds
// no live share the change could reach. Every record write goes through
// storeWriteRecord, so import, migrate, restore and migrate_record cannot put
// an unreviewed selection in front of identity shares either.
//
// The plugin never decides whether live shares exist: it cannot see them, and
// core keeps a share whose record was deleted and purged. Staging is decided
// from the index alone.

const storeStagedPrefix = "staged-v2-"

func stagedKey(id string) string { return storeStagedPrefix + storeKeyComponent(id) }

// stagedDocument is the value under staged-v2-<id>: the staged record, the
// live revision it was staged over (empty for a record with no live
// revision: a new record, a new id from an import, a restore), and when.
type stagedDocument struct {
	Record       subscriptionRecord `json:"record"`
	BaseRevision string             `json:"base_revision"`
	StagedAt     string             `json:"staged_at"`
}

// encodeStaged encodes a staged document. The record inside is bounded at
// maxRecordDocBytes exactly as a live record is, so apply_revision's staged
// read is bounded too.
func encodeStaged(doc stagedDocument) ([]byte, error) {
	doc.Record.LastFetchAt, doc.Record.LastFetchOK, doc.Record.LastError, doc.Record.Userinfo = "", false, "", ""
	record, err := json.Marshal(doc.Record)
	if err != nil {
		return nil, err
	}
	if len(record) > maxRecordDocBytes {
		return nil, fmt.Errorf("subscription %q is %d bytes stored, limit %d", doc.Record.ID, len(record), maxRecordDocBytes)
	}
	return json.Marshal(doc)
}

// loadStaged reads staged-v2-<id>; nil when nothing is staged. The record's
// revision is recomputed, never trusted from storage.
func (rt *runtime) loadStaged(id string) (*stagedDocument, error) {
	value, found, err := rt.kvGet(stagedKey(id))
	if err != nil || !found {
		return nil, err
	}
	if len(value) > maxRecordDocBytes+4<<10 {
		return nil, fmt.Errorf("staged revision of %q exceeds %d bytes stored", id, maxRecordDocBytes)
	}
	var doc stagedDocument
	if err := json.Unmarshal(value, &doc); err != nil {
		return nil, fmt.Errorf("decode staged revision of %q: %w", id, err)
	}
	doc.Record = withRevision(doc.Record)
	return &doc, nil
}

// ── Refusals ──────────────────────────────────────────────────────────────

// The codes a mutating method answers as a structured refusal (plan section
// 2.6). The gateway replaces every error a mutating method returns with one
// fixed string, because plugin diagnostics are untrusted, so a refusal the UI
// has to act on travels as a successful {saved: false, refused} reply.
const (
	refusedFleetToLegacy        = "fleet_to_legacy_refused"
	refusedLegacySourceRetired  = "legacy_source_retired"
	refusedMixedOwnerCreds      = "fleet_mixed_owner_credentials"
	refusedFleetFileUnavailable = "fleet_file_unavailable"
	refusedScriptLink           = "fleet_script_link_unavailable"
	refusedDeleteRequiresPlan   = "fleet_delete_requires_plan"
	refusedPromotionPending     = "promotion_pending"
	refusedStagedMismatch       = "staged_revision_mismatch"
	refusedStoreInconsistent    = "store_inconsistent"
	refusedKVConflict           = kvConflictCode
	refusedRegexIncompatible    = "regex_incompatible"
	refusedMigrationRequired    = storeMigrationRequiredCode
)

// storeRefusal is a structured refusal. Message is fixed text per code with
// record ids, revisions and codes interpolated, and never a wrapped error's
// string, a URL, raw content or a diagnostic: it reaches the browser through
// the successful-result channel, which the gateway does not strip.
type storeRefusal struct {
	Code           string   `json:"code"`
	IDs            []string `json:"ids,omitempty"`
	Files          []string `json:"files,omitempty"`
	Collections    []string `json:"collections,omitempty"`
	StagedRevision string   `json:"staged_revision,omitempty"`
	Message        string   `json:"message"`
}

// refusalError carries a refusal through code that returns errors. A mutating
// handler turns it back into the structured reply with mutationResponse.
type refusalError struct{ refusal storeRefusal }

func (e *refusalError) Error() string { return e.refusal.Code + ": " + e.refusal.Message }

func refusalErr(r storeRefusal) error { return &refusalError{refusal: r} }

// asRefusal recognises a refusal anywhere in err's chain, and maps the two
// store-level errors a mutating method may meet (an unmigrated store and an
// exhausted compare-and-swap) onto their codes.
func asRefusal(err error) (storeRefusal, bool) {
	var re *refusalError
	if errors.As(err, &re) {
		return re.refusal, true
	}
	if r, ok := ruleRefusal(err); ok {
		return r, true
	}
	if errors.Is(err, errStoreMigrationRequired) {
		return storeRefusal{Code: refusedMigrationRequired, Message: "the subscription store still holds the legacy single document; run migrate_store until it reports done, then retry"}, true
	}
	if isKVConflict(err) {
		return storeRefusal{Code: refusedKVConflict, Message: "the subscription index changed twice while this write ran; nothing further was written, retry"}, true
	}
	return storeRefusal{}, false
}

// refusedResponse is the successful reply of a refused mutation. saved is
// false so a caller that does not read refused still never claims the write
// landed.
func refusedResponse(r storeRefusal) response {
	return latticeplugin.RawResultResponse(mustJSON(map[string]any{"saved": false, "refused": r}), "")
}

// mutationResponse answers err from a mutating method: a refusal travels as
// the structured reply, anything else as an error.
func mutationResponse(err error) response {
	if r, ok := asRefusal(err); ok {
		return refusedResponse(r)
	}
	return latticeplugin.ErrorResponse(err)
}

func promotionPendingRefusal(id, entryRevision, recordRevision string) storeRefusal {
	return storeRefusal{
		Code: refusedPromotionPending, IDs: []string{id}, StagedRevision: entryRevision,
		Message: fmt.Sprintf("subscription %q is being promoted to revision %s (the stored record is at %s); the next refresh completes it, then retry", id, entryRevision, orNone(recordRevision)),
	}
}

func storeInconsistentRefusal(id, revision string) storeRefusal {
	return storeRefusal{
		Code: refusedStoreInconsistent, IDs: []string{id},
		Message: fmt.Sprintf("subscription %q has an index entry at revision %s but no stored record and nothing staged at it; run migrate_store with rebuild", id, revision),
	}
}

func orNone(revision string) string {
	if revision == "" {
		return "none"
	}
	return revision
}

// ── Loading ───────────────────────────────────────────────────────────────

// recordState is how a record's index entry and its documents agree.
type recordState int

const (
	// recordConsistent: record-v2 is at the entry's revision (or the record
	// lives in a legacy document the store has not migrated yet).
	recordConsistent recordState = iota
	// recordPromotionPending: the entry names the revision staged-v2 holds
	// while record-v2 is missing or at another revision. An approved
	// promotion was killed after its index put; fetch completes it, render
	// and probe serve the staged document, every write refuses.
	recordPromotionPending
	// recordAhead: record-v2 is at another revision than the entry's and
	// nothing is staged at the entry's. A live write whose index put was
	// refused left it; the record is authoritative, as in S1.
	recordAhead
	// recordStagedOnly: the entry has no live revision (stagedEntryFor).
	recordStagedOnly
	// recordInconsistent: the entry names a live revision with no record,
	// nothing staged at it and no legacy document.
	recordInconsistent
	// recordMissing: no live entry and no record anywhere.
	recordMissing
)

// loadedRecord is a record as every method opens it (storeLoadRecord).
type loadedRecord struct {
	ID string
	// Entry is the live list's entry; HasEntry is false when there is none.
	Entry    indexEntry
	HasEntry bool
	// Archived is true when the id is in the archived list.
	Archived bool
	// Live is the record the store serves, LiveRevision its revision: the
	// entry's during a half-done promotion (Live is then the staged record),
	// the record's otherwise.
	Live         subscriptionRecord
	HasLive      bool
	LiveRevision string
	// Staged is the staged document: read when the caller asked for it and
	// the entry has a staged revision, and whenever classifying the record
	// needed it. During a half-done promotion it is the document being
	// promoted.
	Staged *stagedDocument
	State  recordState
}

// pendingStaged is the staged document of a revision still waiting for a
// plan, or nil (during a half-done promotion the staged document is the one
// being promoted, not a pending one).
func (l loadedRecord) pendingStaged() *stagedDocument {
	if l.Staged == nil || l.State == recordPromotionPending || !l.Entry.hasStaged() {
		return nil
	}
	if l.Staged.Record.Revision != l.Entry.StagedRevision {
		return nil
	}
	return l.Staged
}

// storeLoadRecord opens one record: the one reader every method uses (plan
// section 2.7). idx is the index the caller already read (nil on a store with
// none). It reads record-v2-<id>, and staged-v2-<id> only when wantStaged and
// a staged revision exists, or when the record disagrees with its entry and
// the staged document decides which state that is. It consults staged-v2
// before loadRecord's fall-through to the legacy document, so a never-live
// promotion killed after its index put is completed, never "not found".
func (rt *runtime) storeLoadRecord(idx *indexDocument, id string, wantStaged bool) (loadedRecord, error) {
	out := loadedRecord{ID: id, State: recordMissing}
	if idx != nil {
		if pos := idx.position(id); pos >= 0 {
			out.Entry, out.HasEntry = idx.Records[pos], true
		} else if idx.archivedPosition(id) >= 0 {
			out.Archived = true
		}
	}
	if out.HasEntry && !out.Entry.hasLiveRevision() {
		out.State = recordStagedOnly
		if wantStaged && out.Entry.hasStaged() {
			staged, err := rt.loadStaged(id)
			if err != nil {
				return loadedRecord{}, err
			}
			out.Staged = staged
		}
		return out, nil
	}
	value, found, err := rt.kvGet(recordKey(id))
	if err != nil {
		return loadedRecord{}, err
	}
	var rec subscriptionRecord
	if found {
		if rec, err = decodeRecordDoc(id, value); err != nil {
			return loadedRecord{}, err
		}
		rec = withRevision(rec)
	}
	if !out.HasEntry {
		// No live entry: a store that has not migrated, or an orphan key.
		if found {
			out.Live, out.HasLive, out.LiveRevision, out.State = rec, true, rec.Revision, recordConsistent
			return out, nil
		}
		return rt.loadLegacyInto(out)
	}
	if found && rec.Revision == out.Entry.Revision {
		out.Live, out.HasLive, out.LiveRevision, out.State = rec, true, rec.Revision, recordConsistent
		if wantStaged && out.Entry.hasStaged() {
			if out.Staged, err = rt.loadStaged(id); err != nil {
				return loadedRecord{}, err
			}
		}
		return out, nil
	}
	staged, err := rt.loadStaged(id)
	if err != nil {
		return loadedRecord{}, err
	}
	out.Staged = staged
	switch {
	case staged != nil && staged.Record.Revision == out.Entry.Revision:
		out.Live, out.HasLive, out.LiveRevision, out.State = staged.Record, true, out.Entry.Revision, recordPromotionPending
	case found:
		out.Live, out.HasLive, out.LiveRevision, out.State = rec, true, rec.Revision, recordAhead
	default:
		legacy, err := rt.loadLegacyInto(out)
		if err != nil {
			return loadedRecord{}, err
		}
		if legacy.HasLive {
			return legacy, nil
		}
		out.State = recordInconsistent
	}
	return out, nil
}

// loadLegacyInto fills out from the legacy document, the store's last
// fall-through (loadRecord's own).
func (rt *runtime) loadLegacyInto(out loadedRecord) (loadedRecord, error) {
	legacy, err := rt.legacyDocument()
	if err != nil || legacy == nil {
		return out, err
	}
	for _, rec := range legacy.Records {
		if rec.ID != out.ID {
			continue
		}
		rec = withRevision(rec)
		if isScriptFile(rec) {
			script, err := rt.getFileScript(out.ID)
			if err != nil {
				return loadedRecord{}, err
			}
			rec.Content = script
		}
		out.Live, out.HasLive, out.LiveRevision, out.State = rec, true, rec.Revision, recordConsistent
		return out, nil
	}
	return out, nil
}

// writeRefusalFor is the refusal every write method answers for a record in a
// state no write may land on.
func (l loadedRecord) writeRefusal() (storeRefusal, bool) {
	switch l.State {
	case recordPromotionPending:
		recordRevision := ""
		if l.Entry.Revision != l.LiveRevision {
			recordRevision = l.LiveRevision
		}
		return promotionPendingRefusal(l.ID, l.Entry.Revision, recordRevision), true
	case recordInconsistent:
		return storeInconsistentRefusal(l.ID, l.Entry.Revision), true
	}
	return storeRefusal{}, false
}

// storeCompletePromotion rolls a half-done promotion forward: it puts the
// record from staged-v2 and deletes the staged key (two host calls). Only the
// fetch handler calls it, keyed on the invocation's method; render and probe
// serve the staged document and write nothing.
func (rt *runtime) storeCompletePromotion(l loadedRecord) error {
	if l.State != recordPromotionPending || l.Staged == nil {
		return fmt.Errorf("subscription %q has no half-done promotion to complete", l.ID)
	}
	raw, err := encodeRecord(l.Staged.Record)
	if err != nil {
		return err
	}
	if err := rt.kvPut(recordKey(l.ID), raw); err != nil {
		return err
	}
	return rt.kvDelete(stagedKey(l.ID))
}

// ── Writing ───────────────────────────────────────────────────────────────

// writeOrigin names the method a record write comes from. The guards differ
// per origin only where the plan says so: legacy_source_retired is save's,
// and the mixing guard reads live state at migrate_record.
type writeOrigin string

const (
	originSave          writeOrigin = "save"
	originImport        writeOrigin = "import"
	originMigrate       writeOrigin = "migrate"
	originRestore       writeOrigin = "restore"
	originMigrateRecord writeOrigin = "migrate_record"
)

// writeRequest is one record to write.
type writeRequest struct {
	Record subscriptionRecord
	// IfRevision makes the write conditional (save only): the stored record's
	// live or staged revision must still be this one.
	IfRevision string
	// Strict is the editor's save: a chain that brings in a pattern RE2
	// refuses is refused (savedChainCheck).
	Strict bool
}

// writeResult is what storeWriteRecord did with one record. Exactly one of
// the outcomes holds: written live, staged, discarded back to live content,
// refused, a conditional conflict, or skipped for failing validation.
type writeResult struct {
	ID string
	// Record is the record as written live or as staged, with the entry's
	// fetch bookkeeping attached.
	Record subscriptionRecord
	// Staged is true when the record went to staged-v2-<id>;
	// StagedRevision is then its revision and BaseRevision the live one.
	Staged         bool
	StagedRevision string
	BaseRevision   string
	// Discarded names a staged revision this write replaced with content
	// equal to the live record: nothing is left to stage.
	Discarded string
	// Replaced is true when the id already had a live or staged revision.
	Replaced bool
	Conflict *saveConflict
	Refused  *storeRefusal
	// Skipped is a validation failure's reason (batch writes).
	Skipped string
}

// landed reports whether the write changed the store.
func (r writeResult) landed() bool {
	return r.Conflict == nil && r.Refused == nil && r.Skipped == ""
}

// storeWriteRecord is the one path that writes a record document (plan
// section 3). It stages when the record is fleet-bound before or after the
// write and writes live otherwise, a brand-new id included, and runs the
// guards every origin runs: fleet_script_link_unavailable,
// fleet_to_legacy_refused, legacy_source_retired (save only),
// fleet_mixed_owner_credentials and fleet_file_unavailable. The live branch
// keeps storeSave's record-then-index order; the staged branch writes
// staged-v2 then the index. Every index write carries if_match and earns one
// retry.
//
// Host calls: the index; the stored record when the id has a live revision,
// and its staged document when one exists or the record disagrees with its
// entry; the record or staged put; the index put; two more on a kv_conflict.
func (rt *runtime) storeWriteRecord(req writeRequest, origin writeOrigin) (writeResult, error) {
	results, err := rt.storeWriteRecords([]writeRequest{req}, origin)
	if err != nil {
		return writeResult{}, err
	}
	return results[0], nil
}

// pendingWrite is one record's write inside storeWriteRecords, once decided.
type pendingWrite struct {
	result *writeResult
	req    writeRequest
	// incoming is the fetch bookkeeping a backup or a migration carries on
	// the record; the index is where it lives now.
	incoming indexEntry
	rec      subscriptionRecord
	// entry is the id's live-list entry when it has one.
	entry    indexEntry
	hasEntry bool
	decided  bool
	stage    bool
	discard  bool
	base     string
	key      string
	raw      []byte
}

// storeWriteRecords writes many records with one index read and one index
// write. A single save reads the stored record for its conditional check and
// the fields it preserves; a batch (import, migrate) writes records it never
// read, as saveSubscriptionBatch did, and refuses itself whole before any
// write when its frames could not fit one call.
func (rt *runtime) storeWriteRecords(reqs []writeRequest, origin writeOrigin) ([]writeResult, error) {
	results := make([]writeResult, len(reqs))
	if len(reqs) == 0 {
		return results, nil
	}
	single := len(reqs) == 1 && origin == originSave
	if single {
		if strings.TrimSpace(reqs[0].Record.ID) == "" {
			return nil, fmt.Errorf("subscription id is required")
		}
		if err := validStoreID(reqs[0].Record.ID); err != nil {
			return nil, err
		}
	}
	idx, err := rt.writableIndex()
	if err != nil {
		return nil, err
	}
	return rt.storeWriteRecordsIn(idx, reqs, origin, false)
}

// storeWriteRecordsIn is storeWriteRecords over an index the caller already
// read. opened says the caller opened every record through storeLoadRecord
// and refused the states no write may land on, so the batch skips its own
// check for a half-done promotion.
func (rt *runtime) storeWriteRecordsIn(idx *indexDocument, reqs []writeRequest, origin writeOrigin, opened bool) ([]writeResult, error) {
	results := make([]writeResult, len(reqs))
	if len(reqs) == 0 {
		return results, nil
	}
	single := len(reqs) == 1 && origin == originSave
	now := time.Now().UTC().Format(time.RFC3339)
	writes := make([]*pendingWrite, 0, len(reqs))
	var overlay []recordFacts
	for i, req := range reqs {
		res := &results[i]
		res.ID = req.Record.ID
		nrec, err := normalizeSubscriptionForStore(req.Record)
		if err != nil {
			if single {
				return nil, err
			}
			res.Skipped = err.Error()
			continue
		}
		if idx.archivedPosition(nrec.ID) >= 0 {
			if single {
				res.Conflict = &saveConflict{reason: "archived"}
			} else {
				res.Skipped = "archived: restore or purge the archived record with this id first"
			}
			continue
		}
		overlay = append(overlay, factsOfRecord(nrec))
		w := &pendingWrite{result: res, req: req}
		if !single {
			w.incoming = legacyEntry(req.Record)
		}
		writes = append(writes, w)
	}
	// The graphs the guards read, with the batch's own records overlaid at
	// their new content, so records written together see each other.
	live := newStoreGraph(idx.Records, graphLive)
	union := newStoreGraph(idx.Records, graphUnion)
	effective := newStoreGraph(idx.Records, graphEffective)
	for _, facts := range overlay {
		union.facts = append(union.facts, facts)
		effective = effective.with(facts)
	}
	for _, w := range writes {
		if err := rt.decideWrite(idx, w, origin, single, live, union, effective); err != nil {
			return nil, err
		}
	}
	if !single && !opened {
		if err := rt.refusePendingPromotions(writes, origin); err != nil {
			return nil, err
		}
	}
	var landed []*pendingWrite
	for _, w := range writes {
		if w.decided {
			landed = append(landed, w)
		}
	}
	if len(landed) == 0 {
		return results, nil
	}
	mutate := func(target *indexDocument) error {
		for _, w := range landed {
			if err := applyWriteDelta(target, w, now); err != nil {
				return err
			}
		}
		return nil
	}
	if err := mutate(idx); err != nil {
		return nil, err
	}
	// Every document is encoded before any is written, so a write the index
	// cannot take leaves nothing behind it.
	indexRaw, err := encodeIndex(idx, true)
	if err != nil {
		return nil, err
	}
	frames := kvPutFrameBytes(storeIndexKey, indexRaw)
	for _, w := range landed {
		switch {
		case w.discard:
			if w.result.Discarded != "" {
				w.key = stagedKey(w.rec.ID)
			}
		case w.stage:
			if w.raw, err = encodeStaged(stagedDocument{Record: w.rec, BaseRevision: w.base, StagedAt: now}); err != nil {
				return nil, err
			}
			w.key = stagedKey(w.rec.ID)
		default:
			if w.raw, err = encodeRecord(w.rec); err != nil {
				return nil, err
			}
			w.key = recordKey(w.rec.ID)
		}
		if w.raw != nil {
			frames += kvPutFrameBytes(w.key, w.raw)
		}
	}
	// Core kills a call whose frames pass its signed stdout_bytes, so a batch
	// that cannot fit is refused whole here rather than cut off after some of
	// its records have landed.
	if !single && frames > maxBatchFrameBytes {
		return nil, fmt.Errorf("%s: these records take %d bytes to write and one call may write %d", batchTooLargeCode, frames, maxBatchFrameBytes)
	}
	for _, w := range landed {
		switch {
		case w.raw != nil:
			if err := rt.kvPut(w.key, w.raw); err != nil {
				return nil, err
			}
		case w.key != "":
			if err := rt.kvDelete(w.key); err != nil {
				return nil, err
			}
		}
	}
	if err := rt.putIndexRetrying(idx, indexRaw, true, mutate); err != nil {
		return nil, err
	}
	for _, w := range landed {
		var entry indexEntry
		if pos := idx.position(w.rec.ID); pos >= 0 {
			entry = idx.Records[pos]
		}
		w.result.Record = withBookkeeping(w.rec, entry)
		if w.stage {
			w.result.Staged, w.result.StagedRevision, w.result.BaseRevision = true, w.rec.Revision, w.base
		}
	}
	return results, nil
}

// decideWrite settles one record's write: its stored state (single save),
// the conditional check, the strict chain check, the guards and the staging
// decision. A refusal or a conflict lands on the result and leaves the write
// undecided; only an unexpected failure is an error.
func (rt *runtime) decideWrite(idx *indexDocument, w *pendingWrite, origin writeOrigin, single bool, live, union, effective *storeGraph) error {
	res, rec := w.result, w.req.Record
	var loaded loadedRecord
	if pos := idx.position(rec.ID); pos >= 0 {
		res.Replaced = true
		loaded = loadedRecord{ID: rec.ID, Entry: idx.Records[pos], HasEntry: true, LiveRevision: idx.Records[pos].Revision}
		w.entry, w.hasEntry = idx.Records[pos], true
		if single {
			var err error
			if loaded, err = rt.storeLoadRecord(idx, rec.ID, idx.Records[pos].hasStaged()); err != nil {
				return err
			}
			if r, refused := loaded.writeRefusal(); refused {
				res.Refused = &r
				return nil
			}
		}
	}
	if single {
		// Provenance cannot be forged and an edit cannot drop it: Origin and
		// MigratedFrom come from the stored staged or live record.
		rec.Origin, rec.MigratedFrom = nil, nil
		if staged := loaded.pendingStaged(); staged != nil {
			rec.Origin, rec.MigratedFrom = staged.Record.Origin, staged.Record.MigratedFrom
		} else if loaded.HasLive {
			rec.Origin, rec.MigratedFrom = loaded.Live.Origin, loaded.Live.MigratedFrom
		}
		if conflict := conditionalConflict(loaded, w.req.IfRevision); conflict != nil {
			res.Conflict = conflict
			return nil
		}
	}
	nrec, err := normalizeSubscriptionForStore(rec)
	if err != nil {
		return err
	}
	if single && w.req.Strict {
		var before []json.RawMessage
		if staged := loaded.pendingStaged(); staged != nil {
			before = processSteps(staged.Record)
		} else if loaded.HasLive {
			before = processSteps(loaded.Live)
		}
		if err := savedChainCheck(before, nrec.Process); err != nil {
			if strings.HasPrefix(err.Error(), refusedRegexIncompatible) {
				// The diagnosis is this plugin's own compiler naming the
				// operator's step and pattern and the rewrite it offers, never a
				// provider's text, and the editor needs it to offer the rewrite.
				res.Refused = &storeRefusal{
					Code: refusedRegexIncompatible, IDs: []string{nrec.ID},
					Message: strings.TrimPrefix(err.Error(), refusedRegexIncompatible+": "),
				}
				return nil
			}
			return err
		}
	}
	ctx := writeContext{
		origin: origin, rec: nrec, facts: factsOfRecord(nrec),
		entry: loaded.Entry, hasEntry: loaded.HasEntry,
		live: live.without(nrec.ID), union: union.without(nrec.ID), effective: effective.without(nrec.ID),
	}
	if facts, ok := liveFactsOf(loaded.Entry); ok && loaded.HasEntry {
		// The written record's own live facts stay in the union: a staged
		// edit removes no edge until it is promoted.
		ctx.union.facts = append(ctx.union.facts, facts)
	}
	stage := ctx.stage()
	nrec, refusal := ctx.rules()
	if refusal != nil {
		res.Refused = refusal
		return nil
	}
	w.rec, w.stage, w.decided = nrec, stage, true
	if stage && ctx.liveEntry() && nrec.Revision == loaded.Entry.Revision {
		// The edit is back at the live content: nothing is left to stage, and
		// a staged revision it replaces is dropped with its key.
		w.stage, w.discard = false, true
		res.Discarded = loaded.Entry.StagedRevision
		return nil
	}
	if stage {
		w.base = loaded.Entry.Revision
	}
	return nil
}

// conditionalConflict is the refused conditional save, or nil. The stored
// record may carry a live and a staged revision: an edit based on either is
// current (the editor edits the staged content of a fleet record and the live
// content of a legacy record whose migration is staged).
func conditionalConflict(l loadedRecord, ifRevision string) *saveConflict {
	if ifRevision == "" {
		return nil
	}
	staged := l.pendingStaged()
	if !l.HasLive && staged == nil {
		// Editing something that no longer exists. Saving would silently
		// recreate a deleted record, so it is refused and named.
		return &saveConflict{reason: "deleted"}
	}
	if l.HasLive && l.LiveRevision == ifRevision {
		return nil
	}
	if staged != nil && staged.Record.Revision == ifRevision {
		return nil
	}
	if staged != nil {
		return &saveConflict{reason: "stale", current: withBookkeeping(staged.Record, l.Entry), revision: staged.Record.Revision}
	}
	return &saveConflict{reason: "stale", current: withBookkeeping(l.Live, l.Entry), revision: l.LiveRevision}
}

// applyWriteDelta applies one decided write to an index: the live entry
// re-derived (fetch bookkeeping and staged facts carried), the staged facts
// set on an entry or a stagedEntryFor entry appended, or a staged revision
// dropped. It applies to any index, which is what the kv_conflict retry needs.
func applyWriteDelta(idx *indexDocument, w *pendingWrite, now string) error {
	id := w.rec.ID
	if idx.archivedPosition(id) >= 0 {
		return fmt.Errorf("subscription %q was archived while this write ran; restore it and save again", id)
	}
	pos := idx.position(id)
	switch {
	case w.discard:
		if pos >= 0 {
			idx.Records[pos].clearStaged()
		}
		return nil
	case w.stage:
		if pos >= 0 {
			idx.Records[pos].setStaged(w.rec, now)
			return nil
		}
		if len(idx.Records) >= maxSubscriptionRecords {
			return fmt.Errorf("too many subscriptions: %d, limit %d", len(idx.Records)+1, maxSubscriptionRecords)
		}
		idx.Records = append(idx.Records, stagedEntryFor(w.rec, now))
		return nil
	default:
		entry := indexEntryFor(w.rec)
		if pos >= 0 {
			entry.carryBookkeeping(idx.Records[pos])
			if w.incoming.LastFetchAt != "" {
				entry.LastFetchAt, entry.LastFetchOK, entry.LastError, entry.Userinfo = w.incoming.LastFetchAt, w.incoming.LastFetchOK, w.incoming.LastError, w.incoming.Userinfo
			}
			idx.Records[pos] = entry
			return nil
		}
		if len(idx.Records) >= maxSubscriptionRecords {
			return fmt.Errorf("too many subscriptions: %d, limit %d", len(idx.Records)+1, maxSubscriptionRecords)
		}
		entry.LastFetchAt, entry.LastFetchOK, entry.LastError, entry.Userinfo = w.incoming.LastFetchAt, w.incoming.LastFetchOK, w.incoming.LastError, w.incoming.Userinfo
		idx.Records = append(idx.Records, entry)
		return nil
	}
}

// ── Promotion and discard ─────────────────────────────────────────────────

// storePromote makes a staged revision live (apply_revision's write half):
// the index first, with Revision set to the staged id, the entry re-derived
// from the staged record and its staged facts cleared; then record-v2 from
// the staged document; then the staged key's deletion; then, for a staged
// document with no base revision (a restore), the archive key's deletion. A
// kill after the index put leaves a half-done promotion that fetch completes.
// The index goes first because it is the one write that can fail for a
// reason outside the call (kv_conflict), and with it first that failure
// writes nothing.
func (rt *runtime) storePromote(idx *indexDocument, doc *stagedDocument) error {
	id := doc.Record.ID
	mutate := func(target *indexDocument) error {
		pos := target.position(id)
		if pos < 0 {
			return refusalErr(storeRefusal{Code: refusedStoreInconsistent, IDs: []string{id}, Message: fmt.Sprintf("subscription %q has no index entry to promote", id)})
		}
		entry := indexEntryFor(doc.Record)
		entry.carryBookkeeping(target.Records[pos])
		entry.clearStaged()
		target.Records[pos] = entry
		return nil
	}
	if err := mutate(idx); err != nil {
		return err
	}
	indexRaw, err := encodeIndex(idx, false)
	if err != nil {
		return err
	}
	recordRaw, err := encodeRecord(doc.Record)
	if err != nil {
		return err
	}
	if err := rt.putIndexRetrying(idx, indexRaw, false, mutate); err != nil {
		return err
	}
	if err := rt.kvPut(recordKey(id), recordRaw); err != nil {
		return err
	}
	if err := rt.kvDelete(stagedKey(id)); err != nil {
		return err
	}
	if doc.BaseRevision == "" {
		return rt.kvDelete(archiveKey(id))
	}
	return nil
}

// storeDiscardStaged drops a staged revision (the discard_staged method):
// the index, the staged key's deletion, the index write (two more on a
// kv_conflict). The staged facts are cleared on a record with a live
// revision; the entry of a record that never had one is removed, so no ghost
// entry is left; a staged restore's entry returns to Archived. It refuses
// staged_revision_mismatch when stagedRevision is empty or is not the
// entry's, so a record with nothing staged (a half-done promotion included)
// keeps its staged key.
func (rt *runtime) storeDiscardStaged(id, stagedRevision string) error {
	idx, err := rt.writableIndex()
	if err != nil {
		return err
	}
	pos := idx.position(id)
	if pos < 0 || stagedRevision == "" || !idx.Records[pos].hasStaged() || idx.Records[pos].StagedRevision != stagedRevision {
		current := ""
		if pos >= 0 {
			current = idx.Records[pos].StagedRevision
		}
		return refusalErr(storeRefusal{
			Code: refusedStagedMismatch, IDs: []string{id}, StagedRevision: current,
			Message: fmt.Sprintf("subscription %q has staged revision %s, not %s; list again before discarding", id, orNone(current), orNone(stagedRevision)),
		})
	}
	mutate := func(target *indexDocument) error {
		at := target.position(id)
		if at < 0 || target.Records[at].StagedRevision != stagedRevision {
			// Already discarded or replaced by a concurrent write: nothing
			// of this revision is left to drop.
			return nil
		}
		entry := target.Records[at]
		switch {
		case entry.hasLiveRevision():
			target.Records[at].clearStaged()
		case entry.StagedRestoredAt != "":
			archived := indexEntry{ID: entry.ID, Kind: entry.Kind, Name: entry.Name, DisplayName: entry.DisplayName, ArchivedAt: entry.StagedRestoredAt}
			target.Records = append(target.Records[:at], target.Records[at+1:]...)
			target.Archived = append(target.Archived, archived)
		default:
			target.Records = append(target.Records[:at], target.Records[at+1:]...)
		}
		return nil
	}
	if err := rt.kvDelete(stagedKey(id)); err != nil {
		return err
	}
	if err := mutate(idx); err != nil {
		return err
	}
	indexRaw, err := encodeIndex(idx, false)
	if err != nil {
		return err
	}
	return rt.putIndexRetrying(idx, indexRaw, false, mutate)
}

// batchHostCallCaps is what a batch write may spend on host calls: the
// method's signed budget less what it spends outside storeWriteRecords
// (migrate's three upstream reads).
var batchHostCallCaps = map[writeOrigin]int{originImport: 320, originMigrate: 322}

// refusePendingPromotions protects a half-done promotion from a batch write.
// A batch never opens the records it writes, so it cannot see the state the
// way storeLoadRecord does; the one write that could destroy the roll-forward
// document is a staged write over a live entry with nothing staged, so for
// each of those it reads staged-v2-<id> once and refuses promotion_pending
// when the key holds the entry's own revision. A live write cannot meet the
// state: a promotion leaves the record fleet-bound or writes it live
// unprivileged, and the batch then writes what the operator asked for. The
// reads count against the method's budget, so a batch whose writes and reads
// would not fit is refused whole before any of them.
func (rt *runtime) refusePendingPromotions(writes []*pendingWrite, origin writeOrigin) error {
	var probe []*pendingWrite
	decided := 0
	for _, w := range writes {
		if !w.decided {
			continue
		}
		decided++
		if w.stage && w.hasEntry && w.entry.hasLiveRevision() && !w.entry.hasStaged() {
			probe = append(probe, w)
		}
	}
	if len(probe) == 0 {
		return nil
	}
	// The index read, every record put, the index put and its retry, the
	// settings put, and the probes.
	if limit, capped := batchHostCallCaps[origin]; capped && decided+len(probe)+5 > limit {
		return fmt.Errorf("%s: %d records with %d staged checks take more host calls than one call may make", batchTooLargeCode, decided, len(probe))
	}
	for _, w := range probe {
		staged, err := rt.loadStaged(w.rec.ID)
		if err != nil {
			return err
		}
		if staged != nil && staged.Record.Revision == w.entry.Revision {
			r := promotionPendingRefusal(w.rec.ID, w.entry.Revision, "")
			w.result.Refused, w.decided = &r, false
		}
	}
	return nil
}
