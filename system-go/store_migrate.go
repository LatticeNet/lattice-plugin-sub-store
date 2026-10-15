package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// migrate_store moves a legacy store onto the split store (plan section 3.1).
//
// It is a method rather than a start-up step because host calls exist only
// inside an invocation, and it is chunked because each call is bounded by its
// signed host_calls budget: a chunk of c records costs the legacy document,
// the index, up to c programs, c record writes and the index write, so 64
// records cost at most 131 calls and the chunk that reaches the end adds the
// verify read, the legacy mark and the final index write (134). Progress is
// the cursor in the index, so a chunk that dies resumes, and a record whose
// entry already carries its revision is skipped without a write.
//
// The final chunk verifies: it re-reads the index and compares every id,
// revision and content hash with the legacy document read in the same call.
// Only then is the legacy document marked migrated (it is kept until S2, and
// the mark stops a record deleted later from coming back out of it) and the
// store opened for writes. The legacy program keys are deleted after that,
// up to a chunk per call, so a migration that stops short never loses a
// program the legacy document still points at; done turns true when none is
// left.
//
// {"rebuild": true} is the repair for an index that lost a delta to a
// concurrent write (section 3.4): it re-derives up to a chunk of entries per
// call from their records, resuming after "after".

const (
	defaultMigrateChunk = 64
	maxMigrateChunk     = 64
	// migrateChunkFrameBytes bounds the record writes of one call. Core counts
	// every frame the plugin writes against the method's signed stdout_bytes,
	// host calls included, and a kv.put frame carries its value in base64, so
	// a chunk of large records (a script file's program is inlined) could pass
	// any budget sized by record count. A call stops before the record that
	// would take its frames past this bound, and always writes at least one;
	// the index write and the reply fit in what the budget leaves
	// (TestWriteBudgetsCoverTheirLargestFrames).
	migrateChunkFrameBytes = 5 << 20
	// hostCallFrameOverhead is a host_call frame's bytes besides its params:
	// the protocol, kind, generation, invocation and host call ids and the
	// method, rounded up.
	hostCallFrameOverhead = 256
)

// kvPutFrameBytes is the size of the host_call frame that writes value under
// key: the value travels base64 encoded.
func kvPutFrameBytes(key string, value []byte) int {
	return base64.StdEncoding.EncodedLen(len(value)) + len(key) + hostCallFrameOverhead
}

type migrateStoreRequest struct {
	Chunk   int    `json:"chunk"`
	Rebuild bool   `json:"rebuild"`
	After   string `json:"after"`
	// DeleteLegacy deletes the legacy single document once the split store
	// has verified and its legacy programs are gone (plan section 2.7). Only
	// the operator runs it, after a124 and 0.18 are both live, because the
	// document is also the store's only rollback base.
	DeleteLegacy bool `json:"delete_legacy"`
}

type migrateStoreReply struct {
	Migrated     int  `json:"migrated"`
	Remaining    int  `json:"remaining"`
	Done         bool `json:"done"`
	Verified     bool `json:"verified"`
	StoreVersion int  `json:"store_version"`
	// LegacyPrograms counts legacy program keys still to delete.
	LegacyPrograms int `json:"legacy_programs_pending,omitempty"`
	// RegexIncompatible names the records this call flagged: their chains
	// keep rendering on the bundle, and the editor offers a rewrite.
	RegexIncompatible []string `json:"regex_incompatible,omitempty"`
	// Rebuilt and Next answer a rebuild: entries re-derived, and the id to
	// pass as after for the next chunk (empty when the rebuild is done).
	Rebuilt int    `json:"rebuilt,omitempty"`
	Next    string `json:"next,omitempty"`
	// DeletedLegacy answers delete_legacy.
	DeletedLegacy bool `json:"deleted_legacy,omitempty"`
}

