import { describe, expect, it } from "vitest";
import { ref } from "vue";
import type { BridgeClient } from "@latticenet/plugin-bridge";

import { BINDINGS, KIND_SUB, type SubscriptionListItem } from "./client";
import type { HostContext } from "./host";
import { setLocale, t } from "./i18n";
import { useSubscriptions } from "./useSubscriptions";

/**
 * The plan, migration and staged-revision calls of design 28 S2 (lane 6b)
 * and the structured refusals of the mutating ones (s2-plan section 2.6):
 * what each sends, and what each makes of core's and the plugin's answers.
 */

type Answer = unknown | ((payload: any) => unknown);

function planHost(answers: Record<string, Answer>, withheld: readonly string[] = []) {
  const calls: { method: string; payload: any }[] = [];
  const bridge = {
    call(service: string, method: string, payload: unknown) {
      calls.push({ method: `${service.split("/").pop()}/${method}`, payload });
      structuredClone(payload);
      const answer = answers[`${service.split("/").pop()}/${method}`];
      try {
        const value = typeof answer === "function" ? (answer as (p: unknown) => unknown)(payload) : answer;
        return { promise: Promise.resolve(value), cancel: () => {} };
      } catch (cause) {
        return { promise: Promise.reject(cause), cancel: () => {} };
      }
    },
  } as unknown as BridgeClient;
  const host: HostContext = {
    bridge,
    // A handshake that has landed; the calls only read that it is there.
    init: ref({} as NonNullable<HostContext["init"]["value"]>),
    bootError: ref(""),
    available: (binding) => !withheld.includes(binding.method),
    resize: async () => {},
    pageState: ref({}),
    sendState: () => {},
  };
  return { host, calls, made: (method: string) => calls.filter((call) => call.method === method) };
}

const ROW = { id: "asia", name: "asia", kind: KIND_SUB, source: "fleet", revision: "live1", staged_revision: "next1", flags: { fleet_bound: true }, has_url: false, has_inline_content: false, step_count: 1, disabled_step_count: 0, imported: false } as SubscriptionListItem;
const LIST = { subscriptions: [ROW], store_version: 2 };

