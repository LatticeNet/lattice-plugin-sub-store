import { ref, type Ref } from "vue";

import type { HostContext } from "./host";
import type { ActionId } from "./recordActions";
import type { PaletteCommandId } from "./commandPalette";

/**
 * One pending request from the palette to whichever screen can carry it out.
 *
 * The palette lives in the shell, above both screens, and knows what the
 * operator picked. It cannot run the action: opening a drawer, starting an
 * edit and asking for a delete are things a screen does with its own state,
 * and the screen for a file is not the screen for a subscription.
 *
 * So the palette posts an intent, switches to the owning tab, and the screen
 * consumes it when it next renders. The alternative — hoisting every handler
 * into the shell so the palette can call it — would move a thousand lines of
 * screen state up a level to serve one caller.
 *
 * Keyed by host for the same reason the record catalogue is: a module-level
 * singleton is shared by every test in a file too.
 */
export interface RecordIntent {
  recordId: string;
  action: ActionId;
}

export interface CommandIntent {
  command: PaletteCommandId;
}

export type PendingIntent = RecordIntent | CommandIntent | null;

const pending = new WeakMap<object, Ref<PendingIntent>>();

export function recordIntent(host: HostContext): Ref<PendingIntent> {
  const existing = pending.get(host);
  if (existing) return existing;
  const fresh = ref<PendingIntent>(null);
  pending.set(host, fresh);
  return fresh;
}

export function isRecordIntent(intent: PendingIntent): intent is RecordIntent {
  return intent !== null && "recordId" in intent;
}

export function isCommandIntent(intent: PendingIntent): intent is CommandIntent {
  return intent !== null && "command" in intent;
}

/**
 * Take the intent if it is for this screen, leaving it in place otherwise.
 *
 * A screen must not consume an intent aimed at its sibling: both are kept
 * alive, both watch, and the one that answers first would swallow it. `owns`
 * is the screen's own test — usually whether it holds a record with that id.
 */
export function claimIntent(
  slot: Ref<PendingIntent>,
  owns: (intent: RecordIntent | CommandIntent) => boolean,
): PendingIntent {
  const intent = slot.value;
  if (intent === null || !owns(intent)) return null;
  slot.value = null;
  return intent;
}
