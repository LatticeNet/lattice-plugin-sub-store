import { ref } from "vue";

import { BINDINGS, type MethodBinding } from "../src/client";
import type { HostContext } from "../src/host";
import type { PageState } from "../src/pageState";
import { addressForState, pageStateFromAddress, stateRateLimit } from "./consoleAddress";
import { regexDiagnostics } from "../src/regexRewrite";
import { parseUserinfo } from "../src/rowStatus";
import { fixture, fixtureName, type Fixture, type ShareRow, type StoredRecord } from "./fixtures";

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

/**
 * Whether an operation can change how many nodes there are. In Sub-Store the
 * filters decide which nodes stay; every operator (rename, regex delete, sort,
 * flags, quick settings) rewrites the nodes it is given and hands all of them
 * on. The harness used to take nodes away at every step, so the delta strip
 * read "Regex rename: kept 72 of 119", which the real engine never says.
 */
function removesNodes(step: unknown): boolean {
  const type = (step as { type?: unknown } | null)?.type;
  return typeof type === "string" && type.endsWith("Filter");
}

/**
 * How many of `source` nodes are left after the operations in `ran`, when the
 * record's whole chain turns `source` into `result`. The removal is shared
 * among the chain's filters in order; every other operation keeps its count.
 */
function countAfter(chain: readonly unknown[], ran: readonly unknown[], source: number, result: number): number {
  const filters = chain.filter(removesNodes).length;
  if (filters === 0) return source;
  const done = Math.min(filters, ran.filter(removesNodes).length);
  return source - Math.round(((source - result) * done) / filters);
}

/**
 * 建材市场 in production is 166 in, 25 out after Regex filter. The generic
 * eight-node harness cannot exercise paging or the grouped drop list.
 */
function jiancaiPreview(keptCount: number) {
  const regions = ["亚洲", "欧洲", "美洲", "非洲", "澳洲", "香港", "日本", "新加坡"];
  const all = Array.from({ length: 166 }, (_, i) => {
    const region = regions[i % regions.length]!;
    const n = String(i + 1).padStart(2, "0");
    return {
      name: `${region}${n}${i % 4 === 0 ? "(推荐)" : ""}`,
      type: "ss",
      server: `n${String(i).padStart(3, "0")}.edge.example`,
      port: "443",
    };
  });
  const kept = all.slice(0, keptCount);
  const droppedAll = all.slice(keptCount);
  const named = droppedAll.slice(0, 40);
  return {
    nodes: kept,
    node_count: kept.length,
    source_node_count: all.length,
    dropped: named,
    dropped_count: droppedAll.length,
    dropped_truncated: named.length < droppedAll.length,
  };
}

/** Shaped like the owner's actual deployment, names included.
 *  The names matter: real records carry CJK ("建材市场") and long hyphenated
 *  ids ("openjobs-host-trojan"), and a row tuned only against "Home nodes"
 *  hides every truncation and line-height bug those produce. The fetch
 *  bookkeeping spans its three states too, one sub with traffic, one whose last
 *  refresh failed, one never fetched, so the row status has something to say. */
function cannedRecords(): StoredRecord[] {
  return [
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
    // A real provider link: the token rides in the query string, which is
    // exactly what every read view must mask and no toast may print.
    url: "https://sub.example-provider.com/api/v1/client/subscribe?token=9f8e7d6c5b4a32100123456789abcdef&flag=clash",
    ua: "Surge",
    // Three enabled operations, so the expanded row and the compare panel
    // have a chain to account for step by step.
    process: [
      { type: "Region Filter", args: { value: ["HK", "JP"], keep: true } },
      { type: "Regex Rename Operator", args: { value: [{ expr: "^HK", now: "香港 Hong Kong" }] } },
      { type: "Sort Operator", args: { value: "asc" } },
    ],
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
    source: "remote",
    url: "https://vip.ding202507.xyz/api/v1/client/subscribe?token=jiancai-harness-token&flag=clash",
    last_fetch_at: new Date(Date.now() - 4 * 86400 * 1000).toISOString(),
    last_fetch_ok: true,
    process: [
      { type: "Quick Setting Operator", args: { udp: true } },
      { type: "Regex Filter", args: { value: ["香港|HK"], keep: true } },
      { type: "Regex Delete Operator", args: { value: ["过期|expire"] } },
    ],
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
  {
    // The long-name row and the long document, together: the name has to
    // ellipse in a 375px row, and the rendered document has to run past a
    // screen so the sheet is seen to grow with the page as the one scroller.
    id: "tablet-config-long",
    kind: "file",
    name: "A deliberately long client configuration name for the tablet that has to truncate somewhere sensible",
    tags: ["tablet", "backup"],
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
      ...Array.from({ length: 60 }, (_, i) => `  - DOMAIN-SUFFIX,service-${String(i + 1).padStart(2, "0")}.example,PROXY`),
      "  - MATCH,PROXY",
      "",
    ].join("\n"),
    process: [{ type: "Remove Duplicate Filter" }],
  },
  ];
}

