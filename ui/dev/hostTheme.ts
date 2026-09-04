/**
 * What the console sends the frame, verbatim.
 *
 * The harness has no host, so without this the plugin renders against its own
 * `:root` fallbacks and anyone polishing it is polishing colours the operator
 * never sees. It used to send ten approximate hexes, which is how the harness
 * spent a while showing a warm-neutral palette the console had already
 * dropped.
 *
 * These are the console's values, copied from `lattice-dashboard`: the surfaces
 * and status colours from `src/style/app.css` (light from its `:root`, dark
 * from its `.dark`), and the --primary family from `src/theme/palettes.ts`.
 * That second source matters and was got wrong once: app.css's `:root` is only
 * the pre-mount fallback, and the theme store repaints --primary, --ring and
 * --primary-foreground inline from the default "teal" palette on the first
 * frame. Copying the accent out of app.css sent indigo, so every light-mode
 * pass judged chips, buttons, focus rings and the overlay tint against an
 * accent production replaced -- which is the exact failure this file exists to
 * prevent. app.css's `.dark` already carries teal, so only light was wrong.
 *
 * They are applied the way the bridge applies them, as inline custom properties
 * on `<html>` with `data-theme` and `color-scheme` set first, so the harness
 * exercises the real precedence: an inline property beats the plugin's own
 * declaration, and a name the host does not send falls back to it.
 *
 * Token contract v2, 45 names. Keep this list and `src/tokens.css` in step;
 * `src/tokenContract.test.ts` fails if the plugin reads a name neither one
 * declares.
 */
export type HostScheme = "light" | "dark";

const SHARED: Record<string, string> = {
  "--radius-sm": "3px",
  "--radius-md": "4px",
  "--radius-lg": "6px",
  "--radius-xl": "8px",
  "--radius": "4px",

  "--row-h": "40px",
  "--row-h-compact": "32px",

  "--space-1": "4px",
  "--space-2": "8px",
  "--space-3": "12px",
  "--space-4": "16px",
  "--space-5": "24px",
  "--space-6": "32px",
  "--space-7": "48px",

  "--font-mono": 'ui-monospace, "SF Mono", "JetBrains Mono", "Menlo", "Consolas", monospace',
  "--text-body": "14px",
  "--text-mono": "12px",

  "--duration-fast": "100ms",
  "--duration-base": "200ms",
  "--ease-out": "cubic-bezier(0.19, 1, 0.22, 1)",
};

const LIGHT: Record<string, string> = {
  ...SHARED,
  "--background": "oklch(0.99 0.0015 280)",
  "--foreground": "oklch(0.21 0.02 281)",
  "--card": "oklch(1 0 0)",
  "--card-foreground": "oklch(0.21 0.02 281)",
  "--muted": "oklch(0.965 0.006 280)",
  "--muted-foreground": "oklch(0.524 0.022 281)",
  "--accent": "oklch(0.95 0.014 279)",
  "--accent-foreground": "oklch(0.30 0.05 279)",
  "--border": "oklch(0.91 0.006 281)",
  // PALETTES.teal.light, not app.css's :root. See the note at the top.
  "--primary": "oklch(0.53 0.105 185)",
  "--primary-foreground": "oklch(0.985 0.01 180)",
  "--ring": "oklch(0.53 0.105 185)",
  "--destructive": "oklch(0.583 0.231 27.5)",
  "--destructive-foreground": "oklch(0.985 0.01 20)",
  "--success": "oklch(0.62 0.16 150)",
  "--success-foreground": "oklch(0.985 0.01 155)",
  "--warning": "oklch(0.72 0.16 73)",
  "--warning-foreground": "oklch(0.26 0.05 70)",
  "--info": "oklch(0.6 0.14 240)",
  "--info-foreground": "oklch(0.985 0.01 240)",
  "--success-text": "oklch(0.5 0.14 150)",
  "--warning-text": "oklch(0.52 0.13 73)",
  "--info-text": "oklch(0.5 0.13 240)",
  "--shadow-overlay":
    "0 1px 2px oklch(0 0 0 / 6%), 0 8px 24px -6px oklch(0 0 0 / 12%), 0 4px 40px -4px color-mix(in oklab, var(--primary) 8%, transparent)",
  "--shadow-raised": "0 1px 2px oklch(0 0 0 / 5%)",
};

const DARK: Record<string, string> = {
  ...SHARED,
  "--background": "oklch(0.155 0.012 240)",
  "--foreground": "oklch(0.97 0.004 240)",
  "--card": "oklch(0.195 0.014 240)",
  "--card-foreground": "oklch(0.97 0.004 240)",
  "--muted": "oklch(0.255 0.014 240)",
  "--muted-foreground": "oklch(0.705 0.012 240)",
  "--accent": "oklch(0.285 0.02 235)",
  "--accent-foreground": "oklch(0.97 0.004 240)",
  "--border": "oklch(1 0 0 / 9%)",
  // PALETTES.teal.dark, which is also what app.css's .dark carries.
  "--primary": "oklch(0.81 0.13 180)",
  "--primary-foreground": "oklch(0.17 0.012 240)",
  "--ring": "oklch(0.7 0.12 182)",
  "--destructive": "oklch(0.704 0.191 22.2)",
  "--destructive-foreground": "oklch(0.16 0.02 15)",
  "--success": "oklch(0.706 0.15 156)",
  "--success-foreground": "oklch(0.16 0.02 156)",
  "--warning": "oklch(0.8 0.16 80)",
  "--warning-foreground": "oklch(0.2 0.04 75)",
  "--info": "oklch(0.7 0.12 210)",
  "--info-foreground": "oklch(0.16 0.02 210)",
  "--success-text": "oklch(0.706 0.15 156)",
  "--warning-text": "oklch(0.8 0.16 80)",
  "--info-text": "oklch(0.7 0.12 210)",
  "--shadow-overlay": "0 0 0 1px oklch(1 0 0 / 8%), 0 10px 32px -8px oklch(0 0 0 / 70%)",
  "--shadow-raised": "0 1px 2px oklch(0 0 0 / 40%)",
};

export const HOST_THEME: Record<HostScheme, Record<string, string>> = { light: LIGHT, dark: DARK };

/** The payload the console posts as `lattice.host.theme`. */
export function hostThemeMessage(scheme: HostScheme): {
  type: "lattice.host.theme";
  colorScheme: HostScheme;
  designTokens: Record<string, string>;
} {
  return { type: "lattice.host.theme", colorScheme: scheme, designTokens: HOST_THEME[scheme] };
}

/**
 * Apply it exactly as `applyTheme` in @latticenet/plugin-bridge does: scheme
 * and `data-theme` first, then the tokens as inline properties, and clear the
 * ones a previous scheme set so switching does not leave a stale value behind.
 */
export function applyHostTheme(scheme: HostScheme): void {
  const root = document.documentElement;
  root.style.colorScheme = scheme;
  root.dataset.theme = scheme;
  const next = HOST_THEME[scheme];
  for (const name of Object.keys(HOST_THEME.light)) {
    if (!(name in next)) root.style.removeProperty(name);
  }
  for (const [name, value] of Object.entries(next)) root.style.setProperty(name, value);
}
