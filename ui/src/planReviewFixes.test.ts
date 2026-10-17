import { describe, expect, it } from "vitest";
import { ref } from "vue";
import type { BridgeClient } from "@latticenet/plugin-bridge";

import { BINDINGS, KIND_COLLECTION, KIND_SUB, SOURCE_FLEET, SOURCE_REMOTE, type SubStoreShareRow, type SubscriptionListItem, type SubscriptionRecord } from "./client";
import { planState, type PlanStatus } from "./fleet";
import type { HostContext } from "./host";
import { en } from "./messages/en";
import { ru } from "./messages/ru";
import { zhCN } from "./messages/zh-CN";
import { currentPlan, promotesOtherRevision, stagedPlanState } from "./planFlow";
import { deletePrompt } from "./recordActions";
import { scriptNetworkRefusal } from "./scriptNetwork";
import { useRecordEditor } from "./useRecordEditor";
import { useSubscriptions } from "./useSubscriptions";

/**
 * The fixes the lane 6c reviews asked for that live outside a component: a
 * plan a later save superseded reads stale, the delete prompt counts a
 * combination that gathers a record by tag, the refusal's wording in each
 * locale, and the refusal the plan flow hands the editor.
 */

function item(over: Partial<SubscriptionListItem> = {}): SubscriptionListItem {
  return { id: "r", name: "r", kind: KIND_SUB, has_url: false, has_inline_content: false, step_count: 0, disabled_step_count: 0, imported: false, ...over } as SubscriptionListItem;
}

function plan(over: Partial<PlanStatus> = {}): PlanStatus {
  return {
    approval_id: "apr_1",
    status: "pending",
    plan_sha256: "x",
    subscription_id: "r",
    from_revision: "a",
    to_revision: "b",
    action: "promote",
    mode: "promote",
    created_at: "2026-10-16T00:00:00Z",
    updated_at: "2026-10-16T00:00:00Z",
    ...over,
  } as PlanStatus;
}

describe("a pending plan over a staged revision a later save replaced", () => {
  // Staged b, plan apr_1 filed over b, then a second save staged c.
  const row = item({ id: "r", revision: "a", staged_revision: "c" });

  it("reads stale, as core will once it reads it, without touching core's row", () => {
    const filed = plan({ to_revision: "b" });
    const found = currentPlan([filed], row);
    expect(found?.approval_id).toBe("apr_1");
    expect(planState(found!)).toBe("stale");
    expect(stagedPlanState([filed], row)).toBe("stale");
    expect(filed.stale).toBeUndefined();
    expect(promotesOtherRevision(filed, row)).toBe(true);
  });

  it("reads core's own stale answer the same way, so the row says the plan went stale rather than nothing", () => {
    const staled = plan({ to_revision: "b", status: "rejected", stale: true, reason: "stale" });
    expect(planState(currentPlan([staled], row)!)).toBe("stale");
    expect(currentPlan([staled], row)).toBe(staled);
    // An older plan that was simply rejected is history, not the plan the record waits on.
    expect(currentPlan([plan({ to_revision: "b", status: "rejected" })], row)).toBeUndefined();
  });

  it("keeps a plan over the revision staged now, and a plan that is not this record's promote", () => {
    expect(planState(currentPlan([plan({ approval_id: "new", to_revision: "c" }), plan({ to_revision: "b" })], row)!)).toBe("pending");
    // A sibling's multi-record plan names its own revisions.
    expect(planState(currentPlan([plan({ subscription_id: "other", to_revision: "q" })], row)!)).toBe("pending");
    // Shares only: the live revision stays, so the staged one is not its business.
    expect(planState(currentPlan([plan({ to_revision: "a", mode: "assign" })], row)!)).toBe("pending");
    // Nothing staged: nothing to supersede.
    expect(planState(currentPlan([plan({ to_revision: "b" })], item({ id: "r", revision: "a" }))!)).toBe("pending");
    // A failed apply is a record of what happened, and keeps its state.
    const failed = plan({ to_revision: "b", status: "approved", reason: "apply_revision failed at record r; records applied before it: none." });
    expect(planState(currentPlan([failed], row)!)).toBe("failed");
    expect(promotesOtherRevision(plan({ action: "delete", to_revision: "" }), row)).toBe(false);
  });
});

