package main

// The two record-model names the staging path needs before lane 1's fleet
// record model lands (plan section 2.1). Lane 1 keeps fleetOptions, the
// Fleet and External fields and every normalizeSubscriptionForStore source
// rule; it does not redeclare these.

// subscriptionSourceFleet is an identity-free structured selection over the
// line catalogue. The selection is the record's leading run of Structured
// Filter steps; the record carries no identity and no raw.
const subscriptionSourceFleet = "fleet"

// legacyOrigin is what migrate_record converted a record from: the legacy
// source and its identity fields, kept on the fleet record as MigratedFrom.
// storeWriteRecord preserves it across an edit of the staged migrated record
// as storeSave preserves Origin, so staged_migrated stays true until the
// plan promotes it.
type legacyOrigin struct {
	// Source is vpn-core or vpn-core-graph.
	Source              string   `json:"source"`
	VPNIdentity         string   `json:"vpn_identity,omitempty"`
	EntryRoots          []string `json:"entry_roots,omitempty"`
	GraphOptionsVersion string   `json:"graph_options_version,omitempty"`
	MigratedAt          string   `json:"migrated_at"`
}

// isFleetSource reports whether a record's source is the fleet catalogue.
func isFleetSource(source string) bool { return source == subscriptionSourceFleet }
