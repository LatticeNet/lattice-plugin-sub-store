package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// fleetTestVersion is the catalogue version the test catalogue answers with
// unless a test moves it.
const fleetTestVersion = "lcv1-0000000000000000000000000000000000000000000000000000000000000001"

// fleetTestLineUUID is a deterministic lowercase UUIDv4 for line i.
func fleetTestLineUUID(i int) string {
	return fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
}

// fleetTestTemplates are one valid catalogue template per protocol, with the
// connection parameters core's builder keeps.
var fleetTestTemplates = map[string]map[string]string{
	"vless":       {"encryption": "none", "fp": "chrome", "pbk": "Zk9hGv2m3Tq8sXcRw1yPbN4uL7eA6dK0jI5oF9hQ2tY", "security": "reality", "sid": "ab12", "sni": "www.example.com", "type": "tcp"},
	"trojan":      {"host": "t.example", "path": "/ws", "security": "tls", "sni": "t.example", "type": "ws"},
	"hysteria2":   {"sni": "h.example"},
	"tuic":        {"alpn": "h3", "congestion_control": "bbr", "sni": "tu.example"},
	"vmess":       {"aid": "0", "host": "v.example", "net": "ws", "path": "/v", "sni": "v.example", "tls": "tls"},
	"socks":       {},
	"anytls":      {"security": "tls", "sni": "a.example"},
	"shadowsocks": {},
}

// fleetTestRow is line i of the test catalogue on protocol.
func fleetTestRow(i int, protocol string) model.LineCatalogueRow {
	params := map[string]string{}
	for k, v := range fleetTestTemplates[protocol] {
		params[k] = v
	}
	return model.LineCatalogueRow{
		LineUUID: fleetTestLineUUID(i), LineHashID: fmt.Sprintf("lh-%d", i), NodeID: fmt.Sprintf("node-%d", i%50),
		NodeName: fmt.Sprintf("tokyo-%d", i%50), Name: fmt.Sprintf("line-%d", i),
		NodeTags: []string{"jp", fmt.Sprintf("rack-%d", i%7)}, GroupIDs: []string{"g-asia"},
		Geo:      &model.NodeGeo{Country: "JP", Region: "Tokyo", City: "Tokyo", ASN: 2516, ASOrg: "KDDI", Provider: "maxmind"},
		Machine:  &model.LineCatalogueMachine{Vendor: "acme", Region: "ap-northeast-1"},
		Protocol: protocol, Transport: "tcp", Security: "tls",
		PublicHost: fmt.Sprintf("jp%d.example", i), Addresses: []string{fmt.Sprintf("192.0.2.%d", i%250+1)},
		Managed: true, Status: "ok", ServiceState: "running",
		Chain:    model.LineCatalogueChain{Role: model.LineChainRoleSingle},
		Template: &model.LineCatalogueTemplate{Protocol: protocol, Host: fmt.Sprintf("jp%d.example", i), Port: 443, Params: params, Digest: fmt.Sprintf("tpl-%d", i)},
		Label:    fmt.Sprintf("tokyo-%d line-%d", i%50, i),
	}
}

