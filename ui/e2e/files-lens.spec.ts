import { expect, test, type Page } from "@playwright/test";

/**
 * The record lenses on the plugin chassis, driven at the widths a design
 * review uses.
 *
 * Every assertion here stands in for a measurement someone took by hand and
 * wrote up as a finding: the document that scrolled sideways at 375, the
 * primary action that wrapped under the toolbar at 1440, the name that pushed
 * the Actions column over the Published column. A regression reads as the
 * same sentence the review did.
 */

async function openSubscriptions(page: Page): Promise<void> {
  await page.goto("/dev.html");
  await page.locator(".pc-table .pc-row").first().waitFor();
}

async function openFiles(page: Page): Promise<void> {
  await page.goto("/dev.html?lens=files");
  await page.locator(".pc-table .pc-row").first().waitFor();
}

/** The row whose name is deliberately too long. */
function longRow(page: Page) {
  return page.locator(".pc-row", { hasText: "A deliberately long" }).first();
}

test.describe("375", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("the document never scrolls sideways, with a row open or a sheet up", async ({ page }) => {
    await openSubscriptions(page);
    const frame = page.viewportSize()!.width;
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(frame);
    // The stacked form of the row, not eleven columns squeezed into 375px.
    await expect(page.locator(".pc-table").first()).toHaveAttribute("data-stacked", "true");
    await page.locator("#rec-openjobs-host .pc-toggle").click();
    await page.locator("#rec-chain-openjobs-host").waitFor();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(frame);
    // The sheet opens from the row menu and stays inside the frame for every
    // frame of its entrance.
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
    // Document coordinates: the click scrolls the row into view, and a
    // viewport box would report that scroll as the row moving.
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
    await expect(page.locator(".pc-table").first()).toHaveAttribute("data-stacked", "true");
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375);
    const name = (await longRow(page).locator(".pc-name strong").boundingBox())!;
    expect(Math.round(name.x + name.width)).toBeLessThanOrEqual(375);
  });
});

test.describe("700", () => {
  test.use({ viewport: { width: 700, height: 900 } });

  test("the table scrolls sideways inside its card with the record column pinned", async ({ page }) => {
    await openSubscriptions(page);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(700);
    const wrap = page.locator(".pc-table-wrap").first();
    const [scrollWidth, clientWidth] = await wrap.evaluate((el) => [el.scrollWidth, el.clientWidth]);
    expect(scrollWidth).toBeGreaterThan(clientWidth);
    const name = page.locator("#rec-openjobs-host td.pc-name");
    expect(await name.evaluate((el) => getComputedStyle(el).position)).toBe("sticky");
    const actions = page.locator("#rec-openjobs-host td.pc-actions");
    expect(await actions.evaluate((el) => getComputedStyle(el).position)).toBe("static");
  });
});

test.describe("1440", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the toolbar is one row and the page never scrolls sideways", async ({ page }) => {
    await openSubscriptions(page);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1440);
    const tabs = (await page.locator(".pc-lens-tabs").boundingBox())!;
    const primary = (await page.getByRole("button", { name: "New subscription" }).boundingBox())!;
    // Wrapped under the row, the primary action sat 40px below the tabs.
    expect(Math.abs(primary.y + primary.height / 2 - (tabs.y + tabs.height / 2))).toBeLessThan(4);
  });

  test("a long name is capped so the columns keep their places", async ({ page }) => {
    await openSubscriptions(page);
    const name = (await longRow(page).locator(".pc-toggle > strong").boundingBox())!;
    expect(name.width).toBeLessThanOrEqual(380);
    const wrap = page.locator(".pc-table-wrap").first();
    const [scrollWidth, clientWidth] = await wrap.evaluate((el) => [el.scrollWidth, el.clientWidth]);
    expect(scrollWidth).toBe(clientWidth);
    // The Published header is not under the sticky Actions column.
    const published = (await page.locator("th", { hasText: "Published" }).boundingBox())!;
    const actions = (await page.locator("th.pc-actions").boundingBox())!;
    expect(published.x + published.width).toBeLessThanOrEqual(actions.x + 1);
  });

  test("records fold under their kind and a record folds its chain", async ({ page }) => {
    await openSubscriptions(page);
    const rows = page.locator(".pc-table .pc-row");
    const before = await rows.count();
    await page.locator(".pc-group-row .pc-toggle", { hasText: "Subscriptions" }).click();
    expect(await rows.count()).toBeLessThan(before);
    await page.locator(".pc-group-row .pc-toggle", { hasText: "Subscriptions" }).click();
    expect(await rows.count()).toBe(before);
    const toggle = page.locator("#rec-openjobs-host .pc-toggle");
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    // The record is read first, then its operations land as rows.
    await expect.poll(() => rows.count()).toBeGreaterThan(before);
    // Escape closes the open row and hands focus back to its toggle.
    await page.keyboard.press("Escape");
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(toggle).toBeFocused();
  });

  test("the files list keeps its columns on one row each", async ({ page }) => {
    await openFiles(page);
    const wrap = page.locator(".pc-table-wrap").first();
    const [scrollWidth, clientWidth] = await wrap.evaluate((el) => [el.scrollWidth, el.clientWidth]);
    expect(scrollWidth).toBe(clientWidth);
    const first = page.locator(".pc-table .pc-row").first();
    const box = (await first.boundingBox())!;
    // Two lines, name over id: the design's row, not a wrapped source cell.
    expect(box.height).toBeLessThanOrEqual(44);
  });
});

/**
 * Both entrances run on the motion tokens, which tokens.css zeroes under
 * reduced motion; the stylesheet names them anyway, and this holds that to be
 * true rather than trusting a duration token to stay 0ms.
 */
test.describe("reduced motion", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("nothing in the sheet or the row chain animates", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await openSubscriptions(page);
    expect(await page.evaluate(() => matchMedia("(prefers-reduced-motion: reduce)").matches)).toBe(true);
    await page.locator("#rec-openjobs-host .pc-toggle").click();
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

  // The chassis pins every actions cell with its own stacking context, so a
  // menu drawn inside the cell was painted over by the rows beneath it: the
  // first row's menu showed three of its seven items. Every item must be the
  // thing under the pointer at its own centre, on a row that has rows below.
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
    // Right edges flush with the trigger, and the menu inside the frame.
    const trigger = (await page.locator('[data-row-menu="cdcd-self-host"] button').first().boundingBox())!;
    const menu = (await page.locator(".rec-menu").boundingBox())!;
    expect(Math.abs(menu.x + menu.width - (trigger.x + trigger.width))).toBeLessThanOrEqual(1);
    expect(menu.y).toBeGreaterThanOrEqual(trigger.y + trigger.height);
  });
});
