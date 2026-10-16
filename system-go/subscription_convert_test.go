package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func callConvert(t *testing.T, rt *runtime, payload any) response {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal convert payload: %v", err)
	}
	return rt.handleSubscriptionCall(callPayload{Method: "convert", Payload: raw})
}

func decodeConvert(t *testing.T, res response) subscriptionConvertResult {
	t.Helper()
	if !res.OK {
		t.Fatalf("convert failed: %s", res.Error)
	}
	var out subscriptionConvertResult
	if err := json.Unmarshal(res.Result, &out); err != nil {
		t.Fatalf("decode convert result: %v", err)
	}
	return out
}

// convert is the stateless converter the core uses for per-identity links: a
// list of URIs (or any node text the engine parses) plus a target in, that
// client's document out. It touches no store, so it runs with a host that
// refuses every call.
func TestConvertTurnsURIsIntoTheClientDocumentWithoutTheHost(t *testing.T) {
	rt := &runtime{host: denyHostCalls{}, engine: testEngineWithHeadroom()}
	uris := strings.Split(strings.TrimSpace(shareFleetFixture(2)), "\n")

	meta := decodeConvert(t, callConvert(t, rt, map[string]any{"uris": uris, "target": "ClashMeta"}))
	if !strings.HasPrefix(meta.Content, "proxies:") || !strings.Contains(meta.Content, `"type":"vless"`) || !strings.Contains(meta.Content, `"type":"hysteria2"`) {
		t.Fatalf("ClashMeta document lost the fleet's nodes: %q", head(meta.Content, 160))
	}
	if meta.Target != "ClashMeta" || meta.NodeCount != len(uris) || !strings.Contains(meta.ContentType, "yaml") {
		t.Fatalf("ClashMeta reply target=%q nodes=%d type=%q", meta.Target, meta.NodeCount, meta.ContentType)
	}

	// The URI target keeps its classic base64 envelope by default, and plain
	// asks for the bare list.
	list := decodeConvert(t, callConvert(t, rt, map[string]any{"uris": uris, "target": "URI"}))
	decoded, err := base64.StdEncoding.DecodeString(list.Content)
	if err != nil || countURINodes(string(decoded)) != len(uris) {
		t.Fatalf("URI default must be the base64 list of %d nodes: %v %q", len(uris), err, head(string(decoded), 80))
	}
	plain := decodeConvert(t, callConvert(t, rt, map[string]any{"uris": uris, "target": "URI", "format": "plain"}))
	if countURINodes(plain.Content) != len(uris) {
		t.Fatalf("plain URI list = %q", head(plain.Content, 80))
	}

	// raw accepts any node text the engine parses, YAML included.
	yaml := "proxies:\n  - {name: y1, type: ss, server: 192.0.2.10, port: 8388, cipher: aes-128-gcm, password: pw}\n"
	box := decodeConvert(t, callConvert(t, rt, map[string]any{"raw": yaml, "target": "sing-box"}))
	var doc map[string]any
	if err := json.Unmarshal([]byte(box.Content), &doc); err != nil || box.NodeCount != 1 {
		t.Fatalf("sing-box from YAML: nodes=%d err=%v %q", box.NodeCount, err, head(box.Content, 80))
	}
}

func TestConvertRefusesWhatItCannotHonestlyServe(t *testing.T) {
	rt := &runtime{host: denyHostCalls{}, engine: testEngineWithHeadroom()}
	uris := strings.Split(strings.TrimSpace(shareFleetFixtureNoSS(1)), "\n")
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"no input", map[string]any{"target": "URI"}, "exactly one of uris, raw"},
		{"both inputs", map[string]any{"uris": uris, "raw": "ss://x", "target": "URI"}, "exactly one of uris, raw"},
		{"no target", map[string]any{"uris": uris}, "target"},
		{"unknown target", map[string]any{"uris": uris, "target": "Netscape"}, "target"},
		// Operators would mean user JavaScript, which never runs on the shared
		// warm runtime; convert has no chain at all and says so.
		{"operators", map[string]any{"uris": uris, "target": "URI", "operators": []any{}}, "unknown field"},
		{"multi-line uri", map[string]any{"uris": []string{uris[0] + "\n" + uris[1]}, "target": "URI"}, "one line"},
		// The plugin never sees a link's token: the core resolves the link and
		// sends only the identity's own URIs. A token in the payload is an
		// unknown field, refused before anything runs.
		{"link token", map[string]any{"uris": uris, "target": "URI", "token": "lat$convert-token-sentinel"}, "unknown field"},
		{"unknown format", map[string]any{"uris": uris, "target": "URI", "format": "zip"}, "format"},
		// VLESS and Hysteria2 only, for a client that carries neither.
		{"zero nodes for target", map[string]any{"uris": uris, "target": "Clash"}, zeroNodesForTargetCode},
		{"garbage", map[string]any{"raw": providerErrorPage, "target": "ClashMeta"}, zeroNodesForTargetCode},
	}
	for _, tc := range cases {
		res := callConvert(t, rt, tc.payload)
		if res.OK {
			t.Fatalf("%s: convert accepted it: %s", tc.name, head(string(res.Result), 80))
		}
		if !strings.Contains(res.Error, tc.want) {
			t.Fatalf("%s: error %q does not mention %q", tc.name, res.Error, tc.want)
		}
		// Errors reach the core's audit log; they never quote the input.
		if strings.Contains(res.Error, "convert-token-sentinel") || strings.Contains(res.Error, uris[0]) {
			t.Fatalf("%s: error quotes the input: %q", tc.name, res.Error)
		}
	}
}

