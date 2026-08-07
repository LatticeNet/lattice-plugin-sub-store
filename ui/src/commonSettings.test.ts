import { describe, expect, it } from "vitest";

import {
  applyCommonSettings,
  emptyCommonSettings,
  readCommonSettings,
} from "./commonSettings";
import type { ChainStep } from "./components/ProcessChain.vue";

const QUICK = "Quick Setting Operator";
const USELESS = "Useless Filter";

/**
 * The common-settings block and the operator chain are two views of the same
 * data. The failure mode to guard against is them disagreeing — a toggle that
 * says "on" while the step it stands for is absent, or an edit through one
 * silently discarding what was set through the other.
 */

describe("reading settings out of a chain", () => {
  it("reports defaults for an empty chain", () => {
    expect(readCommonSettings([])).toEqual(emptyCommonSettings());
  });

  it("reads the quick-setting arguments", () => {
    const steps: ChainStep[] = [{ type: QUICK, args: { udp: true, scert: false } }];
    const settings = readCommonSettings(steps);
    expect(settings.udp).toBe("on");
    expect(settings.skipCertVerify).toBe("off");
    // A key the step does not set is "leave as-is", not "off".
    expect(settings.tcpFastOpen).toBe("default");
  });

  // A disabled step is not in force. Showing its values as active would
  // describe a pipeline that is not running.
  it("ignores a disabled step", () => {
    const steps: ChainStep[] = [
      { type: QUICK, args: { udp: true }, disabled: true },
      { type: USELESS, disabled: true },
    ];
    const settings = readCommonSettings(steps);
    expect(settings.udp).toBe("default");
    expect(settings.dropUseless).toBe(false);
  });
});

describe("writing settings back into a chain", () => {
  it("adds the steps it needs and removes them when switched off", () => {
    let steps = applyCommonSettings([], { ...emptyCommonSettings(), udp: "on", dropUseless: true });
    expect(steps.filter((s) => s.type === QUICK)).toHaveLength(1);
    expect(steps.filter((s) => s.type === USELESS)).toHaveLength(1);

    steps = applyCommonSettings(steps, emptyCommonSettings());
    expect(steps.filter((s) => s.type === QUICK)).toHaveLength(0);
    expect(steps.filter((s) => s.type === USELESS)).toHaveLength(0);
  });

  it("round-trips", () => {
    const settings = {
      ...emptyCommonSettings(),
      udp: "on" as const,
      tcpFastOpen: "off" as const,
      dropUseless: true,
    };
    expect(readCommonSettings(applyCommonSettings([], settings))).toEqual(settings);
  });

  // Everything the operator built by hand has to survive a toggle.
  it("leaves unrelated steps alone and in order", () => {
    const before: ChainStep[] = [
      { type: "Regex Filter", args: { value: ["a"] } },
      { type: "Sort Operator", args: { value: "asc" } },
    ];
    const after = applyCommonSettings(before, { ...emptyCommonSettings(), udp: "on" });
    const kept = after.filter((s) => s.type !== QUICK).map((s) => s.type);
    expect(kept).toEqual(["Regex Filter", "Sort Operator"]);
  });

  // A hand-edited Quick Setting step can carry keys these toggles do not cover.
  // Replacing the step wholesale would silently drop them.
  it("preserves arguments the toggles do not manage", () => {
    const before: ChainStep[] = [{ type: QUICK, args: { udp: true, "something else": 42 } }];
    const after = applyCommonSettings(before, { ...emptyCommonSettings(), udp: "off" });
    const quick = after.find((s) => s.type === QUICK);
    expect(quick?.args).toMatchObject({ udp: false, "something else": 42 });
  });

  it("keeps a step that still has unmanaged arguments after the toggles clear", () => {
    const before: ChainStep[] = [{ type: QUICK, args: { udp: true, "something else": 42 } }];
    const after = applyCommonSettings(before, emptyCommonSettings());
    const quick = after.find((s) => s.type === QUICK);
    expect(quick, "a step carrying other arguments must not be deleted").toBeDefined();
    expect(quick?.args).toEqual({ "something else": 42 });
  });

  it("does not duplicate a step that is already there", () => {
    const before: ChainStep[] = [{ type: USELESS }];
    const after = applyCommonSettings(before, { ...emptyCommonSettings(), dropUseless: true });
    expect(after.filter((s) => s.type === USELESS)).toHaveLength(1);
  });
});
