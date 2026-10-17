import { describe, expect, it } from "vitest";
import { ref } from "vue";
import type { BridgeClient } from "@latticenet/plugin-bridge";

import { chainChangeText } from "./chainText";
import { BINDINGS, KIND_COLLECTION, SOURCE_FLEET, SOURCE_REMOTE, type SubscriptionListItem, type SubscriptionRecord } from "./client";
import type { Plan } from "./core-types";
import type { HostContext } from "./host";
import { t } from "./i18n";
import {
  chainChanges,
  chainOnlyPlan,
  deleteNeedsPlan,
  fleetGatherers,
  parseChainChange,
  planChainSections,
  storedSteps,
  type ChainChange,
} from "./planFlow";
import { networkSteps, refusedStepIn, scriptNetworkRefusal, SCRIPT_NETWORK_CODE } from "./scriptNetwork";
import { useRecordEditor } from "./useRecordEditor";
import { emptyDraft, useSubscriptions } from "./useSubscriptions";

/**
 * The UI follow-ups to the S2 security fixes (design 28 S2, lane 6c): a
 * plan's process chain (S2-SR-2) as core lists it and as the propose step
 * reads it, the fleet_script_network_unavailable refusal in each shape it
 * arrives in, and a provider record a fleet-bound combination gathers
 * (S2-SR-1): its staged save, its delete, and the editor opening on the
 * staged revision.
 */

/** A chain change in core's words, as lattice-server writes it. */
function coreLine(change: ChainChange): string {
  if (change.kind === "many") return `the steps changed: ${change.before} before, ${change.after} after, too many to list one by one`;
  if (change.kind === "more") return `and ${change.count} more step changes`;
  if (change.kind === "text") return change.text;
  const notes = change.notes.map((note) => (note.kind === "moved" ? `was step ${note.from}` : note.kind === "now-enabled" ? "now enabled" : note.kind === "now-disabled" ? "now disabled" : "disabled"));
  return `step ${change.position} ${change.verb}: ${change.type}${notes.length ? ` (${notes.join(", ")})` : ""}`;
}

