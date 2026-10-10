package operators

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/fleetfixture"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-sdk/model"
)

// TestPushdownDifferential is the acceptance test of S2 plan section 2.4.
// Over 1000 seeded cases, each a fresh 1000-row fleetfixture catalogue
// (probe blocks on a third, chains on a fifth, unresolved relays with a
// non-nil own geo on a twentieth, null blocks on a tenth) and a random chain
// of one to six steps mixing both structured steps with the S1 operators,
// it runs the chain two ways:
//
//	(a) Pushdown's selector applied by the core's selection, whose matcher is
//	    model.LineCatalogueSelectorMatches with a fixed clock, and every step
//	    the selector does not cover run in the plugin;
//	(b) every step run in the plugin over the whole catalogue.
//
// The node lists must be equal as JSON. The oracle is the SDK function the
// core itself calls, so the plugin's evaluator cannot pass by agreeing with
// a mirror of itself. The advertised field list is the whole selector on
// half the cases and a random part of it on the rest.
func TestPushdownDifferential(t *testing.T) {
	const cases, rowsPerCase = 1000, 1000
	results := make([]differentialResult, cases)
	next := make(chan uint64)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seed := range next {
				results[seed] = differentialCase(seed, rowsPerCase)
			}
		}()
	}
	for seed := range uint64(cases) {
		next <- seed
	}
	close(next)
	wg.Wait()

	var pushed, split, nonEmpty, unresolvedHit int
	for _, res := range results {
		if res.failure != "" {
			t.Fatal(res.failure)
		}
		if res.covered > 0 {
			pushed++
			if res.selected > 0 && res.selected < rowsPerCase {
				split++
			}
			if res.unresolvedGeo {
				unresolvedHit++
			}
		}
		if res.nodes > 0 {
			nonEmpty++
		}
	}
	t.Logf("%d cases: %d pushed a selector, %d of those split the catalogue, %d tested an unresolved relay against a pushed geo, %d ended with nodes",
		cases, pushed, split, unresolvedHit, nonEmpty)
	// The comparison proves nothing on cases where nothing was pushed or
	// nothing survived; hold the mix to a floor so a change to the generator
	// cannot hollow the test out.
	if pushed < cases/3 || split < cases/4 || unresolvedHit < cases/20 || nonEmpty < cases/3 {
		t.Fatalf("the cases are too thin: %d pushed, %d split, %d over unresolved relays, %d non-empty", pushed, split, unresolvedHit, nonEmpty)
	}
}

// differentialResult is one case's outcome; failure is empty when the two
// runs agreed.
type differentialResult struct {
	failure       string
	covered       int
	selected      int
	nodes         int
	unresolvedGeo bool
}

