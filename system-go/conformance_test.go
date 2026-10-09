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
}

func ackedRuntimeBudgets() map[string]invokeBudgetSpec {
	return map[string]invokeBudgetSpec{
		// 2026-08-18: convert and transform_response went from zero host calls to
		// twelve when scripts gained a network. Sub-Store's scripting model
		// assumes one — its own Resolve Domain operator speaks DoH and user
		// scripts fetch rulesets and quota endpoints — and every one of those
		// requests is a host call. Twelve is the gateway's per-invocation limit
		// of eight plus room for the plumbing around it; the timeout follows for
		// the same reason, since a fetching script spends its budget waiting on
		// someone else's server rather than on CPU.
		pluginID + "/engine/convert":            {TimeoutMS: 30_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 12},
		pluginID + "/engine/transform_response": {TimeoutMS: 30_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 12},
		pluginID + "/engine/save_pipeline":      {TimeoutMS: 2_000, StdoutBytes: 32 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		pluginID + "/engine/get_pipeline":       {TimeoutMS: 2_000, StdoutBytes: 1 << 20, StderrBytes: 32 << 10, HostCalls: 1},
		pluginID + "/engine/list_pipelines":     {TimeoutMS: 1_000, StdoutBytes: 128 << 10, StderrBytes: 16 << 10, HostCalls: 1},
		pluginID + "/engine/delete_pipeline":    {TimeoutMS: 2_000, StdoutBytes: 32 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		// run_pipeline is convert plus the stored chain's own read.
		pluginID + "/engine/run_pipeline": {TimeoutMS: 30_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 13},
		// render feeds a public subscription endpoint, so its stdout budget matches
		// the other conversion methods: a large subscription must fail loudly rather
		// than arrive truncated at a client. host_calls covers the heaviest shape the
		// store can express: a script file (document + program key = 2) drawing from
		// a collection (source record + member list = 2) whose members are all remote
		// — one provider fetch each, and maxCollectionMembers caps that at 64.
		// 2026-08-11: the old allowance of 2 priced a real script-file render out —
		// the public share for one would have 502'd on its first request.
		// timeout is 20s because the runner spawns the plugin per invocation and a
		// cold QuickJS/wazero boot costs ~13.5s on the production box (measured
		// 2026-08-11); 10s timed out every script-file render. The warm-engine
		// follow-up should let this come back down.
		pluginID + "/subscription/render": {TimeoutMS: 30_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 76},
		// 2026-10-02: convert is the stateless converter for per-identity
		// links. Zero host calls is the point: the runner then refuses any KV
		// or network access, so the method cannot read or keep state even by
		// mistake. stdout matches render's because the document is the same
		// size; the timeout is render's because a call that lands while the
		// worker's warm runtime is still booting takes the isolated path.
		pluginID + "/subscription/convert": {TimeoutMS: 30_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 0},
		// fetch carries a provider's whole response, so its stdout budget is the
		// 8 MiB the fetch path itself caps at. host_calls is 70: every record kind
		// resolves its variable content at refresh — a script file's read (2), the
		// source record and member list (2), one provider fetch per collection
		// member (maxCollectionMembers is 64), and the refresh bookkeeping's own
		// read and write (2). The timeout is the host maximum: 64 sequential
		// provider fetches cannot promise less.
		// 2026-08-18: +8 host calls on every script-capable path for the script
		// HTTP budget described above. fetch keeps 30s because that is the host
		// maximum — it was already at the ceiling, so its script allowance has
		// to fit inside the time the provider fetches leave rather than extend
		// the call.
		pluginID + "/subscription/fetch": {TimeoutMS: 30_000, StdoutBytes: 8 << 20, StderrBytes: 64 << 10, HostCalls: 78},
		pluginID + "/subscription/probe": {TimeoutMS: 20_000, StdoutBytes: 64 << 10, StderrBytes: 64 << 10, HostCalls: 2},
		// operators returns a fixed catalog and touches nothing, so it gets the
		// smallest budget in the file and zero host calls.
		pluginID + "/subscription/operators":     {TimeoutMS: 2_000, StdoutBytes: 64 << 10, StderrBytes: 16 << 10, HostCalls: 0},
		pluginID + "/subscription/graph_options": {TimeoutMS: 5_000, StdoutBytes: 6 << 20, StderrBytes: 16 << 10, HostCalls: 1},
		// preview runs the pipeline but returns only names and types, so its
		// stdout is far smaller than a conversion's even for a large subscription.
		// Its host_calls match render's: a combination preview renders its
		// members live, one provider fetch each up to maxCollectionMembers — a
		// graph preview's record read, eligibility reload and single compose
		// fit well inside the same allowance, and a file preview refuses
		// node-source work outright. Its timeout matches render's for the same
		// cold-engine reason — 15s still timed out a script file on production
		// (~13.5s boot plus the work itself).
		pluginID + "/subscription/preview": {TimeoutMS: 30_000, StdoutBytes: 1 << 20, StderrBytes: 64 << 10, HostCalls: 76},
		// preview_draft is preview plus the one live resolve of a source the
		// CALLER named, which is why it is declared substore:admin and preview
		// is not. Same shape, same ceiling.
		pluginID + "/subscription/preview_draft": {TimeoutMS: 30_000, StdoutBytes: 1 << 20, StderrBytes: 64 << 10, HostCalls: 76},
		// list returns definitions without their content, so it stays small.
		pluginID + "/subscription/list": {TimeoutMS: 2_000, StdoutBytes: 256 << 10, StderrBytes: 16 << 10, HostCalls: 1},
		// get returns one whole record including inline content, so its ceiling
		// is the per-record inline cap plus room for the rest of the record —
		// not the small `list` ceiling, which carries no content at all.
		// host_calls is 2: a script file's program lives under its own key, so
		// the document read alone is not the whole record. 2026-08-11: duplicating
		// a script file in the UI 502'd here — get is duplicate's first step.
		pluginID + "/subscription/get": {TimeoutMS: 2_000, StdoutBytes: 512 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		// save/delete write the whole records document back through one stdout
		// frame, and the runner caps a frame at stdout_bytes — with a populated
		// store the write frame is the document, base64'd. 4 MiB covers the 1 MiB
		// store cap with envelope headroom; a smaller number makes saves start
		// failing exactly when the store gets valuable. (2026-08-11: first
		// production import died here — 512 KiB fit one record, not twenty.)
		// host_calls is 3 for save: one document load, then either the program
		// key (script records) or the options reload that validates a graph
		// selection, then one document write — the dispatch reads provenance
		// from the document it already loaded rather than re-reading the
		// record twice more. delete is the same shape minus the program write
		// on plain records.
		pluginID + "/subscription/save":   {TimeoutMS: 5_000, StdoutBytes: 4 << 20, StderrBytes: 64 << 10, HostCalls: 3},
		pluginID + "/subscription/delete": {TimeoutMS: 5_000, StdoutBytes: 4 << 20, StderrBytes: 16 << 10, HostCalls: 3},
		// migrate is the only write here and it talks to a second server, so it
		// gets the longest timeout. host_calls is import's 260 plus three upstream
		// fetches. 2026-08-11: the per-record path priced a real migration past
		// the old allowance of 4 and died mid-flight; the batch path then carried
		// the operator's real sixteen-script migration under 48, and this number
		// extends the same cover to a full store.
		pluginID + "/subscription/migrate": {TimeoutMS: 30_000, StdoutBytes: 4 << 20, StderrBytes: 64 << 10, HostCalls: 263},
		// export carries every record including inline content, so it gets the
		// largest read budget here. host_calls is 258: the document, the settings
		// key, and one read per script program — a backup that leaves programs
		// behind cannot be restored, so they are reattached at export time.
		// publish renders (render's 68) and sends once; its stdout is only a
		// small result object because the rendered body goes out over the
		// network, not back up stdout.
		pluginID + "/subscription/publish": {TimeoutMS: 30_000, StdoutBytes: 64 << 10, StderrBytes: 64 << 10, HostCalls: 77},
		pluginID + "/subscription/export":  {TimeoutMS: 5_000, StdoutBytes: 4 << 20, StderrBytes: 32 << 10, HostCalls: 258},
		// import shares migrate's shape without the upstream fetches: the
		// existing-records read, the batch's document load, one key per script
		// program, one document write, one settings write. 260 covers a full
		// 256-record restore where every file is a script — the 48 it replaced
		// covered sixteen, not the 256 its comment claimed.
		pluginID + "/subscription/import":        {TimeoutMS: 30_000, StdoutBytes: 4 << 20, StderrBytes: 64 << 10, HostCalls: 260},
		pluginID + "/subscription/get_settings":  {TimeoutMS: 1_000, StdoutBytes: 16 << 10, StderrBytes: 16 << 10, HostCalls: 1},
		pluginID + "/subscription/save_settings": {TimeoutMS: 1_000, StdoutBytes: 16 << 10, StderrBytes: 16 << 10, HostCalls: 2},
	}
}

// waveRuntimeBudgets is the table the capability-wave manifest will sign
// (S1 plan sections 3.5 and 6), and the one the host-call tests hold this
// code to. The store split re-prices the record paths: a collection reads
// each member's record (64 at most), a save writes a record and the index,
// and a delete archives. ackedRuntimeBudgets stays equal to the manifest
// signed today, which this branch does not change; the wave commit replaces
// ackedRuntimeBudgets with this table and the manifest with the section 6
// diff. Until then the methods present here and absent from the manifest are
// served by the binary already (TestWaveMethodsAreServedBeforeTheManifestDeclaresThem).
//
// Host calls are the measured worst path plus the script HTTP allowance where
// the method grants scripts a network, so the "rejects one extra call" tests
// stay exact: render 130 + 8, publish 131 + 8, fetch 132 + 8 (the fetch path's
// member chains). preview and preview_draft keep render's ceiling, as they
// always have. depends_on is 2, not the plan's 1, so it still answers on a
// store that has not migrated (the index miss and the legacy document).
func waveRuntimeBudgets() map[string]invokeBudgetSpec {
	budgets := ackedRuntimeBudgets()
	set := func(method string, hostCalls, stdoutBytes int) {
		key := pluginID + "/subscription/" + method
		budget := budgets[key]
		budget.HostCalls = hostCalls
		if stdoutBytes > 0 {
			budget.StdoutBytes = stdoutBytes
		}
		budgets[key] = budget
	}
	set("list", 2, 512<<10)
	set("get", 3, 1<<20)
	set("save", 6, 0)
	set("delete", 5, 0)
	set("fetch", 140, 0)
	set("render", 138, 0)
	set("preview", 138, 0)
	set("preview_draft", 138, 0)
	set("publish", 139, 0)
	set("export", 320, 0)
	set("import", 320, 0)
	set("migrate", 325, 0)
	for method, budget := range waveOnlyRuntimeBudgets() {
		budgets[pluginID+"/subscription/"+method] = budget
	}
	return budgets
}

// waveOnlyRuntimeBudgets are the runtime methods the wave adds.
func waveOnlyRuntimeBudgets() map[string]invokeBudgetSpec {
	return map[string]invokeBudgetSpec{
		"depends_on":     {TimeoutMS: 2_000, StdoutBytes: 256 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		"apply_revision": {TimeoutMS: 5_000, StdoutBytes: 64 << 10, StderrBytes: 16 << 10, HostCalls: 4},
		"restore":        {TimeoutMS: 5_000, StdoutBytes: 1 << 20, StderrBytes: 16 << 10, HostCalls: 5},
		"purge":          {TimeoutMS: 5_000, StdoutBytes: 64 << 10, StderrBytes: 16 << 10, HostCalls: 3},
		"reorder":        {TimeoutMS: 5_000, StdoutBytes: 64 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		"migrate_store":  {TimeoutMS: 30_000, StdoutBytes: 64 << 10, StderrBytes: 64 << 10, HostCalls: 140},
	}
}

// The wave's new methods must be served before the manifest declares them:
// the manifest is signed once, and a signed method the artifact does not
// recognise is the broken promise TestManifestInterfacesAreServedAsDeclared
// exists to catch. This is that test for the methods still waiting on the
// signing, and it fails the day one of them is declared without being
// dropped from the wave-only list.
func TestWaveMethodsAreServedBeforeTheManifestDeclaresThem(t *testing.T) {
	declared := map[string]bool{}
	for _, iface := range loadManifestInterfaces(t) {
		for _, method := range iface.Methods {
			declared[iface.Service+"/"+method.Name] = true
		}
	}
	for method := range waveOnlyRuntimeBudgets() {
		if declared[pluginID+"/subscription/"+method] {
			t.Errorf("%s is declared in the manifest now; move its budget into ackedRuntimeBudgets", method)
			continue
		}
		rt := &runtime{host: denyHostCalls{}}
		payload, err := json.Marshal(map[string]any{"service": pluginID + "/subscription", "method": method, "payload": map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		if resp := rt.handle(request{Action: "call", Payload: payload}); refusedAsUnknown(resp) {
			t.Errorf("%s is in the capability wave but this artifact does not serve it: %s", method, resp.Error)
		}
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
