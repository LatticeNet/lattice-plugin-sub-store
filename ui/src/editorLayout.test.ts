import { readFileSync } from "node:fs";
import { parse } from "vue/compiler-sfc";
import { describe, expect, it } from "vitest";

const screen = readFileSync(new URL("./screens/SubscriptionsScreen.vue", import.meta.url), "utf8");
const styles = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

/** CSS with its comments removed, for assertions about declarations. */
function withoutComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

// The editor decides a record and the pane shows what that record produces.
// Keeping them side by side, with the pane in view while the form scrolls, is
// the point: before this the summary appeared below a long form, so the thing
// the operator was changing and the thing it changed were never on screen at
// the same time.
describe("the record editor beside its preview", () => {
  it("asks how much room it has, not how wide the device is", () => {
    // A plugin frame is a pane inside a console. A viewport media query answers
    // a different question, and answers it wrong whenever the console's own
    // chrome changes width.
    expect(styles).toMatch(/\.editor-shell\s*\{[^}]*container-type:\s*inline-size/s);
    expect(styles).toMatch(/@container \(min-width: 1040px\)/);
    expect(screen).toContain('class="configuration editor-shell"');
  });

  it("is one column until both columns can be read", () => {
    // The base rule, outside the container query, is the narrow one: a frame
    // that never reaches the breakpoint still gets a working layout.
    expect(styles).toMatch(/\.editor-layout\s*\{[^}]*grid-template-columns:\s*minmax\(0, 1fr\)/s);
    const wide = styles.slice(styles.indexOf("@container (min-width: 1040px)"));
    expect(wide).toMatch(/grid-template-columns:\s*minmax\(0, 1fr\) minmax\(300px, 380px\)/);
  });

  it("pins the pane only where pinning helps", () => {
    const wide = styles.slice(styles.indexOf("@container (min-width: 1040px)"));
    expect(wide).toMatch(/\.editor-side\s*\{[^}]*position:\s*sticky/s);
    expect(wide).toMatch(/\.editor-side\s*\{[^}]*top:\s*var\(--lt-space-4\)/s);
    // Not in the base rules: sticky in a narrow frame is a column covering the
    // form it is there to explain. Comments are stripped first — this asserts
    // about declarations, and a comment quoting one is prose.
    const narrow = styles.slice(0, styles.indexOf("@container (min-width: 1040px)"));
    const sideRule = narrow.slice(narrow.indexOf(".editor-side {"), narrow.indexOf(".editor-side-head"));
    expect(withoutComments(sideRule)).not.toContain("position: sticky");
  });

  it("gives the pane the control that fills it, once", () => {
    const editor = screen.slice(screen.indexOf('<section v-if="editing"'));
    const aside = editor.slice(editor.indexOf('<aside class="editor-side"'), editor.indexOf("</aside>"));
    expect(aside).toContain("subs.runPreview(draft)");
    // Two buttons for one job is two places to look when nothing happens.
    expect((editor.match(/subs\.runPreview\(draft\)/g) ?? []).length).toBe(1);
    expect(aside).toContain('subs.preview.value ? "Refresh" : "Preview"');
  });

  it("reports a failed preview where the preview would have been", () => {
    // The control moved into the pane, so a failure that only reached the save
    // row at the bottom of a long form was one the operator could cause and
    // never see.
    const editor = screen.slice(screen.indexOf('<section v-if="editing"'));
    const aside = editor.slice(editor.indexOf('<aside class="editor-side"'), editor.indexOf("</aside>"));
    expect(aside).toMatch(/subs\.previewError\.value/);
    expect(aside).toContain('role="alert"');
    const composable = readFileSync(new URL("./useSubscriptions.ts", import.meta.url), "utf8");
    expect(composable).toMatch(/previewError\.value = safeErrorMessage\(cause, "Preview failed"\)/);
    expect(composable).toMatch(/previewError\.value = "";/);
  });

  it("says something true before the first run instead of showing an empty box", () => {
    expect(screen).toContain("Nothing run yet.");
    expect(screen).toContain("without saving it");
  });
  // An error raised inside the editor is about a draft that stops existing the
  // moment the editor closes. Left standing it sits above the list as an alert
  // about nothing on screen.
  it("does not carry the editor's errors back to the list", () => {
    const cancel = screen.slice(screen.indexOf("function cancelEdit"));
    expect(cancel.slice(0, cancel.indexOf("}"))).toContain("subs.clearErrors()");
    const hook = readFileSync(new URL("./useSubscriptions.ts", import.meta.url), "utf8");
    const clear = hook.slice(hook.indexOf("function clearErrors"));
    const body = clear.slice(0, clear.indexOf("}"));
    expect(body).toContain('previewError.value = ""');
    expect(body).toContain('actionError.value = ""');
    // A save reports success and then leaves through the same exit. Clearing
    // the notice on the way out left the save with nothing to show for itself.
    expect(body).not.toContain("notice.value");
  });

  // The pane is 380px beside a form and 836px stacked under one. Only the
  // first has to trade a row's layout for its width, so the row asks the pane
  // how wide IT is rather than reading the frame's breakpoint.
  it("lets the pane's own width decide how a node row is laid out", () => {
    expect(styles).toMatch(/\.editor-side\s*\{[^}]*container-type:\s*inline-size/s);
    const narrow = styles.slice(styles.indexOf("@container (max-width: 460px)"));
    expect(narrow).toMatch(/\.editor-side \.node-row\s*\{[^}]*flex-wrap:\s*wrap/s);
    expect(narrow).toMatch(/\.editor-side \.node-meta\s*\{[^}]*flex:\s*0 0 100%/s);
    // Not a frame-wide rule: the same declaration outside the query would put
    // two-line rows in a pane with room for one.
    const beforeQuery = styles.slice(0, styles.indexOf("@container (max-width: 460px)"));
    expect(beforeQuery).not.toMatch(/\.editor-side \.node-meta\s*\{[^}]*flex:\s*0 0 100%/s);
  });

  // The endpoint sat inside the badge box, whose intrinsic width then claimed
  // most of a 320px row and ellipsed every node name down to "Portland ...".
  // Its place in the row is structural, so the check is too: read the template
  // rather than the order two spans happen to appear in.
  it("keeps the endpoint out of the badge box so a narrow row can reflow it", () => {
    const source = readFileSync(new URL("./components/NodeRows.vue", import.meta.url), "utf8");
    const template = parse(source, { filename: "NodeRows.vue" }).descriptor.template;
    if (!template?.ast) throw new Error("NodeRows template is missing");
    const classOf = (node: Record<string, any>): string =>
      (node.props ?? []).find((p: any) => p.name === "class")?.value?.content ?? "";
    const elements = (node: Record<string, any>): Record<string, any>[] =>
      (node.children ?? []).filter((child: any) => child.type === 1);
    const walk = (node: Record<string, any>, want: string): Record<string, any> | undefined =>
      classOf(node) === want ? node : elements(node).map((child) => walk(child, want)).find(Boolean);
    const row = walk(template.ast as Record<string, any>, "node-row");
    expect(row, "the list has no node-row").toBeTruthy();
    const children = elements(row!).map(classOf);
    expect(children).toContain("node-meta");
    expect(children).toContain("node-tags");
  });
});

