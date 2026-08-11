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
		// than arrive truncated at a client. host_calls is 2 rather than 0 because a
		// remote-backed subscription will read its stored snapshot through the host.
		pluginID + "/subscription/render": {TimeoutMS: 10_000, StdoutBytes: 6 << 20, StderrBytes: 64 << 10, HostCalls: 2},
		// fetch carries a provider's whole response, so its stdout budget is the
		// 8 MiB the fetch path itself caps at, and its timeout is longer because a
		// third-party provider is slower than local conversion.
		pluginID + "/subscription/fetch": {TimeoutMS: 20_000, StdoutBytes: 8 << 20, StderrBytes: 64 << 10, HostCalls: 2},
		// operators returns a fixed catalog and touches nothing, so it gets the
		// smallest budget in the file and zero host calls.
		pluginID + "/subscription/operators": {TimeoutMS: 2_000, StdoutBytes: 64 << 10, StderrBytes: 16 << 10, HostCalls: 0},
		// preview runs the pipeline but returns only names and types, so its
		// stdout is far smaller than a conversion's even for a large subscription.
		pluginID + "/subscription/preview": {TimeoutMS: 15_000, StdoutBytes: 1 << 20, StderrBytes: 64 << 10, HostCalls: 1},
		// list returns definitions without their content, so it stays small.
		pluginID + "/subscription/list": {TimeoutMS: 2_000, StdoutBytes: 256 << 10, StderrBytes: 16 << 10, HostCalls: 1},
		// get returns one whole record including inline content, so its ceiling
		// is the per-record inline cap plus room for the rest of the record —
		// not the small `list` ceiling, which carries no content at all.
		pluginID + "/subscription/get":    {TimeoutMS: 2_000, StdoutBytes: 512 << 10, StderrBytes: 16 << 10, HostCalls: 1},
		pluginID + "/subscription/save":   {TimeoutMS: 5_000, StdoutBytes: 512 << 10, StderrBytes: 64 << 10, HostCalls: 3},
		pluginID + "/subscription/delete": {TimeoutMS: 5_000, StdoutBytes: 64 << 10, StderrBytes: 16 << 10, HostCalls: 2},
		// migrate is the only write here and it talks to a second server, so it
		// gets the longest timeout. host_calls is 48: records persist as ONE
		// document write, but every script file's program is its own key — the
		// operator's real migration carried sixteen — plus three upstream
		// fetches and the doc reads. 2026-08-11: the per-record path priced a
		// real migration past the old allowance of 4 and died mid-flight.
		pluginID + "/subscription/migrate": {TimeoutMS: 30_000, StdoutBytes: 256 << 10, StderrBytes: 64 << 10, HostCalls: 48},
		// export carries every record including inline content, so it gets the
		// largest read budget here; import is bounded by what it accepts.
		// publish renders and sends; its stdout is only a small result object
		// because the rendered body goes out over the network, not back up stdout.
		pluginID + "/subscription/publish":       {TimeoutMS: 20_000, StdoutBytes: 64 << 10, StderrBytes: 64 << 10, HostCalls: 2},
		pluginID + "/subscription/export":        {TimeoutMS: 5_000, StdoutBytes: 4 << 20, StderrBytes: 32 << 10, HostCalls: 2},
		// import shares migrate's shape without the upstream fetches: one
		// document write plus one key per script program. 48 covers a full 256
		// record store restore where every file is a script.
		pluginID + "/subscription/import":        {TimeoutMS: 10_000, StdoutBytes: 256 << 10, StderrBytes: 64 << 10, HostCalls: 48},
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
