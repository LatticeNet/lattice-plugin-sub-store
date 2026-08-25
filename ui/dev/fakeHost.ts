import { ref } from "vue";

import { BINDINGS, type MethodBinding } from "../src/client";
import type { HostContext } from "../src/host";

/**
 * A stand-in for the dashboard host, for looking at the UI in a browser.
 *
 * It answers the same wire shapes the plugin returns, so what renders here is
 * what renders in production. The point is to see the screens, not to
 * approximate them. Anything it cannot answer throws, because a mock that
 * silently returns undefined teaches the UI to tolerate nonsense.
 *
 * Never imported by `src/`; the shipped bundle is built from index.html alone.
 */

interface StoredRecord {
  id: string;
  kind?: string;
  name: string;
  display_name?: string;
  remark?: string;
  tags?: string[];
  source?: string;
  vpn_identity?: string;
  entry_roots?: string[];
  graph_options_version?: string;
  url?: string;
  content?: string;
  ua?: string;
  members?: string[];
  member_tags?: string[];
  failure_mode?: string;
  target?: string;
  file_type?: string;
  node_source?: string;
  query_params?: string[];
  arguments?: Record<string, string>;
  process?: unknown[];
  origin?: unknown;
  last_fetch_at?: string;
  last_fetch_ok?: boolean;
  last_error?: string;
  userinfo?: string;
}

const OPERATORS = [
  "Add Proxies From Subscription Operator",
  "Conditional Filter",
  "Flag Operator",
  "Handle Duplicate Operator",
  "Quick Setting Operator",
  "Regex Delete Operator",
  "Regex Filter",
  "Regex Rename Operator",
  "Regex Sort Operator",
  "Region Filter",
  "Remove Duplicate Filter",
  "Resolve Domain Operator",
  "Script Filter",
  "Script Operator",
  "Sort Operator",
  "Type Filter",
  "Useless Filter",
];

const SCRIPTING = new Set(["Script Operator", "Script Filter"]);

/** Shaped like the owner's actual deployment, names included.
 *  The names matter: real records carry CJK ("建材市场") and long hyphenated
 *  ids ("openjobs-host-trojan"), and a row tuned only against "Home nodes"
 *  hides every truncation and line-height bug those produce. The fetch
 *  bookkeeping spans its three states too, one sub with traffic, one whose last
 *  refresh failed, one never fetched, so the row status has something to say. */
