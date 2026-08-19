<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";

import { BridgeClient, type HostInit } from "@latticenet/plugin-bridge";
import type { MethodBinding } from "./client";
import { provideHost } from "./host";
import { safeErrorMessage } from "./subStoreModel";
import Shell from "./Shell.vue";

/**
 * The real entry: owns the one bridge instance and hands it to the shell.
 *
 * Everything visual lives in Shell.vue so the same screens can be mounted
 * against a fake host by `dev/`.
 */

const init = ref<HostInit>();
const bootError = ref("");

let bridge: BridgeClient | undefined;
try {
  bridge = new BridgeClient({
    window,
    expectedPluginId: "latticenet.sub-store",
    expectedRoutes: ["sub-store"],
    idPrefix: "substore",
  });
  bridge.init
    .then((value) => {
      init.value = value;
    })
    .catch((cause) => {
      bootError.value = safeErrorMessage(
        cause,
        "The console answered the handshake with a refusal and gave no reason.",
      );
    });
} catch (cause) {
  bootError.value = safeErrorMessage(
    cause,
    "This page could not open a channel to the console.",
  );
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

let observer: ResizeObserver | undefined;
onMounted(() => {
  observer = new ResizeObserver(() => {
    bridge?.resize(document.documentElement.scrollHeight);
  });
  observer.observe(document.body);
  void resize();
});

onBeforeUnmount(() => {
  observer?.disconnect();
  bridge?.dispose();
});
</script>

<template>
  <Shell />
</template>
