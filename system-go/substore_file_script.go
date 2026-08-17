package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The runtime a generator script expects.
//
// The embedded Sub-Store bundle exposes exactly one thing: `ProxyUtils`.
// `produceArtifact`, `$content`, `$options` and `$arguments` are not part of it
// — upstream injects them around the script from its own backend. A script
// written against them therefore cannot run here at all until this file builds
// the same surface, which is why porting one was impossible rather than merely
// awkward.
//
// Everything the script can reach is pre-resolved before the engine starts. No
// host call happens while JavaScript is running, which is what makes this fit a
// fork-per-call runtime with a fixed budget: the plugin resolves the node source
// on the Go side, hands the bytes in, and the script sees a plain function.

// fileScriptMember is one subscription's nodes, carrying the name they were
// gathered under.
//
// The name is load-bearing rather than decorative. Upstream tags every proxy
// with `_subName` after producing a collection, and real scripts *filter* on it
// — `ALLOWED_SUB_NAMES.has(proxy._subName)`. Merging the members into one blob
// first and parsing once would drop the tag, and a script that filters on it
// would then keep nothing and emit a configuration with no nodes at all.
type fileScriptMember struct {
	SubName string `json:"sub_name"`
	Raw     string `json:"raw"`
}

// fileScriptArtifact is what one `produceArtifact({name})` call resolves to.
type fileScriptArtifact struct {
	Name    string             `json:"name"`
	Kind    string             `json:"kind"`
	Members []fileScriptMember `json:"members"`
}

type fileScriptRequest struct {
	Script    string               `json:"script"`
	Artifacts []fileScriptArtifact `json:"artifacts"`
	Arguments map[string]string    `json:"arguments"`
	Query     map[string]string    `json:"query"`
}

type fileScriptResult struct {
	Content string            `json:"content"`
	Headers map[string]string `json:"headers"`
}

// fileScriptRuntimeJS is the preamble. It closes over the injected values and
// defines the globals a script reaches for.
const fileScriptRuntimeJS = `
  const root = globalThis.SubStoreProxyUtils;
  const ProxyUtils = root && root.ProxyUtils ? root.ProxyUtils : root;
  if (!ProxyUtils || typeof ProxyUtils.parse !== "function" || typeof ProxyUtils.produce !== "function") {
    throw new Error("Sub-Store core must expose parse and produce");
  }
  globalThis.ProxyUtils = ProxyUtils;

  var $content = "";
  var $arguments = __ARGUMENTS;
  var $options = { _req: { query: __QUERY, method: "GET" }, _res: { headers: {} } };
  // Upstream merges ?$options=a%3Db into the top level, and scripts read both
  // spellings. Doing the same here means a ported script does not have to know
  // which one it landed on.
  for (const key of Object.keys(__QUERY)) {
    if (key !== "$options") $options[key] = __QUERY[key];
  }
  globalThis.$arguments = $arguments;
  globalThis.$options = $options;

  const __ARTIFACT_BY_NAME = {};
  for (const artifact of __ARTIFACTS) {
    __ARTIFACT_BY_NAME[artifact.name] = artifact;
  }

  async function produceArtifact(opts) {
    opts = opts || {};
    const name = String(opts.name == null ? "" : opts.name);
    const artifact = __ARTIFACT_BY_NAME[name];
    if (!artifact) {
      const known = Object.keys(__ARTIFACT_BY_NAME);
      throw new Error(
        "produceArtifact(" + JSON.stringify(name) + "): this file does not declare that source. " +
        (known.length
          ? "It declares " + known.map((k) => JSON.stringify(k)).join(", ") + "."
          : "It declares no node source — set one on the file.")
      );
    }
    // Parsed per member so each proxy keeps the name it was gathered under.
    // A single parse over concatenated text cannot know which member a node
    // came from, and scripts filter on exactly that.
    const proxies = [];
    for (const member of artifact.members) {
      if (!member.raw || !member.raw.trim()) continue;
      for (const proxy of ProxyUtils.parse(member.raw)) {
        if (!proxy) continue;
        if (member.sub_name) proxy._subName = member.sub_name;
        proxies.push(proxy);
      }
    }
    const platform = opts.platform || "ClashMeta";
    const produceType = opts.produceType || opts.produce_type;
    if (produceType === "internal") {
      return ProxyUtils.produce(proxies, platform, "internal", opts.produceOpts);
    }
    return ProxyUtils.produce(proxies, platform, undefined, opts.produceOpts);
  }
  globalThis.produceArtifact = produceArtifact;
`

// fileScriptSource assembles the program the engine runs.
//
// The operator's script goes in verbatim, inside the same async function as the
// preamble so its top-level `await` works and its `$content = …` assignment
// lands on the declared variable rather than throwing in strict mode.
func fileScriptSource(req fileScriptRequest) (string, error) {
	artifacts, err := json.Marshal(req.Artifacts)
	if err != nil {
		return "", fmt.Errorf("encode artifacts: %w", err)
	}
	arguments, err := json.Marshal(orEmptyStringMap(req.Arguments))
	if err != nil {
		return "", fmt.Errorf("encode arguments: %w", err)
	}
	query, err := json.Marshal(orEmptyStringMap(req.Query))
	if err != nil {
		return "", fmt.Errorf("encode query: %w", err)
	}

	preamble := fileScriptRuntimeJS
	preamble = strings.ReplaceAll(preamble, "__ARTIFACTS", string(artifacts))
	preamble = strings.ReplaceAll(preamble, "__ARGUMENTS", string(arguments))
	preamble = strings.ReplaceAll(preamble, "__QUERY", string(query))

	var b strings.Builder
	b.WriteString("(async function() {\n")
	b.WriteString(preamble)
	b.WriteString("\n// ---- operator script ----\n")
	b.WriteString(req.Script)
	b.WriteString("\n// ---- end operator script ----\n")
	b.WriteString(`
  if (typeof $content !== "string") {
    if ($content == null) {
      throw new Error("the script finished without assigning $content");
    }
    $content = String($content);
  }
  const headers = {};
  const declared = $options && $options._res && $options._res.headers;
  if (declared && typeof declared === "object") {
    for (const key of Object.keys(declared)) {
      const value = declared[key];
      if (value != null) headers[String(key)] = String(value);
    }
  }
  return JSON.stringify({ content: $content, headers });
})()`)
	return b.String(), nil
}

func orEmptyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	return in
}

func (engine *subStoreEngine) runFileScript(req fileScriptRequest) (fileScriptResult, error) {
	if strings.TrimSpace(req.Script) == "" {
		return fileScriptResult{}, fmt.Errorf("the file has no script")
	}
	source, err := fileScriptSource(req)
	if err != nil {
		return fileScriptResult{}, err
	}
	// A script file is user JavaScript; it never touches the warm runtime.
	out, err := engine.runIsolatedScript("file script", "file-script.js", source)
	if err != nil {
		return fileScriptResult{}, err
	}
	var result fileScriptResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return fileScriptResult{}, fmt.Errorf("decode file script result: %w", err)
	}
	return result, nil
}
