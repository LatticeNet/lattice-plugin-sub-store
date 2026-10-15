package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/fleetfixture"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// proxyTestNow is the instant the proxy tests' catalogue and predicates read.
var proxyTestNow = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

// selectingCatalogueHost answers KV from memory, core's line catalogue from
// rows filtered by the request's selector through the SDK matcher core
// itself calls (so a pushed selector selects here exactly as in core), paged
// by an offset cursor, and vpn-core's identity list from a canned reply.
type selectingCatalogueHost struct {
	*kvHostCaller
	rows        []model.LineCatalogueRow
	unavailable []string
	// advertised is what the first page lists as selector_fields; nil lists
	// every field the SDK defines.
	advertised []string
	// versionAt chooses the catalogue version per catalogue call (from 1).
	versionAt  func(call int) string
	identities json.RawMessage
	calls      int
	requests   []model.LineCatalogueRequest
	rpcs       []string
	total      int
}

func newSelectingCatalogueHost(rows []model.LineCatalogueRow) *selectingCatalogueHost {
	return &selectingCatalogueHost{kvHostCaller: newKVHostCaller(), rows: rows}
}

func (h *selectingCatalogueHost) call(method string, params any) (json.RawMessage, error) {
	h.total++
	if method != latticeplugin.HostMethodRPCCall {
		return h.kvHostCaller.call(method, params)
	}
	encoded, _ := json.Marshal(params)
	var p struct {
		Service string          `json:"service"`
		Method  string          `json:"method"`
		Request json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(encoded, &p); err != nil {
		return nil, err
	}
	h.rpcs = append(h.rpcs, p.Service+" "+p.Method+" "+string(p.Request))
	switch {
	case p.Service == vpnCoreIdentitiesService && p.Method == "list":
		return append(json.RawMessage(nil), h.identities...), nil
	case p.Service == fleetCatalogueService && p.Method == fleetCatalogueMethod:
	default:
		return nil, fmt.Errorf("unexpected rpc %s %s", p.Service, p.Method)
	}
	var request model.LineCatalogueRequest
	if err := json.Unmarshal(p.Request, &request); err != nil {
		return nil, err
	}
	h.calls++
	h.requests = append(h.requests, request)
	var selected []model.LineCatalogueRow
	for i := range h.rows {
		if request.Selector == nil || model.LineCatalogueSelectorMatches(&h.rows[i], request.Selector, proxyTestNow) {
			selected = append(selected, h.rows[i])
		}
	}
	if request.Selector != nil && len(request.Selector.LineUUIDs) > 0 {
		// Core selects a line_uuids list itself, outside the matcher, and
		// answers it in the list's order.
		order := map[string]int{}
		for i, id := range request.Selector.LineUUIDs {
			order[id] = i
		}
		ordered := make([]model.LineCatalogueRow, len(request.Selector.LineUUIDs))
		found := make([]bool, len(ordered))
		for _, row := range selected {
			if at, ok := order[row.LineUUID]; ok {
				ordered[at], found[at] = row, true
			}
		}
		selected = selected[:0]
		for i := range ordered {
			if found[i] {
				selected = append(selected, ordered[i])
			}
		}
	}
	version := fleetTestVersion
	if h.versionAt != nil {
		version = h.versionAt(h.calls)
	}
	start := 0
	if request.Cursor != "" {
		start, _ = strconv.Atoi(strings.TrimPrefix(request.Cursor, "c"))
	}
	limit := request.Limit
	if limit <= 0 {
		limit = model.MaxLineCataloguePageRows
	}
	end := min(start+limit, len(selected))
	page := model.LineCatalogueResponse{CatalogueVersion: version, Rows: append([]model.LineCatalogueRow{}, selected[start:end]...)}
	if start == 0 {
		page.SelectorFields = h.advertised
		if page.SelectorFields == nil {
			page.SelectorFields = model.LineCatalogueSelectorFields()
		}
		page.Unavailable = h.unavailable
	}
	if end < len(selected) {
		page.Cursor = "c" + strconv.Itoa(end)
	}
	return json.Marshal(page)
}

func newProxyRuntime(t testing.TB, rows []model.LineCatalogueRow) (*runtime, *selectingCatalogueHost) {
	t.Helper()
	host := newSelectingCatalogueHost(rows)
	old := fleetNow
	fleetNow = func() time.Time { return proxyTestNow }
	t.Cleanup(func() { fleetNow = old })
	return &runtime{host: host}, host
}

// proxyCatalogueRows is n lines whose country and protocol cycle, so a
// structured step over either selects a known share.
func proxyCatalogueRows(n int) []model.LineCatalogueRow {
	countries := []string{"JP", "HK", "SG", "US"}
	protocols := []string{"vless", "trojan", "hysteria2"}
	rows := make([]model.LineCatalogueRow, 0, n)
	for i := 1; i <= n; i++ {
		row := fleetTestRow(i, protocols[i%len(protocols)])
		row.Geo.Country = countries[i%len(countries)]
		rows = append(rows, row)
	}
	return rows
}

func structuredStep(t testing.TB, args map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"type": operators.StructuredFilterType, "args": args})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func decodePreviewReply(t *testing.T, res response) fleetPreviewReply {
	t.Helper()
	var reply fleetPreviewReply
	decodeResult(t, res, &reply)
	return reply
}

