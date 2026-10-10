package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/producers"
	"github.com/LatticeNet/lattice-sdk/model"
)

// TestConvertDocumentSubstitutionPerScalar pins that each credential is
// written encoded for the scalar its placeholder sits in, so a password with
// a quote, a backslash, a colon or a hash cannot end a string early or start
// a comment, and that a placeholder in a block scalar, a comment or a longer
// plain scalar is refused rather than guessed at.
func TestConvertDocumentSubstitutionPerScalar(t *testing.T) {
	line := fleetTestLineUUID(1)
	ph, err := model.NewPlanPlaceholder(line, "password")
	if err != nil {
		t.Fatal(err)
	}
	const tricky = `p"a'ss\w#rd: x&y=z/ü`
	substitute := func(content string) (string, error) {
		return substituteDocument(model.ConvertDocument{Content: content, Substitutions: map[string]string{ph: tricky}})
	}
	ok := map[string]struct{ in, want string }{
		"yaml plain mapping value": {"proxies:\n  - name: a\n    password: " + ph + "\n", "proxies:\n  - name: a\n    password: \"p\\\"a'ss\\\\w#rd: x\\u0026y=z/ü\"\n"},
		"yaml plain with comment":  {"password: " + ph + " # set by core\n", "password: \"p\\\"a'ss\\\\w#rd: x\\u0026y=z/ü\" # set by core\n"},
		"yaml flow mapping":        {"- {name: a, password: " + ph + ", port: 443}\n", "- {name: a, password: \"p\\\"a'ss\\\\w#rd: x\\u0026y=z/ü\", port: 443}\n"},
		"yaml double quoted":       {`password: "` + ph + `"`, `password: "p\"a'ss\\w#rd: x\u0026y=z/ü"`},
		"yaml single quoted":       {`password: '` + ph + `'`, `password: 'p"a''ss\w#rd: x&y=z/ü'`},
		"json string":              {`{"password":"` + ph + `","port":443}`, `{"password":"p\"a'ss\\w#rd: x\u0026y=z/ü","port":443}`},
		"uri userinfo":             {"trojan://" + ph + "@t.example:443?sni=t.example#t", "trojan://p%22a%27ss%5Cw%23rd%3A%20x%26y%3Dz%2F%C3%BC@t.example:443?sni=t.example#t"},
		"uri query value":          {"ss://x@h.example:1?password=" + ph + "&a=b", "ss://x@h.example:1?password=p%22a%27ss%5Cw%23rd%3A%20x%26y%3Dz%2F%C3%BC&a=b"},
		"bare line":                {"first\n" + ph + "\nlast", "first\n" + tricky + "\nlast"},
	}
	for name, c := range ok {
		got, err := substitute(c.in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", name, got, c.want)
		}
	}
	// Each quoted result reads back as the credential.
	got, _ := substitute(`{"password":"` + ph + `"}`)
	var decoded struct{ Password string }
	if err := json.Unmarshal([]byte(got), &decoded); err != nil || decoded.Password != tricky {
		t.Fatalf("json round trip: %q (%v)", decoded.Password, err)
	}

	refused := map[string]string{
		"block scalar":                      "script: |\n  const p = '" + ph + "';\n",
		"folded block scalar":               "notes: >-\n  a\n  " + ph + "\n",
		"comment":                           "password: x # " + ph + "\n",
		"longer plain scalar":               "name: node-" + ph + "\n",
		"uri path":                          "vless://u@h.example:443/" + ph + "\n",
		"plain scalar no space after colon": "password:" + ph + "\n",
	}
	for name, content := range refused {
		if _, err := substitute(content); err == nil || !strings.HasPrefix(err.Error(), codeDocumentScalarUnsupported) || strings.Contains(err.Error(), "ss\\w") {
			t.Errorf("%s: err %v", name, err)
		}
	}
	other, _ := model.NewPlanPlaceholder(fleetTestLineUUID(2), "uuid")
	if _, err := substitute("uuid: " + other + "\n"); err == nil || strings.Contains(err.Error(), tricky) {
		t.Errorf("a placeholder with no substitution: err %v", err)
	}
	if got, err := substitute("proxies: []\n"); err != nil || got != "proxies: []\n" {
		t.Errorf("a document without placeholders: %q, %v", got, err)
	}
}

