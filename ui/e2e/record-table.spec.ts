import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Locator, type Page } from "@playwright/test";

import { formatDays, formatPercent, setLocale } from "../src/i18n";
import { LOCALES, TABLES, m, startsWith, type Locale } from "./messages";

/**
 * The Records table (design 28, S1): every state it draws, named by a test id
 * or by the name the page gives a control in its own message table, never by
 * English typed here, at the two widths the design holds it to and in each
 * of the three locales; axe over each with no serious or critical
 * violation; labels that stay whole in the longer Russian and Chinese words;
 * and the order changed from the keyboard alone, from a drag that follows
 * the pointer, and put back when the store refuses it.
 *
 * The harness clock starts at one moment (dev/clock.ts), so "expires in 6
 * days" and every date in a title read the same on every run.
 */

const CLOCK = "2026-10-09T08:00:00Z";
const VIEWPORTS = [
  { width: 1440, height: 900 },
  { width: 375, height: 812 },
];

/**
 * The page in `locale`: the English table ships in the page, the other two
 * arrive after the handshake, and <html lang> says when the page reads one.
 */
async function open(page: Page, query: string, ready: string, locale: Locale = "en"): Promise<void> {
  await page.goto(`/dev.html?view=records&clock=${CLOCK}&locale=${locale}${query}`);
  await expect(page.locator("html")).toHaveAttribute("lang", locale);
  await page.getByTestId(ready).first().waitFor();
}

/** The words the page's own formatting writes in `locale`, for what a cell should hold. */
async function written<T>(locale: Locale, write: () => T): Promise<T> {
  await setLocale(locale);
  return write();
}

const docWidth = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth);

/**
 * Serious and critical violations on the page as it stands, the harness's own
 * bar left out; `within` narrows it to one region, such as a modal sheet that
 * covers the rest of the page with its scrim.
 */
async function seriousViolations(page: Page, within?: string): Promise<string[]> {
  const builder = new AxeBuilder({ page }).exclude(".dev-bar");
  const result = await (within ? builder.include(within) : builder).analyze();
  return result.violations
    .filter((violation) => violation.impact === "serious" || violation.impact === "critical")
    .map((violation) => `${violation.id}: ${violation.nodes.map((node) => node.target.join(" ")).slice(0, 3).join(", ")}`);
}

/** The content is wider than the box: cut by an ellipsis, a clip, or a scroller. */
const widerThanBox = (locator: Locator) => locator.evaluate((el) => el.scrollWidth > el.clientWidth + 1);

/**
 * Every shown element `selector` matches whose words do not fit: cut by its
 * own box (an ellipsis, a clip or a scroller), running past its border where
 * it lets words overflow (into its padding is fine: a header's last letters
 * may sit there in a wider system face), or ending past the frame's right
 * edge. A select is held to its widest option, which the closed control
 * shows once it is chosen.
 */
