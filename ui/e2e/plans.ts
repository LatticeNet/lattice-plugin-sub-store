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
 * The frame holds its width, and axe finds nothing serious. `exclude` names
 * a finding that predates this lane and lives in another surface, so this
 * suite stays about its own states; each use says which and why.
 */
export async function settled(page: Page, width: number, exclude: readonly string[] = []): Promise<void> {
  expect(await docWidth(page)).toBeLessThanOrEqual(width);
  expect(await seriousViolations(page, exclude)).toEqual([]);
}

/** A full-page screenshot into SHOTS_DIR, when it is set. */
export async function shot(page: Page, name: string, meta: { width: number; theme: Theme; locale?: Locale }): Promise<void> {
  const dir = process.env.SHOTS_DIR;
  if (!dir) return;
  mkdirSync(dir, { recursive: true });
  await page.waitForTimeout(250);
  await page.screenshot({ path: join(dir, `${name}-${meta.width}-${meta.theme}-${meta.locale ?? "en"}.png`), fullPage: true });
}
