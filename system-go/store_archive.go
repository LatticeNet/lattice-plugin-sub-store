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
// archive write, the index write (two more on a kv_conflict), then the record
// key's deletion and, when a staged revision existed, the staged key's
// (storeArchive). A fleet-bound record is refused with
// fleet_delete_requires_plan: deleting it changes what identity holders
// receive, so it goes through plans.propose {action: "delete"}, whose apply
// deletes it through apply_revision.
func (rt *runtime) deleteSubscription(id string) error {
	idx, err := rt.writableIndex()
	if err != nil {
		return err
	}
	pos := idx.position(id)
	if pos < 0 {
		return fmt.Errorf("subscription %q was not found", id)
	}
	entry := idx.Records[pos]
	if !entry.hasLiveRevision() {
		return refusalErr(storeRefusal{
			Code: refusedStagedMismatch, IDs: []string{id}, StagedRevision: entry.StagedRevision,
			Message: fmt.Sprintf("subscription %q has no live revision to delete; discard its staged revision %s instead", id, orNone(entry.StagedRevision)),
		})
	}
	if entry.Flags.FleetBound {
		return refusalErr(storeRefusal{
			Code: refusedDeleteRequiresPlan, IDs: []string{id},
			Message: fmt.Sprintf("subscription %q is fleet-bound; delete it through plans.propose with action delete", id),
		})
	}
	loaded, err := rt.storeLoadRecord(idx, id, false)
	if err != nil {
		return err
	}
	if r, refused := loaded.writeRefusal(); refused {
		return refusalErr(r)
	}
	if !loaded.HasLive {
		return fmt.Errorf("subscription %q has an index entry but no stored record; run migrate_store with rebuild", id)
	}
	return rt.storeArchive(idx, loaded)
}

// restoreOutcome is what restore did: the record live, or staged.
type restoreOutcome struct {
	Record         subscriptionRecord
	Staged         bool
	StagedRevision string
}

// restoreSubscription brings an archived record back under the same id. A
// provider record restores live: the index, the archive, the record write,
// the index write (two more on a kv_conflict), then the archive key's
// deletion. A record that would be fleet-bound is staged instead (plan
// section 3): the record goes to staged-v2-<id> with an empty base revision,
// the entry moves to the live list as a stagedEntryFor entry that remembers
// when it was archived, and archive-v2-<id> stays until the promotion deletes
// it or discard_staged returns the entry to Archived. The guards every write
// runs (storeWriteRecord's) run here too.
func (rt *runtime) restoreSubscription(id string) (restoreOutcome, error) {
	idx, err := rt.writableIndex()
	if err != nil {
		return restoreOutcome{}, err
	}
	pos := idx.archivedPosition(id)
	if pos < 0 {
		return restoreOutcome{}, fmt.Errorf("subscription %q is not archived", id)
	}
	if idx.position(id) >= 0 {
		return restoreOutcome{}, fmt.Errorf("subscription %q is both live and archived; purge the archive or rebuild the index", id)
	}
	if len(idx.Records) >= maxSubscriptionRecords {
		return restoreOutcome{}, fmt.Errorf("too many subscriptions: %d, limit %d", len(idx.Records)+1, maxSubscriptionRecords)
	}
	value, found, err := rt.kvGet(archiveKey(id))
	if err != nil {
		return restoreOutcome{}, err
	}
	if !found {
		return restoreOutcome{}, fmt.Errorf("subscription %q is archived in the index but its archive is missing; purge it", id)
	}
	if len(value) > maxRecordDocBytes {
		return restoreOutcome{}, fmt.Errorf("archive of %q exceeds %d bytes", id, maxRecordDocBytes)
	}
	var archived archivedRecord
	if err := json.Unmarshal(value, &archived); err != nil {
		return restoreOutcome{}, fmt.Errorf("decode archive of %q: %w", id, err)
	}
	rec := withRevision(archived.subscriptionRecord)
	archivedEntry := idx.Archived[pos]
	ctx := writeContext{
		origin: originRestore, rec: rec, facts: factsOfRecord(rec),
		live:      newStoreGraph(idx.Records, graphLive),
		union:     newStoreGraph(idx.Records, graphUnion),
		effective: newStoreGraph(idx.Records, graphEffective),
	}
	stage := ctx.stage()
	if r := ctx.refusal(stage); r != nil {
		return restoreOutcome{}, refusalErr(*r)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	mutate := func(target *indexDocument) error {
		at := target.archivedPosition(id)
		if at < 0 {
			return fmt.Errorf("subscription %q was restored or purged while this restore ran", id)
		}
		if target.position(id) >= 0 {
			return fmt.Errorf("subscription %q is both live and archived; purge the archive or rebuild the index", id)
		}
		entry := target.Archived[at]
		target.Archived = append(target.Archived[:at], target.Archived[at+1:]...)
		if stage {
			staged := stagedEntryFor(rec, now)
			staged.StagedRestoredAt = entry.ArchivedAt
			target.Records = append(target.Records, staged)
			return nil
		}
		entry.ArchivedAt = ""
		target.Records = append(target.Records, entry)
		return nil
	}
	if err := mutate(idx); err != nil {
		return restoreOutcome{}, err
	}
	indexRaw, err := encodeIndex(idx, false)
	if err != nil {
		return restoreOutcome{}, err
	}
	if stage {
		stagedRaw, err := encodeStaged(stagedDocument{Record: rec, StagedAt: now})
		if err != nil {
			return restoreOutcome{}, err
		}
		if err := rt.kvPut(stagedKey(id), stagedRaw); err != nil {
			return restoreOutcome{}, err
		}
		if err := rt.putIndexRetrying(idx, indexRaw, false, mutate); err != nil {
			return restoreOutcome{}, err
		}
		return restoreOutcome{Record: rec, Staged: true, StagedRevision: rec.Revision}, nil
	}
	recordRaw, err := encodeRecord(rec)
	if err != nil {
		return restoreOutcome{}, err
	}
	if err := rt.kvPut(recordKey(id), recordRaw); err != nil {
		return restoreOutcome{}, err
	}
	if err := rt.putIndexRetrying(idx, indexRaw, false, mutate); err != nil {
		return restoreOutcome{}, err
	}
	if err := rt.kvDelete(archiveKey(id)); err != nil {
		return restoreOutcome{}, err
	}
	return restoreOutcome{Record: withBookkeeping(rec, archivedEntry)}, nil
}

// purgeSubscription deletes an archived record for good: the index, the
// index write without it (two more on a kv_conflict), then the archive key's
// deletion. The index goes first, so a failure in between leaves an archive
// key nothing names rather than an entry whose archive is gone.
func (rt *runtime) purgeSubscription(id string) error {
	idx, err := rt.writableIndex()
	if err != nil {
		return err
	}
	mutate := func(target *indexDocument) error {
		at := target.archivedPosition(id)
		if at < 0 {
			return fmt.Errorf("subscription %q is not archived; delete archives a live record before it can be purged", id)
		}
		target.Archived = append(target.Archived[:at], target.Archived[at+1:]...)
		return nil
	}
	if err := mutate(idx); err != nil {
		return err
	}
	indexRaw, err := encodeIndex(idx, false)
	if err != nil {
		return err
	}
	if err := rt.putIndexRetrying(idx, indexRaw, false, mutate); err != nil {
		return err
	}
	return rt.kvDelete(archiveKey(id))
}

func stripBookkeeping(rec subscriptionRecord) subscriptionRecord {
	rec.LastFetchAt, rec.LastFetchOK, rec.LastError, rec.Userinfo = "", false, "", ""
	return rec
}
