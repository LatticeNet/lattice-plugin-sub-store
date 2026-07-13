import { describe, expect, it, vi } from "vitest";

import { BridgeClient, canCall } from "./bridge";

function harness() {
  const posted: unknown[] = [];
  let listener: ((event: MessageEvent) => void) | undefined;
  const parent = { postMessage: (message: unknown) => posted.push(message) };
  const win = {
    parent,
    location: { hash: "#lattice_nonce=0123456789abcdef0123456789abcdef" },
    addEventListener: (_name: string, next: (event: MessageEvent) => void) => { listener = next; },
    removeEventListener: vi.fn(),
  } as unknown as Window;
  const dispatch = (data: unknown, source: unknown = parent) => listener?.({ data, source } as MessageEvent);
  return { win, parent, posted, dispatch };
}

describe("BridgeClient", () => {
  it("propagates the fragment nonce and accepts init only from the parent", async () => {
    vi.useFakeTimers();
    const { win, parent, posted, dispatch } = harness();
    const client = new BridgeClient(win);
    expect(posted[0]).toEqual({ type: "lattice.plugin.ready", nonce: client.nonce });
    await vi.advanceTimersByTimeAsync(500);
    expect(posted.filter((message) => (message as { type?: string }).type === "lattice.plugin.ready")).toHaveLength(2);
    const init = {
      type: "lattice.host.init", nonce: client.nonce, version: "1",
      pluginId: "latticenet.sub-store", pluginVersion: "0.3.2-alpha.2", pluginRoute: "sub-store",
      locale: "en", colorScheme: "dark", designTokens: {},
      interfaces: [{ service: "latticenet.sub-store/import", methods: ["status"] }],
    };
    dispatch(init, {});
    dispatch({ ...init, nonce: "wrong" }, parent);
    dispatch({ ...init, pluginId: "other.plugin" }, parent);
    dispatch(init, parent);
    const resolved = await client.init;
    await vi.advanceTimersByTimeAsync(1_000);
    expect(posted.filter((message) => (message as { type?: string }).type === "lattice.plugin.ready")).toHaveLength(2);
    expect(canCall(resolved, "latticenet.sub-store/import", "status")).toBe(true);
    expect(canCall(resolved, "latticenet.sub-store/import", "import")).toBe(false);
    client.dispose();
    vi.useRealTimers();
  });

  it("routes exact service/method calls and resolves structured results", async () => {
    const { win, posted, dispatch } = harness();
    const client = new BridgeClient(win);
    const request = client.call<{ reachable: boolean }>("latticenet.sub-store/import", "status", { base_url: "x" });
    const call = posted.at(-1) as { id: string; service: string; method: string; payload: unknown; nonce: string };
    expect(call.service).toBe("latticenet.sub-store/import");
    expect(call.method).toBe("status");
    dispatch({ type: "lattice.host.result", nonce: call.nonce, id: call.id, result: { reachable: true } });
    await expect(request.promise).resolves.toEqual({ reachable: true });
  });

  it("stops ready retries and rejects all work when host initialization fails", async () => {
    vi.useFakeTimers();
    const { win, posted, dispatch } = harness();
    const client = new BridgeClient(win);
    const request = client.call("latticenet.sub-store/import", "status", { base_url: "x" });

    dispatch({ type: "lattice.host.error", nonce: client.nonce, code: "denied", message: "Initialization denied" });

    await expect(client.init).rejects.toThrow("Initialization denied");
    await expect(request.promise).rejects.toThrow("Initialization denied");
    await vi.advanceTimersByTimeAsync(1_000);
    expect(posted.filter((message) => (message as { type?: string }).type === "lattice.plugin.ready")).toHaveLength(1);
    expect(() => client.call("latticenet.sub-store/import", "status", {})).toThrow("disposed");
    vi.useRealTimers();
  });

  it("routes errors, cancellation, timeout and disposal exactly once", async () => {
    vi.useFakeTimers();
    const { win, posted, dispatch } = harness();
    const client = new BridgeClient(win);
    const failed = client.call("svc", "method", null);
    const failedCall = posted.at(-1) as { id: string; nonce: string };
    dispatch({ type: "lattice.host.error", nonce: failedCall.nonce, id: failedCall.id, code: "denied", message: "Forbidden" });
    await expect(failed.promise).rejects.toThrow("Forbidden");

    const cancelled = client.call("svc", "method", null);
    cancelled.cancel();
    await expect(cancelled.promise).rejects.toThrow("cancelled");
    expect((posted.at(-1) as { type?: string }).type).toBe("lattice.plugin.cancel");

    const timedOut = client.call("svc", "method", null, 5);
    await vi.advanceTimersByTimeAsync(5);
    await expect(timedOut.promise).rejects.toThrow("timed out");

    const disposed = client.call("svc", "method", null);
    client.dispose();
    await expect(disposed.promise).rejects.toThrow("disconnected");
    vi.useRealTimers();
  });
});
