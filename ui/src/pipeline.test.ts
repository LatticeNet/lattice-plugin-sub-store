import { describe, expect, it } from "vitest";

import type { SubStoreShareRow, SubscriptionListItem } from "./client";
import { failingFixture, largeFixture, productionFixture, type Fixture, type StoredRecord } from "../dev/fixtures";
import {
  attentionItems,
  buildLineage,
  clientOfFile,
  daysUntilExpiry,
  formatExpiry,
  formatUsage,
  groupByPrefix,
  pathOf,
  providerFigures,
  recordHealth,
  shareNodeId,
  usageRatio,
  usedBySentence,
} from "./pipeline";

const NOW = Date.parse("2026-09-29T08:00:00Z");
const DAY = 86_400_000;
const GB = 1024 ** 3;

/** What the plugin's `list` answers for a stored record, parsed figures included. */
function row(rec: StoredRecord): SubscriptionListItem {
  const steps = (rec.process ?? []) as { disabled?: boolean }[];
  return {
    id: rec.id,
    kind: rec.kind || "sub",
    name: rec.name,
    display_name: rec.display_name,
    tags: rec.tags,
    source: rec.source,
    has_url: Boolean(rec.url),
    has_inline_content: Boolean(rec.content),
    members: rec.members,
    member_tags: rec.member_tags,
    file_type: rec.file_type,
    node_source: rec.node_source,
    step_count: steps.length,
    disabled_step_count: 0,
    imported: Boolean(rec.origin),
    last_fetch_at: rec.last_fetch_at,
    last_fetch_ok: rec.last_fetch_at ? rec.last_fetch_ok ?? false : undefined,
    last_error: rec.last_error,
    userinfo: rec.userinfo,
  };
}

function rows(fixture: Fixture): { items: SubscriptionListItem[]; shares: SubStoreShareRow[] } {
  return { items: fixture.records.map(row), shares: fixture.shares };
}

function item(partial: Partial<SubscriptionListItem> & { id: string }): SubscriptionListItem {
  return { kind: "sub", name: partial.id, has_url: false, has_inline_content: true, step_count: 0, disabled_step_count: 0, imported: false, ...partial };
}

describe("provider figures", () => {
  it("prefers the fields the plugin parsed", () => {
    const figures = providerFigures(item({ id: "p", upload: 1, download: 2, total: 10, expire: 1893456000, userinfo: "total=999" }));
    expect(figures).toEqual({ upload: 1, download: 2, total: 10, expire: 1893456000 });
  });

  it("falls back to the verbatim header only from a runtime older than the parse", () => {
    expect(providerFigures(item({ id: "p", userinfo: "upload=1; download=2; total=10; expire=0" }))).toEqual({ upload: 1, download: 2, total: 10 });
    expect(providerFigures(item({ id: "p", userinfo: "expire=1893456000000" }))).toEqual({ expire: 1893456000 });
    expect(providerFigures(item({ id: "p" }))).toBeNull();
    expect(providerFigures(item({ id: "p", userinfo: "plan=pro" }))).toBeNull();
  });

  it("never brings back a field the runtime refused", () => {
    // The runtime dropped every field (negative, past int64) and said so.
    expect(providerFigures(item({ id: "p", userinfo: "upload=-1; total=99999999999999999999", userinfo_parsed: true }))).toBeNull();
    // It kept total and refused download; the header is not read for download.
    expect(providerFigures(item({ id: "p", userinfo: "download=-5; total=100", total: 100, userinfo_parsed: true }))).toEqual({ total: 100 });
    // Even a header the fallback would read is left alone once the runtime answered.
    expect(providerFigures(item({ id: "p", userinfo: "total=100", userinfo_parsed: true }))).toBeNull();
  });

  it("measures usage against the total and nothing else", () => {
    expect(usageRatio({ upload: 12 * GB, download: 398 * GB, total: 500 * GB })).toBeCloseTo(0.82, 2);
    expect(usageRatio({ download: 5 })).toBeNull();
    expect(usageRatio({ download: 5, total: 0 })).toBeNull();
    expect(usageRatio(null)).toBeNull();
  });

  it("formats usage as used of total with a percentage, or used alone", () => {
    expect(formatUsage({ upload: 12 * GB, download: 398 * GB, total: 500 * GB })).toBe("410 GB of 500 GB · 82%");
    expect(formatUsage({ download: 3 * GB })).toBe("3 GB used");
    expect(formatUsage({ expire: 1893456000 })).toBe("");
    expect(formatUsage(null)).toBe("");
  });

  it("counts whole days to the expiry and says which side of it we are on", () => {
    const at = (days: number) => ({ expire: Math.floor((NOW + days * DAY + 3600_000) / 1000) });
    expect(daysUntilExpiry(at(6), NOW)).toBe(6);
    expect(formatExpiry(at(6), NOW)).toBe("expires in 6 days");
    expect(formatExpiry(at(1), NOW)).toBe("expires tomorrow");
    expect(formatExpiry(at(0), NOW)).toBe("expires today");
    expect(formatExpiry({ expire: Math.floor((NOW - 3 * DAY) / 1000) }, NOW)).toBe("expired 3 days ago");
    expect(formatExpiry({ expire: Math.floor(Date.parse("2027-03-01T00:00:00Z") / 1000) }, NOW)).toBe("expires 2027-03-01");
    expect(formatExpiry({}, NOW)).toBe("");
  });
});

