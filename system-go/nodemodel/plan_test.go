package nodemodel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

func planTestNode() *Node {
	return &Node{
		Fields: map[string]any{
			"type": "vless", "server": "jp.example", "port": float64(443), "name": "JP 01",
			"uuid": "LATTICE-SYN-x", "network": "tcp",
		},
		Script: map[string]any{"_flag": "scripts only"},
		Lattice: &LatticeFields{
			LineUUID: "6f1c2b3a-4d5e-4f60-8a1b-2c3d4e5f6a7b", LineHashID: "lh-1", NodeID: "n-1",
			Geo:       &model.NodeGeo{Country: "JP", Region: "Tokyo"},
			Chain:     &model.LineCatalogueChain{Role: model.LineChainRoleSingle},
			Tags:      []string{"hk", "premium"},
			Groups:    []string{"g1"},
			Probe:     &model.LineCatalogueProbe{Verdict: model.LineProbeVerdictPass, ConsecutiveFailures: 0},
			Addresses: []string{"192.0.2.10"},
			// Row facts core never sees.
			NodeName: "tokyo-1", Protocol: "vless", ServiceState: "running", Managed: true,
			Machine: &model.LineCatalogueMachine{Vendor: "acme"}, TemplateHost: "jp.example",
		},
	}
}

// TestPlanNodeEncodesLatticeFieldsAtTopLevel pins S2 plan section 2.3's
// encoding: the Lattice fields core carries sit at the top level under its
// names, keys sorted, Script omitted, line_uuid and the row facts core never
// sees left out.
func TestPlanNodeEncodesLatticeFieldsAtTopLevel(t *testing.T) {
	encoded, err := planTestNode().MarshalPlanNode()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range model.SelectionPlanLatticeFields {
		if _, ok := got[key]; !ok {
			t.Errorf("plan node lacks the Lattice field %q: %s", key, encoded)
		}
	}
	for _, key := range []string{"line_uuid", "_lattice", "_flag", "node_name", "protocol", "service_state", "managed", "machine", "template_host"} {
		if _, ok := got[key]; ok {
			t.Errorf("plan node carries %q: %s", key, encoded)
		}
	}
	if string(got["geo"]) != `{"country":"JP","region":"Tokyo","updated_at":"0001-01-01T00:00:00Z"}` {
		t.Errorf("geo = %s", got["geo"])
	}
	if string(got["tags"]) != `["hk","premium"]` || string(got["node_id"]) != `"n-1"` {
		t.Errorf("tags = %s, node_id = %s", got["tags"], got["node_id"])
	}
	// Keys sorted at the top level: the encoder is the node model's own.
	keys := []string{}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	for decoder.More() {
		key, _ := decoder.Token()
		keys = append(keys, key.(string))
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Fatalf("plan node keys not sorted: %v", keys)
		}
	}
}

// TestPlanNodeNeverCarriesLatticeKey pins that no plan node carries
// "_lattice" or a Lattice field a script wrote into Fields: core rejects
// "_lattice" as plan_rejected, and the Lattice block is authoritative.
func TestPlanNodeNeverCarriesLatticeKey(t *testing.T) {
	n := planTestNode()
	n.Fields["_lattice"] = map[string]any{"line_uuid": "forged"}
	n.Fields["geo"] = map[string]any{"country": "US"}
	n.Fields["line_uuid"] = "00000000-0000-4000-8000-000000000000"
	encoded, err := n.MarshalPlanNode()
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "_lattice") || strings.Contains(text, "forged") || strings.Contains(text, `"US"`) || strings.Contains(text, "line_uuid") {
		t.Fatalf("plan node carries a forged Lattice key: %s", text)
	}

	// A node with no Lattice block (a provider node, a node no placeholder
	// names) writes none of the Lattice fields, even ones a script set.
	n.Lattice = nil
	encoded, err = n.MarshalPlanNode()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range model.SelectionPlanStrippedFields {
		if _, ok := got[key]; ok {
			t.Errorf("a node without a Lattice block carries %q", key)
		}
	}
	if _, ok := got["_lattice"]; ok {
		t.Error("a node without a Lattice block carries _lattice")
	}
}

