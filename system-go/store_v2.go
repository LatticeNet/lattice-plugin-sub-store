package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The split store (design 28 section 3.1, plan sections 2.5 and 3.1): one KV
// key per record, one index that list reads in a single call, and an archive
// key per deleted record. It replaces the single legacy document, whose 1 MiB
// cap and full rewrite on every edit no longer fit a store of real size.
//
// Host clients exist only inside an invocation, so the migration cannot run
// at process start: it runs in migrate_store (store_migrate.go), chunked.
// Every other method works on both layouts through the functions here. Reads
// and fetch bookkeeping keep working on a store that has not migrated, so
// scheduled refreshes and share renders never stall on the migration; writes
// on one are refused with storeMigrationRequiredCode and touch nothing.

const (
	storeIndexKey      = "subscriptions-index-v2"
	storeRecordPrefix  = "record-v2-"
	storeArchivePrefix = "archive-v2-"
	storeVersionLegacy = 1
	storeVersionSplit  = 2
	// maxIndexBytes bounds the index a save may write. A migration writes
	// whatever the legacy document held, so only saves are refused at it.
	maxIndexBytes = 384 << 10
	// maxRecordDocBytes bounds one record document: inline content or a
	// program, JSON escaped, plus the rest of the record. kv.get answers it
	// base64 encoded inside the host's 4 MiB response payload.
	maxRecordDocBytes = 2 << 20
	// maxStoreKeyBytes is the server's validateStorageName bound on a key.
	maxStoreKeyBytes = 256
	// storeMigrationRequiredCode leads the refusal of a write on a store
	// whose legacy document has not been migrated.
	storeMigrationRequiredCode = "store_migration_required"
)

var errStoreMigrationRequired = errors.New(storeMigrationRequiredCode + ": the subscription store still holds the legacy single document; run migrate_store until it reports done, then retry")

