<script setup lang="ts">
import { computed, nextTick, onActivated, onBeforeUnmount, onMounted, ref, watch } from "vue";
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
} from "@lucide/vue";

import LtBadge from "../components/lt/LtBadge.vue";
import LtButton from "../components/lt/LtButton.vue";
import LtConfirmDialog from "../components/lt/LtConfirmDialog.vue";
import RecordMenu from "../components/RecordMenu.vue";
import LtDrawer from "../components/lt/LtDrawer.vue";
import LtEmptyState from "../components/lt/LtEmptyState.vue";
import LtIconButton from "../components/lt/LtIconButton.vue";
import LtBatchBar from "../components/lt/LtBatchBar.vue";
import LtSkeleton from "../components/lt/LtSkeleton.vue";
import LtToolbar from "../components/lt/LtToolbar.vue";
import TargetSheet from "../components/TargetSheet.vue";
import { actionsFor, batchActionsFor, type ActionCapabilities, type ActionId } from "../recordActions";
import { claimIntent, isCommandIntent, isRecordIntent, recordIntent } from "../recordIntent";
import { useEditorExit } from "../useEditorExit";
import { anchorTopFrom } from "../overlayAnchor";

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
import { hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import { UNTAGGED, collectTags, matchesQuery, matchesTag, normalizeQuery } from "../recordSearch";
import { formatRelativeTime, formatTraffic, parseUserinfo } from "../rowStatus";
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
    detail: "Fetches an external subscription URL and re-serves it through this pipeline.",
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
  overlayOpen: () => deleting.value.length > 0 || !!drawer.value || !!targetSheet.value,
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

async function submit(): Promise<void> {
  draft.value.tags = parseTags(tagText.value);
  draft.value.memberTags = parseTags(memberTagText.value);
  const ok = await subs.save(draft.value);
  if (ok) cancelEdit();
}

function describe(item: SubscriptionListItem): string {
  if ((item.kind || KIND_SUB) === KIND_COLLECTION) {
    const byId = item.members?.length ?? 0;
    const byTag = item.member_tags?.length ?? 0;
    const parts: string[] = [];
    if (byId) parts.push(`${byId} chosen`);
    if (byTag) parts.push(`${byTag} tag${byTag === 1 ? "" : "s"}`);
    return parts.length ? `Combines ${parts.join(" + ")}` : "Combines nothing yet";
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

onMounted(() => {
  document.addEventListener("click", onDocumentClick, true);
  document.addEventListener("keydown", onDocumentKeydown);
});
onBeforeUnmount(() => {
  document.removeEventListener("click", onDocumentClick, true);
  document.removeEventListener("keydown", onDocumentKeydown);
});
function onDocumentClick(event: MouseEvent): void {
  if (!openMenuId.value) return;
  const target = event.target as HTMLElement | null;
  if (target?.closest("[data-row-menu]")) return;
  closeRowMenu();
}
function onDocumentKeydown(event: KeyboardEvent): void {
  if (event.key !== "Escape") return;
  if (openMenuId.value) {
    closeRowMenu();
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
  return actionsFor(row, actionCaps.value, MENU_ACTIONS);
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
  if (id === "share") return openDrawer("share", row.id, event);
  if (id === "publish") return openDrawer("publish", row.id, event);
  if (id === "duplicate") return void subs.duplicate(row.id);
  if (id === "delete") return requestDelete([row.id], event);
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
/**
 * Where overlays used to open. The host once sized the frame to the content,
 * so an overlay had to be placed at the click rather than centred in a frame
 * whose top might be far above the fold. The frame is a viewport now and the
 * scrim is fixed, so this value is inert; it is still computed and passed
 * because the drawer has not been converted yet and retiring the machinery is
 * the frame owner's to schedule.
 */
const overlayAnchor = ref(32);
function openTargetSheet(row: SubscriptionListItem, event?: Event): void {
  openMenuId.value = "";
  overlayAnchor.value = anchorTopFrom(event);
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
  if (item.last_fetch_ok === false) {
    // When it failed matters as much as that it failed: a row reading only
    // "Failed" cannot be told apart from one that broke three weeks ago, and
    // that is the row an operator is looking for.
    const when = item.last_fetch_at ? formatRelativeTime(item.last_fetch_at) : "";
    return {
      tone: "danger",
      label: when ? `Failed ${when}` : "Failed",
      title: item.last_error || "The last refresh failed",
    };
  }
  if (!item.last_fetch_at) return { tone: "neutral", label: "Never refreshed" };
  const relative = formatRelativeTime(item.last_fetch_at);
  if (item.last_fetch_ok !== true) {
    // Fetched at some point, outcome not reported. Not a failure, and not a
    // success either: rendering it green was the only wrong option.
    return {
      tone: "neutral",
      label: relative ? `Fetched ${relative}, outcome not reported` : "Outcome not reported",
      title: "The server recorded a fetch for this record but not whether it succeeded.",
    };
  }
  return { tone: "ok", label: relative ? `Refreshed ${relative}` : "Refreshed" };
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
  }
}

function requestDelete(ids: string[], event?: Event): void {
  overlayAnchor.value = anchorTopFrom(event);
  deleting.value = ids;
}

const deletingNames = computed(() =>
  deleting.value.map((id) => {
    const item = subs.items.value.find((r) => r.id === id);
    return item ? item.display_name || item.name : id;
  }),
);

async function runDelete(): Promise<void> {
  deleteBusy.value = true;
  try {
    for (const id of deleting.value) {
      markPending(id, true);
      const ok = await subs.remove(id);
      markPending(id, false);
      if (!ok) break; // the composable surfaced the error; stop rather than plough on
    }
  } finally {
    deleteBusy.value = false;
    deleting.value = [];
  }
}

// ── drawer ──────────────────────────────────────────────────────────────────

const drawerItem = computed(() =>
  drawer.value ? subs.items.value.find((r) => r.id === drawer.value?.id) : undefined,
);
const drawerTitle = computed(() => {
  if (!drawer.value || !drawerItem.value) return "";
  const name = drawerItem.value.display_name || drawerItem.value.name;
  if (drawer.value.mode === "preview") return `Preview · ${name}`;
  if (drawer.value.mode === "publish") return `Publish · ${name}`;
  return `Share · ${name}`;
});

function openDrawer(mode: "preview" | "publish" | "share", id: string, event?: Event): void {
  overlayAnchor.value = anchorTopFrom(event);
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

function openShares(recordName: string): void {
  if (!shareOrigin.value) return;
  postNavigate(window, sharesRoute(recordName), shareOrigin.value);
  closeDrawer();
  subs.notice.value = "Asked the console to open Networking → Subscription Shares.";
}

/**
 * Load after the bridge handshake, not on mount.
 *
 * `available()` reads the interfaces the host declares for this frame, and on
 * first paint that has not arrived, so loading in `onMounted` alone silently
 * no-ops and never retries.
 */
async function loadAll(): Promise<void> {
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
          <label class="field field-wide">
            <span class="field-label">Provider link</span>
            <input
              v-model="draft.url"
              type="text"
              autocomplete="off"
              spellcheck="false"
              placeholder="The subscription link your provider gave you"
            />
          </label>
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
          <h3 id="editor-preview-title">Nodes this produces</h3>
          <button
            class="button button-secondary"
            type="button"
            :disabled="!canPreviewNow"
            :title="draftError || 'Run the chain and show the nodes it produces'"
            @click="subs.runPreview(draft)"
          >
            <LoaderCircle v-if="subs.previewing.value" :size="16" class="spin" aria-hidden="true" />
            <Eye v-else :size="16" aria-hidden="true" />
            {{ subs.preview.value ? "Refresh" : "Preview" }}
          </button>
        </div>

        <SubscriptionPreviewSummary
          v-if="subs.preview.value"
          :preview="subs.preview.value"
          :step-label="subs.previewStep.value === null ? '' : previewStepLabel"
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
        :anchor-top="overlayAnchor"
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
          <p>
            Nothing here is reachable until you publish a share for it, in the dashboard under
            Networking.
          </p>
        </div>
        <div class="heading-actions">
          <span
            class="badge mono"
            :title="`${onThisTab.length} shown here. The ${MAX_SUBSCRIPTION_RECORDS} record budget is shared with files.`"
          >{{ onThisTab.length }} / {{ MAX_SUBSCRIPTION_RECORDS }}</span>
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
              <Library :size="12" aria-hidden="true" /> Subs ({{ visibleSingles }})
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
            @click="requestDelete(selectedVisible.map((row) => row.id), $event)"
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
        <div class="rec-head" aria-hidden="true">
          <label class="rec-select" :title="`Select all ${filteredRows.length} shown records`">
            <input
              type="checkbox"
              :checked="allVisibleSelected"
              :indeterminate.prop="selectedCount > 0 && !allVisibleSelected"
              :aria-label="`Select all ${filteredRows.length} shown records`"
              @change="toggleSelectAll()"
            />
          </label>
          <span />
          <span>Record</span>
          <span class="rec-head-status">Last refresh</span>
          <span class="rec-head-spacer" />
        </div>

        <section v-for="group in groups" :key="group.id" class="rec-group">
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

          <ul v-if="!collapsedGroups.has(group.id)" class="rec-list">
            <li
              v-for="row in group.rows"
              :key="row.id"
              class="rec"
              :class="{ 'is-pending': pendingIds.has(row.id), 'is-selected': selectedIds.has(row.id) }"
            >
              <label class="rec-select" :title="`Select ${row.name}`" @click.stop>
                <input
                  type="checkbox"
                  :checked="selectedIds.has(row.id)"
                  :aria-label="`Select ${row.name}`"
                  @change="toggleSelected(row.id)"
                />
              </label>

              <span class="rec-icon" aria-hidden="true">
                <Layers v-if="(row.kind || KIND_SUB) === KIND_COLLECTION" :size="17" />
                <Library v-else :size="17" />
              </span>

              <div class="rec-body">
                <!-- The name opens the client sheet: the daily path is "give me
                     the config for my client", so it is the primary click. -->
                <button
                  type="button"
                  class="rec-name"
                  :title="`Preview or copy ${row.display_name || row.name} for a client`"
                  @click="openTargetSheet(row, $event)"
                >
                  {{ row.display_name || row.name }}
                </button>
                <span class="rec-tags">
                  <LtBadge v-for="tag in row.tags ?? []" :key="tag" tone="neutral">{{ tag }}</LtBadge>
                  <LtBadge v-if="row.imported" tone="neutral">migrated</LtBadge>
                </span>
                <p class="rec-summary" :title="describe(row)">
                  {{ describe(row) }}
                  <template v-if="row.step_count">
                    · {{ row.step_count }} operation(s)<template v-if="row.disabled_step_count">, {{ row.disabled_step_count }} off</template>
                  </template>
                  <template v-if="row.target"> · always {{ row.target }}</template>
                </p>
                <!-- The id is what ties a row to a published share, and it is
                     the first thing to be truncated, so it carries its full
                     value for hover and assistive tech. -->
                <p class="rec-meta mono" :title="row.id">{{ row.id }}</p>
              </div>

              <!-- Refresh state and quota in their own right-aligned column.
                   They used to be tacked onto the end of the id line, so no two
                   rows put them in the same place and a column of them could
                   not be read down. -->
              <div class="rec-status-cell">
                <span
                  :class="`rec-status is-${statusOf(row).tone}`"
                  :title="statusOf(row).title"
                >{{ statusOf(row).label }}</span>
                <span v-if="trafficOf(row)" class="rec-quota">{{ trafficOf(row) }}</span>
              </div>

              <div class="rec-actions" @click.stop>
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
                  :label="`Preview or copy ${row.name}`"
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
            </li>
          </ul>
        </section>
        </template>

      </template>

      <TargetSheet
        :open="!!targetSheet"
        :anchor-top="overlayAnchor"
        :record="targetSheet"
        @close="closeTargetSheet()"
      />

      <LtDrawer :open="!!drawer" :title="drawerTitle" :anchor-top="overlayAnchor" @close="closeDrawer()">
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
            <ul class="node-list">
              <li v-for="(node, index) in subs.rowPreview.value.nodes" :key="`${node.name}-${index}`" class="node-row">
                <span class="node-name" :title="node.name">{{ node.name }}</span>
                <span class="node-tags">
                  <LtBadge tone="neutral">{{ node.type }}</LtBadge>
                  <LtBadge v-if="node.security" tone="neutral">{{ node.security }}</LtBadge>
                  <span v-if="node.server" class="node-meta">{{ node.port ? `${node.server}:${node.port}` : node.server }}</span>
                </span>
              </li>
            </ul>
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
          <p class="row-popover-copy">
            Nothing here is reachable until a share is published for it. Shares live in the
            dashboard, under <strong>Networking → Subscription Shares</strong>.
          </p>
          <p class="row-popover-note">Already published? The Shares view shows its link.</p>
          <div v-if="shareOrigin && drawerItem" class="empty-actions">
            <LtButton variant="primary" @click="openShares(drawerItem.name)">
              <SquareArrowOutUpRight :size="13" aria-hidden="true" /> Open Shares view
            </LtButton>
          </div>
          <p v-else class="row-popover-note">
            This frame cannot ask the console to navigate, open Networking → Subscription Shares
            yourself.
          </p>
        </template>
      </LtDrawer>

      <LtConfirmDialog
        :anchor-top="overlayAnchor"
        :open="deleting.length > 0"
        :title="deleting.length === 1
          ? 'Delete this record? Any combination that includes it stops rendering until you edit it, and any share published for it keeps existing and starts returning nothing.'
          : `Delete ${deleting.length} records? Any combination that includes them stops rendering until you edit it, and any shares published for them keep existing and start returning nothing.`"
        verb="Delete"
        :names="deletingNames"
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
  background: var(--lt-danger);
}

.field-error-jump {
  border: 0;
  border-left: 2px solid var(--lt-danger);
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
  gap: var(--lt-space-1);
  margin-top: var(--lt-space-1);
}

.choice-row button {
  height: var(--lt-control-h);
  padding: 0 var(--lt-space-3);
  border: 1px solid var(--lt-border);
  border-radius: 999px;
  background: var(--lt-bg);
  color: var(--lt-fg-muted);
  font-size: var(--lt-text-sm);
}

.choice-row button:hover { color: var(--lt-fg); border-color: var(--lt-border-strong); }
.choice-row button:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }

.choice-row button.is-active {
  border-color: var(--lt-accent);
  background: var(--lt-accent-soft);
  color: var(--lt-accent);
}
</style>