function cutLabels(page: Page, selector: string): Promise<string[]> {
  return page.locator(selector).evaluateAll((els) => {
    const ctx = document.createElement("canvas").getContext("2d")!;
    const frame = document.documentElement.clientWidth;
    const out: string[] = [];
    for (const el of els as HTMLElement[]) {
      const box = el.getBoundingClientRect();
      if (box.width === 0 || box.height === 0) continue;
      const words = (el.textContent ?? "").trim().replace(/\s+/g, " ").slice(0, 48);
      if (box.right > frame + 1) out.push(`${words}: ends ${Math.round(box.right - frame)}px past the frame`);
      if (el instanceof HTMLSelectElement) {
        const style = getComputedStyle(el);
        ctx.font = `${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;
        const room = el.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
        for (const option of el.options) {
          const width = ctx.measureText(option.text).width;
          if (width > room + 1) out.push(`${option.text}: ${Math.round(width - room)}px wider than its select`);
        }
      } else if (getComputedStyle(el).overflowX !== "visible") {
        if (el.scrollWidth > el.clientWidth + 1) out.push(`${words}: ${el.scrollWidth - el.clientWidth}px over its box`);
      } else {
        const range = document.createRange();
        range.selectNodeContents(el);
        const drawn = range.getBoundingClientRect();
        if (drawn.right > box.right + 1) out.push(`${words}: runs ${Math.round(drawn.right - box.right)}px past its edge`);
      }
    }
    return out;
  });
}

/** Each selector's cut labels, one list, so a failure names every one at once. */
async function cutAmong(page: Page, selectors: readonly string[]): Promise<string[]> {
  const found = await Promise.all(selectors.map(async (selector) => (await cutLabels(page, selector)).map((cut) => `${selector} ${cut}`)));
  return found.flat();
}

/**
 * The English table's strings that `locale` says otherwise, string parts of a
 * rich message included. Strings both tables hold (names, units, protocol
 * words) are left out, so in English the list is empty.
 */
function englishOnly(locale: Locale): string[] {
  const walk = (value: unknown, visit: (text: string) => void): void => {
    if (typeof value === "string") visit(value);
    else if (value && typeof value === "object") for (const inner of Object.values(value)) walk(inner, visit);
  };
  const theirs = new Set<string>();
  walk(TABLES[locale], (text) => theirs.add(text));
  const out = new Set<string>();
  walk(TABLES.en, (text) => {
    if (/[A-Za-z]{2}/.test(text) && !theirs.has(text)) out.add(text);
  });
  return [...out];
}

/**
 * Each shown text, accessible name, title and placeholder that is one of the
 * English table's strings in a page that reads `locale`: a label left in
 * English, as a message read once outside a render keeps the English it read
 * before the locale arrived. The harness's own bar is left out.
 */
function leftInEnglish(page: Page, locale: Locale): Promise<string[]> {
  return page.evaluate((english) => {
    const words = new Set(english);
    const where = (el: Element) => (el.id ? `#${el.id}` : [el.tagName.toLowerCase(), ...el.classList].join("."));
    const out: string[] = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const el = node.parentElement;
      const text = (node.textContent ?? "").trim();
      if (el && words.has(text) && !el.closest(".dev-bar") && el.checkVisibility()) out.push(`"${text}" in ${where(el)}`);
    }
    for (const el of document.querySelectorAll("[aria-label], [title], [placeholder]")) {
      if (el.closest(".dev-bar")) continue;
      for (const name of ["aria-label", "title", "placeholder"]) {
        const value = el.getAttribute(name)?.trim();
        if (value && words.has(value)) out.push(`${name}="${value}" on ${where(el)}`);
      }
    }
    return out;
  }, englishOnly(locale));
}

/** The words around the table that change with the locale: the header, the layer tabs and the toolbar. */
const CHROME = [".ss-header .pc-button", ".pc-lens-tab", ".rec-kind", ".toolbar-sort > span", ".toolbar-sort > select", "[data-testid=records-density]"];
/** The table's own labels at full rows: the headers, and the cells that hold words rather than names. */
const TABLE_WORDS = [
  ".records-table th",
  ".rec-kind-label",
  "[data-testid=record-steps] small",
  "td[data-expiry] .pc-td-body",
  "[data-testid=record-published] .pc-td-body",
  "td.rec-fetch .pc-td-body",
  "[data-testid=record-flagged] .pc-state-dot",
  "[data-testid=record-missing]",
];
/** A stacked row's state line on a phone. */
const STACKED_WORDS = [".rec-kind-label", "[data-testid=record-published] .pc-td-body", "td[data-stack=state] .pc-state-dot", "td[data-expiry] .layer-expiry"];

/**
 * The text's leading `prefix` is drawn inside the element's box, clear of the
 * ellipsis (about one mono character) that a cut line ends in.
 */
const showsPrefix = (locator: Locator, prefix: string) =>
  locator.evaluate((el, prefix) => {
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const at = (node.textContent ?? "").indexOf(prefix);
      if (at < 0) continue;
      const range = document.createRange();
      range.setStart(node, at);
      range.setEnd(node, at + prefix.length);
      return range.getBoundingClientRect().right <= el.getBoundingClientRect().right - 8;
    }
    return false;
  }, prefix);

const LONG = "一个非常非常长的机场订阅名称用来检查截断与换行-and-a-very-long-latin-provider-subscription-name-as-well";

const rowNamed = (page: Page, name: string) => page.getByTestId("record-row").filter({ has: page.getByTestId("record-name").getByText(name, { exact: true }) });
const positionOf = (page: Page, name: string) => rowNamed(page, name).getByTestId("record-position");
const names = (page: Page) => page.getByTestId("record-name").allInnerTexts();

