/**
 * client.ts — every backend method binding for this UI, isolated in one module.
 *
 * Two tiers:
 *  - "active":  methods declared by the manifest on the integration line.
 *    Today that is the shipped `import` adapter surface plus the embedded
 *    `engine` surface (convert / transform_response / pipeline CRUD /
 *    run_pipeline — hephaestus's PR6, per-method budgets included).
 *  - "pending": proposed but not yet declared methods. Empty right now — the
 *    engine contract landed and consumed the whole proposal tier. The tier
 *    mechanism stays: a future wave (e.g. subscription records) enters here
 *    first, and contract.test.ts trips when it becomes declared.
 *
 * Wire shapes mirror system-go's structs exactly (verified against the merged
 * engine implementation 2026-07-27). When the backend changes a shape, this
 * file is the only UI edit.
 */
import type { BridgeClient } from "@latticenet/plugin-bridge";

export const SERVICES = {
  engine: "latticenet.sub-store/engine",
  subscription: "latticenet.sub-store/subscription",
} as const;

export type BindingStatus = "active" | "pending";

export interface MethodBinding {
  readonly service: string;
  readonly method: string;
  readonly status: BindingStatus;
}

function binding(service: string, method: string, status: BindingStatus): MethodBinding {
  return { service, method, status };
}

export const BINDINGS = {
  // ── embedded engine (manifest-declared since the PR6 merge) ──────────────
  engineConvert: binding(SERVICES.engine, "convert", "active"),
  engineTransformResponse: binding(SERVICES.engine, "transform_response", "active"),
  engineSavePipeline: binding(SERVICES.engine, "save_pipeline", "active"),
  engineGetPipeline: binding(SERVICES.engine, "get_pipeline", "active"),
  engineListPipelines: binding(SERVICES.engine, "list_pipelines", "active"),
  engineDeletePipeline: binding(SERVICES.engine, "delete_pipeline", "active"),
  engineRunPipeline: binding(SERVICES.engine, "run_pipeline", "active"),
  // ── subscription platform (design-16) ────────────────────────────────────
  // The backend for these shipped and was signed before any of them had a
  // caller, so the whole feature was unreachable from the UI. `get`, `save`
  // and `delete` did not exist at all: a subscription could only enter the
  // store by migrating from a standalone Sub-Store or restoring a backup.
  subList: binding(SERVICES.subscription, "list", "active"),
  subGet: binding(SERVICES.subscription, "get", "active"),
  subSave: binding(SERVICES.subscription, "save", "active"),
  subDelete: binding(SERVICES.subscription, "delete", "active"),
  subFetch: binding(SERVICES.subscription, "fetch", "active"),
  subPreview: binding(SERVICES.subscription, "preview", "active"),
  subOperators: binding(SERVICES.subscription, "operators", "active"),
  subMigrate: binding(SERVICES.subscription, "migrate", "active"),
  subExport: binding(SERVICES.subscription, "export", "active"),
  subImport: binding(SERVICES.subscription, "import", "active"),
  subGetSettings: binding(SERVICES.subscription, "get_settings", "active"),
  subSaveSettings: binding(SERVICES.subscription, "save_settings", "active"),
  subPublish: binding(SERVICES.subscription, "publish", "active"),
} as const satisfies Record<string, MethodBinding>;

/**
 * Client-side guards mirroring the backend's signed per-method budgets and
 * record limits (system-go constants — keep in sync, BINARY units):
 *  - convert/transform_response/run_pipeline stdout budget: 6 << 20
 *  - run_pipeline raw input cap: 1 << 20
 *  - pipeline records: ≤ 256 records, ≤ 64 operators per record
 *
 * The stdout budget is enforced by ABORT, not truncation: the runner returns
 * `plugin stdout exceeded budget N bytes` and no result at all
 * (system_runner.go:475). So a result the UI can render was never truncated —
 * the only useful warning is proximity to the ceiling, which is why
 * CONVERT_OUTPUT_WARN_BYTES exists alongside the budget itself.
 */
export const CONVERT_OUTPUT_BUDGET_BYTES = 6 * 1024 * 1024;
export const CONVERT_OUTPUT_WARN_BYTES = Math.floor(CONVERT_OUTPUT_BUDGET_BYTES * 0.8);
export const RAW_INPUT_LIMIT_BYTES = 1024 * 1024;
export const MAX_PIPELINE_OPERATORS = 64;
export const MAX_PIPELINE_RECORDS = 256;

