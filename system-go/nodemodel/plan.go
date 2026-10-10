package nodemodel

import (
	"encoding/json"
	"errors"
	"fmt"
)

// A plan node is the node object a selection plan carries (S2 plan section
// 2.3). Core admits a node key only from its compared, mutable and carried
// sets, and "_lattice" is in none of them, so a plan node carries the fleet
// block flattened to the top level under core's names. The plugin flattens
// rather than teaching core a nested object, so core keeps one place for the
// fields it compares.

// PlanLatticeFields are the catalogue metadata keys a plan node carries at
// the top level: core carries them without comparing them, and convert
// strips them before any producer runs. line_uuid is not here: core compares
// it, and the plan names a node's line in SelectionPlanNode.LineUUID.
//
// It mirrors model.SelectionPlanLatticeFields, which the SDK publishes in S2
// (lane 5a); TestPlanLatticeFieldsAreTheSDKList holds the two equal once the
// SDK pin carries the list.
var PlanLatticeFields = []string{"line_hash_id", "node_id", "geo", "chain", "tags", "groups", "probe", "addresses"}

// PlanStrippedFields is what convert removes before any producer runs: the
// Lattice fields and line_uuid. It mirrors model.SelectionPlanStrippedFields.
var PlanStrippedFields = append([]string{"line_uuid"}, PlanLatticeFields...)

// planLatticeKey reports whether key is one of PlanLatticeFields.
func planLatticeKey(key string) bool {
	for _, name := range PlanLatticeFields {
		if name == key {
			return true
		}
	}
	return false
}

// planStrippedKey reports whether key is one of PlanStrippedFields.
func planStrippedKey(key string) bool {
	return key == "line_uuid" || planLatticeKey(key)
}

// MarshalPlanNode writes Fields plus the Lattice fields at the top level
// under core's names (PlanLatticeFields), keys sorted at every depth, Script
// omitted. It is the only encoder a plan uses.
//
// The Lattice block is authoritative: a key of PlanStrippedFields or
// "_lattice" that Fields holds (a script that wrote "geo" or "line_uuid" into
// a node through the bundle) is never written, and the block's own value is
// written in its place. A field the block leaves empty is not written at
// all. Numbers and text are written as MarshalJSON writes them.
func (n *Node) MarshalPlanNode() ([]byte, error) {
	if n == nil {
		return nil, errors.New("nodemodel: a plan node must not be nil")
	}
	fields := make(map[string]any, len(n.Fields)+len(PlanLatticeFields))
	for k, v := range n.Fields {
		if k == latticeKey || planStrippedKey(k) {
			continue
		}
		fields[k] = v
	}
	if n.Lattice != nil {
		block, err := latticeValue(n.Lattice)
		if err != nil {
			return nil, err
		}
		for k, v := range block {
			if planLatticeKey(k) {
				fields[k] = v
			}
		}
	}
	return appendValue(nil, fields, 0)
}

// UnmarshalPlanNode reads a bound node core sends to convert: every key in
// PlanLatticeFields moves into Lattice, so no producer writes it. "_lattice"
// is refused, because a plan node never carries it. line_uuid stays in Fields
// for StripLattice to remove. The protocol tuning fields and parser
// annotations core also carries (flow, up, down, _h2 and the rest) stay in
// Fields: producers read them.
//
// A Lattice key whose value does not decode into its field's type is an
// error. Errors name the key and never quote a value: a bound node carries
// the identity's credential.
func (n *Node) UnmarshalPlanNode(data []byte) error {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("nodemodel: a plan node must be a JSON object")
	}
	if _, ok := fields[latticeKey]; ok {
		return errors.New("nodemodel: a plan node carries no _lattice key")
	}
	block := map[string]any{}
	for _, key := range PlanLatticeFields {
		if v, ok := fields[key]; ok {
			delete(fields, key)
			if v != nil {
				block[key] = v
			}
		}
	}
	n.Fields, n.Script, n.Lattice = fields, nil, nil
	if len(block) == 0 {
		return nil
	}
	encoded, err := json.Marshal(block)
	if err != nil {
		return err
	}
	var l LatticeFields
	if err := json.Unmarshal(encoded, &l); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return fmt.Errorf("nodemodel: plan node field %q has the wrong type", typeErr.Field)
		}
		return errors.New("nodemodel: plan node Lattice fields do not decode")
	}
	n.Lattice = &l
	return nil
}

// StripLattice removes every PlanStrippedFields key from Fields in place: the
// Lattice fields plus line_uuid, which core compares and convert must strip
// too. Convert calls it before any producer runs; producers never call it
// themselves. Unknown keys in a node pass through to the producers, which is
// why the list is explicit rather than "everything core carries".
func StripLattice(n *Node) {
	if n == nil {
		return
	}
	for _, key := range PlanStrippedFields {
		delete(n.Fields, key)
	}
	delete(n.Fields, latticeKey)
}
