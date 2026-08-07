package main

import (
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
		fetched, err := rt.fetchSubscription(rec.ID)
		if err != nil {
			return "", err
		}
		return fetched.Raw, nil
	default:
		// Records written before the source was named: whichever field is set.
		if strings.TrimSpace(rec.URL) != "" {
			fetched, err := rt.fetchSubscription(rec.ID)
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

// renderCollection merges every member's processed nodes, then runs the
// collection's own chain over the whole set.
func (rt *runtime) renderCollection(rec subscriptionRecord, uaClass string) (string, error) {
	members, err := rt.collectionMembers(rec)
	if err != nil {
		return "", err
	}

	parts := make([]string, 0, len(members))
	skipped := make([]string, 0)
	for _, member := range members {
		nodes, err := rt.renderMemberNodes(member)
		if err != nil {
			// Strict is the default because serving only the survivors reaches
			// a client as "those nodes were removed", and the client acts on
			// that by deleting them. Skipping is available because one dead
			// provider should not take down a large collection — but it is a
			// choice the operator makes, not one made for them.
			if rec.FailureMode != failureModeSkip {
				return "", fmt.Errorf("collection %q: %w", rec.ID, err)
			}
			skipped = append(skipped, member.ID)
			continue
		}
		if trimmed := strings.TrimSpace(nodes); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	// Every member failing is not "skip the failures" — it is a collection with
	// nothing in it, and that must never be served as a success.
	if len(parts) == 0 && len(skipped) > 0 {
		return "", fmt.Errorf("collection %q: every member failed (%s)", rec.ID, strings.Join(skipped, ", "))
	}
	merged := strings.Join(parts, "\n")
	if strings.TrimSpace(merged) == "" {
		return "", fmt.Errorf("collection %q produced no nodes", rec.ID)
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
