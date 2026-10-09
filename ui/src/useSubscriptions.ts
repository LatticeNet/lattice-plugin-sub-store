import { cutChain } from "./chainExplain";
import type { ChainStep } from "./components/ProcessChain.vue";
import { computed, ref, watch, type Ref } from "vue";

import {
  BINDINGS,
  callMethod,
  errorCodeOf,
  ERROR_REGEX_INCOMPATIBLE,
  ERROR_STORE_MIGRATION_REQUIRED,
  MIGRATE_STORE_CHUNK,
  MIGRATE_STORE_MAX_CALLS,
  migrationProgress,
  STORE_VERSION_LEGACY,
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
  type MigrateStoreResponse,
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
  type SubscriptionReorderResponse,
  type SubscriptionSaveResponse,
  type SubscriptionSaveConflict,
} from "./client";
import { inStoreOrder, withOrder } from "./recordOrder";
import { regexDiagnostics, type RegexDiagnostic } from "./regexRewrite";
import { conflictChanges, conflictSummary, type FieldChange } from "./recordConflict";
import { deletedNotice } from "./recordActions";
import { filePreviewSupport } from "./filePreview";
import type { HostContext } from "./host";
import { formatBytes, t } from "./i18n";
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
  return cutChain(draft.process as ChainStep[], upTo);
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
  if (!draft.name.trim()) return t.draft.giveName;
  // Byte length, not character count: the backend limit is bytes and content
  // full of non-ASCII names would otherwise pass here and fail there. A client
  // configuration is the likeliest thing to reach the cap, so this runs for
  // every kind rather than only for pasted nodes.
  const bytes = new TextEncoder().encode(draft.content).length;
  if (bytes > MAX_SUBSCRIPTION_INLINE_BYTES) {
    return t.draft.tooLarge(bytes, MAX_SUBSCRIPTION_INLINE_BYTES);
  }
  // A file is the document itself. Without one there is nothing to serve, and
  // a node source alone produces a proxy list with no config around it.
  if (draft.kind === KIND_FILE) {
    if (draft.source === SOURCE_REMOTE) {
      if (!draft.url.trim()) return t.draft.templateLink;
      return "";
    }
    if (!draft.content.trim()) {
      if (draft.fileType === FILE_TYPE_PLAIN) return t.draft.plainText;
      if (draft.fileType === FILE_TYPE_SCRIPT) return t.draft.script;
      return t.draft.config;
    }
    // A program that calls produceArtifact with nothing declared fails at
    // request time with an error only the operator's logs would show.
    if (draft.fileType === FILE_TYPE_SCRIPT && !draft.nodeSource.trim()) {
      if (/produceArtifact\s*\(/.test(draft.content)) {
        return t.draft.scriptSource;
      }
    }
    return "";
  }
  // A collection is defined by what it gathers, not by a source of its own.
  if (draft.kind === KIND_COLLECTION) {
    if (draft.members.length === 0 && draft.memberTags.length === 0) {
      return t.draft.members;
    }
    return "";
  }
  if (draft.source === SOURCE_REMOTE && !draft.url.trim()) {
    return t.draft.providerLink;
  }
  if (draft.source === SOURCE_LOCAL && !draft.content.trim()) {
    return t.draft.nodes;
  }
  if (draft.source === SOURCE_VPN_CORE_GRAPH) {
    if (!draft.vpnIdentity.trim()) return t.draft.identity;
    if (draft.entryRoots.length === 0) return t.draft.root;
    if (new Set(draft.entryRoots).size !== draft.entryRoots.length) return t.draft.uniqueRoots;
    if (!draft.optionsVersion) return t.draft.reloadOptions;
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
/**
 * The record catalogue, shared by everything that reads it.
 *
 * Both screens call this hook and both are kept alive, so a per-instance list
 * meant the same records were fetched twice and could disagree after a write
 * on the other tab. The catalogue is one copy; everything else in this hook —
 * the draft being edited, its preview, its errors — stays per instance,
 * because two editors are genuinely two editors.
 */
interface Catalogue {
  state: Ref<LoadState>;
  items: Ref<SubscriptionListItem[]>;
  loadError: Ref<string>;
  /** A failed background reload: the rows on screen are the last good ones. */
  staleError: Ref<string>;
  /**
   * The store layout the last list reported: 1 legacy single document, 2
   * split index, undefined from a runtime before S1.
   */
  storeVersion: Ref<number | undefined>;
  /** The order the store last accepted, which a refused reorder puts back. */
  confirmedOrder: string[];
  /** Reorders go out one at a time, in the order the operator made them. */
  reorderQueue: Promise<unknown>;
  /** Moves made before a refused reorder are dropped rather than sent after it. */
  reorderGeneration: number;
  /** A read already in flight is joined, not repeated: the shell and the
   *  visible lens both ask for the list when the handshake lands. */
  inFlight: Promise<void> | null;
}

/**
 * One catalogue per host, not one per module: a module-level singleton would
 * be shared by every test in a file as well, so a record left behind by one
 * case would arrive in the next. A host is a plugin instance, which is exactly
 * the scope the records belong to.
 */
const catalogues = new WeakMap<object, Catalogue>();

function catalogueFor(host: HostContext): Catalogue {
  const existing = catalogues.get(host);
  if (existing) return existing;
  const fresh: Catalogue = {
    state: ref<LoadState>("idle"),
    items: ref<SubscriptionListItem[]>([]),
    loadError: ref(""),
    staleError: ref(""),
    storeVersion: ref<number | undefined>(undefined),
    confirmedOrder: [],
    reorderQueue: Promise.resolve(),
    reorderGeneration: 0,
    inFlight: null,
  };
  catalogues.set(host, fresh);
  return fresh;
}

/**
 * Read the catalogue from the host. A first read shows the loading state; a
 * reload behind rows already on screen is silent, and if it fails the rows
 * stay and `staleError` says they are the last good read.
 */
function readCatalogue(host: HostContext, catalogue: Catalogue): Promise<void> {
  if (catalogue.inFlight) return catalogue.inFlight;
  if (!host.bridge || !host.available(BINDINGS.subList)) return Promise.resolve();
  catalogue.inFlight = readCatalogueOnce(host, catalogue).finally(() => {
    catalogue.inFlight = null;
  });
  return catalogue.inFlight;
}

async function readCatalogueOnce(host: HostContext, catalogue: Catalogue): Promise<void> {
  const { state, items, loadError, staleError } = catalogue;
  if (!host.bridge) return;
  const silent = state.value === "ready";
  if (!silent) state.value = "loading";
  loadError.value = "";
  staleError.value = "";
  try {
    const response = await callMethod<SubscriptionListResponse>(host.bridge, BINDINGS.subList, {}).promise;
    items.value = inStoreOrder(response.subscriptions ?? []);
    catalogue.storeVersion.value = typeof response.store_version === "number" ? response.store_version : undefined;
    catalogue.confirmedOrder = items.value.map((item) => item.id);
    state.value = "ready";
  } catch (cause) {
    const message = safeErrorMessage(cause, t.subs.loadFailed);
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

/** Records as they were last read, for readers that do not edit them. The
 *  shell's Refresh reads them again through `reload`. */
export function recordCatalogue(host: HostContext) {
  const catalogue = catalogueFor(host);
  const { state, items, loadError, storeVersion } = catalogue;
  return { state, items, loadError, storeVersion, reload: () => readCatalogue(host, catalogue) };
}

export function useSubscriptions(host: HostContext) {
  const catalogue = catalogueFor(host);
  const { state, items, loadError, staleError, storeVersion } = catalogue;
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
  /** Which operation the current preview stopped after, or null for the whole chain. */
  const previewStep = ref<number | null>(null);
  /** What the preview could not do as asked, and did instead. */
  const previewNote = ref("");
  /** The record most recently read for editing, so a draft can be compared
   *  with what the server holds for it. */
  const lastRead = ref<SubscriptionRecord | null>(null);
  /**
   * The refused save, if the last one was refused as stale.
   *
   * Held rather than flattened into `actionError` because the screen renders it
   * as a decision with buttons, not as a sentence: overwrite, reopen, or leave
   * it alone. Cleared by the next save attempt and by reopening the record.
   */
  const saveConflict = ref<{
    conflict: SubscriptionSaveConflict;
    changes: FieldChange[];
    summary: string;
    /** The record the operator tried to write, kept so Overwrite can retry it. */
    attempted: SubscriptionRecord;
  } | null>(null);
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
  /** `reorder` and `migrate_store` are signed with the S1 manifest; pending until then. */
  const canReorder = computed(() => canMutate.value && host.available(BINDINGS.subReorder));
  const canMigrateStore = computed(() => canMutate.value && host.available(BINDINGS.subMigrateStore));
  const atRecordLimit = computed(() => items.value.length >= MAX_SUBSCRIPTION_RECORDS);

  /**
   * Why the last save was refused, when the reason is one the editor can act
   * on: a pattern the native engine cannot run (`regex_incompatible`). The
   * diagnostics are read from the draft the save carried, so the editor can
   * name the step and offer the rewrite; `message` is what was put beside
   * Save, so it can be withdrawn once the chain no longer has the pattern.
   * Cleared by the next save.
   */
  const saveRefusal = ref<{ code: string; diagnostics: RegexDiagnostic[]; message: string } | null>(null);

  /**
   * The chain on screen no longer has what the last save was refused for, so
   * "Not saved: step 1 uses lookaround" beside Save is no longer true. Only
   * that message is withdrawn: a later failure (a preview, a conflict) put its
   * own message there and keeps it.
   */
  function settleRefusal(): void {
    const refusal = saveRefusal.value;
    if (refusal && actionError.value === refusal.message) actionError.value = "";
  }

  /**
   * A write refused because the store is still the legacy single document.
   * The list said so too if it was read since; this makes the migration
   * prompt appear for a store migrated away under the page as well.
   */
  function migrationRefusal(cause: unknown): string {
    if (errorCodeOf(cause) !== ERROR_STORE_MIGRATION_REQUIRED) return "";
    storeVersion.value = STORE_VERSION_LEGACY;
    return t.subs.legacyRefusal;
  }

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
  function load(): Promise<void> {
    return readCatalogue(host, catalogueFor(host));
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
        throw new Error(t.subs.untrustedOptions);
      }
      graphOptions.value = {
        ...response,
        identities: response.identities.map((item) => ({ ...item })),
        roots: response.roots.map((item) => ({ ...item, eligible_identity_ids: [...item.eligible_identity_ids] })),
      };
      return true;
    } catch (cause) {
      graphOptions.value = null;
      actionError.value = safeErrorMessage(cause, t.subs.optionsFailed);
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
      lastRead.value = response.subscription ?? null;
      return response.subscription ?? null;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, t.subs.readFailed);
      return null;
    } finally {
      busyId.value = null;
      await host.resize();
    }
  }

  /**
   * Persist a draft. `force` re-sends the same write without the revision
   * check, which is the operator's explicit "mine wins" after they have been
   * shown what they would be replacing. It is never the default and never
   * automatic.
   */
  async function save(draft: SubscriptionDraft, force = false): Promise<boolean> {
    if (!host.bridge || !canMutate.value || saving.value) return false;
    saveConflict.value = null;
    saveRefusal.value = null;
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
        actionError.value = t.editor.graphChanged;
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
      // The conditional write. `if_revision` is the fingerprint the record
      // carried when it was read, so the backend can refuse a save whose target
      // has moved since. Omitted when there is nothing to be stale against: a
      // create, or an edit of a record read by a build that returned no
      // revision, in which case the save behaves exactly as it always did.
      const ifRevision = force ? undefined : lastRead.value?.id === record.id ? lastRead.value.revision : undefined;
      const response = await callMethod<SubscriptionSaveResponse>(host.bridge, BINDINGS.subSave, {
        subscription: record,
        ...(ifRevision ? { if_revision: ifRevision } : {}),
      }).promise;
      if (!response.saved && response.conflict) {
        // Not an error: the store did exactly what it was asked to. The
        // operator now has a decision, and the screen renders it as one.
        const current = response.conflict.subscription ?? null;
        const changes = conflictChanges(lastRead.value, current, record);
        saveConflict.value = {
          conflict: response.conflict,
          changes,
          summary: response.conflict.reason === "deleted"
            ? t.subs.deletedWhileOpen
            : conflictSummary(changes),
          attempted: record,
        };
        return false;
      }
      if (!response.saved) {
        actionError.value = t.subs.saveUnconfirmed;
        return false;
      }
      notice.value = t.subs.saved(record.name);
      // The saved record comes back with its new revision. Keeping it as the
      // "last read" copy means a second save from the still-open editor is
      // checked against what was just written rather than against the copy
      // that is now one edit old, which would conflict with itself.
      if (response.subscription) lastRead.value = response.subscription;
      await load();
      return true;
    } catch (cause) {
      if (errorCodeOf(cause) === ERROR_REGEX_INCOMPATIBLE) {
        const diagnostics = regexDiagnostics(draft.process);
        const steps = [...new Set(diagnostics.map((entry) => entry.step))];
        const message = steps.length
          ? t.subs.regexRefused(steps)
          : t.subs.regexRefusedReason(safeErrorMessage(cause, t.subs.regexFallback));
        saveRefusal.value = { code: ERROR_REGEX_INCOMPATIBLE, diagnostics, message };
        actionError.value = message;
        return false;
      }
      actionError.value = migrationRefusal(cause) || safeErrorMessage(cause, t.subs.saveFailed);
      return false;
    } finally {
      saving.value = false;
      await host.resize();
    }
  }

  /**
   * The shares the last delete left serving nothing, while its notice is the
   * one on screen; a screen offers "Open in Publishing" beside it. Any other
   * notice clears them.
   */
  const brokenShares = ref<string[]>([]);
  let brokenNotice = "";
  watch(notice, (text) => {
    if (text !== brokenNotice) brokenShares.value = [];
  });

  /**
   * `ownShares` are the live shares that publish this record itself
   * (ownLiveShares), undefined when the share list is unread; the notice
   * names them.
   */
  async function remove(id: string, ownShares?: readonly string[]): Promise<boolean> {
    if (!host.bridge || !canMutate.value) return false;
    actionError.value = "";
    notice.value = "";
    busyId.value = id;
    try {
      const response = await callMethod<SubscriptionDeleteResponse>(host.bridge, BINDINGS.subDelete, {
        subscription_id: id,
      }).promise;
      if (!response.deleted) {
        actionError.value = t.subs.deleteUnconfirmed;
        return false;
      }
      // Deleting the definition does not retract anything already published.
      // Named as the operator knows it, not by its id.
      const gone = items.value.find((entry) => entry.id === id);
      const label = gone ? gone.display_name || gone.name : t.subs.theRecord;
      brokenNotice = deletedNotice(label, ownShares);
      brokenShares.value = [...(ownShares ?? [])];
      notice.value = brokenNotice;
      await load();
      return true;
    } catch (cause) {
      actionError.value = migrationRefusal(cause) || safeErrorMessage(cause, t.subs.deleteFailed);
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
        actionError.value = t.subs.refreshNoop;
        return false;
      }
      notice.value =
        typeof response.bytes === "number"
          ? t.subs.checked(formatBytes(response.bytes), id, response.source_version ?? "")
          : t.subs.refreshed(id);
      return true;
    } catch (cause) {
      actionError.value = t.subs.refreshFailed(safeErrorMessage(cause, t.subs.refreshFallback));
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
          t.subs.publishStatus(response.status_code, response.subscription_id || t.subs.noRecord),
        );
      }
      notice.value = t.subs.uploaded(formatBytes(response.bytes), id);
      return true;
    } catch (cause) {
      // The cause is what separates "the destination refused the credentials"
      // from "the host is unreachable". Discarding it made every failure read
      // the same and left the operator with nothing to act on.
      actionError.value = t.subs.uploadFailed(safeErrorMessage(cause, t.subs.uploadFallback));
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
        error: safeErrorMessage(cause, viaRender ? t.subs.renderFailed : t.subs.previewFailed),
        nodes: [],
        count: 0,
      };
    } finally {
      await host.resize();
    }
  }

  /** True when the draft's source is exactly what the server holds for it. */
  function sourceUnchanged(draft: SubscriptionDraft): boolean {
    const stored = lastRead.value;
    if (!stored || stored.id !== draft.id.trim()) return false;
    return (
      (stored.source ?? "") === draft.source &&
      (stored.url ?? "") === draft.url.trim() &&
      (stored.ua ?? "") === draft.ua.trim() &&
      (stored.vpn_identity ?? "") === draft.vpnIdentity.trim()
    );
  }

  async function runPreview(draft: SubscriptionDraft, upTo?: number): Promise<void> {
    if (!host.bridge || !canPreview.value || previewing.value) return;
    previewing.value = true;
    actionError.value = "";
    previewError.value = "";
    previewNote.value = "";
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
      let namesASource = Object.values(draftSource).some((value) => value !== undefined);
      let sourceFields: Record<string, string | undefined> = draftSource;
      if (namesASource && !canPreviewDraft.value) {
        // The read-scoped `preview` resolves a SAVED record's source itself:
        // naming the record is not naming a host, the admin already did that
        // when they saved it. So a draft whose source fields still match the
        // stored record previews through that path, with the draft's chain,
        // and only a source the operator changed or a record never saved is
        // out of reach here.
        if (draft.id.trim() && sourceUnchanged(draft)) {
          namesASource = false;
          sourceFields = {};
          previewNote.value = t.subs.savedSourceNote;
        } else {
          // Also on the preview channel, which is the pane the button lives
          // in. Sent to the action channel alone it landed at the bottom of
          // the form, so the pane went on saying nothing had run yet while the
          // reason it had not sat somewhere the click never looked.
          const refusal = t.subs.draftSourceRefused;
          actionError.value = refusal;
          previewError.value = refusal;
          return;
        }
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
          ...sourceFields,
        },
      ).promise;
      preview.value = response;
    } catch (cause) {
      // Reported where the preview would have appeared, not only on the save
      // row: since the control moved into the preview pane, a failure that
      // only surfaced at the bottom of a long form was a failure the operator
      // could press the button for and never see.
      previewError.value = safeErrorMessage(cause, t.subs.previewFailed);
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
    const name = uniqueName(t.subs.copyName(record.name || id), items.value.map((item) => item.name));
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
        actionError.value = t.subs.copyUnconfirmed;
        return null;
      }
      notice.value = t.subs.copied(name);
      await load();
      return copy.id;
    } catch (cause) {
      actionError.value = safeErrorMessage(cause, t.subs.copyFailed);
      return null;
    } finally {
      saving.value = false;
      await host.resize();
    }
  }

  /**
   * The store's new manual order, shown at once and saved behind it.
   *
   * `reorder` names every live id (s1-plan section 3.3), so the whole order
   * goes out each time. Moves are sent one after another in the order they
   * were made: a keyboard operator presses Arrow Up four times faster than four
   * calls return. A refusal puts back the order the store last accepted and
   * drops the moves queued behind it, which were made on top of the refused
   * one. The reason comes back for the live region; "" means saved.
   */
  function reorder(order: string[]): Promise<{ ok: boolean; reason: string; dropped: boolean }> {
    const bridge = host.bridge;
    if (!bridge || !canReorder.value) return Promise.resolve({ ok: false, reason: t.subs.reorderUnavailable, dropped: false });
    const generation = catalogue.reorderGeneration;
    items.value = withOrder(items.value, order);
    const run = catalogue.reorderQueue.then(async () => {
      if (generation !== catalogue.reorderGeneration) return { ok: false, reason: "", dropped: true };
      try {
        await callMethod<SubscriptionReorderResponse>(bridge, BINDINGS.subReorder, { ids: order }).promise;
        catalogue.confirmedOrder = [...order];
        return { ok: true, reason: "", dropped: false };
      } catch (cause) {
        catalogue.reorderGeneration += 1;
        items.value = withOrder(items.value, catalogue.confirmedOrder);
        return { ok: false, reason: migrationRefusal(cause) || safeErrorMessage(cause, t.subs.reorderRefused), dropped: false };
      }
    });
    catalogue.reorderQueue = run;
    return run;
  }

  /** Where a migration run stands, for the prompt that started it. */
  const migration = ref<{ running: boolean; migrated: number; remaining: number; error: string; done: boolean }>({
    running: false,
    migrated: 0,
    remaining: 0,
    error: "",
    done: false,
  });

  /**
   * Split a legacy store, one call at a time, until the runtime says done.
   *
   * A call copies up to MIGRATE_STORE_CHUNK records, fewer when their frames
   * reach the runtime's byte bound; once none remain, one call verifies and
   * later calls delete the legacy program keys (s1-plan section 3.1). Progress
   * lives in the store, so a run that stops resumes where it left off. A call
   * that changes nothing (no record copied, no verification, no program key
   * deleted) is a stall, so the call bound only has to cover the worst real
   * run: one call per record, the verify, and the program deletes.
   */
  async function migrateStore(): Promise<boolean> {
    const bridge = host.bridge;
    if (!bridge || !canMigrateStore.value || migration.value.running) return false;
    migration.value = { running: true, migrated: 0, remaining: 0, error: "", done: false };
    try {
      let previous = "";
      for (let call = 0; call < MIGRATE_STORE_MAX_CALLS; call += 1) {
        const reply = await callMethod<MigrateStoreResponse>(bridge, BINDINGS.subMigrateStore, { chunk: MIGRATE_STORE_CHUNK }).promise;
        migration.value.migrated += Math.max(0, reply.migrated ?? 0);
        migration.value.remaining = Math.max(0, reply.remaining ?? 0);
        if (reply.done) {
          migration.value.done = true;
          break;
        }
        const state = migrationProgress(reply);
        if (!reply.migrated && state === previous) throw new Error(t.subs.migrationStalled(reply.remaining));
        previous = state;
        await host.resize();
      }
      if (!migration.value.done) throw new Error(t.subs.migrationUnfinished);
      await load();
      return true;
    } catch (cause) {
      migration.value.error = safeErrorMessage(cause, t.subs.migrationFailed);
      return false;
    } finally {
      migration.value.running = false;
      await host.resize();
    }
  }

  function clearMessages(): void {
    clearErrors();
    notice.value = "";
  }

  /**
   * The failures raised on one screen, without the confirmations. Leaving the
   * editor has to drop its errors — they are about a draft that stops existing
   * — but a save reports success and then leaves, so clearing everything on the
   * way out threw away the only confirmation the save ever gave.
   */
  function clearErrors(): void {
    actionError.value = "";
    previewError.value = "";
    saveRefusal.value = null;
  }

  return {
    state,
    items,
    loadError,
    actionError,
    notice,
    brokenShares,
    saveConflict,
    saving,
    busyId,
    operators,
    preview,
    previewError,
    previewNote,
    previewing,
    previewStep,
    staleError,
    storeVersion,
    saveRefusal,
    settleRefusal,
    migration,
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
    canReorder,
    canMigrateStore,
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
    reorder,
    migrateStore,
    runPreview,
    toggleRowPreview,
    clearMessages,
    clearErrors,
  };
}

/** The one store both the list and the editor read; each is handed the same
 *  instance rather than constructing its own. */
export type UseSubscriptions = ReturnType<typeof useSubscriptions>;
