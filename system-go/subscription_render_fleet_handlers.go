package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// The render, preview and preview_draft handlers, and the render path they
// share. They were inline in handleSubscriptionCall until S2 (plan section
// 1.1): lane 1 rewrites them for fleet sources (the source dispatch, the
// serve-path rules, the deletion of the export fallback), and the dispatcher
// in subscription_render.go only routes to them, so each function here has
// one owner.

// renderSubscription produces the body the core will serve.
//
// It refuses to return empty content. The core refuses an empty body too, and
// both refusals exist deliberately: a proxy client that receives an empty but
// successful subscription deletes every node it had, so the failure is worth
// stopping twice rather than relying on either layer alone.
func (rt *runtime) renderSubscription(req subscriptionRenderRequest) (renderResult, error) {
	rec, err := rt.getSubscription(req.SubscriptionID)
	if err != nil {
		return renderResult{}, err
	}
	return rt.renderRecord(rec, req)
}

// renderRecord is renderSubscription for a record already read. A fleet sub
// has no document of its own: it renders to a selection plan through render
// (renderFleetSub), so every other caller is refused.
func (rt *runtime) renderRecord(rec subscriptionRecord, req subscriptionRenderRequest) (renderResult, error) {
	subscriptionID, format, uaClass, query := req.SubscriptionID, req.Format, req.UAClass, req.Query
	if recordKind(rec) == kindSub && rec.Source == subscriptionSourceFleet {
		return renderResult{}, fmt.Errorf("subscription %q is a fleet record: it renders to a selection plan that core binds per identity, and has no document of its own", subscriptionID)
	}

	// A file is served as the document it is: no base64 envelope, no client
	// target. The core's format only decides how a NODE LIST is carried, and a
	// configuration is not a node list.
	if recordKind(rec) == kindFile {
		// The core hands back whatever fetch stored: a snapshot envelope, or a
		// version 1 snapshot from a runtime before it. Both read as the text
		// the file paths always took.
		output, headers, err := rt.renderFile(rec, uaClass, query, snapshotText(req.Raw))
		if err != nil {
			return renderResult{}, err
		}
		contentType := fileContentType(rec)
		// A program says what it produced. Its own content-type wins over the
		// type guessed from the file kind, which is the whole reason it can set
		// headers at all.
		for key, value := range headers {
			if strings.EqualFold(key, "content-type") && strings.TrimSpace(value) != "" {
				contentType = value
			}
		}
		// A file marked for download is meant to arrive as a file rather than be
		// rendered in a browser tab. The record has carried this flag since files
		// existed and nothing ever read it; it takes effect once the core applies
		// the headers a render returns (TASK-0025), and until then it is stored
		// and reported rather than silently dropped.
		if rec.Download {
			if headers == nil {
				headers = map[string]string{}
			}
			if _, set := headers["content-disposition"]; !set {
				headers["content-disposition"] = `attachment; filename="` + downloadFilename(rec) + `"`
			}
		}
		return renderResult{Content: output, ContentType: contentType, Headers: headers}, nil
	}

	target, err := rt.renderTarget(rec, req)
	if err != nil {
		return renderResult{}, err
	}

	// A collection has no content of its own — it is defined entirely by the
	// subs it gathers, so the core's snapshot is not an input here.
	if recordKind(rec) == kindCollection {
		// The snapshot as fetch stored it: a version 2 envelope carries the
		// members' nodes, which snapshotText would drop.
		converted, err := rt.renderCollectionResult(rec, target, req.Options, req.Raw, req.Explain)
		if err != nil {
			return renderResult{}, err
		}
		return rt.finishNodeRender(rec, "collection "+quoteLabel(subscriptionID), target, format, req.Explain, converted)
	}

	// The snapshot as fetch stored it, decoded once: at 4096 nodes an envelope
	// is about 3 MB, and decoding it costs as much as parsing its nodes.
	env, enveloped := decodeSnapshotEnvelope(req.Raw)
	raw := req.Raw
	if enveloped {
		raw = envelopeText(env)
	}
	// The core hands back the snapshot it holds for this subscription. Inline
	// content is the fallback for a record that has no remote source at all.
	source := raw
	if strings.TrimSpace(source) == "" {
		source = rec.Content
	}
	// A vpn-core record carries no inline content. Render used to read the
	// export here when core held no snapshot, which handed line owner and
	// identity credentials to any operator who called render through the
	// gateway with no raw (S2 plan section 1.2). Core refreshes before it
	// renders, so the serve path always carries the snapshot, and the
	// fallback is gone for every source.
	if strings.TrimSpace(source) == "" && isVPNCoreSource(rec.Source) {
		return renderResult{}, ownerCredentialsWithheldError(rec.ID)
	}
	if strings.TrimSpace(source) == "" {
		// Saying so beats serving an empty subscription: a client that receives
		// an empty success deletes every node it had.
		return renderResult{}, fmt.Errorf("subscription %q has no content to render", subscriptionID)
	}

	plan, err := rt.chainPlan(rec)
	if err != nil {
		return renderResult{}, fmt.Errorf("subscription %q: %w", subscriptionID, err)
	}
	request := nodeConvertRequest{
		Parts:   []string{source},
		Target:  target,
		Plan:    plan,
		Options: req.Options,
		Explain: req.Explain,
	}
	// A snapshot envelope carries the nodes fetch parsed from the same text;
	// a native render reads them instead of parsing again.
	if enveloped && source == raw && nativeRoute(plan, target) {
		request.Nodes = envelopeNodes(env)
	}
	converted, _, err := rt.convertNodes(request)
	if err != nil {
		return renderResult{}, err
	}
	return rt.finishNodeRender(rec, "subscription "+quoteLabel(subscriptionID), target, format, req.Explain, converted)
}

