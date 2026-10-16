package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// The credential-absence sweep (S2 plan sections 4 and 8): a fleet
// credential exists in exactly two places, core and the plugin's convert.
// The sweep drives the real stdio-json-v2 transport the way core does,
// hands convert bound nodes carrying an identity's credentials for every
// protocol first, so the credentials are in the process, and then calls
// every other subscription method over fleet records, a collection, a
// staged revision and a legacy record under migration. Everything the
// plugin writes is grepped for the fixture's credential set: every frame on
// stdout (method replies, the snapshot a fetch answers, preview and render
// replies, every host call with the values it stores, stderr chunks), the
// whole KV store afterwards, and the message of a panic forced inside the
// engine while it holds the credentials. Only convert's own result frame
// may carry an identity credential; nothing may ever carry the owner
// credential a legacy envelope holds.
const (
	sweepIdentityUUID     = "a1b2c3d4-e5f6-4789-8abc-def012345678"
	sweepIdentityPassword = "s3cr3t/Pa55+w0rd=Id&x"
	sweepIdentityUser     = "identity-user.corp"
	sweepOwnerUUID        = "c0ffee00-1234-4567-89ab-cdef01234567"
	sweepOwnerPassword    = "0wner/s3cret+pw=Q"
)

// credentialDetector finds a secret in bytes in raw, URL-escaped (either
// escaping), JSON-escaped (once or twice), dashless and base64 forms, the
// last also when the secret sits inside a longer base64 run (a vmess link,
// a socks userinfo, a stored KV value).
type credentialDetector struct {
	secrets map[string]string // label to secret
}

var sweepBase64Run = regexp.MustCompile(`[A-Za-z0-9+/_-]{12,}={0,2}`)