for (const locale of LOCALES) {
  const t = TABLES[locale];
  for (const viewport of VIEWPORTS) {
    test.describe(`records states in ${locale} at ${viewport.width}`, () => {
      test.use({ viewport });

      test("loading holds a skeleton, named", async ({ page }) => {
        await open(page, "&state=slow", "records-loading", locale);
        await expect(page.getByTestId("records-loading")).toBeVisible();
        expect(await seriousViolations(page)).toEqual([]);
      });

      test("an empty store offers creation and the import, not an empty table", async ({ page }) => {
        await open(page, "&state=empty", "records-empty", locale);
        await expect(page.getByTestId("records-table")).toHaveCount(0);
        expect(await cutAmong(page, [".ss-header .pc-button", "[data-testid=records-empty] .pc-button"])).toEqual([]);
        expect(await docWidth(page)).toBe(viewport.width);
        expect(await seriousViolations(page)).toEqual([]);
      });

      test("a filter that matches nothing says so, and offers to clear it", async ({ page }) => {
        await open(page, "&q=no-such-record-anywhere", "records-no-match", locale);
        await expect(page.getByTestId("records-no-match").getByRole("button", { name: t.records.clearFilters })).toBeEnabled();
        // The kind filter stays in reach above it.
        await expect(page.getByTestId("kind-all")).toBeChecked();
        expect(await cutAmong(page, [...CHROME, "[data-testid=records-no-match] .pc-button"])).toEqual([]);
        expect(await seriousViolations(page)).toEqual([]);
      });

      test("an unreadable store says so, with a way to try again", async ({ page }) => {
        await open(page, "&state=error", "records-error", locale);
        await expect(page.getByTestId("records-error").getByRole("button", { name: t.common.tryAgain })).toBeVisible();
        expect(await docWidth(page)).toBe(viewport.width);
        expect(await seriousViolations(page)).toEqual([]);
      });

      test("a reload that fails behind the rows keeps them and says they are the last good read", async ({ page }) => {
        await open(page, "&state=stale", "record-row", locale);
        await page.locator(".ss-header").getByRole("button", { name: t.shell.refresh }).click();
        await expect(page.getByTestId("records-stale")).toBeVisible();
        await expect(page.getByTestId("record-row").first()).toBeVisible();
        expect(await seriousViolations(page)).toEqual([]);
      });

      test("a read-only session reads every row, cannot reorder, and does not claim what it cannot know", async ({ page }) => {
        await open(page, "&state=readonly&manifest=s1", "record-row", locale);
        await expect(page.getByTestId("records-readonly")).toBeVisible();
        await expect(page.getByTestId("record-grip")).toHaveCount(0);
        // The share list needs scopes this session lacks: unknown, not "not published".
        await expect(page.getByTestId("record-published").first()).toContainText(t.records.publishedUnknown);
        await expect(page.getByTestId("record-published").filter({ hasText: t.publish.none })).toHaveCount(0);
        expect(await seriousViolations(page)).toEqual([]);
      });

      test("an expired provider, a flagged chain and long names each read as what they are", async ({ page }) => {
        await open(page, "&fixture=states&manifest=s1", "record-row", locale);
        const expired = rowNamed(page, "openjobs-host-trojan").getByTestId("record-expired");
        await expect(expired).toBeVisible();
        await expect(expired).toHaveText(await written(locale, () => t.provider.expiredAgo(2, formatDays(-2))));
        for (const name of ["lookahead-provider", "backreference-rename"]) {
          await expect(rowNamed(page, name).getByTestId("record-flagged"), name).toBeVisible();
        }
        await expect(rowNamed(page, "建材市场").getByTestId("record-flagged")).toHaveCount(0);
        // A long name stays inside its own cell, with the whole name in the
        // title, and the table fits its frame: neither the page nor the table's
        // own wrapper scrolls sideways (the wrapper scrolls rather than the page
        // when the table outgrows it, so the page width alone cannot tell).
        const long = rowNamed(page, LONG).getByTestId("record-name");
        await long.scrollIntoViewIfNeeded();
        const name = (await long.boundingBox())!;
        const cell = (await long.locator("xpath=ancestor::td[1]").boundingBox())!;
        expect(name.x + name.width).toBeLessThanOrEqual(cell.x + cell.width + 1);
        await expect(long).toHaveAttribute("title", /一个非常非常长的机场订阅名称/);
        if (viewport.width > 480) expect(await widerThanBox(long.locator("strong")), "the long name ends in an ellipsis").toBe(true);
        expect(await widerThanBox(page.getByTestId("records-table")), "the table scrolls inside its wrapper").toBe(false);
        expect(await docWidth(page)).toBe(viewport.width);
        expect(await seriousViolations(page)).toEqual([]);
      });

      test("the header and the layer tabs speak the page's locale, and nothing shown is left in English", async ({ page }) => {
        await open(page, "&fixture=states&manifest=s1", "record-row", locale);
        // The page draws English before the handshake; the frame must redraw
        // once the table arrives, not only the rows that load after it.
        for (const layer of ["overview", "records", "shares", "settings"] as const) {
          await expect(page.locator(`#pc-tab-${layer}`)).toHaveText(startsWith(t.layers[layer]));
        }
        const header = page.locator(".ss-header");
        await expect(header.getByRole("button", { name: t.shell.refresh, exact: true })).toBeVisible();
        await expect(header.getByRole("button", { name: t.create.newSource, exact: true })).toBeVisible();
        expect(await leftInEnglish(page, locale)).toEqual([]);
      });

      test("paints no English word before the locale's table has arrived", async ({ page }) => {
        test.skip(locale === "en", "English is the table the page ships with");
        // Before every frame from the first, what that frame would show: any
        // visible text that is an English string this locale translates. The
        // harness's handshake lands 400 ms late, and the table loads after it.
        await page.addInitScript((english) => {
          const words = new Set(english);
          const seen = new Set<string>();
          (window as unknown as { __englishPainted: Set<string> }).__englishPainted = seen;
          const sample = () => {
            if (document.body) {
              const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
              for (let node = walker.nextNode(); node; node = walker.nextNode()) {
                const el = node.parentElement;
                const text = (node.textContent ?? "").trim();
                if (el && words.has(text) && !el.closest(".dev-bar") && el.checkVisibility({ visibilityProperty: true })) seen.add(text);
              }
            }
            requestAnimationFrame(sample);
          };
          requestAnimationFrame(sample);
        }, englishOnly(locale));
        await open(page, "&fixture=states&manifest=s1", "record-row", locale);
        await expect(page.locator("#pc-tab-records")).toHaveText(startsWith(t.layers.records));
        const painted = await page.evaluate(() => [...(window as unknown as { __englishPainted: Set<string> }).__englishPainted]);
        expect(painted).toEqual([]);
      });

      test("the chain editor names every step whole beside its controls, and its pane keeps its buttons inside", async ({ page }) => {
        await open(page, "&fixture=states&manifest=s1", "record-row", locale);
        await rowNamed(page, "lookahead-provider").getByTestId("record-name").click();
        await page.locator(".pc-side-panel").getByRole("button", { name: t.actions.edit, exact: true }).click();
        await page.getByRole("tab", { name: startsWith(t.editor.tabs.operations) }).click();
        await page.locator(".step-bar").first().waitFor();
        // On a phone the toggle's word and five buttons left the name its number alone.
        expect(await cutAmong(page, [".step-label", ".step-toggle", ".step-actions"])).toEqual([]);
        // The pane under the form: Explain and Preview take a row each when one cannot hold both.
        const pastPane = await page.locator(".editor-side .pc-panel-header").evaluate((header) => {
          const edge = header.getBoundingClientRect().right - parseFloat(getComputedStyle(header).paddingRight);
          return [...header.querySelectorAll(".pc-panel-header-end > *")]
            .map((button) => [button.textContent?.trim(), Math.round(button.getBoundingClientRect().right - edge)] as const)
            .filter(([, past]) => past > 1)
            .map(([words, past]) => `${words}: ${past}px past the pane's edge`);
        });
        expect(pastPane).toEqual([]);
        expect(await docWidth(page)).toBe(viewport.width);
        expect(await leftInEnglish(page, locale)).toEqual([]);
      });

      test("every label around and inside the table keeps its words whole", async ({ page }) => {
        await open(page, "&fixture=states&manifest=s1", "record-row", locale);
        // The layer row holds its four tabs without scrolling one out of sight.
        expect(await widerThanBox(page.locator("[data-variant=layer]")), "the layer row scrolls").toBe(false);
        expect(await cutAmong(page, [...CHROME, ...(viewport.width > 480 ? TABLE_WORDS : STACKED_WORDS)])).toEqual([]);
        expect(await docWidth(page)).toBe(viewport.width);
      });

      test("the row menu and the side panel keep their words whole, in the page's locale", async ({ page }) => {
        await open(page, "&fixture=states&manifest=s1", "record-row", locale);
        const row = rowNamed(page, "openjobs-host-trojan");
        await row.getByRole("button", { name: t.records.actionsFor("openjobs-host-trojan") }).click();
        const menu = page.getByRole("menu");
        await menu.evaluate((el) => Promise.all(el.getAnimations().map((animation) => animation.finished)));
        expect(await cutAmong(page, [".rec-menu [role=menuitem]"])).toEqual([]);
        expect(await leftInEnglish(page, locale)).toEqual([]);
        expect(await seriousViolations(page)).toEqual([]);
        await page.keyboard.press("Escape");
        await expect(menu).toHaveCount(0);

        await row.getByTestId("record-name").click();
        const panel = page.locator(".pc-side-panel");
        await expect(panel).toBeVisible();
        await panel.evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((animation) => animation.finished)));
        expect(await cutAmong(page, [".pc-side-panel .pc-button", ".peek-state > *", ".peek-facts dt"])).toEqual([]);
        expect(await leftInEnglish(page, locale)).toEqual([]);
        expect(await docWidth(page)).toBe(viewport.width);
        // On a phone the panel is a modal sheet: the rows under its scrim are not read.
        expect(await seriousViolations(page, viewport.width > 480 ? undefined : ".pc-side-panel")).toEqual([]);
      });

      test("a legacy store prompts the migration and runs it", async ({ page }) => {
        await open(page, "&store=legacy&manifest=s1", "records-migrate", locale);
        // Rows cannot move on a legacy store; the note says why instead of a dead grip.
        await expect(page.getByTestId("record-grip")).toHaveCount(0);
        await expect(page.getByTestId("records-order-note")).toBeVisible();
        expect(await cutAmong(page, ["[data-testid=records-migrate] .pc-button"])).toEqual([]);
        expect(await seriousViolations(page)).toEqual([]);
        await page.getByTestId("records-migrate").getByRole("button", { name: t.records.migrateAction }).click();
        await expect(page.getByTestId("records-migrated")).toBeVisible();
        // The chassis notice names its close button in English unless it is told the word.
        await expect(page.getByTestId("records-migrated").getByRole("button", { name: t.common.dismiss, exact: true })).toBeVisible();
        await expect(page.getByTestId("records-migrate")).toHaveCount(0);
        await expect(page.getByTestId("record-grip").first()).toBeVisible();
      });

      test("compact rows drop the remark and the icon and keep the state", async ({ page }) => {
        await open(page, "&fixture=states", "record-row", locale);
        const row = rowNamed(page, "lookahead-provider");
        await expect(row.locator("td.pc-name small")).toBeVisible();
        await page.getByTestId("records-density").click();
        await expect(page.getByTestId("records-density")).toHaveAttribute("aria-pressed", "true");
        await expect(row.locator("td.pc-name small")).toBeHidden();
        await expect(row.locator(".rec-kind-icon")).toHaveCount(0);
        await expect(row.getByTestId("record-flagged")).toBeVisible();
        // A reference that is gone is a state too: it moves up beside the kind.
        await expect(rowNamed(page, "for-loon-novpn").getByTestId("record-missing")).toBeVisible();
        await expect(page).toHaveURL(/[?&]density=compact(&|#|$)/);
        // One line per cell: the traffic bar goes and the date stays, rather than
        // the bar spilling past the cell's edge.
        if (viewport.width > 480) {
          const expiry = rowNamed(page, "建材市场").locator("td[data-expiry] .pc-td-body");
          await expect(expiry.locator(".layer-expiry")).toHaveText(await written(locale, () => t.provider.expiresIn(6, formatDays(6))));
          await expect(expiry.locator(".usage")).toBeHidden();
          expect(await widerThanBox(expiry)).toBe(false);
        }
        expect(await seriousViolations(page)).toEqual([]);
      });
    });
  }

  test.describe(`at 1440 every column keeps what it says, in ${locale}`, () => {
    test.use({ viewport: { width: 1440, height: 900 } });

    test("the name keeps its room, and the flag, the tag and each cell's facts stay whole", async ({ page }) => {
      await open(page, "&fixture=states&manifest=s1", "record-row", locale);
      const table = page.getByTestId("records-table");
      expect((await table.locator("th.pc-name").boundingBox())!.width).toBeGreaterThanOrEqual(360);
      // A short flagged name is not cut for the marker beside it, and the marker
      // follows the name rather than the cell's far edge.
      for (const name of ["lookahead-provider", "backreference-rename"]) {
        const strong = rowNamed(page, name).getByTestId("record-name").locator("strong");
        expect(await widerThanBox(strong), name).toBe(false);
        // Where the name's text ends, not its button, which may be wider.
        const textEnd = await strong.evaluate((el) => {
          const range = document.createRange();
          range.selectNodeContents(el);
          return range.getBoundingClientRect().right;
        });
        const flag = (await rowNamed(page, name).getByTestId("record-flagged").boundingBox())!;
        expect(flag.x - textEnd, name).toBeLessThan(16);
      }
      // A long name with three tags, the first long itself, stays as tall as a
      // name with a remark: one tag and a count on one line, never a stack, and
      // the kind icon stays on the name's line.
      const longRow = rowNamed(page, LONG);
      const twoLines = (await rowNamed(page, "lookahead-provider").boundingBox())!.height;
      expect((await longRow.boundingBox())!.height).toBeLessThanOrEqual(twoLines + 1);
      await expect(longRow.locator(".pc-tag")).toHaveText(["长名称-a-tag-long-enough-to-crowd-the-name", "+2"]);
      const icon = (await longRow.locator(".rec-kind-icon").boundingBox())!;
      const longName = (await longRow.getByTestId("record-name").boundingBox())!;
      expect(Math.abs(icon.y + icon.height / 2 - (longName.y + longName.height / 2))).toBeLessThan(6);

      // Kind: the name of the kind stays whole beside a reference that is gone,
      // and a file names its source before its client.
      const gone = rowNamed(page, "for-loon-novpn");
      expect(await widerThanBox(gone.locator(".rec-kind-label"))).toBe(false);
      await expect(gone.getByTestId("record-missing")).toHaveText(t.records.fileGone);
      expect((await gone.boundingBox())!.height, "the marker on the second line keeps the row's height").toBeLessThanOrEqual(twoLines);
      expect(await showsPrefix(rowNamed(page, "for-cdcd-egern").locator(".rec-kind-sub"), t.records.fileFrom("merge-cd-openjobs"))).toBe(true);

      // Expiry and traffic: the date whole, the figure inside the cell, both
      // lines inside a row of the usual height.
      expect((await rowNamed(page, "建材市场").boundingBox())!.height).toBeLessThanOrEqual(twoLines);
      const provider = rowNamed(page, "建材市场").locator("td[data-expiry]");
      expect(await widerThanBox(provider.locator(".layer-expiry"))).toBe(false);
      await expect(provider.locator(".usage-figure")).toHaveText(await written(locale, () => formatPercent(0.82)));
      const figure = (await provider.locator(".usage-figure").boundingBox())!;
      const expiryCell = (await provider.boundingBox())!;
      expect(figure.x + figure.width).toBeLessThanOrEqual(expiryCell.x + expiryCell.width);

      // Last fetch: the outcome and its time whole, on a second line when the
      // words need it, inside a row of the usual height.
      const fetched = rowNamed(page, "建材市场").locator("td.rec-fetch .pc-td-body");
      expect(await widerThanBox(fetched), "the last fetch is cut").toBe(false);
      expect((await fetched.boundingBox())!.height).toBeLessThan(twoLines);

      // Steps: the turned-off count on its own line, whole, and in words in the title.
      const steps = longRow.getByTestId("record-steps");
      await expect(steps).toHaveAttribute("title", t.records.steps(2, 1));
      await expect(steps.locator("small")).toHaveText(t.records.stepsOff(1));
      expect(await widerThanBox(steps.locator("small"))).toBe(false);
      const off = (await steps.locator("small").boundingBox())!;
      const stepsCell = (await steps.boundingBox())!;
      expect(off.x + off.width, "the second line ends inside its cell").toBeLessThanOrEqual(stepsCell.x + stepsCell.width);
    });

    test("on a legacy store the counting word fits the node columns", async ({ page }) => {
      await open(page, "&store=legacy&manifest=s1", "records-migrate", locale);
      // Read in the frame the word is drawn: the previews behind it finish within a second.
      const counting = await page.waitForFunction((word) => {
        const cells = [...document.querySelectorAll('[data-testid="record-nodes-in"] .pc-td-body')];
        const words = cells.filter((cell) => cell.textContent?.trim() === word);
        return words.length ? words.map((cell) => cell.scrollWidth > cell.clientWidth + 1) : null;
      }, t.counts.counting);
      expect(await counting.jsonValue()).not.toContain(true);
    });
  });

  test.describe(`at 375 the table is stacked rows, in ${locale}`, () => {
    test.use({ viewport: { width: 375, height: 812 } });

    test("each row carries its four state items, and nothing scrolls sideways", async ({ page }) => {
      await open(page, "&fixture=states", "record-row", locale);
      await expect(page.getByTestId("records-table").locator("table")).toHaveAttribute("data-stacked", "true");
      const row = rowNamed(page, "建材市场");
      await expect(row.getByTestId("record-published")).toBeVisible();
      await expect(row.getByTestId("record-nodes")).toHaveText(t.counts.pair("166", "25"));
      await expect(row.locator("td[data-expiry=soon]")).toContainText(await written(locale, () => t.provider.expiresIn(6, formatDays(6))));
      await expect(row.locator("td.rec-fetch")).toContainText(t.refresh.refreshed);
      expect(await docWidth(page)).toBe(375);
    });
  });
}

