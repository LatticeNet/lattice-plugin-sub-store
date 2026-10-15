package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
)

// resolveSubContent returns one sub's node text, before any of its operators run.
//
// The three sources are the whole vocabulary: the fleet's own vpn-core nodes, a
// remote provider, or content pasted by hand.
func (rt *runtime) resolveSubContent(rec subscriptionRecord, authority legacyAuthority) (string, error) {
	// The two legacy sources read owner or identity credentials, which only
	// core's refresh may (S2 plan section 1.2).
	if isVPNCoreSource(rec.Source) && authority != legacyRefresh {
		return "", ownerCredentialsWithheldError(rec.ID)
	}
	switch rec.Source {
	case subscriptionSourceVPNCore:
		links, err := rt.fetchExport(subStoreRequest{UserID: rec.VPNIdentity})
		if err != nil {
			return "", err
		}
		if len(links) == 0 {
			return "", fmt.Errorf("subscription %q: vpn-core returned no nodes", rec.ID)
		}
		return strings.Join(links, "\n"), nil
	case subscriptionSourceVPNCoreGraph:
		fetched, err := rt.fetchVPNCoreGraph(rec)
		if err != nil {
			return "", err
		}
		return fetched.Raw, nil
	case subscriptionSourceLocal:
		// Explicitly manual: the pasted content is the answer even if a stale
		// URL is still sitting in the record from an earlier edit.
		if strings.TrimSpace(rec.Content) == "" {
			return "", fmt.Errorf("subscription %q has no pasted content", rec.ID)
		}
		return rec.Content, nil
	case subscriptionSourceRemote:
		if strings.TrimSpace(rec.URL) == "" {
			return "", fmt.Errorf("subscription %q has no provider URL", rec.ID)
		}
		// The record is already in hand: fetch its content directly. Going back
		// through fetchSubscription would re-read the whole records document to
		// find the record the caller just had, one extra host round trip per
		// collection member — the N+1 that priced a real collection render past
		// its host_calls budget.
		fetched, err := rt.fetchRecordContent(rec)
		if err != nil {
			return "", err
		}
		return fetched.Raw, nil
	default:
		// Records written before the source was named: whichever field is set.
		if strings.TrimSpace(rec.URL) != "" {
			fetched, err := rt.fetchRecordContent(rec)
			if err != nil {
				return "", err
			}
			return fetched.Raw, nil
		}
		if strings.TrimSpace(rec.Content) != "" {
			return rec.Content, nil
		}
		return "", fmt.Errorf("subscription %q has no content to render", rec.ID)
	}
}

// renderMemberNodes runs one member sub through its own operator chain and
// returns the node list as text, ready to be merged with its siblings.
//
// Each member is processed with its OWN chain before merging, which is the
// upstream semantics and the reason it matters: a per-sub rename or region
// filter has to apply to that sub's nodes, not to everything the collection
// happens to gather.
//
// A member whose source yields no nodes at all (a provider's error page, a
// login wall) is a failed member, not an empty one: the collection's failure
// mode decides what happens next, and a strict refresh fails so the core keeps
// its last good snapshot. A member whose own chain filters every node away
// still contributes nothing, as before.
func (rt *runtime) renderMemberNodes(member subscriptionRecord, authority legacyAuthority) (string, error) {
	// Every caller is a file's node work, and a fleet record has no node
	// text a file may carry.
	if member.Source == subscriptionSourceFleet {
		return "", fleetNodesForFile([]string{member.ID}, "")
	}
	out, err := rt.memberNodes(member, authority)
	if err != nil {
		return "", err
	}
	if out.needsCount {
		if _, err := requireNativeNodesWith(memberLabel(member), out.raw, parseOptionsFor(member)); err != nil {
			return "", err
		}
	}
	return out.raw, nil
}

func memberLabel(member subscriptionRecord) string {
	return fmt.Sprintf("subscription %q", member.ID)
}

