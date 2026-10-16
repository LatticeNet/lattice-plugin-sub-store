package nodemodel

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/LatticeNet/lattice-sdk/model"
)

func sameAsAppendValueOfUnmarshal(t *testing.T, raw []byte) {
	t.Helper()
	got, ok := appendSorted(nil, raw)
	if !ok {
		return
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("appendSorted admits %q, which json.Unmarshal refuses: %v", raw, err)
	}
	want, err := appendValue(nil, v, 0)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("appendSorted(%q) = %s, appendValue of json.Unmarshal %s (%v)", raw, got, want, err)
	}
}

var sortedCorpus = []string{
	`{}`, `[]`, `{"b":1,"a":[3,{"d":null,"c":true}],"e":"x<y>&z","f":-0,"g":1e21,"h":1.5e-7}`, `{"a":"<é\n"}`, `{"a":"é"}`,
	`{"a":1,"a":2}`, `{"a": 1}`, `{"a":01}`, `{"a":1e999}`, `[1,]`, `{"a":"\x"}`, `"\ud800"`, `{"z":{"y":{"x":[1,2,3]}}}`, `123`, `"s"`,
	`{"a":tru}`, `{"a":1}x`, `{"0":0A}`, `{"a":"x"y}`, `[1x]`, "{\"a\":\"\xff\"}", `{"":0}`, `[{"b":0,"a":1},{"a":0}]`,
}

func TestAppendSortedIsAppendValueOfUnmarshal(t *testing.T) {
	for _, raw := range sortedCorpus {
		sameAsAppendValueOfUnmarshal(t, []byte(raw))
	}
}

func FuzzAppendSortedIsAppendValueOfUnmarshal(f *testing.F) {
	for _, raw := range sortedCorpus {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		sameAsAppendValueOfUnmarshal(t, raw)
	})
}

// MarshalPlanNode writes the block from its encoding; the bytes must be
// those of the block decoded and written through the map, which is what it
// always wrote.
func TestMarshalPlanNodeWritesTheBlockAsTheMapPathDoes(t *testing.T) {
	udp := true
	at := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
	blocks := []*LatticeFields{
		{LineUUID: "3f2b8c1e-7d4a-4e5f-9a6b-1c2d3e4f5a6b", LineHashID: "lh_1", NodeID: "node-1", Tags: []string{"b", "a<&>"}, Groups: []string{"g"},
			Addresses: []string{"203.0.113.1"}, Geo: &model.NodeGeo{Country: "JP", City: "東京", Lat: 35.6895, Lon: 139.6917, ASN: 2497, UpdatedAt: at},
			Chain: &model.LineCatalogueChain{Role: model.LineChainRoleEntry, Root: true, ExitGeo: &model.NodeGeo{Country: "US"}},
			Probe: &model.LineCatalogueProbe{Verdict: model.LineProbeVerdictPass, At: at, ColdP50MS: 230, UDPOK: &udp}, Provenance: "p"},
		{LineUUID: "3f2b8c1e-7d4a-4e5f-9a6b-1c2d3e4f5a6b"},
		{},
	}
	for _, block := range blocks {
		n := &Node{Fields: map[string]any{"name": "a \"b\"", "type": "vless", "port": float64(443), "reality-opts": map[string]any{"short-id": "0a", "public-key": "k"}, "geo": "script wrote this"}, Lattice: block}
		got, err := n.MarshalPlanNode()
		if err != nil {
			t.Fatal(err)
		}
		mapped, err := latticeValue(block)
		if err != nil {
			t.Fatal(err)
		}
		want, err := n.appendPlanNodeFromMap(mapped)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("MarshalPlanNode\n%s\nthe map path\n%s (%v)", got, want, err)
		}
	}
}
