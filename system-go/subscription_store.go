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
	// subscriptionSourceFleet is an identity-free structured selection over
	// the line catalogue (design 28, S2 plan section 2.1). The selection is
	// the record's leading run of Structured Filter steps; the record carries
	// no identity, no URL and no content, and it renders to a selection plan
	// that core binds per share identity.
	subscriptionSourceFleet = "fleet"
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
	// Fleet holds the per-record opt-ins of a fleet record, or of a collection
	// that gathers one (S2 plan section 2.1). The options that apply to a
	// share are those of the record the share names, never a member's.
	Fleet *fleetOptions `json:"fleet,omitempty"`
	// External is the H3 opt-in (parse.Options.AllowExternal): a local
	// record may keep exec-shaped nodes. Kept on local records only; a fleet
	// record never emits one.
	External bool `json:"external,omitempty"`
	// MigratedFrom is set by migrate_record on the fleet record it stages from
	// a legacy vpn-core or vpn-core-graph record. The write path preserves it
	// from the stored staged or live record, as it preserves Origin, so an
	// edit of the staged migrated record keeps it. Unlike Origin it is part
	// of the revision: a revision names fixed content, provenance included.
	MigratedFrom *legacyOrigin `json:"migrated_from,omitempty"`
	UA           string        `json:"ua,omitempty"`
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
	// An unknown source used to be saved and then treated as a remote source
	// with no URL; a source this plugin does not know is refused instead, so
	// no fetch or render path meets one.
	if !knownSubscriptionSource(rec.Source) {
		return rec, fmt.Errorf("subscription %q: source %q is not one of vpn-core, vpn-core-graph, remote, local or fleet", rec.ID, rec.Source)
	}
	if err := validateFleetOptions(rec.Fleet); err != nil {
		return rec, fmt.Errorf("subscription %q: %w", rec.ID, err)
	}
	if recordKind(rec) != kindSub || rec.Source != subscriptionSourceLocal {
		rec.External = false
	}
	switch recordKind(rec) {
	case kindFile:
		// A file has a template and, optionally, a node source. Membership and
		// the vpn-core identity belong to other kinds; storing them would leave
		// two answers to "where does this get its content".
		if strings.TrimSpace(rec.URL) == "" && strings.TrimSpace(rec.Content) == "" {
			return rec, fmt.Errorf("file %q needs a template: a URL to fetch, or content", rec.ID)
		}
		// A file reads nodes through its node source; the fleet is a source
		// of subscriptions, and a fleet-bound file does not exist before S3.
		if rec.Source == subscriptionSourceFleet {
			return rec, fmt.Errorf("file %q cannot take the fleet source; name a fleet subscription as its node source instead", rec.ID)
		}
		rec.Kind = kindFile
		rec.Fleet, rec.MigratedFrom = nil, nil
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
		// Fleet stays: whether the collection gathers a fleet record is a
		// fact about the other records, which applyFleetWriteRules decides
		// with the index in hand. MigratedFrom belongs to subs.
		rec.MigratedFrom = nil
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
		if rec.Source != subscriptionSourceFleet {
			rec.Fleet = nil
		}
		switch rec.Source {
		case subscriptionSourceFleet:
			// The selection is the record's leading Structured Filter run; a
			// fleet record carries no identity, no provider and no content,
			// so nothing on it can name an owner credential or a host.
			rec.URL, rec.Content, rec.UA, rec.VPNIdentity, rec.GraphOptionsVersion = "", "", "", "", ""
			rec.EntryRoots = nil
		case subscriptionSourceVPNCoreGraph:
			if err := validateVPNCoreGraphConfig(rec.VPNIdentity, rec.EntryRoots); err != nil {
				return rec, fmt.Errorf("subscription %q: %w", rec.ID, err)
			}
			if !validVPNCoreGraphOptionsVersion(rec.GraphOptionsVersion) {
				return rec, fmt.Errorf("subscription %q: graph options version is invalid", rec.ID)
			}
			rec.URL, rec.Content, rec.UA = "", "", ""
		default:
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

// fleetOptions are the per-record opt-ins design 28 names. Every field is
// off by default and a zero value is omitted on the wire. They are kept on
// fleet records and on collections that gather a fleet record; the options
// that apply to a share are those of the record the share names, never a
// member's (S2 plan section 1.2), so a collection's plan carries one policy
// with no merge rule.
type fleetOptions struct {
	// DDNSDial substitutes a line's dial address with a verified DDNS name.
	// Never sni, host or path. A NAT line (template host equals the provider
	// edge) takes only a verified name whose Target is that edge.
	DDNSDial bool `json:"ddns_dial,omitempty"`
	// ProbeExclusion asks core to exclude a line whose last probe failed
	// ConsecutiveFailures times in a row (1 to 10, default 3 when enabled).
	ProbeExclusion *probeExclusion `json:"probe_exclusion,omitempty"`
	// UsageExclusion asks core to exclude a line on which the share's
	// identity used more than MaxBytesPerLine in the current period.
	UsageExclusion *usageExclusion `json:"usage_exclusion,omitempty"`
}

type probeExclusion struct {
	Enabled             bool `json:"enabled"`
	ConsecutiveFailures int  `json:"consecutive_failures,omitempty"`
}

type usageExclusion struct {
	Enabled         bool  `json:"enabled"`
	MaxBytesPerLine int64 `json:"max_bytes_per_line"`
}

// legacyOrigin is what migrate_record kept of the legacy record it replaced.
type legacyOrigin struct {
	Source              string   `json:"source"` // vpn-core or vpn-core-graph
	VPNIdentity         string   `json:"vpn_identity,omitempty"`
	EntryRoots          []string `json:"entry_roots,omitempty"`
	GraphOptionsVersion string   `json:"graph_options_version,omitempty"`
	MigratedAt          string   `json:"migrated_at"`
}

const (
	// defaultProbeConsecutiveFailures is the probe exclusion's threshold
	// when it is enabled without one.
	defaultProbeConsecutiveFailures = 3
	// maxProbeConsecutiveFailures bounds the probe exclusion's threshold.
	maxProbeConsecutiveFailures = 10
)

// knownSubscriptionSource reports whether source is one this plugin serves:
// the two legacy vpn-core sources, the two provider sources, the fleet, or
// none (a record written before sources were named).
func knownSubscriptionSource(source string) bool {
	switch source {
	case "", subscriptionSourceVPNCore, subscriptionSourceVPNCoreGraph,
		subscriptionSourceRemote, subscriptionSourceLocal, subscriptionSourceFleet:
		return true
	}
	return false
}

// validateFleetOptions refuses a probe threshold outside 1 to 10 and a usage
// threshold that is not positive. A disabled block keeps its values, so the
// editor can remember them, and is still range checked.
func validateFleetOptions(options *fleetOptions) error {
	if options == nil {
		return nil
	}
	if p := options.ProbeExclusion; p != nil && p.ConsecutiveFailures != 0 &&
		(p.ConsecutiveFailures < 1 || p.ConsecutiveFailures > maxProbeConsecutiveFailures) {
		return fmt.Errorf("probe exclusion needs 1 to %d consecutive failures, not %d", maxProbeConsecutiveFailures, p.ConsecutiveFailures)
	}
	if u := options.UsageExclusion; u != nil && (u.MaxBytesPerLine < 0 || (u.Enabled && u.MaxBytesPerLine == 0)) {
		return fmt.Errorf("usage exclusion needs a positive byte threshold per line")
	}
	return nil
}

// Refusal codes of the record rules (S2 plan sections 1.2, 1.4, 2.1, 2.6).
// A mutating method answers each as a structured refusal, never as an error,
// because the gateway replaces every mutation error with one fixed string.
const (
	codeFleetToLegacyRefused       = "fleet_to_legacy_refused"
	codeLegacySourceRetired        = "legacy_source_retired"
	codeFleetMixedOwnerCredentials = "fleet_mixed_owner_credentials"
	codeFleetFileUnavailable       = "fleet_file_unavailable"
	codeFleetScriptLinkUnavailable = "fleet_script_link_unavailable"
)

// fleetRuleError is a refusal of one of the record rules. Message is fixed
// text with record ids and codes interpolated and never a wrapped error, a
// URL or content, so it may travel to the browser in a structured refusal
// (refused.message); IDs, Files and Collections fill refused.ids,
// refused.files and refused.collections.
type fleetRuleError struct {
	Code        string
	IDs         []string
	Files       []string
	Collections []string
	Message     string
}

func (e *fleetRuleError) Error() string { return e.Code + ": " + e.Message }

// fleetRecordFacts is one other record as the write-time rules read it: its
// kind, its source, its tags and the edges it creates. Which state each fact
// comes from is the caller's choice and is fixed by S2 plan section 2.2: at
// save, import, migrate and restore each other record contributes its staged
// facts when it has a staged revision and its live facts otherwise, never
// both; at migrate_record and apply_revision the live facts alone.
type fleetRecordFacts struct {
	ID         string
	Kind       string
	Source     string
	Tags       []string
	Members    []string
	MemberTags []string
	NodeSource string
}

// factsOf is a record's own facts, in the same shape.
func factsOf(rec subscriptionRecord) fleetRecordFacts {
	return fleetRecordFacts{
		ID: rec.ID, Kind: recordKind(rec), Source: rec.Source, Tags: rec.Tags,
		Members: rec.Members, MemberTags: rec.MemberTags, NodeSource: rec.NodeSource,
	}
}

// fleetLiveFacts is the written record's own live state. The source
// transition rules read live state only (S2 plan section 2.1): a legacy
// record whose only fleet-ness is a staged migration is still the live
// legacy record.
type fleetLiveFacts struct {
	// Revision is the live revision; empty when the record has none (a new
	// id, a staged record never promoted, an archived record).
	Revision string
	// Source is the live revision's source.
	Source string
	// FleetBound is the live index entry's fleet_bound flag.
	FleetBound bool
}

// fleetWriteOrigin names the method a write comes from. Only save refuses a
// legacy source (legacy_source_retired): import, migrate and restore still
// write legacy records, because a backup must restore what it holds.
type fleetWriteOrigin string

const (
	fleetWriteSave          fleetWriteOrigin = "save"
	fleetWriteImport        fleetWriteOrigin = "import"
	fleetWriteMigrate       fleetWriteOrigin = "migrate"
	fleetWriteRestore       fleetWriteOrigin = "restore"
	fleetWriteMigrateRecord fleetWriteOrigin = "migrate_record"
	fleetWriteApplyRevision fleetWriteOrigin = "apply_revision"
)

// applyFleetWriteRules runs every record rule that reads the other records,
// for a record already normalised by normalizeSubscriptionForStore, and
// returns the record to store: a collection that gathers no fleet record
// loses its Fleet options and its revision is recomputed. The write path
// (storeWriteRecord) calls it for every origin with the other records' facts
// at the state S2 plan section 2.2 fixes for that origin; others must not
// include the written record itself.
//
// In order, it refuses:
//
//   - fleet_to_legacy_refused: a legacy source on a record that is fleet-bound
//     live or whose live source is fleet, and a members or member_tags list
//     that gathers a legacy record on a collection that is fleet-bound live;
//   - legacy_source_retired (save only): a legacy source unless the record's
//     live revision already has one;
//   - fleet_mixed_owner_credentials: a collection that would gather a legacy
//     and a fleet record, seen from all three sides (the collection itself,
//     a legacy record a fleet-bound collection gathers, a record saved to or
//     switched to fleet that a collection with a legacy member gathers);
//   - fleet_file_unavailable: a file whose node source is or would be
//     fleet-bound, and a record whose write would make a file's node source
//     fleet-bound;
//   - fleet_script_link_unavailable: a link-mode Script Operator or Script
//     Filter on a record that is or would be fleet-bound.
//
// The membership half of fleet_to_legacy_refused at apply_revision also
// triggers on the fleet-boundness the promotion would give the record; the
// caller passes FleetBound true for that case.
func applyFleetWriteRules(rec subscriptionRecord, live fleetLiveFacts, others []fleetRecordFacts, origin fleetWriteOrigin) (subscriptionRecord, error) {
	self := factsOf(rec)
	universe := make([]fleetRecordFacts, 0, len(others)+1)
	for _, other := range others {
		if other.ID != rec.ID {
			universe = append(universe, other)
		}
	}
	universe = append(universe, self)
	byID := make(map[string]fleetRecordFacts, len(universe))
	for _, f := range universe {
		byID[f.ID] = f
	}

	if self.Kind == kindSub && isVPNCoreSource(self.Source) {
		if live.FleetBound || live.Source == subscriptionSourceFleet {
			return rec, &fleetRuleError{Code: codeFleetToLegacyRefused, IDs: []string{rec.ID},
				Message: fmt.Sprintf("record %q is fleet-bound and cannot take the %s source; its identity shares would receive the owner-credential export", rec.ID, self.Source)}
		}
		if origin == fleetWriteSave && !isVPNCoreSource(live.Source) {
			return rec, &fleetRuleError{Code: codeLegacySourceRetired, IDs: []string{rec.ID},
				Message: fmt.Sprintf("record %q cannot take the %s source: new owner-credential records are retired; use a fleet record", rec.ID, self.Source)}
		}
	}

	if self.Kind == kindCollection {
		fleet, legacy := gatheredSources(self, universe)
		if len(fleet) > 0 && len(legacy) > 0 {
			return rec, mixedRefusal(rec.ID, fleet, legacy)
		}
		if live.FleetBound && len(legacy) > 0 {
			return rec, &fleetRuleError{Code: codeFleetToLegacyRefused, IDs: legacy, Collections: []string{rec.ID},
				Message: fmt.Sprintf("collection %q is fleet-bound and cannot gather the legacy records %s", rec.ID, strings.Join(legacy, ", "))}
		}
	}
	if self.Kind == kindSub {
		for _, collection := range universe {
			if collection.Kind != kindCollection || !gathers(collection, self) {
				continue
			}
			fleet, legacy := gatheredSources(collection, universe)
			if len(fleet) > 0 && len(legacy) > 0 {
				return rec, mixedRefusal(collection.ID, fleet, legacy)
			}
		}
	}

	if err := fleetFileRule(self, universe, byID); err != nil {
		return rec, err
	}
	bound := fleetBoundUnder(self, universe)
	if bound {
		if index, ok := fleetScriptLinkStep(processSteps(rec)); ok {
			return rec, &fleetRuleError{Code: codeFleetScriptLinkUnavailable, IDs: []string{rec.ID},
				Message: fmt.Sprintf("record %q is fleet-bound and step %d runs a script fetched by link; link pinning arrives with S3, so paste the script inline", rec.ID, index+1)}
		}
	}
	if self.Kind == kindCollection && rec.Fleet != nil && !bound {
		rec.Fleet = nil
		rec = withRevision(rec)
	}
	return rec, nil
}

// mixedRefusal names a collection and the records on each side.
func mixedRefusal(collection string, fleet, legacy []string) error {
	ids := append(append([]string(nil), fleet...), legacy...)
	return &fleetRuleError{Code: codeFleetMixedOwnerCredentials, IDs: ids, Collections: []string{collection},
		Message: fmt.Sprintf("collection %q would gather the fleet records %s and the legacy records %s; core serves a collection's provider nodes to every identity, so a legacy member would hand out owner credentials", collection, strings.Join(fleet, ", "), strings.Join(legacy, ", "))}
}

// gathers reports whether a collection gathers sub, explicitly or by tag.
func gathers(collection, sub fleetRecordFacts) bool {
	if sub.Kind != kindSub {
		return false
	}
	for _, id := range collection.Members {
		if id == sub.ID {
			return true
		}
	}
	return tagsIntersect(collection.MemberTags, sub.Tags)
}

// tagsIntersect compares tags as collectionMembers does: trimmed, exact.
func tagsIntersect(wanted, tags []string) bool {
	if len(wanted) == 0 || len(tags) == 0 {
		return false
	}
	set := make(map[string]bool, len(wanted))
	for _, tag := range wanted {
		set[strings.TrimSpace(tag)] = true
	}
	for _, tag := range tags {
		if set[strings.TrimSpace(tag)] {
			return true
		}
	}
	return false
}

// gatheredSources is the ids, sorted, of the fleet and the legacy records a
// collection gathers among universe.
func gatheredSources(collection fleetRecordFacts, universe []fleetRecordFacts) (fleet, legacy []string) {
	for _, candidate := range universe {
		if !gathers(collection, candidate) {
			continue
		}
		switch {
		case candidate.Source == subscriptionSourceFleet:
			fleet = append(fleet, candidate.ID)
		case isVPNCoreSource(candidate.Source):
			legacy = append(legacy, candidate.ID)
		}
	}
	sort.Strings(fleet)
	sort.Strings(legacy)
	return fleet, legacy
}

// fleetBoundUnder reports whether a record would be fleet-bound among
// universe: a fleet sub, or a collection that gathers one. Files are never
// fleet-bound in S2.
func fleetBoundUnder(rec fleetRecordFacts, universe []fleetRecordFacts) bool {
	switch rec.Kind {
	case kindSub:
		return rec.Source == subscriptionSourceFleet
	case kindCollection:
		fleet, _ := gatheredSources(rec, universe)
		return len(fleet) > 0
	}
	return false
}

// fleetFileRule refuses a file over a fleet-bound node source, from both
// sides: the file itself, and a sub or collection whose write would make the
// node source of a file fleet-bound.
func fleetFileRule(self fleetRecordFacts, universe []fleetRecordFacts, byID map[string]fleetRecordFacts) error {
	var files []string
	for _, file := range universe {
		if file.Kind != kindFile || strings.TrimSpace(file.NodeSource) == "" {
			continue
		}
		source, ok := byID[strings.TrimSpace(file.NodeSource)]
		if !ok || !fleetBoundUnder(source, universe) {
			continue
		}
		if file.ID == self.ID || source.ID == self.ID || (source.Kind == kindCollection && gathers(source, self)) {
			files = append(files, file.ID)
		}
	}
	if len(files) == 0 {
		return nil
	}
	sort.Strings(files)
	return &fleetRuleError{Code: codeFleetFileUnavailable, IDs: []string{self.ID}, Files: files,
		Message: fmt.Sprintf("the files %s would read fleet nodes through record %q; fleet-bound files arrive with S3", strings.Join(files, ", "), self.ID)}
}

// fleetScriptLinkStep finds the first enabled Script Operator or Script
// Filter that fetches its program by link. A fleet-bound record refuses one
// until S3 pins a link by digest: an approved plan previewed one version of
// the program, and a serve-path render would run whatever the host serves
// next.
func fleetScriptLinkStep(steps []json.RawMessage) (int, bool) {
	for i, raw := range steps {
		var step struct {
			Type     string `json:"type"`
			Disabled bool   `json:"disabled"`
			Args     struct {
				Mode string `json:"mode"`
			} `json:"args"`
		}
		if json.Unmarshal(raw, &step) != nil || step.Disabled {
			continue
		}
		if (step.Type == "Script Operator" || step.Type == "Script Filter") && strings.EqualFold(strings.TrimSpace(step.Args.Mode), "link") {
			return i, true
		}
	}
	return 0, false
}