describe("a plan's process chain", () => {
  // lattice-server substore_svc_plans_chain_test.go TestSubStorePlanChainChanges, case for case.
  const step = (type: unknown, extra: Record<string, unknown> = {}) => ({ type, ...extra });
  const regexA = step("Regex Filter", { args: "a" });
  const regexB = step("Regex Filter", { args: "b" });
  const sortOn = step("Sort Operator");
  const sortOff = step("Sort Operator", { disabled: true });
  const script = step("Script Operator", { args: { content: "x" } });
  const cases: { name: string; before: unknown[]; after: unknown[]; want: string[] }[] = [
    { name: "identical", before: [regexA, sortOn], after: [regexA, sortOn], want: [] },
    { name: "added at the end", before: [regexA], after: [regexA, script], want: ["step 2 added: Script Operator"] },
    { name: "removed", before: [script, regexA], after: [regexA], want: ["step 1 removed: Script Operator"] },
    {
      name: "changed, added and disabled",
      before: [regexA, sortOn],
      after: [regexB, script, sortOff],
      want: ["step 1 changed: Regex Filter", "step 2 added: Script Operator", "step 3 changed: Sort Operator (was step 2, now disabled)"],
    },
    { name: "re-enabled", before: [sortOff], after: [sortOn], want: ["step 1 changed: Sort Operator (now enabled)"] },
    { name: "replaced by another type", before: [regexA], after: [script], want: ["step 1 removed: Regex Filter", "step 1 added: Script Operator"] },
    { name: "a moved step is a removal and an addition", before: [script, regexA], after: [regexA, script], want: ["step 1 removed: Script Operator", "step 2 added: Script Operator"] },
    { name: "added disabled", before: [], after: [sortOff], want: ["step 1 added: Sort Operator (disabled)"] },
    {
      name: "types are cleaned",
      before: [],
      after: [step(`Script‮ Operator\n${"x".repeat(80)}`), step(""), { type: 7 }],
      want: [`step 1 added: Script Operator ${"x".repeat(48 - "Script Operator ".length)}`, "step 2 added: step with no type", "step 3 added: unreadable step"],
    },
  ];
  for (const entry of cases) {
    it(`lists the steps as core does: ${entry.name}`, () => {
      expect(chainChanges(entry.before, entry.after).map(coreLine)).toEqual(entry.want);
    });
  }

  it("summarises a chain too long to list, and bounds the lines", () => {
    const long = Array.from({ length: 257 }, () => sortOn);
    expect(chainChanges(long, long)).toEqual([]);
    expect(chainChanges([], long).map(coreLine)).toEqual(["the steps changed: 0 before, 257 after, too many to list one by one"]);
    const many = Array.from({ length: 138 }, (_, n) => step("Flag Operator", { n }));
    const lines = chainChanges([], many).map(coreLine);
    expect(lines).toHaveLength(128);
    expect(lines.at(-1)).toBe("and 11 more step changes");
  });

  it("reads every line core writes back into its parts, and keeps any other line as it is", () => {
    for (const line of [
      "step 2 added: Script Operator",
      "step 1 removed: Regex Filter (disabled)",
      "step 3 changed: Sort Operator (was step 2, now disabled)",
      "step 1 changed: Sort Operator (now enabled)",
      "step 2 added: step with no type",
      "the steps changed: 300 before, 290 after, too many to list one by one",
      "and 5 more step changes",
    ]) {
      const parsed = parseChainChange(line);
      expect(parsed.kind).not.toBe("text");
      expect(coreLine(parsed)).toBe(line);
    }
    expect(parseChainChange("step 2 rewired: Script Operator")).toEqual({ kind: "text", text: "step 2 rewired: Script Operator" });
    expect(parseChainChange("step 2 added: Script Operator (sent somewhere)")).toEqual({ kind: "text", text: "step 2 added: Script Operator (sent somewhere)" });
  });

  it("says a line in the reader's words, the step type as the operator's own name", () => {
    expect(chainChangeText(parseChainChange("step 3 changed: Sort Operator (was step 2, now disabled)"))).toBe(
      `${t.plans.chain.changed(3, "Sort Operator")} (${t.plans.chain.notes.moved(2)}, ${t.plans.chain.notes.nowDisabled})`,
    );
    expect(chainChangeText(parseChainChange("step 2 added: unreadable step"))).toBe(t.plans.chain.added(2, t.plans.chain.unreadable));
    expect(chainChangeText(parseChainChange("and 4 more step changes"))).toBe(t.plans.chain.more(4));
  });

  const totals = (added: number, removed = 0, changed = 0) => ({ shares: 1, unbound_shares: 0, identities: 1, added, removed, changed, excluded: 0 });
  const plan = (over: Partial<Plan>): Plan =>
    ({ kind: "sub-store.plan", plugin_id: "latticenet.sub-store", subscription_id: "a", from_revision: "r1", to_revision: "r2", totals: totals(0), primary_totals: totals(0), shares: [], unbound_shares: [], identities: [], ...over }) as Plan;
  const section = (id: string, role: string, changes?: string[]) => ({
    subscription_id: id,
    role,
    from_revision: "r1",
    ...(changes ? { chain: { before: "b", after: "c", changes } } : { chain: { before: "b", after: "b" } }),
    totals: totals(0),
    shares: [],
    unbound_shares: [],
    identities: [],
  });

  it("lists the sections whose chain changes, in the plan's order, and says when only the steps change", () => {
    const text = plan({ records: [section("a", "promote", ["step 2 added: Script Operator"]), section("c", "dependent")] });
    expect(planChainSections(text)).toEqual([{ record: "a", role: "promote", changes: [{ kind: "step", verb: "added", position: 2, type: "Script Operator", notes: [] }] }]);
    expect(chainOnlyPlan(text)).toBe(true);
    // A line moved too: the steps are not the only change.
    expect(chainOnlyPlan({ ...text, totals: totals(3) })).toBe(false);
    expect(chainOnlyPlan(plan({ records: [section("a", "promote")] }))).toBe(false);
  });

  it("reads a plan without sections from its top-level chain", () => {
    const text = plan({ chain: { before: "b", after: "c", changes: ["step 1 removed: Regex Filter"] } });
    expect(planChainSections(text).map((entry) => entry.record)).toEqual(["a"]);
  });

  it("reads a record's steps as core does: process, or the operators of an older record", () => {
    expect(storedSteps({ process: [regexA] })).toEqual([regexA]);
    expect(storedSteps({ process: [], operators: [sortOn] })).toEqual([sortOn]);
    expect(storedSteps(null)).toEqual([]);
  });
});

