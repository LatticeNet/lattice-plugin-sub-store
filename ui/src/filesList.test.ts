import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const SRC = new URL(".", import.meta.url);
const read = (path: string) => readFileSync(new URL(path, SRC), "utf8");

/**
 * Files and Shares sit on the same resource-list chassis as Subscriptions.
 * They had been PcTable, so switching lenses changed personality.
 */
describe("the files list is a resource list, not a table", () => {
  const styles = read("styles.css").replace(/\/\*[\s\S]*?\*\//g, "");
  const screen = read("screens/FilesScreen.vue");
  const shares = read("screens/SharesScreen.vue");

  it("draws rec-list, with no PcTable", () => {
    expect(screen).toContain("<PcPanel");
    expect(screen).toContain('class="rec-list"');
    expect(screen).toContain("<RecKindTabs");
    expect(screen).not.toContain("PcTable");
    expect(screen).not.toContain("rec-files");
    expect(styles).not.toContain(".rec-files");
    expect(styles).not.toContain("grid-template-columns:\n    var(--lt-col-select)");
    expect(shares).toContain('class="rec-list"');
    expect(shares).not.toContain("PcTable");
  });

  it("keeps no sideways scroll rule outside a scroller", () => {
    for (const m of styles.matchAll(/([^{}]+)\{[^}]*width:\s*max-content/g)) {
      const selector = m[1]!.trim();
      expect(selector, selector + " widens rows outside a scroller").toMatch(/^\.pc-batch-bar|^\.lt-batchbar/);
    }
  });

  it("opens the sheet without displacing it sideways", () => {
    const frames = styles.match(/@keyframes sheet-in \{[^}]*\}/s);
    expect(frames, "sheet-in keyframes are gone").not.toBeNull();
    expect(frames![0]).not.toMatch(/translateX/);
  });

  it("puts the whole name and the id in the title", () => {
    expect(screen).toContain(":title=\"nameTitle(item)\"");
    expect(screen).toMatch(/function nameTitle[\s\S]*?item\.display_name \|\| item\.name/);
    expect(screen).toContain('class="rec-ident-name"');
  });

  it("says what a file is on All, and filters kind with tabs", () => {
    expect(screen).toContain('<PcKindChip v-if="kindFilter === \'all\'" :label="kindLabel(item)" />');
    expect(screen).toMatch(/function kindLabel[\s\S]*?"configuration"/);
    expect(screen).toContain('label: "Configuration"');
    expect(screen).toContain('label: "Script"');
    expect(screen).toContain('label: "Plain"');
  });

  it("opens a facts well, not a fake chain, with Open on the row", () => {
    expect(screen).toContain('class="rec-file-facts"');
    expect(screen).toContain('class="rec-open"');
    expect(screen).not.toContain("<RecordChainDetail");
    expect(screen).not.toContain('class="rec-detail-bar"');
  });

  it("opens one document surface from every entry", () => {
    expect(screen).toMatch(/if \(id === "output"\) return openFileSheet\(item, event\);/);
    expect(screen).not.toContain("row-popover-document");
    expect(screen).not.toMatch(/mode: "preview"/);
  });
});
