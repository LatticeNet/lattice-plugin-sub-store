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

// legacyCache holds the legacy document for one invocation, and the records
// a handler already opened through storeLoadRecord (primed), so the record
// read fetchSubscription makes costs no second host call.
type legacyCache struct {
	loaded bool
	doc    *subscriptionRecordsDocument
	primed map[string]subscriptionRecord
}

// primeRecord makes loadRecord answer rec for its id for the rest of the
// invocation: the record a handler opened through storeLoadRecord, which is
// the staged document during a half-done promotion.
func (rt *runtime) primeRecord(rec subscriptionRecord) {
	if rt.legacy.primed == nil {
		rt.legacy.primed = map[string]subscriptionRecord{}
	}
	rt.legacy.primed[rec.ID] = rec
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

// loadIndex reads the index; nil when the store has none yet. The document
// keeps the digest it was read at, which its write sends as if_match.
func (rt *runtime) loadIndex() (*indexDocument, error) {
	value, digest, found, err := rt.kvGetWithDigest(storeIndexKey)
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
	idx.digest = digest
	return &idx, nil
}

// encodeIndex encodes the index; bounded refuses one past maxIndexBytes,
// which is how a save that would outgrow list's budget is refused before it
// writes anything.
func encodeIndex(idx *indexDocument, bounded bool) ([]byte, error) {
	idx.Version = storeVersionSplit
	idx.renumber()
	idx.recomputeFlags()
	raw, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	if bounded && len(raw) > maxIndexBytes {
		return nil, fmt.Errorf("the subscription index would be %d bytes, limit %d; delete or purge records before adding more", len(raw), maxIndexBytes)
	}
	return raw, nil
}

// putIndex writes idx with if_match, once. A write that wants the one retry
// a kv_conflict earns goes through putIndexRetrying instead.
func (rt *runtime) putIndex(idx *indexDocument, bounded bool) error {
	raw, err := encodeIndex(idx, bounded)
	if err != nil {
		return err
	}
	return rt.putIndexRaw(idx, raw)
}

// putIndexRaw writes raw, the encoding of idx, with idx's digest as if_match,
// and records the digest of what it wrote so a later write in the same
// invocation compares against it.
func (rt *runtime) putIndexRaw(idx *indexDocument, raw []byte) error {
	if err := rt.kvPutIfMatch(storeIndexKey, raw, idx.digest); err != nil {
		return err
	}
	idx.digest = kvDigest(raw)
	return nil
}

// putIndexRetrying writes raw, the encoding of idx after the write applied its
// delta. On kv_conflict (another invocation wrote the index since this one
// read it: noteFetchOutcome runs outside the plugin gate, plan section 2.7)
// it re-reads the index once, applies mutate, the write's whole delta, to the
// fresh copy and writes that; a second conflict answers errKVConflict, which
// a write method reports as the structured kv_conflict refusal. mutate must
// apply to any index, not only the one first read. idx holds what was
// written on success. Two host calls on the retry, none without it beyond
// the one put.
func (rt *runtime) putIndexRetrying(idx *indexDocument, raw []byte, bounded bool, mutate func(*indexDocument) error) error {
	err := rt.putIndexRaw(idx, raw)
	if err == nil || !isKVConflict(err) || mutate == nil {
		return err
	}
	fresh, err := rt.writableIndex()
	if err != nil {
		return err
	}
	if err := mutate(fresh); err != nil {
		return err
	}
	raw, err = encodeIndex(fresh, bounded)
	if err != nil {
		return err
	}
	if err := rt.putIndexRaw(fresh, raw); err != nil {
		if isKVConflict(err) {
			return fmt.Errorf("%w: the index moved twice while this write ran; nothing further was written, retry", errKVConflict)
		}
		return err
	}
	*idx = *fresh
	return nil
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
	if rec, ok := rt.legacy.primed[id]; ok {
		return rec, true, nil
	}
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

// batchOutcome is what a batch write stored, staged, refused and skipped.
type batchOutcome struct {
	skipped  map[string]string
	replaced map[string]bool
	staged   map[string]string
	refused  map[string]storeRefusal
}

// saveSubscriptionBatch persists many definitions with one index read, one
// record or staged write each and one index write, through storeWriteRecords.
//
// The plugin-call budget charges every host round trip, so the index is
// loaded once, every record is normalized and merged in memory, and the
// whole batch is refused before its first write when the store could not
// take it. A record that fails validation is skipped, and one a guard
// refuses is reported, and neither fails the batch; a store-level failure
// does.
func (rt *runtime) saveSubscriptionBatch(recs []subscriptionRecord, origin writeOrigin) (batchOutcome, error) {
	out := batchOutcome{skipped: map[string]string{}, replaced: map[string]bool{}, staged: map[string]string{}, refused: map[string]storeRefusal{}}
	reqs := make([]writeRequest, len(recs))
	for i, rec := range recs {
		reqs[i] = writeRequest{Record: rec}
	}
	results, err := rt.storeWriteRecords(reqs, origin)
	if err != nil {
		return out, err
	}
	for _, res := range results {
		switch {
		case res.Skipped != "":
			out.skipped[res.ID] = res.Skipped
		case res.Refused != nil:
			out.refused[res.ID] = *res.Refused
			out.skipped[res.ID] = res.Refused.Code + ": " + res.Refused.Message
		case res.Conflict != nil:
			out.skipped[res.ID] = res.Conflict.reason
		default:
			if res.Replaced {
				out.replaced[res.ID] = true
			}
			if res.Staged {
				out.staged[res.ID] = res.StagedRevision
			}
		}
	}
	return out, nil
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
	// selectionVersion is the catalogue version a fleet fetch read; the
	// entry's selection_version takes it on success.
	selectionVersion string
	// index is the index the fetch handler already read, with its digest,
	// and indexRead says it did (index may be nil on a store with none), so
	// the bookkeeping costs no second read.
	index     *indexDocument
	indexRead bool
	// rederive is the record the fetch served when it was ahead of its
	// entry (a live write whose index put was refused): the bookkeeping put
	// re-derives the entry from it, so the mismatch lasts one poll.
	rederive *subscriptionRecord
}

// errDropBookkeeping drops a bookkeeping write: the entry moved under it.
var errDropBookkeeping = errors.New("the entry moved; the fetch bookkeeping is dropped")

// noteFetchOutcome records when a fetch ran and how it went. It is called from
// the fetch method (the core invokes it for every refresh, scheduled or
// manual) and deliberately NOT from fetchSubscription itself: render and
// preview fetch too, and a write per read would rewrite the index on every
// public request. A preview fetch is also not a refresh: recording it would
// tell the operator the served snapshot is fresher than it is.
//
// The write carries if_match (plan section 2.7). Core calls fetch before it
// takes the plugin gate, so this write can race a save or a promotion: on a
// kv_conflict it re-reads once and re-applies only its own delta, and it
// drops the write instead when the entry's Revision or StagedRevision moved,
// because fetch bookkeeping is never worth reverting a promotion. A store
// that has not migrated keeps its bookkeeping in the legacy document, where
// migrate_store finds it. A failed bookkeeping write is swallowed on purpose:
// the fetch's own result is already being reported, and losing the note must
// not turn a good refresh into an error.
func (rt *runtime) noteFetchOutcome(subscriptionID string, outcome fetchOutcome) {
	idx := outcome.index
	if !outcome.indexRead {
		var err error
		if idx, err = rt.loadIndex(); err != nil {
			return
		}
	}
	if idx != nil {
		if pos := idx.position(subscriptionID); pos >= 0 {
			seen := idx.Records[pos]
			apply := func(target *indexDocument) error {
				at := target.position(subscriptionID)
				if at < 0 || target.Records[at].Revision != seen.Revision || target.Records[at].StagedRevision != seen.StagedRevision {
					return errDropBookkeeping
				}
				if outcome.rederive != nil {
					entry := indexEntryFor(*outcome.rederive)
					entry.carryBookkeeping(target.Records[at])
					target.Records[at] = entry
				}
				applyFetchOutcome(&target.Records[at], outcome)
				return nil
			}
			if apply(idx) != nil {
				return
			}
			raw, err := encodeIndex(idx, false)
			if err != nil {
				return
			}
			_ = rt.putIndexRetrying(idx, raw, false, apply)
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
	if outcome.selectionVersion != "" {
		entry.SelectionVersion = outcome.selectionVersion
	}
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