func (rt *runtime) migrateStore(payload json.RawMessage) (migrateStoreReply, error) {
	var req migrateStoreRequest
	if len(payload) > 0 {
		if err := decodeStrictVPNCoreGraphJSON(payload, &req); err != nil {
			return migrateStoreReply{}, fmt.Errorf("invalid migrate_store payload: %w", err)
		}
	}
	chunk := req.Chunk
	if chunk == 0 {
		chunk = defaultMigrateChunk
	}
	if chunk < 1 || chunk > maxMigrateChunk {
		return migrateStoreReply{}, fmt.Errorf("migrate_store chunk must be between 1 and %d", maxMigrateChunk)
	}
	if req.Rebuild && req.DeleteLegacy {
		return migrateStoreReply{}, fmt.Errorf("migrate_store takes rebuild or delete_legacy, not both")
	}
	if req.Rebuild {
		return rt.rebuildIndex(req.After, chunk)
	}
	if req.DeleteLegacy {
		return rt.deleteLegacyDocument()
	}
	idx, err := rt.loadIndex()
	if err != nil {
		return migrateStoreReply{}, err
	}
	if idx != nil && idx.verified() {
		return rt.deleteLegacyPrograms(idx, chunk)
	}
	legacy, found, err := rt.loadSubscriptionRecords()
	if err != nil {
		return migrateStoreReply{}, err
	}
	if idx == nil && (!found || len(legacy.Records) == 0) {
		// Nothing was ever stored in the legacy layout: the store is split
		// from its first write, and there is nothing to move.
		return migrateStoreReply{Done: true, Verified: true, StoreVersion: storeVersionSplit}, nil
	}
	records := append([]subscriptionRecord(nil), legacy.Records...)
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	if idx == nil {
		idx = &indexDocument{Version: storeVersionSplit, Migration: &migrationState{Source: subscriptionRecordsKey}}
	}
	reply := migrateStoreReply{StoreVersion: storeVersionLegacy}
	cursor := idx.Migration.Cursor
	start := sort.Search(len(records), func(i int) bool { return records[i].ID > cursor })
	next := start
	frames := 0
	for ; next < len(records) && reply.Migrated < chunk; next++ {
		rec := records[next]
		if err := validStoreID(rec.ID); err != nil {
			return migrateStoreReply{}, fmt.Errorf("migrate_store cannot key legacy record: %w", err)
		}
		entry := legacyEntry(rec)
		pos := idx.position(rec.ID)
		if pos >= 0 && idx.Records[pos].Revision == entry.Revision && idx.Records[pos].ContentHash == entry.ContentHash {
			cursor = rec.ID
			continue
		}
		migrated := rec
		migrated.SchemaVersion = recordSchemaVersion
		if isScriptFile(rec) {
			program, err := rt.getFileScript(rec.ID)
			if err != nil {
				return migrateStoreReply{}, fmt.Errorf("migrate_store reads the program of %q: %w", rec.ID, err)
			}
			migrated.Content = program
		}
		raw, err := encodeRecord(withRevision(migrated))
		if err != nil {
			return migrateStoreReply{}, fmt.Errorf("migrate_store encodes %q: %w", rec.ID, err)
		}
		size := kvPutFrameBytes(recordKey(rec.ID), raw)
		if reply.Migrated > 0 && frames+size > migrateChunkFrameBytes {
			break
		}
		if err := rt.kvPut(recordKey(rec.ID), raw); err != nil {
			return migrateStoreReply{}, fmt.Errorf("migrate_store writes %q: %w", rec.ID, err)
		}
		frames += size
		if pos >= 0 {
			idx.Records[pos] = entry
		} else {
			idx.Records = append(idx.Records, entry)
		}
		if entry.Flags.RegexIncompatible {
			reply.RegexIncompatible = append(reply.RegexIncompatible, rec.ID)
		}
		reply.Migrated++
		cursor = rec.ID
	}
	idx.Migration.Cursor = cursor
	reply.Remaining = len(records) - next
	if reply.Migrated > 0 || next > start {
		if err := rt.putIndex(idx, false); err != nil {
			return migrateStoreReply{}, err
		}
	}
	if reply.Remaining > 0 {
		return reply, nil
	}
	// The verify rewrites the index and the whole legacy document, so it runs
	// in a call that wrote no record: a chunk that just wrote up to
	// migrateChunkFrameBytes leaves the verify to the next call, which keeps
	// every call inside migrate_store's stdout budget.
	if reply.Migrated > 0 {
		return reply, nil
	}
	return rt.verifyMigration(legacy, records, reply)
}

