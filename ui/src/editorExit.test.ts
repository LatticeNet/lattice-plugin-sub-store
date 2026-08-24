import { describe, expect, it } from "vitest";

import { escapeAction, exitAction, type EditorExitState } from "./editorExit";

function state(over: Partial<EditorExitState> = {}): EditorExitState {
  return { editing: true, dirty: false, overlayOpen: false, ...over };
}

describe("leaving the record editor", () => {
  it("asks before discarding an edit, and only then", () => {
    expect(exitAction(state({ dirty: true }))).toBe("confirm");
    expect(exitAction(state({ dirty: false }))).toBe("leave");
  });

  it("does nothing when the editor is not the current screen", () => {
    expect(exitAction(state({ editing: false, dirty: true }))).toBe("ignore");
    expect(escapeAction(state({ editing: false }))).toBe("ignore");
  });

  it("lets an overlay keep Escape", () => {
    // Dismissing a confirm dialog must not also walk out of the screen behind
    // it, and closing the client sheet must not abandon the draft.
    expect(escapeAction(state({ overlayOpen: true }))).toBe("ignore");
    expect(escapeAction(state({ overlayOpen: true, dirty: true }))).toBe("ignore");
    // A deliberate click is not Escape: the operator aimed at the exit.
    expect(exitAction(state({ overlayOpen: true, dirty: true }))).toBe("confirm");
  });

  it("treats Escape as the same decision once no overlay wants it", () => {
    expect(escapeAction(state({ dirty: true }))).toBe("confirm");
    expect(escapeAction(state({ dirty: false }))).toBe("leave");
  });
});

import { readFileSync } from "node:fs";

const screen = readFileSync(new URL("./screens/SubscriptionsScreen.vue", import.meta.url), "utf8");

// The decision above is only worth anything if the screen actually asks it.
describe("the editor screen delegates its exits", () => {
  it("routes every exit through the decision", () => {
    expect(screen).toContain("applyExit(exitAction(exitState()))");
    expect(screen).toContain("applyExit(escapeAction(exitState()))");
    expect(screen).toContain('class="lt-breadcrumb-root" @click="leaveEditor"');
    expect(screen).toContain('@click="leaveEditor">Cancel</button>');
    // cancelEdit is the unconditional teardown. Only the guard and the
    // confirm's own handler may reach it.
    expect(screen.match(/@click="cancelEdit\(?\)?"/g) ?? []).toHaveLength(0);
  });

  it("reports every overlay that owns Escape", () => {
    expect(screen).toMatch(
      /overlayOpen:\s*\n?\s*discarding\.value \|\| deleting\.value\.length > 0 \|\| !!drawer\.value \|\| !!targetSheet\.value/,
    );
  });

  it("compares against what the editor opened with, fields outside the draft included", () => {
    expect(screen).toMatch(/JSON\.stringify\(\[draft\.value, common\.value, tagText\.value, memberTagText\.value\]\)/);
    expect((screen.match(/markPristine\(\);/g) ?? []).length).toBeGreaterThanOrEqual(2);
    // After the graph options land, so loading them is not read as an edit.
    expect(screen).toMatch(/loadGraphOptionsForDraft\(false\);[\s\S]{0,200}markPristine\(\);/);
  });

  // Parked next to the list's dialogs, the confirm was never rendered while the
  // editor was up, so the exit silently did nothing at all. The tests passed:
  // every piece existed, in the wrong place.
  it("keeps the confirm inside the only screen that can ask it", () => {
    const editor = screen.slice(
      screen.indexOf('<section v-if="editing" class="configuration"'),
    );
    const listStart = editor.indexOf('<section v-else class="configuration"');
    const editorOnly = listStart > 0 ? editor.slice(0, listStart) : editor;
    expect(editorOnly, "the discard confirm is outside the editor section").toContain(
      ':open="discarding"',
    );
  });

  it("says what is at stake before discarding", () => {
    expect(screen).toMatch(/:open="discarding"/);
    expect(screen).toContain("Leave without saving?");
    expect(screen).toContain("will be lost");
    expect(screen).toContain('verb="Discard changes"');
  });
});
