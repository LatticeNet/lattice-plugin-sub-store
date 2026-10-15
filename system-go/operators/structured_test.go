package operators

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/fleetfixture"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-sdk/model"
)

// fixedNow is the clock every structured test and the SDK oracle read.
var fixedNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// runChain compiles steps and runs them over nodes at fixedNow.
func runChain(t *testing.T, nodes []*nodemodel.Node, steps ...string) []*nodemodel.Node {
	t.Helper()
	return structuredPlan(t, steps...).Run(nodes, &Context{Target: "sing-box", Now: fixedNow})
}

func uuidsOf(nodes []*nodemodel.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		if n.Lattice != nil {
			out[i] = n.Lattice.LineUUID
		}
	}
	return out
}

func rowUUIDs(rows []model.LineCatalogueRow) []string {
	out := make([]string, len(rows))
	for i := range rows {
		out[i] = rows[i].LineUUID
	}
	return out
}

// coreSelect is the core's selection of a selector over a catalogue
// (srv/line_catalogue.go:174-195): a selector that names lines selects
// those lines in its own order and applies the rest to each, any other
// selector keeps the catalogue order; every other field is
// model.LineCatalogueSelectorMatches, the function the core calls.
func coreSelect(rows []model.LineCatalogueRow, sel *model.LineCatalogueSelector, now time.Time) []model.LineCatalogueRow {
	if sel == nil {
		return append([]model.LineCatalogueRow(nil), rows...)
	}
	var out []model.LineCatalogueRow
	if len(sel.LineUUIDs) > 0 {
		index := make(map[string]int, len(rows))
		for i := range rows {
			index[rows[i].LineUUID] = i
		}
		for _, id := range sel.LineUUIDs {
			if i, ok := index[id]; ok && model.LineCatalogueSelectorMatches(&rows[i], sel, now) {
				out = append(out, rows[i])
			}
		}
		return out
	}
	for i := range rows {
		if model.LineCatalogueSelectorMatches(&rows[i], sel, now) {
			out = append(out, rows[i])
		}
	}
	return out
}

func filterStep(mode, match, geo string, preds ...string) string {
	args := map[string]any{"mode": mode, "match": match}
	if geo != "" {
		args["geo"] = geo
	}
	var ps []json.RawMessage
	for _, p := range preds {
		ps = append(ps, json.RawMessage(p))
	}
	args["predicates"] = ps
	raw, _ := json.Marshal(map[string]any{"type": StructuredFilterType, "args": args})
	return string(raw)
}

func inPred(field string, values ...string) string {
	raw, _ := json.Marshal(map[string]any{"field": field, "op": OpIn, "values": values})
	return string(raw)
}

func opPred(field, op string, value ...float64) string {
	p := map[string]any{"field": field, "op": op}
	if len(value) > 0 {
		p["value"] = value[0]
	}
	raw, _ := json.Marshal(p)
	return string(raw)
}

