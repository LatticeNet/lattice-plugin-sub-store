import { inject, provide, type InjectionKey, type Ref } from "vue";

import { canCall, type BridgeClient, type HostInit } from "@latticenet/plugin-bridge";
import type { MethodBinding } from "./client";
import type { PageState } from "./pageState";
import { safeErrorMessage } from "./subStoreModel";

/**
 * Host context. The one bridge instance owned by the shell (App.vue), handed
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
  /**
   * The page state the console's address held at the handshake, set no later
   * than `init`. Empty from a console that predates the contract.
   */
  pageState: Ref<PageState>;
  /** Hand the page's full state to the console, which keeps it in its address. */
  sendState: (state: PageState) => void;
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

/** The host context's refs that the handshake fills. */
export type HandshakeTargets = Pick<HostContext, "init" | "pageState" | "bootError">;

/**
 * Hand the bridge's handshake to the host context, as App.vue does for the
 * real frame.
 *
 * The page state goes in before init: the shell reacts to init by applying
 * the state the console's address held, so the other order would open the
 * page on its defaults and then send those defaults back over the address
 * the operator reloaded. A console that predates the contract sends no
 * state and the page opens on its defaults. A refused handshake becomes the
 * boot error. Resolves once either has happened.
 */
export function adoptHandshake(bridge: Pick<BridgeClient, "init">, targets: HandshakeTargets): Promise<void> {
  return bridge.init.then(
    (value) => {
      targets.pageState.value = value.pageState ?? {};
      targets.init.value = value;
    },
    (cause: unknown) => {
      targets.bootError.value = safeErrorMessage(
        cause,
        "The console answered the handshake with a refusal and gave no reason.",
      );
    },
  );
}

export { canCall };
