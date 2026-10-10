package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/parse"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// maxProviderResponseBytes bounds what one provider fetch may return. A provider
// that streams without end would otherwise be able to exhaust this process and
// then the snapshot the core stores.
const maxProviderResponseBytes = 8 << 20

// defaultProviderUA is sent when a subscription names no user agent. Providers
// commonly vary their output by client, so an absent UA is a real choice rather
// than a missing value - this one asks for the plain URI list.
const defaultProviderUA = "Lattice/1.0"

type fetchResult struct {
	Raw            string          `json:"raw"`
	Userinfo       string          `json:"userinfo,omitempty"`
	SourceVersion  string          `json:"source_version,omitempty"`
	SourceManifest json.RawMessage `json:"source_manifest,omitempty"`
	// nodesIn is the source node count the refresh read, when it counted one.
	nodesIn *int
	// nodesOut is the count after the record's chain, when the chain is
	// native and could run at refresh.
	nodesOut *int
}

func isVPNCoreSource(source string) bool {
	return source == subscriptionSourceVPNCore || source == subscriptionSourceVPNCoreGraph
}

// fetchSubscription retrieves the record's current content as the snapshot
// envelope the core stores (snapshot_envelope.go).
//
// Every record kind resolves to its variable content here, because the core
// stores the answer as the snapshot a share renders from: without it a share
// of a collection or a file failed at the snapshot step before render ever
// ran. What each kind stores:
//
//   - sub: the provider body, the vpn-core export, or the pasted content.
//   - collection: every member's nodes after its own chain, with the name a
//     later stage filters on. Merging members into one blob here would lose
//     that name and the per-member chains at render would have nothing to
//     apply to.
//   - config file: its node source resolved the same way (chained, merged).
//   - script file: its node source's members, whole: scripts read per-member
//     provenance.
//   - plain file / anything without a node source: the stored template, which
//     is constant until edited (edits invalidate the render cache directly).
func (rt *runtime) fetchSubscription(subscriptionID string) (fetchResult, error) {
	rec, err := rt.getSubscription(subscriptionID)
	if err != nil {
		return fetchResult{}, err
	}
	var out fetchResult
	var env snapshotEnvelope
	switch recordKind(rec) {
	case kindCollection:
		members, membersNative, err := rt.fetchCollectionSnapshot(rec)
		if err != nil {
			return fetchResult{}, err
		}
		env = membersEnvelope(kindCollection, members)
		if err := withMemberNodes(&env, members, membersNative); err != nil {
			return fetchResult{}, err
		}
	case kindFile:
		if env, err = rt.fetchFileSnapshot(rec); err != nil {
			return fetchResult{}, err
		}
	default:
		if rec.Source == subscriptionSourceFleet {
			// A fleet envelope with a catalogue version is content whatever
			// its row count: a selection that matches nothing is valid, and
			// core answers its shares with the decoy (S2 plan section 1.2).
			return rt.fetchFleetSub(rec)
		}
		if out, err = rt.fetchRecordContent(rec); err != nil {
			return fetchResult{}, err
		}
		plan, err := rt.chainPlan(rec)
		if err != nil {
			return fetchResult{}, fmt.Errorf("subscription %q: %w", rec.ID, err)
		}
		env = textEnvelope(kindSub, out.Raw, out.SourceVersion)
		label := fmt.Sprintf("subscription %q", rec.ID)
		if !planIsNative(plan) {
			// The bundle renders this record whatever the target, so the
			// bundle counts it, on the warm runtime: a refresh never pays an
			// isolated boot (plan section 7, decision 4).
			count, err := rt.requireNodes(label, out.Raw)
			if err != nil {
				return fetchResult{}, err
			}
			out.nodesIn = &count
			env.NodesOmitted = nodesOmittedFallback
			break
		}
		if len(out.Raw) > parse.MaxDocumentBytes {
			// Past the raw bound the core keeps, which is also the parser's:
			// the envelope refuses it with its stated reason, before a parse
			// would refuse it with a less useful one.
			_, err := encodeSnapshotEnvelope(env)
			return fetchResult{}, err
		}
		nodes, err := requireNativeNodes(label, out.Raw)
		if err != nil {
			return fetchResult{}, err
		}
		count := len(nodes)
		out.nodesIn = &count
		if env.Nodes, err = encodeNodes(nodes); err != nil {
			return fetchResult{}, err
		}
		// The envelope holds the nodes before the chain; the count after it
		// is the row's nodes out. The chain runs over the nodes in place,
		// after they were encoded.
		after := len(runChain(plan, nodes, out.Raw))
		out.nodesOut = &after
	}
	if env.Raw == "" && len(env.Members) == 0 {
		// An empty fetch is a failure, not a subscription with no nodes: the
		// core would otherwise replace a good snapshot with an envelope of
		// nothing.
		return fetchResult{}, fmt.Errorf("subscription %q fetched no content", rec.ID)
	}
	if out.Raw, err = encodeSnapshotEnvelope(env); err != nil {
		return fetchResult{}, err
	}
	return out, nil
}

