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
  bridge.init.then((value) => { init.value = value; }).catch((cause) => {
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
    </section>

    <section class="operation" aria-labelledby="import-title">
      <div class="operation-copy">
        <h2 id="import-title">Managed import</h2>
        <p v-if="status?.reachable">Connected to <strong>{{ status.sub_name || collection }}</strong></p>
        <p v-else>Upsert the selected vpn-core links without replacing other Sub-Store collections.</p>
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