describe("prefix grouping", () => {
  const names = productionFixture()
    .records.filter((rec) => rec.kind === "file")
    .map((rec) => rec.name);

  it("folds production's sixteen files into two people and two loners", () => {
    const entries = groupByPrefix(names, (name) => name);
    const shape = entries.map((entry) => (entry.kind === "group" ? `${entry.label} ${entry.members.length}` : entry.member));
    expect(shape).toEqual(["for-cdcd-* 5", "for-clash-novpn", "for-loon-novpn", "for-openjobs-* 9"]);
  });

  it("joins the longest prefix at least three names share", () => {
    const entries = groupByPrefix(["a-b-1", "a-b-2", "a-b-3", "a-c-1", "a-c-2", "a-c-3", "a-d"], (n) => n);
    expect(entries.map((e) => (e.kind === "group" ? e.label : e.member))).toEqual(["a-b-*", "a-c-*", "a-d"]);
  });

  it("dissolves a prefix left with fewer members than the threshold", () => {
    // "x" is shared by four names, but three of them join "x-y", leaving one.
    const entries = groupByPrefix(["x-y-1", "x-y-2", "x-y-3", "x-z"], (n) => n);
    expect(entries.map((e) => (e.kind === "group" ? e.label : e.member))).toEqual(["x-y-*", "x-z"]);
  });

  it("leaves names without separators, and fewer than three sharers, alone", () => {
    expect(groupByPrefix(["建材市场", "机场备用", "家里"], (n) => n).every((e) => e.kind === "single")).toBe(true);
    expect(groupByPrefix(["p-1", "p-2"], (n) => n).every((e) => e.kind === "single")).toBe(true);
    expect(groupByPrefix(["p-1", "p-2"], (n) => n, 2)[0]).toMatchObject({ kind: "group", label: "p-*" });
  });

  it("keeps the underscore a name was written with", () => {
    const [entry] = groupByPrefix(["team_a", "team_b", "team_c"], (n) => n);
    expect(entry).toMatchObject({ kind: "group", label: "team_*" });
  });

  it("groups the large fixture by person", () => {
    const files = largeFixture().records.filter((rec) => rec.kind === "file");
    const entries = groupByPrefix(files, (rec) => rec.name);
    expect(entries).toHaveLength(12);
    expect(entries.every((entry) => entry.kind === "group" && entry.members.length === 15)).toBe(true);
  });
});