// verifyMigration re-reads the index and holds it to the legacy document
// read in the same call, then marks the legacy document and opens the store.
// A verify that fails rewinds the cursor, so the next call walks the legacy
// document again: entries that are right are skipped without a write, and
// whatever the index lost is written again.
func (rt *runtime) verifyMigration(legacy subscriptionRecordsDocument, records []subscriptionRecord, reply migrateStoreReply) (migrateStoreReply, error) {
	check, err := rt.loadIndex()
	if err != nil {
		return migrateStoreReply{}, err
	}
	if check == nil || check.Migration == nil {
		return migrateStoreReply{}, fmt.Errorf("migrate_store verify: the index did not land; call migrate_store again")
	}
	if problem := migrationMismatch(check, records); problem != "" {
		check.Migration.Cursor = ""
		if err := rt.putIndex(check, false); err != nil {
			return migrateStoreReply{}, err
		}
		return migrateStoreReply{}, fmt.Errorf("migrate_store verify: %s; the cursor is rewound, call migrate_store again", problem)
	}
	var programs []string
	for _, rec := range records {
		if isScriptFile(rec) {
			programs = append(programs, rec.ID)
		}
	}
	marked := legacy
	marked.MigratedTo = storeIndexKey
	if err := rt.putLegacyDocument(marked); err != nil {
		return migrateStoreReply{}, fmt.Errorf("migrate_store marks the legacy document: %w", err)
	}
	check.Migration.Verified = time.Now().UTC().Format(time.RFC3339)
	check.Migration.Legacy = subscriptionRecordsKey
	check.Migration.LegacyPrograms = programs
	check.Migration.Done = len(programs) == 0
	if err := rt.putIndex(check, false); err != nil {
		return migrateStoreReply{}, err
	}
	reply.Verified, reply.Done = true, check.Migration.Done
	reply.StoreVersion = storeVersionSplit
	reply.LegacyPrograms = len(programs)
	return reply, nil
}

// migrationMismatch names the first way the index disagrees with the legacy
// records, or "" when every id, revision and content hash matches.
func migrationMismatch(check *indexDocument, records []subscriptionRecord) string {
	if len(check.Records) != len(records) {
		return fmt.Sprintf("the index holds %d records, the legacy document %d", len(check.Records), len(records))
	}
	for _, rec := range records {
		want := legacyEntry(rec)
		pos := check.position(rec.ID)
		if pos < 0 {
			return fmt.Sprintf("%q is missing from the index", rec.ID)
		}
		if got := check.Records[pos]; got.Revision != want.Revision || got.ContentHash != want.ContentHash {
			return fmt.Sprintf("%q differs from the legacy document", rec.ID)
		}
	}
	return ""
}

// deleteLegacyPrograms is the cleanup after a verified migration: up to a
// chunk of legacy program keys per call, then the index write.
func (rt *runtime) deleteLegacyPrograms(idx *indexDocument, chunk int) (migrateStoreReply, error) {
	reply := migrateStoreReply{Verified: true, StoreVersion: storeVersionSplit}
	if idx.Migration == nil || len(idx.Migration.LegacyPrograms) == 0 {
		reply.Done = true
		return reply, nil
	}
	pending := idx.Migration.LegacyPrograms
	n := min(chunk, len(pending))
	for _, id := range pending[:n] {
		if err := rt.kvDelete(fileScriptKey(id)); err != nil {
			return migrateStoreReply{}, fmt.Errorf("migrate_store deletes the legacy program of %q: %w", id, err)
		}
	}
	idx.Migration.LegacyPrograms = append([]string(nil), pending[n:]...)
	idx.Migration.Done = len(idx.Migration.LegacyPrograms) == 0
	if err := rt.putIndex(idx, false); err != nil {
		return migrateStoreReply{}, err
	}
	reply.Done, reply.LegacyPrograms = idx.Migration.Done, len(idx.Migration.LegacyPrograms)
	return reply, nil
}