const records: StoredRecord[] = [
  {
    id: "cdcd-self-host",
    name: "cdcd-self-host",
    tags: ["home", "self"],
    source: "vpn-core",
    target: "",
    process: [
      { type: "Useless Filter" },
      { type: "Quick Setting Operator", args: { udp: true } },
      { type: "Regex Rename Operator", args: { value: [{ expr: "^HK", now: "香港 Hong Kong" }] } },
    ],
    last_fetch_at: new Date(Date.now() - 3 * 3600 * 1000).toISOString(),
    last_fetch_ok: true,
  },
  {
    id: "openjobs-host",
    name: "openjobs-host",
    tags: ["paid"],
    source: "remote",
    url: "https://example.invalid/subscribe",
    ua: "Surge",
    process: [{ type: "Region Filter", args: { value: ["HK", "JP"], keep: true } }],
    last_fetch_at: new Date(Date.now() - 3 * 3600 * 1000).toISOString(),
    last_fetch_ok: true,
    userinfo: "upload=3221225472; download=25769803776; total=536870912000; expire=1893456000",
  },
  {
    id: "openjobs-host-trojan",
    name: "openjobs-host-trojan",
    tags: ["paid"],
    source: "remote",
    url: "https://example.invalid/broken",
    process: [],
    last_fetch_at: new Date(Date.now() - 26 * 3600 * 1000).toISOString(),
    last_fetch_ok: false,
    last_error: 'subscription "openjobs-host-trojan" provider returned status 503',
    userinfo: "upload=1073741824; download=10737418240; total=107374182400",
  },
  {
    id: "jiancai-shichang",
    name: "建材市场",
    display_name: "建材市场机场节点",
    remark: "备用线路，仅在主线路不可用时启用",
    tags: ["backup", "备用"],
    source: "local",
    content: "vless://11111111-1111-1111-1111-111111111111@a.example:443#node-a",
    process: [],
  },
  {
    id: "a-deliberately-long-record-id-that-has-to-truncate-somewhere",
    name: "A deliberately long subscription name that has to truncate somewhere sensible",
    tags: ["home", "paid", "backup", "self", "备用"],
    source: "local",
    content: "vless://22222222-2222-2222-2222-222222222222@b.example:443#node-b",
    process: [],
  },
  {
    id: "merge-cd-openjobs",
    kind: "collection",
    name: "merge-cd-openjobs",
    tags: ["home"],
    members: ["cdcd-self-host", "openjobs-host"],
    member_tags: ["backup"],
    failure_mode: "strict",
    target: "Clash",
    process: [{ type: "Remove Duplicate Filter" }, { type: "Sort Operator", args: { value: "asc" } }],
  },
  {
    id: "merge-openjobs",
    kind: "collection",
    name: "merge-openjobs",
    tags: ["paid"],
    members: ["openjobs-host", "openjobs-host-trojan"],
    member_tags: [],
    failure_mode: "skip",
    target: "",
    process: [{ type: "Remove Duplicate Filter" }],
  },
  {
    id: "phone-config",
    kind: "file",
    name: "Phone config",
    tags: ["phone"],
    source: "local",
    file_type: "config",
    node_source: "merge-cd-openjobs",
    content: [
      "mixed-port: 7890",
      "mode: rule",
      "proxies: []",
      "proxy-groups:",
      "  - name: PROXY",
      "    type: select",
      "    include-all: true",
      "rules:",
      "  - MATCH,PROXY",
      "",
    ].join("\n"),
    process: [],
  },
  {
    id: "guize-buchong",
    kind: "file",
    name: "规则补充",
    display_name: "规则补充（自用）",
    source: "local",
    file_type: "plain",
    content: "DOMAIN-SUFFIX,example.invalid,DIRECT\n",
    process: [],
  },
  {
    id: "generated-config",
    kind: "file",
    name: "Generated config",
    tags: ["phone"],
    source: "local",
    file_type: "script",
    node_source: "merge-cd-openjobs",
    query_params: ["enhanced-mode"],
    arguments: { "enhanced-mode": "fake-ip" },
    content: [
      "// Builds the whole document from the node source.",
      "const proxies = await produceArtifact({",
      '  type: "collection", name: "merge-cd-openjobs",',
      '  platform: "ClashMeta", produceType: "internal",',
      "});",
      "const mode = ($options && $options[\"enhanced-mode\"]) || $arguments[\"enhanced-mode\"];",
      "$content = ProxyUtils.yaml.safeDump({",
      "  mode: \"rule\",",
      "  dns: { \"enhanced-mode\": mode },",
      "  proxies,",
      "  \"proxy-groups\": [{ name: \"PROXY\", type: \"select\", \"include-all\": true }],",
      "  rules: [\"MATCH,PROXY\"],",
      "});",
      "$options._res = { headers: { \"content-type\": \"text/yaml; charset=utf-8\" } };",
      "",
    ].join("\n"),
    process: [],
  },
];

let settings: Record<string, unknown> = { default_target: "", default_ua: "" };

function listView(rec: StoredRecord) {
  const steps = (rec.process ?? []) as { disabled?: boolean }[];
  return {
    id: rec.id,
    kind: rec.kind || "sub",
    name: rec.name,
    display_name: rec.display_name,
    remark: rec.remark,
    tags: rec.tags,
    source: rec.source,
    has_url: Boolean(rec.url),
    has_inline_content: Boolean(rec.content),
    members: rec.members,
    member_tags: rec.member_tags,
    target: rec.target,
    file_type: rec.file_type,
    node_source: rec.node_source,
    query_params: rec.query_params,
    arguments: rec.arguments,
    step_count: steps.length,
    disabled_step_count: steps.filter((s) => s.disabled).length,
    imported: Boolean(rec.origin),
    // Only once fetched, like the backend: absent reads as "never fetched".
    ...(rec.last_fetch_at
      ? {
          last_fetch_at: rec.last_fetch_at,
          last_fetch_ok: rec.last_fetch_ok ?? false,
          last_error: rec.last_error,
          userinfo: rec.userinfo,
        }
      : {}),
  };
}

