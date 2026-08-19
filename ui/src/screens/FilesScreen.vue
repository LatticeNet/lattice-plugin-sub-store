<script setup lang="ts">
import { computed, nextTick, onActivated, onBeforeUnmount, onMounted, ref, watch } from "vue";
import {
  ChevronDown,
  ChevronLeft,
  ChevronsRight,
  CircleAlert,
  RefreshCw,
  CircleCheck,
  ClipboardPaste,
  CopyPlus,
  Ellipsis,
  Eye,
  FileCode,
  FileText,
  Globe,
  Braces,
  LoaderCircle,
  Pencil,
  Plus,
  Share2,
  SquareArrowOutUpRight,
  Trash2,
} from "@lucide/vue";

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
import { hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import { collectTags, matchesQuery, matchesTag, normalizeQuery } from "../recordSearch";
import {
  draftFromRecord,
  emptyDraft,
  knownFileType,
  useSubscriptions,
  validateDraft,
  type SubscriptionDraft,
} from "../useSubscriptions";
import LtBadge from "../components/lt/LtBadge.vue";
import LtButton from "../components/lt/LtButton.vue";
import LtDrawer from "../components/lt/LtDrawer.vue";
import LtIconButton from "../components/lt/LtIconButton.vue";
import LtBatchBar from "../components/lt/LtBatchBar.vue";
import LtToolbar from "../components/lt/LtToolbar.vue";
import CodeEditor from "../components/CodeEditor.vue";
import EngineUnavailable from "../components/EngineUnavailable.vue";
import ProcessChain, { type ChainStep } from "../components/ProcessChain.vue";
import TargetSheet from "../components/TargetSheet.vue";
import LtConfirmDialog from "../components/lt/LtConfirmDialog.vue";
import LtSkeleton from "../components/lt/LtSkeleton.vue";
import LtEmptyState from "../components/lt/LtEmptyState.vue";
import { anchorTopFrom } from "../overlayAnchor";
import type { EditorLanguage } from "../codemirror";

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
  if (isScript.value) return "javascript";
  if (isPlain.value) return "plain";
  return "yaml";
});
const contentLanguage = computed<EditorLanguage>(
  () => contentLanguageOverride.value || autoLanguage.value,
);
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
    has_url: !!draft.value.url.trim(),
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

/** Overlay anchoring. Inert for the sheet, which the viewport frame centres on
 *  screen; the drawer still reads it (see overlayAnchor). */
const overlayAnchor = ref(32);
/** The preview/copy sheet. A file is exactly the thing you hand to a client. */
const targetSheet = ref<SubscriptionListItem | null>(null);
/** Selection for batch delete; the record limit is 256 and deleting one at a
 *  time was the only way out of a bad import. */
const selectedIds = ref<Set<string>>(new Set());
const deleteTargets = ref<{ ids: string[]; names: string[] } | null>(null);
const deleteBusy = ref(false);
/** Rows mid-operation render pending rather than silently unresponsive. */
const pendingIds = ref<Set<string>>(new Set());

const searchText = ref("");
const typeFilter = ref<"" | typeof FILE_TYPE_CONFIG | typeof FILE_TYPE_PLAIN | typeof FILE_TYPE_SCRIPT>("");
const tagFilter = ref("");

const allTags = computed(() => collectTags(allFiles.value));

/**
 * Files get the same search predicate as subscriptions.
 *
 * This screen offers tag chips but its own search never looked at tags, so
 * typing a tag name returned nothing while clicking the chip for the same tag
 * returned rows. One predicate, in recordSearch, for both screens.
 */
const files = computed(() => {
  const query = normalizeQuery(searchText.value);
  return allFiles.value.filter((file) => {
    if (typeFilter.value && knownFileType(file.file_type) !== typeFilter.value) return false;
    if (!matchesTag(file, tagFilter.value)) return false;
    return matchesQuery(file, query);
  });
});

/** The store holds no files at all, which is a different situation from a
 *  filter that matched nothing and needs different copy and different actions.
 *  They used to share one branch, so searching for a name that did not exist
 *  showed the first-run "paste your Mihomo config" panel. */
const storeEmpty = computed(() => allFiles.value.length === 0);

const filtersActive = computed(() => !!searchText.value.trim() || !!typeFilter.value || !!tagFilter.value);

