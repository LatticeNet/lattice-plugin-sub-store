import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

/**
 * Guards for the flows where an operator could lose work or reach a dead end.
 *
 * These read the sources rather than mounting the screens, because what each
 * one protects is a structural property (a handler is scoped to the visible
 * screen, an overlay does not let a key escape, a count is not asserted from a
 * failed read) that has no seam to unit-test through and that is easy to undo
 * by accident. A rendering test would not have caught any of them either: they
 * are all about what happens on the second screen, the failed call, or the key
 * nobody presses in the happy path.
 */
function source(name: string): string {
  return readFileSync(new URL(name, import.meta.url), "utf8");
}

describe("Escape closes exactly the top of the stack", () => {
  // One Escape used to dismiss the "leave without saving?" dialog AND then
  // reach the screen underneath, which re-raised it because the draft was
  // still dirty: the dialog was unclosable from the keyboard. The fix was a
  // `.stop` on every overlay, which left five components each deciding for
  // themselves what a key press meant and two screens each keeping a list of
  // what counted as open.
  //
  // The stronger statement of the same requirement: an overlay registers while
  // it is open, does not touch the key, and the visible screen's one handler
  // closes the top of the stack and stops there.
  it.each([
    "components/lt/LtConfirmDialog.vue",
    "components/TargetSheet.vue",
  ])("%s registers instead of answering the key itself", (file) => {
    const text = source(`./${file}`);
    expect(text).toContain("useOverlayRegistration");
    expect(text).not.toMatch(/@keydown\.esc/);
  });

  it.each(["screens/SubscriptionsScreen.vue", "screens/FilesScreen.vue"])(
    "%s closes the top of the stack before it reads anything of its own",
    (file) => {
      const text = source(`./${file}`);
      const handler = text.slice(text.indexOf("function onDocumentKeydown"));
      const body = handler.slice(0, handler.indexOf("\n}"));
      expect(body).toContain("closeTopOverlay()");
      // First, not eventually: a row menu or an editor acting on a key that
      // belonged to the panel above it is the same bug in the other direction.
      expect(body.indexOf("closeTopOverlay()")).toBeLessThan(body.indexOf("closeRowMenu()"));
    },
  );

  it("leaves the selection bar out of the stack", () => {
    // The batch bar is not an overlay: it takes no scrim, it dismisses nothing,
    // and Escape inside it clears a selection, which is a gesture scoped to the
    // bar rather than a layer to close. The chassis's bar keeps its own scoped
    // handler for exactly that reason; the screens draw that bar and no other.
    for (const file of ["screens/SubscriptionsScreen.vue", "screens/FilesScreen.vue"]) {
      const text = source(`./${file}`);
      expect(text, file).toContain("<PcBatchBar");
      expect(text, file).not.toContain("LtBatchBar");
    }
  });

  it("appears without moving the rows the selection is being made in", () => {
    // The bar used to sit in the flow above the list, so ticking the first
    // checkbox inserted a 42px block and dropped every row 42px in the same
    // frame: the point the cursor was aiming at for the second row now held
    // the checkbox just ticked. The chassis's bar is fixed to the frame, and
    // the page keeps room for it at its foot so it never covers the last row
    // it is acting on: each lens reports its selection and the shell hands
    // that to the workspace.
    const shell = source("./Shell.vue");
    expect(shell).toContain('<PcWorkspace :batch="lens.selected > 0">');
    for (const file of ["screens/SubscriptionsScreen.vue", "screens/FilesScreen.vue"]) {
      expect(source(`./${file}`), file).toMatch(/chrome\.lenses\.(subscriptions|files)\.selected = count/);
    }
  });
});

describe("only the visible screen owns the document keys", () => {
  // The shell keeps both record screens alive across tab switches, so
  // onBeforeUnmount does not run on a tab change. Both screens' Escape
  // handlers stayed bound at once, and Escape acted on the hidden one.
  it.each(["screens/SubscriptionsScreen.vue", "screens/FilesScreen.vue"])(
    "%s releases its listeners on deactivate",
    (file) => {
      const text = source(`./${file}`);
      expect(text).toContain("onDeactivated(releaseDocumentKeys)");
      expect(text).toContain("onActivated(bindDocumentKeys)");
      expect(text).toContain("onBeforeUnmount(releaseDocumentKeys)");
      // The raw calls must not come back alongside the scoped pair, or the
      // listeners get bound twice and removed once.
      expect(text).not.toMatch(/onMounted\(\(\) => \{\s*document\.addEventListener/);
    },
  );
});

describe("a batch delete that stops part way says so", () => {
  const text = source("./screens/SubscriptionsScreen.vue");

  it("records what was done, what failed, and what was never attempted", () => {
    expect(text).toContain("deleteRemainder");
    expect(text).toMatch(/done:\s*string\[\];\s*failed:\s*string;\s*pending:\s*string\[\]/);
  });

  it("offers a retry of only the remainder", () => {
    expect(text).toContain("retryDeleteRemainder");
    expect(text).toMatch(/deleting\.value = \[remainder\.failed, \.\.\.remainder\.pending\]/);
  });

  // The old code cleared the selection in `finally`, so the records that were
  // never attempted had to be found again by hand.
  it("keeps the selection when there is a remainder to retry", () => {
    expect(text).toContain("if (!deleteRemainder.value) selectedIds.value = new Set()");
  });
});

describe("the delete confirm names what it will break", () => {
  const text = source("./screens/SubscriptionsScreen.vue");

  it("finds dependents instead of describing them in general terms", () => {
    expect(text).toContain("deleteDependents");
    // A combination names its parts; a file names its node source. Both are
    // computable from the list already on screen.
    expect(text).toMatch(/item\.members \?\? \[\]/);
    expect(text).toContain("item.node_source && doomed.has(item.node_source)");
  });

  it("says nothing points at it when nothing does", () => {
    expect(text).toContain("Nothing else in this store points at ${object}");
  });

  // Regression: folding the dependents into `names` inflated the typed-arming
  // count, so deleting one record asked the operator to type 4.
  it("keeps consequences out of the list the arming count is taken from", () => {
    expect(text).toContain(':names="deletingNames"');
    expect(text).toContain(':consequences="deleteConsequences"');
  });
});

describe("absent is not rendered as zero", () => {
  const text = source("./screens/SharesScreen.vue");

  it("does not count a share list that failed to load", () => {
    expect(text).toContain("if (store.error.value && store.shares.value === undefined) return null");
  });

  it("still offers a way forward when the session cannot read shares", () => {
    // The permission wall used to be the one state with nothing to press.
    const wall = text.slice(text.indexOf("cannot read the share list"));
    expect(wall.slice(0, 900)).toContain("openInNetworking()");
  });
});

describe("no copy site talks to the clipboard directly", () => {
  // The frame is denied the Clipboard API by Permissions Policy, so a direct
  // call is always the bug this pass fixed, in a place it was missed.
  it.each([
    "screens/SharesScreen.vue",
    "screens/SubscriptionsScreen.vue",
    "screens/SettingsScreen.vue",
    "components/TargetSheet.vue",
    "components/lt/LtCopyButton.vue",
  ])("%s goes through hostClipboard", (file) => {
    expect(source(`./${file}`)).not.toContain("navigator.clipboard");
  });
});
