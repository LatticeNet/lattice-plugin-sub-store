package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// The plan fixtures (S2 plan section 7, lane 7) are what the fleet render
// returns for one catalogue line of every template shape the plugin builds
// a node for, written under testdata/plan-fixtures/ so lattice-server can
// bind them with its own checker (TestPluginPlanFixturesPassBindCheckNode,
// srv/substore_s2_e2e_test.go, which pins the files' digest). Each fixture
// carries the owner-style share URL the line's node would report, the
// catalogue row core builds from it, and the plan: core checks that its own
// template builder turns the share URL into the row's template before it
// binds the plan's node, so a template shape the plugin parses differently
// from core fails there rather than in production.
//
// TestWritePlanFixtures regenerates the files when SUBSTORE_WRITE_PLAN_FIXTURES
// is 1 and otherwise holds the committed files to a fresh render, with every
// placeholder normalised, since each plan computation draws new ones.
const planFixturesDir = "testdata/plan-fixtures"

const writePlanFixturesEnv = "SUBSTORE_WRITE_PLAN_FIXTURES"

// planFixtureOwnerUUID and planFixtureOwnerPassword are the owner credential
// the share URLs carry. Core's template builder drops them; no row and no
// plan may carry them.
const (
	planFixtureOwnerUUID     = "0f0e0d0c-0b0a-4908-8706-050403020100"
	planFixtureOwnerPassword = "owner-share-secret"
)

// planFixtureVariant is one template shape: the share URL a node reports and
// the template core keeps from it (lineClientTemplateFromShareURL keeps the
// allow-listed query parameters, drops flow and the credential).
type planFixtureVariant struct {
	Name     string
	Protocol string
	ShareURL string
	Host     string
	Port     int
	Params   map[string]string
	// Transport and Security are the row's own fields, as core's inventory
	// read fills them.
	Transport, Security string
}

func vmessShareURL(doc map[string]any) string {
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(raw)
}

const planFixturePBK = "Zk9hGv2m3Tq8sXcRw1yPbN4uL7eA6dK0jI5oF9hQ2tY"

