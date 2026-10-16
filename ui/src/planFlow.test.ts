import { describe, expect, it } from "vitest";

import { KIND_COLLECTION, KIND_FILE, KIND_SUB, type SubscriptionListItem } from "./client";
import type { Plan } from "./core-types";
import type { PlanStatus } from "./fleet";
import {
  closureOf,
  currentPlan,
  darkenedShares,
  decoyShares,
  defaultIdentity,
  explicitDependents,
  fileDependents,
  fleetStepDeltas,
  holdsNoLine,
  isFleetBound,
  linesToBind,
  mayHavePromoted,
  mixedBlock,
  partialApply,
  planShareViews,
  planTooLarge,
  promotedAfterFailure,
  remainingRecords,
  shareFields,
  shortRevision,
  stagedFacts,
  stagedPlanState,
  tagDependents,
  undecidedShares,
  unmatchedNameSteps,
  withFleetDeltas,
  type PlanShareRow,
} from "./planFlow";

function item(over: Partial<SubscriptionListItem> = {}): SubscriptionListItem {
  return { id: "r", name: "r", kind: KIND_SUB, has_url: false, has_inline_content: false, step_count: 0, disabled_step_count: 0, imported: false, ...over } as SubscriptionListItem;
}

function share(over: Partial<PlanShareRow> = {}): PlanShareRow {
  return { subscription_id: "r", share_id: "s", slug: "s", enabled: true, path: "/sub/s/token", ...over };
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

const STORE = [
  item({ id: "fleet", source: "fleet", revision: "r1", tags: ["hk"], flags: { fleet_bound: true } }),
  item({ id: "legacy", source: "vpn-core-graph", revision: "r2", flags: { owner_credentials: true } }),
  item({ id: "explicit", kind: KIND_COLLECTION, members: ["fleet"], revision: "r3" }),
  item({ id: "by-tag", kind: KIND_COLLECTION, member_tags: ["hk"], revision: "r4" }),
  item({ id: "outer", kind: KIND_COLLECTION, members: ["explicit"], revision: "r5" }),
  item({ id: "file", kind: KIND_FILE, node_source: "by-tag", revision: "r6" }),
  item({ id: "staged-reader", kind: KIND_COLLECTION, revision: "r7", staged_members: ["legacy"] }),
];

describe("who reads a record", () => {
  it("closes over explicit members, member tags and staged edges, the record first", () => {
    expect(closureOf("fleet", STORE)).toEqual(["fleet", "explicit", "by-tag", "outer"]);
    // An edge only the staged revision creates is in the closure too (core computes it over the union).
    expect(closureOf("legacy", STORE)).toEqual(["legacy", "staged-reader"]);
  });

  it("tells explicit dependents, which block a delete, from tag dependents, which do not", () => {
    expect(explicitDependents("fleet", STORE).map((entry) => entry.id)).toEqual(["explicit"]);
    expect(tagDependents("fleet", STORE).map((entry) => entry.id)).toEqual(["by-tag"]);
  });

  it("finds a file over a collection that reads the record", () => {
    expect(fileDependents("fleet", STORE).map((entry) => entry.id)).toEqual(["file"]);
    expect(fileDependents("legacy", STORE)).toEqual([]);
  });

  it("names a mixed collection's legacy members only when the index flags both sides", () => {
    const mixed = item({ id: "mix", kind: KIND_COLLECTION, members: ["fleet", "legacy"], flags: { fleet_bound: true, owner_credentials: true } });
    expect(mixedBlock(mixed, [...STORE, mixed])?.map((entry) => entry.id)).toEqual(["legacy"]);
    expect(mixedBlock(item({ id: "c", kind: KIND_COLLECTION, members: ["legacy"], flags: { owner_credentials: true } }), STORE)).toBeNull();
  });
});

describe("staged revisions", () => {
  it("reads the live and staged revisions and a record never live", () => {
    expect(stagedFacts(item({ revision: "live", staged_revision: "next", staged_migrated: true }))).toMatchObject({ staged: true, revision: "next", live: "live", neverLive: false, migrated: true });
    expect(stagedFacts(item({ staged_revision: "next" })).neverLive).toBe(true);
    expect(stagedFacts(item({ revision: "live" })).staged).toBe(false);
  });

  it("does not call a staged-only fleet record fleet-bound: it serves no one yet", () => {
    expect(isFleetBound(item({ source: "fleet", revision: "" }))).toBe(false);
    expect(isFleetBound(item({ source: "fleet", revision: "r" }))).toBe(true);
    expect(isFleetBound(item({ kind: KIND_COLLECTION, flags: { fleet_bound: true } }))).toBe(true);
  });

  it("shortens a plugin revision and keeps a short one whole", () => {
    expect(shortRevision("3f1a9c0d5e7b2a4c8d1e6f9a0b3c5d7e")).toBe("3f1a9c0d5e…");
    expect(shortRevision("abc")).toBe("abc");
  });

  it("finds the plan over the staged revision, else a pending or failed one", () => {
    const row = item({ id: "r", revision: "a", staged_revision: "b" });
    expect(currentPlan([plan({ approval_id: "old", to_revision: "z", status: "rejected" }), plan({ approval_id: "over" })], row)?.approval_id).toBe("over");
    // A multi-record plan whose primary is a sibling: its apply failed.
    const failed = plan({ approval_id: "sib", subscription_id: "other", to_revision: "q", status: "approved", reason: "apply_revision failed at record r; records applied before it: other, with their shares' changes written." });
    expect(currentPlan([failed], row)?.approval_id).toBe("sib");
    expect(stagedPlanState([failed], row)).toBe("failed");
    expect(stagedPlanState(undefined, row)).toBe("unknown");
    expect(stagedPlanState([], row)).toBe("none");
  });
});

describe("shares", () => {
  const fleet = STORE[0]!;
  const remote = item({ id: "remote", source: "remote", revision: "r9" });
  const shares = [
    share({ subscription_id: "fleet", share_id: "open" }),
    share({ subscription_id: "fleet", share_id: "bound", identity_id: "vu_a" }),
    share({ subscription_id: "fleet", share_id: "gone", archived_at: "2026-10-01T00:00:00Z" }),
    share({ subscription_id: "remote", share_id: "dark", identity_id: "vu_b" }),
  ];

  it("finds the decoy shares of a fleet-bound record and the darkened identities of any other", () => {
    expect(decoyShares(fleet, shares).map((entry) => entry.share_id)).toEqual(["open"]);
    expect(darkenedShares(remote, shares).map((entry) => entry.share_id)).toEqual(["dark"]);
    expect(darkenedShares(fleet, shares)).toEqual([]);
  });

  it("leaves a share undecided only with neither an identity nor an archival", () => {
    const open = [share({ share_id: "a" }), share({ share_id: "b" }), share({ share_id: "c", identity_id: "vu" })];
    const decisions = { a: { share_id: "a", identity: "vu_a", archive: false }, b: { share_id: "b", identity: "", archive: false } };
    expect(undecidedShares(open, decisions).map((entry) => entry.share_id)).toEqual(["b"]);
    expect(undecidedShares(open, { ...decisions, b: { share_id: "b", identity: "", archive: true } })).toEqual([]);
  });

  it("asks only for moves between none and one identity", () => {
    const open = [share({ share_id: "new" }), share({ share_id: "held", identity_id: "vu_x" }), share({ share_id: "old" })];
    const fields = shareFields(open, {
      new: { share_id: "new", identity: "vu_a", archive: false },
      // Choosing another identity for a bound share is never sent: core refuses it (identity_change_refused).
      held: { share_id: "held", identity: "vu_y", archive: false },
      old: { share_id: "old", identity: "vu_b", archive: true },
    });
    expect(fields).toEqual({ share_identities: { new: "vu_a" }, archive_shares: ["old"] });
    expect(shareFields(open, { held: { share_id: "held", identity: "", archive: false, clear: true } })).toEqual({ share_identities: { held: "" } });
  });
});

describe("migration checks", () => {
  it("starts a graph record's shares on its own identity, an export record's on none", () => {
    const identities = [{ id: "vu_alice" }, { id: "vu_bob" }];
    expect(defaultIdentity({ source: "vpn-core-graph", vpn_identity: "vu_alice" }, identities)).toBe("vu_alice");
    expect(defaultIdentity({ source: "vpn-core-graph", vpn_identity: "vu_gone" }, identities)).toBe("");
    // An export record's identity is a ProxyUser id or "every identity", never a VpnUser.
    expect(defaultIdentity({ source: "vpn-core", vpn_identity: "vu_alice" }, identities)).toBe("");
  });

  it("warns that an identity holds none of the lines, and lists what it still needs bound", () => {
    const empty = { entries: [{ index: 0, provider: true, digest: "p" }], excluded: [{ index: 1, line_uuid: "l1", reason: "not_bound" }, { index: 2, line_uuid: "l2", reason: "credential_not_applied" }, { index: 3, line_uuid: "l3", reason: "service_down" }] };
    expect(holdsNoLine(empty)).toBe(true);
    expect(holdsNoLine({ entries: [{ index: 0, line_uuid: "x", digest: "d" }], excluded: empty.excluded })).toBe(false);
    expect(linesToBind(empty)).toEqual(["l1", "l2"]);
  });

  it("finds a carried-over name filter that keeps none of the fleet names", () => {
    const process = [{ type: "Regex Filter", args: { regex: ["香港|HK"], keep: true } }, { type: "Regex Filter", args: { regex: ["vless"], keep: true } }];
    expect(unmatchedNameSteps(process, ["hk-hkt-01 vless-reality", "jp-tyo hy2"])).toEqual([1]);
    expect(unmatchedNameSteps(process, [])).toEqual([]);
  });
});

describe("plans", () => {
  it("reads core's 1 MiB refusal and its figures", () => {
    expect(planTooLarge("the plan is 1310720 bytes, over 1048576; publish the change in smaller revisions")).toEqual({ bytes: 1310720, limit: 1048576 });
    expect(planTooLarge("plan_required: a live share")).toBeNull();
  });

  it("reads a partial multi-record apply and the records it left", () => {
    const reason =
      "apply_revision failed at record legacy-left; records applied before it: promoted-member, other, with their shares' changes written. The record may have been promoted: list shows its live revision. Propose the rest";
    expect(partialApply(reason)).toEqual({ failed: "legacy-left", applied: ["promoted-member", "other"] });
    expect(partialApply("apply_revision failed; the record may have been promoted")).toBeNull();
    expect(mayHavePromoted(reason)).toBe(true);
    const text = { subscription_id: "promoted-member", apply_order: ["promoted-member", "other", "legacy-left"] } as Plan;
    expect(remainingRecords(text, ["promoted-member", "other"])).toEqual(["legacy-left"]);
  });

  it("says from the list whether a failed plan's record was promoted", () => {
    expect(promotedAfterFailure({ from_revision: "a", to_revision: "b" }, "b")).toBe(true);
    expect(promotedAfterFailure({ from_revision: "a", to_revision: "b" }, "a")).toBe(false);
    expect(promotedAfterFailure({ from_revision: "a", to_revision: "b" }, undefined)).toBeNull();
  });

  it("lists every share of every section with its identity's lines", () => {
    const text = {
      subscription_id: "a",
      shares: [],
      identities: [],
      records: [
        {
          subscription_id: "a",
          role: "self",
          from_revision: "x",
          totals: {} as Plan["totals"],
          shares: [{ share_id: "s1", slug: "one", identity_id: "vu", enabled: true, added: 1, removed: 0, changed: 0, excluded: 1, assigned: true }],
          unbound_shares: [],
          identities: [{ identity_id: "vu", added: [{ line_uuid: "l", names: ["HK 01"], digest: "d" }], removed: [], changed: [], excluded: [{ line_uuid: "m", name: "JP 02", reason: "not_bound" }], unchanged: 3 }],
        },
      ],
    } as unknown as Plan;
    const [view] = planShareViews(text);
    expect(view).toMatchObject({ record: "a", share_id: "s1", assigned: true, unchanged: 3 });
    expect(view!.added.map((line) => line.names[0])).toEqual(["HK 01"]);
    expect(view!.excluded.map((line) => line.reason)).toEqual(["not_bound"]);
  });
});

describe("a fleet record's Steps counts", () => {
  it("counts the pushed steps from the whole catalogue and drops the preview's figures for the steps core ran together", () => {
    const fleet = fleetStepDeltas(
      [
        { index: 1, out: 23, pushed: true },
        { index: 2, in: 23, out: 21, pushed: false },
      ],
      117,
      (index) => `step ${index + 1}`,
    );
    expect(fleet).toEqual([
      { index: 1, label: "step 2", before: 117, after: 23 },
      { index: 2, label: "step 3", before: 23, after: 21 },
    ]);
    const preview = [
      { index: 0, label: "p1", before: 23, after: 23 },
      { index: 1, label: "p2", before: 23, after: 23 },
      { index: 3, label: "p4", before: 21, after: 20 },
    ];
    expect(withFleetDeltas(preview, fleet, 2).map((delta) => `${delta.index}:${delta.before}→${delta.after}`)).toEqual(["1:117→23", "2:23→21", "3:21→20"]);
  });
});
