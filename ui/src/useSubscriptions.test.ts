import { describe, expect, it } from "vitest";
import { ref } from "vue";

import type { BridgeClient } from "./bridge";
import type { HostContext } from "./host";
import { useSubscriptions } from "./useSubscriptions";

interface RecordedCall {
  service: string;
  method: string;
  payload: unknown;
}

function makeHost(
  responses: Record<string, unknown>,
  failures: Record<string, Error> = {},
): { host: HostContext; calls: RecordedCall[] } {
  const calls: RecordedCall[] = [];
  const bridge = {
    call(service: string, method: string, payload: unknown) {
      calls.push({ service, method, payload });
      const key = `${service}/${method}`;
      if (failures[key]) return { promise: Promise.reject(failures[key]), cancel: () => {} };
      return { promise: Promise.resolve(responses[key]), cancel: () => {} };
    },
  } as unknown as BridgeClient;
  return {
    host: {
      bridge,
      init: ref(undefined),
      bootError: ref(""),
      available: () => true,
      resize: async () => {},
    },
    calls,
  };
}

const LIST_KEY = "latticenet.sub-store/subscriptions/list";
const CREATE_KEY = "latticenet.sub-store/subscriptions/create";
const DELETE_KEY = "latticenet.sub-store/subscriptions/delete";
const REFRESH_KEY = "latticenet.sub-store/subscriptions/refresh";

const sample = {
  name: "hk-main",
  display_name: "HK main",
  source: "remote",
  url_hint: "provider.example …c123",
  node_count: 12,
};

describe("useSubscriptions", () => {
  it("loads the list and flips to ready", async () => {
    const { host } = makeHost({ [LIST_KEY]: { subscriptions: [sample] } });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(subs.state.value).toBe("ready");
    expect(subs.items.value).toHaveLength(1);
    expect(subs.items.value[0].name).toBe("hk-main");
  });

  it("surfaces a redacted error when loading fails", async () => {
    const { host } = makeHost({}, { [LIST_KEY]: new Error("fetch https://provider.example/sub failed") });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(subs.state.value).toBe("error");
    expect(subs.loadError.value).not.toContain("https://provider.example");
    expect(subs.loadError.value).toContain("[endpoint]");
  });

  it("creates with snake_case payload and omits an empty display name", async () => {
    const { host, calls } = makeHost({
      [CREATE_KEY]: { subscription: sample },
      [LIST_KEY]: { subscriptions: [] },
    });
    const subs = useSubscriptions(host);
    subs.state.value = "ready";
    const ok = await subs.create({ name: "hk-main", displayName: "", sourceUrl: "https://provider.example/sub?token=x" });
    expect(ok).toBe(true);
    expect(calls[0].payload).toEqual({ name: "hk-main", display_name: undefined, source_url: "https://provider.example/sub?token=x" });
    expect(subs.items.value[0].name).toBe("hk-main");
  });

  it("keeps the list intact when creation fails", async () => {
    const { host } = makeHost({}, { [CREATE_KEY]: new Error("duplicate name") });
    const subs = useSubscriptions(host);
    subs.state.value = "ready";
    const ok = await subs.create({ name: "dup", sourceUrl: "https://provider.example/sub" });
    expect(ok).toBe(false);
    expect(subs.items.value).toHaveLength(0);
    expect(subs.actionError.value).toBe("duplicate name");
  });

  it("removes a row only after the server confirms", async () => {
    const { host } = makeHost({ [LIST_KEY]: { subscriptions: [sample] }, [DELETE_KEY]: { ok: true } });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.remove("hk-main")).toBe(true);
    expect(subs.items.value).toHaveLength(0);
  });

  it("re-fetches the list after a successful refresh", async () => {
    const { host, calls } = makeHost({
      [LIST_KEY]: { subscriptions: [sample] },
      [REFRESH_KEY]: { node_count: 14, changed: true },
    });
    const subs = useSubscriptions(host);
    subs.state.value = "ready";
    subs.items.value = [sample];
    const result = await subs.refresh("hk-main");
    expect(result?.node_count).toBe(14);
    const methods = calls.map((call) => call.method);
    expect(methods).toEqual(["refresh", "list"]);
  });

  it("does not fire when the manifest does not declare the service", async () => {
    const { host, calls } = makeHost({});
    host.available = () => false;
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.create({ name: "x", sourceUrl: "https://a.example/b" })).toBe(false);
    expect(calls).toHaveLength(0);
  });
});