// Each pushable field, run in the plugin over the fixture, keeps exactly
// the rows the SDK's matcher selects when the same step is pushed: the
// same set, in the same order.
func TestStructuredPredicatesMatchSDKMatcher(t *testing.T) {
	rows := fleetfixture.Rows(1000, 42, fixedNow)
	r := rand.New(rand.NewPCG(42, 43))
	values := func(pool []string) []string {
		var out []string
		for len(out) == 0 {
			for _, v := range pool {
				if v != "" && r.IntN(3) == 0 {
					out = append(out, v)
				}
			}
		}
		return out
	}
	someUUIDs := func() []string {
		var out []string
		for range 1 + r.IntN(40) {
			out = append(out, rows[r.IntN(len(rows))].LineUUID)
		}
		return append(out, "0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35") // not in the catalogue
	}
	type fieldCase struct {
		selector string
		pred     func() string
	}
	cases := []fieldCase{
		{"line_uuids", func() string { return inPred("line_uuid", someUUIDs()...) }},
		{"countries", func() string { return inPred("country", values(fleetfixture.Countries)...) }},
		{"regions", func() string { return inPred("region", values(fleetfixture.Regions)...) }},
		{"protocols", func() string { return inPred("protocol", values(fleetfixture.Protocols)...) }},
		{"transports", func() string { return inPred("transport", values(fleetfixture.Transports)...) }},
		{"node_tags", func() string { return inPred("node_tag", values(fleetfixture.Tags)...) }},
		{"group_ids", func() string { return inPred("group", values(fleetfixture.Groups)...) }},
		{"service_states", func() string { return inPred("service_state", values(fleetfixture.ServiceStates)...) }},
		{"chain_roles", func() string {
			return inPred("chain_role", values([]string{"single", "entry", "relay", "exit"})...)
		}},
		{"renewal_within_days", func() string { return opPred("renewal_days", OpLTE, float64(r.IntN(90))) }},
		{"probe_passed_within_hours", func() string { return opPred("probe_passed_hours", OpLTE, float64(1+r.IntN(72))) }},
	}
	covered := map[string]bool{}
	for _, fc := range cases {
		for trial := range 20 {
			step := filterStep(ModeKeep, MatchAll, "", fc.pred())
			plan := structuredPlan(t, step)
			sel, n := Pushdown(plan, allSelectorFields)
			if n != 1 || len(sel.Fields()) != 1 || sel.Fields()[0] != fc.selector {
				t.Fatalf("%s: step %s pushed %+v, %d", fc.selector, step, sel, n)
			}
			want := rowUUIDs(coreSelect(rows, sel, fixedNow))
			got := uuidsOf(plan.Run(fleetfixture.Nodes(rows), &Context{Now: fixedNow}))
			if !slices.Equal(got, want) {
				t.Fatalf("%s trial %d: plugin kept %d rows, SDK matcher %d\nstep %s", fc.selector, trial, len(got), len(want), step)
			}
			if len(want) > 0 && len(want) < len(rows) {
				covered[fc.selector] = true
			}
		}
	}
	for _, f := range allSelectorFields {
		if !covered[f] {
			t.Errorf("no trial over %s split the catalogue, so the comparison proved nothing for it", f)
		}
	}
}