describe("the client a file is for", () => {
  it("reads the client from the name, bounded by separators", () => {
    expect(clientOfFile("for-cdcd-loon")?.label).toBe("Loon");
    expect(clientOfFile("for-openjobs-shenzhen-loon")?.label).toBe("Loon");
    expect(clientOfFile("for-cdcd-shadowrocket")?.label).toBe("Shadowrocket");
    expect(clientOfFile("for-clash-novpn")?.label).toBe("Clash");
    expect(clientOfFile("for-alice-sing-box")?.label).toBe("sing-box");
    expect(clientOfFile("for-alice-mihomo")?.label).toBe("mihomo");
    expect(clientOfFile("for-alice-surge-mac")?.label).toBe("Surge Mac");
  });

  it("names no client it cannot see", () => {
    expect(clientOfFile("for-cdcd-self-use")).toBeNull();
    expect(clientOfFile("for-openjobs-fangfang")).toBeNull();
    expect(clientOfFile("stashed-rules")).toBeNull();
    expect(clientOfFile("gloon")).toBeNull();
  });
});

describe("the lineage graph", () => {
  const { items, shares } = rows(productionFixture());
  const lineage = buildLineage(items, shares);

  it("puts every record and the share in its column", () => {
    expect(lineage.columns.source).toHaveLength(5);
    expect(lineage.columns.combination).toHaveLength(2);
    expect(lineage.columns.file).toHaveLength(16);
    expect(lineage.columns.share).toEqual([lineage.shareNodes.get(shares[0]!.share_id)]);
    expect(lineage.broken).toEqual([]);
  });

  it("draws an edge for every member, node source and share", () => {
    const into = (id: string) => [...(lineage.upstream.get(id) ?? [])].sort();
    expect(into("imported-col-merge-cd-openjobs")).toEqual(["imported-cdcd-self-host", "imported-openjobs-host"]);
    expect(into("imported-col-merge-openjobs")).toEqual(["imported-openjobs-host"]);
    expect(into("imported-file-for-cdcd-loon")).toEqual(["imported-col-merge-cd-openjobs"]);
    expect(into(lineage.shareNodes.get(shares[0]!.share_id)!)).toEqual(["imported-file-for-cdcd-loon"]);
    // Two member edges, two plus one, twelve node sources and the share.
    expect(lineage.edges).toHaveLength(3 + 13 + 1);
  });

  it("keeps a share and a record called share:<its id> apart", () => {
    const share = { ...shares[0]!, share_id: "sh-1", subscription_id: "imported-openjobs-host" };
    const clash = item({ id: "share:sh-1", kind: "collection", members: ["imported-openjobs-host"] });
    const clashing = buildLineage([...items, clash], [share]);
    const node = clashing.shareNodes.get("sh-1")!;
    expect(node).not.toBe("share:sh-1");
    expect(clashing.nodes.get("share:sh-1")?.stage).toBe("combination");
    expect(clashing.nodes.get(node)?.stage).toBe("share");
    expect(clashing.upstream.get("share:sh-1")).toEqual(["imported-openjobs-host"]);
    expect(clashing.upstream.get(node)).toEqual(["imported-openjobs-host"]);
    expect(clashing.columns.combination).toContain("share:sh-1");
    expect(clashing.columns.share).toEqual([node]);
    expect(shareNodeId(share, new Set(["share:sh-1", "share:share:sh-1"]))).toBe("share:share:share:sh-1");
  });

  it("orders sources by the combination they feed", () => {
    expect(lineage.columns.combination).toEqual(["imported-col-merge-cd-openjobs", "imported-col-merge-openjobs"]);
    expect(lineage.columns.source.slice(0, 2)).toEqual(["imported-cdcd-self-host", "imported-openjobs-host"]);
  });

  it("resolves member tags to the sources that carry them", () => {
    const tagged = buildLineage(
      [
        item({ id: "a", tags: ["home"] }),
        item({ id: "b", tags: ["work"] }),
        item({ id: "c", tags: ["home"], kind: "collection" }),
        item({ id: "combo", kind: "collection", members: ["b"], member_tags: ["home"] }),
      ],
      [],
    );
    expect(tagged.upstream.get("combo")).toEqual(["b", "a"]);
    expect(tagged.edges.find((edge) => edge.from === "a")).toMatchObject({ via: "tag", tag: "home" });
  });

  it("reports references nothing answers instead of drawing them", () => {
    const failing = rows(failingFixture());
    const broken = buildLineage(failing.items, failing.shares).broken;
    expect(broken).toContainEqual({ owner: "imported-col-merge-openjobs", ref: "imported-openjobs-host-old", via: "member", reason: "no longer exists" });
    expect(broken).toContainEqual({ owner: "imported-file-for-loon-novpn", ref: "imported-col-merge-retired", via: "node-source", reason: "no longer exists" });
    const wrongKind = buildLineage([item({ id: "f", kind: "file" }), item({ id: "combo", kind: "collection", members: ["f"] })], []).broken;
    expect(wrongKind[0]?.reason).toBe("is a file, not a subscription");
  });

  it("highlights the whole path through a record and leaves its siblings out", () => {
    const path = pathOf(lineage, "imported-col-merge-cd-openjobs");
    expect(path.has("imported-cdcd-self-host")).toBe(true);
    expect(path.has("imported-openjobs-host")).toBe(true);
    expect(path.has("imported-file-for-cdcd-stash")).toBe(true);
    expect(path.has(lineage.shareNodes.get(shares[0]!.share_id)!)).toBe(true);
    expect(path.has("imported-col-merge-openjobs")).toBe(false);
    expect(path.has("imported-file-for-openjobs-loon")).toBe(false);
    // Upstream of a file walks back through its combination to the sources.
    const fromFile = pathOf(lineage, "imported-file-for-cdcd-loon");
    expect(fromFile.has("imported-cdcd-self-host")).toBe(true);
    expect(fromFile.has("imported-file-for-cdcd-stash")).toBe(false);
  });

  it("says what a record feeds in a sentence", () => {
    expect(usedBySentence(lineage, "imported-openjobs-host")).toBe("feeds 2 combinations");
    expect(usedBySentence(lineage, "imported-col-merge-cd-openjobs")).toBe("feeds 4 files");
    expect(usedBySentence(lineage, "imported-file-for-cdcd-loon")).toBe("published at /cdcd");
    expect(usedBySentence(lineage, "imported-cdcd-self-hostbak-20260820")).toBe("");
  });
});

