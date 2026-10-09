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

// framedRuntime drives the real stdio-json-v2 transport with production's
// runtime options (pluginRuntimeOptions, so hostFrameCap is the one that
// ships) and answers the plugin's host calls from an in-memory KV, frame by
// frame, the way the core's worker transport does.
type framedRuntime struct {
	t       *testing.T
	in      *io.PipeWriter
	host    *io.PipeWriter
	out     *io.PipeReader
	scanner *bufio.Scanner
	done    chan error
	kv      *kvHostCaller
	// largestHostResponse is the biggest host_response frame sent, so a test
	// can show it pushed a frame past the size that broke production.
	largestHostResponse int
	// stdoutBytes is what the current invocation wrote, counted as core
	// counts it against the method's signed stdout_bytes: every frame, host
	// calls included, plus its newline.
	stdoutBytes int
}

type framedWire struct {
	Protocol     int                    `json:"protocol"`
	Kind         string                 `json:"kind"`
	Generation   uint64                 `json:"generation"`
	InvocationID string                 `json:"invocation_id"`
	HostCallID   string                 `json:"host_call_id"`
	HostCall     json.RawMessage        `json:"host_call"`
	Response     latticeplugin.Response `json:"response"`
}

func startFramedRuntime(t *testing.T, kv *kvHostCaller) *framedRuntime {
	t.Helper()
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	hostReader, hostWriter := io.Pipe()
	opts := pluginRuntimeOptions(inReader, outWriter)
	opts.OpenHostFromEnv = false
	opts.Host = latticeplugin.NewHostClient(latticeplugin.HostClientOptions{Output: outWriter, Responses: hostReader})
	rt := latticeplugin.NewRuntime(opts)
	f := &framedRuntime{t: t, in: inWriter, host: hostWriter, out: outReader, done: make(chan error, 1), kv: kv}
	f.scanner = bufio.NewScanner(outReader)
	f.scanner.Buffer(make([]byte, 64<<10), 64<<20)
	base := &runtime{engine: sharedWarmTestEngine(t)}
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

func (f *framedRuntime) read() (framedWire, error) {
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
		return got.frame, got.err
	case err := <-f.done:
		return framedWire{}, fmt.Errorf("runtime exited: %v", err)
	case <-time.After(2 * time.Minute):
		return framedWire{}, fmt.Errorf("no frame within two minutes")
	}
}