// leakProbeScript walks every object reachable from globalThis for the
// needles and reports which it found, how many objects it visited and how
// many entries the script environment's in-memory $persistentStore holds
// under the probe's key.
func leakProbeScript(needles []string) string {
	encoded, _ := json.Marshal(needles)
	return `(function () {
  const needles = ` + string(encoded) + `;
  const seen = new Set();
  const found = [];
  const stack = [globalThis];
  let visited = 0;
  while (stack.length > 0 && visited < 2000000) {
    const value = stack.pop();
    if (value === null || (typeof value !== "object" && typeof value !== "function")) continue;
    if (seen.has(value)) continue;
    seen.add(value);
    visited += 1;
    let names = [];
    try { names = Object.getOwnPropertyNames(value); } catch (err) { continue; }
    for (const name of names) {
      let descriptor;
      try { descriptor = Object.getOwnPropertyDescriptor(value, name); } catch (err) { continue; }
      if (!descriptor || !("value" in descriptor)) continue;
      const item = descriptor.value;
      if (typeof item === "string") {
        for (const needle of needles) {
          if (item.indexOf(needle) !== -1 && found.indexOf(needle) === -1) found.push(needle);
        }
      } else if (item !== null && (typeof item === "object" || typeof item === "function")) {
        stack.push(item);
      }
    }
  }
  const stored = globalThis.$persistentStore.read("lattice-test-key");
  return JSON.stringify({ found, visited, stored: stored ? String(stored) : "" });
})()`
}

type leakProbeReport struct {
	Found   []string `json:"found"`
	Visited int      `json:"visited"`
	Stored  string   `json:"stored"`
}

func runLeakProbe(t *testing.T, engine *subStoreEngine, script string) leakProbeReport {
	t.Helper()
	raw, err := engine.runIsolatedScript("probe", "lattice-test-leak-probe.js", script)
	if err != nil {
		t.Fatalf("leak probe: %v", err)
	}
	var report leakProbeReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("decode probe: %v", err)
	}
	return report
}

// What one bundle call converts never survives into another call. The warm
// runtime S1 shared between scriptless calls is gone (S2 plan section 1.4),
// so the guarantee is now structural: every call builds a runtime, seeds it
// from the core's bytecode and discards it, and the bytecode is the only
// thing calls share. The test converts six inputs with distinct credentials
// across six producers, each document carrying its own and none of the
// others', and then shows the structure holds: a credential a call
// parks under the core's global and in the script environment's
// $persistentStore is visible to that call's own probe and to no later
// call's. That store is the only cross-call store a runtime ever had,
// because the runtime has no filesystem (the sandbox tests in
// substore_engine_sandbox_test.go hold that line).
func TestBundleCallsNeverCarryEachOthersCredentials(t *testing.T) {
	engine := testEngineWithHeadroom()
	targets := []string{"ClashMeta", "sing-box", "V2Ray", "Loon", "QX", "JSON"}
	n := len(targets)
	secrets := make([][]string, n)
	for i := 0; i < n; i++ {
		secrets[i] = []string{fmt.Sprintf("%08d-aaaa-4bbb-8ccc-%012d", 7000+i, i), fmt.Sprintf("hy2k%02dz", i)}
	}
	var all []string
	for _, s := range secrets {
		all = append(all, s...)
	}
	for i := 0; i < n; i++ {
		uris := []string{
			fmt.Sprintf("vless://%s@203.0.113.%d:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.example.com&fp=chrome&pbk=AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdefg&sid=0a&type=tcp#id-%02d", secrets[i][0], i+1, i),
			fmt.Sprintf("hysteria2://%s@198.51.100.%d:8443?sni=h2.example.com#id-%02d-hy2", secrets[i][1], i+1, i),
		}
		out, err := engine.convertIsolated(subStoreConversionRequest{Raw: strings.Join(uris, "\n"), Target: targets[i]})
		if err != nil {
			t.Fatalf("call %d (%s): %v", i, targets[i], err)
		}
		document := out.Output
		if targets[i] == "V2Ray" {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(document))
			if err != nil {
				t.Fatalf("call %d V2Ray: %v", i, err)
			}
			document = string(decoded)
		}
		carried := 0
		for _, own := range secrets[i] {
			if strings.Contains(document, own) {
				carried++
			}
		}
		if carried == 0 {
			t.Fatalf("call %d (%s) carries none of its own credentials: %q", i, targets[i], head(document, 160))
		}
		for j, other := range secrets {
			if j == i {
				continue
			}
			for _, secret := range other {
				if strings.Contains(document, secret) {
					t.Fatalf("call %d (%s) carries input %d's credential %q", i, targets[i], j, secret)
				}
			}
		}
	}

	// A later call finds none of the inputs' credentials.
	if report := runLeakProbe(t, engine, leakProbeScript(all)); len(report.Found) != 0 || report.Stored != "" || report.Visited < 100 {
		t.Fatalf("a later call's runtime: %+v; want nothing found, nothing stored, and the core walked", report)
	}
	// A call that parks a credential sees it in its own runtime, which shows
	// the probe is not vacuous, and the next call does not.
	planted := secrets[3][0]
	plant := `globalThis.SubStoreProxyUtils.__latticePlanted = { nested: { value: "x-` + planted + `-x" } };
globalThis.$persistentStore.write("planted", "lattice-test-key");
`
	own := runLeakProbe(t, engine, plant+leakProbeScript([]string{planted}))
	if len(own.Found) != 1 || own.Found[0] != planted || own.Stored != "planted" {
		t.Fatalf("the probe missed what its own call planted: %+v", own)
	}
	if next := runLeakProbe(t, engine, leakProbeScript([]string{planted})); len(next.Found) != 0 || next.Stored != "" {
		t.Fatalf("the next call inherited the planted credential or store entry: %+v", next)
	}
}

