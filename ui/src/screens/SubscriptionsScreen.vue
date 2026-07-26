<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  CircleAlert,
  LoaderCircle,
  LockKeyhole,
  Plus,
  RefreshCw,
  Rss,
  SearchCheck,
  Trash2,
} from "@lucide/vue";

import type { SubscriptionPreviewResponse } from "../client";
import { useHost } from "../host";
import { useSubscriptions } from "../useSubscriptions";
import {
  validateDisplayName,
  validateSubscriptionName,
  validateSubscriptionUrl,
} from "../subscriptionsModel";
import EngineUnavailable from "../components/EngineUnavailable.vue";

const host = useHost();
const subs = useSubscriptions(host);

// ── create form ─────────────────────────────────────────────────────────────
const name = ref("");
const displayName = ref("");
const sourceUrl = ref("");
const checking = ref(false);
const checkResult = ref<SubscriptionPreviewResponse>();

const nameState = computed(() => validateSubscriptionName(name.value));
const displayNameState = computed(() => validateDisplayName(displayName.value));
const urlState = computed(() => validateSubscriptionUrl(sourceUrl.value));
const formValid = computed(
  () => !!nameState.value.value && !displayNameState.value.error && !!urlState.value.value,
);

watch([name, displayName, sourceUrl], () => {
  checkResult.value = undefined;
});

async function checkSource(): Promise<void> {
  if (!urlState.value.value || checking.value) return;
  checking.value = true;
  try {
    checkResult.value = await subs.previewSource(urlState.value.value);
  } finally {
    checking.value = false;
  }
}

async function addSubscription(): Promise<void> {
  if (!formValid.value || !nameState.value.value || !urlState.value.value) return;
  const ok = await subs.create({
    name: nameState.value.value,
    displayName: displayNameState.value.value,
    sourceUrl: urlState.value.value,
  });
  if (ok) {
    name.value = "";
    displayName.value = "";
    sourceUrl.value = "";
    checkResult.value = undefined;
  }
}

// ── row actions ─────────────────────────────────────────────────────────────
const confirmingDelete = ref<string | null>(null);

async function removeSubscription(nameToDelete: string): Promise<void> {
  const ok = await subs.remove(nameToDelete);
  if (ok && confirmingDelete.value === nameToDelete) confirmingDelete.value = null;
}

// ── lifecycle ───────────────────────────────────────────────────────────────
onMounted(() => {
  void subs.load();
});
watch(host.init, (value) => {
  if (value) void subs.load();
});
</script>

