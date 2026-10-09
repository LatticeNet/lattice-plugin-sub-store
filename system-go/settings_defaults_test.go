package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// agentHost is the in-memory store plus a provider that records the user
// agent each fetch sent, counting every host call.
type agentHost struct {
	*kvHostCaller
	total  int
	agents []string
}

func (h *agentHost) call(method string, params any) (json.RawMessage, error) {
	h.total++
	if method != latticeplugin.HostMethodHTTPDo {
		return h.kvHostCaller.call(method, params)
	}
	encoded, _ := json.Marshal(params)
	var request struct {
		Header map[string]string `json:"header"`
	}
	_ = json.Unmarshal(encoded, &request)
	h.agents = append(h.agents, request.Header["User-Agent"])
	return json.Marshal(map[string]any{"status_code": 200, "body_base64": base64.StdEncoding.EncodeToString([]byte(shareFleetFixture(1)))})
}

// Settings' default target and default user agent (S1 plan section 7,
// decision 14) apply exactly where nothing else names a client or an agent:
// the default target is the pin of every record that names none, below an
// explicit target and the record's own pin and above the client the core
// classified, and the default agent is sent for a provider record that names
// none. Settings are read only when a default can apply.
func TestSettingsDefaultsApplyWhereNothingElseNamesThem(t *testing.T) {
	host := &agentHost{kvHostCaller: newKVHostCaller()}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	for _, rec := range []subscriptionRecord{
		{ID: "free", Name: "free", Source: subscriptionSourceLocal, Content: shareFleetFixture(1)},
		{ID: "pinned", Name: "pinned", Source: subscriptionSourceLocal, Content: shareFleetFixture(1), Target: "ClashMeta"},
		{ID: "remote", Name: "remote", Source: subscriptionSourceRemote, URL: "https://provider.example/a"},
		{ID: "remote-ua", Name: "remote-ua", Source: subscriptionSourceRemote, URL: "https://provider.example/b", UA: "Shadowrocket/2070"},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatal(err)
		}
	}
	render := func(payload map[string]any) (string, int) {
		t.Helper()
		host.total = 0
		res := callSubscription(t, rt, "render", payload)
		if !res.OK {
			t.Fatalf("render %v: %s", payload, res.Error)
		}
		var out renderResult
		if err := json.Unmarshal(res.Result, &out); err != nil {
			t.Fatal(err)
		}
		return out.Target, host.total
	}
	fetchAgent := func(id string) string {
		t.Helper()
		host.agents = nil
		if res := callSubscription(t, rt, "fetch", map[string]any{"subscription_id": id}); !res.OK {
			t.Fatalf("fetch %s: %s", id, res.Error)
		}
		if len(host.agents) != 1 {
			t.Fatalf("fetch %s sent %d provider requests", id, len(host.agents))
		}
		return host.agents[0]
	}

	// Without settings: the client the core classified, and the plugin's
	// own agent.
	if target, _ := render(map[string]any{"subscription_id": "free", "format": "plain", "ua_class": "surge"}); target != "Surge" {
		t.Fatalf("no settings: target %q, want the UA class's Surge", target)
	}
	if agent := fetchAgent("remote"); agent != defaultProviderUA {
		t.Fatalf("no settings: agent %q, want %q", agent, defaultProviderUA)
	}

	if res := callSubscription(t, rt, "save_settings", map[string]any{"default_target": "sing-box", "default_ua": "clash.meta/1.19"}); !res.OK {
		t.Fatalf("save settings: %s", res.Error)
	}
	for _, c := range []struct {
		name    string
		payload map[string]any
		want    string
		calls   int
	}{
		{"the default beats the UA class", map[string]any{"subscription_id": "free", "format": "plain", "ua_class": "surge"}, "sing-box", 2},
		{"the default beats ua_target", map[string]any{"subscription_id": "free", "format": "plain", "ua_class": "clash", "ua_target": "ClashMeta"}, "sing-box", 2},
		{"an explicit target beats the default, without reading it", map[string]any{"subscription_id": "free", "format": "plain", "target": "V2Ray"}, "V2Ray", 1},
		{"a format that names a client beats the default", map[string]any{"subscription_id": "free", "format": "clash-meta"}, "ClashMeta", 1},
		{"the record's pin beats the default, without reading it", map[string]any{"subscription_id": "pinned", "format": "plain", "ua_class": "surge"}, "ClashMeta", 1},
	} {
		if target, calls := render(c.payload); target != c.want || calls != c.calls {
			t.Fatalf("%s: target %q in %d host calls, want %q in %d", c.name, target, calls, c.want, c.calls)
		}
	}
	if agent := fetchAgent("remote"); agent != "clash.meta/1.19" {
		t.Fatalf("default agent: %q", agent)
	}
	if agent := fetchAgent("remote-ua"); agent != "Shadowrocket/2070" {
		t.Fatalf("the record's own agent lost to the default: %q", agent)
	}
}
