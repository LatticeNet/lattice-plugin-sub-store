// A stand-in for a real Mihomo generator.
//
// It is deliberately shaped like the ones operators actually run rather than
// like a minimal example, because the point of the fixture is the runtime
// surface: top-level await, produceArtifact with produceType "internal",
// filtering and renaming by _subName, reading both $options query and
// $arguments, dumping YAML through ProxyUtils, assigning $content, and setting
// response headers. A test that exercised fewer of those would pass while a
// ported script still failed.

const SOURCE_TYPE = "collection";
const SOURCE_NAME = "all-nodes";
const TARGET_PLATFORM = "ClashMeta";

// Only these subscriptions contribute nodes, and each gets its own prefix.
// This is the part that breaks if provenance is lost in the merge: the filter
// keeps nothing and the config comes out with no proxies at all.
const SUB_PREFIX_MAP = {
  "home-nodes": "[home]-",
  "office-nodes": "[office]-",
};
const ALLOWED_SUB_NAMES = new Set(Object.keys(SUB_PREFIX_MAP));

const ENHANCED_MODE_DEFAULT = "fake-ip";
const ENHANCED_MODE = (() => {
  try {
    const opt = (typeof $options !== "undefined" && $options) || {};
    const fromQuery = opt && opt._req && opt._req.query && opt._req.query["enhanced-mode"];
    const fromOpt = opt && opt["enhanced-mode"];
    const fromArg =
      (typeof $arguments !== "undefined" && $arguments && $arguments["enhanced-mode"]) || "";
    const v = String(fromQuery ?? fromOpt ?? fromArg ?? "").toLowerCase().trim();
    if (v === "redir-host") return "redir-host";
    if (v === "fake-ip") return "fake-ip";
    if (v === "normal") return "normal";
  } catch (_) {}
  return ENHANCED_MODE_DEFAULT;
})();

async function loadSourceProxies() {
  let result = await produceArtifact({
    type: SOURCE_TYPE,
    name: SOURCE_NAME,
    platform: TARGET_PLATFORM,
    produceType: "internal",
  });
  if (!Array.isArray(result)) {
    const parsed = ProxyUtils.yaml.safeLoad(result);
    result = (parsed && parsed.proxies) || [];
  }
  const total = result.length;
  result = result.filter((proxy) => proxy && proxy.name && ALLOWED_SUB_NAMES.has(proxy._subName));
  console.log(`[source-filter] kept=${result.length} dropped=${total - result.length}`);
  return result.map((proxy) => {
    const prefix = SUB_PREFIX_MAP[proxy._subName];
    if (prefix && !proxy.name.startsWith(prefix)) proxy.name = prefix + proxy.name;
    return proxy;
  });
}

async function buildConfig() {
  const proxies = await loadSourceProxies();
  if (proxies.length === 0) {
    throw new Error("the node source produced nothing the filter kept");
  }
  return {
    mode: "rule",
    "unified-delay": true,
    dns: {
      enable: true,
      "enhanced-mode": ENHANCED_MODE,
      nameserver: ["https://example.invalid/dns-query"],
    },
    proxies,
    "proxy-groups": [
      { name: "PROXY", type: "select", "include-all": true },
      { name: "AUTO", type: "url-test", "include-all": true },
    ],
    rules: ["GEOSITE,cn,DIRECT", "MATCH,PROXY"],
  };
}

$content = ProxyUtils.yaml.safeDump(await buildConfig());

if ($options) {
  $options._res = {
    headers: {
      "profile-update-interval": "24",
      "content-type": "text/yaml; charset=utf-8",
    },
  };
}
