/**
 * navigate.ts, asking the console to change views from inside the frame.
 *
 * The bridge has no navigate capability and the frame is sandboxed, so the one
 * channel that exists is a postMessage to the host window. The console listens
 * for `lattice:navigate` and routes itself; anything else the frame might want
 * (reading the shares list, opening a share directly) crosses the plugin
 * boundary and is deliberately not done here.
 *
 * The target origin is the `host_origin` the frame URL fragment carries, the
 * same value the bridge validates and pins for inbound messages. Reading it
 * again here, rather than trusting a second source, means the navigate request
 * can only ever go to the host this frame was embedded by.
 */

export const NAVIGATE_MESSAGE_TYPE = "lattice:navigate";

/** The dashboard's share management view, where an existing share is changed. */
export const SHARES_LIST_ROUTE = "/network/subscription-shares";

/** The same view, pre-opened on the create form. */
export function sharesRoute(recordName: string): string {
  return `/network/subscription-shares?create=1&for=${encodeURIComponent(recordName)}`;
}

/**
 * The host origin from the frame URL fragment, or null when there is nothing
 * trustworthy to post to. Fail-closed, mirroring the bridge: an absent or
 * non-http(s) value is not an origin, it is a reason to stay silent.
 */
export function hostOriginFromHash(hash: string): string | null {
  const raw = new URLSearchParams(hash.replace(/^#/, "")).get("host_origin")?.trim();
  if (!raw) return null;
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return null;
  return url.origin;
}

/** Fire-and-forget: the console answers by navigating, not by replying. */
export function postNavigate(win: Window, route: string, hostOrigin: string): void {
  win.parent.postMessage({ type: NAVIGATE_MESSAGE_TYPE, route }, hostOrigin);
}
