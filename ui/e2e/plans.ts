import AxeBuilder from "@axe-core/playwright";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { expect, type Locator, type Page } from "@playwright/test";

import { TABLES, type Locale } from "./messages";

/**
 * Shared drives for the lane 6b suites (fleet-view.spec.ts,
 * migration.spec.ts): the harness address for the plans fixture under the
 * 0.18 manifest, the row menu by action id, axe, the frame's width, and a
 * screenshot when SHOTS_DIR names a directory (the PR lists them).
 */

export const CLOCK = "2026-10-16T08:00:00Z";
export const VIEWPORTS = [
  { width: 1440, height: 900 },
  { width: 375, height: 812 },
] as const;
export const THEMES = ["light", "dark"] as const;
export type Theme = (typeof THEMES)[number];

export interface Open {
  view?: "records" | "fleet" | "overview";
  query?: string;
  locale?: Locale;
  theme?: Theme;
}

export async function openPlans(page: Page, options: Open = {}): Promise<void> {
  const locale = options.locale ?? "en";
  const view = options.view ?? "records";
  await page.goto(`/dev.html?view=${view}&fixture=plans&manifest=s2&clock=${CLOCK}&locale=${locale}&theme=${options.theme ?? "light"}${options.query ?? ""}`);
  await expect(page.locator("html")).toHaveAttribute("lang", locale);
  if (view === "records") await page.locator(".row-open").first().waitFor();
}

export function words(locale: Locale = "en") {
  return TABLES[locale];
}

/** Opens a row's menu and runs one of its actions by id. */
export async function runAction(page: Page, recordId: string, action: string): Promise<void> {
  await page.locator(`.rec-menu-wrap[data-row-menu="${recordId}"] button`).first().click();
  await page.locator(`.rec-menu[data-row-menu="${recordId}"] [data-action="${action}"]`).click();
}

export function menuItem(page: Page, recordId: string, action: string): Locator {
  return page.locator(`.rec-menu[data-row-menu="${recordId}"] [data-action="${action}"]`);
}

export async function openMenu(page: Page, recordId: string): Promise<void> {
  await page.locator(`.rec-menu-wrap[data-row-menu="${recordId}"] button`).first().click();
}

export function row(page: Page, recordId: string): Locator {
  return page.locator(`[data-record-row="${recordId}"]`);
}

export function flow(page: Page): Locator {
  return page.getByTestId("plan-flow");
}

export async function seriousViolations(page: Page, exclude: readonly string[] = []): Promise<string[]> {
  let builder = new AxeBuilder({ page }).exclude(".dev-bar");
  for (const selector of exclude) builder = builder.exclude(selector);
  const result = await builder.analyze();
  return result.violations
    .filter((violation) => violation.impact === "serious" || violation.impact === "critical")
    .map((violation) => `${violation.id}: ${violation.nodes.map((node) => node.target.join(" ")).slice(0, 3).join(", ")}`);
}

export const docWidth = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth);

/**
 * Open sheets whose body scrolls sideways, by title. The document's width
 * cannot see this: a nowrap button wider than a 375 sheet scrolled the sheet
 * body and cut the labels of its left column (the second lane 6b design
 * review), while the page itself held its width.
 */
export const sheetOverflow = (page: Page) =>
  page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>(".pc-side-panel .pc-modal-body")]
      .filter((body) => body.scrollWidth > body.clientWidth + 1)
      .map((body) => `${body.closest(".pc-side-panel")?.querySelector("h2")?.textContent ?? "sheet"}: ${body.scrollWidth} > ${body.clientWidth}`),
  );

/**
 * The frame holds its width, an open sheet holds its own, and axe finds
 * nothing serious. `exclude` names a finding that predates this lane and
 * lives in another surface, so this suite stays about its own states; each
 * use says which and why.
 */
export async function settled(page: Page, width: number, exclude: readonly string[] = []): Promise<void> {
  expect(await docWidth(page)).toBeLessThanOrEqual(width);
  expect(await sheetOverflow(page)).toEqual([]);
  expect(await seriousViolations(page, exclude)).toEqual([]);
}

/**
 * Children of a cell that reach past its edges, which a cell with
 * `overflow: hidden` cuts off. Records table cells clip, so a sub-line
 * wider than its column lost its last word in every locale.
 */
export const clippedInCells = (page: Page, selector: string) =>
  page.evaluate((cells) => {
    const out: string[] = [];
    for (const cell of document.querySelectorAll<HTMLElement>(cells)) {
      const box = cell.getBoundingClientRect();
      for (const child of cell.querySelectorAll<HTMLElement>("*")) {
        const rect = child.getBoundingClientRect();
        if (!rect.width || !rect.height) continue;
        if (rect.right > box.right + 0.5 || rect.left < box.left - 0.5) {
          out.push(`${cell.closest("tr")?.getAttribute("data-record-row") ?? "?"} .${child.className}: ${Math.round(rect.right - box.right)}px`);
        }
      }
    }
    return out;
  }, selector);

/** Whether focus is inside the open sheet. */
export const focusInSheet = (page: Page) => page.evaluate(() => !!document.activeElement?.closest(".pc-side-panel"));

/** Runs a row menu item from the keyboard: the trigger, Enter, the arrows down to the item, Enter. */
export async function runActionByKeyboard(page: Page, recordId: string, action: string): Promise<void> {
  await page.locator(`.rec-menu-wrap[data-row-menu="${recordId}"] button`).first().focus();
  await page.keyboard.press("Enter");
  const menu = page.locator(`.rec-menu[data-row-menu="${recordId}"]`);
  await expect(menu).toBeVisible();
  for (let step = 0; step < 16; step += 1) {
    if ((await page.evaluate(() => document.activeElement?.getAttribute("data-action"))) === action) break;
    await page.keyboard.press("ArrowDown");
  }
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-action"))).toBe(action);
  await page.keyboard.press("Enter");
}

/**
 * A screenshot into SHOTS_DIR, when it is set: the full page, or with a
 * sheet or dialog open the viewport, which is what the operator sees. A
 * full-page capture stretches the frame to the table's height and draws an
 * open sheet over only its top, which read at 375 as a flow appended under
 * the table (the lane 6b code review).
 */
export async function shot(page: Page, name: string, meta: { width: number; theme: Theme; locale?: Locale }): Promise<void> {
  const dir = process.env.SHOTS_DIR;
  if (!dir) return;
  mkdirSync(dir, { recursive: true });
  await page.waitForTimeout(250);
  const overlay = await page.locator('.pc-side-panel, .lt-dialog, [data-testid="plan-flow"]').first().isVisible().catch(() => false);
  await page.screenshot({ path: join(dir, `${name}-${meta.width}-${meta.theme}-${meta.locale ?? "en"}.png`), fullPage: !overlay });
}
