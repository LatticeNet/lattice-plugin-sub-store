import { describe, expect, it } from "vitest";

import { FILE_TYPE_CONFIG, FILE_TYPE_PLAIN, FILE_TYPE_SCRIPT } from "./client";
import {
  editorLanguageForContentType,
  editorLanguageForFileType,
  editorLanguageForRender,
  editorLanguageLabel,
} from "./previewLanguage";

describe("preview language mapping", () => {
  it.each([
    ["application/yaml", "yaml"],
    ["yaml", "yaml"],
    ["text/x-yaml; charset=utf-8", "yaml"],
    ["yml", "yaml"],
    ["application/json", "json"],
    ["json", "json"],
    ["application/javascript", "javascript"],
    ["text/ecmascript", "javascript"],
    ["text/ini", "ini"],
    ["text/plain", "plain"],
    ["conf", "plain"],
    ["", "plain"],
    ["application/octet-stream", "plain"],
  ])("maps %s to %s without inspecting the document", (contentType, language) => {
    expect(editorLanguageForContentType(contentType)).toBe(language);
  });

  it("derives a saved row preview from its declared file type", () => {
    expect(editorLanguageForFileType(FILE_TYPE_CONFIG)).toBe("yaml");
    expect(editorLanguageForFileType(FILE_TYPE_SCRIPT)).toBe("javascript");
    expect(editorLanguageForFileType(FILE_TYPE_PLAIN)).toBe("plain");
    expect(editorLanguageForFileType(undefined)).toBe("yaml");
  });

  it("uses the selected target output before a plain transport MIME", () => {
    expect(editorLanguageForRender({
      contentType: "text/plain; charset=utf-8",
      produces: "yaml",
    })).toBe("yaml");
    expect(editorLanguageForRender({
      contentType: "text/plain; charset=utf-8",
      produces: "json",
    })).toBe("json");
  });

  it("provides compact human labels for the evidence strip", () => {
    expect(editorLanguageLabel("yaml")).toBe("YAML");
    expect(editorLanguageLabel("javascript")).toBe("JavaScript");
    expect(editorLanguageLabel("plain")).toBe("Plain text");
  });
});
