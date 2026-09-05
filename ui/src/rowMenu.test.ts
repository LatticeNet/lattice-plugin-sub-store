import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const menu = readFileSync(new URL("./components/RecordMenu.vue", import.meta.url), "utf8");
const styles = readFileSync(new URL("./styles.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const screens = [
  ["SubscriptionsScreen.vue", readFileSync(new URL("./screens/SubscriptionsScreen.vue", import.meta.url), "utf8")],
  ["FilesScreen.vue", readFileSync(new URL("./screens/FilesScreen.vue", import.meta.url), "utf8")],
] as const;

/**
 * The row menu is painted over the rows beneath it.
 *
 * The chassis pins every actions cell (`position: sticky; z-index: 1`), and a
 * z-index makes a stacking context, so a menu positioned inside the cell was
 * painted under the actions cells of every later row: on a seven-row store,
 * the "..." menu on row one showed a blank panel with three items at the
 * bottom. The DOM said seven menuitems; the screen showed three.
 */
describe("the row menu leaves the actions cell", () => {
  it("renders the menu at the end of the document, not inside the sticky cell", () => {
    const template = menu.slice(menu.indexOf("<template>"));
    expect(template).toMatch(/<Teleport to="body">[\s\S]*class="rec-menu"[\s\S]*<\/Teleport>/);
    // The trigger stays where it was, in the cell, so the row keeps its target.
    const wrap = template.indexOf('class="rec-menu-wrap"');
    expect(wrap).toBeGreaterThan(-1);
    expect(wrap).toBeLessThan(template.indexOf("<Teleport"));
  });

  it("places the menu from the trigger's box in document coordinates", () => {
    expect(menu).toContain("getBoundingClientRect()");
    expect(menu).toMatch(/rect\.bottom \+ window\.scrollY/);
    expect(menu).toContain(':style="place ?? undefined"');
    // Absolute against the document, not fixed: a menu on the last row must
    // still count towards the height the frame reports to the host.
    expect(styles).toMatch(/\.rec-menu\s*\{[^}]*position:\s*absolute/s);
    expect(styles).not.toMatch(/\.rec-menu-wrap\s*\{[^}]*position:\s*relative/s);
    expect(styles).not.toMatch(/\.rec-menu\s*\{[^}]*top:\s*calc\(100%/s);
  });

  it("keeps the consumer's attribute on both halves so outside-click and focus still find it", () => {
    expect(menu).toContain("defineOptions({ inheritAttrs: false })");
    expect((menu.match(/v-bind="\$attrs"/g) ?? []).length).toBe(2);
    for (const [name, source] of screens) {
      expect(source, name).toContain('`.rec-menu[data-row-menu="${cssEscape(id)}"] button:not(:disabled)`');
      expect(source, name).toContain('closest("[data-row-menu]")');
    }
  });

  it("follows the trigger while the menu is open and lets go when it closes", () => {
    expect(menu).toContain('window.addEventListener("scroll", position, true)');
    expect(menu).toContain('window.removeEventListener("scroll", position, true)');
    expect(menu).toContain("onBeforeUnmount(unlisten)");
  });
});
