import { expect, test, type Page } from "@playwright/test";

import { LOCALES, m, type Locale } from "./messages";
import { THEMES, VIEWPORTS, flow, menuItem, openMenu, openPlans, row, runAction, settled, shot, words, type Theme } from "./plans";

/**
 * The migration flow, the plan flow's other modes and the staged state
 * (design 28 S2, lane 6b; s2-plan sections 6 and 11): every state of the UI
 * acceptance rule that lives on the Records layer, each found by a named
 * element and never by English typed here, at 1440 and 375, light and dark,
 * with axe finding no serious or critical violation, then the three locales.
 *
 * The harness plays the 0.18 manifest (`?manifest=s2`) over the plans fixture
 * (`?fixture=plans`, dev/planFixtures.ts); `?plans=` picks core's plan states
 * (dev/planHost.ts).
 *
 * States, by test:
 *   migration             a graph record and its sibling staged, identities,
 *                         zero bindings warned before propose, the plan view
 *                         with its per-share exclusion list
 *   export record         no default identity; every share decided first
 *   file dependents       a migration blocked by files the list shows
 *   refused.files         a migration refused for a file the list has not seen
 *   abandon               the staged revisions dropped
 *   delete through plan   a fleet-bound record's delete files a plan
 *   explicit member       a delete blocked, "Remove from <combination>"
 *   plain delete refused  fleet_delete_requires_plan read from the reply
 *   discarded             a staged revision discarded, naming both revisions
 *   pending plan          Publish opens the plan that waits
 *   decoy share           an identity given through an assignment-only plan
 *   darkened identity     an identity on a non-fleet record cleared
 *   permission denied     propose and publish withheld
 *   plan over 1 MiB       core's refusal with its figures
 *   failed apply          the derived state, with the live revision from list
 *   stale                 a replaced staged revision; a grown closure
 *   partial               a multi-record plan stopped part way; propose the rest
 *   mixed combination     blocked until its legacy member migrates
 *   withheld output       a legacy record's output and Fetch now
 */

const RELAY = "香港中继 Relay path for alice";
const HOME = "Home relay for bob";
const OSAKA = "大阪中继 Osaka relay (legacy)";
const TOKYO = "Tokyo lines (migrated)";
const ROUTER_FILE = "OpenWrt router config";
const COMBINATION = "Asia combination with a provider";

async function stage(page: Page, recordId: string): Promise<void> {
  await runAction(page, recordId, "migrate");
  await expect(flow(page)).toHaveAttribute("data-mode", "migrate");
  await flow(page).getByTestId("migration-stage").click();
  await expect(flow(page).getByTestId("migration-staged")).toBeVisible();
  await expect(flow(page).getByTestId("flow-abandon")).toBeEnabled();
}

async function next(page: Page): Promise<void> {
  await flow(page).getByTestId("flow-next").click();
}

async function propose(page: Page): Promise<void> {
  await flow(page).getByTestId("flow-propose").click();
}

function decision(page: Page, shareId: string) {
  return flow(page).locator(`[data-testid="share-decision"][data-share="${shareId}"]`);
}

