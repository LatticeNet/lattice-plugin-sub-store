import { computed, nextTick, ref, watch, type Component, type Ref } from "vue";
import { ClipboardPaste, Globe, Layers, Radar, Server } from "@lucide/vue";

import {
  BINDINGS,
  ERROR_CODES,
  KIND_COLLECTION,
  KIND_FILE,
  KIND_SUB,
  SOURCE_FLEET,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  SOURCE_VPN_CORE,
  SOURCE_VPN_CORE_GRAPH,
  callMethod,
  errorCodeOf,
  type SubscriptionListItem,
} from "./client";
import {
  FLEET_SAMPLE,
  LineRowCache,
  PREDICATE_FIELDS,
  STRUCTURED_FILTER_TYPE,
  fleetOptionsProblem,
  leadingRun,
  linkModeScriptSteps,
  normalizeFleetOptions,
  predicateProblem,
  probeUnavailable,
  problemText,
  readFilterArgs,
  unfinishedCondition,
  type BindPreviewReply,
  type FleetOptions,
  type FleetPreviewReply,
  type FleetPreviewRequest,
  type IdentitiesReply,
  type Identity,
} from "./fleet";
import { NETWORK_STEP_TYPES, type ScriptNetworkRefusal } from "./scriptNetwork";
import { safeErrorMessage } from "./subStoreModel";
import {
  applyCommonSettings,
  emptyCommonSettings,
  readCommonSettings,
  type CommonSettings as CommonSettingsShape,
} from "./commonSettings";
import { enabledStepIndexes, explainChain, type ChainExplanation } from "./chainExplain";
import type { ChainStep } from "./components/ProcessChain.vue";
import type { HostContext } from "./host";
import { t } from "./i18n";
import { overlayDepth } from "./overlayStack";
import { useEditorExit } from "./useEditorExit";
import {
  draftFromRecord,
  emptyDraft,
  reconcileGraphDraftOptions,
  validateDraft,
  type SubscriptionDraft,
  type UseSubscriptions,
} from "./useSubscriptions";

/** Types the common-settings block owns; the chain list hides them. */
const MANAGED_TYPES = ["Quick Setting Operator", "Useless Filter"] as const;

/**
 * A draft with the fleet record's binding rules (s2-plan section 2.1). The
 * rules ride on the draft so the unsaved-edit guard sees them and the save
 * can send them; SubscriptionDraft itself is the store's type.
 */
export type FleetDraft = SubscriptionDraft & { fleet?: FleetOptions };

/** How a fleet read stands: not asked, in flight, answered, failed, or out of this session's reach. */
export type FleetReadState = "idle" | "loading" | "ready" | "error" | "denied" | "unavailable";

export interface FleetReadError {
  code: string;
  message: string;
}

/** The two legacy sources: they serve line owner credentials (design-28.md:230). */
export function isLegacySource(source: string | undefined): boolean {
  return source === SOURCE_VPN_CORE || source === SOURCE_VPN_CORE_GRAPH;
}

/** A call refused for want of a scope, as the host or the gateway says it. */
export function isPermissionDenied(cause: unknown): boolean {
  const code = (cause as { code?: unknown } | null)?.code;
  if (code === "denied" || code === "forbidden") return true;
  const message = cause instanceof Error ? cause.message : "";
  return /\b(forbidden|permission denied|missing scope|vpncore:read)\b/i.test(message);
}

export interface SourceCard {
  id: string;
  readonly title: string;
  readonly detail: string;
  icon: Component;
  /** A legacy card: the source serves line owner credentials. */
  legacy?: boolean;
  /** Why the card cannot be chosen, or "" when it can. */
  readonly disabledReason: string;
}

/**
 * The fleet half of the record editor: the catalogue the selection reads,
 * the selection's counts and sample, the identity list, one identity's bind
 * preview and the row cache the line drawer reads. Every read goes through a
 * runtime proxy method on this plugin's own subscription service or through
 * core's bind service, because the bridge admits only methods this plugin's
 * manifest declares (s2-plan section 0).
 */
