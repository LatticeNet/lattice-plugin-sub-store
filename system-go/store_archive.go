package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// Delete archives (plan section 3.2). The record moves to archive-v2-<id>
// with the time it was archived, its index entry moves from Records to
// Archived, and its id stays reserved until purge, so restore brings it back
// under the same id its shares and collection members name.
//
// Shares are not archived from inside the plugin: core answers shares.archive
// only to an operator's direct gateway call, never to a plugin's rpc.call, so
// the UI archives and restores a record's shares itself around these calls.
// Neither reply lists the shares: the UI already holds them from its own
// shares.list, and the plan's host-call counts leave no room for the call.

// archivedRecord is an archive document: the record plus when it was archived.
type archivedRecord struct {
	subscriptionRecord
	ArchivedAt string `json:"archived_at"`
}

// deleteSubscription archives a live record: the index, the record, the
// archive write, the index write, then the record key's deletion. Five host
// calls. The index lands before the record key goes, so a failure in between
// leaves an orphan record key the next restore overwrites, never an index
// entry pointing at nothing.
func (rt *runtime) deleteSubscription(id string) error {
	idx, err := rt.writableIndex()
	if err != nil {
		return err
	}
	pos := idx.position(id)
	if pos < 0 {
		return fmt.Errorf("subscription %q was not found", id)
	}
	rec, found, err := rt.loadRecord(id)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("subscription %q has an index entry but no stored record; run migrate_store with rebuild", id)
	}
	archivedAt := time.Now().UTC().Format(time.RFC3339)
	archive, err := json.Marshal(archivedRecord{subscriptionRecord: stripBookkeeping(rec), ArchivedAt: archivedAt})
	if err != nil {
		return err
	}
	entry := idx.Records[pos]
	entry.ArchivedAt = archivedAt
	idx.Records = append(idx.Records[:pos], idx.Records[pos+1:]...)
	idx.Archived = append(idx.Archived, entry)
	indexRaw, err := encodeIndex(idx, false)
	if err != nil {
		return err
	}
	if err := rt.kvPut(archiveKey(id), archive); err != nil {
		return err
	}
	if err := rt.kvPut(storeIndexKey, indexRaw); err != nil {
		return err
	}
	return rt.kvDelete(recordKey(id))
}

// restoreSubscription brings an archived record back under the same id: the
// index, the archive, the record write, the index write, then the archive
// key's deletion. Five host calls.
func (rt *runtime) restoreSubscription(id string) (subscriptionRecord, error) {
	idx, err := rt.writableIndex()
	if err != nil {
		return subscriptionRecord{}, err
	}
	pos := idx.archivedPosition(id)
	if pos < 0 {
		return subscriptionRecord{}, fmt.Errorf("subscription %q is not archived", id)
	}
	if idx.position(id) >= 0 {
		return subscriptionRecord{}, fmt.Errorf("subscription %q is both live and archived; purge the archive or rebuild the index", id)
	}
	if len(idx.Records) >= maxSubscriptionRecords {
		return subscriptionRecord{}, fmt.Errorf("too many subscriptions: %d, limit %d", len(idx.Records)+1, maxSubscriptionRecords)
	}
	value, found, err := rt.kvGet(archiveKey(id))
	if err != nil {
		return subscriptionRecord{}, err
	}
	if !found {
		return subscriptionRecord{}, fmt.Errorf("subscription %q is archived in the index but its archive is missing; purge it", id)
	}
	if len(value) > maxRecordDocBytes {
		return subscriptionRecord{}, fmt.Errorf("archive of %q exceeds %d bytes", id, maxRecordDocBytes)
	}
	var archived archivedRecord
	if err := json.Unmarshal(value, &archived); err != nil {
		return subscriptionRecord{}, fmt.Errorf("decode archive of %q: %w", id, err)
	}
	rec := withRevision(archived.subscriptionRecord)
	entry := idx.Archived[pos]
	entry.ArchivedAt = ""
	idx.Archived = append(idx.Archived[:pos], idx.Archived[pos+1:]...)
	idx.Records = append(idx.Records, entry)
	indexRaw, err := encodeIndex(idx, false)
	if err != nil {
		return subscriptionRecord{}, err
	}
	recordRaw, err := encodeRecord(rec)
	if err != nil {
		return subscriptionRecord{}, err
	}
	if err := rt.kvPut(recordKey(id), recordRaw); err != nil {
		return subscriptionRecord{}, err
	}
	if err := rt.kvPut(storeIndexKey, indexRaw); err != nil {
		return subscriptionRecord{}, err
	}
	if err := rt.kvDelete(archiveKey(id)); err != nil {
		return subscriptionRecord{}, err
	}
	return withBookkeeping(rec, entry), nil
}

// purgeSubscription deletes an archived record for good: the index, the
// index write without it, then the archive key's deletion. Three host calls.
// The index goes first, so a failure in between leaves an archive key nothing
// names rather than an entry whose archive is gone.
func (rt *runtime) purgeSubscription(id string) error {
	idx, err := rt.writableIndex()
	if err != nil {
		return err
	}
	pos := idx.archivedPosition(id)
	if pos < 0 {
		return fmt.Errorf("subscription %q is not archived; delete archives a live record before it can be purged", id)
	}
	idx.Archived = append(idx.Archived[:pos], idx.Archived[pos+1:]...)
	if err := rt.putIndex(idx, false); err != nil {
		return err
	}
	return rt.kvDelete(archiveKey(id))
}

func stripBookkeeping(rec subscriptionRecord) subscriptionRecord {
	rec.LastFetchAt, rec.LastFetchOK, rec.LastError, rec.Userinfo = "", false, "", ""
	return rec
}