// runChain runs a native plan over nodes with the context a render gives it,
// before any target is known.
func runChain(plan *operators.Plan, nodes []*nodemodel.Node, raw string) []*nodemodel.Node {
	if plan == nil {
		return nodes
	}
	return plan.Run(nodes, &operators.Context{Raw: raw})
}

// withMemberNodes puts each member's nodes in a collection envelope when
// every chain of the collection ran in Go, and says why they are absent
// otherwise.
func withMemberNodes(env *snapshotEnvelope, members []fileScriptMember, membersNative bool) error {
	if !membersNative {
		env.NodesOmitted = nodesOmittedFallback
		return nil
	}
	for i, member := range members {
		nodes, err := encodeNodes(member.nodes)
		if err != nil {
			return err
		}
		env.Members[i].Nodes = nodes
		env.Members[i].parsedRaw = member.unchained
	}
	return nil
}

// requireNativeNodes parses node text in Go and refuses it when it holds no
// node, as requireNodes does with the bundle.
func requireNativeNodes(label, raw string) ([]*nodemodel.Node, error) {
	nodes, err := parseParts([]string{raw})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	if len(nodes) == 0 {
		return nil, providerNoNodesError(label)
	}
	return nodes, nil
}

// requireNodes refuses node text the engine finds no node in, and returns the
// count it found.
//
// The refresh path used to accept any non-empty 2xx body, so a provider's
// "429 Too Many Requests" page replaced the last good snapshot and then
// rendered to "proxies:\n" for every Clash-family client. Failing the fetch
// instead keeps the core on the snapshot it already has (served stale), and the
// record's bookkeeping says why.
//
// It applies to every plain subscription source, not only provider URLs. Pasted
// content with no node in it and an empty provider account are refused the same
// way, and on a first refresh, with no snapshot to fall back on, the record
// shows the error instead of serving an empty list. A vpn-core graph source
// passes through here too; its composition is a non-empty list of canonical
// VLESS Reality URIs (validateVPNCoreGraphResponse), which the engine reads as
// one node each (TestVPNCoreGraphCanonicalRawIsOneNodePerEntry).
func (rt *runtime) requireNodes(label, raw string) (int, error) {
	count, err := rt.subStoreEngine().countNodes(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", label, err)
	}
	if count == 0 {
		return 0, providerNoNodesError(label)
	}
	return count, nil
}

// snapshotArtifacts is the members object of a collection or a script-sourced
// file as the render paths decode it. A version 1 snapshot is this object; a
// version 2 envelope with members is turned back into it by snapshotText.
type snapshotArtifacts struct {
	SourceID   string             `json:"source_id,omitempty"`
	SourceName string             `json:"source_name,omitempty"`
	SourceKind string             `json:"source_kind,omitempty"`
	Members    []fileScriptMember `json:"members"`
}

// fetchCollectionSnapshot resolves a collection's members to their chained
// node text. Member content moves at provider cadence; resolving it at refresh
// time is what lets a render skip the network entirely.
func (rt *runtime) fetchCollectionSnapshot(rec subscriptionRecord) ([]fileScriptMember, bool, error) {
	members, err := rt.collectionMembers(rec)
	if err != nil {
		return nil, false, err
	}
	return rt.chainMembers(rec, members)
}

