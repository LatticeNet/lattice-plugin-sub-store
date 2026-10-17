package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

func sameAsJSONMarshal(t *testing.T, plan model.SelectionPlan) {
	t.Helper()
	want, wantErr := json.Marshal(plan)
	got, gotErr := appendSelectionPlan(nil, plan)
	if (gotErr == nil) != (wantErr == nil) || gotErr == nil && !bytes.Equal(got, want) {
		t.Fatalf("appendSelectionPlan\n%s (%v)\njson.Marshal\n%s (%v)", got, gotErr, want, wantErr)
	}
}

func TestAppendSelectionPlanIsJSONMarshal(t *testing.T) {
	line := fleetTestLineUUID(1)
	placeholder, err := model.NewPlanPlaceholder(line, "uuid")
	if err != nil {
		t.Fatal(err)
	}
	nodes := []json.RawMessage{json.RawMessage(`{"name":"a & b <c>","port":443,"tags":["x"]}`), json.RawMessage(`{"name":" "}`),
		json.RawMessage("{\"name\":\" \"}"), json.RawMessage(` {"a" : 1} `), json.RawMessage(`{"a":"é"}`), json.RawMessage(`[1]`),
		json.RawMessage(`{"a":`), json.RawMessage{}, nil, json.RawMessage(`{"a":1,"a":2}`), json.RawMessage(`{"n":1e999}`)}
	for _, node := range nodes {
		for _, plan := range []model.SelectionPlan{
			{Kind: model.SelectionPlanKindNodes, Nodes: []model.SelectionPlanNode{{LineUUID: line, Placeholders: map[string]string{"uuid": placeholder, "password": "p<w>"}, Node: node}}},
			{Kind: model.SelectionPlanKindNodes, Nodes: []model.SelectionPlanNode{{Provider: true, Node: node}, {Node: node}},
				Selection: &model.PlanSelection{CatalogueVersion: "lcv1-a&b", LineUUIDs: []string{line, "x\"y"}},
				Policy:    &model.BindPolicy{DDNSDial: true, Probe: &model.ProbeExclusionPolicy{ConsecutiveFailures: 3}}},
			{Kind: model.SelectionPlanKindDocument, Document: "proxies:\n  - <x> &  ", Nodes: []model.SelectionPlanNode{{LineUUID: line, Node: node}},
				ResponseChain: []model.ResponseTransformerStep{{Name: "n", Source: "s<", Arguments: json.RawMessage(`{"a": 1}`), Enabled: true}}},
		} {
			sameAsJSONMarshal(t, plan)
		}
	}
	for _, plan := range []model.SelectionPlan{{}, {Kind: "nodes", Nodes: []model.SelectionPlanNode{}}, {Kind: "nodes", Selection: &model.PlanSelection{}}} {
		sameAsJSONMarshal(t, plan)
	}
}

func FuzzAppendSelectionPlanIsJSONMarshal(f *testing.F) {
	f.Add([]byte(`{"name":"a & b","port":443}`), "uuid", "LATTICE-SYN-x", "lcv1-v", "doc")
	f.Add([]byte(` [1, "<"] `), "k ", "v\x00", "", "")
	f.Fuzz(func(t *testing.T, node []byte, key, value, version, document string) {
		kind := model.SelectionPlanKindNodes
		if document != "" {
			kind = model.SelectionPlanKindDocument
		}
		sameAsJSONMarshal(t, model.SelectionPlan{Kind: kind, Document: document,
			Nodes:     []model.SelectionPlanNode{{LineUUID: key, Placeholders: map[string]string{key: value, "z": version}, Node: node}, {Provider: true, Node: node}},
			Selection: &model.PlanSelection{CatalogueVersion: version, LineUUIDs: []string{key, value}}})
	})
}

// appendSelectionPlan writes SelectionPlan, SelectionPlanNode and
// PlanSelection member by member, and the equality tests populate only the
// fields it knows, so a field added to any of them in the SDK would be
// dropped from the wire with every test green. This names every field the
// encoder writes, with its type and JSON tag, and fails on any other: a new
// field needs a member in appendSelectionPlanChecked or
// appendSelectionPlanNode and a line here. ResponseChain and Policy are
// written by json.Marshal, so their own fields need nothing.
func TestPlanEncoderWritesEveryPlanField(t *testing.T) {
	for _, c := range []struct {
		value any
		want  []string
	}{
		{model.SelectionPlan{}, []string{"Kind string kind", "Nodes []model.SelectionPlanNode nodes", "Document string document,omitempty",
			"ResponseChain []model.ResponseTransformerStep response_chain,omitempty", "Selection *model.PlanSelection selection,omitempty",
			"Policy *model.BindPolicy policy,omitempty"}},
		{model.SelectionPlanNode{}, []string{"LineUUID string line_uuid,omitempty", "Placeholders map[string]string placeholders,omitempty",
			"Provider bool provider,omitempty", "Node json.RawMessage node"}},
		{model.PlanSelection{}, []string{"CatalogueVersion string catalogue_version", "LineUUIDs []string line_uuids"}},
	} {
		typ := reflect.TypeOf(c.value)
		var got []string
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			got = append(got, f.Name+" "+f.Type.String()+" "+f.Tag.Get("json"))
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s has fields %q; appendSelectionPlan writes %q", typ, got, c.want)
		}
	}
}

// marshalRenderReply splices the plan; the bytes must be json.Marshal's.
func TestMarshalRenderReplyIsJSONMarshal(t *testing.T) {
	plans := []json.RawMessage{nil, json.RawMessage(`{"kind":"nodes","nodes":[{"node":{"name":"a"}}]}`), json.RawMessage(`{"a": 1}`),
		json.RawMessage(`{"a":"<"}`), json.RawMessage(`{"plan":"\u0000plan"}`)}
	for _, plan := range plans {
		for _, out := range []renderReply{
			{renderResult: renderResult{ContentType: "application/json", Target: "sing-box", NodeCount: 3}, Plan: plan, LiveRevision: "r1"},
			{Plan: plan},
			{renderResult: renderResult{Content: "x & <y>"}, Plan: plan},
		} {
			want, wantErr := json.Marshal(out)
			got, gotErr := marshalRenderReply(out)
			if (gotErr == nil) != (wantErr == nil) || !bytes.Equal(got, want) {
				t.Fatalf("render reply\n%s (%v)\njson.Marshal\n%s (%v)", got, gotErr, want, wantErr)
			}
		}
	}
}
