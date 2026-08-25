package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// nodeSummary is one node reduced to what an operator needs to judge a pipeline.
//
// The line drawn here is credentials, not endpoints: name, type, server, port
// and the transport flags answer "did my filter keep the right nodes, and are
// they the shape I expect" — the question a preview exists for. What still
// never crosses the boundary is anything that would let the preview double as
// a credential dump: no password, uuid, key or SNI. The operator asked for the
// upstream Sub-Store preview's level of detail (2026-08-11); this matches its
// indicator set (udp / tfo / skip-cert-verify / aead) without its node-detail
// modal, which shows secrets.
type nodeSummary struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Server   string `json:"server,omitempty"`
	Port     string `json:"port,omitempty"`
	Network  string `json:"network,omitempty"`
	Security string `json:"security,omitempty"`
	// The four capability flags upstream surfaces as indicators. omitempty keeps
	// a flag that was never set indistinguishable from one explicitly off —
	// either way there is nothing to show.
	UDP            bool `json:"udp,omitempty"`
	TFO            bool `json:"tfo,omitempty"`
	SkipCertVerify bool `json:"skip_cert_verify,omitempty"`
	AEAD           bool `json:"aead,omitempty"`
	// Was carries the name this node had before the chain ran, and only when
	// the chain changed it. A rename is the one edit a preview cannot show by
	// listing the result alone: the new name looks like it was always there.
	Was string `json:"was,omitempty"`
}

type previewResult struct {
	SourceNodeCount int           `json:"source_node_count"`
	NodeCount       int           `json:"node_count"`
	Nodes           []nodeSummary `json:"nodes"`
	Truncated       bool          `json:"truncated"`
	// Dropped is the source nodes the chain removed, and DroppedCount is how
	// many there were before this list was capped. A count alone says a filter
	// bit; the list says which nodes it bit, which is the question an operator
	// tuning a filter is actually asking.
	Dropped          []nodeSummary `json:"dropped,omitempty"`
	DroppedCount     int           `json:"dropped_count"`
	DroppedTruncated bool          `json:"dropped_truncated,omitempty"`
	SourceVersion   string        `json:"source_version,omitempty"`
	Stale           bool          `json:"stale"`
	// Document is set instead of Nodes when the record is a file. A file is a
	// document, so the question its preview answers is "what will a client
	// receive", not "which nodes survived the filter".
	Document string `json:"document,omitempty"`
}

// maxPreviewDocumentBytes bounds what a file preview returns. The reply travels
// through a budgeted stdout pipe, and a config long enough to overrun it is
// still readable from its first pages.
const maxPreviewDocumentBytes = 64 << 10

// maxPreviewNodes bounds what a preview returns. A 5000-node subscription does
// not need 5000 rows to show whether a pipeline works, and the reply travels
// through a budgeted stdout pipe.
const maxPreviewNodes = 200

