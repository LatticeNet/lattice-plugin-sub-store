/**
 * urlMask.ts, what a provider link looks like when it is read, not edited.
 *
 * A provider link carries the provider's token, in the query string or in the
 * path, so printing it is printing a credential. Every read view masks it
 * after the host: `https://host/…?…`. The host is what an operator needs to
 * recognise the record; the rest is revealed on request, for a minute, by
 * the control beside it. Errors go through safeErrorMessage, which replaces a
 * whole URL; this keeps the part that identifies the provider.
 */

/** Sixty seconds, the length of a reveal before the field masks itself again. */
export const REVEAL_MS = 60_000;

/**
 * `https://host/…?…` for a URL, with userinfo, path, query and fragment each
 * reduced to an ellipsis when present. A string that is not a URL with a
 * host is masked whole: nothing in it can be shown responsibly.
 */
export function maskUrl(raw: string): string {
  const value = raw.trim();
  if (!value) return "";
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return "…";
  }
  if (!url.host) return "…";
  let out = `${url.protocol}//`;
  if (url.username || url.password) out += "…@";
  out += url.host;
  if (url.pathname && url.pathname !== "/") out += "/…";
  if (url.search) out += "?…";
  if (url.hash) out += "#…";
  return out;
}

/** Every scheme://… URL inside a sentence, masked in place. */
export function maskUrlsIn(text: string): string {
  return text.replace(/[a-z][a-z0-9+.-]*:\/\/[^\s"'<>]+/gi, (match) => maskUrl(match));
}
