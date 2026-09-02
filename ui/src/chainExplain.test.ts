import { describe, expect, it } from "vitest";

import { describeDelta, enabledStepIndexes, stepDeltas, stepLabelOf } from "./chainExplain";
import type { ChainStep } from "./components/ProcessChain.vue";

const steps: ChainStep[] = [
  { type: "Region Filter" },
  { type: "Handle Duplicate", disabled: true },
  { type: "Regex Rename", customName: "tidy names" },
  { type: "Append Subscription" },
];

describe("chain explanation", () => {
  it("runs only the enabled steps, in chain order, and labels them by position", () => {
    expect(enabledStepIndexes(steps)).toEqual([0, 2, 3]);
    expect(stepLabelOf(steps[2], 2)).toBe("3. tidy names");
    expect(stepLabelOf(steps[0], 0)).toBe("1. Region Filter");
  });

  it("folds the counts after each step into before and after pairs", () => {
    const deltas = stepDeltas(steps, 48, [31, 31, 43]);
    expect(deltas.map((d) => [d.index, d.before, d.after])).toEqual([[0, 48, 31], [2, 31, 31], [3, 31, 43]]);
    expect(deltas.map(describeDelta)).toEqual([
      "1. Region Filter: kept 31 of 48",
      "3. tidy names: 31 nodes, none removed",
      "4. Append Subscription: 31 became 43",
    ]);
  });

  it("stops where the run stopped", () => {
    expect(stepDeltas(steps, 48, [31])).toHaveLength(1);
    expect(stepDeltas(steps, 48, [])).toEqual([]);
  });
});