// Every way a value is unknown, under every op: keep drops the node, drop
// keeps it. not_in, absent, false and gte are not true over an unknown
// value either.
func TestStructuredUnknownIsNotMatchInKeepAndNotDroppedInDrop(t *testing.T) {
	provider := &nodemodel.Node{Fields: map[string]any{"name": "provider", "type": "trojan", "server": "192.0.2.1", "port": float64(443)}}
	bare := fleetfixture.Node(model.LineCatalogueRow{
		LineUUID: "0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35", LineHashID: "h", NodeID: "n", Protocol: "vless",
		Chain: model.LineCatalogueChain{Role: model.LineChainRoleSingle},
	})
	bare.Lattice.Chain = nil // a block lane 1 always fills, nil here so chain fields are unknown too
	unresolved := fleetfixture.Node(model.LineCatalogueRow{
		LineUUID: "1b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35", LineHashID: "h", NodeID: "n", Protocol: "vless",
		Geo:   &model.NodeGeo{Country: "HK", Region: "Kowloon", City: "Hong Kong", ASN: 4134, ASOrg: "X", Provider: "ipinfo"},
		Chain: model.LineCatalogueChain{Role: model.LineChainRoleRelay, Unresolved: true},
	})
	zeroes := fleetfixture.Node(model.LineCatalogueRow{
		LineUUID: "2b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35", LineHashID: "h", NodeID: "n", Protocol: "vless",
		Geo:     &model.NodeGeo{},
		Machine: &model.LineCatalogueMachine{},
		Probe:   &model.LineCatalogueProbe{Verdict: model.LineProbeVerdictFail, At: fixedNow},
		Chain:   model.LineCatalogueChain{Role: model.LineChainRoleSingle},
	})
	type unknownCase struct {
		node  *nodemodel.Node
		field string
	}
	var cases []unknownCase
	for _, f := range PredicateFields() {
		cases = append(cases, unknownCase{provider, f.Name})
	}
	for _, f := range []string{"country", "region", "city", "asn", "as_org", "geo_provider", "machine_vendor", "renewal_days",
		"chain_role", "path_state", "chain_root", "chain_unresolved",
		"probe_passed_hours", "probe_vantage", "probe_cold_p50_ms", "probe_udp_ok"} {
		cases = append(cases, unknownCase{bare, f})
	}
	for _, f := range []string{"country", "region", "city", "asn", "as_org", "geo_provider"} {
		cases = append(cases, unknownCase{unresolved, f})
	}
	// Known blocks whose values are the catalogue's "unknown": empty texts,
	// a zero renewal and cold p50, a failed probe for the pass window, an
	// untested UDP.
	for _, f := range []string{"country", "region", "city", "asn", "as_org", "geo_provider", "machine_vendor",
		"transport", "security", "status", "service_state", "overlay_status", "probe_vantage", "probe_udp_ok",
		"renewal_days", "probe_cold_p50_ms", "probe_passed_hours"} {
		cases = append(cases, unknownCase{zeroes, f})
	}
	for _, c := range cases {
		spec, _ := LookupPredicateField(c.field)
		for _, op := range spec.Ops {
			p := Predicate{Field: c.field, Op: op}
			switch op {
			case OpIn, OpNotIn:
				p.Values = []string{map[string]string{"asn": "4134", "chain_role": "relay", "path_state": "busy", "line_uuid": "0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35"}[c.field]}
				if p.Values[0] == "" {
					p.Values[0] = "x"
				}
			case OpLTE, OpGTE:
				v := 5.0
				p.Value = &v
			case OpPresent, OpAbsent:
				if c.node == zeroes {
					continue // a known block: present and absent answer for it
				}
			}
			if _, ok := p.Evaluate(c.node, GeoExit, fixedNow); ok {
				t.Errorf("%s %s over %s: known, want unknown", c.field, op, c.node.Name())
				continue
			}
			raw, _ := json.Marshal(p)
			for _, mode := range []string{ModeKeep, ModeDrop} {
				for _, match := range []string{MatchAll, MatchAny} {
					out := runChain(t, []*nodemodel.Node{c.node}, filterStep(mode, match, "", string(raw)))
					if kept := len(out) == 1; kept != (mode == ModeDrop) {
						t.Errorf("%s %s over %s in a %s/%s step: kept=%v", c.field, op, c.node.Name(), mode, match, kept)
					}
				}
			}
		}
	}
	// The renewal and cold p50 zeroes answer present and absent as absent.
	for _, f := range []string{"renewal_days", "probe_cold_p50_ms"} {
		if m, ok := (Predicate{Field: f, Op: OpAbsent}).Evaluate(zeroes, GeoExit, fixedNow); !m || !ok {
			t.Errorf("%s absent over a zero = %v, %v; want a known match", f, m, ok)
		}
	}
	// An unknown predicate does not sink an "any" step another predicate
	// matches, and does sink an "all" step.
	hk := `{"field":"protocol","op":"in","values":["vless"]}`
	probe := opPred("probe_passed_hours", OpLTE, 24)
	if out := runChain(t, []*nodemodel.Node{bare}, filterStep(ModeKeep, MatchAny, "", probe, hk)); len(out) != 1 {
		t.Error("an any step dropped a node one known predicate matched")
	}
	if out := runChain(t, []*nodemodel.Node{bare}, filterStep(ModeKeep, MatchAll, "", probe, hk)); len(out) != 0 {
		t.Error("an all step kept a node with an unknown predicate")
	}
}

// The 16 relays of 2026-10-08 dialled a stale address and would have
// matched their hub's country by their own geo. An unresolved relay's geo
// is unknown to every geo field under the default exit choice, in the
// plugin and in the SDK's matcher alike; the entry toggle reads its own geo,
// which is where its entry machine is.
func TestStructuredUnresolvedRelayGeoIsUnknown(t *testing.T) {
	row := model.LineCatalogueRow{
		LineUUID: "0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35", LineHashID: "h", NodeID: "n", Protocol: "vless",
		Geo:   &model.NodeGeo{Country: "HK", Region: "Kowloon", City: "Hong Kong", ASN: 4134, ASOrg: "HGC", Provider: "ipinfo"},
		Chain: model.LineCatalogueChain{Role: model.LineChainRoleRelay, Unresolved: true},
	}
	n := fleetfixture.Node(row)
	own := map[string]string{"country": "HK", "region": "Kowloon", "city": "Hong Kong", "asn": "4134", "as_org": "HGC", "geo_provider": "ipinfo"}
	for field, v := range own {
		in, notIn := inPred(field, v), fmt.Sprintf(`{"field":%q,"op":"not_in","values":["1"]}`, field)
		for _, p := range []string{in, notIn, opPred(field, OpPresent), opPred(field, OpAbsent)} {
			if out := runChain(t, []*nodemodel.Node{n}, filterStep(ModeKeep, MatchAll, "", p)); len(out) != 0 {
				t.Errorf("exit geo: keep %s kept the unresolved relay", p)
			}
			if out := runChain(t, []*nodemodel.Node{n}, filterStep(ModeDrop, MatchAll, "", p)); len(out) != 1 {
				t.Errorf("exit geo: drop %s dropped the unresolved relay", p)
			}
		}
		if out := runChain(t, []*nodemodel.Node{n}, filterStep(ModeKeep, MatchAll, GeoEntry, in)); len(out) != 1 {
			t.Errorf("entry geo: keep %s dropped the relay whose entry is there", in)
		}
	}
	for _, sel := range []*model.LineCatalogueSelector{{Countries: []string{"HK"}}, {Regions: []string{"Kowloon"}}} {
		if model.LineCatalogueSelectorMatches(&row, sel, fixedNow) {
			t.Errorf("the SDK matcher selects the unresolved relay for %+v", sel)
		}
	}
	// An exit geo on an unresolved chain is inconsistent; the relay is still
	// unknown, as it is to the SDK.
	row.Chain.ExitGeo = &model.NodeGeo{Country: "JP"}
	if out := runChain(t, []*nodemodel.Node{fleetfixture.Node(row)}, filterStep(ModeKeep, MatchAll, "", inPred("country", "JP"))); len(out) != 0 ||
		model.LineCatalogueSelectorMatches(&row, &model.LineCatalogueSelector{Countries: []string{"JP"}}, fixedNow) {
		t.Error("an unresolved relay with an exit geo matched its exit country")
	}
	if got := structuredSortValue(n, "country"); got.ok {
		t.Error("Structured Sort reads an unresolved relay's own country")
	}
}

