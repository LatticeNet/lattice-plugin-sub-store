package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-sdk/model"
)

// processNodes runs the bundle's process over node objects with a record's
// operators, isolated, and returns the node list (S2 plan section 1.4). It is
// how a fleet record, or a fleet-bound collection, with a Script Operator,
// Script Filter or Resolve Domain step runs in S2, at the isolated cost,
// until S3's shim and S4's resolver.
//
// The chain runs with the script network withheld and sees no line identity
// (S2-SR-2). A step that asks for the network fails the call with
// fleet_script_network_unavailable even if the script caught the refusal.
// Nodes travel as node objects with no Lattice block, and every credential
// placeholder, which names its line, is swapped for an opaque token drawn for
// this call (fleetCredentialMasks); on return the tokens are swapped back, so
// the caller recovers each node's line by its placeholder and attaches the
// row's block again outside the script, as it always did, because a script
// could have rewritten anything it was handed. The Script map does not
// travel. A collection's provider mark (fleetProviderMark), a random value
// per computation that carries nothing about a line, rides under
// "_lattice.provenance", which the bundle keeps, and is put back into the
// Script map, so provenance survives the round trip; a script that copies a
// provider node's mark onto a node it invents makes a provider node, the same
// power it holds over a provider node's own fields, and cannot make a fleet
// node, because only the plugin mints placeholders and their tokens.
func (engine *subStoreEngine) processNodes(nodes []*nodemodel.Node, operators []json.RawMessage, target string) (out []*nodemodel.Node, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = redactSubStoreEnginePanic(recovered)
		}
	}()
	if strings.TrimSpace(engine.coreJS) == "" {
		return nil, fmt.Errorf("Sub-Store core bundle is empty")
	}
	masks := fleetCredentialMasks{}
	encoded := make([]json.RawMessage, len(nodes))
	for i, n := range nodes {
		carried, err := masks.carry(n)
		if err != nil {
			return nil, fmt.Errorf("encode node %d: %w", i, err)
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
	refusal, restore := engine.withholdScriptHTTP()
	defer restore()
	before := refusal.refusedCalls()
	rawResult, err := engine.runIsolatedScript("process nodes", "lattice-substore-process-nodes.js", script)
	if asked := refusal.refusedCalls() - before; asked > 0 {
		return nil, fmt.Errorf("%s: a Script or Resolve Domain step asked for the network %d time(s), and a fleet-bound chain runs without one until S3", codeFleetScriptNetworkUnavailable, asked)
	}
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
		masks.restore(n)
		if n.Lattice != nil && n.Lattice.Provenance != "" {
			n.Script = map[string]any{fleetProviderMark: n.Lattice.Provenance}
		}
		n.Lattice = nil
		out = append(out, n)
	}
	return out, nil
}

// fleetCredentialMasks is one processNodes call's swap of credential
// placeholders for opaque tokens. A placeholder carries its line's uuid
// (model.NewPlanPlaceholder), so a script never sees one: it sees a token of
// 16 random bytes that names nothing, the same token for every node of one
// line, so clones stay clones.
type fleetCredentialMasks struct {
	token       map[string]string // placeholder to token
	placeholder map[string]string // token to placeholder
}

// fleetMaskTokenPrefix starts every token. It is not the placeholder prefix,
// so no token reads as a placeholder anywhere it ends up.
const fleetMaskTokenPrefix = "lattice-masked-"

// carry is the node a script receives for n: n's fields with every
// placeholder in a credential field swapped for its token, and no Lattice
// block but a provider node's mark.
func (m *fleetCredentialMasks) carry(n *nodemodel.Node) (*nodemodel.Node, error) {
	fields := n.Fields
	cloned := false
	for _, key := range fleetNodeCredentialKeys {
		value, ok := fields[key].(string)
		if !ok {
			continue
		}
		if _, _, err := model.ParsePlanPlaceholder(value); err != nil {
			continue
		}
		token, err := m.tokenFor(value)
		if err != nil {
			return nil, err
		}
		if !cloned {
			fields, cloned = maps.Clone(n.Fields), true
		}
		fields[key] = token
	}
	carried := &nodemodel.Node{Fields: fields}
	if mark, ok := n.Script[fleetProviderMark].(string); ok && mark != "" {
		carried.Lattice = &nodemodel.LatticeFields{Provenance: mark}
	}
	return carried, nil
}

// tokenFor is placeholder's token, drawn the first time it is asked for.
func (m *fleetCredentialMasks) tokenFor(placeholder string) (string, error) {
	if token, ok := m.token[placeholder]; ok {
		return token, nil
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	token := fleetMaskTokenPrefix + hex.EncodeToString(random)
	if m.token == nil {
		m.token, m.placeholder = map[string]string{}, map[string]string{}
	}
	m.token[placeholder], m.placeholder[token] = token, placeholder
	return token, nil
}

// restore swaps a token in any of a post-chain node's credential fields
// back for its placeholder. A token the script moved elsewhere stays an
// opaque value that binds nothing.
func (m *fleetCredentialMasks) restore(n *nodemodel.Node) {
	if len(m.placeholder) == 0 {
		return
	}
	for _, key := range fleetNodeCredentialKeys {
		if value, ok := n.Fields[key].(string); ok {
			if placeholder, known := m.placeholder[value]; known {
				n.Fields[key] = placeholder
			}
		}
	}
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
