<script setup lang="ts">
import { computed, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from "vue";
import { Library, LoaderCircle, Server, Trash2 } from "@lucide/vue";
import {
  PcActionsCell,
  PcBatchBar,
  PcButton,
  PcCount,
  PcDetailRow,
  PcEmptyState,
  PcGroupRow,
  PcKindChip,
  PcNameCell,
  PcNotice,
  PcPanel,
  PcPanelHeader,
  PcRow,
  PcSelectCell,
  PcSkeleton,
  PcStateDot,
  PcStatePill,
  PcTable,
  PcTagList,
  PcTd,
  PcTh,
  useExpandSet,
} from "@latticenet/plugin-bridge/chassis";

import LtConfirmDialog from "../components/lt/LtConfirmDialog.vue";
import RecordMenu from "../components/RecordMenu.vue";
import SubscriptionPanel from "../components/SubscriptionPanel.vue";
import { closeTopOverlay, overlayDepth } from "../overlayStack";
import LtManualCopy from "../components/lt/LtManualCopy.vue";
import TargetSheet from "../components/TargetSheet.vue";
import { actionsFor, batchActionsFor, type ActionCapabilities, type ActionId, type ResolvedAction } from "../recordActions";
import { claimIntent, isCommandIntent, isRecordIntent, recordIntent } from "../recordIntent";
import { useRecordEditor } from "../useRecordEditor";
import SubscriptionEditor from "../components/SubscriptionEditor.vue";

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
import { matchesQuery, normalizeQuery } from "../recordSearch";
import { formatRelativeTime, formatTraffic, parseUserinfo, tagChips as tagChipsOf } from "../rowStatus";
import { publishStateFor, refreshStateFor, stateTone } from "../shareState";
import { useLensChrome } from "../lensChrome";
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

/**
 * The editor half. It lives in a composable because `editing` is what this
 * screen routes on: the list and the editor are two states of one screen, and
 * the screen owns the routing while SubscriptionEditor.vue owns what it draws.
 */
const editor = useRecordEditor({
  host,
  subs,
  clearListState: () => clearTransientListState(),
  onSaved: (id: string | null) => { if (id) recount(id); },
});
const { editing, editingId, startCreate, startEdit, exit } = editor;
// The whole-store surface is here only for the empty state's migrate form: an
// empty store is exactly when importing an existing Sub-Store is the next step.
const ops = useSubscriptionOps(host);

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

/** The toolbar's search and sort live in the shell; this lens filters on them. */
const chrome = useLensChrome();
const searchText = chrome.search;
const sortKey = chrome.sort;
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



// Files live in the same store but on their own tab.
const onThisTab = computed(() =>
  subs.items.value.filter((i) => (i.kind || KIND_SUB) !== KIND_FILE),
);

/** The search is the one filter left: it matches names, ids, remarks and tags. */
const filtersActive = computed(() => !!searchText.value.trim());

function clearFilters(): void {
  searchText.value = "";
}

function clearTransientListState(): void {
  // A pending confirm or an open drawer must not survive into the editor and
  // reappear when the operator comes back to the list.
  deleting.value = [];
  drawer.value = null;
  expandedId.value = "";
  rowChain.value = null;
  revealSource.hide();
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
  return onThisTab.value.filter((item) => matchesQuery(item, query));
});

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
 * Records fold under their kind, the way lines fold under nodes on the Lines
 * page: one group row for subscriptions and one for combinations, each with
 * its count and a summary sentence, each collapsible on its own.
 */
const groups = computed(() => {
  const singleRows = filteredRows.value.filter((row) => (row.kind || KIND_SUB) !== KIND_COLLECTION);
  const collectionRows = filteredRows.value.filter((row) => (row.kind || KIND_SUB) === KIND_COLLECTION);
  return [
    { id: "subs", label: "Subscriptions", rows: singleRows },
    { id: "collections", label: "Combinations", rows: collectionRows },
  ]
    .filter((group) => group.rows.length > 0)
    .map((group) => ({ ...group, summary: groupSummary(group.rows) }));
});

