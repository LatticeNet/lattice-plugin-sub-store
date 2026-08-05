package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	// subscriptionRecordsKey holds every subscription definition in one document,
	// the same shape the pipeline records use. One document rather than one key
	// per record because the host exposes no key listing or deletion: a document
	// keeps enumerate and delete expressible with the calls that exist.
	subscriptionRecordsKey = "subscriptions-v1"
	// maxSubscriptionRecords and maxSubscriptionDocBytes bound what this plugin
	// writes. The host's KV put accepts a value of any size and KV is serialized
	// into the state file that is rewritten in full on every state write, so an
	// unbounded document here would slow every unrelated write in the server. The
	// plugin bounds itself rather than waiting for the host-side fix.
	maxSubscriptionRecords  = 256
	maxSubscriptionDocBytes = 1 << 20
	// maxSubscriptionInlineBytes bounds inline content on one record. Remote
	// content does not live here; it arrives with the fetch work.
	maxSubscriptionInlineBytes = 256 << 10
)

type subscriptionRecordsDocument struct {
	Version int                  `json:"version"`
	Records []subscriptionRecord `json:"records"`
}

// subscriptionRecord is one subscription definition. Target is the client family
// the engine produces for; the core's format only decides how that output is
// encoded, so the two are independent and both are needed.
type subscriptionRecord struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	URL           string            `json:"url,omitempty"`
	Content       string            `json:"content,omitempty"`
	UA            string            `json:"ua,omitempty"`
	Target        string            `json:"target,omitempty"`
	Operators     []json.RawMessage `json:"operators,omitempty"`
	// Origin is set on an imported record and holds the source object verbatim,
	// so a migration cannot lose a field this plugin does not yet understand.
	Origin *migratedOrigin `json:"origin,omitempty"`
}

func (rt *runtime) loadSubscriptionRecords() (subscriptionRecordsDocument, error) {
	value, found, err := rt.kvGet(subscriptionRecordsKey)
	if err != nil || !found {
		return subscriptionRecordsDocument{Version: 1}, err
	}
	if len(value) > maxSubscriptionDocBytes {
		return subscriptionRecordsDocument{}, fmt.Errorf("subscription records exceed %d bytes", maxSubscriptionDocBytes)
	}
	var doc subscriptionRecordsDocument
	if err := json.Unmarshal(value, &doc); err != nil {
		return subscriptionRecordsDocument{}, fmt.Errorf("decode subscription records: %w", err)
	}
	if doc.Version == 0 {
		doc.Version = 1
	}
	return doc, nil
}

func (rt *runtime) saveSubscriptionRecords(doc subscriptionRecordsDocument) error {
	if len(doc.Records) > maxSubscriptionRecords {
		return fmt.Errorf("too many subscriptions: %d, limit %d", len(doc.Records), maxSubscriptionRecords)
	}
	sort.Slice(doc.Records, func(i, j int) bool { return doc.Records[i].ID < doc.Records[j].ID })
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if len(raw) > maxSubscriptionDocBytes {
		return fmt.Errorf("subscription records are too large: %d bytes, limit %d", len(raw), maxSubscriptionDocBytes)
	}
	return rt.kvPut(subscriptionRecordsKey, raw)
}

// saveSubscription inserts or replaces one definition.
func (rt *runtime) saveSubscription(rec subscriptionRecord) error {
	if strings.TrimSpace(rec.ID) == "" {
		return fmt.Errorf("subscription id is required")
	}
	if len(rec.Content) > maxSubscriptionInlineBytes {
		return fmt.Errorf("subscription %q inline content is too large: %d bytes, limit %d", rec.ID, len(rec.Content), maxSubscriptionInlineBytes)
	}
	if err := validateOperators(rec.Operators); err != nil {
		return fmt.Errorf("subscription %q: %w", rec.ID, err)
	}
	if rec.SchemaVersion == 0 {
		rec.SchemaVersion = 1
	}
	doc, err := rt.loadSubscriptionRecords()
	if err != nil {
		return err
	}
	replaced := false
	for i := range doc.Records {
		if doc.Records[i].ID == rec.ID {
			doc.Records[i] = rec
			replaced = true
			break
		}
	}
	if !replaced {
		doc.Records = append(doc.Records, rec)
	}
	return rt.saveSubscriptionRecords(doc)
}

func (rt *runtime) getSubscription(id string) (subscriptionRecord, error) {
	doc, err := rt.loadSubscriptionRecords()
	if err != nil {
		return subscriptionRecord{}, err
	}
	for _, rec := range doc.Records {
		if rec.ID == id {
			return rec, nil
		}
	}
	return subscriptionRecord{}, fmt.Errorf("subscription %q was not found", id)
}

func (rt *runtime) listSubscriptions() ([]subscriptionRecord, error) {
	doc, err := rt.loadSubscriptionRecords()
	if err != nil {
		return nil, err
	}
	return doc.Records, nil
}

func (rt *runtime) deleteSubscription(id string) error {
	doc, err := rt.loadSubscriptionRecords()
	if err != nil {
		return err
	}
	kept := doc.Records[:0]
	found := false
	for _, rec := range doc.Records {
		if rec.ID == id {
			found = true
			continue
		}
		kept = append(kept, rec)
	}
	if !found {
		return fmt.Errorf("subscription %q was not found", id)
	}
	doc.Records = kept
	return rt.saveSubscriptionRecords(doc)
}
