<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  CircleAlert,
  CircleCheck,
  Eye,
  LoaderCircle,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "@lucide/vue";

import { CONVERT_TARGETS, MAX_SUBSCRIPTION_RECORDS } from "../client";
import { useHost } from "../host";
import {
  draftFromRecord,
  emptyDraft,
  useSubscriptions,
  validateDraft,
  type SubscriptionDraft,
} from "../useSubscriptions";
import EngineUnavailable from "../components/EngineUnavailable.vue";

const host = useHost();
const subs = useSubscriptions(host);

const editing = ref(false);
/** null while creating; the id being edited otherwise. Ids are immutable — a
 *  rename would orphan any share already published against the old id. */
const editingId = ref<string | null>(null);
const draft = ref<SubscriptionDraft>(emptyDraft());
const confirmingDelete = ref<string | null>(null);
const operatorsText = ref("");
const operatorsError = ref("");

const draftError = computed(() => (editing.value ? validateDraft(draft.value) : ""));
const canSave = computed(() => !draftError.value && !operatorsError.value && !subs.saving.value);

function startCreate(): void {
  subs.clearMessages();
  draft.value = emptyDraft();
  operatorsText.value = "";
  operatorsError.value = "";
  editingId.value = null;
  editing.value = true;
}

async function startEdit(id: string): Promise<void> {
  subs.clearMessages();
  const record = await subs.get(id);
  if (!record) return;
  draft.value = draftFromRecord(record);
  operatorsText.value = draft.value.operators.length
    ? JSON.stringify(draft.value.operators, null, 2)
    : "";
  operatorsError.value = "";
  editingId.value = id;
  editing.value = true;
  await host.resize();
}

function cancelEdit(): void {
  editing.value = false;
  editingId.value = null;
  draft.value = emptyDraft();
  operatorsText.value = "";
  operatorsError.value = "";
  subs.preview.value = null;
}

/** Operators are edited as JSON. Parsing here keeps a malformed chain from
 *  reaching the backend as a confusing save failure; unknown operator TYPES
 *  are still the backend's call, because it owns the catalogue. */
function syncOperators(): boolean {
  const text = operatorsText.value.trim();
  if (!text) {
    draft.value.operators = [];
    operatorsError.value = "";
    return true;
  }
  try {
    const parsed: unknown = JSON.parse(text);
    if (!Array.isArray(parsed)) {
      operatorsError.value = "The operator chain must be a JSON array.";
      return false;
    }
    draft.value.operators = parsed;
    operatorsError.value = "";
    return true;
  } catch (cause) {
    operatorsError.value = cause instanceof Error ? `Invalid JSON: ${cause.message}` : "Invalid JSON.";
    return false;
  }
}

async function submit(): Promise<void> {
  if (!syncOperators()) return;
  const ok = await subs.save(draft.value);
  if (ok) cancelEdit();
}

async function previewDraft(): Promise<void> {
  if (!syncOperators()) return;
  await subs.runPreview(draft.value);
}

