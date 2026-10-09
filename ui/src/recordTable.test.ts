import { describe, expect, it } from "vitest";

import { KIND_COLLECTION, KIND_FILE, STORE_VERSION_LEGACY, STORE_VERSION_SPLIT, type SubStoreShareRow, type SubscriptionListItem } from "./client";
import { buildLineage } from "./pipeline";
import {
  TEXT,
  attentionWeight,
  countsNeedPreview,
  expiryOf,
  isFlagged,
  kindCounts,
  kindFacetOf,
  kindOf,
  kindOfFacet,
  lastFetchOf,
  matchesKind,
  nodeCountsOf,
  publishedOf,
  reorderBlock,
  stepsOf,
} from "./recordTable";

const NOW = Date.parse("2026-10-09T08:00:00Z");
const DAY = 86_400_000;

function row(overrides: Partial<SubscriptionListItem>): SubscriptionListItem {
  return { id: "r", kind: "sub", name: "r", has_url: false, has_inline_content: true, step_count: 0, disabled_step_count: 0, imported: false, ...overrides };
}

const split = { storeVersion: STORE_VERSION_SPLIT, preview: undefined, canPreview: true, now: NOW };

describe("the kind filter", () => {
  it("maps stored kinds to the address's words and back", () => {
    expect(kindFacetOf(undefined)).toBe("source");
    expect(kindFacetOf("sub")).toBe("source");
    expect(kindFacetOf(KIND_COLLECTION)).toBe("combination");
    expect(kindFacetOf(KIND_FILE)).toBe("file");
    for (const facet of ["source", "combination", "file"] as const) expect(kindFacetOf(kindOfFacet(facet))).toBe(facet);
  });

  it("matches every record when unset or unknown, and only its kind otherwise", () => {
    const file = row({ kind: KIND_FILE });
    expect(matchesKind(file, "")).toBe(true);
    expect(matchesKind(file, "module")).toBe(true);
    expect(matchesKind(file, "file")).toBe(true);
    expect(matchesKind(file, "source")).toBe(false);
    expect(kindCounts([row({}), row({ kind: KIND_FILE }), row({ kind: KIND_FILE }), row({ kind: KIND_COLLECTION })])).toEqual({
      all: 4, source: 1, combination: 1, file: 2,
    });
  });
});

describe("nodes in and out", () => {
  it("prints the index's counts, and out only when the chain ran natively", () => {
    const counted = nodeCountsOf(row({ nodes_in: 166, nodes_out: 25, step_count: 3, last_fetch_at: new Date(NOW - 2 * 3600_000).toISOString() }), split);
    expect(counted).toMatchObject({ in: "166", out: "25", pair: "166 → 25" });
    expect(counted.title).toBe("166 in, 25 out after the chain, at the fetch 2h ago.");
    const flagged = nodeCountsOf(row({ nodes_in: 120, step_count: 2 }), split);
    expect(flagged).toMatchObject({ in: "120", out: "", pair: "120 in" });
    expect(flagged.title).toContain(TEXT.outNotNative);
  });

  it("hands on every node it read when no step runs", () => {
    expect(nodeCountsOf(row({ nodes_in: 28, step_count: 0 }), split)).toMatchObject({ in: "28", out: "28" });
    expect(nodeCountsOf(row({ nodes_in: 28, step_count: 2, disabled_step_count: 2 }), split)).toMatchObject({ out: "28" });
  });

  it("says not counted on a split store, and counts through previews only on a store without the index", () => {
    expect(nodeCountsOf(row({}), split)).toMatchObject({ in: "", out: "", title: TEXT.notCounted });
    expect(countsNeedPreview(row({}), STORE_VERSION_SPLIT)).toBe(false);
    expect(countsNeedPreview(row({}), STORE_VERSION_LEGACY)).toBe(true);
    expect(countsNeedPreview(row({}), undefined)).toBe(true);
    expect(countsNeedPreview(row({ nodes_in: 3 }), STORE_VERSION_LEGACY)).toBe(false);
    const legacy = { ...split, storeVersion: STORE_VERSION_LEGACY };
    expect(nodeCountsOf(row({}), legacy)).toMatchObject({ in: "counting", pair: "counting" });
    expect(nodeCountsOf(row({}), { ...legacy, preview: { status: "ready", source: 86, result: 78, at: NOW } })).toMatchObject({ in: "86", out: "78", pair: "86 → 78" });
    expect(nodeCountsOf(row({}), { ...legacy, preview: { status: "failed", reason: "503", at: NOW } })).toMatchObject({ in: "unknown" });
    expect(nodeCountsOf(row({}), { ...legacy, canPreview: false }).in).toBe("");
  });

  it("leaves a file's cells empty, with the reason", () => {
    expect(nodeCountsOf(row({ kind: KIND_FILE, nodes_in: 9 }), split)).toEqual({ in: "", out: "", title: TEXT.fileCounts, pair: "" });
    expect(countsNeedPreview(row({ kind: KIND_FILE }), undefined)).toBe(false);
  });
});