export function useFleetEditor(options: {
  host: HostContext;
  subs: UseSubscriptions;
  draft: Ref<FleetDraft>;
  editing: Ref<boolean>;
  editingId: Ref<string | null>;
  memberTagText: Ref<string>;
}) {
  const { host, subs, draft, editing, editingId, memberTagText } = options;

  const canReadCatalogue = computed(() => host.available(BINDINGS.subFleetPreview));
  const canReadIdentities = computed(() => host.available(BINDINGS.subIdentities));
  const canBindPreview = computed(() => host.available(BINDINGS.bindPreview));

  /** The live list row of the record being edited, for its index flags and staged facts. */
  const listItem = computed<SubscriptionListItem | undefined>(() =>
    editingId.value ? subs.items.value.find((item) => item.id === editingId.value) : undefined,
  );

  const isFleetSub = computed(() => draft.value.kind === KIND_SUB && draft.value.source === SOURCE_FLEET);

  /** Every record a combination draft gathers: its members and, by tag, every sub carrying one. */
  const gatheredIds = computed<string[]>(() => {
    if (draft.value.kind !== KIND_COLLECTION) return [];
    const tags = new Set(memberTagText.value.split(/[,\n]/).map((tag) => tag.trim()).filter(Boolean));
    const ids = new Set(draft.value.members);
    for (const item of subs.items.value) {
      if ((item.kind || KIND_SUB) !== KIND_SUB) continue;
      if ((item.tags ?? []).some((tag) => tags.has(tag))) ids.add(item.id);
    }
    return [...ids];
  });

  /** A fleet record now or once its staged revision goes live. */
  function isFleetItem(item: SubscriptionListItem): boolean {
    return item.source === SOURCE_FLEET || item.staged_source === SOURCE_FLEET;
  }

  /**
   * Whether the draft is fleet-bound: a fleet sub, or a combination that
   * gathers one. The Lattice-only steps, the binding rules and the identity
   * preview are offered on these and nowhere else; a file is never fleet-bound
   * in S2 (fleet_file_unavailable).
   */
  const fleetBound = computed(() => {
    if (draft.value.kind === KIND_FILE) return false;
    if (isFleetSub.value) return true;
    if (draft.value.kind !== KIND_COLLECTION) return false;
    const byId = new Map(subs.items.value.map((item) => [item.id, item]));
    return gatheredIds.value.some((id) => {
      const item = byId.get(id);
      return item ? isFleetItem(item) : false;
    });
  });

  // ── catalogue and selection ────────────────────────────────────────────────

  const catalogue = ref<FleetPreviewReply | null>(null);
  const catalogueState = ref<FleetReadState>("idle");
  const catalogueError = ref<FleetReadError | null>(null);
  const selection = ref<FleetPreviewReply | null>(null);
  const selectionState = ref<FleetReadState>("idle");
  const selectionError = ref<FleetReadError | null>(null);

  const rows = new LineRowCache((lineUUIDs) => fleetPreview({ line_uuids: lineUUIDs }).promise);

  function fleetPreview(request: FleetPreviewRequest): { promise: Promise<FleetPreviewReply>; cancel: () => void } {
    if (!host.bridge) return { promise: Promise.reject(new Error("the console bridge is not ready")), cancel: () => {} };
    return callMethod<FleetPreviewReply>(host.bridge, BINDINGS.subFleetPreview, request);
  }

  function readError(cause: unknown, fallback: string): FleetReadError {
    return { code: errorCodeOf(cause), message: safeErrorMessage(cause, fallback) };
  }

  /** The enabled steps of the leading structured run, as the plugin reads them. */
  const runSteps = computed<unknown[]>(() => {
    const run = leadingRun(draft.value.process);
    return draft.value.process.slice(0, run.length);
  });
  const runKey = computed(() => JSON.stringify(runSteps.value));

  let catalogueTicket = 0;
  async function loadCatalogue(): Promise<void> {
    if (!canReadCatalogue.value) {
      catalogueState.value = "unavailable";
      return;
    }
    const ticket = ++catalogueTicket;
    catalogueState.value = "loading";
    catalogueError.value = null;
    try {
      const reply = await fleetPreview({ process: [], sample: FLEET_SAMPLE }).promise;
      if (ticket !== catalogueTicket) return;
      catalogue.value = reply;
      rows.remember(reply.catalogue_version, reply.sample ?? []);
      catalogueState.value = "ready";
    } catch (cause) {
      if (ticket !== catalogueTicket) return;
      catalogueError.value = readError(cause, t.fleetEditor.error.title);
      catalogueState.value = isPermissionDenied(cause) ? "denied" : "error";
    }
  }

  let selectionTicket = 0;
  let selectionTimer: ReturnType<typeof setTimeout> | undefined;
  async function loadSelection(): Promise<void> {
    if (!canReadCatalogue.value) {
      selectionState.value = "unavailable";
      return;
    }
    const ticket = ++selectionTicket;
    selectionState.value = "loading";
    selectionError.value = null;
    try {
      const reply = await fleetPreview({ process: runSteps.value, sample: FLEET_SAMPLE }).promise;
      if (ticket !== selectionTicket) return;
      selection.value = reply;
      rows.remember(reply.catalogue_version, reply.sample ?? []);
      selectionState.value = "ready";
    } catch (cause) {
      if (ticket !== selectionTicket) return;
      selectionError.value = readError(cause, t.fleetEditor.error.title);
      selectionState.value = isPermissionDenied(cause) ? "denied" : "error";
    }
  }

  /** A selection edit settles before it is read, so typing a value is one read, not one per key. */
  function scheduleSelection(delay = 350): void {
    if (selectionTimer) clearTimeout(selectionTimer);
    selectionTimer = setTimeout(() => {
      selectionTimer = undefined;
      void loadSelection();
    }, delay);
  }

  function retry(): void {
    void loadCatalogue();
    void loadSelection();
  }

  watch(
    () => [editing.value, isFleetSub.value] as const,
    ([open, fleet]) => {
      if (!open || !fleet) return;
      if (catalogueState.value === "idle") void loadCatalogue();
      if (selectionState.value === "idle") void loadSelection();
    },
    { immediate: true },
  );
  /** The first condition of the selection still being typed, which holds the read back. */
  const unfinished = computed(() => (isFleetSub.value ? unfinishedCondition(draft.value.process) : null));

  watch(runKey, () => {
    if (!editing.value || !isFleetSub.value) return;
    if (unfinished.value) {
      // The counts on screen stay those of the last complete selection, and
      // the editor says which condition holds the read back.
      if (selectionTimer) clearTimeout(selectionTimer);
      return;
    }
    scheduleSelection();
  });

  /** Whether the catalogue says probe data does not exist on this server yet. */
  const probeMissing = computed(() => probeUnavailable(selection.value ?? catalogue.value));

  /**
   * The served snapshot predates the catalogue: the record's last fetch wrote
   * another catalogue version than the one the editor just read. Only a saved
   * fleet sub has a snapshot to compare.
   */
  const stale = computed(() => {
    const served = listItem.value?.selection_version;
    const now = (selection.value ?? catalogue.value)?.catalogue_version;
    if (!isFleetSub.value || !served || !now) return null;
    return served === now ? null : { served, now };
  });

  // ── identities and one identity's bind preview ─────────────────────────────

  const identities = ref<Identity[]>([]);
  const identitiesState = ref<FleetReadState>("idle");
  const identitiesError = ref<FleetReadError | null>(null);

  async function loadIdentities(): Promise<void> {
    if (!canReadIdentities.value || !host.bridge) {
      identitiesState.value = "unavailable";
      return;
    }
    identitiesState.value = "loading";
    identitiesError.value = null;
    try {
      const reply = await callMethod<IdentitiesReply>(host.bridge, BINDINGS.subIdentities, {}).promise;
      identities.value = Array.isArray(reply.identities) ? reply.identities : [];
      identitiesState.value = "ready";
    } catch (cause) {
      identitiesError.value = readError(cause, t.fleetEditor.identity.error);
      identitiesState.value = isPermissionDenied(cause) ? "denied" : "error";
    }
  }

  const identityId = ref("");
  /** "" previews the live revision; a staged revision id previews what its plan would serve. */
  const bindRevision = ref("");
  const bindPreview = ref<BindPreviewReply | null>(null);
  const bindState = ref<FleetReadState>("idle");
  const bindError = ref<FleetReadError | null>(null);
  const chosenIdentity = computed(() => identities.value.find((identity) => identity.id === identityId.value) ?? null);

  /** A record needs a live or staged revision before core can bind it for an identity. */
  const bindable = computed(() => Boolean(editingId.value) && fleetBound.value);

  let bindTicket = 0;
  async function loadBindPreview(): Promise<void> {
    if (!identityId.value || !editingId.value) {
      bindPreview.value = null;
      bindState.value = "idle";
      return;
    }
    if (!canBindPreview.value || !host.bridge) {
      bindState.value = "unavailable";
      return;
    }
    const ticket = ++bindTicket;
    bindState.value = "loading";
    bindError.value = null;
    try {
      const reply = await callMethod<BindPreviewReply>(host.bridge, BINDINGS.bindPreview, {
        subscription_id: editingId.value,
        identity_id: identityId.value,
        ...(bindRevision.value ? { revision: bindRevision.value } : {}),
      }).promise;
      if (ticket !== bindTicket) return;
      bindPreview.value = reply;
      bindState.value = "ready";
    } catch (cause) {
      if (ticket !== bindTicket) return;
      bindPreview.value = null;
      bindError.value = readError(cause, t.fleetEditor.bind.error);
      bindState.value = isPermissionDenied(cause) ? "denied" : "error";
    }
  }

  function chooseIdentity(id: string): void {
    identityId.value = id;
    void loadBindPreview();
  }

  function chooseRevision(revision: string): void {
    bindRevision.value = revision;
    void loadBindPreview();
  }

  watch(
    () => [editing.value, bindable.value] as const,
    ([open, can]) => {
      if (open && can && identitiesState.value === "idle") void loadIdentities();
    },
    { immediate: true },
  );

  // ── binding rules ──────────────────────────────────────────────────────────

  const rules = computed<FleetOptions>(() => draft.value.fleet ?? {});

  function setRules(next: FleetOptions): void {
    draft.value.fleet = normalizeFleetOptions(next);
  }

  // ── what the save would refuse, said before the round trip ─────────────────

  /**
   * The first thing a save of this draft would refuse for a fleet reason, in
   * the editor's words: a predicate the plugin cannot read
   * (structured_args_invalid), a probe condition while the catalogue lists
   * probe as unavailable (fleet_probe_unavailable), a link-mode script on a
   * fleet-bound record (fleet_script_link_unavailable), or a binding rule out
   * of range. The plugin decides at save regardless; this keeps the operator
   * from finding out one refusal at a time.
   */
  const problem = computed<string>(() => {
    const process = draft.value.process;
    for (let index = 0; index < process.length; index++) {
      const step = (process[index] ?? {}) as { type?: unknown; disabled?: unknown; args?: unknown };
      if (step.type !== STRUCTURED_FILTER_TYPE || step.disabled) continue;
      const args = readFilterArgs(step.args);
      if (args.predicates.length === 0) return t.fleetEditor.validation.noPredicates(index + 1);
      for (let i = 0; i < args.predicates.length; i++) {
        const p = args.predicates[i]!;
        const found = predicateProblem(p);
        if (found) return t.fleetEditor.validation.predicate(index + 1, i + 1, problemText(found, p.field));
        if (probeMissing.value && PREDICATE_FIELDS[p.field]?.probe) return t.fleetEditor.validation.probe(index + 1);
      }
    }
    if (fleetBound.value) {
      const link = linkModeScriptSteps(process)[0];
      if (link !== undefined) return t.fleetEditor.validation.scriptLink(link + 1);
      const rulesProblem = fleetOptionsProblem(draft.value.fleet);
      if (rulesProblem) return t.fleetEditor.validation.rules(t.fleetEditor.rules.problems[rulesProblem]);
    }
    return "";
  });

  function reset(): void {
    catalogueTicket++;
    selectionTicket++;
    bindTicket++;
    if (selectionTimer) clearTimeout(selectionTimer);
    catalogue.value = null;
    catalogueState.value = "idle";
    catalogueError.value = null;
    selection.value = null;
    selectionState.value = "idle";
    selectionError.value = null;
    identities.value = [];
    identitiesState.value = "idle";
    identitiesError.value = null;
    identityId.value = "";
    bindRevision.value = "";
    bindPreview.value = null;
    bindState.value = "idle";
    bindError.value = null;
  }

  return {
    canReadCatalogue,
    canReadIdentities,
    canBindPreview,
    listItem,
    isFleetSub,
    fleetBound,
    catalogue,
    catalogueState,
    catalogueError,
    selection,
    selectionState,
    selectionError,
    unfinished,
    rows,
    runSteps,
    probeMissing,
    stale,
    retry,
    loadSelection,
    identities,
    identitiesState,
    identitiesError,
    loadIdentities,
    identityId,
    chosenIdentity,
    bindRevision,
    bindPreview,
    bindState,
    bindError,
    bindable,
    chooseIdentity,
    chooseRevision,
    loadBindPreview,
    rules,
    setRules,
    problem,
    reset,
  };
}

