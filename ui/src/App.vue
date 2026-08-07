<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { ArrowLeftRight, CircleAlert, Library, Settings, Store, Workflow } from "@lucide/vue";

import { BridgeClient, type HostInit } from "@latticenet/plugin-bridge";
import type { MethodBinding } from "./client";
import { provideHost } from "./host";
import { safeErrorMessage } from "./subStoreModel";
import PipelinesScreen from "./screens/PipelinesScreen.vue";
import ConvertScreen from "./screens/ConvertScreen.vue";
import SubscriptionsScreen from "./screens/SubscriptionsScreen.vue";
import SettingsScreen from "./screens/SettingsScreen.vue";

const init = ref<HostInit>();
const bootError = ref("");

let bridge: BridgeClient | undefined;
try {
  bridge = new BridgeClient({ window, expectedPluginId: "latticenet.sub-store", expectedRoutes: ["sub-store"], idPrefix: "substore" });
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

type TabId = "subscriptions" | "pipelines" | "convert" | "settings";

// Subscriptions leads: it is what this plugin is for. The outbound Import tab
// is gone — it pushed nodes OUT to an external Sub-Store, the opposite of what
// this plugin now does, and sat next to a Settings action that pulled the other
// way with nothing to tell the two apart.
const tabs: { id: TabId; label: string; icon: unknown; screen: unknown }[] = [
  { id: "subscriptions", label: "Subscriptions", icon: Library, screen: SubscriptionsScreen },
  { id: "pipelines", label: "Pipelines", icon: Workflow, screen: PipelinesScreen },
  { id: "convert", label: "Convert", icon: ArrowLeftRight, screen: ConvertScreen },
  { id: "settings", label: "Settings", icon: Settings, screen: SettingsScreen },
];

const activeTab = ref<TabId>("subscriptions");
const activeScreen = computed(() => tabs.find((tab) => tab.id === activeTab.value)?.screen ?? SubscriptionsScreen);

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
        <p>Store subscriptions, process them, and publish them from Lattice itself.</p>
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
