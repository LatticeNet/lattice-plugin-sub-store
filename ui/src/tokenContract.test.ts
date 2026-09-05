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
  "--success-text", "--warning-text", "--info-text",
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
  "--info", "--success-text", "--warning-text", "--info-text",
  "--shadow-overlay",
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

/* ── the numbers behind the colours ───────────────────────────────────────
   Three of the checks below are arithmetic rather than opinion: OKLCH to
   linear sRGB to relative luminance to the WCAG ratio. They exist because a
   status colour that fails as text fails silently. Nothing renders wrong, the
   label is simply hard to read, and the palette that ships is the one nobody
   measured. */

function oklchToSrgb(l: number, c: number, hDeg: number): [number, number, number] {
  const h = (hDeg * Math.PI) / 180;
  const a = c * Math.cos(h);
  const b = c * Math.sin(h);
  const l3 = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m3 = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s3 = (l - 0.0894841775 * a - 1.291485548 * b) ** 3;
  return [
    4.0767416621 * l3 - 3.3077115913 * m3 + 0.2309699292 * s3,
    -1.2684380046 * l3 + 2.6097574011 * m3 - 0.3413193965 * s3,
    -0.0041960863 * l3 - 0.7034186147 * m3 + 1.707614701 * s3,
  ];
}

function luminance(color: string): number {
  const m = /oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)/.exec(color);
  expect(m, `not a plain oklch() literal: ${color}`).not.toBeNull();
  const [r, g, b] = oklchToSrgb(Number(m![1]), Number(m![2]), Number(m![3]));
  const clamp = (x: number) => Math.min(1, Math.max(0, x));
  return 0.2126 * clamp(r) + 0.7152 * clamp(g) + 0.0722 * clamp(b);
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** `--name: value;` out of one of the two fallback blocks. */
function value(scope: string, name: string): string {
  const m = new RegExp(`\\${name}\\s*:\\s*([^;]+);`).exec(scope);
  expect(m, `${name} is not declared in that block`).not.toBeNull();
  return m![1]!.trim();
}

