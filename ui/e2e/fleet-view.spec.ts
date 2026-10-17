import { expect, test, type Page } from "@playwright/test";

import { LOCALES, m, type Locale } from "./messages";
import { THEMES, VIEWPORTS, openPlans, settled, shot, words, type Theme } from "./plans";

/**
 * The Fleet view (design 28 S2, lane 6b; s2-plan section 11): which
 * identities receive what from each fleet-bound record, previewed one
 * identity at a time and never every identity of every record at once. The
 * 6a state set as it applies here, found by named elements, at 1440 and 375,
 * light and dark, with axe; then the three locales.
 *
 * States, by test:
 *   previews       the records, the proof line, Preview all, reason chips,
 *                  an opened identity's exclusions with core's fixes, the
 *                  share form and bind_selection (unavailable: decision 8)
 *   decoy          shares naming no identity, with the assignment plan
 *   staged         a staged revision previewed beside the live one, its plan
 *   loading        ?state=slow
 *   empty          ?state=empty, no fleet record
 *   error          ?fleet=error, the catalogue unavailable
 *   stale          ?fleet=stale, the snapshot older than the catalogue
 *   disabled       ?state=readonly, the plan verbs withheld
 *   denied         ?fleet=denied, no vpncore:read
 *   expired        an expired identity's row
 *   flagged        probe unavailable on this server
 *   too large      snapshot_too_large with the computed figure
 *   transformer    fleet_response_chain_unavailable
 *   dropped        lines with no client shape core can bind
 *   long names     CJK and long Latin names, no horizontal scroll
 *   promoted       a failed apply that the list shows promoted
 *   live/staged    the toggle asks again for every open row
 *   inactive       an expired identity says the decoy collapsed, and
 *                  offers no share
 *   narrow         at 375 a chosen record brings its detail into view
 *   arrows         an arrow key walks the records and keeps focus there;
 *                  Enter, Space or "Show the identities" bring the detail
 *   preview all    a revision switch after Preview all asks again for
 *                  every row, open or not
 */

const ASIA = "亚洲线路 Asia lines";

async function choose(page: Page, recordId: string): Promise<void> {
  await page.locator(`[data-testid="fleet-record"][data-record="${recordId}"]`).click();
  await expect(page.locator(`[data-testid="fleet-record"][data-record="${recordId}"]`)).toHaveClass(/is-selected/);
}

function identity(page: Page, id: string) {
  return page.locator(`[data-testid="fleet-identity"][data-identity="${id}"]`);
}

