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
func (rt *runtime) exportBackup() ([]byte, error) {
	records, err := rt.listSubscriptions()
	if err != nil {
		return nil, err
	}
	settings, err := rt.loadSettings()
	if err != nil {
		return nil, err
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

	existing := map[string]bool{}
	if current, err := rt.listSubscriptions(); err == nil {
		for _, rec := range current {
			existing[rec.ID] = true
		}
	}

	out := importOutcome{Skipped: map[string]string{}}
	for _, rec := range doc.Records {
		if strings.TrimSpace(rec.ID) == "" {
			out.Skipped["(unnamed)"] = "record has no id"
			continue
		}
		if err := rt.saveSubscription(rec); err != nil {
			out.Skipped[rec.ID] = err.Error()
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
