<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  ChevronLeft,
  CircleAlert,
  CircleCheck,
  ClipboardPaste,
  Columns3,
  CopyPlus,
  Eye,
  Globe,
  Layers,
  Library,
  LoaderCircle,
  Plus,
  Rows3,
  Send,
  RefreshCw,
  Server,
  Share2,
  SquareArrowOutUpRight,
  Trash2,
} from "@lucide/vue";

import LtBadge from "../components/lt/LtBadge.vue";
import LtBatchBar from "../components/lt/LtBatchBar.vue";
import LtButton from "../components/lt/LtButton.vue";
import LtConfirmDialog from "../components/lt/LtConfirmDialog.vue";
import LtDrawer from "../components/lt/LtDrawer.vue";
import LtEmptyState from "../components/lt/LtEmptyState.vue";
import LtIconButton from "../components/lt/LtIconButton.vue";
import LtSkeleton from "../components/lt/LtSkeleton.vue";
import LtTable from "../components/lt/LtTable.vue";
import LtToolbar from "../components/lt/LtToolbar.vue";
import { useLtTable, type LtColumn } from "../components/lt/ltTable";

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
const columnsOpen = ref(false);

const isCollection = computed(() => draft.value.kind === KIND_COLLECTION);
const draftError = computed(() => (editing.value ? validateDraft(draft.value) : ""));
const canSave = computed(() => !draftError.value && !subs.saving.value);
const canPreviewNow = computed(
  () => subs.canPreview.value && !subs.previewing.value && !draftError.value,
);

// Files live in the same store but on their own tab. Offering their tags here
// would put a filter in front of the operator that selects nothing.
const onThisTab = computed(() =>
  subs.items.value.filter((i) => (i.kind || KIND_SUB) !== KIND_FILE),
);

const allTags = computed(() => {
  const seen = new Set<string>();
  for (const item of onThisTab.value) for (const tag of item.tags ?? []) seen.add(tag);
  return [...seen].sort();
});

/**
 * "Untagged" is its own filter rather than an absence of one.
 *
 * Once most records carry tags, the few that do not are exactly the ones an
 * operator is looking for — and no combination of the tag buttons can select
 * them.
 */
const UNTAGGED = "\u0000untagged";

function matchesFilter(item: SubscriptionListItem): boolean {
  if (!tagFilter.value) return true;
  if (tagFilter.value === UNTAGGED) return (item.tags ?? []).length === 0;
  return (item.tags ?? []).includes(tagFilter.value);
}

/** Offered only when there is something it would select. */
const hasUntagged = computed(() => onThisTab.value.some((i) => (i.tags ?? []).length === 0));

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
    detail: "URI list, base64, Clash YAML or sing-box JSON — the engine detects the format.",
    icon: ClipboardPaste,
  },
] as const;

