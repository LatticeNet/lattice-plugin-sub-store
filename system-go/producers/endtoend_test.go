package producers

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/parse"
)

// TestProduceEndToEndFromOwnParse is check.mjs --end-to-end in process: every
// corpus case whose parse golden is a node list is parsed by system-go/parse,
// its nodes go through the JSON form the runner answers with (keys sorted,
// every number a float64), and the producers' output from them is judged
// against the produce goldens by the checker's rule. It shows the producers
// do not lean on the key order or the value types the golden JSON happens to
// have.
//
// Where the parse itself diverges from the golden, under the parse-stage
// allowlist (external, underscore, ca), the produce golden describes nodes
// Lattice deliberately does not build, so the case is not judged against it.
// Such a case must still differ from its golden only by dropped nodes and
// deleted keys, which is all the three entries allow.
//
// The nodes exactly as the parser built them must produce the same output as
// their JSON form, except where they hold a value JSON cannot carry: the
// model keeps not-a-number and the infinities (TestNormaliseNotANumberValues
// in system-go/normalise), JSON writes them as null, and the producers treat
// the two as JavaScript does. Those cases are listed in the log.
func TestProduceEndToEndFromOwnParse(t *testing.T) {
	judged := map[string]int{}
	passed := map[string]int{}
	var divergent, notJSON []string
	for _, c := range produceCases(t) {
		input, err := os.ReadFile(c.inputPath)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _, err := parse.Document(string(input), parse.Options{})
		if err != nil {
			t.Errorf("%s: the golden is a node list, but the parse failed: %v", c.id, err)
			continue
		}
		wire, err := json.Marshal(parsed)
		if err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		var own, golden []any
		if err := json.Unmarshal(wire, &own); err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		if err := json.Unmarshal(c.nodes, &golden); err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		exact := reflect.DeepEqual(own, golden)
		if !exact {
			divergent = append(divergent, c.id)
			if at := notRemovalOnly(golden, own); at != "" {
				t.Errorf("%s: the parse differs from the golden by more than dropped nodes and deleted keys, at %s", c.id, at)
			}
		}
		var fromWire []*nodemodel.Node
		if err := json.Unmarshal(wire, &fromWire); err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		jsonSafe := true
		for _, n := range parsed {
			jsonSafe = jsonSafe && onlyJSONValues(n.Fields)
		}
		if !jsonSafe {
			notJSON = append(notJSON, c.id)
		}
		for _, target := range harnessTargets {
			if _, err := os.Stat(filepath.Join(conformanceDir, "goldens", "produce", target.id, c.id+".canon.json")); err != nil {
				continue
			}
			got, _ := produce(t, target.platform, fromWire, c.options)
			if inProcess, _ := produce(t, target.platform, parsed, c.options); jsonSafe && inProcess != got {
				t.Errorf("%s/%s: the parser's nodes and their JSON form produce different output at byte %d\n parser:    %q\n JSON form: %q", target.id, c.id, firstDifference(inProcess, got), clip(inProcess), clip(got))
			}
			if !exact {
				continue
			}
			judged[target.id]++
			if diff := differsFromGolden(t, target, c.id, got); diff != "" {
				t.Errorf("%s/%s: from the parser's own nodes, %s", target.id, c.id, diff)
				continue
			}
			passed[target.id]++
		}
	}
	for _, target := range harnessTargets {
		if judged[target.id] == 0 {
			t.Errorf("%s: no case was judged end to end", target.id)
		}
		t.Logf("%s: %d/%d cases match end to end", target.id, passed[target.id], judged[target.id])
	}
	t.Logf("%d cases parse under the allowlist and are not judged against the produce goldens: %s", len(divergent), strings.Join(divergent, ", "))
	t.Logf("%d cases hold a value JSON writes as null, so their in-process output is not compared: %s", len(notJSON), strings.Join(notJSON, ", "))
}

// notRemovalOnly reports the first own node that is not a golden node with
// keys deleted at some depth, taken in order (dropped golden nodes are
// skipped), or "" when every own node is.
func notRemovalOnly(golden, own []any) string {
	j := 0
	for i, n := range own {
		for j < len(golden) && !keysDeletedOnly(golden[j], n) {
			j++
		}
		if j == len(golden) {
			return fmt.Sprintf("$[%d]", i)
		}
		j++
	}
	return ""
}

// keysDeletedOnly reports whether own is golden with object keys deleted, at
// any depth; lists keep their length and scalars their value.
func keysDeletedOnly(golden, own any) bool {
	switch o := own.(type) {
	case map[string]any:
		g, ok := golden.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range o {
			if gv, ok := g[k]; !ok || !keysDeletedOnly(gv, v) {
				return false
			}
		}
		return true
	case []any:
		g, ok := golden.([]any)
		if !ok || len(g) != len(o) {
			return false
		}
		for i := range o {
			if !keysDeletedOnly(g[i], o[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(golden, own)
}

// onlyJSONValues reports whether v holds no not-a-number or infinite float,
// at any depth: the values the model keeps and JSON writes as null.
func onlyJSONValues(v any) bool {
	switch x := v.(type) {
	case float64:
		return !math.IsNaN(x) && !math.IsInf(x, 0)
	case map[string]any:
		for _, e := range x {
			if !onlyJSONValues(e) {
				return false
			}
		}
	case []any:
		for _, e := range x {
			if !onlyJSONValues(e) {
				return false
			}
		}
	}
	return true
}
