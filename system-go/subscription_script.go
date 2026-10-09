package main

import (
	"strings"
)

// A SCRIPT file is a document a JavaScript program builds.
//
// The other two file types treat the stored text as the document: a config gets
// its `proxies` replaced, plain text is served as written. Neither can express
// the shape operators actually use upstream, where the script IS the file: it
// asks the store for a named collection's nodes, assembles rules, DNS, groups
// and listeners itself, and assigns the result to `$content`.
//
// The program lives in the record's Content on the split store. The legacy
// store kept it under its own key, because every record shared one document
// with a 1 MiB cap; with one key per record that reason is gone, and a
// separate key would cost get, render and export one host call per script.
const fileTypeScript = "script"

// fileScriptKey is where the legacy store kept one file's program. Only
// migrate_store and the legacy reader read it, and migrate_store deletes it
// once the split store is verified.
//
// The separator is a dash, not a slash: the server's plugin KV validates keys
// with validateStorageName and refuses slashes outright.
func fileScriptKey(id string) string {
	return "subscription-script-v1-" + id
}

// getFileScript returns a legacy program, or "" when none is stored.
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
