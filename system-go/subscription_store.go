package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	// subscriptionRecordsKey is the legacy single document that held every
	// definition before the store split (store_v2.go). It is read when the
	// index is absent and by migrate_store, rewritten only for refresh
	// bookkeeping on a store that has not migrated yet and once more to mark
	// it migrated, and kept until S2 deletes it.
	subscriptionRecordsKey = "subscriptions-v1"
	// maxSubscriptionRecords bounds how many live definitions a save may
	// create. The legacy document never held more, and a migrated store may
	// hold more only because migration carries whatever that document held.
	maxSubscriptionRecords = 256
	// maxSubscriptionDocBytes is the most a legacy document could be written
	// at. The reader allows maxLegacyDocSlackBytes on top, which is room for
	// the refresh bookkeeping this runtime still writes into it before the
	// migration and the one marker the migration writes after it.
	maxSubscriptionDocBytes = 1 << 20
	maxLegacyDocSlackBytes  = 64 << 10
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
	// recordSchemaVersion is what every record written to the split store
	// carries. Version 2 means a script file's program is in Content; a
	// version 1 record kept it under subscription-script-v1-<id>.
	recordSchemaVersion = 2
)

type subscriptionRecordsDocument struct {
	Version int                  `json:"version"`
	Records []subscriptionRecord `json:"records"`
	// MigratedTo is set by migrate_store once the split store holds every
	// record and the migration verified it. A reader then treats the
	// document as absent: it is kept for one more release, and without the
	// mark a record deleted after the migration would come back out of it.
	MigratedTo string `json:"migrated_to,omitempty"`
}

