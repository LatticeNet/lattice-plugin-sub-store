import { describe, expect, it } from "vitest";
import { ref } from "vue";
import type { BridgeClient } from "@latticenet/plugin-bridge";

import type { HostContext } from "./host";
import { useShares } from "./useShares";

function sharesHost(reply: () => Promise<unknown>, available = true) {
  let calls = 0;
  const bridge = {
    call() {
      calls += 1;
      return { promise: reply(), cancel: () => {} };
    },
  } as unknown as BridgeClient;
  const host: HostContext = {
    bridge,
    init: ref(undefined),
    bootError: ref(""),
    available: () => available,
    resize: async () => {},
  };
  return { host, count: () => calls };
}

describe("the shared share list", () => {
  it("is one copy per host, and a read in flight is joined rather than repeated", async () => {
    const { host, count } = sharesHost(() => Promise.resolve({ shares: [{ subscription_id: "a", share_id: "1", slug: "a", enabled: true, path: "/sub/a/t" }] }));
    const first = useShares(host);
    const second = useShares(host);
    expect(first.shares.value).toBeUndefined();
    await Promise.all([first.load(), second.load(), first.load()]);
    expect(count()).toBe(1);
    expect(second.shares.value).toHaveLength(1);
    expect(first.shares).toBe(second.shares);
  });

  it("reports a failed read without printing the link it was working on", async () => {
    const { host } = sharesHost(() => Promise.reject(new Error("fetch https://lattice.example/api?token=x refused")));
    const store = useShares(host);
    await store.load();
    expect(store.shares.value).toBeUndefined();
    expect(store.error.value).toBe("fetch [endpoint] refused");
    expect(store.loading.value).toBe(false);
  });

  it("stays unread when the bundle does not declare the method", async () => {
    const { host, count } = sharesHost(() => Promise.resolve({ shares: [] }), false);
    const store = useShares(host);
    await store.load();
    expect(count()).toBe(0);
    expect(store.available.value).toBe(false);
  });
});
