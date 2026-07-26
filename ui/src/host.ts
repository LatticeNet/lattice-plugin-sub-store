import { inject, provide, type InjectionKey, type Ref } from "vue";

import { canCall, type BridgeClient, type HostInit } from "./bridge";
import type { MethodBinding } from "./client";

/**
 * Host context — the one bridge instance owned by the shell (App.vue), handed
 * to screens via provide/inject so no screen constructs its own BridgeClient
 * (a second client would double the ready handshake and split pending calls).
 */
export interface HostContext {
  bridge: BridgeClient | undefined;
  init: Ref<HostInit | undefined>;
  bootError: Ref<string>;
  /** True when the signed manifest declares the binding's service/method. */
  available: (target: MethodBinding) => boolean;
  /** Re-measure the document and tell the host to fit the frame. */
  resize: () => Promise<void>;
}

const HOST_KEY: InjectionKey<HostContext> = Symbol("lattice-plugin-host");

export function provideHost(context: HostContext): void {
  provide(HOST_KEY, context);
}

export function useHost(): HostContext {
  const context = inject(HOST_KEY);
  if (!context) throw new Error("host context used outside the plugin shell");
  return context;
}

export { canCall };
