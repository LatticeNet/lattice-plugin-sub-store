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
}

type previewResult struct {
	SourceNodeCount int           `json:"source_node_count"`
	NodeCount       int           `json:"node_count"`
	Nodes           []nodeSummary `json:"nodes"`
	Truncated       bool          `json:"truncated"`
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

// previewScript reduces each node to its summary inside the engine, so the
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
  const text = function (v) { return v == null ? "" : String(v); };
  const nodes = proxies.map(function (p) {
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
  });
  return JSON.stringify({ source_node_count: sourceNodeCount, nodes: nodes });
})()`, rawJSON, opsJSON, targetJSON)
}