describe("the attention rules", () => {
  it("finds production's two facts and nothing else", () => {
    const { items, shares } = rows(productionFixture());
    const found = attentionItems({ items, shares, lineage: buildLineage(items, shares), now: Date.now() });
    expect(found.map((entry) => entry.key)).toEqual(["provider:imported-unnamed", "files:unpublished"]);
    expect(found[0]).toMatchObject({ tone: "warning", recordId: "imported-unnamed" });
    expect(found[0]!.claim).toBe("建材市场: provider expires in 6 days, 82% of its traffic used");
    expect(found[1]!.claim).toBe("15 files are not published, so no client can fetch them");
    expect(found[1]!.action).toEqual({ label: "Review", view: "files", facet: { published: "no" } });
  });

  it("puts every failure first, worst tone first, and masks what the provider said", () => {
    const { items, shares } = rows(failingFixture());
    const found = attentionItems({ items, shares, lineage: buildLineage(items, shares), now: Date.now() });
    const keys = found.map((entry) => entry.key);
    expect(keys).toEqual([
      "fetch:imported-openjobs-host-trojan",
      "provider:imported-openjobs-host-trojan",
      "members:imported-col-merge-openjobs",
      "source:imported-file-for-loon-novpn",
      "share:sh-oj-loon",
      "provider:imported-unnamed",
      "files:unpublished",
      "share:sh-oj-stash",
      "unused:imported-col-merge-spare",
    ]);
    const fetch = found[0]!;
    expect(fetch.tone).toBe("danger");
    expect(fetch.claim).toContain("https://sub.example-provider.com/…?…");
    expect(fetch.claim).not.toContain("token=");
    expect(found[1]!.claim).toContain("expired 2 days ago");
    expect(found[1]!.claim).toContain("110% of its traffic used");
    expect(found[2]!.claim).toBe("merge-openjobs names 1 member that does not resolve (imported-openjobs-host-old), and 5 files render it");
    expect(found[3]!.claim).toBe("for-loon-novpn renders imported-col-merge-retired, which no longer exists");
    expect(found.at(-1)).toMatchObject({ tone: "neutral", claim: "merge-spare is not used by any file or share" });
  });

  it("is empty for an empty store, and says so when the share list could not be read", () => {
    expect(attentionItems({ items: [], shares: [], lineage: buildLineage([], []), now: NOW })).toEqual([]);
    const { items } = rows(productionFixture());
    const unread = attentionItems({ items, shares: undefined, sharesError: "timeout", lineage: buildLineage(items, undefined), now: Date.now() });
    expect(unread.map((entry) => entry.key)).toEqual(["provider:imported-unnamed", "shares:unread"]);
    // Without the list and without an error it is simply not known yet: no claim at all.
    expect(attentionItems({ items, shares: undefined, lineage: buildLineage(items, undefined), now: Date.now() }).map((e) => e.key)).toEqual(["provider:imported-unnamed"]);
  });

  it("warns at 80 percent and 14 days, not before", () => {
    const at = (days: number) => Math.floor((NOW + days * DAY + 3600_000) / 1000);
    const check = (upload: number, days: number) =>
      attentionItems({
        items: [item({ id: "p", source: "remote", has_url: true, upload, total: 100, expire: at(days) })],
        shares: [],
        lineage: buildLineage([], []),
        now: NOW,
      }).map((entry) => entry.tone);
    expect(check(80, 15)).toEqual([]);
    expect(check(81, 15)).toEqual(["warning"]);
    expect(check(10, 14)).toEqual(["warning"]);
    expect(check(100, 30)).toEqual(["danger"]);
    expect(check(10, -1)).toEqual(["danger"]);
  });
});