describe("the delete prompt over a record a combination gathers by tag", () => {
  const paid = item({ id: "paid", name: "paid-extra", display_name: "Paid provider extra", tags: ["fleet-extra"], source: SOURCE_REMOTE });
  const plus = item({ id: "plus", name: "fleet-plus-paid", display_name: "Family plus", kind: KIND_COLLECTION, members: ["lines"], member_tags: ["fleet-extra"] });
  const lines = item({ id: "lines", name: "all-fleet-lines", source: SOURCE_FLEET });
  const alone = item({ id: "alone", name: "alone" });
  const items = [paid, plus, lines, alone];
  const share = (subscription_id: string, slug: string): SubStoreShareRow => ({ subscription_id, share_id: `s-${slug}`, slug, enabled: true, path: `/sub/${slug}/TOKEN` });

  it("says the combination gathers it and serves fewer nodes, and never that nothing points at it", () => {
    const prompt = deletePrompt(["paid"], items, [share("plus", "plus-family")]);
    expect(prompt.title).not.toContain("Nothing else in this store points at it");
    expect(prompt.title).toContain(en.deletion.gatheredByTag("Family plus", 1, 1));
    expect(prompt.title).toMatch(/^Delete this record\? No other record in this store names it\./);
    // It does not break, so it is not listed with what breaks.
    expect(prompt.consequences).toEqual([]);
    // The combination's share serves fewer nodes, and the dialog names it.
    expect(prompt.served).toEqual(["/plus-family serves fewer nodes: it publishes Family plus, which draws from what is deleted"]);
    expect(prompt.title).not.toContain("No live share serves it");
  });

  it("keeps the old sentence for a record nothing reads, and adds the tag sentence beside what breaks", () => {
    expect(deletePrompt(["alone"], items, []).title).toContain("Nothing else in this store points at it");
    const named = item({ id: "pair", name: "pair", kind: KIND_COLLECTION, members: ["paid"] });
    const both = deletePrompt(["paid"], [...items, named], []);
    expect(both.consequences).toEqual(["pair  (combination, loses a member)"]);
    expect(both.title).toContain("1 other record in this store points at it");
    expect(both.title).toContain(en.deletion.gatheredByTag("Family plus", 1, 1));
  });
});

describe("the refusal's words in each locale", () => {
  it("hedges the step core names, and says it plainly only when it is proven", () => {
    expect(en.plans.scriptNetwork.stepCandidate(2, "Script Operator", "Osaka lines")).toBe(
      "Step 2 (Script Operator) of Osaka lines is the first step in this render that can ask for the network. The render does not say which step asked, so check any later Script or Resolve Domain step too.",
    );
    expect(en.plans.scriptNetwork.step(2, "Script Operator", "this record")).toBe("Step 2 (Script Operator) of this record asked for the network.");
    expect(zhCN.plans.scriptNetwork.stepCandidate(2, "Script Operator", "osaka-lookup")).toContain("第一个可能请求网络的步骤");
    expect(ru.plans.scriptNetwork.stepCandidate(2, "Script Operator", "osaka-lookup")).toContain("первым в этой отрисовке может запросить сеть");
  });

  it("joins 'this record' without a stray space in Chinese, and without 'записи этой записи' in Russian", () => {
    const zhHere = zhCN.plans.scriptNetwork.thisRecord;
    expect(zhCN.plans.scriptNetwork.step(2, "Script Operator", zhHere)).toBe("这条记录的第 2 步（Script Operator）请求了网络。");
    expect(zhCN.plans.scriptNetwork.step(2, "Script Operator", "osaka-lookup")).toBe("osaka-lookup 的第 2 步（Script Operator）请求了网络。");
    expect(zhCN.plans.scriptNetwork.openStepOf(2, zhHere)).toBe("打开这条记录的第 2 步");
    expect(zhCN.plans.scriptNetwork.openStepOf(2, "osaka-lookup")).toBe("打开 osaka-lookup 的第 2 步");
    const ruHere = ru.plans.scriptNetwork.thisRecord;
    for (const text of [
      ru.plans.scriptNetwork.step(2, "Script Operator", ruHere),
      ru.plans.scriptNetwork.stepCandidate(2, "Script Operator", ruHere),
      ru.plans.scriptNetwork.anyStep(ruHere),
      ru.plans.scriptNetwork.openSteps(ruHere),
    ]) {
      expect(text).not.toContain("записи этой записи");
      expect(text).toContain("этой записи");
    }
    expect(ru.plans.scriptNetwork.step(2, "Script Operator", "osaka-lookup")).toContain("записи osaka-lookup");
  });
});