function clearTransientListState(): void {
  // A pending confirm or an open drawer must not survive into the editor and
  // reappear when the operator comes back to the list.
  deleting.value = [];
  drawer.value = null;
  columnsOpen.value = false;
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
  editing.value = true;
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
  editing.value = true;
  if (draft.value.source === SOURCE_VPN_CORE_GRAPH) await loadGraphOptionsForDraft(false);
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

function cancelEdit(): void {
  editing.value = false;
  editingId.value = null;
  draft.value = emptyDraft();
  subs.preview.value = null;
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

/** Nothing on this tab at all — the moment to offer migration alongside
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
  migrateSummary.value =
    `Imported ${landed.length - combos} subscription(s) and ${combos} combination(s). ` +
    "Nothing is published yet — publish a share in Networking → Subscription Shares to make them reachable.";
  migrateUrl.value = "";
}

// ── row status ──────────────────────────────────────────────────────────────

/** "refreshed 3h ago", or "" when the record has never been fetched. */
function refreshedLabel(item: SubscriptionListItem): string {
  if (!item.last_fetch_at) return "";
  const relative = formatRelativeTime(item.last_fetch_at);
  return relative ? `refreshed ${relative}` : "";
}

/** The provider's quota line, compact; "" when there is nothing honest to say. */
function trafficOf(item: SubscriptionListItem): string {
  return formatTraffic(parseUserinfo(item.userinfo));
}

// ── table ───────────────────────────────────────────────────────────────────

/** Rows after tag, kind, and text filters; the table sorts on top of this. */
const filteredRows = computed(() =>
  onThisTab.value.filter((item) => {
    if (!matchesFilter(item)) return false;
    if (kindFilter.value && (item.kind || KIND_SUB) !== (kindFilter.value === "collection" ? KIND_COLLECTION : KIND_SUB)) {
      return false;
    }
    const query = searchText.value.trim().toLowerCase();
    if (!query) return true;
    return [item.name, item.display_name ?? "", item.id, item.remark ?? ""].some((field) =>
      field.toLowerCase().includes(query),
    );
  }),
);

/** Status ranks worst-first so "sort by status" surfaces failures. */
function statusRank(item: SubscriptionListItem): number {
  if (item.last_fetch_ok === false) return 0;
  if (!item.last_fetch_at) return 1;
  return 2;
}

const tableColumns: LtColumn<SubscriptionListItem>[] = [
  { id: "name", label: "Name", sort: (r) => (r.display_name || r.name).toLowerCase() },
  { id: "source", label: "Source", width: "150px" },
  { id: "target", label: "Target", width: "120px", optional: true },
  { id: "status", label: "Status", width: "170px", sort: (r) => `${statusRank(r)}:${r.last_fetch_at ?? ""}` },
  { id: "quota", label: "Quota", width: "150px", optional: true },
  { id: "actions", label: "", width: "150px", align: "right" },
];

const table = useLtTable<SubscriptionListItem>({
  rows: filteredRows,
  columns: tableColumns,
  rowKey: (r) => r.id,
  storageKey: "lt.subscriptions.table",
});

watch(filteredRows, () => table.pruneSelection());

function sourceTone(item: SubscriptionListItem): "neutral" | "accent" {
  return item.source === SOURCE_VPN_CORE || item.source === SOURCE_VPN_CORE_GRAPH ? "accent" : "neutral";
}

function statusOf(item: SubscriptionListItem): { tone: "ok" | "warn" | "danger" | "neutral"; label: string; title?: string } {
  if (item.last_fetch_ok === false) {
    return { tone: "danger", label: "Failed", title: item.last_error || "The last refresh failed" };
  }
  if (!item.last_fetch_at) return { tone: "neutral", label: "Never refreshed" };
  const relative = formatRelativeTime(item.last_fetch_at);
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

async function batchRefresh(): Promise<void> {
  const ids = [...table.selected.value];
  for (const id of ids) {
    // Serial on purpose: each refresh is a provider fetch, and the plugin
    // worker handles one invocation at a time anyway.
    await refreshRow(id);
  }
  table.clearSelection();
}

function requestDelete(ids: string[]): void {
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
    table.clearSelection();
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

function openDrawer(mode: "preview" | "publish" | "share", id: string): void {
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
 * from the frame URL — re-read here rather than trusted from a second source.
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
 * first paint that has not arrived — so loading in `onMounted` alone silently
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
    <section v-if="editing" class="configuration" aria-labelledby="editor-title">
      <nav class="lt-breadcrumb" aria-label="Breadcrumb">
        <button type="button" class="lt-breadcrumb-root" @click="cancelEdit">
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

      <form @submit.prevent="submit">
      <fieldset class="editor-group">
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
              Stored as <code>{{ editingId }}</code>. Renaming is safe — a published share keeps
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

      <fieldset class="editor-group">
        <legend>{{ isCollection ? "What it gathers" : "Where the nodes come from" }}</legend>
        <div class="form-grid">
        <!-- ── sub: where the nodes come from ─────────────────────────── -->
        <div v-if="!isCollection" class="field field-wide">
          <div class="source-grid">
            <button
              v-for="option in SOURCES"
              :key="option.id"
              type="button"
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
            theirs — useful when one share is meant for one person.
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

        <label v-if="!isCollection && draft.source === SOURCE_LOCAL" class="field field-wide">
          <span class="field-label">Nodes</span>
          <textarea
            v-model="draft.content"
            class="code-area"
            rows="12"
            spellcheck="false"
            placeholder="Paste node links, a base64 blob, Clash YAML, or sing-box JSON"
          ></textarea>
          <span class="field-optional">
            Mixed lists work. One node per line for link formats.
          </span>
        </label>

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

      <fieldset class="editor-group">
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

      <div class="editor-block">
        <CommonSettingsBlock :model-value="common" @update:model-value="onCommonChange" />
      </div>

      <div class="editor-block">
          <ProcessChain
            :steps="(draft.process as ChainStep[])"
            :catalog="subs.operators.value"
            :managed-types="MANAGED_TYPES"
            @update:steps="draft.process = $event"
          />
          <span v-if="isCollection" class="field-optional">
            Each member runs its own operations first; these run over everything merged.
          </span>
      </div>

        <!-- Sticky: on a form this long, a save button at the bottom is a
             button you have to go and look for. -->
        <div class="editor-actions">
          <span v-if="draftError" class="field-error">{{ draftError }}</span>
          <button
            class="button button-secondary"
            type="button"
            :disabled="!canPreviewNow"
            :title="draftError || 'Show the nodes this would produce'"
            @click="subs.runPreview(draft)"
          >
            <LoaderCircle v-if="subs.previewing.value" :size="16" class="spin" aria-hidden="true" />
            <Eye v-else :size="16" aria-hidden="true" />
            Preview
          </button>
          <button class="button button-secondary" type="button" @click="cancelEdit">Cancel</button>
          <button class="button button-primary" type="submit" :disabled="!canSave || !subs.canMutate.value">
            <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
            Save
          </button>
        </div>
      </form>

      <SubscriptionPreviewSummary v-if="subs.preview.value" :preview="subs.preview.value" />
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
          <span class="badge mono">{{ subs.items.value.length }} / {{ MAX_SUBSCRIPTION_RECORDS }}</span>
          <LtButton
            variant="primary"
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value"
            @click="startCreate(KIND_SUB)"
          >
            <Plus :size="14" aria-hidden="true" /> New subscription
          </LtButton>
          <LtButton
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value || !singles.length"
            :title="!singles.length ? 'Create a subscription first — there is nothing to combine' : ''"
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
            <button type="button" class="lt-chip" :class="{ 'is-active': kindFilter === '' }" @click="kindFilter = ''">All kinds</button>
            <button type="button" class="lt-chip" :class="{ 'is-active': kindFilter === 'sub' }" @click="kindFilter = 'sub'">
              <Library :size="12" aria-hidden="true" /> Subs ({{ singles.length }})
            </button>
            <button type="button" class="lt-chip" :class="{ 'is-active': kindFilter === 'collection' }" @click="kindFilter = 'collection'">
              <Layers :size="12" aria-hidden="true" /> Combinations ({{ collections.length }})
            </button>
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
          <template #controls>
            <div class="lt-columns">
              <LtIconButton label="Choose columns" @click="columnsOpen = !columnsOpen">
                <Columns3 :size="15" aria-hidden="true" />
              </LtIconButton>
              <div v-if="columnsOpen" class="lt-columns-menu" role="menu">
                <label v-for="column in tableColumns.filter((c) => c.optional)" :key="column.id" class="lt-columns-item">
                  <input
                    type="checkbox"
                    :checked="!table.hidden.value.has(column.id)"
                    @change="table.toggleColumn(column.id)"
                  />
                  {{ column.label }}
                </label>
              </div>
            </div>
            <LtIconButton
              :label="table.compact.value ? 'Comfortable rows' : 'Compact rows'"
              @click="table.setCompact(!table.compact.value)"
            >
              <Rows3 :size="15" aria-hidden="true" />
            </LtIconButton>
          </template>
        </LtToolbar>

        <LtEmptyState
          v-if="!filteredRows.length"
          kind="no-results"
          title="Nothing matches"
          detail="No record matches the current search and filters."
        >
          <LtButton @click="searchText = ''; tagFilter = ''; kindFilter = ''">Clear filters</LtButton>
        </LtEmptyState>

        <LtTable
          v-else
          :columns="table.visibleColumns.value"
          :rows="table.sortedRows.value"
          :row-key="(r: SubscriptionListItem) => r.id"
          :sort="table.sort.value"
          :compact="table.compact.value"
          selectable
          :selected="table.selected.value"
          :all-selected="table.allSelected.value"
          :pending="pendingIds"
          @sort="table.toggleSort"
          @toggle-row="table.toggleRow"
          @toggle-all="table.toggleAll"
          @row-click="(r: SubscriptionListItem) => subs.canMutate.value && startEdit(r.id)"
        >
          <template #cell-name="{ row }">
            <div class="cell-name">
              <span class="cell-name-title">
                <Layers v-if="(row.kind || KIND_SUB) === KIND_COLLECTION" :size="13" aria-hidden="true" />
                {{ row.display_name || row.name }}
                <LtBadge v-for="tag in row.tags ?? []" :key="tag" tone="neutral">{{ tag }}</LtBadge>
                <LtBadge v-if="row.imported" tone="neutral">migrated</LtBadge>
              </span>
              <span class="cell-name-sub mono">{{ row.id }}<template v-if="row.step_count"> · {{ row.step_count }} op(s)<template v-if="row.disabled_step_count">, {{ row.disabled_step_count }} off</template></template></span>
            </div>
          </template>
          <template #cell-source="{ row }">
            <LtBadge :tone="sourceTone(row)">{{ describe(row) }}</LtBadge>
          </template>
          <template #cell-target="{ row }">
            <span class="mono">{{ row.target || "Auto (UA)" }}</span>
          </template>
          <template #cell-status="{ row }">
            <LtBadge dot :tone="statusOf(row).tone" :title="statusOf(row).title">{{ statusOf(row).label }}</LtBadge>
          </template>
          <template #cell-quota="{ row }">
            <span v-if="trafficOf(row)" class="mono">{{ trafficOf(row) }}</span>
            <span v-else class="cell-dim">—</span>
          </template>
          <template #cell-actions="{ row }">
            <div class="cell-actions" @click.stop>
              <LtIconButton
                :label="`Preview ${row.name}`"
                :disabled="!subs.canPreview.value"
                @click="openDrawer('preview', row.id)"
              >
                <Eye :size="15" aria-hidden="true" />
              </LtIconButton>
              <LtIconButton
                :label="`Refresh ${row.name}`"
                :disabled="!subs.canFetch.value"
                @click="refreshRow(row.id)"
              >
                <RefreshCw :size="15" aria-hidden="true" />
              </LtIconButton>
              <LtIconButton
                :label="`Publish ${row.name}`"
                :disabled="!subs.canPublish.value || !subs.canMutate.value"
                @click="openDrawer('publish', row.id)"
              >
                <Send :size="15" aria-hidden="true" />
              </LtIconButton>
              <LtIconButton
                :label="`Share ${row.name}`"
                :disabled="!host.init.value"
                @click="openDrawer('share', row.id)"
              >
                <Share2 :size="15" aria-hidden="true" />
              </LtIconButton>
              <LtIconButton
                :label="`Duplicate ${row.name}`"
                :disabled="!subs.canMutate.value"
                @click="subs.duplicate(row.id)"
              >
                <CopyPlus :size="15" aria-hidden="true" />
              </LtIconButton>
              <LtIconButton
                :label="`Delete ${row.name}`"
                danger
                :disabled="!subs.canMutate.value"
                @click="requestDelete([row.id])"
              >
                <Trash2 :size="15" aria-hidden="true" />
              </LtIconButton>
            </div>
          </template>
        </LtTable>

        <div v-if="storeEmpty === false && ops.canMigrate.value && !subs.items.value.length" />
        <div v-if="ops.canMigrate.value && storeEmpty" class="empty-secondary">
          <span class="field-label">Already running a standalone Sub-Store?</span>
          <form class="empty-inline-form" @submit.prevent="runMigrate">
            <input v-model="migrateUrl" type="text" autocomplete="off" spellcheck="false" placeholder="Its base URL" />
            <button class="button button-secondary" type="submit" :disabled="ops.busy.value">
              <LoaderCircle v-if="ops.busy.value" :size="15" class="spin" aria-hidden="true" />
              Import from it
            </button>
          </form>
          <p class="row-popover-note">
            Importing publishes nothing — each subscription stays unserved until you share it.
          </p>
          <p v-if="ops.actionError.value" class="row-popover-error" role="alert">{{ ops.actionError.value }}</p>
        </div>

        <LtBatchBar :count="table.selected.value.size" @clear="table.clearSelection()">
          <LtButton size="sm" :disabled="!subs.canFetch.value" @click="batchRefresh()">
            <RefreshCw :size="13" aria-hidden="true" /> Refresh {{ table.selected.value.size }}
          </LtButton>
          <LtButton size="sm" variant="danger" :disabled="!subs.canMutate.value" @click="requestDelete([...table.selected.value])">
            <Trash2 :size="13" aria-hidden="true" /> Delete {{ table.selected.value.size }}
          </LtButton>
        </LtBatchBar>
      </template>

      <LtDrawer :open="!!drawer" :title="drawerTitle" @close="closeDrawer()">
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
            <ul class="row-popover-list">
              <li v-for="(node, index) in subs.rowPreview.value.nodes" :key="`${node.name}-${index}`">
                <span>{{ node.name }}</span>
                <LtBadge tone="neutral">{{ node.type }}</LtBadge>
                <LtBadge v-if="node.security" tone="neutral">{{ node.security }}</LtBadge>
                <span v-if="node.server" class="row-node-endpoint mono">{{ node.port ? `${node.server}:${node.port}` : node.server }}</span>
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
            This frame cannot ask the console to navigate — open Networking → Subscription Shares
            yourself.
          </p>
        </template>
      </LtDrawer>

      <LtConfirmDialog
        :open="deleting.length > 0"
        :title="deleting.length === 1
          ? 'Delete this record? Any combination including it stops rendering until edited, and a published share keeps existing.'
          : `Delete ${deleting.length} records? Combinations including them stop rendering until edited, and published shares keep existing.`"
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
/* ── S1 table chrome ─────────────────────────────────────────────────────── */
.lt-search {
  width: 100%;
  height: 30px;
  font: inherit;
  font-size: var(--lt-text-sm);
  padding: 0 var(--lt-space-3);
  border: 1px solid var(--lt-border);
  border-radius: var(--lt-radius-sm);
  background: var(--lt-bg);
  color: var(--lt-fg);
}
.lt-search:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
.lt-chip {
  display: inline-flex;
  align-items: center;
  gap: var(--lt-space-1);
  height: 24px;
  padding: 0 var(--lt-space-2);
  font: inherit;
  font-size: var(--lt-text-xs);
  border: 1px solid var(--lt-border);
  border-radius: 999px;
  background: var(--lt-surface);
  color: var(--lt-fg-muted);
  cursor: pointer;
}
.lt-chip:hover { color: var(--lt-fg); }
.lt-chip.is-active {
  background: color-mix(in oklab, var(--lt-accent) 10%, var(--lt-surface) 90%);
  border-color: var(--lt-accent);
  color: var(--lt-accent);
}
.lt-chip:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
.lt-chip-sep { width: 1px; height: 16px; background: var(--lt-border); margin: 0 var(--lt-space-1); }
.lt-columns { position: relative; }
.lt-columns-menu {
  position: absolute;
  right: 0;
  top: calc(100% + 4px);
  z-index: 40;
  background: var(--lt-surface);
  border: 1px solid var(--lt-border);
  border-radius: var(--lt-radius-sm);
  padding: var(--lt-space-2);
  display: flex;
  flex-direction: column;
  gap: var(--lt-space-1);
  box-shadow: 0 8px 24px color-mix(in oklab, var(--lt-fg) 14%, transparent);
}
.lt-columns-item {
  display: flex;
  align-items: center;
  gap: var(--lt-space-2);
  font-size: var(--lt-text-sm);
  white-space: nowrap;
  cursor: pointer;
}
.cell-name { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.cell-name-title {
  display: inline-flex;
  align-items: center;
  gap: var(--lt-space-1);
  font-weight: 500;
  color: var(--lt-fg);
}
.cell-name-sub { font-family: var(--lt-mono); font-size: var(--lt-text-xs); color: var(--lt-fg-muted); }
.cell-dim { color: var(--lt-fg-muted); }
.cell-actions { display: inline-flex; gap: 2px; justify-content: flex-end; }
.lt-breadcrumb {
  display: flex;
  align-items: center;
  gap: var(--lt-space-2);
  font-size: var(--lt-text-sm);
  margin-bottom: var(--lt-space-3);
}
.lt-breadcrumb-root {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  border: none;
  background: none;
  font: inherit;
  font-size: var(--lt-text-sm);
  color: var(--lt-accent);
  cursor: pointer;
  padding: 2px 4px;
  border-radius: var(--lt-radius-sm);
}
.lt-breadcrumb-root:hover { text-decoration: underline; }
.lt-breadcrumb-root:focus-visible { outline: none; box-shadow: var(--lt-focus-ring); }
.lt-breadcrumb-sep { color: var(--lt-fg-muted); }
.lt-breadcrumb-here { color: var(--lt-fg); font-weight: 500; }

/* ── editor grouping ─────────────────────────────────────────────────────
   The form was one undifferentiated column: name, source, output, settings
   and the operator chain all at the same level, so nothing told the reader
   where one decision ended and the next began. */

.editor-block {
  margin: 0 0 16px;
}

/* The sticky bar floats over the form, so the last block needs room to scroll
   out from under it rather than ending beneath it. */

/* The action bar follows the reader down. A save button that has to be
   scrolled to is a save button people lose. */

/* ── list density ────────────────────────────────────────────────────────
   Rows were 40px with 11px meta text: technically legible, and tiring to
   scan. */

.tag-row {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
  margin-bottom: 4px;
}

.tag-row button,
.choice-row button {
  padding: 4px 11px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: transparent;
  color: var(--muted-foreground, #656d76);
  font-size: 12px;
  cursor: pointer;
}

.tag-row button.is-active,
.choice-row button.is-active {
  border-color: var(--primary, #1769aa);
  background: color-mix(in srgb, var(--primary, #1769aa) 12%, transparent);
  color: var(--primary, #1769aa);
}

.choice-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
}

.choice-row button {
  border-color: var(--border, #d9dde2);
}

.group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 8px;
}

.group-toggle {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 4px 2px;
  border: 0;
  background: transparent;
  color: inherit;
  font-size: 14px;
  font-weight: 700;
  cursor: pointer;
}

.group-caret {
  transition: transform 0.15s ease;
  transform: rotate(-90deg);
}

.group-caret.is-open {
  transform: rotate(0deg);
}

</style>
