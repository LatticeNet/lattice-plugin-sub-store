import { describe, expect, it } from "vitest";
import { ref } from "vue";

import type { BridgeClient } from "./bridge";
import type { HostContext } from "./host";
import { usePipelines } from "./usePipelines";

function makeHost(
  responses: Record<string, unknown>,
  failures: Record<string, Error> = {},
): { host: HostContext; calls: { service: string; method: string; payload: unknown }[] } {
  const calls: { service: string; method: string; payload: unknown }[] = [];
  const bridge = {
    call(service: string, method: string, payload: unknown) {
      calls.push({ service, method, payload });
      const key = `${service}/${method}`;
      if (failures[key]) return { promise: Promise.reject(failures[key]), cancel: () => {} };
      return { promise: Promise.resolve(responses[key]), cancel: () => {} };
    },
  } as unknown as BridgeClient;
  return {
    host: { bridge, init: ref(undefined), bootError: ref(""), available: () => true, resize: async () => {} },
    calls,
  };
}

const LIST = "latticenet.sub-store/engine/list_pipelines";
const SAVE = "latticenet.sub-store/engine/save_pipeline";
const DELETE = "latticenet.sub-store/engine/delete_pipeline";
const RUN = "latticenet.sub-store/engine/run_pipeline";

const sample = { id: "hk-daily", name: "HK daily", target: "Clash", operator_count: 2 };

describe("usePipelines", () => {
  it("loads records and flips to ready", async () => {
    const { host } = makeHost({ [LIST]: { records: [sample], count: 1 } });
    const pipes = usePipelines(host);
    await pipes.load();
    expect(pipes.state.value).toBe("ready");
    expect(pipes.items.value).toHaveLength(1);
    expect(pipes.items.value[0].id).toBe("hk-daily");
  });

  it("surfaces a redacted error on load failure", async () => {
    const { host } = makeHost({}, { [LIST]: new Error("kv read https://internal.example/x failed") });
    const pipes = usePipelines(host);
    await pipes.load();
    expect(pipes.state.value).toBe("error");
    expect(pipes.loadError.value).not.toContain("https://internal.example");
  });

  it("saves with name defaulted to id and reloads the canonical list", async () => {
    const { host, calls } = makeHost({
      [SAVE]: { id: "hk-daily", created: true, count: 1 },
      [LIST]: { records: [sample], count: 1 },
    });
    const pipes = usePipelines(host);
    pipes.state.value = "ready";
    const ok = await pipes.save({ id: "hk-daily", target: "Clash", operators: [{ type: "quick-sort" }] });
    expect(ok).toBe(true);
    expect(calls[0].payload).toEqual({ id: "hk-daily", name: "hk-daily", target: "Clash", operators: [{ type: "quick-sort" }] });
    expect(calls.map((call) => call.method)).toEqual(["save_pipeline", "list_pipelines"]);
    expect(pipes.items.value[0].id).toBe("hk-daily");
  });

  it("keeps the list intact when save fails", async () => {
    const { host } = makeHost({}, { [SAVE]: new Error("too many pipeline records: max 256") });
    const pipes = usePipelines(host);
    pipes.state.value = "ready";
    const ok = await pipes.save({ id: "x", target: "Clash" });
    expect(ok).toBe(false);
    expect(pipes.actionError.value).toContain("max 256");
  });

  it("deletes only after server confirmation", async () => {
    const { host } = makeHost({ [LIST]: { records: [sample], count: 1 }, [DELETE]: { id: "hk-daily", deleted: true, count: 0 } });
    const pipes = usePipelines(host);
    await pipes.load();
    expect(await pipes.remove("hk-daily")).toBe(true);
    expect(pipes.items.value).toHaveLength(0);
  });

  it("runs a pipeline over raw content", async () => {
    const result = { target: "Clash", source_node_count: 3, node_count: 2, output: "proxies: []", output_bytes: 12 };
    const { host, calls } = makeHost({ [RUN]: result });
    const pipes = usePipelines(host);
    const out = await pipes.run("hk-daily", "ss://a\nss://b");
    expect(out?.node_count).toBe(2);
    expect(calls[0].payload).toEqual({ id: "hk-daily", raw: "ss://a\nss://b" });
  });

  it("stays inert when the engine service is not declared", async () => {
    const { host, calls } = makeHost({});
    host.available = () => false;
    const pipes = usePipelines(host);
    await pipes.load();
    expect(await pipes.save({ id: "x", target: "Clash" })).toBe(false);
    expect(await pipes.run("x", "ss://a")).toBeUndefined();
    expect(calls).toHaveLength(0);
  });
});
