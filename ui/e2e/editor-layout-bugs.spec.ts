import { expect, test, type Page } from "@playwright/test";

import { m } from "./messages";

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
  await page.locator(".pc-side-panel").getByRole("button", { name: m.actions.edit }).click();
  await page.locator("#file-editor-title").waitFor();
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
    await expect(page.getByText(m.fileEditor.download)).toBeVisible();
    await box.click();
    await expect(box).not.toBeChecked();
  });

  test("a script has no Operations tab; a config and a plain file keep the chain", async ({ page }) => {
    await openFile(page, "Generated config");
    await expect(page.getByRole("tab", { name: m.editor.tabs.display })).toBeVisible();
    await expect(page.getByRole("tab", { name: m.editor.tabs.content })).toBeVisible();
    await expect(page.getByRole("tab", { name: m.editor.tabs.operations })).toHaveCount(0);

    await openFile(page, "Phone config");
    await page.getByRole("tab", { name: m.editor.tabs.operations }).click();
    await expect(page.getByText(m.chain.add)).toBeVisible();

    await page.getByRole("tab", { name: m.editor.tabs.content }).click();
    await page.getByRole("button", { name: m.fileEditor.types.script.title }).click();
    await expect(page.getByRole("tab", { name: m.editor.tabs.operations })).toHaveCount(0);

    await openFile(page, "规则补充");
    await page.getByRole("tab", { name: m.editor.tabs.operations }).click();
    await expect(page.getByText(m.fileEditor.documentOperations)).toBeVisible();
  });

  test("a share row keeps format and state in their own cells, and Copy link works", async ({ page }) => {
    await openShares(page);
    const row = page.locator(".layer-row", { hasText: m.shares.asClientAsks }).first();
    await expect(row).toBeVisible();
    const format = (await row.locator("td", { hasText: m.shares.asClientAsks }).boundingBox())!;
    const state = (await row.locator(".pc-state-pill").boundingBox())!;
    expect(Math.round(format.x + format.width)).toBeLessThanOrEqual(Math.round(state.x) + 1);
    await row.getByRole("button", { name: m.record.copyLink }).click();
    await expect(row.getByRole("button", { name: m.shares.copied }).or(page.locator(".lt-manual-copy"))).toBeVisible();
  });
});

test.describe("375 shares", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("the table scrolls inside itself and Copy link stays reachable", async ({ page }) => {
    await openShares(page);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375);
    const row = page.locator(".layer-row", { hasText: "cd-ask" }).first();
    await expect(row.locator("td.pc-name")).toBeVisible();
    const copy = row.getByRole("button", { name: m.record.copyLink });
    await copy.scrollIntoViewIfNeeded();
    await copy.click();
    await expect(row.getByRole("button", { name: m.shares.copied }).or(page.locator(".lt-manual-copy"))).toBeVisible();
  });
});