export type FleetEditor = ReturnType<typeof useFleetEditor>;

/**
 * A fleet record's chain with its selection at the head. The common-settings
 * block puts its steps first, and on a fleet record a step ahead of the
 * Structured filters ends the leading run: nothing would be pushed to core,
 * and every catalogue line would be read and stored. The settings' steps move
 * to just after the run; every other order is kept.
 */
export function keepSelectionFirst(process: readonly ChainStep[]): ChainStep[] {
  const managed = new Set<string>(MANAGED_TYPES);
  let head = 0;
  while (head < process.length) {
    const step = process[head]!;
    if (step.type === STRUCTURED_FILTER_TYPE || step.disabled || managed.has(step.type)) head++;
    else break;
  }
  const front = process.slice(0, head);
  const moved = front.filter((step) => managed.has(step.type) && !step.disabled);
  if (!moved.length || !front.some((step) => step.type === STRUCTURED_FILTER_TYPE)) return [...process];
  return [...front.filter((step) => !moved.includes(step)), ...moved, ...process.slice(head)];
}

/** A code the editor treats as "this record's output is not shown here". */
export function isWithheld(code: string): boolean {
  return code === ERROR_CODES.ownerCredentialsWithheld;
}

export type EditorTab = "display" | "content" | "operations";

export interface RecordEditorOptions {
  host: HostContext;
  subs: UseSubscriptions;
  /** Clear anything the list has open, so it does not reappear on the way back. */
  clearListState: () => void;
  /** Re-read a saved record's node count. */
  onSaved: (id: string | null) => void;
  /**
   * The editor gave way to the screen: a save went through (`saved`), or
   * the operator left it (Cancel, the breadcrumb, Escape, keeping the other
   * version of a stale save). Not called when the editor reopens on the
   * current version, which closes and opens it in one step. `id` is the
   * record it held, null for a record never saved.
   */
  onClosed?: (closed: { id: string | null; saved: boolean }) => void;
}

