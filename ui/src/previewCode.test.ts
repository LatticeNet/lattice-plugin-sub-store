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
    expect(filesScreen).toMatch(/<DocumentView\s+class="row-popover-document"/);
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
    expect(filesScreen).toMatch(/class="row-popover-document"[\s\S]*?:aria-labelledby=/);
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

  it("gives the preview workspace a dominant evidence pane", () => {
    expect(styles).toMatch(/\.sheet\s*\{[^}]*width:\s*min\(var\(--lt-preview-workspace-w\)/s);
    expect(styles).toMatch(/\.target-workspace-body\s*\{[^}]*grid-template-columns/s);
  });

  it("keeps line numbers out of document selection", () => {
    expect(source("codemirror.ts")).toContain('userSelect: "none"');
    // The viewer numbers with a counter rather than DOM text, so selecting the
    // document and copying it yields the document and not a column of digits.
    expect(styles).toMatch(/\.doc-line::before\s*\{[^}]*content:\s*counter\(doc-line\)/s);
    expect(styles).toMatch(/\.doc-line::before\s*\{[^}]*user-select:\s*none/s);
  });

  // A viewer that grows without bound leaves a long document past the bottom of
  // the sheet with nothing to scroll it, which is what the previous preview did.
  it("gives the document view its own bounded scroll surface", () => {
    expect(styles).toMatch(/\.doc-scroll\s*\{[^}]*max-height:[^}]*overflow:\s*auto/s);
  });
});
