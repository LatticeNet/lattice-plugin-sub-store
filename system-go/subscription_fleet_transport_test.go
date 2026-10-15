package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// fleetFramed drives the real stdio-json-v2 transport with production's
// runtime options, as store_framed_test.go's harness does, and answers the
// plugin's host calls from any test host (KV and the line catalogue), frame
// by frame. stdoutBytes counts what one invocation wrote the way core counts
// it against the method's signed stdout_bytes: every frame, host calls
// included, plus its newline.
type fleetFramed struct {
	in          *io.PipeWriter
	host        *io.PipeWriter
	scanner     *bufio.Scanner
	done        chan error
	answer      hostCaller
	stdoutBytes int
	largestOut  int
}

func startFleetFramed(t *testing.T, answer hostCaller) *fleetFramed {
	t.Helper()
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	hostReader, hostWriter := io.Pipe()
	opts := pluginRuntimeOptions(inReader, outWriter)
	opts.OpenHostFromEnv = false
	opts.Host = latticeplugin.NewHostClient(latticeplugin.HostClientOptions{Output: outWriter, Responses: hostReader})
	rt := latticeplugin.NewRuntime(opts)
	f := &fleetFramed{in: inWriter, host: hostWriter, done: make(chan error, 1), answer: answer}
	f.scanner = bufio.NewScanner(outReader)
	f.scanner.Buffer(make([]byte, 64<<10), 64<<20)
	base := &runtime{engine: testEngineWithHeadroom()}
	go func() { f.done <- rt.ServeV2(context.Background(), invocationHandler(base), 7) }()
	t.Cleanup(func() {
		_ = inWriter.Close()
		_ = hostWriter.Close()
		_ = outReader.Close()
	})
	if frame, err := f.read(); err != nil || frame.Kind != "runtime_ready" {
		t.Fatalf("runtime_ready: frame=%+v err=%v", frame, err)
	}
	return f
}

func (f *fleetFramed) read() (framedWire, error) {
	type scanned struct {
		frame framedWire
		size  int
		err   error
	}
	result := make(chan scanned, 1)
	go func() {
		if !f.scanner.Scan() {
			result <- scanned{err: fmt.Errorf("transport closed: %v", f.scanner.Err())}
			return
		}
		var frame framedWire
		err := json.Unmarshal(f.scanner.Bytes(), &frame)
		result <- scanned{frame: frame, size: len(f.scanner.Bytes()) + 1, err: err}
	}()
	select {
	case got := <-result:
		f.stdoutBytes += got.size
		f.largestOut = max(f.largestOut, got.size)
		return got.frame, got.err
	case err := <-f.done:
		return framedWire{}, fmt.Errorf("runtime exited: %v", err)
	case <-time.After(2 * time.Minute):
		return framedWire{}, fmt.Errorf("no frame within two minutes")
	}
}

// invoke sends one subscription call and serves its host calls until the
// invocation ends, failing past the method's signed stdout_bytes as core
// would kill the worker.
func (f *fleetFramed) invoke(invocation, method string, payload any) (latticeplugin.Response, error) {
	service := pluginID + "/subscription"
	call := mustJSON(callPayload{Service: service, Method: method, Payload: mustJSON(payload)})
	frame := map[string]any{"protocol": 2, "kind": "invoke", "generation": 7, "invocation_id": invocation, "request": request{Action: latticeplugin.ActionCall, Payload: call}}
	go func() { _ = json.NewEncoder(f.in).Encode(frame) }()
	f.stdoutBytes = 0
	budget, signed := ackedRuntimeBudgets()[service+"/"+method]
	if !signed {
		return latticeplugin.Response{}, fmt.Errorf("%s/%s has no signed budget to enforce", service, method)
	}
	var response latticeplugin.Response
	for {
		wire, err := f.read()
		if err != nil {
			return latticeplugin.Response{}, err
		}
		if budget.StdoutBytes > 0 && f.stdoutBytes > budget.StdoutBytes {
			return latticeplugin.Response{}, fmt.Errorf("plugin wrote %d bytes, over the signed stdout limit %d", f.stdoutBytes, budget.StdoutBytes)
		}
		switch wire.Kind {
		case "host_call":
			var hc struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(wire.HostCall, &hc); err != nil {
				return latticeplugin.Response{}, err
			}
			var params any
			_ = json.Unmarshal(hc.Params, &params)
			result, err := f.answer.call(hc.Method, params)
			if err != nil {
				return latticeplugin.Response{}, err
			}
			if result == nil {
				result = json.RawMessage(`{"ok":true}`)
			}
			reply, _ := json.Marshal(map[string]any{
				"protocol": 2, "kind": "host_response", "generation": 7, "invocation_id": invocation, "host_call_id": wire.HostCallID,
				"host_response": map[string]any{"id": hc.ID, "ok": true, "result": result},
			})
			if _, err := f.host.Write(append(reply, '\n')); err != nil {
				return latticeplugin.Response{}, err
			}
		case "invoke_result":
			response = wire.Response
		case "stderr_complete":
		case "invoke_ready":
			return response, nil
		default:
			return latticeplugin.Response{}, fmt.Errorf("unexpected frame %q", wire.Kind)
		}
	}
}

