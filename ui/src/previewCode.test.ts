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

  it("renders every preview through CodeEditor in explicit preview mode", () => {
    expect(targetSheet).toMatch(/<CodeEditor\s+class="result-doc"[\s\S]*?preview[\s\S]*?readonly/);
    expect(filesScreen).toMatch(/<CodeEditor\s+class="output-area"[\s\S]*?preview[\s\S]*?readonly/);
    expect(filesScreen).toMatch(/<CodeEditor\s+class="row-popover-document"[\s\S]*?preview[\s\S]*?readonly/);
    expect(editor).toContain("preview?: boolean");
  });

  it("gives each read-only viewer a visible accessible name", () => {
    expect(targetSheet).toMatch(/class="result-doc"[\s\S]*?:aria-labelledby=/);
    expect(filesScreen).toMatch(/class="output-area"[\s\S]*?:aria-labelledby=/);
    expect(filesScreen).toMatch(/class="row-popover-document"[\s\S]*?:aria-labelledby=/);
  });

  it("keeps preview configuration out of the editable keymap", () => {
    expect(editor).toContain("preview: false");
    expect(editor).toContain("readonly: props.readonly");
    expect(source("codemirror.ts")).toContain("EditorView.editable.of(false)");
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
});
