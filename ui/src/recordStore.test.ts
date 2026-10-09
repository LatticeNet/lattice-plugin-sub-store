import { describe, expect, it } from "vitest";
import { ref } from "vue";
import type { BridgeClient } from "@latticenet/plugin-bridge";

import { BINDINGS, ERROR_REGEX_INCOMPATIBLE, ERROR_STORE_MIGRATION_REQUIRED, errorCodeOf, type SubscriptionListItem } from "./client";
import type { HostContext } from "./host";
import { emptyDraft, useSubscriptions } from "./useSubscriptions";

/**
 * The store split's writes as the Records screen drives them: the order, the
 * migration, and the refusals a split store answers with. The host answers
 * each method from a queue the test fills, so a refusal can follow a success.
 */
type Answer = unknown | Error | (() => unknown);

function storeHost(answers: Record<string, Answer[]>, withheld: readonly string[] = []) {
  const calls: { method: string; payload: unknown }[] = [];
  const pending: Record<string, ((value: unknown) => void)[]> = {};
  const bridge = {
    call(_service: string, method: string, payload: unknown) {
      calls.push({ method, payload: structuredClone(payload) });
      const queue = answers[method] ?? [];
      const answer = queue.length > 1 ? queue.shift() : queue[0];
      if (answer === "hold") {
        return { promise: new Promise((resolve) => (pending[method] ??= []).push(resolve)), cancel: () => {} };
      }
      const value = typeof answer === "function" ? (answer as () => unknown)() : answer;
      return { promise: value instanceof Error ? Promise.reject(value) : Promise.resolve(value), cancel: () => {} };
    },
  } as unknown as BridgeClient;
  const host: HostContext = {
    bridge,
    init: ref(undefined),
    bootError: ref(""),
    available: (binding) => !withheld.includes(binding.method),
    resize: async () => {},
    pageState: ref({}),
    sendState: () => {},
  };
  return { host, calls, release: (method: string, value: unknown) => pending[method]?.shift()?.(value) };
}

function item(id: string, order: number): SubscriptionListItem {
  return { id, kind: "sub", name: id, has_url: false, has_inline_content: true, step_count: 0, disabled_step_count: 0, imported: false, order };
}

const LISTED = { subscriptions: [item("c", 2), item("a", 0), item("b", 1)], store_version: 2, order: "manual" };
const ids = (subs: ReturnType<typeof useSubscriptions>) => subs.items.value.map((entry) => entry.id);

describe("refusal codes", () => {
  it("are read from the bridge's code or from the message's leading token, and nothing else", () => {
    expect(errorCodeOf(new Error("store_migration_required: the subscription store still holds the legacy single document"))).toBe(ERROR_STORE_MIGRATION_REQUIRED);
    expect(errorCodeOf(Object.assign(new Error("refused"), { code: ERROR_REGEX_INCOMPATIBLE }))).toBe(ERROR_REGEX_INCOMPATIBLE);
    expect(errorCodeOf(Object.assign(new Error("refused"), { code: "denied" }))).toBe("");
    expect(errorCodeOf(new Error("provider returned status 503: store_migration_required"))).toBe("");
    expect(errorCodeOf(new Error("subscription_id is required"))).toBe("");
    expect(errorCodeOf("store_migration_required: as a string")).toBe("");
  });
});

describe("the catalogue of a split store", () => {
  it("keeps the rows in the store's order and the store's version", async () => {
    const { host } = storeHost({ list: [LISTED] });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(ids(subs)).toEqual(["a", "b", "c"]);
    expect(subs.storeVersion.value).toBe(2);
  });

  it("knows a runtime from before the split by the version it does not send", async () => {
    const { host } = storeHost({ list: [{ subscriptions: [item("a", 0)] }] });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(subs.storeVersion.value).toBeUndefined();
  });
});