// The two editors do the same job on different records, and the second one to
// grow a feature is where they silently stop matching. Files had the detail
// screen and the breadcrumb, and then a 1400px single scroll of six fieldsets
// next to a sticky pane, while its sibling was 356px behind three tabs.
describe("the two record editors are the same shape", () => {
  const files = readFileSync(new URL("./screens/FilesScreen.vue", import.meta.url), "utf8");

  it("splits both editors into the same sections", () => {
    for (const [name, source] of [["SubscriptionsScreen.vue", screen], ["FilesScreen.vue", files]] as const) {
      expect(source, name).toMatch(
        /EDITOR_TABS[\s\S]{0,220}id: "display"[\s\S]{0,80}id: "content"[\s\S]{0,80}id: "operations"/,
      );
      expect(source, name + " opens on a section nobody chose").toContain('editorTab = ref<EditorTab>("display")');
      expect(source, name + " keeps the last record's section").toMatch(/editorTab\.value = "display";/);
    }
  });

  // A form that says what is wrong and not where is worse behind tabs than in
  // a single scroll: the field is two sections away and nothing points at it.
  it("points at the section holding the invalid field", () => {
    for (const [name, source] of [["SubscriptionsScreen.vue", screen], ["FilesScreen.vue", files]] as const) {
      expect(source, name).toMatch(/const errorTab = computed/);
      expect(source, name).toContain('v-if="errorTab === tab.id && editorTab !== tab.id"');
      expect(source, name).toContain('class="editor-tab-flag"');
    }
  });

  // A fieldset with no section is a field the operator cannot reach.
  it("gives every fieldset in the files editor a section", () => {
    const opens = files.match(/<fieldset[^>]*>/g) ?? [];
    expect(opens.length).toBeGreaterThan(3);
    for (const tag of opens) {
      expect(tag, "fieldset without a section: " + tag).toMatch(/v-show="editorTab === '(display|content|operations)'"/);
    }
  });

  // The pane is 380px of node rows on one screen and 520px of rendered
  // configuration on the other. One mechanism, one parameter.
  it("gives the wide pane its own breakpoint and its own pinning", () => {
    expect(files).toContain('data-pane="wide"');
    expect(styles).toMatch(/@container \(min-width: 1180px\)[\s\S]{0,400}\.editor-layout\[data-pane="wide"\]/);
    // Each block states its columns AND its pinning. Written as one blanket
    // rule plus overrides, the override reverted the columns and left the
    // pinning, so the wide pane was stacked full width and sticky at once.
    const narrow = styles.slice(styles.indexOf("@container (min-width: 1040px)"), styles.indexOf("@container (min-width: 1180px)"));
    const wide = styles.slice(styles.indexOf("@container (min-width: 1180px)"));
    expect(narrow).toMatch(/\.editor-layout:not\(\[data-pane="wide"\]\) > \.editor-side\s*\{[^}]*position:\s*sticky/s);
    expect(wide).toMatch(/\.editor-layout\[data-pane="wide"\] > \.editor-side\s*\{[^}]*position:\s*sticky/s);
    // Outside a query the pane is never pinned: a sticky full-width block is a
    // block that covers the form under it.
    const base = styles.slice(0, styles.indexOf("@container (min-width: 1040px)"));
    expect(base).toMatch(/\.editor-side\s*\{[^}]*position:\s*static/s);
  });
});
