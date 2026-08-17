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
func (rt *runtime) renderMemberNodes(member subscriptionRecord) (string, error) {
	raw, err := rt.resolveSubContent(member)
	if err != nil {
		return "", err
	}
	operators, err := enabledOperators(member)
	if err != nil {
		return "", fmt.Errorf("subscription %q: %w", member.ID, err)
	}
	if len(operators) == 0 {
		return raw, nil
	}
	// URI is the merge format: it is the one target that round-trips through a
	// second conversion, which is what merging then converting again requires.
	converted, err := rt.subStoreEngine().convert(subStoreConversionRequest{
		Raw:       raw,
		Target:    "URI",
		Operators: operators,
	})
	if err != nil {
		return "", fmt.Errorf("subscription %q: %w", member.ID, err)
	}
	return converted.Output, nil
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
func (rt *runtime) renderCollection(rec subscriptionRecord, uaClass, snapshotRaw string) (string, error) {
	merged := ""
	if strings.TrimSpace(snapshotRaw) != "" {
		var snap snapshotArtifacts
		if err := json.Unmarshal([]byte(snapshotRaw), &snap); err == nil {
			parts := make([]string, 0, len(snap.Members))
			for _, member := range snap.Members {
				if trimmed := strings.TrimSpace(member.Raw); trimmed != "" {
					parts = append(parts, trimmed)
				}
			}
			merged = strings.Join(parts, "\n")
		}
		// A snapshot that does not decode or carries nothing is not a reason to
		// fail the serve: fall through to the live path rather than deny a
		// client its nodes.
	}
	if merged == "" {
		members, err := rt.collectionMembers(rec)
		if err != nil {
			return "", err
		}
		chained, err := rt.chainMembers(rec, members)
		if err != nil {
			return "", err
		}
		parts := make([]string, 0, len(chained))
		for _, member := range chained {
			parts = append(parts, member.Raw)
		}
		merged = strings.Join(parts, "\n")
	}

	operators, err := enabledOperators(rec)
	if err != nil {
		return "", fmt.Errorf("collection %q: %w", rec.ID, err)
	}
	converted, err := rt.subStoreEngine().convert(subStoreConversionRequest{
		Raw:       merged,
		Target:    subscriptionTarget(rec, uaClass),
		Operators: operators,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(converted.Output) == "" {
		return "", fmt.Errorf("collection %q converted to empty content", rec.ID)
	}
	return converted.Output, nil
}
