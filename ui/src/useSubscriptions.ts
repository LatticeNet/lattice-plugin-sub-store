import { computed, ref } from "vue";

import {
  BINDINGS,
  callMethod,
  type SubscriptionListResponse,
  type SubscriptionMutationResponse,
  type SubscriptionPreviewResponse,
  type SubscriptionRefreshResponse,
  type SubscriptionSummary,
} from "./client";
import type { HostContext } from "./host";
import { safeErrorMessage } from "./subStoreModel";

export type LoadState = "idle" | "loading" | "ready" | "error";

export interface CreateSubscriptionInput {
  name: string;
  displayName?: string;
  sourceUrl: string;
}

/**
 * State for the Subscriptions tab: list lifecycle plus per-row mutations.
 * Everything degrades to `available === false` when the signed manifest does
 * not declare the subscriptions service — the screen renders the engine
 * panel in that case and none of the actions can fire.
 */
export function useSubscriptions(host: HostContext) {
  const state = ref<LoadState>("idle");
  const items = ref<SubscriptionSummary[]>([]);
  const loadError = ref("");
  const actionError = ref("");
  const creating = ref(false);
  const busyName = ref<string | null>(null);

  const available = computed(() => host.available(BINDINGS.subscriptionsList));
  const canMutate = computed(
    () => host.available(BINDINGS.subscriptionsCreate) && host.available(BINDINGS.subscriptionsDelete),
  );

  async function load(): Promise<void> {
    if (!host.bridge || !available.value) return;
    const silent = state.value === "ready";
    if (!silent) state.value = "loading";
    loadError.value = "";
    try {
      const response = await callMethod<SubscriptionListResponse>(
        host.bridge,
        BINDINGS.subscriptionsList,
        {},
      ).promise;
      items.value = response.subscriptions ?? [];
      state.value = "ready";
    } catch (cause) {
      if (!silent) state.value = "error";
      loadError.value = safeErrorMessage(cause, "Subscriptions could not be loaded");
    } finally {
      await host.resize();
    }
  }

  async function create(input: CreateSubscriptionInput): Promise<boolean> {
    if (!host.bridge || !canMutate.value || creating.value) return false;
    creating.value = true;
    actionError.value = "";
    try {
      const response = await callMethod<SubscriptionMutationResponse>(
        host.bridge,
        BINDINGS.subscriptionsCreate,
        {
          name: input.name,
          display_name: input.displayName || undefined,
          source_url: input.sourceUrl,
        },
      ).promise;
      upsert(response.subscription);
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Subscription could not be added");
      return false;
    } finally {
      creating.value = false;
      await host.resize();
    }
  }

  async function refresh(name: string): Promise<SubscriptionRefreshResponse | undefined> {
    if (!host.bridge || !host.available(BINDINGS.subscriptionsRefresh) || busyName.value) return undefined;
    busyName.value = name;
    actionError.value = "";
    try {
      const response = await callMethod<SubscriptionRefreshResponse>(
        host.bridge,
        BINDINGS.subscriptionsRefresh,
        { name },
      ).promise;
      // Re-fetch rather than hand-patch: the server owns counts and timestamps.
      await load();
      return response;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Subscription could not be refreshed");
      return undefined;
    } finally {
      busyName.value = null;
      await host.resize();
    }
  }

  async function remove(name: string): Promise<boolean> {
    if (!host.bridge || !canMutate.value || busyName.value) return false;
    busyName.value = name;
    actionError.value = "";
    try {
      await callMethod(host.bridge, BINDINGS.subscriptionsDelete, { name }).promise;
      items.value = items.value.filter((item) => item.name !== name);
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Subscription could not be removed");
      return false;
    } finally {
      busyName.value = null;
      await host.resize();
    }
  }

  async function previewSource(sourceUrl: string): Promise<SubscriptionPreviewResponse | undefined> {
    if (!host.bridge || !host.available(BINDINGS.subscriptionsPreview)) return undefined;
    try {
      return await callMethod<SubscriptionPreviewResponse>(
        host.bridge,
        BINDINGS.subscriptionsPreview,
        { source_url: sourceUrl },
      ).promise;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Subscription source could not be parsed");
      return undefined;
    } finally {
      await host.resize();
    }
  }

  function upsert(summary: SubscriptionSummary): void {
    const index = items.value.findIndex((item) => item.name === summary.name);
    if (index >= 0) items.value.splice(index, 1, summary);
    else items.value = [summary, ...items.value];
  }

  return {
    state,
    items,
    loadError,
    actionError,
    creating,
    busyName,
    available,
    canMutate,
    load,
    create,
    refresh,
    remove,
    previewSource,
  };
}
