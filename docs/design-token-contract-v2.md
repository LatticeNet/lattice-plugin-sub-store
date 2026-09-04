# Design brief: token contract v2 and Sub-Store simplification

Status: design only. No product code, no manifest change, no release. Written 2026-09-03 against `origin/integration` at 3b7127f (sub-store 0.13.0-alpha.31 bound), with the dashboard, bridge, vpn-core, netguard, wireguard and server sources read at the line ranges cited. Nothing in this document was driven in a browser; every "reads as" claim is from the stylesheet and the component source, and the Gate 2 checklist at the end is where those claims get paid for.

This is the first file under `docs/`; the directory did not exist before this brief.

The operator's words, which this brief answers: the Sub-Store lens is "too hard, too angular"; it should have rounded corners, nested blocks and design tokens shared with vpn-core and netguard; its layering and interaction should feel smooth rather than "complex and fragile"; and the question of whether `/network/subscription-shares` belongs inside Sub-Store, with output distributed through the KV store or the static store, needs an answer.

## 1. Gate 1 plan, as first written

Per `rules/frontend.md` the plan is written before any code and then read back. This section is the plan as first drafted; section 2 is the read-back with every generic line marked, and section 3 is the revised plan that the rest of the brief builds on. The three sections are kept rather than collapsed because the marks are the evidence that the defaults were looked at.

### 1.1 Palette (from the dashboard, not invented)

All values are the host's own, from `lattice-dashboard/src/style/app.css`; light at lines 107-160, dark at 163-224. The plugin never declares a colour of its own.

| Name | Host token | Light | Dark | Job in the plugin |
|---|---|---|---|---|
| ground | `--background` | oklch(0.99 0.0015 280) | oklch(0.155 0.012 240) | the frame body |
| surface | `--card` | oklch(1 0 0) | oklch(0.195 0.014 240) | every panel |
| surface-2 | derived: `color-mix(--card 96%, --foreground 4%)` (tokens.css:12) | | | insets, hover, expanded row |
| line | `--border` | oklch(0.91 0.006 281) | oklch(1 0 0 / 9%) | panel border, hairlines |
| ink | `--foreground`, `--muted-foreground` | oklch(0.21 0.02 281), oklch(0.524 0.022 281) | oklch(0.97 0.004 240), oklch(0.705 0.012 240) | text, secondary text |
| accent | `--primary` | oklch(0.541 0.205 278.5) indigo | oklch(0.81 0.13 180) teal | the one coloured rule, focus, primary button |

Status colours come from `--success`, `--warning`, `--info`, `--destructive` and their `-foreground` pairs (app.css:139-144 light, 193-198 dark). They carry meaning and nothing else does.

### 1.2 Type roles

Two roles minimum, three in practice, the host's own stacks (the plugin's tokens.css:41-45 already argues that naming a face in the frame would make it the one panel in the product set in a different typeface, and that argument stands).

- body: the host's sans stack (app.css `body` rule), 14px / 1.5, 600 for names and labels. This is what the plugin uses today (`--lt-text-md`) and what the host chassis rows are set in.
- mono: `--font-mono` (app.css:71, the same stack as tokens.css:45), 12px, for slugs, paths, share URLs, digests, node counts in tables. Never for prose.
- label: 12px, 600, uppercase, 0.04em tracking, muted ink. Column heads and nothing else.

Display sizes: 16px / 650 for a panel title (the existing `.sheet-title`), 20px for the lens title. No other sizes.

### 1.3 Layout

Three surfaces, drawn at 1440. The two record screens (Subscriptions, Files) share one skeleton; the Files differences are noted under the drawing. The right-side panel is the same component in every drawing.

Subscriptions, list state, one row expanded, panel closed:

```
+------------------------------------------------------------------------------------+
| Sub-Store      [Subscriptions]  [Files]  [Shares 3]  [Settings]           lens bar |
+------------------------------------------------------------------------------------+
| [ Search records ...............]  [Kind v] [Tag v] [Density]   [Import] [+ New]   |  toolbar, page level
+------------------------------------------------------------------------------------+
| PANEL  radius 8, 1px line, surface                                                 |
| +--------------------------------------------------------------------------------+ |
| |   NAME               KIND   NODES  PUBLISHED        REFRESH     ACTIONS        | |  thead 32px, label role
| |--------------------------------------------------------------------------------| |  hairline
| |   provider-a         sub    142    cd-self   ok     2h ago      [.][.][.]  v   | |  row 40px
| |--------------------------------------------------------------------------------| |
| | | imported-file      sub     37    none             -           [.][.][.]  ^   | |  lit row: 2px accent left rule, surface-2
| | |  chain  source -> rename -> filter        dropped 3     [Run]  [Explain]      | |  inline expansion, z10, surface-2,
| | |  https://provider.example/••••••••••  [reveal]                                | |  hairline top and bottom, no border
| |--------------------------------------------------------------------------------| |
| |   all-lines          comb   179    net-all   ok     -           [.][.][.]  v   | |
| +--------------------------------------------------------------------------------+ |
|                                                                                    |
| [ 1 selected    Delete    Tag    Clear ]                                            |  batch bar, inline chrome z10
+------------------------------------------------------------------------------------+
```

Same screen with the panel open (Publish for imported-file). The panel is fixed to the frame viewport, inset 12px from the top, right and bottom, 440px wide for record work, 960px for client output; a scrim covers the frame; the source row stays lit under the scrim.

```
+-------------------------------------------------------+   +----------------------------+
| list, under a 24% ink scrim                           |   | Publish · imported-file  x |  head 76px, sticky
|                                                       |   |----------------------------|  hairline
|                                                       |   | Record   imported-file     |
| | imported-file   sub   37   none        (lit)        |   | Slug     [imported-file  ] |  form, 16px rhythm
|                                                       |   |          …/sub/imported-fi |
|                                                       |   | Format   [default       v] |
|                                                       |   | Expires  (o) never         |
|                                                       |   |          ( ) on [date    ] |
|                                                       |   |                            |
|                                                       |   |----------------------------|  hairline
|                                                       |   |           [Cancel] [Publish]|  foot, sticky
+-------------------------------------------------------+   +----------------------------+
                                                             radius 8, shadow-overlay, z20
```

Editor state, both record screens:

```
+------------------------------------------------------------------------------------+
| Subscriptions / provider-a                     • unsaved            [Cancel] [Save] |  breadcrumb + actions, page level
+------------------------------------------------------------------------------------+
| [General]  [Source]  [Operations]  [Output]                                        |  editor tabs, underline
+-----------------------------------------------------------+------------------------+
| PANEL editor-main  radius 8, 1px line                     | editor-side            |
|   Name           [provider-a                    ]         |  surface-2 inset,      |
|   Display name   [Provider A                    ]         |  radius 6, NO border   |
|-----------------------------------------------------------|                        |
|   Source URL     [https://••••••••••••••••] [reveal]      |  Preview               |
|   Refresh        every [6h v]                             |  142 nodes             |
|-----------------------------------------------------------|  last fetch 2h ago     |
|   Operations                                              |  [Preview draft]       |
|   1  rename    pattern [ ^(HK|SG)      ]     [x]          |                        |
|   2  filter    keep    [ region in HK   ]    [x]          |  conflicts and save    |
|   + add step                                              |  errors land here      |
+-----------------------------------------------------------+------------------------+
```

