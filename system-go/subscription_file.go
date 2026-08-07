package main

import (
	"encoding/json"
	"fmt"
	"strings"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// A FILE is a document the core serves whose node list is filled in from a
// subscription.
//
// The point is the separation it buys: an operator keeps one client
// configuration they have tuned — rules, DNS, proxy groups — and the nodes
// inside it follow whatever the named subscription currently resolves to.
// Without this, every node change means hand-editing every config.
//
// Files live in the same store as subscriptions and combinations, behind the
// same `kind` discriminator, so list, get, save and delete already work and the
// signed interface needs no new methods.

const kindFile = "file"

func fileType(rec subscriptionRecord) string {
	if rec.FileType == fileTypePlain {
		return fileTypePlain
	}
	return fileTypeConfig
}

// resolveFileTemplate returns the document before nodes are injected.
func (rt *runtime) resolveFileTemplate(rec subscriptionRecord) (string, error) {
	switch rec.Source {
	case subscriptionSourceRemote:
		if strings.TrimSpace(rec.URL) == "" {
			return "", fmt.Errorf("file %q has no template URL", rec.ID)
		}
		fetched, err := rt.fetchSubscription(rec.ID)
		if err != nil {
			return "", err
		}
		return fetched.Raw, nil
	default:
		if strings.TrimSpace(rec.Content) == "" {
			return "", fmt.Errorf("file %q has no content", rec.ID)
		}
		return rec.Content, nil
	}
}

// renderFile produces the document the core will serve.
func (rt *runtime) renderFile(rec subscriptionRecord, uaClass string) (string, error) {
	template, err := rt.resolveFileTemplate(rec)
	if err != nil {
		return "", err
	}

	operators, err := enabledOperators(rec)
	if err != nil {
		return "", fmt.Errorf("file %q: %w", rec.ID, err)
	}

	// Plain text has no node list to fill: its operations run over the document
	// through the response-transform path, and what comes out is served.
	if fileType(rec) == fileTypePlain {
		if len(operators) == 0 {
			return template, nil
		}
		out, err := rt.subStoreEngine().transformResponse(subStoreResponseTransformRequest{
			Response:  mustJSON(map[string]any{"status": 200, "headers": map[string]any{}, "body": template}),
			Operators: operators,
		})
		if err != nil {
			return "", fmt.Errorf("file %q: %w", rec.ID, err)
		}
		if strings.TrimSpace(out.Body) == "" {
			return "", fmt.Errorf("file %q produced no content", rec.ID)
		}
		return out.Body, nil
	}

	// A config with no node source is a document the operator maintains
	// entirely by hand — rules, a script, a fragment. Serving it unchanged is
	// the correct answer, not an error.
	nodes := ""
	if source := strings.TrimSpace(rec.NodeSource); source != "" {
		if source == rec.ID {
			return "", fmt.Errorf("file %q names itself as its node source", rec.ID)
		}
		nodeRecord, err := rt.getSubscription(source)
		if err != nil {
			return "", fmt.Errorf("file %q: %w", rec.ID, err)
		}
		if recordKind(nodeRecord) == kindFile {
			// A file sourcing a file would let two of them reference each
			// other and render forever, the same reason a collection cannot
			// contain a collection.
			return "", fmt.Errorf("file %q names another file as its node source", rec.ID)
		}
		nodes, err = rt.resolveNodesFor(nodeRecord)
		if err != nil {
			return "", fmt.Errorf("file %q: %w", rec.ID, err)
		}
	}

	merged, err := rt.subStoreEngine().mergeConfig(subStoreConfigMergeRequest{
		Template:  template,
		Raw:       nodes,
		Operators: operators,
	})
	if err != nil {
		return "", fmt.Errorf("file %q: %w", rec.ID, err)
	}
	if strings.TrimSpace(merged.Output) == "" {
		return "", fmt.Errorf("file %q produced no content", rec.ID)
	}
	return merged.Output, nil
}

// previewFileResponse renders a file and returns the document itself.
//
// A file's preview has to answer "what will a client receive". The node-list
// preview cannot: it would parse the template and report the example proxies a
// config ships with as though they were the result.
func previewFileResponse(rt *runtime, rec subscriptionRecord) latticeplugin.Response {
	document, err := rt.renderFile(rec, "")
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	truncated := false
	if len(document) > maxPreviewDocumentBytes {
		document = document[:maxPreviewDocumentBytes]
		truncated = true
	}
	body, err := json.Marshal(previewResult{Document: document, Truncated: truncated})
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// resolveNodesFor returns a record's nodes as text, whichever kind it is.
//
// A collection runs its members and its own chain; a subscription resolves its
// source and runs its chain. Either way the caller gets node text it can hand
// to the config merge.
func (rt *runtime) resolveNodesFor(rec subscriptionRecord) (string, error) {
	if recordKind(rec) == kindCollection {
		// URI is the interchange format: it round-trips through a second parse,
		// which is what handing the result to another stage requires.
		return rt.renderCollection(rec, "")
	}
	return rt.renderMemberNodes(rec)
}

// fileContentType picks what the core sends back. A config is YAML; plain text
// stays plain, because a file holding a rule list is not a document any client
// should be told to parse as something else.
func fileContentType(rec subscriptionRecord) string {
	if fileType(rec) == fileTypePlain {
		return "text/plain; charset=utf-8"
	}
	return "text/yaml; charset=utf-8"
}
