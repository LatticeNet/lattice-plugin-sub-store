import { describe, expect, it } from "vitest";

import { KIND_COLLECTION, KIND_FILE, KIND_SUB, type SubscriptionListItem } from "./client";
import {
  RECORD_ACTIONS,
  actionsFor,
  batchActionsFor,
  type ActionCapabilities,
} from "./recordActions";

function record(over: Partial<SubscriptionListItem> = {}): SubscriptionListItem {
  return { id: "r1", name: "r1", kind: KIND_SUB, ...over } as SubscriptionListItem;
}

function caps(over: Partial<ActionCapabilities> = {}): ActionCapabilities {
  return { ready: true, mutate: true, fetch: true, preview: true, render: true, publish: true, ...over };
}

const idsOf = (record: SubscriptionListItem, c = caps()) => actionsFor(record, c).map((a) => a.id);

describe("what a record offers", () => {
  it("offers a file the document, not its node list", () => {
    // A file is the document it serves. Its nodes are an implementation detail
    // of how the document gets filled in.
    expect(idsOf(record({ kind: KIND_FILE }))).not.toContain("preview");
    expect(idsOf(record({ kind: KIND_SUB }))).toContain("preview");
    const output = actionsFor(record({ kind: KIND_FILE }), caps()).find((a) => a.id === "output");
    expect(output?.label).toBe("Show document");
    expect(actionsFor(record(), caps()).find((a) => a.id === "output")?.label).toBe("Client output…");
  });

  it("does not offer a file a refresh it has no source for", () => {
    expect(idsOf(record({ kind: KIND_FILE }))).not.toContain("refresh");
    expect(idsOf(record({ kind: KIND_COLLECTION }))).toContain("refresh");
  });

  it("keeps the destructive action last and marked", () => {
    const actions = actionsFor(record(), caps());
    expect(actions.at(-1)?.id).toBe("delete");
    expect(actions.at(-1)?.danger).toBe(true);
    expect(actions.filter((a) => a.danger).map((a) => a.id)).toEqual(["delete"]);
  });
});

describe("why an action cannot run", () => {
  it("says which capability is missing rather than only greying out", () => {
    const withoutWrite = actionsFor(record(), caps({ mutate: false }));
    const del = withoutWrite.find((a) => a.id === "delete")!;
    expect(del.disabled).toBe(true);
    expect(del.reason).toContain("token lacks the scope");
    // Reading is unaffected by a missing write scope.
    expect(withoutWrite.find((a) => a.id === "preview")?.disabled).toBe(false);
  });

  it("blocks everything until the console has handed over a session", () => {
    const early = actionsFor(record(), caps({ ready: false }));
    expect(early.every((a) => a.disabled)).toBe(true);
    expect(new Set(early.map((a) => a.reason)).size).toBe(1);
  });

  it("gates refreshing on the fetch method, not on write access", () => {
    // These are different capabilities and the screens had always used fetch;
    // a registry that guessed `mutate` would have quietly changed who can
    // refresh.
    expect(actionsFor(record(), caps({ mutate: false })).find((a) => a.id === "refresh")?.disabled).toBe(false);
    expect(actionsFor(record(), caps({ fetch: false })).find((a) => a.id === "refresh")?.disabled).toBe(true);
  });

  it("distinguishes a missing method from a missing scope", () => {
    const noPublishMethod = actionsFor(record(), caps({ publish: false })).find((a) => a.id === "publish")!;
    expect(noPublishMethod.reason).toContain("does not declare");
    const noWrite = actionsFor(record(), caps({ mutate: false })).find((a) => a.id === "publish")!;
    expect(noWrite.reason).toContain("token lacks the scope");
  });

  it("lets output fall back to whichever method the bundle does declare", () => {
    expect(actionsFor(record(), caps({ render: false })).find((a) => a.id === "output")?.disabled).toBe(false);
    expect(actionsFor(record(), caps({ preview: false })).find((a) => a.id === "output")?.disabled).toBe(false);
    expect(
      actionsFor(record(), caps({ preview: false, render: false })).find((a) => a.id === "output")?.disabled,
    ).toBe(true);
  });

  it("every declaration can explain itself when blocked", () => {
    for (const declaration of RECORD_ACTIONS) {
      const reason = declaration.blocked(caps({ ready: false }), record());
      expect(reason, declaration.id + " greys out with no reason").not.toBe("");
    }
  });
});

