import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

/**
 * The plugin lays out on the console's own token names, and this is what keeps
 * that true.
 *
 * Before token contract v2 the frame received eleven colours, so this lens
 * derived everything else itself: its own radius scale, its own spacing scale
 * with a 20px step the console does not have, its own type sizes, and a green
 * and an amber written as hex, which are right in one theme only. Four plugins
 * did the same four different ways and none of them matched the console, which
 * is why this one reads harder-edged than vpn-core and NetGuard beside it.
 *
 * Two rules follow, and both are checked here rather than by eye:
 *   1. Nothing is referenced that is not declared. A rename that misses a
 *      site leaves `var(--typo)` resolving to nothing, which shows up as one
 *      wrong margin on one screen and nowhere else.
 *   2. Every published name the plugin uses has a fallback here, and every
 *      themed one has a dark fallback too, so the harness and a console older
 *      than the contract render the same design rather than a light-mode box
 *      on a near-black page.
 */
const SRC = new URL(".", import.meta.url);
const tokens = readFileSync(new URL("./tokens.css", SRC), "utf8");
const styles = readFileSync(new URL("./styles.css", SRC), "utf8");

function readAll(dir: URL, out: { name: string; text: string }[] = []): { name: string; text: string }[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const child = new URL(entry.name + (entry.isDirectory() ? "/" : ""), dir);
    if (entry.isDirectory()) {
      readAll(child, out);
      continue;
    }
    if (!/\.(vue|ts|css)$/.test(entry.name) || entry.name.endsWith(".test.ts")) continue;
    out.push({ name: entry.name, text: readFileSync(child, "utf8") });
  }
  return out;
}

const files = readAll(SRC);
const everything = files.map((file) => file.text).join("\n");

/** Names declared anywhere the plugin controls. */
const declared = new Set<string>();
for (const match of everything.matchAll(/(--[a-z0-9-]+)\s*:/g)) declared.add(match[1]!);

/** Names read with var(), minus the ones a var() fallback already covers. */
const referenced = new Map<string, string>();
for (const file of files) {
  for (const match of file.text.matchAll(/var\((--[a-z0-9-]+)\s*([,)])/g)) {
    if (match[2] === ",") continue;
    if (!referenced.has(match[1]!)) referenced.set(match[1]!, file.name);
  }
}

/** The published names this plugin lays out on. */
const PUBLISHED = [
  "--background", "--foreground", "--card", "--card-foreground", "--muted",
  "--muted-foreground", "--accent", "--border", "--primary",
  "--primary-foreground", "--destructive", "--ring",
  "--success", "--warning", "--info",
  "--radius-sm", "--radius-md", "--radius-lg", "--radius-xl",
  "--row-h", "--row-h-compact",
  "--space-1", "--space-2", "--space-3", "--space-4", "--space-5", "--space-6",
  "--font-mono", "--text-body",
  "--shadow-overlay",
  "--duration-fast", "--duration-base", "--ease-out",
];

/** Published names whose value differs between the two themes. */
const THEMED = [
  "--background", "--foreground", "--card", "--card-foreground", "--muted",
  "--muted-foreground", "--accent", "--border", "--primary",
  "--primary-foreground", "--destructive", "--ring", "--success", "--warning",
  "--info", "--shadow-overlay",
];

function block(selector: string): string {
  const at = tokens.indexOf(`${selector} {`);
  expect(at, `tokens.css has no ${selector} rule`).toBeGreaterThan(-1);
  const open = tokens.indexOf("{", at);
  let depth = 0;
  for (let i = open; i < tokens.length; i += 1) {
    if (tokens[i] === "{") depth += 1;
    else if (tokens[i] === "}") {
      depth -= 1;
      if (depth === 0) return tokens.slice(open + 1, i);
    }
  }
  return "";
}

const light = block(":root");
const dark = block(':root[data-theme="dark"]');

describe("the token contract the frame lays out on", () => {
  it("references nothing it has not declared", () => {
    const orphans = [...referenced]
      .filter(([name]) => !declared.has(name))
      .map(([name, where]) => `${name} (${where})`);
    expect(orphans).toEqual([]);
  });

  it("declares a fallback for every published name it uses", () => {
    // In force only where no host sends the contract: the dev harness with the
    // theme message off, and a console older than it. The values are the
    // console's own, so the plugin looks the same either way.
    const missing = PUBLISHED.filter((name) => !new RegExp(`\\${name}\\s*:`).test(light));
    expect(missing).toEqual([]);
  });

  it("repaints every themed fallback for a dark console", () => {
    const unpainted = THEMED.filter((name) => !new RegExp(`\\${name}\\s*:`).test(dark));
    expect(unpainted).toEqual([]);
  });

  it("keeps no colour of its own", () => {
    // A literal here renders a light-mode box on a dark console. The soft fills
    // are mixed into the current surface instead, so they follow the theme.
    const declarations = tokens.replace(/\/\*[\s\S]*?\*\//g, "");
    expect(declarations).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(declarations).not.toMatch(/\brgb[a]?\(/);
  });

  it("lays out on the published scales, not on a scale of its own", () => {
    // The `--lt-` prefix is for what the console has no name for: derivations,
    // measures, the column grid, the motion aliases reduced motion has to be
    // able to zero. A second radius or spacing scale under that prefix is how
    // the divergence started.
    const local = [...declared].filter((name) => name.startsWith("--lt-"));
    const forbidden = local.filter((name) => /^--lt-(radius|space|row-h|surface|fg|bg|mono)/.test(name));
    expect(forbidden).toEqual([]);
  });

  it("keeps reduced motion able to zero the durations", () => {
    // The host writes its tokens as inline properties on <html>, which beat
    // any stylesheet. So motion is read through plugin-owned aliases: a media
    // query on --duration-fast would lose to the host and nothing would stop
    // moving.
    expect(tokens).toMatch(/--lt-dur:\s*var\(--duration-fast/);
    expect(tokens).toMatch(/prefers-reduced-motion[\s\S]{0,120}--lt-dur:\s*0ms/);
    expect(styles).not.toMatch(/transition:[^;]*var\(--duration-/);
  });

  it("gives every overlay one of the three stacking levels", () => {
    // Five overlay kinds used to sit on seven z-index values spread over five
    // files (1, 2, 3, 20, 30, 50, 60), and which covered which was a question
    // you answered by reading all of them.
    for (const name of ["--lt-z-inline", "--lt-z-panel", "--lt-z-modal"]) {
      expect(declared.has(name), `${name} is not declared`).toBe(true);
    }
    const overlays = files.filter((file) => /scrim|backdrop|rec-menu/.test(file.text));
    for (const file of overlays) {
      for (const match of file.text.matchAll(/z-index:\s*([^;]+);/g)) {
        const value = match[1]!.trim();
        // The internals of a scroller (a sticky head, a pinned column) keep
        // their local 1 to 3; they never contest an overlay.
        if (/^[1-3]$/.test(value)) continue;
        expect(value, `${file.name} sets a raw z-index`).toMatch(/^var\(--lt-z-(inline|panel|modal)\)$/);
      }
    }
  });
});
