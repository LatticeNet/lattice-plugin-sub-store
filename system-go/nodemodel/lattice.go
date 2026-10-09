package nodemodel

// LatticeFields are the fleet fields design 28 names. S1 declares them so
// the JSON shape is fixed; S2 fills them. On the wire they travel under the
// node's "_lattice" key, and only when the block is non-empty.
type LatticeFields struct {
	LineUUID   string `json:"line_uuid,omitempty"`
	LineHashID string `json:"line_hash_id,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	// chain, probe, geo, tags, groups, addresses, placeholders: S2. The
	// placeholders are design 28's synthetic credentials
	// (LATTICE-SYN-<line_uuid>-<field>-<random>), generated per render by the
	// fleet path, which does not exist before S2.
}
