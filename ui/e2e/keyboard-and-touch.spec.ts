import { expect, test, type Page } from "@playwright/test";

/**
 * Where focus goes, and how big a thumb's target is: the 2026-10-01 design
 * review drove these by hand and found focus on <body> after most overlays
 * closed, Tab escaping the confirm dialog, and touch targets of 28 to 30px.
 * Each test is one of its findings, stated the way the review stated it.
 */

async function open(page: Page, query: string, ready: string): Promise<void> {
  await page.goto(`/dev.html${query}`);
  await page.locator(ready).first().waitFor();
}

/** What has focus, by its accessible name or text. */
const focused = (page: Page) =>
  page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    if (!el || el === document.body) return "BODY";
    return (el.getAttribute("aria-label") || el.textContent || el.tagName).trim().replace(/\s+/g, " ");
  });

test.describe("focus at 1440", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the confirm dialog keeps Tab inside, and Escape and Cancel give focus back to the menu trigger", async ({ page }) => {
    await open(page, "?view=files", ".layer-row");
    const trigger = page.getByRole("button", { name: "Actions for for-cdcd-egern", exact: true });
    await trigger.focus();
    await page.keyboard.press("Enter");
    await page.keyboard.press("End");
    await expect.poll(() => focused(page)).toBe("Delete");
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("alertdialog");
    await expect(dialog).toBeFocused();
    for (let i = 0; i < 5; i += 1) {
      await page.keyboard.press("Tab");
      expect(await dialog.evaluate((el) => el.contains(document.activeElement)), `Tab ${i + 1}`).toBe(true);
    }
    await page.keyboard.press("Shift+Tab");
    expect(await dialog.evaluate((el) => el.contains(document.activeElement))).toBe(true);
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(trigger).toBeFocused();

    await page.keyboard.press("Enter");
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(trigger).toBeFocused();
  });

  test("a confirmed delete moves focus to the row above, and names the share it left serving nothing", async ({ page }) => {
    await open(page, "?view=files", ".layer-row");
    await page.getByRole("button", { name: "Actions for for-cdcd-loon", exact: true }).click();
    await page.getByRole("menuitem", { name: /Delete/ }).click();
    const dialog = page.getByRole("alertdialog");
    await dialog.locator("input").fill("for-cdcd-loon");
    await dialog.getByRole("button", { name: "Delete" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.locator('[data-record-open="imported-file-for-cdcd-egern"]')).toBeFocused();
    const notice = page.locator(".pc-notice", { hasText: "Deleted for-cdcd-loon" });
    await expect(notice).toContainText("/cdcd still exists and now serves nothing");
    await expect(notice.getByRole("button", { name: "Open in Publishing" })).toBeVisible();

    // The share stays in the console: Shares says it serves nothing, and the proof line no longer counts it live.
    await page.getByRole("tab", { name: /Shares/ }).click();
    await expect(page.locator(".layer-row", { hasText: "/cdcd" })).toContainText("serves nothing");
    await expect(page.locator(".ss-header")).toContainText("0 shares live");
  });

  test("the palette gives focus back where Cmd+K was pressed, and its Edit lands on the editor heading", async ({ page }) => {
    await open(page, "?view=sources", ".layer-row");
    const filter = page.getByRole("searchbox", { name: "Filter sources" });
    await filter.focus();
    await page.keyboard.press("ControlOrMeta+k");
    await expect(page.getByRole("dialog", { name: "Search records and actions" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(filter).toBeFocused();

    await page.keyboard.press("ControlOrMeta+k");
    await page.keyboard.type("openjobs-host-trojan");
    await page.keyboard.press("Enter");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("heading", { name: /Edit source/ })).toBeFocused();
  });

  test("a delete chosen in the palette gives focus to the record's row when cancelled", async ({ page }) => {
    await open(page, "?view=sources", ".layer-row");
    await page.keyboard.press("ControlOrMeta+k");
    await page.keyboard.type("cdcd-self-host.bak");
    await page.keyboard.press("Enter");
    await page.locator(".palette-row", { hasText: "Delete" }).click();
    await expect(page.getByRole("alertdialog")).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.locator('[data-record-open="imported-cdcd-self-hostbak-20260820"]')).toBeFocused();
  });

  test("a panel restored from the address gives focus to its row on Escape", async ({ page }) => {
    await open(page, "?view=files&fixture=large&open=file-grace-surfboard", ".pc-side-panel");
    await expect(page.locator(".pc-side-panel h2")).toHaveText("for-grace-surfboard");
    await page.keyboard.press("Escape");
    await expect(page.locator(".pc-side-panel")).toHaveCount(0);
    await expect(page.locator('[data-record-open="file-grace-surfboard"]')).toBeFocused();
  });

  test("Show them moves focus to the Files layer it opened", async ({ page }) => {
    await open(page, "", ".attention-item");
    await page.getByRole("button", { name: "Show them" }).click();
    await expect(page.locator("#pc-panel-files")).toBeFocused();
  });

  test("Home and End reach the ends of the row menu and the layer row; Tab closes the menu onto its trigger", async ({ page }) => {
    await open(page, "?view=files", ".layer-row");
    const trigger = page.getByRole("button", { name: "Actions for for-cdcd-loon", exact: true });
    await trigger.focus();
    await page.keyboard.press("Enter");
    await page.keyboard.press("End");
    await expect.poll(() => focused(page)).toBe("Delete");
    await page.keyboard.press("Home");
    await expect.poll(() => focused(page)).toBe("Publish…");
    await page.keyboard.press("Tab");
    await expect(page.locator(".rec-menu")).toHaveCount(0);
    await expect(trigger).toBeFocused();

    await page.getByRole("tab", { name: /Files/ }).focus();
    await page.keyboard.press("End");
    await expect(page.getByRole("tab", { name: /Settings/ })).toBeFocused();
    await expect(page).toHaveURL(/[?&]view=settings/);
    await page.keyboard.press("Home");
    await expect(page.getByRole("tab", { name: /Overview/ })).toBeFocused();
  });

  test("after the panel closes, the selected chip opens it again; Escape on the map puts the path out", async ({ page }) => {
    await open(page, "", ".lineage-chip");
    const chip = page.locator('button.lineage-chip[data-map-key="imported-col-merge-openjobs"]');
    await chip.click();
    await expect(page.locator(".pc-side-panel")).toHaveCount(1);
    await page.keyboard.press("Escape");
    await expect(page.locator(".pc-side-panel")).toHaveCount(0);
    await expect(chip).toBeFocused();
    await expect(chip).toHaveAttribute("aria-pressed", "true");
    await chip.click();
    await expect(page.locator(".pc-side-panel")).toHaveCount(1);
    await page.keyboard.press("Escape");
    await page.keyboard.press("Escape");
    await expect(chip).toHaveAttribute("aria-pressed", "false");
    await expect(page.locator('.lineage-chip[data-state="off"]')).toHaveCount(0);
  });

  test("the Files page survives a reload, and a search starts again on page 1", async ({ page }) => {
    await open(page, "?view=files&fixture=large", ".layer-row");
    const footer = page.locator(".pc-pagination");
    await footer.getByRole("button", { name: "Next" }).click();
    await footer.getByRole("button", { name: "Next" }).click();
    await expect(footer).toContainText("Page 3 of 4");
    await expect(page).toHaveURL(/[?&]page=3/);
    await page.reload();
    await expect(footer).toContainText("Files 101 to 150 of 180");
    await page.getByRole("searchbox", { name: "Filter files" }).fill("alice");
    await expect(page).not.toHaveURL(/[?&]page=/);
  });

  test("one share expiry reads the same in attention and in Shares, and Review narrows Shares to it", async ({ page }) => {
    await open(page, "?fixture=failing", ".attention-item");
    await page.getByRole("button", { name: /Show \d+ more/ }).click();
    const claim = await page.locator(".attention-item", { hasText: "/oj-stash" }).locator(".attention-claim").innerText();
    const when = claim.match(/expires in \d+ days/)?.[0];
    expect(when).toBeTruthy();
    await page.locator(".attention-item", { hasText: "/oj-stash" }).getByRole("button", { name: "Review" }).click();
    await expect(page).toHaveURL(/[?&]q=oj-stash/);
    await expect(page.locator(".layer-row")).toHaveCount(1);
    await expect(page.locator(".layer-row")).toContainText(when!);
  });

  test("an attention line offers one control per destination, and names records rather than ids", async ({ page }) => {
    await open(page, "?fixture=failing", ".attention-item");
    const line = page.locator(".attention-item", { hasText: "failed its last refresh" });
    await expect(line.locator("button")).toHaveCount(1);
    await expect(line.getByRole("button", { name: "Open" })).toHaveAttribute("title", "Show openjobs-host-trojan in the side panel");
  });
});

