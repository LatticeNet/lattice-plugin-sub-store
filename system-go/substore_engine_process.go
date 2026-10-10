package main

import (
	"encoding/json"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// processNodes runs the bundle's process over node objects with a record's
// operators, isolated, with the invocation's script network attached as
// today, and returns the node list (S2 plan section 1.4). It is how a fleet
// record with a Script Operator, Script Filter or Resolve Domain step runs
// in S2, at the isolated cost, until S3's shim and S4's resolver. Nodes
// travel with their Lattice block under "_lattice"; the caller discards it on
// return and attaches it again by placeholder, so a script cannot move a
// node to another line, geo or chain.
func (e *subStoreEngine) processNodes(nodes []*nodemodel.Node, operators []json.RawMessage) ([]*nodemodel.Node, error) {
	_, _ = nodes, operators
	return nil, errProcessNodesUnavailable
}