Files differs in the editor body only: one code field (the textarea fallback or the CodeMirror mount) under the name and path, no operations, and the side pane shows the preview and the last upload. The list is the same table with path and size instead of kind and nodes.

Shares lens:

```
+------------------------------------------------------------------------------------+
| Shares                                    3 of 4 live; 1 expired      [+ Publish]  |
+------------------------------------------------------------------------------------+
| PANEL                                                                              |
| | RECORD          SHARE                    FORMAT   EXPIRES      STATE   ACTIONS   |
| |----------------------------------------------------------------------------------|
| | provider-a      /sub/cd-self/••••••••    clash    never        ok      Copy link · Rotate · Pause  |
| | imported-file   /sub/imported/••••••••   default  2026-08-30   expired Renew · Rotate · Delete     |
| | all-lines       /sub/net-all/••••••••    default  never        ok      Copy link · Rotate · Pause  |
+------------------------------------------------------------------------------------+
| Link for provider-a   [ https://lattice.example/sub/cd-self/…  ]   [Dismiss]        |  manual-copy strip, only after a refused copy
+------------------------------------------------------------------------------------+
```

At 375 every surface keeps the same order: the toolbar wraps to two lines with the primary button last; the table keeps the name and published columns and moves the rest into the expansion; the editor side pane stacks under the main panel; the right-side panel becomes full-bleed (inset 0, radius 0) with its head and foot still sticky; the shares table scrolls sideways inside its panel with the record column pinned, as it does today (styles.css:2235-2300).

### 1.4 Signature element

The lit row. When anything opens for a record (the inline expansion, the row menu, the panel), the row takes a 2px accent left rule and the surface-2 tone, and the panel head repeats the record's name and kind. The active editor tab and the active lens tab use the same 2px accent rule, rotated. It is the one place the accent appears as a line, and it answers the question the old anchoring model tried to answer with geometry ("which row did I open") with colour instead.

## 2. Read-back: generic defaults marked

Each plan line was reread against the brief and marked D (a decision for this product, with the reason) or G (a default that could belong to any console). Every G was revised; the revisions are in section 3.

| Plan line | Mark | Why |
|---|---|---|
| Palette from host tokens, no plugin colours | D | The 11-token bridge is the mechanism behind the divergence; the whole point is to stop deriving |
| surface-2 as a colour-mix of card and foreground | G | The host already has this step as `--muted` (app.css:118, 178) and publishes it in the 11. The mix was a workaround for not reading it |
| Status from `--success` etc. | D | Sub-store hardcodes #16a34a and #d97706 (tokens.css:30-31); netguard falls back with a mix; vpn-core keeps two literal sets. One source |
| Body 14 on the host stack | D, pending | 14 is what sub-store and the host use; vpn-core and wireguard are on 13. Which one is the operator's call (section 9) |
| Mono 12 for identifiers | D | Slugs, paths and tokens are the product of this plugin; the mono role is where the eye goes to find them |
| Label role uppercase tracked | G | Uppercase column heads are the shadcn default. Kept for thead only because the shares table already does it and a table head is the one place it earns density; it is banned from form labels and section titles |
| Toolbar: search left, filters, primary right | G | Generic shape, but it is the existing list toolbar (styles.css:373-438) and the host's page header; reuse beats invention inside one product. Kept, flagged as reuse |
| One page panel around the list | D | The list has no container today (no radius or border rule on `.rec-scroll`); the sheet has one with a strong border. The panel is what makes "nested blocks" true |
| Panel radius 8, 1px `--border` | D | Host Card is rounded-xl = 8 (Card.vue:17), vpn-core `.data-panel` 8 (styles.css:181). Matching the neighbours is the ask |
| Inset blocks: surface-2, radius 6, no border | D | The three container rules (section 5): a panel is the one bordered thing. Today editor-side is a bordered inset inside a bordered editor (styles.css:1046-1058) |
| Right panel floating 12px inset, shadow | D | The frame is a viewport now (PluginFrameHost.vue:52-71), so a fixed panel is correct and the document-anchored one is not. Floating rather than edge-attached removes the last corner-only radius and gives the dark theme's rim shadow (app.css:218-220) something to draw |
| 440 / 960 widths | D | The existing drawer and sheet widths (LtDrawer.vue:73, styles.css:1291); no reason found to change them |
| Form rhythm 16px | G | Revised to the scale: 16 inside a panel, 12 inside a row, 16 between page blocks (vpn-core `.workspace > .data-panel`, styles.css:184), 24 between editor columns |
| Underline tabs | G, reuse | Existing (styles.css:97-148). Kept; the corner-only radius on them goes (section 5) |
| Signature: the lit row | D | Replaces the geometry answer to "which row" with a colour answer, and the accent gets exactly one job as a line |
| 375: panel full-bleed radius 0 | D | Frame width is the viewport; a 12px inset at 375 leaves 351px for a form |

## 3. Revised plan

