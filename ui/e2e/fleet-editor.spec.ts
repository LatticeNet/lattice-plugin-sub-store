import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

import { TABLES, m, type Locale } from "./messages";

/**
 * The fleet record editor (design 28 S2, lane 6a; s2-plan section 11): every
 * state the UI acceptance rule names, each found by a named element and never
 * by English typed here, at the two widths the design holds it to, in light
 * and dark, with axe finding no serious or critical violation on each; the
 * three locales; and the keyboard path through adding, moving and removing a
 * condition.
 *
 * The harness plays the 0.18 manifest with `?manifest=s2` and serves the
 * fleet fixture (dev/fleetFixtures.ts) with `?fixture=fleet`; `?fleet=`
 * picks a catalogue state (dev/fleetHost.ts).
 *
 * States, by test:
 *   loading              ?fleet=slow, the skeleton holds
 *   empty                ?fleet=empty, no line matches the selection
 *   error                ?fleet=error, catalogue_unavailable
 *   stale                ?fleet=stale, the snapshot predates the catalogue
 *   disabled             ?state=readonly, the selection is read only
 *   permission denied    ?fleet=denied, no vpncore:read: fleet_preview,
 *                        identities and bind.preview are out of reach
 *   expired              an expired identity's preview
 *   flagged              a probe condition while probe data is unavailable
 *   too large            ?fleet=large, snapshot_too_large with the figure
 *   transformer refused  fleet_response_chain_unavailable
 *   shadowsocks dropped  lines core cannot bind, named
 *   long names           CJK and long Latin labels, no horizontal scroll
 */

const CLOCK = "2026-10-09T08:00:00Z";
const VIEWPORTS = [
  { width: 1440, height: 900 },
  { width: 375, height: 812 },
] as const;

const ASIA = "亚洲线路 Asia lines";
const EVERY = "Every fleet line";
const PROBE = "Lines that passed a probe today";
const REWRITE = "Fleet lines with a document rewrite";
const COMBINATION = "Asia combination with a provider";
const LEGACY = "This fleet (legacy export)";

interface Open {
  query?: string;
  locale?: Locale;
  theme?: "light" | "dark";
}

async function openList(page: Page, options: Open = {}): Promise<void> {
  const locale = options.locale ?? "en";
  await page.goto(`/dev.html?view=records&fixture=fleet&manifest=s2&clock=${CLOCK}&locale=${locale}&theme=${options.theme ?? "light"}${options.query ?? ""}`);
  await expect(page.locator("html")).toHaveAttribute("lang", locale);
  await page.locator(".row-open").first().waitFor();
}

/** Row, then the side panel's Edit, then the Content tab: the path an operator takes. */
async function openRecord(page: Page, name: string, options: Open = {}): Promise<void> {
  const t = TABLES[options.locale ?? "en"];
  await openList(page, options);
  await page.locator(".row-open", { hasText: name }).first().click();
  await page.locator(".pc-side-panel").getByRole("button", { name: t.actions.edit }).click();
  await page.locator("#editor-title").waitFor();
  await page.getByRole("tab", { name: new RegExp(`^${t.editor.tabs.content}`) }).click();
}

async function seriousViolations(page: Page): Promise<string[]> {
  const result = await new AxeBuilder({ page }).exclude(".dev-bar").analyze();
  return result.violations
    .filter((violation) => violation.impact === "serious" || violation.impact === "critical")
    .map((violation) => `${violation.id}: ${violation.nodes.map((node) => node.target.join(" ")).slice(0, 3).join(", ")}`);
}

const docWidth = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth);

async function previewIdentity(page: Page, name: string): Promise<void> {
  await page.getByTestId("identity-row").filter({ hasText: name }).locator("input[type=radio]").check();
  await page.getByTestId("bind-preview").waitFor();
}