describe("the state of one record", () => {
  const { items, shares } = rows(failingFixture());
  const lineage = buildLineage(items, shares);
  const byId = (id: string) => items.find((entry) => entry.id === id)!;

  it("names the worst fact first", () => {
    expect(recordHealth(byId("imported-col-merge-openjobs"), lineage, shares, Date.now()).tone).toBe("error");
    expect(recordHealth(byId("imported-openjobs-host-trojan"), lineage, shares, Date.now()).label).toBe("refresh failed");
    expect(recordHealth(byId("imported-unnamed"), lineage, shares, Date.now())).toMatchObject({ tone: "warning", label: "expires in 6 days" });
  });

  it("is neutral, never healthy, until something has been checked", () => {
    const self = byId("imported-cdcd-self-host");
    expect(recordHealth(self, lineage, shares, Date.now()).tone).toBe("neutral");
    expect(recordHealth(self, lineage, shares, Date.now(), { ok: true }).tone).toBe("healthy");
    expect(recordHealth(self, lineage, shares, Date.now(), { ok: false, reason: "timeout" })).toMatchObject({ tone: "warning", label: "preview failed" });
    expect(recordHealth(byId("imported-file-for-cdcd-egern"), lineage, undefined, Date.now()).label).toBe("unknown");
    expect(recordHealth(byId("imported-file-for-cdcd-loon"), lineage, shares, Date.now()).tone).toBe("healthy");
    expect(recordHealth(byId("imported-file-for-cdcd-egern"), lineage, shares, Date.now()).label).toBe("not published");
  });
});
