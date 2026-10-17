package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// The envelope and its digest splice the encoded rows in place of a slot;
// both must be the bytes json.Marshal writes for the same value.
func TestFleetEnvelopeSpliceIsJSONMarshal(t *testing.T) {
	rows := make([]model.LineCatalogueRow, 0, 3)
	for i := 1; i <= 3; i++ {
		row := fleetProductionRow(i)
		row.Label = `Tokyo <1> & "a" ` + "  é"
		rows = append(rows, row)
	}
	for _, projected := range [][]fleetRow{projectRows(rows), nil} {
		read := fleetCatalogueRead{CatalogueVersion: fleetTestVersion, Rows: projected}
		env, err := fleetSubEnvelope(read, fleetSelection{})
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		got, err := encodeFleetEnvelope(env, len(projected))
		if err != nil || got != string(want) {
			t.Fatalf("envelope\n%s\njson.Marshal\n%s (%v)", got, want, err)
		}
		canonical, err := json.Marshal(struct {
			CatalogueVersion string               `json:"catalogue_version"`
			Selector         *fleetSelectorRecord `json:"selector"`
			Rows             json.RawMessage      `json:"rows"`
		}{env.CatalogueVersion, env.Selector, env.Rows})
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(canonical)
		if digest := fleetEnvelopeDigestPrefix + hex.EncodeToString(sum[:]); env.SourceVersion != digest {
			t.Fatalf("digest %s, json.Marshal's %s", env.SourceVersion, digest)
		}
	}
	// Rows that are not canonical take json.Marshal.
	raw, err := marshalSplicing(struct {
		Rows json.RawMessage `json:"rows"`
	}{rowsSlot}, "rows", rowsSlot, json.RawMessage(`[ {"a":"<"} ]`), func() ([]byte, error) { return []byte("plain"), nil })
	if err != nil || string(raw) != "plain" {
		t.Fatalf("non-canonical rows: %s, %v", raw, err)
	}
}

// The splice finds its member by the slot's text in json.Marshal's encoding
// of the rest. Another raw member may carry that same text, before the slot
// or after it; the value is then marshalled whole, so the raw value never
// lands inside the other member.
func TestSpliceNeverPlacesAtASecondSlot(t *testing.T) {
	type value struct {
		Before json.RawMessage `json:"before"`
		Rows   json.RawMessage `json:"rows"`
		After  json.RawMessage `json:"after"`
	}
	rows := json.RawMessage(`[{"a":1},{"b":"x"}]`)
	decoy := json.RawMessage(`{"rows":"\u0000rows","plan":"\u0000plan"}`)
	for name, v := range map[string]value{
		"slot text before":  {Before: decoy, After: json.RawMessage(`{}`)},
		"slot text after":   {Before: json.RawMessage(`{}`), After: decoy},
		"slot text in both": {Before: decoy, After: decoy},
	} {
		real := v
		real.Rows = rows
		want, err := json.Marshal(real)
		if err != nil {
			t.Fatal(err)
		}
		slotted := v
		slotted.Rows = rowsSlot
		if parts, err := splitAtSlot(slotted, "rows", rowsSlot, rows); err != nil || parts != nil {
			t.Fatalf("%s: split at a slot whose text occurs twice: %q, %v", name, parts, err)
		}
		got, err := marshalSplicing(slotted, "rows", rowsSlot, rows, func() ([]byte, error) { return json.Marshal(real) })
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s:\n%s\njson.Marshal\n%s (%v)", name, got, want, err)
		}
	}
	// The render reply and the envelope go through the same splice.
	out := renderReply{renderResult: renderResult{Content: `{"plan":"\u0000plan"}`, ContentType: "application/json"}, Plan: json.RawMessage(`{"kind":"nodes","nodes":[]}`), LiveRevision: "r1"}
	got, err := marshalRenderReply(out)
	if want, _ := json.Marshal(out); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("render reply:\n%s\njson.Marshal\n%s (%v)", got, want, err)
	}
}
