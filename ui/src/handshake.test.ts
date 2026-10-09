import { ref, watch } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BridgeClient, type HostInit } from "@latticenet/plugin-bridge";
import { adoptHandshake } from "./host";
import { createStateSender, decodeShellState, type PageState } from "./pageState";

/**
 * App.vue's path from the console's handshake to the shell and back, on the
 * real bridge client: the page state the console's address held must be in
 * place when the shell reacts to init, and a state the shell sends must
 * reach the console as the contract's message. The dev harness mounts the
 * shell on a fake host, so without this nothing ran this path before a real
 * console did.
 */

const NONCE = "0123456789abcdef0123456789abcdef";
const HOST = "https://dash.example";

function frame() {
  const posted: { message: Record<string, unknown>; target: unknown }[] = [];
  let listener: ((event: MessageEvent) => void) | undefined;
  const parent = { postMessage: (message: Record<string, unknown>, target: unknown) => posted.push({ message, target }) };
  const win = {
    parent,
    location: { hash: `#lattice_nonce=${NONCE}&host_origin=${encodeURIComponent(HOST)}` },
    addEventListener: (_: string, next: (event: MessageEvent) => void) => {
      listener = next;
    },
    removeEventListener: () => undefined,
  } as unknown as Window;
  const client = new BridgeClient({ window: win, expectedPluginId: "latticenet.sub-store", expectedRoutes: ["sub-store"], idPrefix: "substore" });
  const init = (extra: Record<string, unknown> = {}) =>
    listener?.({
      source: parent,
      origin: HOST,
      data: {
        type: "lattice.host.init",
        nonce: NONCE,
        version: "1",
        pluginId: "latticenet.sub-store",
        pluginVersion: "0.16.0-alpha.1",
        pluginRoute: "sub-store",
        locale: "en",
        colorScheme: "dark",
        designTokens: {},
        interfaces: [{ service: "substore", methods: ["status"] }],
        ...extra,
      },
    } as unknown as MessageEvent);
  const sentStates = () => posted.filter((entry) => entry.message.type === "lattice.plugin.state");
  return { client, init, sentStates };
}

function targets() {
  return { init: ref<HostInit>(), pageState: ref<PageState>({}), bootError: ref("") };
}

afterEach(() => vi.useRealTimers());

describe("the handshake into the shell", () => {
  it("has the address's page state in place when the shell sees init", async () => {
    const { client, init } = frame();
    const host = targets();
    let seen: PageState | undefined;
    watch(host.init, () => {
      seen = { ...host.pageState.value };
    }, { flush: "sync" });
    const adopted = adoptHandshake(client, host);
    init({ pageState: { view: "files", open: "file_a", q: "hk" } });
    await adopted;
    expect(seen).toEqual({ view: "files", open: "file_a", q: "hk" });
    // The Files layer's old address lands on Records filtered to files.
    expect(decodeShellState(host.pageState.value)).toMatchObject({ view: "records", kind: "file", open: "file_a", q: "hk" });
    client.dispose();
  });

  it("opens on the defaults for a console that predates the contract, and drops the console's own keys", async () => {
    const old = frame();
    const oldHost = targets();
    oldHost.pageState.value = { view: "stale" };
    const adoptedOld = adoptHandshake(old.client, oldHost);
    old.init();
    await adoptedOld;
    expect(oldHost.pageState.value).toEqual({});
    expect(oldHost.init.value?.pluginRoute).toBe("sub-store");
    old.client.dispose();

    const reserved = frame();
    const reservedHost = targets();
    const adoptedReserved = adoptHandshake(reserved.client, reservedHost);
    reserved.init({ pageState: { view: "subscriptions", redirect: "/login", token: "x" } });
    await adoptedReserved;
    expect(reservedHost.pageState.value).toEqual({ view: "subscriptions" });
    reserved.client.dispose();
  });

  it("turns a handshake that never completes into the boot error", async () => {
    const { client } = frame();
    const host = targets();
    const adopted = adoptHandshake(client, host);
    client.dispose();
    await adopted;
    expect(host.init.value).toBeUndefined();
    expect(host.bootError.value).not.toBe("");
  });
});

describe("the shell's state back to the console", () => {
  it("reaches the console as lattice.plugin.state on the pinned origin, once, after init", async () => {
    vi.useFakeTimers();
    const { client, init, sentStates } = frame();
    const host = targets();
    const sender = createStateSender((state) => client.sendState(state));
    // Before the handshake the client sends nothing, whatever the shell pushes.
    sender.push({ view: "files" });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sentStates()).toHaveLength(0);

    const adopted = adoptHandshake(client, host);
    init({ pageState: { view: "files" } });
    await adopted;
    sender.seed(host.pageState.value);
    sender.push({ view: "files" });
    sender.push({ view: "files", open: "file_b" });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sentStates()).toEqual([
      { message: { type: "lattice.plugin.state", nonce: NONCE, state: { view: "files", open: "file_b" } }, target: HOST },
    ]);
    sender.dispose();
    client.dispose();
  });
});