// memberOutput is one member after its own chain.
type memberOutput struct {
	// raw is the member's node text: as it arrived for an unchained member,
	// the chain's URI output for a chained one.
	raw string
	// nodes are the native chain's nodes, when the member's chain ran in Go.
	nodes []*nodemodel.Node
	// needsCount is true when raw came back as it arrived, so a combination
	// can confirm all of its members at once. A chained member's count is
	// already known from its conversion.
	needsCount bool
	// native reports that the member's chain runs in Go (an unchained member
	// trivially does).
	native bool
}

// memberNodes resolves one member and runs its own chain, leaving the node
// count of an unchained member to the caller. A chained member goes through
// the dispatcher to URI: in Go when its whole chain is native, on the
// bundle's isolated path otherwise.
func (rt *runtime) memberNodes(member subscriptionRecord, authority legacyAuthority) (memberOutput, error) {
	raw, err := rt.resolveSubContent(member, authority)
	if err != nil {
		return memberOutput{}, err
	}
	plan, err := rt.chainPlan(member)
	if err != nil {
		return memberOutput{}, fmt.Errorf("%s: %w", memberLabel(member), err)
	}
	if enabledSteps(plan) == 0 {
		// Returned as it arrived: the collection parses each member on its own,
		// so a member's encoding never has to match its siblings'. Converting it
		// to URI here would drop every node that format cannot express (HTTP,
		// Snell, SSH), which a same-format combination serves today.
		return memberOutput{raw: raw, needsCount: true, native: true}, nil
	}
	// URI carries a chained member: the chain's output has to be node text the
	// collection can parse again.
	converted, served, err := rt.convertNodes(nodeConvertRequest{
		Parts:         []string{raw},
		Target:        "URI",
		Plan:          plan,
		CarrierCheck:  true,
		AllowExternal: parseOptionsFor(member).AllowExternal,
	})
	if err != nil {
		return memberOutput{}, fmt.Errorf("%s: %w", memberLabel(member), err)
	}
	if converted.SourceNodeCount == 0 {
		return memberOutput{}, providerNoNodesError(memberLabel(member))
	}
	// URI has no form for some protocols, so a chained member with HTTP, Snell
	// or SSH nodes would lose them here while an unchained sibling keeps them.
	// Serving the rest as the member's whole list is what the failure mode
	// exists to prevent, so the member fails instead: strict refuses the
	// refresh and keeps the last good snapshot, skip leaves the member out.
	if converted.CarrierLostNodeCount > 0 {
		return memberOutput{}, memberChainDropsNodesError(memberLabel(member), converted.CarrierLostNodeCount, converted.NodeCount, converted.CarrierLostProtocols)
	}
	return memberOutput{raw: converted.Output, nodes: converted.nodes, native: served == servedNative}, nil
}

// collectionMemberFailureIsSkippable reports whether the collection's failure
// mode may leave a failed member out. A graph member never may: its
// composition is authoritative. A fleet member never may either: a
// collection that silently dropped its fleet half would serve its provider
// half to identity-bound shares as if nothing happened (S2 plan section
// 1.2).
func collectionMemberFailureIsSkippable(collection, member subscriptionRecord) bool {
	return collection.FailureMode == failureModeSkip && member.Source != subscriptionSourceVPNCoreGraph &&
		member.Source != subscriptionSourceFleet
}

// renderCollection merges every member's processed nodes, then runs the
// collection's own chain over the whole set.
//
// A non-empty snapshotRaw is the refresh path's answer: members already
// resolved and chained at fetch time, so the render pays no network and no
// per-member work — only the collection's own chain. An empty one renders
// live, which is how previews and unsaved drafts work.
func (rt *runtime) renderCollection(rec subscriptionRecord, target string, options map[string]bool, snapshotRaw string, authority legacyAuthority) (string, error) {
	converted, err := rt.renderCollectionResult(rec, target, options, snapshotRaw, false, authority)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(converted.Output) == "" {
		return "", fmt.Errorf("collection %q converted to empty content", rec.ID)
	}
	return converted.Output, nil
}