describe("published", () => {
  const shares: SubStoreShareRow[] = [{ subscription_id: "r", share_id: "s", slug: "cdcd", enabled: true, path: "/sub/cdcd/x" }];

  it("is unknown, not unpublished, when the session cannot read the share list or the read failed", () => {
    expect(publishedOf(row({}), { shares: undefined, available: false, error: "", now: NOW })).toMatchObject({ label: "unknown", unknown: true, title: TEXT.publishedUnknownTitle });
    expect(publishedOf(row({}), { shares: undefined, available: true, error: "denied", now: NOW })).toMatchObject({ label: "unknown", unknown: true });
  });

  it("is the share list's verdict otherwise", () => {
    expect(publishedOf(row({}), { shares, available: true, error: "", now: NOW })).toMatchObject({ tone: "ok", label: "/cdcd", unknown: false });
    expect(publishedOf(row({ id: "other" }), { shares, available: true, error: "", now: NOW })).toMatchObject({ label: "not published", unknown: false });
  });
});

describe("expiry and traffic", () => {
  const provider = (expireIn: number | null, extra: Partial<SubscriptionListItem> = {}) =>
    row({ source: "remote", has_url: true, upload: 1, download: 1, total: 10, ...(expireIn === null ? {} : { expire: Math.floor((NOW + expireIn) / 1000) }), ...extra });

  it("reads expired, soon and later from the provider's figures", () => {
    expect(expiryOf(provider(-2 * DAY - 3600_000), NOW)).toMatchObject({ state: "expired", tone: "danger", text: "expired 2 days ago" });
    expect(expiryOf(provider(6 * DAY + 3600_000), NOW)).toMatchObject({ state: "soon", tone: "warn", text: "expires in 6 days" });
    expect(expiryOf(provider(40 * DAY), NOW)).toMatchObject({ state: "ok", tone: "neutral", text: "expires in 40 days" });
    expect(expiryOf(provider(null), NOW)).toMatchObject({ state: "ok", text: "" });
  });

  it("says why the cell is empty for a record that is not a provider, or a provider that sent nothing", () => {
    expect(expiryOf(row({}), NOW)).toMatchObject({ state: "none", title: TEXT.notAProvider, figures: null });
    expect(expiryOf(row({ source: "remote", has_url: true, userinfo_parsed: true }), NOW)).toMatchObject({ state: "unreported", text: TEXT.notReported });
  });
});

describe("last fetch", () => {
  it("reports any record the store has fetch bookkeeping for, and nothing for one never fetched", () => {
    const at = new Date(NOW - 3 * 3600_000).toISOString();
    expect(lastFetchOf(row({ has_url: true, last_fetch_at: at, last_fetch_ok: false, last_error: "provider returned status 503" }), NOW)).toMatchObject({ tone: "danger", label: "Failed 3h ago" });
    // A vpn-core source core refreshed on a schedule: no link, but a fetch on record.
    expect(lastFetchOf(row({ source: "vpn-core", last_fetch_at: at, last_fetch_ok: true }), NOW)).toMatchObject({ tone: "ok", label: "Refreshed 3h ago" });
    expect(lastFetchOf(row({ source: "local" }), NOW)).toBeNull();
  });
});

