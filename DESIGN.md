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

- Existing components to reuse: `CodeEditor`, `TargetSheet`, `ProcessChain`, the plugin chassis (`@latticenet/plugin-bridge/chassis`) for the page skeleton, and the remaining `Lt*` components (confirm dialog, copy, manual copy)
- New/changed components: `TargetSheet` becomes the Client Output Workspace; `CodeEditor` remains the single lazy editor and viewer implementation
- Variants and states: editable and read-only preview, lazy-load textarea fallback, known and plain languages, truncated and complete output
- Token/component ownership: host variables enter through `ui/src/tokens.css`; screens consume `--lt-*`; `CodeEditor` owns CodeMirror assembly and the lazy chunk boundary

## Client Output Workspace

### 迷你体验地图

- 人、场景与触发：日常维护集群的操作员从记录行打开订阅、组合或文件，在不离开 Lattice 的情况下检查并交付产物。
- 主要任务与成功结果：选择一个 Client，看到它实际收到的文档，再复制稳定 share link 或当前可见的一次性文档。
- 核心对象与用语：Record 是存储定义，Client 是产物目标，Document 是交付证据，Share 是稳定 URL，Nodes 是算子链诊断。
- 主路径：打开记录，选择 Client，看到对应目标开始生成，检查产物，复制链接或文档，关闭后回到原列表上下文。
- 失败与替代路径：没有 share 只影响稳定链接；渲染失败保留 Client 选择并提供 Retry；剪贴板被拒时保留可选内容；只读会话退回脱敏节点诊断，不暴露 admin 文档。
- 成功信号与证据：结果标题和正文同步变化且绑定同一目标，例如 `Stash · YAML · 12.4 KB`；复制动作始终复制用户眼前的文档。
- 待验证假设：正常切换目标时自动渲染的延迟可以接受。浏览器检查属于认知走查，不是有参与者的可用性研究。

### 屏幕合同

- 目的与当前对象：为一个具体客户端转换、检查并交付当前记录。
- 首要信息：真实生成文档，而不是目标选择器或解释文字。
- 主动作：Copy document；只有存在 enabled share 时，Copy link 才同等可见。
- 次要动作：切换到 Pipeline nodes 检查保留和过滤结果；该视图不声称自己是客户端文档。
- 始终可见状态：记录、选中 Client、产物语言、字节数、发布状态、生成进度和 include-unsupported 选择。
- 导航与退出：Close 和 Escape 关闭，Tab 留在 modal 内，焦点返回触发它的记录行。
- 状态变体：生成中、成功、渲染失败加 Retry、无 enabled share、share 查询失败、剪贴板拒绝、只读权限、空节点和 CodeMirror 降级。

### Gate 1 布局

```text
Wide
┌──────────────────────────────────────────────────────────────────────────────┐
│ Client output · openjobs-host                                      [Close]   │
├──────────────────────┬───────────────────────────────────────────────────────┤
│ CLIENT               │ CLIENT STASH → OUTPUT YAML → RECORD openjobs-host     │
│ Universal            │ [Document] [Pipeline nodes]                           │
│ Stash  selected      │ What Stash receives                    [Copy document]│
│ mihomo               ├───────────────────────────────────────────────────────┤
│ Egern                │  1 │ proxies:                                         │
│ …                    │  2 │   - name: Hong Kong 01                           │
│                      │  3 │     type: vless                                  │
│ OUTPUT OPTIONS       │    │                                                   │
│ Include unsupported │    │       large read-only CodeMirror evidence        │
│                      │    │                                                   │
│ DELIVERY             │    │                                                   │
│ Not published        │    │                                                   │
│ Copy document        │    │                                                   │
└──────────────────────┴───────────────────────────────────────────────────────┘

Narrow
┌──────────────────────────────────────────┐
│ Client output · openjobs-host     [Close] │
├──────────────────────────────────────────┤
│ [Universal] [Stash] [mihomo] [Egern] →   │
│ Include unsupported [ ]                   │
├──────────────────────────────────────────┤
│ STASH → YAML → 12.4 KB                    │
│ [Document] [Pipeline nodes]               │
│ What Stash receives        [Copy document]│
│  1 │ proxies:                             │
│  2 │   - name: Hong Kong 01               │
│    │              readable output         │
└──────────────────────────────────────────┘
```

- 调色：`--lt-bg`、`--lt-surface`、`--lt-surface-2`、`--lt-border`、`--lt-fg` 和 `--lt-fg-muted`；`--lt-accent` 只标识选中 Client 和焦点。
- 字体角色：控件和说明使用 `--lt-font`；Evidence Rail、记录 ID、产物语言、字节数、行号和文档使用 `--lt-mono`。
- 标志元素：Evidence Rail 不靠装饰，直接显示 Client 选择到产物的因果链。
- 响应式规则：宽屏为稳定决策栏加主证据面；窄屏为可横向滚动的 Client 轨加单列，同时保留相同选择、文档、动作和状态。
- 动效：Client 选择立即响应，内容只使用既有 120ms 功能过渡；reduced motion 仍为零。
- 明确不做：不加第二套语法库、字体、调色、外部预览页、新 RPC 或本片之外的多 Client 对比，也不模仿官方逐 Client 一行加重复图标动作。

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
