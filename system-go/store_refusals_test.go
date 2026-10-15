package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// providerSecretURL is a provider URL carrying a token: no refusal message
// may ever quote it.
const providerSecretURL = "https://provider.invalid/sub?token=secret-provider-token"

// TestMutatingRefusalsAreStructuredResults drives every structured refusal
// code through a mutating method and asserts the shape plan section 2.6
// fixes: a successful reply {saved: false, refused: {code, message}}, never
// an error (the gateway replaces a mutating method's error with one fixed
// string), and a message that carries no URL, no provider token, no raw
// content and no wrapped error's text.
func TestMutatingRefusalsAreStructuredResults(t *testing.T) {
	remote := func(id string, tags ...string) subscriptionRecord {
		return subscriptionRecord{ID: id, Name: id, Source: subscriptionSourceRemote, URL: providerSecretURL, Tags: tags}
	}
	seed := func(t *testing.T, rt *runtime, recs ...subscriptionRecord) {
		t.Helper()
		for _, rec := range recs {
			if err := rt.saveSubscription(rec); err != nil {
				t.Fatalf("seed %s: %v", rec.ID, err)
			}
		}
	}
	saveOf := func(rec subscriptionRecord) map[string]any { return map[string]any{"subscription": rec} }
	cases := []struct {
		name    string
		setup   func(t *testing.T, rt *runtime, host *casKVHost)
		method  string
		payload func(t *testing.T, rt *runtime) any
		code    string
	}{
		{
			name:   "fleet to legacy",
			setup:  func(t *testing.T, rt *runtime, _ *casKVHost) { seedFleetLive(t, rt, fleetRecord("f")) },
			method: "save", code: refusedFleetToLegacy,
			payload: func(*testing.T, *runtime) any {
				return saveOf(subscriptionRecord{ID: "f", Name: "f", Source: subscriptionSourceVPNCore})
			},
		},
		{
			name:   "legacy source retired",
			setup:  func(t *testing.T, rt *runtime, _ *casKVHost) { seed(t, rt, remote("r")) },
			method: "save", code: refusedLegacySourceRetired,
			payload: func(*testing.T, *runtime) any {
				return saveOf(subscriptionRecord{ID: "r", Name: "r", Source: subscriptionSourceVPNCore})
			},
		},
		{
			name: "mixed owner credentials",
			setup: func(t *testing.T, rt *runtime, _ *casKVHost) {
				seed(t, rt, subscriptionRecord{ID: "l", Name: "l", Source: subscriptionSourceVPNCore})
				seedFleetLive(t, rt, fleetRecord("f"))
			},
			method: "save", code: refusedMixedOwnerCreds,
			payload: func(*testing.T, *runtime) any {
				return saveOf(subscriptionRecord{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"f", "l"}})
			},
		},
		{
			name:   "file over a fleet source on save",
			setup:  func(t *testing.T, rt *runtime, _ *casKVHost) { seedFleetLive(t, rt, fleetRecord("f")) },
			method: "save", code: refusedFleetFileUnavailable,
			payload: func(*testing.T, *runtime) any {
				return saveOf(subscriptionRecord{ID: "cfg", Name: "cfg", Kind: kindFile, NodeSource: "f", Content: "proxies: []"})
			},
		},
		{
			name: "file over a fleet source on restore",
			setup: func(t *testing.T, rt *runtime, _ *casKVHost) {
				seed(t, rt, remote("p"), subscriptionRecord{ID: "cfg", Name: "cfg", Kind: kindFile, NodeSource: "p", Content: "proxies: []"})
				if res := callSubscription(t, rt, "delete", map[string]any{"subscription_id": "cfg"}); !res.OK {
					t.Fatal(res.Error)
				}
				// p becomes a fleet record while the file is archived.
				if _, res := saveViaMethod(t, rt, fleetRecord("p"), ""); !res.OK {
					t.Fatal(res.Error)
				}
				promoteStaged(t, rt, "p")
			},
			method: "restore", code: refusedFleetFileUnavailable,
			payload: func(*testing.T, *runtime) any { return map[string]any{"subscription_id": "cfg"} },
		},
		{
			name: "file over a fleet source on migrate_record",
			setup: func(t *testing.T, rt *runtime, _ *casKVHost) {
				seed(t, rt, subscriptionRecord{ID: "l", Name: "l", Source: subscriptionSourceVPNCore}, subscriptionRecord{ID: "cfg", Name: "cfg", Kind: kindFile, NodeSource: "l", Content: "proxies: []"})
			},
			method: "migrate_record", code: refusedFleetFileUnavailable,
			payload: func(*testing.T, *runtime) any { return map[string]any{"subscription_id": "l"} },
		},
		{
			name: "link-mode script on a fleet record", method: "save", code: refusedScriptLink,
			setup: func(*testing.T, *runtime, *casKVHost) {},
			payload: func(*testing.T, *runtime) any {
				rec := fleetRecord("f")
				rec.Process = []json.RawMessage{json.RawMessage(`{"type":"Script Operator","args":{"mode":"link","content":"` + providerSecretURL + `"}}`)}
				return saveOf(rec)
			},
		},
		{
			name:   "delete requires a plan",
			setup:  func(t *testing.T, rt *runtime, _ *casKVHost) { seedFleetLive(t, rt, fleetRecord("f")) },
			method: "delete", code: refusedDeleteRequiresPlan,
			payload: func(*testing.T, *runtime) any { return map[string]any{"subscription_id": "f"} },
		},
		{
			name: "promotion pending",
			setup: func(t *testing.T, rt *runtime, host *casKVHost) {
				seedFleetLive(t, rt, fleetRecord("f"))
				halfDonePromotion(t, rt, host, fleetToLocal("f"))
			},
			method: "save", code: refusedPromotionPending,
			payload: func(*testing.T, *runtime) any { return saveOf(remote("f")) },
		},
		{
			name:   "staged revision mismatch",
			setup:  func(t *testing.T, rt *runtime, _ *casKVHost) { seedFleetLive(t, rt, fleetRecord("f")) },
			method: "discard_staged", code: refusedStagedMismatch,
			payload: func(*testing.T, *runtime) any {
				return map[string]any{"subscription_id": "f", "staged_revision": "rev-none"}
			},
		},
		{
			name: "store inconsistent",
			setup: func(t *testing.T, rt *runtime, host *casKVHost) {
				seed(t, rt, remote("r"))
				delete(host.values, recordKey("r"))
			},
			method: "save", code: refusedStoreInconsistent,
			payload: func(*testing.T, *runtime) any { return saveOf(remote("r")) },
		},
		{
			name: "kv conflict after the retry",
			setup: func(t *testing.T, rt *runtime, host *casKVHost) {
				seed(t, rt, remote("r"))
				host.beforePut = conflictingIndexWrites(t, rt, host, 2)
			},
			method: "save", code: refusedKVConflict,
			payload: func(*testing.T, *runtime) any {
				rec := remote("r")
				rec.DisplayName = "renamed"
				return saveOf(rec)
			},
		},
		{
			name: "regex incompatible", method: "save", code: refusedRegexIncompatible,
			setup: func(*testing.T, *runtime, *casKVHost) {},
			payload: func(*testing.T, *runtime) any {
				rec := remote("r")
				rec.Process = []json.RawMessage{json.RawMessage(`{"type":"Regex Filter","args":{"regex":["(?=HK)"]}}`)}
				return saveOf(rec)
			},
		},
		{
			name: "store migration required",
			setup: func(t *testing.T, _ *runtime, host *casKVHost) {
				seedLegacyStore(t, host.kvHostCaller, []subscriptionRecord{remote("r")})
			},
			method: "save", code: refusedMigrationRequired,
			payload: func(*testing.T, *runtime) any { return saveOf(remote("s")) },
		},
		{
			name: "structured args invalid", method: "save", code: refusedStructuredArgs,
			setup: func(*testing.T, *runtime, *casKVHost) {},
			payload: func(*testing.T, *runtime) any {
				rec := fleetRecord("f")
				rec.Process = []json.RawMessage{json.RawMessage(`{"type":"Structured Filter","args":{"predicates":[{"field":"` + providerSecretURL + `","op":"in","values":["x"]}]}}`)}
				return saveOf(rec)
			},
		},
		{
			name: "probe unavailable",
			setup: func(t *testing.T, _ *runtime, host *casKVHost) {
				page, _ := json.Marshal(model.LineCatalogueResponse{CatalogueVersion: fleetTestVersion, Rows: []model.LineCatalogueRow{}, Unavailable: []string{model.LineCatalogueUnavailableProbe}})
				host.rpc[fleetCatalogueService+"/"+fleetCatalogueMethod] = page
			},
			method: "save", code: refusedProbeUnavailable,
			payload: func(*testing.T, *runtime) any {
				rec := fleetRecord("f")
				rec.Process = []json.RawMessage{probeStructuredStep("probe_udp_ok")}
				return saveOf(rec)
			},
		},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, host := newCASRuntime(t)
			tc.setup(t, rt, host)
			res := callSubscription(t, rt, tc.method, tc.payload(t, rt))
			if !res.OK {
				t.Fatalf("%s answered an error, which the gateway would replace: %s", tc.method, res.Error)
			}
			r, refused := refusalOf(res)
			if !refused || r.Code != tc.code {
				t.Fatalf("%s = %s, want the structured refusal %s", tc.method, res.Result, tc.code)
			}
			for _, banned := range []string{"://", "secret-provider-token", "provider.invalid", "proxies: []", "killed before", "moved"} {
				if strings.Contains(r.Message, banned) {
					t.Fatalf("%s's message carries %q: %s", tc.code, banned, r.Message)
				}
			}
			if r.Message == "" || !strings.Contains(string(res.Result), `"saved":false`) {
				t.Fatalf("refusal shape = %s", res.Result)
			}
			seen[tc.code] = true
		})
	}
	for _, code := range []string{refusedFleetToLegacy, refusedLegacySourceRetired, refusedMixedOwnerCreds, refusedFleetFileUnavailable, refusedScriptLink, refusedDeleteRequiresPlan, refusedPromotionPending, refusedStagedMismatch, refusedStoreInconsistent, refusedKVConflict, refusedRegexIncompatible, refusedMigrationRequired, refusedStructuredArgs, refusedProbeUnavailable} {
		if !seen[code] {
			t.Errorf("no case drove %s", code)
		}
	}
}
