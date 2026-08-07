<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  CircleAlert,
  CircleCheck,
  Eye,
  Globe,
  Layers,
  LoaderCircle,
  Pencil,
  Plus,
  RefreshCw,
  Server,
  ClipboardPaste,
  Trash2,
} from "@lucide/vue";

import {
  CONVERT_TARGETS,
  KIND_COLLECTION,
  KIND_SUB,
  MAX_SUBSCRIPTION_RECORDS,
  SOURCE_LOCAL,
  SOURCE_REMOTE,
  SOURCE_VPN_CORE,
} from "../client";
import { useHost } from "../host";
import {
  draftFromRecord,
  emptyDraft,
  useSubscriptions,
  validateDraft,
  type SubscriptionDraft,
} from "../useSubscriptions";
import EngineUnavailable from "../components/EngineUnavailable.vue";
import ProcessChain, { type ChainStep } from "../components/ProcessChain.vue";

const host = useHost();
const subs = useSubscriptions(host);

const list = ref<typeof KIND_SUB | typeof KIND_COLLECTION>(KIND_SUB);

const editing = ref(false);
/** null while creating; the id being edited otherwise. */
const editingId = ref<string | null>(null);
const draft = ref<SubscriptionDraft>(emptyDraft());
const confirmingDelete = ref<string | null>(null);
const tagText = ref("");
const memberTagText = ref("");

const isCollection = computed(() => draft.value.kind === KIND_COLLECTION);
const draftError = computed(() => (editing.value ? validateDraft(draft.value) : ""));
const canSave = computed(() => !draftError.value && !subs.saving.value);
/** Preview needs something to preview: an invalid draft would just 502. */
const canPreviewNow = computed(
  () => subs.canPreview.value && !subs.previewing.value && !draftError.value,
);

const shown = computed(() => subs.items.value.filter((item) => (item.kind || KIND_SUB) === list.value));
const availableMembers = computed(() =>
  subs.items.value.filter((item) => (item.kind || KIND_SUB) === KIND_SUB),
);
const subCount = computed(() => availableMembers.value.length);

const SOURCES = [
  {
    id: SOURCE_VPN_CORE,
    title: "This fleet's nodes",
    detail: "Reads the live vpn-core export. Nodes added or removed reach clients on the next refresh.",
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
    detail: "Any format the engine recognises: URI list, base64, Clash YAML, sing-box JSON.",
    icon: ClipboardPaste,
  },
] as const;

