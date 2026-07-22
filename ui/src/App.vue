<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import {
  CheckCircle2,
  CircleAlert,
  DownloadCloud,
  Eye,
  EyeOff,
  KeyRound,
  LoaderCircle,
  LockKeyhole,
  PlugZap,
  Store,
} from "@lucide/vue";

import { BridgeClient, canCall, type HostInit } from "./bridge";
import { safeErrorMessage, statusLabel, validateCollection, validateEndpoint } from "./subStoreModel";

const SERVICE = "latticenet.sub-store/import";

interface StatusResponse {
  reachable: boolean;
  sub_name: string;
  error?: string;
}

interface ImportResponse {
  ok: boolean;
  sub_name: string;
  pushed: number;
}

const endpoint = ref("");
const collection = ref("lattice-vpn-core");
const userId = ref("");
const revealEndpoint = ref(false);
const checking = ref(false);
const importing = ref(false);
const status = ref<StatusResponse>();
const result = ref<ImportResponse>();
const error = ref("");
const init = ref<HostInit>();
const bootError = ref("");

let bridge: BridgeClient | undefined;
try {
  bridge = new BridgeClient(window);
  bridge.init.then((value) => {
    init.value = value;
    void loadEndpointStatus();
  }).catch((cause) => {
    bootError.value = safeErrorMessage(cause, "Plugin host unavailable");
  });
} catch (cause) {
  bootError.value = safeErrorMessage(cause, "Plugin host unavailable");
}

const endpointState = computed(() => validateEndpoint(endpoint.value));
const collectionError = computed(() => validateCollection(collection.value));
const busy = computed(() => checking.value || importing.value);
const canCheck = computed(() => canCall(init.value, SERVICE, "status"));
const canImport = computed(() => canCall(init.value, SERVICE, "import"));
const readyForAction = computed(() => !!endpointState.value.value && !collectionError.value && !busy.value && !bootError.value);

watch([endpoint, collection, userId], () => {
  status.value = undefined;
  result.value = undefined;
  preview.value = undefined;
  error.value = "";
});

async function checkStatus(): Promise<void> {
  if (!bridge || !readyForAction.value || !canCheck.value || !endpointState.value.value) return;
  checking.value = true;
  error.value = "";
  try {
    const request = bridge.call<StatusResponse>(SERVICE, "status", {
      base_url: endpointState.value.value,
      sub_name: collection.value.trim(),
    });
    status.value = await request.promise;
    if (!status.value.reachable && status.value.error) error.value = safeErrorMessage(status.value.error, "Sub-Store unavailable");
  } catch (cause) {
    status.value = { reachable: false, sub_name: collection.value.trim() };
    error.value = safeErrorMessage(cause, "Status check failed");
  } finally {
    checking.value = false;
    await resize();
  }
}

async function runImport(): Promise<void> {
  if (!bridge || !readyForAction.value || !canImport.value || !endpointState.value.value) return;
  importing.value = true;
  error.value = "";
  result.value = undefined;
  try {
    const request = bridge.call<ImportResponse>(SERVICE, "import", {
      base_url: endpointState.value.value,
      sub_name: collection.value.trim(),
      user_id: userId.value.trim() || undefined,
    });
    result.value = await request.promise;
  } catch (cause) {
    error.value = safeErrorMessage(cause, "Import failed");
  } finally {
    importing.value = false;
    await resize();
  }
}

// ── preview (design-15 §7) ────────────────────────────────────────────────────
interface PreviewResponse {
  sub_name: string;
  exists: boolean;
  added: string[];
  removed: string[];
  added_count: number;
  removed_count: number;
  unchanged_count: number;
  total_after: number;
}
const preview = ref<PreviewResponse>();
const previewing = ref(false);
const canPreview = computed(() => canCall(init.value, SERVICE, "preview"));

async function runPreview(): Promise<void> {
  if (!bridge || !readyForAction.value || !canPreview.value || !endpointState.value.value) return;
  previewing.value = true;
  error.value = "";
  preview.value = undefined;
  try {
    preview.value = await bridge.call<PreviewResponse>(SERVICE, "preview", {
      base_url: endpointState.value.value,
      sub_name: collection.value.trim(),
      user_id: userId.value.trim() || undefined,
    }).promise;
  } catch (cause) {
    error.value = safeErrorMessage(cause, "Preview failed");
  } finally {
    previewing.value = false;
    await resize();
  }
}

// ── encrypted endpoint vault (design-15 §7) ───────────────────────────────────
interface EndpointStatusResponse {
  has_saved_endpoint: boolean;
  autosync: boolean;
  endpoint_hint?: string;
  autosync_status?: {
    state: "running" | "success" | "error";
    attempted_at?: string;
    last_success_at?: string;
    error?: string;
  };
}
const endpointStatus = ref<EndpointStatusResponse>();
const autosync = ref(false);
const savingEndpoint = ref(false);
const canManageEndpoint = computed(() => canCall(init.value, SERVICE, "save_endpoint") && canCall(init.value, SERVICE, "clear_endpoint"));
const usingSavedRef = computed(() => endpoint.value.trim().startsWith("secret://"));

