package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// A manifest declares which methods a plugin exposes and — since `backing` — who
// actually serves each one. This test holds the artifact to that promise.
//
// It exists because the promise used to go unchecked. Official plugins shipped signed
// manifests declaring interface methods their own artifacts could not answer, and
// lattice-server quietly answered them from an in-core handler instead. Nothing caught
// it: every suite covered what the artifact DOES, never what the manifest CLAIMS. A
// contract nobody verifies is a contract that drifts, and this is the gate that turns
// that drift into a red build.
//
// Sub-Store is genuinely runtime-backed: its own artifact serves every method it
// declares. This test is what keeps that true.
func TestManifestInterfacesAreServedAsDeclared(t *testing.T) {
	for _, iface := range loadManifestInterfaces(t) {
		for _, method := range iface.Methods {
			// Some no-argument methods legitimately reach the host (for example
			// endpoint_status reads the encrypted vault). The conformance probe only
			// needs to prove the artifact recognises the manifest-declared method, so
			// the stub host denies calls without making any external side effect.
			rt := &runtime{host: denyHostCalls{}}
			payload, err := json.Marshal(map[string]any{
				"service": iface.Service,
				"method":  method.Name,
				"payload": map[string]any{},
			})
			if err != nil {
				t.Fatalf("marshal call payload: %v", err)
			}
			resp := rt.handle(request{Action: "call", Payload: payload})
			served := !refusedAsUnknown(resp)

			switch iface.Backing {
			case "runtime":
				// This artifact is the declared owner, so it must at least recognise the
				// method. Rejecting an empty payload is a real answer; not knowing the
				// method at all is a broken promise.
				if !served {
					t.Errorf("%s/%s is declared runtime-backed, but this artifact does not serve it: %s",
						iface.Service, method.Name, resp.Error)
				}
			case "core":
				// The engine would live in lattice-server. If the artifact answers as well,
				// the manifest names two owners for one method and the host has to guess.
				if served {
					t.Errorf("%s/%s is declared core-backed, but this artifact answers it too; backing must name exactly one owner",
						iface.Service, method.Name)
				}
			case "":
				t.Errorf("%s/%s declares no backing, so who serves it is left to inference",
					iface.Service, method.Name)
			default:
				t.Errorf("%s/%s declares unknown backing %q", iface.Service, method.Name, iface.Backing)
			}
		}
	}
}

func TestManifestRuntimeMethodsCarryAckedBudgets(t *testing.T) {
	seen := map[string]bool{}
	for _, iface := range loadManifestInterfaces(t) {
		if iface.Backing != "runtime" {
			continue
		}
		for _, method := range iface.Methods {
			key := iface.Service + "/" + method.Name
			want, ok := ackedRuntimeBudgets()[key]
			if !ok {
				t.Errorf("%s is runtime-backed but has no acked budget table entry", key)
				continue
			}
			seen[key] = true
			if method.Budget == nil {
				t.Errorf("%s is runtime-backed but declares no budget", key)
				continue
			}
			if *method.Budget != want {
				t.Errorf("%s budget drifted from acked table: got %+v want %+v", key, *method.Budget, want)
			}
		}
	}
	for key := range ackedRuntimeBudgets() {
		if !seen[key] {
			t.Errorf("acked budget table entry %s is not declared as a runtime-backed manifest method", key)
		}
	}
}

// denyHostCalls refuses every brokered call without reaching a real host. The
// resulting method-specific error still proves the dispatcher recognised the
// manifest method; only "unsupported service/method" means the artifact lied.
type denyHostCalls struct{}

func (denyHostCalls) call(method string, _ any) (json.RawMessage, error) {
	return nil, fmt.Errorf("conformance host denied %s", method)
}

// refusedAsUnknown separates "I do not implement this" from "I implement this and your
// payload is wrong". Only the former means the artifact cannot serve the method — a
// validation error proves the method is wired up.
func refusedAsUnknown(resp response) bool {
	if resp.OK {
		return false
	}
	return strings.Contains(resp.Error, "unsupported action") ||
		strings.Contains(resp.Error, "unsupported service") ||
		strings.Contains(resp.Error, "unsupported method")
}

type manifestInterface struct {
	Service string `json:"service"`
	Backing string `json:"backing"`
	Methods []struct {
		Name   string            `json:"name"`
		Effect string            `json:"effect"`
		Scopes []string          `json:"scopes"`
		Budget *invokeBudgetSpec `json:"budget,omitempty"`
	} `json:"methods"`
}

