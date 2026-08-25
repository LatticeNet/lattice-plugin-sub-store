import { computed, ref } from "vue";

import {
  BINDINGS,
  callMethod,
  MAX_SUBSCRIPTION_INLINE_BYTES,
  MAX_SUBSCRIPTION_RECORDS,
  SOURCE_VPN_CORE,
  SOURCE_VPN_CORE_GRAPH,
  SOURCE_REMOTE,
  SOURCE_LOCAL,
  FAILURE_STRICT,
  KIND_SUB,
  KIND_COLLECTION,
  KIND_FILE,
  FILE_TYPE_CONFIG,
  FILE_TYPE_PLAIN,
  FILE_TYPE_SCRIPT,
  type OperatorCatalogResponse,
  type OperatorInfo,
  type GraphOptionsResponse,
  type SubscriptionDeleteResponse,
  type SubscriptionFetchResponse,
  type SubscriptionPublishResponse,
  type SubscriptionGetResponse,
  type SubscriptionListItem,
  type SubscriptionListResponse,
  type SubscriptionPreviewNode,
  type SubscriptionPreviewResponse,
  type SubscriptionRecord,
  type SubscriptionRenderResponse,
  type SubscriptionSaveResponse,
} from "./client";
import { filePreviewSupport } from "./filePreview";
import type { HostContext } from "./host";
import { safeErrorMessage } from "./subStoreModel";

export type LoadState = "idle" | "loading" | "ready" | "error";

export interface SubscriptionDraft {
  id: string;
  /** KIND_SUB, KIND_COLLECTION or KIND_FILE. */
  kind: string;
  name: string;
  /**
   * What the lists show instead of the name.
   *
   * Every list already preferred it and nothing could set it, so a record
   * imported from Sub-Store displayed a name its operator had no way to change.
   */
  displayName: string;
  remark: string;
  tags: string[];
  /** "" for url/content, or SOURCE_VPN_CORE for the live node export. */
  source: string;
  vpnIdentity: string;
  entryRoots: string[];
  optionsVersion: string;
  url: string;
  content: string;
  ua: string;
  target: string;
  /** Collection inputs. */
  members: string[];
  memberTags: string[];
  /** Collections only. */
  failureMode: string;
  /** Files only: FILE_TYPE_CONFIG, FILE_TYPE_PLAIN or FILE_TYPE_SCRIPT. */
  fileType: string;
  /** Files only: serve with a filename so a client saves it. */
  download: boolean;
  /** Script files only: URL parameters the program may read. */
  queryParams: string[];
  /** Script files only: `$arguments`. */
  argumentsText: string;
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

/** A display name that no other record is using, suffixed until it is free. */
export function uniqueName(base: string, taken: readonly string[]): string {
  const used = new Set(taken);
  if (!used.has(base)) return base;
  for (let n = 2; n < 1000; n += 1) {
    const candidate = `${base} ${n}`;
    if (!used.has(candidate)) return candidate;
  }
  return `${base} ${used.size + 1}`;
}

/** The chain minus its disabled steps: what the engine would actually run. */
export function enabledSteps(draft: SubscriptionDraft): unknown[] {
  return draft.process.filter(
    (step) => !(step && typeof step === "object" && (step as { disabled?: boolean }).disabled),
  );
}

/**
 * The enabled node-stage chain for a draft preview, including an explicit
 * empty chain.
 *
 * `upTo` cuts the chain after one step, which is how the editor answers the
 * question a long chain always raises: which step dropped my nodes? Upstream
 * can only preview the whole chain, so this is ours. The engine already
 * accepts an arbitrary operator array, so the cut costs nothing but the
 * slice. The index counts positions in the FULL chain (what the operator
 * sees in the list), and disabled and response-stage steps are removed after
 * the cut so the count on screen keeps matching the list.
 */
function previewOperators(draft: SubscriptionDraft, upTo?: number): unknown[] {
  const chain = typeof upTo === "number" ? draft.process.slice(0, upTo + 1) : draft.process;
  return chain
    .filter((step) => !(step && typeof step === "object" && (step as { disabled?: boolean }).disabled))
    .filter(
      (step) => !(step && typeof step === "object" && (step as { type?: string }).type === "Response Transformer"),
    );
}

/** A stored file type the editor knows how to render. */
export function knownFileType(fileType: string | undefined): string {
  if (fileType === FILE_TYPE_PLAIN) return FILE_TYPE_PLAIN;
  if (fileType === FILE_TYPE_SCRIPT) return FILE_TYPE_SCRIPT;
  return FILE_TYPE_CONFIG;
}

/**
 * `$arguments` as one editable block, `name = value` per line.
 *
 * A key/value grid would be more clicks for something operators paste in and out
 * of a script's own comments, where it already looks like this.
 */
export function argumentsToText(args: Record<string, string> | undefined): string {
  if (!args) return "";
  return Object.entries(args)
    .map(([key, value]) => `${key} = ${value}`)
    .join("\n");
}

export function parseArguments(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const split = trimmed.indexOf("=");
    if (split <= 0) continue;
    const key = trimmed.slice(0, split).trim();
    if (key) out[key] = trimmed.slice(split + 1).trim();
  }
  return out;
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
    displayName: "",
    remark: "",
    tags: [],
    source: "",
    vpnIdentity: "",
    entryRoots: [],
    optionsVersion: "",
    url: "",
    content: "",
    ua: "",
    target: "",
    members: [],
    memberTags: [],
    failureMode: FAILURE_STRICT,
    fileType: FILE_TYPE_CONFIG,
    nodeSource: "",
    download: false,
    queryParams: [],
    argumentsText: "",
    process: [],
  };
}