/** "5 records · 2 published", or only the count while the share list is unread. */
function groupSummary(rows: SubscriptionListItem[]): string {
  const count = `${rows.length} record${rows.length === 1 ? "" : "s"}`;
  if (shares.value === undefined) return count;
  const published = rows.filter((row) => publishStateFor(shares.value, row.id).tone === "ok").length;
  return `${count} · ${published} published`;
}

/**
 * Which groups are open. Both by default: a store of a handful of records is
 * a page, not a scroll, and two closed shelves would hide everything the
 * operator came for. A search opens every group that still has rows, and
 * clearing it restores the operator's own set.
 */
const openGroups = useExpandSet(["subs", "collections"]);
watch(
  () => (filtersActive.value ? groups.value.map((group) => group.id) : null),
  (forced) => openGroups.override(forced),
  { immediate: true },
);
function groupOpen(id: string): boolean {
  return openGroups.isOpen(id);
}
function toggleGroup(id: string): void {
  openGroups.toggle(id);
}

/** What the record card's count badge says and what its title explains. */
const countLabel = computed(() => `${filteredRows.value.length} record${filteredRows.value.length === 1 ? "" : "s"}`);
const countTitle = computed(() =>
  filtersActive.value
    ? `${filteredRows.value.length} of ${onThisTab.value.length} records match. The ${MAX_SUBSCRIPTION_RECORDS} record budget is shared with files.`
    : `${onThisTab.value.length} records here. The ${MAX_SUBSCRIPTION_RECORDS} record budget is shared with files.`,
);

/** The lens tells the shell when its editor is up and how many rows are selected. */
watch(
  [editing, () => selectedVisible.value.length],
  ([isEditing, count]) => {
    chrome.lenses.subscriptions.editing = isEditing;
    chrome.lenses.subscriptions.selected = count;
  },
  { immediate: true },
);

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
const actionCaps = computed<ActionCapabilities>(() => ({
  ready: !!host.init.value,
  mutate: subs.canMutate.value,
  fetch: subs.canFetch.value,
  preview: subs.canPreview.value,
  render: subs.canRender.value,
  publish: subs.canPublish.value,
}));

/**
 * The row keeps one text button, Open, and folds every other verb into its
 * menu, the way a Lines row keeps Evidence and a menu. Client output and
 * Refresh were icon buttons on the row; they are the first two items now.
 */
const MENU_ACTIONS = ["output", "refresh", "preview", "share", "publish", "duplicate", "delete"] as const;

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
 * The title on the name: the id that ties a row to a share, and what Open
 * does or why it cannot.
 */
function nameTitle(row: SubscriptionListItem): string {
  const edit = rowAction(row, "edit");
  return edit.disabled ? `${row.id}. ${edit.reason}` : `${row.id}. Open ${row.display_name || row.name}`;
}

/** The chassis's tone for a row verdict. */
const tone = stateTone;

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

function statusOf(item: SubscriptionListItem): { tone: "ok" | "warn" | "danger" | "neutral"; label: string; title?: string } {
  return refreshStateFor(item);
}

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
      document.querySelector<HTMLElement>(`#rec-${cssEscape(id)} .pc-toggle`)?.focus();
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
// Guarded because the panel now reads it as a prop on every render rather
// than inside a v-if in its body, and this screen is rendered without a
// window in the SSR contract test.
const shareOrigin = computed(() =>
  hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash),
);

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
</script>

