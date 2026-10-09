import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

/**
 * The Records table (design 28, S1): every state it draws, named by a test id
 * rather than by its English words, at the two widths the design holds it
 * to; axe over each with no serious or critical violation; and the order
 * changed from the keyboard alone, from a drag that follows the pointer, and
 * put back when the store refuses it.
 *
 * The harness clock starts at one moment (dev/clock.ts), so "expires in 6
 * days" and every date in a title read the same on every run.
 */

const CLOCK = "2026-10-09T08:00:00Z";

async function open(page: Page, query: string, ready: string): Promise<void> {
  await page.goto(`/dev.html?view=records&clock=${CLOCK}${query}`);
  await page.getByTestId(ready).first().waitFor();
}

const docWidth = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth);

/** Serious and critical violations on the page as it stands, the harness's own bar left out. */
async function seriousViolations(page: Page): Promise<string[]> {
  const result = await new AxeBuilder({ page }).exclude(".dev-bar").analyze();
  return result.violations
    .filter((violation) => violation.impact === "serious" || violation.impact === "critical")
    .map((violation) => `${violation.id}: ${violation.nodes.map((node) => node.target.join(" ")).slice(0, 3).join(", ")}`);
}

const rowNamed = (page: Page, name: string) => page.getByTestId("record-row").filter({ has: page.getByTestId("record-name").getByText(name, { exact: true }) });
const positionOf = (page: Page, name: string) => rowNamed(page, name).getByTestId("record-position");
const names = (page: Page) => page.getByTestId("record-name").allInnerTexts();

