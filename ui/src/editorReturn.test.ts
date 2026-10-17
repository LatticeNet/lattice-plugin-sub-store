import { describe, expect, it } from "vitest";
import { ref } from "vue";
import type { BridgeClient } from "@latticenet/plugin-bridge";

import { BINDINGS, KIND_FILE, KIND_SUB, SOURCE_LOCAL, type SubscriptionListItem, type SubscriptionRecord } from "./client";
import type { HostContext } from "./host";
import { claimIntent, isCommandIntent, isFleetReturnIntent, isRecordIntent, type PendingIntent } from "./recordIntent";
import { useFileEditor } from "./useFileEditor";
import { useRecordEditor } from "./useRecordEditor";
import { useSubscriptions } from "./useSubscriptions";

/**
 * Where an editor goes when it closes (the wave 5 design review). The
 * Records screen hears every way out through onClosed: a save, Cancel, the
 * discard confirm, keeping the other version of a stale save. It does not
 * hear a reopen on the current version, which closes and opens the editor
 * in one step and must not send an editor the Fleet view opened back there.
 */

type Closed = { id: string | null; saved: boolean };

function hostWith(answers: Record<string, unknown>): HostContext {
  const bridge = {
    call(service: string, method: string) {
      return { promise: Promise.resolve(answers[`${service}/${method}`]), cancel: () => {} };
    },
  } as unknown as BridgeClient;
  return { bridge, init: ref(undefined), bootError: ref(""), available: () => true, resize: async () => {}, pageState: ref({}), sendState: () => {} };
}
const key = (binding: { service: string; method: string }) => `${binding.service}/${binding.method}`;

const record: SubscriptionRecord = { id: "home", name: "home", source: SOURCE_LOCAL, content: "ss://a", revision: "rev-1" };
const listed = { id: "home", name: "home", kind: KIND_SUB, has_url: false, has_inline_content: true, step_count: 0, disabled_step_count: 0, imported: false } as SubscriptionListItem;

async function openRecordEditor(extra: Record<string, unknown> = {}) {
  const host = hostWith({
    [key(BINDINGS.subGet)]: { subscription: record, live_revision: "rev-1" },
    [key(BINDINGS.subList)]: { subscriptions: [listed] },
    [key(BINDINGS.subSave)]: { saved: true, subscription: { ...record, revision: "rev-2" } },
    ...extra,
  });
  const subs = useSubscriptions(host);
  await subs.load();
  const closed: Closed[] = [];
  const editor = useRecordEditor({ host, subs, clearListState: () => {}, onSaved: () => {}, onClosed: (entry) => closed.push(entry) });
  await editor.startEdit("home");
  return { editor, subs, closed };
}

describe("the record editor tells the screen how it closed", () => {
  it("a save that went through closes as saved, with the record's id", async () => {
    const { editor, closed } = await openRecordEditor();
    editor.draft.value.remark = "edited";
    await editor.submit();
    expect(editor.editing.value).toBe(false);
    expect(closed).toEqual([{ id: "home", saved: true }]);
  });

  it("a save the store refused leaves the editor open and says nothing", async () => {
    const { editor, closed } = await openRecordEditor({ [key(BINDINGS.subSave)]: { saved: false } });
    await editor.submit();
    expect(editor.editing.value).toBe(true);
    expect(closed).toEqual([]);
  });

  it("Cancel, and the discard confirm after an edit, close as not saved", async () => {
    const first = await openRecordEditor();
    first.editor.exit.leaveEditor();
    expect(first.closed).toEqual([{ id: "home", saved: false }]);

    const second = await openRecordEditor();
    second.editor.draft.value.remark = "edited";
    second.editor.exit.leaveEditor();
    // A dirty editor asks first; the confirm's Discard is the leave.
    expect(second.editor.exit.discarding.value).toBe(true);
    expect(second.closed).toEqual([]);
    second.editor.cancelEdit();
    expect(second.editor.editing.value).toBe(false);
    expect(second.closed).toEqual([{ id: "home", saved: false }]);
  });

  it("keeping the other version of a stale save closes as not saved; reopening on it does not close at all", async () => {
    const kept = await openRecordEditor();
    kept.subs.saveConflict.value = { conflict: { id: "home", reason: "changed" }, changes: [], summary: "", attempted: record } as never;
    kept.editor.discardMyEdit();
    expect(kept.closed).toEqual([{ id: "home", saved: false }]);

    const reopened = await openRecordEditor();
    reopened.subs.saveConflict.value = { conflict: { id: "home", reason: "changed" }, changes: [], summary: "", attempted: record } as never;
    await reopened.editor.reopenOnCurrent();
    expect(reopened.editor.editing.value).toBe(true);
    expect(reopened.closed).toEqual([]);
  });
});

describe("the file editor tells the screen how it closed", () => {
  const file: SubscriptionRecord = { id: "conf", name: "conf", kind: KIND_FILE, source: SOURCE_LOCAL, content: "a", file_type: "plain" };
  async function openFileEditor() {
    const host = hostWith({
      [key(BINDINGS.subGet)]: { subscription: file },
      [key(BINDINGS.subList)]: { subscriptions: [{ ...listed, id: "conf", name: "conf", kind: KIND_FILE }] },
      [key(BINDINGS.subSave)]: { saved: true, subscription: file },
    });
    const subs = useSubscriptions(host);
    await subs.load();
    const closed: Closed[] = [];
    const editor = useFileEditor({ host, subs, clearListState: () => {}, overlayOpen: () => false, onClosed: (entry) => closed.push(entry) });
    await editor.startEdit("conf");
    return { editor, closed };
  }

  it("on a save, on Cancel and on the discard confirm", async () => {
    const saved = await openFileEditor();
    await saved.editor.submit();
    expect(saved.closed).toEqual([{ id: "conf", saved: true }]);

    const left = await openFileEditor();
    left.editor.exit.leaveEditor();
    expect(left.closed).toEqual([{ id: "conf", saved: false }]);

    const discarded = await openFileEditor();
    discarded.editor.cancelEdit();
    expect(discarded.closed).toEqual([{ id: "conf", saved: false }]);
  });
});

describe("the way back to the Fleet view", () => {
  const back: PendingIntent = {
    fleet: { record: "osaka", identity: "vu_alice", revision: "live", edited: "osaka", saved: true, staged: false, notice: "Saved osaka." },
  };

  it("is an intent of its own, which the Records screen's record and command checks pass over", () => {
    expect(isFleetReturnIntent(back)).toBe(true);
    expect(isRecordIntent(back)).toBe(false);
    expect(isCommandIntent(back)).toBe(false);
    expect(isFleetReturnIntent({ recordId: "osaka", action: "edit", returnTo: { record: "osaka", identity: "vu_alice", revision: "live" } })).toBe(false);
    expect(isFleetReturnIntent(null)).toBe(false);
  });

  it("is taken by the screen that owns it and left for the others", () => {
    const slot = ref<PendingIntent>(back);
    expect(claimIntent(slot, (intent) => isRecordIntent(intent))).toBeNull();
    expect(slot.value).toEqual(back);
    expect(claimIntent(slot, (intent) => isFleetReturnIntent(intent))).toEqual(back);
    expect(slot.value).toBeNull();
  });
});
