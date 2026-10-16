package main

import (
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
	raw, err := marshalSplicingRows(struct {
		Rows json.RawMessage `json:"rows"`
	}{rowsSlot}, json.RawMessage(`[ {"a":"<"} ]`), func() ([]byte, error) { return []byte("plain"), nil })
	if err != nil || string(raw) != "plain" {
		t.Fatalf("non-canonical rows: %s, %v", raw, err)
	}
}
