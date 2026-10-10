package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/parse"
	"github.com/LatticeNet/lattice-sdk/model"
)

// Fleet render (S2 plan sections 1.2 and 2.3). Each selected row becomes a
// node: the row's template written as the URI core's identity links use,
// with a fresh placeholder in every credential field, parsed by parse.Line so
// the node has exactly the field names the URI parsers give (which is what
// core compares), named by the row's label and carrying the row's Lattice
// block. The record's chain runs over those nodes. Afterwards each node's
// line is recovered from the placeholder its credential field carries, never
// from a field a script could write, and the row's Lattice block is attached
// again by that line. The plan lists the nodes in chain order with their
// placeholders, the selection the rows came from and the record's bind
// policy.

// fleetCredentialFields are the credential fields of each template protocol
// a fleet node can be built for, sorted, in the node field names core
// compares (srv/substore_bind.go, substoreBindTemplateShape). A protocol
// absent here (shadowsocks among them) has no bind shape: core could never
// bind it, so its rows are dropped at render with a warning.
var fleetCredentialFields = map[string][]string{
	"vless":     {"uuid"},
	"trojan":    {"password"},
	"anytls":    {"password"},
	"hysteria2": {"password"},
	"tuic":      {"password", "uuid"},
	"vmess":     {"uuid"},
	"socks":     {"password", "username"},
}

// fleetNodeCredentialKeys are the node fields a credential can sit in, in the
// order line recovery reads them.
var fleetNodeCredentialKeys = []string{"password", "username", "uuid"}

// fleetTemplateReservedParams are the parameters a client entry takes from
// the credential payload or the template's own fields, never from Params
// (store.LineClientTemplateReservedParams in core).
var fleetTemplateReservedParams = model.LineTemplateReservedParams

// policyOf is the bind policy a record's fleet options ask for, or nil when
// they ask for nothing.
func policyOf(options *fleetOptions) *model.BindPolicy {
	if options == nil {
		return nil
	}
	policy := &model.BindPolicy{DDNSDial: options.DDNSDial}
	if p := options.ProbeExclusion; p != nil && p.Enabled {
		n := p.ConsecutiveFailures
		if n == 0 {
			n = defaultProbeConsecutiveFailures
		}
		policy.Probe = &model.ProbeExclusionPolicy{ConsecutiveFailures: n}
	}
	if u := options.UsageExclusion; u != nil && u.Enabled {
		policy.Usage = &model.UsageExclusionPolicy{MaxBytesPerLine: u.MaxBytesPerLine}
	}
	if policy.Probe == nil && policy.Usage == nil && !policy.DDNSDial {
		return nil
	}
	return policy
}