// planFixtureVariants covers every protocol fleetCredentialFields builds a
// node for, and for vless, trojan and vmess the transports the parsers treat
// differently (tcp, ws, grpc; Reality and plain TLS; vmess with and without
// TLS), plus hysteria2's certificate pin.
var planFixtureVariants = []planFixtureVariant{
	{Name: "vless-reality-tcp", Protocol: "vless", Transport: "tcp", Security: "reality",
		ShareURL: "vless://" + planFixtureOwnerUUID + "@203.0.113.10:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.example.com&fp=chrome&pbk=" + planFixturePBK + "&sid=0a1b&type=tcp#owner",
		Host:     "203.0.113.10", Port: 443,
		Params: map[string]string{"encryption": "none", "security": "reality", "sni": "www.example.com", "fp": "chrome", "pbk": planFixturePBK, "sid": "0a1b", "type": "tcp"}},
	{Name: "vless-tls-ws", Protocol: "vless", Transport: "ws", Security: "tls",
		ShareURL: "vless://" + planFixtureOwnerUUID + "@vl.example.com:8443?encryption=none&security=tls&sni=vl.example.com&type=ws&host=vl.example.com&path=%2Fws#owner",
		Host:     "vl.example.com", Port: 8443,
		Params: map[string]string{"encryption": "none", "security": "tls", "sni": "vl.example.com", "type": "ws", "host": "vl.example.com", "path": "/ws"}},
	{Name: "vless-tls-grpc", Protocol: "vless", Transport: "grpc", Security: "tls",
		ShareURL: "vless://" + planFixtureOwnerUUID + "@grpc.example.com:443?encryption=none&security=tls&sni=grpc.example.com&type=grpc&serviceName=lattice#owner",
		Host:     "grpc.example.com", Port: 443,
		Params: map[string]string{"encryption": "none", "security": "tls", "sni": "grpc.example.com", "type": "grpc", "serviceName": "lattice"}},
	{Name: "trojan-tls-tcp", Protocol: "trojan", Transport: "tcp", Security: "tls",
		ShareURL: "trojan://" + planFixtureOwnerPassword + "@tr.example.com:443?security=tls&sni=tr.example.com&type=tcp#owner",
		Host:     "tr.example.com", Port: 443,
		Params: map[string]string{"security": "tls", "sni": "tr.example.com", "type": "tcp"}},
	{Name: "trojan-tls-ws", Protocol: "trojan", Transport: "ws", Security: "tls",
		ShareURL: "trojan://" + planFixtureOwnerPassword + "@trws.example.com:443?security=tls&sni=trws.example.com&type=ws&host=trws.example.com&path=%2Ftr#owner",
		Host:     "trws.example.com", Port: 443,
		Params: map[string]string{"security": "tls", "sni": "trws.example.com", "type": "ws", "host": "trws.example.com", "path": "/tr"}},
	{Name: "hysteria2", Protocol: "hysteria2", Transport: "udp", Security: "tls",
		ShareURL: "hysteria2://" + planFixtureOwnerPassword + "@hy.example.com:8443?sni=hy.example.com#owner",
		Host:     "hy.example.com", Port: 8443,
		Params: map[string]string{"sni": "hy.example.com"}},
	{Name: "hysteria2-pinned", Protocol: "hysteria2", Transport: "udp", Security: "tls",
		ShareURL: "hysteria2://" + planFixtureOwnerPassword + "@198.51.100.20:8443?sni=hy2.example.com&pinSHA256=7a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809#owner",
		Host:     "198.51.100.20", Port: 8443,
		Params: map[string]string{"sni": "hy2.example.com", "pinSHA256": "7a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"}},
	{Name: "tuic", Protocol: "tuic", Transport: "udp", Security: "tls",
		ShareURL: "tuic://" + planFixtureOwnerUUID + ":" + planFixtureOwnerPassword + "@tu.example.com:443?alpn=h3&congestion_control=bbr&sni=tu.example.com#owner",
		Host:     "tu.example.com", Port: 443,
		Params: map[string]string{"alpn": "h3", "congestion_control": "bbr", "sni": "tu.example.com"}},
	{Name: "vmess-tls-ws", Protocol: "vmess", Transport: "ws", Security: "tls",
		ShareURL: vmessShareURL(map[string]any{"v": "2", "ps": "owner", "add": "vm.example.com", "port": "443", "id": planFixtureOwnerUUID,
			"aid": "0", "net": "ws", "host": "vm.example.com", "path": "/vm", "tls": "tls", "sni": "vm.example.com"}),
		Host: "vm.example.com", Port: 443,
		Params: map[string]string{"aid": "0", "net": "ws", "host": "vm.example.com", "path": "/vm", "tls": "tls", "sni": "vm.example.com"}},
	{Name: "vmess-tcp", Protocol: "vmess", Transport: "tcp", Security: "none",
		ShareURL: vmessShareURL(map[string]any{"v": "2", "ps": "owner", "add": "198.51.100.40", "port": "10086", "id": planFixtureOwnerUUID,
			"aid": "0", "net": "tcp"}),
		Host: "198.51.100.40", Port: 10086,
		Params: map[string]string{"aid": "0", "net": "tcp"}},
	{Name: "socks", Protocol: "socks", Transport: "tcp", Security: "none",
		ShareURL: "socks://" + base64.StdEncoding.EncodeToString([]byte("owner:"+planFixtureOwnerPassword)) + "@198.51.100.30:1080#owner",
		Host:     "198.51.100.30", Port: 1080},
	{Name: "anytls", Protocol: "anytls", Transport: "tcp", Security: "tls",
		ShareURL: "anytls://" + planFixtureOwnerPassword + "@at.example.com:443?security=tls&sni=at.example.com#owner",
		Host:     "at.example.com", Port: 443,
		Params: map[string]string{"security": "tls", "sni": "at.example.com"}},
}

// planFixture is one file under testdata/plan-fixtures.
type planFixture struct {
	Name     string                 `json:"name"`
	Protocol string                 `json:"protocol"`
	ShareURL string                 `json:"share_url"`
	Row      model.LineCatalogueRow `json:"row"`
	Plan     json.RawMessage        `json:"plan"`
}

// planFixtureRow is variant v's catalogue row, line i.
func planFixtureRow(i int, v planFixtureVariant) model.LineCatalogueRow {
	row := fleetTestRow(i, v.Protocol)
	row.Name = v.Name
	row.NodeName = fmt.Sprintf("fixture-%02d", i)
	row.Label = row.NodeName + " " + row.Name
	row.Transport, row.Security = v.Transport, v.Security
	row.PublicHost, row.PublicPort = v.Host, v.Port
	row.Addresses = []string{fmt.Sprintf("192.0.2.%d", i)}
	params := map[string]string{}
	for k, value := range v.Params {
		params[k] = value
	}
	if len(params) == 0 {
		params = nil
	}
	row.Template = &model.LineCatalogueTemplate{Protocol: v.Protocol, Host: v.Host, Port: v.Port, Params: params, Digest: fmt.Sprintf("sha256:%064x", i)}
	return row
}