// For a chained line, country, region and city read the exit geo by
// default and the node's own geo on the step's entry toggle. An exit geo
// that lacks a field does not fall back to the node's geo for it, and an
// entry-geo step is never pushed.
func TestStructuredEntryGeoToggle(t *testing.T) {
	row := model.LineCatalogueRow{
		LineUUID: "0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35", LineHashID: "h", NodeID: "n", Protocol: "vless",
		Geo:   &model.NodeGeo{Country: "JP", Region: "Tokyo", City: "Tokyo", ASN: 9304},
		Chain: model.LineCatalogueChain{Role: model.LineChainRoleEntry, ExitGeo: &model.NodeGeo{Country: "HK", City: "Hong Kong"}},
	}
	n := fleetfixture.Node(row)
	keeps := func(geo, pred string) bool {
		return len(runChain(t, []*nodemodel.Node{n}, filterStep(ModeKeep, MatchAll, geo, pred))) == 1
	}
	checks := []struct {
		geo, pred string
		want      bool
	}{
		{"", inPred("country", "hk"), true},
		{GeoExit, inPred("country", "JP"), false},
		{GeoEntry, inPred("country", "JP"), true},
		{GeoEntry, inPred("country", "HK"), false},
		{GeoExit, inPred("city", "Hong Kong"), true},
		{GeoEntry, inPred("city", "Tokyo"), true},
		{GeoExit, inPred("region", "Tokyo"), false},                               // the exit geo has no region: no fallback
		{GeoExit, opPred("region", OpAbsent), true},                               // and reads as absent there
		{GeoEntry, inPred("region", "tokyo"), true},                               // case folds
		{GeoExit, `{"field":"region","op":"not_in","values":["Kowloon"]}`, false}, // empty is unknown to not_in
		{GeoExit, inPred("asn", "9304"), false},
		{GeoEntry, inPred("asn", "9304"), true},
	}
	for _, c := range checks {
		if got := keeps(c.geo, c.pred); got != c.want {
			t.Errorf("geo %q, %s: kept=%v, want %v", c.geo, c.pred, got, c.want)
		}
	}
	if model.LineCatalogueSelectorMatches(&row, &model.LineCatalogueSelector{Regions: []string{"Tokyo"}}, fixedNow) {
		t.Error("the SDK matcher falls back to the node's region; the plugin's rule above would disagree")
	}
	plan := structuredPlan(t, filterStep(ModeKeep, MatchAll, GeoEntry, inPred("country", "JP")))
	if sel, covered := Pushdown(plan, allSelectorFields); sel != nil || covered != 0 {
		t.Fatalf("an entry-geo step was pushed: %+v, %d", sel, covered)
	}
	// The toggle does not stop a step whose predicates read no geo.
	plan = structuredPlan(t, filterStep(ModeKeep, MatchAll, GeoEntry, inPred("protocol", "vless")))
	if _, covered := Pushdown(plan, allSelectorFields); covered != 1 {
		t.Fatalf("an entry-geo step with no geo predicate was not pushed")
	}
}