// fleetProductionRow is line i in the shape a production row has, every
// optional field filled the way the SDK's full row fixture fills it (dated
// geo blocks, a machine with a renewal, DDNS names, a chain root with an exit
// geo, a probe block, usage, extra and the template digest), so a size
// measured over it is the production figure (about 1.42 KB per full row,
// PROGRAM.md:111).
func fleetProductionRow(i int) model.LineCatalogueRow {
	row := fleetTestRow(i, "vless")
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)
	udp := true
	row.NodeName = fmt.Sprintf("Tokyo %02d (IIJ)", i%50)
	row.Name = fmt.Sprintf("vless-reality-%d", 40000+i)
	row.Label = row.NodeName + " " + row.Name
	row.NodeTags = []string{"premium", "asia", fmt.Sprintf("rack-%d", i%7)}
	row.GroupIDs = []string{"grp_asia", "grp_premium"}
	row.Geo = &model.NodeGeo{Country: "JP", Region: "Tokyo", City: "Tokyo", Lat: 35.6895, Lon: 139.6917, IP: fmt.Sprintf("203.0.113.%d", i%250+1), ASN: 2497, ASOrg: "Internet Initiative Japan Inc.", Provider: "IIJ", Source: "operator", UpdatedAt: at}
	row.Machine = &model.LineCatalogueMachine{Vendor: "IIJ", Region: "ap-northeast-1", NextRenewal: at.Add(45 * 24 * time.Hour)}
	row.DDNSNames = []model.LineCatalogueDDNSName{{Name: fmt.Sprintf("tyo%d.example.net", i), Verified: true}, {Name: fmt.Sprintf("tyo%d-v6.example.net", i)}}
	row.Security, row.PublicHost, row.PublicPort = "reality", fmt.Sprintf("203.0.113.%d", i%250+1), 443
	row.ProviderEdge = "edge.provider.example"
	row.Addresses = []string{fmt.Sprintf("203.0.113.%d", i%250+1), fmt.Sprintf("2001:db8::%x", i)}
	row.Overlay, row.OverlayStatus = true, "applied"
	row.Chain = model.LineCatalogueChain{Role: model.LineChainRoleEntry, Root: true, DownstreamLineUUID: fleetTestLineUUID(i + 100000), PathState: model.LinePathConverged,
		ExitGeo: &model.NodeGeo{Country: "US", City: "Los Angeles", Source: "probe", UpdatedAt: at}}
	row.Probe = &model.LineCatalogueProbe{Verdict: model.LineProbeVerdictPass, At: at, Vantage: "control-plane", ColdP50MS: 230, UDPOK: &udp}
	row.Usage = &model.LineCatalogueUsage{IdentityID: "vu_7", UsedBytes: 1 << 30, From: at, To: at.Add(30 * 24 * time.Hour)}
	row.Template.Host = row.PublicHost
	row.Template.Params["sid"] = fmt.Sprintf("%016x", i)
	row.Template.Digest = fmt.Sprintf("sha256:%064x", i)
	row.Extra = map[string]json.RawMessage{"ip_quality": json.RawMessage(`"high"`)}
	return row
}

// fleetTestRows is n lines on protocol, numbered from 1.
func fleetTestRows(n int, protocol string) []model.LineCatalogueRow {
	rows := make([]model.LineCatalogueRow, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, fleetTestRow(i, protocol))
	}
	return rows
}

// fleetCatalogueHost answers KV from memory and core's line catalogue from a
// fixture, paged by an offset cursor. versionAt chooses the catalogue
// version each call answers with (the call number counts from 1).
type fleetCatalogueHost struct {
	*kvHostCaller
	rows        []model.LineCatalogueRow
	versionAt   func(call int) string
	unavailable []string
	calls       int
	requests    []model.LineCatalogueRequest
	other       int
}

func newFleetCatalogueHost(rows []model.LineCatalogueRow) *fleetCatalogueHost {
	return &fleetCatalogueHost{kvHostCaller: newKVHostCaller(), rows: rows}
}