describe("the fleet_script_network_unavailable refusal", () => {
  const plugin = `${SCRIPT_NETWORK_CODE}: subscription "osaka-lookup" is fleet-bound, and a Script or Resolve Domain step asked for the network 1 time(s); a fleet-bound chain runs without a network until S3, so remove the network call or the step`;

  it("reads the plugin's sentence: the record, no step, the sentence without the code", () => {
    expect(scriptNetworkRefusal(new Error(plugin))).toEqual({
      record: "osaka-lookup",
      step: null,
      message: plugin.slice(SCRIPT_NETWORK_CODE.length + 2),
    });
    expect(scriptNetworkRefusal(new Error(`${SCRIPT_NETWORK_CODE}: collection "asia-combination" is fleet-bound, and a Script step asked`))?.record).toBe("asia-combination");
    expect(scriptNetworkRefusal(new Error(`${SCRIPT_NETWORK_CODE}: the fleet draft is fleet-bound, and a Script step asked`))?.record).toBe("");
  });

  it("reads core's sentence with the step it names", () => {
    const refusal = scriptNetworkRefusal(new Error(`${SCRIPT_NETWORK_CODE}: record osaka-lookup: step 2 (Script Operator) asked for the network`));
    expect(refusal?.record).toBe("osaka-lookup");
    expect(refusal?.step).toEqual({ position: 2, type: "Script Operator" });
  });

  it("reads core's typed refusal, as JSON after the code, as bare JSON, as an object and as a reply's refused", () => {
    const typed = { code: SCRIPT_NETWORK_CODE, record: "osaka-lookup", step: { position: 2, type: "Script Operator" }, message: "the render was refused" };
    const want = { record: "osaka-lookup", step: { position: 2, type: "Script Operator" }, message: "the render was refused" };
    expect(scriptNetworkRefusal(new Error(`${SCRIPT_NETWORK_CODE}: ${JSON.stringify(typed)}`))).toEqual(want);
    expect(scriptNetworkRefusal(JSON.stringify(typed))).toEqual(want);
    expect(scriptNetworkRefusal(typed)).toEqual(want);
    expect(scriptNetworkRefusal({ refused: typed })).toEqual(want);
    expect(scriptNetworkRefusal(Object.assign(new Error("call failed"), { data: typed }))).toEqual(want);
    expect(scriptNetworkRefusal({ refused: SCRIPT_NETWORK_CODE, entries: [] })).toEqual({ record: "", step: null, message: "" });
  });

  it("is nothing else: another code, the code inside a sentence, a bind refusal", () => {
    expect(scriptNetworkRefusal(new Error("fleet_response_chain_unavailable: process step 2 (Response Transformer)"))).toBeNull();
    expect(scriptNetworkRefusal(new Error(`the plan failed because ${SCRIPT_NETWORK_CODE}: step 2`))).toBeNull();
    expect(scriptNetworkRefusal({ refused: "no_includable_line", entries: [] })).toBeNull();
    expect(scriptNetworkRefusal({ code: "plan_required", record: "a" })).toBeNull();
    expect(scriptNetworkRefusal(undefined)).toBeNull();
  });

  it("bounds what it shows: a step position out of range, an id that is not one, a long sentence", () => {
    const refusal = scriptNetworkRefusal({ code: SCRIPT_NETWORK_CODE, record: "two words", step: { position: 0, type: "Script Operator" }, message: "x".repeat(2000) });
    expect(refusal?.record).toBe("");
    expect(refusal?.step).toBeNull();
    expect(refusal!.message.length).toBeLessThanOrEqual(600);
    expect(scriptNetworkRefusal({ code: SCRIPT_NETWORK_CODE, record: "a", step: { position: 2, type: "<b>Script</b> Operator" } })?.step).toEqual({ position: 2, type: "bScriptb Operator" });
  });

  it("finds the step in the surface's chain when core names none, and never guesses between several", () => {
    const filter = { type: "Structured Filter", args: {} };
    const lookup = { type: "Script Operator", args: { content: "x" } };
    const resolve = { type: "Resolve Domain Operator" };
    const off = { type: "Script Filter", disabled: true };
    const bare = scriptNetworkRefusal(new Error(plugin))!;
    expect(networkSteps([filter, lookup, off])).toEqual([{ position: 2, type: "Script Operator" }]);
    expect(refusedStepIn(bare, [filter, lookup, off])).toEqual({ position: 2, type: "Script Operator" });
    expect(refusedStepIn(bare, [lookup, resolve])).toBeNull();
    // The step core names wins, typed from the chain when core left the type out.
    expect(refusedStepIn({ ...bare, step: { position: 1, type: "" } }, [lookup, resolve])).toEqual({ position: 1, type: "Script Operator" });
  });
});