// finishNodeRender turns one conversion into the reply for a node-list record
// (a subscription or a combination): refuse a document that carries no node
// for this client, carry it in the client's native encoding, then run the
// record's response chain.
//
// The refusal is the serve path's. A caller that asked to explain (the
// console) gets the document and the flag instead, because the console's job
// is to show why the link would be refused; the path that serves clients never
// sets explain.
func (rt *runtime) finishNodeRender(rec subscriptionRecord, label, target, format string, explain bool, converted subStoreConversionResult) (renderResult, error) {
	if converted.ZeroNodes && !explain {
		return renderResult{}, zeroNodesForTargetError(label, target)
	}
	if strings.TrimSpace(converted.Output) == "" && !explain {
		return renderResult{}, fmt.Errorf("%s converted to empty content", label)
	}
	body, contentType, err := encodeSubscriptionOutput(converted.Output, format, target)
	if err != nil {
		return renderResult{}, err
	}
	result := renderResult{
		ContentType: contentType,
		Target:      target,

		NodeCount:        explainedNodeCount(explain, converted.NodeCount),
		DroppedNodeCount: converted.UnsupportedNodeCount,
		DroppedProtocols: converted.UnsupportedProtocols,
	}
	if converted.ZeroNodes {
		// Only reachable when explaining. The response chain is not run over a
		// document that will never be served.
		result.Content, result.ZeroNodes = body, true
		return result, nil
	}
	result.Content, result.Headers, err = rt.applyResponseChain(rec, body, contentType)
	if err != nil {
		return renderResult{}, err
	}
	return result, nil
}