/** The canned set's shares, one per state the Shares lens and the sheet can show. */
function cannedShares(records: StoredRecord[]): ShareRow[] {
  return [
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
        // A second share on the same record, switched off: the Shares lens
        // and the Published column have to tell "disabled" from "expired".
        subscription_id: records[0]?.id ?? "sub-1",
        share_id: "sh-dev-off",
        slug: "cd-self-old",
        enabled: false,
        default_format: "plain",
        expires_at: new Date(Date.now() + 30 * 86400 * 1000).toISOString(),
        path: "/sub/cd-self-old/oldtokenoldtokenoldtokenoldtokenol",
        url: "https://lattice.example/sub/cd-self-old/oldtokenoldtokenoldtokenoldtokenol",
      },
      {
        // Enabled but past its expiry: reachable in the list, dead to a client.
        subscription_id: "openjobs-host",
        share_id: "sh-dev-expired",
        slug: "openjobs",
        enabled: true,
        default_format: "clash",
        expires_at: new Date(Date.now() - 3 * 86400 * 1000).toISOString(),
        path: "/sub/openjobs/expiredtokenexpiredtokenexpiredtok",
        url: "https://lattice.example/sub/openjobs/expiredtokenexpiredtokenexpiredtok",
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
      {
        // No pinned format: the list writes "as the client asks", which is
        // longer than the Nodes column the shares row used to reuse.
        subscription_id: records[0]?.id ?? "sub-1",
        share_id: "sh-dev-ask",
        slug: "cd-ask",
        enabled: true,
        path: "/sub/cd-ask/asktokenasktokenasktokenasktokenas",
        url: "https://lattice.example/sub/cd-ask/asktokenasktokenasktokenasktokenas",
      },
  ];
}

function cannedFixture(): Fixture {
  const records = cannedRecords();
  return { records, shares: cannedShares(records), counts: { "jiancai-shichang": [166, 25] } };
}

/**
 * Which record set the harness serves, from `?fixture=` (production unless
 * named). The whole set is built once per page load, so an edit made in the
 * harness survives until the reload, the way a store does.
 */
const active: Fixture = fixture(fixtureName(window.location.search), cannedFixture);
const records = active.records;

let settings: Record<string, unknown> = { default_target: "", default_ua: "" };

/**
 * The figures the runtime parses out of the provider header. parseUserinfo
 * follows the runtime's rules exactly (the shared cases are in
 * system-go/testdata/userinfo_cases.json), so the harness can answer the
 * runtime's wire shape, marker included, and the UI's fallback parse stays the
 * path only an older runtime exercises.
 */
function usageOf(userinfo: string | undefined): Record<string, number> {
  return { ...(parseUserinfo(userinfo) ?? {}) };
}

/**
 * A preview whose counts are the record's own (production's 86 in, 78 out).
 * A chain cut part way through has run some of the filters, and `kept` says
 * how many nodes they left (see countAfter).
 */
function countedPreview(source: number, kept: number, label: string) {
  const all = Array.from({ length: source }, (_, i) => ({
    name: `${label} ${String(i + 1).padStart(3, "0")}`,
    type: i % 3 === 0 ? "trojan" : "vless",
    server: `n${String(i).padStart(3, "0")}.edge.example`,
    port: i % 2 === 0 ? "443" : "8443",
  }));
  const nodes = all.slice(0, Math.min(kept, 200));
  const droppedAll = all.slice(kept);
  const named = droppedAll.slice(0, 40);
  return {
    nodes,
    node_count: kept,
    source_node_count: source,
    truncated: kept > nodes.length,
    dropped: named,
    dropped_count: droppedAll.length,
    dropped_truncated: named.length < droppedAll.length,
  };
}

