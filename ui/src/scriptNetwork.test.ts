import { describe, expect, it } from "vitest";
import { BridgeRemoteError } from "@latticenet/plugin-bridge";

import { coreRefusalMessage, goQuote } from "../dev/coreRefusal";
import { scriptNetworkRefusalSamples } from "./core-types";
import { networkSteps, refusedStepIn, scriptNetworkRefusal, SCRIPT_NETWORK_CODE } from "./scriptNetwork";

/**
 * fleet_script_network_unavailable (S2-SR-2). The bridge relays a host
 * error's message and code only, so the UI reads the refusal out of core's
 * sentence. The sentences below the first block are core's own: lattice-server
 * publishes them in core-types.ts (tools/tsgen, from
 * internal/server/testdata/script_network_refusals.json) and its
 * TestScriptNetworkRefusalSamples holds each to the refusal core builds, so
 * a change of wording on either side fails here or there.
 */

/** What the console's client throws for core's 409: core's message with the request id appended. */
function relayed(message: string): BridgeRemoteError {
  return new BridgeRemoteError(`${message} (request_id: req_0123abcd)`, "call_failed");
}

describe("core's own refusal sentences (core-types.ts scriptNetworkRefusalSamples)", () => {
  it("covers every shape core writes: own step, member step, draft, no step, escaped ids", () => {
    expect(scriptNetworkRefusalSamples.length).toBeGreaterThanOrEqual(6);
    expect(scriptNetworkRefusalSamples.some((sample) => sample.step && sample.record !== sample.rendered && sample.rendered !== "")).toBe(true);
    expect(scriptNetworkRefusalSamples.some((sample) => !sample.step)).toBe(true);
    expect(scriptNetworkRefusalSamples.some((sample) => sample.rendered === "")).toBe(true);
    expect(scriptNetworkRefusalSamples.some((sample) => /["\s]/.test(sample.record))).toBe(true);
  });

  for (const sample of scriptNetworkRefusalSamples) {
    it(`reads ${sample.name}: the record holding the step, the record that rendered, the step`, () => {
      const refusal = scriptNetworkRefusal(relayed(sample.message));
      expect(refusal).not.toBeNull();
      expect(refusal!.record).toBe(sample.record);
      expect(refusal!.rendered).toBe(sample.rendered);
      expect(refusal!.step).toEqual(sample.step ?? null);
      expect(refusal!.message.startsWith(SCRIPT_NETWORK_CODE)).toBe(false);
    });
  }

  it("puts a member's step on the member, not on the combination that rendered (the lane 6c code review)", () => {
    const member = scriptNetworkRefusalSamples.find((sample) => sample.step && sample.rendered && sample.record !== sample.rendered)!;
    const refusal = scriptNetworkRefusal(relayed(member.message))!;
    expect(refusal.record).not.toBe(refusal.rendered);
    expect(refusal.record).toBe(member.record);
  });

  it("answers in the harness exactly as core does, so the dev drives parse core's text", () => {
    for (const sample of scriptNetworkRefusalSamples) {
      const holder = sample.step ? sample.record : sample.rendered;
      expect(coreRefusalMessage(sample.rendered, holder, sample.step ?? null)).toBe(sample.message);
    }
    expect(goQuote('a "b" \\ c')).toBe('"a \\"b\\" \\\\ c"');
  });
});

describe("the other shapes a refusal arrives in", () => {
  const plugin = `${SCRIPT_NETWORK_CODE}: subscription "osaka-lookup" is fleet-bound, and a Script or Resolve Domain step asked for the network 1 time(s); a fleet-bound chain runs without a network until S3, so remove the network call or the step`;

  it("reads the plugin's sentence: the record that rendered, no step, the sentence without the code", () => {
    expect(scriptNetworkRefusal(new Error(plugin))).toEqual({
      record: "osaka-lookup",
      rendered: "osaka-lookup",
      step: null,
      message: plugin.slice(SCRIPT_NETWORK_CODE.length + 2),
    });
    expect(scriptNetworkRefusal(new Error(`${SCRIPT_NETWORK_CODE}: collection "asia-combination" is fleet-bound, and a Script step asked`))?.record).toBe("asia-combination");
    expect(scriptNetworkRefusal(new Error(`${SCRIPT_NETWORK_CODE}: the fleet draft is fleet-bound, and a Script step asked`))).toMatchObject({ record: "", rendered: "" });
  });

  it("reads a sentence in neither wording by the record after 'record' and any step it names", () => {
    const refusal = scriptNetworkRefusal(new Error(`${SCRIPT_NETWORK_CODE}: record osaka-lookup: step 2 (Script Operator) asked for the network`));
    expect(refusal).toMatchObject({ record: "osaka-lookup", rendered: null, step: { position: 2, type: "Script Operator" } });
  });

  it("reads core's typed refusal, as JSON after the code, as bare JSON, as an object and as a reply's refused", () => {
    const typed = { code: SCRIPT_NETWORK_CODE, record: "osaka-lookup", step: { position: 2, type: "Script Operator" }, message: "the render was refused" };
    const want = { record: "osaka-lookup", rendered: null, step: { position: 2, type: "Script Operator" }, message: "the render was refused" };
    expect(scriptNetworkRefusal(new Error(`${SCRIPT_NETWORK_CODE}: ${JSON.stringify(typed)}`))).toEqual(want);
    expect(scriptNetworkRefusal(JSON.stringify(typed))).toEqual(want);
    expect(scriptNetworkRefusal(typed)).toEqual(want);
    expect(scriptNetworkRefusal({ refused: typed })).toEqual(want);
    expect(scriptNetworkRefusal(Object.assign(new Error("call failed"), { data: typed }))).toEqual(want);
    expect(scriptNetworkRefusal({ refused: SCRIPT_NETWORK_CODE, entries: [] })).toEqual({ record: "", rendered: null, step: null, message: "" });
  });

  it("is nothing else: another code, the code inside a sentence, a bind refusal", () => {
    expect(scriptNetworkRefusal(new Error("fleet_response_chain_unavailable: process step 2 (Response Transformer)"))).toBeNull();
    expect(scriptNetworkRefusal(new Error(`the plan failed because ${SCRIPT_NETWORK_CODE}: step 2`))).toBeNull();
    expect(scriptNetworkRefusal({ refused: "no_includable_line", entries: [] })).toBeNull();
    expect(scriptNetworkRefusal({ code: "plan_required", record: "a" })).toBeNull();
    expect(scriptNetworkRefusal(undefined)).toBeNull();
  });

  it("bounds what it shows: a step out of range, an id core would not accept, an escape Go does not write, a long sentence", () => {
    const refusal = scriptNetworkRefusal({ code: SCRIPT_NETWORK_CODE, record: " padded", step: { position: 0, type: "Script Operator" }, message: "x".repeat(2000) });
    expect(refusal?.record).toBe("");
    expect(refusal?.step).toBeNull();
    expect(refusal!.message.length).toBeLessThanOrEqual(600);
    // Core accepts spaces and quotes in an id; it refuses control characters and surrounding space.
    expect(scriptNetworkRefusal({ code: SCRIPT_NETWORK_CODE, record: 'two "words"' })?.record).toBe('two "words"');
    expect(scriptNetworkRefusal({ code: SCRIPT_NETWORK_CODE, record: "line\nbreak" })?.record).toBe("");
    expect(scriptNetworkRefusal({ code: SCRIPT_NETWORK_CODE, record: "a".repeat(257) })?.record).toBe("");
    expect(scriptNetworkRefusal({ code: SCRIPT_NETWORK_CODE, record: "a", step: { position: 2, type: "<b>Script</b> Operator" } })?.step).toEqual({ position: 2, type: "bScriptb Operator" });
    const badEscape = `${SCRIPT_NETWORK_CODE}: a step asked for the network while record "x\\qy" rendered, and so on; step 1 (Script Operator) of record "x\\qy" is the first step`;
    expect(scriptNetworkRefusal(new Error(badEscape))).toMatchObject({ record: "", step: { position: 1, type: "Script Operator" } });
    expect(scriptNetworkRefusal(new Error(`${SCRIPT_NETWORK_CODE}: step 1 (Script Operator) of record "\\x41\\u4e1c"`))?.record).toBe("A东");
  });
});

describe("the step a surface names, and whether it is proven", () => {
  const filter = { type: "Structured Filter", args: {} };
  const lookup = { type: "Script Operator", args: { content: "x" } };
  const resolve = { type: "Resolve Domain Operator" };
  const off = { type: "Script Filter", disabled: true };
  const own = scriptNetworkRefusalSamples.find((sample) => sample.step && sample.record === sample.rendered && sample.rendered)!;
  const member = scriptNetworkRefusalSamples.find((sample) => sample.step && sample.rendered && sample.record !== sample.rendered)!;

  it("finds the step in the surface's chain when core names none, and never guesses between several", () => {
    const bare = scriptNetworkRefusal(new Error(`${SCRIPT_NETWORK_CODE}: subscription "osaka-lookup" is fleet-bound, and a Script step asked`))!;
    expect(networkSteps([filter, lookup, off])).toEqual([{ position: 2, type: "Script Operator" }]);
    expect(refusedStepIn(bare, [filter, lookup, off], true)).toEqual({ position: 2, type: "Script Operator", certain: false });
    expect(refusedStepIn(bare, [lookup, resolve])).toBeNull();
    // The step core names wins, typed from the chain when core left the type out.
    expect(refusedStepIn({ ...bare, step: { position: 1, type: "" } }, [lookup, resolve])).toEqual({ position: 1, type: "Script Operator", certain: false });
  });

  it("proves the step only for a subscription that rendered alone, with one step that can ask", () => {
    const refusal = scriptNetworkRefusal(relayed(own.message))!;
    const chain: unknown[] = Array.from({ length: own.step!.position }, (_, index) => (index === own.step!.position - 1 ? lookup : filter));
    expect(refusedStepIn(refusal, chain, true)?.certain).toBe(true);
    // A combination renders its members' chains too.
    expect(refusedStepIn(refusal, chain, false)?.certain).toBe(false);
    // Another step that can ask, and core's step is only the first candidate.
    expect(refusedStepIn(refusal, [...chain, resolve], true)?.certain).toBe(false);
  });

  it("never proves a member's step: the combination that rendered read other chains", () => {
    const refusal = scriptNetworkRefusal(relayed(member.message))!;
    const chain = Array.from({ length: member.step!.position }, (_, index) => (index === member.step!.position - 1 ? lookup : filter));
    expect(refusedStepIn(refusal, chain, true)).toEqual({ ...member.step!, certain: false });
  });
});
