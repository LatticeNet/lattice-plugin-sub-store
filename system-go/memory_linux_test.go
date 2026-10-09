//go:build linux

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/perfgen"
	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// workerBinaryEnv names the built plugin binary TestWorkerVmRSS starts. The
// memory CI job builds it the way the release does and sets it; without it
// the test skips, so the race suite does not pay for a build and four
// renders.
const workerBinaryEnv = "LATTICE_WORKER_BINARY"

// TestWorkerVmRSS records the plugin worker's resident set (S1 plan section
// 5.2). It starts the built binary under the stdio-json-v2 environment the
// host gives a worker (main.go, parseRuntimeV2Environment), drives 4096-node
// sing-box renders through framed stdin with the subscription convert
// method, which makes no host call, and reads VmRSS and VmHWM from
// /proc/<pid>/status: at runtime_ready, once the resident set has settled
// after the background warm-up, and after each render. The numbers are
// recorded, not enforced, until S2 (section 7); the test fails only when the
// worker does not answer.
func TestWorkerVmRSS(t *testing.T) {
	bin := os.Getenv(workerBinaryEnv)
	if bin == "" {
		t.Skipf("%s names no built plugin binary", workerBinaryEnv)
	}
	// fd 3 is the host response channel (LATTICE_HOST_RESPONSE_FD defaults
	// to it). convert never reads it; it is open so the worker starts as it
	// does under the host.
	hostResponses, hostSide, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer hostSide.Close()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "LATTICE_RUNTIME_PROTOCOL="+latticeplugin.RuntimeProtocolStdioJSONV2, "LATTICE_RUNTIME_GENERATION=1")
	cmd.ExtraFiles = []*os.File{hostResponses}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	hostResponses.Close()
	defer func() {
		stdin.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}()
	pid := cmd.Process.Pid
	frames := bufio.NewReader(stdout)
	type frame struct {
		Kind         string                 `json:"kind"`
		InvocationID string                 `json:"invocation_id"`
		Response     latticeplugin.Response `json:"response"`
	}
	next := func() frame {
		t.Helper()
		line, err := frames.ReadBytes('\n')
		if err != nil {
			t.Fatalf("worker stdout ended (%v); stderr:\n%s", err, stderr.String())
		}
		var f frame
		if err := json.Unmarshal(line, &f); err != nil {
			t.Fatalf("worker wrote a line that is not a frame (%v): %.200s", err, line)
		}
		return f
	}
	record := func(label string) {
		t.Helper()
		rss, hwm := procStatusKiB(t, pid, "VmRSS"), procStatusKiB(t, pid, "VmHWM")
		t.Logf("VmRSS %7.1f MiB  VmHWM %7.1f MiB  %s", float64(rss)/1024, float64(hwm)/1024, label)
	}

	if f := next(); f.Kind != "runtime_ready" {
		t.Fatalf("first frame is %q, want runtime_ready", f.Kind)
	}
	record("at runtime_ready")
	settled := waitForSettledRSS(t, pid, 60*time.Second)
	record(fmt.Sprintf("idle, once VmRSS settled after the background warm-up (%s)", settled))

	uris := perfgen.URIs(4096)
	for i := 1; i <= 3; i++ {
		id := strconv.Itoa(i)
		payload := mustJSON(callPayload{Service: pluginID + "/subscription", Method: "convert", Payload: mustJSON(model.ConvertRequest{URIs: uris, Target: "sing-box"})})
		invoke := map[string]any{"protocol": 2, "kind": "invoke", "generation": 1, "invocation_id": id, "request": request{Action: latticeplugin.ActionCall, Payload: payload}}
		start := time.Now()
		if err := json.NewEncoder(stdin).Encode(invoke); err != nil {
			t.Fatal(err)
		}
		var result *latticeplugin.Response
		for {
			f := next()
			if f.InvocationID != id {
				t.Fatalf("frame %q for invocation %q while %s was in flight", f.Kind, f.InvocationID, id)
			}
			if f.Kind == "invoke_result" {
				r := f.Response
				result = &r
			}
			if f.Kind == "invoke_ready" {
				break
			}
		}
		if result == nil || !result.OK {
			t.Fatalf("render %d did not succeed: %+v; stderr:\n%s", i, result, stderr.String())
		}
		var converted subscriptionConvertResult
		if err := json.Unmarshal(result.Result, &converted); err != nil || converted.NodeCount == 0 {
			t.Fatalf("render %d result: %v, %d nodes", i, err, converted.NodeCount)
		}
		record(fmt.Sprintf("after render %d of %d nodes to sing-box (%d nodes written, %d bytes, %s)", i, len(uris), converted.NodeCount, len(converted.Content), time.Since(start).Round(time.Millisecond)))
	}
}

// procStatusKiB reads one kB field of /proc/<pid>/status.
func procStatusKiB(t *testing.T, pid int, field string) int {
	t.Helper()
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if rest, ok := strings.CutPrefix(line, field+":"); ok {
			kib, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(rest), " kB"))
			if err != nil {
				t.Fatalf("%s: %q", field, line)
			}
			return kib
		}
	}
	t.Fatalf("/proc/%d/status has no %s", pid, field)
	return 0
}

// waitForSettledRSS waits until VmRSS has moved by less than 1 MiB over two
// seconds, or until limit, and says which.
func waitForSettledRSS(t *testing.T, pid int, limit time.Duration) string {
	t.Helper()
	const step, window = 200 * time.Millisecond, 10
	start := time.Now()
	var samples []int
	for time.Since(start) < limit {
		samples = append(samples, procStatusKiB(t, pid, "VmRSS"))
		if n := len(samples); n > window {
			lo, hi := samples[n-1], samples[n-1]
			for _, s := range samples[n-1-window:] {
				lo, hi = min(lo, s), max(hi, s)
			}
			if hi-lo < 1024 {
				return "settled after " + time.Since(start).Round(100*time.Millisecond).String()
			}
		}
		time.Sleep(step)
	}
	return "still moving after " + limit.String()
}
