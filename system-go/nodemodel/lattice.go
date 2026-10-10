package nodemodel

import "github.com/LatticeNet/lattice-sdk/model"

// LatticeFields is the fleet block design 28 names (S2 plan section 2.3).
// The fleet render fills every field from the catalogue row a node was built
// from; a provider node has none.
//
// Two encodings exist and they carry different subsets. MarshalJSON, which a
// snapshot envelope and the bundle round trip use, writes the tagged fields
// under the node's "_lattice" key. MarshalPlanNode writes the tagged fields
// other than line_uuid at the top level under core's names
// (PlanLatticeFields), because core admits no "_lattice" key. The untagged
// fields are row facts the Structured Filter predicates read and core never
// sees; neither encoder writes them.
//
// The block is shared, not copied, by Node.Clone: nothing writes to it after
// the fleet render builds it (scripts write Script, and the fleet render
// re-attaches the block by placeholder after any chain), so the pointers and
// slices inside may be shared by clones.
type LatticeFields struct {
	LineUUID   string                    `json:"line_uuid,omitempty"`
	LineHashID string                    `json:"line_hash_id,omitempty"`
	NodeID     string                    `json:"node_id,omitempty"`
	Geo        *model.NodeGeo            `json:"geo,omitempty"`
	Chain      *model.LineCatalogueChain `json:"chain,omitempty"`
	Tags       []string                  `json:"tags,omitempty"`
	Groups     []string                  `json:"groups,omitempty"`
	Probe      *model.LineCatalogueProbe `json:"probe,omitempty"`
	Addresses  []string                  `json:"addresses,omitempty"`

	// Row fields predicates and Structured Sort read and core never sees:
	// kept here, never written by MarshalJSON or MarshalPlanNode.
	NodeName      string                        `json:"-"`
	Machine       *model.LineCatalogueMachine   `json:"-"`
	DDNSNames     []model.LineCatalogueDDNSName `json:"-"`
	Protocol      string                        `json:"-"`
	Transport     string                        `json:"-"`
	Security      string                        `json:"-"`
	Status        string                        `json:"-"`
	ServiceState  string                        `json:"-"`
	OverlayStatus string                        `json:"-"`
	Managed       bool                          `json:"-"`
	Overlay       bool                          `json:"-"`
	ProviderEdge  string                        `json:"-"`
	PublicHost    string                        `json:"-"`
	TemplateHost  string                        `json:"-"`
}