// storeKeyComponent escapes a record id into a key. The server refuses keys
// with a slash, a backslash or a control character, and the legacy document
// never constrained ids, so those bytes and the escape byte itself are
// written as %XX. Every other id is its own key component.
func storeKeyComponent(id string) string {
	var b strings.Builder
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c == '%' || c == '/' || c == '\\' || c < 0x20 || c == 0x7f {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func recordKey(id string) string  { return storeRecordPrefix + storeKeyComponent(id) }
func archiveKey(id string) string { return storeArchivePrefix + storeKeyComponent(id) }

// validStoreID refuses an id whose keys the server would refuse.
// yagni: a legacy id that long cannot migrate either and migrate_store names
// it; no real id comes near 240 bytes, so nothing renames on the way.
func validStoreID(id string) error {
	if len(archiveKey(id)) > maxStoreKeyBytes {
		return fmt.Errorf("subscription id %q is too long to store: at most %d bytes once escaped", truncateUTF8(id, 32), maxStoreKeyBytes-len(storeArchivePrefix))
	}
	return nil
}

// legacyCache holds the legacy document for one invocation.
type legacyCache struct {
	loaded bool
	doc    *subscriptionRecordsDocument
}

// legacyDocument returns the legacy document, read at most once per
// invocation; nil when it is absent or marked migrated.
func (rt *runtime) legacyDocument() (*subscriptionRecordsDocument, error) {
	if rt.legacy.loaded {
		return rt.legacy.doc, nil
	}
	doc, found, err := rt.loadSubscriptionRecords()
	if err != nil {
		return nil, err
	}
	rt.legacy.loaded = true
	if found && doc.MigratedTo == "" {
		rt.legacy.doc = &doc
	}
	return rt.legacy.doc, nil
}

// putLegacyDocument writes the legacy document and keeps the cache in step.
func (rt *runtime) putLegacyDocument(doc subscriptionRecordsDocument) error {
	if err := rt.saveSubscriptionRecords(doc); err != nil {
		return err
	}
	rt.legacy.loaded = true
	rt.legacy.doc = nil
	if doc.MigratedTo == "" {
		rt.legacy.doc = &doc
	}
	return nil
}

// loadIndex reads the index; nil when the store has none yet.
func (rt *runtime) loadIndex() (*indexDocument, error) {
	value, found, err := rt.kvGet(storeIndexKey)
	if err != nil || !found {
		return nil, err
	}
	var idx indexDocument
	if err := json.Unmarshal(value, &idx); err != nil {
		return nil, fmt.Errorf("decode subscription index: %w", err)
	}
	if idx.Version != storeVersionSplit {
		return nil, fmt.Errorf("unsupported subscription index version %d", idx.Version)
	}
	return &idx, nil
}

// encodeIndex encodes the index; bounded refuses one past maxIndexBytes,
// which is how a save that would outgrow list's budget is refused before it
// writes anything.
func encodeIndex(idx *indexDocument, bounded bool) ([]byte, error) {
	idx.Version = storeVersionSplit
	idx.renumber()
	raw, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	if bounded && len(raw) > maxIndexBytes {
		return nil, fmt.Errorf("the subscription index would be %d bytes, limit %d; delete or purge records before adding more", len(raw), maxIndexBytes)
	}
	return raw, nil
}

func (rt *runtime) putIndex(idx *indexDocument, bounded bool) error {
	raw, err := encodeIndex(idx, bounded)
	if err != nil {
		return err
	}
	return rt.kvPut(storeIndexKey, raw)
}

// writableIndex returns the index a write applies its delta to, or the
// store_migration_required refusal. A store with neither an index nor a
// legacy record is new: its first write creates the index.
func (rt *runtime) writableIndex() (*indexDocument, error) {
	idx, err := rt.loadIndex()
	if err != nil {
		return nil, err
	}
	if idx != nil {
		if !idx.verified() {
			return nil, errStoreMigrationRequired
		}
		return idx, nil
	}
	legacy, err := rt.legacyDocument()
	if err != nil {
		return nil, err
	}
	if legacy != nil && len(legacy.Records) > 0 {
		return nil, errStoreMigrationRequired
	}
	return &indexDocument{Version: storeVersionSplit}, nil
}

// encodeRecord is a record document: the record without bookkeeping, which
// lives in the index, bounded.
func encodeRecord(rec subscriptionRecord) ([]byte, error) {
	rec.LastFetchAt, rec.LastFetchOK, rec.LastError, rec.Userinfo = "", false, "", ""
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxRecordDocBytes {
		return nil, fmt.Errorf("subscription %q is %d bytes stored, limit %d", rec.ID, len(raw), maxRecordDocBytes)
	}
	return raw, nil
}

func (rt *runtime) putRecord(rec subscriptionRecord) error {
	raw, err := encodeRecord(rec)
	if err != nil {
		return err
	}
	return rt.kvPut(recordKey(rec.ID), raw)
}

func decodeRecordDoc(id string, value []byte) (subscriptionRecord, error) {
	if len(value) > maxRecordDocBytes {
		return subscriptionRecord{}, fmt.Errorf("subscription %q exceeds %d bytes stored", id, maxRecordDocBytes)
	}
	var rec subscriptionRecord
	if err := json.Unmarshal(value, &rec); err != nil {
		return subscriptionRecord{}, fmt.Errorf("decode subscription %q: %w", id, err)
	}
	return rec, nil
}

// loadRecord reads one record: its own key, and on a miss the legacy
// document (once per invocation), whose script file gets its program from
// the legacy program key. Revision is recomputed, never trusted from storage.
func (rt *runtime) loadRecord(id string) (subscriptionRecord, bool, error) {
	value, found, err := rt.kvGet(recordKey(id))
	if err != nil {
		return subscriptionRecord{}, false, err
	}
	if found {
		rec, err := decodeRecordDoc(id, value)
		if err != nil {
			return subscriptionRecord{}, false, err
		}
		return withRevision(rec), true, nil
	}
	legacy, err := rt.legacyDocument()
	if err != nil || legacy == nil {
		return subscriptionRecord{}, false, err
	}
	for _, rec := range legacy.Records {
		if rec.ID != id {
			continue
		}
		rec = withRevision(rec)
		if isScriptFile(rec) {
			script, err := rt.getFileScript(id)
			if err != nil {
				return subscriptionRecord{}, false, err
			}
			rec.Content = script
		}
		return rec, true, nil
	}
	return subscriptionRecord{}, false, nil
}

// getSubscription is loadRecord for callers that need the record to exist.
func (rt *runtime) getSubscription(id string) (subscriptionRecord, error) {
	rec, found, err := rt.loadRecord(id)
	if err != nil {
		return subscriptionRecord{}, err
	}
	if !found {
		return subscriptionRecord{}, fmt.Errorf("subscription %q was not found", id)
	}
	return rec, nil
}

// saveConflict is a refused conditional save: the reason, and the stored
// record with its current revision when there is one.
type saveConflict struct {
	reason   string
	current  subscriptionRecord
	revision string
}

// storeSave is one save: the index read, the stored record when one exists
// (its Origin is preserved and the conditional write compares against it),
// the record write and the index write. Three host calls for a new record,
// four for an existing one.
//
// strict is the editor's save: a chain that brings in a pattern RE2 refuses
// is refused (savedChainCheck). Import, migrate and backup restore write
// records as they were and pass false; the index flags what they carry.
//
// The conditional write. A blind full-record overwrite is a lost-update
// defect: two operators editing one record, or one operator editing a record
// a refresh or a restore has already moved, and the loser's work disappeared
// with nothing on screen to say it had happened. ifRevision is optional:
// import, migrate and backup restore write records they never read.
func (rt *runtime) storeSave(rec subscriptionRecord, ifRevision string, strict bool) (subscriptionRecord, *saveConflict, error) {
	if strings.TrimSpace(rec.ID) == "" {
		return subscriptionRecord{}, nil, fmt.Errorf("subscription id is required")
	}
	if err := validStoreID(rec.ID); err != nil {
		return subscriptionRecord{}, nil, err
	}
	idx, err := rt.writableIndex()
	if err != nil {
		return subscriptionRecord{}, nil, err
	}
	// An archived record keeps its id until it is purged, so restore can
	// bring it back under the same name its shares and members use.
	if idx.archivedPosition(rec.ID) >= 0 {
		return subscriptionRecord{}, &saveConflict{reason: "archived"}, nil
	}
	pos := idx.position(rec.ID)
	var stored subscriptionRecord
	found := false
	if pos >= 0 {
		stored, found, err = rt.loadRecord(rec.ID)
		if err != nil {
			return subscriptionRecord{}, nil, err
		}
	}
	// Origin records where a record came from during migration. An edit
	// cannot change it, so it is preserved from the stored record; the save
	// method clears it on a new record before it gets here, and only the
	// migration and import paths create a record with one.
	if found {
		rec.Origin = stored.Origin
	}
	if ifRevision != "" {
		if !found {
			// Editing something that no longer exists. Saving would silently
			// recreate a deleted record, so it is refused and named.
			return subscriptionRecord{}, &saveConflict{reason: "deleted"}, nil
		}
		if current := subscriptionRevision(stored); current != ifRevision {
			// The stored record is handed back so the caller can say what
			// changed; it is the one holding the copy that was read.
			return subscriptionRecord{}, &saveConflict{reason: "stale", current: withBookkeeping(stored, idx.Records[pos]), revision: current}, nil
		}
	}
	nrec, err := normalizeSubscriptionForStore(rec)
	if err != nil {
		return subscriptionRecord{}, nil, err
	}
	if strict {
		var before []json.RawMessage
		if found {
			before = processSteps(stored)
		}
		// Unwrapped: the refusal's message leads with its code, which is
		// how the editor recognises it.
		if err := savedChainCheck(before, nrec.Process); err != nil {
			return subscriptionRecord{}, nil, err
		}
	}
	entry := indexEntryFor(nrec)
	if pos >= 0 {
		entry.carryBookkeeping(idx.Records[pos])
		idx.Records[pos] = entry
	} else {
		if len(idx.Records) >= maxSubscriptionRecords {
			return subscriptionRecord{}, nil, fmt.Errorf("too many subscriptions: %d, limit %d", len(idx.Records)+1, maxSubscriptionRecords)
		}
		idx.Records = append(idx.Records, entry)
	}
	// Both documents are encoded before either is written, so a save the
	// index cannot take leaves no orphan record behind it.
	indexRaw, err := encodeIndex(idx, true)
	if err != nil {
		return subscriptionRecord{}, nil, err
	}
	recordRaw, err := encodeRecord(nrec)
	if err != nil {
		return subscriptionRecord{}, nil, err
	}
	if err := rt.kvPut(recordKey(nrec.ID), recordRaw); err != nil {
		return subscriptionRecord{}, nil, err
	}
	if err := rt.kvPut(storeIndexKey, indexRaw); err != nil {
		return subscriptionRecord{}, nil, err
	}
	return withBookkeeping(nrec, entry), nil, nil
}

// saveSubscription is an unconditional save.
func (rt *runtime) saveSubscription(rec subscriptionRecord) error {
	_, conflict, err := rt.storeSave(rec, "", false)
	if err != nil {
		return err
	}
	if conflict != nil {
		return fmt.Errorf("subscription %q: %s", rec.ID, conflict.reason)
	}
	return nil
}

// batchOutcome is what a batch save stored and why it skipped the rest.
type batchOutcome struct {
	skipped  map[string]string
	replaced map[string]bool
}

// saveSubscriptionBatch persists many definitions with one index read, one
// record write each and one index write.
//
// The plugin-call budget charges every host round trip, so the index is
// loaded once, every record is normalized and merged in memory, and the
// whole batch is refused before its first write when the store could not
// take it. A record that fails validation is skipped and does not fail the
// batch; a store-level failure does.
func (rt *runtime) saveSubscriptionBatch(recs []subscriptionRecord) (batchOutcome, error) {
	out := batchOutcome{skipped: map[string]string{}, replaced: map[string]bool{}}
	if len(recs) == 0 {
		return out, nil
	}
	idx, err := rt.writableIndex()
	if err != nil {
		return out, err
	}
	var pending []subscriptionRecord
	for _, rec := range recs {
		nrec, err := normalizeSubscriptionForStore(rec)
		if err != nil {
			out.skipped[rec.ID] = err.Error()
			continue
		}
		if idx.archivedPosition(nrec.ID) >= 0 {
			out.skipped[nrec.ID] = "archived: restore or purge the archived record with this id first"
			continue
		}
		entry := indexEntryFor(nrec)
		// A backup or a migration carries bookkeeping on the record; the
		// index is where it lives now.
		incoming := legacyEntry(rec)
		if pos := idx.position(nrec.ID); pos >= 0 {
			out.replaced[nrec.ID] = true
			entry.carryBookkeeping(idx.Records[pos])
			if incoming.LastFetchAt != "" {
				entry.LastFetchAt, entry.LastFetchOK, entry.LastError, entry.Userinfo = incoming.LastFetchAt, incoming.LastFetchOK, incoming.LastError, incoming.Userinfo
			}
			idx.Records[pos] = entry
		} else {
			entry.LastFetchAt, entry.LastFetchOK, entry.LastError, entry.Userinfo = incoming.LastFetchAt, incoming.LastFetchOK, incoming.LastError, incoming.Userinfo
			idx.Records = append(idx.Records, entry)
		}
		pending = append(pending, nrec)
	}
	// Refuse before spending any record write: paying N host calls first
	// would blow the import budget on a batch that cannot land and leave N
	// orphan records behind the exact failure this path exists to prevent.
	if len(idx.Records) > maxSubscriptionRecords {
		return out, fmt.Errorf("too many subscriptions: %d, limit %d", len(idx.Records), maxSubscriptionRecords)
	}
	indexRaw, err := encodeIndex(idx, true)
	if err != nil {
		return out, err
	}
	encoded := make([][]byte, len(pending))
	frames := kvPutFrameBytes(storeIndexKey, indexRaw)
	for i, rec := range pending {
		if encoded[i], err = encodeRecord(rec); err != nil {
			return out, err
		}
		frames += kvPutFrameBytes(recordKey(rec.ID), encoded[i])
	}
	// Core kills a call whose frames pass its signed stdout_bytes, so a batch
	// that cannot fit is refused whole here rather than cut off after some of
	// its records have landed.
	if frames > maxBatchFrameBytes {
		return out, fmt.Errorf("%s: these records take %d bytes to write and one call may write %d; split them across smaller imports", batchTooLargeCode, frames, maxBatchFrameBytes)
	}
	for i, rec := range pending {
		if err := rt.kvPut(recordKey(rec.ID), encoded[i]); err != nil {
			return out, err
		}
	}
	return out, rt.kvPut(storeIndexKey, indexRaw)
}

// maxBatchFrameBytes bounds the record and index frames one batch writes.
// import and migrate sign 8 MiB of stdout; their reads, reply and Settings
// write stay well under the 1 MiB left over.
const maxBatchFrameBytes = 7 << 20

// batchTooLargeCode opens the refusal of a batch over maxBatchFrameBytes.
const batchTooLargeCode = "batch_too_large"

// maxFetchErrorBytes bounds the failure reason stored for a record. A
// provider can answer with a whole error page, and the index is rewritten in
// full on every write; an unbounded reason would tax every unrelated write.
const maxFetchErrorBytes = 240

// fetchOutcome is what one refresh learned.
type fetchOutcome struct {
	at       time.Time
	userinfo string
	nodesIn  *int
	nodesOut *int
	err      error
}

// noteFetchOutcome records when a fetch ran and how it went. It is called from
// the fetch method (the core invokes it for every refresh, scheduled or
// manual) and deliberately NOT from fetchSubscription itself: render and
// preview fetch too, and a write per read would rewrite the index on every
// public request. A preview fetch is also not a refresh: recording it would
// tell the operator the served snapshot is fresher than it is.
//
// The index is read immediately before it is written and only this record's
// bookkeeping changes, so a concurrent save loses nothing but the window
// between the two calls (plan section 3.4). A store that has not migrated
// keeps its bookkeeping in the legacy document, where migrate_store finds it.
// A failed bookkeeping write is swallowed on purpose: the fetch's own result
// is already being reported, and losing the note must not turn a good refresh
// into an error.
func (rt *runtime) noteFetchOutcome(subscriptionID string, outcome fetchOutcome) {
	idx, err := rt.loadIndex()
	if err != nil {
		return
	}
	if idx != nil {
		if pos := idx.position(subscriptionID); pos >= 0 {
			applyFetchOutcome(&idx.Records[pos], outcome)
			_ = rt.putIndex(idx, false)
			return
		}
		if idx.verified() {
			return
		}
	}
	legacy, err := rt.legacyDocument()
	if err != nil || legacy == nil {
		return
	}
	doc := *legacy
	doc.Records = append([]subscriptionRecord(nil), legacy.Records...)
	for i := range doc.Records {
		if doc.Records[i].ID != subscriptionID {
			continue
		}
		entry := legacyEntry(doc.Records[i])
		applyFetchOutcome(&entry, outcome)
		doc.Records[i].LastFetchAt, doc.Records[i].LastError, doc.Records[i].Userinfo = entry.LastFetchAt, entry.LastError, entry.Userinfo
		doc.Records[i].LastFetchOK = *entry.LastFetchOK
		_ = rt.putLegacyDocument(doc)
		return
	}
}

func applyFetchOutcome(entry *indexEntry, outcome fetchOutcome) {
	entry.LastFetchAt = outcome.at.UTC().Format(time.RFC3339)
	ok := outcome.err == nil
	entry.LastFetchOK = &ok
	if outcome.err != nil {
		entry.LastError = trimFetchError(outcome.err)
		return
	}
	entry.LastError = ""
	// A failure keeps the previous userinfo: it is the provider's quota
	// figures, and a stale figure next to a "refresh failed" badge beats
	// none at all.
	if outcome.userinfo != "" {
		entry.Userinfo = outcome.userinfo
	}
	if outcome.nodesIn != nil {
		count := *outcome.nodesIn
		entry.NodesIn = &count
		// Counted with the chain when it is native, cleared when it is not:
		// a count from before the chain stopped being native would describe
		// a chain the record no longer has.
		entry.NodesOut = nil
		if outcome.nodesOut != nil {
			after := *outcome.nodesOut
			entry.NodesOut = &after
		}
	}
}

func trimFetchError(err error) string {
	text := strings.TrimSpace(err.Error())
	if len(text) > maxFetchErrorBytes {
		text = strings.TrimSpace(truncateUTF8(text, maxFetchErrorBytes)) + "…"
	}
	return text
}
