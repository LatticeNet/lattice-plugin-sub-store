/**
 * hostClipboard.ts, copying from inside a frame that is not allowed to copy.
 *
 * The plugin document is sandboxed `allow-scripts` without `allow-same-origin`,
 * so it runs in an opaque origin and Permissions Policy denies it the async
 * Clipboard API outright. `navigator.clipboard.writeText` here does not fail
 * quietly and it does not fall back: it throws
 *
 *   NotAllowedError: Failed to execute 'writeText' on 'Clipboard': The
 *   Clipboard API has been blocked because of a permissions policy applied to
 *   the current document.
 *
 * and it logs a permissions-policy violation to the console every time. So this
 * module does not attempt it inside a host frame. It asks the console to copy,
 * the way `navigate.ts` asks the console to change views: the host is a
 * top-level same-origin document, it holds the permission, and the operator's
 * click inside this frame still counts as the gesture behind the host's write
 * because user activation propagates from a sandboxed child to its ancestors.
 *
 * Outside a host frame (the dev harness, a standalone page) there is no host to
 * ask and the local clipboard is the only path, so it is used directly.
 *
 * Every caller gets a plain boolean, and false is a real answer that has to be
 * shown to the operator rather than swallowed: the value goes on screen in a
 * selectable field, because a copy button that neither copies nor tells you
 * what to do instead is the bug this module exists to fix.
 */

import { hostOriginFromHash } from "./navigate";

export const CLIPBOARD_MESSAGE_TYPE = "lattice.plugin.clipboard";
export const CLIPBOARD_ACK_TYPE = "lattice.host.clipboard";

/**
 * How long to wait for the host's answer.
 *
 * The host bounds its own clipboard work at 3s and answers every request it can
 * address, so a silence longer than this is a host that does not implement the
 * action at all (an older console) rather than one still working. Treated as a
 * refusal, which puts the value on screen: being told to copy by hand is a
 * worse outcome than a working button and a better one than a dead button.
 */
const ACK_TIMEOUT_MS = 5_000;

let sequence = 0;

interface Channel {
  nonce: string;
  hostOrigin: string;
}

/**
 * The channel this frame was embedded with, or null when there is no host to
 * ask. Read from the frame URL fragment, the same single source the bridge
 * pins, so a copy request can only ever go to the host that framed us.
 */
export function readClipboardChannel(hash: string, win: Window): Channel | null {
  if (win.parent === win) return null;
  const nonce = new URLSearchParams(hash.replace(/^#/, "")).get("lattice_nonce");
  if (!nonce || nonce.length < 16 || nonce.length > 128) return null;
  const hostOrigin = hostOriginFromHash(hash);
  if (!hostOrigin) return null;
  return { nonce, hostOrigin };
}

/**
 * Ask the host to copy, and resolve with what actually happened.
 *
 * Exported for the tests; callers want `copyText`. The listener is removed on
 * every exit including the timeout, so a host that answers late cannot resolve
 * a settled request or leak a handler per copy.
 */
export function requestHostCopy(
  text: string,
  channel: Channel,
  win: Window,
  timeoutMs = ACK_TIMEOUT_MS,
): Promise<boolean> {
  return new Promise<boolean>((resolve) => {
    const id = `clip-${++sequence}-${Date.now()}`;
    let settled = false;

    function finish(ok: boolean): void {
      if (settled) return;
      settled = true;
      win.clearTimeout(timer);
      win.removeEventListener("message", onMessage);
      resolve(ok);
    }

    function onMessage(event: MessageEvent): void {
      // Same identity rules the bridge client uses: the host window, the exact
      // pinned origin, our nonce, and our own request id.
      if (event.source !== win.parent || event.origin !== channel.hostOrigin) return;
      const data = event.data as Record<string, unknown> | null;
      if (!data || typeof data !== "object") return;
      if (data.type !== CLIPBOARD_ACK_TYPE || data.nonce !== channel.nonce || data.id !== id) return;
      finish(data.ok === true);
    }

    const timer = win.setTimeout(() => finish(false), timeoutMs);
    win.addEventListener("message", onMessage);
    try {
      win.parent.postMessage(
        { type: CLIPBOARD_MESSAGE_TYPE, nonce: channel.nonce, id, text },
        channel.hostOrigin,
      );
    } catch {
      finish(false);
    }
  });
}

/**
 * Put text on the operator's clipboard by whatever route this document has, and
 * say honestly whether it landed. Never throws: a caller's job is to show the
 * value when this returns false, not to handle an exception.
 */
export async function copyText(text: string, win: Window = window): Promise<boolean> {
  const channel = readClipboardChannel(win.location.hash, win);
  if (channel) return requestHostCopy(text, channel, win);
  try {
    await win.navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}
