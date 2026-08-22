# Design

## Source of truth

- Status: Active
- Last refreshed: 2026-08-21
- Primary product surfaces: Subscriptions, Files, Settings, record previews and editors
- Evidence reviewed: `README.md`, `../SUBSTORE-UI-DESIGN.md`, `../SUBSTORE-PARITY.md`, `../LATTICE-PRODUCT-DESIGN.md`, `../handoff-for-codex-lattice-substore-prompt.md`, `ui/src/tokens.css`, `ui/src/styles.css`, and the current Vue component layer

## Brand

- Personality: competent, restrained, operational
- Trust signals: honest capability states, explicit save semantics, named resources in destructive confirmations, and real rendered output as evidence
- Avoid: decorative color, fake terminal styling, wall-to-wall cards, ornamental motion, emoji icons, and a second visual system inside the host frame

## Product goals

- Goals: preserve official Sub-Store capability while making frequent fleet work faster, clearer, and more recoverable in Lattice
- Non-goals: copying the official frontend visual language, adding GitHub or gist workflows, replacing the host design system, or adding release-looking stable versions during alpha
- Success signals: common actions stay within one or two steps, every state tells the truth, long real records remain legible, and all output remains usable at 375 and 1440 pixels

## Personas and jobs

- Primary personas: the cluster maintainer who uses the console several times a day, plus a low-frequency subscription recipient who only needs their own published result
- User jobs: source nodes, build and inspect operator chains, render client documents, publish shares, and diagnose why an output is empty or unavailable
- Key contexts of use: a sandboxed opaque-origin iframe, pointer and keyboard input, light and dark host themes, and slow or delayed bridge initialization

## Information architecture

- Primary navigation: Subscriptions, Files, and Settings as stable horizontal tabs
- Core routes/screens: list, record detail, target preview, file preview, and settings tools
- Content hierarchy: action and current state first, operational metadata second, raw IDs and rendered documents as supporting evidence

## Design principles

- Frequency law: behavioral precision outranks visual novelty for a surface used every day
- Chassis, Evidence, Accent: Data-Dense structure is about 80 percent, real nodes and rendered documents about 15 percent, and the mono ID, version, and output line no more than 5 percent
- State honesty: loading, empty, filtered-empty, error, permission-denied, pending, and truncated output remain distinct
- Tradeoffs: density is accepted when grouping and hierarchy keep it readable; extra whitespace and animation are removed before operational context

## Visual language

- Color: `--lt-bg` canvas, `--lt-surface` document surface, `--lt-surface-2` passive inset and gutter, `--lt-border` structure, `--lt-fg` primary ink, and `--lt-fg-muted` supporting ink. `--lt-accent`, `--lt-ok`, `--lt-warn`, and `--lt-danger` carry interaction or semantic meaning only
- Typography: `--lt-font` for interface copy, `--lt-mono` for IDs, versions, counts, and rendered output. Hierarchy comes from tokenized size, weight, and line height, not a bundled font
- Spacing/layout rhythm: one 4 px scale, fluid page width, character-based reading measures, and one scroll surface per overlay
- Shape/radius/elevation: hairline borders, compact radii, and shadows only for overlays
- Motion: functional 120 ms transitions through `--lt-dur`; reduced motion sets the duration to zero
- Imagery/iconography: Lucide icons with text labels; rendered nodes and documents are the visual evidence

## Components

- Existing components to reuse: `CodeEditor`, `TargetSheet`, `ProcessChain`, and the `Lt*` component layer
- New/changed components: S-A extends `CodeEditor` with an explicit preview mode and reuses it for all rendered documents
- Variants and states: editable and read-only preview, lazy-load textarea fallback, known and plain languages, truncated and complete output
- Token/component ownership: host variables enter through `ui/src/tokens.css`; screens consume `--lt-*`; `CodeEditor` owns CodeMirror assembly and the lazy chunk boundary

## Accessibility

- Target standard: WCAG 2.1 AA as the floor
- Keyboard/focus behavior: every control has visible focus, read-only previews remain focusable and labelled, and Tab can leave the viewer without an editor escape sequence
- Contrast/readability: host tokens and semantic status colors only; code uses the larger compact-text step and a dedicated code line-height token
- Screen-reader semantics: visible headings label each viewer; read-only state is exposed by CodeMirror and the textarea fallback
- Reduced motion and sensory considerations: no preview animation; the global reduced-motion contract remains zero duration

## Responsive behavior

- Supported breakpoints/devices: required checks at 375 and 1440 pixels
- Layout adaptations: evidence metadata wraps before the document; preview content remains full width; the target sheet and page own scrolling while the row drawer keeps one bounded inspection region
- Touch/hover differences: no information exists only on hover; text wraps and long content stays selectable

## Interaction states

- Loading: the existing delayed-handshake and inline pending states remain visible; the CodeMirror chunk keeps a usable textarea fallback while loading
- Empty: true empty and filtered-empty states keep different copy and actions
- Error: bridge, render, and lazy-editor failures remain inline and recoverable
- Success: saved and copied results use existing inline or cross-screen feedback rules
- Disabled: the reason stays visible or accessible and never lives only in a title attribute
- Offline/slow network: the fake host delay is part of the manual test contract; a missing editor chunk cannot remove the document

## Content voice

- Tone: plain, direct, operational
- Terminology: use the objects the operator controls, including subscription, combination, file, client, share, and node source
- Microcopy rules: say what happened, what is unavailable, and the next action; never make deletion sound like share retraction

## Implementation constraints

- Framework/styling system: Vue 3 SFCs, repo-local CSS tokens, and CodeMirror 6 in one lazy chunk
- Design-token constraints: the host supplies 11 variables; literal fallbacks stay isolated in `ui/src/tokens.css`, including the documented semantic status block and the theme-invariant overlay shadow. Screens carry no palette of their own
- Performance constraints: no new dependency; report main, CSS, and lazy-chunk deltas against the measured baseline rather than an obsolete budget
- Compatibility constraints: no direct network access, inline script, inline style, external URL, undeclared bridge method, or manifest capability expansion
- Test/screenshot expectations: behavior-oriented Vitest tests, typecheck, build, CSP scan, and real-browser checks at 375 and 1440 in light and dark themes

## Open questions

- [ ] The host token bridge still lacks radius, font, and complete semantic tokens. Owner: dashboard. Impact: plugins must keep local derived fallbacks until the host contract expands.
- [ ] Command-palette shortcut arbitration inside the iframe is unverified. Owner: S-E. Impact: test before implementing cross-tab Command-K capture.
