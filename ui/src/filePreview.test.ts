import { describe, expect, it } from "vitest";

import { FILE_TYPE_CONFIG, FILE_TYPE_SCRIPT, KIND_FILE, KIND_SUB, SOURCE_LOCAL, SOURCE_REMOTE } from "./client";
import { filePreviewSupport, isFileRecord, type FileRecordFacts } from "./filePreview";

function file(extra: Partial<FileRecordFacts> = {}): FileRecordFacts {
  return {
    kind: KIND_FILE,
    file_type: FILE_TYPE_CONFIG,
    source: SOURCE_LOCAL,
    has_url: false,
    step_count: 0,
    ...extra,
  };
}

describe("filePreviewSupport", () => {
  it("supports a self-contained local document", () => {
    expect(filePreviewSupport(file())).toEqual({ supported: true, reason: "" });
  });

  it("refuses a file whose proxies come from another record", () => {
    const support = filePreviewSupport(file({ node_source: "merge-cd-openjobs" }));
    expect(support.supported).toBe(false);
    expect(support.reason).toContain("another record");
  });

  it("refuses a fetched template, by source or by url alone", () => {
    expect(filePreviewSupport(file({ source: SOURCE_REMOTE })).supported).toBe(false);
    expect(filePreviewSupport(file({ source: "", has_url: true })).reason).toContain("fetched from a link");
  });

  it("refuses a persisted local record that still carries a url", () => {
    expect(filePreviewSupport(file({ source: SOURCE_LOCAL, has_url: true })).supported).toBe(false);
  });

  it("refuses a script file", () => {
    expect(filePreviewSupport(file({ file_type: FILE_TYPE_SCRIPT })).reason).toContain("built by a program");
  });

  it("refuses a file with an operator chain", () => {
    expect(filePreviewSupport(file({ step_count: 2 })).reason).toContain("operations");
  });

  /**
   * The backend checks in this order and reports one reason. A UI naming a
   * different one would send the operator after the wrong fact.
   */
  it("names the node source first when a record trips several guards", () => {
    const support = filePreviewSupport(
      file({ node_source: "nodes", file_type: FILE_TYPE_SCRIPT, step_count: 3, has_url: true }),
    );
    expect(support.reason).toContain("another record");
  });

  it("always ends by naming what does work", () => {
    expect(filePreviewSupport(file({ node_source: "nodes" })).reason).toContain("Show the document");
  });

  it("leaves non-file records alone", () => {
    expect(filePreviewSupport({ kind: KIND_SUB, has_url: true, step_count: 4 }).supported).toBe(true);
    expect(filePreviewSupport(null).supported).toBe(true);
  });
});

describe("isFileRecord", () => {
  it("is true only for the file kind", () => {
    expect(isFileRecord({ kind: KIND_FILE })).toBe(true);
    expect(isFileRecord({ kind: KIND_SUB })).toBe(false);
    expect(isFileRecord(undefined)).toBe(false);
  });
});
