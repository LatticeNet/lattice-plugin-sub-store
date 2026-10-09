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
import { t } from "./i18n";

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

/** A URL with a scheme: `https://sub.example.com/api?token=…`. */
const SCHEME_URL = /[a-z][a-z0-9+.-]*:\/\/[^\s"'<>]+/gi;
/**
 * A host with a path or a query and no scheme, the way an engine error often
 * quotes what it fetched: `sub.example.com/api?token=…`. A bare host with
 * neither is left alone; it names the provider and carries no token.
 */
const BARE_URL = /\b((?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}(?::\d{1,5})?)([/?][^\s"'<>]*)/gi;

/** Every link inside a sentence, with a scheme or without, masked in place. */
export function maskUrlsIn(text: string): string {
  return text
    .replace(SCHEME_URL, (match) => maskUrl(match))
    .replace(BARE_URL, (_match, host: string, rest: string) => {
      const [path, query] = [rest.split("?")[0] ?? "", rest.includes("?")];
      return `${host}${path && path !== "/" ? "/…" : ""}${query ? "?…" : ""}`;
    });
}

/** Every link inside a sentence cut to its host, or "the provider" when it has none. */
export function hostsIn(text: string): string {
  return text
    .replace(SCHEME_URL, (match) => {
      try {
        return new URL(match).host || t.refresh.theProvider;
      } catch {
        return t.refresh.theProvider;
      }
    })
    .replace(BARE_URL, (_match, host: string) => host);
}

/**
 * Where a quoted response body starts, from "body:" or from a JSON object or
 * array, or -1. A provider's body can hold anything, a token included.
 */
function bodyStart(text: string): number {
  return text.search(/\bbody:\s*|[{[]\s*"/i);
}

/**
 * Why a source's last refresh failed, as a clause for a sentence that already
 * names the record. The engine's reason repeats the record as
 * `subscription "<id>"`, which nobody reads, may quote the link it fetched,
 * and may quote the response body. The id goes wherever it sits, a body is
 * cut at its start, and every link is cut to its host, which names the
 * provider without its token: "provider returned status 503 from
 * sub.example-provider.com".
 */
export function refreshFailureText(lastError: string | undefined): string {
  let text = (lastError ?? "").trim();
  if (!text) return "";
  text = text.replace(/\bsubscription\s+"[^"]*"\s*:?\s*/gi, "");
  const body = bodyStart(text);
  if (body >= 0) {
    const before = text.slice(0, body).replace(/[\s:,;-]+$/, "");
    text = before ? t.refresh.bodyHiddenAfter(before) : t.refresh.bodyHidden;
  }
  return hostsIn(text).replace(/\s{2,}/g, " ").trim();
}