test.describe("on a wider frame", () => {
  test.use({ viewport: { width: 1920, height: 1000 } });

  test("a long name takes the room it has", async ({ page }) => {
    await open(page, "&fixture=states&manifest=s1", "record-row");
    const long = rowNamed(page, LONG).getByTestId("record-name").locator("strong");
    expect((await long.boundingBox())!.width).toBeGreaterThan(380);
  });
});

test.describe("manual order", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("is not offered while the signed plugin lacks reorder, and the position still shows", async ({ page }) => {
    await open(page, "", "record-row");
    await expect(page.getByTestId("record-grip")).toHaveCount(0);
    await expect(page.getByTestId("records-order-note")).toBeVisible();
    await expect(page.getByTestId("record-position").first()).toHaveText("1");
    // Nor does the row menu offer the moves.
    await page.getByTestId("record-row").first().locator("[data-row-menu]").getByRole("button").click();
    await expect(page.getByRole("menu").getByRole("menuitem").first()).toBeVisible();
    await expect(page.getByRole("menu").getByRole("menuitem", { name: m.records.moveUp })).toHaveCount(0);
  });

  test("moves a row from the keyboard alone, announced, with focus kept on it", async ({ page }) => {
    await open(page, "&manifest=s1", "record-grip");
    const first = (await names(page))[0]!;
    const second = (await names(page))[1]!;
    const live = page.getByTestId("records-live");
    const grip = rowNamed(page, first).getByTestId("record-grip");
    await grip.focus();
    await expect(grip).toHaveAccessibleName(m.records.gripLabel(first, 1, 23));

    // On the grip, the arrows move the row.
    await page.keyboard.press("ArrowDown");
    await expect(live).toHaveText(m.records.moved(first, 2, 23));
    await expect(positionOf(page, first)).toHaveText("2");
    await expect(positionOf(page, second)).toHaveText("1");
    expect((await names(page)).slice(0, 2)).toEqual([second, first]);
    await expect(rowNamed(page, first).getByTestId("record-grip")).toBeFocused();

    // From anywhere in the row, Alt and an arrow do the same.
    await page.keyboard.press("Tab");
    await expect(rowNamed(page, first).getByTestId("record-name")).toBeFocused();
    await page.keyboard.press("Alt+ArrowUp");
    await expect(live).toHaveText(m.records.moved(first, 1, 23));
    expect((await names(page)).slice(0, 2)).toEqual([first, second]);

    // At the top there is nowhere to go, and it says so.
    await rowNamed(page, first).getByTestId("record-grip").focus();
    await page.keyboard.press("ArrowUp");
    await expect(live).toHaveText(m.records.moveAtTop(first));
    expect(await names(page)).toContain(first);
    expect((await names(page))[0]).toBe(first);
  });

  test("moves among the rows a filter shows, across what it hides", async ({ page }) => {
    await open(page, "&manifest=s1&kind=file", "record-grip");
    const [a, b] = (await names(page)).slice(0, 2);
    const before = Number(await positionOf(page, b!).innerText());
    await rowNamed(page, b!).getByTestId("record-grip").focus();
    await page.keyboard.press("ArrowUp");
    // The file takes the place of the file above it, in the store's whole order.
    await expect(positionOf(page, b!)).toHaveText(String(before - 1));
    expect((await names(page)).slice(0, 2)).toEqual([b, a]);
  });

  test("follows a drag one to one, drops where it is let go, and Escape puts it back", async ({ page }) => {
    await open(page, "&manifest=s1", "record-grip");
    const order = await names(page);
    const grip = rowNamed(page, order[0]!).getByTestId("record-grip");
    const start = (await grip.boundingBox())!;
    const rowAtRest = (await rowNamed(page, order[0]!).boundingBox())!;
    const third = (await rowNamed(page, order[2]!).boundingBox())!;
    // Past the next row's middle, far enough that letting go here would move it.
    const travel = Math.round(rowAtRest.height * 1.6);
    await page.mouse.move(start.x + start.width / 2, start.y + start.height / 2);
    await page.mouse.down();
    await page.mouse.move(start.x + start.width / 2, start.y + start.height / 2 + travel, { steps: 4 });
    // The row moves with the pointer, as far as the pointer went.
    const moved = (await rowNamed(page, order[0]!).boundingBox())!;
    expect(Math.round(moved.y - rowAtRest.y)).toBe(travel);
    // Escape mid-drag puts it back where it came from, and nothing is saved.
    await page.keyboard.press("Escape");
    await page.mouse.up();
    expect(await names(page)).toEqual(order);
    await expect.poll(async () => Math.round((await rowNamed(page, order[0]!).boundingBox())!.y)).toBe(Math.round(rowAtRest.y));

    await page.mouse.move(start.x + start.width / 2, start.y + start.height / 2);
    await page.mouse.down();
    await page.mouse.move(start.x + start.width / 2, third.y + third.height - 2, { steps: 8 });
    await page.mouse.up();
    await expect(page.getByTestId("records-live")).toHaveText(m.records.moved(order[0]!, 3, 23));
    expect((await names(page)).slice(0, 3)).toEqual([order[1], order[2], order[0]]);
  });

  test("puts the stored order back when the store refuses a move, and says so", async ({ page }) => {
    await open(page, "&manifest=s1&reorder=fail", "record-grip");
    const order = await names(page);
    await rowNamed(page, order[0]!).getByTestId("record-grip").focus();
    await page.keyboard.press("ArrowDown");
    await expect(page.getByTestId("records-reorder-error")).toBeVisible();
    // The reason is the store's own words; the sentence around it is the table's.
    await expect(page.getByTestId("records-live")).toContainText(m.records.reorderFailed("").split("(")[0]!.trim());
    expect(await names(page)).toEqual(order);
    await expect(rowNamed(page, order[0]!).getByTestId("record-grip")).toBeFocused();
    expect(await seriousViolations(page)).toEqual([]);
  });

  test("announces a move in the page's locale", async ({ page }) => {
    await open(page, "&manifest=s1", "record-grip", "ru");
    const first = (await names(page))[0]!;
    const grip = rowNamed(page, first).getByTestId("record-grip");
    await grip.focus();
    await expect(grip).toHaveAccessibleName(TABLES.ru.records.gripLabel(first, 1, 23));
    await page.keyboard.press("ArrowDown");
    await expect(page.getByTestId("records-live")).toHaveText(TABLES.ru.records.moved(first, 2, 23));
  });
});