for (const viewport of [{ width: 1440, height: 900 }, { width: 375, height: 812 }]) {
  test.describe(`records states at ${viewport.width}`, () => {
    test.use({ viewport });

    test("loading holds a skeleton, named", async ({ page }) => {
      await open(page, "&state=slow", "records-loading");
      await expect(page.getByTestId("records-loading")).toBeVisible();
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("an empty store offers creation and the import, not an empty table", async ({ page }) => {
      await open(page, "&state=empty", "records-empty");
      await expect(page.getByTestId("records-table")).toHaveCount(0);
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("a filter that matches nothing says so, and offers to clear it", async ({ page }) => {
      await open(page, "&q=no-such-record-anywhere", "records-no-match");
      await expect(page.getByTestId("records-no-match").getByRole("button", { name: "Clear filters" })).toBeEnabled();
      // The kind filter stays in reach above it.
      await expect(page.getByRole("radio", { name: /^All/ })).toBeChecked();
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("an unreadable store says so, with a way to try again", async ({ page }) => {
      await open(page, "&state=error", "records-error");
      await expect(page.getByTestId("records-error").getByRole("button", { name: "Try again" })).toBeVisible();
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("a reload that fails behind the rows keeps them and says they are the last good read", async ({ page }) => {
      await open(page, "&state=stale", "record-row");
      await page.locator(".ss-header").getByRole("button", { name: "Refresh" }).click();
      await expect(page.getByTestId("records-stale")).toBeVisible();
      await expect(page.getByTestId("record-row").first()).toBeVisible();
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("a read-only session reads every row, cannot reorder, and does not claim what it cannot know", async ({ page }) => {
      await open(page, "&state=readonly&manifest=s1", "record-row");
      await expect(page.getByTestId("records-readonly")).toBeVisible();
      await expect(page.getByTestId("record-grip")).toHaveCount(0);
      // The share list needs scopes this session lacks: unknown, not "not published".
      await expect(page.getByTestId("record-published").first()).toContainText("unknown");
      await expect(page.getByTestId("record-published").filter({ hasText: "not published" })).toHaveCount(0);
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("an expired provider, a flagged chain and long names each read as what they are", async ({ page }) => {
      await open(page, "&fixture=states&manifest=s1", "record-row");
      const expired = rowNamed(page, "openjobs-host-trojan").getByTestId("record-expired");
      await expect(expired).toBeVisible();
      await expect(expired).toHaveText("expired 2 days ago");
      for (const name of ["lookahead-provider", "backreference-rename"]) {
        await expect(rowNamed(page, name).getByTestId("record-flagged"), name).toBeVisible();
      }
      await expect(rowNamed(page, "建材市场").getByTestId("record-flagged")).toHaveCount(0);
      // Long names stay inside their cell and the page never scrolls sideways.
      const long = page.getByTestId("record-name").filter({ hasText: "一个非常非常长的机场订阅名称" });
      await long.scrollIntoViewIfNeeded();
      const name = (await long.boundingBox())!;
      const table = (await page.getByTestId("records-table").boundingBox())!;
      expect(name.x + name.width).toBeLessThanOrEqual(table.x + table.width + 1);
      await expect(long).toHaveAttribute("title", /一个非常非常长的机场订阅名称/);
      expect(await docWidth(page)).toBe(viewport.width);
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("a legacy store prompts the migration and runs it", async ({ page }) => {
      await open(page, "&store=legacy&manifest=s1", "records-migrate");
      // Rows cannot move on a legacy store; the note says why instead of a dead grip.
      await expect(page.getByTestId("record-grip")).toHaveCount(0);
      await expect(page.getByTestId("records-order-note")).toBeVisible();
      expect(await seriousViolations(page)).toEqual([]);
      await page.getByTestId("records-migrate").getByRole("button", { name: "Migrate store" }).click();
      await expect(page.getByTestId("records-migrated")).toBeVisible();
      await expect(page.getByTestId("records-migrate")).toHaveCount(0);
      await expect(page.getByTestId("record-grip").first()).toBeVisible();
    });

    test("compact rows drop the remark and the icon and keep the state", async ({ page }) => {
      await open(page, "&fixture=states", "record-row");
      const row = rowNamed(page, "lookahead-provider");
      await expect(row.locator("td.pc-name small")).toBeVisible();
      await page.getByTestId("records-density").click();
      await expect(page.getByTestId("records-density")).toHaveAttribute("aria-pressed", "true");
      await expect(row.locator("td.pc-name small")).toBeHidden();
      await expect(row.locator(".rec-kind-icon")).toHaveCount(0);
      await expect(row.getByTestId("record-flagged")).toBeVisible();
      await expect(page).toHaveURL(/[?&]density=compact(&|#|$)/);
    });
  });
}

test.describe("at 375 the table is stacked rows", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("each row carries its four state items, and nothing scrolls sideways", async ({ page }) => {
    await open(page, "&fixture=states", "record-row");
    await expect(page.getByTestId("records-table").locator("table")).toHaveAttribute("data-stacked", "true");
    const row = rowNamed(page, "建材市场");
    await expect(row.getByTestId("record-published")).toBeVisible();
    await expect(row.getByTestId("record-nodes")).toHaveText("166 → 25");
    await expect(row.locator("td[data-expiry=soon]")).toContainText("expires in 6 days");
    await expect(row.locator("td[data-label='Last fetch']")).toContainText("Refreshed");
    expect(await docWidth(page)).toBe(375);
  });
});

test.describe("manual order", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("is not offered while the signed plugin lacks reorder, and the position still shows", async ({ page }) => {
    await open(page, "", "record-row");
    await expect(page.getByTestId("record-grip")).toHaveCount(0);
    await expect(page.getByTestId("records-order-note")).toBeVisible();
    await expect(page.getByTestId("record-position").first()).toHaveText("1");
  });

  test("moves a row from the keyboard alone, announced, with focus kept on it", async ({ page }) => {
    await open(page, "&manifest=s1", "record-grip");
    const first = (await names(page))[0]!;
    const second = (await names(page))[1]!;
    const live = page.getByTestId("records-live");
    const grip = rowNamed(page, first).getByTestId("record-grip");
    await grip.focus();
    await expect(grip).toHaveAccessibleName(`Reorder ${first}, position 1 of 23`);

    // On the grip, the arrows move the row.
    await page.keyboard.press("ArrowDown");
    await expect(live).toHaveText(`Moved ${first} to position 2 of 23.`);
    await expect(positionOf(page, first)).toHaveText("2");
    await expect(positionOf(page, second)).toHaveText("1");
    expect((await names(page)).slice(0, 2)).toEqual([second, first]);
    await expect(rowNamed(page, first).getByTestId("record-grip")).toBeFocused();

    // From anywhere in the row, Alt and an arrow do the same.
    await page.keyboard.press("Tab");
    await expect(rowNamed(page, first).getByTestId("record-name")).toBeFocused();
    await page.keyboard.press("Alt+ArrowUp");
    await expect(live).toHaveText(`Moved ${first} to position 1 of 23.`);
    expect((await names(page)).slice(0, 2)).toEqual([first, second]);

    // At the top there is nowhere to go, and it says so.
    await rowNamed(page, first).getByTestId("record-grip").focus();
    await page.keyboard.press("ArrowUp");
    await expect(live).toHaveText(`${first} is already at the top of the rows shown.`);
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
    await expect(page.getByTestId("records-live")).toHaveText(`Moved ${order[0]} to position 3 of 23.`);
    expect((await names(page)).slice(0, 3)).toEqual([order[1], order[2], order[0]]);
  });

  test("puts the stored order back when the store refuses a move, and says so", async ({ page }) => {
    await open(page, "&manifest=s1&reorder=fail", "record-grip");
    const order = await names(page);
    await rowNamed(page, order[0]!).getByTestId("record-grip").focus();
    await page.keyboard.press("ArrowDown");
    await expect(page.getByTestId("records-reorder-error")).toBeVisible();
    await expect(page.getByTestId("records-live")).toContainText("The new order was not saved");
    expect(await names(page)).toEqual(order);
    await expect(rowNamed(page, order[0]!).getByTestId("record-grip")).toBeFocused();
    expect(await seriousViolations(page)).toEqual([]);
  });
});

test.describe("a chain the native engine cannot run", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("refuses the save, names the step, and the offered rewrite makes it save", async ({ page }) => {
    await open(page, "&fixture=states&manifest=s1", "record-row");
    // Edit lives on the record's panel; the row menu holds the verbs that do not open it.
    await rowNamed(page, "lookahead-provider").getByTestId("record-name").click();
    await page.locator(".pc-side-panel").getByRole("button", { name: "Edit" }).click();
    await page.getByRole("tab", { name: /^Operations/ }).click();
    // Change the chain (turn Sort off), so the save compiles it strictly.
    await page.getByRole("checkbox", { name: /^Enable 2\. / }).uncheck();
    await page.getByRole("button", { name: "Save", exact: true }).click();
    const offer = page.getByTestId("regex-rewrite-offer");
    await expect(offer).toBeVisible();
    await expect(offer).toContainText("^(?!.*(过期|剩余|官网)).*$");
    await expect(offer).toContainText("过期|剩余|官网");
    expect(await seriousViolations(page)).toEqual([]);
    await offer.getByRole("button", { name: "Rewrite step 1" }).click();
    await expect(offer).toContainText("Save again");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByTestId("record-row").first()).toBeVisible();
    await expect(rowNamed(page, "lookahead-provider").getByTestId("record-flagged")).toHaveCount(0);
  });
});
