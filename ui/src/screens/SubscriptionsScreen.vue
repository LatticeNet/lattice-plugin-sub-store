<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  ChevronDown,
  CircleAlert,
  CircleCheck,
  ClipboardPaste,
  CopyPlus,
  Eye,
  Globe,
  Layers,
  Link2,
  LoaderCircle,
  Pencil,
  Plus,
  RefreshCw,
  Server,
  Share2,
  SquareArrowOutUpRight,
  Trash2,
} from "@lucide/vue";

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
  type SubscriptionListItem,
} from "../client";
import { useHost } from "../host";
import { hostOriginFromHash, postNavigate, sharesRoute } from "../navigate";
import { formatRelativeTime, formatTraffic, parseUserinfo } from "../rowStatus";
import {
  draftFromRecord,
  emptyDraft,
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
const confirmingDelete = ref<string | null>(null);
const tagText = ref("");
const memberTagText = ref("");
const tagFilter = ref("");
const showSubs = ref(true);
const showCollections = ref(true);
const sharingId = ref<string | null>(null);
const migrateUrl = ref("");
const migrateSummary = ref("");

const isCollection = computed(() => draft.value.kind === KIND_COLLECTION);
const draftError = computed(() => (editing.value ? validateDraft(draft.value) : ""));
const canSave = computed(() => !draftError.value && !subs.saving.value);
const canPreviewNow = computed(
  () => subs.canPreview.value && !subs.previewing.value && !draftError.value,
);

/** "kept 41 of 52 nodes" when a filter ran, "52 nodes" when nothing was dropped. */
const previewHeadline = computed(() => {
  const preview = subs.preview.value;
  if (!preview) return "";
  const kept = preview.node_count;
  const source = preview.source_node_count ?? kept;
  return source > kept ? `kept ${kept} of ${source} nodes` : `${kept} node(s)`;
});

/** Protocol breakdown of the previewed set, most common first. */
const previewTypeCounts = computed(() => {
  const counts = new Map<string, number>();
  for (const node of subs.preview.value?.nodes ?? []) {
    const type = node.type || "unknown";
    counts.set(type, (counts.get(type) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([type, count]) => ({ type, count }))
    .sort((a, b) => b.count - a.count || a.type.localeCompare(b.type));
});

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

function startCreate(kind: string): void {
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
  await host.resize();
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

async function confirmDelete(id: string): Promise<void> {
  const ok = await subs.remove(id);
  if (ok) confirmingDelete.value = null;
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

// ── sharing ─────────────────────────────────────────────────────────────────

/**
 * Shares are published by the dashboard, not by this frame: the frame can only
 * ask the console to navigate there. The origin is the one the bridge pinned
 * from the frame URL — re-read here rather than trusted from a second source.
 */
const shareOrigin = computed(() => hostOriginFromHash(window.location.hash));

function toggleShare(id: string): void {
  sharingId.value = sharingId.value === id ? null : id;
}

function openShares(recordName: string): void {
  if (!shareOrigin.value) return;
  postNavigate(window, sharesRoute(recordName), shareOrigin.value);
  sharingId.value = null;
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
</script>

<template>
  <EngineUnavailable v-if="host.init.value && !subs.available.value" feature="Subscriptions" />

  <template v-else>
    <!-- ── editor ───────────────────────────────────────────────────────── -->
    <section v-if="editing" class="configuration" aria-labelledby="editor-title">
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
              @click="draft.source = option.id"
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
          <button class="button button-primary" type="submit" :disabled="!canSave">
            <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
            Save
          </button>
        </div>
      </form>

      <div v-if="subs.preview.value" class="preview-summary">
        <p class="mono">
          {{ previewHeadline }}<span v-if="subs.preview.value.truncated"> — truncated</span>
        </p>
        <p v-if="previewTypeCounts.length" class="preview-type-chips">
          <span v-for="entry in previewTypeCounts" :key="entry.type" class="badge">
            {{ entry.type }} × {{ entry.count }}
          </span>
        </p>
        <ul class="sub-list">
          <li
            v-for="(node, index) in subs.preview.value.nodes"
            :key="`${node.name}-${index}`"
            class="sub-card sub-card-column"
          >
            <span class="sub-title">
              {{ node.name }}
              <span class="badge">{{ node.type }}</span>
              <span v-if="node.network" class="badge">{{ node.network }}</span>
              <span v-if="node.security" class="badge">{{ node.security }}</span>
              <span v-if="node.udp" class="badge" title="UDP relay">UDP</span>
              <span v-if="node.tfo" class="badge" title="TCP Fast Open">TFO</span>
              <span v-if="node.skip_cert_verify" class="badge" title="Skips TLS certificate verification">skip-cert</span>
              <span v-if="node.aead" class="badge" title="VMess AEAD">AEAD</span>
            </span>
            <span v-if="node.server" class="sub-meta mono">{{ node.port ? `${node.server}:${node.port}` : node.server }}</span>
          </li>
        </ul>
      </div>
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

      <div v-if="allTags.length || hasUntagged" class="tag-row">
        <button type="button" :class="{ 'is-active': tagFilter === '' }" @click="tagFilter = ''">
          All
        </button>
        <button
          v-for="tag in allTags"
          :key="tag"
          type="button"
          :class="{ 'is-active': tagFilter === tag }"
          @click="tagFilter = tag"
        >
          {{ tag }}
        </button>
        <button
          v-if="hasUntagged"
          type="button"
          :class="{ 'is-active': tagFilter === UNTAGGED }"
          @click="tagFilter = UNTAGGED"
        >
          Untagged
        </button>
      </div>

      <p v-if="!host.init.value || subs.state.value === 'loading'" class="skeleton-row">Loading…</p>
      <div v-else-if="subs.loadError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.loadError.value }}
      </div>

      <template v-else>
        <!-- Single subscriptions -->
        <div class="group-head">
          <button type="button" class="group-toggle" @click="showSubs = !showSubs">
            <ChevronDown :size="15" :class="['group-caret', { 'is-open': showSubs }]" />
            Subscriptions ({{ singles.length }})
          </button>
          <button
            class="button button-primary button-compact"
            type="button"
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value"
            @click="startCreate(KIND_SUB)"
          >
            <Plus :size="15" aria-hidden="true" /> New
          </button>
        </div>

        <!-- Truly empty is a different moment from "the filter hides
             everything": the first deserves guidance, the second an answer. -->
        <div v-if="showSubs && !singles.length && !storeEmpty" class="panel-empty">
          <p class="panel-empty-copy">No subscriptions carry this tag.</p>
        </div>

        <div v-else-if="showSubs && !singles.length" class="panel-empty panel-empty-stack">
          <p class="panel-empty-copy">
            Start with your own fleet: one subscription reading this deployment's vpn-core nodes.
          </p>
          <div class="empty-actions">
            <button
              class="button button-primary"
              type="button"
              :disabled="!subs.canMutate.value"
              @click="startCreate(KIND_SUB)"
            >
              <Server :size="16" aria-hidden="true" /> Add this fleet's nodes
            </button>
          </div>

          <div v-if="ops.canMigrate.value" class="empty-secondary">
            <span class="field-label">Already running a standalone Sub-Store?</span>
            <form class="empty-inline-form" @submit.prevent="runMigrate">
              <input
                v-model="migrateUrl"
                type="text"
                autocomplete="off"
                spellcheck="false"
                placeholder="Its base URL"
              />
              <button class="button button-secondary" type="submit" :disabled="ops.busy.value">
                <LoaderCircle v-if="ops.busy.value" :size="15" class="spin" aria-hidden="true" />
                Import from it
              </button>
            </form>
            <p class="row-popover-note">
              Importing publishes nothing — each subscription stays unserved until you share it.
            </p>
            <p v-if="ops.actionError.value" class="row-popover-error" role="alert">
              {{ ops.actionError.value }}
            </p>
          </div>
        </div>

        <ul v-else-if="showSubs" class="sub-list">
          <li v-for="item in singles" :key="item.id" class="sub-card sub-card-column">
            <div class="sub-card-row">
              <div class="sub-card-main">
                <span class="sub-title">
                  {{ item.display_name || item.name }}
                  <span v-for="tag in item.tags ?? []" :key="tag" class="badge">{{ tag }}</span>
                </span>
                <span class="sub-meta">
                  {{ describe(item) }}
                  <template v-if="item.target"> · {{ item.target }}</template>
                  <template v-if="item.step_count">
                    · {{ item.step_count }} operation(s)<template v-if="item.disabled_step_count">
                      , {{ item.disabled_step_count }} off</template>
                  </template>
                  <template v-if="item.last_fetch_at">
                    ·
                    <span
                      v-if="item.last_fetch_ok === false"
                      class="badge"
                      data-tone="danger"
                      :title="item.last_error || 'The last refresh failed'"
                    >refresh failed</span>
                    <template v-else>{{ refreshedLabel(item) }}</template>
                    <template v-if="trafficOf(item)"> · {{ trafficOf(item) }}</template>
                  </template>
                </span>
              </div>
              <div class="sub-actions">
                <span v-if="item.imported" class="badge">migrated</span>
                <button
                  class="icon-button"
                  type="button"
                  :disabled="!subs.canFetch.value || subs.busyId.value === item.id"
                  :aria-label="`Refresh ${item.name}`"
                  @click="subs.refresh(item.id)"
                >
                  <LoaderCircle v-if="subs.busyId.value === item.id" :size="16" class="spin" aria-hidden="true" />
                  <RefreshCw v-else :size="16" aria-hidden="true" />
                </button>
                <button
                  class="icon-button"
                  type="button"
                  :disabled="!subs.canPreview.value"
                  :aria-label="`Preview ${item.name}`"
                  :aria-expanded="subs.rowPreview.value?.id === item.id"
                  @click="subs.toggleRowPreview(item.id)"
                >
                  <Eye :size="16" aria-hidden="true" />
                </button>
                <button
                  class="icon-button"
                  type="button"
                  :disabled="!host.init.value"
                  :title="
                    host.init.value
                      ? `Share ${item.name}`
                      : 'Shares are published from the Lattice console — this frame is running standalone'
                  "
                  :aria-label="`Share ${item.name}`"
                  :aria-expanded="sharingId === item.id"
                  @click="toggleShare(item.id)"
                >
                  <Share2 :size="16" aria-hidden="true" />
                </button>
                <button
                  class="icon-button"
                  type="button"
                  :disabled="!subs.canMutate.value"
                  title="Make an independent copy of this record"
                  :aria-label="`Duplicate ${item.name}`"
                  @click="subs.duplicate(item.id)"
                >
                  <CopyPlus :size="16" aria-hidden="true" />
                </button>
                <button
                  class="icon-button"
                  type="button"
                  :disabled="!subs.canMutate.value || subs.saving.value"
                  :aria-label="`Edit ${item.name}`"
                  @click="startEdit(item.id)"
                >
                  <Pencil :size="16" aria-hidden="true" />
                </button>
                <button
                  class="icon-button destructive"
                  type="button"
                  :disabled="!subs.canMutate.value"
                  :aria-label="`Delete ${item.name}`"
                  @click="confirmingDelete = confirmingDelete === item.id ? null : item.id"
                >
                  <Trash2 :size="16" aria-hidden="true" />
                </button>
              </div>
            </div>

            <div v-if="subs.rowPreview.value?.id === item.id" class="row-popover">
              <p v-if="subs.rowPreview.value.loading" class="row-popover-note">
                <LoaderCircle :size="13" class="spin" aria-hidden="true" /> Loading…
              </p>
              <p v-else-if="subs.rowPreview.value.error" class="row-popover-error" role="alert">
                {{ subs.rowPreview.value.error }}
              </p>
              <template v-else>
                <p class="row-popover-note">
                  {{ subs.rowPreview.value.count }} node(s) once its operations run
                </p>
                <ul class="row-popover-list">
                  <li v-for="(node, index) in subs.rowPreview.value.nodes" :key="`${node.name}-${index}`">
                    <span>{{ node.name }}</span>
                    <span class="badge">{{ node.type }}</span>
                    <span v-if="node.security" class="badge">{{ node.security }}</span>
                    <span v-if="node.server" class="row-node-endpoint mono">{{ node.port ? `${node.server}:${node.port}` : node.server }}</span>
                  </li>
                </ul>
                <p
                  v-if="subs.rowPreview.value.count > subs.rowPreview.value.nodes.length"
                  class="row-popover-note"
                >
                  …and {{ subs.rowPreview.value.count - subs.rowPreview.value.nodes.length }} more
                </p>
              </template>
            </div>

            <div v-if="sharingId === item.id" class="row-popover">
              <p class="row-popover-copy">
                Nothing here is reachable until a share is published for it. Shares live in the
                dashboard, under <strong>Networking → Subscription Shares</strong>.
              </p>
              <p class="row-popover-note">Already published? The Shares view shows its link.</p>
              <div v-if="shareOrigin" class="empty-actions">
                <button
                  class="button button-primary button-compact"
                  type="button"
                  @click="openShares(item.name)"
                >
                  <SquareArrowOutUpRight :size="13" aria-hidden="true" /> Open Shares view
                </button>
              </div>
              <p v-else class="row-popover-note">
                This frame cannot ask the console to navigate — open Networking → Subscription
                Shares yourself.
              </p>
            </div>

            <div v-if="confirmingDelete === item.id" class="alert" role="alert">
              <span>
                Delete <strong>{{ item.name }}</strong>? Any combination including it stops
                rendering until edited, and a published share keeps existing.
              </span>
              <button class="button button-compact" type="button" @click="confirmingDelete = null">
                Keep
              </button>
              <button
                class="button button-compact destructive"
                type="button"
                @click="confirmDelete(item.id)"
              >
                Delete
              </button>
            </div>
          </li>
        </ul>

        <!-- Combinations -->
        <div class="group-head">
          <button type="button" class="group-toggle" @click="showCollections = !showCollections">
            <ChevronDown :size="15" :class="['group-caret', { 'is-open': showCollections }]" />
            <Layers :size="14" aria-hidden="true" /> Combinations ({{ collections.length }})
          </button>
          <button
            class="button button-primary button-compact"
            type="button"
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value || !singles.length"
            :title="!singles.length ? 'Create a subscription first — there is nothing to combine' : ''"
            @click="startCreate(KIND_COLLECTION)"
          >
            <Plus :size="15" aria-hidden="true" /> New
          </button>
        </div>

        <div v-if="showCollections && !collections.length" class="panel-empty panel-empty-stack">
          <p class="panel-empty-copy">
            A combination merges several subscriptions into one URL — your own nodes plus a
            provider's, deduplicated and renamed however you like.
          </p>
          <div class="empty-actions">
            <button
              class="button button-primary"
              type="button"
              :disabled="!subs.canMutate.value || subs.atRecordLimit.value || !singles.length"
              :title="!singles.length ? 'Create a subscription first — there is nothing to combine' : ''"
              @click="startCreate(KIND_COLLECTION)"
            >
              <Layers :size="16" aria-hidden="true" /> New combination
            </button>
          </div>
        </div>

        <ul v-else-if="showCollections" class="sub-list">
          <li v-for="item in collections" :key="item.id" class="sub-card sub-card-column">
            <div class="sub-card-row">
              <div class="sub-card-main">
                <span class="sub-title">
                  <Link2 :size="14" aria-hidden="true" />
                  {{ item.display_name || item.name }}
                  <span v-for="tag in item.tags ?? []" :key="tag" class="badge">{{ tag }}</span>
                </span>
                <span class="sub-meta">
                  {{ describe(item) }}
                  <template v-if="item.target"> · {{ item.target }}</template>
                  <template v-if="item.step_count"> · {{ item.step_count }} operation(s)</template>
                </span>
              </div>
              <div class="sub-actions">
                <button
                  class="icon-button"
                  type="button"
                  :disabled="!host.init.value"
                  :title="
                    host.init.value
                      ? `Share ${item.name}`
                      : 'Shares are published from the Lattice console — this frame is running standalone'
                  "
                  :aria-label="`Share ${item.name}`"
                  :aria-expanded="sharingId === item.id"
                  @click="toggleShare(item.id)"
                >
                  <Share2 :size="16" aria-hidden="true" />
                </button>
                <button
                  class="icon-button"
                  type="button"
                  :disabled="!subs.canMutate.value"
                  title="Make an independent copy of this record"
                  :aria-label="`Duplicate ${item.name}`"
                  @click="subs.duplicate(item.id)"
                >
                  <CopyPlus :size="16" aria-hidden="true" />
                </button>
                <button
                  class="icon-button"
                  type="button"
                  :disabled="!subs.canMutate.value || subs.saving.value"
                  :aria-label="`Edit ${item.name}`"
                  @click="startEdit(item.id)"
                >
                  <Pencil :size="16" aria-hidden="true" />
                </button>
                <button
                  class="icon-button destructive"
                  type="button"
                  :disabled="!subs.canMutate.value"
                  :aria-label="`Delete ${item.name}`"
                  @click="confirmingDelete = confirmingDelete === item.id ? null : item.id"
                >
                  <Trash2 :size="16" aria-hidden="true" />
                </button>
              </div>
            </div>

            <div v-if="sharingId === item.id" class="row-popover">
              <p class="row-popover-copy">
                Nothing here is reachable until a share is published for it. Shares live in the
                dashboard, under <strong>Networking → Subscription Shares</strong>.
              </p>
              <p class="row-popover-note">Already published? The Shares view shows its link.</p>
              <div v-if="shareOrigin" class="empty-actions">
                <button
                  class="button button-primary button-compact"
                  type="button"
                  @click="openShares(item.name)"
                >
                  <SquareArrowOutUpRight :size="13" aria-hidden="true" /> Open Shares view
                </button>
              </div>
              <p v-else class="row-popover-note">
                This frame cannot ask the console to navigate — open Networking → Subscription
                Shares yourself.
              </p>
            </div>

            <div v-if="confirmingDelete === item.id" class="alert" role="alert">
              <span>
                Delete <strong>{{ item.name }}</strong>? The subscriptions it gathers are not
                affected. A published share keeps existing.
              </span>
              <button class="button button-compact" type="button" @click="confirmingDelete = null">
                Keep
              </button>
              <button
                class="button button-compact destructive"
                type="button"
                @click="confirmDelete(item.id)"
              >
                Delete
              </button>
            </div>
          </li>
        </ul>
      </template>
    </section>
  </template>
</template>

<style scoped>
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
