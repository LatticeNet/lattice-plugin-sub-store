import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Locator, type Page } from "@playwright/test";

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

/** The content is wider than the box: cut by an ellipsis, a clip, or a scroller. */
const widerThanBox = (locator: Locator) => locator.evaluate((el) => el.scrollWidth > el.clientWidth + 1);

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

test.describe("at 1440 every column keeps what it says", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the name keeps its room, and the flag, the tag and each cell's facts stay whole", async ({ page }) => {
    await open(page, "&fixture=states&manifest=s1", "record-row");
    const table = page.getByTestId("records-table");
    expect((await table.locator("th.pc-name").boundingBox())!.width).toBeGreaterThanOrEqual(360);
    // A short flagged name is not cut for the marker beside it, and the marker
    // follows the name rather than the cell's far edge.
    for (const name of ["lookahead-provider", "backreference-rename"]) {
      const open = rowNamed(page, name).getByTestId("record-name");
      expect(await widerThanBox(open.locator("strong")), name).toBe(false);
      const nameBox = (await open.boundingBox())!;
      const flag = (await rowNamed(page, name).getByTestId("record-flagged").boundingBox())!;
      expect(flag.x - (nameBox.x + nameBox.width), name).toBeLessThan(16);
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
    await expect(gone.getByTestId("record-missing")).toHaveText("source gone");
    expect(await showsPrefix(rowNamed(page, "for-cdcd-egern").locator(".rec-kind-sub"), "from merge-cd-openjobs")).toBe(true);

    // Expiry and traffic: the date whole, the figure inside the cell.
    const provider = rowNamed(page, "建材市场").locator("td[data-expiry]");
    expect(await widerThanBox(provider.locator(".layer-expiry"))).toBe(false);
    await expect(provider.locator(".usage-figure")).toHaveText("82%");
    const figure = (await provider.locator(".usage-figure").boundingBox())!;
    const expiryCell = (await provider.boundingBox())!;
    expect(figure.x + figure.width).toBeLessThanOrEqual(expiryCell.x + expiryCell.width);

    // Steps: the turned-off count on its own line, whole, and in words in the title.
    const steps = longRow.getByTestId("record-steps");
    await expect(steps).toHaveAttribute("title", "2 steps, 1 turned off.");
    await expect(steps.locator("small")).toHaveText("1 off");
    expect(await widerThanBox(steps.locator("small"))).toBe(false);
    const off = (await steps.locator("small").boundingBox())!;
    const stepsCell = (await steps.boundingBox())!;
    expect(off.x + off.width, "the second line ends inside its cell").toBeLessThanOrEqual(stepsCell.x + stepsCell.width);
  });

  test("on a wider frame a long name takes the room it has", async ({ page }) => {
    await page.setViewportSize({ width: 1920, height: 1000 });
    await open(page, "&fixture=states&manifest=s1", "record-row");
    const long = rowNamed(page, LONG).getByTestId("record-name").locator("strong");
    expect((await long.boundingBox())!.width).toBeGreaterThan(380);
  });

  test("on a legacy store the counting word fits the node columns", async ({ page }) => {
    await open(page, "&store=legacy&manifest=s1", "records-migrate");
    // Read in the frame the word is drawn: the previews behind it finish within a second.
    const counting = await page.waitForFunction(() => {
      const cells = [...document.querySelectorAll('[data-testid="record-nodes-in"] .pc-td-body')];
      const words = cells.filter((cell) => cell.textContent?.trim() === "counting");
      return words.length ? words.map((cell) => cell.scrollWidth > cell.clientWidth + 1) : null;
    });
    expect(await counting.jsonValue()).not.toContain(true);
  });
});

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
    // Nor does the row menu offer the moves.
    await page.getByTestId("record-row").first().getByRole("button", { name: /^Actions for / }).click();
    await expect(page.getByRole("menu").getByRole("menuitem").first()).toBeVisible();
    await expect(page.getByRole("menu").getByRole("menuitem", { name: "Move up" })).toHaveCount(0);
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
    await expect(page.getByTestId("records-live")).toContainText(`Moved ${first} to position`);
    expect(Number(await positionOf(page, first).innerText())).toBeGreaterThan(onScreen);
  });

  test("the row menu moves a record to either end and one step, from a tap, announced", async ({ page }) => {
    await open(page, "&manifest=s1", "record-grip");
    const first = (await names(page))[0]!;
    const live = page.getByTestId("records-live");
    const menu = page.getByRole("menu");
    const trigger = () => rowNamed(page, first).getByRole("button", { name: `Actions for ${first}` });

    await trigger().tap();
    // At the top there is nowhere up to go.
    await expect(menu.getByRole("menuitem", { name: "Move up" })).toBeDisabled();
    await expect(menu.getByRole("menuitem", { name: "Move to top" })).toBeDisabled();
    await menu.getByRole("menuitem", { name: "Move to bottom" }).tap();
    await expect(live).toHaveText(`Moved ${first} to position 23 of 23.`);
    expect((await names(page)).at(-1)).toBe(first);
    await expect(trigger()).toBeFocused();

    await trigger().tap();
    await expect(menu.getByRole("menuitem", { name: "Move to bottom" })).toBeDisabled();
    await menu.getByRole("menuitem", { name: "Move up" }).tap();
    await expect(live).toHaveText(`Moved ${first} to position 22 of 23.`);

    await trigger().tap();
    await menu.getByRole("menuitem", { name: "Move to top" }).tap();
    await expect(live).toHaveText(`Moved ${first} to position 1 of 23.`);
    expect((await names(page))[0]).toBe(first);
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
    // The notice and the message beside Save follow the chain, not the refusal.
    await expect(offer).toContainText("Every pattern in the chain runs natively now");
    await expect(offer).not.toContainText("cannot run");
    await expect(page.locator(".editor-actions")).not.toContainText("Not saved");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByTestId("record-row").first()).toBeVisible();
    await expect(rowNamed(page, "lookahead-provider").getByTestId("record-flagged")).toHaveCount(0);
  });

  test("a flagged record says so in its panel and on its page, and its editor offers the rewrite before any save", async ({ page }) => {
    await open(page, "&fixture=states&manifest=s1", "record-row");
    await rowNamed(page, "lookahead-provider").getByTestId("record-name").click();
    const panel = page.locator(".pc-side-panel");
    await expect(panel.getByTestId("record-flagged")).toBeVisible();
    await panel.getByRole("button", { name: "Open page" }).click();
    const head = page.locator(".record-head");
    await expect(head.getByTestId("record-flagged")).toBeVisible();
    await head.getByRole("button", { name: "Edit" }).click();
    // Nothing changed and nothing saved: the step and its rewrite are named at once.
    const offer = page.getByTestId("regex-rewrite-offer");
    await expect(offer).toBeVisible();
    await expect(offer).toContainText("Step 1");
    await expect(offer).toContainText("^(?!.*(过期|剩余|官网)).*$");
    expect(await seriousViolations(page)).toEqual([]);
    await offer.getByRole("button", { name: "Rewrite step 1" }).click();
    await expect(offer).toContainText("Every pattern in the chain runs natively now");
    await expect(offer).toContainText("Save to store it");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(rowNamed(page, "lookahead-provider")).toBeVisible();
    await expect(rowNamed(page, "lookahead-provider").getByTestId("record-flagged")).toHaveCount(0);
  });
});
