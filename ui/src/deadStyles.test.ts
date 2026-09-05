import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

/**
 * Every rule in the stylesheet still has something that wears it.
 *
 * A redesign leaves orphans: `.preview-blocked` outlived the paragraph it
 * styled by two releases. Finding them is a grep, which is exactly the problem
 * — a grep also reports every class the code never spells out in full, and
 * deleting those breaks things silently. `is-ok` and `is-warn` are built as
 * `is-${tone}` and colour the row status; `tok-string` and friends are built
 * as `tok-${kind}` by the document viewer; `cm-*` belongs to CodeMirror and is
 * created at runtime by the library we are theming. Delete on grep alone and
 * the row status turns grey with every test still green.
 *
 * So the families that are legitimately invisible to a text search are listed
 * here, each with the reason. Anything else unreferenced is an orphan, and a
 * new one fails this test. Adding to the list is a deliberate act with a
 * sentence attached, which is the point.
 */
const BUILT_AT_RUNTIME: { prefix: string; why: string }[] = [
  { prefix: "cm-", why: "CodeMirror's own classes; we theme what the library creates." },
  { prefix: "tok-", why: "DocumentView builds `tok tok-${token.kind}` per token." },
  { prefix: "is-", why: "Row status and chips build `is-${tone}` from the record." },
  { prefix: "pc-", why: "The plugin chassis renders these classes; this sheet only refines them for this lens." },
];

const SRC = new URL(".", import.meta.url);

function readAll(dir: URL, out: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const child = new URL(entry.name + (entry.isDirectory() ? "/" : ""), dir);
    if (entry.isDirectory()) {
      readAll(child, out);
      continue;
    }
    if (!/\.(vue|ts|html)$/.test(entry.name)) continue;
    if (entry.name.endsWith(".test.ts")) continue;
    out.push(readFileSync(child, "utf8"));
  }
  return out;
}

describe("the stylesheet has no orphans", () => {
  const css = readFileSync(new URL("./styles.css", SRC), "utf8");
  // Comments describe rules that were removed; they are not users of them.
  const declarations = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const source = [
    ...readAll(SRC),
    ...readAll(new URL("../dev/", SRC)),
    readFileSync(new URL("../index.html", SRC), "utf8"),
  ].join("\n");

  const declared = [...new Set([...declarations.matchAll(/\.([a-zA-Z][\w-]*)/g)].map((m) => m[1]!))];

  it("finds the classes it is supposed to be checking", () => {
    expect(declared.length).toBeGreaterThan(150);
    expect(declared).toContain("rec-menu");
    expect(declared).toContain("palette-row");
  });

  it("names every rule that nothing wears", () => {
    const orphans = declared
      .filter((name) => !BUILT_AT_RUNTIME.some((family) => name.startsWith(family.prefix)))
      .filter((name) => !source.includes(name));
    expect(orphans, "unreferenced style rules: " + orphans.join(", ")).toEqual([]);
  });

  it("keeps a reason on every family it cannot see", () => {
    for (const family of BUILT_AT_RUNTIME) {
      expect(family.why.length, family.prefix + " is exempt with no reason").toBeGreaterThan(20);
      // An exemption for a family nothing declares is an exemption nobody needs.
      expect(
        declared.some((name) => name.startsWith(family.prefix)),
        family.prefix + " is exempt but the stylesheet declares nothing with it",
      ).toBe(true);
    }
  });
});

/**
 * The states a screen can be in, and the harness switch that makes them
 * reachable.
 *
 * Before this they could only be produced by breaking something on purpose and
 * putting it back, so in practice nobody looked: the load error, the empty
 * store and the read-only session were written once and never seen again.
 * Walking them found the count badge asserting "0 / 256" during a load, a
 * claim, where the honest answer was that it did not know yet.
 */
describe("every state is reachable in the harness", () => {
  const host = readFileSync(new URL("../dev/fakeHost.ts", SRC), "utf8");
  const shell = readFileSync(new URL("./Shell.vue", SRC), "utf8");
  const screens = [
    readFileSync(new URL("./screens/SubscriptionsScreen.vue", SRC), "utf8"),
    readFileSync(new URL("./screens/FilesScreen.vue", SRC), "utf8"),
  ];

  it("names each state on the URL", () => {
    for (const state of ["empty", "error", "slow", "readonly"]) {
      expect(host, state + " is not reachable").toContain(`"${state}"`);
    }
    expect(host).toContain("harnessState()");
  });

  it("holds the loading state instead of racing the observer", () => {
    // Every timed version lost: by the time anyone looked the list had landed.
    expect(host).toMatch(/if \(harnessState\(\) === "slow"\) return new Promise<T>\(\(\) => \{\}\);/);
  });

  it("withholds the write methods for a read-only session", () => {
    // The shape production takes, rather than a flag the screens read.
    expect(host).toMatch(/WITHHELD[\s\S]{0,120}save: true[\s\S]{0,60}delete: true/);
    expect(host).toMatch(/!WITHHELD\[target\.method\]/);
  });

  it("does not claim a count it does not have yet", () => {
    // The stat strip is a skeleton until the catalogue has landed, and every
    // tile reads from lists that are empty until then; the lens tabs carry no
    // count at all until the list has been read.
    expect(shell).toMatch(/const ready = computed\(\(\) => catalogue\.state\.value === "ready"\)/);
    expect(shell).toContain(`<PcSkeleton v-if="!ready && catalogue.state.value !== 'error'" variant="strip"`);
    expect(shell).toContain('<PcStatStrip v-else-if="ready"');
    expect(shell).toMatch(/subscriptions: ready\.value \? records\.value\.length : null/);
    // The lenses show a skeleton, not an empty table, while the list is coming.
    for (const screen of screens) {
      expect(screen).toMatch(/v-if="!host\.init\.value \|\| subs\.state\.value === 'loading'"[\s\S]{0,80}<PcSkeleton/);
    }
  });
});
