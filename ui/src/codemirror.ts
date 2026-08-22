/**
 * CodeMirror assembly, isolated so it becomes its own lazy chunk.
 *
 * Only CodeEditor.vue imports this module, and only via dynamic import(): the
 * list screens ship none of it, and the editor chunk is fetched from inside
 * the signed bundle the first time an editing surface mounts. The sandbox
 * CSP never sees an external host. Keep this file free of imports from the
 * rest of the app, or the chunk boundary quietly dissolves.
 */
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { javascript } from "@codemirror/lang-javascript";
import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import {
  HighlightStyle,
  StreamLanguage,
  bracketMatching,
  syntaxHighlighting,
} from "@codemirror/language";
import { properties } from "@codemirror/legacy-modes/mode/properties";
import { Compartment, EditorState, type Extension } from "@codemirror/state";
import { EditorView, keymap, lineNumbers, placeholder as cmPlaceholder } from "@codemirror/view";
import { tags } from "@lezer/highlight";

export type EditorLanguage = "yaml" | "javascript" | "json" | "ini" | "plain";

function languageExtension(language: EditorLanguage): Extension {
  switch (language) {
    case "yaml":
      return yaml();
    case "javascript":
      return javascript();
    case "json":
      return json();
    case "ini":
      return StreamLanguage.define(properties);
    default:
      return [];
  }
}

/**
 * Colors come from the host's design tokens, so the editor follows the
 * dashboard theme without carrying a palette of its own. The fallbacks keep
 * the dev harness legible when a token is missing.
 */
const chrome = EditorView.theme({
  "&": {
    backgroundColor: "var(--lt-surface)",
    color: "var(--lt-fg)",
    border: "1px solid var(--lt-border)",
    borderRadius: "var(--lt-radius, 6px)",
    fontSize: "var(--lt-text-sm)",
  },
  "&.cm-focused": {
    outline: "none",
    borderColor: "var(--ring, #2c77b8)",
  },
  ".cm-content": {
    fontFamily: "var(--lt-mono)",
    caretColor: "var(--lt-fg)",
    minHeight: "inherit",
  },
  ".cm-gutters": {
    backgroundColor: "var(--lt-surface-2)",
    color: "var(--lt-fg-muted)",
    border: "none",
    borderRight: "1px solid var(--lt-border)",
    fontSize: "var(--lt-text-xs)",
  },
  ".cm-activeLineGutter": { backgroundColor: "transparent", color: "var(--lt-fg)" },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": {
    backgroundColor: "color-mix(in srgb, var(--ring, #2c77b8) 28%, transparent)",
  },
  ".cm-cursor": { borderLeftColor: "var(--lt-fg)" },
  ".cm-placeholder": { color: "var(--lt-fg-muted)" },
});

/** Semantic colors only. The same discipline as the rest of the plugin. */
const highlight = HighlightStyle.define([
  { tag: [tags.keyword, tags.operatorKeyword], color: "var(--lt-accent)" },
  { tag: [tags.string, tags.special(tags.string)], color: "var(--lt-ok)" },
  { tag: [tags.number, tags.bool, tags.null], color: "var(--lt-warn)" },
  { tag: [tags.comment], color: "var(--lt-fg-muted)", fontStyle: "italic" },
  { tag: [tags.propertyName, tags.definition(tags.propertyName)], color: "var(--lt-fg)" },
  { tag: [tags.function(tags.variableName), tags.function(tags.propertyName)], color: "var(--lt-accent)" },
  { tag: [tags.invalid], color: "var(--lt-danger)" },
]);

export interface EditorHandle {
  getValue(): string;
  setValue(value: string): void;
  setLanguage(language: EditorLanguage): void;
  destroy(): void;
}

export function createEditor(options: {
  parent: HTMLElement;
  value: string;
  language: EditorLanguage;
  readonly?: boolean;
  placeholderText?: string;
  /** Element id that names this editor; applied to the contenteditable. */
  ariaLabelledby?: string;
  onChange: (value: string) => void;
}): EditorHandle {
  const languageSlot = new Compartment();
  let escapeArmed = false;
  const contentAttributes: Record<string, string> = {};
  if (options.ariaLabelledby) {
    contentAttributes["aria-labelledby"] = options.ariaLabelledby;
  }
  if (options.readonly) {
    // editable(false) removes the browser editing surface. The explicit
    // tabindex preserves keyboard focus for reading and copying; CodeMirror
    // supplies aria-readonly from the state facet.
    contentAttributes.tabindex = "0";
  }
  const interactionExtensions: Extension[] = options.readonly
    ? [EditorView.editable.of(false)]
    : [
        history(),
        bracketMatching(),
        keymap.of([
          ...defaultKeymap,
          ...historyKeymap,
          {
            key: "Tab",
            run: (view) => {
              if (escapeArmed) {
                escapeArmed = false;
                return false; // let the browser move focus out
              }
              return indentWithTab.run ? indentWithTab.run(view) : false;
            },
            shift: indentWithTab.shift,
          },
        ]),
        // Tab indents, which makes the editor a keyboard trap unless there is a
        // way out: Escape first, then Tab, moves focus on. A preview has no edit
        // keymap, so ordinary Tab leaves it without this two-step escape hatch.
        keymap.of([{ key: "Escape", run: () => { escapeArmed = true; return false; } }]),
      ];
  const view = new EditorView({
    parent: options.parent,
    state: EditorState.create({
      doc: options.value,
      extensions: [
        lineNumbers(),
        ...interactionExtensions,
        languageSlot.of(languageExtension(options.language)),
        syntaxHighlighting(highlight),
        chrome,
        EditorView.lineWrapping,
        EditorState.readOnly.of(!!options.readonly),
        options.placeholderText ? cmPlaceholder(options.placeholderText) : [],
        Object.keys(contentAttributes).length
          ? EditorView.contentAttributes.of(contentAttributes)
          : [],
        EditorView.updateListener.of((update) => {
          if (update.docChanged) options.onChange(update.state.doc.toString());
        }),
      ],
    }),
  });
  return {
    getValue: () => view.state.doc.toString(),
    setValue: (value) => {
      if (value === view.state.doc.toString()) return;
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
    },
    setLanguage: (language) => {
      view.dispatch({ effects: languageSlot.reconfigure(languageExtension(language)) });
    },
    destroy: () => view.destroy(),
  };
}
