import { computed, ref } from "vue";

import {
  BINDINGS,
  callMethod,
  MAX_SUBSCRIPTION_INLINE_BYTES,
  MAX_SUBSCRIPTION_RECORDS,
  SOURCE_VPN_CORE,
  SOURCE_REMOTE,
  SOURCE_LOCAL,
  FAILURE_STRICT,
  KIND_SUB,
  KIND_COLLECTION,
  KIND_FILE,
  FILE_TYPE_CONFIG,
  FILE_TYPE_PLAIN,
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
  /** KIND_SUB, KIND_COLLECTION or KIND_FILE. */
  kind: string;
  name: string;
  remark: string;
  tags: string[];
  /** "" for url/content, or SOURCE_VPN_CORE for the live node export. */
  source: string;
  vpnIdentity: string;
  url: string;
  content: string;
  ua: string;
  target: string;
  /** Collection inputs. */
  members: string[];
  memberTags: string[];
  /** Collections only. */
  failureMode: string;
  /** Files only: FILE_TYPE_CONFIG or FILE_TYPE_PLAIN. */
  fileType: string;
  /** Files only: the sub or collection whose nodes fill the document. */
  nodeSource: string;
  /** The ordered chain, including disabled steps. */
  process: unknown[];
}

/**
 * A storage key derived from the name.
 *
 * Upstream Sub-Store keys everything by name, which is why renaming a
 * subscription there breaks the URL that points at it. Keeping a derived,
 * immutable id gives the same "you only type a name" experience without that
 * consequence: a share published against a subscription survives a rename.
 */
export function slugify(name: string): string {
  const base = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48);
  return base || "subscription";
}

/** The derived id, suffixed until it does not collide with an existing one. */
export function uniqueId(name: string, taken: readonly string[]): string {
  const base = slugify(name);
  const used = new Set(taken);
  if (!used.has(base)) return base;
  for (let n = 2; n < 1000; n += 1) {
    const candidate = `${base}-${n}`;
    if (!used.has(candidate)) return candidate;
  }
  return `${base}-${used.size + 1}`;
}

/** The chain minus its disabled steps: what the engine would actually run. */
export function enabledSteps(draft: SubscriptionDraft): unknown[] {
  return draft.process.filter(
    (step) => !(step && typeof step === "object" && (step as { disabled?: boolean }).disabled),
  );
}

/** A stored kind the editor knows how to render, defaulting to a plain sub. */
export function knownKind(kind: string | undefined): string {
  if (kind === KIND_COLLECTION) return KIND_COLLECTION;
  if (kind === KIND_FILE) return KIND_FILE;
  return KIND_SUB;
}

export function emptyDraft(): SubscriptionDraft {
  return {
    id: "",
    kind: KIND_SUB,
    name: "",
    remark: "",
    tags: [],
    source: "",
    vpnIdentity: "",
    url: "",
    content: "",
    ua: "",
    target: "",
    members: [],
    memberTags: [],
    failureMode: FAILURE_STRICT,
    fileType: FILE_TYPE_CONFIG,
    nodeSource: "",
    process: [],
  };
}

export function draftFromRecord(record: SubscriptionRecord): SubscriptionDraft {
  return {
    id: record.id,
    kind: knownKind(record.kind),
    name: record.name ?? "",
    remark: record.remark ?? "",
    tags: Array.isArray(record.tags) ? [...record.tags] : [],
    source: record.source ?? "",
    vpnIdentity: record.vpn_identity ?? "",
    url: record.url ?? "",
    content: record.content ?? "",
    ua: record.ua ?? "",
    target: record.target ?? "",
    members: Array.isArray(record.members) ? [...record.members] : [],
    memberTags: Array.isArray(record.member_tags) ? [...record.member_tags] : [],
    failureMode: record.failure_mode || FAILURE_STRICT,
    fileType: record.file_type === FILE_TYPE_PLAIN ? FILE_TYPE_PLAIN : FILE_TYPE_CONFIG,
    nodeSource: record.node_source ?? "",
    process: Array.isArray(record.process) ? [...record.process] : [],
  };
}

/**
 * A subscription needs somewhere for its content to come from. Without either
 * a provider URL or inline content there is nothing to render, and the failure
 * would only surface later as a subscription that serves nothing — which the
 * core turns into a bodiless 404, giving the operator no clue why.
 */
