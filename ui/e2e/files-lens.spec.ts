import { expect, test, type Page } from "@playwright/test";

/**
 * The record lenses on the plugin chassis, driven at the widths a design
 * review uses.
 *
 * Every assertion here stands in for a measurement someone took by hand and
 * wrote up as a finding: the document that scrolled sideways at 375, the
 * primary action that wrapped under the toolbar at 1440, the name that
 * shoved the actions off the row. A regression reads as the same sentence
 * the review did.
 */

async function openSubscriptions(page: Page): Promise<void> {
  await page.goto("/dev.html");
  await page.locator(".rec-row").first().waitFor();
}

async function openFiles(page: Page): Promise<void> {
  await page.goto("/dev.html?lens=files");
  await page.locator(".rec-list .rec-row").first().waitFor();
}

/** The row whose name is deliberately too long. */
function longRow(page: Page) {
  return page.locator(".rec-row, .pc-row", { hasText: "A deliberately long" }).first();
}

test.describe("375", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("the document never scrolls sideways, with a row open or a sheet up", async ({ page }) => {
    await openSubscriptions(page);
    const frame = page.viewportSize()!.width;
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(frame);
    await expect(page.locator(".rec-list")).toBeVisible();
    await expect(page.locator(".pc-table")).toHaveCount(0);
    await page.locator("#rec-openjobs-host .rec-ident").click();
    await page.locator("#rec-chain-openjobs-host").waitFor();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(frame);
    await page.locator('[data-row-menu="openjobs-host"] button').click();
    const seen = await page.evaluate(async () => {
      const doc = document.documentElement;
      const item = [...document.querySelectorAll<HTMLButtonElement>(".rec-menu button")].find((b) => b.textContent!.includes("Client output"))!;
      let max = 0;
      item.click();
      await new Promise<void>((resolve) => {
        const started = performance.now();
        const tick = () => {
          max = Math.max(max, doc.scrollWidth);
          if (performance.now() - started < 600) requestAnimationFrame(tick);
          else resolve();
        };
        requestAnimationFrame(tick);
      });
      return max;
    });
    expect(seen).toBe(frame);
    const close = (await page.locator(".sheet-close").boundingBox())!;
    expect(Math.round(close.x + close.width)).toBeLessThanOrEqual(frame);
  });

  test("the selection bar floats inside the frame and the rows do not move", async ({ page }) => {
    await openSubscriptions(page);
    const row = page.locator("#rec-cdcd-self-host");
    const top = () => row.evaluate((el) => el.getBoundingClientRect().top + window.scrollY);
    const before = await top();
    await row.locator("input[type=checkbox]").click();
    const bar = page.locator(".pc-batch-bar");
    await bar.waitFor();
    const box = (await bar.boundingBox())!;
    const frame = page.viewportSize()!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(Math.round(box.x + box.width)).toBeLessThanOrEqual(frame.width);
    expect(Math.round(box.y + box.height)).toBeLessThanOrEqual(frame.height);
    expect(await top()).toBe(before);
  });

  test("the files list stacks the same way", async ({ page }) => {
    await openFiles(page);
    await expect(page.locator(".rec-list")).toBeVisible();
    await expect(page.locator(".pc-table")).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375);
    const name = (await longRow(page).locator(".rec-ident-name").boundingBox())!;
    expect(Math.round(name.x + name.width)).toBeLessThanOrEqual(375);
  });
});

test.describe("700", () => {
  test.use({ viewport: { width: 700, height: 900 } });

  test("the list stays inside the frame", async ({ page }) => {
    await openSubscriptions(page);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(700);
    const item = (await page.locator("#rec-openjobs-host").boundingBox())!;
    expect(Math.round(item.x + item.width)).toBeLessThanOrEqual(700);
  });
});