async function loadEndpointStatus(): Promise<void> {
  if (!bridge || !canCall(init.value, SERVICE, "endpoint_status")) return;
  try {
    endpointStatus.value = await bridge.call<EndpointStatusResponse>(SERVICE, "endpoint_status", {}).promise;
    autosync.value = endpointStatus.value.autosync;
  } catch {
    endpointStatus.value = undefined;
  }
}

async function saveEndpoint(): Promise<void> {
  if (!bridge || !canManageEndpoint.value || savingEndpoint.value || !endpointState.value.value || usingSavedRef.value) return;
  savingEndpoint.value = true;
  error.value = "";
  try {
    await bridge.call(SERVICE, "save_endpoint", {
      base_url: endpointState.value.value,
      autosync: autosync.value,
    }).promise;
    await loadEndpointStatus();
  } catch (cause) {
    error.value = safeErrorMessage(cause, "Endpoint could not be saved");
  } finally {
    savingEndpoint.value = false;
    await resize();
  }
}

async function clearEndpoint(): Promise<void> {
  if (!bridge || !canManageEndpoint.value || savingEndpoint.value) return;
  savingEndpoint.value = true;
  error.value = "";
  try {
    await bridge.call(SERVICE, "clear_endpoint", {}).promise;
    if (usingSavedRef.value) endpoint.value = "";
    await loadEndpointStatus();
  } catch (cause) {
    error.value = safeErrorMessage(cause, "Saved endpoint could not be cleared");
  } finally {
    savingEndpoint.value = false;
    await resize();
  }
}

function useSavedEndpoint(): void {
  endpoint.value = "secret://latticenet.sub-store/endpoint";
}

async function resize(): Promise<void> {
  await nextTick();
  bridge?.resize(document.documentElement.scrollHeight);
}

let observer: ResizeObserver | undefined;
onMounted(() => {
  observer = new ResizeObserver(() => { bridge?.resize(document.documentElement.scrollHeight); });
  observer.observe(document.body);
  void resize();
});

onBeforeUnmount(() => {
  observer?.disconnect();
  bridge?.dispose();
});
</script>