export function validateDraft(draft: SubscriptionDraft): string {
  // The name is what the operator types; the id is derived from it. Asking for
  // both was asking for a detail with no decision attached to it.
  if (!draft.name.trim()) return "Give it a name.";
  // Byte length, not character count: the backend limit is bytes and content
  // full of non-ASCII names would otherwise pass here and fail there. A client
  // configuration is the likeliest thing to reach the cap, so this runs for
  // every kind rather than only for pasted nodes.
  const bytes = new TextEncoder().encode(draft.content).length;
  if (bytes > MAX_SUBSCRIPTION_INLINE_BYTES) {
    return `Inline content is ${Math.round(bytes / 1024)} KB; the limit is ${MAX_SUBSCRIPTION_INLINE_BYTES / 1024} KB.`;
  }
  // A file is the document itself. Without one there is nothing to serve, and
  // a node source alone produces a proxy list with no config around it.
  if (draft.kind === KIND_FILE) {
    if (draft.source === SOURCE_REMOTE) {
      if (!draft.url.trim()) return "Paste the link the template is fetched from.";
      return "";
    }
    if (!draft.content.trim()) {
      return draft.fileType === FILE_TYPE_PLAIN
        ? "Write the text you want served."
        : "Paste the client configuration this file is built from.";
    }
    return "";
  }
  // A collection is defined by what it gathers, not by a source of its own.
  if (draft.kind === KIND_COLLECTION) {
    if (draft.members.length === 0 && draft.memberTags.length === 0) {
      return "Choose at least one subscription, or a tag to gather by.";
    }
    return "";
  }
  if (draft.source === SOURCE_REMOTE && !draft.url.trim()) {
    return "Paste the provider's subscription link.";
  }
  if (draft.source === SOURCE_LOCAL && !draft.content.trim()) {
    return "Paste the nodes you want served.";
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
      const collection = draft.kind === KIND_COLLECTION;
      const file = draft.kind === KIND_FILE;
      // On create the id is derived here rather than typed; on edit it is
      // carried through untouched, because a share already points at it.
      const id =
        draft.id.trim() ||
        uniqueId(draft.name, items.value.map((item) => item.id));
      const record: SubscriptionRecord = {
        id,
        kind: collection ? KIND_COLLECTION : file ? KIND_FILE : undefined,
        name: draft.name.trim() || draft.id.trim(),
        remark: draft.remark.trim() || undefined,
        tags: draft.tags.length ? draft.tags : undefined,
        // Source and membership are mutually exclusive; the backend clears the
        // wrong set anyway, but sending them would state two answers to "where
        // does this get its content".
        source: collection ? undefined : draft.source || undefined,
        vpn_identity:
          !collection && !file && draft.source === SOURCE_VPN_CORE
            ? draft.vpnIdentity.trim() || undefined
            : undefined,
        url: collection || draft.source === SOURCE_LOCAL ? undefined : draft.url.trim() || undefined,
        content: collection || draft.source === SOURCE_REMOTE ? undefined : draft.content || undefined,
        ua: collection ? undefined : draft.ua.trim() || undefined,
        members: collection && draft.members.length ? draft.members : undefined,
        member_tags: collection && draft.memberTags.length ? draft.memberTags : undefined,
        failure_mode: collection ? draft.failureMode : undefined,
        // A file is served as its own document, so a client target would be a
        // second answer to what shape it comes out in.
        target: file ? undefined : draft.target.trim() || undefined,
        file_type: file ? draft.fileType : undefined,
        // Plain text has no proxy list to fill, so a node source on it would be
        // a stored setting with no effect.
        node_source:
          file && draft.fileType !== FILE_TYPE_PLAIN ? draft.nodeSource.trim() || undefined : undefined,
        process: draft.process.length ? draft.process : undefined,
      };
      const response = await callMethod<SubscriptionSaveResponse>(host.bridge, BINDINGS.subSave, {
        subscription: record,
      }).promise;
      if (!response.saved) {
        actionError.value = "The backend did not confirm the save.";
        return false;
      }
      notice.value = `Saved ${record.name}.`;
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
        // The wire field is still `operators` — it is what the engine takes.
        // Disabled steps are dropped here so the preview shows what would
        // actually run, not what the chain would do if everything were on.
        operators: enabledSteps(draft).length ? enabledSteps(draft) : undefined,
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
