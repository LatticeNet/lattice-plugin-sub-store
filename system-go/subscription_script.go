package main

import (
	"fmt"
	"strings"
)

// A SCRIPT file is a document a JavaScript program builds.
//
// The other two file types treat the stored text as the document: a config gets
// its `proxies` replaced, plain text is served as written. Neither can express
// the shape operators actually use upstream, where the script IS the file — it
// asks the store for a named collection's nodes, assembles rules, DNS, groups
// and listeners itself, and assigns the result to `$content`.
//
// Storage is deliberately split. Generator scripts run 25–60 KB each, and the
// record document is one KV value holding every subscription, combination and
// file: putting scripts inside it would mean re-encoding half a megabyte of
// JavaScript on every unrelated edit, and would run the document into its own
// 1 MB cap after a dozen files. Each script gets its own key instead, and the
// record carries none of it.
const fileTypeScript = "script"

// maxFileScriptBytes bounds one script. Comfortably above the largest real
// generator, and low enough that a runaway paste cannot fill the store.
const maxFileScriptBytes = 512 << 10

// fileScriptKey is where one file's program lives. Versioned like the record
// document so a future format change does not have to guess what it is reading.
//
// The separator is a dash, not a slash: the server's plugin KV validates keys
// with validateStorageName and refuses slashes outright (a slash would let one
// record masquerade as another in composite paths). The slash spelling of this
// key never survived contact with a real host — every test host accepted it,
// and production answered "plugin kv key must not contain a slash" on the first
// script save (2026-08-11).
func fileScriptKey(id string) string {
	return "subscription-script-v1-" + id
}

// putFileScript stores a program under its own key.
func (rt *runtime) putFileScript(id, script string) error {
	if len(script) > maxFileScriptBytes {
		return fmt.Errorf("file %q script is too large: %d bytes, limit %d", id, len(script), maxFileScriptBytes)
	}
	return rt.kvPut(fileScriptKey(id), []byte(script))
}

// getFileScript returns a program, or "" when the file has none stored.
func (rt *runtime) getFileScript(id string) (string, error) {
	value, found, err := rt.kvGet(fileScriptKey(id))
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	return string(value), nil
}

// clearFileScript empties a program's key.
//
// The host exposes kv.get and kv.put and no delete, so this writes zero bytes
// rather than removing the key. An empty value costs nothing and the record
// count is capped, so the worst case is a bounded number of empty keys — but it
// IS a tombstone rather than a deletion, and the difference matters if the key
// space is ever enumerated.
func (rt *runtime) clearFileScript(id string) error {
	return rt.kvPut(fileScriptKey(id), nil)
}

// isScriptFile reports whether a record's document is built by a program.
func isScriptFile(rec subscriptionRecord) bool {
	return recordKind(rec) == kindFile && rec.FileType == fileTypeScript
}

// allowedQueryParams is the set of URL parameters a record lets through to its
// script, lower-cased for matching.
//
// A share URL is public: anything in its query is attacker-controlled input. A
// script that switches behaviour on a query parameter is a legitimate and useful
// thing — an operator toggling DNS mode per client — but only for the parameters
// that operator chose. Everything else is dropped before the script can see it.
func allowedQueryParams(rec subscriptionRecord) map[string]bool {
	if len(rec.QueryParams) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(rec.QueryParams))
	for _, name := range rec.QueryParams {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			allowed[name] = true
		}
	}
	return allowed
}

// filterQuery keeps only the parameters the record declared.
func filterQuery(rec subscriptionRecord, query map[string]string) map[string]string {
	allowed := allowedQueryParams(rec)
	if len(allowed) == 0 || len(query) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range query {
		if allowed[strings.ToLower(strings.TrimSpace(key))] {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