// previewSubscription applies a pipeline and reports the nodes it produces
// without producing a client config. It is how an operator sees the effect of a
// filter before saving it.
//
// credentialsAlreadySynthetic says the caller has already replaced this
// content's secrets with stand-ins before it ever reached the engine, which is
// what the vpn-core graph path does: it composes the entries itself and
// substitutes a synthetic credential of the right shape, so a script still sees
// a well-formed node and the real secret never enters the process. When that has
// happened there is nothing left to withhold, and reducing again would only cost
// the script the field it is entitled to read.
//
// Every other source is arbitrary upstream content whose secret-bearing fields
// this process cannot enumerate, so those get the reduction.
func (rt *runtime) previewSubscription(raw string, operators []json.RawMessage, target string, credentialsAlreadySynthetic bool) (previewResult, error) {
	if strings.TrimSpace(raw) == "" {
		return previewResult{}, fmt.Errorf("preview needs subscription content")
	}
	if err := validateOperators(operators); err != nil {
		return previewResult{}, err
	}
	if strings.TrimSpace(target) == "" {
		target = "URI"
	}

	engine := rt.subStoreEngine()
	run := engine.runCoreScript
	if containsScriptingOperator(operators) {
		// User JavaScript never touches the warm runtime.
		run = engine.runIsolatedScript
	}
	out, err := run("preview", "preview.js", previewScript(raw, operators, target, !credentialsAlreadySynthetic))
	if err != nil {
		return previewResult{}, err
	}
	var decoded struct {
		SourceNodeCount int           `json:"source_node_count"`
		Nodes           []nodeSummary `json:"nodes"`
		Dropped         []nodeSummary `json:"dropped"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		return previewResult{}, fmt.Errorf("decode preview result: %w", err)
	}
	result := previewResult{
		SourceNodeCount: decoded.SourceNodeCount,
		NodeCount:       len(decoded.Nodes),
		Nodes:           decoded.Nodes,
		Dropped:         decoded.Dropped,
		// Counted before the cap below, so a subscription too long to list is
		// still told the truth about how many nodes it lost.
		DroppedCount: len(decoded.Dropped),
	}
	if len(result.Nodes) > maxPreviewNodes {
		result.Nodes = result.Nodes[:maxPreviewNodes]
		result.Truncated = true
	}
	if len(result.Dropped) > maxPreviewNodes {
		result.Dropped = result.Dropped[:maxPreviewNodes]
		result.DroppedTruncated = true
	}
	return result, nil
}

// previewScript reduces each node to its summary inside the engine, so the
// fields a preview must not expose never cross the process boundary at all.
//
// The reduction happens BEFORE the operator chain, not only after it. Reducing
// only on the way out protected the shape of the reply while leaving the chain
// itself holding the full node: a caller-supplied "Script Operator" could read
// the password off a proxy and assign it to `name`, which the summary does
// return, and the redaction was bypassed without ever being touched. Running
// the chain over already-reduced nodes makes that impossible to express, and it
// holds for operators nobody has written yet rather than for the two that ship
// today.
//
// The trade is fidelity: an operator that branches on a field outside this set
// sees it as absent here and may keep or drop a node the served render would
// not. A preview answers "which nodes survive my filter", and answering it
// without handing the caller the credentials is worth more than matching the
// render byte for byte. The render itself is unchanged and still runs the chain
// over whole nodes.
func previewScript(raw string, operators []json.RawMessage, target string, reduceBeforeOperators bool) string {
	rawJSON, _ := json.Marshal(raw)
	opsJSON, _ := json.Marshal(operators)
	targetJSON, _ := json.Marshal(target)
	return fmt.Sprintf(`(async function() {
  const raw = %s;
  const operators = %s || [];
  const target = %s;
  const root = globalThis.SubStoreProxyUtils;
  const core = root && root.ProxyUtils ? root.ProxyUtils : root;
  let proxies = core.parse(raw);
  if (!Array.isArray(proxies)) { throw new Error("parse(raw) must return an array"); }
  const sourceNodeCount = proxies.length;

  // An allowlist, not a denylist of secret-looking names: a protocol added
  // upstream tomorrow brings its own credential field, and a denylist would
  // not know to exclude it. Everything here is either returned by the summary
  // or read to compute it, plus _subName, which is how a real chain filters by
  // the subscription a node came from and which carries nothing secret.
  const VISIBLE = [
    "name", "type", "server", "port", "network",
    "security", "tls", "reality-opts",
    "udp", "tfo", "skip-cert-verify", "aead",
    "_subName"
  ];
  const reduce = function (p) {
    const out = {};
    if (!p || typeof p !== "object") return out;
    for (const key of VISIBLE) {
      if (p[key] !== undefined) out[key] = p[key];
    }
    return out;
  };
  if (%t) { proxies = proxies.map(reduce); }

  const text = function (v) { return v == null ? "" : String(v); };
  const summarize = function (p) {
    p = p || {};
    return {
      name: text(p.name),
      type: text(p.type),
      server: text(p.server),
      port: text(p.port),
      network: text(p.network),
      security: text(p.security != null ? p.security : (p["reality-opts"] != null ? "reality" : (p.tls === true ? "tls" : ""))),
      udp: p.udp === true,
      tfo: p.tfo === true,
      skip_cert_verify: p["skip-cert-verify"] === true,
      aead: p.aead === true
    };
  };
  // Snapshot the input before the chain runs. Operators edit proxies in place,
  // so a snapshot taken afterwards is the output wearing the input's name.
  // These are fresh objects holding copied primitives, so nothing downstream
  // can reach back and change them.
  const sourceNodes = proxies.map(summarize);

  if (operators.length > 0) {
    proxies = await core.process(proxies, operators, target, undefined, undefined, raw);
    if (!Array.isArray(proxies)) { throw new Error("process(...) must return an array"); }
  }
  const nodes = proxies.map(summarize);

  // Pair every produced node back to the source node it came from, so the
  // reply can say what the chain removed and what it renamed rather than only
  // how many survived. The endpoint is the identity: a rename changes the name
  // and leaves type/server/port alone, which is exactly what makes the pairing
  // possible.
  const keyOf = function (n) {
    return text(n.type) + "|" + text(n.server).toLowerCase() + "|" + text(n.port);
  };
  const byKey = new Map();
  for (let i = 0; i < sourceNodes.length; i++) {
    const key = keyOf(sourceNodes[i]);
    if (!byKey.has(key)) { byKey.set(key, []); }
    byKey.get(key).push(i);
  }
  const claimed = sourceNodes.map(function () { return false; });
  // Two passes, because one endpoint can carry several nodes. Exact name
  // matches are claimed first: pairing a node the chain left alone against a
  // renamed sibling would report a rename that never happened.
  const unpaired = [];
  for (const node of nodes) {
    const bucket = byKey.get(keyOf(node)) || [];
    let hit = -1;
    for (const i of bucket) {
      if (!claimed[i] && sourceNodes[i].name === node.name) { hit = i; break; }
    }
    if (hit < 0) { unpaired.push(node); continue; }
    claimed[hit] = true;
  }
  for (const node of unpaired) {
    const bucket = byKey.get(keyOf(node)) || [];
    for (const i of bucket) {
      if (claimed[i]) { continue; }
      claimed[i] = true;
      if (sourceNodes[i].name !== node.name) { node.was = sourceNodes[i].name; }
      break;
    }
    // No leftover at this endpoint means the chain produced a node the input
    // did not have. Saying nothing is right: there is no earlier name to show.
  }
  const dropped = [];
  for (let i = 0; i < sourceNodes.length; i++) {
    if (!claimed[i]) { dropped.push(sourceNodes[i]); }
  }
  return JSON.stringify({ source_node_count: sourceNodeCount, nodes: nodes, dropped: dropped });
})()`, rawJSON, opsJSON, targetJSON, reduceBeforeOperators)
}