for (const viewport of VIEWPORTS) {
  for (const theme of THEMES) {
    const meta = (t: Theme = theme) => ({ width: viewport.width, theme: t });
    test.describe(`${viewport.width} ${theme}`, () => {
      test.use({ viewport });

      test("previews: records, proof line, reason chips, exclusions with fixes, the share form and bind_selection", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme });
        await expect(page.getByTestId("fleet-record")).not.toHaveCount(0);
        await choose(page, "asia-fleet");
        const proof = page.getByTestId("fleet-proof");
        await expect(proof).toContainText(m.fleetEditor.proof.catalogue("lcv1-0a63b2c4…"));
        await expect(proof).toContainText(m.fleetEditor.proof.probeUnavailable);
        await expect(proof).toContainText(m.fleet.identityCount(7));
        await page.getByTestId("fleet-preview-all").click();
        await expect(identity(page, "vu_bob").getByTestId("fleet-identity-result")).toHaveText(m.fleet.counts(4, 17));
        await expect(identity(page, "vu_bob").locator('[data-testid="fleet-reason-chip"][data-reason="not_bound"]')).toBeVisible();
        await identity(page, "vu_alice").getByTestId("fleet-identity-toggle").click();
        const body = identity(page, "vu_alice").getByTestId("fleet-identity-body");
        await expect(body.locator('[data-testid="fleet-exclusion-group"][data-reason="credential_not_applied"]')).toContainText(m.fleetEditor.bind.reasons.credential_not_applied);
        await body.getByTestId("fleet-bind-selection").click();
        await expect(body.getByTestId("fleet-action-note")).toContainText(m.plans.bind.unavailable);
        // Asking again would answer the same: the action stops offering itself.
        await expect(body.getByTestId("fleet-bind-selection")).toBeDisabled();
        await expect(body).toContainText(m.fleet.narrowingNote);
        await settled(page, viewport.width);
        await shot(page, "fleet-previews", meta());
      });

      test("decoy shares and the staged revision with its plan", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme });
        await choose(page, "asia-fleet");
        await expect(page.getByTestId("fleet-decoy")).toContainText(m.fleet.decoy(1));
        await expect(page.getByTestId("fleet-staged").getByTestId("staged-proof")).toBeVisible();
        await expect(page.getByTestId("fleet-staged").getByTestId("plan-chip")).toContainText(m.plans.state.pending);
        await page.getByTestId("fleet-revision-staged").check();
        await expect(page.getByTestId("fleet-proof")).toContainText(m.fleet.revisionPreviewed("").trim());
        await settled(page, viewport.width);
        await shot(page, "fleet-staged-decoy", meta());
        await page.getByTestId("fleet-assign").click();
        await expect(page.getByTestId("plan-flow")).toHaveAttribute("data-mode", "assign");
      });

      test("loading", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme, query: "&state=slow" });
        await expect(page.getByTestId("fleet-loading")).toBeVisible();
        await shot(page, "fleet-loading", meta());
      });

      test("empty: no fleet record yet", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme, query: "&state=empty" });
        await expect(page.getByTestId("fleet-empty")).toContainText(m.fleet.emptyTitle);
        await settled(page, viewport.width);
        await shot(page, "fleet-empty", meta());
      });

      test("error: the catalogue unavailable", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme, query: "&fleet=error" });
        await choose(page, "all-fleet-lines");
        await expect(page.getByTestId("fleet-catalogue-error")).toContainText(m.fleet.catalogueUnavailable);
        await identity(page, "vu_alice").getByTestId("fleet-identity-toggle").click();
        await expect(identity(page, "vu_alice").getByTestId("fleet-preview-error")).toHaveText(m.fleet.catalogueUnavailable);
        await settled(page, viewport.width);
        await shot(page, "fleet-error", meta());
      });

      test("stale: the snapshot older than the catalogue", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme, query: "&fleet=stale" });
        await choose(page, "all-fleet-lines");
        await expect(page.getByTestId("fleet-stale")).toBeVisible();
        await settled(page, viewport.width);
      });

      test("disabled: a read session reads previews and is offered no plan verb", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme, query: "&state=readonly" });
        await choose(page, "asia-fleet");
        // A read session cannot read the share list either, and says so.
        await expect(page.getByTestId("fleet-shares")).toContainText(m.fleet.sharesUnread);
        await identity(page, "vu_alice").getByTestId("fleet-identity-toggle").click();
        await expect(identity(page, "vu_alice").getByTestId("fleet-bind-selection")).toBeDisabled();
        await settled(page, viewport.width);
        await shot(page, "fleet-readonly", meta());
      });

      test("permission denied: no vpncore:read", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme, query: "&fleet=denied" });
        await expect(page.getByTestId("fleet-denied")).toContainText(m.fleet.deniedBody);
        await expect(page.getByTestId("fleet-preview-all")).toBeDisabled();
        await settled(page, viewport.width);
        await shot(page, "fleet-denied", meta());
      });

      test("an expired identity says its share answers the decoy, collapsed, and offers no share", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme });
        await choose(page, "asia-fleet");
        await page.getByTestId("fleet-preview-all").click();
        // Collapsed, the row says the decoy rather than a count the identity would never receive.
        await expect(identity(page, "vu_dave").getByTestId("fleet-identity-result")).toHaveText(m.fleet.answersDecoy(m.fleetEditor.identity.status.expired));
        await identity(page, "vu_dave").getByTestId("fleet-identity-toggle").click();
        await expect(identity(page, "vu_dave").getByTestId("fleet-identity-inactive")).toHaveText(m.fleetEditor.bind.inactive.expired);
        await expect(identity(page, "vu_dave").getByTestId("fleet-create-share")).toBeDisabled();
        await expect(identity(page, "vu_dave").getByTestId("fleet-create-blocked")).toHaveText(m.fleet.createInactive(m.fleetEditor.identity.status.expired));
        await settled(page, viewport.width);
        await shot(page, "fleet-inactive", meta());
      });

      test("live and staged: switching asks again for every open row", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme });
        await choose(page, "asia-fleet");
        await identity(page, "vu_alice").getByTestId("fleet-identity-toggle").click();
        const result = identity(page, "vu_alice").getByTestId("fleet-identity-result");
        await expect(result).toHaveText(/\d/);
        const live = await result.textContent();
        await page.getByTestId("fleet-revision-staged").check();
        // The open row shows the staged figures without being closed and opened again.
        await expect(result).toHaveText(/\d/);
        await expect(result).not.toHaveText(live ?? "");
        await expect(identity(page, "vu_alice").getByTestId("fleet-reason-chip").first()).toBeVisible();
        await settled(page, viewport.width);
        await shot(page, "fleet-staged-row", meta());
        await page.getByTestId("fleet-revision-live").check();
        await expect(result).toHaveText(live ?? "");
      });

      test("too large, transformer refused and dropped lines", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme });
        await choose(page, "huge-fleet");
        await expect(page.getByTestId("fleet-too-large")).toContainText("2953");
        await choose(page, "fleet-with-rewrite");
        await identity(page, "vu_oncall").getByTestId("fleet-identity-toggle").click();
        await expect(identity(page, "vu_oncall").getByTestId("fleet-preview-error")).toHaveText(m.fleetEditor.transformer.title);
        await choose(page, "all-fleet-lines");
        await identity(page, "vu_oncall").getByTestId("fleet-identity-toggle").click();
        await expect(page.getByTestId("fleet-dropped")).toBeVisible();
        await settled(page, viewport.width);
        await shot(page, "fleet-dropped", meta());
      });

      test("long names stay in the frame", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme });
        await choose(page, "asia-fleet");
        await expect(identity(page, "vu_oncall")).toContainText("运维值班手机-shenzhen-office-oncall-rotation-device-07");
        await identity(page, "vu_oncall").getByTestId("fleet-identity-toggle").click();
        await settled(page, viewport.width);
      });

      test("choosing a record brings its detail into view", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme });
        await choose(page, "fleet-probe-fast");
        const detail = page.getByTestId("fleet-detail");
        await expect(detail).toContainText("Lines that passed a probe today");
        if (viewport.width < 960) {
          await expect(detail).toBeFocused();
          await expect(detail).toBeInViewport({ ratio: 0.2 });
        }
        await identity(page, "vu_alice").getByTestId("fleet-identity-toggle").click();
        await expect(identity(page, "vu_alice").getByTestId("fleet-identity-result")).toHaveText(/\d/);
        await settled(page, viewport.width);
        await shot(page, "fleet-chosen", meta());
      });

      test("arrow keys walk the records and keep focus there; Enter and the skip button bring the detail", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme });
        const radios = page.locator('input[name="fleet-record"]');
        await radios.first().focus();
        const before = await page.evaluate(() => window.scrollY);
        await page.keyboard.press("ArrowDown");
        await page.keyboard.press("ArrowDown");
        // Two records further down, focus still in the group, and the page where it was.
        await expect(radios.nth(2)).toBeFocused();
        await expect(radios.nth(2)).toBeChecked();
        expect(await page.evaluate(() => window.scrollY)).toBe(before);
        await shot(page, "fleet-arrows", meta());
        const detail = page.getByTestId("fleet-detail");
        if (viewport.width < 960) {
          await page.keyboard.press("Enter");
          await expect(detail).toBeFocused();
          await expect(detail).toBeInViewport({ ratio: 0.2 });
          // Back in the list, one Tab past it reaches the button that does the same.
          await radios.nth(2).focus();
          await page.keyboard.press("Tab");
          const skip = page.getByTestId("fleet-skip");
          await expect(skip).toBeFocused();
          await expect(skip).toBeVisible();
          await expect(skip).toHaveText(m.fleet.showDetail(await radios.nth(2).getAttribute("aria-label") ?? ""));
          await settled(page, viewport.width);
          await shot(page, "fleet-skip", meta());
          await page.keyboard.press("Enter");
          await expect(detail).toBeFocused();
        } else {
          await expect(page.getByTestId("fleet-skip")).toBeHidden();
        }
      });

      test("after Preview all, a revision switch asks again for every row, open or not", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme });
        await choose(page, "asia-fleet");
        await page.getByTestId("fleet-preview-all").click();
        const bob = identity(page, "vu_bob").getByTestId("fleet-identity-result");
        await expect(bob).toHaveText(m.fleet.counts(4, 17));
        await page.getByTestId("fleet-revision-staged").check();
        // A collapsed row keeps a count, for the staged revision now.
        await expect(bob).toHaveText(/\d/);
        await expect(identity(page, "vu_bob").getByTestId("fleet-reason-chip").first()).toBeVisible();
        await settled(page, viewport.width);
      });

      test("a failed apply that the list shows promoted", async ({ page }) => {
        await openPlans(page, { view: "fleet", theme, query: "&plans=promoted" });
        await choose(page, "asia-fleet");
        await expect(page.getByTestId("fleet-staged").getByTestId("plan-chip")).toContainText(m.plans.state.failed);
        await expect(page.getByTestId("fleet-staged").getByTestId("plan-live-revision")).toContainText(m.plans.view.wasPromoted);
        await settled(page, viewport.width);
        await shot(page, "fleet-failed-promoted", meta());
      });
    });
  }
}

for (const locale of LOCALES.filter((entry) => entry !== "en") as Locale[]) {
  test(`${locale}: the Fleet view reads in the locale`, async ({ page }) => {
    const t = words(locale);
    await page.setViewportSize(VIEWPORTS[0]);
    await openPlans(page, { view: "fleet", locale });
    await choose(page, "asia-fleet");
    await expect(page.getByTestId("fleet-decoy")).toContainText(t.fleet.decoy(1).slice(0, 4));
    await page.getByTestId("fleet-preview-all").click();
    await expect(identity(page, "vu_bob").locator('[data-testid="fleet-reason-chip"][data-reason="not_bound"]')).toContainText(t.fleetEditor.bind.reasons.not_bound);
    await settled(page, VIEWPORTS[0].width);
    await shot(page, "fleet-previews", { width: 1440, theme: "light", locale });
  });
}