export function draftFromRecord(record: SubscriptionRecord): SubscriptionDraft {
  return {
    id: record.id,
    kind: knownKind(record.kind),
    name: record.name ?? "",
    displayName: record.display_name ?? "",
    remark: record.remark ?? "",
    tags: Array.isArray(record.tags) ? [...record.tags] : [],
    source: record.source ?? "",
    vpnIdentity: record.vpn_identity ?? "",
    entryRoots: Array.isArray(record.entry_roots) ? [...record.entry_roots] : [],
    optionsVersion: record.graph_options_version ?? "",
    url: record.url ?? "",
    content: record.content ?? "",
    ua: record.ua ?? "",
    target: record.target ?? "",
    members: Array.isArray(record.members) ? [...record.members] : [],
    memberTags: Array.isArray(record.member_tags) ? [...record.member_tags] : [],
    failureMode: record.failure_mode || FAILURE_STRICT,
    fileType: knownFileType(record.file_type),
    nodeSource: record.node_source ?? "",
    download: Boolean(record.download),
    queryParams: Array.isArray(record.query_params) ? [...record.query_params] : [],
    argumentsText: argumentsToText(record.arguments),
    process: Array.isArray(record.process) && record.process.length > 0
      ? [...record.process]
      : Array.isArray(record.operators)
        ? [...record.operators]
        : [],
  };
}

