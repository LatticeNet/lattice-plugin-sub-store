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
	// subscriptionSourceVPNCore marks a subscription whose content is the live
	// vpn-core node export rather than a provider URL or pasted text. Refreshing
	// one re-reads the export, so nodes added or removed in vpn-core reach
	// clients without anyone re-pasting anything.
	subscriptionSourceVPNCore = "vpn-core"
	// subscriptionSourceVPNCoreGraph composes one identity through ordered,
	// committed line-chain roots. It is deliberately distinct from vpn-core:
	// the legacy source exports every eligible node and remains compatible.
	subscriptionSourceVPNCoreGraph = "vpn-core-graph"
	// The other two sources, named explicitly so a record says where its content
	// comes from instead of leaving it to be inferred from which field happens
	// to be populated. A record written before these existed has an empty
	// source and still resolves url-then-content, which is what it always did.
	subscriptionSourceRemote = "remote"
	subscriptionSourceLocal  = "local"
	// How a collection reacts when one of its members cannot be fetched.
	// Upstream makes this a choice rather than a rule, and it genuinely is one:
	// strict protects a client from silently losing nodes, while skipping keeps
	// the rest of a large collection serving when one provider is down. The
	// default is strict, because the failure it prevents is destructive.
	failureModeStrict = "strict"
	failureModeSkip   = "skip-failed"
	// A file's two shapes. A config carries a client configuration whose
	// `proxies` key is filled from a node source; plain text is served as it
	// is, after its script operations run.
	fileTypeConfig = "config"
	fileTypePlain  = "plain"
)

type subscriptionRecordsDocument struct {
	Version int                  `json:"version"`
	Records []subscriptionRecord `json:"records"`
}