/**
 * Which store layout the harness plays, from `?store=` on its URL: the split
 * index S1 writes (the default), a `legacy` single document not yet migrated
 * (list says store_version 1, every write is refused until migrate_store is
 * done), or `none`, a runtime from before the split that sends no
 * store_version and no index fields at all.
 */
type StoreMode = "split" | "legacy" | "none";
function storeMode(): StoreMode {
  const asked = new URLSearchParams(window.location.search).get("store");
  return asked === "legacy" || asked === "none" ? asked : "split";
}
/** The legacy store migrates in place once migrate_store reports done. */
let legacy = storeMode() === "legacy";
let migratedSoFar = 0;
const MIGRATION_REQUIRED = "store_migration_required: the subscription store still holds the legacy single document; run migrate_store until it reports done, then retry";

const FALLBACK_STEPS = new Set(["Script Operator", "Script Filter", "Resolve Domain Operator"]);

/**
 * The index flags (s1-plan section 2.5) as the chain compiler sets them: a
 * pattern RE2 refuses, and a step that only the fallback bundle runs.
 */
function flagsOf(rec: StoredRecord): { regex_incompatible?: boolean; has_fallback_step?: boolean } | undefined {
  const chain = (rec.process ?? []) as { type?: string; disabled?: boolean }[];
  const flags: { regex_incompatible?: boolean; has_fallback_step?: boolean } = {};
  if (regexDiagnostics(chain).length) flags.regex_incompatible = true;
  if (chain.some((step) => !step.disabled && FALLBACK_STEPS.has(step.type ?? ""))) flags.has_fallback_step = true;
  return Object.keys(flags).length ? flags : undefined;
}

/**
 * The index's node counts: what the last fetch read, and what the chain
 * handed on when it ran natively (a flagged or fallback chain records no
 * out). A record the fixture has no counts for was never counted.
 */
function indexCounts(rec: StoredRecord): { nodes_in?: number; nodes_out?: number } {
  if (storeMode() === "none" || legacy || rec.kind === "file") return {};
  const counted = active.counts[rec.id];
  if (!counted) return {};
  const flags = flagsOf(rec);
  return flags ? { nodes_in: counted[0] } : { nodes_in: counted[0], nodes_out: counted[1] };
}

function listView(rec: StoredRecord, order: number) {
  const steps = (rec.process ?? []) as { disabled?: boolean }[];
  const index = storeMode() === "none" ? {} : { revision: `rev-${rec.id}-1`, order, flags: flagsOf(rec), ...indexCounts(rec) };
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
    ...index,
    // Only once fetched, like the backend: absent reads as "never fetched".
    ...(rec.last_fetch_at
      ? {
          last_fetch_at: rec.last_fetch_at,
          last_fetch_ok: rec.last_fetch_ok ?? false,
          last_error: rec.last_error,
          userinfo: rec.userinfo,
          ...(rec.userinfo ? { ...usageOf(rec.userinfo), userinfo_parsed: true } : {}),
        }
      : {}),
  };
}

/** Records deleted under the split store, kept until purge (s1-plan section 3.2). */
const archived: StoredRecord[] = [];
let listReads = 0;

/** The records whose sources reach vpn-core, directly, as a member or as a file's node source. */
function fleetReaders(): StoredRecord[] {
  const fleet = new Set(records.filter((rec) => rec.source === "vpn-core" || rec.source === "vpn-core-graph").map((rec) => rec.id));
  let grew = true;
  while (grew) {
    grew = false;
    for (const rec of records) {
      if (fleet.has(rec.id)) continue;
      const reaches = (rec.members ?? []).some((id) => fleet.has(id)) || (rec.node_source ? fleet.has(rec.node_source) : false);
      if (reaches) {
        fleet.add(rec.id);
        grew = true;
      }
    }
  }
  return records.filter((rec) => fleet.has(rec.id));
}