test.describe("1440", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the toolbar is one row and the page never scrolls sideways", async ({ page }) => {
    await openSubscriptions(page);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1440);
    const tabs = (await page.locator(".pc-lens-tabs").boundingBox())!;
    const primary = (await page.getByRole("button", { name: "New subscription" }).boundingBox())!;
    expect(Math.abs(primary.y + primary.height / 2 - (tabs.y + tabs.height / 2))).toBeLessThan(4);
  });

  test("a long name ellipses instead of shoving actions off the row", async ({ page }) => {
    await openSubscriptions(page);
    const name = (await longRow(page).locator(".rec-ident-name").boundingBox())!;
    const actions = (await longRow(page).locator(".rec-row-actions").boundingBox())!;
    const ident = (await longRow(page).locator(".rec-ident").boundingBox())!;
    expect(Math.round(ident.x + ident.width)).toBeLessThanOrEqual(Math.round(actions.x) + 1);
    expect(name.width).toBeLessThan(ident.width);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1440);
  });

  test("kind is a tab and a record folds its chain", async ({ page }) => {
    await openSubscriptions(page);
    const items = page.locator(".rec-row");
    const before = await items.count();
    await page.getByRole("tab", { name: "Single" }).click();
    expect(await items.count()).toBeLessThan(before);
    await page.getByRole("tab", { name: "All" }).click();
    expect(await items.count()).toBe(before);
    const toggle = page.locator("#rec-openjobs-host .rec-ident");
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await page.locator("#rec-chain-openjobs-host .rec-chain-steps").waitFor();
    expect(await items.count()).toBe(before);
    expect(await page.locator("#rec-chain-openjobs-host .rec-chain-steps").count()).toBe(1);
    await expect(page.locator("#rec-openjobs-host .rec-open")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(toggle).toBeFocused();
  });

  test("the files list keeps its columns on one row each", async ({ page }) => {
    await openFiles(page);
    await expect(page.locator(".pc-table")).toHaveCount(0);
    const first = page.locator(".rec-list .rec-row").first();
    const box = (await first.locator(".rec-row-bar").boundingBox())!;
    expect(box.height).toBeLessThanOrEqual(48);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1440);
  });
});

test.describe("reduced motion", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("nothing in the sheet or the row chain animates", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await openSubscriptions(page);
    expect(await page.evaluate(() => matchMedia("(prefers-reduced-motion: reduce)").matches)).toBe(true);
    await page.locator("#rec-openjobs-host .rec-ident").click();
    await page.locator("#rec-chain-openjobs-host").waitFor();
    await page.locator('[data-row-menu="openjobs-host"] button').click();
    await page.locator(".rec-menu button", { hasText: "Client output" }).click();
    await page.locator(".sheet").waitFor();
    const timed = await page.evaluate(() =>
      document
        .getAnimations()
        .map((a) => Number(a.effect?.getTiming().duration ?? 0))
        .filter((d) => d > 0),
    );
    expect(timed).toEqual([]);
  });
});

test.describe("row menu", () => {
  test.use({ viewport: { width: 1440, height: 1100 } });

  test("every item of the first row's menu is on top", async ({ page }) => {
    await openSubscriptions(page);
    await page.locator('[data-row-menu="cdcd-self-host"] button').first().click();
    await page.locator(".rec-menu").waitFor();
    const covered = await page.evaluate(() =>
      [...document.querySelectorAll<HTMLElement>(".rec-menu [role=menuitem]")]
        .filter((item) => {
          const box = item.getBoundingClientRect();
          const hit = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2);
          return !(hit === item || item.contains(hit));
        })
        .map((item) => item.textContent!.trim()),
    );
    expect(covered).toEqual([]);
    const trigger = (await page.locator('[data-row-menu="cdcd-self-host"] button').first().boundingBox())!;
    const menu = (await page.locator(".rec-menu").boundingBox())!;
    expect(Math.abs(menu.x + menu.width - (trigger.x + trigger.width))).toBeLessThanOrEqual(1);
    expect(menu.y).toBeGreaterThanOrEqual(trigger.y + trigger.height);
  });
});
