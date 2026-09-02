import { describe, expect, it } from "vitest";

import { describeDelta, enabledStepIndexes, explainChain, foldRuns, nodeKey, stepDeltas, stepLabelOf } from "./chainExplain";
import type { SubscriptionPreviewResponse } from "./client";
import type { ChainStep } from "./components/ProcessChain.vue";

const steps: ChainStep[] = [
  { type: "Region Filter" },
  { type: "Handle Duplicate", disabled: true },
  { type: "Regex Rename Operator", customName: "tidy names" },
  { type: "Append Subscription" },
];

describe("chain explanation", () => {
  it("runs only the enabled operations, in chain order, and labels them by position", () => {
    expect(enabledStepIndexes(steps)).toEqual([0, 2, 3]);
    expect(stepLabelOf(steps[2], 2)).toBe("3. tidy names");
    // The catalogue's wording for a known type, the raw type for an unknown one.
    expect(stepLabelOf(steps[0], 0)).toBe("1. Region filter");
    expect(stepLabelOf(steps[3], 3)).toBe("4. Append Subscription");
  });

  it("folds the counts after each operation into before and after pairs", () => {
    const deltas = stepDeltas(steps, 48, [31, 31, 43]);
    expect(deltas.map((d) => [d.index, d.before, d.after])).toEqual([[0, 48, 31], [2, 31, 31], [3, 31, 43]]);
    expect(deltas.map(describeDelta)).toEqual([
      "1. Region filter: kept 31 of 48",
      "3. tidy names: 31 nodes, none removed",
      "4. Append Subscription: 31 became 43",
    ]);
  });

  it("stops where the run stopped", () => {
    expect(stepDeltas(steps, 48, [31])).toHaveLength(1);
    expect(stepDeltas(steps, 48, [])).toEqual([]);
  });
});

function reply(nodes: string[], dropped: string[], source: number): SubscriptionPreviewResponse {
  return {
    nodes: nodes.map((name) => ({ name, type: "vless", server: `${name}.example`, port: "443" })),
    dropped: dropped.map((name) => ({ name, type: "vless", server: `${name}.example`, port: "443" })),
    node_count: nodes.length,
    dropped_count: dropped.length,
    source_node_count: source,
  };
}

describe("running the chain one operation at a time", () => {
  it("names the operation that first removed each node and keeps the last run as the result", async () => {
    const asked: number[] = [];
    const answers: Record<number, SubscriptionPreviewResponse> = {
      0: reply(["hk", "jp", "sg"], ["us"], 4),
      2: reply(["hk", "jp", "sg"], ["us"], 4),
      3: reply(["hk", "jp"], ["us", "sg"], 4),
    };
    const explanation = await explainChain(steps, async (upTo) => {
      asked.push(upTo);
      return answers[upTo]!;
    });
    expect(asked).toEqual([0, 2, 3]);
    expect(explanation.complete).toBe(true);
    expect(explanation.final).toBe(answers[3]);
    expect(explanation.deltas.map(describeDelta)).toEqual([
      "1. Region filter: kept 3 of 4",
      "3. tidy names: 3 nodes, none removed",
      "4. Append Subscription: kept 2 of 3",
    ]);
    expect(explanation.droppedBy.get("us.example:443")).toBe("1. Region filter");
    expect(explanation.droppedBy.get("sg.example:443")).toBe("4. Append Subscription");
  });

  it("ends where a run fails and says the account is partial", async () => {
    const explanation = await explainChain(steps, async (upTo) => {
      if (upTo === 2) throw new Error("engine timed out");
      return reply(["hk"], [], 1);
    });
    expect(explanation.complete).toBe(false);
    expect(explanation.deltas).toHaveLength(1);
    expect(explanation.final?.node_count).toBe(1);
  });

  it("keys a node by endpoint, and by its pre-chain name without one", () => {
    expect(nodeKey({ name: "x", type: "ss", server: "a.example", port: "8388" })).toBe("a.example:8388");
    expect(nodeKey({ name: "x", type: "ss", server: "a.example" })).toBe("a.example");
    expect(nodeKey({ name: "renamed", type: "ss", was: "hk-01" })).toBe("hk-01");
    expect(nodeKey({ name: "plain", type: "ss" })).toBe("plain");
    expect(foldRuns(steps, [], false)).toEqual({ deltas: [], droppedBy: new Map(), final: null, complete: false });
  });
});