/**
 * The record editor: the draft, its tabs, its validation, its unsaved-edit
 * guard and the three ways out of a stale save.
 *
 * It is a composable rather than a component's own state because `editing` is
 * what the screen routes on: the list and the editor are two states of one
 * screen, not two screens. The screen owns that routing and hands this object
 * to SubscriptionEditor.vue, which owns everything it draws. Before the split,
 * both lived in one 2,449-line file with 33 top-level refs, and reading it to
 * answer "what happens when this save is refused" meant reading all of it.
 */
export function useRecordEditor(options: RecordEditorOptions) {
  const { host, subs } = options;
  const clearTransientListState = options.clearListState;
  const recount = options.onSaved;

  const editing = ref(false);
  const editingId = ref<string | null>(null);
  const draft = ref<FleetDraft>(emptyDraft());
  const common = ref<CommonSettingsShape>(emptyCommonSettings());
  const tagText = ref("");
  const memberTagText = ref("");
  /**
   * The source the record has live, as it was read: what decides which source
   * cards a record may still choose (legacy_source_retired,
   * fleet_to_legacy_refused). "" for a record not yet saved.
   */
  const storedSource = ref("");

  const fleet = useFleetEditor({ host, subs, draft, editing, editingId, memberTagText });

  const isCollection = computed(() => draft.value.kind === KIND_COLLECTION);
  const draftError = computed(() => (editing.value ? validateDraft(draft.value) || fleet.problem.value : ""));
  const canSave = computed(() => !draftError.value && !subs.saving.value);
  const canPreviewNow = computed(
    () => subs.canPreview.value && !subs.previewing.value && !draftError.value,
  );

  /**
   * What the current preview covers. A cut preview has to say so on the result,
   * or the operator reads a partial node list as the record's real output. The
   * chain hands over the label it shows in the list, because chain indices and
   * list positions differ, settings-managed steps live in the same array but
   * are edited above, so a computed position would name a different step than
   * the one that was clicked.
   */
  const previewStepLabel = ref("");

  // ── explain the chain ───────────────────────────────────────────────────────
  // One partial run per enabled step, in order, reading the count after each;
  // then the whole chain again so the panel ends on the full result. The
  // engine does the work; this only asks the same question N times.
  const explaining = ref(false);
  const explanation = ref<ChainExplanation | null>(null);
  const explainable = computed(() => enabledStepIndexes(draft.value.process as ChainStep[]).length > 0);
  async function explainDraft(): Promise<void> {
    if (explaining.value || !canPreviewNow.value) return;
    explaining.value = true;
    explanation.value = null;
    const steps = draft.value.process as ChainStep[];
    try {
      const result = await explainChain(steps, async (upTo) => {
        await subs.runPreview(draft.value, upTo);
        if (!subs.preview.value || subs.previewError.value) throw new Error(subs.previewError.value || t.counts.previewFailed);
        return subs.preview.value;
      });
      explanation.value = result;
      // The last cut is the whole chain, so the pane already holds the full
      // result and the partial-run label would be false.
      if (result.complete) subs.previewStep.value = null;
    } finally {
      explaining.value = false;
    }
  }
  watch(() => draft.value.process, () => { explanation.value = null; }, { deep: true });

  function previewUpToStep(index: number, label: string): void {
    previewStepLabel.value = label;
    void subs.runPreview(draft.value, index);
  }

  /** Only subs can be members; a collection inside a collection would recurse. */
  const memberCandidates = computed(() =>
    subs.items.value.filter((i) => (i.kind || KIND_SUB) === KIND_SUB && i.id !== editingId.value),
  );

  /* The words are getters, read in the locale active when the cards are drawn. */
  const FLEET_CARD: SourceCard = {
    id: SOURCE_FLEET,
    get title() {
      return t.fleetEditor.source.title;
    },
    get detail() {
      return t.fleetEditor.source.detail;
    },
    icon: Radar,
    disabledReason: "",
  };
  const PROVIDER_CARDS: SourceCard[] = [
    {
      id: SOURCE_REMOTE,
      get title() {
        return t.editor.sources.remote.title;
      },
      get detail() {
        return t.editor.sources.remote.detail;
      },
      icon: Globe,
      disabledReason: "",
    },
    {
      id: SOURCE_LOCAL,
      get title() {
        return t.editor.sources.local.title;
      },
      get detail() {
        return t.editor.sources.local.detail;
      },
      icon: ClipboardPaste,
      disabledReason: "",
    },
  ];
  function legacyCards(disabledReason: () => string): SourceCard[] {
    return [
      {
        id: SOURCE_VPN_CORE,
        get title() {
          return t.editor.sources.fleet.title;
        },
        get detail() {
          return t.editor.sources.fleet.detail;
        },
        icon: Server,
        legacy: true,
        get disabledReason() {
          return disabledReason();
        },
      },
      {
        id: SOURCE_VPN_CORE_GRAPH,
        get title() {
          return t.editor.sources.graph.title;
        },
        get detail() {
          return t.editor.sources.graph.detail;
        },
        icon: Layers,
        legacy: true,
        get disabledReason() {
          return disabledReason();
        },
      },
    ];
  }

  /**
   * The source cards a record may choose (s2-plan section 11). Fleet first.
   * The two legacy sources serve line owner credentials, so save refuses them
   * for a new record and for one whose live source is remote, local or fleet
   * (legacy_source_retired): they are not offered there. An existing legacy
   * record keeps its cards, marked, so it can still be edited and saved as
   * it is; a fleet record sees them disabled with the reason
   * (fleet_to_legacy_refused), so the export it may remember is accounted for.
   */
  const SOURCES = computed<SourceCard[]>(() => {
    if (isLegacySource(storedSource.value)) return [FLEET_CARD, ...PROVIDER_CARDS, ...legacyCards(() => "")];
    if (storedSource.value === SOURCE_FLEET) {
      return [FLEET_CARD, ...PROVIDER_CARDS, ...legacyCards(() => t.fleetEditor.legacyRefused)];
    }
    return [FLEET_CARD, ...PROVIDER_CARDS];
  });

  function startCreate(kind: string): void {
    clearTransientListState();
    subs.clearMessages();
    fleet.reset();
    storedSource.value = "";
    draft.value = emptyDraft();
    draft.value.kind = kind;
    // A new source defaults to the fleet: S2 creates no owner-credential
    // record, and the identity is chosen per share.
    if (kind === KIND_SUB) draft.value.source = SOURCE_FLEET;
    common.value = emptyCommonSettings();
    tagText.value = "";
    memberTagText.value = "";
    editingId.value = null;
    editorTab.value = "display";
    editing.value = true;
    markPristine();
  }

  /**
   * The staged revision the editor opened on, with the live revision that
   * keeps serving, or null when it opened on the live record.
   */
  const editingStaged = ref<{ revision: string; live: string } | null>(null);

  /**
   * The step the chain should open, scroll to and focus: a 0-based index
   * into the stored steps, with a ticket so asking for the same step twice
   * opens it twice. Set by openStep, from a refusal that names a step.
   */
  const stepToOpen = ref<{ index: number; ticket: number } | null>(null);
  let stepTicket = 0;

  /**
   * A refusal another surface opened this editor to fix (the plan flow's
   * "Open step" on fleet_script_network_unavailable), with the step it
   * named as it stood then. The editor says it and flags the step until the
   * operator changes that step, or the editor closes, a save included: once
   * the step differs, the refusal is about a chain that is gone. Before
   * this, the plan's "Open step" landed on a bare chain, and nothing on
   * screen tied the bar to the refusal (the lane 6c design review).
   */
  const carried = ref<{ refusal: ScriptNetworkRefusal; step: string } | null>(null);
  function carryRefusal(refusal: ScriptNetworkRefusal | null | undefined): void {
    if (!refusal) {
      carried.value = null;
      return;
    }
    const position = refusal.step?.position ?? 0;
    const stored = position ? (draft.value.process[position - 1] as { type?: unknown } | undefined) : undefined;
    // A position that no longer holds a step that can ask (a migration
    // abandoned, so the legacy chain is numbered differently) names no step.
    const holds = !!stored && typeof stored.type === "string" && NETWORK_STEP_TYPES.has(stored.type);
    carried.value = {
      refusal: holds ? refusal : { ...refusal, step: null },
      step: holds ? JSON.stringify(stored) : "",
    };
  }
  /** The carried refusal while the step it named is unchanged. */
  const carriedRefusal = computed(() => {
    const entry = carried.value;
    if (!entry || !editing.value) return null;
    const position = entry.refusal.step?.position ?? 0;
    if (position && JSON.stringify(draft.value.process[position - 1]) !== entry.step) return null;
    return entry.refusal;
  });

  async function startEdit(id: string): Promise<void> {
    clearTransientListState();
    subs.clearMessages();
    const opened = await subs.getForEdit(id);
    if (!opened) return;
    const record = opened.record;
    editingStaged.value = opened.staged ? { revision: record.revision ?? "", live: opened.liveRevision } : null;
    stepToOpen.value = null;
    carried.value = null;
    fleet.reset();
    storedSource.value = record.source ?? "";
    draft.value = draftFromRecord(record);
    // The binding rules ride on the draft (FleetDraft); the store's draft
    // reader predates them, so they are carried across here.
    if (record.fleet) draft.value.fleet = normalizeFleetOptions(record.fleet);
    // A record stored before the source was named still has url or content set.
    if (!draft.value.source && draft.value.kind === KIND_SUB) {
      draft.value.source = draft.value.url ? SOURCE_REMOTE : SOURCE_LOCAL;
    }
    common.value = readCommonSettings(draft.value.process as ChainStep[]);
    tagText.value = draft.value.tags.join(", ");
    memberTagText.value = draft.value.memberTags.join(", ");
    editingId.value = id;
    editorTab.value = "display";
    editing.value = true;
    if (draft.value.source === SOURCE_VPN_CORE_GRAPH) await loadGraphOptionsForDraft(false);
    // After the graph options land, so loading them is not mistaken for an edit.
    markPristine();
    await host.resize();
  }

  async function selectSource(source: string): Promise<void> {
    const card = SOURCES.value.find((option) => option.id === source);
    if (!card || card.disabledReason) return;
    draft.value.source = source;
    subs.preview.value = null;
    if (source === SOURCE_VPN_CORE_GRAPH) await reloadGraphOptions();
  }

  async function reloadGraphOptions(): Promise<void> {
    await loadGraphOptionsForDraft(true);
  }

  async function loadGraphOptionsForDraft(adopt: boolean): Promise<void> {
    const loaded = await subs.loadGraphOptions();
    if (!loaded || !subs.graphOptions.value) {
      draft.value.optionsVersion = "";
      return;
    }
    const options = subs.graphOptions.value;
    const result = reconcileGraphDraftOptions(draft.value, options, adopt);
    if (!adopt) {
      if (result.stale) {
        subs.actionError.value = t.editor.graphChanged;
      }
      return;
    }
  }

  function addGraphRoot(root: string): void {
    if (!draft.value.entryRoots.includes(root)) draft.value.entryRoots.push(root);
  }

  function removeGraphRoot(index: number): void {
    draft.value.entryRoots.splice(index, 1);
  }

  function moveGraphRoot(index: number, offset: number): void {
    const next = index + offset;
    if (next < 0 || next >= draft.value.entryRoots.length) return;
    const [root] = draft.value.entryRoots.splice(index, 1);
    draft.value.entryRoots.splice(next, 0, root);
  }

  function setGraphIdentity(identity: string): void {
    draft.value.vpnIdentity = identity;
  }

  // Changing the identity can make a chosen root ineligible, and a root that
  // is no longer eligible must not silently stay selected in the draft.
  watch(
    () => draft.value.vpnIdentity,
    (identity, previous) => {
      if (draft.value.source !== SOURCE_VPN_CORE_GRAPH || identity === previous || !subs.graphOptions.value) return;
      const allowed = new Set(
        subs.graphOptions.value.roots
          .filter((root) => root.selectable && root.eligible_identity_ids.includes(identity))
          .map((root) => root.line_uuid),
      );
      const before = draft.value.entryRoots.length;
      draft.value.entryRoots = draft.value.entryRoots.filter((root) => allowed.has(root));
      if (before !== draft.value.entryRoots.length) {
        subs.actionError.value = t.editor.rootsRemoved;
      }
    },
  );

  /**
   * The unsaved-edit guard. The snapshot is the serialised draft plus the text
   * fields that live outside it, because those are edits too. The rest of the
   * rule is shared with the Files editor (useEditorExit.ts) — a second screen
   * carrying its own copy is how the two silently stopped behaving the same.
   */
  const exit = useEditorExit({
    editing,
    fingerprint: () =>
      JSON.stringify([draft.value, common.value, tagText.value, memberTagText.value]),
    // Not a hand-written list any more. Every overlay registers while it is
    // open, so the eighth one cannot be left out of this line the way the Files
    // editor was left out of its own.
    overlayOpen: () => overlayDepth() > 0,
    leave: () => close(false),
  });
  const { discarding, markPristine } = exit;
  const editorDirty = exit.dirty;
  const leaveEditor = exit.leaveEditor;

  function cancelEdit(): void {
    exit.reset();
    editing.value = false;
    editingId.value = null;
    editingStaged.value = null;
    stepToOpen.value = null;
    carried.value = null;
    storedSource.value = "";
    fleet.reset();
    draft.value = emptyDraft();
    subs.preview.value = null;
    // Errors belong to the screen that raised them. A preview refused inside the
    // editor used to follow the operator out to the list and sit above it as an
    // unexplained alert about a draft no longer on screen. Only the errors go:
    // a successful save reports "Saved ..." and then leaves through here, so
    // clearing the notice too left the save with nothing to show for itself.
    subs.clearErrors();
  }

  /** cancelEdit, then tell the screen where the editor went. */
  function close(saved: boolean): void {
    const id = editingId.value;
    cancelEdit();
    options.onClosed?.({ id, saved });
  }

  function parseTags(text: string): string[] {
    return text
      .split(/[,\n]/)
      .map((tag) => tag.trim())
      .filter(Boolean);
  }

  /** The block writes into the chain; the chain stays the single source of truth. */
  function onCommonChange(next: CommonSettingsShape): void {
    common.value = next;
    const applied = applyCommonSettings(draft.value.process as ChainStep[], next);
    draft.value.process = fleet.isFleetSub.value ? keepSelectionFirst(applied) : applied;
  }

  /**
   * The normal save. Takes no argument on purpose: it is bound to the form's
   * submit, which would hand it a SubmitEvent, and a truthy first argument here
   * would have meant "force" and skipped the staleness check on every ordinary
   * keyboard submit. Forcing goes through overwriteWithMine.
   */
  async function submit(): Promise<void> {
    await writeDraft(false);
  }

  async function writeDraft(force: boolean): Promise<void> {
    draft.value.tags = parseTags(tagText.value);
    draft.value.memberTags = parseTags(memberTagText.value);
    const ok = await subs.save(draft.value, force);
    if (ok) {
      if (editingId.value) recount(editingId.value);
      close(true);
    }
  }

  /**
   * The three ways out of a save refused as stale, and none of them is automatic.
   *
   * A merge is not offered on purpose. This record holds an operator chain and a
   * document; merging either without the operator reading both is how a
   * plausible-looking configuration that nobody wrote reaches a client.
   */

  /** Keep their version. The editor closes and the list reloads. */
  function discardMyEdit(): void {
    subs.saveConflict.value = null;
    close(false);
    void subs.load();
  }

  /**
   * Reopen on their version, losing the local draft. Offered because when the
   * changed fields are not the ones being edited, re-applying a small edit on top
   * of the current record is both quick and correct.
   */
  async function reopenOnCurrent(): Promise<void> {
    const id = subs.saveConflict.value?.conflict.id ?? editingId.value;
    subs.saveConflict.value = null;
    if (!id) return;
    cancelEdit();
    await nextTick();
    await startEdit(id);
  }

  /** Mine wins, deliberately, after seeing what it replaces. */
  async function overwriteWithMine(): Promise<void> {
    subs.saveConflict.value = null;
    await writeDraft(true);
  }

  /**
   * The editor's sections, split the way Sub-Store splits them: what the record
   * is called, what it is made of, and what is done to it. A single scroll of
   * eight fieldsets made the operator hunt for the one field they came to change
   * and buried the operator chain. The thing this plugin exists for, below
   * everything else.
   */
  type EditorTab = "display" | "content" | "operations";
  const editorTab = ref<EditorTab>("display");

  function openStep(position: number): void {
    if (!Number.isInteger(position) || position < 1) return;
    editorTab.value = "operations";
    stepTicket += 1;
    stepToOpen.value = { index: position - 1, ticket: stepTicket };
  }
  /* Getters, so a tab is named in the locale active when it is drawn. */
  const EDITOR_TABS: { id: EditorTab; readonly label: string }[] = [
    { id: "display", get label() { return t.editor.tabs.display; } },
    { id: "content", get label() { return t.editor.tabs.content; } },
    { id: "operations", get label() { return t.editor.tabs.operations; } },
  ];

  /**
   * Which tab holds the field the current error is about.
   *
   * A tabbed form that reports "Give it a name." at the bottom of the Content tab
   * says what is wrong and not where: the name lives two tabs away and nothing
   * points at it. Every message except that one is about the source, so this is
   * read off the draft rather than by matching the message text.
   */
  const errorTab = computed<EditorTab | "">(() => {
    if (!draftError.value) return "";
    return draft.value.name.trim() ? "content" : "display";
  });

  /** The chain's size, shown on the tab so it is visible without opening it. */
  /**
   * What the Operations tab badge counts.
   *
   * The raw chain includes the steps the common-settings block above edits, which
   * the chain list deliberately hides. Counting those made the badge read
   * "Operations 1" over a panel saying "No operations" as soon as a quick setting
   * was turned on.
   */
  const chainCount = computed(
    () =>
      (draft.value.process as { type?: string }[]).filter(
        (step) => !(MANAGED_TYPES as readonly string[]).includes(step?.type ?? ""),
      ).length,
  );

  return {
    // routing
    editing,
    editingId,
    // draft
    draft,
    common,
    tagText,
    memberTagText,
    isCollection,
    draftError,
    canSave,
    canPreviewNow,
    previewStepLabel,
    // chain explanation
    explaining,
    explanation,
    explainable,
    explainDraft,
    previewUpToStep,
    // members and sources
    memberCandidates,
    SOURCES,
    storedSource,
    // the fleet source (design 28 S2)
    fleet,
    MANAGED_TYPES,
    // opening and closing
    startCreate,
    startEdit,
    // The discard confirm's way out: a leave like Cancel, so the screen hears it.
    cancelEdit: () => close(false),
    editingStaged,
    stepToOpen,
    openStep,
    carryRefusal,
    carriedRefusal,
    selectSource,
    reloadGraphOptions,
    addGraphRoot,
    removeGraphRoot,
    moveGraphRoot,
    setGraphIdentity,
    // the unsaved-edit guard
    exit,
    discarding,
    editorDirty,
    leaveEditor,
    markPristine,
    // writing
    onCommonChange,
    submit,
    discardMyEdit,
    reopenOnCurrent,
    overwriteWithMine,
    // tabs
    editorTab,
    EDITOR_TABS,
    errorTab,
    chainCount,
  };
}

export type RecordEditor = ReturnType<typeof useRecordEditor>;