describe("a fleet-bound record's delete", () => {
  it("reads the structured refusal, says the delete goes through a plan, and names the record for the plan path", async () => {
    await setLocale("en");
    const { host } = planHost({
      "subscription/list": LIST,
      "subscription/delete": { saved: false, refused: { code: "fleet_delete_requires_plan", ids: ["asia"], message: 'subscription "asia" is fleet-bound' } },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.remove("asia")).toBe(false);
    expect(subs.actionError.value).toBe(t.plans.refusal.notDeleted(t.fleetEditor.refusal.codes.fleet_delete_requires_plan));
    expect(subs.deleteNeedsPlan.value).toBe("asia");
    expect(subs.notice.value).toBe("");
  });

  it("says another refusal in its own words and offers no plan path", async () => {
    const { host } = planHost({
      "subscription/list": LIST,
      "subscription/delete": { saved: false, refused: { code: "promotion_pending", ids: ["asia"], staged_revision: "next1", message: "being promoted" } },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.remove("asia")).toBe(false);
    expect(subs.actionError.value).toContain(t.fleetEditor.refusal.codes.promotion_pending);
    expect(subs.actionError.value.startsWith("Not deleted:")).toBe(true);
    expect(subs.deleteNeedsPlan.value).toBe("");
  });
});

describe("a new record's refusal on the error channel", () => {
  it("names the record being written as itself, by the id it would have had", async () => {
    const { host } = planHost({
      "subscription/list": { subscriptions: [], store_version: 2 },
      // A build before S2 leads the error with the code; ids derive from names.
      "subscription/save": () => {
        throw new Error("fleet_mixed_owner_credentials: collection \"tokyo-mix\" would gather legacy records");
      },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    const draft = { ...(await import("./useSubscriptions")).emptyDraft(), name: "Tokyo mix", kind: "collection", members: ["a"] };
    expect(await subs.save(draft)).toBe(false);
    // The error's text, not a refusal object, so no record is listed at all; above all not "tokyo-mix" as another record.
    expect(subs.actionError.value).not.toContain("Records: tokyo-mix");
  });

  it("passes the derived id to a structured refusal that arrives as a thrown reply", async () => {
    const refusal = Object.assign(new Error("refused"), { saved: false, refused: { code: "fleet_mixed_owner_credentials", ids: ["tokyo-mix", "legacy-a"], message: "mixed" } });
    const { host } = planHost({
      "subscription/list": { subscriptions: [{ ...ROW, id: "legacy-a", name: "Legacy A" }], store_version: 2 },
      "subscription/save": () => {
        throw refusal;
      },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    const draft = { ...(await import("./useSubscriptions")).emptyDraft(), name: "Tokyo mix", kind: "collection", members: ["legacy-a"] };
    expect(await subs.save(draft)).toBe(false);
    expect(subs.actionError.value).toContain(t.fleetEditor.refusal.records("Legacy A"));
    expect(subs.actionError.value).not.toContain("tokyo-mix");
  });
});

describe("publishing a staged revision", () => {
  it("publishes at once when core says no plan is needed", async () => {
    const { host, made } = planHost({
      "subscription/list": LIST,
      "plans/propose": { required: false, reason: "no live share in this record's fleet closure is fleet-bound; publish it" },
      "plans/publish": (payload: any) => ({ published: true, subscription_id: payload.subscription_id, revision: payload.revision, action: "promote" }),
    });
    const subs = useSubscriptions(host);
    await subs.load();
    const outcome = await subs.publishStaged(ROW);
    expect(outcome.kind).toBe("published");
    expect(made("plans/propose")[0]!.payload).toEqual({ subscription_id: "asia", from_revision: "live1", to_revision: "next1" });
    expect(made("plans/publish")[0]!.payload).toEqual({ subscription_id: "asia", revision: "next1", expected_revision: "live1" });
  });

  it("returns core's plan and never publishes past it", async () => {
    const text = JSON.stringify({ subscription_id: "asia", from_revision: "live1", to_revision: "next1", shares: [], identities: [] });
    const { host, made } = planHost({
      "subscription/list": LIST,
      "plans/propose": { required: true, approval_id: "apr_1", plan_sha256: "h", plan: text, status: "pending" },
      "plans/status": { plans: [] },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    const outcome = await subs.publishStaged(ROW);
    expect(outcome.kind).toBe("plan");
    expect(outcome.kind === "plan" && outcome.plan?.to_revision).toBe("next1");
    expect(made("plans/publish")).toHaveLength(0);
  });

  it("keeps core's code on a refusal, so the flow can open the step that fixes it", async () => {
    const { host } = planHost({
      "subscription/list": LIST,
      "plans/propose": { required: false },
      "plans/publish": () => {
        throw new Error("migration_requires_plan: record asia's staged revision is a migration and its fleet closure has live shares");
      },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    const outcome = await subs.publishStaged(ROW);
    expect(outcome).toMatchObject({ kind: "refused", code: "migration_requires_plan" });
  });

  it("reads core's 1 MiB refusal with its figures", async () => {
    const { host } = planHost({
      "subscription/list": LIST,
      "plans/propose": () => {
        throw new Error("the plan is 1310720 bytes, over 1048576; publish the change in smaller revisions");
      },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.publishStaged(ROW)).toMatchObject({ kind: "too-large", bytes: 1310720, limit: 1048576 });
  });

  it("proposes nothing without the propose method (permission denied)", async () => {
    const { host, calls } = planHost({ "subscription/list": LIST }, ["propose"]);
    const subs = useSubscriptions(host);
    await subs.load();
    const before = calls.length;
    expect(await subs.publishStaged(ROW)).toEqual({ kind: "error", message: t.plans.cannotPropose });
    expect(calls.length).toBe(before);
  });
});

describe("deleting through a plan", () => {
  it("proposes the delete with no to_revision and publishes it with the live revision", async () => {
    const { host, made } = planHost({
      "subscription/list": LIST,
      "plans/propose": { required: false },
      "plans/publish": (payload: any) => ({ published: true, subscription_id: payload.subscription_id, revision: payload.revision, action: "delete" }),
    });
    const subs = useSubscriptions(host);
    await subs.load();
    expect((await subs.deleteThroughPlan(ROW)).kind).toBe("published");
    expect(made("plans/propose")[0]!.payload).toEqual({ subscription_id: "asia", from_revision: "live1", action: "delete" });
    expect(made("plans/publish")[0]!.payload).toEqual({ subscription_id: "asia", revision: "live1", expected_revision: "live1", action: "delete" });
  });
});

describe("migrate_record and discard_staged", () => {
  it("stages, and reads the files a refusal names", async () => {
    const reply = { subscription_id: "legacy", live_revision: "l", staged_revision: "s", selection: { count: 0, every_bound_line: true }, legacy: { source: "vpn-core" }, dependents: { collections: [], co_migrate: [] }, warnings: [] };
    const { host, made } = planHost({ "subscription/list": LIST, "subscription/migrate_record": reply });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.migrateRecord("legacy", "l")).toEqual({ kind: "staged", reply });
    expect(made("subscription/migrate_record")[0]!.payload).toEqual({ subscription_id: "legacy", if_revision: "l" });

    const refused = planHost({
      "subscription/list": LIST,
      "subscription/migrate_record": { saved: false, refused: { code: "fleet_file_unavailable", ids: ["legacy"], files: ["router"], message: "feeds the files router" } },
    });
    const other = useSubscriptions(refused.host);
    await other.load();
    const outcome = await other.migrateRecord("legacy");
    expect(outcome).toMatchObject({ kind: "refused", code: "fleet_file_unavailable", files: ["router"] });
    expect(outcome.kind === "refused" && outcome.message.startsWith("Not staged:")).toBe(true);
  });

  it("discards by the staged revision and says a mismatch in the operator's words", async () => {
    const { host, made } = planHost({
      "subscription/list": LIST,
      "subscription/discard_staged": { saved: false, refused: { code: "staged_revision_mismatch", ids: ["asia"], message: "nothing staged" } },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.discardStaged("asia", "next1")).toBe(false);
    expect(made("subscription/discard_staged")[0]!.payload).toEqual({ subscription_id: "asia", staged_revision: "next1" });
    expect(subs.actionError.value).toContain(t.fleetEditor.refusal.codes.staged_revision_mismatch);
    expect(subs.actionError.value.startsWith("Not discarded:")).toBe(true);
  });
});

describe("Fetch now on a withheld record", () => {
  it("says the rule, never a provider outage", async () => {
    const { host } = planHost({
      "subscription/list": LIST,
      "subscription/probe": { subscription_id: "legacy", bytes: 0, stale: true, ok: false, error_code: "owner_credentials_withheld" },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    expect(await subs.refresh("legacy")).toBe(false);
    expect(subs.actionError.value).toBe(t.plans.withheldProbe);
  });
});

describe("bind_selection", () => {
  it("says bulk binding is unavailable while core serves no such method (operator decision 8)", async () => {
    const { host } = planHost({
      "subscription/list": LIST,
      "bind/bind_selection": () => {
        throw new Error("sub-store/bind: unknown method bind_selection");
      },
    });
    const subs = useSubscriptions(host);
    expect(await subs.bindSelection("vu_a", ["l1"])).toEqual({ ok: false, unavailable: true, message: t.plans.bind.unavailable });
    expect(BINDINGS.bindSelection.method).toBe("bind_selection");
  });
});
