import { expect, test, type Page } from "@playwright/test";

/**
 * The layers of design 22, driven at the widths a design review uses.
 *
 * Every assertion stands in for a measurement a review takes by hand: the
 * overview fitting one screen, the map lighting one path, the table keeping
 * its columns at 375 with the name pinned, the side panel becoming a sheet,
 * the record page carrying the only tab row. A regression reads as the same
 * sentence the review would write.
 */

async function open(page: Page, query: string, ready: string): Promise<void> {
  await page.goto(`/dev.html${query}`);
  await page.locator(ready).first().waitFor();
}

const docWidth = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth);

test.describe("375", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("the side panel stays a modal sheet on a phone", async ({ page }) => {
    await open(page, "?view=sources", ".layer-row");
    await page.locator("[data-record-open]").first().click();
    await expect(page.locator(".pc-side-panel")).toBeVisible();
    await expect(page.locator(".pc-overlay[data-kind=panel]")).toHaveCSS("pointer-events", "auto");
  });

  test("the overview becomes stage lists and never scrolls sideways", async ({ page }) => {
    await open(page, "", ".lineage-stages");
    await expect(page.locator(".lineage-canvas")).toHaveCount(0);
    await expect(page.locator(".lineage-feeds").first()).toBeVisible();
    expect(await docWidth(page)).toBe(375);
  });

  test("Search and Refresh sit on the title line, not a row each before the content", async ({ page }) => {
    await open(page, "", ".lineage-stages");
    const title = (await page.locator(".ss-header h1").boundingBox())!;
    for (const name of ["Search records and actions (Cmd+K)", "Refresh"]) {
      const box = (await page.locator(".ss-header").getByRole("button", { name }).boundingBox())!;
      expect(Math.abs(box.y + box.height / 2 - (title.y + title.height / 2)), name).toBeLessThan(8);
      expect(box.width, name).toBeLessThan(160);
    }
    expect(await docWidth(page)).toBe(375);
  });

  test("a table keeps its columns and scrolls inside itself, name pinned", async ({ page }) => {
    await open(page, "?view=sources", ".layer-row");
    expect(await docWidth(page)).toBe(375);
    const wrap = page.locator(".pc-table-wrap").first();
    const name = page.locator(".layer-row td.pc-name").first();
    await wrap.evaluate((el) => (el.scrollLeft = 400));
    const box = (await name.boundingBox())!;
    const wrapBox = (await wrap.boundingBox())!;
    expect(Math.round(box.x)).toBeLessThanOrEqual(Math.round(wrapBox.x) + 1);
    await expect(page.locator(".layer-row").first().locator("td", { hasText: /Provider link|Pasted nodes/ })).toHaveCount(1);
  });

  test("the side panel is a full-height sheet", async ({ page }) => {
    await open(page, "?view=sources", ".layer-row");
    await page.locator(".row-open", { hasText: "建材市场" }).click();
    const panel = page.locator(".pc-side-panel");
    await expect(panel).toBeVisible();
    const box = (await panel.boundingBox())!;
    // Full width, and the frame's height less the chassis's inset at the top.
    expect(Math.round(box.width)).toBe(375);
    expect(box.height).toBeGreaterThanOrEqual(812 - 24);
    await expect(panel.getByText("410 GB of 500 GB", { exact: false })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(panel).toHaveCount(0);
  });

  test("the selection bar floats inside the frame and the rows do not move", async ({ page }) => {
    await open(page, "?view=sources", ".layer-row");
    const row = page.locator(".layer-row").nth(1);
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
});

test.describe("1440", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the overview fits one screen and the toolbar is one row", async ({ page }) => {
    await open(page, "", ".lineage-chip");
    expect(await docWidth(page)).toBe(1440);
    const map = (await page.locator(".overview-map").boundingBox())!;
    expect(map.y + map.height).toBeLessThanOrEqual(900);
    const tabs = (await page.locator(".pc-lens-tabs").boundingBox())!;
    const primary = (await page.getByRole("button", { name: "New subscription" }).boundingBox())!;
    expect(Math.abs(primary.y + primary.height / 2 - (tabs.y + tabs.height / 2))).toBeLessThan(4);
    await expect(page.getByText("15 files are not published")).toBeVisible();
  });

  test("the 256-record store is one picture: folded, capped, and drawn as paths", async ({ page }) => {
    await open(page, "?fixture=large", ".lineage-chip");
    const height = await page.evaluate(() => document.documentElement.scrollHeight);
    expect(height).toBeLessThan(1400);
    for (const column of await page.locator("[data-map-col]").all()) {
      expect(await column.locator(".lineage-chip").count()).toBeLessThanOrEqual(8);
    }
    await expect(page.locator(".lineage-note")).toContainText("the attention list names");
    // Selected, only that record's path is drawn.
    await page.locator('[data-map-key^="group:source:"]').first().click();
    await expect(page.locator(".lineage-note")).toContainText("Showing the selected path");
    await expect(page.locator('.lineage-edge[data-on="false"]')).toHaveCount(0);
    expect(await page.locator('.lineage-edge[data-on="true"]').count()).toBeGreaterThan(0);
  });

  test("an edge that skips a column never runs along a chip", async ({ page }) => {
    await open(page, "", ".lineage-chip");
    await expect.poll(() => page.locator(".lineage-edge").count()).toBeGreaterThan(0);
    // Every horizontal run under the combinations column keeps clear of its chips.
    const clearance = await page.evaluate(() => {
      const canvas = document.querySelector(".lineage-canvas")!.getBoundingClientRect();
      const column = document.querySelectorAll("[data-map-col]")[1]!;
      const chips = [...column.querySelectorAll("[data-map-key]")].map((chip) => chip.getBoundingClientRect());
      const left = column.getBoundingClientRect().left - canvas.left;
      const right = column.getBoundingClientRect().right - canvas.left;
      let worst = Number.POSITIVE_INFINITY;
      let crossing = 0;
      for (const path of document.querySelectorAll<SVGPathElement>(".lineage-edge")) {
        const length = path.getTotalLength();
        // Only an edge that passes the column: it starts left of it and ends right of it.
        if (!(path.getPointAtLength(0).x < left && path.getPointAtLength(length).x > right)) continue;
        crossing += 1;
        for (let at = 0; at <= length; at += 2) {
          const point = path.getPointAtLength(at);
          if (point.x < left || point.x > right) continue;
          for (const chip of chips) {
            const top = chip.top - canvas.top;
            const bottom = chip.bottom - canvas.top;
            const gap = point.y < top ? top - point.y : point.y > bottom ? point.y - bottom : 0;
            worst = Math.min(worst, gap);
          }
        }
      }
      return { worst, crossing };
    });
    // Production has sources rendered straight into files, so some edge does cross.
    expect(clearance.crossing).toBeGreaterThan(0);
    expect(clearance.worst).toBeGreaterThanOrEqual(16);
  });

  test("selecting a chip lights its path, dims the rest and opens the peek", async ({ page }) => {
    await open(page, "", ".lineage-chip");
    await page.locator('[data-map-key="imported-openjobs-host"]').click();
    await expect(page.locator('[data-map-key="imported-openjobs-host"]')).toHaveAttribute("data-state", "selected");
    await expect(page.locator('[data-map-key="imported-col-merge-openjobs"]')).toHaveAttribute("data-state", "on");
    await expect(page.locator('[data-map-key="imported-cdcd-self-hostbak-20260820"]')).toHaveAttribute("data-state", "off");
    expect(await page.locator('.lineage-edge[data-on="true"]').count()).toBeGreaterThan(1);
    await expect(page.locator(".pc-side-panel h2")).toHaveText("openjobs-host");
    await expect(page).toHaveURL(/[?&]open=imported-openjobs-host(&|#|$)/);
  });

  test("a combination's peek links on to its members and the page", async ({ page }) => {
    await open(page, "?view=combinations", ".layer-row");
    await page.locator(".layer-row", { hasText: "merge-openjobs" }).locator("td").nth(3).click();
    const panel = page.locator(".pc-side-panel");
    await panel.getByRole("button", { name: "openjobs-host" }).first().click();
    await expect(panel.locator("h2")).toHaveText("openjobs-host");
    await panel.getByRole("button", { name: "Open page" }).click();
    await expect(page.locator("#record-title")).toHaveText("openjobs-host");
    await expect(page.getByRole("tablist")).toHaveCount(1);
    await expect(page).toHaveURL(/[?&]record=imported-openjobs-host(&|#|$)/);
  });

  test("the record page masks a provider link and reveals it for a minute", async ({ page }) => {
    await open(page, "?record=imported-unnamed", "#record-title");
    await page.getByRole("tab", { name: "Source" }).click();
    const link = page.locator(".record-url code");
    await expect(link).toHaveText("https://vip.ding202507.xyz/…?…");
    await page.getByRole("button", { name: "Reveal for 60s" }).click();
    await expect(link).toContainText("token=");
    await page.getByRole("button", { name: /Sources/ }).click();
    await expect(page.locator(".layer-row").first()).toBeVisible();
  });

  test("a long name ellipses instead of shoving columns off the row", async ({ page }) => {
    await open(page, "?fixture=canned&view=sources", ".layer-row");
    const row = page.locator(".layer-row", { hasText: "A deliberately long" }).first();
    const name = (await row.locator(".row-open strong").boundingBox())!;
    const cell = (await row.locator("td.pc-name").boundingBox())!;
    expect(name.width).toBeLessThanOrEqual(cell.width);
    expect(await docWidth(page)).toBe(1440);
  });

  test("the files table keeps one line per row", async ({ page }) => {
    await open(page, "?view=files", ".layer-row");
    const box = (await page.locator(".layer-row").first().boundingBox())!;
    expect(box.height).toBeLessThanOrEqual(48);
    expect(await docWidth(page)).toBe(1440);
  });

  test("Show them on the overview lands on the unpublished files, each with its Publish", async ({ page }) => {
    await open(page, "", ".attention-item");
    await page.locator(".attention-item", { hasText: "not published" }).getByRole("button", { name: "Show them" }).click();
    await expect(page.locator(".layer-row")).toHaveCount(15);
    await expect(page).toHaveURL(/[?&]published=no(&|#|$)/);
    // On this view each row keeps a Publish… named after its file, which asks
    // the console for its share form on that file.
    await page.getByRole("button", { name: "Publish for-openjobs-loon…", exact: true }).click();
    // A posted message lands on a later task, so the log is polled.
    await expect
      .poll(() => page.evaluate(() => ((window as unknown as { __navigations?: string[] }).__navigations ?? []).at(-1)))
      .toBe("/platform/publishing?origin=share&create=1&for=imported-file-for-openjobs-loon");
  });
});

test.describe("publishing and deleting files", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the Published column is a state and Publish… leads each file's menu", async ({ page }) => {
    await open(page, "?view=files", ".layer-row");
    await expect(page.locator(".row-publish")).toHaveCount(0);
    await expect(page.locator(".layer-row", { hasText: "for-openjobs-loon" }).getByText("not published")).toBeVisible();
    await page.locator('[data-row-menu="imported-file-for-openjobs-loon"] button').first().click();
    const items = page.locator(".rec-menu [role=menuitem]");
    await expect(items.first()).toHaveText("Publish…");
    await items.first().click();
    await expect
      .poll(() => page.evaluate(() => ((window as unknown as { __navigations?: string[] }).__navigations ?? []).at(-1)))
      .toBe("/platform/publishing?origin=share&create=1&for=imported-file-for-openjobs-loon");
  });

  test("the bulk bar hands one file to the share form and says why not more", async ({ page }) => {
    await open(page, "?view=files&published=no", ".layer-row");
    const boxes = page.locator(".layer-row input[type=checkbox]");
    await boxes.nth(0).check();
    await expect(page.locator(".pc-batch-bar").getByRole("button", { name: /^Publish .+…$/ })).toBeVisible();
    await boxes.nth(1).check();
    await expect(page.locator(".pc-batch-bar")).toContainText("Publish one file at a time");
    await expect(page.locator(".pc-batch-bar").getByRole("button", { name: /^Publish / })).toHaveCount(0);
  });
});

/**
 * The page's state lives in the console's address, which the harness plays
 * with its own address bar (dev/consoleAddress.ts): the fake console hands the
 * query over at the handshake and writes every state message back into it.
 * A reload of this tab is therefore the console reload an operator does.
 */
test.describe("page state in the console address", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("a reload lands on the same layer, filter, search and peek", async ({ page }) => {
    await open(page, "?fixture=production", ".attention-item");
    await page.locator(".attention-item", { hasText: "not published" }).getByRole("button", { name: "Show them" }).click();
    await expect(page.locator(".layer-row")).toHaveCount(15);
    await page.getByRole("searchbox", { name: "Filter files" }).fill("loon");
    await expect(page.locator(".layer-row")).toHaveCount(3);
    await page.locator(".layer-row", { hasText: "for-openjobs-loon" }).locator(".row-open").click();
    await expect(page.locator(".pc-side-panel h2")).toHaveText("for-openjobs-loon");
    await expect(page).toHaveURL(/[?&]open=imported-file-for-openjobs-loon(&|#|$)/);
    const url = new URL(page.url());
    expect(Object.fromEntries(url.searchParams)).toEqual({
      fixture: "production",
      view: "files",
      open: "imported-file-for-openjobs-loon",
      q: "loon",
      published: "no",
    });

    await page.reload();
    await expect(page.locator(".pc-side-panel h2")).toHaveText("for-openjobs-loon");
    await expect(page.getByRole("tab", { name: /Files/ })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByRole("searchbox", { name: "Filter files" })).toHaveValue("loon");
    await expect(page.locator(".layer-row")).toHaveCount(3);
    // The reload did not rewrite the address it landed on.
    expect(new URL(page.url()).search).toBe(url.search);
  });

  test("a link to a record inside a folded group opens the group, though the store arrives after the link", async ({ page }) => {
    // The address sets the selection at the handshake; the store is read
    // after. src-06 sits in pasted-uk-*, which opens as it does for a
    // selection made on the page, so its neighbour src-14 is drawn too.
    const group = page.locator('[data-map-key="group:source:pasted-uk"]');
    for (const step of ["load", "reload"]) {
      if (step === "load") await open(page, "?fixture=large&open=src-06", ".lineage-chip");
      else await page.reload();
      await expect(group, step).toHaveAttribute("aria-expanded", "true");
      await expect(page.locator('[data-map-key="src-06"]'), step).toBeVisible();
      await expect(page.locator('[data-map-key="src-14"]'), step).toBeVisible();
      await expect(page.locator(".lineage-note"), step).toContainText("Showing the selected path");
    }
  });

  test("a record the attention list names is drawn itself, and its group stays folded", async ({ page }) => {
    await open(page, "?fixture=large", ".lineage-chip");
    // src-01's last refresh failed; it sits in the folded provider group.
    await expect(page.locator('[data-map-key="src-01"]')).toBeVisible();
    await expect(page.locator('[data-map-key="src-04"]')).toHaveCount(0);
    // Opening a column's "N more" keeps the map drawn as paths.
    await page.locator('[data-map-key^="more:"]').first().click();
    await expect(page.locator(".lineage-note")).toContainText("the attention list names");
  });

  test("from 768px the rows stay live beside the side panel: a click swaps the record, Escape returns to its row", async ({ page }) => {
    await open(page, "?view=sources", ".layer-row");
    const rows = page.locator("[data-record-open]");
    const first = (await rows.nth(0).getAttribute("data-record-open"))!;
    const third = (await rows.nth(2).getAttribute("data-record-open"))!;
    await rows.nth(0).click();
    const title = page.locator(".pc-side-panel h2");
    const firstName = (await title.textContent())!;
    await expect(page.locator(".pc-overlay[data-kind=panel]")).toHaveCSS("pointer-events", "none");
    await rows.nth(2).click();
    await expect(title).not.toHaveText(firstName);
    await expect(page).toHaveURL(new RegExp(`[?&]open=${third}(&|#|$)`));
    await page.keyboard.press("Escape");
    await expect(page.locator(".pc-side-panel")).toHaveCount(0);
    await expect(page.locator(`[data-record-open="${third}"]`)).toBeFocused();
    expect(first).not.toBe(third);
  });

  test("a reload lands on the same record page, and back still goes where it came from", async ({ page }) => {
    await open(page, "?view=combinations", ".layer-row");
    await page.locator(".layer-row", { hasText: "merge-openjobs" }).locator("td").nth(3).click();
    await page.locator(".pc-side-panel").getByRole("button", { name: "Open page" }).click();
    await expect(page.locator("#record-title")).toHaveText("merge-openjobs");
    await expect(page).toHaveURL(/[?&]record=imported-col-merge-openjobs(&|#|$)/);
    await expect(page).toHaveURL(/[?&]view=combinations(&|#|$)/);
    await expect(page).not.toHaveURL(/[?&]open=/);

    await page.reload();
    await expect(page.locator("#record-title")).toHaveText("merge-openjobs");
    await page.locator(".record-crumbs").getByRole("button", { name: "Combinations" }).click();
    await expect(page.locator(".layer-row").first()).toBeVisible();
    await expect(page).toHaveURL(/\?view=combinations(#|$)/);
  });
});

for (const width of [1440, 375]) {
  test.describe(`layer row at ${width}`, () => {
    test.use({ viewport: { width, height: 900 } });

    test("the layer row never scrolls up and down, and the selected underline meets the hairline", async ({ page }) => {
      await open(page, "?fixture=production", ".ss-layer-tabs");
      const row = await page.evaluate(() => {
        const tabs = document.querySelector<HTMLElement>(".ss-layer-tabs")!;
        const bar = document.querySelector<HTMLElement>(".ss-layer-bar")!.getBoundingClientRect();
        const selected = tabs.querySelector<HTMLElement>('[aria-selected="true"]')!.getBoundingClientRect();
        return { overflow: tabs.scrollHeight - tabs.clientHeight, gap: bar.bottom - selected.bottom };
      });
      expect(row.overflow).toBe(0);
      if (width > 620) expect(Math.abs(row.gap)).toBeLessThan(0.5);
    });
  });
}

test.describe("reduced motion", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("nothing on the map or in the sheet animates", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await open(page, "", ".lineage-chip");
    expect(await page.evaluate(() => matchMedia("(prefers-reduced-motion: reduce)").matches)).toBe(true);
    await page.locator('[data-map-key="imported-openjobs-host"]').click();
    await page.goto("/dev.html?view=sources");
    await page.locator('[data-row-menu="imported-openjobs-host"] button').click();
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
    await open(page, "?view=sources", ".layer-row");
    const id = await page.locator(".layer-row").first().getAttribute("id");
    const recordId = id!.replace(/^rec-/, "");
    await page.locator(`[data-row-menu="${recordId}"] button`).first().click();
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
    const trigger = (await page.locator(`[data-row-menu="${recordId}"] button`).first().boundingBox())!;
    const menu = (await page.locator(".rec-menu").boundingBox())!;
    expect(Math.abs(menu.x + menu.width - (trigger.x + trigger.width))).toBeLessThanOrEqual(1);
    expect(menu.y).toBeGreaterThanOrEqual(trigger.y + trigger.height);
    // Opening the menu must not also open the side panel behind it.
    await expect(page.locator(".pc-side-panel")).toHaveCount(0);
  });
});