// subscriptionRecord is one definition. Target is the client family the engine
// produces for; the core's format only decides how that output is encoded, so
// the two are independent and both are needed.
//
// Two kinds share this type, following the model the Sub-Store front end uses:
// a SUB is one source of nodes, and a COLLECTION combines several subs. They
// live in one document with a discriminator rather than in two stores, because
// every consumer — list, render, share — wants them together and the shapes
// differ by only a few fields.
type subscriptionRecord struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	// Kind is "sub" or "collection". Empty means "sub": records written before
	// collections existed are subs, and rewriting them to say so would be a
	// migration with nothing to gain.
	Kind        string   `json:"kind,omitempty"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name,omitempty"`
	Remark      string   `json:"remark,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	URL         string   `json:"url,omitempty"`
	Content     string   `json:"content,omitempty"`
	// Source names where content comes from when it is neither a provider URL
	// nor pasted inline. Empty keeps the original two-source behaviour.
	//
	// This exists because a deployment whose nodes live in vpn-core previously
	// could not serve them natively at all: the export was reachable, but only
	// by the outbound push to an external Sub-Store. The subscription platform
	// had no path to it, which made the native platform useless for exactly the
	// setup it was built to replace.
	Source string `json:"source,omitempty"`
	// VPNIdentity narrows a vpn-core export to one identity. Empty means every
	// eligible identity, which is what the export returns by default.
	VPNIdentity string `json:"vpn_identity,omitempty"`
	// EntryRoots is the authoritative order for a graph subscription. It is
	// accepted only for vpn-core-graph records; there is no free-text fallback.
	EntryRoots []string `json:"entry_roots,omitempty"`
	// GraphOptionsVersion proves the UI selected the identity and roots from one
	// authoritative options projection. The mutation handler revalidates it
	// immediately before storage; compose remains authoritative at use time.
	GraphOptionsVersion string `json:"graph_options_version,omitempty"`
	UA                  string `json:"ua,omitempty"`
	// Members and MemberTags are the collection's inputs: explicit sub ids, plus
	// every sub carrying one of these tags. Tags exist so a collection can be
	// "everything tagged home" and pick up a new sub without being edited.
	Members    []string `json:"members,omitempty"`
	MemberTags []string `json:"member_tags,omitempty"`
	Target     string   `json:"target,omitempty"`
	// FailureMode applies to collections. Empty means strict.
	FailureMode string `json:"failure_mode,omitempty"`
	// ── file-only ─────────────────────────────────────────────────────────
	// FileType is "config", "plain" or "script". Empty means config.
	FileType string `json:"file_type,omitempty"`
	// QueryParams are the URL parameters a script file lets reach `$options`.
	// A share URL is public, so its query is attacker-controlled: only the names
	// the operator listed here get through, and everything else is dropped.
	QueryParams []string `json:"query_params,omitempty"`
	// Arguments is `$arguments` — settings the operator stores with the file
	// rather than passing on the URL.
	Arguments map[string]string `json:"arguments,omitempty"`
	// NodeSource names the subscription or combination whose nodes fill the
	// template's `proxies`. Empty serves the template untouched, which is how
	// a file holding rules or a script is expressed.
	NodeSource string `json:"node_source,omitempty"`
	// Download asks the core to serve this with a filename rather than inline.
	Download bool `json:"download,omitempty"`
	// Process is the ordered operator chain. Entries are kept as raw JSON for
	// the same reason Origin is: an entry carries fields this plugin does not
	// interpret (customName, id, and whatever upstream adds next), and a
	// round trip through a typed struct would drop them silently.
	//
	// A step may be `disabled`, which is why this is not simply the operator
	// list. Disabled steps are stored, shown, and filtered out before the
	// engine sees them — deleting a step to turn it off loses the work.
	Process []json.RawMessage `json:"process,omitempty"`
	// Operators is the pre-collections field name. It is read on load and
	// written back as Process; the accessor below is the only place that
	// knows both spellings.
	Operators []json.RawMessage `json:"operators,omitempty"`
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
	script := ""
	splitScript := false
	// Records written before collections existed spell the chain `operators`.
	// Normalise on the way in so exactly one field is authoritative in storage.
	if len(rec.Process) == 0 && len(rec.Operators) > 0 {
		rec.Process = rec.Operators
	}
	rec.Operators = nil
	if err := validateProcess(rec.Process); err != nil {
		return fmt.Errorf("subscription %q: %w", rec.ID, err)
	}
	switch recordKind(rec) {
	case kindFile:
		// A file has a template and, optionally, a node source. Membership and
		// the vpn-core identity belong to other kinds; storing them would leave
		// two answers to "where does this get its content".
		if strings.TrimSpace(rec.URL) == "" && strings.TrimSpace(rec.Content) == "" {
			return fmt.Errorf("file %q needs a template: a URL to fetch, or content", rec.ID)
		}
		rec.Kind = kindFile
		rec.Members, rec.MemberTags = nil, nil
		rec.VPNIdentity, rec.Target, rec.FailureMode, rec.GraphOptionsVersion = "", "", "", ""
		rec.EntryRoots = nil
		switch fileType(rec) {
		case fileTypePlain:
			// Plain text has no proxy list to fill, so a node source on it would
			// be a stored setting that silently does nothing.
			rec.NodeSource = ""
			rec.QueryParams, rec.Arguments = nil, nil
		case fileTypeScript:
			// The program is the file, and it is large. It goes to its own key so
			// the record document does not carry it; the record keeps a marker
			// that says the content lives elsewhere rather than an empty string
			// that would read as "this file has nothing in it".
			script = rec.Content
			rec.Content = ""
			splitScript = true
		default:
			rec.QueryParams, rec.Arguments = nil, nil
		}
	case kindCollection:
		// A collection with neither members nor tags gathers nothing, and would
		// fail only when someone fetched its URL.
		if len(rec.Members) == 0 && len(rec.MemberTags) == 0 {
			return fmt.Errorf("collection %q must name at least one subscription or tag", rec.ID)
		}
		rec.Kind = kindCollection
		rec.Source, rec.URL, rec.Content, rec.VPNIdentity, rec.UA, rec.GraphOptionsVersion = "", "", "", "", "", ""
		rec.EntryRoots = nil
		rec.FileType, rec.NodeSource, rec.Download = "", "", false
	default:
		// A sub with no source is allowed to exist. Requiring one here would
		// reject legitimate intermediate states — a record arriving mid-import,
		// or one an operator is still filling in — and render already refuses to
		// serve a subscription with nothing in it, which is where the failure
		// actually matters. The editor asks for a source; the store does not
		// insist on one.
		rec.Kind = ""
		rec.Members, rec.MemberTags = nil, nil
		rec.FileType, rec.NodeSource, rec.Download = "", "", false
		if rec.Source == subscriptionSourceVPNCoreGraph {
			if err := validateVPNCoreGraphConfig(rec.VPNIdentity, rec.EntryRoots); err != nil {
				return fmt.Errorf("subscription %q: %w", rec.ID, err)
			}
			if !validVPNCoreGraphOptionsVersion(rec.GraphOptionsVersion) {
				return fmt.Errorf("subscription %q: graph options version is invalid", rec.ID)
			}
			rec.URL, rec.Content, rec.UA = "", "", ""
		} else {
			rec.EntryRoots = nil
			rec.GraphOptionsVersion = ""
		}
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
	// The program is written first. If the record write then fails the script is
	// an orphan under a key nothing reads, which costs a bounded amount of space;
	// the other order would leave a saved script file whose program is missing.
	if splitScript {
		if err := rt.putFileScript(rec.ID, script); err != nil {
			return err
		}
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
			// A script file's program is stored separately. Reattaching it here
			// keeps the split invisible to every caller: edit, render and preview
			// all see one record with its content in it, the same as any other
			// kind. `list` deliberately does not go through this path — a
			// management view must not drag a dozen programs with it.
			if isScriptFile(rec) {
				script, err := rt.getFileScript(id)
				if err != nil {
					return subscriptionRecord{}, err
				}
				rec.Content = script
			}
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
	if err := rt.saveSubscriptionRecords(doc); err != nil {
		return err
	}
	// Best effort: the record is already gone, so failing here would report a
	// deletion that did happen as an error. The cost of the miss is one empty
	// key.
	_ = rt.clearFileScript(id)
	return nil
}
