import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Locator, type Page } from "@playwright/test";

import { LOCALES, TABLES, m, type Locale } from "./messages";
import { THEMES, VIEWPORTS, flow, menuItem, openMenu, openPlans, row, runAction, settled, shot, type Theme } from "./plans";

/**
 * The plan flow's follow-ups to the S2 security fixes (design 28 S2, lane
 * 6c), each found by a named element and never by English typed here, at
 * 1440 and 375, light and dark, with axe finding no serious or critical
 * violation, then the chain and refusal states in zh-CN and ru.
 *
 * The harness plays the 0.18 manifest over the plans fixture
 * (dev/planFixtures.ts, dev/planHost.ts).
 *
 * States, by test:
 *   provider staged      a provider record a fleet-bound combination gathers
 *                        answers staged on save (S2-SR-1): the notice names
 *                        the combination and offers Publish, the side panel
 *                        says why it is staged, the editor reopens on the
 *                        staged revision, and Publish files a plan whose
 *                        only change is a step
 *   provider delete      its delete goes through a plan that names the
 *                        combination; one a combination names explicitly
 *                        is blocked with Remove from
 *   plain delete refused the store's fleet_delete_requires_plan naming the
 *                        combination, with Delete through a plan
 *   chain only           a plan whose only change is a pass-through script
 *                        (S2-SR-2): the step line and the note
 *   propose steps        the migration's propose step lists each record's
 *                        step changes before core answers
 *   editor refusal       fleet_script_network_unavailable in the preview
 *                        pane and the identity preview, the step marked in
 *                        the chain, Open step
 *   plan refusal         the same refusal from plans.propose, beside the
 *                        step line it names, Open step into the editor
 *   applied, rejected    a decided plan that changes a step
 *   migrate withheld     ?fleet=denied leaves migrate_record out (S2-SR-4)
 */

const PAID = "Paid provider extra";
const PLUS = "Fleet lines with a paid extra";
const TOKYO = "Tokyo lines, pass-through script";
const OSAKA = "Osaka lines with a lookup script";
const COMBINATION = "Asia combination with a provider";

async function editRecord(page: Page, name: string, locale: Locale = "en"): Promise<void> {
  const t = TABLES[locale];
  await page.locator(".row-open", { hasText: name }).first().click();
  await page.locator(".pc-side-panel").getByRole("button", { name: t.actions.edit }).click();
  await page.locator("#editor-title").waitFor();
}

async function operationsTab(page: Page, locale: Locale = "en"): Promise<void> {
  await page.getByRole("tab", { name: new RegExp(`^${TABLES[locale].editor.tabs.operations}`) }).click();
}

/** Turns paid-extra's Regex Filter off and saves: the edit the plugin stages. */
async function stageProviderEdit(page: Page, locale: Locale = "en"): Promise<void> {
  await editRecord(page, PAID, locale);
  await operationsTab(page, locale);
  await page.locator(".chain-step").first().locator(".step-toggle input").uncheck();
  await page.getByRole("button", { name: TABLES[locale].editor.save, exact: true }).click();
  await expect(page.locator("#editor-title")).toHaveCount(0);
}

/** A close-up of one element into SHOTS_DIR, named zz-…: a full-page shot of the long table shrinks a notice past reading. */
async function closeUp(locator: Locator, name: string, meta: { width: number; theme: Theme; locale?: Locale }): Promise<void> {
  const dir = process.env.SHOTS_DIR;
  if (!dir) return;
  mkdirSync(dir, { recursive: true });
  await locator.screenshot({ path: join(dir, `zz-${name}-${meta.width}-${meta.theme}-${meta.locale ?? "en"}.png`) });
}

/** The notice's sentence keeps the notice's width: an action beside it at 375 squeezed it to a word a line. */
async function textHoldsWidth(notice: Locator): Promise<void> {
  const [box, text] = await Promise.all([notice.boundingBox(), notice.locator(".pc-notice-body").first().boundingBox()]);
  expect(text!.width).toBeGreaterThan(box!.width * 0.6);
}

const chainLine = (t: (typeof TABLES)[Locale], verb: "added" | "removed" | "changed", position: number, type: string, note = "") =>
  `${t.plans.chain[verb](position, type)}${note ? ` (${note})` : ""}`;