// TestFleetPreviewCountsPerStep reads the catalogue once under the pushed
// selector and counts each later step of the leading structured run in Go:
// the pushed step reports the rows core answered, each plugin step its in and
// out, and a non-structured step ends the run.
func TestFleetPreviewCountsPerStep(t *testing.T) {
	rows := proxyCatalogueRows(120)
	rt, host := newProxyRuntime(t, rows)
	jp, jpTrojan, jpTrojanNoVless := 0, 0, 0
	for _, row := range rows {
		if row.Geo.Country != "JP" {
			continue
		}
		jp++
		if row.Protocol == "trojan" || row.Protocol == "vless" {
			jpTrojan++
			if row.Protocol != "vless" {
				jpTrojanNoVless++
			}
		}
	}
	process := []json.RawMessage{
		// Pushable: keep, all, one "in" over a selector field.
		structuredStep(t, map[string]any{"mode": "keep", "match": "all", "predicates": []map[string]any{{"field": "country", "op": "in", "values": []string{"JP"}}}}),
		// Not pushable (match any), so the run continues in the plugin.
		structuredStep(t, map[string]any{"mode": "keep", "match": "any", "predicates": []map[string]any{{"field": "protocol", "op": "in", "values": []string{"trojan"}}, {"field": "protocol", "op": "in", "values": []string{"vless"}}}}),
		structuredStep(t, map[string]any{"mode": "drop", "predicates": []map[string]any{{"field": "protocol", "op": "in", "values": []string{"vless"}}}}),
		// A non-structured step ends the leading run; it is not counted.
		json.RawMessage(`{"type":"Regex Filter","args":{"keep":true,"regex":["line"]}}`),
	}
	res := callSubscription(t, rt, "fleet_preview", map[string]any{"process": process, "sample": 3})
	reply := decodePreviewReply(t, res)

	if reply.CatalogueVersion != fleetTestVersion || len(reply.SelectorFields) == 0 {
		t.Fatalf("catalogue facts missing: %+v", reply)
	}
	if reply.Covered != 1 || reply.Pushed == nil || len(reply.Pushed.Countries) != 1 || reply.Pushed.Countries[0] != "JP" {
		t.Fatalf("pushed %+v covered %d, want countries [JP] over one step", reply.Pushed, reply.Covered)
	}
	if host.calls != 1 || host.requests[0].Selector == nil || len(host.requests[0].Selector.Countries) != 1 {
		t.Fatalf("catalogue read %d times with %+v, want once under the pushed selector", host.calls, host.requests)
	}
	want := []fleetPreviewStep{
		{Index: 0, Out: jp, Pushed: true},
		{Index: 1, In: &jp, Out: jpTrojan},
		{Index: 2, In: &jpTrojan, Out: jpTrojanNoVless},
	}
	if len(reply.Steps) != len(want) {
		t.Fatalf("steps %+v, want %d rows", reply.Steps, len(want))
	}
	for i, step := range reply.Steps {
		w := want[i]
		if step.Index != w.Index || step.Out != w.Out || step.Pushed != w.Pushed || (w.In == nil) != (step.In == nil) || (w.In != nil && *w.In != *step.In) {
			t.Errorf("step %d = %+v (in %v), want %+v (in %v)", i, step, step.In, w, w.In)
		}
	}
	if reply.Count != jpTrojanNoVless || len(reply.Sample) != 3 {
		t.Fatalf("count %d sample %d, want %d and 3", reply.Count, len(reply.Sample), jpTrojanNoVless)
	}
	for _, row := range reply.Sample {
		if row.Protocol != "trojan" {
			t.Fatalf("sample row %s on %s survived the drop step", row.LineUUID, row.Protocol)
		}
	}
	if reply.EstimatedEnvelopeBytes <= 0 || len(reply.Rows) != 0 {
		t.Fatalf("estimated envelope %d, rows %d: the process form estimates and lists no rows", reply.EstimatedEnvelopeBytes, len(reply.Rows))
	}

	t.Run("a narrower core is evaluated in Go without a second read", func(t *testing.T) {
		rt, host := newProxyRuntime(t, rows)
		host.advertised = []string{"protocols"}
		reply := decodePreviewReply(t, callSubscription(t, rt, "fleet_preview", map[string]any{"process": process[:1]}))
		if host.calls != 1 || reply.Covered != 0 || reply.Pushed != nil {
			t.Fatalf("calls %d covered %d pushed %+v, want one read and the run in Go", host.calls, reply.Covered, reply.Pushed)
		}
		// The test core ignores nothing (it filtered by country anyway), so
		// the Go step sees the selection and keeps it whole.
		if reply.Count != jp || len(reply.Steps) != 1 || reply.Steps[0].In == nil || reply.Steps[0].Pushed {
			t.Fatalf("count %d steps %+v, want %d from one plugin step", reply.Count, reply.Steps, jp)
		}
	})

	t.Run("no structured step counts the whole catalogue", func(t *testing.T) {
		rt, _ := newProxyRuntime(t, rows)
		reply := decodePreviewReply(t, callSubscription(t, rt, "fleet_preview", map[string]any{"process": []json.RawMessage{}}))
		if reply.Count != len(rows) || len(reply.Steps) != 0 || reply.Pushed != nil || len(reply.Sample) != defaultFleetPreviewSample {
			t.Fatalf("count %d steps %d pushed %+v sample %d", reply.Count, len(reply.Steps), reply.Pushed, len(reply.Sample))
		}
	})

	t.Run("a refused catalogue read is an error naming the catalogue", func(t *testing.T) {
		rt, host := newProxyRuntime(t, rows)
		host.versionAt = func(int) string { return "not-a-version" }
		res := callSubscription(t, rt, "fleet_preview", map[string]any{"process": process})
		if res.OK || !strings.HasPrefix(res.Error, codeCatalogueUnavailable) {
			t.Fatalf("got %+v, want %s", res, codeCatalogueUnavailable)
		}
	})
}