func TestManifestDeclaresConvertAsAStatelessAdminRead(t *testing.T) {
	for _, iface := range loadManifestInterfaces(t) {
		if iface.Service != pluginID+"/subscription" {
			continue
		}
		for _, method := range iface.Methods {
			if method.Name != "convert" {
				continue
			}
			if !reflect.DeepEqual(method.Scopes, []string{"substore:admin"}) {
				t.Fatalf("convert scopes = %v", method.Scopes)
			}
			if method.Budget == nil || method.Budget.HostCalls != 0 {
				t.Fatalf("convert must declare zero host calls, so the runner itself forbids any store or network access: %+v", method.Budget)
			}
			return
		}
	}
	t.Fatal("manifest does not declare subscription/convert")
}

// convert stays on the warm runtime because the pinned core keeps no input
// between calls, and the evidence for that is tied to one bundle: the
// isolation test above, and the source audit in
// tools/substore-core/state-audit.json, which lists every piece of
// module-scope state on the parse and produce path with the reason it holds
// no data. A re-pinned bundle has not been audited, so it fails here until
// tools/substore-core/audit-state.mjs passes against the new pin and
// state-audit.json names it.
func TestEmbeddedCoreIsTheStateAuditedCore(t *testing.T) {
	sum := sha256.Sum256([]byte(embeddedSubStoreCoreJS))
	embedded := hex.EncodeToString(sum[:])
	var pin struct {
		Commit       string `json:"commit"`
		OutputSHA256 string `json:"output_sha256"`
	}
	var audit struct {
		Commit       string `json:"commit"`
		OutputSHA256 string `json:"output_sha256"`
		Reviewed     []struct {
			File   string `json:"file"`
			Kind   string `json:"kind"`
			Reason string `json:"reason"`
		} `json:"reviewed"`
	}
	for path, out := range map[string]any{"../tools/substore-core/pin.json": &pin, "../tools/substore-core/state-audit.json": &audit} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if embedded != pin.OutputSHA256 {
		t.Fatalf("embedded core %s is not the pinned bundle %s", embedded, pin.OutputSHA256)
	}
	if audit.OutputSHA256 != embedded || audit.Commit != pin.Commit {
		t.Fatalf("state-audit.json reviews %s at %s, but the embedded core is %s at %s; run tools/substore-core/audit-state.mjs against the new pin and review every finding before convert may share the warm runtime", audit.OutputSHA256, audit.Commit, embedded, pin.Commit)
	}
	if len(audit.Reviewed) == 0 {
		t.Fatal("state-audit.json reviews nothing")
	}
	for _, entry := range audit.Reviewed {
		if reason := strings.TrimSpace(entry.Reason); reason == "" || strings.HasPrefix(reason, "TODO") {
			t.Fatalf("state-audit.json has no reason for the %s in %s", entry.Kind, entry.File)
		}
	}
}
