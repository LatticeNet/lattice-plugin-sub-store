import { computed, ref, type ComputedRef, type Ref } from "vue";

import { escapeAction, exitAction, type ExitAction } from "./editorExit";

export interface EditorExitOptions {
  /** True while the editor is the current screen. */
  editing: Ref<boolean>;
  /**
   * Everything an edit can change, serialised. It is the screen's job to name
   * those fields: a draft plus whatever text inputs live outside it, because
   * those are edits too.
   */
  fingerprint: () => string;
  /**
   * True while something the screen owns is layered over the editor. The
   * discard confirm is not included — this composable owns that one.
   */
  overlayOpen: () => boolean;
  /** Drop the draft and return to the list. */
  leave: () => void;
}

export interface EditorExit {
  /** Set while the confirm is deciding whether an unsaved edit may go. */
  discarding: Ref<boolean>;
  dirty: ComputedRef<boolean>;
  /** Snapshot the current draft as the thing edits are measured against. */
  markPristine: () => void;
  /** A deliberate exit: the breadcrumb, the Cancel button. */
  leaveEditor: () => void;
  /** Escape, which overlays own while they are open. */
  onEscape: () => void;
  /** Forget the snapshot and close the confirm; call from the screen's leave. */
  reset: () => void;
}

/**
 * The unsaved-edit guard, shared by every screen with a record editor.
 *
 * Both ways out are a single gesture — a click on the breadcrumb and a press of
 * Escape — and no editor here autosaves, so without a comparison an unsaved
 * edit is one stray keystroke away from being gone with nothing said.
 *
 * It lives here rather than in a screen because the second screen to grow an
 * editor is where this kind of rule silently diverges: Files had the detail
 * screen and the breadcrumb but none of the guard, so its Cancel threw work
 * away without asking and its Escape did nothing at all.
 */
export function useEditorExit(options: EditorExitOptions): EditorExit {
  const pristine = ref("");
  const discarding = ref(false);

  function markPristine(): void {
    pristine.value = options.fingerprint();
  }

  const dirty = computed(
    () => options.editing.value && pristine.value !== options.fingerprint(),
  );

  function state() {
    return {
      editing: options.editing.value,
      dirty: dirty.value,
      overlayOpen: discarding.value || options.overlayOpen(),
    };
  }

  function apply(action: ExitAction): void {
    if (action === "ignore") return;
    if (action === "confirm") {
      discarding.value = true;
      return;
    }
    options.leave();
  }

  return {
    discarding,
    dirty,
    markPristine,
    leaveEditor: () => apply(exitAction(state())),
    onEscape: () => apply(escapeAction(state())),
    reset: () => {
      discarding.value = false;
      pristine.value = "";
    },
  };
}
