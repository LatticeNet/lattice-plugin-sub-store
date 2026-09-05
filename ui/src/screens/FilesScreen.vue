<script setup lang="ts">
import { computed, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from "vue";
import {
  Braces,
  ChevronLeft,
  CircleAlert,
  ClipboardPaste,
  Eye,
  FileCode,
  FileText,
  Globe,
  LoaderCircle,
  SquareArrowOutUpRight,
  Trash2,
} from "@lucide/vue";
import {
  PcActionsCell,
  PcBatchBar,
  PcButton,
  PcCount,
  PcEmptyState,
  PcKindChip,
  PcLensTab,
  PcLensTabs,
  PcNameCell,
  PcNotice,
  PcPanel,
  PcPanelBody,
  PcPanelHeader,
  PcRow,
  PcSelectCell,
  PcSidePanel,
  PcSkeleton,
  PcStateDot,
  PcStatePill,
  PcTable,
  PcTagList,
  PcTd,
  PcTh,
} from "@latticenet/plugin-bridge/chassis";

import {
  FILE_TYPE_CONFIG,
  FILE_TYPE_PLAIN,
  FILE_TYPE_SCRIPT,
  KIND_COLLECTION,
  KIND_FILE,
  KIND_SUB,
  MAX_SUBSCRIPTION_RECORDS,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  type SubscriptionListItem,
} from "../client";
import { filePreviewSupport } from "../filePreview";
import { useHost } from "../host";
import { closeTopOverlay, overlayDepth } from "../overlayStack";
import { hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import { tagChips } from "../rowStatus";
import { matchesQuery, normalizeQuery } from "../recordSearch";
import { publishStateFor, stateTone } from "../shareState";
import { useLensChrome } from "../lensChrome";
import { useShares } from "../useShares";
import { actionsFor, batchActionsFor, type ActionCapabilities, type ActionId } from "../recordActions";
import { claimIntent, isCommandIntent, isRecordIntent, recordIntent } from "../recordIntent";
import { useEditorExit } from "../useEditorExit";
import {
  draftFromRecord,
  emptyDraft,
  knownFileType,
  useSubscriptions,
  validateDraft,
  type SubscriptionDraft,
} from "../useSubscriptions";
import CodeEditor from "../components/CodeEditor.vue";
import MaskedUrlInput from "../components/MaskedUrlInput.vue";
import DocumentView from "../components/DocumentView.vue";
import EngineUnavailable from "../components/EngineUnavailable.vue";
import ProcessChain, { type ChainStep } from "../components/ProcessChain.vue";
import TargetSheet from "../components/TargetSheet.vue";
import LtConfirmDialog from "../components/lt/LtConfirmDialog.vue";
import RecordMenu from "../components/RecordMenu.vue";
import type { EditorLanguage } from "../codemirror";
import {
  editorLanguageForFileType,
  editorLanguageLabel,
} from "../previewLanguage";

/**
 * The Files tab.
 *
 * A file is a document the core serves, usually a client configuration the
 * operator has already tuned, whose proxy list is filled in from a
 * subscription or a combination. It is the piece that lets nodes change without
 * anyone hand-editing a config, and it shares the subscription store, so
 * everything here runs on methods the signed manifest already declares.
 *
 * It deliberately reuses the sibling tab's list idiom, toolbar and drawer
 * rather than a near-identical set of its own. It had its own before, and the
 * two tabs had drifted into looking like two products: a different row shape, a
 * toolbar whose styles were scoped to the other screen and so never applied
 * here, and row-scoped panels that pushed the list around instead of opening
 * beside it.
 */

const host = useHost();
const subs = useSubscriptions(host);

const editing = ref(false);
const editingId = ref<string | null>(null);
const draft = ref<SubscriptionDraft>(emptyDraft());
const tagText = ref("");

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

const isPlain = computed(() => draft.value.fileType === FILE_TYPE_PLAIN);
const isScript = computed(() => draft.value.fileType === FILE_TYPE_SCRIPT);

/**
 * Editor highlighting. The file type decides the sensible default (script →
 * JavaScript, config → YAML), and the selector lets the operator override it
 * for the odd file. A JSON template, an INI ruleset. Without inventing new
 * file types. Pure presentation: nothing about the record changes.
 */
const CONTENT_LANGUAGES: ReadonlyArray<{ id: EditorLanguage; label: string }> = [
  { id: "yaml", label: "YAML" },
  { id: "javascript", label: "JavaScript" },
  { id: "json", label: "JSON" },
  { id: "ini", label: "INI" },
  { id: "plain", label: "Plain text" },
];
const contentLanguageOverride = ref<EditorLanguage | "">("");
const autoLanguage = computed<EditorLanguage>(() => {
  return editorLanguageForFileType(draft.value.fileType);
});
const contentLanguage = computed<EditorLanguage>(
  () => contentLanguageOverride.value || autoLanguage.value,
);
const contentLanguageLabel = computed(() => editorLanguageLabel(contentLanguage.value));
const queryParamText = ref("");
const isRemote = computed(() => draft.value.source === SOURCE_REMOTE);
const draftError = computed(() => (editing.value ? validateDraft(draft.value) : ""));
const canSave = computed(() => !draftError.value && !subs.saving.value);
/**
 * Whether `preview` will answer for the draft as it stands.
 *
 * The backend refuses a file that needs a node source, a fetch, a program or a
 * chain, because preview is signed for two host calls and each of those is
 * host-capable work. Both this screen and the row used to offer the control
 * anyway and print the refusal, a backend sentence, as the reason. The row now
 * renders such a file instead; the editor cannot, because render takes the
 * SAVED record and the editor's whole point is unsaved text, so it says so and
 * names what does work.
 */
const draftPreview = computed(() =>
  filePreviewSupport({
    kind: KIND_FILE,
    file_type: draft.value.fileType,
    node_source: draft.value.nodeSource,
    source: draft.value.source,
    // Source is the current authority. The form retains a previous link so an
    // operator can switch back without retyping it, but local text must not be
    // classified as a fetch because that dormant field is still populated.
    has_url: isRemote.value && !!draft.value.url.trim(),
    step_count: (draft.value.process as unknown[]).length,
  }),
);

/** Preview needs a saved record, a readable draft, and a shape the backend
 *  will actually answer for. */
const canPreviewNow = computed(
  () =>
    subs.canPreview.value &&
    !subs.previewing.value &&
    !draftError.value &&
    !!editingId.value &&
    draftPreview.value.supported,
);

const allFiles = computed(() => subs.items.value.filter((i) => i.kind === KIND_FILE));

/** The preview/copy sheet. A file is exactly the thing you hand to a client. */
const targetSheet = ref<SubscriptionListItem | null>(null);
const targetSheetTrigger = ref<HTMLElement | null>(null);
/** Selection for batch delete; the record limit is 256 and deleting one at a
 *  time was the only way out of a bad import. */
const selectedIds = ref<Set<string>>(new Set());
const deleteTargets = ref<{ ids: string[]; names: string[] } | null>(null);
const deleteBusy = ref(false);
/** Rows mid-operation render pending rather than silently unresponsive. */
const pendingIds = ref<Set<string>>(new Set());

/** The toolbar's search lives in the shell; this lens filters on it. */
const chrome = useLensChrome();
const searchText = chrome.search;

/**
 * Files get the same search predicate as subscriptions: one predicate, in
 * recordSearch, so a name, an id, a remark and a tag all match here too.
 */
const files = computed(() => {
  const query = normalizeQuery(searchText.value);
  return allFiles.value.filter((file) => matchesQuery(file, query));
});

/** Whether anyone can fetch a file: the host's share list, folded onto the row. */
const shareStore = useShares(host);
function publishedOf(item: SubscriptionListItem) {
  return publishStateFor(shareStore.shares.value, item.id);
}
const tone = stateTone;

/** What a file is, for the kind chip beside its name. */
function kindLabel(item: SubscriptionListItem): string {
  const type = knownFileType(item.file_type);
  if (type === FILE_TYPE_SCRIPT) return "script";
  if (type === FILE_TYPE_PLAIN) return "plain text";
  return "configuration";
}

/** The store holds no files at all, which is a different situation from a
 *  filter that matched nothing and needs different copy and different actions.
 *  They used to share one branch, so searching for a name that did not exist
 *  showed the first-run "paste your Mihomo config" panel. */
const storeEmpty = computed(() => allFiles.value.length === 0);

const filtersActive = computed(() => !!searchText.value.trim());

function clearFilters(): void {
  searchText.value = "";
}

/** What the batch controls report and act on: only rows that exist and are on
 *  screen. A stale id from a filtered or already-deleted row must never be
 *  part of what Delete promises. */
const selectedVisible = computed(() => files.value.filter((file) => selectedIds.value.has(file.id)));
const selectedCount = computed(() => selectedVisible.value.length);
const allVisibleSelected = computed(
  () => files.value.length > 0 && selectedCount.value === files.value.length,
);

function toggleSelectAll(): void {
  selectedIds.value = allVisibleSelected.value
    ? new Set()
    : new Set(files.value.map((file) => file.id));
}

function openFileSheet(item: SubscriptionListItem, event?: Event): void {
  closeRowMenu();
  targetSheetTrigger.value = (event?.currentTarget as HTMLElement | null | undefined) ?? null;
  targetSheet.value = item;
}

function closeTargetSheet(): void {
  targetSheet.value = null;
  const trigger = targetSheetTrigger.value;
  targetSheetTrigger.value = null;
  void nextTick(() => trigger?.focus());
}

function toggleSelected(id: string): void {
  const next = new Set(selectedIds.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  selectedIds.value = next;
}

function requestDelete(ids: string[]): void {
  closeRowMenu();
  const names = ids.map((id) => {
    const file = allFiles.value.find((entry) => entry.id === id);
    return file ? file.display_name || file.name : id;
  });
  deleteTargets.value = { ids, names };
}

/**
 * Stop on the first failure rather than ploughing through the rest.
 *
 * This loop ignored every result, so a delete refused by the backend left the
 * dialog reporting success and the remaining records deleted anyway. It also
 * reported `subs.saving` as its busy state, which is the SAVE flag: the
 * confirm button never showed that anything was happening.
 */
async function confirmDelete(): Promise<void> {
  const target = deleteTargets.value;
  if (!target) return;
  deleteBusy.value = true;
  try {
    for (const id of target.ids) {
      markPending(id, true);
      const ok = await subs.remove(id);
      markPending(id, false);
      if (!ok) break;
    }
  } finally {
    deleteBusy.value = false;
    deleteTargets.value = null;
    selectedIds.value = new Set();
  }
}

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

/** The lens tells the shell when its editor is up and how many rows are selected. */
watch(
  [editing, selectedCount],
  ([isEditing, count]) => {
    chrome.lenses.files.editing = isEditing;
    chrome.lenses.files.selected = count;
  },
  { immediate: true },
);

// ── row menu ────────────────────────────────────────────────────────────────

/**
 * Which file's overflow menu is open; only ever one.
 *
 * It had no way to close other than clicking the same trigger again: no
 * outside-click handler and no Escape, so a menu opened by accident stayed
 * open over the rows underneath it.
 */
const openFileMenuId = ref("");

function closeRowMenu(): void {
  const id = openFileMenuId.value;
  openFileMenuId.value = "";
  if (id) {
    void nextTick(() => {
      document.querySelector<HTMLElement>(`[data-row-menu="${cssEscape(id)}"] button`)?.focus();
    });
  }
}

/** Ids come from the store and are not guaranteed selector-safe. */
function cssEscape(value: string): string {
  const escape = (globalThis as { CSS?: { escape?: (v: string) => string } }).CSS?.escape;
  return escape ? escape(value) : value.replace(/["\\]/g, "\\$&");
}

async function toggleFileMenu(id: string): Promise<void> {
  const opening = openFileMenuId.value !== id;
  openFileMenuId.value = opening ? id : "";
  await host.resize();
  if (!opening) return;
  await nextTick();
  document.querySelector<HTMLElement>(`.rec-menu[data-row-menu="${cssEscape(id)}"] button:not(:disabled)`)?.focus();
}

function onRowMenuKeydown(event: KeyboardEvent): void {
  const step = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
  if (!step) return;
  event.preventDefault();
  const items = [...(event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>("button:not(:disabled)")];
  if (!items.length) return;
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  items[(current + step + items.length) % items.length]?.focus();
}

function onDocumentClick(event: MouseEvent): void {
  if (!openFileMenuId.value) return;
  if ((event.target as HTMLElement | null)?.closest("[data-row-menu]")) return;
  openFileMenuId.value = "";
}

/** The one Escape arbiter for this screen; see the sibling tab for why every
 *  overlay stopped answering the key itself. */
function onDocumentKeydown(event: KeyboardEvent): void {
  if (event.key !== "Escape") return;
  if (closeTopOverlay()) return;
  if (openFileMenuId.value) {
    closeRowMenu();
    return;
  }
  // Escape is how every other surface in this frame steps back, and the editor
  // is a screen you enter, so it answers the same key. Who owns the key while
  // an overlay is up is decided in editorExit.ts.
  exit.onEscape();
}

// ── drawer ──────────────────────────────────────────────────────────────────

/**
 * The drawer carries the share guidance, the one row-scoped panel that is not
 * the document. The document itself has one surface, the sheet: the row menu
 * used to open a second viewer here, capped at eight lines and scrolling
 * inside itself, while the name and the » on the same row opened the sheet.
 * Two viewers for one job, and the drawer's was the one that broke the
 * one-scroller rule.
 */
const drawer = ref<{ mode: "share"; id: string } | null>(null);
/** What opened the panel, so focus returns there on close. */
const drawerTrigger = ref<HTMLElement | null>(null);

const drawerItem = computed(() =>
  drawer.value ? allFiles.value.find((file) => file.id === drawer.value?.id) : undefined,
);

const drawerTitle = computed(() => {
  if (!drawer.value || !drawerItem.value) return "";
  return `Publish · ${drawerItem.value.display_name || drawerItem.value.name}`;
});

function openDrawer(mode: "share", id: string, event?: Event): void {
  closeRowMenu();
  drawerTrigger.value = (event?.currentTarget as HTMLElement | null | undefined) ?? null;
  drawer.value = { mode, id };
}

function closeDrawer(): void {
  drawer.value = null;
}

// ── editor ──────────────────────────────────────────────────────────────────

/** Anything that resolves to nodes. A file sourcing a file would recurse. */
const nodeSources = computed(() =>
  subs.items.value.filter((i) => (i.kind || KIND_SUB) === KIND_SUB || i.kind === KIND_COLLECTION),
);

/**
 * The stored node source when no candidate matches it, meaning the record it
 * names is gone. A `select` whose value matches no option renders blank, so
 * the field said "nothing chosen" over a file that is in fact pointing at a
 * deleted record, and the first touch of the control would silently rewrite
 * it. The dangling id is offered as its own option instead, marked for what
 * it is.
 */
const danglingNodeSource = computed(() => {
  const id = draft.value.nodeSource.trim();
  if (!id || subs.state.value !== "ready") return "";
  return nodeSources.value.some((item) => item.id === id) ? "" : id;
});

const FILE_TYPES = [
  {
    id: FILE_TYPE_CONFIG,
    title: "Client configuration",
    detail: "Mihomo or Clash YAML. Its proxies get replaced from the node source you pick below.",
    icon: FileCode,
  },
  {
    id: FILE_TYPE_PLAIN,
    title: "Plain text",
    detail: "A rule list, a fragment, anything else. Served exactly as written.",
    icon: FileText,
  },
  {
    id: FILE_TYPE_SCRIPT,
    title: "Built by a script",
    detail: "A JavaScript program assembles the whole document from your nodes.",
    icon: Braces,
  },
] as const;

const TEMPLATE_SOURCES = [
  {
    id: SOURCE_LOCAL,
    title: "Text I paste",
    detail: "Kept in this deployment. Edit it here whenever you like.",
    icon: ClipboardPaste,
  },
  {
    id: SOURCE_REMOTE,
    title: "A link",
    detail: "Fetched from a URL, so a template you maintain elsewhere stays the source of truth.",
    icon: Globe,
  },
] as const;

function sourceName(id: string | undefined): string {
  if (!id) return "";
  const found = subs.items.value.find((item) => item.id === id);
  return found ? found.display_name || found.name : id;
}

/**
 * A file pointing at a record that is no longer in the store.
 *
 * Deleting a subscription does not touch the files that draw from it, and the
 * row showed the dangling id as though it were a name, so a file that cannot
 * serve at all looked exactly like one that can. The store is the authority
 * here, so this is only asked once the list has actually loaded.
 */
function nodeSourceMissing(item: SubscriptionListItem): boolean {
  const id = (item.node_source ?? "").trim();
  if (!id || subs.state.value !== "ready") return false;
  return !subs.items.value.some((entry) => entry.id === id);
}

/**
 * The name cell ellipses at 375, so the whole name rides in the title, with
 * the id (what ties a file to a share) the way the sibling tab does it.
 */
function nameTitle(item: SubscriptionListItem): string {
  return `${item.id}. ${item.display_name || item.name}`;
}

/**
 * What this session may do, in the shape the action registry reads. The same
 * five capabilities the sibling screen reports, so "why is that greyed out"
 * has one answer across both.
 */
const actionCaps = computed<ActionCapabilities>(() => ({
  ready: !!host.init.value,
  mutate: subs.canMutate.value,
  fetch: subs.canFetch.value,
  preview: subs.canPreview.value,
  render: subs.canRender.value,
  publish: subs.canPublish.value,
}));

// A file has no node preview and nothing to refresh: the registry decides that
// from the record's kind, so this names the menu's slots and not the rules.
// Publish is deliberately absent: this screen has no publish drawer, and the
// registry offering an action a screen cannot carry out is worse than not
// offering it. Adding the flow is a decision, not a wiring gap.
const MENU_ACTIONS = ["output", "share", "duplicate", "delete"] as const;

function menuActionsFor(item: SubscriptionListItem) {
  return actionsFor(item, actionCaps.value, MENU_ACTIONS);
}

/** What the selection can carry; blocked if any record in it refuses. */
const batchActions = computed(() => batchActionsFor(selectedVisible.value, actionCaps.value));

/** One resolved action, for the icon buttons that sit in the row itself. */
function rowAction(item: SubscriptionListItem, id: ActionId) {
  return (
    actionsFor(item, actionCaps.value, [id])[0] ?? { id, label: "", icon: "", danger: false, reason: "", disabled: true }
  );
}

/**
 * The registry says what and when; this says how. Every caller goes through
 * here — the row's icon buttons, its menu, and the palette — so an action
 * means the same thing wherever it was started from.
 */
function runRowAction(id: ActionId, item: SubscriptionListItem, event: MouseEvent): void {
  closeRowMenu();
  if (id === "edit") return void startEdit(item.id);
  if (id === "refresh") return void refreshRow(item.id);
  if (id === "output") return openFileSheet(item, event);
  if (id === "share") return openDrawer("share", item.id, event);
  if (id === "duplicate") return void subs.duplicate(item.id);
  if (id === "delete") return requestDelete([item.id]);
}

/**
 * Requests from the palette. Only intents this screen owns are taken: both
 * screens are kept alive and both watch, so the sibling must find its own.
 */
const intent = recordIntent(host);
watch(
  intent,
  (value) => {
    if (isCommandIntent(value) && value.command === "new-file") {
      claimIntent(intent, () => true);
      startCreate();
      return;
    }
    if (!isRecordIntent(value)) return;
    const item = subs.items.value.find((row) => row.id === value.recordId);
    if (!item || item.kind !== KIND_FILE) return;
    claimIntent(intent, () => true);
    runRowAction(value.action, item, new MouseEvent("click"));
  },
  { immediate: true },
);

/**
 * The editor's sections, split the way the subscription editor splits them:
 * what the file is called, what it is made of, and what is done to it.
 *
 * This form was a single scroll of six fieldsets — 1400px, nearly a viewport —
 * while its sibling doing the same job was 356px behind three tabs. The pane
 * beside it is sticky, so the operator scrolled a screen and a half of form
 * past a fixed panel to reach the field they came for.
 */
type EditorTab = "display" | "content" | "operations";
const editorTab = ref<EditorTab>("display");
const EDITOR_TABS: { id: EditorTab; label: string }[] = [
  { id: "display", label: "Display" },
  { id: "content", label: "Content" },
  { id: "operations", label: "Operations" },
];

/**
 * Which section holds the invalid field. A form that says what is wrong and not
 * where is worse behind tabs than in a single scroll: the name lives two tabs
 * away and nothing points at it. Every message except the name one is about
 * what the file is made of, so this is read off the draft rather than by
 * matching message text.
 */
const errorTab = computed<EditorTab | "">(() => {
  if (!draftError.value) return "";
  return draft.value.name.trim() ? "content" : "display";
});

/** The chain's size, shown on the tab so it is visible without opening it. */
const chainCount = computed(() => (draft.value.process as unknown[]).length);

/**
 * The unsaved-edit guard, the same one the Subscriptions editor uses. The
 * snapshot is the serialised draft plus the two text fields that live outside
 * it — tags and the query-parameter allowlist — because those are edits too.
 * The language selector is not: it changes how the document is coloured, not
 * what gets saved.
 */
const exit = useEditorExit({
  editing,
  fingerprint: () => JSON.stringify([draft.value, tagText.value, queryParamText.value]),
  // The registered depth, not a hand-written list: this screen is where such a
  // list was forgotten in the first place.
  overlayOpen: () => overlayDepth() > 0 || !!openFileMenuId.value,
  leave: () => cancelEdit(),
});
const { discarding, markPristine } = exit;
const editorDirty = exit.dirty;
const leaveEditor = exit.leaveEditor;

function clearTransientListState(): void {
  openFileMenuId.value = "";
  drawer.value = null;
  deleteTargets.value = null;
}

function startCreate(fileType: string = FILE_TYPE_CONFIG): void {
  clearTransientListState();
  subs.clearMessages();
  draft.value = emptyDraft();
  draft.value.kind = KIND_FILE;
  draft.value.fileType = fileType;
  draft.value.source = SOURCE_LOCAL;
  // Most deployments have exactly one thing worth pointing at, and picking it
  // by default is the difference between a form that works and one that saves
  // a config serving no nodes. Plain text has no proxy list to fill, so the
  // default would be a stored setting with no effect.
  draft.value.nodeSource =
    fileType !== FILE_TYPE_PLAIN && nodeSources.value.length === 1 ? nodeSources.value[0]!.id : "";
  tagText.value = "";
  // The allowlist is the guard on what a public share URL may reach in a
  // script. Leaving the previous record's value in place meant a new file
  // silently inherited an allowlist nobody chose for it.
  queryParamText.value = "";
  contentLanguageOverride.value = "";
  editorTab.value = "display";
  editingId.value = null;
  editing.value = true;
  markPristine();
}

async function startEdit(id: string): Promise<void> {
  clearTransientListState();
  subs.clearMessages();
  const record = await subs.get(id);
  if (!record) return;
  draft.value = draftFromRecord(record);
  if (!draft.value.source) draft.value.source = draft.value.url ? SOURCE_REMOTE : SOURCE_LOCAL;
  tagText.value = draft.value.tags.join(", ");
  queryParamText.value = draft.value.queryParams.join(", ");
  contentLanguageOverride.value = "";
  editorTab.value = "display";
  editingId.value = id;
  editing.value = true;
  markPristine();
  await host.resize();
}

function cancelEdit(): void {
  exit.reset();
  editing.value = false;
  editingId.value = null;
  draft.value = emptyDraft();
  subs.preview.value = null;
  // Errors belong to the screen that raised them, and the notice does not: a
  // successful save reports "Saved ..." and then leaves through here.
  subs.clearErrors();
}

function splitList(text: string): string[] {
  return text
    .split(/[,\n]/)
    .map((entry) => entry.trim())
    .filter(Boolean);
}

async function submit(): Promise<void> {
  draft.value.tags = splitList(tagText.value);
  draft.value.queryParams = splitList(queryParamText.value);
  const ok = await subs.save(draft.value);
  if (ok) cancelEdit();
}

/**
 * Load after the bridge handshake, not on mount: `available()` reads the
 * interfaces the host declares for this frame, and on first paint that has not
 * arrived, so loading in `onMounted` alone silently no-ops and never retries.
 */
async function loadAll(): Promise<void> {
  void shareStore.load();
  await subs.load();
  await subs.loadOperators();
}

// The screens are kept alive between tab switches, so a record created on the
// sibling tab would otherwise be missing here until a full reload, most
// visibly in the node-source picker, which lists subscriptions.
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
  if (host.init.value) void loadAll();
  bindDocumentKeys();
});
onActivated(bindDocumentKeys);
onDeactivated(releaseDocumentKeys);
onBeforeUnmount(releaseDocumentKeys);

watch(host.init, (value) => {
  if (value) void loadAll();
});
</script>

<template>
  <EngineUnavailable v-if="host.init.value && !subs.available.value" feature="Files" />

  <template v-else>
    <!-- ── editor ───────────────────────────────────────────────────────── -->
    <section v-if="editing" class="configuration editor-shell" aria-labelledby="file-editor-title">
      <!-- The sibling editor has one; without it this screen's only way back is
           the Cancel button at the far bottom of a long form. -->
      <nav class="lt-breadcrumb" aria-label="Breadcrumb">
        <button type="button" class="lt-breadcrumb-root" @click="leaveEditor">
          <ChevronLeft :size="14" aria-hidden="true" /> Files
        </button>
        <span class="lt-breadcrumb-sep" aria-hidden="true">/</span>
        <span class="lt-breadcrumb-here" aria-current="page">
          {{ editingId ? draft.displayName || draft.name || editingId : "New file" }}
        </span>
      </nav>
      <div class="section-heading">
        <div>
          <h2 id="file-editor-title">
            {{ editingId ? "Edit" : "New" }} file
            <span v-if="editorDirty" class="editor-dirty" role="status" title="Not saved yet. The draft stays here while you look at another lens.">Unsaved changes</span>
          </h2>
          <p>
            A document served as it is, with its proxy list kept in step with a subscription.
          </p>
        </div>
      </div>

      <div v-if="subs.actionError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
      </div>

      <PcLensTabs v-model="editorTab" label="Editor sections">
        <PcLensTab
          v-for="tab in EDITOR_TABS"
          :key="tab.id"
          :value="tab.id"
          :count="tab.id === 'operations' && chainCount ? chainCount : null"
        >
          {{ tab.label }}
          <span
            v-if="errorTab === tab.id && editorTab !== tab.id"
            class="editor-tab-flag"
            :title="draftError"
            aria-label="This section has a problem"
          />
        </PcLensTab>
      </PcLensTabs>

      <!-- Form and evidence side by side, the same layout the subscription
           editor uses. The pane is wider here because the evidence is: a node
           list is short rows, a rendered configuration is 80-column text, and
           squeezing that into a 380px column to match would be shape over
           substance. -->
      <div class="editor-layout" data-pane="wide">
      <form class="editor-main" @submit.prevent="submit">
        <PcPanel v-show="editorTab === 'display'" class="editor-group" role="group" label="Basics">
          <PcPanelHeader title="Basics" description="How the file is named, tagged and listed." />
          <PcPanelBody>
          <div class="form-grid">
            <label class="field field-wide">
              <span class="field-label">Name</span>
              <input v-model="draft.name" type="text" autocomplete="off" placeholder="Phone config" />
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
              <input v-model="draft.displayName" type="text" autocomplete="off" placeholder="Phone" />
              <span class="field-optional">Shown in the list instead of the name.</span>
            </label>

            <label class="field">
              <span class="field-label">Tags</span>
              <input v-model="tagText" type="text" autocomplete="off" spellcheck="false" placeholder="phone, laptop" />
            </label>

            <label class="field field-wide">
              <span class="field-label">Note</span>
              <input v-model="draft.remark" type="text" autocomplete="off" placeholder="Optional" />
            </label>

            <label class="field field-wide checkbox-field">
              <input v-model="draft.download" type="checkbox" />
              <span>
                <span class="field-label">Save rather than show</span>
                <span class="field-optional">
                  Served with a filename, so a browser downloads it instead of rendering it in a
                  tab. Clients that fetch the URL directly are unaffected.
                </span>
              </span>
            </label>
          </div>
          </PcPanelBody>
        </PcPanel>

        <PcPanel v-show="editorTab === 'content'" class="editor-group" role="group" label="What kind of file">
          <PcPanelHeader title="What kind of file" description="Plain text served as written, a template with a proxy list, or a script." />
          <PcPanelBody>
          <div class="form-grid">
            <div class="field field-wide">
              <div class="source-grid">
                <button
                  v-for="option in FILE_TYPES"
                  :key="option.id"
                  type="button"
                  :class="['source', { 'is-active': draft.fileType === option.id }]"
                  @click="draft.fileType = option.id"
                >
                  <component :is="option.icon" :size="17" aria-hidden="true" />
                  <span class="source-title">{{ option.title }}</span>
                  <span class="source-detail">{{ option.detail }}</span>
                </button>
              </div>
            </div>
          </div>
          </PcPanelBody>
        </PcPanel>

        <PcPanel v-show="editorTab === 'content'" class="editor-group" role="group" :label="isScript ? 'The program' : isPlain ? 'The text' : 'The template'">
          <PcPanelHeader :title="isScript ? 'The program' : isPlain ? 'The text' : 'The template'" :description="isScript ? 'What runs when a client asks for this file.' : isPlain ? 'What is served, exactly as written.' : 'What is served, with its proxy list filled in from the source below.'" />
          <PcPanelBody>
          <div class="form-grid">
            <div v-if="!isScript" class="field field-wide">
              <div class="source-grid">
                <button
                  v-for="option in TEMPLATE_SOURCES"
                  :key="option.id"
                  type="button"
                  :class="['source', { 'is-active': draft.source === option.id }]"
                  @click="draft.source = option.id"
                >
                  <component :is="option.icon" :size="17" aria-hidden="true" />
                  <span class="source-title">{{ option.title }}</span>
                  <span class="source-detail">{{ option.detail }}</span>
                </button>
              </div>
            </div>

            <template v-if="isRemote && !isScript">
              <!-- A template link can carry a token like a provider link, so
                   it reads masked and shows whole only while edited. -->
              <div class="field field-wide">
                <span class="field-label">Link</span>
                <MaskedUrlInput
                  v-model="draft.url"
                  aria-label="Link"
                  placeholder="Where the template is fetched from"
                />
              </div>
              <label class="field">
                <span class="field-label">User agent</span>
                <input v-model="draft.ua" type="text" autocomplete="off" placeholder="Optional" />
              </label>
            </template>

            <div v-if="isScript || !isRemote" class="field field-wide">
              <span id="file-content-label" class="field-label field-label-row">
                {{ isScript ? "Script" : isPlain ? "Text" : "Configuration" }}
                <select
                  v-model="contentLanguageOverride"
                  class="select select-compact"
                  aria-label="Editor highlighting"
                >
                  <option value="">Auto ({{ CONTENT_LANGUAGES.find((l) => l.id === autoLanguage)?.label }})</option>
                  <option v-for="lang in CONTENT_LANGUAGES" :key="lang.id" :value="lang.id">
                    {{ lang.label }}
                  </option>
                </select>
              </span>
              <CodeEditor
                aria-labelledby="file-content-label"
                v-model="draft.content"
                :language="contentLanguage"
                :rows="isScript ? 22 : 16"
                :placeholder="
                  isScript
                    ? 'Paste the generator. Call produceArtifact({name, produceType: \'internal\'}) for your nodes and assign the result to $content.'
                    : isPlain
                      ? 'Anything you want served verbatim'
                      : 'Paste the Mihomo or Clash config you already run'
                "
              />
              <span v-if="isScript" class="field-optional">
                Runs in the engine's sandbox: no filesystem, and network only through
                <code>$substore.http</code>. Every request leaves through the server's guarded
                egress (private addresses refused, redirects re-checked), capped at 8 requests per
                call. It reaches <code>ProxyUtils</code>, <code>produceArtifact()</code>,
                <code>$arguments</code> and <code>$options</code>, and returns its document by
                assigning <code>$content</code>. Response headers go in
                <code>$options._res.headers</code>.
              </span>
              <span v-else-if="!isPlain" class="field-optional">
                Keep your own rules, DNS and groups. Only <code>proxies</code> is replaced, and any
                group left pointing at a node that is gone gets the new ones instead.
              </span>
            </div>
          </div>
          </PcPanelBody>
        </PcPanel>

        <PcPanel v-if="!isPlain" v-show="editorTab === 'content'" class="editor-group" role="group" label="Where its nodes come from">
          <PcPanelHeader title="Where its nodes come from" description="The subscription whose nodes fill the proxy list." />
          <PcPanelBody>
          <div class="form-grid">
            <label class="field field-wide">
              <span class="field-label">Node source</span>
              <select v-model="draft.nodeSource" class="select">
                <option value="">Leave the configuration exactly as written</option>
                <option v-if="danglingNodeSource" :value="danglingNodeSource">
                  {{ danglingNodeSource }} (no longer in the store)
                </option>
                <option v-for="item in nodeSources" :key="item.id" :value="item.id">
                  {{ item.display_name || item.name }}
                  {{ item.kind === KIND_COLLECTION ? "(combination)" : "" }}
                </option>
              </select>
              <span class="field-optional">
                <template v-if="danglingNodeSource">
                  The record this file draws from is not in the store any more, so serving it
                  fails. Point it at another source, or clear it to serve the text as written.
                </template>
                <template v-else-if="!nodeSources.length">
                  There is nothing to point at yet, create a subscription on the Subscriptions tab
                  first.
                </template>
                <template v-else-if="isScript">
                  This is what <code>produceArtifact()</code> hands back. Each node keeps the name of
                  the subscription it came from, so a script can filter or rename by source.
                </template>
                <template v-else>
                  Whatever this resolves to at request time becomes the file's proxy list.
                </template>
              </span>
            </label>
          </div>
          </PcPanelBody>
        </PcPanel>

        <PcPanel v-if="isScript" v-show="editorTab === 'content'" class="editor-group" role="group" label="What the script can read">
          <PcPanelHeader title="What the script can read" description="The settings handed to the script, and the request parameters it may read." />
          <PcPanelBody>
          <div class="form-grid">
            <label class="field field-wide">
              <span class="field-label">Settings <span class="field-optional">($arguments)</span></span>
              <textarea
                v-model="draft.argumentsText"
                class="code-area"
                rows="4"
                spellcheck="false"
                placeholder="enhanced-mode = fake-ip"
              ></textarea>
              <span class="field-optional">One <code>name = value</code> per line.</span>
            </label>

            <label class="field field-wide">
              <span class="field-label">URL parameters the script may read</span>
              <input
                v-model="queryParamText"
                type="text"
                autocomplete="off"
                spellcheck="false"
                placeholder="enhanced-mode"
              />
              <span class="field-optional">
                A share link is public, so anything in its query is input from whoever holds the
                link. Only the names listed here reach the script; everything else is dropped before
                it runs. Leave empty and the script sees no query at all.
              </span>
            </label>
          </div>
          </PcPanelBody>
        </PcPanel>

        <!-- A program does the whole job, including anything an operator chain
             would have done. Offering one as well would ask which runs first. -->
        <PcPanel v-if="!isScript" v-show="editorTab === 'operations'" class="editor-group" role="group" label="Operations">
          <PcPanelHeader title="Operations" description="Run in order over the nodes before they are placed into the document.">
            <PcCount v-if="chainCount" :value="chainCount" :label="`${chainCount} operation${chainCount === 1 ? '' : 's'} in the chain`" />
          </PcPanelHeader>
          <PcPanelBody>
          <ProcessChain
            :steps="(draft.process as ChainStep[])"
            :catalog="subs.operators.value"
            :catalog-state="subs.operatorsState.value"
            :chain="isPlain ? 'response' : 'nodes'"
            :heading="isPlain ? 'Document operations' : undefined"
            :empty-copy="isPlain ? 'No operations. The text is served exactly as written.' : undefined"
            @update:steps="draft.process = $event"
          />
          <p class="field-optional">
            <template v-if="isPlain">
              A script receives the document and returns what gets served. The node operators do
              not appear here. The engine skips them for responses.
            </template>
            <template v-else>
              Operations run over the nodes before they are placed into the configuration.
            </template>
          </p>
          </PcPanelBody>
        </PcPanel>

        <div class="editor-actions">
          <span v-if="subs.actionError.value" class="field-error" role="alert">{{ subs.actionError.value }}</span>
          <p v-if="draftError" class="field-error">{{ draftError }}</p>
          <button class="button button-secondary" type="button" @click="leaveEditor">Cancel</button>
          <button class="button button-primary" type="submit" :disabled="!canSave">
            <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
            Save
          </button>
        </div>
      </form>

      <PcPanel class="editor-side" role="complementary" label="What a client receives">
        <!-- The chassis header, written out: the rendered document below is
             labelled by this heading, and the component gives its h2 no id. -->
        <header class="pc-panel-header">
          <div><h2 id="file-editor-preview-label">What a client receives</h2></div>
          <div class="pc-panel-header-end">
          <button
            class="button button-secondary"
            type="button"
            :disabled="!canPreviewNow"
            :title="
              editingId
                ? draftError || draftPreview.reason || 'Render this file and show what a client would receive'
                : 'Save it once, then preview'
            "
            @click="subs.runPreview(draft)"
          >
            <LoaderCircle v-if="subs.previewing.value" :size="16" class="spin" aria-hidden="true" />
            <Eye v-else :size="16" aria-hidden="true" />
            {{ subs.preview.value?.document ? "Refresh" : "Preview" }}
          </button>
          </div>
        </header>
        <PcPanelBody>

        <template v-if="subs.preview.value?.document">
          <p class="preview-evidence-meta">
            {{ contentLanguageLabel }} · {{ subs.preview.value.document.length }} characters<span
              v-if="subs.preview.value.truncated"
            > · truncated</span>
          </p>
          <DocumentView
            class="output-area"
            :text="subs.preview.value.document"
            :language="contentLanguage"
            :aria-labelledby="'file-editor-preview-label'"
          />
        </template>
        <p v-else-if="subs.previewError.value" class="editor-side-note is-error" role="alert">
          {{ subs.previewError.value }}
        </p>
        <p v-else-if="editingId && !draftPreview.supported" class="editor-side-note">
          {{ draftPreview.reason }} It is on this file's row menu, and it shows the record as last
          saved rather than the edits here.
        </p>
        <p v-else-if="draftError" class="editor-side-note">{{ draftError }}</p>
        <p v-else class="editor-side-note">
          Nothing run yet. Preview renders this draft without saving it, so the document can be
          read before anyone else receives it.
        </p>
        </PcPanelBody>
      </PcPanel>
      </div>

      <!-- Leaving with unsaved changes. It lives inside the editor because that
           is the only screen it can be asked from. -->
      <LtConfirmDialog
        :open="discarding"
        title="Leave without saving? The changes you made to this file are not stored yet and will be lost."
        verb="Discard changes"
        :names="[draft.displayName || draft.name || (editingId ?? 'this file')]"
        @confirm="cancelEdit()"
        @cancel="discarding = false"
      />
    </section>

    <!-- ── list ─────────────────────────────────────────────────────────── -->
    <section v-else class="lens" aria-labelledby="files-title">
      <h2 id="files-title" class="pc-sr-only">Files</h2>

      <PcNotice v-if="subs.actionError.value" tone="danger">{{ subs.actionError.value }}</PcNotice>
      <PcNotice v-else-if="subs.notice.value" tone="success">{{ subs.notice.value }}</PcNotice>

      <PcPanel v-if="!host.init.value || subs.state.value === 'loading'" label="Loading files">
        <PcSkeleton :count="4" label="Loading the files" />
      </PcPanel>

      <template v-else-if="subs.loadError.value">
        <PcNotice tone="danger" title="The list could not be loaded">
          {{ subs.loadError.value }}
          <template #actions><PcButton compact @click="loadAll()">Try again</PcButton></template>
        </PcNotice>
        <PcPanel label="Files">
          <PcEmptyState kind="error" title="Nothing could be loaded">
            <p>This is not an empty store, it is an unanswered question.</p>
          </PcEmptyState>
        </PcPanel>
      </template>

      <!-- An empty store and a filter that matched nothing are different
           situations with different copy and different actions. -->
      <PcPanel v-else-if="storeEmpty" label="Files">
        <PcEmptyState title="No files yet">
          <template #icon><FileCode :size="26" aria-hidden="true" /></template>
          <p>
            Paste the Mihomo config you already run. Lattice keeps your rules and groups and replaces
            only the proxy list, from whichever subscription you point it at, so nodes can change
            without you editing anything.
          </p>
          <template #actions>
            <PcButton variant="primary" :disabled="!subs.canMutate.value" @click="startCreate()">
              <template #icon><FileCode :size="15" aria-hidden="true" /></template>
              Add a configuration
            </PcButton>
            <PcButton :disabled="!subs.canMutate.value" @click="startCreate(FILE_TYPE_PLAIN)">
              <template #icon><FileText :size="15" aria-hidden="true" /></template>
              New plain-text file
            </PcButton>
          </template>
        </PcEmptyState>
      </PcPanel>

      <template v-else>
        <!-- A write can succeed and its trailing reload still fail. The rows
             below are then the last good read. -->
        <PcNotice v-if="subs.staleError.value" tone="warning" title="Showing the last good read">
          The newest reload failed ({{ subs.staleError.value }}).
        </PcNotice>

        <PcPanel label="Files">
          <PcPanelHeader
            title="Files"
            description="A configuration you keep, with its nodes kept current. Publish one from the console under Networking to give it a URL."
          >
            <PcCount
              :value="`${files.length} file${files.length === 1 ? '' : 's'}`"
              :label="`${allFiles.length} files. The ${MAX_SUBSCRIPTION_RECORDS} record budget is shared with subscriptions.`"
            />
          </PcPanelHeader>

          <PcEmptyState v-if="!files.length" kind="no-match" title="No file matches that search">
            <p>Nothing here is called, tagged or described as <span class="pc-mono">{{ searchText.trim() }}</span>.</p>
            <template #actions>
              <PcButton :disabled="!filtersActive" @click="clearFilters()">Clear the search</PcButton>
            </template>
          </PcEmptyState>

          <!-- One row per file: what it is, where its template comes from, where
               its nodes come from, and whether anyone can fetch it. -->
          <PcTable v-else :min-width="960" label="Files">
            <template #head>
              <PcSelectCell
                header
                :checked="allVisibleSelected"
                :indeterminate="selectedCount > 0 && !allVisibleSelected"
                :label="`Select all ${files.length} shown files`"
                @change="toggleSelectAll()"
              />
              <PcTh name>File</PcTh>
              <PcTh>Source</PcTh>
              <PcTh>Nodes</PcTh>
              <PcTh numeric>Operations</PcTh>
              <PcTh>Published</PcTh>
              <PcTh actions>Actions</PcTh>
            </template>
            <tbody>
              <PcRow
                v-for="item in files"
                :key="item.id"
                :id="`rec-${item.id}`"
                :selected="selectedIds.has(item.id)"
                :class="{ 'is-pending': pendingIds.has(item.id) }"
              >
                <PcSelectCell
                  :checked="selectedIds.has(item.id)"
                  :label="`Select ${item.name}`"
                  @change="toggleSelected(item.id)"
                />

                <PcNameCell :name="item.display_name || item.name" :id="item.id" :title="nameTitle(item)">
                  <template #after>
                    <PcKindChip :label="kindLabel(item)" />
                    <PcTagList v-if="tagChips(item.tags, false).all.length" :tags="tagChips(item.tags, false).all" :max="2" />
                  </template>
                  <template #status>
                    <PcStatePill :tone="tone(publishedOf(item).tone)" :label="publishedOf(item).label" :title="publishedOf(item).title" />
                  </template>
                </PcNameCell>

                <!-- Where its template comes from, which is the one thing that
                     decides whether Refresh does anything. -->
                <PcTd label="Source">
                  <span class="cell-muted">{{ item.source === SOURCE_REMOTE ? "Fetched from a link" : "Stored here" }}</span>
                </PcTd>

                <!-- Where its proxy list comes from. A file pointing at a record
                     that is no longer in the store cannot serve at all, and used
                     to look exactly like one that can. -->
                <PcTd
                  label="Nodes"
                  :title="nodeSourceMissing(item)
                    ? `This file draws its proxy list from ${item.node_source}, which is not in the store. Serving it fails until the source is restored or the file points somewhere else.`
                    : item.node_source ? `Its proxy list comes from ${sourceName(item.node_source)}` : 'Served exactly as written; no proxy list is filled in.'"
                >
                  <PcStateDot v-if="nodeSourceMissing(item)" tone="error" :label="`source ${item.node_source} is gone`" />
                  <span v-else-if="item.node_source" class="cell-muted">from {{ sourceName(item.node_source) }}</span>
                  <span v-else class="cell-muted">as written</span>
                </PcTd>

                <PcTd
                  label="Operations"
                  numeric
                  mono
                  :title="item.step_count ? `${item.step_count} operation(s) run on its nodes before the document is served${item.disabled_step_count ? `, ${item.disabled_step_count} switched off` : ''}` : 'No operations run on its nodes.'"
                >
                  {{ item.step_count }}<span v-if="item.disabled_step_count" class="cell-muted"> ({{ item.disabled_step_count }} off)</span>
                </PcTd>

                <PcTd label="Published" stack="state">
                  <PcStatePill :tone="tone(publishedOf(item).tone)" :label="publishedOf(item).label" :title="publishedOf(item).title" />
                </PcTd>

                <PcActionsCell>
                  <PcButton
                    compact
                    :disabled="rowAction(item, 'edit').disabled"
                    :title="rowAction(item, 'edit').reason || rowAction(item, 'edit').title"
                    @click="runRowAction('edit', item, $event)"
                  >
                    Open
                  </PcButton>
                  <RecordMenu
                    :data-row-menu="item.id"
                    :name="item.name"
                    :actions="menuActionsFor(item)"
                    :open="openFileMenuId === item.id"
                    @toggle="toggleFileMenu(item.id)"
                    @run="(id, event) => runRowAction(id, item, event)"
                    @keydown="onRowMenuKeydown"
                  />
                </PcActionsCell>
              </PcRow>
            </tbody>
          </PcTable>
        </PcPanel>
      </template>

      <PcBatchBar :count="selectedCount" noun="selected" @clear="selectedIds = new Set()">
        <PcButton
          v-for="action in batchActions"
          :key="action.id"
          variant="danger"
          compact
          :disabled="action.disabled"
          :title="action.reason || undefined"
          @click="requestDelete(selectedVisible.map((file) => file.id))"
        >
          <template #icon><Trash2 :size="13" aria-hidden="true" /></template>
          {{ action.label }} {{ selectedCount }} file{{ selectedCount === 1 ? "" : "s" }}
        </PcButton>
      </PcBatchBar>

      <TargetSheet
        :open="!!targetSheet"
        :record="targetSheet"
        @close="closeTargetSheet()"
      />

      <!-- One side panel, as on the sibling tab, for the share guidance. The
           document is the sheet's. -->
      <PcSidePanel :open="!!drawer" :title="drawerTitle" size="record" :return-focus-to="drawerTrigger" @close="closeDrawer()">
        <template v-if="drawer?.mode === 'share'">
          <p class="row-popover-copy">
            Nothing here is reachable until a share is published for it. Shares live in the
            dashboard, under <strong>Networking → Subscription Shares</strong>.
          </p>
          <p class="row-popover-note">Already published? The Shares view shows its link.</p>
          <div v-if="shareOrigin && drawerItem" class="empty-actions">
            <PcButton variant="primary" @click="openShares(drawerItem.name)">
              <template #icon><SquareArrowOutUpRight :size="15" aria-hidden="true" /></template>
              Open Shares view
            </PcButton>
          </div>
          <p v-else class="row-popover-note">
            This frame cannot ask the console to navigate, open Networking → Subscription Shares
            yourself.
          </p>
        </template>
      </PcSidePanel>

      <LtConfirmDialog
        :open="!!deleteTargets"
        :title="(deleteTargets?.ids.length ?? 0) === 1
          ? 'Delete this file? Any share published for it keeps existing and starts returning nothing.'
          : `Delete ${deleteTargets?.ids.length ?? 0} files? Any shares published for them keep existing and start returning nothing.`"
        verb="Delete"
        :names="deleteTargets?.names ?? []"
        :busy="deleteBusy"
        @cancel="deleteTargets = null"
        @confirm="confirmDelete()"
      />
    </section>
  </template>
</template>
