package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// nodeSummary is one node reduced to what an operator needs to judge a pipeline.
//
// It deliberately carries no address, port, password, uuid or any other field
// that would let the preview double as a credential dump. A preview exists to
// answer "did my filter keep the right nodes", and name plus type answers that.
type nodeSummary struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type previewResult struct {
	SourceNodeCount int           `json:"source_node_count"`
	NodeCount       int           `json:"node_count"`
	Nodes           []nodeSummary `json:"nodes"`
	Truncated       bool          `json:"truncated"`
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
func (rt *runtime) previewSubscription(raw string, operators []json.RawMessage, target string) (previewResult, error) {
	if strings.TrimSpace(raw) == "" {
		return previewResult{}, fmt.Errorf("preview needs subscription content")
	}
	if err := validateOperators(operators); err != nil {
		return previewResult{}, err
	}
	if strings.TrimSpace(target) == "" {
		target = "URI"
	}

	out, err := rt.subStoreEngine().runCoreScript("preview", "preview.js", previewScript(raw, operators, target))
	if err != nil {
		return previewResult{}, err
	}
	var decoded struct {
		SourceNodeCount int           `json:"source_node_count"`
		Nodes           []nodeSummary `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		return previewResult{}, fmt.Errorf("decode preview result: %w", err)
	}
	result := previewResult{
		SourceNodeCount: decoded.SourceNodeCount,
		NodeCount:       len(decoded.Nodes),
		Nodes:           decoded.Nodes,
	}
	if len(result.Nodes) > maxPreviewNodes {
		result.Nodes = result.Nodes[:maxPreviewNodes]
		result.Truncated = true
	}
	return result, nil
}

// previewScript reduces each node to name and type inside the engine, so the
// fields a preview must not expose never cross the process boundary at all.
func previewScript(raw string, operators []json.RawMessage, target string) string {
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
  if (operators.length > 0) {
    proxies = await core.process(proxies, operators, target, undefined, undefined, raw);
    if (!Array.isArray(proxies)) { throw new Error("process(...) must return an array"); }
  }
  const nodes = proxies.map(function (p) {
    return { name: String(p && p.name != null ? p.name : ""), type: String(p && p.type != null ? p.type : "") };
  });
  return JSON.stringify({ source_node_count: sourceNodeCount, nodes: nodes });
})()`, rawJSON, opsJSON, targetJSON)
}