// ── engine shapes (manifest: latticenet.sub-store/engine) ───────────────────

/** convert request: raw subscription content + target format + optional
 *  operator chain. The engine never fetches — the UI supplies the content. */
export interface ConversionRequest {
  raw: string;
  target: string;
  operators?: unknown[];
}

export interface ConversionResult {
  target: string;
  source_node_count: number;
  node_count: number;
  output: string;
  output_bytes: number;
}

/** transform_response: reshape an HTTP response object through the core. */
export interface ResponseTransformRequest {
  response: unknown;
  target?: string;
  operators?: unknown[];
}

export interface ResponseTransformResult {
  target?: string;
  status: number;
  headers: Record<string, unknown>;
  body: string;
  body_bytes: number;
}

export interface PipelineRecord {
  id: string;
  name: string;
  target: string;
  operators?: unknown[];
}

export interface PipelineListItem {
  id: string;
  name: string;
  target: string;
  operator_count: number;
}

export interface PipelineListResponse {
  records: PipelineListItem[];
  count: number;
}

export interface PipelineSaveResponse {
  id: string;
  created: boolean;
  count: number;
}

export interface PipelineGetResponse {
  found: boolean;
  record?: PipelineRecord;
  id?: string;
}

export interface PipelineDeleteResponse {
  id: string;
  deleted: boolean;
  count: number;
}

// ── subscription shapes (manifest: latticenet.sub-store/subscription) ───────

/** One stored subscription. Mirrors system-go's `subscriptionRecord`. */
export interface SubscriptionRecord {
  schema_version?: number;
  id: string;
  /**
   * "collection" combines other subs, "file" is a served document, empty or
   * "sub" is one source of nodes.
   */
  kind?: string;
  name: string;
  display_name?: string;
  remark?: string;
  tags?: string[];
  /** Collection inputs: explicit sub ids, plus every sub carrying these tags. */
  members?: string[];
  member_tags?: string[];
  /** Collections only: "strict" (default) or "skip-failed". */
  failure_mode?: string;
  url?: string;
  content?: string;
  /** "vpn-core" reads the live node export; empty uses url/content. */
  source?: string;
  /** Narrows a vpn-core export to one identity. Empty means all of them. */
  vpn_identity?: string;
  ua?: string;
  target?: string;
  /** Files only: "config" is a client configuration, "plain" is served as-is. */
  file_type?: string;
  /** Files only: the id of the sub or collection whose nodes get injected. */
  node_source?: string;
  /** Files only: served with a filename so a client saves rather than shows it. */
  download?: boolean;
  /** Script files only: URL parameters the program is allowed to see. */
  query_params?: string[];
  /** Script files only: `$arguments`, stored with the file rather than on the URL. */
  arguments?: Record<string, string>;
  /** The ordered operator chain. Entries may be disabled without deletion. */
  process?: unknown[];
  /**
   * Fetch bookkeeping, written by the refresh path rather than by a caller.
   * RFC3339 time, how that fetch went, the trimmed reason when it failed, and
   * the provider's subscription-userinfo header verbatim.
   */
  last_fetch_at?: string;
  last_fetch_ok?: boolean;
  last_error?: string;
  userinfo?: string;
  /** Set by migration only. The backend refuses to take this from a caller. */
  origin?: { source: string; kind: string; raw?: unknown };
}

/**
 * The list view deliberately omits `content` and `operators`: a management
 * list must not double as a dump of every provider payload. Editing one record
 * calls `get`, which returns the whole thing.
 */
export interface SubscriptionListItem {
  id: string;
  kind: string;
  name: string;
  display_name?: string;
  remark?: string;
  tags?: string[];
  source?: string;
  has_url: boolean;
  has_inline_content: boolean;
  members?: string[];
  member_tags?: string[];
  target?: string;
  file_type?: string;
  node_source?: string;
  step_count: number;
  disabled_step_count: number;
  imported: boolean;
  /**
   * Fetch bookkeeping, present only once the record has been refreshed at all.
   * Absent means "never fetched" — not "failed".
   */
  last_fetch_at?: string;
  last_fetch_ok?: boolean;
  last_error?: string;
  userinfo?: string;
}

export const KIND_SUB = "sub";
export const KIND_COLLECTION = "collection";
export const KIND_FILE = "file";

/**
 * A config is a client configuration whose `proxies` get replaced from a node
 * source; plain text is served exactly as stored, after its chain runs.
 */
