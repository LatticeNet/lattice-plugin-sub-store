package main

import (
	"encoding/json"
	"fmt"
	"os"
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
		Budget *invokeBudgetSpec `json:"budget,omitempty"`
	} `json:"methods"`
}

type invokeBudgetSpec struct {
	TimeoutMS   int `json:"timeout_ms"`
	StdoutBytes int `json:"stdout_bytes"`
	StderrBytes int `json:"stderr_bytes"`
	HostCalls   int `json:"host_calls"`
}

func ackedRuntimeBudgets() map[string]invokeBudgetSpec {
	return map[string]invokeBudgetSpec{
		pluginID + "/engine/convert":            {TimeoutMS: 10_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 0},
		pluginID + "/engine/transform_response": {TimeoutMS: 10_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 0},
		pluginID + "/engine/save_pipeline":      {TimeoutMS: 2_000, StdoutBytes: 32 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		pluginID + "/engine/get_pipeline":       {TimeoutMS: 2_000, StdoutBytes: 1 << 20, StderrBytes: 32 << 10, HostCalls: 1},
		pluginID + "/engine/list_pipelines":     {TimeoutMS: 1_000, StdoutBytes: 128 << 10, StderrBytes: 16 << 10, HostCalls: 1},
		pluginID + "/engine/delete_pipeline":    {TimeoutMS: 2_000, StdoutBytes: 32 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		pluginID + "/engine/run_pipeline":       {TimeoutMS: 10_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 1},
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
		pluginID + "/subscription/render": {TimeoutMS: 20_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 68},
		// fetch carries a provider's whole response, so its stdout budget is the
		// 8 MiB the fetch path itself caps at, and its timeout is longer because a
		// third-party provider is slower than local conversion. host_calls is 4:
		// read the record, one network read, then the refresh bookkeeping is its
		// own document read and write. (The old 2 let the read happen and killed
		// the bookkeeping, so every refresh 502'd after succeeding.)
		pluginID + "/subscription/fetch": {TimeoutMS: 20_000, StdoutBytes: 8 << 20, StderrBytes: 64 << 10, HostCalls: 4},
		// operators returns a fixed catalog and touches nothing, so it gets the
		// smallest budget in the file and zero host calls.
		pluginID + "/subscription/operators": {TimeoutMS: 2_000, StdoutBytes: 64 << 10, StderrBytes: 16 << 10, HostCalls: 0},
		// preview runs the pipeline but returns only names and types, so its
		// stdout is far smaller than a conversion's even for a large subscription.
		// Its host_calls match render's: a file preview renders the file, node
		// source and all. Its timeout matches render's for the same cold-engine
		// reason — 15s still timed out a script file on production (~13.5s boot
		// plus the work itself).
		pluginID + "/subscription/preview": {TimeoutMS: 20_000, StdoutBytes: 1 << 20, StderrBytes: 64 << 10, HostCalls: 68},
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
		// host_calls is 3 for save: one document load, the program key when the
		// record is a script (or its clear when a script becomes something else),
		// one document write — the dispatch reads provenance from the document it
		// already loaded rather than re-reading the record twice more. delete is
		// the same shape minus the program write on plain records.
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
		pluginID + "/subscription/publish": {TimeoutMS: 20_000, StdoutBytes: 64 << 10, StderrBytes: 64 << 10, HostCalls: 69},
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