test.describe("manual order on a phone", () => {
  test.use({ viewport: { width: 375, height: 812 }, hasTouch: true });

  test("a row held at the bottom edge scrolls the page under it, and lands past what was on screen", async ({ page }) => {
    await open(page, "&fixture=states&manifest=s1", "record-grip");
    const order = await names(page);
    const first = order[0]!;
    // The first row near the top of the window, clear of the harness's bar.
    const top = (await rowNamed(page, first).boundingBox())!.y;
    await page.evaluate((by) => window.scrollBy(0, by), top - 80);
    const tops = await Promise.all(order.map(async (name) => (await rowNamed(page, name).boundingBox())!.y));
    const onScreen = tops.filter((y) => y < 812).length;
    const before = await page.evaluate(() => window.scrollY);
    const grip = (await rowNamed(page, first).getByTestId("record-grip").boundingBox())!;
    await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
    await page.mouse.down();
    await page.mouse.move(grip.x + grip.width / 2, 811, { steps: 10 });
    // Held still at the edge, the page keeps scrolling under the row.
    await expect.poll(() => page.evaluate(() => window.scrollY), { timeout: 5000 }).toBeGreaterThan(before + 400);
    await page.mouse.up();
    // "Moved <name> to position" whatever the position is.
    await expect(page.getByTestId("records-live")).toContainText(m.records.moved(first, 0, 0).split(" 0 ")[0]!);
    expect(Number(await positionOf(page, first).innerText())).toBeGreaterThan(onScreen);
  });

  test("the row menu moves a record to either end and one step, from a tap, announced", async ({ page }) => {
    await open(page, "&manifest=s1", "record-grip");
    const first = (await names(page))[0]!;
    const live = page.getByTestId("records-live");
    const menu = page.getByRole("menu");
    const trigger = () => rowNamed(page, first).getByRole("button", { name: m.records.actionsFor(first) });

    await trigger().tap();
    // At the top there is nowhere up to go.
    await expect(menu.getByRole("menuitem", { name: m.records.moveUp })).toBeDisabled();
    await expect(menu.getByRole("menuitem", { name: m.records.moveTop })).toBeDisabled();
    await menu.getByRole("menuitem", { name: m.records.moveBottom }).tap();
    await expect(live).toHaveText(m.records.moved(first, 23, 23));
    expect((await names(page)).at(-1)).toBe(first);
    await expect(trigger()).toBeFocused();

    await trigger().tap();
    await expect(menu.getByRole("menuitem", { name: m.records.moveBottom })).toBeDisabled();
    await menu.getByRole("menuitem", { name: m.records.moveUp }).tap();
    await expect(live).toHaveText(m.records.moved(first, 22, 23));

    await trigger().tap();
    await menu.getByRole("menuitem", { name: m.records.moveTop }).tap();
    await expect(live).toHaveText(m.records.moved(first, 1, 23));
    expect((await names(page))[0]).toBe(first);
  });
});

