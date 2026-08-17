<script setup lang="ts">
import { computed, ref } from "vue";
import { ArrowLeftRight, CircleAlert, FileCode, Library, Settings, Store, Workflow } from "@lucide/vue";

import { useHandshakeTimeout } from "./handshakeTimeout";
import { useHost } from "./host";
import StandaloneNotice from "./components/StandaloneNotice.vue";
import PipelinesScreen from "./screens/PipelinesScreen.vue";
import ConvertScreen from "./screens/ConvertScreen.vue";
import SubscriptionsScreen from "./screens/SubscriptionsScreen.vue";
import FilesScreen from "./screens/FilesScreen.vue";
import SettingsScreen from "./screens/SettingsScreen.vue";

/**
 * The plugin's chrome and its tabs, with no knowledge of how the host is
 * reached.
 *
 * Split out of App.vue so the same screens can be mounted against a fake host
 * in `dev/`. Until that existed the UI had only ever been checked by building
 * it and reading the markup, which is how an operator picker that rendered
 * empty and a data load that never ran both reached production.
 */

const host = useHost();

/**
 * The handshake failed or never came. Outside the console that is permanent,
 * so the tabs — each of which would show "Loading…" forever — are replaced by
 * one honest notice. A handshake that lands late flips this back off.
 */
const handshakeExpired = useHandshakeTimeout(host.init);
const standalone = computed(
  () => !host.init.value && (handshakeExpired.value || !!host.bootError.value),
);

type TabId = "subscriptions" | "files" | "pipelines" | "convert" | "settings";

// Subscriptions leads: it is what this plugin is for.
const tabs: { id: TabId; label: string; icon: unknown; screen: unknown }[] = [
  { id: "subscriptions", label: "Subscriptions", icon: Library, screen: SubscriptionsScreen },
  { id: "files", label: "Files", icon: FileCode, screen: FilesScreen },
  { id: "pipelines", label: "Pipelines", icon: Workflow, screen: PipelinesScreen },
  { id: "convert", label: "Convert", icon: ArrowLeftRight, screen: ConvertScreen },
  { id: "settings", label: "Settings", icon: Settings, screen: SettingsScreen },
];

const activeTab = ref<TabId>("subscriptions");
const activeScreen = computed(
  () => tabs.find((tab) => tab.id === activeTab.value)?.screen ?? SubscriptionsScreen,
);
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

    <StandaloneNotice v-if="standalone" :detail="host.bootError.value" />

    <template v-else>
      <div v-if="host.bootError.value" class="alert" role="alert">
        <CircleAlert :size="17" aria-hidden="true" />
        <span>{{ host.bootError.value }}</span>
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
    </template>
  </main>
</template>
