package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The two record kinds, following the Sub-Store front end's model: a sub is one
// source of nodes; a collection combines subs.
const (
	kindSub        = "sub"
	kindCollection = "collection"
)

// maxCollectionMembers bounds how many subs one collection can pull in. Each
// member is fetched and processed before the collection's own chain runs, so an
// unbounded collection is an unbounded amount of work behind a single public
// URL that anyone holding the token can trigger.
const maxCollectionMembers = 64

// recordKind normalises the discriminator. Records written before collections
// existed carry no kind and are subs.
func recordKind(rec subscriptionRecord) string {
	switch rec.Kind {
	case kindCollection:
		return kindCollection
	case kindFile:
		return kindFile
	default:
		return kindSub
	}
}

// processSteps returns the chain in storage order, tolerating the pre-collections
// field name. Reading both spellings here is what keeps the migration from
// having to rewrite every stored record.
func processSteps(rec subscriptionRecord) []json.RawMessage {
	if len(rec.Process) > 0 {
		return rec.Process
	}
	return rec.Operators
}

// processStepMeta is the part of a step this plugin interprets. Everything else
// in the entry is passed through untouched.
type processStepMeta struct {
	Type       string `json:"type"`
	CustomName string `json:"customName,omitempty"`
	Disabled   bool   `json:"disabled,omitempty"`
}

func decodeStep(raw json.RawMessage) (processStepMeta, error) {
	var meta processStepMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return processStepMeta{}, fmt.Errorf("process step is not an object: %w", err)
	}
	return meta, nil
}

// enabledOperators is what actually reaches the engine: the chain in order, with
// disabled steps removed.
//
// Disabling rather than deleting is the point of the flag — an operator chain is
// something people tune, and losing a step's arguments to try the pipeline
// without it makes the tuning destructive.
func enabledOperators(rec subscriptionRecord) ([]json.RawMessage, error) {
	steps := processSteps(rec)
	out := make([]json.RawMessage, 0, len(steps))
	for index, raw := range steps {
		meta, err := decodeStep(raw)
		if err != nil {
			return nil, fmt.Errorf("process step %d: %w", index+1, err)
		}
		if meta.Disabled {
			continue
		}
		out = append(out, raw)
	}
	return out, nil
}

// validateProcess checks every step, including the disabled ones.
//
// A disabled step is still stored and will run the moment someone re-enables it,
// so accepting an unknown type here would only move the failure to whenever that
// happens — which is exactly when nobody is looking at this screen.
func validateProcess(steps []json.RawMessage) error {
	return validateProcessAgainst(steps, operatorCatalogSet())
}

// validateResponseProcess checks a chain that runs over a served document.
func validateResponseProcess(steps []json.RawMessage) error {
	return validateProcessAgainst(steps, responseOperators)
}

func validateProcessAgainst(steps []json.RawMessage, known map[string]bool) error {
	if len(steps) > maxPipelineOperators {
		return fmt.Errorf("process has %d steps, limit %d", len(steps), maxPipelineOperators)
	}
	for index, raw := range steps {
		meta, err := decodeStep(raw)
		if err != nil {
			return fmt.Errorf("process step %d: %w", index+1, err)
		}
		if strings.TrimSpace(meta.Type) == "" {
			return fmt.Errorf("process step %d has no type", index+1)
		}
		if !known[meta.Type] {
			return fmt.Errorf("process step %d: unknown operator %q", index+1, meta.Type)
		}
	}
	return nil
}

// collectionMembers resolves a collection's inputs to sub records, in a stable
// order: explicit members first in the order given, then tag matches by id.
//
// A member id that does not resolve is an error rather than a silent omission.
// Quietly dropping it would shrink the served subscription and look to a client
// exactly like nodes being withdrawn.
func (rt *runtime) collectionMembers(rec subscriptionRecord) ([]subscriptionRecord, error) {
	all, err := rt.listSubscriptions()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]subscriptionRecord, len(all))
	for _, candidate := range all {
		byID[candidate.ID] = candidate
	}

	seen := make(map[string]bool, len(rec.Members))
	out := make([]subscriptionRecord, 0, len(rec.Members))
	for _, id := range rec.Members {
		member, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("collection %q names a subscription that no longer exists: %q", rec.ID, id)
		}
		if recordKind(member) != kindSub {
			// Nesting a collection inside a collection would let two of them
			// reference each other and render forever; a file is a document,
			// not a node source for a collection.
			return nil, fmt.Errorf("collection %q names %q, which is not a subscription", rec.ID, id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, member)
	}

	if len(rec.MemberTags) > 0 {
		wanted := make(map[string]bool, len(rec.MemberTags))
		for _, tag := range rec.MemberTags {
			wanted[strings.TrimSpace(tag)] = true
		}
		tagged := make([]subscriptionRecord, 0)
		for _, candidate := range all {
			if recordKind(candidate) != kindSub || seen[candidate.ID] {
				continue
			}
			for _, tag := range candidate.Tags {
				if wanted[strings.TrimSpace(tag)] {
					seen[candidate.ID] = true
					tagged = append(tagged, candidate)
					break
				}
			}
		}
		// Tag matches are ordered by id so the output does not depend on the
		// order records happen to sit in storage.
		sort.Slice(tagged, func(i, j int) bool { return tagged[i].ID < tagged[j].ID })
		out = append(out, tagged...)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("collection %q has no members", rec.ID)
	}
	if len(out) > maxCollectionMembers {
		return nil, fmt.Errorf("collection %q has %d members, limit %d", rec.ID, len(out), maxCollectionMembers)
	}
	return out, nil
}
