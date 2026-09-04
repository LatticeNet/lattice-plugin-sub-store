import { computed, nextTick, ref, watch } from "vue";
import { ClipboardPaste, Globe, Layers, Server } from "@lucide/vue";

import {
  KIND_COLLECTION,
  KIND_SUB,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  SOURCE_VPN_CORE,
  SOURCE_VPN_CORE_GRAPH,
  type SubscriptionListItem,
} from "./client";
import {
  applyCommonSettings,
  emptyCommonSettings,
  readCommonSettings,
  type CommonSettings as CommonSettingsShape,
} from "./commonSettings";
import { enabledStepIndexes, explainChain, type ChainExplanation } from "./chainExplain";
import type { ChainStep } from "./components/ProcessChain.vue";
import type { HostContext } from "./host";
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

export type EditorTab = "display" | "content" | "operations";

export interface RecordEditorOptions {
  host: HostContext;
  subs: UseSubscriptions;
  /** Clear anything the list has open, so it does not reappear on the way back. */
  clearListState: () => void;
  /** Re-read a saved record's node count. */
  onSaved: (id: string | null) => void;
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
  const draft = ref<SubscriptionDraft>(emptyDraft());
  const common = ref<CommonSettingsShape>(emptyCommonSettings());
  const tagText = ref("");
  const memberTagText = ref("");

  const isCollection = computed(() => draft.value.kind === KIND_COLLECTION);
  const draftError = computed(() => (editing.value ? validateDraft(draft.value) : ""));
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
        if (!subs.preview.value || subs.previewError.value) throw new Error(subs.previewError.value || "Preview failed");
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

  const SOURCES = [
    {
      id: SOURCE_VPN_CORE,
      title: "This fleet's nodes",
      detail: "Reads the live vpn-core export. Nodes added or removed reach clients on refresh.",
      icon: Server,
    },
    {
      id: SOURCE_VPN_CORE_GRAPH,
      title: "A converged path",
      detail: "Composes selected applied line-chain roots in the exact order shown.",
      icon: Layers,
    },
    {
      id: SOURCE_REMOTE,
      title: "A provider link",
      detail: "Fetches an external subscription link and re-serves it through this record's operations.",
      icon: Globe,
    },
    {
      id: SOURCE_LOCAL,
      title: "Nodes I paste",
      detail: "URI list, base64, Clash YAML or sing-box JSON. The engine detects the format.",
      icon: ClipboardPaste,
    },
  ] as const;

  function startCreate(kind: string): void {
    clearTransientListState();
    subs.clearMessages();
    draft.value = emptyDraft();
    draft.value.kind = kind;
    if (kind === KIND_SUB) draft.value.source = SOURCE_VPN_CORE;
    common.value = emptyCommonSettings();
    tagText.value = "";
    memberTagText.value = "";
    editingId.value = null;
    editorTab.value = "display";
    editing.value = true;
    markPristine();
  }

  async function startEdit(id: string): Promise<void> {
    clearTransientListState();
    subs.clearMessages();
    const record = await subs.get(id);
    if (!record) return;
    draft.value = draftFromRecord(record);
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
        subs.actionError.value = "Graph options changed. Reload and review the identity and roots before saving.";
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
        subs.actionError.value = "Some selected roots were removed because they are not eligible for this identity.";
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
    leave: () => cancelEdit(),
  });
  const { discarding, markPristine } = exit;
  const editorDirty = exit.dirty;
  const leaveEditor = exit.leaveEditor;

  function cancelEdit(): void {
    exit.reset();
    editing.value = false;
    editingId.value = null;
    draft.value = emptyDraft();
    subs.preview.value = null;
    // Errors belong to the screen that raised them. A preview refused inside the
    // editor used to follow the operator out to the list and sit above it as an
    // unexplained alert about a draft no longer on screen. Only the errors go:
    // a successful save reports "Saved ..." and then leaves through here, so
    // clearing the notice too left the save with nothing to show for itself.
    subs.clearErrors();
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
    draft.value.process = applyCommonSettings(draft.value.process as ChainStep[], next);
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
      cancelEdit();
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
    cancelEdit();
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
  const EDITOR_TABS: { id: EditorTab; label: string }[] = [
    { id: "display", label: "Display" },
    { id: "content", label: "Content" },
    { id: "operations", label: "Operations" },
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
    MANAGED_TYPES,
    // opening and closing
    startCreate,
    startEdit,
    cancelEdit,
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