<template>
  <EngineUnavailable v-if="host.init.value && !subs.available.value" feature="Subscriptions" />

  <template v-else>
    <!-- ── editor ───────────────────────────────────────────────────────── -->
    <SubscriptionEditor v-if="editing" :editor="editor" :subs="subs" />

    <!-- ── list ─────────────────────────────────────────────────────────── -->
    <section v-else class="lens" aria-labelledby="subs-title">
      <h2 id="subs-title" class="pc-sr-only">Subscriptions</h2>

      <PcNotice v-if="subs.actionError.value" tone="danger">{{ subs.actionError.value }}</PcNotice>
      <PcNotice v-else-if="subs.notice.value" tone="success">{{ subs.notice.value }}</PcNotice>
      <PcNotice v-if="migrateSummary" tone="success">{{ migrateSummary }}</PcNotice>

      <!--
        A batch delete that stopped part way. The error notice above already
        says why the one record failed; this says what that means for the
        other eleven, which is the part the operator cannot work out alone.
      -->
      <PcNotice
        v-if="deleteRemainder"
        tone="warning"
        :title="`${deleteRemainder.done.length} deleted, 1 failed, ${deleteRemainder.pending.length} not attempted`"
      >
        The run stopped at <strong>{{ namesFor([deleteRemainder.failed])[0] }}</strong>, so nothing after
        it was touched. These records are still here and still selected:
        <ul class="partial-strip__names">
          <li v-for="name in namesFor([deleteRemainder.failed, ...deleteRemainder.pending])" :key="name" class="pc-mono">
            {{ name }}
          </li>
        </ul>
        <template #actions>
          <PcButton compact @click="retryDeleteRemainder()">
            Retry the {{ deleteRemainder.pending.length + 1 }} that remain
          </PcButton>
          <PcButton compact @click="deleteRemainder = null">Dismiss</PcButton>
        </template>
      </PcNotice>

      <!--
        A copy that the clipboard refused. Sits with the other notices rather
        than inside the row, because the row list re-sorts and a reveal
        anchored to a row would move out from under the operator mid-copy.
      -->
      <div v-if="manualShareLink" class="manual-copy-strip">
        <div class="manual-copy-strip__head">
          <span class="manual-copy-strip__label">Link for {{ manualShareLink.label }}</span>
          <PcButton compact @click="manualShareLink = null">Dismiss</PcButton>
        </div>
        <LtManualCopy :value="manualShareLink.value" subject="link" />
      </div>

      <PcPanel v-if="!host.init.value || subs.state.value === 'loading'" label="Loading subscriptions">
        <PcSkeleton :count="6" label="Loading the subscriptions" />
      </PcPanel>

      <template v-else-if="subs.loadError.value">
        <PcNotice tone="danger" title="The list could not be loaded">
          {{ subs.loadError.value }}
          <template #actions><PcButton compact @click="loadAll()">Try again</PcButton></template>
        </PcNotice>
        <PcPanel label="Subscriptions">
          <PcEmptyState kind="error" title="Nothing could be loaded">
            <p>This is not an empty store, it is an unanswered question.</p>
          </PcEmptyState>
        </PcPanel>
      </template>

      <PcPanel v-else-if="storeEmpty" label="Subscriptions">
        <PcEmptyState title="No subscriptions yet">
          <template #icon><Library :size="26" aria-hidden="true" /></template>
          <p>Start with your own fleet: one subscription reading this deployment's vpn-core nodes.</p>
          <template #actions>
            <PcButton variant="primary" :disabled="!subs.canMutate.value" @click="startCreate(KIND_SUB)">
              <template #icon><Server :size="15" aria-hidden="true" /></template>
              Add this fleet's nodes
            </PcButton>

            <!-- An empty store is exactly when importing from an existing
                 Sub-Store is the right move. -->
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
                <PcButton type="submit" :busy="ops.busy.value" :disabled="!migrateUrl.trim()">Import from it</PcButton>
              </form>
              <p class="row-popover-note">
                Importing publishes nothing. Each subscription stays unserved until you share it.
              </p>
              <p v-if="ops.actionError.value" class="row-popover-error" role="alert">{{ ops.actionError.value }}</p>
            </div>
          </template>
        </PcEmptyState>
      </PcPanel>

      <template v-else>
        <!-- A write can succeed and its trailing reload still fail. The rows
             below are then the last good read, and saying so beats either
             blanking them or pretending they are current. -->
        <PcNotice v-if="subs.staleError.value" tone="warning" title="Showing the last good read">
          The newest reload failed ({{ subs.staleError.value }}).
        </PcNotice>

        <PcPanel label="Records">
          <PcPanelHeader
            title="Records"
            description="One row per record, folded under its kind. A combination is a set of subscriptions merged into one output."
          >
            <PcCount :value="countLabel" :label="countTitle" />
          </PcPanelHeader>

          <PcEmptyState v-if="!filteredRows.length" kind="no-match" title="No record matches that search">
            <p>Nothing here is called, tagged or described as <span class="pc-mono">{{ searchText.trim() }}</span>.</p>
            <template #actions>
              <PcButton :disabled="!filtersActive" @click="clearFilters()">Clear the search</PcButton>
            </template>
          </PcEmptyState>

          <!-- One table, one row per record under its kind, columns the operator
               scans for: what feeds it, how many nodes go in and come out, how
               many operations, whether anyone can fetch it, when it was last
               fetched. A row's chevron opens its operations as rows beneath it. -->
          <PcTable v-else :min-width="1040" label="Subscriptions and combinations">
            <template #head>
              <PcSelectCell
                header
                :checked="allVisibleSelected"
                :indeterminate="selectedCount > 0 && !allVisibleSelected"
                :label="`Select all ${filteredRows.length} shown records`"
                @change="toggleSelectAll()"
              />
              <PcTh name>Record</PcTh>
              <PcTh>Source</PcTh>
              <PcTh numeric>Nodes</PcTh>
              <PcTh numeric>Operations</PcTh>
              <PcTh>Published</PcTh>
              <PcTh>Last fetch</PcTh>
              <PcTh actions>Actions</PcTh>
            </template>

            <tbody v-for="group in groups" :key="group.id">
              <PcGroupRow :expanded="groupOpen(group.id)">
                <td class="pc-select" data-stack="actions" />
                <PcNameCell
                  :name="group.label"
                  :expanded="groupOpen(group.id)"
                  :controls="`rec-${group.rows[0]!.id}`"
                  @toggle="toggleGroup(group.id)"
                >
                  <template #after><PcCount :value="group.rows.length" /></template>
                </PcNameCell>
                <PcTd :colspan="5" stack="summary"><span class="pc-group-summary">{{ group.summary }}</span></PcTd>
                <PcActionsCell />
              </PcGroupRow>

              <template v-if="groupOpen(group.id)">
                <template v-for="row in group.rows" :key="row.id">
                  <PcRow
                    :id="`rec-${row.id}`"
                    :selected="selectedIds.has(row.id)"
                    :open="expandedId === row.id"
                    :class="{ 'is-pending': pendingIds.has(row.id) }"
                  >
                    <PcSelectCell
                      :checked="selectedIds.has(row.id)"
                      :label="`Select ${row.name}`"
                      @change="toggleSelected(row.id)"
                    />

                    <!-- The chevron opens the chain under the row: a real
                         button, so Enter and Space toggle it; Escape is handled
                         at the document. The id rides in the title, because it
                         is what ties a row to a share and the first thing
                         truncated. -->
                    <PcNameCell
                      :name="row.display_name || row.name"
                      :id="row.id"
                      :level="1"
                      :title="nameTitle(row)"
                      :expanded="expandedId === row.id"
                      :controls="`rec-chain-${row.id}`"
                      @toggle="toggleRow(row.id)"
                    >
                      <template v-if="tagChips(row).all.length" #after>
                        <PcTagList :tags="tagChips(row).all" :max="2" />
                      </template>
                      <template #status>
                        <PcStatePill :tone="tone(publishedOf(row).tone)" :label="publishedOf(row).label" :title="publishedOf(row).title" />
                        <PcStateDot :tone="tone(statusOf(row).tone)" :label="statusOf(row).label" :title="statusOf(row).title" />
                      </template>
                    </PcNameCell>

                    <PcTd label="Source" :title="row.remark || describe(row)"><span class="cell-muted">{{ describe(row) }}</span></PcTd>

                    <!-- "in → out" from the last preview run this session made
                         for the row, "?" until one has. The title says which
                         run and when. -->
                    <PcTd label="Nodes" numeric mono :title="nodesTitle(row)">{{ nodesOf(row) }}</PcTd>

                    <PcTd label="Operations" numeric mono :title="row.target ? `Always rendered for ${row.target}` : undefined">
                      {{ row.step_count }}<span v-if="row.disabled_step_count" class="cell-muted"> ({{ row.disabled_step_count }} off)</span>
                    </PcTd>

                    <!-- Whether anyone can fetch this record: the host's share
                         list, folded onto the row. -->
                    <PcTd label="Published" stack="state">
                      <PcStatePill
                        :tone="tone(publishedOf(row).tone)"
                        :label="publishedOf(row).label"
                        :title="shares === undefined ? sharesError || publishedOf(row).title : publishedOf(row).title"
                      />
                    </PcTd>

                    <PcTd label="Last fetch" stack="state">
                      <PcStateDot :tone="tone(statusOf(row).tone)" :label="statusOf(row).label" :title="statusOf(row).title" />
                      <small v-if="trafficOf(row)" :title="trafficOf(row)">{{ trafficOf(row) }}</small>
                    </PcTd>

                    <PcActionsCell>
                      <PcButton
                        compact
                        :disabled="rowAction(row, 'edit').disabled"
                        :title="rowAction(row, 'edit').reason || rowAction(row, 'edit').title"
                        @click="runRowAction('edit', row, $event)"
                      >
                        Open
                      </PcButton>
                      <RecordMenu
                        :data-row-menu="row.id"
                        :name="row.name"
                        :actions="menuActionsFor(row)"
                        :open="openMenuId === row.id"
                        @toggle="toggleRowMenu(row.id)"
                        @run="(id, event) => runRowAction(id, row, event)"
                        @keydown="onRowMenuKeydown"
                      />
                    </PcActionsCell>
                  </PcRow>

                  <!-- The chain, read in place: one row per operation with what
                       it kept, then the source and the nodes the chain removed
                       with the operation that removed each. -->
                  <template v-if="expandedId === row.id">
                    <PcRow
                      v-for="(step, index) in chainSteps"
                      :key="`${row.id}-${index}`"
                      :id="index === 0 ? `rec-chain-${row.id}` : undefined"
                      :class="{ 'is-off': step.disabled }"
                    >
                      <td class="pc-select" data-stack="actions" />
                      <PcNameCell :name="stepLabelOf(step, index)" :level="2" :sub="`operation ${index + 1}`">
                        <template v-if="step.disabled" #after><PcKindChip label="off" title="Switched off: the chain skips this operation" /></template>
                      </PcNameCell>
                      <PcTd label="Source" />
                      <PcTd label="Nodes" numeric mono>{{ chainDeltaText(index) }}</PcTd>
                      <PcTd label="Operations" numeric mono>{{ index + 1 }} of {{ chainSteps.length }}</PcTd>
                      <PcTd label="Published" />
                      <PcTd label="Last fetch" />
                      <PcActionsCell />
                    </PcRow>
                    <PcDetailRow :colspan="8" :id="chainSteps.length ? undefined : `rec-chain-${row.id}`">
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
                        <p v-if="!chainSteps.length" class="rec-chain-note">
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
                    </PcDetailRow>
                  </template>
                </template>
              </template>
            </tbody>
          </PcTable>
        </PcPanel>
      </template>

      <!-- The bar names the count it will act on, and that count is the
           intersection with what is on screen: a stale id from a filtered or
           already-deleted row must never be part of what Delete promises. It
           floats over the foot of the frame, out of the document flow, so the
           rows never move under the cursor. -->
      <PcBatchBar :count="selectedCount" noun="selected" @clear="selectedIds = new Set()">
        <PcButton
          v-for="action in batchActions"
          :key="action.id"
          variant="danger"
          compact
          :disabled="action.disabled"
          :title="action.reason || undefined"
          @click="requestDelete(selectedVisible.map((row) => row.id))"
        >
          <template #icon><Trash2 :size="13" aria-hidden="true" /></template>
          {{ action.label }} {{ selectedCount }} record{{ selectedCount === 1 ? "" : "s" }}
        </PcButton>
      </PcBatchBar>

      <TargetSheet
        :open="!!targetSheet"
        :record="targetSheet"
        @close="closeTargetSheet()"
      />

      <SubscriptionPanel
        :open="!!drawer"
        :mode="drawer?.mode ?? null"
        :title="drawerTitle"
        :item="drawerItem ?? null"
        :subs="subs"
        :busy-id="subs.busyId.value"
        :published="drawerItem ? publishedOf(drawerItem) : null"
        :share-origin="shareOrigin"
        :return-focus-to="drawerTrigger"
        @close="closeDrawer()"
        @publish="publishFromDrawer"
        @open-shares="openShares"
      />

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
