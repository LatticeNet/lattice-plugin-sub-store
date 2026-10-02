package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// resolveSubContent returns one sub's node text, before any of its operators run.
//
// The three sources are the whole vocabulary: the fleet's own vpn-core nodes, a
// remote provider, or content pasted by hand.
func (rt *runtime) resolveSubContent(rec subscriptionRecord) (string, error) {
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
func (rt *runtime) renderMemberNodes(member subscriptionRecord) (string, error) {
	raw, needsCount, err := rt.memberNodes(member)
	if err != nil {
		return "", err
	}
	if needsCount {
		if err := rt.requireNodes(memberLabel(member), raw); err != nil {
			return "", err
		}
	}
	return raw, nil
}

func memberLabel(member subscriptionRecord) string {
	return fmt.Sprintf("subscription %q", member.ID)
}

// memberNodes resolves one member and runs its own chain, leaving the node
// count of an unchained member to the caller: needsCount is true when the text
// came back as it arrived, so a combination can confirm all of its members in
// one engine call. A chained member's count is already known from its
// conversion.
func (rt *runtime) memberNodes(member subscriptionRecord) (raw string, needsCount bool, err error) {
	raw, err = rt.resolveSubContent(member)
	if err != nil {
		return "", false, err
	}
	operators, err := enabledOperators(member)
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", memberLabel(member), err)
	}
	if len(operators) == 0 {
		// Returned as it arrived: the collection parses each member on its own,
		// so a member's encoding never has to match its siblings'. Converting it
		// to URI here would drop every node that format cannot express (HTTP,
		// Snell, SSH), which a same-format combination serves today.
		return raw, true, nil
	}
	// URI carries a chained member: the chain's output has to be node text the
	// collection can parse again.
	converted, err := rt.subStoreEngine().convert(subStoreConversionRequest{
		Raw:       raw,
		Target:    "URI",
		Operators: operators,
	})
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", memberLabel(member), err)
	}
	if converted.SourceNodeCount == 0 {
		return "", false, providerNoNodesError(memberLabel(member))
	}
	return converted.Output, false, nil
}

func collectionMemberFailureIsSkippable(collection, member subscriptionRecord) bool {
	return collection.FailureMode == failureModeSkip && member.Source != subscriptionSourceVPNCoreGraph
}

// renderCollection merges every member's processed nodes, then runs the
// collection's own chain over the whole set.
//
// A non-empty snapshotRaw is the refresh path's answer: members already
// resolved and chained at fetch time, so the render pays no network and no
// per-member work — only the collection's own chain. An empty one renders
// live, which is how previews and unsaved drafts work.
func (rt *runtime) renderCollection(rec subscriptionRecord, target string, options map[string]bool, snapshotRaw string) (string, error) {
	converted, err := rt.renderCollectionResult(rec, target, options, snapshotRaw, false)
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
func (rt *runtime) renderCollectionResult(rec subscriptionRecord, target string, options map[string]bool, snapshotRaw string, explain bool) (subStoreConversionResult, error) {
	var parts []string
	if strings.TrimSpace(snapshotRaw) != "" {
		var snap snapshotArtifacts
		if err := json.Unmarshal([]byte(snapshotRaw), &snap); err == nil {
			for _, member := range snap.Members {
				if trimmed := strings.TrimSpace(member.Raw); trimmed != "" {
					parts = append(parts, trimmed)
				}
			}
		}
		// A snapshot that does not decode or carries nothing is not a reason to
		// fail the serve: fall through to the live path rather than deny a
		// client its nodes.
	}
	if len(parts) == 0 {
		members, err := rt.collectionMembers(rec)
		if err != nil {
			return subStoreConversionResult{}, err
		}
		chained, err := rt.chainMembers(rec, members)
		if err != nil {
			return subStoreConversionResult{}, err
		}
		for _, member := range chained {
			parts = append(parts, member.Raw)
		}
	}

	operators, err := enabledOperators(rec)
	if err != nil {
		return subStoreConversionResult{}, fmt.Errorf("collection %q: %w", rec.ID, err)
	}
	return rt.subStoreEngine().convert(subStoreConversionRequest{
		RawParts:  parts,
		Target:    target,
		Operators: operators,
		Options:   options,
		Explain:   explain,
	})
}
