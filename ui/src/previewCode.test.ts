import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const SOURCE_ROOT = new URL(".", import.meta.url);

function source(path: string): string {
  return readFileSync(new URL(path, SOURCE_ROOT), "utf8");
}

describe("rendered documents use the shared read-only code viewer", () => {
  const targetSheet = source("components/TargetSheet.vue");
  const filesScreen = source("screens/FilesScreen.vue");
  const editor = source("components/CodeEditor.vue");
  const styles = source("styles.css");

  it("removes the three raw preformatted preview surfaces", () => {
    expect(targetSheet).not.toContain('<pre class="result-doc"');
    expect(filesScreen).not.toContain('<pre class="output-area mono"');
    expect(filesScreen).not.toContain('<pre class="row-popover-document mono"');
  });

  // A read-only preview must not mount an editor. CodeMirror installs its
  // layout and highlighting as stylesheets it creates at runtime, and the
  // plugin frame's policy has no 'unsafe-inline', so in production every one of
  // those rules was dropped and the preview rendered line numbers over nothing.
  // The viewer is styled by the bundle's own stylesheet, which the policy
  // already allows.
  it("renders every preview through the document view, not an editor", () => {
    expect(targetSheet).toMatch(/<DocumentView[\s\S]*?class="result-doc"/);
    expect(filesScreen).toMatch(/<DocumentView\s+class="output-area"/);
    // The files drawer no longer holds a viewer of its own: the row menu opens
    // the same sheet the name and the » open (see filesList.test.ts).
    expect(filesScreen).not.toContain("row-popover-document");
    for (const [name, markup] of [
      ["TargetSheet.vue", targetSheet],
      ["FilesScreen.vue", filesScreen],
    ] as const) {
      const previews = markup.match(/<CodeEditor[^>]*(preview|readonly)/g) ?? [];
      expect(previews, name + " mounts an editor for a read-only preview").toHaveLength(0);
    }
  });

  it("gives each read-only viewer a visible accessible name", () => {
    expect(targetSheet).toMatch(/class="result-doc"[\s\S]*?:aria-labelledby=/);
    expect(filesScreen).toMatch(/class="output-area"[\s\S]*?:aria-labelledby=/);
  });

  // Every call is flattened to plain data in one place. A screen that reaches
  // for the bridge directly skips that and posts its reactive objects at the
  // host, where the structured clone rejects them and the call never leaves.
  it("routes every call through the one door that flattens the payload", () => {
    expect(source("client.ts")).toMatch(/callMethod[\s\S]*?wireSafe\(payload\)/);
    for (const file of ["screens/SubscriptionsScreen.vue", "screens/FilesScreen.vue", "screens/SettingsScreen.vue", "useSubscriptions.ts"]) {
      expect(source(file), file + " calls the bridge directly").not.toMatch(/bridge\.call\(/);
    }
  });

  it("keeps preview configuration out of the editable keymap", () => {
    expect(editor).toContain("readonly: props.readonly");
    expect(source("codemirror.ts")).toContain("EditorView.editable.of(false)");
  });

  // The editors that remain still create their stylesheets at runtime, so they
  // only render at all if the document carries the nonce the server minted and
  // named in style-src.
  it("asks for a style nonce and hands it to the editor first", () => {
    expect(source("../index.html")).toContain('name="lattice-csp-nonce"');
    expect(source("../index.html")).toContain("__LATTICE_CSP_NONCE__");
    const cm = source("codemirror.ts");
    expect(cm).toContain("EditorView.cspNonce.of(cspNonce())");
    expect(cm).toMatch(/cspNonce\(\)[\s\S]*?__LATTICE_CSP_NONCE__/);
  });

  it("keeps the plain-text fallback visible and says when highlighting failed", () => {
    expect(editor).toContain('v-if="!ready"');
    expect(editor).toContain("failed.value = true");
    expect(editor).toContain('class="code-editor-fallback-note" role="status"');
    expect(editor).toContain("Syntax highlighting is unavailable.");
  });

  it("lets the current draft source decide whether its retained url is live", () => {
    expect(filesScreen).toContain(
      "has_url: isRemote.value && !!draft.value.url.trim()",
    );
  });

  it("lets fieldsets shrink inside a narrow plugin viewport", () => {
    expect(styles).toMatch(/\.editor-group\s*\{[^}]*min-width:\s*0[^}]*width:\s*100%/s);
  });

  /**
   * Each pane owns its scroll.
   *
   * The sheet used to be one scroll surface with three sticky layers inside it
   * — the header, the client rail and the output toolbar — so the whole sheet
   * moved while pieces of it stayed nailed down. Reading a long document
   * dragged the client list past its own heading.
   */
  // One scroller per document (DESIGN-PROGRAM 1). The sheet used to be a
  // fixed frame with three scrollers inside it, and reaching a row meant
  // guessing which one owned the wheel. There is still exactly one; which
  // element owns it is what changed. It was the page, back when the panel was
  // positioned in document coordinates because the frame had no viewport. The
  // frame is a viewport now, the panel is fixed inside it, and a fixed panel
  // that is as tall as a 3,845px configuration is a panel with most of itself
  // off screen. So the panel owns the wheel, and nothing inside it does.
  it("is one scroller with two pinned strips, and nothing else scrolls", () => {
    const decl = styles.replace(/\/\*[\s\S]*?\*\//g, "");
    expect(decl).toMatch(/\.sheet\s*\{[^}]*position:\s*fixed/s);
    expect(decl).toMatch(/\.sheet\s*\{[^}]*overflow-y:\s*auto/s);
    expect(decl).not.toMatch(/\.sheet\s*\{[^}]*overflow:\s*hidden/s);
    expect(decl).not.toMatch(/\.sheet-scrim\s*\{[^}]*overflow-y:\s*auto/s);
    // Neither pane scrolls on its own either.
    expect(decl).not.toMatch(/\.target-controls\s*\{[^}]*overflow-y:\s*auto/s);
    expect(decl).not.toMatch(/\.output-panel\s*\{[^}]*overflow-y:\s*auto/s);
    // Two strips are pinned, and they stack. The sheet's own header, because
    // the sheet is as tall as the document it shows and the only way out used
    // to scroll off the top with the first screenful; and under it the
    // document's title with its copy action, so a long document can be copied
    // from wherever the reader is in it. Nothing else is: three pinned layers
    // over one scroller is what made the sheet feel nailed down while
    // everything moved.
    for (const rule of ["\\.target-controls", "\\.target-output-toolbar"]) {
      const m = new RegExp(rule + "\\s*\\{[^}]*position:\\s*sticky", "s");
      expect(decl, rule + " is still pinned").not.toMatch(m);
    }
    expect(decl).toMatch(/\.sheet-head\s*\{[^}]*position:\s*sticky/s);
    expect(decl).toMatch(/\.output-heading\s*\{[^}]*position:\s*sticky/s);
    // The second strip clears the first rather than opening underneath it.
    expect(decl).toMatch(/\.output-heading\s*\{[^}]*top:\s*var\(--lt-sheet-head-h\)/s);
    // Nor does the panel body: the panel is the scroller, its body is not a
    // second one inside it.
    expect(decl).not.toMatch(/\.sheet-body\s*\{[^}]*overflow-y:\s*auto/s);
    expect(decl).not.toMatch(/\.sheet-body\s*\{[^}]*max-height/s);
  });

  it("gives the preview workspace a dominant evidence pane", () => {
    // A right-side panel wide enough for the rail and an 80-column document,
    // inset from the frame edge by one step so its shadow has room to land.
    expect(styles).toMatch(/\.sheet\s*\{[^}]*width:\s*min\(960px, calc\(100% - var\(--space-3\) \* 2\)\)/s);
    // Record work is the same panel at a form's width.
    expect(styles).toMatch(/\.sheet\[data-size="record"\]\s*\{[^}]*width:\s*min\(440px/s);
    expect(styles).toMatch(/\.target-workspace-body\s*\{[^}]*grid-template-columns/s);
  });

  it("keeps line numbers out of document selection", () => {
    expect(source("codemirror.ts")).toContain('userSelect: "none"');
    // The viewer numbers with a counter rather than DOM text, so selecting the
    // document and copying it yields the document and not a column of digits.
    expect(styles).toMatch(/\.doc-line::before\s*\{[^}]*content:\s*counter\(doc-line\)/s);
    expect(styles).toMatch(/\.doc-line::before\s*\{[^}]*user-select:\s*none/s);
  });

  // One scroller per document (DESIGN-PROGRAM 1): the viewer is as tall as
  // what it holds and the page scrolls it. It used to cap itself at a row
  // count, which put a second wheel inside every panel that showed a
  // document, and each panel then overrode the cap one by one. Growth is
  // bounded by the tokenizer's MAX_RENDERED_LINES, which the viewer reports.
  it("lets the document view grow instead of scrolling inside itself", () => {
    const decl = styles.replace(/\/\*[\s\S]*?\*\//g, "");
    expect(decl).not.toMatch(/\.doc-scroll\s*\{[^}]*max-height/s);
    expect(decl).not.toMatch(/\.doc-scroll\s*\{[^}]*overflow/s);
    expect(source("components/DocumentView.vue")).not.toMatch(/\brows\??:/);
    expect(source("components/DocumentView.vue")).toContain("doc-truncated");
  });
});
