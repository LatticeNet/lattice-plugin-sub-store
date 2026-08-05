package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

// scriptingOperators run operator-supplied JavaScript inside the engine. They are
// legitimate and upstream ships them, but they are the two entries whose blast
// radius is not bounded by the operator's own arguments, so they are marked for
// the UI rather than hidden.
var scriptingOperators = map[string]bool{
	"Script Operator": true,
	"Script Filter":   true,
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
}

// operatorCatalogInfo is what the UI lists so an operator can be chosen rather
// than typed from memory.
func operatorCatalogInfo() []operatorInfo {
	out := make([]operatorInfo, 0, len(operatorCatalog))
	for _, name := range operatorCatalog {
		out = append(out, operatorInfo{Type: name, Scripting: scriptingOperators[name]})
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