const HANDLERS: Record<string, (payload: any) => unknown> = {
  "subscription/list": () => {
    const state = harnessState();
    listReads += 1;
    if (state === "empty") return { subscriptions: [], ...(storeMode() === "none" ? {} : { store_version: 2, order: "manual" }) };
    if (state === "error") throw new Error("the store could not be read (harness ?state=error)");
    // The first read lands; every read after it fails, so the rows on screen
    // become the last good read the moment anything reloads the list.
    if (state === "stale" && listReads > 1) throw new Error("the store could not be read (harness ?state=stale)");
    const subscriptions = records.map((rec, order) => listView(rec, order));
    if (storeMode() === "none") return { subscriptions };
    return { subscriptions, store_version: legacy ? 1 : 2, order: "manual" };
  },
  /**
   * `{ids}`, every live id once. `?reorder=fail` refuses every call, so the
   * table's revert and its announcement can be looked at.
   */
  "subscription/reorder": ({ ids }) => {
    if (legacy) throw new Error(MIGRATION_REQUIRED);
    if (new URLSearchParams(window.location.search).get("reorder") === "fail") {
      throw new Error("the index changed under the reorder (harness ?reorder=fail)");
    }
    const wanted = Array.isArray(ids) ? (ids as string[]) : [];
    if (wanted.length !== records.length || new Set(wanted).size !== wanted.length) {
      throw new Error(`reorder names ${wanted.length} records, the store holds ${records.length}; list again and resend every id once`);
    }
    const byId = new Map(records.map((rec) => [rec.id, rec]));
    const next = wanted.map((id) => {
      const found = byId.get(id);
      if (!found) throw new Error(`reorder names "${id}", which is not a live record or is named twice`);
      return found;
    });
    records.splice(0, records.length, ...next);
    return { reordered: true, count: next.length };
  },
  /** One chunk of the legacy store per call, until it reports done and verified. */
  "subscription/migrate_store": ({ chunk }) => {
    if (!legacy) return { migrated: 0, remaining: 0, done: true, verified: true, store_version: 2 };
    const size = Math.max(1, Math.min(Number(chunk) || 64, 64));
    const step = Math.min(size, records.length - migratedSoFar);
    migratedSoFar += step;
    const remaining = records.length - migratedSoFar;
    if (remaining === 0) legacy = false;
    const flagged = records.filter((rec) => flagsOf(rec)?.regex_incompatible).map((rec) => rec.id);
    return { migrated: step, remaining, done: remaining === 0, verified: remaining === 0, store_version: remaining === 0 ? 2 : 1, regex_incompatible: flagged };
  },
  "subscription/restore": ({ subscription_id }) => {
    if (legacy) throw new Error(MIGRATION_REQUIRED);
    const index = archived.findIndex((rec) => rec.id === subscription_id);
    if (index === -1) throw new Error(`subscription "${subscription_id}" is not archived`);
    const [restored] = archived.splice(index, 1);
    records.push(restored!);
    return { id: subscription_id, restored: true, subscription: restored };
  },
  "subscription/purge": ({ subscription_id }) => {
    if (legacy) throw new Error(MIGRATION_REQUIRED);
    const index = archived.findIndex((rec) => rec.id === subscription_id);
    if (index === -1) throw new Error(`subscription "${subscription_id}" is not archived`);
    archived.splice(index, 1);
    return { id: subscription_id, purged: true };
  },
  "subscription/depends_on": () => ({
    records: fleetReaders().map((rec) => ({ id: rec.id, revision: `rev-${rec.id}-1` })),
    version: `idx-${records.length}`,
  }),
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
    // The real backend fingerprints every record it hands out, and the editor
    // sends it back to make the save conditional. Without one here the dev
    // harness would exercise the unconditional path only.
    return { subscription: { ...found, revision: `rev-${found.id}-1` } };
  },
  /**
   * `?conflict=1` makes every save answer as though someone else had written to
   * the record first. The lost-update path is the one that is impossible to
   * reach by hand (it needs two operators, or a refresh landing in the seconds
   * between a read and a save), so without a switch it never gets looked at.
   *
   * `?conflict=deleted` gives the other reason: the record went away.
   */
  "subscription/save": ({ subscription, if_revision }) => {
    if (legacy) throw new Error(MIGRATION_REQUIRED);
    // Save-time strict compile (s1-plan section 3.1): a chain the caller
    // changed must compile under RE2. An unchanged chain on a flagged record
    // saves, so an edit to its name is never refused for its old patterns.
    const before = records.find((r) => r.id === subscription.id);
    const changed = JSON.stringify(before?.process ?? []) !== JSON.stringify(subscription.process ?? []);
    const refused = changed ? regexDiagnostics((subscription.process ?? []) as unknown[]) : [];
    if (refused.length) {
      const first = refused[0]!;
      throw new Error(`regex_incompatible: step ${first.step} pattern ${JSON.stringify(first.pattern)} needs lookaround or a backreference, which RE2 cannot compile`);
    }
    const mode = new URLSearchParams(window.location.search).get("conflict");
    if (mode && if_revision) {
      if (mode === "deleted") {
        return { saved: false, conflict: { id: subscription.id, reason: "deleted" } };
      }
      const stored = records.find((r) => r.id === subscription.id);
      return {
        saved: false,
        conflict: {
          id: subscription.id,
          reason: "stale",
          revision: `rev-${subscription.id}-2`,
          subscription: {
            ...stored,
            revision: `rev-${subscription.id}-2`,
            display_name: "Renamed by the other operator",
            remark: "They also left a note here explaining why they changed it",
            ua: "ClashMetaForAndroid",
            tags: ["paid", "eu", "reviewed"],
          },
        },
      };
    }
    const index = records.findIndex((r) => r.id === subscription.id);
    if (index === -1) records.push(subscription);
    else records[index] = subscription;
    return { subscription: { ...subscription, revision: `rev-${subscription.id}-2` }, saved: true };
  },
  "subscription/delete": ({ subscription_id }) => {
    if (legacy) throw new Error(MIGRATION_REQUIRED);
    const index = records.findIndex((r) => r.id === subscription_id);
    if (index === -1) throw new Error(`subscription "${subscription_id}" was not found`);
    const [gone] = records.splice(index, 1);
    // The split store archives on delete; restore brings it back under the same id.
    if (storeMode() !== "none") archived.push(gone!);
    return { id: subscription_id, deleted: true, ...(storeMode() !== "none" ? { archived: true } : {}) };
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
  // The admin-scoped twin of preview, for a draft that names a source. The
  // harness answers it exactly as preview does; what it exists to exercise is
  // the gate on the client, which withholds it in the read-only state.
  "subscription/preview_draft": (payload) => HANDLERS["subscription/preview"]!(payload),
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
    // node count that shrinks per operation, otherwise the per-operation
    // preview looks identical at every cut and the screen cannot be checked
    // at all. No operators means the stored chain, the way the plugin reads
    // it, so the list's own count reflects each record's chain too.
    const stored = ((found?.process ?? []) as { disabled?: boolean }[]).filter((step) => !step.disabled);
    const ran: unknown[] = Array.isArray(operators) ? operators : stored;
    if (found?.id === "jiancai-shichang") return jiancaiPreview(countAfter(stored, ran, 166, 25));
    const counts = found ? active.counts[found.id] : undefined;
    if (found && counts) return countedPreview(counts[0], countAfter(stored, ran, counts[0], counts[1]), found.name);
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
    // Each filter in the run takes one node away and every other operation
    // keeps them all, so a cut preview says which filter removed what.
    const keptCount = Math.max(1, all.length - ran.filter(removesNodes).length);
    // The renaming operator is the first step, so its mark only survives while
    // that step is still in the run.
    const kept = all.slice(0, keptCount).map((node) => (ran.length > 0 ? node : { ...node, was: undefined }));
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
  "shares/list": () => {
    if (harnessState() === "sharesfail") throw new Error("the share list could not be read (harness ?state=sharesfail)");
    if (harnessState() === "empty") return { shares: [] };
    return { shares: active.shares };
  },
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
  // `?state=slow` holds rather than delays. Every timed version lost the race:
  // by the time anyone had switched to the window the list had arrived, which
  // is the same problem as having no state at all. A loading state exists to
  // be looked at, so this one stays up until the page is reloaded without it.
  if (harnessState() === "slow") return new Promise<T>(() => {});
  return new Promise((resolve) => setTimeout(() => resolve(value), 180));
}