// TestFleetPreviewRowLookup answers the rows the detail drawer names, in the
// order it names them, through one catalogue read under a line_uuids
// selector.
func TestFleetPreviewRowLookup(t *testing.T) {
	rows := proxyCatalogueRows(40)
	rt, host := newProxyRuntime(t, rows)
	wanted := []string{rows[30].LineUUID, rows[2].LineUUID, rows[17].LineUUID}
	reply := decodePreviewReply(t, callSubscription(t, rt, "fleet_preview", map[string]any{"line_uuids": wanted}))
	if host.calls != 1 || host.requests[0].Selector == nil || len(host.requests[0].Selector.LineUUIDs) != 3 {
		t.Fatalf("catalogue requests %+v, want one under the three line uuids", host.requests)
	}
	if reply.Count != 3 || len(reply.Rows) != 3 || len(reply.Sample) != 0 || len(reply.Steps) != 0 {
		t.Fatalf("reply %+v, want the three rows only", reply)
	}
	for i, row := range reply.Rows {
		if row.LineUUID != wanted[i] || row.Label == "" || row.Template == nil {
			t.Fatalf("row %d = %s (label %q), want %s with its label and template", i, row.LineUUID, row.Label, wanted[i])
		}
	}
	res := callSubscription(t, rt, "fleet_preview", map[string]any{"line_uuids": wanted, "process": []json.RawMessage{}})
	if res.OK {
		t.Fatalf("a request with both forms was answered: %s", res.Result)
	}
}