for (const viewport of VIEWPORTS) {
  for (const theme of THEMES) {
    const meta = (t: Theme = theme) => ({ width: viewport.width, theme: t });
    test.describe(`${viewport.width} ${theme}`, () => {
      test.use({ viewport });

      test("provider staged: a gathered record's edit stages, names the combination, and publishes through a plan", async ({ page }) => {
        await openPlans(page, { theme });
        await stageProviderEdit(page);
        const notice = page.getByTestId("records-staged-notice");
        await expect(notice).toContainText(m.plans.stagedSave.gathered(PLUS, 1));
        await expect(notice.getByTestId("records-publish-staged")).toBeEnabled();
        await textHoldsWidth(notice);
        await closeUp(notice, "provider-staged-notice", meta());
        await expect(row(page, "paid-extra").getByTestId("staged-badge")).toHaveAttribute("title", new RegExp(m.plans.stagedSave.gatheredBy(PLUS, 1).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
        await settled(page, viewport.width);
        await shot(page, "provider-staged-notice", meta());

        // The side panel says why a record whose own flags are not fleet-bound waits.
        await page.locator(".row-open", { hasText: PAID }).first().click();
        await expect(page.locator(".pc-side-panel").getByTestId("record-plan-gathered")).toHaveText(m.plans.stagedSave.gatheredBy(PLUS, 1));
        await settled(page, viewport.width);
        await shot(page, "provider-staged-panel", meta());

        // The editor reopens on the staged revision, so the next save builds on it.
        await page.locator(".pc-side-panel").getByRole("button", { name: m.actions.edit }).click();
        await page.locator("#editor-title").waitFor();
        await expect(page.getByTestId("editor-staged")).toBeVisible();
        await operationsTab(page);
        await expect(page.locator(".chain-step").first().locator(".step-toggle input")).not.toBeChecked();
        await page.getByRole("button", { name: m.common.cancel, exact: true }).click();
        await expect(page.locator("#editor-title")).toHaveCount(0);

        await runAction(page, "paid-extra", "publish-staged");
        await expect(flow(page)).toHaveAttribute("data-mode", "publish");
        await expect(flow(page).getByTestId("plan-chain-section")).toHaveAttribute("data-record", "paid-extra");
        await expect(flow(page).getByTestId("plan-chain-line")).toHaveText([chainLine(m, "changed", 1, "Regex Filter", m.plans.chain.notes.nowDisabled)]);
        await expect(flow(page).getByTestId("plan-chain-only")).toHaveText(m.plans.chain.onlyChain);
        await settled(page, viewport.width);
        await shot(page, "provider-publish-plan", meta());
      });

      test("provider delete: a gathered record's delete is a plan that names the combination; an explicit member is blocked", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "paid-extra", "delete");
        await expect(flow(page)).toHaveAttribute("data-mode", "delete");
        await expect(flow(page).getByTestId("delete-intro")).toHaveText(m.plans.stagedSave.deleteIntroGathered(PLUS, 1));
        await expect(page.locator(".pc-side-panel")).toContainText(m.plans.stagedSave.deleteDescriptionGathered);
        await settled(page, viewport.width);
        await shot(page, "provider-delete-changes", meta());
        await flow(page).getByTestId("flow-next").click();
        await flow(page).getByTestId("flow-propose").click();
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.pending);
        const family = flow(page).locator('[data-testid="plan-share"][data-share="sh-plus-family"]');
        await expect(family.getByTestId("plan-share-counts")).toHaveText(m.plans.view.shareCounts(0, 2, 0, 0));
        await settled(page, viewport.width);
        await shot(page, "provider-delete-plan", meta());

        await openPlans(page, { theme });
        await runAction(page, "openjobs-host", "delete");
        await expect(flow(page).getByTestId("delete-intro")).toHaveText(m.plans.stagedSave.deleteIntroGathered(COMBINATION, 1));
        await expect(flow(page).getByTestId("delete-blocked")).toBeVisible();
        await expect(flow(page).getByTestId("delete-remove-member")).toHaveText(m.migration.removeFrom(COMBINATION));
        await settled(page, viewport.width);
        await shot(page, "provider-delete-explicit", meta());
      });

      test("plain delete refused: the store names the combination that gathers the record", async ({ page }) => {
        await openPlans(page, { theme });
        await row(page, "paid-extra").locator("input[type=checkbox]").first().check();
        await page.locator(".pc-batch-bar").getByRole("button", { name: new RegExp(m.actions.delete) }).click();
        // Nothing names it as a member, so the dialog asks for no typed name; the store still refuses.
        await page.locator(".lt-dialog").getByRole("button", { name: m.common.delete, exact: true }).click();
        const notice = page.getByTestId("records-error-notice");
        await expect(notice).toContainText(m.plans.refusal.notDeleted(m.plans.stagedSave.gatheredDelete(PLUS, 1)));
        await textHoldsWidth(notice);
        // The batch's remainder notice lists the record; at 375 its list scrolled sideways by a few pixels (axe on CI's fonts).
        const remainder = page.getByTestId("records-delete-remainder");
        await expect(remainder).toBeVisible();
        await textHoldsWidth(remainder);
        expect(await remainder.locator(".partial-strip__names").evaluate((list) => list.scrollWidth - list.clientWidth)).toBeLessThanOrEqual(0);
        await closeUp(notice, "provider-delete-refused", meta());
        await settled(page, viewport.width);
        await shot(page, "provider-delete-refused", meta());
        await notice.getByTestId("records-delete-plan").click();
        await expect(flow(page)).toHaveAttribute("data-mode", "delete");
      });

      test("chain only: a plan whose only change is a step says so", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "tokyo-scripted", "publish-staged");
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.pending);
        await expect(flow(page).getByTestId("plan-chain-line")).toHaveText([chainLine(m, "added", 2, "Script Operator")]);
        await expect(flow(page).getByTestId("plan-chain")).toHaveAttribute("data-only", "true");
        await expect(flow(page).getByTestId("plan-chain-only")).toHaveText(m.plans.chain.onlyChain);
        await expect(flow(page).getByTestId("plan-totals")).toContainText(m.plans.view.totals(1, 1, 0, 0, 0).split(" · ")[2]!);
        await settled(page, viewport.width);
        await shot(page, "chain-only-plan", meta());
      });

      test("propose steps: the migration's propose step lists each record's step changes", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "relay-graph", "migrate");
        await flow(page).getByTestId("migration-stage").click();
        await expect(flow(page).getByTestId("migration-staged")).toBeVisible();
        await flow(page).getByTestId("flow-next").click();
        await flow(page).locator('[data-testid="share-decision"][data-share="sh-pair-family"]').getByTestId("share-identity").selectOption("vu_alice");
        await flow(page).getByTestId("flow-next").click();
        const relay = flow(page).locator('[data-testid="propose-steps"][data-record="relay-graph"]');
        await expect(relay).toHaveAttribute("data-status", "ready");
        await expect(relay.getByTestId("propose-step-line")).toHaveText([chainLine(m, "added", 1, "Structured Filter")]);
        const home = flow(page).locator('[data-testid="propose-steps"][data-record="home-graph"]');
        await expect(home.getByTestId("propose-step-line")).toHaveText([chainLine(m, "added", 1, "Structured Filter")]);
        await expect(flow(page).getByTestId("propose-steps-note")).toHaveText(m.plans.chain.proposeNote);
        await settled(page, viewport.width);
        await shot(page, "propose-steps", meta());
      });

      test("editor refusal: the preview and the identity preview name the step, the chain marks it, Open step opens it", async ({ page }) => {
        await openPlans(page, { theme });
        await editRecord(page, OSAKA);
        await expect(page.getByTestId("editor-staged")).toBeVisible();
        await page.locator(".editor-side").getByRole("button", { name: m.editor.preview, exact: true }).click();
        const refusal = page.locator(".editor-side .pc-panel-body > [data-testid=script-network-refusal], .editor-side [data-testid=script-network-refusal]").first();
        await expect(refusal).toHaveAttribute("data-position", "2");
        await expect(refusal.getByTestId("script-network-step")).toHaveText(m.plans.scriptNetwork.step(2, "Script Operator", m.plans.scriptNetwork.thisRecord));
        await expect(refusal.getByTestId("script-network-rule")).toHaveText(m.plans.scriptNetwork.rule);
        await expect(page.getByTestId("preview-error")).toHaveCount(0);
        await settled(page, viewport.width);
        await shot(page, "editor-network-refusal", meta());

        await refusal.getByTestId("script-network-open-step").click();
        const step = page.locator('.chain-step[data-step-index="1"]');
        await expect(step.locator(".step-title")).toHaveAttribute("aria-expanded", "true");
        await expect(step.locator(".step-title")).toBeFocused();
        await expect(step.getByTestId("step-network-flag")).toHaveText(m.plans.scriptNetwork.flag);
        await settled(page, viewport.width);
        await shot(page, "editor-network-step-open", meta());

        // Core's bind.preview refuses the same render for an identity.
        await page.getByTestId("identity-row").filter({ hasText: "erin-tablet" }).locator("input[type=radio]").check();
        const bind = page.getByTestId("identity-preview").getByTestId("script-network-refusal");
        await expect(bind).toHaveAttribute("data-position", "2");
        await expect(bind.getByTestId("script-network-step")).toHaveText(m.plans.scriptNetwork.step(2, "Script Operator", m.plans.scriptNetwork.thisRecord));
        await settled(page, viewport.width);
        await shot(page, "editor-bind-network-refusal", meta());
      });

      test("plan refusal: propose names the step beside its line, and Open step opens it in the editor", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "osaka-lookup", "publish-staged");
        await expect(flow(page)).toHaveAttribute("data-step", "propose");
        const steps = flow(page).locator('[data-testid="propose-steps"][data-record="osaka-lookup"]');
        await expect(steps.getByTestId("propose-step-line")).toHaveText([new RegExp(`^${chainLine(m, "changed", 2, "Script Operator").replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`)]);
        await expect(steps.getByTestId("propose-step-flag")).toHaveText(m.plans.scriptNetwork.flag);
        const refusal = flow(page).getByTestId("script-network-refusal");
        await expect(refusal).toHaveAttribute("data-record", "osaka-lookup");
        await expect(refusal).toHaveAttribute("data-position", "2");
        await expect(refusal.getByTestId("script-network-step")).toHaveText(m.plans.scriptNetwork.step(2, "Script Operator", OSAKA));
        await expect(flow(page).getByTestId("flow-banner")).toHaveCount(0);
        await settled(page, viewport.width);
        await shot(page, "propose-network-refusal", meta());

        await refusal.getByTestId("script-network-open-step").click();
        await page.locator("#editor-title").waitFor();
        const step = page.locator('.chain-step[data-step-index="1"]');
        await expect(step.locator(".step-title")).toHaveAttribute("aria-expanded", "true");
        await expect(step.locator(".step-title")).toBeFocused();
        await settled(page, viewport.width);
        await shot(page, "propose-network-open-step", meta());
      });

      test("applied and rejected: a decided plan that changes a step keeps its step lines", async ({ page }) => {
        for (const decided of ["applied", "rejected"] as const) {
          await openPlans(page, { theme, query: `&plans=${decided}` });
          await runAction(page, "tokyo-scripted", "publish-staged");
          await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state[decided]);
          await expect(flow(page).getByTestId("plan-chain-line")).toHaveText([chainLine(m, "added", 2, "Script Operator")]);
          await expect(flow(page).getByTestId("plan-status")).toContainText(decided === "applied" ? m.plans.view.appliedNote : m.plans.view.rejectedNote);
          await settled(page, viewport.width);
          await shot(page, `chain-plan-${decided}`, meta());
        }
      });

      test("migrate withheld: a token without vpncore:read is not offered migrate_record", async ({ page }) => {
        await openPlans(page, { theme, query: "&fleet=denied" });
        await openMenu(page, "relay-graph");
        await expect(menuItem(page, "relay-graph", "migrate")).toBeDisabled();
        await expect(menuItem(page, "relay-graph", "migrate")).toHaveAttribute("title", m.plans.actions.noMigrate);
        await settled(page, viewport.width);
        await shot(page, "migrate-withheld", meta());
      });
    });
  }
}