async function confirmDelete(id: string): Promise<void> {
  const ok = await subs.remove(id);
  if (ok) confirmingDelete.value = null;
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
          <p>Stored definitions. A subscription is served only once you publish a share for it.</p>
        </div>
        <div class="heading-actions">
          <span class="badge mono">{{ subs.items.value.length }} / {{ MAX_SUBSCRIPTION_RECORDS }}</span>
          <button
            class="button button-primary button-compact"
            type="button"
            :disabled="!subs.canMutate.value || subs.atRecordLimit.value || editing"
            @click="startCreate"
          >
            <Plus :size="16" aria-hidden="true" /> New subscription
          </button>
        </div>
      </div>

      <p v-if="!subs.canMutate.value" class="permission-note">
        This bundle declares the subscription list but not <code>save</code> or
        <code>delete</code>, so the list is read-only.
      </p>
      <p v-else-if="subs.atRecordLimit.value" class="permission-note">
        The record limit is reached. Delete a subscription before adding another.
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
          <span class="field-label">Provider URL</span>
          <input
            v-model="draft.url"
            type="text"
            autocomplete="off"
            spellcheck="false"
            placeholder="The provider&apos;s subscription link"
          />
          <span class="field-optional">Leave empty if the content is pasted below instead.</span>
        </label>

        <label class="field">
          <span class="field-label">User agent</span>
          <input v-model="draft.ua" type="text" autocomplete="off" placeholder="Provider-specific, optional" />
        </label>

        <label class="field">
          <span class="field-label">Target</span>
          <select v-model="draft.target" class="select">
            <option value="">No conversion</option>
            <option v-for="target in CONVERT_TARGETS" :key="target.id" :value="target.id">
              {{ target.label }}
            </option>
          </select>
        </label>

        <label class="field field-wide">
          <span class="field-label">Inline content</span>
          <textarea
            v-model="draft.content"
            class="code-area"
            rows="6"
            spellcheck="false"
            placeholder="Paste nodes here for a subscription with no provider"
          ></textarea>
        </label>

        <label class="field field-wide">
          <span class="field-label">Operator chain</span>
          <textarea
            v-model="operatorsText"
            class="code-area"
            rows="5"
            spellcheck="false"
            placeholder='[{"type":"Flag Operator"}]'
            @blur="syncOperators"
          ></textarea>
          <span v-if="operatorsError" class="field-error">{{ operatorsError }}</span>
          <span v-else-if="subs.operators.value.length" class="field-optional">
            {{ subs.operators.value.length }} operator types are accepted. An unknown type is
            refused rather than ignored.
          </span>
        </label>

        <div class="form-actions">
          <span v-if="draftError" class="field-error">{{ draftError }}</span>
          <button
            class="button button-secondary"
            type="button"
            :disabled="!subs.canPreview.value || subs.previewing.value"
            @click="previewDraft"
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
          {{ subs.preview.value.count }} node(s)<span v-if="subs.preview.value.truncated"> — list truncated</span>
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
          <h2 id="subs-list-title">Stored</h2>
          <p>Refresh pulls from the provider now; the last good snapshot is served if it fails.</p>
        </div>
      </div>

      <p v-if="subs.state.value === 'loading'" class="skeleton-row">Loading…</p>
      <div v-else-if="subs.loadError.value" class="alert" role="alert">
        <CircleAlert :size="16" aria-hidden="true" /> {{ subs.loadError.value }}
      </div>
      <div v-else-if="!subs.items.value.length" class="panel-empty">
        <p class="panel-empty-copy">
          No subscriptions yet. Create one, or migrate them from a standalone Sub-Store in Settings.
        </p>
      </div>

      <ul v-else class="sub-list">
        <li v-for="item in subs.items.value" :key="item.id" class="sub-card sub-card-column">
          <div class="sub-card-row">
            <div class="sub-card-main">
              <span class="sub-title">{{ item.name }}</span>
              <span class="sub-meta mono">
                {{ item.id }}
                · {{ item.has_url ? "provider" : "inline" }}
                <template v-if="item.target"> · {{ item.target }}</template>
                <template v-if="item.operator_count"> · {{ item.operator_count }} operator(s)</template>
              </span>
            </div>
            <div class="sub-actions">
              <span v-if="item.imported" class="badge">migrated</span>
              <button
                class="icon-button"
                type="button"
                :disabled="!subs.canFetch.value || subs.busyId.value === item.id"
                :aria-label="`Refresh ${item.id}`"
                :title="item.has_url ? 'Fetch from the provider now' : 'No provider URL to fetch from'"
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

          <!-- Deletion is confirmed inline rather than through window.confirm:
               a modal dialog inside the sandboxed frame blocks the host. -->
          <div v-if="confirmingDelete === item.id" class="alert" role="alert">
            <span>
              Delete <code>{{ item.id }}</code>? Any share published for it keeps existing and must
              be removed separately in the dashboard.
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