// A keep step's line_uuid list orders what it keeps, in the plugin as in
// the core, which is how a migrated graph record keeps its roots in
// committed order.
func TestStructuredLineUUIDKeepsListOrder(t *testing.T) {
	rows := fleetfixture.Rows(50, 5, fixedNow)
	want := []string{rows[30].LineUUID, rows[2].LineUUID, rows[17].LineUUID, rows[2].LineUUID, rows[44].LineUUID}
	first := []string{want[0], want[1], want[2], want[4]}
	step := filterStep(ModeKeep, MatchAll, "", inPred("line_uuid", want...))
	if got := uuidsOf(runChain(t, fleetfixture.Nodes(rows), step)); !slices.Equal(got, first) {
		t.Fatalf("kept %q, want the list's order %q", got, first)
	}
	sel, covered := Pushdown(structuredPlan(t, step), allSelectorFields)
	if covered != 1 || !slices.Equal(sel.LineUUIDs, first) {
		t.Fatalf("pushed %+v, want the list's first occurrences in order", sel)
	}
	if got := rowUUIDs(coreSelect(rows, sel, fixedNow)); !slices.Equal(got, first) {
		t.Fatalf("the core selects %q", got)
	}
	// Combined with a second predicate the order still holds.
	proto := rows[17].Protocol
	var both []string
	for _, id := range first {
		for _, r := range rows {
			if r.LineUUID == id && strings.EqualFold(r.Protocol, proto) {
				both = append(both, id)
			}
		}
	}
	step2 := filterStep(ModeKeep, MatchAll, "", inPred("protocol", proto), inPred("line_uuid", want...))
	if got := uuidsOf(runChain(t, fleetfixture.Nodes(rows), step2)); !slices.Equal(got, both) {
		t.Fatalf("kept %q, want %q", got, both)
	}
	// Under "any" the listed lines come first in list order, every other
	// kept line after them in input order.
	any := filterStep(ModeKeep, MatchAny, "", inPred("line_uuid", rows[40].LineUUID, rows[1].LineUUID), inPred("protocol", proto))
	got := uuidsOf(runChain(t, fleetfixture.Nodes(rows), any))
	if len(got) < 2 || got[0] != rows[40].LineUUID || got[1] != rows[1].LineUUID {
		t.Fatalf("any: kept %q, want the listed lines first", got)
	}
	var rest []string
	for _, r := range rows {
		if r.LineUUID != rows[40].LineUUID && r.LineUUID != rows[1].LineUUID && strings.EqualFold(r.Protocol, proto) {
			rest = append(rest, r.LineUUID)
		}
	}
	if !slices.Equal(got[2:], rest) {
		t.Fatalf("any: the unlisted kept lines are %q, want input order %q", got[2:], rest)
	}
	// A drop step keeps its survivors in input order.
	drop := filterStep(ModeDrop, MatchAll, "", inPred("line_uuid", rows[3].LineUUID, rows[0].LineUUID))
	got = uuidsOf(runChain(t, fleetfixture.Nodes(rows), drop))
	if want := rowUUIDs(append(append([]model.LineCatalogueRow(nil), rows[1:3]...), rows[4:]...)); !slices.Equal(got, want) {
		t.Fatalf("drop: kept %q", got)
	}
}

