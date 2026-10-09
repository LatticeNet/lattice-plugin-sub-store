package parse

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The parsers are imported by the conformance runner and by the native
// render path, and must stay free of package main, the SDK and the script
// engine (S1 plan section 1.1). Their non-test files may import the standard
// library, the model, the normaliser and the YAML library; nothing else.
func TestParsePackageImportsOnlyTheModelAndTheStandardLibrary(t *testing.T) {
	allowed := []string{
		"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel",
		"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise",
		"gopkg.in/yaml.v3",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			standard := !strings.Contains(strings.SplitN(path, "/", 2)[0], ".")
			if standard || slices.Contains(allowed, path) {
				continue
			}
			t.Errorf("%s imports %s", file, path)
		}
	}
	if checked == 0 {
		t.Fatal("no source files found")
	}
}
