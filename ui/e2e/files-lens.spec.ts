import { expect, test, type Page } from "@playwright/test";

/**
 * The Files lens, driven at the widths a design review uses.
 *
 * Every assertion here stands in for a measurement someone took by hand and
 * wrote up as a finding. The numbers in the comments are what the broken build
 * produced, so a regression reads as the same sentence the review did.
 */

const LONG_NAME_MIN = 50;

/** Open the lens and hand back the row whose name is deliberately too long. */
async function openFiles(page: Page): Promise<void> {
  await page.goto("/dev.html");
  await page.getByRole("tab", { name: /^Files/ }).click();
  await page.locator(".rec-files .rec").first().waitFor();
}

/** The index of the row carrying the 100-character name. */
async function longRowIndex(page: Page): Promise<number> {
  return page.evaluate((min) => {
    const rows = [...document.querySelectorAll(".rec-files .rec")];
    return rows.findIndex((r) => (r.querySelector(".rec-name-text")?.textContent ?? "").length > min);
  }, LONG_NAME_MIN);
}

test.describe("375", () => {
  test.use({ viewport: { width: 375, height: 800 } });

  /**
   * High 1. The sheet is `right: 0`, so a `translateX(16px)` entrance pushed it
   * outside the frame for the length of the animation: the document measured
   * 391 on a 375 frame and settled back to 375 once it finished. Sampling after
   * the animation passes either way, so this samples every frame of it.
   */
  test("the document never scrolls sideways while the sheet opens", async ({ page }) => {
    await openFiles(page);
    const i = await longRowIndex(page);
    const seen = await page.evaluate(async (index) => {
      const doc = document.documentElement;
      const row = document.querySelectorAll(".rec-files .rec")[index] as HTMLElement;
      let max = 0;
      (row.querySelector(".rec-name") as HTMLElement).click();
      await new Promise<void>((resolve) => {
        const started = performance.now();
        const tick = () => {
          max = Math.max(max, doc.scrollWidth);
          if (performance.now() - started < 600) requestAnimationFrame(tick);
          else resolve();
        };
        requestAnimationFrame(tick);
      });
      return { max, innerWidth: window.innerWidth };
    }, i);
    expect(seen.max).toBe(seen.innerWidth);
  });

  test("the sheet and its close control stay inside the frame", async ({ page }) => {
    await openFiles(page);
    const i = await longRowIndex(page);
    await page.locator(".rec-files .rec").nth(i).locator(".rec-name").click();
    await page.locator(".sheet").waitFor();
    const frame = page.viewportSize()!.width;
    const sheet = (await page.locator(".sheet").boundingBox())!;
    const close = (await page.locator(".sheet-close").boundingBox())!;
    expect(sheet.x).toBe(0);
    expect(Math.round(sheet.x + sheet.width)).toBeLessThanOrEqual(frame);
    expect(Math.round(close.x + close.width)).toBeLessThanOrEqual(frame);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(frame);
  });

  /**
   * High 2. A 112px action column sat beside the name on line 1, leaving a
   * 100-character name 131px to render in while the source line under it got
   * 255px. The name takes the row now and the actions drop to a line of
   * their own.
   */
  test("the name spans the row and the actions drop below it", async ({ page }) => {
    await openFiles(page);
    const i = await longRowIndex(page);
    const row = page.locator(".rec-files .rec").nth(i);
    const body = (await row.locator(".rec-body").boundingBox())!;
    const actions = (await row.locator(".rec-actions").boundingBox())!;
    const rowBox = (await row.boundingBox())!;
    // Against the row's content edge: the row carries 12px of padding, which
    // is not width the name is allowed to take.
    const padRight = await row.evaluate((el) => parseFloat(getComputedStyle(el).paddingRight));
    // The name reaches that edge rather than stopping short of a reserved
    // action column.
    expect(body.width).toBeGreaterThan(240);
    expect(Math.round(body.x + body.width)).toBeGreaterThanOrEqual(Math.round(rowBox.x + rowBox.width - padRight) - 1);
    // And the actions are on a later line, not beside it.
    expect(actions.y).toBeGreaterThanOrEqual(body.y + body.height);
  });

  /** Nit. 26px is a quarter of the area a finger needs. */
  test("the row actions are finger-sized", async ({ page }) => {
    await openFiles(page);
    const button = page.locator(".rec-files .rec .rec-actions .lt-iconbtn").first();
    const box = (await button.boundingBox())!;
    expect(box.width).toBeGreaterThanOrEqual(44);
    expect(box.height).toBeGreaterThanOrEqual(44);
    const close = page.locator(".sheet-close");
    await page.locator(".rec-files .rec").first().locator(".rec-name").click();
    await close.waitFor();
    const closeBox = (await close.boundingBox())!;
    expect(closeBox.width).toBeGreaterThanOrEqual(44);
  });

  /**
   * Medium 3. The sheet is as tall as the document it shows (3845px for a real
   * configuration), the page is the one scroller, and the only way out used to
   * scroll off the top with the first screenful.
   */
  test("a close control stays on screen down a long document", async ({ page }) => {
    await openFiles(page);
    const i = await longRowIndex(page);
    await page.locator(".rec-files .rec").nth(i).locator(".rec-name").click();
    await page.locator(".sheet").waitFor();
    const tall = await page.evaluate(() => document.documentElement.scrollHeight);
    expect(tall).toBeGreaterThan(1500);
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
    await page.waitForTimeout(150);
    const close = (await page.locator(".sheet-close").boundingBox())!;
    const height = page.viewportSize()!.height;
    expect(close.y).toBeGreaterThanOrEqual(0);
    expect(close.y + close.height).toBeLessThanOrEqual(height);
  });

  /** Nit. "Stored here" and "nodes from merge-cd-openjobs" ran together. */
  test("the two source facts are told apart", async ({ page }) => {
    await openFiles(page);
    const cell = page.locator(".rec-files .rec .rec-status-cell").first();
    const status = (await cell.locator(".rec-status").boundingBox())!;
    const quota = (await cell.locator(".rec-quota").boundingBox())!;
    const separated =
      quota.y >= status.y + status.height ||
      (await cell
        .locator(".rec-status")
        .evaluate((el) => getComputedStyle(el, "::after").content))
        .replace(/["']/g, "")
        .trim().length > 0;
    expect(separated).toBe(true);
  });
});

test.describe("768", () => {
  test.use({ viewport: { width: 768, height: 900 } });

  test("the document never scrolls sideways while the sheet opens", async ({ page }) => {
    await openFiles(page);
    const i = await longRowIndex(page);
    const seen = await page.evaluate(async (index) => {
      const doc = document.documentElement;
      const row = document.querySelectorAll(".rec-files .rec")[index] as HTMLElement;
      let max = 0;
      (row.querySelector(".rec-name") as HTMLElement).click();
      await new Promise<void>((resolve) => {
        const started = performance.now();
        const tick = () => {
          max = Math.max(max, doc.scrollWidth);
          if (performance.now() - started < 600) requestAnimationFrame(tick);
          else resolve();
        };
        requestAnimationFrame(tick);
      });
      return { max, innerWidth: window.innerWidth };
    }, i);
    expect(seen.max).toBe(seen.innerWidth);
  });
});

test.describe("1440", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  /**
   * Medium 1. `max-width: 68%` cut a 671px name to 597px inside an 878px cell
   * and left roughly 180px blank beside it. The tags give instead, so the name
   * spends what is there.
   */
  test("the name uses the room the cell has", async ({ page }) => {
    await openFiles(page);
    const i = await longRowIndex(page);
    const row = page.locator(".rec-files .rec").nth(i);
    const cell = (await row.locator(".rec-body").boundingBox())!;
    const name = (await row.locator(".rec-name").boundingBox())!;
    // Nothing blank between the end of the name and the end of its cell beyond
    // the tag strip's own gap.
    expect(name.width).toBeGreaterThan(cell.width * 0.95);
  });

  /**
   * Medium 2. As `1fr` the name track took 878px and started SOURCE at x=995,
   * a 740px void. Capped, SOURCE comes back left and the actions stay right.
   */
  test("the name track is capped and the columns still line up", async ({ page }) => {
    await openFiles(page);
    const templates = await page.evaluate(() =>
      [...new Set([...document.querySelectorAll(".rec-files .rec")].map((r) => getComputedStyle(r).gridTemplateColumns))],
    );
    // Every row is its own grid; a content-sized track would give each a
    // different one and the columns would not line up down the list.
    expect(templates).toHaveLength(1);
    const nameTrack = Number(templates[0]!.split(" ")[2]!.replace("px", ""));
    expect(nameTrack).toBeLessThanOrEqual(48 * 16);
    const i = await longRowIndex(page);
    const row = page.locator(".rec-files .rec").nth(i);
    const source = (await row.locator(".rec-status-cell").boundingBox())!;
    expect(source.x).toBeLessThan(900);
    const rowBox = (await row.boundingBox())!;
    const actions = (await row.locator(".rec-actions").boundingBox())!;
    const padRight = await row.evaluate((el) => parseFloat(getComputedStyle(el).paddingRight));
    // The slack went to SOURCE, not to the action column. As minmax(actions,
    // 1fr) that cell was 569px of mostly nothing, and its buttons are
    // `opacity: 0` until hover, so the row's right half read as empty.
    expect(actions.width).toBeLessThan(200);
    // And the buttons still sit on the row's right edge.
    expect(Math.round(actions.x + actions.width)).toBeGreaterThanOrEqual(Math.round(rowBox.x + rowBox.width - padRight) - 1);
  });

  /** Nit. `pretty` only suppresses a one-word orphan; the tail stayed put. */
  test("the lede balances its lines", async ({ page }) => {
    await openFiles(page);
    const wrap = await page
      .locator(".section-heading p")
      .first()
      .evaluate((el) => getComputedStyle(el).textWrap || getComputedStyle(el).textWrapStyle);
    expect(wrap).toBe("balance");
  });

  /** Desktop still gets a target bigger than 26px. */
  test("the row actions are mouse-sized", async ({ page }) => {
    await openFiles(page);
    const row = page.locator(".rec-files .rec").first();
    await row.hover();
    const box = (await row.locator(".rec-actions .lt-iconbtn").first().boundingBox())!;
    expect(box.width).toBeGreaterThanOrEqual(32);
    expect(box.height).toBeGreaterThanOrEqual(32);
  });
});

/**
 * Medium 4. Both animations run on --lt-dur-base, which tokens.css already
 * zeroes under reduced motion, so they were inert before this. The stylesheet
 * now names them anyway, and this holds that to be true rather than trusting a
 * duration token to stay 0ms.
 */
test.describe("reduced motion", () => {
  // reducedMotion is not a test.use fixture; it is set on the page below.
  test.use({ viewport: { width: 1440, height: 900 } });

  test("nothing in the sheet or the row chain animates", async ({ page }) => {
    // Set on the page rather than through test.use: the describe-level fixture
    // did not reach the context here, and the test then quietly asserted
    // against normal motion, where it passed for the wrong reason.
    await page.emulateMedia({ reducedMotion: "reduce" });
    await openFiles(page);
    expect(await page.evaluate(() => matchMedia("(prefers-reduced-motion: reduce)").matches)).toBe(true);
    const i = await longRowIndex(page);
    await page.locator(".rec-files .rec").nth(i).locator(".rec-name").click();
    await page.locator(".sheet").waitFor();
    // Nothing is allowed to run over time. Counting "running" animations races
    // the zero-duration ones, which stay listed for a frame after they finish;
    // their duration is the contract, so assert on that.
    // Nothing is allowed to run over time. Counting "running" animations would
    // race the zero-duration ones, which stay listed for a frame after they
    // finish; their duration is the contract, so assert on that.
    const timed = await page.evaluate(() =>
      document
        .getAnimations()
        .map((a) => Number(a.effect?.getTiming().duration ?? 0))
        .filter((d) => d > 0),
    );
    expect(timed).toEqual([]);
  });
});
