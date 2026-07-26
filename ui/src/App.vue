<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { ArrowLeftRight, CircleAlert, DownloadCloud, Rss, Store } from "@lucide/vue";

import { BridgeClient, type HostInit } from "./bridge";
import type { MethodBinding } from "./client";
import { provideHost } from "./host";
import { safeErrorMessage } from "./subStoreModel";
import ImportScreen from "./screens/ImportScreen.vue";
import SubscriptionsScreen from "./screens/SubscriptionsScreen.vue";
import ConvertScreen from "./screens/ConvertScreen.vue";

const init = ref<HostInit>();
const bootError = ref("");

let bridge: BridgeClient | undefined;
try {
  bridge = new BridgeClient(window);
  bridge.init.then((value) => {
    init.value = value;
  }).catch((cause) => {
    bootError.value = safeErrorMessage(cause, "Plugin host unavailable");
  });
} catch (cause) {
  bootError.value = safeErrorMessage(cause, "Plugin host unavailable");
}

async function resize(): Promise<void> {
  await nextTick();
  bridge?.resize(document.documentElement.scrollHeight);
}

provideHost({
  bridge,
  init,
  bootError,
  available: (target: MethodBinding) =>
    init.value?.interfaces.some(
      (contract) => contract.service === target.service && contract.methods.includes(target.method),
    ) === true,
  resize,
});

type TabId = "import" | "subscriptions" | "convert";

const tabs: { id: TabId; label: string; icon: unknown; screen: unknown }[] = [
  { id: "import", label: "Import", icon: DownloadCloud, screen: ImportScreen },
  { id: "subscriptions", label: "Subscriptions", icon: Rss, screen: SubscriptionsScreen },
  { id: "convert", label: "Convert", icon: ArrowLeftRight, screen: ConvertScreen },
];

const activeTab = ref<TabId>("import");
const activeScreen = computed(() => tabs.find((tab) => tab.id === activeTab.value)?.screen ?? ImportScreen);

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
        <p>Import vpn-core lines, manage subscriptions, and convert configurations.</p>
      </div>
    </header>

    <div v-if="bootError" class="alert" role="alert">
      <CircleAlert :size="17" aria-hidden="true" />
      <span>{{ bootError }}</span>
    </div>

    <nav class="tab-bar" role="tablist" aria-label="Sub-Store sections">
      <button
        v-for="tab in tabs"
        :id="`tab-${tab.id}`"
        :key="tab.id"
        class="tab"
        type="button"
        role="tab"
        :aria-selected="activeTab === tab.id"
        :aria-controls="`panel-${tab.id}`"
        :data-active="activeTab === tab.id"
        @click="activeTab = tab.id"
      >
        <component :is="tab.icon" :size="15" aria-hidden="true" />
        {{ tab.label }}
      </button>
    </nav>

    <KeepAlive>
      <component
        :is="activeScreen"
        :id="`panel-${activeTab}`"
        role="tabpanel"
        :aria-labelledby="`tab-${activeTab}`"
      />
    </KeepAlive>
  </main>
</template>
