/**
 * useRecordChain.ts, one record read in full and its chain accounted for.
 *
 * The record page's Nodes and Steps tabs need the same three things: the
 * stored record (its chain, its link), a preview of the whole chain, and what
 * each enabled operation kept. The list used to compute this inline for the
 * row it expanded; the page and the list now share it. One partial run per
 * enabled operation answers "which step dropped my nodes", and a combination,
 * whose operations run over its members' merged output, is answered by one
 * whole run.
 */
import { computed, ref, shallowRef, type Ref } from "vue";

import { cutChain, enabledStepIndexes, explainChain, type ChainExplanation } from "./chainExplain";
import { BINDINGS, callMethod, KIND_COLLECTION, KIND_FILE, KIND_SUB, type SubscriptionPreviewResponse, type SubscriptionRecord } from "./client";
import type { ChainStep } from "./components/ProcessChain.vue";
import type { HostContext } from "./host";
import { t } from "./i18n";
import type { NodeCountQueue } from "./nodeCounts";
import { safeErrorMessage } from "./subStoreModel";
import type { UseSubscriptions } from "./useSubscriptions";

export interface RecordChain {
  id: Ref<string>;
  loading: Ref<boolean>;
  error: Ref<string>;
  record: Ref<SubscriptionRecord | null>;
  explanation: Ref<ChainExplanation | null>;
  /** The chain position being previewed right now. */
  running: Ref<number | null>;
  steps: Ref<ChainStep[]>;
  isCombination: Ref<boolean>;
  load: (id: string) => Promise<void>;
  clear: () => void;
}

export function useRecordChain(host: HostContext, subs: UseSubscriptions, counts: NodeCountQueue): RecordChain {
  const id = ref("");
  const loading = ref(false);
  const error = ref("");
  const record = shallowRef<SubscriptionRecord | null>(null);
  const explanation = shallowRef<ChainExplanation | null>(null);
  const running = ref<number | null>(null);

  const steps = computed<ChainStep[]>(() => {
    const current = record.value;
    if (!current) return [];
    const process = Array.isArray(current.process) && current.process.length ? current.process : current.operators;
    return (Array.isArray(process) ? process : []) as ChainStep[];
  });
  const isCombination = computed(() => (record.value?.kind || KIND_SUB) === KIND_COLLECTION);

  function clear(): void {
    id.value = "";
    loading.value = false;
    error.value = "";
    record.value = null;
    explanation.value = null;
    running.value = null;
  }

  async function load(target: string): Promise<void> {
    clear();
    id.value = target;
    loading.value = true;
    const current = () => id.value === target;
    const read = await subs.get(target);
    if (!current()) return;
    loading.value = false;
    if (!read) {
      error.value = subs.actionError.value || t.boot.recordUnread;
      subs.actionError.value = "";
      return;
    }
    record.value = read;
    const bridge = host.bridge;
    // A file previews as a document, not a node set; its page reads the
    // document on the Output tab instead.
    if (!bridge || !subs.canPreview.value || read.kind === KIND_FILE) return;
    const chain = steps.value;
    try {
      if (isCombination.value || !enabledStepIndexes(chain).length) {
        const result = await callMethod<SubscriptionPreviewResponse>(bridge, BINDINGS.subPreview, { subscription_id: target }).promise;
        if (!current()) return;
        explanation.value = { deltas: [], droppedBy: new Map(), final: result, complete: true };
        counts.record(target, result);
        return;
      }
      const explained = await explainChain(chain, async (upTo) => {
        if (!current()) throw new Error("moved on");
        running.value = upTo;
        try {
          return await callMethod<SubscriptionPreviewResponse>(bridge, BINDINGS.subPreview, {
            subscription_id: target,
            operators: cutChain(chain, upTo),
          }).promise;
        } catch (cause) {
          // explainChain stops at the first run that throws and keeps what it
          // learned; the reason is ours to report.
          if (current()) error.value = safeErrorMessage(cause, t.subs.previewFailed);
          throw cause;
        } finally {
          running.value = null;
        }
      });
      if (!current()) return;
      explanation.value = explained;
      if (explained.complete && explained.final) counts.record(target, explained.final);
    } catch (cause) {
      if (current()) error.value = safeErrorMessage(cause, t.subs.previewFailed);
    }
  }

  return { id, loading, error, record, explanation, running, steps, isCombination, load, clear };
}
