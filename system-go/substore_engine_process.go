package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-sdk/model"
)

// processNodes runs the bundle's process over node objects with a record's
// operators, isolated, with the invocation's script network attached as for
// any other isolated call, and returns the node list (S2 plan section 1.4).
// It is how a fleet record, or a fleet-bound collection, with a Script
// Operator, Script Filter or Resolve Domain step runs in S2, at the isolated
// cost, until S3's shim and S4's resolver.
//
// Nodes travel as node objects with their Lattice block under "_lattice",
// which the bundle keeps; the Script map does not travel. On return every
// node's Lattice block is discarded, because a script could have rewritten
// it: the caller attaches the row's block again by the placeholder in the
// node's credential field. A collection's provider mark (fleetProviderMark)
// rides under "_lattice.provenance" and is put back into the Script map, so
// provenance survives the round trip; a script that copies a provider node's
// block onto a node it invents makes a provider node, the same power it holds
// over a provider node's own fields, and cannot make a fleet node, because
// only the plugin mints placeholders.
func (engine *subStoreEngine) processNodes(nodes []*nodemodel.Node, operators []json.RawMessage, target string) (out []*nodemodel.Node, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = redactSubStoreEnginePanic(recovered)
		}
	}()
	if strings.TrimSpace(engine.coreJS) == "" {
		return nil, fmt.Errorf("Sub-Store core bundle is empty")
	}
	encoded := make([]json.RawMessage, len(nodes))
	for i, n := range nodes {
		carried := n
		if mark, ok := n.Script[fleetProviderMark].(string); ok && mark != "" {
			carried = &nodemodel.Node{Fields: n.Fields, Lattice: &nodemodel.LatticeFields{Provenance: mark}}
		}
		raw, err := carried.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("encode node %d: %w", i, err)
		}
		encoded[i] = raw
	}
	script, err := subStoreProcessNodesScript(encoded, operators, target)
	if err != nil {
		return nil, err
	}
	rawResult, err := engine.runIsolatedScript("process nodes", "lattice-substore-process-nodes.js", script)
	if err != nil {
		return nil, err
	}
	var result struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(rawResult), &result); err != nil {
		return nil, fmt.Errorf("decode Sub-Store processed nodes: %w", err)
	}
	if len(result.Nodes) > model.MaxSubscriptionRecordNodes {
		return nil, fmt.Errorf("the chain returned %d nodes; one record carries at most %d", len(result.Nodes), model.MaxSubscriptionRecordNodes)
	}
	out = make([]*nodemodel.Node, 0, len(result.Nodes))
	for i, raw := range result.Nodes {
		n := &nodemodel.Node{}
		// The decoder names a type, never a value.
		if err := n.UnmarshalJSON(raw); err != nil {
			return nil, fmt.Errorf("processed node %d: %w", i, err)
		}
		if n.Lattice != nil && n.Lattice.Provenance != "" {
			n.Script = map[string]any{fleetProviderMark: n.Lattice.Provenance}
		}
		n.Lattice = nil
		out = append(out, n)
	}
	return out, nil
}

// subStoreProcessNodesScript is processNodes' IIFE: the node objects through
// the bundle's process with the operators, returned as JSON.
func subStoreProcessNodesScript(nodes []json.RawMessage, operators []json.RawMessage, target string) (string, error) {
	if nodes == nil {
		nodes = []json.RawMessage{}
	}
	if operators == nil {
		operators = []json.RawMessage{}
	}
	encodedNodes, err := json.Marshal(nodes)
	if err != nil {
		return "", fmt.Errorf("encode nodes: %w", err)
	}
	encodedOperators, err := json.Marshal(operators)
	if err != nil {
		return "", fmt.Errorf("encode operators: %w", err)
	}
	encodedTarget, err := json.Marshal(target)
	if err != nil {
		return "", fmt.Errorf("encode target: %w", err)
	}
	return fmt.Sprintf(`(async function() {
  const nodes = %s;
  const operators = %s;
  const target = %s;
  const root = globalThis.SubStoreProxyUtils;
  const core = root && root.ProxyUtils ? root.ProxyUtils : root;
  if (!core || typeof core.process !== "function") {
    throw new Error("Sub-Store core must expose process(proxies, operators)");
  }
  let proxies = [];
  for (const proxy of nodes) proxies.push(proxy);
  proxies = await core.process(proxies, operators, target, undefined, undefined, "");
  if (!Array.isArray(proxies)) {
    throw new Error("Sub-Store process(proxies, operators) must return an array");
  }
  return JSON.stringify({ nodes: proxies });
})()`, encodedNodes, encodedOperators, encodedTarget), nil
}