function clearFilters(): void {
  searchText.value = "";
  typeFilter.value = "";
  tagFilter.value = "";
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
  overlayAnchor.value = anchorTopFrom(event);
  targetSheet.value = item;
}

function toggleSelected(id: string): void {
  const next = new Set(selectedIds.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  selectedIds.value = next;
}

function requestDelete(ids: string[], event?: Event): void {
  closeRowMenu();
  overlayAnchor.value = anchorTopFrom(event);
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

/** Grouped by what the file IS, which is how an operator looks for one. */
const fileGroups = computed(() => {
  const groups: { id: string; label: string; rows: SubscriptionListItem[] }[] = [
    { id: FILE_TYPE_CONFIG, label: "Client configurations", rows: [] },
    { id: FILE_TYPE_SCRIPT, label: "Built by a script", rows: [] },
    { id: FILE_TYPE_PLAIN, label: "Plain text", rows: [] },
  ];
  for (const file of files.value) {
    const group = groups.find((entry) => entry.id === knownFileType(file.file_type));
    (group ?? groups[0]!).rows.push(file);
  }
  return groups.filter((group) => group.rows.length > 0);
});

const collapsedGroups = ref<Set<string>>(new Set());
function toggleGroup(id: string): void {
  const next = new Set(collapsedGroups.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  collapsedGroups.value = next;
}

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
  document.querySelector<HTMLElement>(`[data-row-menu="${cssEscape(id)}"] .rec-menu button:not(:disabled)`)?.focus();
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

function onDocumentKeydown(event: KeyboardEvent): void {
  if (event.key === "Escape" && openFileMenuId.value) closeRowMenu();
}

// ── drawer ──────────────────────────────────────────────────────────────────

/**
 * One drawer carries every row-scoped panel, as on the sibling tab. These were
 * inline blocks that grew inside the row, so opening a 60 KB preview shoved the
 * rest of the list off the screen and the operator lost their place.
 */
const drawer = ref<{ mode: "preview" | "share"; id: string } | null>(null);

const drawerItem = computed(() =>
  drawer.value ? allFiles.value.find((file) => file.id === drawer.value?.id) : undefined,
);

const drawerTitle = computed(() => {
  if (!drawer.value || !drawerItem.value) return "";
  const name = drawerItem.value.display_name || drawerItem.value.name;
  return drawer.value.mode === "preview" ? `Document · ${name}` : `Share · ${name}`;
});

function openDrawer(mode: "preview" | "share", id: string, event?: Event): void {
  closeRowMenu();
  overlayAnchor.value = anchorTopFrom(event);
  drawer.value = { mode, id };
  if (mode === "preview" && subs.rowPreview.value?.id !== id) void subs.toggleRowPreview(id);
}

function closeDrawer(): void {
  if (drawer.value?.mode === "preview" && subs.rowPreview.value) {
    void subs.toggleRowPreview(subs.rowPreview.value.id);
  }
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

function describe(item: SubscriptionListItem): string {
  if (item.file_type === FILE_TYPE_PLAIN) return "Plain text";
  const kind = item.file_type === FILE_TYPE_SCRIPT ? "Built by a script" : "Client configuration";
  return item.node_source ? `${kind} · nodes from ${sourceName(item.node_source)}` : `${kind} · no node source`;
}

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
  editingId.value = null;
  editing.value = true;
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
  editingId.value = id;
  editing.value = true;
  await host.resize();
}

function cancelEdit(): void {
  editing.value = false;
  editingId.value = null;
  draft.value = emptyDraft();
  subs.preview.value = null;
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
  await subs.load();
  await subs.loadOperators();
}

// The screens are kept alive between tab switches, so a record created on the
// sibling tab would otherwise be missing here until a full reload, most
// visibly in the node-source picker, which lists subscriptions.
onActivated(() => {
  if (host.init.value) void loadAll();
});

onMounted(() => {
  if (host.init.value) void loadAll();
  document.addEventListener("click", onDocumentClick, true);
  document.addEventListener("keydown", onDocumentKeydown);
});

onBeforeUnmount(() => {
  document.removeEventListener("click", onDocumentClick, true);
  document.removeEventListener("keydown", onDocumentKeydown);
});

watch(host.init, (value) => {
  if (value) void loadAll();
});
</script>

<template>
  <EngineUnavailable v-if="host.init.value && !subs.available.value" feature="Files" />

  <template v-else>
    <!-- ── editor ───────────────────────────────────────────────────────── -->
    <section v-if="editing" class="configuration" aria-labelledby="file-editor-title">
      <!-- The sibling editor has one; without it this screen's only way back is
           the Cancel button at the far bottom of a long form. -->
      <nav class="lt-breadcrumb" aria-label="Breadcrumb">
        <button type="button" class="lt-breadcrumb-root" @click="cancelEdit">
          <ChevronLeft :size="14" aria-hidden="true" /> Files
        </button>
        <span class="lt-breadcrumb-sep" aria-hidden="true">/</span>
        <span class="lt-breadcrumb-here" aria-current="page">
          {{ editingId ? draft.displayName || draft.name || editingId : "New file" }}
        </span>
      </nav>
      <div class="section-heading">
        <div>
          <h2 id="file-editor-title">{{ editingId ? "Edit" : "New" }} file</h2>
          <p>
            A document served as it is, with its proxy list kept in step with a subscription.
          </p>
        </div>
      </div>

      <div v-if="subs.actionError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
      </div>

      <form @submit.prevent="submit">
        <fieldset class="editor-group">
          <legend>Basics</legend>
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
        </fieldset>

        <fieldset class="editor-group">
          <legend>What kind of file</legend>
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
        </fieldset>

        <fieldset class="editor-group">
          <legend>{{ isScript ? "The program" : isPlain ? "The text" : "The template" }}</legend>
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
              <label class="field field-wide">
                <span class="field-label">Link</span>
                <input
                  v-model="draft.url"
                  type="text"
                  autocomplete="off"
                  spellcheck="false"
                  placeholder="Where the template is fetched from"
                />
              </label>
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
        </fieldset>

        <fieldset v-if="!isPlain" class="editor-group">
          <legend>Where its nodes come from</legend>
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
        </fieldset>

        <fieldset v-if="isScript" class="editor-group">
          <legend>What the script can read</legend>
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
        </fieldset>

        <!-- A program does the whole job, including anything an operator chain
             would have done. Offering one as well would ask which runs first. -->
        <fieldset v-if="!isScript" class="editor-group">
          <legend>Operations</legend>
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
        </fieldset>

        <!-- A disabled control with the reason only in its title is a control
             nobody on a touch device or a screen reader can find out about. -->
        <p v-if="editingId && !draftPreview.supported" class="field-optional preview-blocked">
          {{ draftPreview.reason }} It is on this file's row menu, and it shows the record as last
          saved rather than the edits above.
        </p>

        <div class="editor-actions">
          <span v-if="subs.actionError.value" class="field-error" role="alert">{{ subs.actionError.value }}</span>
          <p v-if="draftError" class="field-error">{{ draftError }}</p>
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
            Preview
          </button>
          <button class="button button-secondary" type="button" @click="cancelEdit">Cancel</button>
          <button class="button button-primary" type="submit" :disabled="!canSave">
            <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
            Save
          </button>
        </div>
      </form>

      <div v-if="subs.preview.value?.document" class="preview-summary">
        <p class="mono">
          What a client receives<span v-if="subs.preview.value.truncated">, truncated</span>
        </p>
        <pre class="output-area mono" tabindex="0">{{ subs.preview.value.document }}</pre>
      </div>
    </section>

    <!-- ── list ─────────────────────────────────────────────────────────── -->
    <section v-else class="configuration" aria-labelledby="files-title">
      <div class="section-heading">
        <div>
          <h2 id="files-title">Files</h2>
          <p>
            A configuration you keep, with its nodes kept current. Publish one from the dashboard
            under Networking to give it a URL.
          </p>
        </div>
        <div class="heading-actions">
          <span class="badge mono" :title="`${allFiles.length} of ${MAX_SUBSCRIPTION_RECORDS} records stored`">
            {{ allFiles.length }} / {{ MAX_SUBSCRIPTION_RECORDS }}
          </span>
          <LtButton
            variant="primary"
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value"
            :title="subs.atRecordLimit.value
              ? `The store holds ${MAX_SUBSCRIPTION_RECORDS} records; delete one to add another`
              : !subs.canMutate.value ? 'This bundle does not declare the save and delete methods' : ''"
            @click="startCreate()"
          >
            <Plus :size="14" aria-hidden="true" /> New file
          </LtButton>
        </div>
      </div>

      <div v-if="subs.actionError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
      </div>
      <div v-else-if="subs.notice.value" class="alert alert-ok" role="status">
        <CircleCheck :size="16" aria-hidden="true" /> {{ subs.notice.value }}
      </div>

      <LtSkeleton v-if="!host.init.value || subs.state.value === 'loading'" :rows="4" :columns="4" />

      <LtEmptyState
        v-else-if="subs.loadError.value"
        kind="error"
        title="The list could not be loaded"
        :detail="subs.loadError.value"
      >
        <LtButton variant="primary" @click="loadAll()">Retry</LtButton>
      </LtEmptyState>

      <!-- An empty store and a filter that matched nothing are different
           situations. They shared one branch, so searching for a name that did
           not exist answered with the first-run panel and its "add a
           configuration" button, and the "Nothing matches" state below could
           never render at all. -->
      <LtEmptyState
        v-else-if="storeEmpty"
        title="No files yet"
        detail="Paste the Mihomo config you already run. Lattice keeps your rules and groups and replaces only the proxy list, from whichever subscription you point it at, so nodes can change without you editing anything."
      >
        <LtButton variant="primary" :disabled="!subs.canMutate.value" @click="startCreate()">
          <FileCode :size="14" aria-hidden="true" /> Add a configuration
        </LtButton>
        <LtButton :disabled="!subs.canMutate.value" @click="startCreate(FILE_TYPE_PLAIN)">
          <FileText :size="14" aria-hidden="true" /> New plain-text file
        </LtButton>
      </LtEmptyState>

      <template v-else>
        <LtToolbar>
          <template #search>
            <input
              v-model="searchText"
              class="lt-search"
              type="search"
              placeholder="Filter by name, id, remark, tag"
              aria-label="Filter files"
            />
          </template>
          <template #filters>
            <button type="button" class="lt-chip" :class="{ 'is-active': typeFilter === '' }" :aria-pressed="typeFilter === ''" @click="typeFilter = ''">All kinds</button>
            <button type="button" class="lt-chip" :class="{ 'is-active': typeFilter === FILE_TYPE_CONFIG }" :aria-pressed="typeFilter === FILE_TYPE_CONFIG" @click="typeFilter = FILE_TYPE_CONFIG">
              <FileCode :size="12" aria-hidden="true" /> Configurations
            </button>
            <button type="button" class="lt-chip" :class="{ 'is-active': typeFilter === FILE_TYPE_SCRIPT }" :aria-pressed="typeFilter === FILE_TYPE_SCRIPT" @click="typeFilter = FILE_TYPE_SCRIPT">
              <Braces :size="12" aria-hidden="true" /> Scripts
            </button>
            <button type="button" class="lt-chip" :class="{ 'is-active': typeFilter === FILE_TYPE_PLAIN }" :aria-pressed="typeFilter === FILE_TYPE_PLAIN" @click="typeFilter = FILE_TYPE_PLAIN">
              <FileText :size="12" aria-hidden="true" /> Plain text
            </button>
            <template v-if="allTags.length">
              <span class="lt-chip-sep" aria-hidden="true" />
              <button type="button" class="lt-chip" :class="{ 'is-active': tagFilter === '' }" :aria-pressed="tagFilter === ''" @click="tagFilter = ''">All tags</button>
              <button
                v-for="tag in allTags"
                :key="tag"
                type="button"
                class="lt-chip"
                :class="{ 'is-active': tagFilter === tag }"
                :aria-pressed="tagFilter === tag"
                @click="tagFilter = tag"
              >{{ tag }}</button>
            </template>
          </template>
        </LtToolbar>

        <LtBatchBar :count="selectedCount" @clear="selectedIds = new Set()">
          <button
            class="button button-danger button-compact"
            type="button"
            :disabled="!subs.canMutate.value"
            @click="requestDelete(selectedVisible.map((file) => file.id), $event)"
          >
            <Trash2 :size="14" aria-hidden="true" />
            Delete {{ selectedCount }} file{{ selectedCount === 1 ? "" : "s" }}
          </button>
        </LtBatchBar>

        <!-- A write can succeed and its trailing reload still fail. The rows
             below are then the last good read. This strip used to REPLACE the
             list, so the one message saying "showing the last good read" was
             the only thing left on screen and there was nothing to show. -->
        <p v-if="subs.staleError.value" class="stale-strip" role="status">
          Showing the last good read. The newest reload failed ({{ subs.staleError.value }}).
        </p>

        <LtEmptyState
          v-if="!files.length"
          kind="no-results"
          title="Nothing matches"
          detail="No file matches the current search and filters."
        >
          <LtButton :disabled="!filtersActive" @click="clearFilters()">Clear filters</LtButton>
        </LtEmptyState>

        <template v-else>
        <div class="rec-head" aria-hidden="true">
          <label class="rec-select" :title="`Select all ${files.length} shown files`">
            <input
              type="checkbox"
              :checked="allVisibleSelected"
              :indeterminate.prop="selectedCount > 0 && !allVisibleSelected"
              :aria-label="`Select all ${files.length} shown files`"
              @change="toggleSelectAll()"
            />
          </label>
          <span />
          <span>File</span>
          <span class="rec-head-status">Source</span>
          <span class="rec-head-spacer" />
        </div>

        <section v-for="group in fileGroups" :key="group.id" class="rec-group">
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
            <span>{{ group.label }}</span>
            <span class="rec-group-count">{{ group.rows.length }}</span>
          </button>

          <ul v-if="!collapsedGroups.has(group.id)" class="rec-list">
            <li
              v-for="item in group.rows"
              :key="item.id"
              class="rec"
              :class="{ 'is-pending': pendingIds.has(item.id), 'is-selected': selectedIds.has(item.id) }"
            >
              <label class="rec-select" :title="`Select ${item.name}`" @click.stop>
                <input
                  type="checkbox"
                  :checked="selectedIds.has(item.id)"
                  :aria-label="`Select ${item.name}`"
                  @change="toggleSelected(item.id)"
                />
              </label>

              <span class="rec-icon" aria-hidden="true">
                <FileText v-if="item.file_type === FILE_TYPE_PLAIN" :size="17" />
                <Braces v-else-if="item.file_type === FILE_TYPE_SCRIPT" :size="17" />
                <FileCode v-else :size="17" />
              </span>

              <div class="rec-body">
                <!-- The name opens the client sheet, as on the sibling screen:
                     the daily job is "give me this for my client". -->
                <button
                  type="button"
                  class="rec-name"
                  :title="`Preview or copy ${item.display_name || item.name} for a client`"
                  @click="openFileSheet(item, $event)"
                >
                  {{ item.display_name || item.name }}
                </button>
                <span class="rec-tags">
                  <LtBadge v-for="tag in item.tags ?? []" :key="tag" tone="neutral">{{ tag }}</LtBadge>
                </span>
                <p class="rec-summary" :title="describe(item)">
                  {{ describe(item) }}
                  <template v-if="item.step_count">
                    · {{ item.step_count }} operation(s)<template v-if="item.disabled_step_count">, {{ item.disabled_step_count }} off</template>
                  </template>
                </p>
                <p class="rec-meta mono" :title="item.id">{{ item.id }}</p>
              </div>

              <!-- Same column the sibling tab puts refresh state in. A file's
                   equivalent fact is where its template comes from, which is
                   the one thing that decides whether Refresh does anything. -->
              <div class="rec-status-cell">
                <span class="rec-status">{{ item.source === SOURCE_REMOTE ? "Fetched from a link" : "Stored here" }}</span>
                <span
                  v-if="item.node_source"
                  class="rec-quota"
                  :class="{ 'is-danger': nodeSourceMissing(item) }"
                  :title="nodeSourceMissing(item)
                    ? `This file draws its proxy list from ${item.node_source}, which is not in the store. Serving it fails until the source is restored or the file points somewhere else.`
                    : `Its proxy list comes from ${sourceName(item.node_source)}`"
                >
                  <template v-if="nodeSourceMissing(item)">node source {{ item.node_source }} is gone</template>
                  <template v-else>nodes from {{ sourceName(item.node_source) }}</template>
                </span>
              </div>

              <div class="rec-actions" @click.stop>
                <LtIconButton
                  v-if="item.source === SOURCE_REMOTE"
                  :label="`Refresh ${item.name} from its template URL`"
                  :disabled="!subs.canFetch.value || pendingIds.has(item.id)"
                  @click="refreshRow(item.id)"
                >
                  <RefreshCw :size="15" :class="pendingIds.has(item.id) ? 'spin' : ''" aria-hidden="true" />
                </LtIconButton>
                <LtIconButton
                  :label="`Edit ${item.name}`"
                  :disabled="!subs.canMutate.value"
                  @click="startEdit(item.id)"
                >
                  <Pencil :size="15" aria-hidden="true" />
                </LtIconButton>
                <LtIconButton
                  :label="`Preview or copy ${item.name} for a client`"
                  @click="openFileSheet(item, $event)"
                >
                  <ChevronsRight :size="15" aria-hidden="true" />
                </LtIconButton>
                <div class="rec-menu-wrap" :data-row-menu="item.id">
                  <LtIconButton
                    :label="`More actions for ${item.name}`"
                    :aria-haspopup="true"
                    :aria-expanded="openFileMenuId === item.id"
                    @click="toggleFileMenu(item.id)"
                  >
                    <Ellipsis :size="15" aria-hidden="true" />
                  </LtIconButton>
                  <div v-if="openFileMenuId === item.id" class="rec-menu" role="menu" @keydown="onRowMenuKeydown">
                    <button
                      type="button"
                      role="menuitem"
                      :disabled="!subs.canPreview.value && !subs.canRender.value"
                      @click="openDrawer('preview', item.id, $event)"
                    >
                      <Eye :size="14" aria-hidden="true" /> Show document
                    </button>
                    <button
                      type="button"
                      role="menuitem"
                      :disabled="!host.init.value"
                      @click="openDrawer('share', item.id, $event)"
                    >
                      <Share2 :size="14" aria-hidden="true" /> Share…
                    </button>
                    <button
                      type="button"
                      role="menuitem"
                      :disabled="!subs.canMutate.value"
                      @click="closeRowMenu(); subs.duplicate(item.id)"
                    >
                      <CopyPlus :size="14" aria-hidden="true" /> Duplicate
                    </button>
                    <span class="rec-menu-sep" role="separator" />
                    <button
                      type="button"
                      role="menuitem"
                      class="is-danger"
                      :disabled="!subs.canMutate.value"
                      @click="requestDelete([item.id], $event)"
                    >
                      <Trash2 :size="14" aria-hidden="true" /> Delete
                    </button>
                  </div>
                </div>
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
        @close="targetSheet = null"
      />

      <!-- One drawer, as on the sibling tab. These were inline blocks that grew
           inside the row, so opening a 60 KB preview pushed the rest of the
           list off the screen. -->
      <LtDrawer :open="!!drawer" :title="drawerTitle" :anchor-top="overlayAnchor" @close="closeDrawer()">
        <template v-if="drawer?.mode === 'preview'">
          <p v-if="subs.rowPreview.value?.loading" class="row-popover-note">
            <LoaderCircle :size="13" class="spin" aria-hidden="true" /> Rendering…
          </p>
          <p v-else-if="subs.rowPreview.value?.error" class="row-popover-error" role="alert">
            {{ subs.rowPreview.value.error }}
          </p>
          <template v-else-if="subs.rowPreview.value">
            <p class="row-popover-note">
              What a client receives<span v-if="subs.rowPreview.value.truncated"> · truncated</span>
            </p>
            <pre class="row-popover-document mono" tabindex="0">{{ subs.rowPreview.value.document }}</pre>
          </template>
        </template>

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
        :open="!!deleteTargets"
        :anchor-top="overlayAnchor"
        :title="(deleteTargets?.ids.length ?? 0) === 1
          ? 'Delete this file? A published share for it keeps existing and starts returning nothing.'
          : `Delete ${deleteTargets?.ids.length ?? 0} files? Published shares for them keep existing and start returning nothing.`"
        verb="Delete"
        :names="deleteTargets?.names ?? []"
        :busy="deleteBusy"
        @cancel="deleteTargets = null"
        @confirm="confirmDelete()"
      />
    </section>
  </template>
</template>
