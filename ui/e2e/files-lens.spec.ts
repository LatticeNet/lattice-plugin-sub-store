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

/**
 * The contrast of an element's text against the first opaque background
 * behind it, with every opacity on the way multiplied in. Colours go through
 * a canvas so oklch tokens come back as sRGB.
 */
function contrastOf(page: Page, selector: string): Promise<number> {
  return page.evaluate((sel) => {
    const el = document.querySelector<HTMLElement>(sel)!;
    const ctx = document.createElement("canvas").getContext("2d", { willReadFrequently: true })!;
    const rgba = (css: string): [number, number, number, number] => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = "#000";
      ctx.fillStyle = css;
      ctx.fillRect(0, 0, 1, 1);
      const [r, g, b, a] = ctx.getImageData(0, 0, 1, 1).data;
      return [r!, g!, b!, a! / 255];
    };
    let opacity = 1;
    let bg: [number, number, number, number] | null = null;
    for (let node: HTMLElement | null = el; node; node = node.parentElement) {
      const style = getComputedStyle(node);
      opacity *= Number(style.opacity);
      const fill = rgba(style.backgroundColor);
      if (!bg && fill[3] > 0.99) bg = fill;
    }
    const back = bg ?? [255, 255, 255, 1];
    const ink = rgba(getComputedStyle(el).color);
    const alpha = ink[3] * opacity;
    const seen = ink.slice(0, 3).map((c, i) => c * alpha + back[i]! * (1 - alpha));
    const lum = (rgb: number[]) => {
      const [r, g, b] = rgb.map((v) => {
        const c = v / 255;
        return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
    };
    const [hi, lo] = [lum(seen), lum(back.slice(0, 3))].sort((a, b) => b - a);
    return (hi! + 0.05) / (lo! + 0.05);
  }, selector);
}

test.describe("375", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("the selected layer tab is scrolled into view, on load and on change", async ({ page }) => {
    const inView = () => page.evaluate(() => {
      const row = document.querySelector(".ss-layer-tabs")!.getBoundingClientRect();
      const tab = document.querySelector('.ss-layer-tabs [aria-selected="true"]')!.getBoundingClientRect();
      return tab.left >= row.left && tab.right <= row.right;
    });
    for (const view of ["files", "shares", "settings"]) {
      await open(page, `?view=${view}`, ".ss-layer-tabs");
      // The counts land after the layer is applied and widen every tab, so
      // the tab has to be in view once they are all there, not only before.
      await expect(page.locator(".ss-layer-tabs .pc-count"), view).toHaveCount(4);
      await expect.poll(inView, view).toBe(true);
    }
    await page.getByRole("tab", { name: /Overview/ }).click();
    await expect.poll(inView, "back to overview").toBe(true);
    expect(await docWidth(page)).toBe(375);
  });

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

  test("the overview fits one screen, and the primary action sits in the header after Refresh", async ({ page }) => {
    await open(page, "", ".lineage-chip");
    expect(await docWidth(page)).toBe(1440);
    const map = (await page.locator(".overview-map").boundingBox())!;
    expect(map.y + map.height).toBeLessThanOrEqual(900);
    // As vpn-core places its own: in the header, on Refresh's line, to its right.
    const refresh = (await page.locator(".ss-header").getByRole("button", { name: "Refresh" }).boundingBox())!;
    const primary = (await page.locator(".ss-header").getByRole("button", { name: "New subscription" }).boundingBox())!;
    expect(Math.abs(primary.y + primary.height / 2 - (refresh.y + refresh.height / 2))).toBeLessThan(2);
    expect(primary.x).toBeGreaterThan(refresh.x + refresh.width);
    // The tab row holds the layers and nothing else.
    await expect(page.locator(".ss-layer-bar").getByRole("button", { name: /New / })).toHaveCount(0);
    await expect(page.getByText("15 files are not published")).toBeVisible();
  });

  test("each layer's own create action takes the header's place", async ({ page }) => {
    await open(page, "?view=sources", ".layer-row");
    const header = page.locator(".ss-header");
    for (const [tab, name] of [["Sources", "New subscription"], ["Combinations", "New combination"], ["Files", "New file"], ["Shares", "Open in Publishing"]] as const) {
      await page.getByRole("tab", { name: new RegExp(`^${tab}`) }).click();
      await expect(header.locator(".ss-head-primary"), tab).toHaveCount(1);
      await expect(header.locator(".ss-head-primary"), tab).toContainText(name);
    }
    await page.getByRole("tab", { name: /^Settings/ }).click();
    await expect(header.locator(".ss-head-primary")).toHaveCount(0);
  });

  test("an empty store leaves create to the empty state, and the header does not repeat it", async ({ page }) => {
    for (const view of ["", "?view=sources", "?view=combinations", "?view=files"]) {
      await open(page, `${view ? `${view}&` : "?"}state=empty`, ".pc-empty");
      await expect(page.locator(".ss-header .ss-head-primary"), view || "overview").toHaveCount(0);
    }
    await open(page, "?state=empty", ".pc-empty");
    await expect(page.getByRole("button", { name: "Go to Sources" })).toBeVisible();
  });

  test("with the record catalogue unread, create stays in place and is disabled with the reason", async ({ page }) => {
    await open(page, "?state=error", ".ss-header .ss-head-primary");
    const header = page.locator(".ss-header");
    await expect(header.getByRole("button", { name: "New subscription" })).toBeDisabled();
    await expect(header.getByRole("button", { name: "More things to create" })).toBeDisabled();
    await expect(header.getByRole("button", { name: "New subscription" })).toHaveAttribute("title", /could not be read.*Refresh first/);
    await page.getByRole("tab", { name: /^Files/ }).click();
    await expect(header.getByRole("button", { name: "New file" })).toBeDisabled();
    // The palette's create commands carry the same reason, written out.
    await header.getByRole("button", { name: "Search records and actions (Cmd+K)" }).click();
    await page.locator(".palette-input").getByRole("combobox").fill("new");
    await expect(page.getByRole("option", { name: /New file/ })).toHaveAttribute("aria-disabled", "true");
    await expect(page.getByRole("option", { name: /New file/ })).toContainText("could not be read");
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

  for (const theme of ["light", "dark"]) {
    test(`off the selected path a chip's label keeps its ink and only the dot dims (${theme})`, async ({ page }) => {
      await open(page, `?theme=${theme}`, ".lineage-chip");
      await page.locator('[data-map-key="imported-openjobs-host"]').click();
      const off = '[data-map-key="imported-cdcd-self-hostbak-20260820"]';
      await expect(page.locator(off)).toHaveAttribute("data-state", "off");
      await expect(page.locator(off)).toHaveCSS("opacity", "1");
      await expect(page.locator(`${off} .lineage-dot`)).toHaveCSS("opacity", "0.38");
      expect(await contrastOf(page, `${off} .lineage-name`)).toBeGreaterThanOrEqual(4.5);
    });
  }

  test("at rest, strands to different files rise in different places, and strands to one file share one", async ({ page }) => {
    await open(page, "", ".lineage-chip");
    await expect.poll(() => page.locator(".lineage-edge").count()).toBeGreaterThan(0);
    const risers = await page.evaluate(() => {
      const canvas = document.querySelector(".lineage-canvas")!.getBoundingClientRect();
      const cols = [...document.querySelectorAll("[data-map-col]")].map((col) => col.getBoundingClientRect());
      const gapLeft = cols[1]!.right - canvas.left;
      const gapRight = cols[2]!.left - canvas.left;
      const out: Array<{ end: number; x: number }> = [];
      for (const path of document.querySelectorAll<SVGPathElement>(".lineage-edge")) {
        const length = path.getTotalLength();
        const start = path.getPointAtLength(0);
        const end = path.getPointAtLength(length);
        // Only a strand from a source that skips the combinations column into a file.
        if (!(start.x < cols[1]!.left - canvas.left && end.x >= gapRight - 2 && end.x < cols[2]!.right - canvas.left)) continue;
        // The riser: the rightmost point inside the gap before the files that is
        // away from the final run and its corner (the lane enters the gap from
        // the left and turns up or down there).
        let x = Number.NEGATIVE_INFINITY;
        for (let at = 0; at <= length; at += 1) {
          const point = path.getPointAtLength(at);
          if (point.x > gapLeft && point.x < gapRight && Math.abs(point.y - end.y) > 12) x = Math.max(x, point.x);
        }
        if (Number.isFinite(x)) out.push({ end: Math.round(end.y), x: Math.round(x * 10) / 10 });
      }
      return out;
    });
    const byTarget = new Map<number, Set<number>>();
    for (const riser of risers) byTarget.set(riser.end, (byTarget.get(riser.end) ?? new Set()).add(riser.x));
    // Production has three sources rendered straight into files, so there are several targets.
    expect(byTarget.size).toBeGreaterThan(1);
    for (const [end, xs] of byTarget) expect(xs.size, `target at y ${end}`).toBe(1);
    const xs = [...byTarget.values()].map((set) => [...set][0]!).sort((a, b) => a - b);
    for (let i = 1; i < xs.length; i += 1) expect(xs[i]! - xs[i - 1]!).toBeGreaterThanOrEqual(3);
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

test.describe("the large store's files, fifty a page", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  const footer = (page: Page) => page.locator(".pc-pagination");

  test("pages by fifty, lands on the top of the next page, and a filter starts again on page 1", async ({ page }) => {
    await open(page, "?view=files&fixture=large", ".layer-row");
    await expect(page.locator(".layer-row")).toHaveCount(50);
    await expect(footer(page)).toContainText("Files 1 to 50 of 180");
    await expect(footer(page)).toContainText("Page 1 of 4");
    await footer(page).getByRole("button", { name: "Next" }).click();
    await expect(footer(page)).toContainText("Files 51 to 100 of 180");
    // Next sat under the last row; the new page shows from its top.
    const top = await page.locator(".rec-list").evaluate((el) => el.getBoundingClientRect().top);
    expect(top).toBeGreaterThanOrEqual(0);
    expect(top).toBeLessThan(900);
    // Select all takes the rows on screen, and the bar counts those.
    await page.getByRole("checkbox", { name: "Select all 50 shown files" }).check();
    await expect(page.locator(".pc-batch-bar")).toContainText("50");
    await page.locator(".pc-batch-bar").getByRole("button", { name: "Clear" }).click();
    await page.getByRole("searchbox", { name: "Filter files" }).fill("alice");
    await expect(page.locator(".layer-row")).toHaveCount(15);
    await expect(footer(page)).toHaveCount(0);
  });

  test("a link to a file on a later page turns to that page", async ({ page }) => {
    await open(page, "?view=files&fixture=large&open=file-ivan-loon", ".layer-row");
    await expect(page.locator("#rec-file-ivan-loon")).toBeVisible();
    await expect(footer(page)).not.toContainText("Page 1 of");
    await expect(page.locator(".pc-side-panel h2")).toHaveText("for-ivan-loon");
  });
});

test.describe("touch at 375", () => {
  test.use({ viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true });

  test("search, Refresh, the primary and its chevron, a row's Publish and its menu are 44px targets", async ({ page }) => {
    await open(page, "", ".ss-header .ss-head-primary");
    const header = page.locator(".ss-header");
    const targets = [
      header.getByRole("button", { name: "Search records and actions (Cmd+K)" }),
      header.getByRole("button", { name: "Refresh" }),
      header.getByRole("button", { name: "New subscription" }),
      header.getByRole("button", { name: "More things to create" }),
    ];
    for (const target of targets) {
      const box = (await target.boundingBox())!;
      expect(Math.min(box.width, box.height), await target.getAttribute("aria-label") ?? await target.innerText()).toBeGreaterThanOrEqual(44);
    }
    // Search and Refresh stay on the title line; the primary takes the row under the description.
    const title = (await header.locator("h1").boundingBox())!;
    const search = (await targets[0]!.boundingBox())!;
    expect(Math.abs(search.y + search.height / 2 - (title.y + title.height / 2))).toBeLessThan(8);
    const description = (await header.locator(".pc-title-copy > p").boundingBox())!;
    expect((await targets[2]!.boundingBox())!.y).toBeGreaterThan(description.y + description.height - 1);
    expect(await docWidth(page)).toBe(375);

    await open(page, "?view=files&published=no", ".layer-row");
    const row = page.locator(".layer-row").first();
    for (const target of [row.locator(".row-publish"), row.locator("[data-row-menu] button").first()]) {
      const box = (await target.boundingBox())!;
      expect(Math.min(box.width, box.height)).toBeGreaterThanOrEqual(44);
    }
    await row.locator("[data-row-menu] button").first().click();
    // The menu scales in; measure it where it comes to rest.
    await page.locator(".rec-menu").evaluate((el) => Promise.all(el.getAnimations().map((a) => a.finished)));
    for (const item of await page.locator(".rec-menu [role=menuitem]").all()) {
      expect((await item.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    }
  });

  test("the files footer puts the range on its own line, with 44px Previous and Next under it", async ({ page }) => {
    await open(page, "?view=files&fixture=large", ".pc-pagination");
    const footer = page.locator(".pc-pagination");
    const range = (await footer.locator("> span").first().boundingBox())!;
    const previous = (await footer.getByRole("button", { name: "Previous" }).boundingBox())!;
    const next = (await footer.getByRole("button", { name: "Next" }).boundingBox())!;
    expect(previous.y).toBeGreaterThan(range.y + range.height - 1);
    expect(Math.abs(next.y - previous.y)).toBeLessThan(1);
    for (const box of [previous, next]) expect(Math.min(box.width, box.height)).toBeGreaterThanOrEqual(44);
    expect(await docWidth(page)).toBe(375);
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

  test("deleting a file a live share serves names the share and asks for the file's name", async ({ page }) => {
    await open(page, "?view=files", ".layer-row");
    await page.locator('[data-row-menu="imported-file-for-cdcd-loon"] button').first().click();
    await page.locator(".rec-menu [role=menuitem]", { hasText: "Delete" }).click();
    const dialog = page.getByRole("alertdialog");
    await expect(dialog).toContainText("/cdcd stops serving: it publishes for-cdcd-loon");
    const confirm = dialog.getByRole("button", { name: "Delete", exact: true });
    await expect(confirm).toBeDisabled();
    await dialog.getByRole("textbox").fill("for-cdcd-loo");
    await expect(confirm).toBeDisabled();
    await dialog.getByRole("textbox").fill("for-cdcd-loon");
    await expect(confirm).toBeEnabled();
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
    // Its folded group says how many more are inside than the ones drawn under it.
    const group = page.locator('[data-map-key^="group:source:"][aria-expanded="false"]', { has: page.locator('text=/more inside$/') }).first();
    await expect(group.locator(".lineage-figure")).toHaveText(/^\d+ more inside$/);
    // The folded rows count with a noun, and the files row counts families as well as files.
    await expect(page.locator('[data-map-key="more:file"] .lineage-name')).toHaveText(/^\d+ more famil(y|ies)(?: and \d+ files?)?, \d+ files$/);
    for (const more of await page.locator('[data-map-key^="more:"]').all()) {
      await expect(more).toHaveAccessibleName(/^\d+ more [a-z]/);
    }
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