// maxRebuildChunk bounds one rebuild call: the index, up to three reads per
// entry (record-v2, staged-v2, archive-v2), the fresh index read, the write
// and the if_match retry fit migrate_store's 140 host calls.
const maxRebuildChunk = 45

// rebuildIndex re-derives up to chunk live entries, in id order after the
// id after, from their documents. The index is read again before it is
// written and only the rebuilt entries change, since the reads in between
// take time a concurrent write may use; the write carries if_match.
//
// S2 reads staged-v2-<id> wherever the record document alone would mislead
// (plan section 2.2):
//
//   - record-v2 at the entry's revision: the entry is derived from it, and
//     carryBookkeeping keeps the fetch fields and every staged fact;
//   - record-v2 at another revision: when staged-v2 carries the entry's
//     revision this is a half-done promotion killed after its index put, and
//     the entry is derived from the staged document (fetch completes it);
//     otherwise record-v2 is authoritative, as in S1;
//   - no record-v2: an entry with no live revision whose staged document has
//     an empty base revision (a staged new record or a staged restore) is
//     kept as it is; an entry whose staged document carries its revision is a
//     never-live half-done promotion, derived from that document; only an id
//     with nothing staged moves to Archived (when an archive exists) or loses
//     its entry.
//
// yagni: a save whose new entry a concurrent write dropped leaves a record no
// entry names, and nothing here can find it (the host lists no keys); saving
// the record again recovers it. The S2 compare-and-swap on every index write
// makes the case one that only a pre-S2 store can still hold.
func (rt *runtime) rebuildIndex(after string, chunk int) (migrateStoreReply, error) {
	idx, err := rt.loadIndex()
	if err != nil {
		return migrateStoreReply{}, err
	}
	if idx == nil || !idx.verified() {
		return migrateStoreReply{}, errStoreMigrationRequired
	}
	chunk = min(chunk, maxRebuildChunk)
	old := make(map[string]indexEntry, len(idx.Records))
	ids := make([]string, 0, len(idx.Records))
	for _, entry := range idx.Records {
		ids = append(ids, entry.ID)
		old[entry.ID] = entry
	}
	sort.Strings(ids)
	start := sort.SearchStrings(ids, after)
	if start < len(ids) && ids[start] == after {
		start++
	}
	end := min(start+chunk, len(ids))
	type rebuilt struct {
		id       string
		entry    *indexEntry // set: replace with this entry
		keep     bool        // the entry stays as it is
		archived string      // set: move to Archived with this time
	}
	var work []rebuilt
	derive := func(id string, rec subscriptionRecord) rebuilt {
		entry := indexEntryFor(withRevision(rec))
		return rebuilt{id: id, entry: &entry}
	}
	for _, id := range ids[start:end] {
		entry := old[id]
		if !entry.hasLiveRevision() {
			// A staged new record or a staged restore: its staged document
			// is all there is, and a record key under its id is an orphan.
			staged, err := rt.loadStaged(id)
			if err != nil {
				return migrateStoreReply{}, err
			}
			switch {
			case staged != nil:
				work = append(work, rebuilt{id: id, keep: true})
			case entry.StagedRestoredAt != "":
				work = append(work, rebuilt{id: id, archived: entry.StagedRestoredAt})
			default:
				work = append(work, rebuilt{id: id})
			}
			continue
		}
		value, found, err := rt.kvGet(recordKey(id))
		if err != nil {
			return migrateStoreReply{}, err
		}
		if found {
			rec, err := decodeRecordDoc(id, value)
			if err != nil {
				return migrateStoreReply{}, err
			}
			rec = withRevision(rec)
			if rec.Revision != entry.Revision {
				staged, err := rt.loadStaged(id)
				if err != nil {
					return migrateStoreReply{}, err
				}
				if staged != nil && staged.Record.Revision == entry.Revision {
					work = append(work, derive(id, staged.Record))
					continue
				}
			}
			work = append(work, derive(id, rec))
			continue
		}
		staged, err := rt.loadStaged(id)
		if err != nil {
			return migrateStoreReply{}, err
		}
		switch {
		case staged != nil && staged.Record.Revision == entry.Revision:
			work = append(work, derive(id, staged.Record))
			continue
		}
		archive, found, err := rt.kvGet(archiveKey(id))
		if err != nil {
			return migrateStoreReply{}, err
		}
		item := rebuilt{id: id}
		if found {
			var doc archivedRecord
			if json.Unmarshal(archive, &doc) == nil && doc.ArchivedAt != "" {
				item.archived = doc.ArchivedAt
			} else {
				item.archived = time.Now().UTC().Format(time.RFC3339)
			}
		}
		work = append(work, item)
	}
	next := ""
	if end < len(ids) {
		next = ids[end-1]
	}
	apply := func(target *indexDocument) error {
		for _, item := range work {
			pos := target.position(item.id)
			if pos < 0 || item.keep {
				continue
			}
			switch {
			case item.entry != nil:
				entry := *item.entry
				entry.carryBookkeeping(target.Records[pos])
				if entry.StagedRevision == entry.Revision {
					// The derived entry is the promoted revision: nothing of
					// it is staged any more.
					entry.clearStaged()
				}
				target.Records[pos] = entry
			case item.archived != "":
				entry := target.Records[pos]
				entry.ArchivedAt = item.archived
				entry.clearStaged()
				target.Records = append(target.Records[:pos], target.Records[pos+1:]...)
				target.Archived = append(target.Archived, entry)
			default:
				target.Records = append(target.Records[:pos], target.Records[pos+1:]...)
			}
		}
		return nil
	}
	fresh, err := rt.loadIndex()
	if err != nil {
		return migrateStoreReply{}, err
	}
	if fresh == nil {
		return migrateStoreReply{}, fmt.Errorf("rebuild: the index disappeared")
	}
	if err := apply(fresh); err != nil {
		return migrateStoreReply{}, err
	}
	raw, err := encodeIndex(fresh, false)
	if err != nil {
		return migrateStoreReply{}, err
	}
	if err := rt.putIndexRetrying(fresh, raw, false, apply); err != nil {
		return migrateStoreReply{}, err
	}
	return migrateStoreReply{Rebuilt: len(work), Next: next, Done: next == "", Verified: true, StoreVersion: storeVersionSplit}, nil
}