/**
 * Which state to render, from `?state=` on the harness URL.
 *
 * A screen's failure states were only ever reachable by breaking something on
 * purpose and putting it back afterwards, so in practice nobody looked at
 * them: the empty state, the load error and the read-only session were written
 * once and never seen again. Naming them makes the whole set walkable in a
 * minute, which is the only way an audit of them stays honest.
 *
 * `?state=slow` is deliberately not instant — a skeleton that flashes past is a
 * skeleton nobody has actually judged.
 */
export type HarnessState = "ok" | "empty" | "error" | "slow" | "readonly" | "stale" | "noadmin" | "sharesfail";

/** Whether the handshake declares the S1 capability-wave methods (`?manifest=s1`). */
export function s1Manifest(): boolean {
  return new URLSearchParams(window.location.search).get("manifest") === "s1";
}

export function harnessState(): HarnessState {
  const asked = new URLSearchParams(window.location.search).get("state");
  const known: HarnessState[] = ["ok", "empty", "error", "slow", "readonly", "stale", "noadmin", "sharesfail"];
  return (known as string[]).includes(asked ?? "") ? (asked as HarnessState) : "ok";
}

export function createFakeHost(): HostContext {
  const init = ref<any>();
  const bootError = ref("");
  const pageState = ref<PageState>({});
  const allowState = stateRateLimit();

  // The handshake lands late on purpose: a screen that loads before it and
  // never retries is the bug this harness exists to make visible.
  setTimeout(() => {
    // Like the console: the address's query is the page state, read at the
    // handshake, so a reload of this tab lands where the page last said.
    pageState.value = pageStateFromAddress(window.location.search);
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
            "fetch", "probe", "render", "operators", "graph_options", "preview", "preview_draft", "list",
            "get", "save", "delete", "migrate", "export", "import", "get_settings", "save_settings", "publish",
            // `?manifest=s1` plays the capability-wave manifest (s1-plan
            // section 6), which declares the store split's methods. Without
            // it the handshake is what manifest.json declares today, and the
            // UI keeps every control those methods drive out of reach.
            ...(s1Manifest() ? ["restore", "purge", "reorder", "migrate_store", "depends_on"] : []),
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

  // A read-only session, which is a token or a bundle without the write
  // methods rather than a flag. Withholding them here exercises the same path
  // production takes, including every "why is this greyed out" sentence.
  // `noadmin` keeps the editor but withholds the one admin-scoped preview,
  // which is the session the compare panel's stored-source path exists for.
  const WITHHELD: Record<string, true> = harnessState() === "readonly"
    ? { save: true, delete: true, publish: true, preview_draft: true, reorder: true, migrate_store: true, restore: true, purge: true }
    : harnessState() === "noadmin"
      ? { preview_draft: true }
      : {};
  // The share list is substore:admin plus proxy:admin, so a read-only session
  // cannot read it at all and the Published column says it does not know.
  const SHARES_WITHHELD = harnessState() === "readonly";

  const bridge = {
    call<T>(service: string, method: string, payload: unknown) {
      // The real bridge posts to the host, which structured-clones the payload.
      // Handing it over by reference made the harness answer calls that could
      // never have left the frame: a Proxy cannot be cloned, and every object a
      // screen holds is one. Cloning here means the harness fails the same way
      // production does.
      try {
        structuredClone(payload);
      } catch (cause) {
        return {
          promise: Promise.reject(
            new Error(
              `payload for ${method} cannot cross the host transport: ${
                cause instanceof Error ? cause.message : String(cause)
              }`,
            ),
          ),
          cancel: () => {},
        };
      }
      const key = `${service.split("/").pop()}/${method}`;
      const handler = HANDLERS[key];
      if (!handler) return { promise: Promise.reject(new Error(`the dev harness has no answer for ${key}`)), cancel: () => {} };
      // A refusal comes back after the same latency as an answer, as it does
      // over the host transport. Thrown straight out of the call, it settled
      // in the tick the read began, so the loading state between a retry and
      // its failure was never painted here.
      let promise: Promise<T>;
      try {
        promise = delay(handler((payload ?? {}) as any) as T);
      } catch (cause) {
        promise = delay(undefined).then(() => Promise.reject(cause));
      }
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
      !WITHHELD[target.method] &&
      !(SHARES_WITHHELD && target.service === BINDINGS.sharesList.service) &&
      init.value?.interfaces.some(
        (contract: any) =>
          contract.service === target.service && contract.methods.includes(target.method),
      ) === true,
    resize: async () => {},
    pageState,
    // What the console does with `lattice.plugin.state`: validate, count
    // against the budget, and replace the query with history replace, never
    // push. Cloned first, as postMessage would, so a reactive proxy fails
    // here the way it would fail in production.
    sendState: (state) => {
      const cloned = structuredClone(state);
      if (!allowState()) return;
      const query = addressForState(window.location.search, cloned);
      if (query === null) return;
      window.history.replaceState(window.history.state, "", `${window.location.pathname}${query}${window.location.hash}`);
    },
  };
}

export { BINDINGS };
