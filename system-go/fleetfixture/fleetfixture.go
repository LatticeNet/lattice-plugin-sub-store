// Package fleetfixture generates deterministic synthetic line catalogue rows
// in the SDK's shape (model.LineCatalogueRow), for tests and benchmarks of
// the fleet path: the structured steps, the pushdown classifier and the
// differential test that holds the two to the SDK's matcher (S2 plan
// section 2.4). Nothing outside tests imports it.
//
// Every row passes model.LineCatalogueRow.Validate. The mix is chosen so
// that every predicate field has rows on both sides and rows where it is
// unknown: probe blocks on about a third of the rows, chains on about a
// fifth, relays whose chain is unresolved (with a non-nil own geo, so the
// unknown-geo rule decides) on about a twentieth, and null geo and machine
// blocks on about a tenth. Text values vary in case where the core folds
// case, so a matcher that compared exactly would be caught.
package fleetfixture

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-sdk/model"
)

// Mix is how often each kind of row appears, as one in N rows. A zero field
// takes the default.
type Mix struct {
	Probe      int // probe block present; default 3
	Chain      int // part of a chain (entry, relay or exit); default 5
	Unresolved int // a relay whose chain is unresolved; default 20
	Null       int // null geo and null machine; default 10
}

func (m Mix) withDefaults() Mix {
	if m.Probe <= 0 {
		m.Probe = 3
	}
	if m.Chain <= 0 {
		m.Chain = 5
	}
	if m.Unresolved <= 0 {
		m.Unresolved = 20
	}
	if m.Null <= 0 {
		m.Null = 10
	}
	return m
}

// Value pools. Several values differ only in case from another, because
// the core compares countries, regions, protocols, transports and service
// states with case folding and tags, groups and chain roles exactly.
var (
	Countries     = []string{"HK", "JP", "SG", "US", "DE", "hk", "Jp", ""}
	Regions       = []string{"Kowloon", "kowloon", "Tokyo", "Central", "Oregon", "Hesse", ""}
	Cities        = []string{"Hong Kong", "Tokyo", "Singapore", "Portland", "Frankfurt", ""}
	ASNs          = []int{4134, 9304, 16509, 24940, 0}
	ASOrgs        = []string{"China Telecom", "HGC Global", "Amazon", "Hetzner", ""}
	GeoProviders  = []string{"ipinfo", "maxmind", ""}
	Tags          = []string{"hk", "jp", "edge", "core", "premium", "Edge"}
	Groups        = []string{"g-ops", "g-retail", "g-lab"}
	Vendors       = []string{"Vultr", "vultr", "Hetzner", "AWS", ""}
	Protocols     = []string{"vless", "VLESS", "trojan", "hysteria2", "tuic", "vmess", "anytls", "socks"}
	Transports    = []string{"tcp", "ws", "WS", "grpc", "xhttp", ""}
	Securities    = []string{"tls", "reality", "none", ""}
	Statuses      = []string{"ok", "pending", "error", "stale", ""}
	ServiceStates = []string{"running", "Running", "down", "restarting", "unknown", ""}
	OverlayStates = []string{"planned", "applied", "failed"}
	Vantages      = []string{"control-plane", "node:n-hk-01", "node:n-jp-02"}
)

// Rows returns n rows from seed with the default mix, renewal and probe
// instants spread around now.
func Rows(n int, seed uint64, now time.Time) []model.LineCatalogueRow {
	return Catalogue(n, seed, now, Mix{})
}

// Catalogue returns n rows from seed with the given mix. The same arguments
// always give the same rows.
func Catalogue(n int, seed uint64, now time.Time, mix Mix) []model.LineCatalogueRow {
	mix = mix.withDefaults()
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	rows := make([]model.LineCatalogueRow, n)
	for i := range rows {
		rows[i] = row(r, i, now, mix)
	}
	return rows
}

func pick[T any](r *rand.Rand, pool []T) T { return pool[r.IntN(len(pool))] }

// subset is a random subset of pool, in pool order, possibly empty.
func subset(r *rand.Rand, pool []string) []string {
	var out []string
	for _, v := range pool {
		if r.IntN(3) == 0 {
			out = append(out, v)
		}
	}
	return out
}

func uuid(r *rand.Rand) string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func geo(r *rand.Rand) *model.NodeGeo {
	return &model.NodeGeo{
		Country:  pick(r, Countries),
		Region:   pick(r, Regions),
		City:     pick(r, Cities),
		ASN:      pick(r, ASNs),
		ASOrg:    pick(r, ASOrgs),
		Provider: pick(r, GeoProviders),
	}
}