// renderPlanFixtures renders every variant through the plugin's own fleet
// path: a fleet record over a one-line catalogue, fetched, then rendered on
// the serve path with the snapshot the fetch wrote, as core does.
func renderPlanFixtures(t *testing.T) []planFixture {
	t.Helper()
	freshFleetPlans(t)
	out := make([]planFixture, 0, len(planFixtureVariants))
	for i, v := range planFixtureVariants {
		row := planFixtureRow(i+1, v)
		if err := (model.LineCatalogueResponse{CatalogueVersion: fleetTestVersion, Rows: []model.LineCatalogueRow{row}}).Validate(); err != nil {
			t.Fatalf("%s: the row fails the SDK's check: %v", v.Name, err)
		}
		rt, _, snapshot := fetchedFleetRecord(t, []model.LineCatalogueRow{row}, subscriptionRecord{ID: "fixture-" + v.Name})
		reply, res := renderFleetCall(t, rt, map[string]any{"subscription_id": "fixture-" + v.Name, "raw": snapshot, "target": "sing-box"})
		if !res.OK {
			t.Fatalf("%s: render: %s", v.Name, res.Error)
		}
		plan := decodeRenderPlan(t, reply.Plan)
		if len(plan.Nodes) != 1 || plan.Nodes[0].LineUUID != row.LineUUID {
			t.Fatalf("%s: the plan has %d nodes for one line", v.Name, len(plan.Nodes))
		}
		out = append(out, planFixture{Name: v.Name, Protocol: v.Protocol, ShareURL: v.ShareURL, Row: row, Plan: reply.Plan})
	}
	return out
}

// planPlaceholderPattern matches a plan placeholder's random tail, which
// differs on every computation.
var planPlaceholderPattern = regexp.MustCompile(`(LATTICE-SYN-[0-9a-f-]{36}-[a-z]+-)[0-9a-f]{32}`)

func normalisePlaceholders(b []byte) []byte {
	return planPlaceholderPattern.ReplaceAll(b, []byte("${1}*"))
}

func encodePlanFixture(t *testing.T, f planFixture) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWritePlanFixtures(t *testing.T) {
	fixtures := renderPlanFixtures(t)
	for _, f := range fixtures {
		for _, secret := range []string{planFixtureOwnerUUID, planFixtureOwnerPassword} {
			if bytes.Contains(f.Plan, []byte(secret)) {
				t.Fatalf("%s: the plan carries the owner credential", f.Name)
			}
			row, _ := json.Marshal(f.Row)
			if bytes.Contains(row, []byte(secret)) {
				t.Fatalf("%s: the row carries the owner credential", f.Name)
			}
		}
	}
	if os.Getenv(writePlanFixturesEnv) == "1" {
		if err := os.RemoveAll(planFixturesDir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(planFixturesDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range fixtures {
			if err := os.WriteFile(filepath.Join(planFixturesDir, f.Name+".json"), encodePlanFixture(t, f), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("wrote %d plan fixtures to %s", len(fixtures), planFixturesDir)
		return
	}
	entries, err := os.ReadDir(planFixturesDir)
	if err != nil {
		t.Fatalf("%v; run with %s=1 to write the fixtures", err, writePlanFixturesEnv)
	}
	var committed []string
	for _, e := range entries {
		committed = append(committed, e.Name())
	}
	var want []string
	for _, f := range fixtures {
		want = append(want, f.Name+".json")
	}
	sort.Strings(want)
	if strings.Join(committed, ",") != strings.Join(want, ",") {
		t.Fatalf("committed fixtures %v, the render gives %v; run with %s=1", committed, want, writePlanFixturesEnv)
	}
	for _, f := range fixtures {
		got, err := os.ReadFile(filepath.Join(planFixturesDir, f.Name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if fresh := encodePlanFixture(t, f); !bytes.Equal(normalisePlaceholders(got), normalisePlaceholders(fresh)) {
			t.Errorf("%s: the committed fixture is not what the fleet render returns now; run with %s=1 and re-pin the digest in lattice-server", f.Name, writePlanFixturesEnv)
		}
	}
}

// TestPlanFixturesCoverEveryBindShape holds the variant list to the
// protocols the fleet render builds nodes for, so a protocol added to
// fleetCredentialFields gets a fixture core binds.
func TestPlanFixturesCoverEveryBindShape(t *testing.T) {
	covered := map[string]bool{}
	for _, v := range planFixtureVariants {
		covered[v.Protocol] = true
	}
	for protocol := range fleetCredentialFields {
		if !covered[protocol] {
			t.Errorf("no plan fixture renders a %s line", protocol)
		}
	}
}
