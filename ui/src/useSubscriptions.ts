import { computed, ref } from "vue";

import {
  BINDINGS,
  callMethod,
  MAX_SUBSCRIPTION_INLINE_BYTES,
  MAX_SUBSCRIPTION_RECORDS,
  type OperatorCatalogResponse,
  type OperatorInfo,
  type SubscriptionDeleteResponse,
  type SubscriptionFetchResponse,
  type SubscriptionGetResponse,
  type SubscriptionListItem,
  type SubscriptionListResponse,
  type SubscriptionPreviewResponse,
  type SubscriptionRecord,
  type SubscriptionSaveResponse,
} from "./client";
import type { HostContext } from "./host";
import { safeErrorMessage } from "./subStoreModel";

export type LoadState = "idle" | "loading" | "ready" | "error";

export interface SubscriptionDraft {
  id: string;
  name: string;
  url: string;
  content: string;
  ua: string;
  target: string;
  operators: unknown[];
}

export function emptyDraft(): SubscriptionDraft {
  return { id: "", name: "", url: "", content: "", ua: "", target: "", operators: [] };
}

export function draftFromRecord(record: SubscriptionRecord): SubscriptionDraft {
  return {
    id: record.id,
    name: record.name ?? "",
    url: record.url ?? "",
    content: record.content ?? "",
    ua: record.ua ?? "",
    target: record.target ?? "",
    operators: Array.isArray(record.operators) ? [...record.operators] : [],
  };
}

/**
 * A subscription needs somewhere for its content to come from. Without either
 * a provider URL or inline content there is nothing to render, and the failure
 * would only surface later as a subscription that serves nothing — which the
 * core turns into a bodiless 404, giving the operator no clue why.
 */
export function validateDraft(draft: SubscriptionDraft): string {
  if (!draft.id.trim()) return "An id is required.";
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(draft.id.trim())) {
    return "Ids may use letters, digits, dot, dash and underscore, and must start with a letter or digit.";
  }
  if (!draft.url.trim() && !draft.content.trim()) {
    return "Give the subscription a provider URL or some inline content — otherwise it has nothing to serve.";
  }
  // Byte length, not character count: the backend limit is bytes and a
  // subscription full of non-ASCII names would otherwise pass here and fail
  // there.
  const bytes = new TextEncoder().encode(draft.content).length;
  if (bytes > MAX_SUBSCRIPTION_INLINE_BYTES) {
    return `Inline content is ${Math.round(bytes / 1024)} KB; the limit is ${MAX_SUBSCRIPTION_INLINE_BYTES / 1024} KB.`;
  }
  return "";
}

/**
 * State for the Subscriptions tab.
 *
 * The backend for this shipped signed and deployed with no caller at all, so
 * every method here was reachable in production and unreachable from a human.
 * Availability still degrades per binding: an older signed bundle without
 * `save`/`delete` renders the list read-only rather than throwing on click.
 */
