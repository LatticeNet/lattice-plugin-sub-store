package fleetfixture

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/LatticeNet/lattice-sdk/model"
)

var fixedNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// The rows are SDK rows and pass the SDK's own row check, so a consumer that
// validates every row once, as the fleet fetch does, accepts the fixture.
func TestRowsAreValidSDKRows(t *testing.T) {
	rows := Rows(1000, 1, fixedNow)
	for _, r := range rows {
		if err := r.Validate(); err != nil {
			t.Fatalf("row does not validate: %v", err)
		}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var back model.LineCatalogueRow
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(back, r) {
			t.Fatalf("row does not survive the wire:\n%+v\n%+v", r, back)
		}
	}
}

func TestRowsAreDeterministic(t *testing.T) {
	a, b := Rows(200, 7, fixedNow), Rows(200, 7, fixedNow)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the same seed gave different rows")
	}
	if reflect.DeepEqual(a, Rows(200, 8, fixedNow)) {
		t.Fatal("two seeds gave the same rows")
	}
	seen := map[string]bool{}
	for _, r := range a {
		if seen[r.LineUUID] {
			t.Fatalf("line_uuid %s repeats", r.LineUUID)
		}
		seen[r.LineUUID] = true
	}
}

// The mix is what the differential test relies on: enough probe blocks,
// chains, unresolved relays with their own geo, and null blocks.
func TestRowsCarryTheMix(t *testing.T) {
	rows := Rows(3000, 3, fixedNow)
	var probe, chained, unresolved, nullGeo, exitGeo int
	for _, r := range rows {
		if r.Probe != nil {
			probe++
		}
		if r.Chain.Role != model.LineChainRoleSingle {
			chained++
		}
		if r.Chain.Unresolved {
			unresolved++
			if r.Geo == nil || r.Geo.Country == "" {
				t.Fatal("an unresolved relay has no own geo, so the unknown rule is not exercised")
			}
			if r.Chain.ExitGeo != nil {
				t.Fatal("an unresolved relay carries an exit geo")
			}
		}
		if r.Geo == nil {
			nullGeo++
		}
		if r.Chain.ExitGeo != nil {
			exitGeo++
		}
	}
	within := func(name string, got, n int) {
		t.Helper()
		want := len(rows) / n
		if got < want*2/3 || got > want*4/3 {
			t.Errorf("%s on %d rows, want about %d", name, got, want)
		}
	}
	within("probe blocks", probe, 3)
	within("unresolved relays", unresolved, 20)
	within("null geo", nullGeo, 10)
	if chained < len(rows)/6 || exitGeo == 0 {
		t.Errorf("%d chained rows, %d with an exit geo", chained, exitGeo)
	}
}

func TestNodeCarriesTheRowsLatticeFields(t *testing.T) {
	for _, r := range Rows(200, 11, fixedNow) {
		n := Node(r)
		l := n.Lattice
		if n.Name() != r.Label || n.Type() != r.Protocol || n.Server() != r.Template.Host {
			t.Fatalf("node fields %v do not follow row %s", n.Fields, r.LineUUID)
		}
		if l.LineUUID != r.LineUUID || l.NodeID != r.NodeID || l.Geo != r.Geo || l.Probe != r.Probe ||
			!reflect.DeepEqual(*l.Chain, r.Chain) || !reflect.DeepEqual(l.Tags, r.NodeTags) ||
			!reflect.DeepEqual(l.Groups, r.GroupIDs) || l.Machine != r.Machine ||
			l.Protocol != r.Protocol || l.Transport != r.Transport || l.Security != r.Security ||
			l.Status != r.Status || l.ServiceState != r.ServiceState || l.OverlayStatus != r.OverlayStatus ||
			l.Managed != r.Managed || l.Overlay != r.Overlay || l.NodeName != r.NodeName ||
			!reflect.DeepEqual(l.DDNSNames, r.DDNSNames) {
			t.Fatalf("lattice block %+v does not follow row %+v", l, r)
		}
	}
}
