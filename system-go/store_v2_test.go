package main

import (
	"encoding/json"
	"testing"
	"time"
)

func fixedTime() time.Time { return time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC) }

// listedEntries is the store's live entries as list reads them.
func listedEntries(t *testing.T, rt *runtime) []indexEntry {
	t.Helper()
	rt.legacy = legacyCache{}
	listing, err := rt.storeListing()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return listing.Records
}

// indexEntryOf is one live entry, bookkeeping included.
func indexEntryOf(t *testing.T, rt *runtime, id string) indexEntry {
	t.Helper()
	for _, entry := range listedEntries(t, rt) {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("%q is not listed", id)
	return indexEntry{}
}

// seedLegacyStore writes a legacy store the way the 0.16 runtime left it: one
// document with every record, and each script file's program under its own
// key with the record's Content empty and ScriptDigest set.
func seedLegacyStore(t *testing.T, host *kvHostCaller, records []subscriptionRecord) subscriptionRecordsDocument {
	t.Helper()
	doc := subscriptionRecordsDocument{Version: 1}
	for _, rec := range records {
		rec.SchemaVersion = 1
		if isScriptFile(rec) {
			host.values[fileScriptKey(rec.ID)] = []byte(rec.Content)
			rec.ScriptDigest = digestOf(rec.Content)
			rec.Content = ""
		}
		doc.Records = append(doc.Records, withRevision(rec))
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	host.values[subscriptionRecordsKey] = raw
	return doc
}