// renderCollectionResult is renderCollection's whole answer: the document plus
// the counts and the zero-node flag the serve path decides on.
//
// Members are handed to the engine as separate parts, each parsed on its own,
// and their node lists concatenated. Joining them as text and parsing once
// lost whole members whenever their encodings differed (a URI list beside a
// base64 list or a Clash document), and served the rest as complete. Parts
// also cover a snapshot the core stored before this change, whose members are
// provider bodies exactly as they arrived.
//
// The collection renders in Go only when its own chain, every member's chain
// and the target are native (plan section 1.3): the members' nodes are then
// concatenated as the member chains left them, with no URI round trip, and
// the collection chain runs over them. Otherwise the whole collection renders
// on the bundle's isolated path from the members' texts.
func (rt *runtime) renderCollectionResult(rec subscriptionRecord, target string, options map[string]bool, snapshotRaw string, explain bool, authority legacyAuthority) (subStoreConversionResult, error) {
	plan, err := rt.chainPlan(rec)
	if err != nil {
		return subStoreConversionResult{}, fmt.Errorf("collection %q: %w", rec.ID, err)
	}
	// Core hands back only snapshots this plugin wrote, so a non-empty one
	// that does not decode, or holds no usable member block, is an error and
	// never a reason to resolve the members live (S2 plan section 1.2): the
	// live path would read a legacy member's export for whoever called
	// render with a broken raw. An empty one renders live, which is how
	// previews and unsaved drafts work.
	members, membersNative, err := snapshotMembers(snapshotRaw, nativeRoute(plan, target))
	if err != nil {
		return subStoreConversionResult{}, fmt.Errorf("%s: collection %q has a snapshot this plugin cannot read; the next refresh replaces it", codeSnapshotMalformed, rec.ID)
	}
	// A fleet block has no text a document can carry: its nodes exist only
	// over placeholders, inside a plan. A collection rendered as a document
	// is not fleet-bound live, so a snapshot holding fleet blocks predates
	// the change that unbound it.
	for _, member := range members {
		if member.block.fleet != nil {
			return subStoreConversionResult{}, fmt.Errorf("%s: collection %q is not fleet-bound and its snapshot holds the fleet block of %q; the next refresh replaces it", codeFleetEnvelopeMismatch, rec.ID, member.block.id)
		}
	}
	if len(members) == 0 {
		gathered, err := rt.collectionMembers(rec)
		if err != nil {
			return subStoreConversionResult{}, err
		}
		if members, membersNative, err = rt.chainMembers(rec, gathered, false, authority); err != nil {
			return subStoreConversionResult{}, err
		}
	}
	return rt.convertCollectionMembers(plan, target, options, explain, members, membersNative)
}

// convertCollectionMembers converts a collection's resolved members with its
// own chain: in Go when every chain ran there, on the bundle from the
// members' texts otherwise.
func (rt *runtime) convertCollectionMembers(plan *operators.Plan, target string, options map[string]bool, explain bool, members []fileScriptMember, membersNative bool) (subStoreConversionResult, error) {
	request := nodeConvertRequest{
		Target:         target,
		Plan:           plan,
		MemberFallback: !membersNative,
		Options:        options,
		Explain:        explain,
	}
	for _, member := range members {
		request.Parts = append(request.Parts, member.Raw)
		request.Nodes = append(request.Nodes, member.nodes...)
	}
	converted, _, err := rt.convertNodes(request)
	return converted, err
}

// errSnapshotMalformed is snapshotMembers' refusal of a snapshot it cannot
// read; its caller names the record.
var errSnapshotMalformed = fmt.Errorf("%s: the snapshot is not one this plugin can read; the next refresh replaces it", codeSnapshotMalformed)

