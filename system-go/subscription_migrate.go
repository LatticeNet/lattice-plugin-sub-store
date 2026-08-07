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
	Imported []string `json:"imported"`
	// Skipped is about records the source offered and this plugin refused.
	Skipped map[string]string `json:"skipped"`
	// Unavailable is about whole endpoints the source did not answer. It is
	// separate because "this source has no combinations" is not a failure, and
	// mixing the two makes a clean import of an older source look broken — while
	// still saying so, because a mistyped endpoint name looks identical to an
	// absent one from here.
	Unavailable map[string]string `json:"unavailable,omitempty"`
	Total       int               `json:"total"`
	Truncated   bool              `json:"truncated"`
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

	subs, err := rt.fetchUpstreamList(base, "subs")
	if err != nil {
		return migrationReport{}, err
	}
	// Combinations and files were left behind by the first version of this,
	// which read subscriptions alone: an operator with four subscriptions, two
	// combinations and fifteen files got four records and no warning.
	//
	// A missing endpoint is not fatal. An older source may not serve all three,
	// and importing what it does have beats refusing the whole migration.
	collections, collectionsErr := rt.fetchUpstreamList(base, "collections")
	// `wholeFiles` is upstream's name for this; `files` is a different route.
	files, filesErr := rt.fetchUpstreamList(base, "wholeFiles")

	report := migrationReport{Skipped: map[string]string{}}
	report.Total = len(subs) + len(collections) + len(files)
	if collectionsErr != nil || filesErr != nil {
		report.Unavailable = map[string]string{}
		if collectionsErr != nil {
			report.Unavailable["collections"] = collectionsErr.Error()
		}
		if filesErr != nil {
			report.Unavailable["files"] = filesErr.Error()
		}
	}

	// The cap covers the whole import rather than each kind, because the store
	// it protects is shared. Subscriptions come first so a combination that
	// survives the cap can still find its members.
	budget := maxMigrationRecords
	items, budget := takeWithinBudget(subs, budget)
	collections, budget = takeWithinBudget(collections, budget)
	files, budget = takeWithinBudget(files, budget)
	report.Truncated = budget == 0 && report.Total > maxMigrationRecords

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

	// Order matters. A combination names its members and a file names its node
	// source, so both must be written after the records they point at exist —
	// otherwise the first render is the thing that discovers the reference is
	// dangling.
	rt.importUpstreamCollections(collections, &report)
	rt.importUpstreamFiles(files, &report)
	return report, nil
}

// takeWithinBudget trims a batch to what is left of the import cap.
func takeWithinBudget(items []json.RawMessage, budget int) ([]json.RawMessage, int) {
	if budget <= 0 {
		return nil, 0
	}
	if len(items) > budget {
		return items[:budget], 0
	}
	return items, budget - len(items)
}

// migratedRecordID derives a stable id from the source name so re-running an
// import replaces rather than duplicates.
//
// Subscriptions keep the bare prefix: records already imported by an earlier
// version carry it, and changing it would orphan them.
func migratedRecordID(name string) string {
	return migratedKindID("", name)
}

// migratedKindID namespaces an id by what it came from.
//
// Upstream keeps subscriptions, combinations and files in three separate
// namespaces, so a subscription and a file may share a name. Deriving one id
// from the name alone silently overwrote the first with the second — found by
// a test whose source served the same list for every endpoint, which is exactly
// what a collision looks like from the store's side.
func migratedKindID(kind, name string) string {
	var b strings.Builder
	b.WriteString("imported-")
	if kind != "" {
		b.WriteString(kind)
		b.WriteString("-")
	}
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			b.WriteRune('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" || id == "imported" || id == "imported-"+kind {
		if kind == "" {
			return "imported-unnamed"
		}
		return "imported-" + kind + "-unnamed"
	}
	return id
}
