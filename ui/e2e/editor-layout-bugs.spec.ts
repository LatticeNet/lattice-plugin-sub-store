import { expect, test, type Page } from "@playwright/test";

// The editor drives run on the hand-made set, which has the file shapes they
// were written against (a config, a script, a plain file).
async function openFiles(page: Page): Promise<void> {
  await page.goto("/dev.html?fixture=canned&view=files");
  await page.locator(".layer-row").first().waitFor();
}

async function openShares(page: Page): Promise<void> {
  await page.goto("/dev.html?fixture=canned&view=shares");
  await page.locator(".layer-row").first().waitFor();
}

/** Row, then the side panel's Edit: the one path into the editor from a table. */
async function openFile(page: Page, name: string): Promise<void> {
  await openFiles(page);
  await page.locator(".row-open", { hasText: name }).first().click();
  await page.locator(".pc-side-panel").getByRole("button", { name: "Edit" }).click();
  await page.getByRole("heading", { name: /file/i }).waitFor();
}

test.describe("1440 files editor and shares", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the download checkbox is a square and toggles", async ({ page }) => {
    await openFile(page, "Phone config");
    const box = page.locator(".checkbox-field input[type=checkbox]");
    await expect(box).toBeVisible();
    const size = (await box.boundingBox())!;
    expect(Math.abs(size.width - size.height), "checkbox is not square").toBeLessThanOrEqual(1);
    expect(size.width).toBeGreaterThanOrEqual(14);
    expect(size.width).toBeLessThanOrEqual(20);
    expect(size.height).toBeLessThanOrEqual(20);
    await expect(box).not.toBeChecked();
    await box.click();
    await expect(box).toBeChecked();
    await expect(page.getByText("Save rather than show")).toBeVisible();
    await box.click();
    await expect(box).not.toBeChecked();
  });

  test("a script has no Operations tab; a config and a plain file keep the chain", async ({ page }) => {
    await openFile(page, "Generated config");
    await expect(page.getByRole("tab", { name: "Display" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Content" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Operations" })).toHaveCount(0);

    await openFile(page, "Phone config");
    await page.getByRole("tab", { name: "Operations" }).click();
    await expect(page.getByText("Add an operation")).toBeVisible();

    await page.getByRole("tab", { name: "Content" }).click();
    await page.getByRole("button", { name: "Built by a script" }).click();
    await expect(page.getByRole("tab", { name: "Operations" })).toHaveCount(0);

    await openFile(page, "规则补充");
    await page.getByRole("tab", { name: "Operations" }).click();
    await expect(page.getByText("Document operations")).toBeVisible();
  });

  test("a share row keeps format and state in their own cells, and Copy link works", async ({ page }) => {
    await openShares(page);
    const row = page.locator(".layer-row", { hasText: "as the client asks" }).first();
    await expect(row).toBeVisible();
    const format = (await row.locator("td", { hasText: "as the client asks" }).boundingBox())!;
    const state = (await row.locator(".pc-state-pill").boundingBox())!;
    expect(Math.round(format.x + format.width)).toBeLessThanOrEqual(Math.round(state.x) + 1);
    await row.getByRole("button", { name: "Copy link" }).click();
    await expect(row.getByRole("button", { name: "Copied" }).or(page.getByText("Link for"))).toBeVisible();
  });
});

test.describe("375 shares", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("the table scrolls inside itself and Copy link stays reachable", async ({ page }) => {
    await openShares(page);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375);
    const row = page.locator(".layer-row", { hasText: "cd-ask" }).first();
    await expect(row.locator("td.pc-name")).toBeVisible();
    const copy = row.getByRole("button", { name: "Copy link" });
    await copy.scrollIntoViewIfNeeded();
    await copy.click();
    await expect(row.getByRole("button", { name: "Copied" }).or(page.getByText("Link for"))).toBeVisible();
  });
});