// snapshotMembers reads a collection snapshot's members: a version 2
// envelope, or the members object a version 1 snapshot is (and snapshotText
// gives back). Every block's member facts are read (id, source, revision,
// steps), provider blocks included. A fleet block is kept with its selection
// (catalogue version, selector, rows) and no text; a provider block with no
// text is skipped. A non-empty snapshot it cannot read, or one that holds no
// usable block, is snapshot_malformed: core hands the plugin only snapshots
// the plugin wrote, so such a snapshot is never a reason to resolve the
// members live (S2 plan section 1.2).
//
// membersNative reports that every provider member's chain ran in Go when
// the snapshot was taken, which only a version 2 envelope can say: its
// members carry their nodes, or it says some or all were omitted for size.
// Anything else (a version 1 snapshot, an envelope whose members ran on the
// bundle, one written before member nodes existed) renders whole on the
// bundle, as the members' texts came from it.
//
// Nodes are read only when the render will read them, and then every
// provider member gets its nodes: decoded where the envelope carries them,
// parsed from the member's text where the size bound left them out. Nodes
// that do not decode leave every member to its text, as before.
func snapshotMembers(snapshotRaw string, decode bool) ([]fileScriptMember, bool, error) {
	if strings.TrimSpace(snapshotRaw) == "" {
		return nil, false, nil
	}
	malformed := errSnapshotMalformed
	if env, ok := decodeSnapshotEnvelope(snapshotRaw); ok && len(env.Members) > 0 {
		withNodes, providers := 0, 0
		members := make([]fileScriptMember, 0, len(env.Members))
		for _, member := range env.Members {
			block := memberBlock{id: member.ID, source: member.Source, revision: member.Revision, steps: member.Steps}
			if member.Source == subscriptionSourceFleet {
				block.fleet = &fleetMemberBlock{catalogueVersion: member.CatalogueVersion, selector: member.Selector, rows: member.Rows}
				members = append(members, fileScriptMember{SubName: member.SubName, block: block})
				continue
			}
			if strings.TrimSpace(member.Raw) == "" {
				continue
			}
			providers++
			if len(member.Nodes) > 0 {
				withNodes++
			}
			members = append(members, fileScriptMember{SubName: member.SubName, Raw: strings.TrimSpace(member.Raw), block: block})
		}
		if len(members) == 0 {
			return nil, false, malformed
		}
		membersNative := providers > 0 && (withNodes == providers || env.NodesOmitted == nodesOmittedSize)
		if membersNative && decode {
			at := 0
			for _, member := range env.Members {
				if member.Source == subscriptionSourceFleet {
					at++
					continue
				}
				if strings.TrimSpace(member.Raw) == "" {
					continue
				}
				var nodes []*nodemodel.Node
				ok := true
				if len(member.Nodes) > 0 {
					nodes, ok = decodeNodes(member.Nodes)
				} else {
					// Left out for size: the member's text, parsed. For a
					// member with no chain of its own that is what the live
					// path does; a chained member's nodes go only when its
					// chain's nodes alone pass the bound, and its text is
					// that chain's URI output (encodeSnapshotEnvelope).
					var err error
					nodes, err = parseParts([]string{members[at].Raw})
					ok = err == nil
				}
				if !ok {
					// Undecodable nodes: parse the texts instead.
					for i := range members {
						members[i].nodes = nil
					}
					break
				}
				members[at].nodes = nodes
				at++
			}
		}
		return members, membersNative, nil
	}
	var snap snapshotArtifacts
	if err := json.Unmarshal([]byte(snapshotRaw), &snap); err != nil {
		return nil, false, malformed
	}
	members := make([]fileScriptMember, 0, len(snap.Members))
	for _, member := range snap.Members {
		if trimmed := strings.TrimSpace(member.Raw); trimmed != "" {
			members = append(members, fileScriptMember{SubName: member.SubName, Raw: trimmed})
		}
	}
	if len(members) == 0 {
		return nil, false, malformed
	}
	return members, false, nil
}