func row(r *rand.Rand, i int, now time.Time, mix Mix) model.LineCatalogueRow {
	node := r.IntN(64)
	nodeName := fmt.Sprintf("n-%02d", node)
	name := ""
	if r.IntN(2) == 0 {
		name = fmt.Sprintf("line-%d", i)
	}
	hash := fmt.Sprintf("%016x", r.Uint64())
	label := nodeName + " " + hash[:8]
	if name != "" {
		label = nodeName + " " + name
	}
	protocol := pick(r, Protocols)
	host := fmt.Sprintf("198.51.100.%d", 1+node%250)
	out := model.LineCatalogueRow{
		LineUUID:     uuid(r),
		LineHashID:   hash,
		NodeID:       fmt.Sprintf("node-%02d", node),
		NodeName:     nodeName,
		Name:         name,
		Label:        label,
		NodeTags:     subset(r, Tags),
		GroupIDs:     subset(r, Groups),
		Geo:          geo(r),
		Machine:      &model.LineCatalogueMachine{Vendor: pick(r, Vendors), Region: "r1"},
		Protocol:     protocol,
		Transport:    pick(r, Transports),
		Security:     pick(r, Securities),
		PublicHost:   host,
		Addresses:    []string{host},
		Managed:      r.IntN(2) == 0,
		Status:       pick(r, Statuses),
		ServiceState: pick(r, ServiceStates),
		Chain:        model.LineCatalogueChain{Role: model.LineChainRoleSingle},
		Template: &model.LineCatalogueTemplate{
			Protocol: protocol,
			Host:     host,
			Port:     443 + r.IntN(1000),
			Params:   map[string]string{"sni": "example.com"},
			Digest:   "td-" + hash[:12],
		},
	}
	// Renewal: unknown on a fifth of the machines, otherwise from 30 days
	// past to 120 days ahead, at an hour boundary or not.
	if r.IntN(5) != 0 {
		out.Machine.NextRenewal = now.Add(time.Duration(r.IntN(150*24)-30*24)*time.Hour + time.Duration(r.IntN(2))*30*time.Minute).UTC()
	}
	if r.IntN(4) == 0 {
		out.DDNSNames = []model.LineCatalogueDDNSName{{Name: fmt.Sprintf("l%d.ddns.example.net", i), Verified: r.IntN(2) == 0}}
	}
	if r.IntN(4) == 0 {
		out.Overlay = true
		out.OverlayStatus = pick(r, OverlayStates)
	}
	if r.IntN(mix.Chain) == 0 {
		out.Chain = chain(r)
	}
	if r.IntN(mix.Unresolved) == 0 {
		// An unresolved relay: no edges, so no exit geo, and its own geo is
		// set so only the unknown-geo rule keeps it out of a country match.
		out.Chain = model.LineCatalogueChain{Role: model.LineChainRoleRelay, Unresolved: true}
		out.Geo = geo(r)
		out.Geo.Country = pick(r, []string{"HK", "JP", "SG"})
		out.Geo.Region = pick(r, []string{"Kowloon", "Tokyo"})
	}
	if r.IntN(mix.Probe) == 0 {
		out.Probe = probe(r, now)
	}
	if r.IntN(mix.Null) == 0 && !out.Chain.Unresolved {
		out.Geo = nil
		out.Machine = nil
	}
	return out
}

func chain(r *rand.Rand) model.LineCatalogueChain {
	c := model.LineCatalogueChain{Role: pick(r, []string{model.LineChainRoleEntry, model.LineChainRoleRelay, model.LineChainRoleExit})}
	if c.Role != model.LineChainRoleExit {
		c.DownstreamLineUUID = uuid(r)
		if r.IntN(4) != 0 {
			c.ExitGeo = geo(r)
		}
		c.PathServiceState = pick(r, []string{"running", "down", "restarting", "unknown", ""})
	}
	if c.Role == model.LineChainRoleEntry && r.IntN(2) == 0 {
		c.Root = true
		c.PathState = pick(r, []string{model.LinePathConverged, model.LinePathDrifted, model.LinePathBusy})
	}
	return c
}

func probe(r *rand.Rand, now time.Time) *model.LineCatalogueProbe {
	p := &model.LineCatalogueProbe{
		Verdict: pick(r, []string{model.LineProbeVerdictPass, model.LineProbeVerdictPass, model.LineProbeVerdictFail}),
		At:      now.Add(-time.Duration(r.IntN(96*60)) * time.Minute).UTC(),
		Vantage: pick(r, Vantages),
	}
	if p.Verdict == model.LineProbeVerdictFail {
		p.ConsecutiveFailures = 1 + r.IntN(5)
	}
	if r.IntN(4) != 0 {
		p.ColdP50MS = 20 + r.IntN(900)
	}
	switch r.IntN(3) {
	case 0:
		ok := true
		p.UDPOK = &ok
	case 1:
		ok := false
		p.UDPOK = &ok
	}
	return p
}

// Node is the fleet node a row becomes for the structured steps: name from
// the row's label, type, server and port from its template, and the
// Lattice block filled from the row as the fleet render fills it (S2 plan
// sections 1.2 and 2.3). It builds no credential and runs no parser: the
// structured steps read the Lattice block only, and the S1 operators a test
// chains with them read name, type, server and port.
func Node(row model.LineCatalogueRow) *nodemodel.Node {
	fields := map[string]any{"name": row.Label, "type": row.Protocol}
	templateHost := ""
	if row.Template != nil {
		templateHost = row.Template.Host
		fields["server"] = row.Template.Host
		fields["port"] = float64(row.Template.Port)
	}
	ch := row.Chain
	return &nodemodel.Node{
		Fields: fields,
		Lattice: &nodemodel.LatticeFields{
			LineUUID:      row.LineUUID,
			LineHashID:    row.LineHashID,
			NodeID:        row.NodeID,
			Geo:           row.Geo,
			Chain:         &ch,
			Tags:          row.NodeTags,
			Groups:        row.GroupIDs,
			Probe:         row.Probe,
			Addresses:     row.Addresses,
			Machine:       row.Machine,
			DDNSNames:     row.DDNSNames,
			Protocol:      row.Protocol,
			Transport:     row.Transport,
			Security:      row.Security,
			Status:        row.Status,
			ServiceState:  row.ServiceState,
			OverlayStatus: row.OverlayStatus,
			Managed:       row.Managed,
			Overlay:       row.Overlay,
			ProviderEdge:  row.ProviderEdge,
			PublicHost:    row.PublicHost,
			TemplateHost:  templateHost,
			NodeName:      row.NodeName,
		},
	}
}

// Nodes is Node over every row, in order.
func Nodes(rows []model.LineCatalogueRow) []*nodemodel.Node {
	out := make([]*nodemodel.Node, len(rows))
	for i, r := range rows {
		out[i] = Node(r)
	}
	return out
}