export function useSubscriptions(host: HostContext) {
  const state = ref<LoadState>("idle");
  const items = ref<SubscriptionListItem[]>([]);
  const loadError = ref("");
  const actionError = ref("");
  const notice = ref("");
  const saving = ref(false);
  const busyId = ref<string | null>(null);

  const operators = ref<OperatorInfo[]>([]);
  const preview = ref<SubscriptionPreviewResponse | null>(null);
  const previewing = ref(false);

  const available = computed(() => host.available(BINDINGS.subList));
  const canMutate = computed(() => host.available(BINDINGS.subSave) && host.available(BINDINGS.subDelete));
  const canFetch = computed(() => host.available(BINDINGS.subFetch));
  const canPreview = computed(() => host.available(BINDINGS.subPreview));
  const atRecordLimit = computed(() => items.value.length >= MAX_SUBSCRIPTION_RECORDS);

  async function load(): Promise<void> {
    if (!host.bridge || !available.value) return;
    const silent = state.value === "ready";
    if (!silent) state.value = "loading";
    loadError.value = "";
    try {
      const response = await callMethod<SubscriptionListResponse>(host.bridge, BINDINGS.subList, {}).promise;
      items.value = response.subscriptions ?? [];
      state.value = "ready";
    } catch (cause) {
      if (!silent) state.value = "error";
      loadError.value = safeErrorMessage(cause, "Subscriptions could not be loaded");
    } finally {
      await host.resize();
    }
  }

  async function loadOperators(): Promise<void> {
    if (!host.bridge || !host.available(BINDINGS.subOperators) || operators.value.length > 0) return;
    try {
      const response = await callMethod<OperatorCatalogResponse>(host.bridge, BINDINGS.subOperators, {}).promise;
      operators.value = response.operators ?? [];
    } catch {
      // A missing catalogue costs the editor its operator hints and nothing
      // else, so it is not worth an error banner over the whole tab.
      operators.value = [];
    }
  }

  /** Full record including content and operators — `list` omits both. */
  async function get(id: string): Promise<SubscriptionRecord | null> {
    if (!host.bridge || !host.available(BINDINGS.subGet)) return null;
    actionError.value = "";
    busyId.value = id;
    try {
      const response = await callMethod<SubscriptionGetResponse>(host.bridge, BINDINGS.subGet, {
        subscription_id: id,
      }).promise;
      return response.subscription ?? null;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Subscription could not be read");
      return null;
    } finally {
      busyId.value = null;
      await host.resize();
    }
  }

  async function save(draft: SubscriptionDraft): Promise<boolean> {
    if (!host.bridge || !canMutate.value || saving.value) return false;
    const invalid = validateDraft(draft);
    if (invalid) {
      actionError.value = invalid;
      return false;
    }
    saving.value = true;
    actionError.value = "";
    notice.value = "";
    try {
      // `origin` is deliberately not sent: it records that a record came from a
      // migration, and the backend preserves or clears it rather than trusting
      // a caller. Sending it would be ignored anyway; omitting it says so.
      const record: SubscriptionRecord = {
        id: draft.id.trim(),
        name: draft.name.trim() || draft.id.trim(),
        url: draft.url.trim() || undefined,
        content: draft.content || undefined,
        ua: draft.ua.trim() || undefined,
        target: draft.target.trim() || undefined,
        operators: draft.operators.length ? draft.operators : undefined,
      };
      const response = await callMethod<SubscriptionSaveResponse>(host.bridge, BINDINGS.subSave, {
        subscription: record,
      }).promise;
      if (!response.saved) {
        actionError.value = "The backend did not confirm the save.";
        return false;
      }
      notice.value = `Saved ${record.id}.`;
      await load();
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Subscription could not be saved");
      return false;
    } finally {
      saving.value = false;
      await host.resize();
    }
  }

  async function remove(id: string): Promise<boolean> {
    if (!host.bridge || !canMutate.value) return false;
    actionError.value = "";
    notice.value = "";
    busyId.value = id;
    try {
      const response = await callMethod<SubscriptionDeleteResponse>(host.bridge, BINDINGS.subDelete, {
        subscription_id: id,
      }).promise;
      if (!response.deleted) {
        actionError.value = "The backend did not confirm the deletion.";
        return false;
      }
      // Deleting the definition does not retract anything already published.
      notice.value = `Deleted ${id}. A share published for it still exists — remove it in the dashboard.`;
      await load();
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Subscription could not be deleted");
      return false;
    } finally {
      busyId.value = null;
      await host.resize();
    }
  }

  async function refresh(id: string): Promise<boolean> {
    if (!host.bridge || !canFetch.value) return false;
    actionError.value = "";
    notice.value = "";
    busyId.value = id;
    try {
      const response = await callMethod<SubscriptionFetchResponse>(host.bridge, BINDINGS.subFetch, {
        subscription_id: id,
      }).promise;
      if (response.error) {
        // A failed fetch is not a failed subscription: the server keeps the
        // last good snapshot and clients stay working. Say both things.
        actionError.value = `Provider fetch failed: ${response.error}. The last good snapshot is still being served.`;
        return false;
      }
      notice.value = `Fetched ${response.bytes} bytes for ${id}.`;
      return true;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Subscription could not be refreshed");
      return false;
    } finally {
      busyId.value = null;
      await host.resize();
    }
  }

  async function runPreview(draft: SubscriptionDraft): Promise<void> {
    if (!host.bridge || !canPreview.value || previewing.value) return;
    previewing.value = true;
    actionError.value = "";
    preview.value = null;
    try {
      // Sending the draft rather than the id previews unsaved edits; the
      // backend falls back to the stored record when raw is empty.
      const response = await callMethod<SubscriptionPreviewResponse>(host.bridge, BINDINGS.subPreview, {
        subscription_id: draft.id.trim(),
        raw: draft.content,
        target: draft.target.trim(),
        operators: draft.operators.length ? draft.operators : undefined,
      }).promise;
      preview.value = response;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Preview failed");
    } finally {
      previewing.value = false;
      await host.resize();
    }
  }

  function clearMessages(): void {
    actionError.value = "";
    notice.value = "";
  }

  return {
    state,
    items,
    loadError,
    actionError,
    notice,
    saving,
    busyId,
    operators,
    preview,
    previewing,
    available,
    canMutate,
    canFetch,
    canPreview,
    atRecordLimit,
    load,
    loadOperators,
    get,
    save,
    remove,
    refresh,
    runPreview,
    clearMessages,
  };
}