describe("a batch is judged by every record in it", () => {
  it("refuses the whole selection when one record refuses", () => {
    const rows = [record({ id: "a" }), record({ id: "b" })];
    expect(batchActionsFor(rows, caps())[0]?.disabled).toBe(false);
    // Reporting "Delete 12" and then refusing four is worse than saying up
    // front that the set cannot go.
    expect(batchActionsFor(rows, caps({ mutate: false }))[0]?.disabled).toBe(true);
    expect(batchActionsFor(rows, caps({ mutate: false }))[0]?.reason).toContain("token lacks the scope");
  });

  // What the set is judged on today. The stronger rules — filter by every
  // record's kind, refuse the set if any record refuses — were written and
  // then removed: with one all-kinds, capabilities-only action in the registry
  // neither branch could be reached, so no test could hold them up. The
  // upgrade path is marked in the source.
  it("labels a selection by what it holds", () => {
    const mixed = [record({ id: "a", kind: KIND_SUB }), record({ id: "b", kind: KIND_FILE })];
    expect(batchActionsFor(mixed, caps()).map((a) => a.id)).toEqual(["delete"]);
  });

  it("offers only what a selection can carry", () => {
    expect(batchActionsFor([record()], caps()).map((a) => a.id)).toEqual(["delete"]);
    expect(batchActionsFor([], caps())).toEqual([]);
  });
});

import { readFileSync } from "node:fs";

/**
 * The registry is only worth having if the screens read it. Inline capability
 * expressions are how the five copies happened in the first place: each one
 * drifted, and "why is this greyed out" got a different answer depending on
 * where you clicked.
 */
describe("the screens ask the registry rather than re-deciding", () => {
  const screens = [
    ["SubscriptionsScreen.vue", readFileSync(new URL("./screens/SubscriptionsScreen.vue", import.meta.url), "utf8")],
    ["FilesScreen.vue", readFileSync(new URL("./screens/FilesScreen.vue", import.meta.url), "utf8")],
  ] as const;

  it("builds its capabilities once, from the hook", () => {
    for (const [name, source] of screens) {
      expect(source, name).toMatch(/const actionCaps = computed<ActionCapabilities>/);
      for (const key of ["mutate", "fetch", "preview", "render", "publish"]) {
        expect(source, name + " never reports " + key).toContain(key + ": subs.can");
      }
    }
  });

  it("has no capability check left in the markup", () => {
    for (const [name, source] of screens) {
      const template = source.slice(source.indexOf("<template>"));
      // Creating a record is not an action ON a record, so the create buttons
      // keep their own guard. Everything that acts on one goes through the
      // registry.
      const lines = template.split("\n");
      const offenders = lines
        .map((line, i) => ({ line, context: lines.slice(Math.max(0, i - 6), i + 3).join(" ") }))
        .filter((entry) => /:disabled="!subs\.can(Mutate|Fetch|Preview|Render|Publish)\.value/.test(entry.line))
        .filter((entry) => !/startCreate|atRecordLimit/.test(entry.context))
        .map((entry) => entry.line.trim());
      expect(offenders, name + " decides a record action inline: " + offenders.join(", ")).toEqual([]);
    }
  });

  it("dispatches by action id rather than by menu position", () => {
    for (const [name, source] of screens) {
      expect(source, name).toMatch(/function runRowAction\(id: ActionId/);
      expect(source, name).toContain("RecordMenu");
    }
  });
});
