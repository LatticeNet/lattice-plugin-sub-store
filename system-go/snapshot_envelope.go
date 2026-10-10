package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-sdk/model"
)

// The snapshot envelope (plan section 2.5) is what fetch hands the core as
// raw and what render gets back. It is bounded by MaxSubscriptionRawBytes and
// carries nothing volatile: the core skips a render when raw is byte for byte
// what it already holds, so a timestamp in here would re-render every share on
// every refresh.
//
// A version 1 snapshot (anything a runtime before the envelope stored: a
// provider body, a resolved node list, a template, or the members object of a
// collection) is read as it always was and never rewritten in place; the next
// fetch writes version 2.

const (
	snapshotEnvelopeVersion = 2
	// snapshotTooLargeCode leads the refusal of an envelope the core would
	// refuse anyway, so the reason reaches the operator instead of a bare
	// size error from the server.
	snapshotTooLargeCode = "snapshot_too_large"
	// Why an envelope carries no nodes, or not all of them. size: the nodes
	// would have taken it past the raw bound, so render parses the text of
	// whatever carries none. fallback_chain: a chain runs on the bundle, which
	// parses the text itself.
	nodesOmittedSize     = "size"
	nodesOmittedFallback = "fallback_chain"
)

type snapshotEnvelope struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	// Raw is the provider text, the joined export or the graph raw, verbatim,
	// for the bundle fallback and re-parsing.
	Raw       string `json:"raw,omitempty"`
	RawSHA256 string `json:"raw_sha256,omitempty"`
	// Nodes is the compact model after parse and normalise, before the chain,
	// filled when the record's chain is native. NodesOmitted says why it is
	// absent when it should not be ("size", "fallback_chain"); render then
	// parses Raw.
	Nodes        []json.RawMessage `json:"nodes,omitempty"`
	NodesOmitted string            `json:"nodes_omitted,omitempty"`
	// A collection and a script file carry their members.
	SourceID   string           `json:"source_id,omitempty"`
	SourceName string           `json:"source_name,omitempty"`
	SourceKind string           `json:"source_kind,omitempty"`
	Members    []envelopeMember `json:"members,omitempty"`
	// SourceVersion is the graph composition version (vpn-core-graph). On a
	// fleet envelope it is a copy of the envelope digest the fetch reply
	// carries ("lfe1-..."); core never reads this copy.
	SourceVersion string `json:"source_version,omitempty"`
	// CatalogueVersion and Rows are the fleet block (S2 plan section 2.2), at
	// the top level because core reads the selection there; core reads only
	// catalogue_version and rows[].line_uuid. A fleet sub writes its
	// catalogue version and its selected rows as fleetRow. A collection with
	// a fleet member writes a derived version, "lcv1-" + sha256 over its
	// fleet members' versions in member order, and Rows holds one
	// fleetUnionRow per selected line; the members' own rows travel in
	// Members and are never copied to the top level.
	CatalogueVersion string `json:"catalogue_version,omitempty"`
	// Rows is []fleetRow for a sub and []fleetUnionRow for a collection: the
	// literal [] when the selection matched nothing, never absent on a fleet
	// envelope.
	Rows json.RawMessage `json:"rows,omitempty"`
	// Selector is the selector fetch pushed down and the leading structured
	// steps it evaluated itself, so render can tell whether the snapshot's
	// rows still answer the revision's selection.
	Selector *fleetSelectorRecord `json:"selector,omitempty"`
}

// fleetSelectorRecord says how a fleet block's rows were selected: the
// selector core evaluated, how many leading Structured Filter steps the
// fetch evaluated in all (pushed or in Go), and the digest of those steps as
// stored. Render compares StepHash with the rendered revision's own leading
// run before it trusts the rows.
type fleetSelectorRecord struct {
	Pushed   *model.LineCatalogueSelector `json:"pushed,omitempty"`
	Leading  int                          `json:"leading"`
	StepHash string                       `json:"step_hash"`
}

// fleetUnionRow is one selected line of a collection's top-level block:
// the line and the index of the member block it came from (about 60 bytes).
type fleetUnionRow struct {
	LineUUID string `json:"line_uuid"`
	Member   int    `json:"member"`
}

