import { describe, expect, it } from "vitest";

import { readFileSync } from "node:fs";

import { formatBytes, formatRelativeTime, parseUserinfo, tagChips } from "./rowStatus";

const NOW = Date.parse("2026-08-10T12:00:00Z");

describe("formatRelativeTime", () => {
  it("phrases recent fetches loosely and older ones precisely", () => {
    expect(formatRelativeTime("2026-08-10T11:59:40Z", NOW)).toBe("just now");
    expect(formatRelativeTime("2026-08-10T11:45:00Z", NOW)).toBe("15m ago");
    expect(formatRelativeTime("2026-08-10T09:00:00Z", NOW)).toBe("3h ago");
    expect(formatRelativeTime("2026-08-08T12:00:00Z", NOW)).toBe("2d ago");
    expect(formatRelativeTime("2026-06-01T12:00:00Z", NOW)).toBe("2026-06-01");
  });

  // The server stamps the fetch; a browser clock slightly behind is normal,
  // and "-3s ago" would read as a bug rather than as clock skew.
  it("treats a small negative gap as just now", () => {
    expect(formatRelativeTime("2026-08-10T12:00:10Z", NOW)).toBe("just now");
  });

  it("says nothing for a timestamp it cannot parse", () => {
    expect(formatRelativeTime("not a time", NOW)).toBe("");
    expect(formatRelativeTime("", NOW)).toBe("");
  });
});

/**
 * The runtime's cases, read from where its own test reads them, so the UI's
 * fallback and the runtime's parser cannot drift apart unnoticed.
 */
const USERINFO_CASES = JSON.parse(
  readFileSync(new URL("../../system-go/testdata/userinfo_cases.json", import.meta.url), "utf8"),
) as { cases: { name: string; raw: string; want: Record<string, number> }[] };

describe("parseUserinfo, by the runtime's rules", () => {
  it("has the shared cases to check", () => {
    expect(USERINFO_CASES.cases.length).toBeGreaterThan(10);
  });

  for (const tc of USERINFO_CASES.cases) {
    it(tc.name, () => {
      const want = Object.keys(tc.want).length ? tc.want : null;
      expect(parseUserinfo(tc.raw)).toEqual(want);
    });
  }

  it("returns null for no header at all", () => {
    expect(parseUserinfo(undefined)).toBeNull();
  });
});

describe("formatBytes", () => {
  it("scales and keeps integers exact", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(500 * 1024 * 1024 * 1024)).toBe("500 GB");
  });
});

describe("tagChips", () => {
  it("shows two and counts the rest, with the whole list for a title", () => {
    expect(tagChips(["home", "paid", "backup", "self"], false)).toEqual({
      shown: ["home", "paid"],
      more: 2,
      all: ["home", "paid", "backup", "self"],
    });
  });

  it("counts the migration marker as a tag, last", () => {
    expect(tagChips(["paid"], true)).toEqual({ shown: ["paid", "migrated"], more: 0, all: ["paid", "migrated"] });
    expect(tagChips(["a", "b"], true).more).toBe(1);
  });

  it("shows nothing for a record with no tags", () => {
    expect(tagChips(undefined, false)).toEqual({ shown: [], more: 0, all: [] });
  });
});
