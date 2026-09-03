import { describe, expect, it } from "vitest";

import { conflictChanges, conflictSummary, describeValue } from "./recordConflict";
import type { SubscriptionRecord } from "./client";

function rec(overrides: Partial<SubscriptionRecord> = {}): SubscriptionRecord {
  return { id: "s1", name: "provider", ...overrides };
}

describe("describeValue", () => {
  it("names emptiness rather than printing nothing", () => {
    expect(describeValue(undefined)).toBe("empty");
    expect(describeValue("")).toBe("empty");
    expect(describeValue([])).toBe("empty");
  });

  it("reads booleans as the switch they are in the editor", () => {
    expect(describeValue(true)).toBe("on");
    expect(describeValue(false)).toBe("off");
  });

  it("prints a short list but counts a long one", () => {
    expect(describeValue(["home", "paid"])).toBe("home, paid");
    expect(describeValue(Array.from({ length: 12 }, (_, i) => `tag-number-${i}`))).toBe("12 entries");
  });

  // The operator is deciding whose edit wins, not reading a diff of a 40 KB
  // config, so long values become a size.
  it("summarises long text by length", () => {
    expect(describeValue("x".repeat(4000))).toBe("4000 characters");
  });

  it("counts structured values instead of dumping them", () => {
    expect(describeValue([{ a: 1 }, { b: 2 }])).toBe("2 entries");
    expect(describeValue({ a: "1", b: "2" })).toBe("2 keys");
  });
});

describe("conflictChanges", () => {
  it("lists only the fields that actually moved", () => {
    const changes = conflictChanges(
      rec({ name: "old", ua: "Surge" }),
      rec({ name: "new", ua: "Surge" }),
    );
    expect(changes.map((change) => change.label)).toEqual(["Name"]);
    expect(changes[0]).toMatchObject({ before: "old", after: "new", contested: false });
  });

  it("marks a field the operator also edited as contested", () => {
    const opened = rec({ name: "old", remark: "note" });
    const theirs = rec({ name: "theirs", remark: "note" });
    const mine = rec({ name: "mine", remark: "note" });
    const [change] = conflictChanges(opened, theirs, mine);
    expect(change!.contested).toBe(true);
  });

  // Editing a field back to the value it already had is not a contest: both
  // sides agree on the outcome.
  it("does not call a field contested when the draft matches what was opened", () => {
    const opened = rec({ name: "old" });
    const theirs = rec({ name: "theirs" });
    const mine = rec({ name: "old" });
    const [change] = conflictChanges(opened, theirs, mine);
    expect(change!.contested).toBe(false);
  });

  // The wire omits an empty list, a draft sends [], and a cleared field is "".
  // All three read as "nothing set", and rows saying a field went from "empty"
  // to "empty" are exactly the noise that stops operators reading the list.
  it("treats absent, null and every spelling of empty as the same value", () => {
    expect(conflictChanges(rec({ remark: "" }), rec({ remark: undefined }))).toEqual([]);
    expect(conflictChanges(rec({ tags: [] }), rec({ tags: undefined }))).toEqual([]);
    expect(conflictChanges(rec({ arguments: {} }), rec({ arguments: undefined }))).toEqual([]);
    expect(conflictChanges(rec({ process: [] }), rec({ process: undefined }))).toEqual([]);
    // But a real value against empty is still a change.
    expect(conflictChanges(rec({ tags: [] }), rec({ tags: ["home"] }))).toHaveLength(1);
  });

  it("compares arrays and objects by content, not by identity", () => {
    expect(conflictChanges(rec({ tags: ["a", "b"] }), rec({ tags: ["a", "b"] }))).toEqual([]);
    expect(conflictChanges(rec({ tags: ["a"] }), rec({ tags: ["b"] }))).toHaveLength(1);
  });

  // A script file's program lives behind its digest, so a program-only change
  // still surfaces as a named field rather than as nothing at all.
  it("reports a program change through its digest", () => {
    const changes = conflictChanges(
      rec({ script_digest: "aaaa" }),
      rec({ script_digest: "bbbb" }),
    );
    expect(changes.map((change) => change.label)).toEqual(["Program"]);
  });

  it("returns nothing when either side is missing", () => {
    expect(conflictChanges(null, rec())).toEqual([]);
    expect(conflictChanges(rec(), null)).toEqual([]);
  });
});

describe("conflictSummary", () => {
  // The dangerous case: the backend refused the write, so something did change,
  // and an empty diff must not be reported as "nothing changed".
  it("stays honest when it cannot name what moved", () => {
    const text = conflictSummary([]);
    expect(text).toContain("was changed while you had it open");
    expect(text).not.toMatch(/nothing|no change/i);
  });

  it("says a reopen is safe when nothing is contested", () => {
    const text = conflictSummary([
      { label: "Remark", before: "a", after: "b", contested: false },
    ]);
    expect(text).toContain("none of them fields you edited");
    expect(text).toContain("Reopening keeps both changes");
  });

  it("says plainly that saving anyway replaces their work", () => {
    const text = conflictSummary([
      { label: "Name", before: "a", after: "b", contested: true },
      { label: "Remark", before: "c", after: "d", contested: false },
    ]);
    expect(text).toContain("2 fields");
    expect(text).toContain("1 of them is a field you also edited");
    expect(text).toContain("replaces their version with yours");
  });
});
