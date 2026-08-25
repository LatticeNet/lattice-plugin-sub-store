import { readFileSync } from "node:fs";
import { parse } from "vue/compiler-sfc";
import { describe, expect, it } from "vitest";

import { KIND_COLLECTION, KIND_FILE, KIND_SUB, type SubscriptionListItem } from "./client";
import {
  moveSelection,
  paletteActionsFor,
  paletteEntries,
  PALETTE_COMMANDS,
} from "./commandPalette";
import type { ActionCapabilities } from "./recordActions";

function record(over: Partial<SubscriptionListItem> = {}): SubscriptionListItem {
  return { id: "r1", name: "r1", kind: KIND_SUB, ...over } as SubscriptionListItem;
}

function caps(over: Partial<ActionCapabilities> = {}): ActionCapabilities {
  return { ready: true, mutate: true, fetch: true, preview: true, render: true, publish: true, ...over };
}

const RECORDS = [
  record({ id: "home-sub", name: "home", tags: ["self"] }),
  record({ id: "work-col", name: "work", kind: KIND_COLLECTION }),
  record({ id: "phone-file", name: "phone config", kind: KIND_FILE }),
];

describe("what the palette lists", () => {
  it("finds records with the same predicate the list screens use", () => {
    // Not a second matcher: a palette that disagrees with the filter box above
    // the rows is a palette nobody trusts.
    const byTag = paletteEntries("self", RECORDS, caps()).filter((e) => e.kind === "record");
    expect(byTag.map((e) => e.key)).toEqual(["record:home-sub"]);
    const byName = paletteEntries("phone", RECORDS, caps()).filter((e) => e.kind === "record");
    expect(byName.map((e) => e.key)).toEqual(["record:phone-file"]);
  });

  it("says which screen owns each entry, so selecting one can go there", () => {
    const all = paletteEntries("", RECORDS, caps());
    expect(all.find((e) => e.key === "record:phone-file")?.tab).toBe("files");
    expect(all.find((e) => e.key === "record:work-col")?.tab).toBe("subscriptions");
    expect(all.find((e) => e.key === "command:new-file")?.tab).toBe("files");
  });

  it("puts records before commands", () => {
    const kinds = paletteEntries("", RECORDS, caps()).map((e) => e.kind);
    expect(kinds.indexOf("command")).toBeGreaterThan(kinds.lastIndexOf("record"));
  });

  it("names the kind so two records with one name stay apart", () => {
    const entries = paletteEntries("", RECORDS, caps());
    expect(entries.find((e) => e.key === "record:work-col")?.hint).toContain("Combination");
    expect(entries.find((e) => e.key === "record:phone-file")?.hint).toContain("File");
    expect(entries.find((e) => e.key === "record:home-sub")?.hint).toContain("home-sub");
  });

  it("prefers the display name, which is what the list shows", () => {
    const named = [record({ id: "r9", name: "raw-name", display_name: "Home" })];
    expect(paletteEntries("", named, caps())[0]?.label).toBe("Home");
  });

  it("caps how many records it lists rather than rendering the whole store", () => {
    const many = Array.from({ length: 60 }, (_, i) => record({ id: "r" + i, name: "rec " + i }));
    const entries = paletteEntries("rec", many, caps(), 20);
    expect(entries.filter((e) => e.kind === "record")).toHaveLength(20);
  });

  it("keeps a command that cannot run, and says why", () => {
    // Dropping it teaches the operator the feature does not exist, when in
    // fact their token cannot reach it.
    const entries = paletteEntries("new", RECORDS, caps({ mutate: false }));
    const create = entries.filter((e) => e.kind === "command");
    expect(create).toHaveLength(PALETTE_COMMANDS.length);
    expect(create.every((e) => e.disabled)).toBe(true);
    expect(create[0]?.reason).toContain("token lacks the scope");
  });
});

describe("what the palette offers for a chosen record", () => {
  it("takes the actions and the reasons from the registry", () => {
    const offered = ["edit", "preview", "delete"] as const;
    const actions = paletteActionsFor(record(), caps({ mutate: false }), offered);
    expect(actions.map((a) => a.id)).toEqual(["edit", "preview", "delete"]);
    expect(actions.find((a) => a.id === "delete")?.disabled).toBe(true);
    expect(actions.find((a) => a.id === "delete")?.reason).toContain("token lacks the scope");
    expect(actions.find((a) => a.id === "preview")?.disabled).toBe(false);
  });

  it("does not offer a file the actions a file has no use for", () => {
    const offered = ["edit", "preview", "refresh", "delete"] as const;
    const actions = paletteActionsFor(record({ kind: KIND_FILE }), caps(), offered);
    expect(actions.map((a) => a.id)).toEqual(["edit", "delete"]);
  });

  it("marks the destructive one", () => {
    const actions = paletteActionsFor(record(), caps(), ["delete"]);
    expect(actions[0]?.danger).toBe(true);
  });
});

