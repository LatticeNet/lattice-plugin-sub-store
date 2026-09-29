<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";

import { BridgeClient, type HostInit } from "@latticenet/plugin-bridge";
import type { MethodBinding } from "./client";
import { provideHost } from "./host";
import { hostOriginFromHash } from "./navigate";
import { listenForInitPageState, stateMessage, type PageState } from "./pageState";
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
const pageState = ref<PageState>({});

let bridge: BridgeClient | undefined;
/** The origin the bridge pins; the state message goes nowhere else. */
const hostOrigin = hostOriginFromHash(window.location.hash);
const hashNonce = new URLSearchParams(window.location.hash.replace(/^#/, "")).get("lattice_nonce") ?? "";
/**
 * Registered before the client's own listener. The browser runs microtasks
 * between two listeners of one message, so a listener added after the client
 * would hear init only once the shell had already reacted to it, without the
 * page state.
 */
let stopPageState: (() => void) | undefined = hostOrigin && hashNonce
  ? listenForInitPageState(window, hashNonce, hostOrigin, (state) => {
      pageState.value = state;
    })
  : undefined;
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
  stopPageState?.();
  stopPageState = undefined;
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
  pageState,
  sendState: (state: PageState) => {
    if (!bridge || !hostOrigin) return;
    window.parent.postMessage(stateMessage(bridge.nonce, state), hostOrigin);
  },
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
  stopPageState?.();
  bridge?.dispose();
});
</script>

<template>
  <Shell />
</template>