test.describe("touch at 375", () => {
  test.use({ viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true });

  async function shortSides(page: Page, selector: string): Promise<Array<[string, number]>> {
    return page.locator(selector).evaluateAll((els) =>
      els
        .filter((el) => el.getBoundingClientRect().width > 0)
        .map((el) => {
          const r = el.getBoundingClientRect();
          return [(el.getAttribute("aria-label") || el.textContent || el.tagName).trim().slice(0, 30), Math.round(Math.min(r.width, r.height))] as [string, number];
        }),
    );
  }

  test("the layer tabs, attention actions, map chips, filters and Copy link are 44px", async ({ page }) => {
    await open(page, "?fixture=failing", ".attention-item");
    for (const selector of ["[data-variant=layer] [role=tab]", ".attention-item .pc-button", ".attention-more .pc-button", ".lineage-chip"]) {
      for (const [name, side] of await shortSides(page, selector)) expect(side, `${selector} ${name}`).toBeGreaterThanOrEqual(44);
    }
    await open(page, "?view=files", ".layer-row");
    for (const [name, side] of await shortSides(page, ".rec-tools input, .rec-tools select")) expect(side, name).toBeGreaterThanOrEqual(44);
    await open(page, "?view=shares", ".layer-row");
    for (const [name, side] of await shortSides(page, ".layer-row .pc-button")) expect(side, name).toBeGreaterThanOrEqual(44);
  });

  test("the side panel's close, actions and links, and the confirm dialog's controls, are 44px", async ({ page }) => {
    await open(page, "?view=combinations&open=imported-col-merge-cd-openjobs", ".pc-side-panel");
    for (const [name, side] of await shortSides(page, ".pc-side-panel button")) expect(side, name).toBeGreaterThanOrEqual(44);
    await open(page, "?view=files", ".layer-row");
    await page.getByRole("button", { name: "Actions for for-cdcd-loon", exact: true }).tap();
    await page.getByRole("menuitem", { name: /Delete/ }).tap();
    for (const [name, side] of await shortSides(page, "[role=alertdialog] button, [role=alertdialog] input")) expect(side, name).toBeGreaterThanOrEqual(44);
  });

  test("a tap beside a row's checkbox selects the row instead of opening its panel", async ({ page }) => {
    await open(page, "?view=files", ".layer-row");
    const box = page.getByRole("checkbox", { name: "Select for-cdcd-loon" });
    const at = (await box.boundingBox())!;
    await page.touchscreen.tap(at.x + at.width + 10, at.y + at.height / 2);
    await expect(box).toBeChecked();
    await expect(page.locator(".pc-side-panel")).toHaveCount(0);
    await page.touchscreen.tap(at.x + at.width + 10, at.y + at.height / 2);
    await expect(box).not.toBeChecked();
  });
});
