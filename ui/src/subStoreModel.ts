export interface EndpointValidation {
  value?: string;
  error?: string;
}

export function validateEndpoint(input: string): EndpointValidation {
  const raw = input.trim();
  if (!raw) return { error: "Sub-Store endpoint is required" };
  if (raw.length > 2048 || hasControl(raw)) return { error: "Endpoint is too long or contains invalid characters" };
  if (hasUnsafePathSegment(raw)) return { error: "Endpoint path contains an unsafe segment" };
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return { error: "Enter an absolute HTTP or HTTPS endpoint" };
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return { error: "Endpoint must use HTTP or HTTPS" };
  if (url.username || url.password) return { error: "Endpoint cannot include URL credentials" };
  if (url.search || url.hash) return { error: "Endpoint cannot include a query or fragment" };
  if (!url.hostname || url.pathname.split("/").every((segment) => !segment)) return { error: "Include the Sub-Store secret path" };
  if (url.protocol === "http:" && !isLoopback(url.hostname)) return { error: "Remote endpoints must use HTTPS" };
  return { value: url.toString().replace(/\/$/, "") };
}

export function validateCollection(input: string): string | undefined {
  const value = input.trim();
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value)) {
    return "Collection must start with a letter or number and use only letters, numbers, dot, underscore, or hyphen";
  }
  return undefined;
}

export function safeErrorMessage(error: unknown, fallback: string): string {
  const raw = error instanceof Error ? error.message : fallback;
  const redacted = raw.replace(/https?:\/\/[^\s"']+/gi, "[endpoint]").trim();
  return redacted || fallback;
}

export function statusLabel(status?: { reachable: boolean }): string {
  if (!status) return "Not checked";
  return status.reachable ? "Reachable" : "Unavailable";
}

function isLoopback(hostname: string): boolean {
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (host === "localhost" || host === "::1") return true;
  const parts = host.split(".");
  return parts.length === 4 && parts[0] === "127" && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
}

function hasUnsafePathSegment(raw: string): boolean {
  const scheme = raw.indexOf("://");
  if (scheme < 0) return false;
  const slash = raw.indexOf("/", scheme + 3);
  if (slash < 0) return false;
  const path = raw.slice(slash).split(/[?#]/, 1)[0];
  return path.split("/").some((segment) => {
    try {
      const decoded = decodeURIComponent(segment).toLowerCase();
      return decoded === "." || decoded === ".." || hasControl(decoded);
    } catch {
      return true;
    }
  });
}

function hasControl(value: string): boolean {
  return Array.from(value).some((character) => {
    const code = character.codePointAt(0) ?? 0;
    return code < 0x20 || code === 0x7f;
  });
}