const HANDLERS: Record<string, (payload: any) => unknown> = {
  "subscription/list": () => ({ subscriptions: records.map(listView) }),
  "subscription/operators": () => ({
    operators: [
      ...OPERATORS.map((type) => ({ type, scripting: SCRIPTING.has(type) })),
      // The response chain's only step. Flagged the way the plugin flags it, so
      // the harness exercises the same palette split the real host does.
      { type: "Response Transformer", scripting: true, response: true },
    ],
  }),
  "subscription/graph_options": () => ({
    schema_version: 1,
    ok: true,
    options_version: `ov1:${"a".repeat(64)}`,
    identities: [
      { id: "identity-a", label: "Primary", status: "eligible", selectable: true },
      { id: "identity-b", label: "Secondary", status: "eligible", selectable: true },
    ],
    roots: [
      { line_uuid: "11111111-1111-4111-8111-111111111111", label: "Home entry", source_node_id: "node-home", source: "managed", target_label: "Exit", status: "converged", path_summary: "Home entry → Exit (1 hop)", eligible_identity_ids: ["identity-a"], selectable: true },
      { line_uuid: "22222222-2222-4222-8222-222222222222", label: "Secondary entry", source_node_id: "node-edge", source: "managed", status: "converged", path_summary: "Secondary entry", eligible_identity_ids: ["identity-b"], selectable: true },
      { line_uuid: "33333333-3333-4333-8333-333333333333", label: "Drifted entry", source_node_id: "node-drift", source: "managed", status: "drifted", path_summary: "Drifted entry", reason: "graph_drifted", eligible_identity_ids: [], selectable: false },
    ],
  }),
  "subscription/get": ({ subscription_id }) => {
    const found = records.find((r) => r.id === subscription_id);
    if (!found) throw new Error(`subscription "${subscription_id}" was not found`);
    return { subscription: found };
  },
  "subscription/save": ({ subscription }) => {
    const index = records.findIndex((r) => r.id === subscription.id);
    if (index === -1) records.push(subscription);
    else records[index] = subscription;
    return { subscription, saved: true };
  },
  "subscription/delete": ({ subscription_id }) => {
    const index = records.findIndex((r) => r.id === subscription_id);
    if (index === -1) throw new Error(`subscription "${subscription_id}" was not found`);
    records.splice(index, 1);
    return { id: subscription_id, deleted: true };
  },
  /**
   * What the row's Refresh button actually calls. It has to move the record's
   * bookkeeping, because the row reads its status back out of the list: a probe
   * that answered a canned ok left every row saying exactly what it said
   * before, so nothing about refresh could be checked here.
   */
  "subscription/probe": ({ subscription_id }) => {
    const found = records.find((r) => r.id === subscription_id);
    if (!found) throw new Error(`subscription "${subscription_id}" was not found`);
    found.last_fetch_at = new Date().toISOString();
    if (found.id === "openjobs-host-trojan") {
      // A provider that is down stays down. The failure path needs a record
      // that reaches it every time, or it is never seen.
      found.last_fetch_ok = false;
      found.last_error = `subscription "${found.id}" provider returned status 503`;
      return { subscription_id, bytes: 0, stale: true, ok: false, error_code: "provider_status" };
    }
    found.last_fetch_ok = true;
    found.last_error = undefined;
    return { subscription_id, bytes: 4096, stale: false, ok: true };
  },
  "subscription/fetch": ({ subscription_id }) => {
    const found = records.find((r) => r.id === subscription_id);
    if (!found) throw new Error(`subscription "${subscription_id}" was not found`);
    // The harness records the outcome the way the backend does, so a refresh
    // moves the row's status instead of only flashing a banner.
    found.last_fetch_at = new Date().toISOString();
    if (found.id === "openjobs-host-trojan") {
      found.last_fetch_ok = false;
      found.last_error = `subscription "${found.id}" provider returned status 503`;
      throw new Error(found.last_error);
    }
    found.last_fetch_ok = true;
    found.last_error = undefined;
    const raw = "vless://11111111-1111-1111-1111-111111111111@a.example:443#node-a";
    if (found.source === "remote" && !found.userinfo) {
      found.userinfo = "upload=0; download=1073741824; total=536870912000";
    }
    return {
      raw,
      userinfo: found.userinfo ?? "",
      subscription_id,
      bytes: raw.length,
      fetched_at: found.last_fetch_at,
    };
  },
  "subscription/preview": ({ subscription_id, operators }) => {
    const found = records.find((r) => r.id === subscription_id);
    if (found?.kind === "file") {
      // The plugin REFUSES a file preview that would need host work, in this
      // order (system-go previewFileResponse). The harness answered every file
      // with a document, which is why a UI that offered a preview it could not
      // get was never caught here: production returned these sentences and the
      // sheet printed them at the operator.
      if ((found.node_source ?? "").trim()) {
        throw new Error("file preview does not expose node-source content");
      }
      if (found.source === "remote" || (found.url ?? "").trim()) {
        throw new Error("file preview requires a self-contained local document");
      }
      if (found.file_type === "script" || (found.process ?? []).length > 0) {
        throw new Error("file preview requires a self-contained local document");
      }
      // A self-contained document previews as itself.
      return { document: found.content ?? "", node_count: 0, nodes: [] };
    }
    // A cut preview sends fewer operators, so the harness answers with a
    // node count that shrinks per step, otherwise the per-step preview looks
    // identical at every step and the screen cannot be checked at all.
    const steps = Array.isArray(operators) ? operators.length : 3;
    // Eight nodes in, and the chain takes some out. The harness used to list
    // six and claim a source of eight, so the two nodes the count said were
    // removed did not exist and the pane could not be checked against them.
    const all = [
      { name: "🇭🇰 Hong Kong 01", type: "vless", server: "hk-01.edge.example", port: "443", was: "hk-01" },
      { name: "🇭🇰 Hong Kong 02", type: "vless", server: "hk-02.edge.example", port: "8443", was: "hk-02" },
      { name: "🇯🇵 Tokyo 01", type: "trojan", server: "nrt-01.edge.example", port: "443" },
      { name: "🇸🇬 Singapore 01", type: "vmess", server: "sin-01.edge.example", port: "443" },
      { name: "🇺🇸 Portland 01", type: "vless", server: "pdx-01.edge.example", port: "2053" },
      { name: "🇩🇪 Frankfurt 01", type: "trojan", server: "fra-01.edge.example", port: "443" },
      { name: "🇷🇺 Moscow 01", type: "ss", server: "svo-01.edge.example", port: "8388" },
      { name: "🇮🇷 Tehran 01", type: "ss", server: "thr-01.edge.example", port: "8388" },
    ];
    // A cut preview sends fewer operators, so the count shrinks per step:
    // otherwise the per-step preview looks identical at every step and the
    // screen cannot be checked at all.
    const keptCount = Array.isArray(operators) ? Math.max(1, all.length - steps) : all.length;
    // The renaming operator is the first step, so its mark only survives while
    // that step is still in the run.
    const kept = all.slice(0, keptCount).map((node) => (steps > 0 ? node : { ...node, was: undefined }));
    const dropped = all.slice(keptCount);
    return {
      nodes: kept,
      // The real shape: node_count, not count. The UI once read `count`, a
      // field the backend never sent, and printed "undefined node(s)".
      node_count: kept.length,
      source_node_count: all.length,
      dropped,
      dropped_count: dropped.length,
    };
  },
  /**
   * The document a client would actually receive. The sheet's copy action is
   * the only path that produces a whole configuration rather than a node
   * summary, so the harness has to answer it or that action can only ever be
   * checked in production.
   */
  "subscription/render": ({ subscription_id, target, ua_class, options }) => {
    const found = records.find((r) => r.id === subscription_id);
    if (found?.id === "openjobs-host-trojan") {
      throw new Error(`subscription "${found.id}" provider returned status 503`);
    }
    if (found?.kind === "file") {
      // renderFile ignores the target and the produce options: a file is served
      // as the document it is. A harness that varied the output by target would
      // let a sheet offering fourteen of them look correct.
      const injected = (found.content ?? "").replace(
        "proxies: []",
        "proxies:\n  - {name: 🇭🇰 Hong Kong 01, type: vless, server: a.example, port: 443}",
      );
      return {
        content: injected,
        content_type:
          found.file_type === "config" ? "text/yaml; charset=utf-8" : "text/plain; charset=utf-8",
      };
    }
    // Explicit target wins, mirroring resolveRenderTarget in system-go.
    const client = String(target || ua_class || "URI");
    const flags = (options ?? {}) as Record<string, boolean>;
    const includeUnsupported = flags["include-unsupported-proxy"] === true;
    const yamlTargets = new Set(["Stash", "Clash", "ClashMeta", "Egern"]);
    const jsonTargets = new Set(["sing-box", "JSON"]);
    const confTargets = new Set([
      "Surfboard",
      "Surge",
      "SurgeMac",
      "Loon",
      "Shadowrocket",
      "QX",
    ]);
    const content = jsonTargets.has(client)
      ? JSON.stringify({
          target: client,
          ...(includeUnsupported ? { includeUnsupportedProxy: true } : {}),
          proxies: [{ name: "🇭🇰 Hong Kong 01", type: "vless", server: "a.example", port: 443 }],
        }, null, 2)
      : yamlTargets.has(client)
        ? `# ${found?.name ?? subscription_id} rendered for ${client}\n${includeUnsupported ? "# include-unsupported-proxy: on\n" : ""}proxies:\n  - {name: 🇭🇰 Hong Kong 01, type: vless, server: a.example, port: 443}\n`
        : confTargets.has(client)
          ? `# ${found?.name ?? subscription_id} rendered for ${client}\n${includeUnsupported ? "# include-unsupported-proxy: on\n" : ""}Hong Kong 01 = vless, a.example, 443, udp=true\n`
          : `# ${found?.name ?? subscription_id} rendered for ${client}\n${includeUnsupported ? "# include-unsupported-proxy: on\n" : ""}vless://canned@a.example:443#%F0%9F%87%AD%F0%9F%87%B0%20Hong%20Kong%2001\n`;
    // Clash carries neither VLESS nor Hysteria2, so a fleet of them renders for
    // it as an all but empty document. The harness reproduces that, because a
    // sheet that never sees it looks correct while the real one is unreadable.
    const clashRefusesEverything = client === "Clash" && !includeUnsupported;
    return {
      content: clashRefusesEverything ? "proxies:\n" : content,
      content_type: jsonTargets.has(client)
        ? "application/json; charset=utf-8"
        : yamlTargets.has(client)
          ? "text/yaml; charset=utf-8"
          : "text/plain; charset=utf-8",
      node_count: 4,
      dropped_node_count: clashRefusesEverything ? 4 : 0,
      ...(clashRefusesEverything ? { dropped_protocols: ["hysteria2", "vless"] } : {}),
    };
  },
  /**
   * Core-backed shares bridge. One subscription and one file are published, so
   * every sheet state is reachable: a client-pinned link, a file's single
   * link, and the "no published share" note on everything else. The
   * subscription's slug matches the owner's deployment.
   */
  "shares/list": () => ({
    shares: [
      {
        subscription_id: records[0]?.id ?? "sub-1",
        share_id: "sh-dev",
        slug: "cd-self",
        enabled: true,
        default_format: "plain",
        path: "/sub/cd-self/devtokendevtokendevtokendevtoken",
        url: "https://lattice.example/sub/cd-self/devtokendevtokendevtokendevtoken",
      },
      {
        // A published FILE too. Without one the file sheet's link branch is
        // unreachable here, and that branch is the one that must NOT pin a
        // client onto the URL: the serve path ignores ?target= for a file.
        subscription_id: "phone-config",
        share_id: "sh-dev-file",
        slug: "phone",
        enabled: true,
        default_format: "plain",
        path: "/sub/phone/filetokenfiletokenfiletokenfiletok",
        url: "https://lattice.example/sub/phone/filetokenfiletokenfiletokenfiletok",
      },
    ],
  }),
  /**
   * Publish pushes the rendered document at a destination the operator names.
   * The manifest declares it, so the row menu offers it; the harness had no
   * answer, so every publish here died on "the dev harness has no answer for
   * subscription/publish" and the drawer could never be checked.
   */
  "subscription/publish": ({ subscription_id, destination }) => {
    const found = records.find((r) => r.id === subscription_id);
    if (!found) throw new Error(`subscription "${subscription_id}" was not found`);
    const target = String(destination ?? "");
    if (!target) throw new Error("publish needs a destination");
    // A destination that is not reachable is the normal failure, and the
    // message quotes the URL, which is exactly what the redactor exists for.
    if (target.includes("example.invalid")) {
      throw new Error(`publish to ${target} failed: connection refused`);
    }
    return { subscription_id, bytes: 2048, status_code: 200 };
  },
  "subscription/get_settings": () => settings,
  "subscription/save_settings": (payload) => {
    settings = { ...settings, ...payload };
    return settings;
  },
  "subscription/export": () => ({ backup: JSON.stringify({ version: 1, records }, null, 2) }),
  "subscription/import": () => ({ imported: records.map((r) => r.id) }),
  "subscription/migrate": () => ({ imported: ["migrated-one"], skipped: { "migrated-two": "no nodes" } }),
};

