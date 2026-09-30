import { describe, expect, it } from "vitest";

import { KIND_COLLECTION, KIND_FILE, KIND_SUB, type SubscriptionListItem } from "./client";
import { BINDINGS, type MethodBinding } from "./client";
import {
  RECORD_ACTIONS,
  ROW_MENU_ACTIONS,
  actionCapabilities,
  actionsFor,
  batchActionsFor,
  deletePrompt,
  rowMenuFor,
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

  it("names the two ways out of the store by their outcome and says what each does", () => {
    // The share action opens the console's form and the record comes back
    // published, so it carries that word; the upload sends a rendered
    // document to a URL and creates no share, so it must not. Both used to
    // read Share… and Publish… side by side, with no title on either.
    const actions = actionsFor(record(), caps());
    expect(actions.find((a) => a.id === "share")?.label).toBe("Publish…");
    expect(actions.find((a) => a.id === "publish")?.label).toBe("Upload document…");
    for (const action of actions) expect(action.title, action.id).toMatch(/\S\.$/);
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

  it("builds its capabilities once, from the shared reader", () => {
    for (const [name, source] of screens) {
      expect(source, name).toContain("const actionCaps = computed<ActionCapabilities>(() => actionCapabilities(host));");
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

  // A menu can hold two blocked items blocked for different reasons: a missing
  // method and a missing scope are not the same problem. Printing the first
  // and stopping attributed one item's reason to the other.
  it("explains every distinct reason, not just the first", () => {
    const menu = readFileSync(new URL("./components/RecordMenu.vue", import.meta.url), "utf8");
    expect(menu).toMatch(/new Set\(props\.actions\.filter\(\(a\) => a\.disabled\)\.map\(\(a\) => a\.reason\)\)/);
    expect(menu).toContain('v-for="reason in reasons()"');
    expect(menu, "still prints only the first").not.toContain("actions.find((a) => a.disabled)?.reason");
  });

  it("dispatches by action id rather than by menu position", () => {
    for (const [name, source] of screens) {
      expect(source, name).toMatch(/function runRowAction\(id: ActionId/);
      expect(source, name).toContain("RecordMenu");
    }
  });
});

describe("one reader of capabilities, one row menu, one delete prompt", () => {
  const host = (missing: MethodBinding[] = [], ready = true) => ({
    init: { value: ready ? {} : null },
    available: (binding: MethodBinding) => !missing.includes(binding),
  });

  it("reads each capability from its own method", () => {
    expect(actionCapabilities(host())).toEqual(caps());
    expect(actionCapabilities(host([BINDINGS.subDelete])).mutate).toBe(false);
    expect(actionCapabilities(host([BINDINGS.subProbe])).fetch).toBe(false);
    expect(actionCapabilities(host([BINDINGS.subPreview])).preview).toBe(false);
    expect(actionCapabilities(host([BINDINGS.subRender])).render).toBe(false);
    expect(actionCapabilities(host([BINDINGS.subPublish])).publish).toBe(false);
    expect(actionCapabilities(host([], false)).ready).toBe(false);
  });

  it("gives every surface the same menu, filtered by kind", () => {
    const sub = rowMenuFor(record({ has_url: true }), caps()).map((a) => a.id);
    expect(sub).toEqual(actionsFor(record({ has_url: true }), caps(), ROW_MENU_ACTIONS).map((a) => a.id));
    expect(rowMenuFor(record({ kind: KIND_FILE }), caps()).map((a) => a.id)).not.toContain("refresh");
    // A file's menu leads with Publish…, which opens the console's form; a source's does not carry it.
    expect(rowMenuFor(record({ kind: KIND_FILE }), caps()).map((a) => a.id)).toEqual(["share", "output", "duplicate", "delete"]);
    expect(rowMenuFor(record({ kind: KIND_FILE }), caps())[0]!.label).toBe("Publish…");
    expect(rowMenuFor(record({ has_url: true }), caps()).map((a) => a.id)).not.toContain("share");
    // A read-only session sees the same items, disabled with a reason.
    const readOnly = rowMenuFor(record(), caps({ mutate: false }));
    const del = readOnly.find((a) => a.id === "delete");
    expect(del?.disabled).toBe(true);
    expect(del?.reason).toBeTruthy();
  });

  it("names the records a delete breaks and keeps them out of the names", () => {
    const items = [
      record({ id: "a", name: "openjobs-host" }),
      record({ id: "c", name: "merge-openjobs", kind: KIND_COLLECTION, members: ["a"] }),
      record({ id: "f", name: "for-openjobs-loon", kind: KIND_FILE, node_source: "a" }),
      record({ id: "b", name: "alone" }),
    ];
    const breaking = deletePrompt(["a"], items);
    expect(breaking.names).toEqual(["openjobs-host"]);
    expect(breaking.consequences).toEqual([
      "merge-openjobs  (combination, loses a member)",
      "for-openjobs-loon  (file, loses its node source)",
    ]);
    expect(breaking.title).toContain("2 other records in this store point at it");
    const quiet = deletePrompt(["b"], items);
    expect(quiet.consequences).toEqual([]);
    expect(quiet.title).toContain("Nothing else in this store points at it");
    const files = deletePrompt(["f"], items);
    expect(files.title).toMatch(/^Delete this file\?/);
  });
});