func TestStructuredSortUnknownLast(t *testing.T) {
	mk := func(id int, country string, p50 int, renewal time.Duration, state, role, nodeName string) *nodemodel.Node {
		row := model.LineCatalogueRow{
			LineUUID: fmt.Sprintf("%08x-4a5d-4a37-8c47-62f1a2d93c35", id), LineHashID: "h", NodeID: "n", NodeName: nodeName,
			Label: fmt.Sprintf("line %d", id), Protocol: "vless", ServiceState: state,
			Chain: model.LineCatalogueChain{Role: role},
		}
		if country != "-" {
			row.Geo = &model.NodeGeo{Country: country, Region: country + "-r"}
		}
		if p50 >= 0 {
			row.Probe = &model.LineCatalogueProbe{Verdict: model.LineProbeVerdictPass, At: fixedNow, ColdP50MS: p50}
		}
		if renewal != 0 {
			row.Machine = &model.LineCatalogueMachine{NextRenewal: fixedNow.Add(renewal)}
		}
		return fleetfixture.Node(row)
	}
	provider := &nodemodel.Node{Fields: map[string]any{"name": "provider", "type": "trojan"}}
	nodes := func() []*nodemodel.Node {
		return []*nodemodel.Node{
			mk(1, "JP", 300, 48*time.Hour, "running", "single", "beta"),
			provider,
			mk(2, "-", -1, 0, "", "", ""), // every key unknown
			mk(3, "hk", 120, -24*time.Hour, "down", "entry", "Alpha"), // a past renewal is known and nearest
			mk(4, "", 0, 0, "running", "exit", "gamma"),               // empty country and zero p50 are unknown
			mk(5, "HK", 120, 10*24*time.Hour, "Running", "single", "alpha"),
		}
	}
	names := func(ns []*nodemodel.Node) []string {
		out := make([]string, len(ns))
		for i, n := range ns {
			out[i] = n.Name()
		}
		return out
	}
	cases := []struct {
		key, order string
		want       []string
	}{
		{"probe_cold_p50_ms", OrderAsc, []string{"line 3", "line 5", "line 1", "provider", "line 2", "line 4"}},
		{"probe_cold_p50_ms", OrderDesc, []string{"line 1", "line 3", "line 5", "provider", "line 2", "line 4"}},
		{"renewal_days", OrderAsc, []string{"line 3", "line 1", "line 5", "provider", "line 2", "line 4"}},
		{"renewal_days", OrderDesc, []string{"line 5", "line 1", "line 3", "provider", "line 2", "line 4"}},
		{"country", OrderAsc, []string{"line 3", "line 5", "line 1", "provider", "line 2", "line 4"}},
		{"country", OrderDesc, []string{"line 1", "line 3", "line 5", "provider", "line 2", "line 4"}},
		{"region", OrderAsc, []string{"line 4", "line 3", "line 5", "line 1", "provider", "line 2"}}, // line 4's region is "-r"
		{"service_state", OrderAsc, []string{"line 3", "line 1", "line 4", "line 5", "provider", "line 2"}},
		{"service_state", OrderDesc, []string{"line 1", "line 4", "line 5", "line 3", "provider", "line 2"}},
		{"chain_role", OrderAsc, []string{"line 3", "line 4", "line 1", "line 5", "provider", "line 2"}},
		{"node_name", OrderAsc, []string{"line 3", "line 5", "line 1", "line 4", "provider", "line 2"}},
		{"node_name", OrderDesc, []string{"line 4", "line 1", "line 3", "line 5", "provider", "line 2"}},
	}
	for _, c := range cases {
		step := fmt.Sprintf(`{"type":"Structured Sort Operator","args":{"key":%q,"order":%q}}`, c.key, c.order)
		if got := names(runChain(t, nodes(), step)); !slices.Equal(got, c.want) {
			t.Errorf("%s %s: %q\nwant %q", c.key, c.order, got, c.want)
		}
	}
}

// A Lattice-only step's arguments are read here or refused: never a
// fallback the bundle would skip.
func TestStructuredArgsRefusedAtCompile(t *testing.T) {
	for _, step := range []string{
		`{"type":"Structured Filter","args":{"predicates":[]}}`,
		`{"type":"Structured Filter","args":{"mode":"invert","predicates":[{"field":"country","op":"in","values":["HK"]}]}}`,
		`{"type":"Structured Filter"}`,
		`{"type":"Structured Sort Operator","args":{"key":"usage"}}`,
		`{"type":"Structured Sort Operator","args":"asc"}`,
	} {
		_, err := Compile("r", []json.RawMessage{json.RawMessage(step)})
		ae, ok := AsStepArgs(err)
		if !ok || ae.Step != 1 || !strings.HasPrefix(err.Error(), CodeStructuredArgs+": process step 1 (") {
			t.Errorf("%s: err = %v, want a %s refusal", step, err, CodeStructuredArgs)
		}
	}
	// Disabled, a step is not read, as every operator's disabled step.
	plan, err := Compile("r", []json.RawMessage{json.RawMessage(`{"type":"Structured Filter","disabled":true,"args":{"predicates":[]}}`)})
	if err != nil || plan.Steps[0].Kind != KindDisabled {
		t.Fatalf("a disabled step: %v, %+v", err, plan)
	}
	plan = structuredPlan(t,
		filterStep(ModeKeep, MatchAll, "", inPred("country", "HK")),
		`{"type":"Structured Sort Operator","args":{"key":"country"}}`,
		`{"type":"Regex Filter","args":{"regex":["x"]}}`,
	)
	if !plan.Native() || plan.HasFallback() {
		t.Fatal("a chain of structured steps and native operators is not native")
	}
	if !slices.Equal(FleetVocabulary(), []string{StructuredFilterType, StructuredSortType}) {
		t.Fatalf("FleetVocabulary = %q", FleetVocabulary())
	}
	for _, name := range FleetVocabulary() {
		if !slices.Contains(Vocabulary(), name) {
			t.Fatalf("%s is not in the vocabulary", name)
		}
	}
}

