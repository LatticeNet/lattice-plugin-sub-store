package main

import (
	"encoding/json"
	"strings"
	"testing"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// publishCountingHost answers like the fleet catalogue host and counts the
// operator HTTP calls a publish would make.
type publishCountingHost struct {
	*fleetCatalogueHost
	sent int
}

func (h *publishCountingHost) call(method string, params any) (json.RawMessage, error) {
	if method == latticeplugin.HostMethodHTTPOperatorDo {
		h.sent++
		return json.RawMessage(`{"status_code":200}`), nil
	}
	return h.fleetCatalogueHost.call(method, params)
}

// TestPublishRefusesFleetBoundRecord pins fleet_publish_unavailable: a fleet
// record, and a collection that gathers one by id or by tag, render to a
// plan core binds per identity, so publish refuses before rendering and
// sends nothing; a provider record still publishes.
func TestPublishRefusesFleetBoundRecord(t *testing.T) {
	host := &publishCountingHost{fleetCatalogueHost: newFleetCatalogueHost(fleetTestRows(2, "vless"))}
	rt := &runtime{host: host, engine: testEngineWithHeadroom()}
	saveFleetRecord(t, rt, subscriptionRecord{ID: "jp", Tags: []string{"asia"}})
	for _, rec := range []subscriptionRecord{
		{ID: "local", Name: "local", Source: subscriptionSourceLocal, Content: "trojan://pw@192.0.2.10:443#one", Target: "URI"},
		{ID: "by-id", Name: "by-id", Kind: kindCollection, Members: []string{"jp", "local"}},
		{ID: "by-tag", Name: "by-tag", Kind: kindCollection, MemberTags: []string{"asia"}},
		{ID: "providers", Name: "providers", Kind: kindCollection, Members: []string{"local"}, Target: "URI"},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatalf("save %s: %v", rec.ID, err)
		}
	}
	for _, id := range []string{"jp", "by-id", "by-tag"} {
		_, err := rt.publishSubscription(id, "https://destination.invalid/x", "PUT", "plain")
		if err == nil || !strings.HasPrefix(err.Error(), codeFleetPublishUnavailable) {
			t.Fatalf("publish %s: err %v", id, err)
		}
	}
	if host.sent != 0 || host.calls != 0 {
		t.Fatalf("a refused publish sent %d requests and read %d catalogue pages", host.sent, host.calls)
	}
	if _, err := rt.publishSubscription("local", "https://destination.invalid/x", "PUT", "plain"); err != nil || host.sent != 1 {
		t.Fatalf("a provider record no longer publishes: %v (sent %d)", err, host.sent)
	}
}