func (h *fleetCatalogueHost) call(method string, params any) (json.RawMessage, error) {
	if method != latticeplugin.HostMethodRPCCall {
		return h.kvHostCaller.call(method, params)
	}
	encoded, _ := json.Marshal(params)
	var p struct {
		Service string                     `json:"service"`
		Method  string                     `json:"method"`
		Request model.LineCatalogueRequest `json:"request"`
	}
	if err := json.Unmarshal(encoded, &p); err != nil {
		return nil, err
	}
	if p.Service != fleetCatalogueService || p.Method != fleetCatalogueMethod {
		h.other++
		return nil, fmt.Errorf("unexpected rpc %s %s", p.Service, p.Method)
	}
	h.calls++
	h.requests = append(h.requests, p.Request)
	version := fleetTestVersion
	if h.versionAt != nil {
		version = h.versionAt(h.calls)
	}
	start := 0
	if p.Request.Cursor != "" {
		start, _ = strconv.Atoi(strings.TrimPrefix(p.Request.Cursor, "c"))
	}
	limit := p.Request.Limit
	if limit <= 0 {
		limit = model.MaxLineCataloguePageRows
	}
	end := min(start+limit, len(h.rows))
	page := model.LineCatalogueResponse{CatalogueVersion: version, Rows: h.rows[start:end]}
	if start == 0 {
		page.SelectorFields = model.LineCatalogueSelectorFields()
		page.Unavailable = h.unavailable
	}
	if end < len(h.rows) {
		page.Cursor = "c" + strconv.Itoa(end)
	}
	if page.Rows == nil {
		page.Rows = []model.LineCatalogueRow{}
	}
	return json.Marshal(page)
}

func newFleetRuntime(t *testing.T, rows []model.LineCatalogueRow) (*runtime, *fleetCatalogueHost) {
	t.Helper()
	host := newFleetCatalogueHost(rows)
	return &runtime{host: host, engine: testEngineWithHeadroom()}, host
}

