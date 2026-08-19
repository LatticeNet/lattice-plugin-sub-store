/**
 * filePreview.ts. Which file records `subscription/preview` will answer, and
 * what to tell the operator about the rest.
 *
 * Preview is signed with a two-host-call budget (system-go's
 * subscription_hostcall_budget_test pins it at 2), so previewing a file may
 * not resolve a node source, fetch a remote template, run a stored program or
 * run an operator chain. Each is host-capable work that belongs to render, and
 * the backend refuses those records rather than blow the budget and 502.
 *
 * The refusal is correct. What was wrong is that the UI offered the control
 * anyway and then put the backend's own sentence on screen as the explanation.
 * Every fact the backend checks is on the list row, so the UI can decide the
 * same thing before offering anything, and say it in the operator's terms.
 *
 * The order below mirrors previewFileResponse exactly: node source first, then
 * a fetched template, then a program or a chain. A record can trip several,
 * and the two must name the same one.
 */
import {
  FILE_TYPE_SCRIPT,
  KIND_FILE,
  SOURCE_REMOTE,
  type SubscriptionListItem,
} from "./client";

/** The subset of a list row the decision reads. */
export type FileRecordFacts = Pick<
  SubscriptionListItem,
  "kind" | "file_type" | "node_source" | "source" | "has_url" | "step_count"
>;

export interface FilePreviewSupport {
  /** True when `subscription/preview` returns this file's document. */
  supported: boolean;
  /** Why it will not, in the operator's terms. Empty when supported. */
  reason: string;
}

const SUPPORTED: FilePreviewSupport = { supported: true, reason: "" };

/** A rendered document is the fallback everywhere preview is refused, so each
 *  reason ends by naming it rather than leaving the operator at a dead end. */
const FALLBACK = "Show the document instead: it renders the file in full.";

export function isFileRecord(item: { kind?: string } | null | undefined): boolean {
  return item?.kind === KIND_FILE;
}

/**
 * Whether preview can answer for this file, and why not when it cannot.
 *
 * Non-file records are reported as supported: preview is their normal path and
 * none of these guards apply to them.
 */
export function filePreviewSupport(item: FileRecordFacts | null | undefined): FilePreviewSupport {
  if (!item || !isFileRecord(item)) return SUPPORTED;
  if ((item.node_source ?? "").trim()) {
    return {
      supported: false,
      reason: `This file fills its proxy list from another record, and a preview does not resolve one. ${FALLBACK}`,
    };
  }
  if (item.source === SOURCE_REMOTE || item.has_url) {
    return {
      supported: false,
      reason: `This file's template is fetched from a link, and a preview does not fetch. ${FALLBACK}`,
    };
  }
  if (item.file_type === FILE_TYPE_SCRIPT) {
    return {
      supported: false,
      reason: `This file is built by a program, and a preview does not run one. ${FALLBACK}`,
    };
  }
  if ((item.step_count ?? 0) > 0) {
    return {
      supported: false,
      reason: `This file has operations that run over the document, and a preview does not run them. ${FALLBACK}`,
    };
  }
  return SUPPORTED;
}