func TestManifestKeepsCredentialBearingSubscriptionMethodsOnAdminScope(t *testing.T) {
	want := map[string][]string{
		"fetch":  {"substore:admin"},
		"render": {"substore:admin"},
		// convert returns only what the caller sent, reshaped, but what the
		// core sends is one identity's credentials; it is declared at the
		// render path's scope, which is the core's own.
		"convert": {"substore:admin"},
		"probe":   {"substore:read"},
		"preview": {"substore:read"},
	}
	for _, iface := range loadManifestInterfaces(t) {
		if iface.Service != pluginID+"/subscription" {
			continue
		}
		for _, method := range iface.Methods {
			expected, ok := want[method.Name]
			if ok && !reflect.DeepEqual(method.Scopes, expected) {
				t.Errorf("%s scopes=%v want=%v", method.Name, method.Scopes, expected)
			}
		}
	}
}

type invokeBudgetSpec struct {
	TimeoutMS   int `json:"timeout_ms"`
	StdoutBytes int `json:"stdout_bytes"`
	StderrBytes int `json:"stderr_bytes"`
	HostCalls   int `json:"host_calls"`
	// HTTPResponseBytes bounds the body of each http.do or http.operator.do
	// response the method receives. Absent means the host's default, 256 KiB.
	HTTPResponseBytes int `json:"http_response_bytes,omitempty"`
}

