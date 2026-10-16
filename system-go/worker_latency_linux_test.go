//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// TestWorkerBundleCallLatency records, for the built worker
// LATTICE_WORKER_BINARY names, how long the first and the later convert
// calls that reach the embedded bundle take through framed stdin: a Tier 2
// target (Loon) over 64 share links, the call that ran on the warm runtime
// until S2 and runs on a fresh isolated runtime seeded from the lazily
// compiled bytecode since. It asserts nothing about time; it is the harness
// S2 plan section 5's "before" and "after" pairs are measured with, against
// the 0.17.0-alpha.2 release binary and the branch's own build
// (testdata/bench/s2-before-after-linux-arm64.txt).
func TestWorkerBundleCallLatency(t *testing.T) {
	bin := os.Getenv(workerBinaryEnv)
	if bin == "" {
		t.Skipf("%s names no built plugin binary", workerBinaryEnv)
	}
	hostResponses, hostSide, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer hostSide.Close()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "LATTICE_RUNTIME_PROTOCOL="+latticeplugin.RuntimeProtocolStdioJSONV2, "LATTICE_RUNTIME_GENERATION=1")
	cmd.ExtraFiles = []*os.File{hostResponses}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	hostResponses.Close()
	defer func() {
		stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	frames := bufio.NewReader(stdout)
	next := func() (kind, id string, res latticeplugin.Response) {
		t.Helper()
		line, err := frames.ReadBytes('\n')
		if err != nil {
			t.Fatalf("worker stdout ended: %v", err)
		}
		var f struct {
			Kind         string                 `json:"kind"`
			InvocationID string                 `json:"invocation_id"`
			Response     latticeplugin.Response `json:"response"`
		}
		if err := json.Unmarshal(line, &f); err != nil {
			t.Fatalf("not a frame: %.200s", line)
		}
		return f.Kind, f.InvocationID, f.Response
	}
	if kind, _, _ := next(); kind != "runtime_ready" {
		t.Fatalf("first frame %q", kind)
	}
	t.Logf("runtime_ready after %s", time.Since(started).Round(time.Millisecond))
	uris := perfgen.URIs(64)
	for i := 1; i <= 4; i++ {
		id := strconv.Itoa(i)
		payload := mustJSON(callPayload{Service: pluginID + "/subscription", Method: "convert", Payload: mustJSON(model.ConvertRequest{URIs: uris, Target: "Loon"})})
		invoke := map[string]any{"protocol": 2, "kind": "invoke", "generation": 1, "invocation_id": id, "request": request{Action: latticeplugin.ActionCall, Payload: payload}}
		start := time.Now()
		if err := json.NewEncoder(stdin).Encode(invoke); err != nil {
			t.Fatal(err)
		}
		var result *latticeplugin.Response
		for {
			kind, got, res := next()
			if got != id {
				t.Fatalf("frame %q for invocation %q while %s was in flight", kind, got, id)
			}
			if kind == "invoke_result" {
				result = &res
			}
			if kind == "invoke_ready" {
				break
			}
		}
		if result == nil || !result.OK {
			t.Fatalf("convert %d failed: %+v", i, result)
		}
		t.Logf("bundle convert %d (64 links to Loon): %s", i, time.Since(start).Round(time.Millisecond))
	}
}
