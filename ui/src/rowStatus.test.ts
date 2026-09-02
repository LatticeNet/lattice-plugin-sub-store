import { describe, expect, it } from "vitest";

import { formatBytes, formatRelativeTime, formatTraffic, parseUserinfo, tagChips } from "./rowStatus";

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

describe("parseUserinfo", () => {
  it("reads the four known keys regardless of spacing and case", () => {
    expect(parseUserinfo("upload=1; download=2; Total=3; expire=1893456000")).toEqual({
      upload: 1,
      download: 2,
      total: 3,
      expire: 1893456000,
    });
  });

  it("drops junk rather than formatting it", () => {
    expect(parseUserinfo("upload=abc; total=-5; download=7")).toEqual({ download: 7 });
    expect(parseUserinfo("upload; =3; =; download=4")).toEqual({ download: 4 });
  });

  it("returns null when there is nothing usable", () => {
    expect(parseUserinfo(undefined)).toBeNull();
    expect(parseUserinfo("")).toBeNull();
    expect(parseUserinfo("garbage")).toBeNull();
    expect(parseUserinfo("unknown=1")).toBeNull();
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

describe("formatTraffic", () => {
  it("combines used and total, then expiry", () => {
    const info = parseUserinfo("upload=1073741824; download=2147483648; total=536870912000; expire=1893456000");
    expect(formatTraffic(info)).toBe("3 GB / 500 GB · until 2030-01-01");
  });

  it("shows what it has and stays silent about the rest", () => {
    expect(formatTraffic({ total: 1073741824 })).toBe("0 B / 1 GB");
    expect(formatTraffic({ upload: 5, download: 7 })).toBe("12 B used");
    expect(formatTraffic(null)).toBe("");
    expect(formatTraffic({})).toBe("");
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