// ackedRuntimeBudgets is the table the signed manifest is pinned to, and the
// one the host-call tests hold this code to. Host calls are the measured
// worst reachable path, on a split store and on one that has not migrated
// yet, plus the script HTTP allowance (scriptHTTPMaxCalls) on the two
// methods that give scripts a network, render and publish; for those two the
// budget is exact, so one more call is refused
// (TestWorstHostCallPathsSetTheSignedBudgets). The S1 store split re-priced
// the record paths: a collection reads each member's own record (64 at most),
// a save writes a record and the index, and a delete archives.
//
// http_response_bytes is 8 MiB, the provider fetch's own cap
// (maxProviderResponseBytes), on every method that can reach a provider body:
// fetch and probe (fetchSubscription), render and publish (a file's remote
// template on every render, a collection's members when no snapshot is
// held), and preview and preview_draft (a provider record or draft resolved
// live). Each of them would otherwise refuse, at the host's 256 KiB default,
// a body that fetch stores (TestProviderFetchingMethodsAreSignedForAProviderBody).
// Until the host response frame rises past 4 MiB, a body reaches the plugin
// only up to about 3 MiB after base64 (design 28). The bound is per method,
// not per host call, so in render and publish it also covers the scripts'
// own requests; the script gateway holds those to 256 KiB per response itself
// (scriptHTTPMaxResponseBytes), and publish's send answers with a status.
func ackedRuntimeBudgets() map[string]invokeBudgetSpec {
	return map[string]invokeBudgetSpec{
		// 2026-08-18: convert and transform_response went from zero host calls to
		// twelve when scripts gained a network. Sub-Store's scripting model
		// assumes one: its own Resolve Domain operator speaks DoH and user
		// scripts fetch rulesets and quota endpoints, and every one of those
		// requests is a host call. Twelve is the gateway's per-invocation limit
		// of eight plus room for the plumbing around it; the timeout follows for
		// the same reason, since a fetching script spends its budget waiting on
		// someone else's server rather than on CPU.
		pluginID + "/engine/convert":            {TimeoutMS: 30_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 12},
		pluginID + "/engine/transform_response": {TimeoutMS: 30_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 12},
		pluginID + "/engine/save_pipeline":      {TimeoutMS: 2_000, StdoutBytes: 2 << 20, StderrBytes: 16 << 10, HostCalls: 2},
		pluginID + "/engine/get_pipeline":       {TimeoutMS: 2_000, StdoutBytes: 1 << 20, StderrBytes: 32 << 10, HostCalls: 1},
		pluginID + "/engine/list_pipelines":     {TimeoutMS: 1_000, StdoutBytes: 128 << 10, StderrBytes: 16 << 10, HostCalls: 1},
		pluginID + "/engine/delete_pipeline":    {TimeoutMS: 2_000, StdoutBytes: 2 << 20, StderrBytes: 16 << 10, HostCalls: 2},
		// run_pipeline is convert plus the stored chain's own read.
		pluginID + "/engine/run_pipeline": {TimeoutMS: 30_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 13},
		// render feeds a public subscription endpoint, so its stdout budget matches
		// the other conversion methods: a large subscription must fail loudly rather
		// than arrive truncated at a client. host_calls covers the heaviest shape
		// the store can express: a script file whose node source is a collection
		// of 64 tagged provider records that name no user agent, on a store that
		// has not migrated. That is the file's record miss, the legacy document,
		// the file's program key, the source record, the listing the tags need,
		// Settings for the default agent, 64 member records and 64 provider
		// fetches (134; 132 on a split store), plus the 8 calls scripts may spend.
		// 2026-08-11: the old allowance of 2 priced a real script-file render out,
		// and the public share for one would have 502'd on its first request.
		// The timeout is the host maximum because the runner spawns the plugin
		// per invocation and a cold QuickJS/wazero boot cost about 13.5s on the
		// production box (measured 2026-08-11).
		pluginID + "/subscription/render": {TimeoutMS: 30_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 142, HTTPResponseBytes: 8 << 20},
		// 2026-10-02: convert is the stateless converter for per-identity
		// links. Zero host calls is the point: the runner then refuses any KV
		// or network access, so the method cannot read or keep state even by
		// mistake. stdout matches render's because the document is the same
		// size; the timeout is render's because a call that lands while the
		// worker's warm runtime is still booting takes the isolated path.
		pluginID + "/subscription/convert": {TimeoutMS: 30_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 0},
		// fetch carries a provider's whole response, so its stdout budget is
		// the 8 MiB the fetch path itself caps at (maxProviderResponseBytes),
		// as its HTTP response budget is. host_calls: the refresh of the
		// render shape above is that render's 134 reads plus the bookkeeping
		// read and write, 136 on a store that has not migrated and 134 on a
		// split one. fetch gives scripts no
		// network, so the 4 left over are headroom. The timeout is the host
		// maximum: 64 sequential provider fetches cannot promise less.
		pluginID + "/subscription/fetch": {TimeoutMS: 30_000, StdoutBytes: 8 << 20, StderrBytes: 64 << 10, HostCalls: 140, HTTPResponseBytes: 8 << 20},
		// probe is the console's row check (its Refresh button, on every kind
		// of record): fetchSubscription without the bookkeeping, so the same
		// provider bodies and fetch's resolution less its read and write, 134
		// on a store that has not migrated. It answers a byte count, not the
		// body, and gives scripts no network; the 4 left over are fetch's
		// headroom. The old allowance of 2 priced only a graph record, and a
		// provider record that names no agent needs 3.
		pluginID + "/subscription/probe": {TimeoutMS: 20_000, StdoutBytes: 64 << 10, StderrBytes: 64 << 10, HostCalls: 138, HTTPResponseBytes: 8 << 20},
		// operators returns a fixed catalog and touches nothing, so it gets the
		// smallest budget in the file and zero host calls.
		pluginID + "/subscription/operators":     {TimeoutMS: 2_000, StdoutBytes: 64 << 10, StderrBytes: 16 << 10, HostCalls: 0},
		pluginID + "/subscription/graph_options": {TimeoutMS: 5_000, StdoutBytes: 6 << 20, StderrBytes: 16 << 10, HostCalls: 1},
		// preview runs the pipeline but returns only names and types, so its
		// stdout is far smaller than a conversion's even for a large subscription.
		// A combination preview renders its members live: the record, the
		// listing for tag members, Settings for the default agent, one record
		// and one provider fetch per member, 132 at worst on a store that has
		// not migrated. Scripts get no network here, so the rest is headroom.
		// Its timeout matches render's for the same cold-engine reason: 15s
		// still timed out a script file on production (about 13.5s boot plus
		// the work itself).
		pluginID + "/subscription/preview": {TimeoutMS: 30_000, StdoutBytes: 1 << 20, StderrBytes: 64 << 10, HostCalls: 138, HTTPResponseBytes: 8 << 20},
		// preview_draft is preview plus the one live resolve of a source the
		// CALLER named, which is why it is declared substore:admin and preview
		// is not. Same shape, same ceiling.
		pluginID + "/subscription/preview_draft": {TimeoutMS: 30_000, StdoutBytes: 1 << 20, StderrBytes: 64 << 10, HostCalls: 138, HTTPResponseBytes: 8 << 20},
		// list reads the index, plus the legacy document on a store that has not
		// migrated (2). The index carries more per row than the old listing
		// (bookkeeping, node counts, flags), and at 300 records it needs the
		// 512 KiB (TestIndexAt300RecordsFitsListBudget).
		pluginID + "/subscription/list": {TimeoutMS: 2_000, StdoutBytes: 512 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		// get returns one whole record. A script file carries its program inline
		// now, so the ceiling is 1 MiB. host_calls is 3 for a script file on a
		// store that has not migrated: its key's miss, the legacy document and
		// the legacy program key. 2026-08-11: duplicating a script file in the
		// UI 502'd here with the budget at 1; get is duplicate's first step.
		pluginID + "/subscription/get": {TimeoutMS: 2_000, StdoutBytes: 4 << 20, StderrBytes: 16 << 10, HostCalls: 3},
		// save reads the index, writes the record and writes the index; an
		// existing record is read first for its provenance and the conditional
		// check, and a graph record reloads the options that validate its
		// selection (5 at worst, one spare). stdout is unchanged from the single
		// document store: 4 MiB is what the response frame of a large record
		// needs. (2026-08-11: the first production import died at 512 KiB.)
		// delete archives: the index, the record, the archive write, the record
		// key's deletion and the index write.
		pluginID + "/subscription/save":   {TimeoutMS: 5_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 6},
		pluginID + "/subscription/delete": {TimeoutMS: 5_000, StdoutBytes: 4 << 20, StderrBytes: 16 << 10, HostCalls: 5},
		// migrate is import's shape plus three fetches from the standalone
		// Sub-Store it imports from, so it gets the longest timeout.
		pluginID + "/subscription/migrate": {TimeoutMS: 30_000, StdoutBytes: 8 << 20, StderrBytes: 64 << 10, HostCalls: 325},
		// publish renders (render's shape, 135 with the send on a store that has
		// not migrated) and sends once, plus the script allowance. The rendered
		// body leaves in the http.operator.do host_call frame, which core counts
		// as stdout: up to maxPublishBytes as base64, beside the script requests.
		pluginID + "/subscription/publish": {TimeoutMS: 30_000, StdoutBytes: 8 << 20, StderrBytes: 64 << 10, HostCalls: 143, HTTPResponseBytes: 8 << 20},
		// export reads the index, every record and Settings (N + 2); a store that
		// migrated from an oversized legacy document can hold 300 records (302).
		// Before migration it reads the legacy document and one program key per
		// script file instead.
		pluginID + "/subscription/export": {TimeoutMS: 5_000, StdoutBytes: 4 << 20, StderrBytes: 32 << 10, HostCalls: 320},
		// import reads the index, writes each record and the index once, and
		// writes Settings: N + 3, which 320 covers for the largest store export
		// produces (300 records).
		pluginID + "/subscription/import":        {TimeoutMS: 30_000, StdoutBytes: 8 << 20, StderrBytes: 64 << 10, HostCalls: 320},
		pluginID + "/subscription/get_settings":  {TimeoutMS: 1_000, StdoutBytes: 16 << 10, StderrBytes: 16 << 10, HostCalls: 1},
		pluginID + "/subscription/save_settings": {TimeoutMS: 1_000, StdoutBytes: 16 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		// depends_on reads the index, and the legacy document on a store that
		// has not migrated, so core's fleet re-render gets an answer either way.
		pluginID + "/subscription/depends_on": {TimeoutMS: 2_000, StdoutBytes: 256 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		// apply_revision answers its stated refusal before any host call: no
		// revision is staged before S2. Zero makes the runner refuse KV and
		// network access outright; S2 signs the count its staging needs.
		pluginID + "/subscription/apply_revision": {TimeoutMS: 5_000, StdoutBytes: 64 << 10, StderrBytes: 16 << 10, HostCalls: 0},
		// restore: the index, the archive, the record write, the archive's
		// deletion and the index write; it answers with the record. purge: the
		// index, the archive's deletion and the index write. reorder: the index
		// and its write.
		pluginID + "/subscription/restore": {TimeoutMS: 5_000, StdoutBytes: 6 << 20, StderrBytes: 16 << 10, HostCalls: 5},
		pluginID + "/subscription/purge":   {TimeoutMS: 5_000, StdoutBytes: 1 << 20, StderrBytes: 16 << 10, HostCalls: 3},
		pluginID + "/subscription/reorder": {TimeoutMS: 5_000, StdoutBytes: 1 << 20, StderrBytes: 16 << 10, HostCalls: 2},
		// migrate_store: a chunk of 64 script files reads the document, the
		// index miss and 64 programs and writes 64 records and the index; the
		// verify runs in a later call that writes no record (store_migrate.go).
		// stdout_bytes is the host maximum because core counts the kv.put
		// frames, base64 values included: a chunk stops before its record
		// frames pass migrateChunkFrameBytes, and the verify call rewrites the
		// index twice and the legacy document once. restore, purge and reorder
		// write the index (and restore a record) the same way.
		pluginID + "/subscription/migrate_store": {TimeoutMS: 30_000, StdoutBytes: 8 << 20, StderrBytes: 64 << 10, HostCalls: 140},
	}
}

func loadManifestInterfaces(t *testing.T) []manifestInterface {
	t.Helper()
	raw, err := os.ReadFile("../manifest.json")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		Interfaces []manifestInterface `json:"interfaces"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(m.Interfaces) == 0 {
		t.Fatal("manifest declares no interfaces to verify")
	}
	return m.Interfaces
}