function startCreate(kind?: string): void {
  subs.clearMessages();
  draft.value = emptyDraft();
  draft.value.kind = kind ?? list.value;
  if (draft.value.kind === KIND_SUB) draft.value.source = SOURCE_VPN_CORE;
  if (kind) list.value = kind as typeof KIND_SUB;
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
  if (!draft.value.source) {
    draft.value.source = draft.value.url ? SOURCE_REMOTE : SOURCE_LOCAL;
  }
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

function toggleMember(id: string): void {
  const current = new Set(draft.value.members);
  if (current.has(id)) current.delete(id);
  else current.add(id);
  draft.value.members = [...current];
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

function sourceLabel(item: {
  kind: string;
  source?: string;
  has_url: boolean;
  members?: string[];
  member_tags?: string[];
}): string {
  if ((item.kind || KIND_SUB) === KIND_COLLECTION) {
    const byId = item.members?.length ?? 0;
    const byTag = item.member_tags?.length ?? 0;
    const parts: string[] = [];
    if (byId) parts.push(`${byId} subscription${byId === 1 ? "" : "s"}`);
    if (byTag) parts.push(`${byTag} tag${byTag === 1 ? "" : "s"}`);
    return parts.join(" + ") || "nothing selected";
  }
  if (item.source === SOURCE_VPN_CORE) return "fleet nodes";
  if (item.source === SOURCE_LOCAL) return "pasted";
  return item.has_url ? "provider link" : "pasted";
}

/**
 * Load after the bridge handshake, not on mount.
 *
 * `available()` reads the interfaces the host declared for this frame, and on
 * first paint that has not arrived yet — so loading in `onMounted` alone
 * silently no-ops and never retries. That is exactly what left the list empty
 * and the operator picker with nothing in it.
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
    <section class="configuration" aria-labelledby="subs-title">
      <div class="section-heading">
        <div>
          <h2 id="subs-title">Subscriptions</h2>
          <p>
            A <strong>subscription</strong> is one source of nodes. A
            <strong>combination</strong> merges several and processes the result. Neither is
            reachable until you publish a share for it in the dashboard.
          </p>
        </div>
        <div class="heading-actions">
          <span class="badge mono">{{ subs.items.value.length }} / {{ MAX_SUBSCRIPTION_RECORDS }}</span>
          <button
            class="button button-primary button-compact"
            type="button"
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value || editing"
            @click="startCreate()"
          >
            <Plus :size="16" aria-hidden="true" />
            New {{ list === KIND_COLLECTION ? "combination" : "subscription" }}
          </button>
        </div>
      </div>

      <nav class="tab-bar" role="tablist" aria-label="Record kind">
        <button
          class="tab"
          type="button"
          role="tab"
          :aria-selected="list === KIND_SUB"
          :data-active="list === KIND_SUB"
          @click="list = KIND_SUB"
        >
          Subscriptions
          <span v-if="subCount" class="tab-count">{{ subCount }}</span>
        </button>
        <button
          class="tab"
          type="button"
          role="tab"
          :aria-selected="list === KIND_COLLECTION"
          :data-active="list === KIND_COLLECTION"
          @click="list = KIND_COLLECTION"
        >
          <Layers :size="15" aria-hidden="true" /> Combinations
          <span v-if="subs.items.value.length - subCount" class="tab-count">
            {{ subs.items.value.length - subCount }}
          </span>
        </button>
      </nav>

      <p v-if="host.init.value && !subs.canMutate.value" class="permission-note">
        This bundle declares the list but not <code>save</code> or <code>delete</code>, so it is
        read-only.
      </p>

      <div v-if="subs.actionError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.actionError.value }}
      </div>
      <div v-else-if="subs.notice.value" class="alert alert-ok" role="status">
        <CircleCheck :size="16" aria-hidden="true" /> {{ subs.notice.value }}
      </div>

      <!-- ── editor ─────────────────────────────────────────────────────── -->
      <form v-if="editing" class="form-grid" @submit.prevent="submit">
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
              Stored as <code>{{ editingId }}</code>. Renaming is safe — any share already
              published keeps working.
            </template>
            <template v-else>The only thing you have to fill in.</template>
          </span>
        </label>

        <!-- ── sub-only: where the nodes come from ─────────────────────── -->
        <div v-if="!isCollection" class="field field-wide">
          <span class="field-label">Where the nodes come from</span>
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
            The vpn-core export normally returns every node this fleet serves. Naming a proxy user
            here narrows it to that user's nodes — useful when one share is meant for one person.
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
              Some providers return a different node list per client. Set this if yours does.
            </span>
          </label>
        </template>

        <label v-if="!isCollection && draft.source === SOURCE_LOCAL" class="field field-wide">
          <span class="field-label">Nodes</span>
          <textarea
            v-model="draft.content"
            class="code-area"
            rows="8"
            spellcheck="false"
            placeholder="Paste node links, a base64 blob, Clash YAML, or sing-box JSON"
          ></textarea>
          <span class="field-optional">
            The engine detects the format. Mixed lists work; one node per line for link formats.
          </span>
        </label>

        <!-- ── collection-only: what it gathers ──────────────────────────
             Written as v-if rather than v-else-if on purpose: an else-if here
             would bind to whichever source block happens to sit above it, so
             reordering the source fields would silently change when this
             renders. -->
        <template v-if="isCollection">
          <div class="field field-wide">
            <span class="field-label">Subscriptions to combine</span>
            <p v-if="!availableMembers.length" class="field-optional">
              There are no subscriptions yet — a combination has nothing to gather on its own.
              Create a subscription first.
            </p>
            <div v-else class="member-grid">
              <label v-for="item in availableMembers" :key="item.id" class="member">
                <input
                  type="checkbox"
                  :checked="draft.members.includes(item.id)"
                  @change="toggleMember(item.id)"
                />
                <span class="member-name">{{ item.name }}</span>
                <span class="member-meta mono">{{ sourceLabel(item) }}</span>
              </label>
            </div>
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
              Gathering by tag means a new subscription joins this combination by being tagged,
              without editing it here.
            </span>
          </label>
        </template>

        <label class="field">
          <span class="field-label">Tags</span>
          <input
            v-model="tagText"
            type="text"
            autocomplete="off"
            spellcheck="false"
            placeholder="home, backup"
          />
        </label>

        <label class="field">
          <span class="field-label">Client format</span>
          <select v-model="draft.target" class="select">
            <option value="">Decide from the client that asks</option>
            <option v-for="target in CONVERT_TARGETS" :key="target.id" :value="target.id">
              {{ target.label }}
            </option>
          </select>
          <span class="field-optional">
            Left automatic, Surge gets Surge and Clash gets Clash from the same URL.
          </span>
        </label>

        <label class="field field-wide">
          <span class="field-label">Note</span>
          <input v-model="draft.remark" type="text" autocomplete="off" placeholder="Optional" />
        </label>

        <!-- ── the operator chain, shared by both kinds ────────────────── -->
        <div class="field field-wide">
          <ProcessChain
            :steps="(draft.process as ChainStep[])"
            :catalog="subs.operators.value"
            @update:steps="draft.process = $event"
          />
          <span v-if="isCollection" class="field-optional">
            Each member runs its own chain first; this one then runs over everything merged.
          </span>
        </div>

        <div class="form-actions">
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
            Preview nodes
          </button>
          <button class="button button-secondary" type="button" @click="cancelEdit">Cancel</button>
          <button class="button button-primary" type="submit" :disabled="!canSave">
            <LoaderCircle v-if="subs.saving.value" :size="16" class="spin" aria-hidden="true" />
            {{ editingId === null ? "Create" : "Save changes" }}
          </button>
        </div>
      </form>

      <div v-if="subs.preview.value" class="preview-summary">
        <p class="mono">
          {{ subs.preview.value.count }} node(s)<span v-if="subs.preview.value.truncated"> — truncated</span>
        </p>
        <ul class="sub-list">
          <li v-for="(node, index) in subs.preview.value.nodes" :key="`${node.name}-${index}`" class="sub-card">
            <span class="sub-title">{{ node.name }}</span>
            <span class="sub-meta mono">{{ node.type }}</span>
          </li>
        </ul>
      </div>
    </section>

    <!-- ── list ─────────────────────────────────────────────────────────── -->
    <section class="configuration" aria-labelledby="subs-list-title">
      <div class="section-heading">
        <div>
          <h2 id="subs-list-title">
            {{ list === KIND_COLLECTION ? "Combinations" : "Stored subscriptions" }}
          </h2>
          <p>Refresh pulls from the source now; the last good snapshot is served if it fails.</p>
        </div>
      </div>

      <p v-if="!host.init.value || subs.state.value === 'loading'" class="skeleton-row">Loading…</p>
      <div v-else-if="subs.loadError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.loadError.value }}
      </div>

      <!-- Empty states say what to do next rather than only that nothing is here. -->
      <div v-else-if="!shown.length" class="panel-empty">
        <template v-if="list === KIND_COLLECTION">
          <p class="panel-empty-copy">
            A <strong>combination</strong> merges several subscriptions and processes the merged
            result as one — one URL that serves your own nodes plus a provider's, deduplicated and
            renamed however you like.
          </p>
          <button
            class="button button-primary"
            type="button"
            :disabled="!subs.canMutate.value || !availableMembers.length"
            @click="startCreate(KIND_COLLECTION)"
          >
            <Plus :size="16" aria-hidden="true" /> Create a combination
          </button>
          <p v-if="!availableMembers.length" class="panel-empty-copy">
            Create a subscription first — there is nothing to combine yet.
          </p>
        </template>
        <template v-else>
          <p class="panel-empty-copy">
            Start with your own fleet: one subscription reading this deployment's vpn-core nodes,
            published at a URL your clients can use.
          </p>
          <button
            class="button button-primary"
            type="button"
            :disabled="!subs.canMutate.value"
            @click="startCreate(KIND_SUB)"
          >
            <Server :size="16" aria-hidden="true" /> Add this fleet's nodes
          </button>
          <p class="panel-empty-copy">
            Or migrate everything from a standalone Sub-Store in <strong>Settings</strong>.
          </p>
        </template>
      </div>

      <ul v-else class="sub-list">
        <li v-for="item in shown" :key="item.id" class="sub-card sub-card-column">
          <div class="sub-card-row">
            <div class="sub-card-main">
              <span class="sub-title">{{ item.display_name || item.name }}</span>
              <span class="sub-meta mono">
                {{ sourceLabel(item) }}
                <template v-if="item.target"> · {{ item.target }}</template>
                <template v-if="item.step_count">
                  · {{ item.step_count }} step(s)<template v-if="item.disabled_step_count">
                    ({{ item.disabled_step_count }} off)</template>
                </template>
              </span>
              <span v-if="item.tags?.length" class="sub-meta">
                <span v-for="tag in item.tags" :key="tag" class="badge">{{ tag }}</span>
              </span>
            </div>
            <div class="sub-actions">
              <span v-if="item.imported" class="badge">migrated</span>
              <button
                v-if="item.kind !== KIND_COLLECTION"
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
                :disabled="!subs.canMutate.value"
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

          <div v-if="confirmingDelete === item.id" class="alert" role="alert">
            <span>
              Delete <strong>{{ item.name }}</strong>?
              <template v-if="item.kind !== KIND_COLLECTION">
                Any combination that includes it will stop rendering until it is edited.
              </template>
              Any share published for it keeps existing and must be removed in the dashboard.
            </span>
            <button class="button button-compact" type="button" @click="confirmingDelete = null">
              Keep
            </button>
            <button
              class="button button-compact destructive"
              type="button"
              :disabled="subs.busyId.value === item.id"
              @click="confirmDelete(item.id)"
            >
              Delete
            </button>
          </div>
        </li>
      </ul>
    </section>
  </template>
</template>

<style scoped>
.tab-count {
  margin-left: 6px;
  padding: 1px 6px;
  border-radius: 999px;
  background: color-mix(in srgb, currentColor 16%, transparent);
  font-size: 10.5px;
  font-weight: 700;
}

/* The source is the first real decision when creating a subscription, so it is
   three explained choices rather than a dropdown of three words. */
.source-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
  gap: 10px;
  margin-top: 8px;
}

.source {
  display: grid;
  grid-template-columns: auto 1fr;
  grid-template-areas: "icon title" "icon detail";
  gap: 2px 10px;
  align-items: start;
  padding: 13px 14px;
  border: 1px solid var(--border, #242d3a);
  border-radius: 11px;
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease;
}

.source > svg {
  grid-area: icon;
  margin-top: 2px;
  color: var(--text-3, #7c8896);
}

.source.is-active {
  border-color: var(--accent, #2dd4bf);
  background: color-mix(in srgb, var(--accent, #2dd4bf) 9%, transparent);
}

.source.is-active > svg {
  color: var(--accent, #2dd4bf);
}

.source-title {
  grid-area: title;
  font-size: 13.5px;
  font-weight: 650;
}

.source-detail {
  grid-area: detail;
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--text-3, #7c8896);
}

.member-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 8px;
  margin-top: 8px;
}

.member {
  display: grid;
  grid-template-columns: auto 1fr;
  grid-template-areas: "box name" "box meta";
  gap: 2px 8px;
  align-items: center;
  padding: 9px 11px;
  border: 1px solid var(--border, #242d3a);
  border-radius: 9px;
  cursor: pointer;
}

.member input {
  grid-area: box;
}

.member-name {
  grid-area: name;
  font-size: 13px;
  font-weight: 650;
}

.member-meta {
  grid-area: meta;
  font-size: 11px;
  color: var(--text-3, #7c8896);
}
</style>