// TestUnmarshalPlanNodeMovesLatticeFieldsAndStripRemovesThem pins convert's
// side: the carried Lattice keys move into Lattice, line_uuid stays for
// StripLattice, protocol tuning fields stay in Fields, and "_lattice" is
// refused.
func TestUnmarshalPlanNodeMovesLatticeFieldsAndStripRemovesThem(t *testing.T) {
	encoded, err := planTestNode().MarshalPlanNode()
	if err != nil {
		t.Fatal(err)
	}
	var bound map[string]any
	if err := json.Unmarshal(encoded, &bound); err != nil {
		t.Fatal(err)
	}
	bound["uuid"] = "11111111-2222-4333-8444-555555555555"
	bound["flow"] = "xtls-rprx-vision"
	bound["up"] = "50 Mbps"
	bound["line_uuid"] = "6f1c2b3a-4d5e-4f60-8a1b-2c3d4e5f6a7b"
	raw, _ := json.Marshal(bound)

	var n Node
	if err := n.UnmarshalPlanNode(raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range model.SelectionPlanLatticeFields {
		if _, ok := n.Fields[key]; ok {
			t.Errorf("Fields still holds the Lattice key %q", key)
		}
	}
	if n.Lattice == nil || n.Lattice.NodeID != "n-1" || n.Lattice.Geo == nil || n.Lattice.Geo.Country != "JP" || !reflect.DeepEqual(n.Lattice.Tags, []string{"hk", "premium"}) {
		t.Fatalf("Lattice = %+v", n.Lattice)
	}
	if n.Fields["flow"] != "xtls-rprx-vision" || n.Fields["up"] != "50 Mbps" {
		t.Errorf("a carried tuning field left Fields: %v", n.Fields)
	}
	if _, ok := n.Fields["line_uuid"]; !ok {
		t.Error("UnmarshalPlanNode removed line_uuid; StripLattice owns that")
	}
	StripLattice(&n)
	for _, key := range append(model.SelectionPlanStrippedFields, "_lattice") {
		if _, ok := n.Fields[key]; ok {
			t.Errorf("StripLattice left %q", key)
		}
	}
	out, err := n.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	// A producer that writes Fields writes no line, geo, address or node id.
	for _, needle := range []string{"6f1c2b3a", "192.0.2.10", `"n-1"`, "Tokyo"} {
		if n.Lattice != nil {
			n.Lattice = nil
			out, _ = n.MarshalJSON()
		}
		if strings.Contains(string(out), needle) {
			t.Errorf("stripped node still carries %q: %s", needle, out)
		}
	}

	if err := (&Node{}).UnmarshalPlanNode([]byte(`{"type":"vless","_lattice":{}}`)); err == nil {
		t.Error("a plan node carrying _lattice was accepted")
	}
	if err := (&Node{}).UnmarshalPlanNode([]byte(`{"type":"vless","geo":"JP"}`)); err == nil || strings.Contains(err.Error(), "JP") {
		t.Errorf("a mistyped Lattice field: err %v", err)
	}
	if err := (&Node{}).UnmarshalPlanNode([]byte(`[]`)); err == nil {
		t.Error("a plan node that is not an object was accepted")
	}
}

// TestPlanNodeFieldListsAreTheSDKLists keeps the SDK lists the plan node
// encoders read in the shape S2 plan section 2.3 states.
func TestPlanNodeFieldListsAreTheSDKLists(t *testing.T) {
	if model.SelectionPlanStrippedFields[0] != "line_uuid" || !reflect.DeepEqual(model.SelectionPlanStrippedFields[1:], model.SelectionPlanLatticeFields) {
		t.Fatalf("stripped %v, lattice %v", model.SelectionPlanStrippedFields, model.SelectionPlanLatticeFields)
	}
	want := []string{"line_hash_id", "node_id", "geo", "chain", "tags", "groups", "probe", "addresses"}
	if !reflect.DeepEqual(model.SelectionPlanLatticeFields, want) {
		t.Fatalf("model.SelectionPlanLatticeFields = %v, want %v", model.SelectionPlanLatticeFields, want)
	}
}