The plan in section 1 stands with these changes. Surface-2 is `--muted`, read from the bridge, and the colour-mix at tokens.css:12 is deleted. The label role is thead-only; form labels are body 14 / 600. Spacing is the host scale `--space-1..7` = 4 / 8 / 12 / 16 / 24 / 32 / 48 with the roles: 12 inside a row, 16 inside a panel and between page blocks, 24 between editor columns, 32 above a lens title, 48 never inside the frame (it exists for the host's page gutters and is published so the scale is one list). Sub-store's 20px step (`--lt-space-5`, used for the editor column gap at styles.css:1040) goes to 24.

Everything else in the drawings is as drawn.

## 4. (a) Token contract v2: the bridge payload

### 4.1 What crosses the frame today

`PluginFrameHost.vue:163-175` declares `TOKEN_NAMES` as exactly eleven colour custom properties, reads them with `getComputedStyle(document.documentElement)` (177-180), and sends them in `lattice.host.init` (224-225) and again on every `.dark` class change through a `MutationObserver` (308). `lattice-plugin-bridge/src/bridge.ts:103-107` holds the same eleven as a `Set`, and `applyTheme` (341-348) writes only those onto the plugin's `documentElement` as inline custom properties, after setting `color-scheme` and `data-theme`. `bridge.test.ts:203-211` proves the filter drops `--evil` and `color`. vpn-core's private `bridge.ts:28-32` is a third copy of the same eleven; vpn-core is the one plugin whose `ui/package.json` does not depend on `@latticenet/plugin-bridge` (sub-store, netguard and wireguard pin 0.1.0-alpha.1).

No radius, spacing, type, semantic status, shadow or motion token crosses. Every plugin re-derives them: sub-store tokens.css (3/4/6, body 14, spacing with a 20 step, its own accent-tinted shadow), vpn-core and wireguard (5/6/8, body 13, no motion), netguard (4/6, no spacing scale, motion named like the host).

Two facts about the receiver matter for the design. First, `applyTheme` sets the properties as inline styles on `<html>`, and an inline custom property beats any `:root { --x: ... }` in the plugin's own stylesheet. So when the host publishes a name a plugin already declares on `:root` (wireguard's `--radius-sm: 5px`, vpn-core's `--radius: 6px`), the host value wins the moment that plugin ships a build on the new bridge, and the plugin's own declaration becomes exactly what it should be: the fallback for the harness and any older host. That is why the contract publishes the host's own names and not a namespaced copy. Second, the allowlist is by name only; the values are strings written through `style.setProperty`, which cannot execute or load anything, so growing the list from 11 to 42 names widens nothing but the palette.

### 4.2 The payload

The message shape is unchanged (`type`, `nonce`, `colorScheme`, `designTokens`); only the key set of `designTokens` grows. Values below are the host's, light then dark, from app.css; "new" marks a token that app.css does not define today and must gain before the host can send it.

```json
{
  "type": "lattice.host.theme",
  "nonce": "<frame nonce>",
  "colorScheme": "dark",
  "designTokens": {
    "--background": "oklch(0.155 0.012 240)",
    "--foreground": "oklch(0.97 0.004 240)",
    "--card": "oklch(0.195 0.014 240)",
    "--card-foreground": "oklch(0.97 0.004 240)",
    "--muted": "oklch(0.255 0.014 240)",
    "--muted-foreground": "oklch(0.705 0.012 240)",
    "--accent": "oklch(0.285 0.02 235)",
    "--accent-foreground": "oklch(0.97 0.004 240)",
    "--border": "oklch(1 0 0 / 9%)",
    "--primary": "oklch(0.81 0.13 180)",
    "--primary-foreground": "oklch(0.17 0.012 240)",
    "--destructive": "oklch(0.704 0.191 22.2)",
    "--destructive-foreground": "oklch(0.16 0.02 15)",
    "--success": "oklch(0.706 0.15 156)",
    "--success-foreground": "oklch(0.16 0.02 156)",
    "--warning": "oklch(0.8 0.16 80)",
    "--warning-foreground": "oklch(0.2 0.04 75)",
    "--info": "oklch(0.7 0.12 210)",
    "--info-foreground": "oklch(0.16 0.02 210)",
    "--ring": "oklch(0.7 0.12 182)",

    "--radius-sm": "3px",
    "--radius-md": "4px",
    "--radius-lg": "6px",
    "--radius-xl": "8px",
    "--radius": "4px",

    "--row-h": "40px",
    "--row-h-compact": "32px",

    "--space-1": "4px",
    "--space-2": "8px",
    "--space-3": "12px",
    "--space-4": "16px",
    "--space-5": "24px",
    "--space-6": "32px",
    "--space-7": "48px",

    "--font-mono": "ui-monospace, \"SF Mono\", \"JetBrains Mono\", \"Menlo\", \"Consolas\", monospace",
    "--text-body": "14px",
    "--text-mono": "12px",

    "--shadow-overlay": "0 0 0 1px oklch(1 0 0 / 8%), 0 10px 32px -8px oklch(0 0 0 / 70%)",
    "--shadow-raised": "0 1px 2px oklch(0 0 0 / 40%)",

    "--duration-fast": "100ms",
    "--duration-base": "200ms",
    "--ease-out": "cubic-bezier(0.19, 1, 0.22, 1)"
  }
}
```

Forty-two names: the eleven that cross today, plus nine colours (`--accent` and its foreground for hover, `--destructive-foreground`, and the three status pairs), five radius names, two row heights, seven spacing steps, three type tokens, two shadows, three motion tokens.

Where each comes from, and what the host has to add:

| Group | Host source | Status |
|---|---|---|
| The eleven | app.css `:root` and `.dark` | crosses today |
| `--accent`, `--accent-foreground`, `--destructive-foreground`, status pairs | app.css:119-120, 123, 125-130 light; 180-198 dark | defined, not sent |
| `--radius-sm/md/lg/xl` | app.css:22-25, inside `@theme inline` | defined; see the emission check below |
| `--radius` | app.css:86, plain `:root` | defined, not sent; equals `--radius-md` and is published only because shadcn code reads it |
| `--row-h`, `--row-h-compact` | app.css:118-119 | defined, not sent |
| `--space-1..7` | none; the host lays out with Tailwind's spacing utilities | new: add to the plain `:root` block. Tailwind v4 uses `--spacing` as its base and does not claim `--space-*` |
| `--font-mono` | app.css:71, inside `@theme inline` | defined; see the emission check |
| `--text-body`, `--text-mono` | none | new: add to `:root`; the value of `--text-body` is the operator's decision (section 9) |
| `--shadow-overlay`, `--shadow-raised` | app.css:97-101 light, 218-220 dark | defined, not sent |
| `--duration-fast`, `--duration-base`, `--ease-out` | app.css:88-90 | defined, not sent |

The emission check. `--radius-sm/md/lg/xl` and `--font-mono` live inside the `@theme inline { }` block (app.css:14-83), and Tailwind v4 emits theme variables into the compiled `:root` only when the corresponding utility is used somewhere, unless the block is `@theme static`. `rounded-sm/md/lg/xl` and `font-mono` are used in 12 / 61 / 31 / 6 / 50 files of `lattice-dashboard/src`, so they are almost certainly emitted, but "almost certainly" is not a contract: the first Gate 2 step for the dashboard half is `getComputedStyle(document.documentElement).getPropertyValue("--radius-sm")` returning `3px` in the built console. If it returns an empty string, the four radius steps and `--font-mono` are duplicated into the plain `:root` block next to `--radius`, which is where every other published token already lives.

Considered and left out: `--popover` and `--popover-foreground` (the row menu and the palette sit on `--card`; the host's popover values equal card in both themes), `--input` (plugin fields use `--border`), `--secondary` (equal to `--muted` in both themes), `--chart-*` and `--sidebar-*` (no plugin draws charts or a sidebar), `--content-max` and `--content-narrow` (the frame is fluid by decision, tokens.css:64-65).

### 4.3 Code sites and shipping order

The dashboard half: `TOKEN_NAMES` in `PluginFrameHost.vue:163-175` grows to the 42 names; the new `:root` tokens land in app.css; nothing else in the host changes, because the observer at line 308 already re-sends on theme change. The bridge half: `TOKEN_NAMES` in `lattice-plugin-bridge/src/bridge.ts:103-107` grows to the same list, and the package goes from 0.1.0-alpha.1 to alpha.2. `bridge.test.ts` gains one assertion per new group (a radius, a spacing step, a shadow, a duration land on `documentElement.style`) beside the existing `--evil` rejection.

Order: dashboard first. An older plugin build on the old bridge filters the new names out and renders exactly as before, so the host can ship on the normal a89+ deploy without waiting for any plugin. Each plugin then adopts on its next signed release by bumping the bridge dependency (sub-store, netguard, wireguard) or by replacing the private copy with the package (vpn-core). Until a plugin adopts, its `:root` values stay in force; after it adopts, they are fallbacks. The plugin signing ceremony for each of those releases is the operator's and is listed as blocked in section 9.

### 4.4 The shared token sheet and sub-store's alias layer

One `plugin-tokens.css` maps the published names to the neutral roles every plugin lays out with, with the fallbacks every plugin needs in its harness. It carries no colour of its own. The two candidate homes are a CSS asset of `@latticenet/plugin-bridge` (the package's `files` is `dist` and `README.md` today, package.json:13-16, so it would gain a `./tokens.css` export) or `lattice-plugin-template/ui`, which is copied, not depended on. The bridge asset means one dependency bump per plugin per token change; the template means one copy per plugin forever. The recommendation is the bridge asset; the choice is the operator's (section 9).

Sub-store's `tokens.css` becomes an alias layer for the transition, so the 527 rule blocks in styles.css keep compiling while they are migrated section by section:

```css
:root {
  --lt-surface-2: var(--muted, #f3f4f6);
  --lt-hover: var(--accent, #eef0f4);
  --lt-ok: var(--success, #16a34a);
  --lt-ok-contrast: var(--success-foreground, #ffffff);
  --lt-warn: var(--warning, #d97706);
  --lt-warn-contrast: var(--warning-foreground, #111827);
  --lt-info: var(--info, #2563eb);
  --lt-danger-contrast: var(--destructive-foreground, #ffffff);
  --lt-radius-sm: var(--radius-sm, 3px);
  --lt-radius: var(--radius-md, 4px);
  --lt-radius-lg: var(--radius-lg, 6px);
  --lt-radius-xl: var(--radius-xl, 8px);
  --lt-space-5: var(--space-5, 24px);
  --lt-row-h: var(--row-h, 40px);
  --lt-row-h-compact: var(--row-h-compact, 32px);
  --lt-mono: var(--font-mono, ui-monospace, monospace);
  --lt-shadow-overlay: var(--shadow-overlay, 0 12px 32px rgb(0 0 0 / 0.24));
  --lt-dur: var(--duration-fast, 100ms);
  --lt-dur-base: var(--duration-base, 200ms);
  --lt-ease: var(--ease-out, cubic-bezier(0.19, 1, 0.22, 1));
}
```

The literals at tokens.css:30-31, the colour-mix at line 12 and the accent-tinted shadow at 109 are the first things to go. When the last `--lt-` reference is gone from styles.css and the SFCs, the file goes with it. `contract.test.ts` and `deadStyles.test.ts` stay as the guards; the former gains a check that no `--lt-` name is declared without a `var(--host-name, ...)` on its right-hand side, which is what stops a literal from creeping back.

## 5. (b) The styles.css census and the three container rules

The sheet is 2,494 lines, 24 named sections (`── base` at 22 through `── reduced motion` at 2306), 527 rule blocks. Around it sit tokens.css (124 lines) and 1,029 lines of scoped `<style>` across 17 SFCs (ProcessChain 240, OperatorArgs 159, MemberPicker 159, CommonSettings 73, LtConfirmDialog 62, SubscriptionsScreen 54, LtManualCopy 48, LtDrawer 48, LtEmptyState 32, LtBatchBar 27, and the rest under 25 each).

The three rules every replacement below follows:

1. Three container levels: page, panel, row. A panel is the one bordered thing (1px `--border`, radius 8, `--card`). Inside a panel, blocks separate by a hairline or a `--muted` surface step and never by a second border. A row is 40px (32 compact) and separates by hairlines.
2. No corner-only radii and no literal px radii. Every radius is one of the four published steps or the pill, whole. A block that used to be edge-attached either floats (the panel) or is full-bleed (the panel at 375, the table in a narrow frame).
3. The radius follows the container level, not the component: 3 for inline marks (chips, kbd, menu items, focus outlines on inline text), 4 for controls and rows (anything clickable at 24px or taller, inputs, code fields), 6 for inset blocks and floating menus, 8 for page-level panels and the right-side panel. Badges and counts keep the pill.

### 5.1 Radius: every site

The 61 `border-radius` declarations in styles.css, with the replacement under the rules. The count matches the map's census (14 `--lt-radius-sm`, 14 `--lt-radius-lg`, 10 `--lt-radius`, 6 pills, 4 literal 2px, 1 literal 3px, 8 corner-only, 2 zeros in the narrow-frame section).

| Line | Selector | Today | Replacement | Rule |
|---|---|---|---|---|
| 77 | `.title-mark` | 4 | 4 | control |
| 114 | `.tab` | 3 3 0 0 | 4, all corners, on the hover fill only | 2, 3 |
| 124 | `.tab-count` | pill | pill | badge |
| 164 | `.alert` | 4 | 6 | inset block, not a control |
| 186 | `.field-error` | 0 3 3 0 | none: text with the 2px left rule, no fill | 1, 2 |
| 206 | `.badge` | pill | pill | badge |
| 230 | `.button` | 4 | 4 | control |
| 257 | `.link-button` | 3 | 3 | inline |
| 298 | `.code-area` | 4 | 4 | control |
| 340 | `input[type="checkbox"]` | 3px literal | `--radius-sm` | 2 |
| 385 | `.lt-search` | 3 | 4 | control (an input) |
| 400 | `.lt-chip` | pill | pill | badge |
| 429 | `.lt-breadcrumb-root` | 3 | 3 | inline |
| 463 | `.empty-inline-form > input` | 3 | 4 | control |
| 486 | `.rec-group-head` | 3 | 4 | row |
| 502 | `.rec-group-count` | pill | pill | badge |
| 571 | `.rec-expand` | 3 | 4 | control (32px icon button) |
| 597 | `.rec-publish-link:focus-visible` | 2px literal | `--radius-sm` | 2 |
| 696 | `.rec-reveal:focus-visible` | 2px literal | `--radius-sm` | 2 |
| 755 | `.rec-name` | 2px literal | `--radius-sm` | 2 |
| 874 | `.rec-menu` | 6 | 6, border `--border` (not strong) plus `--shadow-overlay` | floating |
| 886 | `.rec-menu button` | 3 | 3 | inline (menu item) |
| 919 | `.stale-strip` | 0 3 3 0 | none: left rule, no fill | 1, 2 |
| 937 | `.editor-group` | 6, bordered | none: hairline section inside the editor panel | 1 |
| 985 | `.editor-tab` | 3 3 0 0 | 4 on hover fill only | 2, 3 |
| 997 | `.editor-tab-count` | pill | pill | badge |
| 1021 | `.source` | 6, bordered inset | 6, `--muted`, no border | 1 |
| 1058 | `.editor-side` | 6, bordered inset | 6, `--muted`, no border | 1 |
| 1086 | `.editor-side-note.is-error` | 3 | none: left rule text | 1 |
| 1143 | `.doc-scroll` | 6 | 6, hairline | inset |
| 1213 | `.output-dropped` | 4 | 4 | control-sized block |
| 1239 | `.code-editor-host .cm-editor` | 6 | 4 | a field is a control |
| 1298 | `.sheet` | 6 0 0 6 | 8 all corners at 1440, 0 at 375 (the panel, section 6) | 2 |
| 1359 | `.sheet-close` | 3 | 4 | control |
| 1370 | `.sheet-error` | 3 | 6 | inset block |
| 1455 | `.target-chip` | 3 | 3 | inline |
| 1516 | `.permission-strip` | 0 3 3 0 | none: left rule text | 1, 2 |
| 1568 | `.output-tabs button` | 3 | 4 | control |
| 1627 | `.output-state` | 6 | 6 | inset |
| 1762 | `.row-popover-error` | 0 3 3 0 | none: left rule text | 1, 2 |
| 1778 | `.preview-summary` | 6, bordered | 6, `--muted`, no border | 1 |
| 1788 | `.preview-cut` | 0 3 3 0 | none: left rule text | 1, 2 |
| 1804 | `.graph-root-order li` | 4 | 4 | row |
| 1816 | `.graph-root-candidates button` | 4 | 4 | control |
| 1841 | `.standalone-notice` | 6 | 8 | page-level panel |
| 1859 | `.sheet` (narrow) | 0 | 0 | full-bleed at 375, kept |
| 1931 | `.rec-scroll .rec-expand` (narrow) | 0 | 0 | kept |
| 2001 | `.tab-search` | 4 | 4 | control |
| 2030 | `.palette` | 6 | 6 | modal; host Dialog is rounded-lg = 6 (DialogContent.vue:41) |
| 2056 | `.palette-hint` | 3 | 3 | inline (kbd) |
| 2069 | `.palette-row` | 3 | 3 | inline (menu item) |
| 2128 | `.chain-deltas` | 6 | 6, no border | inset |
| 2143 | `.editor-dirty` | pill | pill | badge |
| 2233 | `.masked-url-reveal:focus-visible` | 2px literal | `--radius-sm` | 2 |
| 2325 | `.manual-copy-strip` | 6 | 6 | inset |
| 2354 | `.partial-strip` | 6 | 6 | inset |
| 2388 | `.partial-strip__names` | 4 | 4 | control-sized |
| 2404 | `.conflict-panel` | 6, bordered | 6, `--muted`, no border | 1 |
| 2430 | `.conflict-table` | 4 | 0, hairlines | 1 (a table inside an inset) |

Two containers that have no radius today gain one: the record list gets the page panel (8, `--border`, `--card`) around `.rec-scroll`, and the editor main column gets the same, with `.editor-group` demoted to hairline sections inside it. That is the "nested blocks" the operator asked for, and it is also why the sheet's strong border at 1290-1305 and the drawer's at LtDrawer.vue:76 read hard: they were the only bordered things on a page whose main content had no container at all, so the eye read them as the container.

In the SFC scoped blocks: LtDrawer.vue:78 (4 0 0 4) goes with the drawer (section 6); LtConfirmDialog's box takes 6 (modal); the Lt primitives' radii follow the same table once their styles move to the shared sheet.

### 5.2 Borders: 34 full, 25 hairline, 8 left rules

Under rule 1 the 34 full 1px borders split three ways. The ones that stay are the panel borders: the record list panel (new), the editor main panel (new), the right-side panel, the row menu, the palette and the confirm dialog, plus inputs and buttons, which are controls and keep their border as the host's do. The ones that go are every border on a block that lives inside a panel: `.editor-group` (937), `.source` (1021), `.editor-side` (1058), `.preview-summary` (1778), `.chain-deltas` (2128), `.conflict-panel` (2404), and the border on `.alert` (162) becomes a fill plus a 6px radius with no left-rule width, so an alert is one thing (a box) rather than two (a box with a rule). The 25 hairlines stay; they are the inside of a panel by rule.

The 8 left rules (185, 664, 918, 1369, 1515, 1761, 1787, 2211) are all the same idea, an inline note with a coloured edge, and they all pair the rule with a fill and a corner-only radius. They become one class, `.note` with `.is-error` and `.is-warn`, a 2px left rule, 8px left padding, no fill, no radius. A status colour on a 2px line is enough; the fill was compensation for a hard box.

`--lt-border-strong` (tokens.css:14) has fourteen users in two roles. Six are resting borders: the sheet (1296), the row menu (873), the drawer (LtDrawer.vue:77), the batch bar (LtBatchBar.vue:26), the manual-copy strip (LtManualCopy.vue:100) and the checkbox (339). The three that float (sheet, menu, drawer, all replaced by the panel and the menu) become `--border` plus `--shadow-overlay`, because the host's dark shadow already carries the 1px rim (app.css:218-220) that the strong border was imitating, and the light shadow carries an accent-tinted spread; the batch bar, the strip and the checkbox take plain `--border`. The other eight are hover states on chips and choice buttons (240, 406, 1027, 1464, SubscriptionsScreen.vue:2441, OperatorArgs.vue:424, CommonSettings.vue:132) and one arrow glyph (1557); those keep the token, which stays as a hover-only derivation (a mix of `--border` and `--foreground` is a derivation, not a colour of the plugin's own). The rule is that the strong border never appears at rest.

### 5.3 The stacking ladder

Today: 1 (shares sticky column, 2283; narrow-frame expansions, 1919), 2 (output heading, 1592), 3 (sheet head, 1325), 20 (row menu, 870), 30 (batch bar, LtBatchBar.vue:30), 50 (drawer scrim, LtDrawer.vue:67), 60 (sheet scrim 1279, palette scrim 2014, confirm dialog LtConfirmDialog.vue:109). Five overlay kinds on seven values. Section 6 replaces this with three tokens: `--z-inline: 10` (row menu, batch bar), `--z-panel: 20` (the one right-side panel), `--z-modal: 30` (confirm, palette). The sticky internals keep 1 to 3; they are local to their own scroll containers and never contest an overlay.

### 5.4 Scoped styles move to the sheet

The 1,029 lines of scoped `<style>` are the reason a token change has to be verified in 18 places. The Lt primitives (11 files, 339 lines) move into styles.css under a `── lt primitives` section, because they are the design system and the sheet is where the census lives. ProcessChain, OperatorArgs and MemberPicker (558 lines) stay scoped: they are component-local layout, they reference tokens only, and moving them buys nothing but a longer sheet. The test that keeps this honest is `deadStyles.test.ts`, which already polices the sheet, plus one new assertion in `contract.test.ts`: no scoped block declares a custom property.

## 6. (c) Interaction simplification

### 6.1 The overlay model as it stands

One screen, `SubscriptionsScreen.vue` (2,449 lines, 33 top-level refs, 63 `v-if` branches), stacks five overlay kinds on three positioning models. `LtDrawer` is absolute against `.workspace` and takes `anchorTop` (LtDrawer.vue:18, 45, 70-72); `openDrawer` at SubscriptionsScreen.vue:1255 still sets `overlayAnchor` from `anchorTopFrom(event)`, and so do the sites at 913 and 1124, and FilesScreen.vue:249, 270 and 429. `TargetSheet` keeps the prop "while row callers finish removing document-coordinate anchors" (TargetSheet.vue:42-43), and its stylesheet section says two contradictory things twenty lines apart: "Fixed, and measured against the window. The frame is a viewport" (styles.css:1266-1267) and "The scrim covers the workspace, not the window: the sheet sits in the document, at the row that opened it" (1274-1275), while the rule itself is `position: absolute; top: var(--overlay-anchor-top, 0)` (1288-1289). The command palette is fixed (2012). The confirm dialog is absolute (LtConfirmDialog.vue:100). The row menu is absolute to its row (866-870). The batch bar is its own layer (LtBatchBar.vue:30).

`overlayAnchor.ts` explains why all of this existed: the frame used to be sized to its content, so `fixed` and `sticky` had nothing to resolve against (overlayAnchor.ts:1-19). `PluginFrameHost.vue:52-71` says that model is gone: the pane fills the shell's main region, the plugin scrolls inside it, and `100vh`, `fixed` and `sticky` resolve against the visible window. The anchoring code is solving a problem the host no longer has, and every remaining `anchorTop` is a coordinate computed from the document and applied inside a viewport.

Escape is the other cost. `LtConfirmDialog`, `LtDrawer`, `LtBatchBar` and `TargetSheet` each carry `@keydown.esc.stop` (LtDrawer.vue:46, TargetSheet.vue:554), and `recoverableFlows.test.ts:19` ("Escape cannot be swallowed twice") and `:35` ("only the visible screen owns the document keys") exist because the stop modifiers were the fix for a key that either closed two things or nothing. `editorExit.ts` decides what Escape and the breadcrumb do for the editor (`escapeAction` ignores while `overlayOpen`), and `useEditorExit.ts` shares that with Files; but `overlayOpen` is a closure each screen writes by hand (SubscriptionsScreen.vue:722 lists `editing`, `drawer`, `targetSheet`, `deleting.length`), so a sixth overlay would be forgotten there the same way Files forgot the guard in the first place (useEditorExit.ts:47-49).

### 6.2 The model after

Three levels, three tokens, one panel component, one arbiter.

Inline, `--z-inline: 10`: the row expansion (already in flow, no z needed), the row menu, the batch bar. Positioned relative to their row or the workspace bottom. They never take a scrim.

Panel, `--z-panel: 20`: one component, `LtPanel`, used by the row drawer (preview, upload to target, publish a share, renew) and by the client output sheet. `position: fixed`, inset `12px 12px 12px auto`, `width: min(440px, 100% - 24px)` for `size="record"` and `min(960px, 100% - 24px)` for `size="output"`, radius 8, `--card`, 1px `--border`, `--shadow-overlay`, entrance 200ms expo-out translateY(-6px) and opacity from 0.6 (the existing `sheet-in`, which was already fixed to not displace sideways, styles.css:1305-1317). At 375 (`max-width: 640px`) inset 0, radius 0, width 100%. The head is sticky at 76px (`--lt-sheet-head-h` today) and carries the record name and kind, the close button and, for the output sheet, the copy controls; the foot is sticky for the forms that submit (publish, upload). The body scrolls inside the panel, one scroller, because the panel is fixed and the document behind it is under a scrim. A scrim at `--z-panel` covers the frame; clicking it closes. Focus is trapped with the existing `trapDialogTab` (dialogFocus.ts), and on close focus returns to the element that opened it, which the component receives as `returnFocusTo` rather than computing from an event. The source row is lit (section 1.4) for as long as the panel is open.

Modal, `--z-modal: 30`: the confirm dialog and the command palette, both `position: fixed`, centred, the palette's existing 12vh top offset kept. A modal can open over a panel (delete from inside the output sheet asks first); a panel never opens over a modal.

What is retired: `overlayAnchor.ts` and `overlayAnchor.test.ts` whole; `anchorTopFrom` imports at SubscriptionsScreen.vue:44 and FilesScreen.vue:70; the `overlayAnchor` refs at SubscriptionsScreen.vue:910 and FilesScreen.vue:184 and their six write sites; the `anchorTop` prop on LtDrawer, TargetSheet and LtConfirmDialog and the `:anchor-top` bindings at SubscriptionsScreen.vue:1784, 2322, 2327, 2380; the `--overlay-anchor-top` custom property; the `.lt-drawer*` block in LtDrawer.vue (the component is replaced by LtPanel); the `.sheet` and `.sheet-scrim` rules at styles.css:1276-1300 (the sheet head, sub, error and body rules move under `── panel` unchanged); `clampAnchorTop`.

### 6.3 The single exit arbiter

`editorExit.ts` stays the decision (`exitAction`, `escapeAction` are pure and tested). What changes is where `overlayOpen` comes from. A module-level overlay stack, `overlayStack.ts`, exports `register(close: () => void): () => void`; every overlay component calls it on open and the returned dispose on close. `LtPanel`, `LtConfirmDialog`, the palette and the row menu register; the batch bar does not (it is chrome, not an overlay, and Escape clearing a selection is not something anyone asked for). One document `keydown` listener, owned by the visible screen exactly as the "only the visible screen owns the document keys" test demands, handles Escape as: if the stack is non-empty, call the top close and stop; otherwise call `exit.onEscape()`. `useEditorExit`'s `overlayOpen` option becomes `() => overlayStack.depth > 0` by default, so a seventh overlay cannot be forgotten. Every `@keydown.esc.stop` on the four components is deleted. `recoverableFlows.test.ts:19` is rewritten from "cannot be swallowed twice" to "Escape closes exactly the top of the stack, then the editor asks", which is a stronger statement of the same requirement, and the test at line 35 stays as it is.

The dirty-draft rule is unchanged: a panel open over a dirty editor still gets Escape first, and the editor asks on the next Escape.

### 6.4 The split of SubscriptionsScreen.vue

The screen becomes three components with one owner each: `SubscriptionsList.vue` (toolbar, table, expansion, row menu, batch bar, selection, the delete remainder flow), `SubscriptionEditor.vue` (draft, tabs, validation, `useEditorExit`, `recordConflict`), and `SubscriptionPanel.vue` (the four panel modes, each a small form or view). `SubscriptionsScreen.vue` keeps the routing between list and editor and nothing else. The 33 refs distribute by owner; `subs` (the record store) stays shared. FilesScreen.vue takes the same split; its panel has two modes (preview, upload).

### 6.5 The fragile paths

Every async action keeps the sequence the design program already requires: trigger, acknowledgement, progress, outcome, recovery. The changes below are to where each step lands, not to the sequence.

Clipboard. The mechanism stays: the frame is sandboxed without `allow-same-origin`, so it asks the host through `lattice.plugin.clipboard` (hostClipboard.ts:1-28, 5s ack), the host copies in pluginClipboard.ts (3s, execCommand fallback) and toasts in its own lane (PluginFrameHost.vue:183-196), and a refusal puts the value on screen in `LtManualCopy`. What changes is that there is one copy state machine, in `LtCopyButton` (idle, copying, copied for 1.5s, refused), and every copy site uses it, which the test at recoverableFlows.test.ts:110 ("no copy site talks to the clipboard directly") already enforces. On refused, the button's own row reveals the value pre-selected, in place, rather than a strip somewhere else on the page: for the shares table that means the `manual-copy-strip` (SharesScreen.vue:205-211) moves from above the table into the row; for the panel it is under the control. The reveal clears on the next copy or after 60s, the way `revealSource` already masks itself again. The 5s and 3s timeouts stay; there is no evidence they are wrong.

Multi-step forms. The share round trip is the one that loses work: the plugin posts `lattice:navigate` to `/network/subscription-shares?create=1&for=<name>` (SubscriptionsScreen.vue:1288-1293), the host applies the deep link (SubscriptionSharesView.vue:468-490), and on return `PluginFrameHost` is remounted because it is keyed by route inside `PluginView.vue:351-353`, so filters, selection, expanded rows and the panel are gone. Section 7 makes the share create a panel form in the plugin: record (prefilled, read-only), slug (suggested from the record name, validated by the same rule the host uses, `SHARE_SLUG_RE`, with a 409 from the server shown as a field error under the slug, not a toast), format (optional), expiry (never, or a date), then Publish. The outcome is shown in the same panel: the share row as the Shares lens will print it, with the copy control. Upload to target is the same panel with the destination picker and method; it already is a single form (`SubscriptionPublishControl`), it only loses its position anchoring. The editor's multi-tab draft is not multi-step (one Save), and stays; `recordConflict.ts`'s stale-save diff renders in the editor side pane as a choice (keep mine, take theirs, see the diff), not as a modal, because the operator needs the form visible to decide.

Error states. Three placements and no fourth. A field error is text with a left rule under its control. A panel-level failure (publish refused, upload failed, render failed) is a `.note.is-error` at the top of the panel body, under the sticky head, so it is visible however far the body is scrolled; today `.sheet-error` is a box in flow. A page-level failure (the list could not load, the share list could not be read) is the existing `.alert`, and "absent is not rendered as zero" (recoverableFlows.test.ts:96-103) stays: a share list that failed to load prints a dash and an action, never 0. The partial batch delete keeps its remainder flow (SubscriptionsScreen.vue:1184-1240): the strip names what was done, what failed and what was never attempted, and offers a retry of only the remainder. Nothing that failed part way may look like success.

## 7. (d) Subscription shares: where they belong

### 7.1 The job

The operator comes to Sub-Store to make a subscription reachable by a client and to know that it is. Every other verb on the lens serves that one: import, edit the chain, preview, then publish, copy the link, and later renew or rotate. Today the first half of that job lives in the plugin and the second half lives on a host page under Networking, and crossing between them costs the plugin its state (section 6.5). The Shares lens inside the plugin can list and copy but not act: "Renew share…" is a link (SubscriptionsScreen.vue:768-776), the drawer in share mode is two paragraphs and a button that leaves (2354-2372), and the empty state tells the operator to go elsewhere.

### 7.2 The ruling and why it holds

PROGRAM.md's 2026-09-03 ruling is that the share object belongs in core because the token never enters the plugin sandbox, and that secrets, tokens, public routes and the audit trail are platform. Reading the server confirms every part of that. `handleSubscriptionShare` (server_subscription_share.go:220-420) answers every miss with the same decoy and an audit record, validates the format and a bounded `?target=` set, checks the publishing plane, resolves by token, caches per share, format, UA class and variant, revalidates by digest before paying for a render, serves the last good body on provider outage, and sets `Cache-Control: no-store` and `Subscription-Userinfo`. The publishing plane projects a share as a reserved AnyHost route (publishing.go:144-158) rather than storing it twice. None of that should be re-implemented behind a plugin's 4 MiB frame cap and 18-entry KV bucket, and none of it can be: the plugin runtime has `kv.get/put`, `secret.*`, `http.do` and `rpc.call` and nothing that serves a public route.

So the object stays in core. What moves is the verbs. The precedent is already in production: netguard's `latticenet.netguard/firewall` service registers `upsert_group`, `delete_group`, `upsert_zone`, `upsert_binding`, `adopt` and `plan` as core-backed methods (server_network_plugins.go:137-148), each re-checking the operator principal and invoking the same REST handler bodies through `invokePluginOperation`. Sub-store's `latticenet.sub-store/shares` service (server_substore_plugins.go:15-86) registers one method, `list`, with `backing: core` and scopes `substore:admin` plus `proxy:admin`, re-checks `proxy:admin` server-side, and returns rows whose `path` and `url` embed the token because "the URL is the product" (39-43). The token already reaches the plugin UI frame through that method; it never reaches the plugin runtime, and nothing below changes that.

### 7.3 The server change, sketched

`registerSubStorePluginRPC` grows its method list from `[]string{"list"}` to `list, create, set_enabled, set_expiry, rotate, delete`. Each method re-checks `proxy:admin` through `pluginGatewayScopeAllowed` exactly as `list` does at line 58, because the manifest scopes are enforced by the gateway but a manifest mistake must not widen who mints tokens. Each write records the same audit action the REST API records (`subscription.share.create`, `.update`, `.rotate`, `.delete`, server_subscription_share_api.go:17-20) with the same metadata (`share_id`, `slug`, `token_sha256`, and for expiry `expires_from` / `expires_to`), so the audit trail cannot tell which surface the operator used, which is the point.

The request shapes, restricted to what the plugin may do:

- `create {subscription_id, slug, default_format?, expires_at?}`. The server fixes `Source` to `{kind: plugin, plugin_id: latticenet.sub-store, subscription_id}` itself; the plugin cannot name another source kind, so it cannot create a share for a proxy user or another plugin. Slug validation, collision (409), format validation and token minting are the existing `createSubscriptionShare` body (78-131) factored into a store-level function both the REST handler and the RPC call. Returns the `subStoreShareRow` for the new share.
- `set_enabled {share_id, enabled}` and `set_expiry {share_id, expires_at | clear_expiry}`: the two halves of `updateSubscriptionShare` (182-250), including the "expires_at must be in the future" refusal and the cache invalidation at 231. Split into two methods rather than one `update` so each has one effect and one audit line; the plugin has no reason to change format after creation.
- `rotate {share_id}`: `rotateSubscriptionShare` (262-294). Returns the row with the new path.
- `delete {share_id}`: the delete branch of `handleSubscriptionShareItem` (136-180).

Every write method first loads the share and refuses, with the same "not found" the REST API gives, any share whose `Source.PluginID` is not `latticenet.sub-store`, so the plugin can only act on its own. `refresh` is not exposed: it fetches the provider through the share, which the plugin does directly.

The manifest change in `lattice-plugin-sub-store/manifest.json`: the `shares` interface lists the six methods, the five writes with `effect: write` and scopes `["substore:admin", "proxy:admin"]`, `backing: core`. This is a manifest change and therefore a signed release, which is the operator's ceremony (section 9). `ui/src/client.ts` BINDINGS gains the five methods; `useShares.ts` gains the write functions and invalidates its one shared list on each; `shareState.ts` is unchanged, it folds whatever the list says.

### 7.4 The plugin surfaces after

The panel in share mode is the create form (section 6.5). The row menu's share item keeps its three faces from `shareActionFor` but the two that were links become actions: "Copy share link" copies, "Renew share…" opens the panel on the expiry step of the existing share, "Publish…" opens the create form. The Shares lens goes from read-only to read-write for sub-store's own shares: Copy link, Rotate, Pause / Resume, Renew, Delete, each with a confirm where the REST API's semantics warrant one (rotate and delete invalidate every client that holds the link; pause does not, it is reversible). Rotate's confirm says what it breaks in the same voice as the record delete confirm (recoverableFlows.test.ts:73-92).

Vocabulary, fixed once: "Publish" means create a share, everywhere. The operator-target upload, which the drawer titles "Upload · name" (SubscriptionsScreen.vue:1249) and the method calls `publish` (subscription_publish.go:19-63), is "Upload to target" in the panel title, the row menu, the toast and the method documentation; the method name in the manifest stays, because renaming it is a wire change nobody needs.

### 7.5 The host page

`/network/subscription-shares` stays. It is the cross-origin list (every share, whatever its source) and the only home for proxy-user shares (`ShareSource.Kind` `core_proxy_user` has no plugin). Whether it stays a top-level Networking entry (nav.ts:112) or becomes a lens of Publishing (nav.ts:124, which already lists `proxy:admin` among its scopes) is an information-architecture decision the operator owns (section 9); the argument for the lens is that Publishing is already the answer to "what URL is this reachable at", and a share is one row of that answer. `pluginNavigationModel.ts:59`'s `create` and `for` parameters become unused once the plugin creates its own shares and are removed, which shrinks the one parameterised route the frame may drive.

### 7.6 KV and static distribution: no

The operator asked whether Sub-Store's output could be distributed through the KV store or the static store. Read against the server, neither can carry what `/sub/` carries, and building either would be a second, worse share system.

KV bindings require a storage bearer token on every GET: `serveKVBinding` calls `authorizeStorageToken` (server_storage.go:616), which reads the `Authorization` header through `bearerToken` (server.go:8684). Clash, Stash, sing-box and Shadowrocket fetch a subscription URL with no way to attach a header, so a KV-served subscription is unreachable by the clients it exists for.

Static bindings are anonymous public hosting: `serveStaticBinding` (server_storage.go:365-410) has no token gate, no decoy on miss, sets `Cache-Control: public, max-age=60`, and stores text `Content` only. A subscription on a static path is a secret-bearing document that anyone who guesses the path can read, cached by every intermediary for a minute after rotation. It would also need a `static.put` host call and a `static:write` capability that the SDK does not have (host actions are `kv.*`, `secret.*`, `http.do`, `rpc.call`), plus a re-publish job on every provider refresh, because a static object is a snapshot and a subscription is a render.

`/sub/` already does per-client target rendering, digest revalidation, stale-on-outage, `Subscription-Userinfo`, audit and decoy. If the operator wants a rendered artifact pushed to a third party, `subscription.publish` (subscription_publish.go) already renders a record and PUTs, POSTs or PATCHes it to any operator target with a `secret://` destination; the only design work is to surface it honestly as "Upload to target" with a picker over operator targets. The decision goes in PROGRAM.md's decisions and DESIGN.md's open questions; no code.

## 8. (e) Gate 2 checklist

Nothing in this brief is done until it has been rendered and driven. The harness cannot do it today: `ui/dev/fakeHost.ts` sends no `lattice.host.theme` (grep for `theme`, `designTokens`, `colorScheme` returns nothing), so a harness screenshot shows the tokens.css fallbacks (blue accent, light greys), not the production teal on slate. The first item is therefore the harness.

Harness and host:

- fakeHost.ts sends `lattice.host.init` and `lattice.host.theme` with the 42-token payload from section 4.2, both schemes, the production values; a toggle in the harness chrome switches them. The same for vpn-core `ui/dev/host.ts` and netguard `ui/dev/host.ts`.
- In the built console, `getComputedStyle(document.documentElement).getPropertyValue(name)` is non-empty for all 42 names in light and in dark (the `@theme inline` emission check, section 4.2).
- In the plugin frame in the console, the same 42 names are present on `documentElement.style` after init, and change on the host's theme toggle without a reload.

Each surface, at 1440 and at 375, light and dark, with realistic content (the live-shaped data: a provider subscription with a masked URL, an imported file with no share, a combination with 179 nodes, one expired share, a slug 40 characters long, a display name that wraps):

- Subscriptions list: loading skeleton; empty (no records); error (list failed); the table with 40 rows; density compact; a row expanded with a chain that dropped nodes; a row expanded with an error; the row menu open at the bottom row (it must not clip); selection of 3 with the batch bar; the lit row under every open state.
- The panel, every mode: preview loading, preview error, preview with more nodes than shown; upload to target idle, busy, refused with the error under the head; publish a share with the slug suggested, with a 409 as a field error, with an expiry in the past refused, and the created state with the copy control; renew on an expired share; the client output sheet at 960 with a 3,000-line document, its head and the output heading both pinned while the body scrolls; the panel at 375 full-bleed with the foot reachable.
- Escape: with a panel over a dirty editor, first Escape closes the panel, second asks; with a confirm over a panel, Escape closes the confirm only; with nothing open, Escape in a clean editor leaves; the Files screen behaves identically. Focus returns to the opener on every close.
- Editor: clean and dirty (the badge); Save conflict with the diff in the side pane; validation error on a field; the side pane stacked at 375; the code field in Files with the CodeMirror mount and with the textarea fallback.
- Clipboard: copy succeeds (host toast, button says Copied); host refuses (value revealed in place, pre-selected, Dismiss); host older than the message (5s silence treated as refusal); the same three on the shares table and in the panel.
- Shares lens: list not yet read (dash, not zero); list failed (the way-forward action); 0 shares (empty state that now offers Publish rather than a pointer to the console); the four states per row (ok, expired, disabled, no public base URL so the path alone copies); rotate and delete confirms naming what they break; pause and resume without a confirm; the table scrolling sideways at 375 with the record column pinned.
- Reduced motion: every entrance is instant; hover and focus rings still appear.
- Console: no CSP violation, no permissions-policy violation from a clipboard attempt inside the frame, no Vue warning.

Then `design-reviewer` on the rendered result, not on this document. Every plugin release that follows waits for the operator's signing ceremony.

## 9. Decisions reserved for the operator

These are not decided here. Each changes the outcome and is the operator's to make.

1. Body type size: 14 (host, sub-store) or 13 (vpn-core, wireguard). The payload's `--text-body` carries whichever is chosen; vpn-core and wireguard move to it on their next release either way.
2. Where the shared `plugin-tokens.css` lives: as a CSS asset exported by `@latticenet/plugin-bridge` (recommended; one dependency bump per plugin per change) or in `lattice-plugin-template/ui` (one copy per plugin).
3. Whether the share URL with its token may be shown inside the plugin frame at create time. `shares.list` already returns it to the frame (server_substore_plugins.go:39-44), so `create` returning the same row adds no new exposure; the alternative is `create` returning the slug only and the panel telling the operator to copy from the Shares lens after a fresh list.
4. Whether `/network/subscription-shares` stays a top-level Networking entry or becomes a lens of Publishing once the plugin owns its own verbs.
5. The plugin signing ceremony for every release this brief implies: the sub-store manifest change (section 7.3), and the bridge adoption in sub-store, netguard, wireguard and vpn-core (section 4.3). The dashboard half rides the normal deploy approval.

## 10. What this brief did not verify

The remount on return from the host share page is inferred from `PluginFrameHost` living only under `PluginView.vue:351` with a route-keyed frame; it was not driven. The harness being themeless is inferred from grep, not from rendering. Whether Tailwind emits the `@theme inline` radius variables is an expectation, not a measurement, and the check is the first Gate 2 item. No live GET was made in this pass; the production state cited in the map (server a88, sub-store alpha.31, one live share `cd-self`) is the map's, not this document's. PROGRAM.md's production truth section (lines 30-60) is stale on the server tag and both plugin versions and should be refreshed by whoever next touches it; that is a documentation fix outside this lane.