/** Slight latency so loading states are visible rather than theoretical. */
function delay<T>(value: T): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(value), 180));
}

export function createFakeHost(): HostContext {
  const init = ref<any>();
  const bootError = ref("");

  // The handshake lands late on purpose: a screen that loads before it and
  // never retries is the bug this harness exists to make visible.
  setTimeout(() => {
    init.value = {
      version: "1",
      pluginId: "latticenet.sub-store",
      route: "sub-store",
      interfaces: [
        {
          service: "latticenet.sub-store/subscription",
          // Exactly what manifest.json declares for this service. It was
          // missing "probe", which is what the row's Refresh button asks for,
          // so every Refresh in the harness rendered disabled and the refresh
          // path, its notice and its failure state were undrivable here.
          methods: [
            "fetch", "probe", "render", "operators", "graph_options", "preview", "list", "get", "save",
            "delete", "migrate", "export", "import", "get_settings", "save_settings", "publish",
          ],
        },
        {
          service: "latticenet.sub-store/engine",
          methods: [
            "convert", "transform_response", "save_pipeline", "get_pipeline",
            "list_pipelines", "delete_pipeline", "run_pipeline",
          ],
        },
        {
          service: "latticenet.sub-store/shares",
          methods: ["list"],
        },
      ],
    };
  }, 400);

  const bridge = {
    call<T>(service: string, method: string, payload: unknown) {
      const key = `${service.split("/").pop()}/${method}`;
      const handler = HANDLERS[key];
      const promise = handler
        ? delay(handler((payload ?? {}) as any) as T)
        : Promise.reject(new Error(`the dev harness has no answer for ${key}`));
      return { promise, cancel: () => {} };
    },
    resize() {},
    dispose() {},
    init: Promise.resolve(undefined),
  } as unknown as HostContext["bridge"];

  return {
    bridge,
    init,
    bootError,
    available: (target: MethodBinding) =>
      init.value?.interfaces.some(
        (contract: any) =>
          contract.service === target.service && contract.methods.includes(target.method),
      ) === true,
    resize: async () => {},
  };
}

export { BINDINGS };
