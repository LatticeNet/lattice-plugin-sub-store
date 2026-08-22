const FOCUSABLE = [
  "a[href]",
  "button:not(:disabled)",
  "input:not(:disabled)",
  "select:not(:disabled)",
  "textarea:not(:disabled)",
  "[contenteditable='true']",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

/** Keep Tab inside an aria-modal overlay without adding a focus-trap package. */
export function trapDialogTab(event: KeyboardEvent, root: HTMLElement): void {
  if (event.key !== "Tab") return;
  const focusable = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE));
  if (!focusable.length) {
    event.preventDefault();
    root.focus();
    return;
  }
  const first = focusable[0]!;
  const last = focusable[focusable.length - 1]!;
  const target = event.target;
  const inside = target ? root.contains(target as Node) : false;
  if (event.shiftKey && (!inside || target === first || target === root)) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && (!inside || target === last || target === root)) {
    event.preventDefault();
    first.focus();
  }
}
