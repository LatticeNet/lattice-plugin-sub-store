package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
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
	// ── fetch bookkeeping ───────────────────────────────────────────────────
	// Written by the fetch path and preserved across edits (an edit replaces the
	// record wholesale, so without preservation every save would claim a fetched
	// record was never refreshed). LastFetchAt is RFC3339, LastFetchOK says how
	// that fetch went, LastError is the trimmed reason when it failed, and
	// Userinfo is the provider's subscription-userinfo header verbatim — the
	// traffic figures a client shows as remaining quota. All four are absent on
	// records written before this existed, which reads as "never fetched".
	LastFetchAt string `json:"last_fetch_at,omitempty"`
	LastFetchOK bool   `json:"last_fetch_ok,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	Userinfo    string `json:"userinfo,omitempty"`
	// ScriptDigest fingerprints a script file's program, which is stored under
	// its own key and is therefore not part of this record's own bytes.
	//
	// It exists so Revision can cover the program without reading it. The save
	// path is budgeted at three host round trips (load the document, write the
	// program, write the document) and fetching the previous program to compare
	// it would need a fourth, so the digest is written alongside the program and
	// travels with the record instead. Empty on every record that is not a
	// script file.
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
// value.
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
	// Content is deliberately left in. For a script file the stored record's
	// Content is empty and ScriptDigest stands in for the program, so both
	// shapes are covered by the one hash.
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

// normalizeSubscriptionForStore validates one record and returns its stored
// shape, plus the program lifted out of a script file ("" for everything
// else). Pure: no reads, no writes — so the batch import path can normalize N
// records and pay one document write for all of them.
func normalizeSubscriptionForStore(rec subscriptionRecord) (subscriptionRecord, string, error) {
	if strings.TrimSpace(rec.ID) == "" {
		return rec, "", fmt.Errorf("subscription id is required")
	}
	if len(rec.Content) > maxSubscriptionInlineBytes {
		return rec, "", fmt.Errorf("subscription %q inline content is too large: %d bytes, limit %d", rec.ID, len(rec.Content), maxSubscriptionInlineBytes)
	}
	script := ""
	// Records written before collections existed spell the chain `operators`.
	// Normalise on the way in so exactly one field is authoritative in storage.
	if len(rec.Process) == 0 && len(rec.Operators) > 0 {
		rec.Process = rec.Operators
	}
	rec.Operators = nil
	if err := validateProcess(rec.Process); err != nil {
		return rec, "", fmt.Errorf("subscription %q: %w", rec.ID, err)
	}
	switch recordKind(rec) {
	case kindFile:
		// A file has a template and, optionally, a node source. Membership and
		// the vpn-core identity belong to other kinds; storing them would leave
		// two answers to "where does this get its content".
		if strings.TrimSpace(rec.URL) == "" && strings.TrimSpace(rec.Content) == "" {
			return rec, "", fmt.Errorf("file %q needs a template: a URL to fetch, or content", rec.ID)
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
			rec.ScriptDigest = digestOf(script)
		default:
			rec.QueryParams, rec.Arguments = nil, nil
		}
	case kindCollection:
		// A collection with neither members nor tags gathers nothing, and would
		// fail only when someone fetched its URL.
		if len(rec.Members) == 0 && len(rec.MemberTags) == 0 {
			return rec, "", fmt.Errorf("collection %q must name at least one subscription or tag", rec.ID)
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
				return rec, "", fmt.Errorf("subscription %q: %w", rec.ID, err)
			}
			if !validVPNCoreGraphOptionsVersion(rec.GraphOptionsVersion) {
				return rec, "", fmt.Errorf("subscription %q: graph options version is invalid", rec.ID)
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
	// A record that stopped being a script file must not keep the digest of the
	// program it used to have: the revision is a claim about current content.
	if !isScriptFile(rec) {
		rec.ScriptDigest = ""
	}
	// Stamped last, after every field this function normalises, so the
	// fingerprint describes what is actually stored rather than what arrived.
	// A caller-supplied Revision never survives: it is zeroed and recomputed.
	rec = withRevision(rec)
	return rec, script, nil
}

// mergeRecordIntoDoc inserts or replaces one record by id.
func mergeRecordIntoDoc(doc *subscriptionRecordsDocument, rec subscriptionRecord) {
	for i := range doc.Records {
		if doc.Records[i].ID == rec.ID {
			doc.Records[i] = rec
			return
		}
	}
	doc.Records = append(doc.Records, rec)
}

// saveSubscriptionInDoc merges one record into an already-loaded document and
// persists it. The caller loads the document, so a dispatch that needs the
// record's previous state — provenance, fetch bookkeeping — reads it from the
// same copy instead of paying a second host round trip. The runner bills every
// round trip against the signed host_calls budget, and the save path's
// read-modify-readback used to cost up to seven for one record.
//
// The program is written before the document. If the document write then fails
// the script is an orphan under a key nothing reads, which costs a bounded
// amount of space; the other order would leave a saved script file whose
// program is missing. The returned record carries its content again, so the
// caller can answer with what was written without a read-back.
func (rt *runtime) saveSubscriptionInDoc(doc *subscriptionRecordsDocument, rec subscriptionRecord, wasScript bool) (subscriptionRecord, error) {
	nrec, script, err := normalizeSubscriptionForStore(rec)
	if err != nil {
		return rec, err
	}
	mergeRecordIntoDoc(doc, nrec)
	if script != "" {
		if err := rt.putFileScript(nrec.ID, script); err != nil {
			return rec, err
		}
	}
	if err := rt.saveSubscriptionRecords(*doc); err != nil {
		return rec, err
	}
	// A record that used to be a script and is now anything else leaves its
	// program key behind unless it is cleared here. The key is bounded, but the
	// host serialises KV into the state file on every write, so an orphan taxes
	// every unrelated write forever. Best effort: the record is already saved,
	// and a missed clear is the leak this used to have, not a new failure.
	if wasScript && script == "" {
		_ = rt.clearFileScript(nrec.ID)
	}
	if script != "" {
		nrec.Content = script
	}
	return nrec, nil
}

// saveSubscription inserts or replaces one definition.
func (rt *runtime) saveSubscription(rec subscriptionRecord) error {
	doc, err := rt.loadSubscriptionRecords()
	if err != nil {
		return err
	}
	wasScript := false
	for _, existing := range doc.Records {
		if existing.ID == rec.ID {
			wasScript = isScriptFile(existing)
			break
		}
	}
	_, err = rt.saveSubscriptionInDoc(&doc, rec, wasScript)
	return err
}

// saveSubscriptionBatch persists many definitions with ONE document write.
//
// The plugin-call budget charges every host round trip: a per-record save
// costs a read and a write each, so importing twenty records (sixteen of them
// script files, each with its own program key) blew the signed host_calls
// budget and the import died mid-way with a 502. Here the document is loaded
// once, every record is normalized and merged in memory, the program keys go
// out together, and one document write finishes the batch.
//
// Records are normalized exactly once, inside. A record that fails validation
// is reported in the skipped map and does not fail the batch; a store-level
// failure (oversize document, KV error) fails it.
func (rt *runtime) saveSubscriptionBatch(recs []subscriptionRecord) (map[string]string, error) {
	skipped := map[string]string{}
	if len(recs) == 0 {
		return skipped, nil
	}
	doc, err := rt.loadSubscriptionRecords()
	if err != nil {
		return skipped, err
	}
	type program struct{ id, body string }
	var programs []program
	for _, rec := range recs {
		nrec, script, err := normalizeSubscriptionForStore(rec)
		if err != nil {
			skipped[rec.ID] = err.Error()
			continue
		}
		mergeRecordIntoDoc(&doc, nrec)
		if script != "" {
			programs = append(programs, program{nrec.ID, script})
		}
	}
	// Reject before spending any program write: an oversize merge fails the
	// document write below regardless, and paying 2+N host calls first would
	// blow the import budget on a batch that cannot land, leaving N orphan
	// program keys behind the exact 502 this path exists to prevent.
	if len(doc.Records) > maxSubscriptionRecords {
		return skipped, fmt.Errorf("too many subscriptions: %d, limit %d", len(doc.Records), maxSubscriptionRecords)
	}
	for _, p := range programs {
		if err := rt.putFileScript(p.id, p.body); err != nil {
			return skipped, err
		}
	}
	return skipped, rt.saveSubscriptionRecords(doc)
}

func (rt *runtime) getSubscription(id string) (subscriptionRecord, error) {
	doc, err := rt.loadSubscriptionRecords()
	if err != nil {
		return subscriptionRecord{}, err
	}
	for _, rec := range doc.Records {
		if rec.ID == id {
			// Recomputed rather than read out of storage, so a record written
			// before this field existed still reports a revision, and so the
			// value a caller is handed cannot be stale. It is computed on the
			// STORED shape, before the program is reattached below, because that
			// is the shape the save path compares against.
			rec = withRevision(rec)
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
	wasScript := false
	for _, rec := range doc.Records {
		if rec.ID == id {
			found = true
			wasScript = isScriptFile(rec)
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
	// Only a script file has a program key to clear. Clearing unconditionally
	// billed every delete a third host round trip against a budget sized for
	// two, so deleting a plain subscription 502'd in production. Best effort:
	// the record is already gone, so failing here would report a deletion that
	// did happen as an error; the cost of the miss is one empty key.
	if wasScript {
		_ = rt.clearFileScript(id)
	}
	return nil
}

// maxFetchErrorBytes bounds the failure reason stored on a record. A provider
// can answer with a whole error page, and the records document is rewritten in
// full on every save — an unbounded reason would tax every unrelated write.
const maxFetchErrorBytes = 240

// noteFetchOutcome records when a fetch ran and how it went. It is called from
// the fetch method — which the core invokes for every refresh, scheduled or
// manual — and deliberately NOT from fetchSubscription itself: render and
// preview fetch too, and a write per read would rewrite the records document
// on every public request. A preview fetch is also not a refresh — recording
// it would tell the operator the served snapshot is fresher than it is.
//
// A failed bookkeeping write is swallowed on purpose: the fetch's own result is
// already being reported, and losing the note must not turn a good refresh into
// an error.
func (rt *runtime) noteFetchOutcome(subscriptionID string, fetchedAt time.Time, userinfo string, fetchErr error) {
	doc, err := rt.loadSubscriptionRecords()
	if err != nil {
		return
	}
	for i := range doc.Records {
		if doc.Records[i].ID != subscriptionID {
			continue
		}
		doc.Records[i].LastFetchAt = fetchedAt.UTC().Format(time.RFC3339)
		if fetchErr != nil {
			doc.Records[i].LastFetchOK = false
			doc.Records[i].LastError = trimFetchError(fetchErr)
		} else {
			doc.Records[i].LastFetchOK = true
			doc.Records[i].LastError = ""
			// A failure keeps the previous userinfo: it is the provider's quota
			// figures, and a stale figure next to a "refresh failed" badge beats
			// none at all.
			if userinfo != "" {
				doc.Records[i].Userinfo = userinfo
			}
		}
		_ = rt.saveSubscriptionRecords(doc)
		return
	}
}

func trimFetchError(err error) string {
	text := strings.TrimSpace(err.Error())
	if len(text) > maxFetchErrorBytes {
		text = strings.TrimSpace(text[:maxFetchErrorBytes]) + "…"
	}
	return text
}