// bindTestPlan stands in for core's bind: it writes a credential where each
// placeholder is and the identity's flow on vless, and returns the bound
// node objects as core sends them to convert.
func bindTestPlan(t *testing.T, plan model.SelectionPlan) []json.RawMessage {
	t.Helper()
	out := make([]json.RawMessage, 0, len(plan.Nodes))
	for i, node := range plan.Nodes {
		var fields map[string]any
		if err := json.Unmarshal(node.Node, &fields); err != nil {
			t.Fatal(err)
		}
		for field := range node.Placeholders {
			switch field {
			case "uuid":
				fields[field] = fmt.Sprintf("1b2c3d4e-0000-4000-8000-%012d", i)
			default:
				fields[field] = fmt.Sprintf("bound-%s-%d", field, i)
			}
		}
		if fields["type"] == "vless" {
			fields["flow"] = "xtls-rprx-vision"
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, raw)
	}
	return out
}

// TestConvertStripsLatticeFieldsBeforeProducers runs a bound plan of every
// template protocol through every client convert produces for, the native
// producers in Go and the rest through the bundle, and asserts that no line,
// geo, address or node id the plan carried reaches the output.
func TestConvertStripsLatticeFieldsBeforeProducers(t *testing.T) {
	var rows []fleetRow
	for i, protocol := range []string{"vless", "trojan", "hysteria2", "tuic", "vmess", "socks", "anytls"} {
		row := projectFleetRow(fleetTestRow(i+1, protocol))
		row.NodeID = fmt.Sprintf("nodeidsentinel%d", i)
		row.LineHashID = fmt.Sprintf("lhashsentinel%d", i)
		row.Addresses = []string{fmt.Sprintf("198.51.100.%d", i+1)}
		row.Geo = &model.NodeGeo{Country: "JP", City: "Citysentinel", ASOrg: "ASORGSENTINEL"}
		row.NodeTags, row.GroupIDs = []string{"tagsentinel"}, []string{"groupsentinel"}
		rows = append(rows, row)
	}
	_, plan := buildTestPlan(t, subscriptionRecord{ID: "all", Name: "all"}, rows)
	bound := bindTestPlan(t, plan)
	forbidden := []string{"nodeidsentinel", "lhashsentinel", "198.51.100.", "Citysentinel", "ASORGSENTINEL", "tagsentinel", "groupsentinel", "line_uuid", "_lattice"}
	for i := range rows {
		forbidden = append(forbidden, fleetTestLineUUID(i+1))
	}

	rt := &runtime{host: denyHostCalls{}, engine: testEngineWithHeadroom()}
	for target := range subscriptionConvertTargets {
		res := callConvert(t, rt, map[string]any{"nodes": bound, "target": target, "format": "plain"})
		if !res.OK {
			// A client that carries none of these protocols refuses the
			// document as zero nodes; that is not a leak.
			if strings.HasPrefix(res.Error, zeroNodesForTargetCode) {
				continue
			}
			t.Fatalf("%s (native %v): %s", target, producers.Native(target), res.Error)
		}
		out := decodeConvert(t, res)
		for _, needle := range forbidden {
			if strings.Contains(out.Content, needle) {
				t.Errorf("%s (native %v) output carries %q", target, producers.Native(target), needle)
			}
		}
		if out.NodeCount == 0 {
			t.Errorf("%s produced no node", target)
		}
	}

	// A bound node carrying _lattice is refused: a plan node never has one.
	var withBlock map[string]any
	_ = json.Unmarshal(bound[0], &withBlock)
	withBlock["_lattice"] = map[string]any{"line_uuid": fleetTestLineUUID(1)}
	raw, _ := json.Marshal(withBlock)
	if res := callConvert(t, rt, map[string]any{"nodes": []json.RawMessage{raw}, "target": "URI"}); res.OK || !strings.Contains(res.Error, "_lattice") {
		t.Fatalf("a bound node with _lattice: ok=%v error=%q", res.OK, res.Error)
	}
}