// paddedRows is n catalogue lines whose node tags carry pad bytes each, so a
// test can size a plan: tags travel in every plan node's Lattice fields.
func paddedRows(n, pad int) []model.LineCatalogueRow {
	rows := make([]model.LineCatalogueRow, 0, n)
	for i := 1; i <= n; i++ {
		row := fleetTestRow(i, "vless")
		if pad > 0 {
			row.NodeTags = append(row.NodeTags, fmt.Sprintf("pad-%d-", i)+strings.Repeat("x", pad))
		}
		rows = append(rows, row)
	}
	return rows
}

// planBytesFor is the encoded plan of a plain fleet record over rows.
func planBytesFor(t *testing.T, rows []model.LineCatalogueRow) (fleetPlanOutput, error) {
	t.Helper()
	rt := &runtime{engine: testEngineWithHeadroom()}
	return rt.buildFleetPlan(fleetPlanInput{Label: "subscription \"cap\"", Record: subscriptionRecord{ID: "cap", Source: subscriptionSourceFleet, Revision: "r-cap"},
		CatalogueVersion: fleetTestVersion, Rows: projectRows(rows), Target: "sing-box"})
}

// TestRenderPlanAtCapFitsStdoutBudget pushes a 4096-node plan sized just
// under model.MaxRenderPlanBytes through the real framed transport, as a
// staged render that reads its rows from the catalogue over host calls, and
// asserts the whole invocation stays inside render's signed stdout_bytes:
// the cap leaves room for the reply's other fields and every frame.
func TestRenderPlanAtCapFitsStdoutBudget(t *testing.T) {
	const n = 4096
	base, err := planBytesFor(t, paddedRows(n, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Each byte of tag padding adds about one byte per node; aim 64 KiB under
	// the cap and walk down until the plan fits.
	pad := max(0, (model.MaxRenderPlanBytes-64<<10-len(base.Encoded))/n)
	var rows []model.LineCatalogueRow
	var plan fleetPlanOutput
	for ; ; pad -= 8 {
		rows = paddedRows(n, pad)
		if plan, err = planBytesFor(t, rows); err == nil {
			break
		}
		if !strings.HasPrefix(err.Error(), "subscription \"cap\": "+codePlanTooLarge) || pad <= 0 {
			t.Fatal(err)
		}
	}
	if len(plan.Encoded) < model.MaxRenderPlanBytes-256<<10 {
		t.Fatalf("calibrated plan is %d bytes, not near the %d cap", len(plan.Encoded), model.MaxRenderPlanBytes)
	}

	host := newFleetCatalogueHost(rows)
	rt := &runtime{host: host, engine: testEngineWithHeadroom()}
	saveFleetRecord(t, rt, subscriptionRecord{ID: "cap", Process: []json.RawMessage{renameStep("tokyo", "tokyo")}})
	staged := stageRecord(t, rt, subscriptionRecord{ID: "cap", Source: subscriptionSourceFleet})
	f := startFleetFramed(t, host)
	response, err := f.invoke("1", "render", map[string]any{"subscription_id": "cap", "target": "sing-box", "revision": staged})
	if err != nil {
		t.Fatalf("a plan at the cap broke the transport: %v", err)
	}
	if !response.OK {
		t.Fatalf("render = %+v", response.Error)
	}
	var reply renderReply
	if err := json.Unmarshal(response.Result, &reply); err != nil {
		t.Fatal(err)
	}
	t.Logf("plan %d bytes (cap %d); invocation wrote %d of %d stdout bytes", len(reply.Plan), model.MaxRenderPlanBytes, f.stdoutBytes, 6<<20)
	if len(reply.Plan) < model.MaxRenderPlanBytes-256<<10 || len(reply.Plan) > model.MaxRenderPlanBytes {
		t.Fatalf("the rendered plan is %d bytes", len(reply.Plan))
	}
}

// boundNodes is a plan's nodes as core sends them to convert after binding:
// every placeholder replaced by an identity credential of the same shape,
// each node carrying pad extra bytes in its tags (a Lattice field convert
// strips), so a test can size the request.
func boundNodes(t *testing.T, plan fleetPlanOutput, pad int) []json.RawMessage {
	t.Helper()
	decoded, err := model.DecodeSelectionPlan(plan.Encoded)
	if err != nil {
		t.Fatal(err)
	}
	nodes := make([]json.RawMessage, 0, len(decoded.Nodes))
	for i, node := range decoded.Nodes {
		text := string(node.Node)
		for field, placeholder := range node.Placeholders {
			text = strings.ReplaceAll(text, placeholder, fmt.Sprintf("%08x-1111-4111-8111-%012x", i, len(field)))
		}
		if pad > 0 {
			var fields map[string]any
			if err := json.Unmarshal([]byte(text), &fields); err != nil {
				t.Fatal(err)
			}
			tags, _ := fields["tags"].([]any)
			fields["tags"] = append(tags, strings.Repeat("t", pad))
			raw, _ := json.Marshal(fields)
			text = string(raw)
		}
		nodes = append(nodes, json.RawMessage(text))
	}
	return nodes
}

// TestConvertRequestAt6MiBThroughTransport pushes a bound convert request
// just under model.MaxConvertRequestBytes, the largest core may send, through
// the real framed transport, and asserts it converts inside convert's signed
// stdout_bytes with no Lattice field or placeholder in the document, which
// closes the transport half of KI-24.
func TestConvertRequestAt6MiBThroughTransport(t *testing.T) {
	const n = 4096
	plan, err := planBytesFor(t, paddedRows(n, 0))
	if err != nil {
		t.Fatal(err)
	}
	encode := func(pad int) []byte {
		raw, err := model.EncodeConvertRequest(model.ConvertRequest{Target: "ClashMeta", Format: "plain", Nodes: boundNodes(t, plan, pad)})
		if err != nil {
			return nil
		}
		return raw
	}
	base := encode(0)
	if base == nil {
		t.Fatal("the unpadded request does not encode")
	}
	pad := (model.MaxConvertRequestBytes - 64<<10 - len(base)) / n
	request := encode(pad)
	for request == nil && pad > 0 {
		pad -= 16
		request = encode(pad)
	}
	if len(request) < model.MaxConvertRequestBytes-256<<10 {
		t.Fatalf("calibrated request is %d bytes, not near the %d bound", len(request), model.MaxConvertRequestBytes)
	}
	var payload map[string]any
	if err := json.Unmarshal(request, &payload); err != nil {
		t.Fatal(err)
	}
	f := startFleetFramed(t, denyHostCalls{})
	response, err := f.invoke("1", "convert", payload)
	if err != nil {
		t.Fatalf("a %d byte convert request broke the transport: %v", len(request), err)
	}
	if !response.OK {
		t.Fatalf("convert = %s", response.Error)
	}
	var out subscriptionConvertResult
	if err := json.Unmarshal(response.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.NodeCount != n || strings.Contains(out.Content, "line_uuid") || strings.Contains(out.Content, "LATTICE-SYN-") || strings.Contains(out.Content, "ttttttttt") {
		t.Fatalf("convert produced %d nodes, or a Lattice field or placeholder reached the document", out.NodeCount)
	}
	t.Logf("convert request %d bytes (bound %d); invocation wrote %d stdout bytes", len(request), model.MaxConvertRequestBytes, f.stdoutBytes)
}

// TestPlanAndConvertRequestAt4096Under6MiB pins the slice's size bound: a
// 4096-node plan and the convert request core builds from it after binding
// are each under 6 MiB, the plan under the render cap. It also records the
// production row shape: there the snapshot's raw bound stops a record near
// 2660 lines (TestFleetEnvelopeRowsFitRawBound), and a plan over that many
// production lines stays under the cap too, while 4096 of them would not.
func TestPlanAndConvertRequestAt4096Under6MiB(t *testing.T) {
	plan, err := planBytesFor(t, paddedRows(4096, 0))
	if err != nil {
		t.Fatalf("a 4096-node plan: %v", err)
	}
	request, err := model.EncodeConvertRequest(model.ConvertRequest{Target: "sing-box", Format: "plain", Nodes: boundNodes(t, plan, 0)})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("4096 nodes: plan %d bytes (cap %d), convert request %d bytes (bound %d)", len(plan.Encoded), model.MaxRenderPlanBytes, len(request), model.MaxConvertRequestBytes)
	if plan.NodeCount != 4096 || len(plan.Encoded) > model.MaxRenderPlanBytes || len(request) > 6<<20 {
		t.Fatalf("plan %d nodes, %d bytes; convert request %d bytes", plan.NodeCount, len(plan.Encoded), len(request))
	}

	production := func(n int) []model.LineCatalogueRow {
		rows := make([]model.LineCatalogueRow, 0, n)
		for i := 1; i <= n; i++ {
			rows = append(rows, fleetProductionRow(i))
		}
		return rows
	}
	ceiling, err := planBytesFor(t, production(2661))
	if err != nil {
		t.Fatalf("a plan at the envelope ceiling of production rows: %v", err)
	}
	full, err := planBytesFor(t, production(4096))
	if err == nil || !strings.Contains(err.Error(), codePlanTooLarge) {
		t.Fatalf("4096 production rows: %d bytes, err %v; the recorded figure moved", len(full.Encoded), err)
	}
	t.Logf("production shape: 2661 lines plan %d bytes; 4096 lines refused (%v)", len(ceiling.Encoded), err)
}
