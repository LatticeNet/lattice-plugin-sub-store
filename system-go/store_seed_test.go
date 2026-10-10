package main

import "fmt"

// saveSubscription seeds a store for a test: it writes one record live with
// S1's storeSave semantics (normalized, its index entry derived and its fetch
// bookkeeping carried), and runs neither the staging decision nor the record
// rules. Tests need store states no write method can reach, such as a fleet
// record live without a plan or a collection already mixing a legacy and a
// fleet member, to pin how render and fetch refuse them. Production code has
// no way to call it: every record write a method makes goes through
// storeWriteRecord (TestEveryRecordWriteGoesThroughStoreWriteRecord).
func (rt *runtime) saveSubscription(rec subscriptionRecord) error {
	idx, err := rt.writableIndex()
	if err != nil {
		return err
	}
	if idx.archivedPosition(rec.ID) >= 0 {
		return fmt.Errorf("subscription %q: archived", rec.ID)
	}
	nrec, err := normalizeSubscriptionForStore(rec)
	if err != nil {
		return err
	}
	entry := indexEntryFor(nrec)
	if pos := idx.position(nrec.ID); pos >= 0 {
		entry.carryBookkeeping(idx.Records[pos])
		idx.Records[pos] = entry
	} else {
		if len(idx.Records) >= maxSubscriptionRecords {
			return fmt.Errorf("too many subscriptions: %d, limit %d", len(idx.Records)+1, maxSubscriptionRecords)
		}
		incoming := legacyEntry(rec)
		entry.LastFetchAt, entry.LastFetchOK, entry.LastError, entry.Userinfo = incoming.LastFetchAt, incoming.LastFetchOK, incoming.LastError, incoming.Userinfo
		idx.Records = append(idx.Records, entry)
	}
	indexRaw, err := encodeIndex(idx, true)
	if err != nil {
		return err
	}
	recordRaw, err := encodeRecord(nrec)
	if err != nil {
		return err
	}
	if err := rt.kvPut(recordKey(nrec.ID), recordRaw); err != nil {
		return err
	}
	return rt.putIndexRaw(idx, indexRaw)
}