// TestFleetPreviewRefusesOver256LineUUIDs pages the drawer: a lookup past 256
// lines is refused before any host call, with the code the UI matches.
func TestFleetPreviewRefusesOver256LineUUIDs(t *testing.T) {
	rt, host := newProxyRuntime(t, proxyCatalogueRows(4))
	ids := make([]string, maxFleetPreviewLookup+1)
	for i := range ids {
		ids[i] = fleetTestLineUUID(i + 1)
	}
	res := callSubscription(t, rt, "fleet_preview", map[string]any{"line_uuids": ids})
	if res.OK || !strings.HasPrefix(res.Error, lineUUIDsTooManyCode) {
		t.Fatalf("got %+v, want %s", res, lineUUIDsTooManyCode)
	}
	if host.total != 0 {
		t.Fatalf("the refusal made %d host calls", host.total)
	}
	reply := decodePreviewReply(t, callSubscription(t, rt, "fleet_preview", map[string]any{"line_uuids": ids[:maxFleetPreviewLookup]}))
	if reply.Count != 4 {
		t.Fatalf("a lookup of exactly %d lines answered %d rows, want the 4 the catalogue holds", maxFleetPreviewLookup, reply.Count)
	}
}

// TestFleetProxyRepliesCarryNoCredentialKeys holds both proxies to the
// catalogue's and the identity list's credential-free contract: a reply of
// either is screened for credential-named keys before it leaves, and a
// relayed reply carrying one is refused rather than relayed.
func TestFleetProxyRepliesCarryNoCredentialKeys(t *testing.T) {
	rows := fleetfixture.Rows(300, 7, proxyTestNow)
	for i := range rows {
		rows[i].Label = rows[i].NodeName + " " + rows[i].Name
	}
	rt, host := newProxyRuntime(t, rows)
	for _, payload := range []map[string]any{
		{"process": []json.RawMessage{}, "sample": maxFleetPreviewSample},
		{"line_uuids": []string{rows[0].LineUUID, rows[1].LineUUID}},
	} {
		res := callSubscription(t, rt, "fleet_preview", payload)
		if !res.OK {
			t.Fatalf("fleet_preview %v: %s", payload, res.Error)
		}
		var value any
		if err := json.Unmarshal(res.Result, &value); err != nil {
			t.Fatal(err)
		}
		if key, found := credentialKeyIn(value); found {
			t.Fatalf("fleet_preview reply carries the credential-named key %q", key)
		}
	}

	host.identities = json.RawMessage(`{"identities":[{"id":"vu_1","name":"alice","status":"active","bound_lines":3,"password":"hunter2"}],"count":1}`)
	if res := callSubscription(t, rt, "identities", map[string]any{}); res.OK || strings.Contains(res.Error, "hunter2") {
		t.Fatalf("identities relayed a credential-named key or quoted its value: %+v", res)
	}
	for _, key := range []string{"uuid", "Password", "private_key", "pre-shared-key", "auth_str", "api_token", "client_secret", "credential", "username"} {
		if !credentialKeyName(key) {
			t.Errorf("credentialKeyName(%q) = false", key)
		}
	}
	for _, key := range []string{"id", "line_uuid", "node_id", "bound_lines", "public_host", "pbk", "sni", "label"} {
		if credentialKeyName(key) {
			t.Errorf("credentialKeyName(%q) = true; a catalogue or identity field would be refused", key)
		}
	}
}

