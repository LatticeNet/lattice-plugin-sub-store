import { describe, expect, it } from "vitest";

import {
  UNTAGGED,
  collectTags,
  matchesQuery,
  matchesSearch,
  matchesTag,
  normalizeQuery,
  type SearchableRecord,
} from "./recordSearch";

/**
 * The list filter used to exist three times over, with different field sets,
 * and the copies had drifted. These pin the behaviour both screens now share:
 * what the box matches, that "untagged" is selectable, and that the chip counts
 * and the rows cannot disagree because they run the same predicate.
 */

const record = (over: Partial<SearchableRecord> = {}): SearchableRecord => ({
  id: "home-nodes",
  name: "Home nodes",
  ...over,
});

describe("normalizeQuery", () => {
  it("folds case and trims", () => {
    expect(normalizeQuery("  Provider A  ")).toBe("provider a");
  });

  it("treats nothing, empty and whitespace alike", () => {
    expect(normalizeQuery(undefined)).toBe("");
    expect(normalizeQuery(null)).toBe("");
    expect(normalizeQuery("   ")).toBe("");
  });
});

describe("matchesQuery", () => {
  it("matches everything when there is no query", () => {
    expect(matchesQuery(record(), "")).toBe(true);
  });

  it("matches the name, the display name, the id and the remark", () => {
    const item = record({ display_name: "Home", remark: "the flat", id: "abc-123" });
    expect(matchesQuery(item, "home nodes")).toBe(true);
    expect(matchesQuery(item, "home")).toBe(true);
    expect(matchesQuery(item, "abc-1")).toBe(true);
    expect(matchesQuery(item, "flat")).toBe(true);
  });

  it("matches tags, which the Files screen's own copy of this never did", () => {
    expect(matchesQuery(record({ tags: ["phone", "backup"] }), "backup")).toBe(true);
    expect(matchesQuery(record({ tags: ["phone"] }), "backup")).toBe(false);
  });

  it("matches the query as one substring rather than as separate words", () => {
    // "provider b" must not also return "Provider A": splitting on the space
    // would make every multi-word search wider than the operator asked for.
    const providerA = record({ id: "provider-a", name: "Provider A" });
    expect(matchesQuery(providerA, "provider b")).toBe(false);
    expect(matchesQuery(providerA, "provider a")).toBe(true);
  });

  it("is case-insensitive on the record side too", () => {
    expect(matchesQuery(record({ name: "PROVIDER" }), "provider")).toBe(true);
  });

  it("survives records missing every optional field", () => {
    expect(matchesQuery({ id: "x", name: "x" }, "zz")).toBe(false);
  });

  it("matchesSearch normalizes for the caller", () => {
    expect(matchesSearch(record(), "  HOME  ")).toBe(true);
    expect(matchesSearch(record(), undefined)).toBe(true);
  });
});

describe("matchesTag", () => {
  it("passes everything with no filter", () => {
    expect(matchesTag(record({ tags: [] }), "")).toBe(true);
  });

  it("selects by exact tag, not by prefix", () => {
    expect(matchesTag(record({ tags: ["home"] }), "home")).toBe(true);
    expect(matchesTag(record({ tags: ["homelab"] }), "home")).toBe(false);
  });

  it("selects the records no tag chip can reach", () => {
    expect(matchesTag(record({ tags: [] }), UNTAGGED)).toBe(true);
    expect(matchesTag(record(), UNTAGGED)).toBe(true);
    expect(matchesTag(record({ tags: ["home"] }), UNTAGGED)).toBe(false);
  });

  it("uses a sentinel a real tag cannot collide with", () => {
    // Tags are trimmed on the way in, so no stored tag can carry a control
    // character; the pseudo-tag has to stay outside the space of real values.
    expect(UNTAGGED).not.toMatch(/^[\w -]+$/);
  });
});

describe("collectTags", () => {
  it("returns each tag once, sorted", () => {
    const tags = collectTags([
      record({ tags: ["paid", "home"] }),
      record({ tags: ["home"] }),
      record({}),
    ]);
    expect(tags).toEqual(["home", "paid"]);
  });

  it("is empty when nothing is tagged", () => {
    expect(collectTags([record(), record()])).toEqual([]);
  });
});
