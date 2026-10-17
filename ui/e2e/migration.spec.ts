import { expect, test, type Page } from "@playwright/test";

import { LOCALES, m, type Locale } from "./messages";
import {
  THEMES,
  VIEWPORTS,
  clippedInCells,
  flow,
  focusInSheet,
  menuItem,
  openMenu,
  openPlans,
  row,
  runAction,
  runActionByKeyboard,
  settled,
  shot,
  words,
  type Theme,
} from "./plans";

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
 *
 * And what the lane 6b reviews asked for: a sibling that fails to stage
 * blocks the flow until a retry; abandon asks first, also at the status;
 * a carried-over step's warning and its way out; the files-blocked footer;
 * the delete's own wording and danger styling; a pending delete plan in the
 * table; "Remove from" opening the members with a notice; the discard
 * dialog's heading; a plan reopened later; and the Overview's attention
 * items leading to a panel that shows the plan.
 *
 * And the second design review's: focus lands in a sheet opened from the
 * row menu; the Published cell keeps its sentences inside the cell; the
 * "Abandon and edit steps" button fits a 375 sheet (settled() checks every
 * open sheet's width); a delete over a staged record names what it drops
 * and wins over the promote in the table; abandoning a partly applied plan
 * names the combination it leaves blocked; and applied and rejected plans.
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
        // Nothing is staged yet: the sibling will be, not is.
        await expect(flow(page).getByTestId("migration-together-row")).toContainText(m.migration.stagedWithIt);
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
        // The carried-over step's warning is said again beside the record it is about.
        await expect(flow(page).getByTestId("propose-record").first().getByTestId("propose-warning")).toContainText("Regex Filter");
        await propose(page);
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.pending);
        await expect(flow(page).getByTestId("plan-view")).toBeVisible();
        // Both records the plan promotes, each with its revisions.
        await expect(flow(page).getByTestId("plan-revision")).toHaveCount(2);
        await expect(flow(page).getByTestId("plan-revision").last()).toContainText(HOME);
        await expect(flow(page).getByTestId("plan-assignment")).toHaveCount(3);
        // A share whose identity receives nothing starts open, with its left-out lines and their fix.
        const family = flow(page).locator('[data-testid="plan-share"][data-share="sh-pair-family"]');
        await expect(family.locator("details")).toHaveAttribute("open", "");
        await expect(family.getByTestId("plan-share-counts")).toContainText(m.plans.view.shareCounts(0, 0, 0, 1).split(" · ")[2]!);
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
        // The footer names the files, not "stage it first", which the files forbid.
        await expect(flow(page).getByTestId("flow-why")).toHaveText(m.migration.filesBlockShort);
        await settled(page, viewport.width);
        await shot(page, "migration-blocked-files", meta());
        // Each file opens in its own panel.
        await flow(page).getByTestId("migration-file-link").click();
        await expect(flow(page)).toHaveCount(0);
        await expect(page.locator(".pc-side-panel h2")).toHaveText(ROUTER_FILE);
      });

      test("migration refused for files named in refused.files", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "late-legacy", "migrate");
        await flow(page).getByTestId("migration-stage").click();
        await expect(flow(page).getByTestId("migration-refused")).toHaveAttribute("data-code", "fleet_file_unavailable");
        await expect(flow(page).getByTestId("migration-refused-file")).toHaveText("late-config");
        // Each refused file is named once, in the list; the sentence above it says why.
        await expect(flow(page).getByTestId("migration-refused")).toContainText(m.migration.filesBlock(1));
        expect((await flow(page).getByTestId("migration-refused").innerText()).split("late-config").length - 1).toBe(1);
        await expect(flow(page).getByTestId("flow-next")).toBeDisabled();
        // Refused for a file: staging again would be refused again.
        await expect(flow(page).getByTestId("migration-stage")).toBeDisabled();
        await expect(flow(page).getByTestId("flow-why")).toHaveText(m.migration.filesBlockShort);
        await settled(page, viewport.width);
        await shot(page, "migration-refused-files", meta());
      });

      test("abandon migration asks first, naming what it drops, then drops what the flow staged", async ({ page }) => {
        await openPlans(page, { theme });
        await stage(page, "relay-graph");
        await flow(page).getByTestId("flow-abandon").click();
        const ask = flow(page).getByTestId("abandon-confirm");
        await expect(ask.getByTestId("abandon-line")).toHaveCount(2);
        await expect(ask.getByTestId("abandon-line").first()).toContainText(RELAY);
        await expect(ask.getByTestId("abandon-line").last()).toContainText(HOME);
        await expect(ask.locator("[data-abandon-heading]")).toBeFocused();
        await settled(page, viewport.width);
        await shot(page, "migration-abandon-confirm", meta());
        // Keeping it leaves everything staged.
        await ask.getByTestId("abandon-cancel").click();
        await expect(ask).toHaveCount(0);
        await expect(flow(page).getByTestId("migration-staged")).toBeVisible();
        await flow(page).getByTestId("flow-abandon").click();
        await flow(page).getByTestId("abandon-run").click();
        await expect(flow(page)).toHaveCount(0);
        await expect(page.getByText(m.migration.abandoned(`${RELAY}, ${HOME}`))).toBeVisible();
        await expect(row(page, "relay-graph").getByTestId("staged-badge")).toHaveCount(0);
      });

      test("a fleet-bound record's delete goes through a plan that archives its own shares", async ({ page }) => {
        await openPlans(page, { theme });
        await openMenu(page, "fleet-probe-fast");
        // The menu says what a fleet-bound delete does: its shares are archived through a plan.
        await expect(menuItem(page, "fleet-probe-fast", "delete")).toHaveAttribute("title", m.plans.actions.deleteFleetTitle);
        await menuItem(page, "fleet-probe-fast", "delete").click();
        await expect(flow(page)).toHaveAttribute("data-mode", "delete");
        await expect(flow(page).getByTestId("delete-archived-share")).toContainText("/probe-erin");
        await next(page);
        // A destructive verb, styled as one, whose label holds whether or not core needs a plan.
        await expect(flow(page).getByTestId("flow-propose")).toHaveText(m.migration.proposeVerb.delete);
        await expect(flow(page).getByTestId("flow-propose")).toHaveAttribute("data-variant", "danger");
        await expect(flow(page).getByTestId("propose-proof")).toHaveText(m.migration.deleteProof);
        await shot(page, "delete-propose", meta());
        await propose(page);
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.pending);
        await expect(flow(page).getByTestId("plan-archival")).toHaveCount(1);
        await settled(page, viewport.width);
        await shot(page, "delete-through-plan", meta());
        // Back in the table, the record about to be archived says so.
        await flow(page).getByTestId("flow-close").click();
        const badge = row(page, "fleet-probe-fast").getByTestId("plan-badge");
        await expect(badge).toHaveAttribute("data-action", "delete");
        await expect(badge).toContainText(m.plans.staged.deleteState.pending);
        await settled(page, viewport.width);
        await shot(page, "delete-pending-row", meta());
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
        // It opens where the change is, and says why it opened and how to get back to the delete.
        await expect(page.locator('.ed-tab[aria-selected="true"]')).toHaveText(new RegExp(m.editor.tabs.content));
        await expect(page.getByTestId("member-removed-notice")).toContainText(m.plans.memberRemoved.title("亚洲线路 Asia lines"));
        await settled(page, viewport.width);
        await shot(page, "delete-remove-member-editor", meta());
      });

      // The store's refusal of a plain delete is read from its structured
      // reply (planCalls.test.ts, and plan-followups.spec.ts over a stale
      // list); a fleet-bound record selected alone never reaches the plain
      // dialog, which would offer a Delete the store refuses (the lane 6c
      // design review).
      test("a fleet-bound record's batch delete opens the plan flow, not a Delete the store refuses", async ({ page }) => {
        await openPlans(page, { theme });
        await row(page, "fleet-probe-fast").locator("input[type=checkbox]").first().check();
        await page.locator(".pc-batch-bar").getByRole("button", { name: new RegExp(m.actions.delete) }).click();
        await expect(page.locator(".lt-dialog")).toHaveCount(0);
        await expect(flow(page)).toHaveAttribute("data-mode", "delete");
        await settled(page, viewport.width);
        await shot(page, "delete-batch-plan", meta());
      });

      test("discard names both revisions and the plan it stales", async ({ page }) => {
        await openPlans(page, { theme });
        await expect(row(page, "asia-fleet").getByTestId("staged-badge")).toHaveAttribute("data-state", "pending");
        await runAction(page, "asia-fleet", "discard-staged");
        const dialog = page.locator(".lt-dialog");
        await expect(dialog).toContainText(m.plans.discard.title);
        // What happens, not "this also breaks 2 records": the notes are not records.
        await expect(dialog.locator(".lt-dialog-subtitle")).toHaveText(m.plans.discard.whatHappens);
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
        // Reopened later, the plan text is not in hand: the view says where it is, and the
        // steps this sitting never showed are not marked done.
        await expect(flow(page).getByTestId("plan-content-elsewhere")).toHaveText(m.plans.view.contentInApprovals);
        await expect(flow(page).locator('[data-testid="flow-step"][data-done="true"]')).toHaveCount(0);
        await settled(page, viewport.width);
        await shot(page, "plan-reopened", meta());
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
        await settled(page, viewport.width);
        await shot(page, "withheld-output", meta());
        await notice.getByTestId("withheld-migrate").click();
        await expect(flow(page)).toHaveAttribute("data-mode", "migrate");
      });

      test("a sibling that fails to stage blocks the flow until a retry stages it", async ({ page }) => {
        await openPlans(page, { theme, query: "&plans=sibling" });
        await runAction(page, "relay-graph", "migrate");
        await flow(page).getByTestId("migration-stage").click();
        await expect(flow(page).getByTestId("migration-staged")).toBeVisible();
        const failed = flow(page).getByTestId("migration-sibling-failed");
        await expect(failed).toContainText(HOME);
        await expect(failed.getByTestId("migration-retry")).toBeEnabled();
        await expect(flow(page).getByTestId("flow-next")).toBeDisabled();
        if (viewport.width < 480) {
          // Below 480px the retry sits under the reason rather than squeezing it into a narrow column.
          const body = await failed.locator(".pc-notice-body").boundingBox();
          const retry = await failed.getByTestId("migration-retry").boundingBox();
          expect(retry!.y).toBeGreaterThanOrEqual(body!.y + body!.height - 1);
        }
        await settled(page, viewport.width);
        await shot(page, "migration-sibling-failed", meta());
        await failed.getByTestId("migration-retry").click();
        await expect(failed).toHaveCount(0);
        await expect(flow(page).locator('[data-testid="migration-together-row"][data-record="home-graph"]')).toContainText(new RegExp(m.migration.stagedAs("[0-9a-f]+")));
        await expect(flow(page).getByTestId("flow-next")).toBeEnabled();
      });

      test("abandon is offered at the status too, and names the plan it stales", async ({ page }) => {
        await openPlans(page, { theme });
        await stage(page, "relay-graph");
        await next(page);
        await decision(page, "sh-pair-family").getByTestId("share-archive").check();
        await next(page);
        await propose(page);
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.pending);
        await flow(page).getByTestId("flow-abandon").click();
        const ask = flow(page).getByTestId("abandon-confirm");
        await expect(ask.getByTestId("abandon-line").last()).toContainText("apr_");
        await settled(page, viewport.width);
        await shot(page, "migration-abandon-status", meta());
        await ask.getByTestId("abandon-run").click();
        await expect(flow(page)).toHaveCount(0);
        await expect(row(page, "relay-graph").getByTestId("staged-badge")).toHaveCount(0);
      });

      test("a carried-over step's warning: abandon, then the record's steps open in its editor", async ({ page }) => {
        await openPlans(page, { theme });
        await stage(page, "relay-graph");
        const warnings = flow(page).getByTestId("migration-warnings");
        await expect(warnings.getByTestId("migration-warning").first()).toContainText("Regex Filter");
        await expect(warnings).toContainText(m.migration.warningsFix);
        // A short label that fits a 375 sheet; the record is named in the notice and in the button's name.
        await expect(warnings.getByTestId("migration-fix-steps").first()).toHaveText(m.migration.abandonAndEditShort);
        await expect(warnings.getByTestId("migration-fix-steps").first()).toHaveAttribute("aria-label", m.migration.abandonAndEditFor(RELAY));
        await expect(warnings).toContainText(RELAY);
        await settled(page, viewport.width);
        await shot(page, "migration-fix-steps", meta());
        await warnings.getByTestId("migration-fix-steps").first().click();
        await expect(flow(page).getByTestId("abandon-confirm")).toContainText(m.migration.abandonThenEdit(RELAY));
        await flow(page).getByTestId("abandon-run").click();
        await expect(flow(page)).toHaveCount(0);
        await expect(page.locator("#editor-title")).toBeVisible();
        await expect(page.locator('.ed-tab[aria-selected="true"]')).toHaveText(new RegExp(m.editor.tabs.operations));
        await settled(page, viewport.width);
      });

      test("an Overview attention item opens a panel and page that show the failed plan", async ({ page }) => {
        await openPlans(page, { view: "overview", theme, query: "&plans=failed" });
        const item = page.locator('[data-attention^="plan:"]');
        await expect(item).toContainText(m.plans.attention.failed("亚洲线路 Asia lines"));
        await item.getByRole("button", { name: m.attention.open }).click();
        const panel = page.locator(".pc-side-panel");
        const state = panel.getByTestId("record-plan-state");
        await expect(state.getByTestId("plan-chip")).toContainText(m.plans.state.failed);
        await expect(state.getByTestId("plan-live-revision")).toContainText(m.plans.view.notPromoted);
        await expect(state.getByTestId("staged-proof")).toBeVisible();
        await expect(state.getByTestId("record-plan-decoy")).toContainText(m.fleet.decoy(1));
        await settled(page, viewport.width);
        await shot(page, "overview-failed-panel", meta());
        await panel.getByRole("button", { name: m.record.openPage }).click();
        await expect(page.getByTestId("record-plan-state").getByTestId("plan-chip")).toContainText(m.plans.state.failed);
        // The page fades in; axe reads a half-faded colour as low contrast.
        await page.locator(".record-page").evaluate((el) => Promise.allSettled(el.getAnimations({ subtree: true }).map((animation) => animation.finished)));
        await settled(page, viewport.width);
        await shot(page, "overview-failed-page", meta());
      });

      test("a combination core refuses says so in its panel, with the member's migration", async ({ page }) => {
        await openPlans(page, { view: "overview", theme });
        const item = page.locator('[data-attention="mixed:mixed-combo"]');
        await item.getByRole("button", { name: m.attention.open }).click();
        const mixed = page.locator(".pc-side-panel").getByTestId("record-plan-mixed");
        await expect(mixed).toContainText(OSAKA);
        await mixed.getByRole("button", { name: m.migration.migrateMember(OSAKA) }).click();
        await expect(flow(page)).toHaveAttribute("data-mode", "migrate");
      });

      test("a delete over a staged record names what it drops, and the table says delete over promote", async ({ page }) => {
        await openPlans(page, { theme });
        await runAction(page, "asia-fleet", "delete");
        const drops = flow(page).getByTestId("delete-drops");
        // What Discard names, the delete names too: the staged revision and the plan over it.
        await expect(drops.getByTestId("delete-drop")).toHaveCount(2);
        const droppedLine = m.migration.deleteDropsStaged("@@").replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace("@@", "[0-9a-f]+…");
        await expect(drops.getByTestId("delete-drop").first()).toHaveText(new RegExp(`^${droppedLine}$`));
        await expect(drops.getByTestId("delete-drop").last()).toHaveText(m.migration.deleteStalesPlan("apr_0001asiafl"));
        await settled(page, viewport.width);
        await shot(page, "delete-staged-drops", meta());
        await flow(page).getByTestId("delete-remove-member").click();
        await page.getByRole("button", { name: m.editor.save, exact: true }).click();
        await expect(page.locator("#editor-title")).toHaveCount(0);
        await runAction(page, "asia-fleet", "delete");
        await expect(flow(page).getByTestId("delete-blocked")).toHaveCount(0);
        await next(page);
        await propose(page);
        await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state.pending);
        // The plan archives every share of the record; an archived share answers nothing, so none is listed as answering the decoy.
        await expect(flow(page).getByTestId("plan-archival")).toHaveCount(2);
        await expect(flow(page).getByTestId("plan-unbound")).toHaveCount(0);
        await flow(page).getByTestId("flow-close").click();
        // A record about to be archived does not read as one about to be promoted.
        const badge = row(page, "asia-fleet").getByTestId("plan-badge");
        await expect(badge).toHaveAttribute("data-action", "delete");
        await expect(badge).toContainText(m.plans.staged.deleteState.pending);
        await expect(row(page, "asia-fleet").getByTestId("staged-badge")).toHaveCount(0);
        await settled(page, viewport.width);
        await shot(page, "delete-staged-row", meta());
      });

      test("abandoning a partly applied plan names the combination it leaves blocked", async ({ page }) => {
        await openPlans(page, { theme, query: "&plans=partial" });
        await runAction(page, "legacy-left", "publish-staged");
        await expect(flow(page).getByTestId("plan-partial")).toBeVisible();
        // The line under the button no longer says everything keeps serving.
        await expect(flow(page).getByTestId("flow-abandon-note")).toHaveText(m.migration.abandonNoteBlocked("Japan combination"));
        await flow(page).getByTestId("flow-abandon").click();
        const ask = flow(page).getByTestId("abandon-confirm");
        await expect(ask.getByTestId("abandon-line").first()).toContainText(OSAKA);
        await expect(ask.getByTestId("abandon-line").nth(1)).toHaveText(m.migration.abandonBlocked("Japan combination", "Japan family link"));
        await expect(ask.getByTestId("abandon-finish")).toHaveText(m.migration.abandonFinishRest);
        await settled(page, viewport.width);
        await shot(page, "migration-abandon-partial", meta());
      });

      test("a plan the approver applied, and one the approver rejected", async ({ page }) => {
        for (const decided of ["applied", "rejected"] as const) {
          await openPlans(page, { theme, query: `&plans=${decided}` });
          await runAction(page, "asia-fleet", "share-identities");
          await decision(page, "sh-asia-open").getByTestId("share-identity").selectOption("vu_alice");
          await next(page);
          await propose(page);
          await expect(flow(page).getByTestId("plan-chip")).toContainText(m.plans.state[decided]);
          if (decided === "applied") {
            await expect(flow(page).getByTestId("plan-status")).toContainText(m.plans.view.appliedNote);
            await expect(flow(page).getByTestId("plan-open-approvals")).toHaveCount(0);
          } else {
            await expect(flow(page).getByTestId("plan-status")).toContainText(m.plans.view.rejectedNote);
            await expect(flow(page).getByTestId("plan-reason")).toContainText("rejected by the approver");
            await expect(flow(page).getByTestId("plan-propose-again")).toBeVisible();
          }
          await settled(page, viewport.width);
          await shot(page, `plan-${decided}`, meta());
        }
      });
    });
  }
}