// fleetTemplateURI writes a template as the client URI core's identity links
// write it (lineClientURI, srv/line_client_templates.go), with creds, keyed
// by credential field, in place of the identity's credential. It refuses a
// reserved parameter. No flow is written: core fills the identity's own.
func fleetTemplateURI(t fleetTemplate, creds map[string]string, label string) (string, error) {
	hostPort := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	query := url.Values{}
	keys := make([]string, 0, len(t.Params))
	for key := range t.Params {
		if fleetTemplateReservedParams[key] {
			return "", fmt.Errorf("template carries the reserved param %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		query.Set(key, t.Params[key])
	}
	fragment := ""
	if label != "" {
		fragment = "#" + url.PathEscape(label)
	}
	withUser := func(user *url.Userinfo) string {
		return (&url.URL{Scheme: t.Protocol, User: user, Host: hostPort, RawQuery: query.Encode()}).String() + fragment
	}
	switch t.Protocol {
	case "vless":
		return withUser(url.User(creds["uuid"])), nil
	case "trojan", "hysteria2", "anytls":
		return withUser(url.User(creds["password"])), nil
	case "tuic":
		return withUser(url.UserPassword(creds["uuid"], creds["password"])), nil
	case "socks":
		userinfo := base64.StdEncoding.EncodeToString([]byte(creds["username"] + ":" + creds["password"]))
		return "socks://" + userinfo + "@" + hostPort + fragment, nil
	case "vmess":
		document := map[string]string{"v": "2", "ps": label, "add": t.Host, "port": strconv.Itoa(t.Port), "id": creds["uuid"]}
		for key, value := range t.Params {
			document[key] = value
		}
		raw, err := json.Marshal(document)
		if err != nil {
			return "", err
		}
		return "vmess://" + base64.StdEncoding.EncodeToString(raw), nil
	}
	return "", fmt.Errorf("protocol %q has no client URI", t.Protocol)
}

// Why a row has no node. Each is reported per record and never fails the
// render: core could not bind such a row either.
const (
	fleetDropNoTemplate    = "no_template"
	fleetDropNoBindShape   = "no_bind_shape"
	fleetDropTemplateParse = "template_unparsed"
)

// fleetRowNode builds one row's node over the placeholders minted for it:
// the template's URI parsed by parse.Line, named by the row's label, the
// server rewritten to a verified DDNS name when the record opts in, and the
// row's Lattice block attached. drop names why a row has no node; err is set
// only for a placeholder or URI failure, which no valid row produces.
func fleetRowNode(row fleetRow, placeholders map[string]string, options *fleetOptions) (node *nodemodel.Node, drop string, err error) {
	if row.Template == nil {
		return nil, fleetDropNoTemplate, nil
	}
	if _, ok := fleetCredentialFields[row.Template.Protocol]; !ok {
		return nil, fleetDropNoBindShape, nil
	}
	uri, err := fleetTemplateURI(*row.Template, placeholders, row.Label)
	if err != nil {
		return nil, "", err
	}
	parsed, ok, err := parse.Line(uri, parse.Options{})
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, fleetDropTemplateParse, nil
	}
	parsed.SetName(row.Label)
	if options != nil && options.DDNSDial {
		if name := fleetDialName(row); name != "" {
			parsed.Set(name, "server")
		}
	}
	parsed.Lattice = fleetLattice(row)
	return parsed, "", nil
}

// fleetDialName is the verified DDNS name a row's node dials when its record
// opts in, or "" to keep the template host. A NAT line (the template host is
// the provider edge) takes only a name whose verified target is that edge,
// because the node's own public addresses are not reachable from outside.
func fleetDialName(row fleetRow) string {
	nat := row.ProviderEdge != "" && row.Template != nil && strings.EqualFold(row.Template.Host, row.ProviderEdge)
	for _, name := range row.DDNSNames {
		if !name.Verified {
			continue
		}
		if nat && !strings.EqualFold(name.Target, row.ProviderEdge) {
			continue
		}
		return name.Name
	}
	return ""
}

// fleetPlaceholders is one plan computation's placeholder table. Placeholders
// are fresh per computation, never per row read, and only the plugin mints
// them, so a node carrying one is the line it names.
type fleetPlaceholders struct {
	line    map[string]string            // placeholder to line_uuid
	fields  map[string]map[string]string // line_uuid to field to placeholder
	lattice map[string]*nodemodel.LatticeFields
}

func newFleetPlaceholders(rows int) *fleetPlaceholders {
	return &fleetPlaceholders{
		line:    make(map[string]string, rows),
		fields:  make(map[string]map[string]string, rows),
		lattice: make(map[string]*nodemodel.LatticeFields, rows),
	}
}

// mint draws one placeholder per credential field of the row's template
// protocol. A row with no template or no bind shape gets none.
func (p *fleetPlaceholders) mint(row fleetRow) (map[string]string, error) {
	if row.Template == nil {
		return nil, nil
	}
	fields := fleetCredentialFields[row.Template.Protocol]
	out := make(map[string]string, len(fields))
	for _, field := range fields {
		placeholder, err := model.NewPlanPlaceholder(row.LineUUID, field)
		if err != nil {
			return nil, err
		}
		out[field] = placeholder
		p.line[placeholder] = row.LineUUID
	}
	p.fields[row.LineUUID] = out
	return out, nil
}

// recover finds the line a post-chain node stands for from a placeholder in
// one of its credential fields, and whether it found one.
func (p *fleetPlaceholders) recover(n *nodemodel.Node) (string, bool) {
	for _, key := range fleetNodeCredentialKeys {
		value, ok := n.String(key)
		if !ok {
			continue
		}
		if line, known := p.line[value]; known {
			return line, true
		}
	}
	return "", false
}

// fleetRowNodes builds the node of every row that has one, minting its
// placeholders. drops counts the rows without a node by reason.
func fleetRowNodes(rows []fleetRow, options *fleetOptions, table *fleetPlaceholders) (nodes []*nodemodel.Node, drops map[string][]string, err error) {
	nodes = make([]*nodemodel.Node, 0, len(rows))
	for _, row := range rows {
		placeholders, err := table.mint(row)
		if err != nil {
			return nil, nil, fmt.Errorf("line %s: %w", row.LineUUID, err)
		}
		node, drop, err := fleetRowNode(row, placeholders, options)
		if err != nil {
			return nil, nil, fmt.Errorf("line %s: %w", row.LineUUID, err)
		}
		if drop != "" {
			if drops == nil {
				drops = map[string][]string{}
			}
			drops[drop] = append(drops[drop], row.LineUUID)
			continue
		}
		table.lattice[row.LineUUID] = node.Lattice
		nodes = append(nodes, node)
	}
	return nodes, drops, nil
}

// fleetPlanInput is everything one plan computation reads.
type fleetPlanInput struct {
	// Label names the record in errors ("subscription \"id\"").
	Label string
	// Record is the revision rendered: its chain and its fleet options.
	Record subscriptionRecord
	// CatalogueVersion and Rows are the selection, rows in selection order.
	CatalogueVersion string
	Rows             []fleetRow
	// Target is the client the chain sees (operators.Context.Target).
	Target string
}

// fleetPlanOutput is one plan computation's result.
type fleetPlanOutput struct {
	// Encoded is the plan as the render reply carries it.
	Encoded json.RawMessage
	// NodeCount counts the plan's nodes; Dropped counts the rows that had no
	// node, by reason, each with its lines.
	NodeCount int
	Dropped   map[string][]string
}

// buildFleetPlan computes a fleet sub's plan: nodes from rows, the chain,
// line recovery, encoding. It refuses a chain with a Response Transformer
// (fleet_response_chain_unavailable, until S3 runs response chains in
// convert), a link-mode script step (fleet_script_link_unavailable, until S3
// pins links by digest) and a plan past model.MaxRenderPlanBytes (plan_too_large).
func (rt *runtime) buildFleetPlan(in fleetPlanInput) (fleetPlanOutput, error) {
	plan, err := rt.chainPlan(in.Record)
	if err != nil {
		return fleetPlanOutput{}, fmt.Errorf("%s: %w", in.Label, err)
	}
	if err := fleetChainRefusals(in.Label, in.Record, plan); err != nil {
		return fleetPlanOutput{}, err
	}
	table := newFleetPlaceholders(len(in.Rows))
	nodes, drops, err := fleetRowNodes(in.Rows, in.Record.Fleet, table)
	if err != nil {
		return fleetPlanOutput{}, fmt.Errorf("%s: %w", in.Label, err)
	}
	nodes, err = rt.runFleetChain(plan, nodes, in.Target)
	if err != nil {
		return fleetPlanOutput{}, fmt.Errorf("%s: %w", in.Label, err)
	}
	selection := &model.PlanSelection{CatalogueVersion: in.CatalogueVersion, LineUUIDs: make([]string, 0, len(in.Rows))}
	for _, row := range in.Rows {
		selection.LineUUIDs = append(selection.LineUUIDs, row.LineUUID)
	}
	planNodes, err := fleetPlanNodes(nodes, table)
	if err != nil {
		return fleetPlanOutput{}, fmt.Errorf("%s: %w", in.Label, err)
	}
	out := model.SelectionPlan{
		Kind:      model.SelectionPlanKindNodes,
		Nodes:     planNodes,
		Selection: selection,
		Policy:    policyOf(in.Record.Fleet),
	}
	encoded, err := encodeFleetPlan(out)
	if err != nil {
		return fleetPlanOutput{}, fmt.Errorf("%s: %w", in.Label, err)
	}
	return fleetPlanOutput{Encoded: encoded, NodeCount: len(planNodes), Dropped: drops}, nil
}

// fleetChainRefusals refuses the chain shapes a fleet-bound record cannot run
// in S2.
func fleetChainRefusals(label string, rec subscriptionRecord, plan *operators.Plan) error {
	if plan != nil && len(plan.ResponseSteps()) > 0 {
		return fmt.Errorf("%s: %s is fleet-bound, and a Response Transformer runs in convert only from S3; remove the step or move it to a file", codeFleetResponseChainUnavailable, label)
	}
	if index, ok := fleetScriptLinkStep(processSteps(rec)); ok {
		return fmt.Errorf("%s: %s is fleet-bound and step %d runs a script fetched by link; link pinning arrives with S3", codeFleetScriptLinkUnavailable, label, index+1)
	}
	return nil
}

// runFleetChain runs a fleet record's chain over its nodes: in Go when every
// step is native, otherwise on the isolated bundle through
// engine.processNodes, with the nodes as JSON (S2 plan section 1.4).
func (rt *runtime) runFleetChain(plan *operators.Plan, nodes []*nodemodel.Node, target string) ([]*nodemodel.Node, error) {
	if plan == nil {
		return nodes, nil
	}
	if planIsNative(plan) {
		return plan.Run(nodes, &operators.Context{Target: target}), nil
	}
	return rt.subStoreEngine().processNodes(nodes, bundleOperators(plan))
}

// fleetPlanNodes turns post-chain nodes into plan nodes. A node whose
// credential field holds a placeholder this computation minted is that line:
// its Lattice block is attached again from the row and its placeholders are
// the ones minted for the line, so core can check every one is intact. Any
// other node carries no line and no Lattice block, and core excludes it with
// no_line.
func fleetPlanNodes(nodes []*nodemodel.Node, table *fleetPlaceholders) ([]model.SelectionPlanNode, error) {
	out := make([]model.SelectionPlanNode, 0, len(nodes))
	for i, n := range nodes {
		node := model.SelectionPlanNode{}
		if line, ok := table.recover(n); ok {
			n.Lattice = table.lattice[line]
			node.LineUUID = line
			node.Placeholders = table.fields[line]
		} else {
			n.Lattice = nil
		}
		encoded, err := n.MarshalPlanNode()
		if err != nil {
			return nil, fmt.Errorf("plan node %d: %w", i, err)
		}
		node.Node = encoded
		out = append(out, node)
	}
	return out, nil
}

// encodeFleetPlan validates a plan (its selection and policy included) and
// encodes it, refusing one past model.MaxRenderPlanBytes with plan_too_large:
// a larger plan would pass the SDK's own bound and still be killed by core
// for exceeding render's stdout budget once the reply's other fields are
// added.
func encodeFleetPlan(plan model.SelectionPlan) (json.RawMessage, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	if len(raw) > model.MaxRenderPlanBytes {
		return nil, fmt.Errorf("%s: the plan encodes to %d bytes, and a render reply carries at most %d; narrow the selection or split the record", codePlanTooLarge, len(raw), model.MaxRenderPlanBytes)
	}
	return raw, nil
}

// fleetNodesOut is the node count after a fleet record's whole chain, for
// fetch's nodes_out, when the chain runs in Go; ok is false otherwise. The
// placeholders it mints are never stored.
func fleetNodesOut(rec subscriptionRecord, rows []fleetRow) (int, bool) {
	plan, err := (&runtime{}).chainPlan(rec)
	if err != nil || !planIsNative(plan) {
		return 0, false
	}
	nodes, _, err := fleetRowNodes(rows, rec.Fleet, newFleetPlaceholders(len(rows)))
	if err != nil {
		return 0, false
	}
	if plan != nil {
		nodes = plan.Run(nodes, &operators.Context{})
	}
	return len(nodes), true
}

// errProcessNodesUnavailable is processNodes' answer until the bundle entry
// lands.
var errProcessNodesUnavailable = errors.New("a fleet record whose chain runs on the bundle needs engine.processNodes, which this build does not carry yet")