/**
 * A subscription needs somewhere for its content to come from. Without either
 * a provider URL or inline content there is nothing to render, and the failure
 * would only surface later as a subscription that serves nothing, which the
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
      if (draft.fileType === FILE_TYPE_PLAIN) return "Write the text you want served.";
      if (draft.fileType === FILE_TYPE_SCRIPT) return "Paste the script that builds this file.";
      return "Paste the client configuration this file is built from.";
    }
    // A program that calls produceArtifact with nothing declared fails at
    // request time with an error only the operator's logs would show.
    if (draft.fileType === FILE_TYPE_SCRIPT && !draft.nodeSource.trim()) {
      if (/produceArtifact\s*\(/.test(draft.content)) {
        return "This script asks for nodes, choose the subscription or combination it should get them from.";
      }
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
  if (draft.source === SOURCE_VPN_CORE_GRAPH) {
    if (!draft.vpnIdentity.trim()) return "Choose an eligible VPN identity.";
    if (draft.entryRoots.length === 0) return "Choose at least one eligible graph root.";
    if (new Set(draft.entryRoots).size !== draft.entryRoots.length) return "Graph roots must be unique.";
    if (!draft.optionsVersion) return "Reload graph options before saving.";
  }
  return "";
}

export function reconcileGraphDraftOptions(
  draft: SubscriptionDraft,
  options: GraphOptionsResponse,
  adopt: boolean,
): { stale: boolean; removedRoots: number } {
  const stale = draft.optionsVersion !== options.options_version;
  if (!adopt) return { stale, removedRoots: 0 };
  draft.optionsVersion = options.options_version;
  if (!options.identities.some((item) => item.selectable && item.id === draft.vpnIdentity)) {
    draft.vpnIdentity = "";
  }
  const allowed = new Set(options.roots
    .filter((item) => item.selectable && item.eligible_identity_ids.includes(draft.vpnIdentity))
    .map((item) => item.line_uuid));
  const before = draft.entryRoots.length;
  draft.entryRoots = draft.entryRoots.filter((root) => allowed.has(root));
  return { stale, removedRoots: before - draft.entryRoots.length };
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
  /** A failed background reload: the rows on screen are the last good ones. */
  const staleError = ref("");
  /** Whether the operator catalogue is still coming, or is simply not there. */
  const operatorsState = ref<LoadState>("idle");
  const actionError = ref("");
  /** A preview failure, kept apart so it can be shown where the preview goes. */
  const previewError = ref("");
  const notice = ref("");
  const saving = ref(false);
  const busyId = ref<string | null>(null);

  const operators = ref<OperatorInfo[]>([]);
  const preview = ref<SubscriptionPreviewResponse | null>(null);
  const previewing = ref(false);
  /** Which step the current preview stopped after, or null for the whole chain. */
  const previewStep = ref<number | null>(null);
  const graphOptions = ref<GraphOptionsResponse | null>(null);
  const graphOptionsLoading = ref(false);

  const available = computed(() => host.available(BINDINGS.subList));
  const canMutate = computed(() => host.available(BINDINGS.subSave) && host.available(BINDINGS.subDelete));
  const canFetch = computed(() => host.available(BINDINGS.subProbe));
  const canPreview = computed(() => host.available(BINDINGS.subPreview));
  // Resolving a source the caller named is substore:admin, so it is a separate
  // binding and a separate question from whether preview works at all.
  const canPreviewDraft = computed(() => host.available(BINDINGS.subPreviewDraft));
  /** Render is what produces the document a client receives. It is also the
   *  only path that answers for the files preview refuses. */
  const canRender = computed(() => host.available(BINDINGS.subRender));
  const canPublish = computed(() => host.available(BINDINGS.subPublish));
  const canLoadGraphOptions = computed(() => host.available(BINDINGS.subGraphOptions));
  const atRecordLimit = computed(() => items.value.length >= MAX_SUBSCRIPTION_RECORDS);

  /**
   * A reload that fails behind a successful write must not replace the list.
   *
   * `load()` runs again after save, delete and refresh. When that trailing read
   * failed it used to set `loadError`, and the screen keys its error state off
   * that alone, so a write that SUCCEEDED could blank the rows the operator
   * still had and tell them the list could not be loaded. A silent reload now
   * reports through `staleError`, which the screen shows as a strip above rows
   * that are still exactly what the server last sent.
   */
  async function load(): Promise<void> {
    if (!host.bridge || !available.value) return;
    const silent = state.value === "ready";
    if (!silent) state.value = "loading";
    loadError.value = "";
    staleError.value = "";
    try {
      const response = await callMethod<SubscriptionListResponse>(host.bridge, BINDINGS.subList, {}).promise;
      items.value = response.subscriptions ?? [];
      state.value = "ready";
    } catch (cause) {
      const message = safeErrorMessage(cause, "Subscriptions could not be loaded");
      if (silent) {
        staleError.value = message;
      } else {
        state.value = "error";
        loadError.value = message;
      }
    } finally {
      await host.resize();
    }
  }

  async function loadOperators(): Promise<void> {
    if (!host.bridge || !host.available(BINDINGS.subOperators) || operators.value.length > 0) return;
    operatorsState.value = "loading";
    try {
      const response = await callMethod<OperatorCatalogResponse>(host.bridge, BINDINGS.subOperators, {}).promise;
      operators.value = response.operators ?? [];
      operatorsState.value = "ready";
    } catch {
      // A missing catalogue costs the editor its operator hints and nothing
      // else, so it is not worth an error banner over the whole tab, but the
      // chain must be able to say "unavailable" instead of "loading" forever.
      operators.value = [];
      operatorsState.value = "error";
    }
  }

  async function loadGraphOptions(): Promise<boolean> {
    if (!host.bridge || !canLoadGraphOptions.value || graphOptionsLoading.value) return false;
    graphOptionsLoading.value = true;
    actionError.value = "";
    try {
      const response = await callMethod<GraphOptionsResponse>(host.bridge, BINDINGS.subGraphOptions, {}).promise;
      if (!response.ok || response.schema_version !== 1 || !response.options_version) {
        throw new Error("The server returned a graph option set this page cannot trust, so the editor was left closed rather than shown with stale choices.");
      }
      graphOptions.value = {
        ...response,
        identities: response.identities.map((item) => ({ ...item })),
        roots: response.roots.map((item) => ({ ...item, eligible_identity_ids: [...item.eligible_identity_ids] })),
      };
      return true;
    } catch (cause) {
      graphOptions.value = null;
      actionError.value = safeErrorMessage(cause, "Graph options could not be loaded");
      return false;
    } finally {
      graphOptionsLoading.value = false;
      await host.resize();
    }
  }

  /** Full record including content and operators, `list` omits both. */
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
    if (draft.source === SOURCE_VPN_CORE_GRAPH) {
      const options = graphOptions.value;
      const identity = options?.identities.find((item) => item.id === draft.vpnIdentity && item.selectable);
      const eligible = new Set(options?.roots.filter((item) => item.selectable && item.eligible_identity_ids.includes(draft.vpnIdentity)).map((item) => item.line_uuid));
      if (!options || options.options_version !== draft.optionsVersion || !identity || draft.entryRoots.some((root) => !eligible.has(root))) {
        actionError.value = "Graph options changed. Reload and review the identity and roots before saving.";
        return false;
      }
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
        display_name: draft.displayName.trim() || undefined,
        remark: draft.remark.trim() || undefined,
        tags: draft.tags.length ? draft.tags : undefined,
        // Source and membership are mutually exclusive; the backend clears the
        // wrong set anyway, but sending them would state two answers to "where
        // does this get its content".
        source: collection ? undefined : draft.source || undefined,
        vpn_identity:
          !collection && !file && (draft.source === SOURCE_VPN_CORE || draft.source === SOURCE_VPN_CORE_GRAPH)
            ? draft.vpnIdentity.trim() || undefined
            : undefined,
        entry_roots:
          !collection && !file && draft.source === SOURCE_VPN_CORE_GRAPH
            ? [...draft.entryRoots]
            : undefined,
        graph_options_version:
          !collection && !file && draft.source === SOURCE_VPN_CORE_GRAPH
            ? draft.optionsVersion
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
        download: file && draft.download ? true : undefined,
        // Plain text has no proxy list to fill, so a node source on it would be
        // a stored setting with no effect.
        node_source:
          file && draft.fileType !== FILE_TYPE_PLAIN ? draft.nodeSource.trim() || undefined : undefined,
        // Only a program reads these, and only a program can be confused by
        // them surviving a type change.
        query_params:
          file && draft.fileType === FILE_TYPE_SCRIPT && draft.queryParams.length
            ? draft.queryParams
            : undefined,
        arguments:
          file && draft.fileType === FILE_TYPE_SCRIPT && draft.argumentsText.trim()
            ? parseArguments(draft.argumentsText)
            : undefined,
        process: draft.process.length ? draft.process : undefined,
      };
      const response = await callMethod<SubscriptionSaveResponse>(host.bridge, BINDINGS.subSave, {
        subscription: record,
      }).promise;
      if (!response.saved) {
        actionError.value = "The server did not confirm the save, so this record may or may not have been written. Reload the list before saving again.";
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
        actionError.value = "The server did not confirm the deletion, so this record may or may not still exist. Reload the list before deleting again.";
        return false;
      }
      // Deleting the definition does not retract anything already published.
      notice.value = `Deleted ${id}. Deleting the definition does not retract anything already published: if a share exists for it, remove that in the dashboard under Networking.`;
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
      const response = await callMethod<SubscriptionFetchResponse>(host.bridge, BINDINGS.subProbe, {
        subscription_id: id,
      }).promise;
      if (!response.ok) {
        // A failed fetch is not a failed subscription: the server keeps the
        // last good snapshot and clients stay working. Say both things.
        actionError.value = "The provider refresh failed and changed nothing. Clients keep getting whatever this record already had, which may be nothing.";
        return false;
      }
      notice.value =
        typeof response.bytes === "number"
          ? `Checked ${response.bytes} bytes for ${id}${response.source_version ? ` at ${response.source_version}` : ""}.`
          : `Refreshed ${id}.`;
      return true;
    } catch (cause) {
      actionError.value = `${safeErrorMessage(cause, "Subscription could not be refreshed")}. The refresh did not complete, so nothing about this record changed.`;
      return false;
    } finally {
      busyId.value = null;
      // The core may have moved a record's bookkeeping since the list was
      // read, reload so the row shows the current status rather than what it
      // said before. Best-effort: a reload failure must not replace the
      // check's own (already reported) outcome with an unhandled rejection.
      try {
        await load();
        await host.resize();
      } catch {
        /* the list keeps its last data; the next load retries */
      }
    }
  }

  async function publish(id: string, destination: string, method: string, format: string): Promise<boolean> {
    if (!host.bridge || !canPublish.value || !id.trim()) return false;
    actionError.value = "";
    notice.value = "";
    busyId.value = id;
    try {
      const response = await callMethod<SubscriptionPublishResponse>(host.bridge, BINDINGS.subPublish, {
        subscription_id: id,
        destination: destination.trim(),
        method: method.trim().toUpperCase() || "PUT",
        format: format.trim() || "plain",
      }).promise;
      if (response.subscription_id !== id || response.status_code < 200 || response.status_code >= 300) {
        throw new Error(
          `the publish call came back with status ${response.status_code} for record ${response.subscription_id || "(none)"}`,
        );
      }
      notice.value = `Published ${response.bytes} bytes for ${id}. The destination accepted them; whether anything downstream serves them is not visible from here.`;
      return true;
    } catch (cause) {
      // The cause is what separates "the destination refused the credentials"
      // from "the host is unreachable". Discarding it made every failure read
      // the same and left the operator with nothing to act on.
      actionError.value = `Publish failed: ${safeErrorMessage(cause, "the destination did not accept it")}. The saved definition and destination were not changed.`;
      return false;
    } finally {
      busyId.value = null;
      await host.resize();
    }
  }

  /**
   * The row-level glance: the first few node names a record produces, without
   * opening the editor.
   *
   * One open popover at a time, tracked here rather than per row, because the
   * state is genuinely singular, two rows never need comparing, and two open
   * panels would each need their own loading and error handling for no gain.
   */
  interface RowPreview {
    id: string;
    loading: boolean;
    error: string;
    nodes: SubscriptionPreviewNode[];
    count: number;
    /** Set for a file record: its preview is the served document, not nodes. */
    document?: string;
    truncated?: boolean;
  }

  const rowPreview = ref<RowPreview | null>(null);

  async function toggleRowPreview(id: string): Promise<void> {
    if (rowPreview.value?.id === id) {
      rowPreview.value = null;
      await host.resize();
      return;
    }
    if (!host.bridge) return;
    // A file whose document needs a node source, a fetch, a program or a chain
    // is refused by preview and always has been: those are host-capable and
    // preview is signed for two host calls. Render is the call that answers,
    // and it answers with the same thing the drawer shows, the served
    // document, so the row asks the one that can work rather than relaying a
    // backend refusal the UI could see coming.
    const row = items.value.find((item) => item.id === id);
    const viaRender = !filePreviewSupport(row).supported && canRender.value;
    if (!viaRender && !canPreview.value) return;
    rowPreview.value = { id, loading: true, error: "", nodes: [], count: 0 };
    await host.resize();
    try {
      if (viaRender) {
        const served = await callMethod<SubscriptionRenderResponse>(host.bridge, BINDINGS.subRender, {
          subscription_id: id,
          format: "plain",
        }).promise;
        if (rowPreview.value?.id !== id) return;
        rowPreview.value = {
          id,
          loading: false,
          error: "",
          nodes: [],
          count: 0,
          document: served?.content ?? "",
        };
        return;
      }
      // No raw and no operators: the backend previews the stored record with
      // its own chain, fetching first when the record has no inline content.
      const response = await callMethod<SubscriptionPreviewResponse>(
        host.bridge,
        BINDINGS.subPreview,
        { subscription_id: id },
      ).promise;
      // The operator may have moved to another row while this was in flight;
      // landing the answer now would label it with the wrong record.
      if (rowPreview.value?.id !== id) return;
      const nodes = response.nodes ?? [];
      rowPreview.value = {
        id,
        loading: false,
        error: "",
        nodes: nodes.slice(0, 5),
        count: response.node_count ?? nodes.length,
        document: response.document,
        truncated: response.truncated,
      };
    } catch (cause) {
      if (rowPreview.value?.id !== id) return;
      rowPreview.value = {
        id,
        loading: false,
        error: safeErrorMessage(cause, viaRender ? "The document could not be rendered" : "Preview failed"),
        nodes: [],
        count: 0,
      };
    } finally {
      await host.resize();
    }
  }

  async function runPreview(draft: SubscriptionDraft, upTo?: number): Promise<void> {
    if (!host.bridge || !canPreview.value || previewing.value) return;
    previewing.value = true;
    actionError.value = "";
    previewError.value = "";
    preview.value = null;
    previewStep.value = typeof upTo === "number" ? upTo : null;
    try {
      const operators = previewOperators(draft, upTo);
      // Sending the draft rather than the id previews unsaved edits; the
      // backend falls back to the stored record when raw is empty. A draft
      // whose source is the fleet or a provider link has no pasted content at
      // all, so its source goes along and the engine resolves it live (read
      // only; nothing is persisted as a refresh). A graph draft's authority is
      // its selection alone, so nothing else is sent for it.
      const draftSource =
        !draft.source || draft.source === SOURCE_LOCAL || draft.source === SOURCE_VPN_CORE_GRAPH
          ? {}
          : draft.source === SOURCE_VPN_CORE
            ? {
                source: draft.source,
                vpn_identity: draft.vpnIdentity.trim() || undefined,
              }
            : {
                source: draft.source,
                url: draft.url.trim() || undefined,
                ua: draft.ua.trim() || undefined,
              };
      // Naming a source is naming a host for the control plane to read, which
      // is substore:admin. Only that shape goes to the admin method; a preview
      // of stored or pasted content stays on the read-scoped one, so a
      // read-only operator keeps the preview they are entitled to.
      const namesASource = Object.values(draftSource).some((value) => value !== undefined);
      if (namesASource && !canPreviewDraft.value) {
        // Also on the preview channel, which is the pane the button now lives
        // in. Sent to the action channel alone it landed at the bottom of the
        // form, so the pane went on saying nothing had run yet while the reason
        // it had not sat somewhere the click never looked.
        const refusal =
          "Previewing an unsaved draft resolves the source you named, which needs admin access. Save the subscription first, or preview pasted content.";
        actionError.value = refusal;
        previewError.value = refusal;
        return;
      }
      const response = await callMethod<SubscriptionPreviewResponse>(
        host.bridge,
        namesASource ? BINDINGS.subPreviewDraft : BINDINGS.subPreview,
        {
          subscription_id: draft.id.trim(),
          raw: draft.source === SOURCE_VPN_CORE_GRAPH ? undefined : draft.content,
          target: draft.target.trim(),
          // Explicit draft authority must not fall back to the stored process.
          // Node preview excludes both disabled and response-stage steps.
          operators,
          graph_selection: draft.source === SOURCE_VPN_CORE_GRAPH ? {
            schema_version: 1,
            options_version: draft.optionsVersion,
            identity_id: draft.vpnIdentity,
            entry_roots: [...draft.entryRoots],
          } : undefined,
          ...draftSource,
        },
      ).promise;
      preview.value = response;
    } catch (cause) {
      // Reported where the preview would have appeared, not only on the save
      // row: since the control moved into the preview pane, a failure that
      // only surfaced at the bottom of a long form was a failure the operator
      // could press the button for and never see.
      previewError.value = safeErrorMessage(cause, "Preview failed");
      actionError.value = previewError.value;
    } finally {
      previewing.value = false;
      await host.resize();
    }
  }

  /**
   * Copy a record under a new name.
   *
   * Fifteen files that are variations of one another is the normal shape of a
   * real deployment, and building each from scratch means re-pasting a 60 KB
   * generator every time. The copy is a full read-then-write rather than a
   * shallow clone of the list row, because the list deliberately omits content
   * and a copy made from it would be empty.
   */
  async function duplicate(id: string): Promise<string | null> {
    if (!host.bridge || !canMutate.value) return null;
    const record = await get(id);
    if (!record) return null;
    // The NAME has to be unique too, not only the id. Copying twice produced
    // two rows reading "Home nodes copy", which is a list an operator cannot
    // act on. The id that distinguishes them is not shown.
    const name = uniqueName(`${record.name || id} copy`, items.value.map((item) => item.name));
    const copy: SubscriptionRecord = {
      ...record,
      id: uniqueId(name, items.value.map((item) => item.id)),
      name,
      // A copy has not been imported from anywhere; carrying the origin would
      // claim a provenance it does not have.
      origin: undefined,
      // Nor has it ever been fetched: the source's refresh bookkeeping would
      // claim a freshness the copy has not earned.
      last_fetch_at: undefined,
      last_fetch_ok: undefined,
      last_error: undefined,
      userinfo: undefined,
    };
    saving.value = true;
    actionError.value = "";
    notice.value = "";
    try {
      const response = await callMethod<SubscriptionSaveResponse>(host.bridge, BINDINGS.subSave, {
        subscription: copy,
      }).promise;
      if (!response.saved) {
        actionError.value = "The server did not confirm the copy, so the new record may or may not have been written. Reload the list before copying again.";
        return null;
      }
      notice.value = `Copied to ${name}.`;
      await load();
      return copy.id;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, "Record could not be copied");
      return null;
    } finally {
      saving.value = false;
      await host.resize();
    }
  }

  function clearMessages(): void {
    actionError.value = "";
    previewError.value = "";
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
    previewError,
    previewing,
    previewStep,
    staleError,
    operatorsState,
    graphOptions,
    graphOptionsLoading,
    rowPreview,
    available,
    canMutate,
    canFetch,
    canPreview,
    canPreviewDraft,
    canRender,
    canPublish,
    canLoadGraphOptions,
    atRecordLimit,
    load,
    loadOperators,
    loadGraphOptions,
    get,
    save,
    remove,
    duplicate,
    refresh,
    publish,
    runPreview,
    toggleRowPreview,
    clearMessages,
  };
}