func saveFleetRecord(t *testing.T, rt *runtime, rec subscriptionRecord) subscriptionRecord {
	t.Helper()
	rec.Source = subscriptionSourceFleet
	if rec.Name == "" {
		rec.Name = rec.ID
	}
	if err := rt.saveSubscription(rec); err != nil {
		t.Fatalf("save %s: %v", rec.ID, err)
	}
	saved, err := rt.getSubscription(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

// decodeFleetEnvelopeRows reads a fleet sub envelope and its rows.
func decodeFleetEnvelopeRows(t *testing.T, raw string) (snapshotEnvelope, []fleetRow) {
	t.Helper()
	env, ok := decodeSnapshotEnvelope(raw)
	if !ok {
		t.Fatalf("not an envelope: %.200s", raw)
	}
	var rows []fleetRow
	if err := json.Unmarshal(env.Rows, &rows); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return env, rows
}

// TestFleetFetchWritesCatalogueBlock pins the fleet sub envelope: kind sub,
// source kind fleet, the catalogue version and the projected rows at the top
// level where core reads them, the selector record, no raw, and the reply's
// source_version the envelope digest rather than the catalogue version.
func TestFleetFetchWritesCatalogueBlock(t *testing.T) {
	rt, host := newFleetRuntime(t, fleetTestRows(3, "vless"))
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp"})
	out, err := rt.fetchSubscription("jp")
	if err != nil {
		t.Fatal(err)
	}
	env, rows := decodeFleetEnvelopeRows(t, out.Raw)
	if env.Kind != kindSub || env.SourceKind != subscriptionSourceFleet || env.CatalogueVersion != fleetTestVersion || env.Raw != "" || len(env.Nodes) != 0 {
		t.Fatalf("envelope = %+v", env)
	}
	if env.Selector == nil || env.Selector.StepHash == "" || env.Selector.Leading != 0 {
		t.Fatalf("selector record = %+v", env.Selector)
	}
	if len(rows) != 3 || rows[0].LineUUID != fleetTestLineUUID(1) || rows[0].Label != "tokyo-1 line-1" || rows[0].Template == nil || rows[0].Template.Host != "jp1.example" {
		t.Fatalf("rows = %+v", rows)
	}
	if !strings.HasPrefix(out.SourceVersion, fleetEnvelopeDigestPrefix) || out.SourceVersion != env.SourceVersion {
		t.Fatalf("source_version = %q, envelope copy %q", out.SourceVersion, env.SourceVersion)
	}
	if out.nodesIn == nil || *out.nodesIn != 3 || out.nodesOut == nil || *out.nodesOut != 3 {
		t.Fatalf("nodes in/out = %v/%v", out.nodesIn, out.nodesOut)
	}
	// Core reads catalogue_version and rows[].line_uuid at the top level.
	var core struct {
		CatalogueVersion string `json:"catalogue_version"`
		Rows             []struct {
			LineUUID string `json:"line_uuid"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out.Raw), &core); err != nil || core.CatalogueVersion != fleetTestVersion || len(core.Rows) != 3 {
		t.Fatalf("core's reader sees %+v (%v)", core, err)
	}
	// The projection leaves out what render does not read.
	for _, absent := range []string{`"extra"`, `"usage"`, `"digest"`, `"dropped"`, `"public_port"`} {
		if strings.Contains(out.Raw, absent) {
			t.Errorf("envelope carries %s", absent)
		}
	}
	if host.calls != 1 || host.requests[0].Limit != fleetCataloguePageRows {
		t.Fatalf("catalogue calls = %d, requests %+v", host.calls, host.requests)
	}
}

// TestFleetFetchRestartsOnVersionMove pins the reader contract: a page whose
// catalogue_version differs from the first page's restarts the read without
// a cursor, once, and a second move fails the fetch.
func TestFleetFetchRestartsOnVersionMove(t *testing.T) {
	rows := fleetTestRows(2500, "vless")
	rt, host := newFleetRuntime(t, rows)
	moved := "lcv1-00000000000000000000000000000000000000000000000000000000000000ff"
	host.versionAt = func(call int) string {
		if call >= 2 {
			return moved
		}
		return fleetTestVersion
	}
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp"})
	out, err := rt.fetchSubscription("jp")
	if err != nil {
		t.Fatal(err)
	}
	env, got := decodeFleetEnvelopeRows(t, out.Raw)
	if env.CatalogueVersion != moved || len(got) != 2500 {
		t.Fatalf("version %q rows %d", env.CatalogueVersion, len(got))
	}
	// Page 1, page 2 (moved), then a fresh pass of three pages.
	if host.calls != 5 || host.requests[2].Cursor != "" {
		t.Fatalf("calls = %d, restart cursor %q", host.calls, host.requests[2].Cursor)
	}

	rt2, host2 := newFleetRuntime(t, rows)
	host2.versionAt = func(call int) string { return fmt.Sprintf("lcv1-%064d", call) }
	saveFleetRecord(t, rt2, subscriptionRecord{ID: "jp"})
	if _, err := rt2.fetchSubscription("jp"); err == nil || !strings.Contains(err.Error(), codeCatalogueUnavailable) {
		t.Fatalf("a catalogue that moves on every page: err %v", err)
	}
}

// TestFleetFetchByteIdenticalWhenUnmoved pins that a fleet envelope carries
// nothing volatile: two fetches of one unmoved catalogue are byte for byte
// the same, placeholders included (none are stored).
func TestFleetFetchByteIdenticalWhenUnmoved(t *testing.T) {
	rt, _ := newFleetRuntime(t, fleetTestRows(40, "trojan"))
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp"})
	first, err := rt.fetchSubscription("jp")
	if err != nil {
		t.Fatal(err)
	}
	second, err := rt.fetchSubscription("jp")
	if err != nil {
		t.Fatal(err)
	}
	if first.Raw != second.Raw || first.SourceVersion != second.SourceVersion {
		t.Fatal("two fetches of an unmoved catalogue differ")
	}
	if strings.Contains(first.Raw, model.PlanPlaceholderPrefix) {
		t.Fatal("a fetch stored a placeholder")
	}
}

// TestFleetFetchZeroRowsIsValid pins that a selection matching nothing is a
// fetch, not a failure: rows is the literal [], never absent or null.
func TestFleetFetchZeroRowsIsValid(t *testing.T) {
	rt, _ := newFleetRuntime(t, nil)
	saveFleetRecord(t, rt, subscriptionRecord{ID: "empty"})
	out, err := rt.fetchSubscription("empty")
	if err != nil {
		t.Fatalf("a zero-row selection failed the fetch: %v", err)
	}
	env, rows := decodeFleetEnvelopeRows(t, out.Raw)
	if string(env.Rows) != "[]" || len(rows) != 0 || env.CatalogueVersion == "" {
		t.Fatalf("rows %s, version %q", env.Rows, env.CatalogueVersion)
	}
	if !strings.Contains(out.Raw, `"rows":[]`) {
		t.Fatalf("envelope does not carry rows as []: %s", out.Raw)
	}
}

// TestFleetFetch10000PagesWithinBounds reads a 10000-line catalogue in ten
// pages of a thousand and refuses a selection past the plan node bound with
// snapshot_too_large rather than storing it.
func TestFleetFetch10000PagesWithinBounds(t *testing.T) {
	rt, host := newFleetRuntime(t, fleetTestRows(10000, "vless"))
	saveFleetRecord(t, rt, subscriptionRecord{ID: "all"})
	_, err := rt.fetchSubscription("all")
	if err == nil || !strings.HasPrefix(err.Error(), "subscription \"all\": "+snapshotTooLargeCode) {
		t.Fatalf("a 10000-line selection: err %v", err)
	}
	if host.calls > fleetCatalogueMaxPagesPerPass {
		t.Fatalf("catalogue calls = %d", host.calls)
	}
	for _, req := range host.requests {
		raw, _ := json.Marshal(req)
		if len(raw) > model.MaxLineCatalogueRequestBytes || req.Limit != model.MaxLineCataloguePageRows {
			t.Fatalf("request %s", raw)
		}
	}
}

// TestFleetFetchHostCallsAt4096 pins the fetch's host calls at the plan node
// bound: the record read (index and record), five pages, nothing else.
func TestFleetFetchHostCallsAt4096(t *testing.T) {
	rt, host := newFleetRuntime(t, fleetTestRows(4096, "vless"))
	saveFleetRecord(t, rt, subscriptionRecord{ID: "full"})
	before := host.calls
	if _, err := rt.fetchSubscription("full"); err != nil {
		t.Fatal(err)
	}
	if got := host.calls - before; got != 5 {
		t.Fatalf("catalogue pages = %d, want 5", got)
	}
	if host.other != 0 {
		t.Fatalf("other rpc calls = %d", host.other)
	}
}

// TestFleetEnvelopeRowsFitRawBound measures the projected row on the
// production row shape and records the ceiling: the full SDK row measures
// about 1.42 KB on production, and the projection has to stay under 1 KB so
// about 4000 rows fit in the core's 4 MiB.
func TestFleetEnvelopeRowsFitRawBound(t *testing.T) {
	rows := make([]model.LineCatalogueRow, 0, 4096)
	for i := 1; i <= 4096; i++ {
		rows = append(rows, fleetProductionRow(i))
	}
	projected := make([]fleetRow, len(rows))
	full := 0
	for i, row := range rows {
		projected[i] = projectFleetRow(row)
		raw, _ := json.Marshal(row)
		full += len(raw)
	}
	encoded, err := encodeFleetRows(projected)
	if err != nil {
		t.Fatal(err)
	}
	perRow := len(encoded) / len(rows)
	ceiling := model.MaxSubscriptionRawBytes / perRow
	// Measured 2026-10-10 on this fixture (1818 bytes per full row, heavier
	// than production's 1.42 KB): 1574 bytes projected, 2661 rows per
	// envelope; scaled to production about 1.2 KB and 3400 rows. The S2
	// plan expected under 1 KB and about 4000 rows; the projection drops only
	// extra, usage, public_port and the template's dropped list and digest,
	// so the 4096-node plan bound is not reachable through one fleet
	// envelope and the operator splits such a record (section 12).
	t.Logf("projected row %d bytes (full SDK row %d); %d rows per %d MiB envelope", perRow, full/len(rows), ceiling, model.MaxSubscriptionRawBytes>>20)
	if perRow >= full/len(rows) {
		t.Fatalf("projection does not shrink the row: %d >= %d", perRow, full/len(rows))
	}
	fit := projected[:min(len(projected), ceiling*95/100)]
	env, err := fleetSubEnvelope(fleetCatalogueRead{CatalogueVersion: fleetTestVersion, Rows: fit}, fleetSelection{StepHash: fleetStepHash(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encodeFleetEnvelope(env, len(fit)); err != nil {
		t.Fatalf("%d projected rows, inside the measured ceiling, do not fit the raw bound: %v", len(fit), err)
	}

	// Past the bound the refusal states the measured figure.
	big := make([]fleetRow, 0, 9000)
	for len(big) < 9000 {
		big = append(big, projected...)
	}
	big = big[:9000]
	env, err = fleetSubEnvelope(fleetCatalogueRead{CatalogueVersion: fleetTestVersion, Rows: big}, fleetSelection{StepHash: fleetStepHash(nil)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = encodeFleetEnvelope(env, len(big))
	if err == nil || !strings.HasPrefix(err.Error(), snapshotTooLargeCode) || !strings.Contains(err.Error(), "holds 9000 lines") || !strings.Contains(err.Error(), "narrow the selection") {
		t.Fatalf("over-bound refusal: %v", err)
	}
}

// TestFleetRowProjectionCoversRenderFields walks every row field render and
// the Structured Filter predicates read against fleetRow, and holds each
// fleetRow JSON tag to the SDK row's tag for the same field.
func TestFleetRowProjectionCoversRenderFields(t *testing.T) {
	read := []string{
		// Render: the row-to-node table of S2 plan section 1.2.
		"line_uuid", "line_hash_id", "node_id", "label", "template", "ddns_names", "provider_edge", "addresses", "geo", "chain", "probe", "node_tags", "group_ids",
		// Predicate.Evaluate's field table (section 2.4) and Structured Sort's keys.
		"machine", "protocol", "transport", "security", "managed", "overlay", "overlay_status", "status", "service_state", "public_host", "node_name", "name",
	}
	tags := map[string]reflect.StructField{}
	rowType := reflect.TypeOf(fleetRow{})
	for i := 0; i < rowType.NumField(); i++ {
		name, _, _ := strings.Cut(rowType.Field(i).Tag.Get("json"), ",")
		tags[name] = rowType.Field(i)
	}
	for _, name := range read {
		if _, ok := tags[name]; !ok {
			t.Errorf("fleetRow has no %q field", name)
		}
	}
	sdk := reflect.TypeOf(model.LineCatalogueRow{})
	for name, field := range tags {
		if name == "label" {
			continue // the S2 SDK row's field; model.LineCatalogueRow carries it until the pin moves
		}
		sdkField, ok := sdk.FieldByName(field.Name)
		if !ok {
			t.Errorf("fleetRow.%s has no SDK row field", field.Name)
			continue
		}
		sdkName, _, _ := strings.Cut(sdkField.Tag.Get("json"), ",")
		if sdkName != name {
			t.Errorf("fleetRow.%s is %q on the wire, the SDK row says %q", field.Name, name, sdkName)
		}
	}
	// The projection copies every field it declares.
	row := fleetTestRow(7, "vless")
	row.Probe = &model.LineCatalogueProbe{Verdict: model.LineProbeVerdictPass}
	row.DDNSNames = []model.LineCatalogueDDNSName{{Name: "jp7.ddns.example", Verified: true}}
	row.Overlay, row.OverlayStatus, row.ProviderEdge = true, "applied", "edge.example"
	projected := projectFleetRow(row)
	value := reflect.ValueOf(projected)
	for i := 0; i < value.NumField(); i++ {
		if value.Field(i).IsZero() {
			t.Errorf("projection left fleetRow.%s empty", rowType.Field(i).Name)
		}
	}
}

// TestSaveRefusesUnknownSource pins that a source outside the known set is
// refused at save instead of being stored and read as a remote source with
// no URL.
func TestSaveRefusesUnknownSource(t *testing.T) {
	rt, _ := newKVRuntime(t)
	err := rt.saveSubscription(subscriptionRecord{ID: "odd", Name: "odd", Source: "ftp"})
	if err == nil || !strings.Contains(err.Error(), `source "ftp"`) {
		t.Fatalf("unknown source: err %v", err)
	}
	for _, source := range []string{"", subscriptionSourceRemote, subscriptionSourceLocal, subscriptionSourceFleet} {
		rec := subscriptionRecord{ID: "ok-" + source, Name: "ok", Source: source, Content: "ss://YWVzLTEyOC1nY206cHc@192.0.2.10:8388#one"}
		if source == "" {
			rec.ID = "ok-none"
		}
		if err := rt.saveSubscription(rec); err != nil {
			t.Errorf("source %q refused: %v", source, err)
		}
	}
}

// TestFleetRecordNormalisation pins what a fleet record keeps: no URL,
// content, agent or identity, its fleet options range checked, and External
// kept on local records only.
func TestFleetRecordNormalisation(t *testing.T) {
	rec, err := normalizeSubscriptionForStore(subscriptionRecord{
		ID: "f", Name: "f", Source: subscriptionSourceFleet, URL: "https://x.invalid/s", Content: "vless://x", UA: "x",
		VPNIdentity: "id-1", External: true,
		Fleet: &fleetOptions{DDNSDial: true, ProbeExclusion: &probeExclusion{Enabled: true, ConsecutiveFailures: 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.URL != "" || rec.Content != "" || rec.UA != "" || rec.VPNIdentity != "" || rec.External || rec.Fleet == nil || !rec.Fleet.DDNSDial {
		t.Fatalf("fleet record = %+v", rec)
	}
	for name, options := range map[string]*fleetOptions{
		"probe 0 failures is the default": nil,
		"probe 11":                        {ProbeExclusion: &probeExclusion{Enabled: true, ConsecutiveFailures: 11}},
		"probe negative":                  {ProbeExclusion: &probeExclusion{ConsecutiveFailures: -1}},
		"usage zero":                      {UsageExclusion: &usageExclusion{Enabled: true}},
		"usage negative":                  {UsageExclusion: &usageExclusion{MaxBytesPerLine: -5}},
	} {
		_, err := normalizeSubscriptionForStore(subscriptionRecord{ID: "f", Name: "f", Source: subscriptionSourceFleet, Fleet: options})
		if (err == nil) != (options == nil) {
			t.Errorf("%s: err %v", name, err)
		}
	}
	remote, err := normalizeSubscriptionForStore(subscriptionRecord{ID: "r", Name: "r", Source: subscriptionSourceRemote, URL: "https://x.invalid/s", External: true, Fleet: &fleetOptions{DDNSDial: true}})
	if err != nil || remote.External || remote.Fleet != nil {
		t.Fatalf("remote record = %+v, %v", remote, err)
	}
	local, err := normalizeSubscriptionForStore(subscriptionRecord{ID: "l", Name: "l", Source: subscriptionSourceLocal, Content: "x", External: true})
	if err != nil || !local.External {
		t.Fatalf("local record = %+v, %v", local, err)
	}
	if _, err := normalizeSubscriptionForStore(subscriptionRecord{ID: "file", Name: "file", Kind: kindFile, Content: "proxies: []", Source: subscriptionSourceFleet}); err == nil {
		t.Fatal("a file took the fleet source")
	}
	// A record with none of the new fields keeps its S1 revision.
	plain := subscriptionRecord{ID: "p", Name: "p", Source: subscriptionSourceRemote, URL: "https://x.invalid/s"}
	before := subscriptionRevision(plain)
	after, err := normalizeSubscriptionForStore(plain)
	if err != nil || after.Revision != before {
		t.Fatalf("revision moved for a record without S2 fields: %q to %q (%v)", before, after.Revision, err)
	}
}