// jsonUnescaper undoes the escapes encoding/json writes into a string. The
// \u escapes are spelled with \x5c, the backslash, so no tool that reads this
// file decodes them.
var jsonUnescaper = strings.NewReplacer("\x5cu0026", "&", "\x5cu003c", "<", "\x5cu003e", ">", `\"`, `"`, `\/`, "/", `\\`, `\`)

func (d credentialDetector) views(b []byte, depth int) [][]byte {
	text := string(b)
	seen := map[string]bool{}
	var out [][]byte
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, []byte(s))
		}
	}
	add(text)
	once := jsonUnescaper.Replace(text)
	add(once)
	add(jsonUnescaper.Replace(once))
	for _, v := range []string{text, once} {
		if q, err := url.QueryUnescape(v); err == nil {
			add(q)
		}
		if p, err := url.PathUnescape(v); err == nil {
			add(p)
		}
	}
	if depth > 0 {
		base := append([][]byte(nil), out...)
		for _, v := range base {
			// A run may begin with characters that are not part of the
			// base64 text ("//" after a scheme), so every alignment is tried.
			for _, run := range sweepBase64Run.FindAll(v, -1) {
				for k := 0; k < 4 && k < len(run); k++ {
					s := bytes.TrimRight(run[k:], "=")
					if len(s)%4 == 1 {
						s = s[:len(s)-1]
					}
					for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
						decoded, err := enc.DecodeString(string(s))
						if err != nil || len(decoded) < 4 {
							continue
						}
						out = append(out, d.views(decoded, depth-1)...)
					}
				}
			}
		}
	}
	return out
}

// find answers the labels of every secret found in b, sorted.
func (d credentialDetector) find(b []byte) []string {
	found := map[string]bool{}
	for _, view := range d.views(b, 2) {
		for label, secret := range d.secrets {
			if found[label] {
				continue
			}
			forms := []string{secret, strings.ToUpper(secret)}
			if dashless := strings.ReplaceAll(secret, "-", ""); dashless != secret {
				forms = append(forms, dashless, strings.ToUpper(dashless))
			}
			for _, form := range forms {
				if bytes.Contains(view, []byte(form)) {
					found[label] = true
					break
				}
			}
		}
	}
	labels := make([]string, 0, len(found))
	for label := range found {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

var identityCredentials = credentialDetector{secrets: map[string]string{
	"identity uuid": sweepIdentityUUID, "identity password": sweepIdentityPassword, "identity username": sweepIdentityUser,
}}

var allCredentials = credentialDetector{secrets: map[string]string{
	"identity uuid": sweepIdentityUUID, "identity password": sweepIdentityPassword, "identity username": sweepIdentityUser,
	"owner uuid": sweepOwnerUUID, "owner password": sweepOwnerPassword,
}}

// TestCredentialDetectorFindsEveryForm keeps the sweep honest: the detector
// finds each secret in every form a reply could carry it in, and nothing in
// bytes that do not hold it.
func TestCredentialDetectorFindsEveryForm(t *testing.T) {
	vmess, _ := json.Marshal(map[string]string{"v": "2", "add": "198.51.100.1", "port": "443", "id": sweepIdentityUUID})
	cases := map[string]string{
		"raw":            "uuid=" + sweepIdentityUUID,
		"query escaped":  "p=" + url.QueryEscape(sweepIdentityPassword),
		"path escaped":   "trojan://" + url.PathEscape(sweepIdentityPassword) + "@h:1",
		"userinfo":       (&url.URL{Scheme: "tuic", User: url.UserPassword(sweepIdentityUUID, sweepIdentityPassword), Host: "h:1"}).String(),
		"json escaped":   string(mustJSON(map[string]string{"password": sweepIdentityPassword})),
		"double json":    string(mustJSON(map[string]string{"raw": string(mustJSON(map[string]string{"password": sweepIdentityPassword}))})),
		"dashless":       "id " + strings.ReplaceAll(sweepIdentityUUID, "-", ""),
		"upper":          strings.ToUpper(sweepIdentityUUID),
		"base64":         base64.StdEncoding.EncodeToString([]byte(sweepIdentityPassword)),
		"vmess link":     "vmess://" + base64.StdEncoding.EncodeToString(vmess),
		"socks userinfo": "socks://" + base64.StdEncoding.EncodeToString([]byte(sweepIdentityUser+":"+sweepIdentityPassword)) + "@h:1",
		"kv value":       string(mustJSON(map[string]string{"value_base64": base64.StdEncoding.EncodeToString(mustJSON(map[string]string{"content": "vmess://" + base64.StdEncoding.EncodeToString(vmess)}))})),
	}
	for name, haystack := range cases {
		if found := identityCredentials.find([]byte(haystack)); len(found) == 0 {
			t.Errorf("%s: the detector missed the credential in %q", name, haystack)
		}
	}
	if found := allCredentials.find([]byte(`{"name":"tokyo-1 line-1","uuid":"LATTICE-SYN-00000001-0000-4000-8000-000000000001-uuid-0123456789abcdef0123456789abcdef"}`)); len(found) != 0 {
		t.Errorf("a placeholder node matched %v", found)
	}
}

// sweepHost answers the framed plugin's host calls: KV from memory, the
// line catalogue from fixture rows, vpn-core's identity list and graph
// options credential-free, claim_apply granted. Every http.do is refused,
// and the legacy export and compose answer owner credentials, so a method
// that reached them would put the owner credential where the sweep looks.
type sweepHost struct {
	*fleetCatalogueHost
	legacyCalls int
}

func (h *sweepHost) call(method string, params any) (json.RawMessage, error) {
	switch method {
	case latticeplugin.HostMethodHTTPDo:
		return nil, errors.New("the sweep host has no network")
	case latticeplugin.HostMethodRPCCall:
		encoded, _ := json.Marshal(params)
		var p struct {
			Service string `json:"service"`
			Method  string `json:"method"`
		}
		_ = json.Unmarshal(encoded, &p)
		switch p.Service + "/" + p.Method {
		case vpnCoreIdentitiesService + "/list":
			return json.RawMessage(`{"identities":[{"id":"vu-a","name":"alice","status":"active","bound_lines":8}],"count":1}`), nil
		case subStorePlansService + "/claim_apply":
			return json.RawMessage(`{"granted":true}`), nil
		case "latticenet.vpn-core/subscription-sources/graph_options":
			return json.RawMessage(`{"ok":true,"options_version":"ov1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","identities":[{"id":"vu-a","name":"alice","selectable":true}],"roots":[]}`), nil
		case "latticenet.vpn-core/nodes/export":
			h.legacyCalls++
			return mustJSON(map[string]any{"links": sweepOwnerLinks()}), nil
		case "latticenet.vpn-core/subscription-sources/compose":
			h.legacyCalls++
			links := sweepOwnerLinks()
			return mustJSON(map[string]any{"ok": true, "raw": strings.Join(links, "\n"), "entries": links, "source_version": "sv"}), nil
		}
	}
	return h.fleetCatalogueHost.call(method, params)
}

// sweepOwnerLinks are the owner-credential links a legacy record's export
// and envelope hold.
func sweepOwnerLinks() []string {
	return []string{
		"vless://" + sweepOwnerUUID + "@203.0.113.50:443?encryption=none&security=reality&sni=www.example.com&pbk=" + planFixturePBK + "&sid=0a1b&type=tcp#owner-vless",
		"trojan://" + url.PathEscape(sweepOwnerPassword) + "@203.0.113.51:443?security=tls&sni=t.example#owner-trojan",
	}
}

// sweepFrame is one line the plugin wrote, with the invocation's method.
type sweepFrame struct {
	method string
	kind   string
	raw    []byte
}

// sweepFramed is the framed transport with production's runtime options,
// recording every line the plugin writes. A host-call error is answered as
// an error host response, as core answers it.
type sweepFramed struct {
	in, host *io.PipeWriter
	lines    chan []byte
	done     chan error
	answer   hostCaller
	frames   []sweepFrame
}

func startSweepFramed(t *testing.T, answer hostCaller) *sweepFramed {
	t.Helper()
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	hostReader, hostWriter := io.Pipe()
	opts := pluginRuntimeOptions(inReader, outWriter)
	opts.OpenHostFromEnv = false
	opts.Host = latticeplugin.NewHostClient(latticeplugin.HostClientOptions{Output: outWriter, Responses: hostReader})
	rt := latticeplugin.NewRuntime(opts)
	f := &sweepFramed{in: inWriter, host: hostWriter, lines: make(chan []byte, 16), done: make(chan error, 1), answer: answer}
	go func() {
		scanner := bufio.NewScanner(outReader)
		scanner.Buffer(make([]byte, 64<<10), 64<<20)
		for scanner.Scan() {
			f.lines <- append([]byte(nil), scanner.Bytes()...)
		}
		close(f.lines)
	}()
	base := &runtime{engine: testEngineWithHeadroom()}
	go func() { f.done <- rt.ServeV2(context.Background(), invocationHandler(base), 7) }()
	t.Cleanup(func() {
		_ = inWriter.Close()
		_ = hostWriter.Close()
		_ = outReader.Close()
	})
	if line, err := f.next(); err != nil || !bytes.Contains(line, []byte(`"runtime_ready"`)) {
		t.Fatalf("runtime_ready: %s %v", line, err)
	}
	return f
}

func (f *sweepFramed) next() ([]byte, error) {
	select {
	case line, ok := <-f.lines:
		if !ok {
			return nil, errors.New("transport closed")
		}
		return line, nil
	case err := <-f.done:
		return nil, fmt.Errorf("runtime exited: %v", err)
	case <-time.After(3 * time.Minute):
		return nil, errors.New("no frame within three minutes")
	}
}

// invoke sends one subscription call, serves its host calls and answers
// the invocation's result. Every line the invocation wrote is recorded.
func (f *sweepFramed) invoke(t *testing.T, invocation, method string, payload any) latticeplugin.Response {
	t.Helper()
	call := mustJSON(callPayload{Service: pluginID + "/subscription", Method: method, Payload: mustJSON(payload)})
	frame := map[string]any{"protocol": 2, "kind": "invoke", "generation": 7, "invocation_id": invocation, "request": request{Action: latticeplugin.ActionCall, Payload: call}}
	go func() { _ = json.NewEncoder(f.in).Encode(frame) }()
	var response latticeplugin.Response
	for {
		line, err := f.next()
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		var wire framedWire
		if err := json.Unmarshal(line, &wire); err != nil {
			t.Fatalf("%s: a line that is not a frame: %.200s", method, line)
		}
		f.frames = append(f.frames, sweepFrame{method: method, kind: wire.Kind, raw: line})
		switch wire.Kind {
		case "host_call":
			var hc struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(wire.HostCall, &hc); err != nil {
				t.Fatal(err)
			}
			var params any
			_ = json.Unmarshal(hc.Params, &params)
			hostResponse := map[string]any{"id": hc.ID, "ok": true}
			if result, err := f.answer.call(hc.Method, params); err != nil {
				hostResponse["ok"], hostResponse["error"] = false, err.Error()
			} else {
				if result == nil {
					result = json.RawMessage(`{"ok":true}`)
				}
				hostResponse["result"] = result
			}
			reply := mustJSON(map[string]any{"protocol": 2, "kind": "host_response", "generation": 7, "invocation_id": invocation,
				"host_call_id": wire.HostCallID, "host_response": hostResponse})
			if _, err := f.host.Write(append(reply, '\n')); err != nil {
				t.Fatal(err)
			}
		case "invoke_result":
			response = wire.Response
		case "invoke_ready":
			return response
		}
	}
}

// sweepEchoScript is a Script Operator that reads every argument the
// bundle hands it and every global it can reach, and writes them into the
// first node's name, so a credential anywhere in the script's reach lands
// in the plan the sweep reads.
const sweepEchoScript = `function operator(proxies) {
  function dump(v) {
    try { var s = JSON.stringify(v); return s === undefined ? String(v) : s; } catch (e) { return String(v); }
  }
  var parts = ["ARGS " + dump(Array.prototype.slice.call(arguments, 1))];
  Object.getOwnPropertyNames(globalThis).forEach(function (k) {
    var v;
    try { v = globalThis[k]; } catch (e) { return; }
    if (typeof v === "function") { parts.push(k + "=function"); return; }
    parts.push(k + "=" + dump(v).slice(0, 20000));
  });
  if (proxies.length > 0) { proxies[0].name = "SWEEP-DUMP " + parts.join(" | "); }
  return proxies;
}`

// sweepRows is one catalogue line per template protocol, shadowsocks (no
// bind shape) included.
func sweepRows() []model.LineCatalogueRow {
	var rows []model.LineCatalogueRow
	for i, protocol := range []string{"vless", "trojan", "hysteria2", "tuic", "vmess", "socks", "anytls", "shadowsocks"} {
		rows = append(rows, fleetTestRow(i+1, protocol))
	}
	return rows
}

// sweepBoundNodes is a plan's nodes as core sends them to convert after it
// bound them for the fixture identity: every placeholder replaced by the
// identity's credential for that field.
func sweepBoundNodes(t *testing.T, encoded json.RawMessage) []json.RawMessage {
	t.Helper()
	plan := decodeRenderPlan(t, encoded)
	nodes := make([]json.RawMessage, 0, len(plan.Nodes))
	for _, node := range plan.Nodes {
		var fields map[string]any
		if err := json.Unmarshal(node.Node, &fields); err != nil {
			t.Fatal(err)
		}
		for field := range node.Placeholders {
			switch field {
			case "uuid":
				fields[field] = sweepIdentityUUID
			case "password":
				fields[field] = sweepIdentityPassword
			case "username":
				fields[field] = sweepIdentityUser
			default:
				t.Fatalf("placeholder field %q", field)
			}
		}
		nodes = append(nodes, mustJSON(fields))
	}
	return nodes
}

func TestCredentialAbsenceSweep(t *testing.T) {
	freshFleetPlans(t)
	host := &sweepHost{fleetCatalogueHost: newFleetCatalogueHost(sweepRows())}
	host.applySelector = true
	seed := &runtime{host: host, engine: testEngineWithHeadroom()}
	rename := renameStep("tokyo", "osaka")
	sortDesc := json.RawMessage(`{"type":"Sort Operator","args":"desc"}`)
	seedRecords(t, seed,
		subscriptionRecord{ID: "fleet", Source: subscriptionSourceFleet, Process: []json.RawMessage{rename, sortDesc}},
		subscriptionRecord{ID: "fleet-script", Source: subscriptionSourceFleet, Process: []json.RawMessage{scriptStep(t, sweepEchoScript)}},
		subscriptionRecord{ID: "local-p", Source: subscriptionSourceLocal, Content: "trojan://provider-pw@198.51.100.7:443#provider"},
		subscriptionRecord{ID: "coll", Kind: kindCollection, Members: []string{"fleet", "local-p"}, Process: []json.RawMessage{sortDesc}},
		subscriptionRecord{ID: "spare", Source: subscriptionSourceLocal, Content: "trojan://spare-pw@198.51.100.8:443#spare"},
		subscriptionRecord{ID: "legacy-g", Source: subscriptionSourceVPNCoreGraph, VPNIdentity: "vu-a",
			EntryRoots: []string{fleetTestLineUUID(1), fleetTestLineUUID(2)}, GraphOptionsVersion: "ov1:" + strings.Repeat("a", 64), Target: "URI"},
	)
	legacyLive, err := seed.getSubscription("legacy-g")
	if err != nil {
		t.Fatal(err)
	}

	f := startSweepFramed(t, host)
	n := 0
	call := func(method string, payload any) latticeplugin.Response {
		t.Helper()
		n++
		return f.invoke(t, fmt.Sprint(n), method, payload)
	}
	must := func(method string, payload any) json.RawMessage {
		t.Helper()
		res := call(method, payload)
		if !res.OK {
			t.Fatalf("%s %v: %s", method, payload, res.Error)
		}
		return res.Result
	}
	fetched := map[string]string{}
	for _, id := range []string{"fleet", "fleet-script", "coll", "local-p"} {
		var out struct {
			Raw string `json:"raw"`
		}
		if err := json.Unmarshal(must("fetch", map[string]any{"subscription_id": id}), &out); err != nil || out.Raw == "" {
			t.Fatalf("fetch %s: %v", id, err)
		}
		fetched[id] = out.Raw
	}
	var rendered renderReply
	if err := json.Unmarshal(must("render", map[string]any{"subscription_id": "fleet", "raw": fetched["fleet"], "target": "sing-box"}), &rendered); err != nil {
		t.Fatal(err)
	}
	bound := sweepBoundNodes(t, rendered.Plan)
	if len(bound) != 7 {
		t.Fatalf("the fleet plan has %d nodes, want one per bindable protocol", len(bound))
	}

	// Convert holds the identity's credentials. Its own result frames are
	// the one place they may appear, and they must appear there, or the
	// sweep proves nothing.
	for _, target := range []string{"sing-box", "ClashMeta", "URI", "V2Ray", "Surge", "Loon"} {
		req := model.ConvertRequest{Target: target, Format: "plain", Nodes: bound}
		raw, err := model.EncodeConvertRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		res := call("convert", payload)
		if !res.OK {
			t.Fatalf("convert %s: %s", target, res.Error)
		}
		if found := identityCredentials.find(res.Result); len(found) == 0 {
			t.Fatalf("convert %s's document carries no identity credential the detector finds; the sweep would be vacuous", target)
		}
	}

	// The serve path again, from the cache and on other targets, the
	// collection, and a script that dumps everything it can reach.
	must("render", map[string]any{"subscription_id": "fleet", "raw": fetched["fleet"], "target": "sing-box"})
	must("render", map[string]any{"subscription_id": "fleet", "raw": fetched["fleet"], "target": "ClashMeta"})
	must("render", map[string]any{"subscription_id": "coll", "raw": fetched["coll"], "target": "sing-box"})
	must("render", map[string]any{"subscription_id": "fleet", "target": "sing-box", "member_revisions": map[string]string{}})
	dump := must("render", map[string]any{"subscription_id": "fleet-script", "raw": fetched["fleet-script"], "target": "sing-box"})
	if !bytes.Contains(dump, []byte("SWEEP-DUMP ARGS")) {
		t.Fatalf("the echo script did not run on the serve path: %.300s", dump)
	}

	// Every read method.
	must("preview", map[string]any{"subscription_id": "fleet", "target": "URI"})
	must("preview", map[string]any{"subscription_id": "coll", "target": "URI"})
	must("preview_draft", map[string]any{"source": subscriptionSourceFleet, "operators": []json.RawMessage{rename}, "target": "URI"})
	must("list", map[string]any{})
	must("get", map[string]any{"subscription_id": "fleet"})
	exported := must("export", map[string]any{})
	must("get_settings", map[string]any{})
	must("save_settings", map[string]any{"schema_version": 1})
	must("depends_on", map[string]any{})
	must("depends_on", map[string]any{"subscription_id": "fleet", "include_staged": true})
	call("probe", map[string]any{"subscription_id": "fleet"})
	must("fleet_preview", map[string]any{"process": []json.RawMessage{structuredKeep("protocol", "vless", "trojan")}})
	must("fleet_preview", map[string]any{"line_uuids": []string{fleetTestLineUUID(1), fleetTestLineUUID(5)}})
	must("identities", map[string]any{})
	call("graph_options", map[string]any{})
	must("operators", map[string]any{})

	// A staged edit: rendered by revision, discarded, staged again and
	// promoted with the grant.
	var saved saveReply
	if err := json.Unmarshal(must("save", map[string]any{"subscription": subscriptionRecord{ID: "fleet", Name: "fleet", Source: subscriptionSourceFleet,
		Process: []json.RawMessage{structuredKeep("protocol", "vless", "vmess"), rename}}}), &saved); err != nil || !saved.Staged {
		t.Fatalf("the fleet edit did not stage: %+v %v", saved, err)
	}
	must("render", map[string]any{"subscription_id": "fleet", "raw": fetched["fleet"], "target": "sing-box", "revision": saved.StagedRevision})
	must("discard_staged", map[string]any{"subscription_id": "fleet", "staged_revision": saved.StagedRevision})
	if err := json.Unmarshal(must("save", map[string]any{"subscription": subscriptionRecord{ID: "fleet", Name: "fleet", Source: subscriptionSourceFleet,
		Process: []json.RawMessage{rename}}}), &saved); err != nil || !saved.Staged {
		t.Fatalf("the second fleet edit did not stage: %+v %v", saved, err)
	}
	must("apply_revision", applyRequest("fleet", saved.StagedRevision, saved.BaseRevision))

	// The legacy record under migration: migrate_record stages its fleet
	// revision; a revision carrying the echo script is staged over it and
	// rendered over the legacy envelope, whose raw holds the owner links.
	must("migrate_record", map[string]any{"subscription_id": "legacy-g"})
	if err := json.Unmarshal(must("save", map[string]any{"subscription": subscriptionRecord{ID: "legacy-g", Name: "legacy-g", Source: subscriptionSourceFleet,
		Process: []json.RawMessage{structuredKeep("line_uuid", fleetTestLineUUID(1), fleetTestLineUUID(2)), scriptStep(t, sweepEchoScript)}}}), &saved); err != nil || !saved.Staged {
		t.Fatalf("the migrated revision did not stage: %+v %v", saved, err)
	}
	legacyEnvelope, err := encodeSnapshotEnvelope(textEnvelope(kindSub, strings.Join(sweepOwnerLinks(), "\n"), ""))
	if err != nil {
		t.Fatal(err)
	}
	if found := allCredentials.find([]byte(legacyEnvelope)); len(found) != 2 {
		t.Fatalf("the legacy envelope carries %v, want both owner credentials", found)
	}
	staged := must("render", map[string]any{"subscription_id": "legacy-g", "raw": legacyEnvelope, "target": "sing-box", "revision": saved.StagedRevision})
	if !bytes.Contains(staged, []byte("SWEEP-DUMP ARGS")) {
		t.Fatalf("the echo script did not run on the staged migration render: %.300s", staged)
	}
	// The live legacy revision is not rendered here: core serves a legacy
	// record from the envelope it holds, and that render answers the links
	// the caller handed in, which is the legacy path S6 retires, not a leak.
	if live, _ := seed.getSubscription("legacy-g"); live.Revision != legacyLive.Revision {
		t.Fatalf("the migration changed the live legacy record")
	}

	// Every write method.
	var backup struct {
		Backup string `json:"backup"`
	}
	_ = json.Unmarshal(exported, &backup)
	call("import", map[string]any{"backup": backup.Backup})
	call("migrate", map[string]any{})
	must("reorder", map[string]any{"ids": []string{"coll", "fleet", "fleet-script", "legacy-g", "local-p", "spare"}})
	must("delete", map[string]any{"subscription_id": "spare"})
	must("restore", map[string]any{"subscription_id": "spare"})
	must("delete", map[string]any{"subscription_id": "spare"})
	must("purge", map[string]any{"subscription_id": "spare"})
	call("migrate_store", map[string]any{})
	if res := call("publish", map[string]any{"subscription_id": "fleet", "destination": "https://publish.example/target", "method": "PUT"}); res.OK {
		t.Fatalf("publish of a fleet record succeeded: %.200s", res.Result)
	}

	// Every method the manifest signs is in the sweep.
	called := map[string]bool{}
	for _, frame := range f.frames {
		called[frame.method] = true
	}
	for key := range ackedRuntimeBudgets() {
		method, ok := strings.CutPrefix(key, pluginID+"/subscription/")
		if ok && !called[method] {
			t.Errorf("the sweep never calls %s", method)
		}
	}

	for _, frame := range f.frames {
		if frame.method == "convert" && frame.kind == "invoke_result" {
			continue
		}
		if found := allCredentials.find(frame.raw); len(found) != 0 {
			t.Errorf("%s wrote a %s frame carrying %v: %.300s", frame.method, frame.kind, found, frame.raw)
		}
	}
	for key, value := range host.values {
		if found := allCredentials.find(value); len(found) != 0 {
			t.Errorf("the plugin stored %v under %s", found, key)
		}
	}
	if host.legacyCalls != 0 {
		t.Errorf("%d calls reached the legacy export or compose", host.legacyCalls)
	}

	// A panic inside the engine while it holds the credentials: a nil node
	// after a credentialed one panics in processNodes, under the engine's
	// recover, which must surface only the redacted form. The redactor is
	// also handed a panic value that carries the credential set, which is
	// what a panic raised over a node's fields would carry.
	node := &nodemodel.Node{Fields: map[string]any{"type": "vless", "name": "n", "server": "198.51.100.1", "port": 443, "uuid": sweepIdentityUUID, "password": sweepIdentityPassword}}
	_, err = testEngineWithHeadroom().processNodes([]*nodemodel.Node{node, nil}, []json.RawMessage{rename}, "sing-box")
	if err == nil || !strings.Contains(err.Error(), "panic_sha256=") {
		t.Fatalf("the forced panic surfaced as %v, not the redacted engine panic", err)
	}
	for _, perr := range []error{err, redactSubStoreEnginePanic("encoding " + sweepIdentityUUID + ":" + sweepIdentityPassword + " for " + sweepIdentityUser + " " + sweepOwnerUUID)} {
		if found := allCredentials.find([]byte(perr.Error())); len(found) != 0 || !strings.Contains(perr.Error(), "panic_sha256=") {
			t.Errorf("the engine's panic error carries %v: %s", found, perr)
		}
	}
	t.Logf("swept %d frames from %d invocations and %d KV values", len(f.frames), n, len(host.values))
}