type envelopeMember struct {
	SubName string            `json:"sub_name"`
	Raw     string            `json:"raw"`
	Nodes   []json.RawMessage `json:"nodes,omitempty"`
	// ID, Source and Revision name the member the block was built from, so
	// render can dispatch per block and the plan cache key can read the
	// member revisions from the envelope instead of member records. Fetch
	// writes them on every block, provider blocks included; a block without
	// them is an S1 or legacy-shaped block.
	ID       string `json:"id,omitempty"`
	Source   string `json:"source,omitempty"`
	Revision string `json:"revision,omitempty"`
	// Steps is the member's process chain as stored. A fleet member's chain
	// runs at render over the block's rows, so the block carries it.
	Steps []json.RawMessage `json:"steps,omitempty"`
	// The member's fleet block, the same shape as the top level of a fleet
	// sub. Rows is present on every fleet block, the literal [] for a
	// selection that matched nothing, which keeps a zero-row fleet member
	// distinguishable from a provider block (no rows field at all).
	CatalogueVersion string               `json:"catalogue_version,omitempty"`
	Selector         *fleetSelectorRecord `json:"selector,omitempty"`
	Rows             json.RawMessage      `json:"rows,omitempty"`
	// parsedRaw says Nodes are Raw parsed and nothing more (the member has no
	// chain of its own), so the size bound can leave them out first. Never
	// stored: render tells the members apart by whether they carry nodes.
	parsedRaw bool
}

// fleetRow is the catalogue row projected to what render and the post-fetch
// steps read: every field a Structured Filter predicate names, the template,
// the names, the label render names the node by, and the Lattice block.
// Field names and JSON tags are the SDK row's own (model.LineCatalogueRow),
// so core's reader of rows[].line_uuid and the UI's CatalogueRow type both
// read it; extra, usage, public_port, template.dropped and template.digest
// are left out. TestFleetRowProjectionCoversRenderFields holds the type to
// every field render and the predicates read.
type fleetRow struct {
	LineUUID      string                        `json:"line_uuid"`
	LineHashID    string                        `json:"line_hash_id"`
	NodeID        string                        `json:"node_id"`
	NodeName      string                        `json:"node_name,omitempty"`
	Name          string                        `json:"name,omitempty"`
	Label         string                        `json:"label,omitempty"`
	NodeTags      []string                      `json:"node_tags,omitempty"`
	GroupIDs      []string                      `json:"group_ids,omitempty"`
	Geo           *model.NodeGeo                `json:"geo,omitempty"`
	Machine       *model.LineCatalogueMachine   `json:"machine,omitempty"`
	DDNSNames     []model.LineCatalogueDDNSName `json:"ddns_names,omitempty"`
	Protocol      string                        `json:"protocol"`
	Transport     string                        `json:"transport,omitempty"`
	Security      string                        `json:"security,omitempty"`
	PublicHost    string                        `json:"public_host,omitempty"`
	ProviderEdge  string                        `json:"provider_edge,omitempty"`
	Addresses     []string                      `json:"addresses,omitempty"`
	Managed       bool                          `json:"managed,omitempty"`
	Overlay       bool                          `json:"overlay,omitempty"`
	OverlayStatus string                        `json:"overlay_status,omitempty"`
	Status        string                        `json:"status,omitempty"`
	ServiceState  string                        `json:"service_state,omitempty"`
	Chain         model.LineCatalogueChain      `json:"chain"`
	Probe         *model.LineCatalogueProbe     `json:"probe"`
	Template      *fleetTemplate                `json:"template,omitempty"`
}

// fleetTemplate is the catalogue template without its dropped list and
// digest: what a row's node is built from.
type fleetTemplate struct {
	Protocol string            `json:"protocol"`
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	Params   map[string]string `json:"params,omitempty"`
}

// textEnvelope wraps one text snapshot.
func textEnvelope(kind, raw, sourceVersion string) snapshotEnvelope {
	sum := sha256.Sum256([]byte(raw))
	return snapshotEnvelope{Version: snapshotEnvelopeVersion, Kind: kind, Raw: raw, RawSHA256: hex.EncodeToString(sum[:]), SourceVersion: sourceVersion}
}

// membersEnvelope wraps a collection's or a script file's members.
func membersEnvelope(kind string, members []fileScriptMember) snapshotEnvelope {
	env := snapshotEnvelope{Version: snapshotEnvelopeVersion, Kind: kind, Members: make([]envelopeMember, 0, len(members))}
	for _, member := range members {
		block := envelopeMember{SubName: member.SubName, Raw: member.Raw,
			ID: member.block.id, Source: member.block.source, Revision: member.block.revision}
		if fleet := member.block.fleet; fleet != nil {
			// A fleet member's chain runs at render over its rows, so the
			// block carries the chain; a provider member's chain already ran.
			block.Steps = member.block.steps
			block.CatalogueVersion, block.Selector, block.Rows = fleet.catalogueVersion, fleet.selector, fleet.rows
		}
		env.Members = append(env.Members, block)
	}
	return env
}

