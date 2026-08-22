import {
  FILE_TYPE_PLAIN,
  FILE_TYPE_SCRIPT,
} from "./client";
import type { EditorLanguage } from "./codemirror";

/**
 * Render responses name a MIME-like content type, while CodeMirror needs one
 * of the small language modes already shipped in the lazy editor chunk. Keep
 * unknown output honest as plain text instead of guessing from its contents.
 */
export function editorLanguageForContentType(contentType: string): EditorLanguage {
  const normalized = contentType.trim().toLowerCase();
  if (normalized.includes("yaml") || normalized === "yml") return "yaml";
  if (normalized.includes("json")) return "json";
  if (normalized.includes("javascript") || normalized.includes("ecmascript")) {
    return "javascript";
  }
  if (normalized.includes("ini")) return "ini";
  return "plain";
}

/** A saved file row has no render content type, but its file type is stable. */
export function editorLanguageForFileType(fileType: string | undefined): EditorLanguage {
  if (fileType === FILE_TYPE_SCRIPT) return "javascript";
  if (fileType === FILE_TYPE_PLAIN) return "plain";
  return "yaml";
}

/**
 * `subscription/render` is transported as plain text even when the selected
 * client target produces YAML or JSON. The declared output kind therefore
 * outranks the response MIME. File callers choose their stable file type before
 * reaching this helper. This order mirrors what the operator selected rather
 * than the envelope used to carry it.
 */
export function editorLanguageForRender(options: {
  contentType: string;
  produces?: string;
}): EditorLanguage {
  return editorLanguageForContentType(options.produces || options.contentType);
}

export function editorLanguageLabel(language: EditorLanguage): string {
  switch (language) {
    case "yaml":
      return "YAML";
    case "javascript":
      return "JavaScript";
    case "json":
      return "JSON";
    case "ini":
      return "INI";
    default:
      return "Plain text";
  }
}
