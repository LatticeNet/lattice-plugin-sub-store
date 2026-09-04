/**
 * Who owns Escape, and what "an overlay is open" means.
 *
 * Before this, every overlay answered Escape itself with `@keydown.esc.stop`,
 * and the `.stop` was load-bearing: without it one press cancelled the discard
 * dialog and then reached the screen, which saw a dirty draft and raised the
 * same dialog again. Each screen also kept its own hand-written list of what
 * counted as open (`deleting.length || drawer || targetSheet`), so the second
 * screen to grow an overlay forgot one, which is exactly how the Files editor
 * shipped without the guard.
 *
 * Both problems are the same problem: no one place knew what was layered over
 * what. This is that place. An overlay registers a close function while it is
 * open, the visible screen's single document handler closes the top of the
 * stack, and "is an overlay open" is the depth rather than a list someone has
 * to remember to extend.
 *
 * Deliberately not reactive and not a store: the two callers are a keydown
 * handler and `useEditorExit`'s state snapshot, both of which read it at the
 * moment of the event. A ref here would invite rendering off it, and an
 * overlay whose own render depends on the stack it is in is a loop.
 */
type CloseFn = () => void;

interface Entry {
  id: number;
  close: CloseFn;
}

const stack: Entry[] = [];
let sequence = 0;

/**
 * Claim the top of the stack until the returned function is called. Registering
 * twice for the same overlay is a bug in the caller, not something this
 * smooths over: each open must pair with exactly one dispose.
 */
export function registerOverlay(close: CloseFn): () => void {
  const entry: Entry = { id: ++sequence, close };
  stack.push(entry);
  return () => {
    const at = stack.findIndex((item) => item.id === entry.id);
    if (at !== -1) stack.splice(at, 1);
  };
}

/** How many overlays are layered right now. */
export function overlayDepth(): number {
  return stack.length;
}

/**
 * Close the topmost overlay, if there is one. Returns whether it did, so the
 * caller can decide what Escape means when nothing was layered.
 *
 * The entry is not popped here. It is the overlay's own state change that
 * unmounts it and runs its dispose, which keeps one path for "this is closed"
 * whether the operator pressed Escape, clicked the scrim, or the screen closed
 * it in code.
 */
export function closeTopOverlay(): boolean {
  const top = stack[stack.length - 1];
  if (!top) return false;
  top.close();
  return true;
}

/** Tests only: a module-level stack outlives a component tree that threw. */
export function resetOverlayStack(): void {
  stack.length = 0;
}