// encodeSnapshotEnvelope is the raw fetch returns. Nodes that would take it
// over the core's bound are left out with nodes_omitted "size"; an envelope
// still over the bound without them is refused here with a stated reason.
//
// Nodes go in two steps. First the ones render derives exactly from the text
// beside them, by the same parse: a subscription's, and those of a member
// with no chain of its own. Then, if the envelope is still over the bound,
// the nodes a member chain left. For such a member render parses its text,
// which is the chain's URI output, so its nodes take a URI round trip that a
// live render skips (a gRPC link gains mode=gun). The first step leaves that
// to a collection whose member chains' nodes alone pass the bound.
func encodeSnapshotEnvelope(env snapshotEnvelope) (string, error) {
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	for _, keepChained := range []bool{true, false} {
		if len(raw) <= model.MaxSubscriptionRawBytes || !envelopeHasNodes(env) {
			break
		}
		env.Nodes = nil
		env.Members = append([]envelopeMember(nil), env.Members...)
		for i := range env.Members {
			if !keepChained || env.Members[i].parsedRaw {
				env.Members[i].Nodes = nil
			}
		}
		env.NodesOmitted = nodesOmittedSize
		if raw, err = json.Marshal(env); err != nil {
			return "", err
		}
	}
	if len(raw) > model.MaxSubscriptionRawBytes {
		return "", fmt.Errorf("%s: the snapshot is %d bytes once enveloped, and the core keeps at most %d; the source returned more than one subscription can carry", snapshotTooLargeCode, len(raw), model.MaxSubscriptionRawBytes)
	}
	return string(raw), nil
}

func envelopeHasNodes(env snapshotEnvelope) bool {
	if len(env.Nodes) > 0 {
		return true
	}
	for _, member := range env.Members {
		if len(member.Nodes) > 0 {
			return true
		}
	}
	return false
}

// envelopeNodes is the nodes a subscription's version 2 envelope carries,
// decoded, or nil when it carries none (or any of them fails to decode), in
// which case render parses the text.
func envelopeNodes(env snapshotEnvelope) []*nodemodel.Node {
	if env.Kind != kindSub || len(env.Nodes) == 0 {
		return nil
	}
	nodes, ok := decodeNodes(env.Nodes)
	if !ok {
		return nil
	}
	return nodes
}

// decodeSnapshotEnvelope reports whether raw is a version 2 envelope. A JSON
// object counts only with version 2 and a kind this plugin writes, so a
// provider body that happens to be a JSON document with a version field is
// still read as the version 1 text it is.
func decodeSnapshotEnvelope(raw string) (snapshotEnvelope, bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return snapshotEnvelope{}, false
	}
	var env snapshotEnvelope
	if json.Unmarshal([]byte(trimmed), &env) != nil || env.Version != snapshotEnvelopeVersion {
		return snapshotEnvelope{}, false
	}
	switch env.Kind {
	case kindSub, kindCollection, kindFile:
		return env, true
	default:
		return snapshotEnvelope{}, false
	}
}

// snapshotText turns a snapshot of either version into the text the render
// paths read: a members envelope becomes the members object they already
// decode, a text envelope its raw, and a version 1 snapshot stays as it is.
func snapshotText(raw string) string {
	env, ok := decodeSnapshotEnvelope(raw)
	if !ok {
		return raw
	}
	return envelopeText(env)
}

// envelopeText is snapshotText for an envelope already decoded.
func envelopeText(env snapshotEnvelope) string {
	if len(env.Members) == 0 {
		return env.Raw
	}
	members := make([]fileScriptMember, 0, len(env.Members))
	for _, member := range env.Members {
		members = append(members, fileScriptMember{SubName: member.SubName, Raw: member.Raw})
	}
	text, err := json.Marshal(snapshotArtifacts{SourceID: env.SourceID, SourceName: env.SourceName, SourceKind: env.SourceKind, Members: members})
	if err != nil {
		// Unreachable for strings; an empty snapshot renders live.
		return ""
	}
	return string(text)
}
