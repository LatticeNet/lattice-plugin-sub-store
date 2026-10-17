package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Two structural rules the plan states over the code rather than over a
// behaviour, held by walking this package's own source.

// parsePackageSource parses every non-test file of this package.
func parsePackageSource(t *testing.T) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	return files
}

// exprText is a small printer for the expressions the walks compare: an
// identifier or a selector chain over identifiers.
func exprText(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprText(x.X) + "." + x.Sel.Name
	}
	return ""
}

func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

// TestEveryRecordWriteGoesThroughStoreWriteRecord: the record document
// (record-v2-<id>) is put only by storeWriteRecord's live branch
// (storeWriteRecordsIn), by storePromote inside apply_revision, by
// storeCompletePromotion, which fetch calls to roll a half-done promotion
// forward, by restoreSubscription once the same staging decision chose a
// live restore, and by migrate_store's rewrite of a legacy store (plan
// section 3). A put elsewhere would write a record no guard and no staging
// decision saw.
func TestEveryRecordWriteGoesThroughStoreWriteRecord(t *testing.T) {
	allowed := []string{"migrateStore", "restoreSubscription", "storeCompletePromotion", "storePromote", "storeWriteRecordsIn"}
	var writers []string
	for _, file := range parsePackageSource(t) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// Expressions this function assigns a record key to.
			keys := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if assign, ok := n.(*ast.AssignStmt); ok {
					for i, rhs := range assign.Rhs {
						if call, ok := rhs.(*ast.CallExpr); ok && calleeName(call) == "recordKey" && i < len(assign.Lhs) {
							keys[exprText(assign.Lhs[i])] = true
						}
					}
				}
				return true
			})
			writes := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				if name := calleeName(call); name != "kvPut" && name != "kvPutIfMatch" {
					return true
				}
				switch arg := call.Args[0].(type) {
				case *ast.CallExpr:
					writes = writes || calleeName(arg) == "recordKey"
				default:
					writes = writes || keys[exprText(arg)]
				}
				return true
			})
			if writes {
				writers = append(writers, fn.Name.Name)
			}
		}
	}
	sort.Strings(writers)
	if strings.Join(writers, ",") != strings.Join(allowed, ",") {
		t.Fatalf("record-v2 is put by %v; only %v may put it", writers, allowed)
	}
}

// methodReach is, per dispatched subscription method, whether the code it
// reaches calls a vpn-core service: a function body naming one of the
// service constants or a "latticenet.vpn-core/" literal.
func methodReach(t *testing.T) map[string]bool {
	t.Helper()
	files := parsePackageSource(t)
	calls := map[string][]string{}
	marked := map[string]bool{}
	dispatch := map[string]string{}
	markers := map[string]bool{"vpnCoreLinesService": true, "vpnCoreIdentitiesService": true, "fleetCatalogueService": true, "vpnCoreGraphService": true}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if callee := calleeName(x); callee != "" {
						calls[name] = append(calls[name], callee)
					}
				case *ast.Ident:
					if markers[x.Name] {
						marked[name] = true
					}
					// A function passed as a value is reached too.
					calls[name] = append(calls[name], x.Name)
				case *ast.BasicLit:
					if x.Kind == token.STRING {
						if value, err := strconv.Unquote(x.Value); err == nil && strings.HasPrefix(value, "latticenet.vpn-core/") {
							marked[name] = true
						}
					}
				}
				return true
			})
			if name != "handleSubscriptionCall" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				clause, ok := n.(*ast.CaseClause)
				if !ok {
					return true
				}
				var handler string
				ast.Inspect(&ast.BlockStmt{List: clause.Body}, func(m ast.Node) bool {
					if call, ok := m.(*ast.CallExpr); ok && handler == "" {
						handler = calleeName(call)
					}
					return true
				})
				for _, value := range clause.List {
					if lit, ok := value.(*ast.BasicLit); ok {
						method, _ := strconv.Unquote(lit.Value)
						dispatch[method] = handler
					}
				}
				return true
			})
		}
	}
	if len(dispatch) == 0 {
		t.Fatal("found no dispatched method")
	}
	out := map[string]bool{}
	for method, handler := range dispatch {
		seen := map[string]bool{}
		queue := []string{handler}
		for len(queue) > 0 && !out[method] {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			if marked[name] {
				out[method] = true
			}
			queue = append(queue, calls[name]...)
		}
		if !out[method] {
			out[method] = false
		}
	}
	return out
}

// TestProxyMethodsDeclareVpncoreRead: core exempts a plugin's rpc.call from
// the vpncore:read check it applies to an operator's direct catalogue read,
// so every method an operator can call whose reply can carry catalogue rows,
// identities, graph options or plan nodes declares vpncore:read beside its
// sub-store scope (plan sections 4 and 10). The walk finds every dispatched
// method whose code reaches a vpn-core service; each must declare the scope,
// be core-only at the gateway, or be listed here with why its reply carries
// none of it. Every method that declares the scope must reach a vpn-core
// service, so no grant goes unused.
func TestProxyMethodsDeclareVpncoreRead(t *testing.T) {
	// fetch and apply_revision are core-only at the gateway (core's
	// subStoreCoreOnlyMethod), so no operator reaches them.
	coreOnly := map[string]bool{"fetch": true, "apply_revision": true}
	exempt := map[string]string{
		"probe":   "answers a byte count, a digest and an error code; the rows stay in the plugin",
		"save":    "reads one catalogue page for its unavailable list and answers the record it wrote",
		"publish": "refuses a fleet-bound record (fleet_publish_unavailable) and sends a provider document to the operator's destination; it answers the destination's status",
	}
	reach := methodReach(t)
	declared := map[string][]string{}
	for _, iface := range loadManifestInterfaces(t) {
		if iface.Service != pluginID+"/subscription" {
			continue
		}
		for _, method := range iface.Methods {
			declared[method.Name] = method.Scopes
		}
	}
	for method, reaches := range reach {
		scopes, ok := declared[method]
		if !ok {
			continue
		}
		hasRead := slices.Contains(scopes, "vpncore:read")
		switch {
		case reaches && !hasRead && !coreOnly[method] && exempt[method] == "":
			t.Errorf("%s reaches a vpn-core service and declares %v without vpncore:read", method, scopes)
		case hasRead && !reaches:
			t.Errorf("%s declares vpncore:read and reaches no vpn-core service: an unused grant", method)
		case !reaches && exempt[method] != "":
			t.Errorf("%s is exempt (%s) but reaches no vpn-core service; drop the exemption", method, exempt[method])
		}
	}
	// migrate_record's warnings say whether a root is in the catalogue and
	// whether a name-based step matches a line label (S2-SR-4).
	for _, method := range []string{"fleet_preview", "identities", "graph_options", "preview", "preview_draft", "render", "migrate_record"} {
		if !reach[method] || !slices.Contains(declared[method], "vpncore:read") {
			t.Errorf("%s: reaches %v, scopes %v; it relays vpn-core data and must declare vpncore:read", method, reach[method], declared[method])
		}
	}
}
