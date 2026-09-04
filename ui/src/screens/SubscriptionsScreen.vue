<script setup lang="ts">
import { computed, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from "vue";
import {
  ChevronDown,
  ChevronLeft,
  ChevronsRight,
  CircleAlert,
  CircleCheck,
  ClipboardPaste,
  CopyPlus,
  Ellipsis,
  Eye,
  Globe,
  Layers,
  Library,
  LoaderCircle,
  Pencil,
  Plus,
  Send,
  RefreshCw,
  Server,
  Share2,
  SquareArrowOutUpRight,
  Trash2,
  TriangleAlert,
  ListOrdered,
} from "@lucide/vue";

import LtBadge from "../components/lt/LtBadge.vue";
import LtButton from "../components/lt/LtButton.vue";
import LtConfirmDialog from "../components/lt/LtConfirmDialog.vue";
import RecordMenu from "../components/RecordMenu.vue";
import LtPanel from "../components/lt/LtPanel.vue";
import { closeTopOverlay, overlayDepth } from "../overlayStack";
import LtEmptyState from "../components/lt/LtEmptyState.vue";
import LtManualCopy from "../components/lt/LtManualCopy.vue";
import LtIconButton from "../components/lt/LtIconButton.vue";
import LtBatchBar from "../components/lt/LtBatchBar.vue";
import LtSkeleton from "../components/lt/LtSkeleton.vue";
import LtToolbar from "../components/lt/LtToolbar.vue";
import TargetSheet from "../components/TargetSheet.vue";
import { actionsFor, batchActionsFor, type ActionCapabilities, type ActionId, type ResolvedAction } from "../recordActions";
import { claimIntent, isCommandIntent, isRecordIntent, recordIntent } from "../recordIntent";
import { useEditorExit } from "../useEditorExit";

import {
  CONVERT_TARGETS,
  FAILURE_SKIP,
  FAILURE_STRICT,
  KIND_COLLECTION,
  KIND_FILE,
  KIND_SUB,
  MAX_SUBSCRIPTION_RECORDS,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  SOURCE_VPN_CORE,
  SOURCE_VPN_CORE_GRAPH,
  type SubscriptionListItem,
} from "../client";
import { useHost } from "../host";
import { copyText } from "../hostClipboard";
import { SHARES_LIST_ROUTE, hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import { UNTAGGED, collectTags, matchesQuery, matchesTag, normalizeQuery } from "../recordSearch";
import { formatRelativeTime, formatTraffic, parseUserinfo, tagChips as tagChipsOf } from "../rowStatus";
import { publishStateFor, refreshStateFor } from "../shareState";
import { useShares } from "../useShares";
import {
  cutChain,
  enabledStepIndexes,
  explainChain,
  nodeKey,
  stepLabelOf,
  type ChainExplanation,
} from "../chainExplain";
import { createNodeCountQueue, nodeCountLabel, nodeCountTitle } from "../nodeCounts";
import { maskUrl } from "../urlMask";
import { useReveal } from "../reveal";
import MaskedUrlInput from "../components/MaskedUrlInput.vue";
import { safeErrorMessage } from "../subStoreModel";
import {
  BINDINGS,
  callMethod,
  type SubscriptionPreviewNode,
  type SubscriptionPreviewResponse,
  type SubscriptionRecord,
} from "../client";
import {
  draftFromRecord,
  emptyDraft,
  reconcileGraphDraftOptions,
  useSubscriptions,
  validateDraft,
  type SubscriptionDraft,
} from "../useSubscriptions";
import { useSubscriptionOps } from "../useSubscriptionOps";
import {
  applyCommonSettings,
  emptyCommonSettings,
  readCommonSettings,
  type CommonSettings as CommonSettingsShape,
} from "../commonSettings";
import EngineUnavailable from "../components/EngineUnavailable.vue";
import ProcessChain, { type ChainStep } from "../components/ProcessChain.vue";
import CodeEditor from "../components/CodeEditor.vue";
import CommonSettingsBlock from "../components/CommonSettings.vue";
import MemberPicker from "../components/MemberPicker.vue";
import GraphSubscriptionEditor from "../components/GraphSubscriptionEditor.vue";
import SubscriptionPreviewSummary from "../components/SubscriptionPreviewSummary.vue";
import NodeRows from "../components/NodeRows.vue";
import SubscriptionPublishControl from "../components/SubscriptionPublishControl";

/** Types the common-settings block owns; the chain list hides them. */
const MANAGED_TYPES = ["Quick Setting Operator", "Useless Filter"] as const;


const host = useHost();
const subs = useSubscriptions(host);
// The whole-store surface is here only for the empty state's migrate form: an
// empty store is exactly when importing an existing Sub-Store is the next step.
const ops = useSubscriptionOps(host);

const editing = ref(false);
const editingId = ref<string | null>(null);
const draft = ref<SubscriptionDraft>(emptyDraft());
const common = ref<CommonSettingsShape>(emptyCommonSettings());
const tagText = ref("");
const memberTagText = ref("");
const tagFilter = ref("");
const searchText = ref("");
const kindFilter = ref<"" | "sub" | "collection">("");
const publishDestination = ref("");
const publishMethod = ref("PUT");
const publishFormat = ref("plain");
const migrateUrl = ref("");
const migrateSummary = ref("");

// One drawer at a time carries all row-scoped work; one dialog carries every
// destructive confirmation, single or batch.
const drawer = ref<{ mode: "preview" | "publish" | "share"; id: string } | null>(null);
const deleting = ref<string[]>([]);
const deleteBusy = ref(false);
// Rows currently mid-operation (refresh or delete) render pending.
const pendingIds = ref<Set<string>>(new Set());

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

// Files live in the same store but on their own tab. Offering their tags here
// would put a filter in front of the operator that selects nothing.
const onThisTab = computed(() =>
  subs.items.value.filter((i) => (i.kind || KIND_SUB) !== KIND_FILE),
);

const allTags = computed(() => collectTags(onThisTab.value));

/** Tag and search both live in recordSearch, so the chip counts and the rows
 *  can never disagree about what "matching" means. */
function matchesFilter(item: SubscriptionListItem): boolean {
  return matchesTag(item, tagFilter.value);
}

/** Offered only when there is something it would select. */
const hasUntagged = computed(() => onThisTab.value.some((i) => (i.tags ?? []).length === 0));

const filtersActive = computed(
  () => !!searchText.value.trim() || !!tagFilter.value || !!kindFilter.value,
);

function clearFilters(): void {
  searchText.value = "";
  tagFilter.value = "";
  kindFilter.value = "";
}

const singles = computed(() =>
  onThisTab.value.filter((i) => (i.kind || KIND_SUB) === KIND_SUB && matchesFilter(i)),
);
const collections = computed(() =>
  onThisTab.value.filter((i) => (i.kind || KIND_SUB) === KIND_COLLECTION && matchesFilter(i)),
);
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

function clearTransientListState(): void {
  // A pending confirm or an open drawer must not survive into the editor and
  // reappear when the operator comes back to the list.
  deleting.value = [];
  drawer.value = null;
  expandedId.value = "";
  rowChain.value = null;
  revealSource.hide();
}

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

/** The Source column: where a record's nodes come from, in three words. */
function describe(item: SubscriptionListItem): string {
  if ((item.kind || KIND_SUB) === KIND_COLLECTION) {
    const byId = item.members?.length ?? 0;
    const byTag = item.member_tags?.length ?? 0;
    const parts: string[] = [];
    if (byId) parts.push(`${byId} member${byId === 1 ? "" : "s"}`);
    if (byTag) parts.push(`${byTag} tag${byTag === 1 ? "" : "s"}`);
    return parts.length ? parts.join(", ") : "No members yet";
  }
  if (item.source === SOURCE_VPN_CORE) return "This fleet's nodes";
  if (item.source === SOURCE_VPN_CORE_GRAPH) return "Converged graph path";
  if (item.source === SOURCE_LOCAL) return "Pasted nodes";
  return item.has_url ? "Provider link" : "Pasted nodes";
}

// ── empty state: guidance, not a dead end ───────────────────────────────────

/** Nothing on this tab at all. The moment to offer migration alongside
 *  creation. A filter that merely hides everything is not this moment. */
const storeEmpty = computed(() => onThisTab.value.length === 0);

async function runMigrate(): Promise<void> {
  migrateSummary.value = "";
  const ok = await ops.migrate(migrateUrl.value);
  if (!ok) return;
  await subs.load();
  // The report names what was imported by id; counting those ids by kind is
  // what makes the summary true rather than approximated.
  const imported = new Set(ops.report.value?.imported ?? []);
  const landed = subs.items.value.filter((item) => imported.has(item.id));
  const combos = landed.filter((item) => item.kind === KIND_COLLECTION).length;
  const skipped = Object.keys(ops.report.value?.skipped ?? {}).length;
  migrateSummary.value =
    `Imported ${landed.length - combos} subscription(s) and ${combos} combination(s)` +
    (skipped ? `, and skipped ${skipped}` : "") +
    ". Nothing is published yet, so publish a share under Networking, then Subscription Shares, to make them reachable.";
  migrateUrl.value = "";
}

// ── row status ──────────────────────────────────────────────────────────────


/** The provider's quota line, compact; "" when there is nothing honest to say. */
function trafficOf(item: SubscriptionListItem): string {
  return formatTraffic(parseUserinfo(item.userinfo));
}

// ── table ───────────────────────────────────────────────────────────────────

/** Rows after tag, kind, and text filters; the table sorts on top of this. */
/**
 * How the list is ordered.
 *
 * At the record limit of 256 an unsorted list is a list you scroll. Freshness
 * is the default because the question that brings someone here is usually
 * "what did I just change" or "what has stopped refreshing".
 */
type SortKey = "recent" | "name" | "status";
const sortKey = ref<SortKey>("recent");

/** Selection for batch delete; deleting 40 stale imports one dialog at a time
 *  is how an operator ends up not cleaning up at all. */
const selectedIds = ref<Set<string>>(new Set());
function toggleSelected(id: string): void {
  const next = new Set(selectedIds.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  selectedIds.value = next;
}

const unsortedRows = computed(() => {
  const query = normalizeQuery(searchText.value);
  return onThisTab.value.filter((item) => {
    if (!matchesFilter(item)) return false;
    if (kindFilter.value && (item.kind || KIND_SUB) !== (kindFilter.value === "collection" ? KIND_COLLECTION : KIND_SUB)) {
      return false;
    }
    return matchesQuery(item, query);
  });
});

/**
 * Chip counts reflect the search too.
 *
 * They used to apply the tag filter but not the search box, so typing left
 * "Subs (12)" sitting above a list of two, two ways of narrowing the same list
 * telling the operator different things.
 */
const searchedRows = computed(() => {
  const query = normalizeQuery(searchText.value);
  return onThisTab.value.filter((item) => matchesFilter(item) && matchesQuery(item, query));
});
const visibleSingles = computed(
  () => searchedRows.value.filter((item) => (item.kind || KIND_SUB) === KIND_SUB).length,
);
const visibleCollections = computed(
  () => searchedRows.value.filter((item) => item.kind === KIND_COLLECTION).length,
);

/** Rank for the status sort: what needs attention first. */
function statusWeight(item: SubscriptionListItem): number {
  const tone = statusOf(item).tone;
  if (tone === "danger") return 0;
  if (tone === "warn") return 1;
  if (tone === "neutral") return 2;
  return 3;
}

const filteredRows = computed(() => {
  const rows = [...unsortedRows.value];
  if (sortKey.value === "name") {
    rows.sort((a, b) => (a.display_name || a.name).localeCompare(b.display_name || b.name));
  } else if (sortKey.value === "status") {
    rows.sort((a, b) => statusWeight(a) - statusWeight(b) || a.name.localeCompare(b.name));
  } else {
    rows.sort((a, b) => String(b.last_fetch_at ?? "").localeCompare(String(a.last_fetch_at ?? "")));
  }
  return rows;
});


/**
 * What "N selected" actually means.
 *
 * The raw set outlives the rows: filter the list, or delete a record from its
 * row menu, and its id stays selected. The bar then offered to delete more
 * records than it could name, and the confirmation listed a bare id for one
 * that no longer existed. Everything the batch controls report and act on is
 * the intersection with what is currently on screen.
 */
const selectedVisible = computed(() =>
  filteredRows.value.filter((row) => selectedIds.value.has(row.id)),
);
const selectedCount = computed(() => selectedVisible.value.length);
const allVisibleSelected = computed(
  () => filteredRows.value.length > 0 && selectedCount.value === filteredRows.value.length,
);

function toggleSelectAll(): void {
  selectedIds.value = allVisibleSelected.value
    ? new Set()
    : new Set(filteredRows.value.map((row) => row.id));
}

/**
 * Records are shown as a grouped list, the way Sub-Store shows them: one
 * section for single subscriptions and one for combinations, each collapsible
 * and carrying its own count.
 *
 * This replaced a dense table whose fixed column widths could not hold real
 * data: inside the console's frame a name like "merge-cd-openjobs" wrapped
 * onto three lines, its tags spilled into the neighbouring column, and two
 * columns (Status, Quota) were "Never refreshed" and "-" for every row. A
 * table earns its columns by having values in them; this data does not.
 */
const groups = computed(() => {
  const singles = filteredRows.value.filter((row) => (row.kind || KIND_SUB) !== KIND_COLLECTION);
  const collections = filteredRows.value.filter((row) => (row.kind || KIND_SUB) === KIND_COLLECTION);
  return [
    { id: "subs", label: "Subscriptions", rows: singles },
    { id: "collections", label: "Combinations", rows: collections },
  ].filter((group) => group.rows.length > 0);
});

const collapsedGroups = ref<Set<string>>(new Set());
function toggleGroup(id: string): void {
  const next = new Set(collapsedGroups.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  collapsedGroups.value = next;
}

/** Which record's per-row menu is open; only ever one. */
const openMenuId = ref("");

/**
 * A popover that only closes when another one opens is a popover the operator
 * has to fight. Outside click and Escape both dismiss it, and because it is
 * absolutely positioned it never changes the document height, on the last row
 * it could otherwise extend past the frame with its own items unreachable, so
 * opening one also re-reports the height.
 */
function closeRowMenu(): void {
  const id = openMenuId.value;
  openMenuId.value = "";
  // Focus falls to the document when the menu it was in disappears, which
  // drops a keyboard operator back at the top of the page. It belongs on the
  // control that opened the menu.
  if (id) {
    void nextTick(() => {
      const trigger = document.querySelector<HTMLElement>(`[data-row-menu="${cssEscape(id)}"] button`);
      trigger?.focus();
    });
  }
}

/** Ids come from the store and are not guaranteed selector-safe. */
function cssEscape(value: string): string {
  const escape = (globalThis as { CSS?: { escape?: (v: string) => string } }).CSS?.escape;
  return escape ? escape(value) : value.replace(/["\\]/g, "\\$&");
}

async function toggleRowMenu(id: string): Promise<void> {
  const opening = openMenuId.value !== id;
  openMenuId.value = opening ? id : "";
  await host.resize();
  if (!opening) return;
  // A menu that opens without focus is a menu Escape cannot close and arrow
  // keys cannot reach, which is most of what `role="menu"` promises.
  await nextTick();
  document.querySelector<HTMLElement>(`[data-row-menu="${cssEscape(id)}"] .rec-menu button:not(:disabled)`)?.focus();
}

/** Up and down walk the open menu; Escape is handled at the document. */
function onRowMenuKeydown(event: KeyboardEvent): void {
  const step = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
  if (!step) return;
  event.preventDefault();
  const menu = (event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>("button:not(:disabled)");
  const items = [...menu];
  if (!items.length) return;
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  items[(current + step + items.length) % items.length]?.focus();
}
// Re-read on return: a file saved on the sibling tab changes what this list
// can point at, and a restore from Settings replaces everything.
onActivated(() => {
  if (host.init.value) void loadAll();
});

/**
 * The document listeners are bound while this screen is the visible one, not
 * for the life of the component.
 *
 * The shell keeps both record screens alive across tab switches (`<KeepAlive>`
 * in Shell.vue), so `onBeforeUnmount` does not run when the operator moves to
 * the sibling tab. Both screens' Escape handlers therefore stayed live at
 * once: with a Files draft open and the Subscriptions tab in front, one
 * Escape reached the Files screen and closed its editor, or raised a discard
 * dialog on a screen nobody could see. `onDeactivated` is the matching half
 * of `onActivated`, and binding to that pair means exactly one screen owns
 * the key at a time.
 */
function bindDocumentKeys(): void {
  document.addEventListener("click", onDocumentClick, true);
  document.addEventListener("keydown", onDocumentKeydown);
}
function releaseDocumentKeys(): void {
  document.removeEventListener("click", onDocumentClick, true);
  document.removeEventListener("keydown", onDocumentKeydown);
}

onMounted(() => {
  bindDocumentKeys();
});
onActivated(bindDocumentKeys);
onDeactivated(releaseDocumentKeys);
onBeforeUnmount(releaseDocumentKeys);
function onDocumentClick(event: MouseEvent): void {
  if (!openMenuId.value) return;
  const target = event.target as HTMLElement | null;
  if (target?.closest("[data-row-menu]")) return;
  closeRowMenu();
}
/**
 * The one Escape arbiter for this screen, in the order the operator built the
 * stack in: the topmost overlay, then the row menu, then the open row, then
 * the editor.
 *
 * Every overlay used to answer the key itself with `@keydown.esc.stop`, and
 * the `.stop` was the only thing keeping one press from closing a dialog and
 * then re-raising it from the screen underneath. They register with
 * overlayStack now and none of them handles the key, so there is exactly one
 * decision and adding an eighth overlay cannot forget to join it.
 */
function onDocumentKeydown(event: KeyboardEvent): void {
  if (event.key !== "Escape") return;
  if (closeTopOverlay()) return;
  if (openMenuId.value) {
    closeRowMenu();
    return;
  }
  if (expandedId.value && !editing.value) {
    collapseRow();
    return;
  }
  // Escape is how every other surface in this frame steps back, and the editor
  // is a screen you enter, so it answers the same key. Who owns the key while
  // an overlay is up is decided in editorExit.ts.
  exit.onEscape();
}
/**
 * What this session may do, in the shape the action registry reads. One place
 * to answer "why is that greyed out", rather than an inline expression per
 * control that drifts from its neighbours.
 */
/**
 * How many records this tab holds, or null while that is not known: before
 * the host answers, during a load, and after a load that failed. The last
 * case printed "0 / 256" over the error, a count of records it never saw.
 */
const listed = computed(() =>
  !host.init.value || subs.loadError.value || subs.state.value === "loading" ? null : onThisTab.value.length,
);

const actionCaps = computed<ActionCapabilities>(() => ({
  ready: !!host.init.value,
  mutate: subs.canMutate.value,
  fetch: subs.canFetch.value,
  preview: subs.canPreview.value,
  render: subs.canRender.value,
  publish: subs.canPublish.value,
}));

const MENU_ACTIONS = ["preview", "share", "publish", "duplicate", "delete"] as const;

function menuActionsFor(row: SubscriptionListItem) {
  return actionsFor(row, actionCaps.value, MENU_ACTIONS).map((action) =>
    action.id === "share" ? shareActionFor(row, action) : action,
  );
}

/**
 * The share item named by what it will do for this row. A record with no
 * share gets published, a live share gets its link copied, a dead one gets
 * renewed in the console. The menu said "Share…" for all three and left the
 * operator to find out which.
 */
function shareActionFor(row: SubscriptionListItem, action: ResolvedAction): ResolvedAction {
  const state = publishedOf(row);
  if (state.tone === "ok") {
    return { ...action, label: "Copy share link", icon: "link", title: `Copies the link a client fetches, ${state.label}.` };
  }
  if (state.tone === "warn") {
    return { ...action, label: "Renew share…", title: `${state.title} Renewing it happens in the console, under Networking.` };
  }
  return action;
}

/** The tags a row shows: two, then "+N", the whole list in the title. */
function tagChips(row: SubscriptionListItem) {
  return tagChipsOf(row.tags, row.imported);
}

/**
 * The name opens the record where this session can edit it. Where it cannot,
 * the name is text: a button that opens nothing is worse than none, and the
 * » at the row's end still gives the client output.
 */
function openRecord(row: SubscriptionListItem, event: MouseEvent): void {
  if (!rowAction(row, "edit").disabled) runRowAction("edit", row, event);
}
function nameTitle(row: SubscriptionListItem): string {
  const edit = rowAction(row, "edit");
  return edit.disabled ? `${row.id}. ${edit.reason}` : `${row.id}. Open ${row.display_name || row.name}`;
}

/**
 * What the selection can carry. A batch is allowed only where every record in
 * it allows the action: reporting "Delete 12" and then refusing four of them
 * is worse than saying up front that the set cannot go.
 */
const batchActions = computed(() => batchActionsFor(selectedVisible.value, actionCaps.value));

/** One resolved action, for the icon buttons that sit in the row itself. */
function rowAction(row: SubscriptionListItem, id: ActionId) {
  return (
    actionsFor(row, actionCaps.value, [id])[0] ?? { id, label: "", icon: "", danger: false, reason: "", disabled: true }
  );
}

/**
 * Requests from the palette, which can see every record but cannot open this
 * screen's drawers. Only intents this screen owns are taken: both screens are
 * kept alive and both watch, so the sibling must be able to find its own.
 */
const intent = recordIntent(host);
watch(
  intent,
  (value) => {
    if (isCommandIntent(value) && value.command !== "new-file") {
      claimIntent(intent, () => true);
      startCreate(value.command === "new-collection" ? KIND_COLLECTION : KIND_SUB);
      return;
    }
    if (!isRecordIntent(value)) return;
    const row = subs.items.value.find((item) => item.id === value.recordId);
    if (!row || row.kind === KIND_FILE) return;
    claimIntent(intent, () => true);
    runRowAction(value.action, row, new MouseEvent("click"));
  },
  { immediate: true },
);

/**
 * The registry says what and when; this says how. Every caller goes through
 * here — the row's icon buttons, its menu, and the palette — so an action
 * means the same thing wherever it was started from.
 */
function runRowAction(id: ActionId, row: SubscriptionListItem, event: MouseEvent): void {
  closeRowMenu();
  if (id === "edit") return void startEdit(row.id);
  if (id === "refresh") return void refreshRow(row.id);
  if (id === "output") return openTargetSheet(row, event);
  if (id === "preview") return openDrawer("preview", row.id, event);
  if (id === "share") {
    return publishedOf(row).tone === "ok" ? void copyShareLink(row) : openDrawer("share", row.id, event);
  }
  if (id === "publish") return openDrawer("publish", row.id, event);
  if (id === "duplicate") return void subs.duplicate(row.id);
  if (id === "delete") return requestDelete([row.id]);
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

/** The preview/copy sheet: the one-click path to a client configuration. The
 *  whole row goes in, because the sheet's shape depends on what the record is
 *  (a file has no client to pick) and not only on its id. */
const targetSheet = ref<SubscriptionListItem | null>(null);
const targetSheetTrigger = ref<HTMLElement | null>(null);
/** What opened the panel, so focus goes back there when it closes. The panel
 *  is told rather than measuring an event, which is what the retired anchoring
 *  model was doing with the same click. */
const drawerTrigger = ref<HTMLElement | null>(null);
function openTargetSheet(row: SubscriptionListItem, event?: Event): void {
  openMenuId.value = "";
  targetSheetTrigger.value = (event?.currentTarget as HTMLElement | null | undefined) ?? null;
  targetSheet.value = row;
}

function closeTargetSheet(): void {
  targetSheet.value = null;
  const trigger = targetSheetTrigger.value;
  targetSheetTrigger.value = null;
  void nextTick(() => trigger?.focus());
}

function sourceTone(item: SubscriptionListItem): "neutral" | "accent" {
  return item.source === SOURCE_VPN_CORE || item.source === SOURCE_VPN_CORE_GRAPH ? "accent" : "neutral";
}

function statusOf(item: SubscriptionListItem): { tone: "ok" | "warn" | "danger" | "neutral"; label: string; title?: string } {
  return refreshStateFor(item);
}

// ── published shares ────────────────────────────────────────────────────────
// The host's share list, the one copy the Shares lens and the lens switch
// read too, folded onto each row. `undefined` until it has been read, so the
// column can say "not yet" rather than "not published" while the call is in
// flight.
const shareStore = useShares(host);
const shares = shareStore.shares;
const sharesError = shareStore.error;
function publishedOf(item: SubscriptionListItem) {
  return publishStateFor(shares.value, item.id);
}
const publishedCount = computed(() => shares.value === undefined
  ? null
  : filteredRows.value.filter((row) => publishStateFor(shares.value, row.id).tone === "ok").length);

// ── node counts ─────────────────────────────────────────────────────────────
// The server keeps no count from the last preview or fetch (`list` carries
// fetch bookkeeping and operation counts, a preview's node_count lives only
// in its reply), so the NODES column is computed here: lazily, once per
// record per session, two previews in flight at a time, through the same
// read-scoped `preview` the row's eye uses. The rows render first and print
// "?" until their count lands; a preview of a provider link fetches the
// provider, exactly as the eye does.
const counts = createNodeCountQueue((id) => {
  if (!host.bridge) return Promise.reject(new Error("The console is not connected"));
  return callMethod<SubscriptionPreviewResponse>(host.bridge, BINDINGS.subPreview, { subscription_id: id })
    .promise.catch((cause) => {
      // The reason is shown in the cell's title, so it goes through the same
      // redaction every other error does: a fetch failure quotes the link.
      throw new Error(safeErrorMessage(cause, "Preview failed"));
    });
});
watch(
  () => (subs.canPreview.value && !editing.value ? filteredRows.value.map((row) => row.id) : []),
  (ids) => counts.request(ids),
  { immediate: true },
);
function nodesOf(row: SubscriptionListItem): string {
  return nodeCountLabel(counts.stateOf(row.id));
}
function nodesTitle(row: SubscriptionListItem): string {
  return nodeCountTitle(counts.stateOf(row.id), subs.canPreview.value);
}
/** The node set may have changed: count it again on the next render. */
function recount(id: string): void {
  counts.forget(id);
  if (subs.canPreview.value && filteredRows.value.some((row) => row.id === id)) counts.request([id]);
}

// ── inline chain ────────────────────────────────────────────────────────────
// A row expands to its operations and what each one kept, so the chain can be
// read without opening the editor. The list item carries only counts, so the
// record is read on expand and the chain explained the way the editor does:
// one partial run per enabled operation. One row at a time; Escape collapses.
interface RowChain {
  id: string;
  loading: boolean;
  error: string;
  record: SubscriptionRecord | null;
  explanation: ChainExplanation | null;
  /** The chain position being previewed right now. */
  running: number | null;
}
const expandedId = ref("");
const rowChain = ref<RowChain | null>(null);
const revealSource = useReveal();

const chainSteps = computed<ChainStep[]>(() => {
  const record = rowChain.value?.record;
  if (!record) return [];
  const process = Array.isArray(record.process) && record.process.length ? record.process : record.operators;
  return (Array.isArray(process) ? process : []) as ChainStep[];
});
const chainIsCombination = computed(() => (rowChain.value?.record?.kind || KIND_SUB) === KIND_COLLECTION);
const chainDropped = computed(() => rowChain.value?.explanation?.final?.dropped ?? []);

function chainDeltaText(index: number): string {
  const chain = rowChain.value;
  const step = chainSteps.value[index];
  if (!step || !chain) return "";
  if (step.disabled) return "off";
  if (chainIsCombination.value) return "";
  const delta = chain.explanation?.deltas.find((entry) => entry.index === index);
  if (delta) {
    if (delta.after === delta.before) return `${delta.after}, none removed`;
    if (delta.after < delta.before) return `kept ${delta.after} of ${delta.before}`;
    return `${delta.before} became ${delta.after}`;
  }
  if (chain.running === index) return "running…";
  if (!subs.canPreview.value) return "";
  if (chain.explanation) return chain.explanation.complete ? "" : "not run";
  return "…";
}

/** Which operation removed a node, by the key the runs are folded on. */
function droppedBy(node: SubscriptionPreviewNode): string {
  return rowChain.value?.explanation?.droppedBy.get(nodeKey(node)) ?? "the chain";
}

function collapseRow(): void {
  const id = expandedId.value;
  expandedId.value = "";
  rowChain.value = null;
  revealSource.hide();
  if (id) {
    void nextTick(() => {
      document.querySelector<HTMLElement>(`[data-expand="${cssEscape(id)}"]`)?.focus();
    });
  }
  void host.resize();
}

async function toggleRow(id: string): Promise<void> {
  if (expandedId.value === id) {
    collapseRow();
    return;
  }
  expandedId.value = id;
  revealSource.hide();
  rowChain.value = { id, loading: true, error: "", record: null, explanation: null, running: null };
  await host.resize();
  const record = await subs.get(id);
  if (expandedId.value !== id) return;
  if (!record) {
    rowChain.value = { id, loading: false, error: subs.actionError.value || "The record could not be read", record: null, explanation: null, running: null };
    subs.actionError.value = "";
    return;
  }
  rowChain.value = { id, loading: false, error: "", record, explanation: null, running: null };
  // Work on the ref's own proxy, not the object handed to it: writes through
  // a plain copy would render nothing, and the copy never equals the proxy.
  const chain = rowChain.value;
  await host.resize();
  const bridge = host.bridge;
  if (!bridge || !subs.canPreview.value) return;
  const steps = chainSteps.value;
  const current = () => rowChain.value?.id === id && expandedId.value === id;
  try {
    if ((record.kind || KIND_SUB) === KIND_COLLECTION || !enabledStepIndexes(steps).length) {
      // The engine runs a combination's operations over its members' merged
      // output and reports one result, and a chain with nothing enabled has
      // nothing to account for: one whole run answers both.
      const result = await callMethod<SubscriptionPreviewResponse>(bridge, BINDINGS.subPreview, { subscription_id: id }).promise;
      if (!current()) return;
      chain.explanation = { deltas: [], droppedBy: new Map(), final: result, complete: true };
      counts.record(id, result);
      return;
    }
    const explanation = await explainChain(steps, async (upTo) => {
      if (!current()) throw new Error("collapsed");
      chain.running = upTo;
      try {
        return await callMethod<SubscriptionPreviewResponse>(bridge, BINDINGS.subPreview, {
          subscription_id: id,
          operators: cutChain(steps, upTo),
        }).promise;
      } catch (cause) {
        if (current()) chain.error = safeErrorMessage(cause, "Preview failed");
        throw cause;
      } finally {
        chain.running = null;
      }
    });
    if (!current()) return;
    chain.explanation = explanation;
    if (explanation.complete && explanation.final) counts.record(id, explanation.final);
  } finally {
    await host.resize();
  }
}

// ── row + batch operations ──────────────────────────────────────────────────

function markPending(id: string, on: boolean): void {
  const next = new Set(pendingIds.value);
  if (on) next.add(id);
  else next.delete(id);
  pendingIds.value = next;
}

async function refreshRow(id: string): Promise<void> {
  markPending(id, true);
  try {
    await subs.refresh(id);
  } finally {
    markPending(id, false);
    recount(id);
  }
}

function requestDelete(ids: string[]): void {
  deleting.value = ids;
}

const deletingNames = computed(() => namesFor(deleting.value));

/**
 * The records that break if this delete goes ahead, found rather than described.
 *
 * The dialog already warned, in general terms, that "any combination that
 * includes it stops rendering". Every reference it was talking about is
 * computable from the list already on screen: a combination names its parts in
 * `members`, and a file draws its nodes from `node_source`. So the warning is
 * now the actual names, and a record with no dependents no longer carries a
 * warning about dependents it does not have.
 */
const deleteDependents = computed(() => {
  const doomed = new Set(deleting.value);
  if (!doomed.size) return [] as Array<{ name: string; because: string }>;
  const found: Array<{ name: string; because: string }> = [];
  for (const item of subs.items.value) {
    if (doomed.has(item.id)) continue;
    const label = item.display_name || item.name;
    if ((item.members ?? []).some((member) => doomed.has(member))) {
      found.push({ name: label, because: "combination, loses a member" });
      continue;
    }
    if (item.node_source && doomed.has(item.node_source)) {
      found.push({ name: label, because: "file, loses its node source" });
    }
  }
  return found;
});

/**
 * What will break, phrased for the dialog's second list. Kept out of `names`
 * because `names` is what the operator types the count of to arm the confirm,
 * and these records are not being deleted.
 */
const deleteConsequences = computed(() =>
  deleteDependents.value.map((entry) => `${entry.name}  (${entry.because})`),
);

const deleteTitle = computed(() => {
  const count = deleting.value.length;
  const one = count === 1;
  const subject = one ? "this record" : `${count} records`;
  const object = one ? "it" : "them";
  const dependents = deleteDependents.value.length;
  const shares = one
    ? "Any share published for it keeps existing and starts returning nothing."
    : "Any share published for them keeps existing and starts returning nothing.";
  if (!dependents) return `Delete ${subject}? Nothing else in this store points at ${object}. ${shares}`;
  const breaks = dependents === 1
    ? `1 other record in this store points at ${object} and stops working`
    : `${dependents} other records in this store point at ${object} and stop working`;
  return `Delete ${subject}? ${breaks} until you edit them, listed below. ${shares}`;
});

/**
 * What a partly-finished batch delete left behind.
 *
 * Null while nothing has half-failed. Set when a run stops early, and read by
 * the strip that offers a retry of just the part that did not happen.
 */
const deleteRemainder = ref<{ done: string[]; failed: string; pending: string[] } | null>(null);

/** Display names for a set of ids, falling back to the id when it is gone. */
function namesFor(ids: string[]): string[] {
  return ids.map((id) => {
    const item = subs.items.value.find((r) => r.id === id);
    return item ? item.display_name || item.name : id;
  });
}

async function runDelete(): Promise<void> {
  deleteBusy.value = true;
  deleteRemainder.value = null;
  const queue = [...deleting.value];
  const done: string[] = [];
  try {
    for (let index = 0; index < queue.length; index += 1) {
      const id = queue[index]!;
      markPending(id, true);
      const ok = await subs.remove(id);
      markPending(id, false);
      if (ok) {
        done.push(id);
        continue;
      }
      // Stopping is right: the rest of the batch is likely to fail the same
      // way, and deleting on through an error the operator has not read is how
      // a wrong batch runs to completion. But stopping silently was not. The
      // dialog used to close on this path having said only which record
      // failed, so a run of twelve that died on the sixth left no way to know
      // that five were gone and six had never been attempted.
      deleteRemainder.value = { done, failed: id, pending: queue.slice(index + 1) };
      return;
    }
  } finally {
    deleteBusy.value = false;
    deleting.value = [];
    // The selection is kept when there is a remainder to retry. Clearing it was
    // the second half of the same bug: the records that were never attempted
    // had to be found again by hand.
    if (!deleteRemainder.value) selectedIds.value = new Set();
  }
}

/** Retry only the records the stopped run never reached, plus the one that failed. */
function retryDeleteRemainder(): void {
  const remainder = deleteRemainder.value;
  if (!remainder) return;
  deleteRemainder.value = null;
  deleting.value = [remainder.failed, ...remainder.pending];
}

// ── drawer ──────────────────────────────────────────────────────────────────

const drawerItem = computed(() =>
  drawer.value ? subs.items.value.find((r) => r.id === drawer.value?.id) : undefined,
);
const drawerTitle = computed(() => {
  if (!drawer.value || !drawerItem.value) return "";
  const name = drawerItem.value.display_name || drawerItem.value.name;
  if (drawer.value.mode === "preview") return `Preview · ${name}`;
  if (drawer.value.mode === "publish") return `Upload · ${name}`;
  return publishedOf(drawerItem.value).tone === "warn" ? `Renew share · ${name}` : `Publish · ${name}`;
});

function openDrawer(mode: "preview" | "publish" | "share", id: string, event?: Event): void {
  drawerTrigger.value = (event?.currentTarget as HTMLElement | null | undefined) ?? null;
  drawer.value = { mode, id };
  if (mode === "preview" && subs.rowPreview.value?.id !== id) {
    void subs.toggleRowPreview(id);
  }
}

function closeDrawer(): void {
  if (drawer.value?.mode === "preview" && subs.rowPreview.value) {
    void subs.toggleRowPreview(subs.rowPreview.value.id);
  }
  drawer.value = null;
}

async function publishFromDrawer(destination: string, method: string, format: string): Promise<void> {
  if (!drawer.value) return;
  if (await subs.publish(drawer.value.id, destination, method, format)) closeDrawer();
}

// ── sharing ─────────────────────────────────────────────────────────────────

/**
 * Shares are published by the dashboard, not by this frame: the frame can only
 * ask the console to navigate there. The origin is the one the bridge pinned
 * from the frame URL, re-read here rather than trusted from a second source.
 */
const shareOrigin = computed(() => hostOriginFromHash(window.location.hash));

/**
 * The console's share view takes a record name only for its create form; an
 * existing share is found in the list, so a record that already has one opens
 * the list rather than a second create form.
 */
function openShares(record: SubscriptionListItem): void {
  if (!shareOrigin.value) return;
  const route = publishedOf(record).shares.length ? SHARES_LIST_ROUTE : sharesRoute(record.name);
  postNavigate(window, route, shareOrigin.value);
  closeDrawer();
  subs.notice.value = "Asked the console to open Networking → Subscription Shares.";
}

/**
 * The link this row's live share serves, when it could not be put on the
 * clipboard. Held here rather than in the row so the reveal survives the row
 * list re-sorting under it, and cleared by the next copy or by dismissing it.
 */
const manualShareLink = ref<{ id: string; label: string; value: string } | null>(null);

/** The live share's link onto the clipboard, the way the Shares lens copies it. */
async function copyShareLink(row: SubscriptionListItem): Promise<void> {
  const state = publishedOf(row);
  const share = state.shares.find((candidate) => candidate.slug === state.slug);
  if (!share) return;
  const link = share.url || share.path;
  if (!link) return;
  manualShareLink.value = null;
  if (await copyText(link)) {
    subs.actionError.value = "";
    subs.notice.value = `Copied the link for ${state.label}.`;
    return;
  }
  // Sending the operator to another lens to do by hand what this button was
  // for is not a recovery. The link goes on screen here, selected.
  subs.notice.value = "";
  subs.actionError.value = "";
  manualShareLink.value = { id: row.id, label: state.label, value: link };
  await host.resize();
}

/**
 * Load after the bridge handshake, not on mount.
 *
 * `available()` reads the interfaces the host declares for this frame, and on
 * first paint that has not arrived, so loading in `onMounted` alone silently
 * no-ops and never retries.
 */
async function loadAll(): Promise<void> {
  void shareStore.load();
  await subs.load();
  await subs.loadOperators();
}

onMounted(() => {
  if (host.init.value) void loadAll();
});

watch(host.init, (value) => {
  if (value) void loadAll();
});

watch(() => draft.value.vpnIdentity, (identity, previous) => {
  if (draft.value.source !== SOURCE_VPN_CORE_GRAPH || identity === previous || !subs.graphOptions.value) return;
  const allowed = new Set(subs.graphOptions.value.roots.filter((root) => root.selectable && root.eligible_identity_ids.includes(identity)).map((root) => root.line_uuid));
  const before = draft.value.entryRoots.length;
  draft.value.entryRoots = draft.value.entryRoots.filter((root) => allowed.has(root));
  if (before !== draft.value.entryRoots.length) {
    subs.actionError.value = "Some selected roots were removed because they are not eligible for this identity.";
  }
});
</script>

<template>
  <EngineUnavailable v-if="host.init.value && !subs.available.value" feature="Subscriptions" />

  <template v-else>
    <!-- ── editor ───────────────────────────────────────────────────────── -->
    <section v-if="editing" class="configuration editor-shell" aria-labelledby="editor-title">
      <nav class="lt-breadcrumb" aria-label="Breadcrumb">
        <button type="button" class="lt-breadcrumb-root" @click="leaveEditor">
          <ChevronLeft :size="14" aria-hidden="true" /> Subscriptions
        </button>
        <span class="lt-breadcrumb-sep" aria-hidden="true">/</span>
        <span class="lt-breadcrumb-here" aria-current="page">
          {{ editingId ? draft.displayName || draft.name || editingId : (isCollection ? "New combination" : "New subscription") }}
        </span>
      </nav>
      <div class="section-heading">
        <div>
          <h2 id="editor-title">
            {{ editingId ? "Edit" : "New" }}
            {{ isCollection ? "combination" : "subscription" }}
            <!-- The draft survives a switch to another lens and back; this
                 says so on return, so an edit is not mistaken for saved. -->
            <span v-if="editorDirty" class="editor-dirty" role="status" title="Not saved yet. The draft stays here while you look at another lens.">Unsaved changes</span>
          </h2>
          <p v-if="isCollection">
            Merges several subscriptions and processes the merged result as one.
          </p>
          <p v-else>One source of nodes, processed and served.</p>
        </div>
      </div>

      <div v-if="subs.actionError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
      </div>

      <!--
        A save refused because the record moved underneath it. Rendered where
        the operator is, above the editor they are still holding, rather than
        as a dialog: their work is on the screen behind it and covering that up
        while asking whose version wins is the wrong way round. Nothing is
        merged and nothing is discarded until they choose.
      -->
      <section v-if="subs.saveConflict.value" class="conflict-panel" role="alert" aria-labelledby="conflict-title">
        <div class="conflict-panel__head">
          <TriangleAlert :size="16" aria-hidden="true" />
          <h3 id="conflict-title" class="conflict-panel__title">Your edit was not saved</h3>
        </div>
        <p class="conflict-panel__summary">{{ subs.saveConflict.value.summary }}</p>

        <table v-if="subs.saveConflict.value.changes.length" class="conflict-table">
          <thead>
            <tr>
              <th scope="col">Field</th>
              <th scope="col">When you opened it</th>
              <th scope="col">Now</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="change in subs.saveConflict.value.changes"
              :key="change.label"
              :class="{ 'is-contested': change.contested }"
            >
              <th scope="row">
                {{ change.label }}
                <span v-if="change.contested" class="conflict-tag">you edited this too</span>
              </th>
              <td class="mono">{{ change.before }}</td>
              <td class="mono">{{ change.after }}</td>
            </tr>
          </tbody>
        </table>

        <div class="conflict-panel__actions">
          <LtButton variant="primary" @click="reopenOnCurrent()">
            Reopen on their version
          </LtButton>
          <LtButton @click="overwriteWithMine()">Replace theirs with mine</LtButton>
          <LtButton @click="discardMyEdit()">Discard my edit</LtButton>
        </div>
        <p class="conflict-panel__note">
          Reopening loses what you typed. Replacing loses what they wrote. Nothing here merges
          the two, because an operator chain merged without being read is a configuration nobody
          wrote.
        </p>
      </section>

      <nav class="editor-tabs" role="tablist" aria-label="Editor sections">
        <button
          v-for="tab in EDITOR_TABS"
          :key="tab.id"
          type="button"
          role="tab"
          class="editor-tab"
          :aria-selected="editorTab === tab.id"
          :data-active="editorTab === tab.id"
          @click="editorTab = tab.id"
        >
          {{ tab.label }}
          <span v-if="tab.id === 'operations' && chainCount" class="editor-tab-count">{{ chainCount }}</span>
          <span
            v-if="errorTab === tab.id && editorTab !== tab.id"
            class="editor-tab-flag"
            :title="draftError"
            aria-label="This section has a problem"
          />
        </button>
      </nav>

      <div class="editor-layout">
      <form class="editor-main" @submit.prevent="submit">
      <fieldset v-show="editorTab === 'display'" class="editor-group">
        <legend>Basics</legend>
        <div class="form-grid">
        <label class="field field-wide">
          <span class="field-label">Name</span>
          <input
            v-model="draft.name"
            type="text"
            autocomplete="off"
            :placeholder="isCollection ? 'Everything' : 'Home nodes'"
          />
          <span class="field-optional">
            <template v-if="editingId">
              Stored as <code>{{ editingId }}</code>. Renaming is safe. A published share keeps
              working.
            </template>
            <template v-else>The only thing you have to fill in.</template>
          </span>
        </label>

        <label class="field">
          <span class="field-label">Display name <span class="field-optional">(optional)</span></span>
          <input v-model="draft.displayName" type="text" autocomplete="off" placeholder="Home" />
          <span class="field-optional">Shown in the list instead of the name.</span>
        </label>

        <label class="field">
          <span class="field-label">Tags</span>
          <input
            v-model="tagText"
            type="text"
            autocomplete="off"
            spellcheck="false"
            placeholder="home, backup"
          />
          <span class="field-optional">Used to group, filter, and gather by.</span>
        </label>

        <label class="field field-wide">
          <span class="field-label">Note</span>
          <input v-model="draft.remark" type="text" autocomplete="off" placeholder="Optional" />
        </label>
        </div>
      </fieldset>

      <fieldset v-show="editorTab === 'content'" class="editor-group">
        <legend>{{ isCollection ? "What it gathers" : "Where the nodes come from" }}</legend>
        <div class="form-grid">
        <!-- ── sub: where the nodes come from ─────────────────────────── -->
        <div v-if="!isCollection" class="field field-wide">
          <!-- These cards are a single choice, so they carry the semantics of
               one: a radiogroup whose selected member is announced, not a row
               of buttons distinguishable only by tint. -->
          <div class="source-grid" role="radiogroup" aria-label="Where the nodes come from">
            <button
              v-for="option in SOURCES"
              :key="option.id"
              type="button"
              role="radio"
              :aria-checked="draft.source === option.id"
              :class="['source', { 'is-active': draft.source === option.id }]"
              @click="selectSource(option.id)"
            >
              <component :is="option.icon" :size="17" aria-hidden="true" />
              <span class="source-title">{{ option.title }}</span>
              <span class="source-detail">{{ option.detail }}</span>
            </button>
          </div>
        </div>

        <label v-if="!isCollection && draft.source === SOURCE_VPN_CORE" class="field field-wide">
          <span class="field-label">Limit to one VPN user</span>
          <input
            v-model="draft.vpnIdentity"
            type="text"
            autocomplete="off"
            spellcheck="false"
            placeholder="Leave empty to include everyone's nodes"
          />
          <span class="field-optional">
            The export returns every node this fleet serves. Naming a proxy user narrows it to
            theirs, useful when one share is meant for one person.
          </span>
        </label>

        <GraphSubscriptionEditor
          v-if="!isCollection && draft.source === SOURCE_VPN_CORE_GRAPH"
          :draft="draft"
          :options="subs.graphOptions.value"
          :loading="subs.graphOptionsLoading.value"
          :read-only="!subs.canMutate.value"
          @reload="reloadGraphOptions"
          @identity="setGraphIdentity"
          @add="addGraphRoot"
          @remove="removeGraphRoot"
          @move="moveGraphRoot"
        />

        <template v-if="!isCollection && draft.source === SOURCE_REMOTE">
          <!-- The link carries the provider's token, so it reads masked and
               shows whole only while it is being edited or revealed. -->
          <div class="field field-wide">
            <span class="field-label">Provider link</span>
            <MaskedUrlInput
              v-model="draft.url"
              aria-label="Provider link"
              placeholder="The subscription link your provider gave you"
            />
            <span class="field-optional">Shown masked after the host; the record keeps the whole link.</span>
          </div>
          <label class="field">
            <span class="field-label">User agent</span>
            <input v-model="draft.ua" type="text" autocomplete="off" placeholder="Optional" />
            <span class="field-optional">
              Some providers return a different list per client. Set this if yours does.
            </span>
          </label>
        </template>

        <div v-if="!isCollection && draft.source === SOURCE_LOCAL" class="field field-wide">
          <span id="draft-nodes-label" class="field-label">Nodes</span>
          <CodeEditor
            aria-labelledby="draft-nodes-label"
            v-model="draft.content"
            language="plain"
            :rows="12"
            placeholder="Paste node links, a base64 blob, Clash YAML, or sing-box JSON"
          />
          <span class="field-optional">
            Mixed lists work. One node per line for link formats.
          </span>
        </div>

        <!-- ── collection: what it gathers ────────────────────────────── -->
        <template v-if="isCollection">
          <div class="field field-wide">
            <span class="field-label">Choose subscriptions</span>
            <MemberPicker
              :candidates="memberCandidates"
              :selected="draft.members"
              @update:selected="draft.members = $event"
            />
          </div>

          <label class="field field-wide">
            <span class="field-label">…and everything tagged</span>
            <input
              v-model="memberTagText"
              type="text"
              autocomplete="off"
              spellcheck="false"
              placeholder="home, backup"
            />
            <span class="field-optional">
              Gathering by tag means a new subscription joins by being tagged, without editing
              this combination.
            </span>
          </label>

          <div class="field field-wide">
            <span class="field-label">If a member cannot be fetched</span>
            <div class="choice-row">
              <button
                type="button"
                :class="{ 'is-active': draft.failureMode !== FAILURE_SKIP }"
                @click="draft.failureMode = FAILURE_STRICT"
              >
                Fail the whole thing
              </button>
              <button
                type="button"
                :class="{ 'is-active': draft.failureMode === FAILURE_SKIP }"
                @click="draft.failureMode = FAILURE_SKIP"
              >
                Skip it and serve the rest
              </button>
            </div>
            <span class="field-optional">
              Failing is the safer default: serving only the survivors reaches a client as “those
              nodes were removed”, and it deletes them. Skipping is right when one flaky provider
              should not take down a large combination.
            </span>
          </div>
        </template>

        </div>
      </fieldset>

      <fieldset v-show="editorTab === 'content'" class="editor-group">
        <legend>Output</legend>
        <div class="form-grid">
        <label class="field">
          <span class="field-label">Client format</span>
          <select v-model="draft.target" class="select">
            <option value="">Decide from the client that asks</option>
            <option v-for="target in CONVERT_TARGETS" :key="target.id" :value="target.id">
              {{ target.label }}
            </option>
          </select>
          <span class="field-optional">
            Left automatic, Surge gets Surge and Clash gets Clash from one URL.
          </span>
        </label>

        </div>
      </fieldset>

      <div v-show="editorTab === 'operations'" class="editor-block">
        <CommonSettingsBlock :model-value="common" @update:model-value="onCommonChange" />
      </div>

      <div v-show="editorTab === 'operations'" class="editor-block">
          <ProcessChain
            :steps="(draft.process as ChainStep[])"
            :catalog="subs.operators.value"
            :catalog-state="subs.operatorsState.value"
            :managed-types="MANAGED_TYPES"
            :can-preview-step="canPreviewNow"
            :previewing-step="subs.previewing.value ? subs.previewStep.value : null"
            @update:steps="draft.process = $event"
            @preview-step="previewUpToStep"
          />
          <span v-if="isCollection" class="field-optional">
            Each member runs its own operations first; these run over everything merged.
          </span>
      </div>

        <!-- Deliberately not sticky. The frame is a viewport now, so it could
             be, but a bar pinned over a form this tall covers a field for the
             whole time it is being filled in. Save belongs at the end of the
             form; what needed to stay in view was the preview, and that is the
             pane beside it. -->
        <div class="editor-actions">
          <!-- The failure belongs next to the button that produced it: this
               form is long, and a banner at the top is off-screen from the
               click that triggered it. -->
          <span v-if="subs.actionError.value" class="field-error" role="alert">{{ subs.actionError.value }}</span>
          <!-- Clickable, because the field it names is usually on another tab. -->
          <button
            v-else-if="draftError"
            type="button"
            class="field-error field-error-jump"
            :title="`Go to the ${EDITOR_TABS.find((t) => t.id === errorTab)?.label} section`"
            @click="editorTab = errorTab || editorTab"
          >
            {{ draftError }}
          </button>
          <button class="button button-secondary" type="button" @click="leaveEditor">Cancel</button>
          <button class="button button-primary" type="submit" :disabled="!canSave || !subs.canMutate.value">
            <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
            Save
          </button>
        </div>
      </form>

      <!-- What this record would produce, beside the form that decides it. The
           frame is a viewport now, so the pane can stay in view while a long
           form scrolls under it. Below the breakpoint it becomes the last block
           instead: a sticky column in a 375px frame is a column that covers the
           form. -->
      <aside class="editor-side" aria-labelledby="editor-preview-title">
        <div class="editor-side-head">
          <h3 id="editor-preview-title">Source and result</h3>
          <div class="editor-side-actions">
            <button
              class="button button-secondary"
              type="button"
              :disabled="!canPreviewNow || !explainable || explaining"
              :title="!explainable ? 'Add an operation first; there is nothing to explain' : (draftError || 'Run the chain one operation at a time and say what each one kept')"
              @click="explainDraft()"
            >
              <LoaderCircle v-if="explaining" :size="16" class="spin" aria-hidden="true" />
              <ListOrdered v-else :size="16" aria-hidden="true" />
              Explain chain
            </button>
            <button
              class="button button-secondary"
              type="button"
              :disabled="!canPreviewNow || explaining"
              :title="draftError || 'Run the chain and show the nodes it produces'"
              @click="subs.runPreview(draft)"
            >
              <LoaderCircle v-if="subs.previewing.value && !explaining" :size="16" class="spin" aria-hidden="true" />
              <Eye v-else :size="16" aria-hidden="true" />
              {{ subs.preview.value ? "Refresh" : "Preview" }}
            </button>
          </div>
        </div>

        <!-- What the preview could not do as asked and did instead: a read
             session previewing a saved record's stored source. -->
        <p v-if="subs.preview.value && subs.previewNote.value" class="editor-side-note is-note" role="status">
          {{ subs.previewNote.value }}
        </p>
        <SubscriptionPreviewSummary
          v-if="subs.preview.value"
          :preview="subs.preview.value"
          :step-label="subs.previewStep.value === null ? '' : previewStepLabel"
          :deltas="explanation?.deltas ?? []"
          :dropped-by="explanation?.droppedBy"
        />
        <p v-else-if="subs.previewError.value" class="editor-side-note is-error" role="alert">
          {{ subs.previewError.value }}
        </p>
        <p v-else-if="draftError" class="editor-side-note">{{ draftError }}</p>
        <p v-else class="editor-side-note">
          Nothing run yet. Preview walks the chain over this draft without saving it, so the
          operations can be checked before anyone else sees them.
        </p>
      </aside>
      </div>

      <!-- Leaving with unsaved changes. It lives inside the editor because that
           is the only screen it can be asked from: parked next to the list's
           dialogs it was never rendered while the editor was up, and the exit
           silently did nothing at all. -->
      <LtConfirmDialog
        :open="discarding"
        title="Leave without saving? The changes you made to this record are not stored yet and will be lost."
        verb="Discard changes"
        :names="[draft.displayName || draft.name || (editingId ?? 'this record')]"
        @confirm="cancelEdit()"
        @cancel="discarding = false"
      />
    </section>

    <!-- ── list ─────────────────────────────────────────────────────────── -->
    <section v-else class="configuration" aria-labelledby="subs-title">
      <div class="section-heading">
        <div>
          <h2 id="subs-title">Subscriptions</h2>
          <!-- Nothing to sum up while the list is unread or unreadable: a failed
               load used to print "None of these records is published" over the
               error, a verdict about records it never saw. -->
          <p v-if="subs.loadError.value || publishedCount === null">
            A record reaches a client only through a share, published in the console under Networking.
          </p>
          <p v-else-if="publishedCount === 0">
            None of these records is published: no client can fetch any of them until a share exists
            for it, in the console under Networking.
          </p>
          <p v-else>
            {{ publishedCount }} of {{ filteredRows.length }} shown records {{ publishedCount === 1 ? 'is' : 'are' }} published; the rest reach no client until a share exists.
          </p>
        </div>
        <div class="heading-actions">
          <!-- While the list is still coming this said "0 / 256", which is a
               claim and not an unknown: an operator glancing at a slow load
               was told the store was empty. An em-dash says nothing yet. -->
          <span
            class="badge mono"
            :title="listed !== null
              ? `${listed} shown here. The ${MAX_SUBSCRIPTION_RECORDS} record budget is shared with files.`
              : subs.loadError.value
                ? `Unknown: the list could not be read. The ${MAX_SUBSCRIPTION_RECORDS} record budget is shared with files.`
                : `Counting. The ${MAX_SUBSCRIPTION_RECORDS} record budget is shared with files.`"
          >{{ listed ?? "—" }} / {{ MAX_SUBSCRIPTION_RECORDS }}</span>
          <LtButton
            variant="primary"
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value"
            :title="subs.atRecordLimit.value
              ? `The store holds ${MAX_SUBSCRIPTION_RECORDS} records; delete one to add another`
              : !subs.canMutate.value ? 'This session cannot create or delete records here. Either the installed bundle does not declare those methods, or your token lacks the scope.' : ''"
            @click="startCreate(KIND_SUB)"
          >
            <Plus :size="14" aria-hidden="true" /> New subscription
          </LtButton>
          <LtButton
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value || !singles.length"
            :title="!singles.length
              ? 'Create a subscription first. There is nothing to combine'
              : subs.atRecordLimit.value
                ? `The store holds ${MAX_SUBSCRIPTION_RECORDS} records; delete one to add another`
                : !subs.canMutate.value ? 'This session cannot create or delete records here. Either the installed bundle does not declare those methods, or your token lacks the scope.' : ''"
            @click="startCreate(KIND_COLLECTION)"
          >
            <Layers :size="14" aria-hidden="true" /> New combination
          </LtButton>
        </div>
      </div>

      <div v-if="subs.actionError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
      </div>
      <div v-else-if="subs.notice.value" class="alert alert-ok" role="status">
        <CircleCheck :size="16" aria-hidden="true" /> {{ subs.notice.value }}
      </div>
      <div v-if="migrateSummary" class="alert alert-ok" role="status">
        <CircleCheck :size="16" aria-hidden="true" /> {{ migrateSummary }}
      </div>

      <!--
        A batch delete that stopped part way. The error alert above already
        says why the one record failed; this says what that means for the
        other eleven, which is the part the operator cannot work out alone.
      -->
      <div v-if="deleteRemainder" class="partial-strip" role="status">
        <div class="partial-strip__head">
          <span class="partial-strip__label">
            {{ deleteRemainder.done.length }} deleted,
            1 failed,
            {{ deleteRemainder.pending.length }} not attempted
          </span>
          <div class="partial-strip__actions">
            <LtButton
              v-if="deleteRemainder.pending.length || deleteRemainder.failed"
              size="sm"
              variant="primary"
              @click="retryDeleteRemainder()"
            >
              Retry the {{ deleteRemainder.pending.length + 1 }} that remain
            </LtButton>
            <LtButton size="sm" @click="deleteRemainder = null">Dismiss</LtButton>
          </div>
        </div>
        <p class="partial-strip__note">
          The run stopped at
          <strong>{{ namesFor([deleteRemainder.failed])[0] }}</strong>
          so nothing after it was touched. The records below are still here and
          still selected.
        </p>
        <ul class="partial-strip__names">
          <li v-for="name in namesFor([deleteRemainder.failed, ...deleteRemainder.pending])" :key="name" class="mono">
            {{ name }}
          </li>
        </ul>
      </div>

      <!--
        A copy that the clipboard refused. Sits with the other status strips
        rather than inside the row, because the row list re-sorts and a reveal
        anchored to a row would move out from under the operator mid-copy.
      -->
      <div v-if="manualShareLink" class="manual-copy-strip">
        <div class="manual-copy-strip__head">
          <span class="manual-copy-strip__label">Link for {{ manualShareLink.label }}</span>
          <LtButton size="sm" @click="manualShareLink = null">Dismiss</LtButton>
        </div>
        <LtManualCopy :value="manualShareLink.value" subject="link" />
      </div>

      <LtSkeleton v-if="!host.init.value || subs.state.value === 'loading'" :rows="6" :columns="5" />

      <LtEmptyState
        v-else-if="subs.loadError.value"
        kind="error"
        title="The list could not be loaded"
        :detail="subs.loadError.value"
      >
        <LtButton variant="primary" @click="loadAll()">Retry</LtButton>
      </LtEmptyState>

      <LtEmptyState
        v-else-if="storeEmpty"
        title="No subscriptions yet"
        detail="Start with your own fleet: one subscription reading this deployment's vpn-core nodes."
      >
        <LtButton variant="primary" :disabled="!subs.canMutate.value" @click="startCreate(KIND_SUB)">
          <Server :size="14" aria-hidden="true" /> Add this fleet's nodes
        </LtButton>

        <!-- An empty store is exactly when importing from an existing Sub-Store
             is the right move. This form used to be gated on the store being
             empty while living inside the branch that only renders when it is
             not, so it could never appear at all. -->
        <div v-if="ops.canMigrate.value" class="empty-secondary">
          <span class="field-label">Already running a standalone Sub-Store?</span>
          <form class="empty-inline-form" @submit.prevent="runMigrate">
            <input
              v-model="migrateUrl"
              type="text"
              autocomplete="off"
              spellcheck="false"
              placeholder="Its base URL"
              aria-label="Standalone Sub-Store base URL"
            />
            <button class="button button-secondary" type="submit" :disabled="ops.busy.value || !migrateUrl.trim()">
              <LoaderCircle v-if="ops.busy.value" :size="15" class="spin" aria-hidden="true" />
              Import from it
            </button>
          </form>
          <p class="row-popover-note">
            Importing publishes nothing. Each subscription stays unserved until you share it.
          </p>
          <p v-if="ops.actionError.value" class="row-popover-error" role="alert">{{ ops.actionError.value }}</p>
        </div>
      </LtEmptyState>

      <template v-else>
        <LtToolbar>
          <template #search>
            <input
              v-model="searchText"
              class="lt-search"
              type="search"
              placeholder="Filter by name, id, remark"
              aria-label="Filter subscriptions"
            />
          </template>
          <template #filters>
            <button
              type="button"
              class="lt-chip"
              :class="{ 'is-active': kindFilter === '' }"
              :aria-pressed="kindFilter === ''"
              @click="kindFilter = ''"
            >All kinds</button>
            <button
              type="button"
              class="lt-chip"
              :class="{ 'is-active': kindFilter === 'sub' }"
              :aria-pressed="kindFilter === 'sub'"
              @click="kindFilter = 'sub'"
            >
              <Library :size="12" aria-hidden="true" /> Subscriptions ({{ visibleSingles }})
            </button>
            <button
              type="button"
              class="lt-chip"
              :class="{ 'is-active': kindFilter === 'collection' }"
              :aria-pressed="kindFilter === 'collection'"
              @click="kindFilter = 'collection'"
            >
              <Layers :size="12" aria-hidden="true" /> Combinations ({{ visibleCollections }})
            </button>
            <span class="lt-chip-sep" aria-hidden="true" />
            <label class="lt-sort">
              <span class="lt-sort-label">Sort</span>
              <select v-model="sortKey" class="select select-compact" aria-label="Sort records">
                <option value="recent">Recently refreshed</option>
                <option value="name">Name</option>
                <option value="status">Needs attention</option>
              </select>
            </label>
            <span v-if="allTags.length || hasUntagged" class="lt-chip-sep" aria-hidden="true" />
            <button v-if="allTags.length || hasUntagged" type="button" class="lt-chip" :class="{ 'is-active': tagFilter === '' }" @click="tagFilter = ''">All tags</button>
            <button
              v-for="tag in allTags"
              :key="tag"
              type="button"
              class="lt-chip"
              :class="{ 'is-active': tagFilter === tag }"
              @click="tagFilter = tag"
            >
              {{ tag }}
            </button>
            <button
              v-if="hasUntagged"
              type="button"
              class="lt-chip"
              :class="{ 'is-active': tagFilter === UNTAGGED }"
              @click="tagFilter = UNTAGGED"
            >
              Untagged
            </button>
          </template>
        </LtToolbar>

        <!-- The bar names the count it will act on, and that count is the
             intersection with what is on screen: a stale id from a filtered or
             already-deleted row must never be part of what Delete promises. -->
        <LtBatchBar :count="selectedCount" @clear="selectedIds = new Set()">
          <button
            v-for="action in batchActions"
            :key="action.id"
            class="button button-danger button-compact"
            type="button"
            :disabled="action.disabled"
            :title="action.reason || undefined"
            @click="requestDelete(selectedVisible.map((row) => row.id))"
          >
            <Trash2 :size="14" aria-hidden="true" />
            {{ action.label }} {{ selectedCount }} record{{ selectedCount === 1 ? "" : "s" }}
          </button>
        </LtBatchBar>

        <!-- A write can succeed and its trailing reload still fail. The rows
             below are then the last good read, and saying so beats either
             blanking them or pretending they are current. -->
        <p v-if="subs.staleError.value" class="stale-strip" role="status">
          Showing the last good read. The newest reload failed ({{ subs.staleError.value }}).
        </p>

        <LtEmptyState
          v-if="!filteredRows.length"
          kind="no-results"
          title="Nothing matches"
          detail="No record matches the current search and filters."
        >
          <LtButton :disabled="!filtersActive" @click="clearFilters()">Clear filters</LtButton>
        </LtEmptyState>

        <template v-else>
        <!-- One table, one row per record, columns the operator scans for:
             what feeds it, how many nodes go in and come out, how many
             operations, whether anyone can fetch it, when it was last fetched.
             At a narrow width the table keeps its columns and scrolls sideways
             inside itself, with the record column pinned. -->
        <!-- A table in role, so each cell is announced under its column. It
             stays a grid of divs rather than a <table>: each row is its own
             grid so the chain can open under it and the record column can pin
             at a narrow width, neither of which table layout gives. -->
        <div class="rec-scroll" role="table" aria-label="Subscriptions and combinations">
        <div class="rec-head" role="row">
          <label class="rec-select" role="columnheader" :title="`Select all ${filteredRows.length} shown records`">
            <input
              type="checkbox"
              :checked="allVisibleSelected"
              :indeterminate.prop="selectedCount > 0 && !allVisibleSelected"
              :aria-label="`Select all ${filteredRows.length} shown records`"
              @change="toggleSelectAll()"
            />
          </label>
          <!-- Named by attribute, not hidden text: an absolutely positioned
               span here escaped the scroller and widened the page at 375px. -->
          <span role="columnheader" aria-label="Expand" />
          <span class="rec-head-record" role="columnheader">Record</span>
          <span role="columnheader">Source</span>
          <span class="rec-head-nodes" role="columnheader" title="Nodes in and out of the chain, from the last preview run">Nodes</span>
          <span class="rec-head-ops" role="columnheader">Operations</span>
          <span class="rec-head-published" role="columnheader">Published</span>
          <span class="rec-head-status" role="columnheader">Last fetch</span>
          <span class="rec-head-spacer" role="columnheader" aria-label="Actions" />
        </div>

        <div v-for="group in groups" :key="group.id" class="rec-group" role="rowgroup">
          <div role="row">
          <div role="cell" aria-colspan="9">
          <button
            type="button"
            class="rec-group-head"
            :aria-expanded="!collapsedGroups.has(group.id)"
            @click="toggleGroup(group.id)"
          >
            <ChevronDown
              :size="14"
              class="rec-group-caret"
              :class="{ 'is-collapsed': collapsedGroups.has(group.id) }"
              aria-hidden="true"
            />
            <Layers v-if="group.id === 'collections'" :size="14" aria-hidden="true" />
            <span>{{ group.label }}</span>
            <span class="rec-group-count">{{ group.rows.length }}</span>
          </button>
          </div>
          </div>

          <ul v-if="!collapsedGroups.has(group.id)" class="rec-list" role="presentation">
            <li
              v-for="row in group.rows"
              :key="row.id"
              class="rec"
              role="row"
              :class="{ 'is-pending': pendingIds.has(row.id), 'is-selected': selectedIds.has(row.id), 'is-open': expandedId === row.id }"
            >
              <label class="rec-select" role="cell" :title="`Select ${row.name}`" @click.stop>
                <input
                  type="checkbox"
                  :checked="selectedIds.has(row.id)"
                  :aria-label="`Select ${row.name}`"
                  @change="toggleSelected(row.id)"
                />
              </label>

              <!-- Opens the chain under the row. A button, so Enter and Space
                   toggle it natively; Escape is handled at the document. -->
              <div class="rec-expand-cell" role="cell">
              <button
                type="button"
                class="rec-expand"
                :data-expand="row.id"
                :aria-expanded="expandedId === row.id"
                :aria-controls="`rec-chain-${row.id}`"
                :aria-label="`${expandedId === row.id ? 'Collapse' : 'Expand'} ${row.name}: its operations and what each one kept`"
                @click="toggleRow(row.id)"
              >
                <ChevronDown
                  :size="14"
                  class="rec-group-caret"
                  :class="{ 'is-collapsed': expandedId !== row.id }"
                  aria-hidden="true"
                />
              </button>
              </div>

              <div class="rec-body" role="cell">
                <!-- The name opens the record; the » at the row's end is the
                     client output. Both opened the sheet, so the click an
                     operator makes most duplicated a button and none reached
                     the editor. Text, not a button, where this session cannot
                     edit. The id is what ties a row to a share, and it is the
                     first thing truncated, so it rides in the title. -->
                <component
                  :is="rowAction(row, 'edit').disabled ? 'span' : 'button'"
                  :type="rowAction(row, 'edit').disabled ? undefined : 'button'"
                  class="rec-name"
                  :title="nameTitle(row)"
                  @click="openRecord(row, $event)"
                >
                  <Layers v-if="(row.kind || KIND_SUB) === KIND_COLLECTION" :size="14" class="rec-kind" aria-hidden="true" />
                  <Library v-else :size="14" class="rec-kind" aria-hidden="true" />
                  <span class="rec-name-text">{{ row.display_name || row.name }}</span>
                </component>
                <span v-if="tagChips(row).shown.length" class="rec-tags" :title="tagChips(row).all.join(', ')">
                  <LtBadge v-for="tag in tagChips(row).shown" :key="tag" tone="neutral">{{ tag }}</LtBadge>
                  <LtBadge v-if="tagChips(row).more" tone="neutral">+{{ tagChips(row).more }}</LtBadge>
                </span>
              </div>

              <span class="rec-source" role="cell" :title="row.remark || describe(row)">{{ describe(row) }}</span>

              <!-- "in → out" from the last preview run this session made for
                   the row, "?" until one has. The title says which run and
                   when. -->
              <span class="rec-nodes mono" role="cell" :title="nodesTitle(row)">{{ nodesOf(row) }}</span>

              <span class="rec-ops mono" role="cell" :title="row.target ? `Always rendered for ${row.target}` : undefined">
                {{ row.step_count }}<template v-if="row.disabled_step_count"> <span class="rec-ops-off">({{ row.disabled_step_count }} off)</span></template>
              </span>

              <!-- Whether anyone can fetch this record. The banner above said
                   "nothing is reachable until you publish a share" and no row
                   said which rows that was true of. -->
              <div class="rec-published-cell" role="cell" @click.stop>
                <span
                  v-if="publishedOf(row).slug"
                  :class="`rec-published mono is-${publishedOf(row).tone}`"
                  :title="publishedOf(row).title"
                >{{ publishedOf(row).label }}</span>
                <button
                  v-else-if="shares !== undefined"
                  type="button"
                  class="rec-publish-link"
                  :title="publishedOf(row).title + ' Opens the share form in the console.'"
                  :disabled="rowAction(row, 'share').disabled"
                  @click="runRowAction('share', row, $event)"
                >{{ publishedOf(row).label }}</button>
                <span v-else class="rec-published is-neutral" :title="sharesError || publishedOf(row).title">{{ publishedOf(row).label }}</span>
              </div>

              <div class="rec-status-cell" role="cell">
                <span
                  :class="`rec-status is-${statusOf(row).tone}`"
                  :title="statusOf(row).title"
                >{{ statusOf(row).label }}</span>
                <span v-if="trafficOf(row)" class="rec-quota">{{ trafficOf(row) }}</span>
              </div>

              <div class="rec-actions" role="cell" @click.stop>
                <LtIconButton
                  :label="`Refresh ${row.name}`"
                  :disabled="rowAction(row, 'refresh').disabled"
                  :title="rowAction(row, 'refresh').reason || undefined"
                  @click="runRowAction('refresh', row, $event)"
                >
                  <RefreshCw :size="15" aria-hidden="true" />
                </LtIconButton>
                <LtIconButton
                  :label="`Edit ${row.name}`"
                  :disabled="rowAction(row, 'edit').disabled"
                  :title="rowAction(row, 'edit').reason || undefined"
                  @click="runRowAction('edit', row, $event)"
                >
                  <Pencil :size="15" aria-hidden="true" />
                </LtIconButton>
                <LtIconButton
                  :label="`Client output for ${row.name}`"
                  :disabled="rowAction(row, 'output').disabled"
                  :title="rowAction(row, 'output').reason || undefined"
                  @click="runRowAction('output', row, $event)"
                >
                  <ChevronsRight :size="15" aria-hidden="true" />
                </LtIconButton>
                <RecordMenu
                  :data-row-menu="row.id"
                  :name="row.name"
                  :actions="menuActionsFor(row)"
                  :open="openMenuId === row.id"
                  @toggle="toggleRowMenu(row.id)"
                  @run="(id, event) => runRowAction(id, row, event)"
                  @keydown="onRowMenuKeydown"
                />
              </div>

              <!-- The chain, read in place: each operation with what it kept,
                   and the nodes the chain removed with the operation that
                   removed each. -->
              <div
                v-if="expandedId === row.id"
                :id="`rec-chain-${row.id}`"
                class="rec-chain"
                role="cell"
                aria-colspan="9"
              >
                <p v-if="rowChain?.loading" class="rec-chain-note" role="status">
                  <LoaderCircle :size="13" class="spin" aria-hidden="true" /> Reading the record…
                </p>
                <template v-else-if="rowChain?.record">
                  <p class="rec-chain-source">
                    <span class="rec-chain-eyebrow">Source</span>
                    <span>{{ describe(row) }}</span>
                    <!-- The provider link, masked after the host: the token
                         rides in its query string. Reveal shows it for a
                         minute. -->
                    <template v-if="rowChain.record.url">
                      <code class="rec-chain-url" :title="revealSource.on.value ? 'Masks itself again after a minute' : 'Masked: the query string carries the provider token'">{{ revealSource.on.value ? rowChain.record.url : maskUrl(rowChain.record.url) }}</code>
                      <button type="button" class="rec-reveal" @click="revealSource.toggle()">
                        {{ revealSource.on.value ? "Hide" : "Reveal" }}
                      </button>
                    </template>
                  </p>
                  <p v-if="rowChain.error" class="rec-chain-note is-error" role="alert">{{ rowChain.error }}</p>
                  <ol v-if="chainSteps.length" class="rec-chain-list" aria-label="Operations, in order">
                    <li
                      v-for="(step, index) in chainSteps"
                      :key="index"
                      :class="{ 'is-off': step.disabled, 'is-cut': /^kept/.test(chainDeltaText(index)) }"
                    >
                      <span class="rec-chain-label">{{ stepLabelOf(step, index) }}</span>
                      <span class="rec-chain-delta mono">{{ chainDeltaText(index) }}</span>
                    </li>
                  </ol>
                  <p v-else class="rec-chain-note">
                    No operations. The nodes are served as the source provides them<template v-if="rowChain.explanation?.final">: {{ rowChain.explanation.final.node_count }} of them</template>.
                  </p>
                  <p v-if="chainIsCombination && chainSteps.length" class="rec-chain-note">
                    The engine runs a combination's operations over its members' merged output and reports one result<template v-if="rowChain.explanation?.final">, {{ rowChain.explanation.final.node_count }} nodes</template>; per-operation counts exist for a subscription only.
                  </p>
                  <p v-else-if="!subs.canPreview.value && chainSteps.length" class="rec-chain-note">
                    This session cannot run a preview, so what each operation kept is unknown.
                  </p>
                  <ul v-if="chainDropped.length" class="rec-chain-dropped" aria-label="Nodes the chain removed">
                    <li v-for="(node, index) in chainDropped" :key="`${node.name}-${index}`">
                      <span class="rec-chain-dropped-name" :title="node.name">{{ node.name }}</span>
                      <span v-if="node.server" class="mono rec-chain-dropped-endpoint">{{ node.port ? `${node.server}:${node.port}` : node.server }}</span>
                      <span class="rec-chain-dropped-by">removed by {{ droppedBy(node) }}</span>
                    </li>
                    <li v-if="rowChain.explanation?.final?.dropped_truncated" class="rec-chain-note">
                      Naming the first {{ chainDropped.length }} of {{ rowChain.explanation.final.dropped_count }}.
                    </li>
                  </ul>
                </template>
                <p v-else-if="rowChain?.error" class="rec-chain-note is-error" role="alert">{{ rowChain.error }}</p>
              </div>
            </li>
          </ul>
        </div>
        </div>
        </template>

      </template>

      <TargetSheet
        :open="!!targetSheet"
        :record="targetSheet"
        @close="closeTargetSheet()"
      />

      <LtPanel :open="!!drawer" :title="drawerTitle" :return-focus-to="drawerTrigger" @close="closeDrawer()">
        <template v-if="drawer?.mode === 'preview'">
          <p v-if="subs.rowPreview.value?.loading" class="row-popover-note">
            <LoaderCircle :size="13" class="spin" aria-hidden="true" /> Loading…
          </p>
          <p v-else-if="subs.rowPreview.value?.error" class="row-popover-error" role="alert">
            {{ subs.rowPreview.value.error }}
          </p>
          <template v-else-if="subs.rowPreview.value">
            <p class="row-popover-note">
              {{ subs.rowPreview.value.count }} node(s) once its operations run
            </p>
            <NodeRows :nodes="subs.rowPreview.value.nodes" />
            <p v-if="subs.rowPreview.value.count > subs.rowPreview.value.nodes.length" class="row-popover-note">
              …and {{ subs.rowPreview.value.count - subs.rowPreview.value.nodes.length }} more
            </p>
          </template>
        </template>

        <SubscriptionPublishControl
          v-else-if="drawer?.mode === 'publish'"
          :saved="true"
          :read-only="!subs.canMutate.value"
          :busy="subs.busyId.value === drawer.id"
          :error="subs.actionError.value"
          @publish="publishFromDrawer"
        />

        <template v-else-if="drawer?.mode === 'share'">
          <p v-if="drawerItem && publishedOf(drawerItem).tone === 'warn'" class="row-popover-copy">
            {{ publishedOf(drawerItem).title }} Renewing or enabling it happens in the
            dashboard, under <strong>Networking → Subscription Shares</strong>.
          </p>
          <template v-else>
            <p class="row-popover-copy">
              Nothing here is reachable until a share is published for it. Shares live in the
              dashboard, under <strong>Networking → Subscription Shares</strong>.
            </p>
            <p class="row-popover-note">Already published? The Shares lens shows its link.</p>
          </template>
          <div v-if="shareOrigin && drawerItem" class="empty-actions">
            <LtButton variant="primary" @click="openShares(drawerItem)">
              <SquareArrowOutUpRight :size="13" aria-hidden="true" /> Open Shares view
            </LtButton>
          </div>
          <p v-else class="row-popover-note">
            This frame cannot ask the console to navigate, open Networking → Subscription Shares
            yourself.
          </p>
        </template>
      </LtPanel>

      <LtConfirmDialog
        :open="deleting.length > 0"
        :title="deleteTitle"
        verb="Delete"
        :names="deletingNames"
        :consequences="deleteConsequences"
        :busy="deleteBusy"
        @confirm="runDelete()"
        @cancel="deleting = []"
      />
    </section>
  </template>
</template>

<style scoped>
/* A tab whose section holds the problem. A dot, not a colour swap: the tab is
   still a tab, and colour alone would say nothing to anyone who cannot see it
   (hence the title and the label). */
.editor-tab-flag {
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: var(--destructive);
}

.field-error-jump {
  border: 0;
  border-left: 2px solid var(--destructive);
  font: inherit;
  font-size: var(--lt-text-sm);
  text-align: left;
  cursor: pointer;
}
.field-error-jump:hover { text-decoration: underline; }
.field-error-jump:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

/* The toolbar, chip and breadcrumb rules that used to live here now sit in
   styles.css. They were scoped, and the Files screen used the same class
   names, so its search box and every filter chip rendered as raw user-agent
   controls: white boxes in a dark toolbar. What is left below is genuinely
   this screen's own. */

/* A two-way choice where both sides need their consequence spelled out, so
   neither is a default the reader can skip. */
.choice-row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-top: var(--space-1);
}

.choice-row button {
  height: var(--lt-control-h);
  padding: 0 var(--space-3);
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--background);
  color: var(--muted-foreground);
  font-size: var(--lt-text-sm);
}

.choice-row button:hover { color: var(--foreground); border-color: var(--lt-border-strong); }
.choice-row button:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

.choice-row button.is-active {
  border-color: var(--primary);
  background: var(--lt-accent-soft);
  color: var(--primary);
}
</style>
