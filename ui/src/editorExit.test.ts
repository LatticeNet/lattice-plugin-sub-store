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

import { en } from "./messages/en";

// Each editor is three files: the Records screen routes between the list and
// the editors, a composable holds the draft and the guard (useRecordEditor,
// useFileEditor), and a component draws it (SubscriptionEditor.vue,
// FileEditor.vue). What these tests guard is a property of the editor, not of
// a file, so they read each editor's three as one.
const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
const subsScreen = read("./screens/SubscriptionsScreen.vue");
const subsEditorState = read("./useRecordEditor.ts");
const subsEditorView = read("./components/SubscriptionEditor.vue");
const screen = [subsScreen, subsEditorState, subsEditorView].join("\n");
const filesEditorState = read("./useFileEditor.ts");
const filesEditorView = read("./components/FileEditor.vue");
const files = [subsScreen, filesEditorState, filesEditorView].join("\n");

// Both kinds of record have an editor, and the second one to grow it is where
// a rule like this silently diverges: Files had the detail screen and the
// breadcrumb and none of the guard, so its Cancel threw work away without
// asking and its Escape did nothing at all. They are checked together.
const EDITORS = [
  ["the record editor", screen],
  ["the file editor", files],
] as const;

// The decision above is only worth anything if the screens actually ask it.
describe("the editor screens delegate their exits", () => {
  it("routes every exit through the shared guard", () => {
    for (const [name, source] of EDITORS) {
      expect(source, name + " does not use the shared guard").toContain("useEditorExit({");
      expect(source, name + " leaves without asking").toContain(
        'class="lt-breadcrumb-root" @click="leaveEditor"',
      );
      expect(source, name + " has a Cancel that skips the guard").toContain(
        '@click="leaveEditor">{{ t.common.cancel }}</button>',
      );
      // The unconditional teardown. Only the guard and the confirm's own
      // handler may reach it.
      expect(source.match(/@click="cancelEdit\(?\)?"/g) ?? [], name).toHaveLength(0);
      // Escape is how every surface in this frame steps back.
      expect(source, name + " ignores Escape in the editor").toContain("exit.onEscape()");
    }
  });

  it("reads what is layered from the stack, not from a hand-written list", () => {
    // Each screen used to name its own overlays here, and the second screen to
    // grow one forgot: the Files editor shipped with no guard at all. Every
    // overlay registers with overlayStack while it is open now, so this line
    // cannot fall behind the screen it describes. The discard confirm is the
    // guard's own and is still not counted here.
    expect(screen).toMatch(/overlayOpen: \(\) => overlayDepth\(\) > 0/);
    expect(files).toMatch(/overlayOpen: \(\) => overlayDepth\(\) > 0/);
    expect(subsEditorState).toContain('from "./overlayStack"');
    expect(subsScreen).toContain('from "../overlayStack"');
    expect(filesEditorState).toContain("overlayOpen: options.overlayOpen");
  });

  it("snapshots each editor against the fields that screen can edit", () => {
    expect(files).toMatch(/fingerprint: \(\) =>[\s\S]{0,120}draft\.value/);
    expect(files).toMatch(/fingerprint: \(\) =>[\s\S]{0,160}queryParamText\.value/);
    expect((files.match(/markPristine\(\);/g) ?? []).length).toBeGreaterThanOrEqual(2);
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
    // It now cannot be anywhere else: the editor is its own component and the
    // screen renders that component only while editing. The check is that it
    // has not drifted back out to the screen beside the list's dialogs.
    expect(subsEditorView, "the discard confirm left the editor").toContain(':open="discarding"');
    expect(subsScreen, "the discard confirm is back beside the list").not.toContain(':open="discarding"');
    expect(filesEditorView, "the discard confirm left the file editor").toContain(':open="discarding"');
  });

  it("says what is at stake before discarding", () => {
    expect(screen).toMatch(/:open="discarding"/);
    expect(screen).toContain(':title="t.editor.leaveConfirm"');
    expect(screen).toContain(':verb="t.editor.discardVerb"');
    expect(files).toContain(':title="t.fileEditor.leaveConfirm"');
    // The words themselves, in the English table every locale mirrors.
    expect(en.editor.leaveConfirm).toContain("Leave without saving?");
    expect(en.editor.leaveConfirm).toContain("will be lost");
    expect(en.fileEditor.leaveConfirm).toContain("will be lost");
    expect(en.editor.discardVerb).toBe("Discard changes");
  });
});