describe("reordering", () => {
  it("shows the new order at once and sends every id once, in it", async () => {
    const { host, calls } = storeHost({ list: [LISTED], reorder: [{ reordered: true, count: 3 }] });
    const subs = useSubscriptions(host);
    await subs.load();
    const pending = subs.reorder(["c", "a", "b"]);
    expect(ids(subs)).toEqual(["c", "a", "b"]);
    expect(subs.items.value.map((entry) => entry.order)).toEqual([0, 1, 2]);
    expect(await pending).toEqual({ ok: true, reason: "", dropped: false });
    expect(calls.filter((call) => call.method === "reorder").map((call) => call.payload)).toEqual([{ ids: ["c", "a", "b"] }]);
  });

  it("sends moves one at a time, in the order they were made", async () => {
    const { host, calls, release } = storeHost({ list: [LISTED], reorder: ["hold"] });
    const subs = useSubscriptions(host);
    await subs.load();
    const first = subs.reorder(["b", "a", "c"]);
    const second = subs.reorder(["b", "c", "a"]);
    await Promise.resolve();
    expect(calls.filter((call) => call.method === "reorder")).toHaveLength(1);
    release("reorder", { reordered: true, count: 3 });
    await first;
    await Promise.resolve();
    release("reorder", { reordered: true, count: 3 });
    await second;
    expect(calls.filter((call) => call.method === "reorder").map((call) => call.payload)).toEqual([{ ids: ["b", "a", "c"] }, { ids: ["b", "c", "a"] }]);
    expect(ids(subs)).toEqual(["b", "c", "a"]);
  });

  it("puts back the order the store last accepted when one is refused, and drops the moves made on top of it", async () => {
    const { host, calls } = storeHost({
      list: [LISTED],
      reorder: [{ reordered: true, count: 3 }, new Error("the index changed under the reorder"), { reordered: true, count: 3 }],
    });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.reorder(["b", "a", "c"])).toMatchObject({ ok: true });
    const refused = subs.reorder(["b", "c", "a"]);
    const onTop = subs.reorder(["c", "b", "a"]);
    expect(await refused).toEqual({ ok: false, reason: "the index changed under the reorder", dropped: false });
    expect(await onTop).toEqual({ ok: false, reason: "", dropped: true });
    expect(ids(subs)).toEqual(["b", "a", "c"]);
    expect(calls.filter((call) => call.method === "reorder")).toHaveLength(2);
  });

  it("marks the store legacy when the refusal says it has not been migrated", async () => {
    const { host } = storeHost({ list: [LISTED], reorder: [new Error("store_migration_required: the subscription store still holds the legacy single document")] });
    const subs = useSubscriptions(host);
    await subs.load();
    const result = await subs.reorder(["c", "b", "a"]);
    expect(result.ok).toBe(false);
    expect(result.reason).toMatch(/one document/);
    expect(subs.storeVersion.value).toBe(1);
    expect(ids(subs)).toEqual(["a", "b", "c"]);
  });

  it("is not offered while the signed plugin does not declare it, or to a session that cannot write", async () => {
    for (const withheld of [["reorder"], ["save"]]) {
      const { host, calls } = storeHost({ list: [LISTED] }, withheld);
      const subs = useSubscriptions(host);
      await subs.load();
      expect(subs.canReorder.value).toBe(false);
      expect((await subs.reorder(["c", "b", "a"])).ok).toBe(false);
      expect(ids(subs)).toEqual(["a", "b", "c"]);
      expect(calls.some((call) => call.method === "reorder")).toBe(false);
    }
  });
});

