package normalise

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The model and the normaliser are imported by the conformance runner and by
// the parsers and producers, and must stay free of package main, the SDK and
// the script engine (S1 plan section 1.1). Their non-test files may import
// the standard library and, for normalise, nodemodel; nothing else. The
// exceptions are S2's: nodemodel's fleet block is typed with the SDK's
// catalogue types (S2 plan section 2.3), so nodemodel may import the SDK's
// model package, and it decodes bound nodes with the SDK's strictjson; model
// imports only the standard library and strictjson, and strictjson only the
// standard library.
func TestModelPackagesImportOnlyTheStandardLibrary(t *testing.T) {
	const nodemodelPath = "github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	const sdkModelPath = "github.com/LatticeNet/lattice-sdk/model"
	const sdkStrictJSONPath = "github.com/LatticeNet/lattice-sdk/strictjson"
	for dir, allowed := range map[string][]string{
		".":            {nodemodelPath},
		"../nodemodel": {sdkModelPath, sdkStrictJSONPath},
	} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
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
			t.Errorf("no source files found in %s", dir)
		}
	}
}
