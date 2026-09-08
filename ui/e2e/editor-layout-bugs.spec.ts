import { expect, test, type Page } from "@playwright/test";

async function openFiles(page: Page): Promise<void> {
  await page.goto("/dev.html?lens=files");
  await page.locator(".rec-list .rec-row").first().waitFor();
}

async function openShares(page: Page): Promise<void> {
  await page.goto("/dev.html?lens=shares");
  await page.locator(".rec-list .rec-row").first().waitFor();
}

async function openFile(page: Page, name: string): Promise<void> {
  await openFiles(page);
  const row = page.locator(".rec-row", { hasText: name }).first();
  await row.getByRole("button", { name: "Open" }).click();
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

  test("share format does not paint over the live pill, and Copy link works", async ({ page }) => {
    await openShares(page);
    const ident = page.locator(".rec-ident", { hasText: "as the client asks" }).first();
    await expect(ident).toBeVisible();
    const format = (await ident.locator(".rec-col-nodes").boundingBox())!;
    const state = (await ident.locator(".rec-col-status").boundingBox())!;
    expect(Math.round(format.x + format.width)).toBeLessThanOrEqual(Math.round(state.x) + 1);
    const row = page.locator(".rec-row", { hasText: "as the client asks" }).first();
    await row.getByRole("button", { name: "Copy link" }).click();
    await expect(row.getByRole("button", { name: "Copied" }).or(page.getByText("Link for"))).toBeVisible();
  });
});

test.describe("375 shares", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("the live pill stays in its own cell and does not cover the name", async ({ page }) => {
    await openShares(page);
    const ident = page.locator(".rec-ident", { hasText: "cd-ask" }).first();
    await expect(ident).toBeVisible();
    const name = (await ident.locator(".rec-ident-name").boundingBox())!;
    const state = (await ident.locator(".rec-col-status").boundingBox())!;
    expect(Math.round(name.x + name.width)).toBeLessThanOrEqual(Math.round(state.x) + 1);
    await expect(ident.locator(".rec-col-nodes")).toBeHidden();
    const row = page.locator(".rec-row", { hasText: "cd-ask" }).first();
    await row.getByRole("button", { name: "Copy link" }).click();
    await expect(row.getByRole("button", { name: "Copied" }).or(page.getByText("Link for"))).toBeVisible();
  });
});
