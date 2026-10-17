package main

import (
	"encoding/json"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// The handler benchmarks measure the plugin's share of a fleet-bound
// share's cache miss at 1000 production-shaped lines (lattice-server's
// TestS2FleetRenderLatency1000 measures the whole miss): fetch over a host
// that answers one pre-encoded catalogue page, and convert over the bound
// plan core sends. The perf job records them; they carry no target of their
// own.

// pageHost answers the line catalogue with one page encoded once and
// everything else from memory.
type pageHost struct {
	*kvHostCaller
	page json.RawMessage
}

func (h *pageHost) call(method string, params any) (json.RawMessage, error) {
	if method == latticeplugin.HostMethodRPCCall {
		return h.page, nil
	}
	return h.kvHostCaller.call(method, params)
}

func productionPage(b *testing.B, n int) json.RawMessage {
	b.Helper()
	page := model.LineCatalogueResponse{CatalogueVersion: fleetTestVersion, SelectorFields: model.LineCatalogueSelectorFields()}
	for i := 1; i <= n; i++ {
		page.Rows = append(page.Rows, fleetProductionRow(i))
	}
	raw, err := json.Marshal(page)
	if err != nil {
		b.Fatal(err)
	}
	return raw
}

func BenchmarkFleetFetch1000(b *testing.B) {
	host := &pageHost{kvHostCaller: newKVHostCaller(), page: productionPage(b, 1000)}
	rt := &runtime{host: host, engine: testEngineWithHeadroom()}
	rec := subscriptionRecord{ID: "fleet-1000", Name: "fleet-1000", Source: subscriptionSourceFleet}
	if err := rt.saveSubscription(rec); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		out, err := rt.fetchSubscription("fleet-1000")
		if err != nil || out.nodesIn == nil || *out.nodesIn != 1000 {
			b.Fatalf("fetch: %v", err)
		}
	}
}

func BenchmarkFleetConvert1000(b *testing.B) {
	rows := make([]model.LineCatalogueRow, 0, 1000)
	for i := 1; i <= 1000; i++ {
		rows = append(rows, fleetProductionRow(i))
	}
	rt := &runtime{engine: testEngineWithHeadroom()}
	plan, err := rt.buildFleetPlan(fleetPlanInput{Label: "subscription \"bench\"", Record: withRevision(subscriptionRecord{ID: "bench", Source: subscriptionSourceFleet}),
		CatalogueVersion: fleetTestVersion, Rows: projectRows(rows), Target: "sing-box"})
	if err != nil {
		b.Fatal(err)
	}
	t := &testing.T{}
	payload, err := model.EncodeConvertRequest(model.ConvertRequest{Target: "sing-box", Nodes: boundNodes(t, plan, 0)})
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if resp := rt.convertCall(payload); !resp.OK {
			b.Fatalf("convert: %s", resp.Error)
		}
	}
}