// chainMembers renders each member through its own chain, honoring the
// collection's failure mode. Shared by the collection snapshot and the live
// render paths so the two can never drift apart.
//
// membersNative reports that the collection's own chain and every member's
// chain run in Go. Each member then carries its nodes, and the members that
// arrived unchained are counted by parsing them in Go; otherwise they are
// counted on the bundle's warm runtime in one call, as before, and no member
// carries nodes. A chained member runs on whichever engine its own chain
// allows: the snapshot is taken before any target is known, and its member
// texts have to be what a live render of the same collection computes.
func (rt *runtime) chainMembers(rec subscriptionRecord, members []subscriptionRecord) ([]fileScriptMember, bool, error) {
	type resolvedMember struct {
		member  subscriptionRecord
		out     memberOutput
		dropped bool
	}
	plan, err := rt.chainPlan(rec)
	if err != nil {
		return nil, false, fmt.Errorf("collection %q: %w", rec.ID, err)
	}
	membersNative := planIsNative(plan)
	resolved := make([]resolvedMember, 0, len(members))
	skipped := make([]string, 0)
	// fail applies the collection's failure mode to one member.
	//
	// Strict is the default because serving only the survivors reaches a
	// client as "those nodes were removed", and the client acts on that by
	// deleting them. Skipping is available because one dead provider should
	// not take down a large collection, but it is a choice the operator
	// makes, not one made for them. Graph members are never skippable: their
	// composition is authoritative, and silently serving a graph collection
	// without them would misrepresent it.
	fail := func(member subscriptionRecord, err error) error {
		if !collectionMemberFailureIsSkippable(rec, member) {
			return fmt.Errorf("collection %q: %w", rec.ID, err)
		}
		skipped = append(skipped, member.ID)
		return nil
	}
	for _, member := range members {
		out, err := rt.memberNodes(member)
		if err != nil {
			if err := fail(member, err); err != nil {
				return nil, false, err
			}
			continue
		}
		membersNative = membersNative && out.native
		resolved = append(resolved, resolvedMember{member: member, out: out})
	}
	// A member whose source parses to no nodes (a provider's error page) is a
	// failed member, not an empty one. When the collection runs in Go each
	// unchained member is parsed in Go, which is also where its nodes come
	// from; otherwise every unchained member is counted in one engine call
	// rather than one call per member.
	var texts []string
	var at []int
	for i, entry := range resolved {
		if entry.out.needsCount {
			texts, at = append(texts, entry.out.raw), append(at, i)
		}
	}
	if len(texts) > 0 && membersNative {
		for _, i := range at {
			nodes, err := requireNativeNodes(memberLabel(resolved[i].member), resolved[i].out.raw)
			if err != nil {
				if err := fail(resolved[i].member, err); err != nil {
					return nil, false, err
				}
				resolved[i].dropped = true
				continue
			}
			resolved[i].out.nodes = nodes
		}
	} else if len(texts) > 0 {
		counts, err := rt.subStoreEngine().countNodesEach(texts)
		if err != nil {
			return nil, false, fmt.Errorf("collection %q: %w", rec.ID, err)
		}
		for j, i := range at {
			if counts[j] > 0 {
				continue
			}
			if err := fail(resolved[i].member, providerNoNodesError(memberLabel(resolved[i].member))); err != nil {
				return nil, false, err
			}
			resolved[i].dropped = true
		}
	}
	out := make([]fileScriptMember, 0, len(resolved))
	for _, entry := range resolved {
		if !entry.dropped && strings.TrimSpace(entry.out.raw) != "" {
			member := fileScriptMember{SubName: memberSubName(entry.member), Raw: entry.out.raw}
			if membersNative {
				member.nodes = entry.out.nodes
				member.unchained = entry.out.needsCount
			}
			out = append(out, member)
		}
	}
	// Every member failing is not "skip the failures" — it is a collection with
	// nothing in it, and that must never be served as a success.
	if len(out) == 0 && len(members) > 0 {
		return nil, false, fmt.Errorf("collection %q: every member failed (%s)", rec.ID, strings.Join(skipped, ", "))
	}
	if len(out) == 0 {
		return nil, false, fmt.Errorf("collection %q produced no nodes", rec.ID)
	}
	return out, membersNative, nil
}

