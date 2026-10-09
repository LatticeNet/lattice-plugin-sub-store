<script setup lang="ts">
import { computed, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from "vue";
import { Braces, FileCode, FileText, GripVertical, Layers, Library, Rows3, Server, SquareArrowOutUpRight, Trash2 } from "@lucide/vue";
import {
  PcBatchBar,
  PcButton,
  PcCount,
  PcEmptyState,
  PcNotice,
  PcPagination,
  PcPanel,
  PcRow,
  PcSearchField,
  PcSelectCell,
  PcSkeleton,
  PcStateDot,
  PcTable,
  PcTagList,
  PcTh,
  useMediaQuery,
} from "@latticenet/plugin-bridge/chassis";

import LtConfirmDialog from "../components/lt/LtConfirmDialog.vue";
import LtManualCopy from "../components/lt/LtManualCopy.vue";
import RecordMenu, { type RecordMove } from "../components/RecordMenu.vue";
import SubscriptionPanel from "../components/SubscriptionPanel.vue";
import TargetSheet from "../components/TargetSheet.vue";
import SubscriptionEditor from "../components/SubscriptionEditor.vue";
import FileEditor from "../components/FileEditor.vue";
import EngineUnavailable from "../components/EngineUnavailable.vue";
import MaskedUrlInput from "../components/MaskedUrlInput.vue";
import UsageBar from "../components/UsageBar.vue";
import { closeTopOverlay, overlayDepth } from "../overlayStack";
import { actionCapabilities, batchActionsFor, deletePrompt, ownLiveShares, rowMenuFor, type ActionCapabilities, type ActionId } from "../recordActions";
import { claimIntent, isCommandIntent, isRecordIntent, recordIntent } from "../recordIntent";
import { anchorAfterDelete, focusRowAfterDelete } from "../rowFocus";
import { isSelectCell } from "../selectCell";
import { useRecordEditor } from "../useRecordEditor";
import { useFileEditor } from "../useFileEditor";
import {
  BINDINGS,
  FILE_TYPE_PLAIN,
  FILE_TYPE_SCRIPT,
  KIND_COLLECTION,
  KIND_FILE,
  KIND_SUB,
  MAX_SUBSCRIPTION_RECORDS,
  STORE_VERSION_LEGACY,
  type SubscriptionListItem,
} from "../client";
import { useHost } from "../host";
import { copyText } from "../hostClipboard";
import { SHARES_LIST_ROUTE, hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import { pageHolding, toggleShown, usePages } from "../paging";
import { matchesQuery, normalizeQuery } from "../recordSearch";
import { publishStateFor, shareLinkOf, stateTone } from "../shareState";
import { useLensChrome } from "../lensChrome";
import { useShares } from "../useShares";
import { useNodeCounts } from "../useNodeCounts";
import { describeSubStoreBase, resolveSubStoreBase } from "../migrateUrl";
import { buildLineage, plural } from "../pipeline";
import { dropMove, edgeScrollSpeed, gapAt, namedMove, stepMove, type MoveId } from "../recordOrder";
import {
  KIND_FACETS,
  TEXT,
  attentionWeight,
  countsNeedPreview,
  expiryOf,
  isFlagged,
  kindCounts,
  kindFacetOf,
  kindOf,
  lastFetchOf,
  matchesKind,
  nodeCountsOf,
  publishedOf,
  reorderBlock,
  stepsOf,
  type KindFacet,
} from "../recordTable";
import { knownFileType, useSubscriptions } from "../useSubscriptions";
import { useSubscriptionOps } from "../useSubscriptionOps";

/**
 * Records: every source, combination and file in one table (design 28, S1).
 *
 * Three tables used to list them, one layer each, with three toolbars and
 * three ideas of which columns mattered. One table answers the questions
 * every kind shares: is it published, how many nodes go in and come out, how
 * long its provider has left, when it was last fetched, and whether its chain
 * needs a rewrite. The kind filter is where the three layers went; their old
 * addresses land on it (pageState.ts).
 *
 * The list and the two editors are three states of one screen: a record opens
 * in the record editor, a file in the file editor, and the screen routes on
 * which of them is editing. Rows follow the store's manual order unless a
 * sort is chosen, and where the session may write and the signed plugin
 * offers `reorder`, a row moves by drag or from the keyboard, announced.
 */

const host = useHost();
const subs = useSubscriptions(host);
const chrome = useLensChrome();

// ── editors ─────────────────────────────────────────────────────────────────

const editor = useRecordEditor({
  host,
  subs,
  clearListState: () => clearTransientListState(),
  onSaved: (id: string | null) => { if (id) recount(id); },
});
const fileEditor = useFileEditor({
  host,
  subs,
  clearListState: () => clearTransientListState(),
  // The registered depth, not a hand-written list of overlays.
  overlayOpen: () => overlayDepth() > 0 || !!openMenuId.value,
});
const editing = computed(() => editor.editing.value || fileEditor.editing.value);

/** Edit the record in the editor its kind belongs to. */
function startEdit(row: SubscriptionListItem): void {
  if (row.kind === KIND_FILE) void fileEditor.startEdit(row.id);
  else void editor.startEdit(row.id);
}

// The whole-store surface is here only for the empty state's import form: an
// empty store is exactly when importing an existing Sub-Store is the next step.
const ops = useSubscriptionOps(host);

// ── published shares ────────────────────────────────────────────────────────

const shareStore = useShares(host);
const shares = shareStore.shares;
function isPublished(item: SubscriptionListItem): boolean {
  return publishStateFor(shares.value, item.id).tone === "ok";
}

// ── filters ─────────────────────────────────────────────────────────────────

const searchText = chrome.search;
const sortKey = chrome.sort;
const facets = chrome.facets;

/** The kind filter, on the address as `kind` so the old per-kind links land on it. */
const kindFacet = computed<KindFacet | "">({
  get: () => ((KIND_FACETS as readonly string[]).includes(facets.kind) ? (facets.kind as KindFacet) : ""),
  set: (value) => {
    facets.kind = value;
  },
});
type FileTypeFilter = "" | "config" | "script" | "plain";
/** Files only: a type filter means nothing beside sources and combinations. */
const fileType = computed<FileTypeFilter>({
  get: () => (kindFacet.value === "file" && ["config", "script", "plain"].includes(facets.type) ? (facets.type as FileTypeFilter) : ""),
  set: (value) => {
    facets.type = value;
  },
});
function fileTypeOf(item: SubscriptionListItem): Exclude<FileTypeFilter, ""> {
  const type = knownFileType(item.file_type);
  if (type === FILE_TYPE_SCRIPT) return "script";
  if (type === FILE_TYPE_PLAIN) return "plain";
  return "config";
}

const compact = computed({
  get: () => chrome.density.value === "compact",
  set: (value: boolean) => {
    chrome.density.value = value ? "compact" : "expanded";
  },
});

const kindTotals = computed(() => kindCounts(subs.items.value));
const ofKind = computed(() => subs.items.value.filter((item) => matchesKind(item, kindFacet.value)));
const searched = computed(() => {
  const query = normalizeQuery(searchText.value);
  return ofKind.value.filter((item) => matchesQuery(item, query));
});
const facetCounts = computed(() => {
  const rows = searched.value;
  const published = rows.filter(isPublished).length;
  const migrated = rows.filter((item) => item.imported).length;
  return {
    all: rows.length,
    published,
    unpublished: rows.length - published,
    migrated,
    local: rows.length - migrated,
    config: rows.filter((item) => item.kind === KIND_FILE && fileTypeOf(item) === "config").length,
    script: rows.filter((item) => item.kind === KIND_FILE && fileTypeOf(item) === "script").length,
    plain: rows.filter((item) => item.kind === KIND_FILE && fileTypeOf(item) === "plain").length,
  };
});
const filtered = computed(() =>
  searched.value.filter((item) => {
    if (fileType.value && fileTypeOf(item) !== fileType.value) return false;
    if (facets.published === "no" && isPublished(item)) return false;
    if (facets.published === "yes" && !isPublished(item)) return false;
    if (facets.origin === "migrated" && !item.imported) return false;
    if (facets.origin === "local" && item.imported) return false;
    return true;
  }),
);
const filtersActive = computed(
  () => !!searchText.value.trim() || !!fileType.value || !!facets.published || !!facets.origin,
);
function clearFilters(): void {
  searchText.value = "";
  fileType.value = "";
  facets.published = "";
  facets.origin = "";
}

/**
 * How the rows are ordered. Manual is the store's own order and the default:
 * it is the order the operator chose, and the only one rows can be moved in.
 * The others answer a question: what changed last, what is it called, what
 * needs attention.
 */
const sorted = computed(() => {
  const rows = [...filtered.value];
  const now = Date.now();
  if (sortKey.value === "name") {
    rows.sort((a, b) => (a.display_name || a.name).localeCompare(b.display_name || b.name));
  } else if (sortKey.value === "status") {
    rows.sort((a, b) => attentionWeight(a, now) - attentionWeight(b, now) || a.name.localeCompare(b.name));
  } else if (sortKey.value === "recent") {
    rows.sort((a, b) => String(b.last_fetch_at ?? "").localeCompare(String(a.last_fetch_at ?? "")));
  }
  return rows;
});

/**
 * Fifty rows a page, as vpn-core pages its identities: the large store's 160
 * files were one 7,590 px page with no way to the last row but scrolling. A
 * filter, a search or a new sort starts again on page 1.
 */
const PAGE_SIZE = 50;
const { page, table } = usePages(
  () => sorted.value,
  PAGE_SIZE,
  () => [searchText.value, kindFacet.value, fileType.value, facets.published, facets.origin, sortKey.value],
  { page: chrome.page, ready: () => subs.state.value === "ready" },
);
/** A panel opened from a link or the Overview shows its row: turn to its page. */
watch(
  () => [chrome.openId.value, subs.state.value === "ready"] as const,
  ([id]) => {
    if (!id || table.value.rows.some((row) => row.id === id)) return;
    const holder = pageHolding(sorted.value.findIndex((row) => row.id === id), PAGE_SIZE);
    if (holder) page.value = holder;
  },
  { immediate: true },
);
/** Next from the footer lands on the top of the new page, not on its last rows. */
const listRoot = ref<HTMLElement | null>(null);
function turnPage(next: number): void {
  page.value = next;
  void nextTick(() => {
    const top = listRoot.value?.getBoundingClientRect().top;
    // Only the frame's own document scrolls; the console around it stays put.
    if (top !== undefined && top < 0) window.scrollTo({ top: window.scrollY + top - 8 });
  });
}

/** The store's own position of every record, 1-based, which the order rail prints. */
const positions = computed(() => new Map(subs.items.value.map((item, index) => [item.id, index + 1])));
const total = computed(() => subs.items.value.length);

// ── empty states ────────────────────────────────────────────────────────────

const storeEmpty = computed(() => subs.items.value.length === 0);
/** The kind filter shows a kind the store holds none of. */
const kindEmpty = computed(() => !storeEmpty.value && !!kindFacet.value && ofKind.value.length === 0);
const hasSource = computed(() => subs.items.value.some((item) => (item.kind || KIND_SUB) === KIND_SUB));

const migrateUrl = ref("");
const migrateSummary = ref("");
const migrateConfirm = ref(false);
const migrateParsed = computed(() => describeSubStoreBase(migrateUrl.value));
const migrateConfirmNames = computed(() =>
  migrateParsed.value.ok ? [`The Sub-Store at ${migrateParsed.value.origin}`] : [],
);

async function runMigrate(): Promise<void> {
  migrateSummary.value = "";
  const parsed = migrateParsed.value;
  if (!parsed.ok) {
    ops.actionError.value = parsed.reason;
    return;
  }
  migrateConfirm.value = true;
}

async function confirmMigrate(): Promise<void> {
  migrateConfirm.value = false;
  const ok = await ops.migrate(resolveSubStoreBase(migrateUrl.value));
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
    ". Nothing is published yet, so publish a share under Platform, then Publishing, to make them reachable.";
  migrateUrl.value = "";
}

// ── the legacy store ────────────────────────────────────────────────────────

const legacyStore = computed(() => subs.storeVersion.value === STORE_VERSION_LEGACY);
const migrateStoreBlock = computed(() => {
  if (!subs.canMutate.value) return TEXT.migrateReadOnly;
  if (!subs.canMigrateStore.value) return TEXT.migrateUnsigned;
  return "";
});
const migratedCount = ref(0);
async function migrateStore(): Promise<void> {
  if (await subs.migrateStore()) migratedCount.value = subs.migration.value.migrated;
}

// ── selection ───────────────────────────────────────────────────────────────

/** Selection for batch delete; the record limit is 256 and one at a time is how cleanup stops happening. */
const selectedIds = ref<Set<string>>(new Set());
function toggleSelected(id: string): void {
  const next = new Set(selectedIds.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  selectedIds.value = next;
}
/**
 * What the batch controls report and act on: only rows that exist and are on
 * screen, which with paging means this page. A stale id from a filtered,
 * paged-away or deleted row is never part of what Delete promises.
 */
const selectedVisible = computed(() => table.value.rows.filter((row) => selectedIds.value.has(row.id)));
const selectedCount = computed(() => selectedVisible.value.length);
const allVisibleSelected = computed(() => table.value.rows.length > 0 && selectedCount.value === table.value.rows.length);
/** Select all acts on this page only, and leaves what is selected on other pages alone. */
function toggleSelectAll(): void {
  selectedIds.value = toggleShown(selectedIds.value, table.value.rows.map((row) => row.id));
}

const countLabel = computed(() => plural(sorted.value.length, "record"));
const countTitle = computed(() =>
  `${sorted.value.length} of ${total.value} records shown. The store holds at most ${MAX_SUBSCRIPTION_RECORDS}.`,
);

/** The lens tells the shell when an editor is up and how many rows are selected. */
watch(
  [editing, () => selectedVisible.value.length],
  ([isEditing, count]) => {
    chrome.lenses.records.editing = isEditing;
    chrome.lenses.records.selected = count;
  },
  { immediate: true },
);

// ── rows: menus, panels, verbs ──────────────────────────────────────────────

const drawer = ref<{ mode: "preview" | "publish" | "share"; id: string } | null>(null);
const drawerTrigger = ref<HTMLElement | null>(null);
const deleting = ref<string[]>([]);
const deleteBusy = ref(false);
/** Rows mid-operation (refresh, delete) render pending. */
const pendingIds = ref<Set<string>>(new Set());
const openMenuId = ref("");

function clearTransientListState(): void {
  // A pending confirm or an open drawer must not survive into the editor and
  // reappear when the operator comes back to the list.
  deleting.value = [];
  drawer.value = null;
  openMenuId.value = "";
}

/** Ids come from the store and are not guaranteed selector-safe. */
function cssEscape(value: string): string {
  const escape = (globalThis as { CSS?: { escape?: (v: string) => string } }).CSS?.escape;
  return escape ? escape(value) : value.replace(/["\\]/g, "\\$&");
}

/**
 * A popover that only closes when another one opens is a popover the operator
 * has to fight. Outside click and Escape both dismiss it, and focus goes back
 * to the control that opened it.
 */
function closeRowMenu(): void {
  const id = openMenuId.value;
  openMenuId.value = "";
  if (id) {
    void nextTick(() => {
      document.querySelector<HTMLElement>(`[data-row-menu="${cssEscape(id)}"] button`)?.focus();
    });
  }
}

async function toggleRowMenu(id: string): Promise<void> {
  const opening = openMenuId.value !== id;
  openMenuId.value = opening ? id : "";
  await host.resize();
  if (!opening) return;
  // A menu that opens without focus is a menu Escape cannot close and arrow
  // keys cannot reach, which is most of what `role="menu"` promises.
  await nextTick();
  document.querySelector<HTMLElement>(`.rec-menu[data-row-menu="${cssEscape(id)}"] button:not(:disabled)`)?.focus();
}

/** Up and down walk the open menu; Escape is handled at the document. */
function onRowMenuKeydown(event: KeyboardEvent): void {
  const step = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
  if (!step) return;
  event.preventDefault();
  const items = [...(event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>("button:not(:disabled)")];
  if (!items.length) return;
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  items[(current + step + items.length) % items.length]?.focus();
}

const actionCaps = computed<ActionCapabilities>(() => actionCapabilities(host));
function menuActionsFor(row: SubscriptionListItem) {
  return rowMenuFor(row, actionCaps.value);
}
const batchActions = computed(() => batchActionsFor(selectedVisible.value, actionCaps.value));

function nameTitle(row: SubscriptionListItem): string {
  return `Show ${row.display_name || row.name} in the side panel`;
}

function openRow(row: SubscriptionListItem, event: MouseEvent): void {
  // The checkbox, the grip, the menu and Publish are controls of their own inside the row.
  const target = event.target as HTMLElement | null;
  if (isSelectCell(target) || target?.closest("input, [data-row-menu], .rec-menu, .row-publish, .rec-grip")) return;
  chrome.openRecord(row.id);
}

/**
 * Requests from the palette and the header, which can see every record but
 * cannot open this screen's drawers or editors.
 */
const intent = recordIntent(host);
watch(
  intent,
  (value) => {
    if (isCommandIntent(value)) {
      claimIntent(intent, () => true);
      if (value.command === "new-file") fileEditor.startCreate();
      else editor.startCreate(value.command === "new-collection" ? KIND_COLLECTION : KIND_SUB);
      return;
    }
    if (!isRecordIntent(value)) return;
    const row = subs.items.value.find((item) => item.id === value.recordId);
    if (!row) return;
    claimIntent(intent, () => true);
    runRowAction(value.action, row, new MouseEvent("click"));
  },
  { immediate: true },
);

/**
 * The registry says what and when; this says how. The row menu, the batch bar
 * and the palette all come through here, so an action means the same thing
 * wherever it was started.
 */
function runRowAction(id: ActionId, row: SubscriptionListItem, event: MouseEvent): void {
  closeRowMenu();
  if (id === "edit") return startEdit(row);
  if (id === "refresh") return void refreshRow(row.id);
  if (id === "output") return openTargetSheet(row, event);
  if (id === "preview") return openDrawer("preview", row.id, event);
  if (id === "share") {
    // A file goes straight to the console's share form where the frame can ask
    // for it; a record with a live share copies that share's link.
    if (row.kind === KIND_FILE) return shareOrigin.value ? openShares(row) : openDrawer("share", row.id, event);
    return isPublished(row) ? void copyShareLink(row) : openDrawer("share", row.id, event);
  }
  if (id === "publish") return openDrawer("publish", row.id, event);
  if (id === "duplicate") return void subs.duplicate(row.id);
  if (id === "delete") return requestDelete([row.id]);
}

/** The preview and copy sheet: the one-click path to a client configuration. */
const targetSheet = ref<SubscriptionListItem | null>(null);
const targetSheetTrigger = ref<HTMLElement | null>(null);
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

const drawerItem = computed(() => (drawer.value ? subs.items.value.find((r) => r.id === drawer.value?.id) : undefined));
const drawerTitle = computed(() => {
  if (!drawer.value || !drawerItem.value) return "";
  const name = drawerItem.value.display_name || drawerItem.value.name;
  if (drawer.value.mode === "preview") return `Preview · ${name}`;
  if (drawer.value.mode === "publish") return `Upload · ${name}`;
  return publishStateFor(shares.value, drawerItem.value.id).tone === "warn" ? `Renew share · ${name}` : `Publish · ${name}`;
});
function openDrawer(mode: "preview" | "publish" | "share", id: string, event?: Event): void {
  drawerTrigger.value = (event?.currentTarget as HTMLElement | null | undefined) ?? null;
  drawer.value = { mode, id };
  if (mode === "preview" && subs.rowPreview.value?.id !== id) void subs.toggleRowPreview(id);
}
function closeDrawer(): void {
  if (drawer.value?.mode === "preview" && subs.rowPreview.value) void subs.toggleRowPreview(subs.rowPreview.value.id);
  drawer.value = null;
}
async function publishFromDrawer(destination: string, method: string, format: string): Promise<void> {
  if (!drawer.value) return;
  if (await subs.publish(drawer.value.id, destination, method, format)) closeDrawer();
}

// ── sharing ─────────────────────────────────────────────────────────────────

/**
 * Shares are published by the dashboard, not by this frame: the frame can only
 * ask the console to navigate there, to the origin the bridge pinned. Guarded
 * for the SSR contract test, which renders this screen without a window.
 */
const shareOrigin = computed(() => hostOriginFromHash(typeof window === "undefined" ? "" : window.location.hash));

/**
 * The console's share form for a record with none, its share list for one
 * that has some (the list is where an existing share is changed).
 */
function openShares(record: SubscriptionListItem): void {
  if (!shareOrigin.value) return;
  const existing = record.kind !== KIND_FILE && publishStateFor(shares.value, record.id).shares.length > 0;
  postNavigate(window, existing ? SHARES_LIST_ROUTE : sharesRoute(record.id), shareOrigin.value);
  closeDrawer();
  subs.notice.value = record.kind === KIND_FILE
    ? `Asked the console to open its share form for ${record.display_name || record.name}. The file is published once the share is saved there.`
    : "Asked the console to open Platform → Publishing.";
}

/** The console's share list, where a share a delete left serving nothing is removed or repointed. */
function openPublishing(): void {
  if (shareOrigin.value) postNavigate(window, SHARES_LIST_ROUTE, shareOrigin.value);
}

/** The live share's link, when the clipboard refused it; cleared by the next copy or Dismiss. */
const manualShareLink = ref<{ id: string; label: string; value: string } | null>(null);
async function copyShareLink(row: SubscriptionListItem): Promise<void> {
  const state = publishStateFor(shares.value, row.id);
  const share = state.shares.find((candidate) => candidate.slug === state.slug);
  if (!share) return;
  const link = shareLinkOf(share);
  if (!link) return;
  manualShareLink.value = null;
  if (await copyText(link)) {
    subs.actionError.value = "";
    subs.notice.value = `Copied the link for ${state.label}.`;
    return;
  }
  subs.notice.value = "";
  subs.actionError.value = "";
  manualShareLink.value = { id: row.id, label: state.label, value: link };
  await host.resize();
}

// ── refresh and delete ──────────────────────────────────────────────────────

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
  closeRowMenu();
  deleting.value = ids;
}
/** The dialog's words, from the one builder every surface uses: what breaks is listed by name. */
const deleteDialog = computed(() => deletePrompt(deleting.value, subs.items.value, shares.value));

/** What a batch delete that stopped part way left behind, for a retry of just that part. */
const deleteRemainder = ref<{ done: string[]; failed: string; pending: string[] } | null>(null);
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
  // Read before the rows go: the row that will sit where they were.
  const anchor = anchorAfterDelete(sorted.value.map((row) => row.id), queue);
  try {
    for (let index = 0; index < queue.length; index += 1) {
      const id = queue[index]!;
      markPending(id, true);
      const ok = await subs.remove(id, ownLiveShares(id, shares.value));
      markPending(id, false);
      if (ok) {
        done.push(id);
        continue;
      }
      // Stop, and say what stopping means for the rest: which went, which
      // failed, which were never attempted.
      deleteRemainder.value = { done, failed: id, pending: queue.slice(index + 1) };
      return;
    }
  } finally {
    deleteBusy.value = false;
    deleting.value = [];
    // Kept when there is a remainder to retry, so the untouched records need not be found again.
    if (!deleteRemainder.value) selectedIds.value = new Set();
    if (done.length) {
      await nextTick();
      focusRowAfterDelete(listRoot.value, anchor);
    }
  }
}

function retryDeleteRemainder(): void {
  const remainder = deleteRemainder.value;
  if (!remainder) return;
  deleteRemainder.value = null;
  deleting.value = [remainder.failed, ...remainder.pending];
}

// ── cells ───────────────────────────────────────────────────────────────────

const tone = stateTone;
const lineage = computed(() => buildLineage(subs.items.value, shares.value));
/** Under 480px the table is stacked rows, which print nodes as one "in → out" item. */
const stacked = useMediaQuery("(max-width: 480px)");

/**
 * The counts a store without the index cannot give: counted here through the
 * read-scoped preview, two at a time, for the rows on this page only.
 */
const counts = useNodeCounts(host);
watch(
  () =>
    subs.canPreview.value && !editing.value
      ? table.value.rows.filter((row) => countsNeedPreview(row, subs.storeVersion.value)).map((row) => row.id)
      : [],
  (ids) => counts.request(ids),
  { immediate: true },
);
/**
 * Every cell of the rows on this page, worked out once per change rather than
 * once per binding: a row reads its kind, counts and expiry several times.
 */
const cells = computed(() => {
  const now = Date.now();
  const items = subs.items.value;
  const graph = lineage.value;
  const shareInput = { shares: shares.value, available: shareStore.available.value, error: shareStore.error.value, now };
  return new Map(
    table.value.rows.map((row) => [
      row.id,
      {
        kind: kindOf(row, items, graph),
        published: publishedOf(row, shareInput),
        counts: nodeCountsOf(row, {
          storeVersion: subs.storeVersion.value,
          preview: counts.stateOf(row.id),
          canPreview: subs.canPreview.value,
          now,
        }),
        expiry: expiryOf(row, now),
        fetch: lastFetchOf(row, now),
      },
    ]),
  );
});
type RowCells = NonNullable<ReturnType<typeof cells.value.get>>;
function cell(row: SubscriptionListItem): RowCells {
  return cells.value.get(row.id)!;
}
/** The node set may have changed: count it again on the next render. */
function recount(id: string): void {
  counts.forget(id);
  if (subs.canPreview.value && table.value.rows.some((row) => row.id === id && countsNeedPreview(row, subs.storeVersion.value))) counts.request([id]);
}

function kindIcon(row: SubscriptionListItem) {
  if (row.kind === KIND_COLLECTION) return Layers;
  if (row.kind === KIND_FILE) {
    const type = fileTypeOf(row);
    return type === "script" ? Braces : type === "plain" ? FileText : FileCode;
  }
  return row.source === "vpn-core" || row.source === "vpn-core-graph" ? Server : Library;
}

// ── manual order ────────────────────────────────────────────────────────────

/** The order rail shows while the rows are in the store's order. */
const showRail = computed(() => sortKey.value === "manual");
/** Why the rows cannot be moved here, or "" when they can. */
const reorderReason = computed(() =>
  reorderBlock({
    canMutate: subs.canMutate.value,
    available: host.available(BINDINGS.subReorder),
    storeVersion: subs.storeVersion.value,
    sort: sortKey.value,
  }),
);
const canMove = computed(() => !reorderReason.value);

/** The polite live region's text; cleared first so the same sentence is read again. */
const liveMessage = ref("");
function announce(text: string): void {
  liveMessage.value = "";
  void nextTick(() => {
    liveMessage.value = text;
  });
}
const reorderError = ref("");

function labelOf(row: SubscriptionListItem): string {
  return row.display_name || row.name;
}

/** The grip of a row, after a re-render moved it. */
function focusGrip(id: string): void {
  void nextTick(() => {
    document.querySelector<HTMLElement>(`[data-grip="${cssEscape(id)}"]`)?.focus();
  });
}

/**
 * Save a new order for one moved row: shown at once, announced at once, and
 * put back with a second announcement if the store refuses it. The row stays
 * on screen: when it moved onto another page, the table turns to that page.
 */
async function commitOrder(row: SubscriptionListItem, order: string[], keepFocus: boolean): Promise<void> {
  reorderError.value = "";
  const pending = subs.reorder(order);
  announce(TEXT.moved(labelOf(row), order.indexOf(row.id) + 1, order.length));
  const holder = pageHolding(sorted.value.findIndex((entry) => entry.id === row.id), PAGE_SIZE);
  if (holder && holder !== page.value) page.value = holder;
  if (keepFocus) focusGrip(row.id);
  const result = await pending;
  if (result.ok || result.dropped) return;
  reorderError.value = TEXT.reorderFailed(result.reason);
  announce(reorderError.value);
  if (keepFocus) focusGrip(row.id);
}

/** One step up or down among the rows shown, from the keyboard. */
function moveStep(row: SubscriptionListItem, direction: -1 | 1): void {
  if (!canMove.value) return;
  const order = subs.items.value.map((item) => item.id);
  const next = stepMove(order, sorted.value.map((item) => item.id), row.id, direction);
  if (!next) {
    announce(TEXT.moveAtEdge(labelOf(row), direction < 0 ? "top" : "bottom"));
    return;
  }
  void commitOrder(row, next, true);
}

/**
 * The row menu's moves, while the rows can be moved: up, down, and to either
 * end of the rows shown, each off where the row already is. They run the
 * same commit as a drag, so the announcement and the revert on refusal come
 * with them; focus goes back to the menu's trigger, wherever the row lands.
 */
function movesFor(row: SubscriptionListItem): RecordMove[] {
  if (!canMove.value) return [];
  const at = sorted.value.findIndex((item) => item.id === row.id);
  const top = at <= 0;
  const bottom = at < 0 || at >= sorted.value.length - 1;
  return [
    { id: "up", label: TEXT.moveUp, title: TEXT.moveTitle, disabled: top },
    { id: "down", label: TEXT.moveDown, title: TEXT.moveTitle, disabled: bottom },
    { id: "top", label: TEXT.moveTop, title: TEXT.moveTitle, disabled: top },
    { id: "bottom", label: TEXT.moveBottom, title: TEXT.moveTitle, disabled: bottom },
  ];
}
function moveFromMenu(row: SubscriptionListItem, where: MoveId): void {
  closeRowMenu();
  if (!canMove.value) return;
  const order = subs.items.value.map((item) => item.id);
  const next = namedMove(order, sorted.value.map((item) => item.id), row.id, where);
  if (!next) {
    announce(TEXT.moveAtEdge(labelOf(row), where === "up" || where === "top" ? "top" : "bottom"));
    return;
  }
  void commitOrder(row, next, false);
}

/** On the grip, the arrows move the row; anywhere in the row, Alt and the arrows do. */
function onGripKeydown(row: SubscriptionListItem, event: KeyboardEvent): void {
  if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
  event.preventDefault();
  event.stopPropagation();
  moveStep(row, event.key === "ArrowUp" ? -1 : 1);
}
function onRowKeydown(row: SubscriptionListItem, event: KeyboardEvent): void {
  if (!event.altKey || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) return;
  if ((event.target as HTMLElement | null)?.closest(".rec-menu, input, select, textarea")) return;
  event.preventDefault();
  moveStep(row, event.key === "ArrowUp" ? -1 : 1);
}

/**
 * A drag that follows the pointer: the lifted row moves with it one to one,
 * the rows it passes slide aside to open the gap it would land in, release
 * drops it there, and Escape or a cancelled pointer slides everything back.
 * Pointer events rather than HTML5 drag, so touch works and the row itself is
 * what moves, not a ghost image of it. Held near the top or bottom of the
 * window, the page scrolls under the row, so a drag reaches rows that were
 * off screen when it began. Not reactive state: nothing in the template reads
 * it, and the transforms are written to the rows directly.
 */
interface DragState {
  id: string;
  pointerId: number;
  startY: number;
  /** The lifted row's middle when the drag began, in page coordinates. */
  startMiddle: number;
  /** Every row on the page, in drawing order, and the middle of each. */
  rows: HTMLElement[];
  middles: number[];
  shown: string[];
  from: number;
  height: number;
  gap: number;
  moved: boolean;
  /** The pointer's last height in the window, which the edge scroll keeps reading while it is held still. */
  clientY: number;
  /** The pending edge-scroll frame, 0 when none. */
  frame: number;
}
let dragging: DragState | null = null;

function onGripPointerDown(row: SubscriptionListItem, event: PointerEvent): void {
  if (!canMove.value || event.button !== 0 || dragging) return;
  const grip = event.currentTarget as HTMLElement;
  const rowEl = grip.closest("tr");
  const body = rowEl?.parentElement;
  if (!rowEl || !body) return;
  event.preventDefault();
  grip.setPointerCapture?.(event.pointerId);
  const scroll = window.scrollY;
  const rows = [...body.querySelectorAll<HTMLElement>("tr[data-record-row]")];
  const middles = rows.map((el) => {
    const rect = el.getBoundingClientRect();
    return rect.top + scroll + rect.height / 2;
  });
  const own = rowEl.getBoundingClientRect();
  const from = rows.indexOf(rowEl);
  dragging = {
    id: row.id,
    pointerId: event.pointerId,
    startY: event.clientY + scroll,
    startMiddle: own.top + scroll + own.height / 2,
    rows,
    middles,
    shown: rows.map((el) => el.dataset.recordRow ?? ""),
    from,
    height: own.height,
    gap: from,
    moved: false,
    clientY: event.clientY,
    frame: 0,
  };
  rowEl.classList.add("is-dragging");
  for (const el of rows) if (el !== rowEl) el.classList.add("is-making-room");
}

/** The rows between where the lifted row was and where it would land, moved over by its height. */
function makeRoom(state: DragState): void {
  state.rows.forEach((el, index) => {
    if (index === state.from) return;
    let shift = 0;
    if (state.gap > state.from && index > state.from && index < state.gap) shift = -state.height;
    if (state.gap < state.from && index >= state.gap && index < state.from) shift = state.height;
    el.style.transform = shift ? `translateY(${shift}px)` : "";
  });
}

/**
 * Put the lifted row under the pointer and open the gap it is over. Distances
 * are in page coordinates, so a scroll since the drag began counts as travel.
 */
function follow(state: DragState): void {
  const dy = state.clientY + window.scrollY - state.startY;
  if (Math.abs(dy) > 2) state.moved = true;
  state.rows[state.from]!.style.transform = `translateY(${dy}px)`;
  const gap = gapAt(state.middles, state.startMiddle + dy);
  if (gap !== state.gap) {
    state.gap = gap;
    makeRoom(state);
  }
}

/** One frame of scrolling while the row is held in an edge zone; it stops at the end of the page or out of the zone. */
function edgeScroll(): void {
  const state = dragging;
  if (!state) return;
  state.frame = 0;
  const speed = edgeScrollSpeed(state.clientY, window.innerHeight);
  if (!speed) return;
  const before = window.scrollY;
  window.scrollBy(0, speed);
  if (window.scrollY === before) return;
  follow(state);
  state.frame = requestAnimationFrame(edgeScroll);
}

function onGripPointerMove(event: PointerEvent): void {
  const state = dragging;
  if (!state || event.pointerId !== state.pointerId) return;
  state.clientY = event.clientY;
  follow(state);
  if (!state.frame && edgeScrollSpeed(state.clientY, window.innerHeight)) state.frame = requestAnimationFrame(edgeScroll);
}

function endDrag(drop: boolean): void {
  const state = dragging;
  if (!state) return;
  dragging = null;
  if (state.frame) cancelAnimationFrame(state.frame);
  const lifted = state.rows[state.from]!;
  lifted.classList.remove("is-dragging");
  if (!drop && state.moved) {
    // Cancelled: the row glides back to where it came from and the others
    // close the gap. A drop does not glide, because the rows are about to be
    // drawn again in their new order.
    lifted.classList.add("is-settling");
    window.setTimeout(() => lifted.classList.remove("is-settling"), 300);
  }
  for (const el of state.rows) {
    if (drop) el.classList.remove("is-making-room");
    el.style.transform = "";
  }
  if (!drop) window.setTimeout(() => state.rows.forEach((el) => el.classList.remove("is-making-room")), 300);
  if (!drop || !state.moved) return;
  const row = subs.items.value.find((item) => item.id === state.id);
  const order = subs.items.value.map((item) => item.id);
  const next = row ? dropMove(order, state.shown, state.id, state.gap) : null;
  if (row && next) void commitOrder(row, next, false);
}

function onGripPointerUp(event: PointerEvent): void {
  if (dragging && event.pointerId === dragging.pointerId) endDrag(true);
}
function onGripPointerCancel(event: PointerEvent): void {
  if (dragging && event.pointerId === dragging.pointerId) endDrag(false);
}

// ── document keys ───────────────────────────────────────────────────────────

/**
 * The document listeners are bound while this screen is the visible one, not
 * for the life of the component: the shell keeps screens alive across layer
 * switches (`<KeepAlive>`), and a hidden screen must not answer Escape.
 */
function bindDocumentKeys(): void {
  document.addEventListener("click", onDocumentClick, true);
  document.addEventListener("keydown", onDocumentKeydown);
}
function releaseDocumentKeys(): void {
  document.removeEventListener("click", onDocumentClick, true);
  document.removeEventListener("keydown", onDocumentKeydown);
}
onMounted(bindDocumentKeys);
onActivated(bindDocumentKeys);
onDeactivated(releaseDocumentKeys);
onBeforeUnmount(releaseDocumentKeys);

function onDocumentClick(event: MouseEvent): void {
  if (!openMenuId.value) return;
  if ((event.target as HTMLElement | null)?.closest("[data-row-menu]")) return;
  closeRowMenu();
}

/**
 * The one Escape arbiter for this screen, in the order the operator built the
 * stack: a drag in progress, the topmost overlay, the row menu, then the
 * editor. Overlays register with overlayStack and none answers the key itself,
 * so there is exactly one decision.
 */
function onDocumentKeydown(event: KeyboardEvent): void {
  if (event.key !== "Escape") return;
  if (dragging) {
    event.preventDefault();
    endDrag(false);
    return;
  }
  if (closeTopOverlay(event)) return;
  if (openMenuId.value) {
    closeRowMenu();
    return;
  }
  // Who owns the key while an overlay is up is decided in editorExit.ts.
  if (fileEditor.editing.value) fileEditor.exit.onEscape();
  else editor.exit.onEscape();
}

// ── loading ─────────────────────────────────────────────────────────────────

/**
 * Load after the bridge handshake, not on mount: `available()` reads the
 * interfaces the host declares for this frame, and on first paint those have
 * not arrived.
 */
async function loadAll(): Promise<void> {
  void shareStore.load();
  await subs.load();
  await subs.loadOperators();
}
// Re-read on return: a restore from Settings replaces everything.
onActivated(() => {
  if (host.init.value) void loadAll();
});
onMounted(() => {
  if (host.init.value) void loadAll();
});
watch(host.init, (value) => {
  if (value) void loadAll();
});

/** Facet values the kind filter offers, with their counts. */
const kindOptions = computed(() => [
  { value: "" as const, label: TEXT.kindAll, count: kindTotals.value.all },
  ...KIND_FACETS.map((facet) => ({ value: facet, label: TEXT.kindPlural[facet], count: kindTotals.value[facet] })),
]);

/** What the empty state of a kind with no records offers, by kind. */
const emptyKind = computed<KindFacet>(() => kindFacet.value || "source");
const noun = computed(() => (kindFacet.value ? TEXT.kindNoun[kindFacet.value] : "record"));
</script>

<template>
  <EngineUnavailable v-if="host.init.value && !subs.available.value" feature="Records" />

  <template v-else>
    <!-- ── editors ──────────────────────────────────────────────────────── -->
    <SubscriptionEditor v-if="editor.editing.value" :editor="editor" :subs="subs" />
    <FileEditor v-else-if="fileEditor.editing.value" :editor="fileEditor" :subs="subs" />

    <!-- ── list ─────────────────────────────────────────────────────────── -->
    <section v-else class="lens records" aria-labelledby="records-title">
      <h2 id="records-title" class="pc-sr-only">{{ TEXT.layer }}</h2>
      <!-- Every move and every refused save is read out here, politely, so a
           keyboard operator hears where the row went without losing focus. -->
      <p class="pc-sr-only" role="status" aria-live="polite" data-testid="records-live">{{ liveMessage }}</p>
      <p id="records-grip-help" class="pc-sr-only">{{ TEXT.gripHelp }}</p>

      <PcNotice v-if="subs.actionError.value" tone="danger">{{ subs.actionError.value }}</PcNotice>
      <PcNotice v-else-if="subs.notice.value" tone="success">
        {{ subs.notice.value }}
        <template v-if="subs.brokenShares.value.length && shareOrigin" #actions>
          <PcButton compact @click="openPublishing()">Open in Publishing</PcButton>
        </template>
      </PcNotice>
      <PcNotice v-if="migrateSummary" tone="success">{{ migrateSummary }}</PcNotice>
      <PcNotice v-if="reorderError" tone="danger" dismissible data-testid="records-reorder-error" @dismiss="reorderError = ''">
        {{ reorderError }}
      </PcNotice>

      <!-- A legacy store reads fine and refuses every write until it is split.
           The prompt says so before a save does, and runs the split here. -->
      <PcNotice v-if="legacyStore" tone="warning" :title="TEXT.migrateTitle" data-testid="records-migrate">
        <p>{{ TEXT.migrateBody }}</p>
        <p v-if="subs.migration.value.running" role="status">
          {{ TEXT.migrateProgress(subs.migration.value.migrated, subs.migration.value.remaining) }}
        </p>
        <p v-else-if="subs.migration.value.error" role="alert">{{ TEXT.migrateFailed(subs.migration.value.error) }}</p>
        <p v-if="migrateStoreBlock" class="rec-list-note">{{ migrateStoreBlock }}</p>
        <template v-if="!migrateStoreBlock" #actions>
          <PcButton variant="primary" compact :busy="subs.migration.value.running" @click="migrateStore()">{{ TEXT.migrateAction }}</PcButton>
        </template>
      </PcNotice>
      <PcNotice v-else-if="migratedCount" tone="success" dismissible data-testid="records-migrated" @dismiss="migratedCount = 0">
        {{ TEXT.migrateDone(migratedCount) }}
      </PcNotice>

      <!-- A batch delete that stopped part way: what that means for the rest. -->
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

      <!-- A copy the clipboard refused. With the notices rather than in the row,
           because rows re-sort and a reveal anchored to one would move. -->
      <div v-if="manualShareLink" class="manual-copy-strip">
        <div class="manual-copy-strip__head">
          <span class="manual-copy-strip__label">Link for {{ manualShareLink.label }}</span>
          <PcButton compact @click="manualShareLink = null">Dismiss</PcButton>
        </div>
        <LtManualCopy :value="manualShareLink.value" subject="link" />
      </div>

      <PcPanel v-if="!host.init.value || subs.state.value === 'loading'" label="Loading records" data-testid="records-loading">
        <PcSkeleton :count="6" label="Loading the records" />
      </PcPanel>

      <template v-else-if="subs.loadError.value">
        <PcNotice tone="danger" title="The list could not be loaded" data-testid="records-error">
          {{ subs.loadError.value }}
          <template #actions><PcButton compact @click="loadAll()">Try again</PcButton></template>
        </PcNotice>
        <PcPanel :label="TEXT.layer">
          <PcEmptyState kind="error" title="Nothing could be loaded">
            <p>This is not an empty store, it is an unanswered question.</p>
          </PcEmptyState>
        </PcPanel>
      </template>

      <!-- Nothing in the store at all: the moment to offer creation and the
           import from a standalone Sub-Store, side by side. -->
      <PcPanel v-else-if="storeEmpty" :label="TEXT.layer" data-testid="records-empty">
        <PcEmptyState title="No records yet">
          <template #icon><Library :size="26" aria-hidden="true" /></template>
          <p>
            Start with your own fleet: one source reading this deployment's vpn-core nodes. Combinations
            and client files build on sources.
          </p>
          <template #actions>
            <PcButton variant="primary" :disabled="!subs.canMutate.value" @click="editor.startCreate(KIND_SUB)">
              <template #icon><Server :size="15" aria-hidden="true" /></template>
              Add this fleet's nodes
            </PcButton>
            <PcButton :disabled="!subs.canMutate.value" @click="fileEditor.startCreate()">
              <template #icon><FileCode :size="15" aria-hidden="true" /></template>
              Add a configuration
            </PcButton>
            <div v-if="ops.canMigrate.value" class="empty-secondary">
              <span class="field-label">Already running a standalone Sub-Store?</span>
              <form class="empty-inline-form" @submit.prevent="runMigrate">
                <MaskedUrlInput
                  v-model="migrateUrl"
                  placeholder="Backend URL, or the official UI address with ?api="
                  aria-label="Running Sub-Store backend URL"
                />
                <PcButton type="submit" :busy="ops.busy.value" :disabled="!migrateParsed.ok">Import from it</PcButton>
              </form>
              <p class="row-popover-note">
                Paste the official UI address or the backend URL after ?api=. The control plane fetches
                it, so a Sub-Store that exists only on this laptop at 127.0.0.1 is unreachable unless
                the server can open that origin. The path is the API secret, used for this import only.
                Importing publishes nothing.
              </p>
              <p v-if="migrateUrl.trim() && !migrateParsed.ok" class="row-popover-error" role="status">{{ migrateParsed.reason }}</p>
              <p v-if="ops.actionError.value" class="row-popover-error" role="alert">{{ ops.actionError.value }}</p>
              <LtConfirmDialog
                :open="migrateConfirm"
                title="Import from this Sub-Store? The records it lists are written here as imported-* ids. Re-running replaces those ids. Nothing is published."
                verb="Import"
                :names="migrateConfirmNames"
                :busy="ops.busy.value"
                @cancel="migrateConfirm = false"
                @confirm="confirmMigrate()"
              />
            </div>
          </template>
        </PcEmptyState>
      </PcPanel>

      <template v-else>
        <!-- A write can succeed and its trailing reload still fail. The rows
             below are then the last good read, and saying so beats either
             blanking them or pretending they are current. -->
        <PcNotice v-if="subs.staleError.value" tone="warning" title="Showing the last good read" data-testid="records-stale">
          The newest reload failed ({{ subs.staleError.value }}).
        </PcNotice>

        <PcPanel :label="TEXT.layer">
          <div ref="listRoot" class="rec-list">
            <div class="rec-tools">
              <!-- The kind filter is where the three per-kind layers went. A
                   radio group, not a tab row: it narrows this table, it is
                   not a place of its own. -->
              <fieldset class="rec-kinds">
                <legend class="pc-sr-only">{{ TEXT.kindLegend }}</legend>
                <label v-for="option in kindOptions" :key="option.value || 'all'" class="rec-kind" :class="{ 'is-selected': kindFacet === option.value }">
                  <input v-model="kindFacet" type="radio" name="record-kind" :value="option.value" :data-testid="`kind-${option.value || 'all'}`" />
                  <span>{{ option.label }}</span>
                  <span class="rec-kind-count pc-mono">{{ option.count }}</span>
                </label>
              </fieldset>
              <PcSearchField v-model="searchText" placeholder="Filter by name, id, remark, tag" label="Filter records" />
              <label class="toolbar-sort">
                <span>Published</span>
                <select v-model="facets.published" class="pc-select" aria-label="Filter by whether a live share serves the record" :disabled="!shareStore.available.value">
                  <option value="">All {{ facetCounts.all }}</option>
                  <option value="yes">Published {{ facetCounts.published }}</option>
                  <option value="no">Not published {{ facetCounts.unpublished }}</option>
                </select>
              </label>
              <label v-if="kindFacet === 'file'" class="toolbar-sort">
                <span>Type</span>
                <select v-model="fileType" class="pc-select" aria-label="Filter by file type">
                  <option value="">All</option>
                  <option value="config">Configuration {{ facetCounts.config }}</option>
                  <option value="script">Script {{ facetCounts.script }}</option>
                  <option value="plain">Plain {{ facetCounts.plain }}</option>
                </select>
              </label>
              <label class="toolbar-sort">
                <span>Origin</span>
                <select v-model="facets.origin" class="pc-select" aria-label="Filter by where the record came from">
                  <option value="">All {{ facetCounts.all }}</option>
                  <option value="migrated">Migrated {{ facetCounts.migrated }}</option>
                  <option value="local">Made here {{ facetCounts.local }}</option>
                </select>
              </label>
              <label class="toolbar-sort">
                <span>Sort</span>
                <select v-model="sortKey" class="pc-select" aria-label="Sort records">
                  <option value="manual">{{ TEXT.sortManual }}</option>
                  <option value="recent">Recently refreshed</option>
                  <option value="name">Name</option>
                  <option value="status">Needs attention</option>
                </select>
              </label>
              <button
                type="button"
                class="rec-density"
                :aria-pressed="compact"
                :title="TEXT.densityTitle"
                data-testid="records-density"
                @click="compact = !compact"
              >
                <Rows3 :size="14" aria-hidden="true" />
                {{ TEXT.density }}
              </button>
              <PcCount :value="countLabel" :label="countTitle" />
              <p v-if="!subs.canMutate.value" class="rec-list-note" data-testid="records-readonly">{{ TEXT.readOnlyNote }}</p>
              <p v-else-if="showRail && reorderReason" class="rec-list-note" data-testid="records-order-note">{{ reorderReason }}</p>
            </div>

            <!-- A kind the store holds none of: what that kind is and how to
                 make one. Inside the card, so the kind filter stays in reach. -->
            <div v-if="kindEmpty" data-testid="records-empty">
              <PcEmptyState v-if="emptyKind === 'combination'" :title="TEXT.noKind(noun)">
                <template #icon><Layers :size="26" aria-hidden="true" /></template>
                <p>A combination merges several sources and runs one chain over the result, so a file can render all of them at once.</p>
                <template #actions>
                  <PcButton
                    :disabled="!subs.canMutate.value || !hasSource"
                    :title="hasSource ? undefined : 'Create a source first. There is nothing to combine'"
                    @click="editor.startCreate(KIND_COLLECTION)"
                  >
                    New combination
                  </PcButton>
                </template>
              </PcEmptyState>
              <PcEmptyState v-else-if="emptyKind === 'file'" :title="TEXT.noKind(noun)">
                <template #icon><FileCode :size="26" aria-hidden="true" /></template>
                <p>
                  Paste the Mihomo config you already run. Lattice keeps your rules and groups and replaces
                  only the proxy list, from whichever source you point it at, so nodes can change
                  without you editing anything.
                </p>
                <template #actions>
                  <PcButton variant="primary" :disabled="!subs.canMutate.value" @click="fileEditor.startCreate()">
                    <template #icon><FileCode :size="15" aria-hidden="true" /></template>
                    Add a configuration
                  </PcButton>
                  <PcButton :disabled="!subs.canMutate.value" @click="fileEditor.startCreate(FILE_TYPE_PLAIN)">
                    <template #icon><FileText :size="15" aria-hidden="true" /></template>
                    New plain-text file
                  </PcButton>
                </template>
              </PcEmptyState>
              <PcEmptyState v-else :title="TEXT.noKind(noun)">
                <template #icon><Library :size="26" aria-hidden="true" /></template>
                <p>Start with your own fleet: one source reading this deployment's vpn-core nodes.</p>
                <template #actions>
                  <PcButton variant="primary" :disabled="!subs.canMutate.value" @click="editor.startCreate(KIND_SUB)">
                    <template #icon><Server :size="15" aria-hidden="true" /></template>
                    Add this fleet's nodes
                  </PcButton>
                </template>
              </PcEmptyState>
            </div>

            <PcEmptyState
              v-else-if="!sorted.length"
              kind="no-match"
              :title="searchText.trim() ? 'No record matches that search' : `No ${noun}s match these filters`"
              data-testid="records-no-match"
            >
              <p v-if="searchText.trim()">
                Nothing here is called, tagged or described as <span class="pc-mono">{{ searchText.trim() }}</span>.
              </p>
              <p v-else-if="facets.published === 'no'">Every {{ noun }} here is published.</p>
              <p v-else>Nothing in this store matches the filters above.</p>
              <template #actions>
                <PcButton :disabled="!filtersActive" @click="clearFilters()">Clear filters</PcButton>
              </template>
            </PcEmptyState>

            <PcTable
              v-else
              :min-width="showRail ? 1300 : 1244"
              :density="compact ? 'compact' : 'comfortable'"
              :label="TEXT.layer"
              class="layer-table records-table"
              :data-rail="showRail ? 'true' : undefined"
              data-testid="records-table"
            >
              <template #head>
                <PcSelectCell
                  header
                  :checked="allVisibleSelected"
                  :indeterminate="selectedCount > 0 && !allVisibleSelected"
                  :label="`Select all ${table.rows.length} shown records`"
                  @change="toggleSelectAll()"
                />
                <PcTh v-if="showRail" numeric width="56px">#</PcTh>
                <PcTh name>Name</PcTh>
                <!-- The name takes what these leave: 367px at 1440 with the
                     order rail. Expiry and Steps put their second fact on a
                     line of its own rather than widening the row. -->
                <PcTh width="192px">Kind</PcTh>
                <PcTh width="120px">Published</PcTh>
                <PcTh numeric width="84px">Nodes in</PcTh>
                <PcTh numeric width="84px">Nodes out</PcTh>
                <PcTh numeric width="64px">Steps</PcTh>
                <PcTh width="168px">Expiry and traffic</PcTh>
                <PcTh width="152px">Last fetch</PcTh>
                <PcTh actions width="48px" aria-label="Actions" />
              </template>
              <tbody>
                <PcRow
                  v-for="row in table.rows"
                  :id="`rec-${row.id}`"
                  :key="row.id"
                  class="layer-row record-row"
                  :class="{ 'is-pending': pendingIds.has(row.id) }"
                  :selected="selectedIds.has(row.id) || chrome.openId.value === row.id"
                  :data-record-row="row.id"
                  :data-kind="kindFacetOf(row.kind)"
                  data-testid="record-row"
                  @click="openRow(row, $event)"
                  @keydown="onRowKeydown(row, $event)"
                >
                  <PcSelectCell :checked="selectedIds.has(row.id)" :label="`Select ${row.name}`" @change="toggleSelected(row.id)" />
                  <td v-if="showRail" class="rec-order">
                    <span class="rec-order-inner">
                      <button
                        v-if="canMove"
                        type="button"
                        class="rec-grip"
                        :data-grip="row.id"
                        :aria-label="TEXT.gripLabel(labelOf(row), positions.get(row.id) ?? 0, total)"
                        aria-describedby="records-grip-help"
                        data-testid="record-grip"
                        @click.stop
                        @keydown="onGripKeydown(row, $event)"
                        @pointerdown="onGripPointerDown(row, $event)"
                        @pointermove="onGripPointerMove"
                        @pointerup="onGripPointerUp"
                        @pointercancel="onGripPointerCancel"
                      >
                        <GripVertical :size="14" aria-hidden="true" />
                      </button>
                      <span class="rec-position" data-testid="record-position">{{ positions.get(row.id) }}</span>
                    </span>
                  </td>
                  <td class="pc-name" data-stack="name">
                    <div class="pc-name-line">
                      <component :is="kindIcon(row)" v-if="!compact" class="rec-kind-icon" :size="14" aria-hidden="true" />
                      <button
                        type="button"
                        class="row-open"
                        :data-record-open="row.id"
                        :title="nameTitle(row)"
                        data-testid="record-name"
                        @click.stop="chrome.openRecord(row.id)"
                      >
                        <strong>{{ row.display_name || row.name }}</strong>
                      </button>
                      <span v-if="isFlagged(row)" class="rec-flag" :title="TEXT.flaggedTitle" data-testid="record-flagged">
                        <PcStateDot tone="warning" :label="TEXT.flagged" />
                        <span class="pc-sr-only">{{ TEXT.flaggedTitle }}</span>
                      </span>
                      <span v-if="row.tags?.length" class="pc-name-after"><PcTagList :tags="row.tags" :max="1" /></span>
                    </div>
                    <!-- The remark, when there is one. Not the id: a migrated
                         record's id is `imported-` and its name again. -->
                    <small v-if="row.remark" :title="row.remark">{{ row.remark }}</small>
                  </td>
                  <td data-stack="summary" data-label="Kind" :title="cell(row).kind.title" class="rec-kind-cell">
                    <!-- A reference that answers nothing leads the second line,
                         so the kind's name stays whole beside it; compact rows
                         have no second line, so there it follows the name. -->
                    <span class="pc-td-body rec-kind-line">
                      <span class="rec-kind-label">{{ cell(row).kind.label }}</span>
                      <PcStateDot
                        v-if="cell(row).kind.missing && compact"
                        tone="error"
                        :label="cell(row).kind.missingLabel"
                        data-testid="record-missing"
                      />
                    </span>
                    <small v-if="cell(row).kind.detail || (cell(row).kind.missing && !compact)" class="rec-kind-sub">
                      <PcStateDot
                        v-if="cell(row).kind.missing && !compact"
                        tone="error"
                        :label="cell(row).kind.missingLabel"
                        data-testid="record-missing"
                      /><template v-if="cell(row).kind.missing && !compact && cell(row).kind.detail"> · </template>{{ cell(row).kind.detail }}
                    </small>
                  </td>
                  <td data-stack="state" data-label="Published" :title="cell(row).published.title" data-testid="record-published">
                    <span class="pc-td-body">
                      <span v-if="cell(row).published.slug" class="layer-share">
                        <PcStateDot :tone="tone(cell(row).published.tone)" :label="cell(row).published.label" />
                      </span>
                      <!-- The column is a state. Publish… lives in the row menu;
                           only the not-published view, where publishing is the
                           task, keeps a button in the row. -->
                      <button
                        v-else-if="facets.published === 'no' && shareOrigin && shares !== undefined"
                        type="button"
                        class="row-publish"
                        :aria-label="`Publish ${labelOf(row)}…`"
                        :title="`Open the console's share form for ${labelOf(row)}, with this record chosen`"
                        @click.stop="openShares(row)"
                      >
                        Publish…
                      </button>
                      <span v-else-if="cell(row).published.unknown" class="layer-muted">{{ TEXT.publishedUnknown }}</span>
                      <span v-else-if="shares !== undefined" class="layer-muted">not published</span>
                    </span>
                  </td>
                  <template v-if="!stacked">
                    <td class="pc-numeric pc-mono" data-stack="detail" data-label="Nodes in" :title="cell(row).counts.title" data-testid="record-nodes-in">
                      <span class="pc-td-body">{{ cell(row).counts.in }}</span>
                    </td>
                    <td class="pc-numeric pc-mono" data-stack="detail" data-label="Nodes out" :title="cell(row).counts.title" data-testid="record-nodes-out">
                      <span class="pc-td-body">{{ cell(row).counts.out }}</span>
                    </td>
                  </template>
                  <td v-else data-stack="state" data-label="Nodes" :title="cell(row).counts.title" class="pc-mono rec-nodes-pair" data-testid="record-nodes">
                    {{ cell(row).counts.pair }}
                  </td>
                  <td class="pc-numeric pc-mono rec-steps" data-stack="detail" data-label="Steps" :title="stepsOf(row).title" data-testid="record-steps">
                    <span class="pc-td-body">{{ stepsOf(row).count }}</span>
                    <small v-if="stepsOf(row).off">{{ stepsOf(row).off }}</small>
                  </td>
                  <td data-stack="state" data-label="Expiry and traffic" :title="cell(row).expiry.title" :data-expiry="cell(row).expiry.state">
                    <!-- The expiry first, the bar under it at full rows: when the
                         cell is short of room the bar is what gives way, never
                         the date, and compact rows drop the bar. -->
                    <span v-if="cell(row).expiry.figures" class="pc-td-body layer-provider">
                      <span
                        v-if="cell(row).expiry.text"
                        class="layer-expiry"
                        :data-tone="cell(row).expiry.tone"
                        :data-testid="cell(row).expiry.state === 'expired' ? 'record-expired' : undefined"
                      >{{ cell(row).expiry.text }}</span>
                      <UsageBar :figures="cell(row).expiry.figures!" />
                    </span>
                    <span v-else-if="cell(row).expiry.state === 'unreported'" class="pc-td-body layer-muted">{{ TEXT.notReported }}</span>
                  </td>
                  <td data-stack="state" data-label="Last fetch" :title="cell(row).fetch ? undefined : TEXT.notFetched">
                    <span v-if="cell(row).fetch" class="pc-td-body">
                      <PcStateDot
                        :tone="tone(cell(row).fetch!.tone)"
                        :label="cell(row).fetch!.label"
                        :title="cell(row).fetch!.title || cell(row).fetch!.label"
                      />
                    </span>
                  </td>
                  <td class="pc-actions" data-stack="actions">
                    <div class="pc-row-actions">
                      <RecordMenu
                        :data-row-menu="row.id"
                        :name="row.name"
                        :actions="menuActionsFor(row)"
                        :moves="movesFor(row)"
                        :open="openMenuId === row.id"
                        @toggle="toggleRowMenu(row.id)"
                        @run="(id, event) => runRowAction(id, row, event)"
                        @move="(where) => moveFromMenu(row, where)"
                        @keydown="onRowMenuKeydown"
                      />
                    </div>
                  </td>
                </PcRow>
              </tbody>
            </PcTable>
            <!-- More than a page: a footer, as vpn-core's Users has. Fewer and there is none. -->
            <PcPagination
              v-if="!kindEmpty && sorted.length && table.pages > 1"
              :page="table.page"
              :pages="table.pages"
              :from="table.from"
              :to="table.to"
              :total="table.total"
              noun="Records"
              label="Records pagination"
              @update:page="turnPage"
            />
          </div>
        </PcPanel>
      </template>

      <!-- The bar names the count it acts on, the intersection with what is on
           screen, and floats over the foot of the frame so rows never move. -->
      <PcBatchBar :count="selectedCount" noun="selected" @clear="selectedIds = new Set()">
        <!-- The console's share form takes one record, so one selected record
             can be handed over and a larger selection is told why not. -->
        <PcButton
          v-if="selectedCount === 1 && shareOrigin"
          compact
          :aria-label="`Publish ${labelOf(selectedVisible[0]!)}…`"
          @click="openShares(selectedVisible[0]!)"
        >
          <template #icon><SquareArrowOutUpRight :size="13" aria-hidden="true" /></template>
          Publish…
        </PcButton>
        <span v-else-if="selectedCount > 1" class="batch-note">Publish one record at a time: the console's share form takes one.</span>
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

      <TargetSheet :open="!!targetSheet" :record="targetSheet" @close="closeTargetSheet()" />

      <SubscriptionPanel
        :open="!!drawer"
        :mode="drawer?.mode ?? null"
        :title="drawerTitle"
        :item="drawerItem ?? null"
        :subs="subs"
        :busy-id="subs.busyId.value"
        :published="drawerItem ? publishStateFor(shares, drawerItem.id) : null"
        :share-origin="shareOrigin"
        :return-focus-to="drawerTrigger"
        @close="closeDrawer()"
        @publish="publishFromDrawer"
        @open-shares="openShares"
      />

      <LtConfirmDialog
        :open="deleting.length > 0"
        :title="deleteDialog.title"
        verb="Delete"
        :names="deleteDialog.names"
        :consequences="deleteDialog.consequences"
        :served="deleteDialog.served"
        :confirm-text="deleteDialog.confirmText"
        :focus-record="deleting[0] ?? ''"
        :busy="deleteBusy"
        @confirm="runDelete()"
        @cancel="deleting = []"
      />
    </section>
  </template>
</template>
