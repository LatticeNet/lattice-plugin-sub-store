/**
 * client.ts — every backend method binding for this UI, isolated in one module.
 *
 * Two tiers:
 *  - "active":  methods declared by the currently signed manifest (the shipped
 *    `latticenet.sub-store/import` adapter). Safe to call in production today.
 *  - "pending": the embedded-engine surface proposed to hephaestus for TASK-0002
 *    (letter 20260726-0718Z-athena-substore-ui-contract-proposal.md). The
 *    manifest does not declare these yet, so `canCall` gates every screen that
 *    uses them and they render honest unavailable-states instead of failing.
 *
 * When the TASK-0002 contract lands, flip bindings from "pending" to "active"
 * and adjust the response types here — nothing else in the UI references method
 * names directly. contract.test.ts trips if either tier drifts from the
 * manifest.
 */
import type { BridgeClient } from "./bridge";

export const SERVICES = {
  import: "latticenet.sub-store/import",
  subscriptions: "latticenet.sub-store/subscriptions",
  convert: "latticenet.sub-store/convert",
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
  // ── embedded engine (proposed; un-declared until TASK-0002 lands) ───────
  subscriptionsList: binding(SERVICES.subscriptions, "list", "pending"),
  subscriptionsPreview: binding(SERVICES.subscriptions, "preview", "pending"),
  subscriptionsCreate: binding(SERVICES.subscriptions, "create", "pending"),
  subscriptionsUpdate: binding(SERVICES.subscriptions, "update", "pending"),
  subscriptionsDelete: binding(SERVICES.subscriptions, "delete", "pending"),
  subscriptionsRefresh: binding(SERVICES.subscriptions, "refresh", "pending"),
  convertTargets: binding(SERVICES.convert, "targets", "pending"),
  convertPreview: binding(SERVICES.convert, "preview", "pending"),
  convertRun: binding(SERVICES.convert, "convert", "pending"),
} as const satisfies Record<string, MethodBinding>;

/** Host output cap is 1 MiB per invocation; keep UI-side headroom for the envelope. */
export const OUTPUT_SIZE_BUDGET_BYTES = 950_000;

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

// ── embedded-engine shapes (provisional — see module header) ───────────────

export interface SubscriptionSummary {
  name: string;
  display_name: string;
  source: string;
  /** Redacted URL hint (e.g. host + trailing characters); never the full URL. */
  url_hint: string;
  node_count?: number;
  last_refresh_at?: string;
  last_error?: string;
}

export interface SubscriptionListResponse {
  subscriptions: SubscriptionSummary[];
}

export interface SubscriptionPreviewResponse {
  node_count: number;
  node_types: Record<string, number>;
  sample_names: string[];
  warnings: string[];
}

export interface SubscriptionMutationResponse {
  subscription: SubscriptionSummary;
}

export interface SubscriptionRefreshResponse {
  node_count: number;
  changed: boolean;
}

export interface ConvertTarget {
  id: string;
  label: string;
  produces: string;
}

export interface ConvertTargetsResponse {
  targets: ConvertTarget[];
}

export interface ConvertPreviewResponse {
  node_count: number;
  groups: string[];
  warnings: string[];
  size_estimate_bytes: number;
}

export interface ConvertRunResponse {
  content: string;
  content_type: string;
  file_name: string;
  size_bytes: number;
}

/** Typed call through the bridge; every UI data path funnels through here. */
export function callMethod<T>(
  bridge: BridgeClient,
  target: MethodBinding,
  payload: unknown,
  timeoutMs?: number,
): { promise: Promise<T>; cancel: () => void } {
  return bridge.call<T>(target.service, target.method, payload, timeoutMs);
}