describe("a provider record a fleet-bound combination gathers", () => {
  const item = (over: Partial<SubscriptionListItem>): SubscriptionListItem =>
    ({ id: "x", kind: "sub", name: "x", has_url: false, has_inline_content: false, step_count: 0, disabled_step_count: 0, imported: false, ...over }) as SubscriptionListItem;
  const paid = item({ id: "paid-extra", name: "paid-extra", display_name: "Paid provider extra", source: SOURCE_REMOTE, tags: ["fleet-extra"], revision: "live-1" });
  const fleetLines = item({ id: "all-fleet-lines", source: SOURCE_FLEET, revision: "f1", flags: { fleet_bound: true } });
  const plus = item({ id: "fleet-plus-paid", kind: KIND_COLLECTION, display_name: "Fleet lines with a paid extra", members: ["all-fleet-lines"], member_tags: ["fleet-extra"], flags: { fleet_bound: true } });
  const plain = item({ id: "provider-pair", kind: KIND_COLLECTION, members: ["paid-extra"] });
  const items = [paid, fleetLines, plus, plain];

  it("names the fleet-bound combinations that gather it, by tag or by id, and no other", () => {
    expect(fleetGatherers(paid, items).map((entry) => entry.id)).toEqual(["fleet-plus-paid"]);
    expect(deleteNeedsPlan(paid, items)).toBe(true);
    expect(deleteNeedsPlan(paid, [paid, plain])).toBe(false);
    expect(deleteNeedsPlan(fleetLines, [fleetLines])).toBe(true);
  });

  type Answer = unknown | ((payload: unknown) => unknown);
  function hostWith(answers: Record<string, Answer>) {
    const calls: string[] = [];
    const bridge = {
      call(service: string, method: string, payload: unknown) {
        calls.push(method);
        const answer = answers[`${service}/${method}`];
        try {
          const value = typeof answer === "function" ? (answer as (p: unknown) => unknown)(payload) : answer;
          return { promise: Promise.resolve(value), cancel: () => {} };
        } catch (cause) {
          return { promise: Promise.reject(cause), cancel: () => {} };
        }
      },
    } as unknown as BridgeClient;
    const host: HostContext = { bridge, init: ref(undefined), bootError: ref(""), available: () => true, resize: async () => {}, pageState: ref({}), sendState: () => {} };
    return { host, calls };
  }
  const key = (binding: { service: string; method: string }) => `${binding.service}/${binding.method}`;
  const record: SubscriptionRecord = { id: "paid-extra", name: "paid-extra", display_name: "Paid provider extra", source: SOURCE_REMOTE, url: "https://sub.example-provider.com/s", tags: ["fleet-extra"], process: [] };
  const staged = { ...record, process: [{ type: "Regex Filter", args: { regex: ["HK"], keep: true }, disabled: true }], revision: "staged-abcdef1234567890" };

  it("says a staged save is staged, and which combination staged it, and offers Publish", async () => {
    const { host } = hostWith({
      [key(BINDINGS.subSave)]: { saved: true, staged: true, staged_revision: "staged-abcdef1234567890", subscription: staged },
      [key(BINDINGS.subList)]: { subscriptions: [{ ...paid, staged_revision: "staged-abcdef1234567890" }, fleetLines, plus, plain] },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    const draft = { ...emptyDraft(), id: "paid-extra", name: "paid-extra", displayName: "Paid provider extra", source: SOURCE_REMOTE, url: "https://sub.example-provider.com/s" };
    expect(await subs.save(draft)).toBe(true);
    expect(subs.notice.value).toBe(`${t.plans.stagedSave.saved("Paid provider extra", "staged-abc…")} ${t.plans.stagedSave.gathered("Fleet lines with a paid extra", 1)}`);
    expect(subs.stagedSave.value).toEqual({ id: "paid-extra", revision: "staged-abcdef1234567890" });
    // Any other notice takes the offer away.
    subs.notice.value = "something else";
    await Promise.resolve();
    expect(subs.stagedSave.value).toBeNull();
  });

  it("says a refused delete needs a plan because of the combination, and offers the plan", async () => {
    const { host } = hostWith({
      [key(BINDINGS.subDelete)]: {
        saved: false,
        refused: { code: "fleet_delete_requires_plan", ids: ["paid-extra"], collections: ["fleet-plus-paid"], message: "gathered" },
      },
      [key(BINDINGS.subList)]: { subscriptions: items },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.remove("paid-extra")).toBe(false);
    expect(subs.actionError.value).toBe(t.plans.refusal.notDeleted(t.plans.stagedSave.gatheredDelete("Fleet lines with a paid extra", 1)));
    expect(subs.deleteNeedsPlan.value).toBe("paid-extra");
  });

  it("opens the editor on the staged revision, and a migration's legacy record on its live one", async () => {
    const reply = { subscription: { ...record, revision: "live-1" }, live_revision: "live-1", staged: { revision: staged.revision, subscription: staged } };
    const { host } = hostWith({ [key(BINDINGS.subGet)]: reply, [key(BINDINGS.subList)]: { subscriptions: [{ ...paid, staged_revision: staged.revision }] } });
    const subs = useSubscriptions(host);
    await subs.load();
    const editor = useRecordEditor({ host, subs, clearListState: () => {}, onSaved: () => {} });
    await editor.startEdit("paid-extra");
    expect(editor.editingStaged.value).toEqual({ revision: staged.revision, live: "live-1" });
    expect((editor.draft.value.process[0] as { disabled?: boolean }).disabled).toBe(true);

    const migrated = hostWith({ [key(BINDINGS.subGet)]: reply, [key(BINDINGS.subList)]: { subscriptions: [{ ...paid, staged_revision: staged.revision, staged_migrated: true }] } });
    const subs2 = useSubscriptions(migrated.host);
    await subs2.load();
    const editor2 = useRecordEditor({ host: migrated.host, subs: subs2, clearListState: () => {}, onSaved: () => {} });
    await editor2.startEdit("paid-extra");
    expect(editor2.editingStaged.value).toBeNull();
    expect(editor2.draft.value.process).toEqual([]);
  });

  it("reads both revisions for the propose step without touching the editor's read or the page's messages", async () => {
    const { host } = hostWith({ [key(BINDINGS.subGet)]: { subscription: { ...record, revision: "live-1" }, live_revision: "live-1", staged: { revision: staged.revision, subscription: staged } } });
    const subs = useSubscriptions(host);
    subs.actionError.value = "kept";
    const read = await subs.readRevisions("paid-extra");
    expect(read?.live?.revision).toBe("live-1");
    expect(read?.staged?.revision).toBe(staged.revision);
    expect(subs.actionError.value).toBe("kept");
    const failing = hostWith({ [key(BINDINGS.subGet)]: () => { throw new Error("subscription was not found"); } });
    expect(await useSubscriptions(failing.host).readRevisions("gone")).toBeNull();
  });
});
