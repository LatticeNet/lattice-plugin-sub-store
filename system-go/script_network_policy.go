package main

import (
	"encoding/json"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// Which methods may run the engine's JavaScript with the host egress broker
// attached.
//
// This process never learns the caller's scopes. There is no scope field on the
// SDK request and nothing here reads one, so the signed manifest's per-method
// scope is the only bound that exists anywhere in the chain. A bound that only
// exists in the manifest can only be honoured here by method identity, which is
// what this table is: the set of methods whose declared scope is broad enough to
// cover reaching the network.
//
// It is deliberately small, and it is not the set of methods that CAN run
// JavaScript. Preview, convert, transform_response and run_pipeline all still
// run an operator chain; they simply run it with no network. Their scripts get
// the same refusal a script already gets when it outlives its invocation
// ("the network is not available for this call"), which the shim converts into
// an ordinary callback error rather than a crash.
//
// Gating on the operator TYPE instead would not work. "Script Operator" is not
// the only operator that reaches the network: "Resolve Domain Operator" with
// provider "Custom" takes a resolver URL in its own arguments and fetches it,
// and it is not a scripting operator. The chain as a whole is caller-authored,
// network-capable configuration, so the grant has to sit above the chain.
//
// TestScriptNetworkGrantsAreAdminScopedInTheManifest reads manifest.json and
// fails if an entry here is not declared substore:admin, so the table cannot
// drift away from the document that actually enforces it.
var scriptNetworkMethods = map[string]bool{
	// Serving a subscription is the one path whose whole job is to produce the
	// document a client receives, so its chain runs for real: remote rulesets,
	// Resolve Domain, produceArtifact downloads.
	pluginID + "/subscription.render": true,
	// publish renders the same document and ships it to an operator target.
	pluginID + "/subscription.publish": true,
}

// callTarget names the "service.method" an invocation is aimed at.
//
// It reads only the routing fields and never copies the payload: a 4 MiB import
// body should not be duplicated to answer a policy question. The false return
// covers describe, health and plan, none of which run a caller's chain.
func callTarget(req request) (string, bool) {
	if req.Action != latticeplugin.ActionCall {
		return "", false
	}
	if req.Service != "" || req.Method != "" {
		return req.Service + "." + req.Method, true
	}
	var call struct {
		Service string `json:"service"`
		Method  string `json:"method"`
	}
	if len(req.Payload) == 0 || json.Unmarshal(req.Payload, &call) != nil {
		return "", false
	}
	return call.Service + "." + call.Method, true
}

// scriptNetworkAllowed reports whether this invocation's JavaScript may reach
// the host egress broker. It fails closed: an unroutable or unrecognised
// request gets no network.
func scriptNetworkAllowed(req request) bool {
	target, ok := callTarget(req)
	if !ok {
		return false
	}
	return scriptNetworkMethods[target]
}
