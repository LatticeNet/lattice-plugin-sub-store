/**
 * usePipeline.ts, the pipeline model bound to what this session has read.
 *
 * pipeline.ts is pure; this is where it meets the three shared reads (the
 * record catalogue, the host's share list, the node counts) and a clock. Every
 * layer that prints a record's state calls it, so the overview's dot, the
 * table's state cell, the side panel and the record page cannot disagree.
 */
import { computed, getCurrentScope, onScopeDispose, ref } from "vue";

import { KIND_FILE, type SubscriptionListItem } from "./client";
import type { HostContext } from "./host";
import { nodeCountLabel, nodeCountTitle } from "./nodeCounts";
import {
  EXPIRY_WARN_DAYS,
  attentionItems,
  buildLineage,
  formatExpiry,
  recordHealth,
  shareLive,
  type RecordHealth,
} from "./pipeline";
import { shareStateOf } from "./shareState";
import { recordCatalogue } from "./useSubscriptions";
import { useNodeCounts } from "./useNodeCounts";
import { useShares } from "./useShares";
import { BINDINGS } from "./client";
import type { ChipFacts } from "./components/LineageMap.vue";

export function usePipeline(host: HostContext) {
  const catalogue = recordCatalogue(host);
  const shareStore = useShares(host);
  const counts = useNodeCounts(host);

  /** A clock for the relative phrases, moved once a minute. */
  const now = ref(Date.now());
  const timer = setInterval(() => {
    now.value = Date.now();
  }, 60_000);
  if (getCurrentScope()) onScopeDispose(() => clearInterval(timer));

  const ready = computed(() => catalogue.state.value === "ready");
  const items = computed<SubscriptionListItem[]>(() => (ready.value ? catalogue.items.value : []));
  const byId = computed(() => new Map(items.value.map((item) => [item.id, item])));
  const shares = shareStore.shares;
  const canPreview = computed(() => host.available(BINDINGS.subPreview));

  const lineage = computed(() => buildLineage(items.value, shares.value));
  const attention = computed(() =>
    attentionItems({
      items: items.value,
      shares: shares.value,
      sharesError: shareStore.error.value,
      lineage: lineage.value,
      now: now.value,
    }),
  );

  function item(id: string): SubscriptionListItem | undefined {
    return byId.value.get(id);
  }

  /** How this session's preview of the record went, when one has answered. */
  function previewOf(id: string): { ok: boolean; reason?: string } | undefined {
    const state = counts.stateOf(id);
    if (state?.status === "ready") return { ok: true };
    if (state?.status === "failed") return { ok: false, reason: state.reason };
    return undefined;
  }

  function health(id: string): RecordHealth {
    const record = item(id);
    if (!record) return { tone: "neutral", label: "gone", title: "This record is not in the store." };
    return recordHealth(record, lineage.value, shares.value, now.value, previewOf(id));
  }

  /** "86 → 78", "…" while counting, "?" when nothing answered. Files have none. */
  function nodes(id: string): string {
    const record = item(id);
    if (!record || record.kind === KIND_FILE) return "";
    return nodeCountLabel(counts.stateOf(id));
  }

  function nodesTitle(id: string): string {
    return nodeCountTitle(counts.stateOf(id), canPreview.value, now.value);
  }

  /** Count what is not counted yet, in the order given; files are skipped. */
  function requestCounts(ids: readonly string[]): void {
    if (!canPreview.value) return;
    counts.request(ids.filter((id) => item(id) && item(id)!.kind !== KIND_FILE));
  }

  /** What the map's chip for a node shows. */
  function chipFacts(id: string): ChipFacts {
    const node = lineage.value.nodes.get(id);
    if (node?.share) {
      // Expired serves nothing: broken. Switched off is a choice: neutral.
      // Live but running out inside the attention window: warning.
      const share = node.share;
      const state = shareStateOf(share, now.value);
      const at = share.expires_at ? Date.parse(share.expires_at) : Number.NaN;
      const soon = Number.isFinite(at) && at > now.value && at - now.value <= EXPIRY_WARN_DAYS * 86_400_000;
      const tone: ChipFacts["tone"] =
        state.label === "expired" ? "error" : state.label === "disabled" ? "neutral" : soon ? "warning" : "healthy";
      return {
        tone,
        state: soon ? `${state.label}, ${formatExpiry({ expire: Math.floor(at / 1000) }, now.value)}` : state.label,
        figure: share.default_format || "",
        title: state.title,
      };
    }
    const state = health(id);
    const figure = nodes(id).replace(" → ", "→");
    return { tone: state.tone, state: state.label, figure, title: [state.title, figure ? nodesTitle(id) : ""].filter(Boolean).join(" ") };
  }

  /** The live shares for a record, first one first. */
  function liveShares(id: string) {
    return (shares.value ?? []).filter((share) => share.subscription_id === id && shareLive(share, now.value));
  }

  return {
    catalogue,
    shareStore,
    counts,
    now,
    ready,
    items,
    shares,
    lineage,
    attention,
    item,
    health,
    nodes,
    nodesTitle,
    requestCounts,
    chipFacts,
    liveShares,
    canPreview,
  };
}

export type Pipeline = ReturnType<typeof usePipeline>;
