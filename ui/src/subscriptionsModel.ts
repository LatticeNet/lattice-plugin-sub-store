export interface FieldValidation {
  value?: string;
  error?: string;
}

/**
 * Subscription record validators. Names follow the same Lattice-safe charset
 * as collection names; URLs are ordinary absolute http(s) URLs — unlike the
 * managed-import endpoint they may carry a query string (providers put tokens
 * there), so only scheme, credentials, host, and control characters are
 * constrained here. Fetch policy itself is enforced server-side by the
 * http:operator-target capability.
 */
export function validateSubscriptionName(input: string): FieldValidation {
  const value = input.trim();
  if (!value) return { error: "Name is required" };
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value)) {
    return { error: "Start with a letter or number; use only letters, numbers, dot, underscore, or hyphen" };
  }
  return { value };
}

export function validateDisplayName(input: string): FieldValidation {
  const value = input.trim();
  if (!value) return { value: undefined };
  if (value.length > 64 || hasControl(value)) {
    return { error: "Display name is too long or contains invalid characters" };
  }
  return { value };
}

export function validateSubscriptionUrl(input: string): FieldValidation {
  const raw = input.trim();
  if (!raw) return { error: "Subscription URL is required" };
  if (raw.length > 2048 || hasControl(raw)) {
    return { error: "URL is too long or contains invalid characters" };
  }
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return { error: "Enter an absolute HTTP or HTTPS URL" };
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return { error: "URL must use HTTP or HTTPS" };
  }
  if (url.username || url.password) {
    return { error: "URL cannot include credentials" };
  }
  if (!url.hostname) {
    return { error: "URL must include a host" };
  }
  if (url.protocol === "http:" && !isLoopback(url.hostname)) {
    return { error: "Remote subscription URLs must use HTTPS" };
  }
  return { value: url.toString() };
}

export function isLoopback(hostname: string): boolean {
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (host === "localhost" || host === "::1") return true;
  const parts = host.split(".");
  return parts.length === 4 && parts[0] === "127" && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
}

function hasControl(value: string): boolean {
  return Array.from(value).some((character) => {
    const code = character.codePointAt(0) ?? 0;
    return code < 0x20 || code === 0x7f;
  });
}