describe("the status colours read as text", () => {
  it("the light ink steps clear AA on both light grounds", () => {
    // The fills do not, which is why the ink steps exist: --warning as text on
    // a white card measures 2.5:1 and --success 3.4:1, and this lens writes
    // "expired", "never fetched" and "published" as coloured 12px labels.
    for (const ink of ["--success-text", "--warning-text", "--info-text"]) {
      for (const ground of ["--card", "--background"]) {
        const ratio = contrast(value(light, ink), value(light, ground));
        expect(ratio, `${ink} on ${ground} is ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(4.5);
      }
    }
  });

  it("writes no status as the fill it ships with", () => {
    // The fills stay fills: a dot, a 2px rule, a soft background. The moment
    // one is a `color:` it is ink, and ink is the -text step.
    const decl = [styles, ...files.filter((f) => f.name.endsWith(".vue")).map((f) => f.text)]
      .join("\n")
      .replace(/\/\*[\s\S]*?\*\//g, "");
    for (const name of ["--success", "--warning", "--info"]) {
      expect(decl, `${name} is used as a text colour somewhere`).not.toMatch(
        new RegExp(`[^-]color:\\s*var\\(\\${name}[,)]`),
      );
    }
  });

  it("derives an ink step for the accent and the danger colour too", () => {
    // Neither is a published name: --primary written as text on its own soft
    // fill measures 2.9:1 on the palette the console paints, and worse on
    // three of the eleven the operator can pick.
    for (const ink of ["--lt-accent-ink", "--lt-danger-ink"]) {
      expect(value(light, ink)).toMatch(/color-mix\(in oklab, var\(--(primary|destructive)\)/);
      expect(value(dark, ink)).toMatch(/color-mix\(in oklab, var\(--(primary|destructive)\)/);
    }
  });
});

describe("the scrims darken", () => {
  it("mixes no foreground into a veil", () => {
    // --foreground is near-white on a dark console, so a scrim mixed from it
    // RAISED the page's luminance: the list behind an open panel brightened by
    // about a third and the panel ended up darker than the page it floats over.
    for (const scope of [light, dark]) {
      for (const name of ["--lt-scrim", "--lt-scrim-strong"]) {
        expect(value(scope, name)).toMatch(/^oklch\(0 0 0 \/ \d+%\)$/);
      }
    }
    const overlays = files.filter((file) => /scrim|backdrop/.test(file.text));
    for (const file of overlays) {
      for (const m of file.text.matchAll(/(?:^|\n)\s*background:\s*([^;]+);/g)) {
        if (!/scrim|backdrop/.test(m[1]!) && !/var\(--lt-scrim/.test(m[1]!)) continue;
        expect(m[1]!, `${file.name} paints a veil from the foreground`).not.toMatch(/var\(--foreground\)/);
      }
    }
  });

  it("is one step heavier under a modal than under the panel", () => {
    const strength = (v: string) => Number(/\/\s*(\d+)%/.exec(v)![1]);
    for (const scope of [light, dark]) {
      expect(strength(value(scope, "--lt-scrim-strong"))).toBeGreaterThan(strength(value(scope, "--lt-scrim")));
    }
  });
});

describe("the dev harness sends what the console sends", () => {
  // The harness is where every Gate 2 pass on this lens happens, so a payload
  // that differs from production means the colours signed off are not the
  // colours shipped. It has been wrong twice: first with no theme message at
  // all, then with ten approximate hexes, and then with the light accent
  // copied out of app.css's :root -- which is only the pre-mount fallback. The
  // theme store repaints --primary from the default "teal" palette on the
  // first frame, so that payload was indigo where production is teal, and it
  // hid two contrast failures that only appear on teal.
  const harness = readFileSync(new URL("../dev/hostTheme.ts", SRC), "utf8");
  const scheme = (name: "SHARED" | "LIGHT" | "DARK"): string => {
    const at = harness.indexOf(`const ${name}: Record`);
    expect(at, `dev/hostTheme.ts has no ${name} payload`).toBeGreaterThan(-1);
    return harness.slice(at, harness.indexOf("\n};", at));
  };
  const sent = (block: string, name: string): string => {
    const m = new RegExp(`"\\${name}":\\s*"([^"]+)"`).exec(block);
    expect(m, `${name} is not in that payload`).not.toBeNull();
    return m![1]!;
  };
  const LIGHT = scheme("LIGHT");
  const DARK = scheme("DARK");
  const SHARED = scheme("SHARED");

  it("carries every published name the plugin lays out on", () => {
    const missing = PUBLISHED.filter((name) => !new RegExp(`"\\${name}":`).test(LIGHT + DARK + SHARED));
    expect(missing).toEqual([]);
  });

  it("sends the accent the console actually paints, in both schemes", () => {
    // Hue, not the literal: the exact lightness is the palette's business, but
    // teal is 180-ish and the indigo that was here is 278.
    for (const [name, block] of [["light", LIGHT], ["dark", DARK]] as const) {
      for (const token of ["--primary", "--ring"]) {
        const hue = Number(/oklch\([\d.]+ [\d.]+ ([\d.]+)\)/.exec(sent(block, token))![1]);
        expect(hue, `the ${name} ${token} is at hue ${hue}, not on the teal the console paints`)
          .toBeGreaterThan(170);
        expect(hue).toBeLessThan(200);
      }
    }
  });

  it("sends an accent whose own label clears AA", () => {
    // The primary button is the control that saves, applies and confirms.
    for (const block of [LIGHT, DARK]) {
      const ratio = contrast(sent(block, "--primary-foreground"), sent(block, "--primary"));
      expect(ratio, `the primary button label reads ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("sends status ink that clears AA on the ground it is sent with", () => {
    for (const block of [LIGHT, DARK]) {
      for (const ink of ["--success-text", "--warning-text", "--info-text"]) {
        const ratio = contrast(sent(block, ink), sent(block, "--card"));
        expect(ratio, `${ink} reads ${ratio.toFixed(2)}:1 on --card`).toBeGreaterThanOrEqual(4.5);
      }
    }
  });
});