<template>
  <main class="workspace">
    <header class="page-header">
      <div class="title-mark" aria-hidden="true"><Store :size="19" /></div>
      <div class="title-copy">
        <h1>Sub-Store</h1>
        <p>Publish vpn-core connections into a managed Sub-Store collection.</p>
      </div>
      <span class="status" :data-tone="status?.reachable ? 'positive' : status ? 'negative' : 'neutral'">
        <CheckCircle2 v-if="status?.reachable" :size="14" aria-hidden="true" />
        <CircleAlert v-else-if="status" :size="14" aria-hidden="true" />
        {{ statusLabel(status) }}
      </span>
    </header>

    <div v-if="bootError" class="alert" role="alert">
      <CircleAlert :size="17" aria-hidden="true" />
      <span>{{ bootError }}</span>
    </div>

    <section class="configuration" aria-labelledby="connection-title">
      <div class="section-heading">
        <div>
          <h2 id="connection-title">Connection</h2>
          <p>Target endpoint and collection identity</p>
        </div>
        <button
          class="button button-secondary"
          type="button"
          :disabled="!readyForAction || !canCheck"
          @click="checkStatus"
        >
          <LoaderCircle v-if="checking" class="spin" :size="16" aria-hidden="true" />
          <PlugZap v-else :size="16" aria-hidden="true" />
          {{ checking ? 'Checking' : 'Check status' }}
        </button>
      </div>

      <div class="form-grid">
        <label class="field field-wide">
          <span class="field-label">Sub-Store endpoint</span>
          <span class="secret-input">
            <KeyRound :size="15" aria-hidden="true" />
            <input
              v-model="endpoint"
              :type="revealEndpoint ? 'text' : 'password'"
              autocomplete="off"
              spellcheck="false"
              placeholder="Sub-Store URL with secret path"
              @keyup.enter="checkStatus"
            />
            <button
              class="icon-button"
              type="button"
              :aria-label="revealEndpoint ? 'Hide endpoint' : 'Reveal endpoint'"
              :title="revealEndpoint ? 'Hide endpoint' : 'Reveal endpoint'"
              @click="revealEndpoint = !revealEndpoint"
            >
              <EyeOff v-if="revealEndpoint" :size="16" aria-hidden="true" />
              <Eye v-else :size="16" aria-hidden="true" />
            </button>
          </span>
          <small v-if="endpoint && endpointState.error" class="field-error">{{ endpointState.error }}</small>
        </label>

        <label class="field">
          <span class="field-label">Collection</span>
          <input v-model="collection" type="text" autocomplete="off" spellcheck="false" />
          <small v-if="collectionError" class="field-error">{{ collectionError }}</small>
        </label>

        <label class="field">
          <span class="field-label">VPN identity</span>
          <input v-model="userId" type="text" autocomplete="off" spellcheck="false" placeholder="All eligible identities" />
        </label>
      </div>

      <div v-if="canManageEndpoint || endpointStatus?.has_saved_endpoint" class="endpoint-vault">
        <div class="vault-row">
          <template v-if="endpointStatus?.has_saved_endpoint">
            <span class="vault-saved"><LockKeyhole :size="13" aria-hidden="true" /> Saved endpoint: <strong class="mono">{{ endpointStatus.endpoint_hint || '(saved)' }}</strong><span v-if="endpointStatus.autosync" class="badge" data-tone="info">auto-sync on</span></span>
            <button class="button button-secondary button-compact" type="button" :disabled="usingSavedRef" @click="useSavedEndpoint">Use saved</button>
            <button v-if="canManageEndpoint" class="button button-secondary button-compact destructive" type="button" :disabled="savingEndpoint" @click="clearEndpoint">Clear</button>
          </template>
          <span v-else class="vault-note">The endpoint is kept for this session only — save it to the encrypted vault to reuse it and enable auto-sync.</span>
        </div>
        <div v-if="canManageEndpoint && !usingSavedRef" class="vault-row">
          <label class="toggle-field"><input v-model="autosync" type="checkbox" /><span>Auto-sync on vpn-core changes</span></label>
          <button class="button button-secondary button-compact" type="button" :disabled="savingEndpoint || !endpointState.value" @click="saveEndpoint">
            <LoaderCircle v-if="savingEndpoint" class="spin" :size="13" aria-hidden="true" />
            Save endpoint (encrypted)
          </button>
        </div>
        <div v-if="endpointStatus?.autosync_status" class="vault-row" aria-live="polite">
          <span class="vault-note">
            Last auto-sync:
            <span class="badge" :data-tone="endpointStatus.autosync_status.state === 'success' ? 'success' : endpointStatus.autosync_status.state === 'error' ? 'warning' : 'info'">
              {{ endpointStatus.autosync_status.state }}
            </span>
            <span v-if="endpointStatus.autosync_status.attempted_at" class="mono">{{ endpointStatus.autosync_status.attempted_at }}</span>
            <span v-if="endpointStatus.autosync_status.error">— {{ endpointStatus.autosync_status.error }}</span>
          </span>
        </div>
      </div>
    </section>

    <section class="operation" aria-labelledby="import-title">
      <div class="operation-copy">
        <h2 id="import-title">Managed import</h2>
        <p v-if="status?.reachable">Connected to <strong>{{ status.sub_name || collection }}</strong></p>
        <p v-else>Upsert the selected vpn-core links without replacing other Sub-Store collections.</p>
      </div>

      <div v-if="preview" class="preview-panel" aria-live="polite">
        <div class="preview-summary">
          <span>Would publish <strong>{{ preview.total_after }}</strong> links to <strong>{{ preview.sub_name }}</strong>:</span>
          <span class="badge" data-tone="info">+{{ preview.added_count }} new</span>
          <span class="badge" data-tone="neutral">{{ preview.unchanged_count }} unchanged</span>
          <span class="badge" :data-tone="preview.removed_count ? 'warning' : 'neutral'">-{{ preview.removed_count }} removed</span>
          <span v-if="!preview.exists" class="badge" data-tone="info">collection will be created</span>
        </div>
        <ul v-if="preview.added.length" class="preview-list"><li v-for="label in preview.added" :key="'a-' + label">+ {{ label }}</li></ul>
        <ul v-if="preview.removed.length" class="preview-list preview-removed"><li v-for="label in preview.removed" :key="'r-' + label">− {{ label }}</li></ul>
      </div>
      <div v-if="result" class="result" aria-live="polite">
        <CheckCircle2 :size="17" aria-hidden="true" />
        <span><strong>{{ result.pushed }}</strong> links published to <strong>{{ result.sub_name }}</strong></span>
      </div>
      <div v-else-if="error" class="result result-error" role="alert">
        <CircleAlert :size="17" aria-hidden="true" />
        <span>{{ error }}</span>
      </div>

      <div class="operation-action">
        <span v-if="init && !canImport" class="permission-note">
          <LockKeyhole :size="14" aria-hidden="true" />
          Administrator access required
        </span>
        <button
          v-if="canPreview"
          class="button button-secondary"
          type="button"
          :disabled="!readyForAction || previewing"
          @click="runPreview"
        >
          <LoaderCircle v-if="previewing" class="spin" :size="16" aria-hidden="true" />
          <Eye v-else :size="16" aria-hidden="true" />
          {{ previewing ? 'Previewing' : 'Preview changes' }}
        </button>
        <button
          class="button button-primary"
          type="button"
          :disabled="!readyForAction || !canImport"
          @click="runImport"
        >
          <LoaderCircle v-if="importing" class="spin" :size="16" aria-hidden="true" />
          <DownloadCloud v-else :size="16" aria-hidden="true" />
          {{ importing ? 'Importing' : 'Import now' }}
        </button>
      </div>
    </section>
  </main>
</template>