// fetchFileSnapshot resolves what a file's render varies by: its node source,
// or nothing at all (the template is the answer until someone edits it).
func (rt *runtime) fetchFileSnapshot(rec subscriptionRecord) (snapshotEnvelope, error) {
	source := strings.TrimSpace(rec.NodeSource)
	if source == "" {
		// A file without a node source is static until edited; the template is
		// the honest content, and an edit changes its hash.
		return textEnvelope(kindFile, rec.Content, ""), nil
	}
	sourceRecord, err := rt.getSubscription(source)
	if err != nil {
		return snapshotEnvelope{}, fmt.Errorf("file %q: %w", rec.ID, err)
	}
	if recordKind(sourceRecord) == kindFile {
		return snapshotEnvelope{}, fmt.Errorf("file %q names another file as its node source", rec.ID)
	}
	if fileType(rec) == fileTypeConfig {
		nodes, err := rt.resolveNodesFor(sourceRecord)
		if err != nil {
			return snapshotEnvelope{}, fmt.Errorf("file %q: %w", rec.ID, err)
		}
		return textEnvelope(kindFile, nodes, ""), nil
	}
	// A script file reads per-member provenance, so its snapshot carries the
	// members whole rather than a merged blob.
	var members []fileScriptMember
	if recordKind(sourceRecord) == kindCollection {
		gathered, err := rt.collectionMembers(sourceRecord)
		if err != nil {
			return snapshotEnvelope{}, fmt.Errorf("file %q: %w", rec.ID, err)
		}
		members, _, err = rt.chainMembers(sourceRecord, gathered)
		if err != nil {
			return snapshotEnvelope{}, fmt.Errorf("file %q: %w", rec.ID, err)
		}
	} else {
		raw, err := rt.renderMemberNodes(sourceRecord)
		if err != nil {
			return snapshotEnvelope{}, fmt.Errorf("file %q: %w", rec.ID, err)
		}
		members = []fileScriptMember{{SubName: memberSubName(sourceRecord), Raw: raw}}
	}
	env := membersEnvelope(kindFile, members)
	env.SourceID, env.SourceName, env.SourceKind = sourceRecord.ID, sourceRecord.Name, recordKind(sourceRecord)
	return env, nil
}

