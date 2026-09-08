import { maskUrl } from "./urlMask";

/**
 * The URL migrate sends to the backend.
 *
 * Official Sub-Store's address bar is the frontend plus `?api=` pointing at
 * the backend (origin plus the secret path). Operators paste that. The
 * backend refuses a query string and requires the secret path, so this
 * unwraps `api` when present. The secret stays in the resolved value; it is
 * never written to source, and callers must not print it.
 */
export function resolveSubStoreBase(raw: string): string {
  const value = raw.trim();
  if (!value) return "";
  try {
    const url = new URL(value);
    const api = url.searchParams.get("api")?.trim();
    return api || value;
  } catch {
    return value;
  }
}

export type SubStoreBase =
  | { ok: true; origin: string; masked: string }
  | { ok: false; reason: string };

/**
 * What the import confirm can say without repeating the secret path.
 *
 * Origin-only input is refused on purpose: the running instance authenticates
 * by path, and a host method that discovered that path is not something this
 * UI can invent.
 */
export function describeSubStoreBase(raw: string): SubStoreBase {
  const value = resolveSubStoreBase(raw);
  if (!value) return { ok: false, reason: "Paste the backend URL of the running Sub-Store." };
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return { ok: false, reason: "This is not an absolute http(s) URL." };
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return { ok: false, reason: "Use http or https." };
  }
  if (!url.host) return { ok: false, reason: "The URL needs a host." };
  if (url.search || url.hash) {
    return {
      ok: false,
      reason: "Use the backend URL (the value after ?api=), not the frontend address with its query string still attached.",
    };
  }
  if (!url.pathname || url.pathname === "/") {
    return {
      ok: false,
      reason: "The origin is not enough. The path after the port is the API secret; paste the backend URL from the running Sub-Store.",
    };
  }
  return { ok: true, origin: `${url.protocol}//${url.host}`, masked: maskUrl(value) };
}
