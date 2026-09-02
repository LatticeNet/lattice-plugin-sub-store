import { describe, expect, it } from "vitest";

import { createNodeCountQueue, nodeCountLabel, nodeCountTitle, type NodeCountReply } from "./nodeCounts";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (cause: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) await Promise.resolve();
}

describe("the node-count queue", () => {
  it("keeps two previews in flight and starts the rest as they answer", async () => {
    const pending = new Map<string, ReturnType<typeof deferred<NodeCountReply>>>();
    const started: string[] = [];
    const queue = createNodeCountQueue((id) => {
      started.push(id);
      const gate = deferred<NodeCountReply>();
      pending.set(id, gate);
      return gate.promise;
    });

    queue.request(["a", "b", "c", "d"]);
    expect(started).toEqual(["a", "b"]);
    expect(queue.stateOf("c")).toEqual({ status: "queued" });

    pending.get("a")!.resolve({ source_node_count: 48, node_count: 31 });
    await settle();
    expect(started).toEqual(["a", "b", "c"]);
    expect(queue.stateOf("a")).toMatchObject({ status: "ready", source: 48, result: 31 });

    pending.get("b")!.reject(new Error("provider returned status 503"));
    await settle();
    expect(started).toEqual(["a", "b", "c", "d"]);
    expect(queue.stateOf("b")).toMatchObject({ status: "failed", reason: "provider returned status 503" });
  });

  it("counts a record once per session and again after it is forgotten", async () => {
    let runs = 0;
    const queue = createNodeCountQueue(async () => {
      runs += 1;
      return { source_node_count: 8, node_count: 8 };
    });
    queue.request(["a"]);
    queue.request(["a"]);
    await settle();
    expect(runs).toBe(1);
    queue.request(["a"]);
    await settle();
    expect(runs).toBe(1);
    queue.forget("a");
    queue.request(["a"]);
    await settle();
    expect(runs).toBe(2);
  });

  it("adopts a count another run produced instead of fetching it again", async () => {
    let runs = 0;
    const queue = createNodeCountQueue(async () => {
      runs += 1;
      return { node_count: 1 };
    });
    queue.record("a", { source_node_count: 22, node_count: 22 });
    queue.request(["a"]);
    await settle();
    expect(runs).toBe(0);
    expect(nodeCountLabel(queue.stateOf("a"))).toBe("22 → 22");
  });

  it("drops an answer for a record forgotten while it was counting", async () => {
    const gate = deferred<NodeCountReply>();
    const queue = createNodeCountQueue(() => gate.promise);
    queue.request(["a"]);
    queue.forget("a");
    gate.resolve({ node_count: 3 });
    await settle();
    expect(queue.stateOf("a")).toBeUndefined();
  });
});

describe("what the cell says", () => {
  const now = Date.parse("2026-09-02T04:00:00Z");

  it("prints a question mark until something has answered", () => {
    expect(nodeCountLabel(undefined)).toBe("?");
    expect(nodeCountLabel({ status: "queued" })).toBe("…");
    expect(nodeCountLabel({ status: "failed", reason: "x", at: now })).toBe("?");
    expect(nodeCountTitle(undefined, true)).toBe("Not counted yet.");
    expect(nodeCountTitle(undefined, false)).toContain("cannot run a preview");
  });

  it("names the run the number came from and when", () => {
    const state = { status: "ready" as const, source: 48, result: 31, at: now - 3 * 3600 * 1000 };
    expect(nodeCountLabel(state)).toBe("48 → 31");
    expect(nodeCountTitle(state, true, now)).toBe("48 in, 31 out, from a preview run 3h ago.");
    expect(nodeCountTitle({ status: "failed", reason: "provider returned status 503", at: now - 60_000 }, true, now))
      .toBe("The preview run 1m ago failed: provider returned status 503");
  });
});
