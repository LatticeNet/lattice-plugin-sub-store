package main

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The catalog must equal what the pinned engine actually implements. Extracting
// the names from the bundle rather than trusting the Go list means a pin bump
// that adds or renames an operator fails here instead of drifting quietly into a
// catalog that lies.
func TestOperatorCatalogMatchesTheBundledEngine(t *testing.T) {
	source, err := os.ReadFile("lib/substore-core.js")
	if err != nil {
		t.Fatalf("read engine bundle: %v", err)
	}
	re := regexp.MustCompile(`"([A-Z][A-Za-z0-9 ]{3,30}(?:Operator|Filter))"`)
	found := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(source), -1) {
		found[m[1]] = true
	}
	var fromBundle []string
	for name := range found {
		fromBundle = append(fromBundle, name)
	}
	sort.Strings(fromBundle)

	catalog := append([]string(nil), operatorCatalog...)
	sort.Strings(catalog)

	if strings.Join(fromBundle, "|") != strings.Join(catalog, "|") {
		t.Fatalf("catalog drifted from the pinned engine.\nbundle:  %v\ncatalog: %v", fromBundle, catalog)
	}
}

// Upstream drops an operator it does not recognise and reports success, so a
// typo would otherwise produce a pipeline that quietly does nothing.
func TestValidateOperatorsRejectsUnknownTypes(t *testing.T) {
	err := validateOperators([]json.RawMessage{
		json.RawMessage(`{"type":"Flag Operator","args":{}}`),
		json.RawMessage(`{"type":"Flag Operatr","args":{}}`),
	})
	if err == nil {
		t.Fatal("a misspelled operator was accepted")
	}
	if !strings.Contains(err.Error(), "silently") {
		t.Fatalf("the error should say why this matters, got %v", err)
	}
}

func TestValidateOperatorsAcceptsEveryCatalogEntry(t *testing.T) {
	var ops []json.RawMessage
	for _, name := range operatorCatalog {
		ops = append(ops, json.RawMessage(`{"type":`+mustQuote(name)+`,"args":{}}`))
	}
	if err := validateOperators(ops); err != nil {
		t.Fatalf("a catalog entry was rejected: %v", err)
	}
}

func TestValidateOperatorsRejectsMalformedEntries(t *testing.T) {
	if err := validateOperators([]json.RawMessage{json.RawMessage(`"not an object"`)}); err == nil {
		t.Fatal("a non-object operator was accepted")
	}
	if err := validateOperators([]json.RawMessage{json.RawMessage(`{"args":{}}`)}); err == nil {
		t.Fatal("an operator with no type was accepted")
	}
	if err := validateOperators([]json.RawMessage{json.RawMessage(`{"type":"   "}`)}); err == nil {
		t.Fatal("an operator with a blank type was accepted")
	}
}

// Script operators run operator-supplied JavaScript. They are legitimate, but the
// UI needs to be able to say so, so they are marked rather than hidden.
func TestScriptingOperatorsAreMarked(t *testing.T) {
	byType := map[string]operatorInfo{}
	for _, info := range operatorCatalogInfo() {
		byType[info.Type] = info
	}
	if !byType["Script Operator"].Scripting || !byType["Script Filter"].Scripting {
		t.Fatal("script operators are not marked as scripting")
	}
	if byType["Flag Operator"].Scripting {
		t.Fatal("a non-scripting operator was marked as scripting")
	}
	if len(byType) != len(operatorCatalog)+len(fleetOperators) {
		t.Fatalf("catalog info has %d entries, catalog and fleet steps have %d", len(byType), len(operatorCatalog)+len(fleetOperators))
	}
	for _, name := range fleetOperators {
		if info := byType[name]; !info.Fleet || info.Scripting || info.Response {
			t.Fatalf("%s is listed as %+v, want fleet only", name, info)
		}
	}
	for _, name := range operatorCatalog {
		if byType[name].Fleet {
			t.Fatalf("the engine operator %s is flagged fleet", name)
		}
	}
}

// The engine does not know the Lattice-only steps and would skip them, so
// the guard on chains it runs refuses them, while a record's process
// vocabulary holds them.
func TestFleetStepsAreRecordVocabularyButNotEngineVocabulary(t *testing.T) {
	for _, name := range fleetOperators {
		if err := validateOperators([]json.RawMessage{json.RawMessage(`{"type":` + mustQuote(name) + `,"args":{}}`)}); err == nil {
			t.Fatalf("validateOperators accepted %s for the engine", name)
		}
		if !processVocabulary()[name] {
			t.Fatalf("processVocabulary does not hold %s", name)
		}
	}
}

func mustQuote(s string) string {
	out, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(out)
}
