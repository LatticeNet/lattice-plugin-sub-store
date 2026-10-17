package main

import (
	"encoding/json"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// benchmarkFleetPlan measures one serve-path plan computation over n
// production-shaped rows with a native chain (a rename): rows to nodes over
// fresh placeholders, the chain, line recovery and encoding. The plan cache
// is bypassed, so every iteration computes. The perf job reads it (S2 plan
// section 5); no wall-clock figure is asserted here.
func benchmarkFleetPlan(b *testing.B, n int) {
	rows := make([]model.LineCatalogueRow, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, fleetTestRow(i, "vless"))
	}
	projected := projectRows(rows)
	rt := &runtime{engine: testEngineWithHeadroom()}
	rename, _ := json.Marshal(map[string]any{"type": "Regex Rename Operator", "args": []map[string]string{{"expr": "^tokyo", "now": "osaka"}}})
	rec := subscriptionRecord{ID: "bench", Source: subscriptionSourceFleet, Process: []json.RawMessage{rename}}
	rec = withRevision(rec)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		out, err := rt.buildFleetPlan(fleetPlanInput{Label: "subscription \"bench\"", Record: rec, CatalogueVersion: fleetTestVersion, Rows: projected, Target: "sing-box"})
		if err != nil || out.NodeCount != n {
			b.Fatalf("plan: %d nodes, %v", out.NodeCount, err)
		}
	}
}

func BenchmarkFleetPlan1000(b *testing.B) { benchmarkFleetPlan(b, 1000) }

func BenchmarkFleetPlan4096(b *testing.B) { benchmarkFleetPlan(b, 4096) }

// BenchmarkValidateCatalogueRows4096 measures what a fetch spends validating
// the catalogue pages of a 4096-line selection: validateCataloguePage over
// five pages of production-shaped rows as fetchCataloguePage reads them
// (model.ReadLineCataloguePage, outside the timer), every row's fields
// checked and every bound read from the bytes received.
func BenchmarkValidateCatalogueRows4096(b *testing.B) {
	pages := make([]model.ReceivedLineCataloguePage, 0, 5)
	for start := 1; start <= 4096; start += model.MaxLineCataloguePageRows {
		page := model.LineCatalogueResponse{CatalogueVersion: fleetTestVersion}
		for i := start; i < start+model.MaxLineCataloguePageRows && i <= 4096; i++ {
			page.Rows = append(page.Rows, fleetProductionRow(i))
		}
		if start+model.MaxLineCataloguePageRows <= 4096 {
			page.Cursor = "c"
		}
		raw, err := json.Marshal(page)
		if err != nil {
			b.Fatal(err)
		}
		received, err := model.ReadLineCataloguePage(raw)
		if err != nil || !received.Sized() {
			b.Fatalf("read the page: %v, row sizes %v", err, received.Sized())
		}
		pages = append(pages, received)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, page := range pages {
			if err := validateCataloguePage(page); err != nil {
				b.Fatal(err)
			}
		}
	}
}
