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

/** Every URL inside a sentence cut to its host, or "the provider" when it has none. */
export function hostsIn(text: string): string {
  return text.replace(/[a-z][a-z0-9+.-]*:\/\/[^\s"'<>]+/gi, (match) => {
    try {
      return new URL(match).host || "the provider";
    } catch {
      return "the provider";
    }
  });
}

/**
 * Why a source's last refresh failed, as a clause for a sentence that already
 * names the record. The engine's reason opens with `subscription "<id>"`,
 * which repeats the record by an id nobody reads, and quotes the link it
 * fetched; the prefix goes and the link is cut to its host, which names the
 * provider without its token. "provider returned status 503 from
 * sub.example-provider.com".
 */
export function refreshFailureText(lastError: string | undefined): string {
  const raw = (lastError ?? "").trim();
  if (!raw) return "";
  return hostsIn(raw.replace(/^subscription\s+"[^"]*"\s*:?\s*/i, "")).trim();
}
