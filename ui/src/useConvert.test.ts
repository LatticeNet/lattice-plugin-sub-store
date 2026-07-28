import { describe, expect, it } from "vitest";
import { ref } from "vue";

import type { BridgeClient } from "@latticenet/plugin-bridge";

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

const CONVERT = "latticenet.sub-store/engine/convert";

const sample = {
  target: "Clash",
  source_node_count: 3,
  node_count: 2,
  output: "proxies: []",
  output_bytes: 12,
};

describe("useConvert", () => {
  it("converts raw content with target and operators", async () => {
    const { host, calls } = makeHost({ [CONVERT]: sample });
    const convert = useConvert(host);
    expect(await convert.produce("ss://a\nss://b", "Clash", [{ type: "quick-sort" }])).toBe(true);
    expect(calls[0].payload).toEqual({ raw: "ss://a\nss://b", target: "Clash", operators: [{ type: "quick-sort" }] });
    expect(convert.result.value?.node_count).toBe(2);
  });

  it("refuses empty input without firing", async () => {
    const { host, calls } = makeHost({ [CONVERT]: sample });
    const convert = useConvert(host);
    expect(await convert.produce("   ", "Clash")).toBe(false);
    expect(await convert.produce("ss://a", "")).toBe(false);
    expect(calls).toHaveLength(0);
  });

  // Literal sizes on purpose: deriving the input from the threshold makes the
  // assertion hold for ANY threshold — including 1.0, which would make the badge
  // unreachable again, the exact defect this warning replaced.
  it("warns when a result approaches the output ceiling (5.5 MiB of a 6 MiB ceiling)", async () => {
    const { host } = makeHost({ [CONVERT]: { ...sample, output_bytes: 5.5 * 1024 * 1024 } });
    const convert = useConvert(host);
    await convert.produce("ss://a", "Clash");
    expect(convert.resultNearBudget.value).toBe(true);
  });

  it("does not warn on an ordinary result of 1 MiB (a rendered result was never truncated)", async () => {
    const { host } = makeHost({ [CONVERT]: { ...sample, output_bytes: 1024 * 1024 } });
    const convert = useConvert(host);
    await convert.produce("ss://a", "Clash");
    expect(convert.resultNearBudget.value).toBe(false);
  });

  it("surfaces a redacted error on failure", async () => {
    const { host } = makeHost({}, { [CONVERT]: new Error("core panic at https://internal.example/x") });
    const convert = useConvert(host);
    expect(await convert.produce("ss://a", "Clash")).toBe(false);
    expect(convert.actionError.value).not.toContain("https://internal.example");
  });

  it("resets stale results", async () => {
    const { host } = makeHost({ [CONVERT]: sample });
    const convert = useConvert(host);
    await convert.produce("ss://a", "Clash");
    convert.reset();
    expect(convert.result.value).toBeUndefined();
  });

  it("stays inert when convert is not declared", async () => {
    const { host, calls } = makeHost({});
    host.available = () => false;
    const convert = useConvert(host);
    expect(await convert.produce("ss://a", "Clash")).toBe(false);
    expect(calls).toHaveLength(0);
  });
});
