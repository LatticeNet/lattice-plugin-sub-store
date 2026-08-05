package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxMigrationRecords bounds one import so a misconfigured or hostile endpoint
// cannot fill the record store in a single call.
const maxMigrationRecords = 512

// migratedOrigin preserves an imported record exactly as the source produced it.
//
// The operator's requirement is that migrating loses nothing, and the honest way
// to promise that is not to map carefully - it is to keep the original. A field
// this plugin does not understand today survives here and can be read later,
// rather than being silently dropped by a mapping that only knew about the
// fields someone thought of.
type migratedOrigin struct {
	Source string          `json:"source"`
	Kind   string          `json:"kind"`
	Raw    json.RawMessage `json:"raw"`
}

type migrationReport struct {
	Imported  []string          `json:"imported"`
	Skipped   map[string]string `json:"skipped"`
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
}

// upstreamSub is the subset of an upstream Sub-Store subscription this plugin
// maps onto its own record. Everything else is preserved verbatim in Origin.
type upstreamSub struct {
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Content string            `json:"content"`
	UA      string            `json:"ua"`
	Process []json.RawMessage `json:"process"`
}

// migrateFromSubStore imports subscriptions from a running Sub-Store instance.
//
// It is additive and idempotent: nothing local is deleted, and re-running
// replaces the records it created rather than duplicating them. That matters
// because the operator will run it more than once - once to see what happens,
// and once for real.
func (rt *runtime) migrateFromSubStore(req subStoreRequest) (migrationReport, error) {
	base, err := validateBaseURL(req.BaseURL)
	if err != nil {
		return migrationReport{}, err
	}

	status, body, err := rt.httpDo("GET", base+"/api/subs", nil)
	if err != nil {
		return migrationReport{}, fmt.Errorf("list subscriptions from the source: %s", redactURLs(err.Error()))
	}
	if status < 200 || status >= 300 {
		return migrationReport{}, fmt.Errorf("the source returned status %d listing subscriptions", status)
	}

	var envelope struct {
		Status string            `json:"status"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return migrationReport{}, fmt.Errorf("decode the source's subscription list: %w", err)
	}

	report := migrationReport{Skipped: map[string]string{}, Total: len(envelope.Data)}
	items := envelope.Data
	if len(items) > maxMigrationRecords {
		items = items[:maxMigrationRecords]
		report.Truncated = true
	}

	for i, raw := range items {
		var sub upstreamSub
		if err := json.Unmarshal(raw, &sub); err != nil {
			report.Skipped[fmt.Sprintf("item-%d", i)] = "could not be decoded"
			continue
		}
		name := strings.TrimSpace(sub.Name)
		if name == "" {
			report.Skipped[fmt.Sprintf("item-%d", i)] = "has no name"
			continue
		}
		id := migratedRecordID(name)
		// An unknown operator would make every later render fail, so it is
		// reported here instead of being written and discovered at serve time.
		if err := validateOperators(sub.Process); err != nil {
			report.Skipped[name] = err.Error()
			continue
		}
		rec := subscriptionRecord{
			ID:        id,
			Name:      name,
			URL:       strings.TrimSpace(sub.URL),
			Content:   sub.Content,
			UA:        strings.TrimSpace(sub.UA),
			Operators: sub.Process,
			Origin:    &migratedOrigin{Source: "sub-store", Kind: "subscription", Raw: raw},
		}
		if err := rt.saveSubscription(rec); err != nil {
			report.Skipped[name] = err.Error()
			continue
		}
		report.Imported = append(report.Imported, id)
	}
	return report, nil
}

// migratedRecordID derives a stable id from the source name so re-running an
// import replaces rather than duplicates.
func migratedRecordID(name string) string {
	var b strings.Builder
	b.WriteString("imported-")
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			b.WriteRune('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "imported" || id == "" {
		return "imported-unnamed"
	}
	return id
}