// Only a Lattice-only step after the leading run would be lost on a chain
// the bundle runs whole.
func TestStructuredAfterLeadingRun(t *testing.T) {
	sf := filterStep(ModeKeep, MatchAll, "", inPred("country", "HK"))
	script := `{"type":"Script Filter","args":{"mode":"script","content":"x"}}`
	sort := `{"type":"Structured Sort Operator","args":{"key":"country"}}`
	cases := []struct {
		steps []string
		want  int
	}{
		{[]string{sf, sf, script}, 0},
		{[]string{sf, script, sf}, 3},
		{[]string{script, sort}, 2},
		{[]string{sf, sort, script}, 2},
		{[]string{sf, `{"type":"Structured Sort Operator","disabled":true,"args":{"key":"country"}}`, script}, 0},
	}
	for i, c := range cases {
		if got := StructuredAfterLeadingRun(structuredPlan(t, c.steps...)); got != c.want {
			t.Errorf("case %d: %d, want %d", i, got, c.want)
		}
	}
	if StructuredAfterLeadingRun(nil) != 0 {
		t.Fatal("nil plan")
	}
}

// The windows read the instants the core reads, inclusive at the edge, and
// the clock comes from the context.
func TestStructuredWindowsAtTheEdge(t *testing.T) {
	at := func(renewal time.Duration, probed time.Duration) *nodemodel.Node {
		return fleetfixture.Node(model.LineCatalogueRow{
			LineUUID: "0b3cd6a9-4a5d-4a37-8c47-62f1a2d93c35", LineHashID: "h", NodeID: "n", Protocol: "vless",
			Machine: &model.LineCatalogueMachine{NextRenewal: fixedNow.Add(renewal)},
			Probe:   &model.LineCatalogueProbe{Verdict: model.LineProbeVerdictPass, At: fixedNow.Add(-probed)},
			Chain:   model.LineCatalogueChain{Role: model.LineChainRoleSingle},
		})
	}
	cases := []struct {
		node *nodemodel.Node
		pred string
		want bool
	}{
		{at(3*24*time.Hour, 0), opPred("renewal_days", OpLTE, 3), true},
		{at(3*24*time.Hour+time.Second, 0), opPred("renewal_days", OpLTE, 3), false},
		{at(3*24*time.Hour, 0), opPred("renewal_days", OpGTE, 3), true},
		{at(3*24*time.Hour-time.Second, 0), opPred("renewal_days", OpGTE, 3), false},
		{at(-10*24*time.Hour, 0), opPred("renewal_days", OpLTE, 0), true},
		{at(36*time.Hour, 0), opPred("renewal_days", OpLTE, 1.5), true},
		{at(0, 24*time.Hour), opPred("probe_passed_hours", OpLTE, 24), true},
		{at(0, 24*time.Hour+time.Second), opPred("probe_passed_hours", OpLTE, 24), false},
		{at(0, 24*time.Hour), opPred("probe_passed_hours", OpGTE, 24), true},
		{at(0, 23*time.Hour), opPred("probe_passed_hours", OpGTE, 24), false},
	}
	for i, c := range cases {
		got := len(runChain(t, []*nodemodel.Node{c.node}, filterStep(ModeKeep, MatchAll, "", c.pred))) == 1
		if got != c.want {
			t.Errorf("case %d %s: %v, want %v", i, c.pred, got, c.want)
		}
	}
	// A later clock moves the window.
	n := at(3*24*time.Hour, 0)
	plan := structuredPlan(t, filterStep(ModeKeep, MatchAll, "", opPred("renewal_days", OpGTE, 3)))
	if out := plan.Run([]*nodemodel.Node{n}, &Context{Now: fixedNow.Add(time.Hour)}); len(out) != 0 {
		t.Fatal("the step did not read the context's clock")
	}
}
