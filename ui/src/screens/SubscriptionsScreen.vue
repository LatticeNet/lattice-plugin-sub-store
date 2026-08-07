<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  CircleAlert,
  CircleCheck,
  Eye,
  Layers,
  LoaderCircle,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "@lucide/vue";

import {
  CONVERT_TARGETS,
  KIND_COLLECTION,
  KIND_SUB,
  MAX_SUBSCRIPTION_RECORDS,
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

/** Which list is showing. Two lists rather than two tabs of the plugin, because
 *  a collection is built out of subs and you need to see both while editing. */
const list = ref<typeof KIND_SUB | typeof KIND_COLLECTION>(KIND_SUB);

const editing = ref(false);
/** null while creating; the id being edited otherwise. Ids are immutable — a
 *  rename would orphan any share already published against the old id. */
const editingId = ref<string | null>(null);
const draft = ref<SubscriptionDraft>(emptyDraft());
const confirmingDelete = ref<string | null>(null);
const tagText = ref("");
const memberTagText = ref("");

const isCollection = computed(() => draft.value.kind === KIND_COLLECTION);
const draftError = computed(() => (editing.value ? validateDraft(draft.value) : ""));
const canSave = computed(() => !draftError.value && !subs.saving.value);

const shown = computed(() => subs.items.value.filter((item) => (item.kind || KIND_SUB) === list.value));
/** Only subs can be members; a collection inside a collection would recurse. */
const availableMembers = computed(() =>
  subs.items.value.filter((item) => (item.kind || KIND_SUB) === KIND_SUB),
);

function startCreate(): void {
  subs.clearMessages();
  draft.value = emptyDraft();
  draft.value.kind = list.value;
  // A new sub defaults to the fleet's own nodes: that is what a Lattice
  // deployment has, and every other source needs something supplied first.
  if (list.value === KIND_SUB) draft.value.source = SOURCE_VPN_CORE;
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

function sourceLabel(item: { kind: string; source?: string; has_url: boolean; members?: string[]; member_tags?: string[] }): string {
  if ((item.kind || KIND_SUB) === KIND_COLLECTION) {
    const byId = item.members?.length ?? 0;
    const byTag = item.member_tags?.length ?? 0;
    const parts: string[] = [];
    if (byId) parts.push(`${byId} subscription${byId === 1 ? "" : "s"}`);
    if (byTag) parts.push(`${byTag} tag${byTag === 1 ? "" : "s"}`);
    return parts.join(" + ") || "nothing selected";
  }
  if (item.source === SOURCE_VPN_CORE) return "vpn-core";
  return item.has_url ? "provider" : "inline";
}

onMounted(async () => {
  await subs.load();
  await subs.loadOperators();
});
</script>

<template>
  <EngineUnavailable v-if="!subs.available.value" feature="Subscriptions" />

  <template v-else>
    <section class="configuration" aria-labelledby="subs-title">
      <div class="section-heading">
        <div>
          <h2 id="subs-title">Subscriptions</h2>
          <p>
            A <strong>subscription</strong> is one source of nodes. A
            <strong>combination</strong> merges several and processes the result. Neither is
            reachable until you publish a share for it.
          </p>
        </div>
        <div class="heading-actions">
          <span class="badge mono">{{ subs.items.value.length }} / {{ MAX_SUBSCRIPTION_RECORDS }}</span>
          <button
            class="button button-primary button-compact"
            type="button"
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value || editing"
            @click="startCreate"
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
        </button>
      </nav>

      <p v-if="!subs.canMutate.value" class="permission-note">
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
        <label class="field">
          <span class="field-label">Id</span>
          <input
            v-model="draft.id"
            type="text"
            autocomplete="off"
            spellcheck="false"
            :disabled="editingId !== null"
            placeholder="home-nodes"
          />
          <span v-if="editingId !== null" class="field-optional">
            Ids are permanent — a share already published points at this one.
          </span>
        </label>

        <label class="field">
          <span class="field-label">Name</span>
          <input v-model="draft.name" type="text" autocomplete="off" placeholder="Defaults to the id" />
        </label>

        <label class="field field-wide">
          <span class="field-label">Note</span>
          <input v-model="draft.remark" type="text" autocomplete="off" placeholder="Optional" />
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
          <span class="field-optional">A combination can gather every subscription by tag.</span>
        </label>

        <label class="field">
          <span class="field-label">Target</span>
          <select v-model="draft.target" class="select">
            <option value="">Decide from the client</option>
            <option v-for="target in CONVERT_TARGETS" :key="target.id" :value="target.id">
              {{ target.label }}
            </option>
          </select>
        </label>

        <!-- ── sub-only: where the nodes come from ─────────────────────── -->
        <template v-if="!isCollection">
          <label class="field field-wide">
            <span class="field-label">Content comes from</span>
            <select v-model="draft.source" class="select">
              <option :value="SOURCE_VPN_CORE">This fleet's vpn-core nodes</option>
              <option value="">A provider URL or pasted content</option>
            </select>
            <span v-if="draft.source === SOURCE_VPN_CORE" class="field-optional">
              Reads the live node export. Nodes added or removed in vpn-core reach clients on the
              next refresh.
            </span>
          </label>

          <label v-if="draft.source === SOURCE_VPN_CORE" class="field">
            <span class="field-label">VPN identity</span>
            <input
              v-model="draft.vpnIdentity"
              type="text"
              autocomplete="off"
              spellcheck="false"
              placeholder="All eligible identities"
            />
          </label>

          <template v-else>
            <label class="field field-wide">
              <span class="field-label">Provider URL</span>
              <input
                v-model="draft.url"
                type="text"
                autocomplete="off"
                spellcheck="false"
                placeholder="The provider&apos;s subscription link"
              />
            </label>
            <label class="field">
              <span class="field-label">User agent</span>
              <input v-model="draft.ua" type="text" autocomplete="off" placeholder="Optional" />
            </label>
            <label class="field field-wide">
              <span class="field-label">Inline content</span>
              <textarea
                v-model="draft.content"
                class="code-area"
                rows="5"
                spellcheck="false"
                placeholder="Paste nodes here instead of giving a URL"
              ></textarea>
            </label>
          </template>
        </template>

        <!-- ── collection-only: what it gathers ────────────────────────── -->
        <template v-else>
          <div class="field field-wide">
            <span class="field-label">Subscriptions to combine</span>
            <p v-if="!availableMembers.length" class="field-optional">
              There are no subscriptions yet. Create one first — a combination has nothing to
              gather on its own.
            </p>
            <div v-else class="member-grid">
              <label v-for="item in availableMembers" :key="item.id" class="member">
                <input
                  type="checkbox"
                  :checked="draft.members.includes(item.id)"
                  @change="toggleMember(item.id)"
                />
                <span class="member-name">{{ item.name }}</span>
                <span class="member-meta mono">{{ item.id }} · {{ sourceLabel(item) }}</span>
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
            :disabled="!subs.canPreview.value || subs.previewing.value"
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

      <p v-if="subs.state.value === 'loading'" class="skeleton-row">Loading…</p>
      <div v-else-if="subs.loadError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.loadError.value }}
      </div>
      <div v-else-if="!shown.length" class="panel-empty">
        <p class="panel-empty-copy">
          <template v-if="list === KIND_COLLECTION">
            No combinations yet. A combination merges several subscriptions and processes the
            result as one.
          </template>
          <template v-else>
            No subscriptions yet. Create one from this fleet's vpn-core nodes, or migrate from a
            standalone Sub-Store in Settings.
          </template>
        </p>
      </div>

      <ul v-else class="sub-list">
        <li v-for="item in shown" :key="item.id" class="sub-card sub-card-column">
          <div class="sub-card-row">
            <div class="sub-card-main">
              <span class="sub-title">{{ item.display_name || item.name }}</span>
              <span class="sub-meta mono">
                {{ item.id }} · {{ sourceLabel(item) }}
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
                :aria-label="`Refresh ${item.id}`"
                @click="subs.refresh(item.id)"
              >
                <LoaderCircle v-if="subs.busyId.value === item.id" :size="16" class="spin" aria-hidden="true" />
                <RefreshCw v-else :size="16" aria-hidden="true" />
              </button>
              <button
                class="icon-button"
                type="button"
                :disabled="!subs.canMutate.value"
                :aria-label="`Edit ${item.id}`"
                @click="startEdit(item.id)"
              >
                <Pencil :size="16" aria-hidden="true" />
              </button>
              <button
                class="icon-button destructive"
                type="button"
                :disabled="!subs.canMutate.value"
                :aria-label="`Delete ${item.id}`"
                @click="confirmingDelete = confirmingDelete === item.id ? null : item.id"
              >
                <Trash2 :size="16" aria-hidden="true" />
              </button>
            </div>
          </div>

          <!-- Confirmed inline rather than through window.confirm: a modal
               inside the sandboxed frame blocks the host. -->
          <div v-if="confirmingDelete === item.id" class="alert" role="alert">
            <span>
              Delete <code>{{ item.id }}</code>?
              <template v-if="item.kind !== KIND_COLLECTION">
                Any combination that names it will stop rendering until it is edited.
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