/*
 * Focus after a row menu item opens a sheet: in it, at both widths, chosen
 * by keyboard. The menu put focus back on its trigger after the sheet had
 * taken it, so the next Tab went to a row of the table, under the scrim at
 * 375 (the second lane 6b design review).
 */
for (const viewport of VIEWPORTS) {
  test.describe(`${viewport.width} focus`, () => {
    test.use({ viewport });

    const cases = [
      { record: "relay-graph", action: "migrate", mode: "migrate" },
      { record: "fleet-probe-fast", action: "delete", mode: "delete" },
      { record: "asia-fleet", action: "share-identities", mode: "assign" },
      { record: "asia-fleet", action: "publish-staged", mode: "publish" },
    ];
    for (const entry of cases) {
      test(`${entry.action} from the row menu puts focus in the sheet, and Escape gives it back to the menu`, async ({ page }) => {
        await openPlans(page);
        await runActionByKeyboard(page, entry.record, entry.action);
        await expect(flow(page)).toHaveAttribute("data-mode", entry.mode);
        await expect.poll(() => focusInSheet(page)).toBe(true);
        // Still there once everything the menu scheduled has run.
        await page.waitForTimeout(300);
        expect(await focusInSheet(page)).toBe(true);
        await page.keyboard.press("Tab");
        expect(await focusInSheet(page)).toBe(true);
        await shot(page, `focus-${entry.action}`, { width: viewport.width, theme: "light" });
        await page.keyboard.press("Escape");
        await expect(flow(page)).toHaveCount(0);
        await expect(page.locator(`.rec-menu-wrap[data-row-menu="${entry.record}"] button`).first()).toBeFocused();
      });
    }

    test("discard from the row menu puts focus in its dialog", async ({ page }) => {
      await openPlans(page);
      await runActionByKeyboard(page, "asia-fleet", "discard-staged");
      const dialog = page.locator(".lt-dialog");
      await expect(dialog).toBeVisible();
      await page.waitForTimeout(300);
      expect(await page.evaluate(() => !!document.activeElement?.closest(".lt-dialog, [role=alertdialog], [role=dialog]"))).toBe(true);
    });
  });
}

/*
 * The Published cell keeps its sentences inside the cell: the chassis floors
 * a sub-line at 126px, wider than the column at 1440, and the cell clips.
 */
for (const locale of LOCALES) {
  for (const theme of THEMES) {
    test(`${locale} ${theme}: the Published cell's sentences stay inside the cell at 1440`, async ({ page }) => {
      await page.setViewportSize(VIEWPORTS[0]);
      await openPlans(page, { locale, theme });
      await expect(row(page, "asia-fleet").getByTestId("record-decoy")).toBeVisible();
      expect(await clippedInCells(page, "td.rec-published")).toEqual([]);
      expect(await clippedInCells(page, "td.rec-kind-cell")).toEqual([]);
      await shot(page, "records-published-cell", { width: 1440, theme, locale });
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