describe("moving through the list", () => {
  it("wraps at both ends so the keyboard never dead-ends", () => {
    expect(moveSelection(0, -1, 3)).toBe(2);
    expect(moveSelection(2, 1, 3)).toBe(0);
    expect(moveSelection(1, 1, 3)).toBe(2);
  });

  it("stays put on an empty list", () => {
    expect(moveSelection(0, 1, 0)).toBe(0);
  });
});

/**
 * The surface, checked at the source. The model above is where the rules live;
 * these are the wiring facts that a refactor can quietly break and no unit
 * test would notice.
 */
describe("the palette's wiring", () => {
  const shell = readFileSync(new URL("./Shell.vue", import.meta.url), "utf8");
  const palette = readFileSync(new URL("./components/CommandPalette.vue", import.meta.url), "utf8");
  const styles = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

  it("is reachable without knowing the shortcut", () => {
    // A palette only Cmd+K can open is one most operators never find, so the
    // entry must actually render — not merely exist in the file behind a
    // condition. Read from the template rather than the text.
    const template = parse(shell, { filename: "Shell.vue" }).descriptor.template;
    if (!template?.ast) throw new Error("Shell template is missing");
    const find = (node: Record<string, any>): Record<string, any> | undefined => {
      const classProp = (node.props ?? []).find((p: any) => p.name === "class");
      if (classProp?.value?.content === "tab-search") return node;
      return (node.children ?? [])
        .filter((c: any) => c.type === 1)
        .map((c: any) => find(c))
        .find(Boolean);
    };
    const button = find(template.ast as Record<string, any>);
    expect(button, "no search entry in the template").toBeTruthy();
    const directives = (button!.props ?? []).filter((p: any) => p.type === 7).map((p: any) => p.name);
    expect(directives, "the search entry is conditional").not.toContain("if");
    expect(directives).not.toContain("show");
    expect(shell).toMatch(/@click="openPalette\(\)"/);
  });

  it("keeps the search entry out of the tablist", () => {
    // A button inside role="tablist" announces itself as a tab and joins the
    // arrow-key order.
    const tablist = shell.slice(shell.indexOf('role="tablist"'), shell.indexOf("</nav>"));
    expect(tablist).not.toContain("tab-search");
  });

  it("reads the shared catalogue rather than loading its own list", () => {
    expect(shell).toContain("recordCatalogue(host)");
    expect(shell).not.toContain("useSubscriptions(");
  });

  it("hands the work to the screen that owns the record", () => {
    // The shell cannot open another screen's drawer; it posts an intent and
    // switches tabs.
    expect(shell).toMatch(/intent\.value = \{ recordId: record\.id, action \}/);
    expect(shell).toMatch(/activeTab\.value = record\.kind === "file" \? "files" : "subscriptions"/);
  });

  it("tells assistive tech which row is active", () => {
    expect(palette).toContain(':aria-activedescendant="rows.length ? `palette-row-${cursor}` : undefined"');
    expect(palette).toContain('role="listbox"');
    expect(palette).toContain(':id="`palette-row-${index}`"');
  });

  // Bound to the input alone, Escape stopped working the moment focus landed
  // anywhere else, and an aria-modal overlay you cannot dismiss is one you are
  // stuck inside. Tab used the helper the drawer and the client sheet already
  // share rather than a fourth version of a focus trap.
  it("lets the dialog own its keys, not the input", () => {
    expect(palette).toMatch(/class="palette"[\s\S]{0,200}@keydown="onKeydown"/);
    const input = palette.slice(palette.indexOf("<input"), palette.indexOf("/>", palette.indexOf("<input")));
    expect(input, "the input still owns the keys").not.toContain("@keydown");
    expect(palette).toContain("trapDialogTab(event, dialog.value)");
    expect(palette).toContain('from "../dialogFocus"');
  });

  it("steps back a level before it closes", () => {
    const escape = palette.slice(palette.indexOf('event.key === "Escape"'));
    expect(escape.slice(0, 400)).toContain("chosen.value = null");
  });

  it("fits a frame narrower than it is", () => {
    // Fixed to the frame, so it must size against the frame rather than assume
    // a desktop. Measured at 373px: 349px wide, no overflow.
    expect(styles).toMatch(/\.palette\s*\{[^}]*width:\s*min\(640px, calc\(100vw/s);
  });
});
