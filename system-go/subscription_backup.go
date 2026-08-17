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
// A script file's program lives under its own key, so a plain record list
// carries the file's name but not the program that IS the file — a backup
// shaped like that restores every script file as a skip ("needs a template"),
// which is a backup in name only. Each program is reattached here: one extra
// host read per script file, billed against export's host_calls budget.
func (rt *runtime) exportBackup() ([]byte, error) {
	records, err := rt.listSubscriptions()
	if err != nil {
		return nil, err
	}
	settings, err := rt.loadSettings()
	if err != nil {
		return nil, err
	}
	for i := range records {
		if !isScriptFile(records[i]) {
			continue
		}
		script, err := rt.getFileScript(records[i].ID)
		if err != nil {
			return nil, fmt.Errorf("export reads the program of %q: %w", records[i].ID, err)
		}
		if script == "" {
			// A script file without its program cannot be restored from this
			// backup — the import would skip it as "needs a template". Failing
			// loudly beats writing a backup that looks complete and is not.
			return nil, fmt.Errorf("script file %q has no stored program; the backup cannot restore it", records[i].ID)
		}
		records[i].Content = script
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	if records == nil {
		records = []subscriptionRecord{}
	}
	return json.MarshalIndent(subscriptionBackup{
		Format:   subscriptionBackupFormat,
		Settings: settings,
		Records:  records,
	}, "", "  ")
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
	// downstream — after its program keys are already written. Refuse it here.
	if len(doc.Records) > maxSubscriptionRecords {
		return importOutcome{}, fmt.Errorf("backup carries %d records, limit %d", len(doc.Records), maxSubscriptionRecords)
	}

	existing := map[string]bool{}
	if current, err := rt.listSubscriptions(); err == nil {
		for _, rec := range current {
			existing[rec.ID] = true
		}
	}

	out := importOutcome{Skipped: map[string]string{}}
	// Persist everything in one document write. The plugin call budget charges
	// per host round trip, and per-record saves priced a twenty-record import
	// out of its own host_calls allowance. Normalisation happens once inside
	// the batch; per-record failures come back as skips.
	batchSkipped, err := rt.saveSubscriptionBatch(doc.Records)
	if err != nil {
		return importOutcome{}, err
	}
	for _, rec := range doc.Records {
		if strings.TrimSpace(rec.ID) == "" {
			out.Skipped["(unnamed)"] = "record has no id"
			continue
		}
		if why, bad := batchSkipped[rec.ID]; bad {
			out.Skipped[rec.ID] = why
			continue
		}
		if existing[rec.ID] {
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
