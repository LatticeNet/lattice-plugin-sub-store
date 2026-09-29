<script setup lang="ts">
import { computed, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from "vue";
import { Layers, Library, Server, Trash2 } from "@lucide/vue";
import {
  PcBatchBar,
  PcButton,
  PcCount,
  PcEmptyState,
  PcKindChip,
  PcNotice,
  PcPanel,
  PcRow,
  PcSearchField,
  PcSelectCell,
  PcSkeleton,
  PcStateDot,
  PcTable,
  PcTagList,
  PcTh,
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
import { publishStateFor, refreshStateFor, stateTone } from "../shareState";
import { useLensChrome } from "../lensChrome";
import { useShares } from "../useShares";
import { nodeCountLabel, nodeCountTitle } from "../nodeCounts";
import { useNodeCounts } from "../useNodeCounts";
import MaskedUrlInput from "../components/MaskedUrlInput.vue";
import UsageBar from "../components/UsageBar.vue";
import { describeSubStoreBase, resolveSubStoreBase } from "../migrateUrl";
import {
  buildLineage,
  formatExpiry,
  isProviderLink,
  plural,
  providerFigures,
  recordLabel,
  sourceKindLabel,
  usedBy,
  type ViewId,
} from "../pipeline";
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
import { type ChainStep } from "../components/ProcessChain.vue";
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
 * One screen for two layers. Sources and Combinations are the same store,
 * the same editor and the same row verbs; what differs is which records the
 * table holds and which columns mean something for them.
 */
const props = defineProps<{ kind: "sub" | "collection" }>();
const isComboLayer = computed(() => props.kind === KIND_COLLECTION);
const viewId = computed<ViewId>(() => (isComboLayer.value ? "combinations" : "sources"));
const noun = computed(() => (isComboLayer.value ? "combination" : "source"));

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

/** The list's search and sort live in this card; the shell only keeps Add. */
const chrome = useLensChrome();
const searchText = chrome.search;
const sortKey = chrome.sort;
const publishDestination = ref("");
const publishMethod = ref("PUT");
const publishFormat = ref("plain");
const migrateUrl = ref("");
const migrateSummary = ref("");
const migrateConfirm = ref(false);
const migrateParsed = computed(() => describeSubStoreBase(migrateUrl.value));
const migrateConfirmNames = computed(() =>
  migrateParsed.value.ok ? [`The Sub-Store at ${migrateParsed.value.origin}`] : [],
);

// One drawer at a time carries all row-scoped work; one dialog carries every
// destructive confirmation, single or batch.
const drawer = ref<{ mode: "preview" | "publish" | "share"; id: string } | null>(null);
const deleting = ref<string[]>([]);
const deleteBusy = ref(false);
// Rows currently mid-operation (refresh or delete) render pending.
const pendingIds = ref<Set<string>>(new Set());



// The records this layer lists: one kind, never files.
const onThisTab = computed(() =>
  subs.items.value.filter((i) => ((i.kind || KIND_SUB) === KIND_COLLECTION) === isComboLayer.value && i.kind !== KIND_FILE),
);

/**
 * The migration marker is a facet, not a chip on every row: production's
 * records are all migrated, and a chip on all of them says nothing about any
 * one. The facet lives on the address (`?origin=migrated`).
 */
const originFilter = chrome.facets;
function originMatches(item: SubscriptionListItem): boolean {
  if (originFilter.origin === "migrated") return item.imported;
  if (originFilter.origin === "local") return !item.imported;
  return true;
}
const originCounts = computed(() => {
  const migrated = onThisTab.value.filter((item) => item.imported).length;
  return { all: onThisTab.value.length, migrated, local: onThisTab.value.length - migrated };
});
const filtersActive = computed(() => !!searchText.value.trim() || !!originFilter.origin);

function clearFilters(): void {
  searchText.value = "";
  originFilter.origin = "";
}

function clearTransientListState(): void {
  // A pending confirm or an open drawer must not survive into the editor and
  // reappear when the operator comes back to the list.
  deleting.value = [];
  drawer.value = null;
}





// ── empty state: guidance, not a dead end ───────────────────────────────────

/** Nothing on this tab at all. The moment to offer migration alongside
 *  creation. A filter that merely hides everything is not this moment. */
const storeEmpty = computed(() => onThisTab.value.length === 0);

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
    ". Nothing is published yet, so publish a share under Networking, then Subscription Shares, to make them reachable.";
  migrateUrl.value = "";
}

// ── row status ──────────────────────────────────────────────────────────────



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
  const rows = unsortedRows.value.filter(originMatches);
  if (sortKey.value === "name" || (isComboLayer.value && sortKey.value === "recent")) {
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

function opsOf(row: SubscriptionListItem): string {
  const n = row.step_count;
  const label = `${n} op${n === 1 ? "" : "s"}`;
  return row.disabled_step_count ? `${label} (${row.disabled_step_count} off)` : label;
}


/** What the record card's count badge says and what its title explains. */
const countLabel = computed(() => plural(filteredRows.value.length, noun.value));
const countTitle = computed(() =>
  filtersActive.value
    ? `${filteredRows.value.length} of ${onThisTab.value.length} records match. The ${MAX_SUBSCRIPTION_RECORDS} record budget is shared with files.`
    : `${onThisTab.value.length} records here. The ${MAX_SUBSCRIPTION_RECORDS} record budget is shared with files.`,
);

/** The lens tells the shell when its editor is up and how many rows are selected. */
watch(
  [editing, () => selectedVisible.value.length],
  ([isEditing, count]) => {
    chrome.lenses[viewId.value].editing = isEditing;
    chrome.lenses[viewId.value].selected = count;
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
  document.querySelector<HTMLElement>(`.rec-menu[data-row-menu="${cssEscape(id)}"] button:not(:disabled)`)?.focus();
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
 * One affordance per row: the row opens the side panel, and this one menu
 * holds the verbs that act on the record without opening it. Preview,
 * publishing and editing live on the panel and the record page.
 */
const MENU_ACTIONS = ["output", "refresh", "duplicate", "delete"] as const;

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

/** The name opens the peek; the id is its title so a row can be tied to a share. */
function nameTitle(row: SubscriptionListItem): string {
  return `${row.id}. Show it in the side panel.`;
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
    const mine = isComboLayer.value ? "new-collection" : "new-subscription";
    if (isCommandIntent(value) && value.command === mine) {
      claimIntent(intent, () => true);
      startCreate(isComboLayer.value ? KIND_COLLECTION : KIND_SUB);
      return;
    }
    if (!isRecordIntent(value)) return;
    const row = onThisTab.value.find((item) => item.id === value.recordId);
    if (!row) return;
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
const counts = useNodeCounts(host);
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

// ── columns ─────────────────────────────────────────────────────────────────
// What each column says, and why a cell is empty when it is: a blank cell
// with its reason on hover, never a printed n/a.

const lineage = computed(() => buildLineage(subs.items.value, shares.value));

function usedByOf(row: SubscriptionListItem): { text: string; title: string } {
  const used = usedBy(lineage.value, row.id);
  const parts: string[] = [];
  if (used.combinations.length) parts.push(plural(used.combinations.length, "combination"));
  if (used.files.length) parts.push(plural(used.files.length, "file"));
  if (used.shares.length) parts.push(plural(used.shares.length, "share"));
  if (!parts.length) return { text: "", title: `Nothing uses this ${noun.value}: no combination, file or share draws from it.` };
  const names = [...used.combinations, ...used.files].map((id) => {
    const item = subs.items.value.find((entry) => entry.id === id);
    return item ? recordLabel(item) : id;
  });
  return { text: parts.join(", "), title: names.join(", ") };
}

function membersOf(row: SubscriptionListItem): { text: string; title: string; missing: number } {
  const resolved = lineage.value.upstream.get(row.id) ?? [];
  const missing = lineage.value.broken.filter((ref) => ref.owner === row.id);
  const names = resolved.map((id) => {
    const item = subs.items.value.find((entry) => entry.id === id);
    return item ? recordLabel(item) : id;
  });
  const shown = names.slice(0, 2).join(", ") + (names.length > 2 ? ` +${names.length - 2}` : "");
  const title = [
    ...names,
    ...missing.map((ref) => `${ref.ref} (${ref.reason})`),
    ...(row.member_tags?.length ? [`and every source tagged ${row.member_tags.join(", ")}`] : []),
  ].join(", ");
  return { text: shown, title, missing: missing.length };
}

function stepsOf(row: SubscriptionListItem): string {
  return row.disabled_step_count ? `${row.step_count} (${row.disabled_step_count} off)` : String(row.step_count);
}

const NOT_A_PROVIDER = "Only a provider link reports traffic and expiry. This source's nodes are already in hand.";
const NOT_FETCHED = "Only a provider link is refreshed. This source's nodes are already in hand.";

function figuresOf(row: SubscriptionListItem) {
  return isProviderLink(row) ? providerFigures(row) : null;
}

function openRow(row: SubscriptionListItem, event: MouseEvent): void {
  // The checkbox and the menu are controls of their own inside the row.
  const target = event.target as HTMLElement | null;
  if (target?.closest("input, [data-row-menu], .rec-menu")) return;
  chrome.openRecord(row.id);
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
    <section v-else class="lens" :aria-labelledby="`${viewId}-title`">
      <h2 :id="`${viewId}-title`" class="pc-sr-only">{{ isComboLayer ? "Combinations" : "Sources" }}</h2>

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

      <PcPanel v-if="!host.init.value || subs.state.value === 'loading'" :label="`Loading ${noun}s`">
        <PcSkeleton :count="6" :label="`Loading the ${noun}s`" />
      </PcPanel>

      <template v-else-if="subs.loadError.value">
        <PcNotice tone="danger" title="The list could not be loaded">
          {{ subs.loadError.value }}
          <template #actions><PcButton compact @click="loadAll()">Try again</PcButton></template>
        </PcNotice>
        <PcPanel :label="isComboLayer ? 'Combinations' : 'Sources'">
          <PcEmptyState kind="error" title="Nothing could be loaded">
            <p>This is not an empty store, it is an unanswered question.</p>
          </PcEmptyState>
        </PcPanel>
      </template>

      <PcPanel v-else-if="storeEmpty && isComboLayer" label="Combinations">
        <PcEmptyState title="No combinations yet">
          <template #icon><Layers :size="26" aria-hidden="true" /></template>
          <p>A combination merges several sources and runs one chain over the result, so a file can render all of them at once.</p>
          <template #actions>
            <PcButton
              :disabled="!subs.canMutate.value || !subs.items.value.some((item) => (item.kind || KIND_SUB) === KIND_SUB)"
              :title="subs.items.value.some((item) => (item.kind || KIND_SUB) === KIND_SUB) ? undefined : 'Create a source first. There is nothing to combine'"
              @click="startCreate(KIND_COLLECTION)"
            >
              New combination
            </PcButton>
          </template>
        </PcEmptyState>
      </PcPanel>

      <PcPanel v-else-if="storeEmpty" label="Sources">
        <PcEmptyState title="No sources yet">
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
        <PcNotice v-if="subs.staleError.value" tone="warning" title="Showing the last good read">
          The newest reload failed ({{ subs.staleError.value }}).
        </PcNotice>

        <PcPanel :label="isComboLayer ? 'Combinations' : 'Sources'">
          <div class="rec-list" :aria-label="isComboLayer ? 'Combinations' : 'Sources'">
            <div class="rec-tools">
              <PcSearchField v-model="searchText" placeholder="Filter by name, id, remark, tag" :label="`Filter ${noun}s`" />
              <label class="toolbar-sort">
                <span>Origin</span>
                <select v-model="originFilter.origin" class="pc-select" aria-label="Filter by where the record came from">
                  <option value="">All {{ originCounts.all }}</option>
                  <option value="migrated">Migrated {{ originCounts.migrated }}</option>
                  <option value="local">Made here {{ originCounts.local }}</option>
                </select>
              </label>
              <label v-if="!isComboLayer" class="toolbar-sort">
                <span>Sort</span>
                <select v-model="sortKey" class="pc-select" aria-label="Sort records">
                  <option value="recent">Recently refreshed</option>
                  <option value="name">Name</option>
                  <option value="status">Needs attention</option>
                </select>
              </label>
              <PcCount :value="countLabel" :label="countTitle" />
              <p v-if="!subs.canMutate.value" class="rec-list-note">This session cannot create records here.</p>
            </div>

            <PcEmptyState
              v-if="!filteredRows.length"
              kind="no-match"
              :title="searchText.trim() ? 'No record matches that search' : `No ${noun}s match that filter`"
            >
              <p v-if="searchText.trim()">
                Nothing here is called, tagged or described as <span class="pc-mono">{{ searchText.trim() }}</span>.
              </p>
              <p v-else>None of the {{ plural(onThisTab.length, noun) }} here {{ originFilter.origin === "migrated" ? "was migrated" : "was made here" }}.</p>
              <template #actions>
                <PcButton :disabled="!filtersActive" @click="clearFilters()">Clear filters</PcButton>
              </template>
            </PcEmptyState>

            <PcTable v-else :stacked="false" :min-width="isComboLayer ? 760 : 1040" :label="isComboLayer ? 'Combinations' : 'Sources'" class="layer-table">
              <template #head>
                <PcSelectCell
                  header
                  :checked="allVisibleSelected"
                  :indeterminate="selectedCount > 0 && !allVisibleSelected"
                  :label="`Select all ${filteredRows.length} shown records`"
                  @change="toggleSelectAll()"
                />
                <PcTh name>Name</PcTh>
                <PcTh v-if="!isComboLayer" width="120px">Kind</PcTh>
                <PcTh v-else width="220px">Members</PcTh>
                <PcTh numeric width="96px">Nodes</PcTh>
                <PcTh numeric width="72px">Steps</PcTh>
                <template v-if="!isComboLayer">
                  <PcTh width="200px">Provider</PcTh>
                  <PcTh width="150px">Last fetch</PcTh>
                </template>
                <PcTh width="190px">Used by</PcTh>
                <PcTh actions width="48px"><span class="pc-sr-only">Actions</span></PcTh>
              </template>
              <tbody>
                <PcRow
                  v-for="row in filteredRows"
                  :id="`rec-${row.id}`"
                  :key="row.id"
                  class="layer-row"
                  :class="{ 'is-pending': pendingIds.has(row.id) }"
                  :selected="selectedIds.has(row.id) || chrome.openId.value === row.id"
                  @click="openRow(row, $event)"
                >
                  <PcSelectCell :checked="selectedIds.has(row.id)" :label="`Select ${row.name}`" @change="toggleSelected(row.id)" />
                  <td class="pc-name" data-stack="name">
                    <div class="pc-name-line">
                      <button type="button" class="row-open" :title="nameTitle(row)" @click.stop="chrome.openRecord(row.id)">
                        <strong>{{ row.display_name || row.name }}</strong>
                      </button>
                      <span v-if="row.tags?.length" class="pc-name-after"><PcTagList :tags="row.tags" :max="2" /></span>
                    </div>
                    <small :title="row.remark || row.id">{{ row.remark || row.id }}</small>
                  </td>
                  <td v-if="!isComboLayer" data-stack="detail" data-label="Kind"><span class="pc-td-body">{{ sourceKindLabel(row) }}</span></td>
                  <td v-else data-stack="detail" data-label="Members" :title="membersOf(row).title">
                    <span class="pc-td-body layer-members">
                      <span class="layer-members-names">{{ membersOf(row).text }}</span>
                      <PcStateDot v-if="membersOf(row).missing" tone="error" :label="`${membersOf(row).missing} missing`" />
                    </span>
                  </td>
                  <td class="pc-numeric pc-mono" data-stack="detail" data-label="Nodes" :title="nodesTitle(row)"><span class="pc-td-body">{{ nodesOf(row) }}</span></td>
                  <td class="pc-numeric pc-mono" data-stack="detail" data-label="Steps" :title="row.target ? `Always rendered for ${row.target}` : undefined"><span class="pc-td-body">{{ stepsOf(row) }}</span></td>
                  <template v-if="!isComboLayer">
                    <td data-stack="detail" data-label="Provider" :title="figuresOf(row) ? undefined : NOT_A_PROVIDER">
                      <span v-if="figuresOf(row)" class="pc-td-body layer-provider">
                        <UsageBar :figures="figuresOf(row)!" />
                        <span v-if="formatExpiry(figuresOf(row), Date.now())" class="layer-expiry">{{ formatExpiry(figuresOf(row), Date.now()) }}</span>
                      </span>
                      <span v-else-if="isProviderLink(row)" class="pc-td-body layer-muted" title="The provider has not sent traffic figures, or the link has not been refreshed yet.">not reported</span>
                    </td>
                    <td data-stack="detail" data-label="Last fetch" :title="isProviderLink(row) ? undefined : NOT_FETCHED">
                      <span v-if="isProviderLink(row)" class="pc-td-body">
                        <PcStateDot :tone="tone(statusOf(row).tone)" :label="statusOf(row).label" :title="statusOf(row).title || statusOf(row).label" />
                      </span>
                    </td>
                  </template>
                  <td data-stack="detail" data-label="Used by" :title="usedByOf(row).title"><span class="pc-td-body">{{ usedByOf(row).text }}</span></td>
                  <td class="pc-actions" data-stack="actions">
                    <div class="pc-row-actions">
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
                  </td>
                </PcRow>
              </tbody>
            </PcTable>
          </div>
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