for (const viewport of VIEWPORTS) {
  for (const theme of ["light", "dark"] as const) {
    test.describe(`${viewport.width} ${theme}`, () => {
      test.use({ viewport });

      test("a fleet record's selection: markers, counts, proof line, sample and rules", async ({ page }) => {
        await openRecord(page, ASIA, { theme });
        const selection = page.getByTestId("fleet-selection");
        await expect(page.getByTestId("source-fleet")).toHaveAttribute("aria-checked", "true");
        await expect(page.getByTestId("fleet-identity-proof")).toHaveText(m.fleetEditor.identityPerShare);
        // The legacy export is accounted for, and cannot be chosen.
        await expect(page.getByTestId("source-vpn-core")).toHaveAttribute("aria-disabled", "true");
        await expect(page.getByTestId("source-vpn-core")).toContainText(m.fleetEditor.legacyRefused);
        const proof = page.getByTestId("fleet-proof");
        await expect(proof).toContainText(m.fleetEditor.proof.catalogue("lcv1-0a63b2c4…"));
        await expect(proof).toContainText(m.fleetEditor.proof.lines(117));
        await expect(proof).toContainText(m.fleetEditor.proof.pushable(11));
        await expect(proof).toContainText(m.fleetEditor.proof.probeUnavailable);
        const markers = selection.getByTestId("fleet-step-marker");
        await expect(markers.nth(0)).toHaveText(m.fleetEditor.pushed);
        await expect(markers.nth(1)).toHaveText(m.fleetEditor.local);
        await expect(selection.getByTestId("fleet-step-count").nth(0)).toHaveText(m.fleetEditor.removed(117, 23));
        await expect(selection.getByTestId("fleet-step-count").nth(1)).toHaveText(m.fleetEditor.removed(23, 21));
        await expect(selection.getByTestId("fleet-sample-row")).toHaveCount(8);
        await expect(page.getByTestId("fleet-probe-flagged")).toHaveCount(0);
        await expect(page.getByTestId("rule-probe")).toBeChecked();
        await expect(page.getByTestId("rule-usage-amount")).toHaveValue("50");
        expect(await docWidth(page)).toBeLessThanOrEqual(viewport.width);
        expect(await seriousViolations(page)).toEqual([]);
      });

      test("an identity's preview, its exclusions and the line drawer", async ({ page }) => {
        await openRecord(page, ASIA, { theme });
        await previewIdentity(page, "alice-laptop");
        const preview = page.getByTestId("bind-preview");
        await expect(preview.getByTestId("bind-entry").first()).toBeVisible();
        const groups = preview.getByTestId("bind-exclusion-group");
        await expect(groups.filter({ hasText: m.fleetEditor.bind.reasons.credential_not_applied })).toHaveCount(1);
        await expect(preview.getByTestId("bind-revision")).toBeVisible();
        await preview.getByTestId("bind-entry").first().getByRole("button").click();
        const drawer = page.getByTestId("line-drawer");
        await expect(drawer).toBeVisible();
        await expect(drawer.getByTestId("line-probe")).toContainText(m.fleetEditor.line.noProbe);
        expect(await seriousViolations(page)).toEqual([]);
        await page.keyboard.press("Escape");
        await expect(drawer).toHaveCount(0);
        expect(await docWidth(page)).toBeLessThanOrEqual(viewport.width);
      });

      test("expired: an expired identity's share answers the decoy, and says so", async ({ page }) => {
        await openRecord(page, ASIA, { theme });
        await previewIdentity(page, "dave-contractor");
        await expect(page.getByTestId("bind-inactive")).toHaveText(m.fleetEditor.bind.inactive.expired);
        await expect(page.getByTestId("identity-row").filter({ hasText: "dave-contractor" }).getByTestId("identity-status")).toHaveText(
          m.fleetEditor.identity.status.expired,
        );
        expect(await seriousViolations(page)).toEqual([]);
      });

      test("an identity bound to no selected line: not_bound, and the decoy", async ({ page }) => {
        await openRecord(page, ASIA, { theme });
        await previewIdentity(page, "carol 张");
        await expect(page.getByTestId("bind-refused")).toContainText(m.fleetEditor.bind.refused.no_includable_line);
        await expect(page.getByTestId("bind-exclusion-group").filter({ hasText: m.fleetEditor.bind.reasons.not_bound })).toHaveCount(1);
        await expect(page.getByTestId("bind-open-users")).toBeVisible();
        expect(await seriousViolations(page)).toEqual([]);
      });
    });
  }

  test.describe(`${viewport.width} states`, () => {
    test.use({ viewport });

    test("loading: the skeleton holds while the catalogue is read", async ({ page }) => {
      await openRecord(page, ASIA, { query: "&fleet=slow" });
      await expect(page.getByTestId("fleet-selection").getByRole("status", { name: m.fleetEditor.loading })).toBeVisible();
      await expect(page.getByTestId("fleet-selection")).toHaveAttribute("aria-busy", "true");
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("empty: no line matches the selection", async ({ page }) => {
      await openRecord(page, ASIA, { query: "&fleet=empty" });
      await expect(page.getByTestId("fleet-empty")).toContainText(m.fleetEditor.empty.title);
      await expect(page.getByTestId("fleet-sample")).toHaveCount(0);
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("error: the catalogue is unavailable, with a retry", async ({ page }) => {
      await openRecord(page, ASIA, { query: "&fleet=error" });
      const error = page.getByTestId("fleet-error");
      await expect(error).toContainText(m.fleetEditor.error.title);
      await expect(error.getByRole("button", { name: m.fleetEditor.retry })).toBeVisible();
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("stale: the served snapshot predates the catalogue", async ({ page }) => {
      await openRecord(page, ASIA, { query: "&fleet=stale" });
      await expect(page.getByTestId("fleet-stale")).toContainText(m.fleetEditor.stale("lcv1-9f1e2d3c…", "lcv1-0a63b2c4…"));
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("disabled: a read-scoped session cannot open the editor, and is told why", async ({ page }) => {
      // The record editor is a write surface: a session without the write
      // methods gets Edit disabled with the reason beside it, and the
      // selection's read-only form is what an editor shows if the write
      // methods leave the handshake while it is open.
      await openList(page, { query: "&state=readonly" });
      await page.locator(".row-open", { hasText: ASIA }).first().click();
      const edit = page.locator(".pc-side-panel").getByRole("button", { name: m.actions.edit });
      await expect(edit).toBeDisabled();
      await expect(edit).toHaveAttribute("title", m.record.editBlocked);
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("permission denied: no vpncore:read, so no catalogue, identities or bind preview", async ({ page }) => {
      await openRecord(page, ASIA, { query: "&fleet=denied" });
      await expect(page.getByTestId("fleet-denied")).toContainText(m.fleetEditor.denied.title);
      await expect(page.getByTestId("identity-denied")).toContainText(m.fleetEditor.denied.title);
      // The steps are still the operator's to read and edit.
      await expect(page.getByTestId("fleet-step")).toHaveCount(2);
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("flagged: a probe condition while this server has no probe data", async ({ page }) => {
      await openRecord(page, PROBE);
      await expect(page.getByTestId("fleet-probe-flagged")).toHaveText(m.fleetEditor.probeFlagged);
      await expect(page.getByTestId("predicate-problem")).toHaveText(m.fleetEditor.probeFlagged);
      // Save says why, before the round trip refuses it (fleet_probe_unavailable).
      await expect(page.locator(".field-error-jump")).toHaveText(m.fleetEditor.validation.probe(1));
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("too large: the selection's snapshot is over core's bound, with the figure", async ({ page }) => {
      await openRecord(page, ASIA, { query: "&fleet=large" });
      const notice = page.getByTestId("fleet-too-large");
      await expect(notice).toContainText(m.fleetEditor.tooLarge.title);
      await expect(notice).toContainText("snapshot_too_large");
      await expect(notice).toContainText(/2[,\s]?\d{3}/);
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("transformer refused: a document rewrite on a fleet record", async ({ page }) => {
      await openRecord(page, REWRITE);
      await expect(page.getByTestId("fleet-transformer")).toContainText(m.fleetEditor.transformer.title);
      // Render refuses the record (fleet_response_chain_unavailable); the node
      // preview leaves response steps out, so the editor says it from the chain.
      await expect(page.getByTestId("fleet-transformer")).toContainText("fleet_response_chain_unavailable");
      await expect(page.getByTestId("fleet-transformer")).toContainText(m.fleetEditor.step(2));
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("shadowsocks dropped: selected lines core cannot bind are named", async ({ page }) => {
      await openRecord(page, EVERY);
      await expect(page.getByTestId("fleet-no-steps")).toHaveText(m.fleetEditor.noSteps);
      await expect(page.getByTestId("fleet-dropped")).toContainText("shadowsocks");
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("long names: CJK and long Latin labels wrap or ellipsize, never scroll the page", async ({ page }) => {
      await openRecord(page, EVERY);
      await page.getByTestId("fleet-sample").getByRole("button", { name: m.fleetEditor.sampleAll(50) }).click();
      await expect(page.getByTestId("fleet-sample-row").filter({ hasText: "香港沙田数据中心" }).first()).toBeVisible();
      await page.locator(".editor-side").getByRole("button", { name: m.editor.preview }).click();
      await page.getByTestId("placeholder-document").waitFor();
      await previewIdentity(page, "运维值班手机-shenzhen-office-oncall-rotation-device-07");
      await expect(page.getByTestId("bind-entry").filter({ hasText: "tokyo-iij-premium-residential" }).first()).toBeVisible();
      expect(await docWidth(page)).toBeLessThanOrEqual(viewport.width);
      expect(await seriousViolations(page)).toEqual([]);
    });

    test("the placeholder document: placeholders only, said so", async ({ page }) => {
      await openRecord(page, ASIA);
      await page.locator(".editor-side").getByRole("button", { name: m.editor.preview }).click();
      const doc = page.getByTestId("placeholder-document");
      await expect(doc).toContainText(m.fleetEditor.document.proof);
      await expect(doc).toContainText("LATTICE-SYN-");
      expect(await seriousViolations(page)).toEqual([]);
    });
  });
}

test.describe("1440 sources, chain and rules", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("a new source defaults to Fleet lines and offers no legacy source", async ({ page }) => {
    await openList(page);
    await page.locator(".ss-header").getByRole("button", { name: m.create.newSource, exact: true }).click();
    await page.getByRole("tab", { name: new RegExp(`^${m.editor.tabs.content}`) }).click();
    await expect(page.getByTestId("source-fleet")).toHaveAttribute("aria-checked", "true");
    await expect(page.getByTestId("source-vpn-core")).toHaveCount(0);
    await expect(page.getByTestId("source-vpn-core-graph")).toHaveCount(0);
    await expect(page.getByTestId("fleet-no-steps")).toBeVisible();
    await page.getByTestId("fleet-add-step").click();
    await expect(page.getByTestId("fleet-step")).toHaveCount(1);
    // A new step needs a value before it can be saved.
    await expect(page.getByTestId("predicate-problem")).toHaveText(m.structured.problems.needsValues);
    await expect(page.getByTestId("identity-save-first")).toHaveText(m.fleetEditor.identity.saveFirst);
  });

  test("a legacy record keeps its card, marked, and its output is withheld", async ({ page }) => {
    await openRecord(page, LEGACY);
    await expect(page.getByTestId("source-vpn-core")).toHaveAttribute("aria-checked", "true");
    await expect(page.getByTestId("source-vpn-core").getByTestId("source-owner-credentials")).toHaveText(m.fleetEditor.ownerCredentials);
    await expect(page.getByTestId("legacy-pointer")).toHaveText(m.fleetEditor.legacyPointer);
    await expect(page.getByTestId("preview-withheld")).toContainText(m.fleetEditor.withheld.title);
    expect(await seriousViolations(page)).toEqual([]);
  });

  test("the chain offers the Lattice-only steps on fleet records only, and no link-mode script there", async ({ page }) => {
    await openRecord(page, ASIA);
    await page.getByRole("tab", { name: new RegExp(`^${m.editor.tabs.operations}`) }).click();
    const palette = page.locator(".add-grid");
    await expect(palette.getByRole("button", { name: new RegExp(m.operators["Structured Filter"].label) })).toBeVisible();
    await expect(palette.getByRole("button", { name: new RegExp(m.operators["Structured Sort Operator"].label) })).toContainText(m.fleetEditor.latticeOnly);
    await palette.getByRole("button", { name: `${m.operators["Script Operator"].label} JS`, exact: true }).click();
    const script = page.locator(".chain-step").last();
    await expect(script.locator("select option[value=link]")).toHaveCount(0);
    await expect(script.getByTestId("script-link-proof")).toHaveText(m.fleetEditor.scriptLink);

    await openRecord(page, "openjobs-host");
    await page.getByRole("tab", { name: new RegExp(`^${m.editor.tabs.operations}`) }).click();
    await expect(page.locator(".add-grid").getByRole("button", { name: new RegExp(m.operators["Structured Filter"].label) })).toHaveCount(0);
  });

  test("a combination that gathers a fleet record has its own binding rules", async ({ page }) => {
    await openRecord(page, COMBINATION);
    await expect(page.getByTestId("binding-rules")).toBeVisible();
    await expect(page.getByTestId("rules-collection-proof")).toHaveText(m.fleetEditor.rules.collectionProof);
    await expect(page.getByTestId("rule-ddns")).toBeChecked();
    await page.getByTestId("rule-usage").check();
    await expect(page.getByTestId("rule-usage-amount")).toHaveValue("50");
    expect(await seriousViolations(page)).toEqual([]);
  });

  test("the keyboard path: add, move and remove a condition with focus kept", async ({ page }) => {
    await openRecord(page, ASIA);
    const step = page.getByTestId("fleet-step").first();
    const rows = step.getByTestId("predicate");
    await expect(rows).toHaveCount(2);

    await step.getByRole("button", { name: m.structured.addCondition }).focus();
    await page.keyboard.press("Enter");
    await expect(rows).toHaveCount(3);
    // Focus lands on the new condition's field select.
    await expect(rows.nth(2).locator("select").first()).toBeFocused();
    await page.keyboard.press("Tab");
    await page.keyboard.press("Tab");
    await expect(rows.nth(2).getByRole("button", { name: m.structured.moveUp(3) })).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(rows.nth(1).getByRole("button", { name: m.structured.moveUp(2) })).toBeFocused();
    await expect(rows.nth(2).locator("select").first()).toHaveValue("protocol");

    await rows.nth(1).getByRole("button", { name: m.structured.removeCondition(2) }).focus();
    await page.keyboard.press("Enter");
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(1).locator("select").first()).toBeFocused();

    // A value is typed and committed with Enter; Backspace on an empty box takes the last one back.
    const values = rows.nth(0).locator("input[type=text]");
    await values.fill("TW");
    await page.keyboard.press("Enter");
    await expect(rows.nth(0)).toContainText("TW");
    await page.keyboard.press("Backspace");
    await expect(rows.nth(0).getByRole("button", { name: m.structured.removeValue("TW") })).toHaveCount(0);

    // The segments are radios: the arrow keys move the choice.
    await step.getByRole("radio", { name: m.structured.mode.keep }).focus();
    await page.keyboard.press("ArrowRight");
    await expect(step.getByRole("radio", { name: m.structured.mode.drop })).toBeChecked();
  });
});

for (const locale of ["zh-CN", "ru"] as const) {
  test.describe(`${locale}`, () => {
    for (const viewport of VIEWPORTS) {
      test(`${viewport.width}: the editor speaks ${locale}`, async ({ page }) => {
        await page.setViewportSize(viewport);
        const t = TABLES[locale];
        await openRecord(page, ASIA, { locale });
        await expect(page.getByTestId("fleet-selection").getByRole("heading", { name: t.fleetEditor.selectionTitle, exact: true })).toBeVisible();
        await expect(page.getByTestId("fleet-step-marker").first()).toHaveText(t.fleetEditor.pushed);
        await expect(page.getByTestId("fleet-step-count").first()).toHaveText(t.fleetEditor.removed(117, 23));
        await expect(page.getByTestId("fleet-proof")).toContainText(t.fleetEditor.proof.lines(117));
        await expect(page.getByTestId("binding-rules")).toContainText(t.fleetEditor.rules.title);
        await previewIdentity(page, "dave-contractor");
        await expect(page.getByTestId("bind-inactive")).toHaveText(t.fleetEditor.bind.inactive.expired);
        expect(await docWidth(page)).toBeLessThanOrEqual(viewport.width);
        expect(await seriousViolations(page)).toEqual([]);
      });
    }
  });
}
