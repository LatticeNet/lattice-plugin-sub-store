import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const SRC = new URL(".", import.meta.url);
const read = (path: string) => readFileSync(new URL(path, SRC), "utf8");

/**
 * The Files list fits 375 (DESIGN-PROGRAM 1, the 375 rules, and section 4).
 *
 * It borrowed the subscriptions table's rows, whose column tokens live on
 * .rec-scroll. Without that ancestor the grid template was invalid, so every
 * cell stacked into one column, and the table's max-content rule under 760px
 * ran each row 636px wide: the document scrolled sideways to 687px on a 375px
 * screen. Two guards: the list has a grid of its own, and the table's sideways
 * rules stay scoped to its scroller.
 */
describe("the files list fits the viewport", () => {
  const styles = read("styles.css").replace(/\/\*[\s\S]*?\*\//g, "");
  const screen = read("screens/FilesScreen.vue");

  it("has a grid of its own", () => {
    expect(screen).toContain('<div class="rec-files">');
    expect(styles).toMatch(/\.rec-files \.rec-head,\s*\.rec-files \.rec \{[^}]*grid-template-columns/s);
  });

  it("keeps the table's sideways scroll to the table", () => {
    const narrow = styles.slice(styles.indexOf("@media (max-width: 760px)"));
    for (const m of narrow.matchAll(/([^{}]+)\{[^}]*width:\s*max-content/g)) {
      const selector = m[1]!.trim();
      expect(selector, selector + " widens rows outside .rec-scroll").toMatch(/^\.rec-scroll /);
    }
    // The source cell is the row's second line under the name, not a column.
    expect(narrow).toMatch(/\.rec-files \.rec-status-cell \{[^}]*grid-column: 3 \/ -1/s);
    expect(narrow).toMatch(/\.rec-files \.rec-head-source \{ display: none; \}/);
  });

  it("puts the whole name in the title and ellipses the cell", () => {
    expect(screen).toMatch(/class="rec-name"[\s\S]*?:title="nameTitle\(item\)"/);
    expect(screen).toMatch(/function nameTitle[\s\S]*?item\.display_name \|\| item\.name/);
    expect(styles).toMatch(/\.rec-name-text \{[^}]*text-overflow: ellipsis/);
  });

  it("opens one document surface from every entry", () => {
    // The name, the » and the row menu all open the sheet. The drawer kept a
    // second viewer, capped at eight lines and scrolling inside itself.
    expect(screen).toMatch(/if \(id === "output"\) return openFileSheet\(item, event\);/);
    expect(screen).not.toContain("row-popover-document");
    expect(screen).not.toMatch(/mode: "preview"/);
  });
});