describe("the kind cell", () => {
  const items = [
    row({ id: "src", name: "cdcd-self-host" }),
    row({ id: "combo", kind: KIND_COLLECTION, name: "merge", members: ["src", "gone"] }),
    row({ id: "loon", kind: KIND_FILE, name: "for-cdcd-loon", file_type: "config", node_source: "combo" }),
    row({ id: "rules", kind: KIND_FILE, name: "rules", file_type: "plain" }),
    row({ id: "clash", kind: KIND_FILE, name: "for-clash-rules", file_type: "plain" }),
    row({ id: "orphan", kind: KIND_FILE, name: "for-stash", file_type: "script", node_source: "retired" }),
  ];
  const lineage = buildLineage(items, undefined);
  const cell = (id: string) => kindOf(items.find((item) => item.id === id)!, items, lineage);

  it("names what a record is and what it connects to", () => {
    expect(cell("src")).toMatchObject({ label: "Pasted nodes", detail: "feeds 1 combination", missing: 0 });
    expect(cell("combo")).toMatchObject({ label: "Combination", detail: "cdcd-self-host", missing: 1, missingLabel: "1 missing" });
    expect(cell("rules")).toMatchObject({ label: "Plain text file", detail: TEXT.fileServedAsWritten });
  });

  it("names a file's source before its client, so a narrow cell cuts the client the row's name already says", () => {
    expect(cell("loon")).toMatchObject({ label: "Configuration file", detail: "from merge · for Loon", missing: 0 });
    expect(cell("clash").detail).toBe(`${TEXT.fileServedAsWritten} · for Clash`);
  });

  it("marks a file whose node source is gone, and keeps the id in the title", () => {
    expect(cell("orphan")).toMatchObject({ label: "Script file", detail: "for Stash", missing: 1, missingLabel: TEXT.fileGone });
    expect(cell("orphan").title).toContain("retired is no longer in the store");
  });
});

describe("the steps cell", () => {
  it("puts the turned-off steps on a line of their own, and says both in the title", () => {
    expect(stepsOf(row({ step_count: 2, disabled_step_count: 1 }))).toEqual({ count: "2", off: "1 off", title: "2 steps, 1 turned off." });
    expect(stepsOf(row({ step_count: 1 }))).toEqual({ count: "1", off: "", title: "1 step." });
    expect(stepsOf(row({ step_count: 3, target: "Surge" })).title).toBe("3 steps. Always rendered for Surge.");
  });
});

describe("whether rows can be moved", () => {
  const ok = { canMutate: true, available: true, storeVersion: STORE_VERSION_SPLIT, sort: "manual" };

  it("can on a split store, in manual order, for a session that writes, with reorder signed", () => {
    expect(reorderBlock(ok)).toBe("");
  });

  it("says why not, the session first, then the signed plugin, then the store, then the sort", () => {
    expect(reorderBlock({ ...ok, canMutate: false, available: false })).toBe(TEXT.reorderReadOnly);
    expect(reorderBlock({ ...ok, available: false })).toBe(TEXT.reorderUnsigned);
    expect(reorderBlock({ ...ok, storeVersion: undefined })).toBe(TEXT.reorderUnsigned);
    expect(reorderBlock({ ...ok, storeVersion: STORE_VERSION_LEGACY, sort: "name" })).toBe(TEXT.reorderLegacy);
    expect(reorderBlock({ ...ok, sort: "recent" })).toBe(TEXT.reorderSorted);
  });
});

describe("needs attention", () => {
  it("ranks failures and expiry first, then flags and soon-to-expire, then the rest", () => {
    const failed = row({ has_url: true, last_fetch_at: new Date(NOW - 3600_000).toISOString(), last_fetch_ok: false });
    const expired = row({ source: "remote", has_url: true, expire: Math.floor((NOW - DAY) / 1000) });
    const flagged = row({ flags: { regex_incompatible: true } });
    const quiet = row({});
    expect([quiet, flagged, expired, failed].map((item) => attentionWeight(item, NOW))).toEqual([3, 1, 0, 0]);
    expect(isFlagged(flagged)).toBe(true);
    expect(isFlagged(row({ flags: { has_fallback_step: true } }))).toBe(false);
  });
});
