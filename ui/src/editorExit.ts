/**
 * What leaving the record editor should do.
 *
 * The editor is a screen you enter and it does not autosave, while both ways
 * out are a single gesture: a click on the breadcrumb and a press of Escape.
 * Either of them discarding an edit without asking is the kind of loss nobody
 * files a bug about, they just stop trusting the form. The decision lives here
 * rather than inline so it can be exercised without mounting a 1600 line screen.
 */

export interface EditorExitState {
  /** The editor is the current screen. */
  editing: boolean;
  /** The draft differs from what the editor opened with. */
  dirty: boolean;
  /** A confirm, a delete, a drawer or the client sheet is open over it. */
  overlayOpen: boolean;
}

export type ExitAction = "ignore" | "leave" | "confirm";

/**
 * A deliberate exit: the breadcrumb, the Cancel button. It never ignores,
 * because the operator asked to leave; it only decides whether to ask first.
 */
export function exitAction(state: EditorExitState): ExitAction {
  if (!state.editing) return "ignore";
  return state.dirty ? "confirm" : "leave";
}

/**
 * Escape. Overlays own it while they are open, which is the whole reason this
 * is a separate decision: dismissing a confirm dialog must not also walk out of
 * the screen behind it.
 */
export function escapeAction(state: EditorExitState): ExitAction {
  if (state.overlayOpen) return "ignore";
  return exitAction(state);
}