describe("the refusal the plan flow hands the editor", () => {
  type Answer = unknown;
  function hostWith(answers: Record<string, Answer>) {
    const bridge = {
      call(service: string, method: string) {
        return { promise: Promise.resolve(answers[`${service}/${method}`]), cancel: () => {} };
      },
    } as unknown as BridgeClient;
    const host: HostContext = { bridge, init: ref(undefined), bootError: ref(""), available: () => true, resize: async () => {}, pageState: ref({}), sendState: () => {} };
    return host;
  }
  const key = (binding: { service: string; method: string }) => `${binding.service}/${binding.method}`;
  const filter = { type: "Regex Filter", args: { regex: ["JP"], keep: true } };
  const lookup = { type: "Script Operator", customName: "Geo lookup", args: { mode: "script", content: "await $substore.http.get('x')" } };
  const record: SubscriptionRecord = { id: "osaka", name: "osaka", source: SOURCE_FLEET, process: [filter, lookup], revision: "live-1" };
  const refusal = scriptNetworkRefusal(
    new Error(
      'fleet_script_network_unavailable: a step asked for the network while record "osaka" rendered, and a chain that reaches fleet nodes runs without one until S3; step 2 (Script Operator) of record "osaka" is the first step in that render that can ask for it, so remove the network call or the step',
    ),
  )!;

  async function openEditor() {
    const host = hostWith({ [key(BINDINGS.subGet)]: { subscription: record, live_revision: "live-1" }, [key(BINDINGS.subList)]: { subscriptions: [item({ id: "osaka", source: SOURCE_FLEET })] } });
    const subs = useSubscriptions(host);
    await subs.load();
    const editor = useRecordEditor({ host, subs, clearListState: () => {}, onSaved: () => {} });
    await editor.startEdit("osaka");
    return editor;
  }

  it("holds the refusal while the step it names is unchanged", async () => {
    const editor = await openEditor();
    editor.carryRefusal(refusal);
    expect(editor.carriedRefusal.value?.step).toEqual({ position: 2, type: "Script Operator" });
    // Another step changes: the refusal still stands.
    editor.draft.value.process = [{ ...filter, args: { regex: ["JP|KR"], keep: true } }, lookup];
    expect(editor.carriedRefusal.value).not.toBeNull();
  });

  it("lets go once the step changes, is removed, or the editor closes", async () => {
    const editor = await openEditor();
    editor.carryRefusal(refusal);
    editor.draft.value.process = [filter, { ...lookup, args: { mode: "script", content: "return proxies" } }];
    expect(editor.carriedRefusal.value).toBeNull();
    editor.draft.value.process = [filter, lookup];
    expect(editor.carriedRefusal.value).not.toBeNull();
    editor.draft.value.process = [filter];
    expect(editor.carriedRefusal.value).toBeNull();
    editor.draft.value.process = [filter, lookup];
    editor.cancelEdit();
    expect(editor.carriedRefusal.value).toBeNull();
  });

  it("names no step when the position holds no step that can ask (a chain numbered differently)", async () => {
    const editor = await openEditor();
    editor.carryRefusal({ ...refusal, step: { position: 1, type: "Script Operator" } });
    expect(editor.carriedRefusal.value).not.toBeNull();
    expect(editor.carriedRefusal.value?.step).toBeNull();
  });
});