describe("migrating a legacy store", () => {
  it("runs chunk after chunk until the runtime says done, then reads the split store", async () => {
    const { host, calls } = storeHost({
      list: [{ ...LISTED, store_version: 1 }, LISTED],
      migrate_store: [
        { migrated: 64, remaining: 236, done: false },
        { migrated: 64, remaining: 172, done: false },
        { migrated: 64, remaining: 108, done: false },
        { migrated: 64, remaining: 44, done: false },
        { migrated: 44, remaining: 0, done: true, verified: true, store_version: 2 },
      ],
    });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(subs.storeVersion.value).toBe(1);
    expect(await subs.migrateStore()).toBe(true);
    const chunks = calls.filter((call) => call.method === "migrate_store");
    expect(chunks).toHaveLength(5);
    expect(chunks.every((call) => (call.payload as { chunk: number }).chunk === 64)).toBe(true);
    expect(subs.migration.value).toMatchObject({ running: false, migrated: 300, remaining: 0, done: true, error: "" });
    expect(subs.storeVersion.value).toBe(2);
  });

  it("stops on a chunk that moves nothing while records remain, and says so", async () => {
    const { host, calls } = storeHost({ list: [{ ...LISTED, store_version: 1 }], migrate_store: [{ migrated: 64, remaining: 40, done: false }, { migrated: 0, remaining: 40, done: false }] });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.migrateStore()).toBe(false);
    expect(calls.filter((call) => call.method === "migrate_store")).toHaveLength(2);
    expect(subs.migration.value.error).toMatch(/moved nothing while 40 records remain/);
  });

  it("is not run where the signed plugin does not declare it", async () => {
    const { host, calls } = storeHost({ list: [{ ...LISTED, store_version: 1 }] }, ["migrate_store"]);
    const subs = useSubscriptions(host);
    await subs.load();
    expect(subs.canMigrateStore.value).toBe(false);
    expect(await subs.migrateStore()).toBe(false);
    expect(calls.some((call) => call.method === "migrate_store")).toBe(false);
  });
});

describe("a save the split store refuses", () => {
  const draft = () => ({
    ...emptyDraft(),
    id: "p",
    name: "provider",
    source: "local",
    content: "trojan://x@y:443#z",
    process: [{ type: "Regex Filter", args: { regex: ["^(?!.*(过期|官网)).*$"], keep: true } }],
  });

  it("keeps the regex refusal with the step and the rewrite, read from the draft", async () => {
    const { host } = storeHost({ list: [LISTED], save: [new Error("regex_incompatible: step 1 pattern needs lookaround")] });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.save(draft())).toBe(false);
    const message = "Not saved: step 1 uses lookaround or a backreference, which the native engine cannot run.";
    expect(subs.saveRefusal.value).toEqual({
      code: ERROR_REGEX_INCOMPATIBLE,
      diagnostics: [{ step: 1, type: "Regex Filter", pattern: "^(?!.*(过期|官网)).*$", rewrite: "过期|官网" }],
      message,
    });
    expect(subs.actionError.value).toBe(message);
    // The next attempt starts clean, and leaving the editor clears it too.
    subs.clearErrors();
    expect(subs.saveRefusal.value).toBeNull();
  });

  it("withdraws its own message beside Save once the chain is fixed, and nobody else's", async () => {
    const { host } = storeHost({ list: [LISTED], save: [new Error("regex_incompatible: step 1 pattern needs lookaround")] });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.save(draft())).toBe(false);
    subs.settleRefusal();
    expect(subs.actionError.value).toBe("");
    // The refusal itself stays, so the editor can say the chain is fixed and to save again.
    expect(subs.saveRefusal.value?.code).toBe(ERROR_REGEX_INCOMPATIBLE);

    expect(await subs.save(draft())).toBe(false);
    subs.actionError.value = "The preview could not run.";
    subs.settleRefusal();
    expect(subs.actionError.value).toBe("The preview could not run.");
  });

  it("names the migration rather than the raw code when the store is still legacy", async () => {
    const { host } = storeHost({ list: [LISTED], save: [new Error("store_migration_required: the subscription store still holds the legacy single document")] });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.save(draft())).toBe(false);
    expect(subs.saveRefusal.value).toBeNull();
    expect(subs.actionError.value).toMatch(/Migrate it from the Records table/);
    expect(subs.storeVersion.value).toBe(1);
  });

  it("still names an unrelated refusal as it was", async () => {
    const { host } = storeHost({ list: [LISTED], save: [new Error("subscription records are limited to 256")] });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.save(draft())).toBe(false);
    expect(subs.saveRefusal.value).toBeNull();
    expect(subs.actionError.value).toBe("subscription records are limited to 256");
  });
});

describe("the store split's bindings", () => {
  it("are active now the 0.17 manifest declares them", () => {
    for (const binding of [BINDINGS.subRestore, BINDINGS.subPurge, BINDINGS.subReorder, BINDINGS.subMigrateStore, BINDINGS.subDependsOn]) {
      expect(binding.status, binding.method).toBe("active");
    }
  });
});