test.describe("a chain the native engine cannot run", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  const PATTERN = "^(?!.*(过期|剩余|官网)).*$";
  const step1 = m.regexOffer.step(1, m.operators["Regex Filter"].label);

  test("refuses the save, names the step, and the offered rewrite makes it save", async ({ page }) => {
    await open(page, "&fixture=states&manifest=s1", "record-row");
    // Edit lives on the record's panel; the row menu holds the verbs that do not open it.
    await rowNamed(page, "lookahead-provider").getByTestId("record-name").click();
    await page.locator(".pc-side-panel").getByRole("button", { name: m.actions.edit }).click();
    await page.getByRole("tab", { name: startsWith(m.editor.tabs.operations) }).click();
    // Change the chain (turn Sort off), so the save compiles it strictly.
    await page.getByRole("checkbox", { name: startsWith(m.chain.enable("2. ")) }).uncheck();
    await page.getByRole("button", { name: m.editor.save, exact: true }).click();
    const offer = page.getByTestId("regex-rewrite-offer");
    await expect(offer).toBeVisible();
    await expect(offer).toContainText(step1);
    await expect(offer).toContainText(PATTERN);
    await expect(offer).toContainText("过期|剩余|官网");
    expect(await seriousViolations(page)).toEqual([]);
    await offer.getByRole("button", { name: m.regexOffer.rewriteAction(1) }).click();
    await expect(offer).toContainText(m.regexOffer.resolvedAfterRefusal);
    // The notice and the message beside Save follow the chain, not the refusal.
    await expect(offer).toContainText(m.regexOffer.resolvedTitle);
    await expect(offer).not.toContainText(m.regexOffer.title);
    await expect(page.locator(".editor-actions")).not.toContainText(m.subs.regexRefused([1]));
    await page.getByRole("button", { name: m.editor.save, exact: true }).click();
    await expect(page.getByTestId("record-row").first()).toBeVisible();
    await expect(rowNamed(page, "lookahead-provider").getByTestId("record-flagged")).toHaveCount(0);
  });

  test("a flagged record says so in its panel and on its page, and its editor offers the rewrite before any save", async ({ page }) => {
    await open(page, "&fixture=states&manifest=s1", "record-row");
    await rowNamed(page, "lookahead-provider").getByTestId("record-name").click();
    const panel = page.locator(".pc-side-panel");
    await expect(panel.getByTestId("record-flagged")).toBeVisible();
    // The reason in words, where no pointer can hover for a title, with the way out.
    await expect(panel).toContainText(m.records.flaggedTitle);
    await panel.getByRole("button", { name: m.record.openPage }).click();
    const head = page.locator(".record-head");
    await expect(head.getByTestId("record-flagged")).toBeVisible();
    await expect(head).toContainText(m.records.flaggedTitle);
    await head.getByRole("button", { name: m.actions.edit }).click();
    // Nothing changed and nothing saved: the step and its rewrite are named at once.
    const offer = page.getByTestId("regex-rewrite-offer");
    await expect(offer).toBeVisible();
    await expect(offer).toContainText(step1);
    await expect(offer).toContainText(PATTERN);
    await expect(offer).toContainText(m.regexOffer.pending);
    expect(await seriousViolations(page)).toEqual([]);
    await offer.getByRole("button", { name: m.regexOffer.rewriteAction(1) }).click();
    await expect(offer).toContainText(m.regexOffer.resolvedTitle);
    await expect(offer).toContainText(m.regexOffer.resolvedAfterRewrite);
    await page.getByRole("button", { name: m.editor.save, exact: true }).click();
    await expect(rowNamed(page, "lookahead-provider")).toBeVisible();
    await expect(rowNamed(page, "lookahead-provider").getByTestId("record-flagged")).toHaveCount(0);
  });
});