// previewCall serves preview and preview_draft.
//
// preview renders what is already saved. preview_draft additionally resolves
// an UNSAVED draft's source live, from fields the caller supplies.
//
// They are two operations, not one with a flag, because the difference is
// exactly a scope: preview reads a record an admin already configured, while
// preview_draft lets the caller name the host the control plane will go and
// talk to. That second thing is what `fetch` is declared substore:admin to
// do, and it cannot be gated inside a single method because this process
// never learns who is calling. Splitting them puts the bound in the manifest,
// which is the only place it is enforced.
func (rt *runtime) previewCall(method string, payload json.RawMessage) response {
	var req struct {
		SubscriptionID string            `json:"subscription_id"`
		Raw            string            `json:"raw"`
		Target         string            `json:"target"`
		Operators      []json.RawMessage `json:"operators"`
		GraphSelection json.RawMessage   `json:"graph_selection"`
		// Source fields let an UNSAVED draft say where its nodes come from;
		// without them a fleet- or provider-sourced draft previewed as
		// "no content" while the nodes were right there. preview_draft only.
		Source      string `json:"source,omitempty"`
		URL         string `json:"url,omitempty"`
		UA          string `json:"ua,omitempty"`
		VPNIdentity string `json:"vpn_identity,omitempty"`
	}
	if len(payload) > 0 {
		if len(payload) > model.MaxSubscriptionRequestBytes {
			return latticeplugin.ErrorResponse(errors.New("preview payload exceeds bounds"))
		}
		if err := decodeStrictVPNCoreGraphJSON(payload, &req); err != nil {
			return latticeplugin.ErrorResponse(fmt.Errorf("invalid preview payload: %w", err))
		}
	}
	// Refused rather than ignored. Silently dropping the fields would let a
	// caller believe they had previewed their draft's real source while they
	// were looking at nothing, and a preview that lies about where its nodes
	// came from is worse than one that says it cannot.
	if method == "preview" {
		for name, value := range map[string]string{
			"source": req.Source, "url": req.URL, "ua": req.UA, "vpn_identity": req.VPNIdentity,
		} {
			if strings.TrimSpace(value) != "" {
				return latticeplugin.ErrorResponse(fmt.Errorf(
					"preview does not resolve an unsaved draft: %q names a source the caller chose; use preview_draft, which is declared substore:admin", name))
			}
		}
	}
	// Set where the graph redactor runs: that path substitutes synthetic
	// credentials into the content itself, so the preview does not reduce
	// the node a second time and can let a script read the stand-in.
	credentialsAlreadySynthetic := false
	var graphSelection *vpnCoreGraphSelection
	if len(req.GraphSelection) > 0 {
		if bytes.Equal(bytes.TrimSpace(req.GraphSelection), []byte("null")) {
			return latticeplugin.ErrorResponse(errors.New("graph_selection must be an object"))
		}
		var selection vpnCoreGraphSelection
		if err := decodeStrictVPNCoreGraphJSON(req.GraphSelection, &selection); err != nil {
			return latticeplugin.ErrorResponse(errors.New("invalid graph_selection payload"))
		}
		graphSelection = &selection
	}
	raw := req.Raw
	operators := req.Operators
	target := req.Target
	previewSourceVersion := ""
	var selectedRecord *subscriptionRecord
	if graphSelection != nil {
		if strings.TrimSpace(req.Raw) != "" {
			return latticeplugin.ErrorResponse(errors.New("graph preview does not accept caller raw content"))
		}
		if strings.TrimSpace(req.SubscriptionID) != "" {
			rec, err := rt.getSubscription(req.SubscriptionID)
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			if rec.Source != subscriptionSourceVPNCoreGraph {
				return latticeplugin.ErrorResponse(errors.New("graph selection is only valid for vpn-core-graph records"))
			}
			selectedRecord = &rec
			if operators == nil {
				storedOperators, err := storedPreviewOperators(rec)
				if err != nil {
					return latticeplugin.ErrorResponse(err)
				}
				operators = storedOperators
			}
			if strings.TrimSpace(target) == "" {
				target = rec.Target
			}
		}
		composed, err := rt.previewVPNCoreGraph(*graphSelection)
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		raw, err = redactVPNCoreGraphPreviewEntries(composed.Entries, graphSelection.EntryRoots)
		credentialsAlreadySynthetic = true
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		previewSourceVersion = composed.SourceVersion
	}
	if strings.TrimSpace(req.SubscriptionID) != "" {
		var rec subscriptionRecord
		if selectedRecord != nil {
			rec = *selectedRecord
		} else {
			var err error
			rec, err = rt.getSubscription(req.SubscriptionID)
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
		}
		// A file's content is a document, not a node list. Parsing it as
		// nodes would show the example proxies its template ships with and
		// call them the result, which is worse than showing nothing.
		if recordKind(rec) == kindFile {
			return previewFileResponse(rt, rec)
		}
		// A combination has no content of its own either: its nodes are its
		// members', merged with each member's chain and then its own already
		// run. Previewing that merged list is what the row's eye means on a
		// combination — asking previewSubscription to fetch it again would
		// report "no content" for a combination that serves fifty nodes.
		if recordKind(rec) == kindCollection {
			// The merged list is parsed back below, so it renders in URI —
			// the one target that round-trips through a parse — whatever the
			// record's serving target is.
			previewRec := rec
			previewRec.Target = ""
			merged, err := rt.renderCollection(previewRec, "URI", nil, "")
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			out, err := rt.previewSubscription(merged, nil, "URI", false)
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			body, err := json.Marshal(out)
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			return latticeplugin.RawResultResponse(body, "")
		}
		if operators == nil {
			storedOperators, err := storedPreviewOperators(rec)
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			operators = storedOperators
		}
		if strings.TrimSpace(target) == "" {
			target = rec.Target
		}
		if rec.Source == subscriptionSourceVPNCore {
			if err := validateOperators(operators); err != nil {
				return latticeplugin.ErrorResponse(errors.New("legacy vpn-core preview operators are invalid"))
			}
			if containsScriptingOperator(operators) {
				return latticeplugin.ErrorResponse(errors.New("legacy vpn-core preview does not allow scripting operators"))
			}
		}
		if strings.TrimSpace(raw) == "" {
			raw = rec.Content
			// Same reason as render: a vpn-core or provider record has no
			// inline content, and a preview that showed nothing would look
			// like a broken subscription rather than one sourced from
			// somewhere else. The fetch here is a read for the preview only —
			// it is not recorded as a refresh, because the served snapshot
			// does not move.
			if strings.TrimSpace(raw) == "" &&
				(rec.Source == subscriptionSourceVPNCore || strings.TrimSpace(rec.URL) != "") {
				fetched, err := rt.fetchRecordContent(rec)
				if err != nil {
					return latticeplugin.ErrorResponse(err)
				}
				raw = fetched.Raw
			}
		}
		// A stored graph source is authoritative. Caller-supplied preview bytes
		// must never replace the exact composition bound to its identity and
		// ordered roots.
		if rec.Source == subscriptionSourceVPNCoreGraph && graphSelection == nil {
			composed, err := rt.fetchVPNCoreGraph(rec)
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			raw, err = redactVPNCoreGraphPreviewEntries(composed.Entries, rec.EntryRoots)
			credentialsAlreadySynthetic = true
			if err != nil {
				return latticeplugin.ErrorResponse(err)
			}
			previewSourceVersion = composed.SourceVersion
		}
	} else if strings.TrimSpace(raw) == "" && req.Source != "" && req.Source != subscriptionSourceLocal {
		// An unsaved draft carries its source but no content. Resolve the
		// source live — the same guarded path a saved record's refresh
		// takes — so the preview shows the nodes the draft would produce.
		// A preview fetch is a read, not a refresh: nothing is persisted.
		fetched, err := rt.fetchRecordContent(subscriptionRecord{
			Source:      req.Source,
			URL:         req.URL,
			UA:          req.UA,
			VPNIdentity: req.VPNIdentity,
		})
		if err != nil {
			return latticeplugin.ErrorResponse(err)
		}
		raw = fetched.Raw
	}
	out, err := rt.previewSubscription(raw, operators, target, credentialsAlreadySynthetic)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	out.SourceVersion = previewSourceVersion
	out.Stale = false
	body, err := json.Marshal(out)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// renderCall serves render: the body the core serves for a share.
func (rt *runtime) renderCall(payload json.RawMessage) response {
	var req struct {
		SubscriptionID string `json:"subscription_id"`
		Format         string `json:"format"`
		UAClass        string `json:"ua_class"`
		// UATarget is what the core resolved from the agent; it travels
		// only when the URL named no target (subscriptionRenderRequest).
		UATarget string `json:"ua_target"`
		// Target names the client explicitly — the parity contract with
		// Sub-Store, whose subscription URLs carry ?target=. An explicit
		// target outranks both the record's pin and the UA class: the
		// caller who spells out a client means that client.
		Target string `json:"target"`
		// Options are produce() flags under Sub-Store's own names
		// (include-unsupported-proxy, ...). Booleans only.
		Options map[string]bool `json:"options"`
		Raw     string          `json:"raw"`
		// Only the parameters the core decided to forward reach here, and the
		// record narrows them again to the names it declared.
		Query map[string]string `json:"query"`
		// Explain asks the render to also report what the chosen client
		// dropped. The console sets it; the path that serves a client does
		// not, so serving pays nothing for a diagnosis nobody reads.
		Explain bool `json:"explain"`
		// Revision names the revision to render (S2 plan section 2.7):
		// empty or the live one serves, the staged one previews. Core's
		// previews and plans name it; an older core sends none.
		Revision string `json:"revision"`
		// MemberRevisions names, for a collection, the revision each member
		// renders at; "" names a deleted member.
		MemberRevisions map[string]string `json:"member_revisions"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return latticeplugin.ErrorResponse(fmt.Errorf("invalid render payload: %w", err))
		}
	}
	out, err := rt.render(subscriptionRenderRequest{
		SubscriptionID: req.SubscriptionID,
		Format:         req.Format,
		UAClass:        req.UAClass,
		UATarget:       req.UATarget,
		Target:         req.Target,
		Options:        req.Options,
		Raw:            req.Raw,
		Query:          req.Query,
		Explain:        req.Explain,
	}, fleetRenderOptions{Revision: req.Revision, MemberRevisions: req.MemberRevisions})
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	body, err := json.Marshal(out)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// renderReply is render's reply: the S1 result, the live revision of the
// record (which core checks a plan's from_revision against), and, for a
// fleet-bound record, the selection plan in place of a document.
type renderReply struct {
	renderResult
	Plan         json.RawMessage `json:"plan,omitempty"`
	LiveRevision string          `json:"live_revision,omitempty"`
}

// render is the render method: a fleet sub renders to a plan
// (renderFleetSub), every other record to its document as before. A named
// revision other than the live one is revision_unknown until the staged
// store lands (lane 4); the live one renders as if none were named.
func (rt *runtime) render(req subscriptionRenderRequest, opts fleetRenderOptions) (renderReply, error) {
	rec, err := rt.getSubscription(req.SubscriptionID)
	if err != nil {
		return renderReply{}, err
	}
	fleetSub := recordKind(rec) == kindSub && rec.Source == subscriptionSourceFleet
	fleetCollection, owner := false, false
	if recordKind(rec) == kindCollection {
		// A fleet-bound collection never takes a path that reads member
		// text as provider content: its plan is built node-wise from its
		// blocks, and a legacy-shaped snapshot is refused.
		if fleetCollection, owner, err = rt.collectionFleetState(rec); err != nil {
			return renderReply{}, err
		}
	}
	if fleetSub || fleetCollection {
		target, err := rt.renderTarget(rec, req)
		if err != nil {
			return renderReply{}, err
		}
		var out fleetRendered
		if fleetSub {
			out, err = rt.renderFleetSub(rec, req, opts, target)
		} else {
			out, err = rt.renderFleetCollection(rec, req, opts, target, owner)
		}
		if err != nil {
			return renderReply{}, err
		}
		result := renderResult{ContentType: targetContentType(target), Target: target, NodeCount: explainedNodeCount(req.Explain, out.NodeCount)}
		if req.Explain {
			result.DroppedNodeCount, result.DroppedProtocols = fleetDroppedSummary(out.Dropped)
		}
		return renderReply{renderResult: result, Plan: out.Plan, LiveRevision: out.LiveRevision}, nil
	}
	if opts.staged(rec.Revision) {
		return renderReply{}, fmt.Errorf("%s: subscription %q has no revision %q; its live revision is %q", codeRevisionUnknown, rec.ID, opts.Revision, rec.Revision)
	}
	result, err := rt.renderRecord(rec, req)
	if err != nil {
		return renderReply{}, err
	}
	return renderReply{renderResult: result, LiveRevision: rec.Revision}, nil
}

// fleetDroppedSummary counts the rows that had no node and names their
// protocols, sorted, "unknown" for a row with none, as an explaining render
// reports the nodes a client dropped.
func fleetDroppedSummary(dropped []fleetDrop) (int, []string) {
	if len(dropped) == 0 {
		return 0, nil
	}
	seen := map[string]bool{}
	var protocols []string
	for _, drop := range dropped {
		protocol := drop.Protocol
		if protocol == "" {
			protocol = "unknown"
		}
		if !seen[protocol] {
			seen[protocol] = true
			protocols = append(protocols, protocol)
		}
	}
	sort.Strings(protocols)
	return len(dropped), protocols
}