// fetchRecordContent resolves where a record's current content lives and reads
// it: the live vpn-core export over rpc:call, pasted text, or a provider URL
// behind guarded egress. It backs both the refresh path (a stored record) and
// the preview path (an unsaved draft — the engine otherwise never sees the
// draft's source, and a fleet-sourced preview would report "no content" while
// the nodes are right there).
func (rt *runtime) fetchRecordContent(rec subscriptionRecord) (fetchResult, error) {
	label := rec.ID
	if label == "" {
		label = "the unsaved draft"
	}
	// A vpn-core subscription has no provider to reach: its content is the
	// current node export, read over rpc:call. It is handled before the URL
	// checks because there is no URL involved and none should be required.
	if rec.Source == subscriptionSourceVPNCore {
		links, err := rt.fetchExport(subStoreRequest{UserID: rec.VPNIdentity})
		if err != nil {
			return fetchResult{}, err
		}
		if len(links) == 0 {
			// Serving nothing would reach a client as "you have no nodes" and
			// wipe its configuration, so an empty export is an error here
			// rather than empty content passed downstream.
			return fetchResult{}, fmt.Errorf("subscription %s: vpn-core returned no nodes", label)
		}
		return fetchResult{Raw: strings.Join(links, "\n")}, nil
	}
	if rec.Source == subscriptionSourceVPNCoreGraph {
		composed, err := rt.fetchVPNCoreGraph(rec)
		if err != nil {
			return fetchResult{}, err
		}
		return fetchResult{
			Raw:            composed.Raw,
			SourceVersion:  composed.SourceVersion,
			SourceManifest: append(json.RawMessage(nil), composed.SourceManifest...),
		}, nil
	}

	// A manual subscription has nothing to fetch; its content is what was
	// pasted. Returning it here means "refresh" is harmless rather than an
	// error the operator has to learn to ignore.
	if rec.Source == subscriptionSourceLocal {
		if strings.TrimSpace(rec.Content) == "" {
			return fetchResult{}, fmt.Errorf("subscription %q has no pasted content", label)
		}
		return fetchResult{Raw: rec.Content}, nil
	}

	target := strings.TrimSpace(rec.URL)
	if target == "" {
		return fetchResult{}, fmt.Errorf("subscription %q has no URL to fetch", label)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return fetchResult{}, fmt.Errorf("subscription %q has an unparseable URL", label)
	}
	// The scheme is checked here as well as by the broker. A provider URL that is
	// not http(s) is a configuration mistake worth naming at its source, rather
	// than a broker rejection the operator has to trace back.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fetchResult{}, fmt.Errorf("subscription %q URL must be http or https", label)
	}

	// The record's own agent, then the operator's default from Settings, then
	// the plugin's. Settings are read only here, once per invocation, so a
	// record that names its agent costs no extra host call.
	ua := strings.TrimSpace(rec.UA)
	if ua == "" {
		settings, err := rt.invocationSettings()
		if err != nil {
			return fetchResult{}, err
		}
		ua = settings.DefaultUA
	}
	if ua == "" {
		ua = defaultProviderUA
	}
	raw, err := rt.callHost(latticeplugin.HostMethodHTTPDo, map[string]any{
		"method": "GET",
		"url":    target,
		"header": map[string]string{"User-Agent": ua},
	})
	if err != nil {
		return fetchResult{}, redactProviderError(label, err)
	}
	var out struct {
		StatusCode int               `json:"status_code"`
		Header     map[string]string `json:"header,omitempty"`
		BodyBase64 string            `json:"body_base64,omitempty"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fetchResult{}, fmt.Errorf("decode provider response: %w", err)
	}
	if out.StatusCode < 200 || out.StatusCode >= 300 {
		return fetchResult{}, fmt.Errorf("subscription %q provider returned status %d", label, out.StatusCode)
	}
	body, err := base64.StdEncoding.DecodeString(out.BodyBase64)
	if err != nil {
		return fetchResult{}, fmt.Errorf("decode provider body: %w", err)
	}
	if len(body) == 0 {
		// An empty body is a failed fetch, not a subscription with no nodes.
		// Returning it as success would overwrite a good snapshot with nothing.
		return fetchResult{}, fmt.Errorf("subscription %q provider returned an empty body", label)
	}
	if len(body) > maxProviderResponseBytes {
		return fetchResult{}, fmt.Errorf("subscription %q provider returned %d bytes, limit %d", label, len(body), maxProviderResponseBytes)
	}

	return fetchResult{Raw: string(body), Userinfo: userinfoHeader(out.Header)}, nil
}

// userinfoHeader finds the provider's traffic figures. Header names are matched
// case-insensitively because providers are inconsistent about the casing and the
// value is what a client displays as its remaining quota.
func userinfoHeader(header map[string]string) string {
	for name, value := range header {
		if strings.EqualFold(name, "subscription-userinfo") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// redactProviderError keeps a failing URL out of the error text. A provider URL
// is frequently a bearer credential in path form, and this error travels into
// the core's audit log and the operator's screen.
func redactProviderError(label string, err error) error {
	return fmt.Errorf("subscription %q provider request failed: %s", label, redactURLs(err.Error()))
}

func redactURLs(text string) string {
	var out []string
	for _, field := range strings.Fields(text) {
		if strings.Contains(field, "://") {
			out = append(out, "<redacted-url>")
			continue
		}
		out = append(out, field)
	}
	return strings.Join(out, " ")
}
