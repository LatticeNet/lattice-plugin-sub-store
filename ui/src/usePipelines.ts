import { computed, ref } from "vue";

import {
  BINDINGS,
  callMethod,
  type ConversionResult,
  type PipelineDeleteResponse,
  type PipelineListItem,
  type PipelineListResponse,
  type PipelineRecord,
  type PipelineSaveResponse,
} from "./client";
import type { HostContext } from "./host";
import { safeErrorMessage } from "./subStoreModel";

export type LoadState = "idle" | "loading" | "ready" | "error";

export interface PipelineDraft {
  id: string;
  name?: string;
  target: string;
  operators?: unknown[];
}

/**
 * State for the Pipelines tab: record lifecycle plus run. Pipelines are named
 * conversion recipes (target + operator chain) stored server-side; running one
 * converts operator-supplied raw subscription content through the record's
 * recipe. Everything degrades to `available === false` when the manifest does
 * not declare the engine service.
 */
export function usePipelines(host: HostContext) {
  const state = ref<LoadState>("idle");
  const items = ref<PipelineListItem[]>([]);
  const loadError = ref("");
  const actionError = ref("");
  const saving = ref(false);
  const busyId = ref<string | null>(null);

  const available = computed(() => host.available(BINDINGS.engineListPipelines));
  const canMutate = computed(
    () => host.available(BINDINGS.engineSavePipeline) && host.available(BINDINGS.engineDeletePipeline),
  );
  const canRun = computed(() => host.available(BINDINGS.engineRunPipeline));

  async function load(): Promise<void> {
    if (!host.bridge || !available.value) return;
    const silent = state.value === "ready";
    if (!silent) state.value = "loading";
    loadError.value = "";
    try {
      const response = await callMethod<PipelineListResponse>(host.bridge, BINDINGS.engineListPipelines, {}).promise;
      items.value = response.records ?? [];
      state.value = "ready";
    } catch (cause) {
      if (!silent) state.value = "error";
      loadError.value = safeErrorMessage(cause, "Pipelines could not be loaded");
    } finally {
      await host.resize();
    }
  }

  async function save(draft: PipelineDraft): Promise<boolean> {
    if (!host.bridge || !canMutate.value || saving.value) return false;
    saving.value = true;
    actionError.value = "";
    try {
      const record: PipelineRecord = {
        id: draft.id,
        name: draft.name || draft.id,
        target: draft.target,
        operators: draft.operators,
      };
      await callMethod<PipelineSaveResponse>(host.bridge, BINDINGS.engineSavePipeline, record).promise;
      // Re-fetch: the server owns normalization and the canonical list order.
      await load();
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Pipeline could not be saved");
      return false;
    } finally {
      saving.value = false;
      await host.resize();
    }
  }

  async function get(id: string): Promise<PipelineRecord | undefined> {
    if (!host.bridge || !host.available(BINDINGS.engineGetPipeline)) return undefined;
    try {
      const response = await callMethod<{ found: boolean; record?: PipelineRecord }>(
        host.bridge,
        BINDINGS.engineGetPipeline,
        { id },
      ).promise;
      return response.found ? response.record : undefined;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Pipeline could not be read");
      return undefined;
    }
  }

  async function remove(id: string): Promise<boolean> {
    if (!host.bridge || !canMutate.value || busyId.value) return false;
    busyId.value = id;
    actionError.value = "";
    try {
      const response = await callMethod<PipelineDeleteResponse>(host.bridge, BINDINGS.engineDeletePipeline, { id }).promise;
      if (response.deleted) items.value = items.value.filter((item) => item.id !== id);
      return response.deleted;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Pipeline could not be deleted");
      return false;
    } finally {
      busyId.value = null;
      await host.resize();
    }
  }

  async function run(id: string, raw: string): Promise<ConversionResult | undefined> {
    if (!host.bridge || !canRun.value || busyId.value) return undefined;
    busyId.value = id;
    actionError.value = "";
    try {
      return await callMethod<ConversionResult>(
        host.bridge,
        BINDINGS.engineRunPipeline,
        { id, raw },
        60_000,
      ).promise;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Pipeline run failed");
      return undefined;
    } finally {
      busyId.value = null;
      await host.resize();
    }
  }

  return {
    state,
    items,
    loadError,
    actionError,
    saving,
    busyId,
    available,
    canMutate,
    canRun,
    load,
    save,
    get,
    remove,
    run,
  };
}
