package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// subStoreConfigMergeRequest injects a node list into a client configuration
// template.
//
// This is what makes a "file" more than stored text: an operator keeps one
// Mihomo config they have tuned — rules, DNS, groups — and the proxies are
// filled in from a subscription that changes underneath it. Editing the config
// by hand every time a node moves is the thing this removes.
type subStoreConfigMergeRequest struct {
	// Template is the client configuration, as YAML.
	Template string `json:"template"`
	// Raw is the node content whose proxies get injected. Empty means the
	// template is served unchanged.
	Raw string `json:"raw"`
	// Operators run over the nodes before they are injected.
	Operators []json.RawMessage `json:"operators,omitempty"`
}

type subStoreConfigMergeResult struct {
	Output    string `json:"output"`
	NodeCount int    `json:"node_count"`
}

// subStoreConfigMergeScript parses the template, replaces its `proxies` key
// with the produced nodes, and dumps it back.
//
// It is a real YAML round trip rather than string substitution because a
// template is a document an operator maintains: a marker they must remember to
// leave in place is a trap, and a regex over YAML breaks the first time
// somebody indents differently.
func subStoreConfigMergeScript(req subStoreConfigMergeRequest) (string, error) {
	template, err := json.Marshal(req.Template)
	if err != nil {
		return "", fmt.Errorf("encode template: %w", err)
	}
	raw, err := json.Marshal(req.Raw)
	if err != nil {
		return "", fmt.Errorf("encode raw: %w", err)
	}
	operators, err := json.Marshal(req.Operators)
	if err != nil {
		return "", fmt.Errorf("encode operators: %w", err)
	}

	return fmt.Sprintf(`(async function() {
  // Replacing the proxy list leaves every proxy-group still naming the example
  // nodes the operator wrote their template around. Mihomo refuses to start on a
  // group that references a proxy which no longer exists, so a config that
  // merged cleanly would fail on the client with nothing to point at.
  function repairProxyGroups(config, proxies) {
    const groups = config["proxy-groups"];
    if (!Array.isArray(groups)) return;
    const nodeNames = [];
    for (const proxy of proxies) {
      if (proxy && typeof proxy.name === "string") nodeNames.push(proxy.name);
    }
    const known = new Set(nodeNames);
    for (const group of groups) {
      if (group && typeof group.name === "string") known.add(group.name);
    }
    for (const name of ["DIRECT", "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE", "GLOBAL"]) {
      known.add(name);
    }
    for (const group of groups) {
      if (!group || typeof group !== "object") continue;
      const listed = Array.isArray(group.proxies);
      if (listed) {
        group.proxies = group.proxies.filter(function(entry) {
          return typeof entry === "string" && known.has(entry);
        });
      }
      // A group that builds its own membership — a filter, a provider, Mihomo's
      // include-all — is not empty by mistake and must be left alone.
      const selectsByRule = Boolean(
        group["include-all"] || group["include-all-proxies"] ||
        group["include-all-providers"] || group.filter || group.use
      );
      if (!selectsByRule && (!listed || group.proxies.length === 0)) {
        group.proxies = nodeNames.slice();
      }
    }
  }

  const template = %s;
  const raw = %s;
  const operators = %s || [];
  const root = globalThis.SubStoreProxyUtils;
  const core = root && root.ProxyUtils ? root.ProxyUtils : root;
  if (!core || !core.yaml || typeof core.yaml.safeLoad !== "function" || typeof core.yaml.safeDump !== "function") {
    throw new Error("Sub-Store core must expose yaml.safeLoad and yaml.safeDump");
  }

  let config = core.yaml.safeLoad(template);
  if (config === null || config === undefined) config = {};
  if (typeof config !== "object" || Array.isArray(config)) {
    throw new Error("the configuration template must be a YAML mapping");
  }

  let nodeCount = 0;
  if (typeof raw === "string" && raw.trim() !== "") {
    if (typeof core.parse !== "function" || typeof core.produce !== "function") {
      throw new Error("Sub-Store core must expose parse(raw) and produce(proxies, target, type)");
    }
    let proxies = core.parse(raw);
    if (!Array.isArray(proxies)) {
      throw new Error("Sub-Store parse(raw) must return an array");
    }
    if (operators.length > 0) {
      if (typeof core.process !== "function") {
        throw new Error("Sub-Store core must expose process(proxies, operators)");
      }
      proxies = await core.process(proxies, operators, "ClashMeta", undefined, undefined, raw);
      if (!Array.isArray(proxies)) {
        throw new Error("Sub-Store process(proxies, operators) must return an array");
      }
    }
    // "internal" asks Sub-Store for the proxy objects rather than a serialized
    // document. They come back in exactly the shape a Mihomo config expects, so
    // this file never has to know that shape itself and nothing is re-parsed.
    const list = core.produce(proxies, "ClashMeta", "internal");
    if (!Array.isArray(list)) {
      throw new Error("Sub-Store produce(..., \"internal\") must return an array");
    }
    if (list.length === 0) {
      throw new Error("the node source produced no proxies");
    }
    config.proxies = list;
    nodeCount = list.length;
    repairProxyGroups(config, list);
  }

  const output = core.yaml.safeDump(config);
  if (typeof output !== "string") {
    throw new Error("yaml.safeDump must return a string");
  }
  return JSON.stringify({ output, node_count: nodeCount });
})()`, template, raw, operators), nil
}

func (engine subStoreEngine) mergeConfig(req subStoreConfigMergeRequest) (subStoreConfigMergeResult, error) {
	if strings.TrimSpace(req.Template) == "" {
		return subStoreConfigMergeResult{}, fmt.Errorf("the configuration template is empty")
	}
	script, err := subStoreConfigMergeScript(req)
	if err != nil {
		return subStoreConfigMergeResult{}, err
	}
	out, err := engine.runCoreScript("config merge", "config-merge.js", script)
	if err != nil {
		return subStoreConfigMergeResult{}, err
	}
	var result subStoreConfigMergeResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return subStoreConfigMergeResult{}, fmt.Errorf("decode config merge result: %w", err)
	}
	return result, nil
}