/* The new sentences in the other two locales, at both widths: the step lines, the note, the refusal and the staged notice. */
for (const locale of LOCALES.filter((entry) => entry !== "en")) {
  const t = TABLES[locale];
  for (const viewport of VIEWPORTS) {
    test.describe(`${locale} ${viewport.width}`, () => {
      test.use({ viewport });

      test("the chain-only plan, the plan refusal and the staged notice", async ({ page }) => {
        await openPlans(page, { locale });
        await runAction(page, "tokyo-scripted", "publish-staged");
        await expect(flow(page).getByTestId("plan-chain-line")).toHaveText([chainLine(t, "added", 2, "Script Operator")]);
        await expect(flow(page).getByTestId("plan-chain-only")).toHaveText(t.plans.chain.onlyChain);
        await settled(page, viewport.width);
        await shot(page, "chain-only-plan", { width: viewport.width, theme: "light", locale });

        await openPlans(page, { locale });
        await runAction(page, "osaka-lookup", "publish-staged");
        const refusal = flow(page).getByTestId("script-network-refusal");
        await expect(refusal.getByTestId("script-network-step")).toHaveText(t.plans.scriptNetwork.step(2, "Script Operator", OSAKA));
        await expect(refusal.getByTestId("script-network-rule")).toHaveText(t.plans.scriptNetwork.rule);
        await settled(page, viewport.width);
        await shot(page, "propose-network-refusal", { width: viewport.width, theme: "light", locale });

        await openPlans(page, { locale });
        await stageProviderEdit(page, locale);
        await expect(page.getByTestId("records-staged-notice")).toContainText(t.plans.stagedSave.gathered(PLUS, 1));
        await textHoldsWidth(page.getByTestId("records-staged-notice"));
        await closeUp(page.getByTestId("records-staged-notice"), "provider-staged-notice", { width: viewport.width, theme: "light", locale });
        await settled(page, viewport.width);
        await shot(page, "provider-staged-notice", { width: viewport.width, theme: "light", locale });
      });
    });
  }
}
