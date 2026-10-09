package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// subscriptionBackupFormat names the envelope. Import refuses anything else
// rather than guessing, so a file from another tool cannot be half-read into a
// state nobody intended.
const subscriptionBackupFormat = "lattice.sub-store.subscriptions.v1"

type subscriptionBackup struct {
	Format   string               `json:"format"`
	Settings pluginSettings       `json:"settings"`
	Records  []subscriptionRecord `json:"records"`
}

type importOutcome struct {
	Imported []string          `json:"imported"`
	Skipped  map[string]string `json:"skipped"`
	Replaced []string          `json:"replaced"`
}

// exportBackup writes everything this plugin owns.
//
// Records are sorted by id so two exports of the same data are byte-identical:
// an export that depended on map order would diff against itself and be useless
// for comparing a backup against what is live.
//
// On the split store that is the index, one read per live record (a script
// file's program is in its record) and the settings: N + 2 host calls. On a
// store that has not migrated it is the legacy document, its program keys and
// the settings. Archived records are not exported; a backup restores what is
// live.
// maxExportReplyBytes bounds export's reply. Core counts the reply frame
// against the method's signed stdout_bytes (8 MiB), and the backup travels
// as a JSON string inside it, so its quotes and newlines are escaped a second
// time. The rest covers the frame and the reads (320 small host_call frames).
const maxExportReplyBytes = 8<<20 - 512<<10

// exportTooLargeCode opens the refusal of a backup past maxExportReplyBytes.
const exportTooLargeCode = "export_too_large"

// exportReply wraps a backup for the export method, or refuses one that core
// would kill on the way out.
//
// yagni: a store past the bound cannot be backed up in one call. A chunked
// export (records after an id, as rebuild pages) lifts it; until a store needs
// it, the refusal says what happened instead of a dropped connection.
func exportReply(backup []byte) (json.RawMessage, error) {
	reply := mustJSON(map[string]any{"backup": string(backup)})
	if len(reply) > maxExportReplyBytes {
		return nil, fmt.Errorf("%s: the backup is %d bytes once encoded, and one call can return %d", exportTooLargeCode, len(reply), maxExportReplyBytes)
	}
	return reply, nil
}

func (rt *runtime) exportBackup() ([]byte, error) {
	listing, err := rt.storeListing()
	if err != nil {
		return nil, err
	}
	settings, err := rt.loadSettings()
	if err != nil {
		return nil, err
	}
	records := make([]subscriptionRecord, 0, len(listing.Records))
	legacy, err := rt.exportLegacyRecords(listing)
	if err != nil {
		return nil, err
	}
	for _, entry := range listing.Records {
		rec, ok := legacy[entry.ID]
		if !ok {
			stored, found, err := rt.loadRecord(entry.ID)
			if err != nil {
				return nil, fmt.Errorf("export reads %q: %w", entry.ID, err)
			}
			if !found {
				return nil, fmt.Errorf("export: %q is listed but has no stored record; run migrate_store with rebuild", entry.ID)
			}
			rec = withBookkeeping(stored, entry)
		}
		if isScriptFile(rec) && rec.Content == "" {
			// A script file without its program cannot be restored from this
			// backup: the import would skip it as "needs a template". Failing
			// loudly beats writing a backup that looks complete and is not.
			return nil, fmt.Errorf("script file %q has no stored program; the backup cannot restore it", entry.ID)
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return json.MarshalIndent(subscriptionBackup{
		Format:   subscriptionBackupFormat,
		Settings: settings,
		Records:  records,
	}, "", "  ")
}

// exportLegacyRecords returns the records a store that has not migrated
// still keeps in the legacy document, programs reattached, by id. The
// document is already cached from the listing, so only programs cost calls.
func (rt *runtime) exportLegacyRecords(listing storeListing) (map[string]subscriptionRecord, error) {
	out := map[string]subscriptionRecord{}
	if listing.Version != storeVersionLegacy {
		return out, nil
	}
	legacy, err := rt.legacyDocument()
	if err != nil || legacy == nil {
		return out, err
	}
	for _, rec := range legacy.Records {
		if isScriptFile(rec) {
			script, err := rt.getFileScript(rec.ID)
			if err != nil {
				return nil, fmt.Errorf("export reads the program of %q: %w", rec.ID, err)
			}
			rec.Content = script
		}
		out[rec.ID] = withRevision(rec)
	}
	return out, nil
}

// importBackup restores records.
//
// It is additive by default: a record already present is reported as replaced
// rather than silently overwritten, and nothing absent from the backup is
// deleted. A restore that quietly removed newer work would be worse than no
// restore at all.
func (rt *runtime) importBackup(data []byte) (importOutcome, error) {
	var doc subscriptionBackup
	if err := json.Unmarshal(data, &doc); err != nil {
		return importOutcome{}, fmt.Errorf("decode backup: %w", err)
	}
	if doc.Format != subscriptionBackupFormat {
		if strings.TrimSpace(doc.Format) == "" {
			return importOutcome{}, fmt.Errorf("backup has no format")
		}
		return importOutcome{}, fmt.Errorf("unsupported backup format %q", doc.Format)
	}
	// The store can never hold more than this, so a larger backup can only fail
	// downstream. Refuse it here.
	if len(doc.Records) > maxSubscriptionRecords {
		return importOutcome{}, fmt.Errorf("backup carries %d records, limit %d", len(doc.Records), maxSubscriptionRecords)
	}

	out := importOutcome{Skipped: map[string]string{}}
	// One index read, one write per record, one index write: the plugin call
	// budget charges per host round trip, and per-record saves priced a
	// twenty-record import out of its own host_calls allowance. Normalisation
	// happens once inside the batch; per-record failures come back as skips.
	batch, err := rt.saveSubscriptionBatch(doc.Records)
	if err != nil {
		if strings.HasPrefix(err.Error(), batchTooLargeCode) {
			return importOutcome{}, fmt.Errorf("%w; split the backup into smaller ones", err)
		}
		return importOutcome{}, err
	}
	for _, rec := range doc.Records {
		if strings.TrimSpace(rec.ID) == "" {
			out.Skipped["(unnamed)"] = "record has no id"
			continue
		}
		if why, bad := batch.skipped[rec.ID]; bad {
			out.Skipped[rec.ID] = why
			continue
		}
		if batch.replaced[rec.ID] {
			out.Replaced = append(out.Replaced, rec.ID)
		}
		out.Imported = append(out.Imported, rec.ID)
	}
	if doc.Settings != (pluginSettings{}) {
		if err := rt.saveSettings(doc.Settings); err != nil {
			out.Skipped["(settings)"] = err.Error()
		}
	}
	return out, nil
}