// deleteLegacyDocument deletes subscriptions-v1 (migrate_store
// {"delete_legacy": true}): the index, the document's deletion, the index
// write that records it (two more on a kv_conflict). It refuses until the
// split store has verified and every legacy program key is gone, so it never
// deletes the only copy of a record or a program. Core's readers stopped
// reading the document at a124 (plan section 2.6).
func (rt *runtime) deleteLegacyDocument() (migrateStoreReply, error) {
	idx, err := rt.loadIndex()
	if err != nil {
		return migrateStoreReply{}, err
	}
	if idx == nil || !idx.verified() {
		return migrateStoreReply{}, errStoreMigrationRequired
	}
	if idx.Migration != nil && len(idx.Migration.LegacyPrograms) > 0 {
		return migrateStoreReply{}, fmt.Errorf("migrate_store still has %d legacy program keys to delete; run migrate_store until it reports done, then delete the legacy document", len(idx.Migration.LegacyPrograms))
	}
	if err := rt.kvDelete(subscriptionRecordsKey); err != nil {
		return migrateStoreReply{}, err
	}
	rt.legacy = legacyCache{loaded: true}
	reply := migrateStoreReply{Done: true, Verified: true, StoreVersion: storeVersionSplit, DeletedLegacy: true}
	if idx.Migration == nil || idx.Migration.Legacy == "" {
		return reply, nil
	}
	mutate := func(target *indexDocument) error {
		if target.Migration != nil {
			target.Migration.Legacy = ""
		}
		return nil
	}
	if err := mutate(idx); err != nil {
		return migrateStoreReply{}, err
	}
	raw, err := encodeIndex(idx, false)
	if err != nil {
		return migrateStoreReply{}, err
	}
	return reply, rt.putIndexRetrying(idx, raw, false, mutate)
}
