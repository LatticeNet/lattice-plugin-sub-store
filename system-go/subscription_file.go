package main

import (
	"encoding/json"
	"errors"
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
	switch rec.FileType {
	case fileTypePlain:
		return fileTypePlain
	case fileTypeScript:
		return fileTypeScript
	default:
		return fileTypeConfig
	}
}

// resolveFileTemplate returns the document before nodes are injected.
func (rt *runtime) resolveFileTemplate(rec subscriptionRecord) (string, error) {
	switch rec.Source {
	case subscriptionSourceRemote:
		if strings.TrimSpace(rec.URL) == "" {
			return "", fmt.Errorf("file %q has no template URL", rec.ID)
		}
		// The record is in hand; fetching by id would re-read the whole records
		// document to find it again, one wasted host call per render.
		fetched, err := rt.fetchRecordContent(rec)
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

// renderFile produces the document the core will serve. A non-empty
// snapshotRaw is the refresh path's resolved node content: the render spends
// nothing reaching it again. Empty renders live, which is how previews work.
func (rt *runtime) renderFile(rec subscriptionRecord, uaClass string, query map[string]string, snapshotRaw string) (string, map[string]string, error) {
	// A script file has no template to resolve: the program is the document, and
	// it decides for itself what the nodes turn into.
	if isScriptFile(rec) {
		return rt.renderScriptFile(rec, query, snapshotRaw)
	}

	template, err := rt.resolveFileTemplate(rec)
	if err != nil {
		return "", nil, err
	}

	operators, err := enabledOperators(rec)
	if err != nil {
		return "", nil, fileError(rec.ID, err)
	}

	// Plain text has no node list to fill: its operations run over the document
	// through the response-transform path, and what comes out is served.
	if fileType(rec) == fileTypePlain {
		if len(operators) == 0 {
			return template, nil, nil
		}
		out, err := rt.subStoreEngine().transformResponse(subStoreResponseTransformRequest{
			Response:  mustJSON(map[string]any{"status": 200, "headers": map[string]any{}, "body": template}),
			Operators: operators,
		})
		if err != nil {
			return "", nil, fileError(rec.ID, err)
		}
		if strings.TrimSpace(out.Body) == "" {
			return "", nil, fmt.Errorf("file %q produced no content", rec.ID)
		}
		return out.Body, nil, nil
	}

	// A config with no node source is a document the operator maintains
	// entirely by hand — rules, a script, a fragment. Serving it unchanged is
	// the correct answer, not an error.
	nodes := strings.TrimSpace(snapshotRaw)
	if source := strings.TrimSpace(rec.NodeSource); source != "" && nodes == "" {
		if source == rec.ID {
			return "", nil, fmt.Errorf("file %q names itself as its node source", rec.ID)
		}
		nodeRecord, err := rt.getSubscription(source)
		if err != nil {
			return "", nil, fileError(rec.ID, err)
		}
		if recordKind(nodeRecord) == kindFile {
			// A file sourcing a file would let two of them reference each
			// other and render forever, the same reason a collection cannot
			// contain a collection.
			return "", nil, fmt.Errorf("file %q names another file as its node source", rec.ID)
		}
		nodes, err = rt.resolveNodesFor(nodeRecord)
		if err != nil {
			return "", nil, fileError(rec.ID, err)
		}
	}

	merged, err := rt.subStoreEngine().mergeConfig(subStoreConfigMergeRequest{
		Template:  template,
		Raw:       nodes,
		Operators: operators,
	})
	if err != nil {
		return "", nil, fileError(rec.ID, err)
	}
	if strings.TrimSpace(merged.Output) == "" {
		return "", nil, fmt.Errorf("file %q produced no content", rec.ID)
	}
	return merged.Output, nil, nil
}

// resolveScriptArtifacts gathers what `produceArtifact` can hand back.
//
// The record's node source is resolved to one artifact whose members are
// rendered individually, because a proxy has to keep the name of the
// subscription it came from: scripts filter on `_subName`, and a merged blob
// cannot say which member produced which node.
//
// The artifact is registered under both the source's id and its name, since a
// ported script names it the way upstream did while the record refers to it by
// id, and making the operator reconcile the two by hand is a trap.
func (rt *runtime) resolveScriptArtifacts(rec subscriptionRecord) ([]fileScriptArtifact, error) {
	source := strings.TrimSpace(rec.NodeSource)
	if source == "" {
		return nil, nil
	}
	if source == rec.ID {
		return nil, fmt.Errorf("file %q names itself as its node source", rec.ID)
	}
	sourceRecord, err := rt.getSubscription(source)
	if err != nil {
		return nil, fileError(rec.ID, err)
	}
	if recordKind(sourceRecord) == kindFile {
		return nil, fmt.Errorf("file %q names another file as its node source", rec.ID)
	}

	var members []fileScriptMember
	kind := recordKind(sourceRecord)
	if kind == kindCollection {
		gathered, err := rt.collectionMembers(sourceRecord)
		if err != nil {
			return nil, fileError(rec.ID, err)
		}
		for _, member := range gathered {
			raw, err := rt.renderMemberNodes(member)
			if err != nil {
				// A collection's own failure mode decides this: strict refuses so
				// a client never silently loses nodes, skip-failed keeps serving.
				if !collectionMemberFailureIsSkippable(sourceRecord, member) {
					return nil, fileError(rec.ID, err)
				}
				continue
			}
			members = append(members, fileScriptMember{SubName: memberSubName(member), Raw: raw})
		}
		if len(members) == 0 {
			return nil, fmt.Errorf("file %q: collection %q produced no members", rec.ID, sourceRecord.ID)
		}
	} else {
		raw, err := rt.renderMemberNodes(sourceRecord)
		if err != nil {
			return nil, fileError(rec.ID, err)
		}
		members = []fileScriptMember{{SubName: memberSubName(sourceRecord), Raw: raw}}
	}

	artifact := fileScriptArtifact{Name: sourceRecord.ID, Kind: kind, Members: members}
	artifacts := []fileScriptArtifact{artifact}
	if name := strings.TrimSpace(sourceRecord.Name); name != "" && name != sourceRecord.ID {
		alias := artifact
		alias.Name = name
		artifacts = append(artifacts, alias)
	}
	return artifacts, nil
}

// scriptArtifactsFor returns the artifacts a script runs against: the
// snapshot's when the refresh path resolved them, resolved live otherwise. The
// snapshot envelope carries the source's identity so the alias registration a
// ported script relies on — artifact named both the id and the display name —
// is identical either way.
func (rt *runtime) scriptArtifactsFor(rec subscriptionRecord, snapshotRaw string) ([]fileScriptArtifact, error) {
	if strings.TrimSpace(snapshotRaw) == "" {
		return rt.resolveScriptArtifacts(rec)
	}
	var snap snapshotArtifacts
	if err := json.Unmarshal([]byte(snapshotRaw), &snap); err != nil || len(snap.Members) == 0 || snap.SourceID == "" {
		// Core hands back only snapshots this plugin wrote, so one that does
		// not read is an error, never a reason to resolve the node source
		// live (S2 plan section 1.2): the live path would read a legacy
		// source's export for whoever called render with a broken raw.
		return nil, fmt.Errorf("%s: file %q has a snapshot this plugin cannot read; the next refresh replaces it", codeSnapshotMalformed, rec.ID)
	}
	kind := snap.SourceKind
	if kind == "" {
		kind = kindSub
	}
	artifact := fileScriptArtifact{Name: snap.SourceID, Kind: kind, Members: snap.Members}
	artifacts := []fileScriptArtifact{artifact}
	if name := strings.TrimSpace(snap.SourceName); name != "" && name != snap.SourceID {
		alias := artifact
		alias.Name = name
		artifacts = append(artifacts, alias)
	}
	return artifacts, nil
}

// memberSubName is the name a script sees on `proxy._subName`. Upstream tags
// with the subscription's name, so a ported script's lookup tables match.
func memberSubName(member subscriptionRecord) string {
	if name := strings.TrimSpace(member.Name); name != "" {
		return name
	}
	return member.ID
}

// renderScriptFile runs the file's program and returns what it produced.
func (rt *runtime) renderScriptFile(rec subscriptionRecord, query map[string]string, snapshotRaw string) (string, map[string]string, error) {
	artifacts, err := rt.scriptArtifactsFor(rec, snapshotRaw)
	if err != nil {
		return "", nil, err
	}
	out, err := rt.subStoreEngine().runFileScript(fileScriptRequest{
		Script:    rec.Content,
		Artifacts: artifacts,
		Arguments: rec.Arguments,
		Query:     filterQuery(rec, query),
	})
	if err != nil {
		return "", nil, fileError(rec.ID, err)
	}
	if strings.TrimSpace(out.Content) == "" {
		return "", nil, fmt.Errorf("file %q produced no content", rec.ID)
	}
	return out.Content, out.Headers, nil
}

// previewFileResponse renders a file and returns the document itself.
//
// A file's preview has to answer "what will a client receive". The node-list
// preview cannot: it would parse the template and report the example proxies a
// config ships with as though they were the result.
func previewFileResponse(rt *runtime, rec subscriptionRecord) latticeplugin.Response {
	// Preview is a read-only view of an operator-owned document.  It must never
	// turn into a provider fetch or execute a stored program/transform: those
	// paths are host-capable and belong to render/publish, not preview.
	if strings.TrimSpace(rec.NodeSource) != "" {
		return latticeplugin.ErrorResponse(fmt.Errorf("file preview does not expose node-source content"))
	}
	if rec.Source == subscriptionSourceRemote || strings.TrimSpace(rec.URL) != "" {
		return latticeplugin.ErrorResponse(fmt.Errorf("file preview requires a self-contained local document"))
	}
	if isScriptFile(rec) || len(processSteps(rec)) != 0 {
		return latticeplugin.ErrorResponse(fmt.Errorf("file preview requires a self-contained local document"))
	}
	// A preview has no request behind it, so a script sees an empty query and
	// falls back to whatever defaults it declares.
	document, _, err := rt.renderFile(rec, "", nil, "")
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
		return rt.renderCollection(rec, subscriptionTarget(rec, ""), nil, "")
	}
	return rt.renderMemberNodes(rec)
}

// fileContentType picks what the core sends back. A config is YAML; plain text
// stays plain, because a file holding a rule list is not a document any client
// should be told to parse as something else.
func fileContentType(rec subscriptionRecord) string {
	switch fileType(rec) {
	case fileTypePlain:
		return "text/plain; charset=utf-8"
	case fileTypeScript:
		// A program can emit YAML, JSON or a rule list, and it says which through
		// `$options._res.headers`. Guessing YAML for all of them would label a
		// JSON document as something no client should parse it as, so the default
		// claims nothing and the script's own header replaces it.
		return "text/plain; charset=utf-8"
	default:
		return "text/yaml; charset=utf-8"
	}
}

// downloadFilename is what a browser saves a file as.
//
// The display name is preferred because that is the name the operator gave it
// and the one they will look for on disk; the id is the fallback. Characters a
// filesystem or a header cannot carry are replaced rather than escaped, since a
// quoted filename containing a quote is a header-injection shape.
func downloadFilename(rec subscriptionRecord) string {
	name := strings.TrimSpace(rec.DisplayName)
	if name == "" {
		name = strings.TrimSpace(rec.Name)
	}
	if name == "" {
		name = rec.ID
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20, r == '"', r == '\\', r == '/', r == 0x7f:
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	cleaned := strings.TrimSpace(b.String())
	if cleaned == "" {
		return "subscription"
	}
	if fileType(rec) == fileTypeConfig && !strings.Contains(cleaned, ".") {
		return cleaned + ".yaml"
	}
	return cleaned
}

// fleetNodesForFile is the refusal every file path gives a fleet record met
// on its way to nodes (S2 plan section 1.2): a file render carries no plan,
// so a fleet record's nodes would reach the file's clients with placeholder
// credentials, served unbound. Fleet-bound files do not exist before S3: the
// write path refuses to create one, and each live path below refuses at the
// point it meets the fleet record, at no extra host call.
//
// yagni: a file rendered from its snapshot is not checked here, because the
// fetch that wrote the snapshot refused and the write path keeps a file's
// node source from becoming fleet-bound; when lane 4's index entry read
// lands in render, its node source's live fleet_bound flag is checked there
// at no extra host call.
func fleetNodesForFile(ids []string, collection string) error {
	message := fmt.Sprintf("the fleet records %s reach a file only from S3", strings.Join(ids, ", "))
	refusal := &fleetRuleError{Code: codeFleetFileUnavailable, IDs: ids, Message: message}
	if collection != "" {
		refusal.Collections = []string{collection}
		refusal.Message = fmt.Sprintf("collection %q gathers the fleet records %s, which reach a file only from S3", collection, strings.Join(ids, ", "))
	}
	return refusal
}

// fileError wraps an error a file's node work returned. A fleet refusal is
// returned unwrapped, its code leading and the file named in it, so core
// and the UI read fleet_file_unavailable; anything else is prefixed with the
// file as before.
func fileError(fileID string, err error) error {
	var refusal *fleetRuleError
	if errors.As(err, &refusal) && refusal.Code == codeFleetFileUnavailable {
		named := *refusal
		named.Files = []string{fileID}
		named.Message = fmt.Sprintf("file %q: %s", fileID, refusal.Message)
		return &named
	}
	return fmt.Errorf("file %q: %w", fileID, err)
}
