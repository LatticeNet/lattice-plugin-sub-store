package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	rt := &runtime{host: denyHostCalls{}, engine: sharedWarmTestEngine(t)}
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
	rt := &runtime{host: denyHostCalls{}, engine: sharedWarmTestEngine(t)}
	uris := strings.Split(strings.TrimSpace(shareFleetFixtureNoSS(1)), "\n")
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"no input", map[string]any{"target": "URI"}, "uris or raw"},
		{"both inputs", map[string]any{"uris": uris, "raw": "ss://x", "target": "URI"}, "uris or raw"},
		{"no target", map[string]any{"uris": uris}, "target"},
		{"unknown target", map[string]any{"uris": uris, "target": "Netscape"}, "target"},
		// Operators would mean user JavaScript, which never runs on the shared
		// warm runtime; convert has no chain at all and says so.
		{"operators", map[string]any{"uris": uris, "target": "URI", "operators": []any{}}, "unknown field"},
		{"multi-line uri", map[string]any{"uris": []string{uris[0] + "\n" + uris[1]}, "target": "URI"}, "one line"},
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
	}
}

// The isolation guarantee per-identity links rest on. convert runs on the warm
// runtime, which persists across calls and records, so it has to be shown that
// nothing one call converts survives into another call's document. Twelve
// converts with distinct credentials, cycling through targets so the same
// producer runs back to back with different input, all on one warm runtime:
// each document carries its own credentials and none of the others'. Then the
// runtime itself is searched: no reachable global holds any of them, and the
// runtime's only cross-call store (the script environment's in-memory
// $persistentStore) was never written.
func TestConvertCallsOnOneWarmRuntimeNeverCarryEachOthersCredentials(t *testing.T) {
	engine := testEngineWithHeadroom()
	if err := engine.prewarm(); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	if _, err, served := engine.runWarm("probe", "lattice-test-store-spy.js", `(function () {
  globalThis.__latticeStoreWrites = 0;
  const write = globalThis.$persistentStore.write;
  globalThis.$persistentStore.write = function (data, key) {
    globalThis.__latticeStoreWrites += 1;
    return write(data, key);
  };
  return JSON.stringify({ ok: true });
})()`); err != nil || !served {
		t.Fatalf("install store spy: served=%v err=%v", served, err)
	}
	rt := &runtime{host: denyHostCalls{}, engine: engine}

	const n = 12
	targets := []string{"ClashMeta", "sing-box", "URI", "ClashMeta", "Stash", "V2Ray", "Loon", "sing-box", "QX", "Egern", "Shadowrocket", "JSON"}
	secrets := make([][]string, n)
	for i := 0; i < n; i++ {
		uuid := fmt.Sprintf("%08d-aaaa-4bbb-8ccc-%012d", 7000+i, i)
		pass := fmt.Sprintf("hy2k%02dz", i)
		secrets[i] = []string{uuid, pass}
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
		out := decodeConvert(t, callConvert(t, rt, map[string]any{"uris": uris, "target": targets[i], "format": "plain"}))
		document := out.Content
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
					t.Fatalf("call %d (%s) carries call %d's credential %q", i, targets[i], j, secret)
				}
			}
		}
	}
	warm, isolated := engine.pathCounts()
	if isolated != 0 || warm < n {
		t.Fatalf("converts must all land on the warm runtime: warm=%d isolated=%d", warm, isolated)
	}

	needles, _ := json.Marshal(all)
	probe := `(function () {
  const needles = ` + string(needles) + `;
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
  return JSON.stringify({ found, visited, writes: globalThis.__latticeStoreWrites });
})()`
	type probeReport struct {
		Found   []string `json:"found"`
		Visited int      `json:"visited"`
		Writes  int      `json:"writes"`
	}
	runProbe := func() probeReport {
		t.Helper()
		raw, err, served := engine.runWarm("probe", "lattice-test-leak-probe.js", probe)
		if err != nil || !served {
			t.Fatalf("leak probe: served=%v err=%v", served, err)
		}
		var report probeReport
		if err := json.Unmarshal([]byte(raw), &report); err != nil {
			t.Fatalf("decode probe: %v", err)
		}
		return report
	}
	report := runProbe()
	if len(report.Found) != 0 {
		t.Fatalf("credentials reachable from the warm runtime's globals after the calls: %v", report.Found)
	}
	if report.Writes != 0 {
		t.Fatalf("convert wrote %d entries into the runtime's cross-call store", report.Writes)
	}
	if report.Visited < 100 {
		t.Fatalf("the probe walked only %d objects; it did not reach the core", report.Visited)
	}
	t.Logf("leak probe walked %d objects reachable from globalThis; store writes %d", report.Visited, report.Writes)

	// Control: the probe is not vacuous. A credential parked two levels deep
	// under the core's own global, and one store write, must both be seen.
	if _, err, served := engine.runWarm("probe", "lattice-test-plant.js", `(function () {
  globalThis.SubStoreProxyUtils.__latticePlanted = { nested: { value: "x-`+secrets[3][0]+`-x" } };
  globalThis.$persistentStore.write("planted", "lattice-test-key");
  return JSON.stringify({ ok: true });
})()`); err != nil || !served {
		t.Fatalf("plant control: served=%v err=%v", served, err)
	}
	control := runProbe()
	if len(control.Found) != 1 || control.Found[0] != secrets[3][0] || control.Writes != 1 {
		t.Fatalf("the probe missed a planted credential or store write: %+v", control)
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