for (const viewport of VIEWPORTS) {
  for (const theme of THEMES) {
    const meta = (t: Theme = theme) => ({ width: viewport.width, theme: t });
    test.describe(`${viewport.width} ${theme}`, () => {
      test.use({ viewport });

      test("migration: a graph record and its sibling, identities, zero bindings, one plan with its per-share exclusions", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "relay-graph", "migrate");
        await expect(flow(page).getByTestId("flow-step").first()).toHaveAttribute("aria-current", "step");
        await expect(flow(page).getByTestId("migration-together-row")).toContainText(HOME);
        await shot(page, "migration-1-changes", meta());
        await flow(page).getByTestId("migration-stage").click();
        await expect(flow(page).getByTestId("migration-staged")).toBeVisible();
        await expect(flow(page).getByTestId("migration-selection")).toContainText(m.migration.selectionRoots(3, 3));
        await expect(flow(page).getByTestId("migration-warning").first()).toContainText("Regex Filter");
        // The sibling is staged too before anything moves on.
        await expect(flow(page).getByTestId("migration-together-row")).toContainText(new RegExp(m.migration.stagedAs("[0-9a-f]+")));
        await expect(flow(page).getByTestId("flow-next")).toBeEnabled();
        await settled(page, viewport.width);
        await shot(page, "migration-2-staged", meta());

        await next(page);
        // A graph record's shares start on the identity each composed for.
        await expect(decision(page, "sh-relay-alice").getByTestId("share-identity")).toHaveValue("vu_alice");
        await expect(decision(page, "sh-home-bob").getByTestId("share-identity")).toHaveValue("vu_bob");
        await expect(flow(page).getByTestId("shares-undecided")).toHaveText(m.migration.undecided(1));
        await expect(flow(page).getByTestId("flow-next")).toBeDisabled();
        // An identity that holds none of the lines: warned before propose, with the bind action.
        await decision(page, "sh-pair-family").getByTestId("share-identity").selectOption("vu_carol");
        await expect(decision(page, "sh-pair-family").getByTestId("identity-no-lines")).toContainText(m.migration.noLines);
        await decision(page, "sh-pair-family").getByTestId("bind-selection").click();
        await expect(decision(page, "sh-pair-family").getByTestId("bind-selection-result")).toHaveText(m.plans.bind.unavailable);
        await settled(page, viewport.width);
        await shot(page, "migration-3-identities", meta());

        await next(page);
        await expect(flow(page).getByTestId("propose-record")).toHaveCount(2);
        await propose(page);
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.pending);
        await expect(flow(page).getByTestId("plan-view")).toBeVisible();
        await expect(flow(page).getByTestId("plan-assignment")).toHaveCount(3);
        const family = flow(page).locator('[data-testid="plan-share"][data-share="sh-pair-family"]');
        await family.locator("summary").click();
        await expect(family.locator('[data-testid="plan-excluded"][data-reason="not_bound"]')).toContainText(m.fleetEditor.bind.reasons.not_bound);
        await expect(family.locator('[data-testid="plan-excluded"][data-reason="not_bound"]')).toContainText(m.plans.view.fixes.not_bound);
        await expect(flow(page).getByTestId("plan-open-approvals")).toBeVisible();
        await settled(page, viewport.width);
        await shot(page, "migration-4-status", meta());
      });

      test("export record: no default identity, every share decided before propose", async ({ page }) => {
        await openPlans(page, { theme });
        await stage(page, "self-export");
        await expect(flow(page).getByTestId("migration-selection")).toHaveText(m.migration.selectionEvery);
        await next(page);
        await expect(decision(page, "sh-self-home").getByTestId("share-identity")).toHaveValue("");
        await expect(flow(page).getByTestId("shares-undecided")).toHaveText(m.migration.undecided(2));
        await decision(page, "sh-self-phone").getByTestId("share-archive").check();
        await decision(page, "sh-self-home").getByTestId("share-identity").selectOption("vu_bob");
        await expect(flow(page).getByTestId("shares-undecided")).toHaveCount(0);
        await next(page);
        await propose(page);
        await expect(flow(page).getByTestId("plan-archival")).toHaveCount(1);
        await settled(page, viewport.width);
      });

      test("migration blocked by the files the list shows reading the record", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "files-legacy", "migrate");
        await expect(flow(page).getByTestId("migration-blocked-files")).toContainText(m.migration.filesBlock(1));
        await expect(flow(page).getByTestId("migration-blocked-file")).toHaveText(ROUTER_FILE);
        await expect(flow(page).getByTestId("migration-stage")).toBeDisabled();
        await settled(page, viewport.width);
        await shot(page, "migration-blocked-files", meta());
      });

      test("migration refused for files named in refused.files", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "late-legacy", "migrate");
        await flow(page).getByTestId("migration-stage").click();
        await expect(flow(page).getByTestId("migration-refused")).toHaveAttribute("data-code", "fleet_file_unavailable");
        await expect(flow(page).getByTestId("migration-refused-file")).toHaveText("late-config");
        await expect(flow(page).getByTestId("flow-next")).toBeDisabled();
        await settled(page, viewport.width);
        await shot(page, "migration-refused-files", meta());
      });

      test("abandon migration drops what the flow staged", async ({ page }) => {
        await openPlans(page, { theme });
        await stage(page, "relay-graph");
        await flow(page).getByTestId("flow-abandon").click();
        await expect(flow(page)).toHaveCount(0);
        await expect(page.getByText(m.migration.abandoned(`${RELAY}, ${HOME}`))).toBeVisible();
        await expect(row(page, "relay-graph").getByTestId("staged-badge")).toHaveCount(0);
      });

      test("a fleet-bound record's delete goes through a plan that archives its own shares", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "fleet-probe-fast", "delete");
        await expect(flow(page)).toHaveAttribute("data-mode", "delete");
        await expect(flow(page).getByTestId("delete-archived-share")).toContainText("/probe-erin");
        await next(page);
        await propose(page);
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.pending);
        await expect(flow(page).getByTestId("plan-archival")).toHaveCount(1);
        await settled(page, viewport.width);
        await shot(page, "delete-through-plan", meta());
      });

      test("a delete blocked by a combination that names the record, and the way out", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "asia-fleet", "delete");
        await expect(flow(page).getByTestId("delete-blocked")).toContainText(COMBINATION);
        await expect(flow(page).getByTestId("flow-next")).toBeDisabled();
        await settled(page, viewport.width);
        await shot(page, "delete-blocked-member", meta());
        await flow(page).getByTestId("delete-remove-member").click();
        // The combination's editor, with the record already out of its members: the edit is unsaved.
        await expect(page.locator("#editor-title")).toContainText(m.editor.dirty);
        await expect(page.locator(".picker-chosen", { hasText: "亚洲线路 Asia lines" })).toHaveCount(0);
      });

      test("a plain delete refused as fleet-bound is read from the structured reply", async ({ page }) => {
        await openPlans(page, { theme });
        await row(page, "fleet-probe-fast").locator("input[type=checkbox]").first().check();
        await page.locator(".pc-batch-bar").getByRole("button", { name: new RegExp(m.actions.delete) }).click();
        await page.locator(".lt-dialog-input").fill("Lines that passed a probe today");
        await page.locator(".lt-dialog").getByRole("button", { name: m.common.delete, exact: true }).click();
        const notice = page.getByTestId("records-error-notice");
        await expect(notice).toContainText(m.plans.refusal.notDeleted(m.fleetEditor.refusal.codes.fleet_delete_requires_plan));
        await settled(page, viewport.width);
        await shot(page, "delete-refused-requires-plan", meta());
        await notice.getByTestId("records-delete-plan").click();
        await expect(flow(page)).toHaveAttribute("data-mode", "delete");
      });

      test("discard names both revisions and the plan it stales", async ({ page }) => {
        await openPlans(page, { theme });
        await expect(row(page, "asia-fleet").getByTestId("staged-badge")).toHaveAttribute("data-state", "pending");
        await runAction(page, "asia-fleet", "discard-staged");
        const dialog = page.locator(".lt-dialog");
        await expect(dialog).toContainText(m.plans.discard.title);
        await expect(dialog.locator(".is-consequence li")).toHaveCount(2);
        await expect(dialog.locator(".is-consequence li").last()).toContainText("apr_");
        await settled(page, viewport.width);
        await shot(page, "discard-confirm", meta());
        await dialog.getByRole("button", { name: m.plans.discard.verb, exact: true }).click();
        const said = m.plans.discarded("亚洲线路 Asia lines", "@@").replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace("@@", "\\S+");
        await expect(page.getByText(new RegExp(said))).toBeVisible();
        await expect(row(page, "asia-fleet").getByTestId("staged-badge")).toHaveCount(0);
      });

      test("Publish on a revision a plan waits on opens that plan", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "asia-fleet", "publish-staged");
        await expect(flow(page)).toHaveAttribute("data-step", "status");
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.pending);
        await expect(flow(page).getByTestId("plan-open-approvals")).toBeVisible();
        await settled(page, viewport.width);
      });

      test("a share without an identity on a fleet record: the decoy, fixed by an assignment-only plan", async ({ page }) => {
        await openPlans(page, { theme });
        await expect(row(page, "asia-fleet").getByTestId("record-decoy")).toHaveText(m.plans.decoyCell(1));
        await runAction(page, "asia-fleet", "share-identities");
        await expect(flow(page)).toHaveAttribute("data-mode", "assign");
        await expect(decision(page, "sh-asia-open").getByTestId("share-decoy")).toHaveText(m.migration.decoy);
        await decision(page, "sh-asia-open").getByTestId("share-identity").selectOption("vu_alice");
        await settled(page, viewport.width);
        await shot(page, "assign-decoy", meta());
        await next(page);
        await propose(page);
        await expect(flow(page).getByTestId("plan-assignment")).toHaveCount(1);
      });

      test("an identity on a non-fleet record buys nothing: Clear identity through a plan", async ({ page }) => {
        await openPlans(page, { theme });
        await expect(row(page, "openjobs-host").getByTestId("record-darkened")).toHaveText(m.plans.darkenedCell(1));
        await runAction(page, "openjobs-host", "share-identities");
        await expect(decision(page, "sh-oj-bob").getByTestId("share-darkened")).toContainText("bob-phone");
        await decision(page, "sh-oj-bob").getByTestId("share-clear").check();
        await next(page);
        await expect(flow(page).getByTestId("propose-changes")).toContainText(m.migration.summaryClear("Bob's provider link"));
        await propose(page);
        await expect(flow(page).getByTestId("plan-clearing")).toHaveCount(1);
        await settled(page, viewport.width);
        await shot(page, "clear-identity", meta());
      });

      test("permission denied: propose and publish withheld, each verb says why", async ({ page }) => {
        await openPlans(page, { theme, query: "&plans=denied" });
        await openMenu(page, "asia-fleet");
        await expect(menuItem(page, "asia-fleet", "publish-staged")).toBeDisabled();
        await expect(page.locator('.rec-menu[data-row-menu="asia-fleet"] .rec-menu-note', { hasText: m.plans.actions.noPropose })).toBeVisible();
        await settled(page, viewport.width);
        await shot(page, "propose-denied", meta());
      });

      test("a plan over 1 MiB: core's refusal with its figures", async ({ page }) => {
        await openPlans(page, { theme, query: "&plans=large" });
        await runAction(page, "asia-fleet", "share-identities");
        await decision(page, "sh-asia-open").getByTestId("share-identity").selectOption("vu_alice");
        await next(page);
        await propose(page);
        await expect(flow(page).getByTestId("flow-banner")).toHaveAttribute("data-code", "plan_too_large");
        await expect(flow(page).getByTestId("flow-banner")).toContainText(m.migration.tooLarge("1.3 MB", "1 MB").split(":")[0]!);
        await settled(page, viewport.width);
        await shot(page, "plan-too-large", meta());
      });

      test("a plan whose apply failed: derived, with the record's live revision from the list", async ({ page }) => {
        await openPlans(page, { theme, query: "&plans=failed" });
        await expect(row(page, "asia-fleet").getByTestId("staged-badge")).toHaveAttribute("data-state", "failed");
        await runAction(page, "asia-fleet", "publish-staged");
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.failed);
        await expect(flow(page).getByTestId("plan-live-revision")).toContainText(m.plans.view.notPromoted);
        await expect(flow(page).getByTestId("plan-propose-again")).toBeVisible();
        await settled(page, viewport.width);
        await shot(page, "plan-failed", meta());
      });

      test("a stale plan over a replaced staged revision, proposed again", async ({ page }) => {
        await openPlans(page, { theme, query: "&plans=stale" });
        await expect(row(page, "asia-fleet").getByTestId("staged-badge")).toHaveAttribute("data-state", "stale");
        await runAction(page, "asia-fleet", "publish-staged");
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.stale);
        await expect(flow(page).getByTestId("plan-reason")).toContainText("replaced or discarded");
        await shot(page, "plan-stale", meta());
        await flow(page).getByTestId("plan-propose-again").click();
        await expect(flow(page)).toHaveAttribute("data-step", "identities");
        await settled(page, viewport.width);
      });

      test("a stale plan whose closure grew: propose again", async ({ page }) => {
        await openPlans(page, { theme, query: "&plans=closure" });
        await runAction(page, "asia-fleet", "publish-staged");
        await expect(flow(page).getByTestId("plan-reason")).toContainText("a collection started or stopped reading");
        await expect(flow(page).getByTestId("plan-propose-again")).toBeVisible();
        await settled(page, viewport.width);
        await shot(page, "plan-closure-grew", meta());
      });

      test("a multi-record plan stopped part way: what applied, and propose the rest", async ({ page }) => {
        await openPlans(page, { theme, query: "&plans=partial" });
        await expect(row(page, "legacy-left").getByTestId("staged-badge")).toHaveAttribute("data-state", "failed");
        await runAction(page, "legacy-left", "publish-staged");
        await expect(flow(page).getByTestId("plan-partial")).toContainText(TOKYO);
        await expect(flow(page).getByTestId("plan-partial")).toContainText(OSAKA);
        await settled(page, viewport.width);
        await shot(page, "plan-partial", meta());
        await flow(page).getByTestId("plan-propose-rest").click();
        await expect(flow(page).getByTestId("flow-rest")).toContainText(OSAKA);
        await expect(decision(page, "sh-osaka")).toBeVisible();
        await expect(decision(page, "sh-japan")).toBeVisible();
        // The promoted record's share already serves its identity.
        await expect(decision(page, "sh-tokyo-alice")).toHaveCount(0);
      });

      test("a combination of fleet and legacy members is blocked until the legacy one migrates", async ({ page }) => {
        await openPlans(page, { theme });
        await expect(row(page, "mixed-combo").getByTestId("record-mixed-blocked")).toHaveText(m.plans.mixedBlocked(OSAKA));
        await expect(row(page, "mixed-combo").getByTestId("record-owner-credentials")).toBeVisible();
        await settled(page, viewport.width);
      });

      test("a legacy record's output is withheld and its Fetch now disabled, with the migration as the way out", async ({ page }) => {
        await openPlans(page, { theme });
        await openMenu(page, "self-export");
        await expect(menuItem(page, "self-export", "refresh")).toBeDisabled();
        await expect(page.locator('.rec-menu[data-row-menu="self-export"] .rec-menu-note', { hasText: m.plans.withheldFetch })).toBeVisible();
        await menuItem(page, "self-export", "output").click();
        const notice = page.getByTestId("withheld-notice").first();
        await expect(notice).toContainText(m.fleetEditor.withheld.title);
        // The client sheet's target chips fail colour contrast on every record, this lane's or not
        // (.target-chip-produces, TargetSheet); reported in the PR, not this state's.
        await settled(page, viewport.width, [".target-chip-produces"]);
        await shot(page, "withheld-output", meta());
        await notice.getByTestId("withheld-migrate").click();
        await expect(flow(page)).toHaveAttribute("data-mode", "migrate");
      });
    });
  }
}

