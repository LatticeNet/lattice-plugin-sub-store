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
import type { BridgeClient } from "./bridge";

export const SERVICES = {
  import: "latticenet.sub-store/import",
  engine: "latticenet.sub-store/engine",
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
  // ── shipped adapter (manifest-declared) ──────────────────────────────────
  importStatus: binding(SERVICES.import, "status", "active"),
  importPreview: binding(SERVICES.import, "preview", "active"),
  importRun: binding(SERVICES.import, "import", "active"),
  endpointStatus: binding(SERVICES.import, "endpoint_status", "active"),
  endpointSave: binding(SERVICES.import, "save_endpoint", "active"),
  endpointClear: binding(SERVICES.import, "clear_endpoint", "active"),
  // ── embedded engine (manifest-declared since the PR6 merge) ──────────────
  engineConvert: binding(SERVICES.engine, "convert", "active"),
  engineTransformResponse: binding(SERVICES.engine, "transform_response", "active"),
  engineSavePipeline: binding(SERVICES.engine, "save_pipeline", "active"),
  engineGetPipeline: binding(SERVICES.engine, "get_pipeline", "active"),
  engineListPipelines: binding(SERVICES.engine, "list_pipelines", "active"),
  engineDeletePipeline: binding(SERVICES.engine, "delete_pipeline", "active"),
  engineRunPipeline: binding(SERVICES.engine, "run_pipeline", "active"),
} as const satisfies Record<string, MethodBinding>;

/**
 * Client-side guards mirroring the backend's signed per-method budgets and
 * record limits (system-go constants — keep in sync, BINARY units):
 *  - convert/transform_response/run_pipeline stdout budget: 6 << 20
 *  - run_pipeline raw input cap: 1 << 20
 *  - pipeline records: ≤ 256 records, ≤ 64 operators per record
 */
export const CONVERT_OUTPUT_BUDGET_BYTES = 6 * 1024 * 1024;
export const RAW_INPUT_LIMIT_BYTES = 1024 * 1024;
export const MAX_PIPELINE_OPERATORS = 64;
export const MAX_PIPELINE_RECORDS = 256;

// ── shipped adapter shapes (manifest: latticenet.sub-store/import) ──────────

export interface StatusResponse {
  reachable: boolean;
  sub_name: string;
  error?: string;
}

export interface ImportResponse {
  ok: boolean;
  sub_name: string;
  pushed: number;
}

export interface ImportPreviewResponse {
  sub_name: string;
  exists: boolean;
  added: string[];
  removed: string[];
  added_count: number;
  removed_count: number;
  unchanged_count: number;
  total_after: number;
}

export interface AutosyncStatus {
  state: "running" | "success" | "error";
  attempted_at?: string;
  last_success_at?: string;
  error?: string;
}

export interface EndpointStatusResponse {
  has_saved_endpoint: boolean;
  autosync: boolean;
  endpoint_hint?: string;
  autosync_status?: AutosyncStatus;
}

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
