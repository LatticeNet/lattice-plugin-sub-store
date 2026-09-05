import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const SRC = new URL(".", import.meta.url);
const read = (path: string) => readFileSync(new URL(path, SRC), "utf8");

/**
 * The Files lens is the chassis's table card, the same one the Subscriptions
 * lens and the Lines page draw, rather than a grid of its own.
 *
 * It had its own grid once, borrowed from the sibling table without the
 * ancestor its column tokens lived on, so every cell stacked into one column
 * and the document scrolled sideways to 687px on a 375px screen. The chassis
 * owns the narrow forms now: a pinned name column under 720px and a stacked
 * row under 480px, both measured on the Lines page.
 */
describe("the files list is the chassis table", () => {
  const styles = read("styles.css").replace(/\/\*[\s\S]*?\*\//g, "");
  const screen = read("screens/FilesScreen.vue");

  it("draws the chassis card and table, with no grid of its own", () => {
    expect(screen).toContain("<PcPanel");
    expect(screen).toMatch(/<PcTable v-else :min-width="\d+" label="Files">/);
    expect(screen).not.toContain("rec-files");
    expect(styles).not.toContain(".rec-files");
    expect(styles).not.toContain("grid-template-columns:\n    var(--lt-col-select)");
  });

  it("keeps no sideways scroll rule outside a scroller", () => {
    // The table wrap is the only thing allowed to scroll sideways, and it is
    // the chassis's; nothing in this sheet widens a row past the frame.
    for (const m of styles.matchAll(/([^{}]+)\{[^}]*width:\s*max-content/g)) {
      const selector = m[1]!.trim();
      expect(selector, selector + " widens rows outside a scroller").toMatch(/^\.pc-batch-bar|^\.lt-batchbar/);
    }
  });

  /**
   * The sheet's entrance does not push it out of the frame.
   *
   * `.sheet` is `right: 0`, so its right edge is already flush with the frame's;
   * a `translateX` entrance moved the whole panel outside for the length of the
   * animation and the document grew by exactly that much (391 on a 375 frame).
   * The browser drive in e2e/ measures it frame by frame; this is the guard CI
   * can run, because CI has no browser.
   */
  it("opens the sheet without displacing it sideways", () => {
    const frames = styles.match(/@keyframes sheet-in \{[^}]*\}/s);
    expect(frames, "sheet-in keyframes are gone").not.toBeNull();
    expect(frames![0]).not.toMatch(/translateX/);
  });

  it("puts the whole name and the id in the title", () => {
    expect(screen).toMatch(/<PcNameCell :name="item\.display_name \|\| item\.name" :id="item\.id" :title="nameTitle\(item\)"/);
    expect(screen).toMatch(/function nameTitle[\s\S]*?item\.display_name \|\| item\.name/);
  });

  it("says what a file is on its row, since the rows are not grouped by kind", () => {
    expect(screen).toContain('<PcKindChip :label="kindLabel(item)" />');
    expect(screen).toMatch(/function kindLabel[\s\S]*?"configuration"/);
  });

  it("opens one document surface from every entry", () => {
    // The row menu's "Show document" and the palette open the sheet. The
    // drawer kept a second viewer, capped at eight lines and scrolling inside
    // itself.
    expect(screen).toMatch(/if \(id === "output"\) return openFileSheet\(item, event\);/);
    expect(screen).not.toContain("row-popover-document");
    expect(screen).not.toMatch(/mode: "preview"/);
  });
});