// TestIdentitiesProxyIsVerbatim relays vpn-core's identity list byte for
// byte after the screen, in one host call, and asks for it with an empty
// request.
func TestIdentitiesProxyIsVerbatim(t *testing.T) {
	rt, host := newProxyRuntime(t, nil)
	reply := `{"identities":[{"id":"vu_1","name":"alice","group":"ops","status":"active","bound_lines":12,"expires_at":"2027-01-01T00:00:00Z"},{"id":"vu_2","status":"expired","reason":"expired","bound_lines":0}],"count":2}`
	host.identities = json.RawMessage(reply)
	res := callSubscription(t, rt, "identities", map[string]any{})
	if !res.OK {
		t.Fatalf("identities: %s", res.Error)
	}
	if !bytes.Equal(res.Result, []byte(reply)) {
		t.Fatalf("relayed %s, want %s verbatim", res.Result, reply)
	}
	if host.total != 1 || len(host.rpcs) != 1 || host.rpcs[0] != vpnCoreIdentitiesService+" list {}" {
		t.Fatalf("host calls %d rpcs %v, want one identities list with {}", host.total, host.rpcs)
	}
	if res := callSubscription(t, rt, "identities", map[string]any{"identity_id": "vu_1"}); res.OK {
		t.Fatalf("identities accepted a request field: %s", res.Result)
	}
	if budget := ackedRuntimeBudgets()[pluginID+"/subscription/identities"]; budget.HostCalls != 1 {
		t.Fatalf("identities is signed at %d host calls, want 1", budget.HostCalls)
	}
}

// BenchmarkFleetPreview10000 measures fleet_preview over a 10000-line
// catalogue whose version moves once during the read, so the restart is in
// the figure (S2 plan section 5: under 500 ms in the perf job).
func BenchmarkFleetPreview10000(b *testing.B) {
	rows := fleetfixture.Rows(10000, 11, proxyTestNow)
	for i := range rows {
		rows[i].Label = rows[i].NodeName + " " + rows[i].Name
	}
	process, err := json.Marshal(map[string]any{"process": []json.RawMessage{
		structuredStep(b, map[string]any{"mode": "keep", "match": "all", "predicates": []map[string]any{{"field": "protocol", "op": "in", "values": []string{"vless", "trojan", "hysteria2", "tuic", "vmess", "anytls", "socks"}}}}),
		structuredStep(b, map[string]any{"mode": "drop", "match": "any", "predicates": []map[string]any{{"field": "service_state", "op": "in", "values": []string{"down"}}, {"field": "chain_unresolved", "op": "true"}}}),
	}})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		rt, host := newProxyRuntime(b, rows)
		host.versionAt = func(call int) string {
			if call >= 3 {
				return "lcv1-0000000000000000000000000000000000000000000000000000000000000002"
			}
			return fleetTestVersion
		}
		b.StartTimer()
		res := rt.handleSubscriptionCall(callPayload{Method: "fleet_preview", Payload: process})
		if !res.OK {
			b.Fatalf("fleet_preview: %s", res.Error)
		}
		if host.calls > 22 {
			b.Fatalf("fleet_preview made %d catalogue calls, signed at 22 host calls", host.calls)
		}
	}
}