// differentialCase runs one seeded case both ways.
func differentialCase(seed uint64, rowsPerCase int) differentialResult {
	rows := fleetfixture.Rows(rowsPerCase, seed, fixedNow)
	r := rand.New(rand.NewPCG(seed, 0xd1ff))
	steps := randomChain(r, rows)
	raw := make([]json.RawMessage, len(steps))
	for i, s := range steps {
		raw[i] = json.RawMessage(s)
	}
	fail := func(format string, args ...any) differentialResult {
		return differentialResult{failure: fmt.Sprintf("case %d: ", seed) + fmt.Sprintf(format, args...)}
	}
	plan, err := Compile("r", raw)
	if err != nil {
		return fail("compile %q: %v", steps, err)
	}
	if !plan.Native() {
		return fail("the chain %q is not native", steps)
	}
	advertised := allSelectorFields
	if r.IntN(2) == 0 {
		advertised = nil
		for _, f := range allSelectorFields {
			if r.IntN(3) != 0 {
				advertised = append(advertised, f)
			}
		}
	}
	sel, covered := Pushdown(plan, advertised)
	if sel != nil {
		if err := sel.Validate(); err != nil {
			return fail("Pushdown built a selector the core refuses: %v", err)
		}
		if unsupported := sel.UnsupportedFields(advertised); len(unsupported) != 0 {
			return fail("pushed unadvertised fields %v", unsupported)
		}
	}
	ctx := &Context{Target: "sing-box", Now: fixedNow}
	selected := coreSelect(rows, sel, fixedNow)
	rest := &Plan{Revision: "r", Steps: plan.Steps[covered:]}
	a := rest.Run(fleetfixture.Nodes(selected), ctx)
	b := plan.Run(fleetfixture.Nodes(rows), ctx)
	ja, err := json.Marshal(a)
	if err != nil {
		return fail("%v", err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		return fail("%v", err)
	}
	if string(ja) != string(jb) {
		return fail("pushdown changed the result\nchain %q\nselector %+v covering %d\nwith pushdown %d nodes %q\nwithout     %d nodes %q",
			steps, sel, covered, len(a), firstNames(a), len(b), firstNames(b))
	}
	res := differentialResult{covered: covered, selected: len(selected), nodes: len(b)}
	if sel != nil && len(sel.Countries)+len(sel.Regions) > 0 {
		for _, row := range rows {
			if row.Chain.Unresolved {
				res.unresolvedGeo = true
				break
			}
		}
	}
	return res
}

func firstNames(nodes []*nodemodel.Node) []string {
	var out []string
	for i, n := range nodes {
		if i == 8 {
			out = append(out, "...")
			break
		}
		out = append(out, n.Name())
	}
	return out
}

// s1Steps are native S1 operators over the fixture's names ("n-NN line-N"
// or "n-NN <hash>"), types and ports. Several depend on the order they see
// the nodes in, so a pushdown that reordered rows would show.
var s1Steps = []string{
	`{"type":"Regex Filter","args":{"regex":["^n-[0-3]"],"keep":true}}`,
	`{"type":"Regex Filter","args":{"regex":["line-[0-9]*[05]$"],"keep":false}}`,
	`{"type":"Sort Operator","args":"asc"}`,
	`{"type":"Sort Operator","args":"desc"}`,
	`{"type":"Regex Rename Operator","args":[{"expr":"^(n-[0-9]+) .*$","now":"$1"}]}`,
	`{"type":"Handle Duplicate Operator","args":{}}`,
	`{"type":"Type Filter","args":{"value":["vless","trojan","tuic"],"keep":true}}`,
	`{"type":"Regex Sort Operator","args":{"expressions":["^n-1","^n-4"],"order":"original"}}`,
	`{"type":"Flag Operator","args":{"mode":"add"}}`,
	`{"type":"Conditional Filter","args":{"rule":{"proposition":"IN","attr":"type","value":"vless hysteria2 anytls"}}}`,
	`{"type":"Useless Filter"}`,
}

// randomChain is one to six steps. The first is a Structured Filter four
// times in five, shaped to be pushable three times in four; later steps are
// Structured Filters two times in five, pushable-shaped half the time, so
// most chains open with a run the classifier can take part of. The rest mix
// Structured Sort and the S1 operators.
func randomChain(r *rand.Rand, rows []model.LineCatalogueRow) []string {
	n := 1 + r.IntN(6)
	out := make([]string, 0, n)
	for i := range n {
		switch k := r.IntN(10); {
		case i == 0 && k < 8:
			out = append(out, randomFilter(r, rows, r.IntN(4) != 0))
		case i > 0 && k < 4:
			out = append(out, randomFilter(r, rows, r.IntN(2) == 0))
		case k < 6:
			key := pick(r, StructuredSortKeys())
			out = append(out, fmt.Sprintf(`{"type":"Structured Sort Operator","args":{"key":%q,"order":%q}}`, key, pick(r, []string{OrderAsc, OrderDesc})))
		default:
			out = append(out, pick(r, s1Steps))
		}
	}
	return out
}

func pick[T any](r *rand.Rand, pool []T) T { return pool[r.IntN(len(pool))] }

// randomFilter is a Structured Filter. A pushable one is keep, all, exit
// and built from "in" and whole lte predicates over pushable fields; any
// other draws mode, match, geo, field and op freely.
func randomFilter(r *rand.Rand, rows []model.LineCatalogueRow, pushable bool) string {
	args := map[string]any{}
	if !pushable {
		args["mode"] = pick(r, []string{ModeKeep, ModeKeep, ModeDrop})
		args["match"] = pick(r, []string{MatchAll, MatchAll, MatchAny})
		args["geo"] = pick(r, []string{GeoExit, GeoExit, GeoEntry})
	}
	fields := PredicateFields()
	var preds []map[string]any
	used := map[string]bool{}
	for range 1 + r.IntN(3) {
		f := pick(r, fields)
		if pushable {
			for f.Selector == "" || used[f.Name] {
				f = pick(r, fields)
			}
		}
		used[f.Name] = true
		op := pick(r, f.Ops)
		if pushable {
			op = OpIn
			if f.Name == "renewal_days" || f.Name == "probe_passed_hours" {
				op = OpLTE
			}
		}
		p := map[string]any{"field": f.Name, "op": op}
		switch op {
		case OpIn, OpNotIn:
			p["values"] = randomValues(r, f.Name, rows)
		case OpLTE, OpGTE:
			p["value"] = randomWindow(r, f.Name, pushable)
		}
		preds = append(preds, p)
	}
	args["predicates"] = preds
	raw, _ := json.Marshal(map[string]any{"type": StructuredFilterType, "args": args})
	return string(raw)
}

func randomValues(r *rand.Rand, field string, rows []model.LineCatalogueRow) []string {
	from := func(pool []string) []string {
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
	switch field {
	case "line_uuid":
		var out []string
		for range 1 + r.IntN(200) {
			out = append(out, rows[r.IntN(len(rows))].LineUUID)
		}
		return out
	case "country":
		return from(fleetfixture.Countries)
	case "region":
		return from(fleetfixture.Regions)
	case "city":
		return from(fleetfixture.Cities)
	case "asn":
		return from([]string{"4134", "9304", "16509", "24940"})
	case "as_org":
		return from(fleetfixture.ASOrgs)
	case "geo_provider":
		return from(fleetfixture.GeoProviders)
	case "node_tag":
		return from(fleetfixture.Tags)
	case "group":
		return from(fleetfixture.Groups)
	case "machine_vendor":
		return from(fleetfixture.Vendors)
	case "protocol":
		return from(fleetfixture.Protocols)
	case "transport":
		return from(fleetfixture.Transports)
	case "security":
		return from(fleetfixture.Securities)
	case "overlay_status":
		return from(fleetfixture.OverlayStates)
	case "status":
		return from(fleetfixture.Statuses)
	case "service_state":
		return from(fleetfixture.ServiceStates)
	case "chain_role":
		return from([]string{"single", "entry", "relay", "exit"})
	case "path_state":
		return from([]string{"converged", "drifted", "busy"})
	case "probe_vantage":
		return from(fleetfixture.Vantages)
	}
	panic("no values for " + field)
}

func randomWindow(r *rand.Rand, field string, whole bool) float64 {
	var v float64
	switch field {
	case "renewal_days":
		v = float64(r.IntN(100) - 10)
	case "probe_passed_hours":
		v = float64(r.IntN(96))
	case "probe_cold_p50_ms":
		v = float64(r.IntN(1000))
	}
	if whole {
		if field == "renewal_days" && v < 0 {
			v = -v
		}
		if field == "probe_passed_hours" && v == 0 {
			v = 1
		}
		return v
	}
	if r.IntN(3) == 0 {
		v += 0.5
	}
	return v
}

// The generator above is only worth what its chains reach; this pins that
// every step type and every predicate field turn up.
func TestPushdownDifferentialReachesEveryField(t *testing.T) {
	rows := fleetfixture.Rows(50, 1, fixedNow)
	seenField := map[string]bool{}
	seenType := map[string]bool{}
	for seed := range uint64(1000) {
		r := rand.New(rand.NewPCG(seed, 0xd1ff))
		for _, s := range randomChain(r, rows) {
			step, err := DecodeStep(json.RawMessage(s))
			if err != nil {
				t.Fatal(err)
			}
			seenType[step.Type] = true
			if step.Type == StructuredFilterType {
				args, err := ParseStructuredFilter(step.Args)
				if err != nil {
					t.Fatalf("generated an invalid step %s: %v", s, err)
				}
				for _, p := range args.Predicates {
					seenField[p.Field+" "+p.Op] = true
				}
			}
		}
	}
	for _, f := range PredicateFields() {
		for _, op := range f.Ops {
			if !seenField[f.Name+" "+op] {
				t.Errorf("the generator never builds %s %s", f.Name, op)
			}
		}
	}
	for _, typ := range []string{StructuredFilterType, StructuredSortType} {
		if !seenType[typ] {
			t.Errorf("the generator never builds a %s", typ)
		}
	}
	for _, s := range s1Steps {
		step, _ := DecodeStep(json.RawMessage(s))
		if !seenType[step.Type] {
			t.Errorf("the generator never builds a %s", step.Type)
		}
	}
	if !slices.Contains(allSelectorFields, "line_uuids") {
		t.Fatal("the selector has no line list")
	}
}
