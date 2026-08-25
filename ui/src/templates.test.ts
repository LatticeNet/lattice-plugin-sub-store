import { readdirSync, readFileSync } from "node:fs";
import { parse } from "vue/compiler-sfc";
import { describe, expect, it } from "vitest";

/**
 * Every single-file component parses.
 *
 * A malformed template — one stray `</div>` left behind while replacing a
 * block — passed both the typecheck and the whole suite: vue-tsc does not
 * report SFC template parse errors, and the component tests stub the screens
 * they do not mount. Only the dev server complained, and only when a person
 * happened to load the page. This closes that.
 */
function componentFiles(): { name: string; source: string }[] {
  const roots = ["./components", "./components/lt", "./screens", "."];
  const seen = new Set<string>();
  const out: { name: string; source: string }[] = [];
  for (const dir of roots) {
    const url = new URL(dir, import.meta.url);
    for (const entry of readdirSync(url, { withFileTypes: true })) {
      if (!entry.isFile() || !entry.name.endsWith(".vue")) continue;
      const key = dir + "/" + entry.name;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({ name: key, source: readFileSync(new URL(key, import.meta.url), "utf8") });
    }
  }
  return out;
}

describe("every component parses", () => {
  const files = componentFiles();

  it("finds the screens and the components", () => {
    const names = files.map((f) => f.name);
    expect(names.some((n) => n.includes("SubscriptionsScreen.vue"))).toBe(true);
    expect(names.some((n) => n.includes("FilesScreen.vue"))).toBe(true);
    expect(files.length).toBeGreaterThan(10);
  });

  it("reports no parse errors", () => {
    for (const file of files) {
      const { descriptor, errors } = parse(file.source, { filename: file.name });
      expect(errors.map((e) => e.message), file.name).toEqual([]);
      expect(descriptor.template || descriptor.script || descriptor.scriptSetup, file.name).toBeTruthy();
    }
  });
});