<template>
  <section>
    <EngineUnavailable v-if="!subs.available.value" feature="Subscription management" />

    <template v-else>
      <section class="configuration" aria-labelledby="add-subscription-title">
        <div class="section-heading">
          <div>
            <h2 id="add-subscription-title">Add subscription</h2>
            <p>Remote provider URLs are stored server-side; only a redacted hint comes back.</p>
          </div>
        </div>

        <div class="form-grid">
          <label class="field">
            <span class="field-label">Name</span>
            <input v-model="name" type="text" autocomplete="off" spellcheck="false" placeholder="hk-main" />
            <small v-if="name && nameState.error" class="field-error">{{ nameState.error }}</small>
          </label>
          <label class="field">
            <span class="field-label">Display name <span class="field-optional">optional</span></span>
            <input v-model="displayName" type="text" autocomplete="off" spellcheck="false" placeholder="HK main line" />
            <small v-if="displayName && displayNameState.error" class="field-error">{{ displayNameState.error }}</small>
          </label>
          <label class="field field-wide">
            <span class="field-label">Subscription URL</span>
            <input
              v-model="sourceUrl"
              type="password"
              autocomplete="off"
              spellcheck="false"
              placeholder="Provider subscription URL (https)"
              @keyup.enter="checkSource"
            />
            <small v-if="sourceUrl && urlState.error" class="field-error">{{ urlState.error }}</small>
          </label>
        </div>

        <div v-if="checkResult" class="preview-panel" aria-live="polite">
          <div class="preview-summary">
            <span>Parses as <strong>{{ checkResult.node_count }}</strong> nodes</span>
            <span
              v-for="(count, type) in checkResult.node_types"
              :key="type"
              class="badge"
              data-tone="neutral"
            >{{ type }} ×{{ count }}</span>
            <span
              v-for="warning in checkResult.warnings"
              :key="warning"
              class="badge"
              data-tone="warning"
            >{{ warning }}</span>
          </div>
          <ul v-if="checkResult.sample_names.length" class="preview-list">
            <li v-for="sample in checkResult.sample_names.slice(0, 8)" :key="sample">{{ sample }}</li>
          </ul>
        </div>

        <div class="form-actions">
          <button
            class="button button-secondary"
            type="button"
            :disabled="!urlState.value || checking"
            @click="checkSource"
          >
            <LoaderCircle v-if="checking" class="spin" :size="15" aria-hidden="true" />
            <SearchCheck v-else :size="15" aria-hidden="true" />
            {{ checking ? "Parsing" : "Parse check" }}
          </button>
          <button
            class="button button-primary"
            type="button"
            :disabled="!formValid || subs.creating.value || !subs.canMutate.value"
            @click="addSubscription"
          >
            <LoaderCircle v-if="subs.creating.value" class="spin" :size="15" aria-hidden="true" />
            <Plus v-else :size="15" aria-hidden="true" />
            {{ subs.creating.value ? "Adding" : "Add subscription" }}
          </button>
          <span v-if="!subs.canMutate.value" class="permission-note">
            <LockKeyhole :size="14" aria-hidden="true" />
            Administrator access required
          </span>
        </div>
      </section>

      <section class="configuration" aria-labelledby="subscriptions-title">
        <div class="section-heading">
          <div>
            <h2 id="subscriptions-title">Subscriptions</h2>
            <p>{{ subs.items.value.length ? `${subs.items.value.length} configured` : "Sources the engine converts from" }}</p>
          </div>
          <button
            class="button button-secondary button-compact"
            type="button"
            :disabled="subs.state.value === 'loading'"
            @click="subs.load"
          >
            <RefreshCw :size="13" aria-hidden="true" />
            Reload
          </button>
        </div>

        <div v-if="subs.state.value === 'loading'" class="sub-list" aria-label="Loading subscriptions">
          <div v-for="row in 3" :key="row" class="skeleton-row" />
        </div>

        <div v-else-if="subs.state.value === 'error'" class="alert" role="alert">
          <CircleAlert :size="17" aria-hidden="true" />
          <span>{{ subs.loadError.value }}</span>
          <button class="button button-secondary button-compact" type="button" @click="subs.load">Retry</button>
        </div>

        <div v-else-if="!subs.items.value.length" class="panel-empty">
          <Rss :size="22" aria-hidden="true" />
          <div class="panel-empty-copy">
            <h3>No subscriptions yet</h3>
            <p>Add a provider URL above. The engine fetches and parses it through the host's network capability — the frame itself never talks to the network.</p>
          </div>
        </div>

        <ul v-else class="sub-list">
          <li v-for="item in subs.items.value" :key="item.name" class="sub-card">
            <div class="sub-card-main">
              <div class="sub-title">
                <strong>{{ item.display_name || item.name }}</strong>
                <code class="mono sub-hint">{{ item.url_hint }}</code>
              </div>
              <div class="sub-meta">
                <span class="badge" data-tone="neutral">{{ item.name }}</span>
                <span v-if="item.node_count !== undefined" class="badge" data-tone="info">{{ item.node_count }} nodes</span>
                <span v-if="item.last_refresh_at" class="mono">synced {{ item.last_refresh_at }}</span>
                <span v-if="item.last_error" class="badge" data-tone="warning">{{ item.last_error }}</span>
              </div>
            </div>
            <div class="sub-actions">
              <template v-if="confirmingDelete === item.name">
                <button
                  class="button button-secondary button-compact destructive"
                  type="button"
                  :disabled="subs.busyName.value === item.name"
                  @click="removeSubscription(item.name)"
                >
                  Confirm delete
                </button>
                <button class="button button-secondary button-compact" type="button" @click="confirmingDelete = null">
                  Cancel
                </button>
              </template>
              <template v-else>
                <button
                  class="button button-secondary button-compact"
                  type="button"
                  :disabled="!!subs.busyName.value"
                  :title="`Re-fetch ${item.name}`"
                  @click="subs.refresh(item.name)"
                >
                  <LoaderCircle v-if="subs.busyName.value === item.name" class="spin" :size="13" aria-hidden="true" />
                  <RefreshCw v-else :size="13" aria-hidden="true" />
                  Refresh
                </button>
                <button
                  v-if="subs.canMutate.value"
                  class="icon-button"
                  type="button"
                  :aria-label="`Delete ${item.name}`"
                  :title="`Delete ${item.name}`"
                  :disabled="!!subs.busyName.value"
                  @click="confirmingDelete = item.name"
                >
                  <Trash2 :size="15" aria-hidden="true" />
                </button>
              </template>
            </div>
          </li>
        </ul>

        <div v-if="subs.actionError.value" class="alert" role="alert">
          <CircleAlert :size="17" aria-hidden="true" />
          <span>{{ subs.actionError.value }}</span>
        </div>
      </section>
    </template>
  </section>
</template>