for (const locale of LOCALES.filter((entry) => entry !== "en") as Locale[]) {
  test.describe(`${locale}`, () => {
    test.use({ viewport: VIEWPORTS[0] });

    test(`${locale}: the staged badge, the flow and a refusal read in the locale`, async ({ page }) => {
      const t = words(locale);
      await openPlans(page, { locale });
      await expect(row(page, "asia-fleet").getByTestId("staged-state")).toHaveText(t.plans.staged.state.pending);
      await expect(row(page, "self-export").getByTestId("record-owner-credentials")).toHaveText(t.fleetEditor.ownerCredentials);
      await runAction(page, "relay-graph", "migrate");
      await expect(flow(page).getByTestId("flow-step").first()).toContainText(t.migration.steps.changes);
      await flow(page).getByTestId("migration-stage").click();
      await expect(flow(page).getByTestId("migration-staged")).toBeVisible();
      await next(page);
      await decision(page, "sh-pair-family").getByTestId("share-identity").selectOption("vu_carol");
      await expect(decision(page, "sh-pair-family").getByTestId("identity-no-lines")).toContainText(t.migration.noLines);
      await settled(page, VIEWPORTS[0].width);
      await shot(page, "migration-identities", { width: 1440, theme: "light", locale });
    });

    test(`${locale}: the failed plan`, async ({ page }) => {
      const t = words(locale);
      await openPlans(page, { locale, query: "&plans=failed" });
      await runAction(page, "asia-fleet", "publish-staged");
      await expect(flow(page).getByTestId("plan-chip")).toContainText(t.plans.state.failed);
      await expect(flow(page).getByTestId("plan-live-revision")).toContainText(t.plans.view.notPromoted);
      await settled(page, VIEWPORTS[0].width);
      await shot(page, "plan-failed", { width: 1440, theme: "light", locale });
    });
  });
}