// subscriptionRecord is one definition. Target is the client family the engine
// produces for; the core's format only decides how that output is encoded, so
// the two are independent and both are needed.
//
// Two kinds share this type, following the model the Sub-Store front end uses:
// a SUB is one source of nodes, and a COLLECTION combines several subs. They
// share one type with a discriminator rather than two, because every consumer
// (list, render, share) wants them together and the shapes differ by only a
// few fields.
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
	// File-only fields follow.
	// FileType is "config", "plain" or "script". Empty means config.
	FileType string `json:"file_type,omitempty"`
	// QueryParams are the URL parameters a script file lets reach `$options`.
	// A share URL is public, so its query is attacker-controlled: only the names
	// the operator listed here get through, and everything else is dropped.
	QueryParams []string `json:"query_params,omitempty"`
	// Arguments is `$arguments`: settings the operator stores with the file
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
	// engine sees them; deleting a step to turn it off loses the work.
	Process []json.RawMessage `json:"process,omitempty"`
	// Operators is the pre-collections field name. It is read on load and
	// written back as Process; the accessor below is the only place that
	// knows both spellings.
	Operators []json.RawMessage `json:"operators,omitempty"`
	// Origin is set on an imported record and holds the source object verbatim,
	// so a migration cannot lose a field this plugin does not yet understand.
	Origin *migratedOrigin `json:"origin,omitempty"`
	// MigratedFrom is set by migrate_record on the fleet record it builds
	// from a legacy one (store_record_model.go, plan section 2.1).
	MigratedFrom *legacyOrigin `json:"migrated_from,omitempty"`
	// Fetch bookkeeping. On the split store it lives in the index entry
	// (store_index.go), where list reads it in one key, and a record document
	// carries none of it; a legacy record carries it here, and migrate_store
	// moves it into the index. LastFetchAt is RFC3339, LastFetchOK says how
	// that fetch went, LastError is the trimmed reason when it failed, and
	// Userinfo is the provider's subscription-userinfo header verbatim.
	LastFetchAt string `json:"last_fetch_at,omitempty"`
	LastFetchOK bool   `json:"last_fetch_ok,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	Userinfo    string `json:"userinfo,omitempty"`
	// ScriptDigest fingerprints a script file's program.
	//
	// The legacy store kept the program under its own key, so the revision
	// could not cover it without a second read; the digest stood in for it.
	// The split store keeps the program in Content, but the revision still
	// hashes the digest rather than the program, so a record's revision is the
	// same before and after migrate_store and an editor holding it is not told
	// its record changed when only its storage did. Empty on every record that
	// is not a script file.
	ScriptDigest string `json:"script_digest,omitempty"`
	// Revision fingerprints the operator-editable content of this record, so a
	// save can be refused when the stored copy has moved since it was read.
	//
	// Derived, never trusted from a caller, and recomputed at every read and
	// every write: the stored value is a cache, and correctness does not depend
	// on it. That is what lets this exist without a migration, because a record
	// written before this field gets a correct revision the first time anything
	// reads it.
	//
	// A counter was the obvious alternative and is wrong here. The fetch path
	// writes LastFetchAt, LastFetchOK, LastError and Userinfo on a schedule
	// nobody triggers, and a counter would bump on every one of those, so an
	// operator who opened a record and typed for a minute would be told their
	// edit conflicted with a refresh that changed nothing they can see. A
	// fingerprint over content only stays still through all of that and moves
	// exactly when someone changes something the operator would have to
	// reconcile. The excluded fields are listed in subscriptionRevision.
	Revision string `json:"revision,omitempty"`
}

// subscriptionRevision fingerprints a record's operator-editable content.
//
// Excluded, deliberately: the four fetch-bookkeeping fields, because they are
// written by a background refresh and are preserved across an edit anyway, so
// including them would manufacture conflicts out of events the operator did
// not cause and cannot act on. Origin is excluded for the same reason, being
// server-owned and preserved on save. Revision itself is zeroed so the
// fingerprint is a function of content alone rather than of its own previous
// value. SchemaVersion and a script file's Content describe where the record
// is stored, not what it says (ScriptDigest stands in for the program), so a
// record migrated from the legacy document keeps the revision it had there.
//
// Marshalling is deterministic: encoding/json writes struct fields in
// declaration order and map keys sorted, so the same content always hashes the
// same way. A hash rather than a counter also means two writes that produce
// identical content leave the revision alone, which is the honest answer.
func subscriptionRevision(rec subscriptionRecord) string {
	rec.LastFetchAt, rec.LastFetchOK = "", false
	rec.LastError, rec.Userinfo = "", ""
	rec.Origin = nil
	rec.Revision = ""
	rec.SchemaVersion = 0
	if isScriptFile(rec) {
		rec.Content = ""
	}
	encoded, err := json.Marshal(rec)
	if err != nil {
		// A record that cannot be marshalled cannot be stored either, so the
		// save will fail on its own. Returning empty means "no revision", which
		// every comparison below treats as unknown rather than as a match.
		return ""
	}
	return digestOf(string(encoded))
}

// digestOf is the one fingerprint function: 16 bytes of SHA-256, hex. Short
// enough to sit in a record and a payload without being noise, long enough that
// a collision is not a thing that happens.
func digestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:16])
}

// withRevision returns the record with its fingerprint recomputed. Every path
// that hands a record outward goes through here, so a caller can never be given
// a stale revision to send back.
func withRevision(rec subscriptionRecord) subscriptionRecord {
	rec.Revision = ""
	rec.Revision = subscriptionRevision(rec)
	return rec
}

// loadSubscriptionRecords reads the legacy document. Absent is an empty
// version 1 document; found reports which.
func (rt *runtime) loadSubscriptionRecords() (subscriptionRecordsDocument, bool, error) {
	value, found, err := rt.kvGet(subscriptionRecordsKey)
	if err != nil || !found {
		return subscriptionRecordsDocument{Version: 1}, false, err
	}
	if len(value) > maxSubscriptionDocBytes+maxLegacyDocSlackBytes {
		return subscriptionRecordsDocument{}, true, fmt.Errorf("subscription records exceed %d bytes", maxSubscriptionDocBytes+maxLegacyDocSlackBytes)
	}
	var doc subscriptionRecordsDocument
	if err := json.Unmarshal(value, &doc); err != nil {
		return subscriptionRecordsDocument{}, true, fmt.Errorf("decode subscription records: %w", err)
	}
	if doc.Version == 0 {
		doc.Version = 1
	}
	return doc, true, nil
}

// saveSubscriptionRecords rewrites the legacy document. It is no save path:
// it carries refresh bookkeeping on a store that has not migrated and the
// migration's mark, so it bounds bytes and never the record count, which a
// legacy document past the save cap still has to keep.
func (rt *runtime) saveSubscriptionRecords(doc subscriptionRecordsDocument) error {
	sort.Slice(doc.Records, func(i, j int) bool { return doc.Records[i].ID < doc.Records[j].ID })
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if len(raw) > maxSubscriptionDocBytes+maxLegacyDocSlackBytes {
		return fmt.Errorf("subscription records are too large: %d bytes, limit %d", len(raw), maxSubscriptionDocBytes+maxLegacyDocSlackBytes)
	}
	return rt.kvPut(subscriptionRecordsKey, raw)
}

// normalizeSubscriptionForStore validates one record and returns its stored
// shape. Pure: no reads, no writes, so the batch import path can normalize N
// records before it spends a single host call.
func normalizeSubscriptionForStore(rec subscriptionRecord) (subscriptionRecord, error) {
	if strings.TrimSpace(rec.ID) == "" {
		return rec, fmt.Errorf("subscription id is required")
	}
	if err := validStoreID(rec.ID); err != nil {
		return rec, err
	}
	if len(rec.Content) > maxSubscriptionInlineBytes {
		return rec, fmt.Errorf("subscription %q inline content is too large: %d bytes, limit %d", rec.ID, len(rec.Content), maxSubscriptionInlineBytes)
	}
	// Records written before collections existed spell the chain `operators`.
	// Normalise on the way in so exactly one field is authoritative in storage.
	if len(rec.Process) == 0 && len(rec.Operators) > 0 {
		rec.Process = rec.Operators
	}
	rec.Operators = nil
	if err := validateProcess(rec.Process); err != nil {
		return rec, fmt.Errorf("subscription %q: %w", rec.ID, err)
	}
	switch recordKind(rec) {
	case kindFile:
		// A file has a template and, optionally, a node source. Membership and
		// the vpn-core identity belong to other kinds; storing them would leave
		// two answers to "where does this get its content".
		if strings.TrimSpace(rec.URL) == "" && strings.TrimSpace(rec.Content) == "" {
			return rec, fmt.Errorf("file %q needs a template: a URL to fetch, or content", rec.ID)
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
			// The program is the file and stays in Content. Its digest is what
			// the revision hashes (see ScriptDigest).
			rec.ScriptDigest = digestOf(rec.Content)
		default:
			rec.QueryParams, rec.Arguments = nil, nil
		}
	case kindCollection:
		// A collection with neither members nor tags gathers nothing, and would
		// fail only when someone fetched its URL.
		if len(rec.Members) == 0 && len(rec.MemberTags) == 0 {
			return rec, fmt.Errorf("collection %q must name at least one subscription or tag", rec.ID)
		}
		rec.Kind = kindCollection
		rec.Source, rec.URL, rec.Content, rec.VPNIdentity, rec.UA, rec.GraphOptionsVersion = "", "", "", "", "", ""
		rec.EntryRoots = nil
		rec.FileType, rec.NodeSource, rec.Download = "", "", false
	default:
		// A sub with no source is allowed to exist. Requiring one here would
		// reject legitimate intermediate states (a record arriving mid-import,
		// or one an operator is still filling in), and render already refuses
		// to serve a subscription with nothing in it, which is where the
		// failure actually matters. The editor asks for a source; the store
		// does not insist on one.
		rec.Kind = ""
		rec.Members, rec.MemberTags = nil, nil
		rec.FileType, rec.NodeSource, rec.Download = "", "", false
		if rec.Source == subscriptionSourceVPNCoreGraph {
			if err := validateVPNCoreGraphConfig(rec.VPNIdentity, rec.EntryRoots); err != nil {
				return rec, fmt.Errorf("subscription %q: %w", rec.ID, err)
			}
			if !validVPNCoreGraphOptionsVersion(rec.GraphOptionsVersion) {
				return rec, fmt.Errorf("subscription %q: graph options version is invalid", rec.ID)
			}
			rec.URL, rec.Content, rec.UA = "", "", ""
		} else {
			rec.EntryRoots = nil
			rec.GraphOptionsVersion = ""
		}
	}
	rec.SchemaVersion = recordSchemaVersion
	// A record that stopped being a script file must not keep the digest of the
	// program it used to have: the revision is a claim about current content.
	if !isScriptFile(rec) {
		rec.ScriptDigest = ""
	}
	// Stamped last, after every field this function normalises, so the
	// fingerprint describes what is actually stored rather than what arrived.
	// A caller-supplied Revision never survives: it is zeroed and recomputed.
	return withRevision(rec), nil
}