// invoke sends one call and serves its host calls until the invocation ends.
func (f *framedRuntime) invoke(invocation, method string, payload any) (latticeplugin.Response, error) {
	service := pluginID + "/subscription"
	call := mustJSON(callPayload{Service: service, Method: method, Payload: mustJSON(payload)})
	frame := map[string]any{"protocol": 2, "kind": "invoke", "generation": 7, "invocation_id": invocation, "request": request{Action: latticeplugin.ActionCall, Payload: call}}
	// The pipe blocks until the runtime reads, and a runtime that refuses the
	// frame stops reading: the write runs beside the frame loop.
	go func() { _ = json.NewEncoder(f.in).Encode(frame) }()
	f.stdoutBytes = 0
	budget, signed := ackedRuntimeBudgets()[service+"/"+method]
	if !signed {
		return latticeplugin.Response{}, fmt.Errorf("%s/%s has no signed budget to enforce", service, method)
	}
	limit := budget.StdoutBytes
	var response latticeplugin.Response
	for {
		wire, err := f.read()
		if err != nil {
			return latticeplugin.Response{}, err
		}
		if limit > 0 && f.stdoutBytes > limit {
			return latticeplugin.Response{}, fmt.Errorf("plugin exceeded stdout limit %d", limit)
		}
		switch wire.Kind {
		case "host_call":
			var call struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(wire.HostCall, &call); err != nil {
				return latticeplugin.Response{}, err
			}
			var params any
			_ = json.Unmarshal(call.Params, &params)
			result, err := f.kv.call(call.Method, params)
			if err != nil {
				return latticeplugin.Response{}, err
			}
			if result == nil {
				result = json.RawMessage(`{"ok":true}`)
			}
			reply, _ := json.Marshal(map[string]any{
				"protocol": 2, "kind": "host_response", "generation": 7, "invocation_id": invocation, "host_call_id": wire.HostCallID,
				"host_response": map[string]any{"id": call.ID, "ok": true, "result": result},
			})
			f.largestHostResponse = max(f.largestHostResponse, len(reply))
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

// The overflow of 2026-08-11, reproduced on the store that replaces it: a
// legacy document of about 770 KiB is one kv.get whose base64 answer passes
// 1 MiB, which is where every call died in production. Through the real
// framed transport, migrate_store reads it, writes every record and the
// index, verifies and opens the store, and the records read back through the
// same transport.
func TestLegacyDocumentAtOverflowSizeMigratesThroughTheFramedTransport(t *testing.T) {
	kv := newKVHostCaller()
	var records []subscriptionRecord
	content := "ss://YWVzLTEyOC1nY206cHc@192.0.2.10:8388#" + strings.Repeat("n", 19<<10)
	for i := 0; i < 41; i++ {
		records = append(records, subscriptionRecord{ID: fmt.Sprintf("big-%02d", i), Name: fmt.Sprintf("big %d", i), Source: subscriptionSourceLocal, Content: content})
	}
	seedLegacyStore(t, kv, records)
	if size := len(kv.values[subscriptionRecordsKey]); size < 770<<10 || size > maxSubscriptionDocBytes {
		t.Fatalf("fixture document is %d bytes, want about 770 KiB", size)
	}

	f := startFramedRuntime(t, kv)
	response, err := f.invoke("1", "migrate_store", map[string]any{})
	if err != nil {
		t.Fatalf("migrate_store through the framed transport: %v", err)
	}
	if !response.OK {
		t.Fatalf("migrate_store refused: %s", response.Error)
	}
	if f.largestHostResponse <= 1<<20 {
		t.Fatalf("the largest host response was %d bytes; the fixture must push one past 1 MiB", f.largestHostResponse)
	}
	var reply migrateStoreReply
	if err := json.Unmarshal(response.Result, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Verified || reply.Migrated != len(records) || reply.Remaining != 0 {
		t.Fatalf("migrate_store = %+v, want all %d records migrated in one chunk", reply, len(records))
	}
	// The verify rewrites the legacy document, so it has a call of its own.
	response, err = f.invoke("2", "migrate_store", map[string]any{})
	if err != nil || !response.OK {
		t.Fatalf("the verify through the framed transport: err=%v response=%+v", err, response)
	}
	reply = migrateStoreReply{}
	if err := json.Unmarshal(response.Result, &reply); err != nil {
		t.Fatal(err)
	}
	if !reply.Done || !reply.Verified || reply.Migrated != 0 {
		t.Fatalf("the verify = %+v", reply)
	}
	got, err := f.invoke("3", "get", map[string]any{"subscription_id": "big-40"})
	if err != nil || !got.OK {
		t.Fatalf("get after migration: err=%v response=%+v", err, got)
	}
	var out struct {
		Subscription subscriptionRecord `json:"subscription"`
	}
	if err := json.Unmarshal(got.Result, &out); err != nil || out.Subscription.Content != content {
		t.Fatalf("migrated record did not read back whole: err=%v %d bytes", err, len(out.Subscription.Content))
	}
	if _, found := kv.values[recordKey("big-00")]; !found {
		t.Fatal("the migrated record is not under its own key")
	}
}

// A render request the core may send (a 4 MiB snapshot envelope JSON escaped
// inside a request of up to model.MaxSubscriptionRequestBytes) is answered,
// and the runtime goes on serving after it.
//
// The pinned SDK refuses any invoke frame over its DefaultMaxRequestBytes
// (1 MiB) in decodeInvokeV2, whatever Runtime.MaxRequestBytes says, and ends
// the runtime. That refusal is the SDK's, not this plugin's, so the test
// skips on exactly that error and starts asserting by itself once the SDK
// pin moves to a release that honours the runtime's frame cap.
func TestFramedTransportServesAFiveMiBRenderRequest(t *testing.T) {
	kv := newKVHostCaller()
	f := startFramedRuntime(t, kv)
	raw := strings.Repeat("x", 5<<20)
	response, err := f.invoke("1", "render", map[string]any{"subscription_id": "absent", "format": "plain", "raw": raw})
	if err != nil && strings.Contains(err.Error(), "invalid stdio-json-v2 frame") {
		t.Skip("lattice-sdk decodeInvokeV2 refuses invoke frames over DefaultMaxRequestBytes regardless of Runtime.MaxRequestBytes; hostFrameCap takes effect with the SDK fix")
	}
	if err != nil {
		t.Fatalf("a 5 MiB render request killed the transport: %v", err)
	}
	if response.OK || !strings.Contains(response.Error, "was not found") {
		t.Fatalf("render answered %+v, want the not-found refusal for its own record", response)
	}
	if next, err := f.invoke("2", "list", map[string]any{}); err != nil || !next.OK {
		t.Fatalf("the runtime stopped serving after the large request: err=%v response=%+v", err, next)
	}
}

// The frame cap covers the largest request the core builds: a render request
// at model.MaxSubscriptionRequestBytes and a convert request at
// model.MaxConvertRequestBytes, plus the invoke envelope around them.
func TestHostFrameCapCoversTheCoreRequestBounds(t *testing.T) {
	const envelope = 64 << 10
	for name, bound := range map[string]int{
		"render":  model.MaxSubscriptionRequestBytes,
		"convert": model.MaxConvertRequestBytes,
	} {
		if hostFrameCap < bound+envelope {
			t.Errorf("hostFrameCap %d does not hold a %s request of %d bytes plus its frame", hostFrameCap, name, bound)
		}
	}
	if got := pluginRuntimeOptions(nil, nil).MaxRequestBytes; got != hostFrameCap {
		t.Fatalf("production runtime options carry MaxRequestBytes %d, want hostFrameCap %d", got, hostFrameCap)
	}
}

// The payload checks follow the request bounds core enforces when it builds
// the request: convert at model.MaxConvertRequestBytes, preview at
// model.MaxSubscriptionRequestBytes. Both used to stop at the 4 MiB response
// bound and refuse requests core sends.
func TestPayloadBoundsFollowTheCoreRequestBounds(t *testing.T) {
	rt := &runtime{host: denyHostCalls{}, engine: sharedWarmTestEngine(t)}
	padded := func(size int) json.RawMessage {
		head := `{"target":"URI"`
		return json.RawMessage(head + strings.Repeat(" ", size-len(head)-1) + "}")
	}
	for name, test := range map[string]struct {
		method string
		fits   int
		over   int
	}{
		"convert": {method: "convert", fits: model.MaxSubscriptionResponseBytes + 1<<20, over: model.MaxConvertRequestBytes + 1},
		"preview": {method: "preview", fits: model.MaxSubscriptionResponseBytes + 1<<20, over: model.MaxSubscriptionRequestBytes + 1},
	} {
		fits := rt.handleSubscriptionCall(callPayload{Method: test.method, Payload: padded(test.fits)})
		if strings.Contains(fits.Error, "exceeds") {
			t.Errorf("%s refused a %d byte request core may send: %s", name, test.fits, fits.Error)
		}
		over := rt.handleSubscriptionCall(callPayload{Method: test.method, Payload: padded(test.over)})
		if over.OK || !strings.Contains(over.Error, "exceeds") {
			t.Errorf("%s accepted a %d byte request: %+v", name, test.over, over.Error)
		}
	}
}