export const FILE_TYPE_CONFIG = "config";
export const FILE_TYPE_PLAIN = "plain";
/**
 * The document is built by a JavaScript program rather than stored as text.
 * The program asks for the file's node source through `produceArtifact()` and
 * assigns what it built to `$content`.
 */
export const FILE_TYPE_SCRIPT = "script";

export interface SubscriptionListResponse {
  subscriptions: SubscriptionListItem[];
}

export interface SubscriptionGetResponse {
  subscription: SubscriptionRecord;
}

export interface SubscriptionSaveResponse {
  subscription: SubscriptionRecord;
  saved: boolean;
}

export interface SubscriptionDeleteResponse {
  id: string;
  deleted: boolean;
}

export interface SubscriptionFetchResponse {
  subscription_id: string;
  bytes: number;
  fetched_at?: string;
  userinfo?: string;
  error?: string;
}

/** preview reduces nodes to name/type on the engine side, so a preview of a
 *  large subscription cannot blow the stdout budget. */
export interface SubscriptionPreviewNode {
  name: string;
  type: string;
}

export interface SubscriptionPreviewResponse {
  nodes: SubscriptionPreviewNode[];
  /**
   * Nodes before and after the chain runs. The wire names are `node_count` /
   * `source_node_count` (system-go's previewResult) — an earlier reading of
   * this type called the count `count`, a field the backend never sends, so
   * the editor's preview header rendered "undefined node(s)" in production.
   */
  node_count: number;
  source_node_count?: number;
  truncated?: boolean;
  /**
   * Set instead of `nodes` when the record is a file. A file is a document, so
   * its preview answers "what will a client receive" rather than "which nodes
   * survived the filter".
   */
  document?: string;
}

export interface OperatorInfo {
  type: string;
  summary?: string;
  scripting?: boolean;
  /**
   * Runs over a served document rather than a node list. The two chains do not
   * mix: the engine skips proxy operators for responses and these for nodes.
   */
  response?: boolean;
}

export interface OperatorCatalogResponse {
  operators: OperatorInfo[];
}

export interface SubscriptionSettings {
  default_target?: string;
  default_ua?: string;
  refresh_minutes?: number;
  [key: string]: unknown;
}

export interface MigrationReport {
  imported: string[];
  skipped: Record<string, string>;
  [key: string]: unknown;
}

export interface BackupExportResponse {
  backup: string;
}

/** Mirrors system-go's `maxSubscriptionInlineBytes`. Enforced by the backend;
 *  checked here so a paste that cannot be saved says so before a round trip. */
export const MAX_SUBSCRIPTION_INLINE_BYTES = 256 * 1024;
export const MAX_SUBSCRIPTION_RECORDS = 256;

/** Mirrors system-go's `subscriptionSourceVPNCore`. */
export const SOURCE_VPN_CORE = "vpn-core";
/** A provider that is fetched over HTTP. */
export const SOURCE_REMOTE = "remote";
/** Nodes pasted by hand. The engine accepts every format it recognises. */
export const SOURCE_LOCAL = "local";

/** How a combination reacts when a member cannot be fetched. */
export const FAILURE_STRICT = "strict";
export const FAILURE_SKIP = "skip-failed";

/** Curated produce targets for the pinned upstream core. "Clash" and
 *  "sing-box" are pinned by the system-go engine tests; the rest are
 *  upstream-canonical spellings, not backend-test-pinned (hephaestus's
 *  2026-07-27 caveat). */
export const CONVERT_TARGETS: readonly { id: string; label: string; produces: string }[] = [
  { id: "Clash", label: "Clash", produces: "yaml" },
  { id: "ClashMeta", label: "Clash Meta", produces: "yaml" },
  { id: "sing-box", label: "sing-box", produces: "json" },
  { id: "Surge", label: "Surge", produces: "conf" },
  { id: "Loon", label: "Loon", produces: "conf" },
  { id: "Stash", label: "Stash", produces: "yaml" },
  { id: "QX", label: "Quantumult X", produces: "conf" },
  { id: "Shadowrocket", label: "Shadowrocket", produces: "conf" },
  { id: "URI", label: "URI list", produces: "text" },
  { id: "V2Ray", label: "V2Ray", produces: "text" },
];

/** Typed call through the bridge; every UI data path funnels through here. */
export function callMethod<T>(
  bridge: BridgeClient,
  target: MethodBinding,
  payload: unknown,
  timeoutMs?: number,
): { promise: Promise<T>; cancel: () => void } {
  return bridge.call<T>(target.service, target.method, payload, timeoutMs);
}
