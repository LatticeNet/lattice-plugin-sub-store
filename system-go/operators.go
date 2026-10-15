package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
)

// operatorCatalog is every operator type the pinned Sub-Store core implements.
//
// It is not documentation. Upstream's process() **silently ignores an operator
// whose type it does not recognise**, so a typo produces a pipeline that reports
// success and does nothing. This list is what turns that into an error.
//
// The names are not invented here: they are the literals in the bundled
// lib/substore-core.js, and a test re-extracts them from that file and fails if
// the two disagree. A pin bump that adds or renames an operator therefore breaks
// the build rather than drifting quietly.
var operatorCatalog = []string{
	"Add Proxies From Subscription Operator",
	"Conditional Filter",
	"Flag Operator",
	"Handle Duplicate Operator",
	"Quick Setting Operator",
	"Regex Delete Operator",
	"Regex Filter",
	"Regex Rename Operator",
	"Regex Sort Operator",
	"Region Filter",
	"Remove Duplicate Filter",
	"Resolve Domain Operator",
	"Script Filter",
	"Script Operator",
	"Sort Operator",
	"Type Filter",
	"Useless Filter",
}

// fleetOperators are the two Lattice-only steps (design 28; S2 plan section
// 2.4), Structured Filter and Structured Sort. They are not in
// operatorCatalog, which is pinned to the bundled engine's literals: the
// engine does not know them and would skip them, so validateOperators, which
// guards chains the engine runs, keeps refusing them. They run in Go only,
// over the fleet block a fleet node carries, and the process vocabulary a
// record's chain is checked against holds them (processVocabulary).
var fleetOperators = operators.FleetVocabulary()

// scriptingOperators run operator-supplied JavaScript inside the engine. They are
// legitimate and upstream ships them, but they are the two entries whose blast
// radius is not bounded by the operator's own arguments, so they are marked for
// the UI rather than hidden.
var scriptingOperators = map[string]bool{
	"Script Operator": true,
	"Script Filter":   true,
}

// responseOperators run on the response path, where a step receives the whole
// response rather than a node list.
//
// They are deliberately NOT in operatorCatalog: that list is pinned to the proxy
// operators the bundled engine exposes, and the engine skips proxy operators for
// responses just as it skips these for nodes. Two chains, two vocabularies —
// merging them would let either kind be accepted where it does nothing.
var responseOperators = map[string]bool{
	"Response Transformer": true,
}

func responseOperatorInfo() []operatorInfo {
	out := make([]operatorInfo, 0, len(responseOperators))
	for name := range responseOperators {
		out = append(out, operatorInfo{Type: name, Scripting: true, Response: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

func operatorCatalogSet() map[string]bool {
	set := make(map[string]bool, len(operatorCatalog))
	for _, name := range operatorCatalog {
		set[name] = true
	}
	return set
}

type operatorInfo struct {
	Type      string `json:"type"`
	Scripting bool   `json:"scripting"`
	// Response marks a step that runs over a served document rather than a node
	// list. The two chains do not mix, and the UI needs to know which is which
	// to avoid offering a step the engine would skip.
	Response bool `json:"response,omitempty"`
	// Fleet marks a Lattice-only step: it reads the fleet block of a fleet
	// node, so the editor offers it on fleet-bound records only (a provider
	// node has no fleet block, and every predicate over it is unknown).
	Fleet bool `json:"fleet,omitempty"`
}

// operatorCatalogInfo is what the UI lists so an operator can be chosen rather
// than typed from memory: the engine's proxy operators and the Lattice-only
// steps, each flagged.
func operatorCatalogInfo() []operatorInfo {
	out := make([]operatorInfo, 0, len(operatorCatalog)+len(fleetOperators))
	for _, name := range operatorCatalog {
		out = append(out, operatorInfo{Type: name, Scripting: scriptingOperators[name]})
	}
	for _, name := range fleetOperators {
		out = append(out, operatorInfo{Type: name, Fleet: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// validateOperators refuses an operator the engine would ignore.
//
// This exists because of how upstream fails: an unrecognised type is dropped
// without complaint, so the conversion succeeds, the output is wrong, and nothing
// says so. Refusing here converts a silent wrong answer into a loud one.
func validateOperators(operators []json.RawMessage) error {
	known := operatorCatalogSet()
	for i, raw := range operators {
		var op struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &op); err != nil {
			return fmt.Errorf("operator %d is not an object: %w", i, err)
		}
		name := strings.TrimSpace(op.Type)
		if name == "" {
			return fmt.Errorf("operator %d has no type", i)
		}
		if !known[name] {
			return fmt.Errorf("operator %d has unknown type %q; the engine would ignore it silently", i, name)
		}
	}
	return nil
}

func containsScriptingOperator(operators []json.RawMessage) bool {
	for _, raw := range operators {
		var operator struct {
			Type string `json:"type"`
		}
		// Trim exactly as validateOperators does, so classification can never
		// disagree with validation about whether a chain carries user script.
		if json.Unmarshal(raw, &operator) == nil && scriptingOperators[strings.TrimSpace(operator.Type)] {
			return true
		}
	}
	return false
}

// savedChainCheck is the save-time compilation of a chain (design 28, "Node
// filtering"; S1 plan section 3.1). A save that brings in a pattern RE2
// refuses, by adding a step or by changing one, is refused with the
// regex_incompatible code, each step and pattern named and the rewrite
// offered where the idiom has one. A pattern the stored chain already
// carried is not refused again: a record the migration flagged keeps saving
// when an unrelated field changes, and keeps rendering on the bundle until
// its chain is edited.
//
// "Already carried" is counted per step type and pattern, not compared as
// bytes, because the editor rewrites a chain's JSON when it opens it.
func savedChainCheck(stored, incoming []json.RawMessage) error {
	if _, err := operators.CompileStrict(incoming); err == nil {
		return nil
	} else if _, refused := operators.AsIncompatible(err); !refused {
		return err
	}
	plan, err := operators.Compile("", incoming)
	if err != nil {
		return err
	}
	carried := map[[2]string]int{}
	if before, err := operators.Compile("", stored); err == nil {
		for _, d := range before.Incompatible() {
			carried[[2]string{before.Steps[d.Step-1].Step.Type, d.Pattern}]++
		}
	}
	var brought []operators.Diagnostic
	for _, d := range plan.Incompatible() {
		key := [2]string{plan.Steps[d.Step-1].Step.Type, d.Pattern}
		if carried[key] > 0 {
			carried[key]--
			continue
		}
		brought = append(brought, d)
	}
	if len(brought) == 0 {
		return nil
	}
	return &operators.IncompatibleError{Diagnostics: brought}
}
