import { describe, expect, it } from "vitest";
import { ref } from "vue";

import type { BridgeClient } from "./bridge";
import { OUTPUT_SIZE_BUDGET_BYTES } from "./client";
import type { HostContext } from "./host";
import { useConvert } from "./useConvert";

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

const TARGETS_KEY = "latticenet.sub-store/convert/targets";
const PREVIEW_KEY = "latticenet.sub-store/convert/preview";
const RUN_KEY = "latticenet.sub-store/convert/convert";

const targets = [
  { id: "clash", label: "Clash", produces: "yaml" },
  { id: "sing-box", label: "sing-box", produces: "json" },
];

describe("useConvert", () => {
  it("loads targets and preselects the first", async () => {
    const { host } = makeHost({ [TARGETS_KEY]: { targets } });
    const convert = useConvert(host);
    await convert.loadTargets();
    expect(convert.targetsState.value).toBe("ready");
    expect(convert.targets.value).toHaveLength(2);
    expect(convert.targetId.value).toBe("clash");
  });

  it("reports target load failure without crashing selection", async () => {
    const { host } = makeHost({}, { [TARGETS_KEY]: new Error("engine busy") });
    const convert = useConvert(host);
    await convert.loadTargets();
    expect(convert.targetsState.value).toBe("error");
    expect(convert.targetsError.value).toBe("engine busy");
    expect(convert.canConvert.value).toBe(false);
  });

  it("requires a selection and a target before converting", async () => {
    const { host } = makeHost({ [TARGETS_KEY]: { targets } });
    const convert = useConvert(host);
    await convert.loadTargets();
    expect(convert.canConvert.value).toBe(false);
    convert.toggle("hk-main");
    expect(convert.canConvert.value).toBe(true);
    convert.toggle("hk-main");
    expect(convert.canConvert.value).toBe(false);
  });

  it("flags previews whose estimate crosses the output budget", async () => {
    const { host } = makeHost({
      [TARGETS_KEY]: { targets },
      [PREVIEW_KEY]: { node_count: 320, groups: ["Auto"], warnings: [], size_estimate_bytes: OUTPUT_SIZE_BUDGET_BYTES + 1 },
    });
    const convert = useConvert(host);
    await convert.loadTargets();
    convert.toggle("hk-main");
    expect(await convert.runPreview()).toBe(true);
    expect(convert.previewOverBudget.value).toBe(true);
  });

  it("produces output content with the chosen subscriptions", async () => {
    const { host, calls } = makeHost({
      [TARGETS_KEY]: { targets },
      [RUN_KEY]: { content: "proxies: []", content_type: "text/yaml", file_name: "lattice.yaml", size_bytes: 12 },
    });
    const convert = useConvert(host);
    await convert.loadTargets();
    convert.toggle("a");
    convert.toggle("b");
    expect(await convert.produce()).toBe(true);
    expect(convert.output.value?.file_name).toBe("lattice.yaml");
    expect(calls.at(-1)?.payload).toEqual({ subscriptions: ["a", "b"], target: "clash" });
  });

  it("clears stale results when the selection changes", async () => {
    const { host } = makeHost({
      [TARGETS_KEY]: { targets },
      [RUN_KEY]: { content: "x", content_type: "text/yaml", file_name: "f", size_bytes: 1 },
    });
    const convert = useConvert(host);
    await convert.loadTargets();
    convert.toggle("a");
    await convert.produce();
    convert.toggle("b");
    expect(convert.output.value).toBeUndefined();
  });

  it("stays inert when the convert service is not declared", async () => {
    const { host, calls } = makeHost({});
    host.available = () => false;
    const convert = useConvert(host);
    await convert.loadTargets();
    convert.toggle("a");
    expect(await convert.produce()).toBe(false);
    expect(calls).toHaveLength(0);
  });

  it("normalizes sparse preview and output responses", async () => {
    const { host } = makeHost({
      [TARGETS_KEY]: { targets },
      [PREVIEW_KEY]: { node_count: 5 },
      [RUN_KEY]: {},
    });
    const convert = useConvert(host);
    await convert.loadTargets();
    convert.toggle("a");
    expect(await convert.runPreview()).toBe(true);
    expect(convert.preview.value?.groups).toEqual([]);
    expect(convert.preview.value?.warnings).toEqual([]);
    expect(convert.preview.value?.size_estimate_bytes).toBe(0);
    expect(convert.previewOverBudget.value).toBe(false);
    expect(await convert.produce()).toBe(true);
    expect(convert.output.value?.content).toBe("");
    expect(convert.output.value?.file_name).toBe("sub-store-output.txt");
  });
});
