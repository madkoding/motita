// The colour theme of the page: a mode (light, dark or whatever the system prefers) and a colour
// scheme, saved in localStorage and applied to <html> as data attributes. The palettes themselves
// live in themes.css; public/theme-init.js applies the saved choice before the first paint.
export const THEME_KEY = "motita.ui.theme";
export type Mode = "system" | "light" | "dark";
export type ResolvedMode = "light" | "dark";
export type Scheme =
  | "motita"
  | "violet"
  | "forest"
  | "amber"
  | "rose"
  | "graphite";
export interface Theme {
  mode: Mode;
  scheme: Scheme;
}
export const DEFAULT_THEME: Theme = { mode: "system", scheme: "motita" };
export const MODES: Mode[] = ["system", "light", "dark"];

// The schemes the page offers, in the order of the picker. Their palettes (one per resolved mode)
// are in themes.css, selected by data-scheme; the picker's cards carry the same attribute, so
// each card previews its own colours.
export const SCHEMES: Scheme[] = [
  "motita",
  "violet",
  "forest",
  "amber",
  "rose",
  "graphite",
];

function pick<T extends string>(allowed: readonly T[], v: unknown, d: T): T {
  if ((allowed as readonly unknown[]).includes(v)) {
    return v as T;
  }
  return d;
}

export function loadTheme(): Theme {
  try {
    const raw = JSON.parse(localStorage.getItem(THEME_KEY) || "{}");
    return {
      mode: pick(MODES, raw.mode, DEFAULT_THEME.mode),
      scheme: pick(SCHEMES, raw.scheme, DEFAULT_THEME.scheme),
    };
  } catch {
    return { ...DEFAULT_THEME };
  }
}

const LIGHT_QUERY = "(prefers-color-scheme: light)";

// systemLight is whether the system asks for a light page.
export function systemLight(): boolean {
  return globalThis.matchMedia?.(LIGHT_QUERY).matches === true;
}

// resolveMode is the mode that is drawn: "system" follows what the system asks for.
export function resolveMode(mode: Mode, light = systemLight()): ResolvedMode {
  if (mode === "system") {
    if (light) {
      return "light";
    }
    return "dark";
  }
  return mode;
}

// The colour of the browser's own bar, by mode.
const BAR: Record<ResolvedMode, string> = {
  dark: "#0a0a0f",
  light: "#f4f6fa",
};

export function applyTheme(theme: Theme): void {
  const root = document.documentElement;
  const mode = resolveMode(theme.mode);
  root.dataset.mode = mode;
  root.dataset.scheme = theme.scheme;
  root.style.colorScheme = mode;
  document
    .querySelector('meta[name="color-scheme"]')
    ?.setAttribute("content", mode);
  document
    .querySelector('meta[name="theme-color"]')
    ?.setAttribute("content", BAR[mode]);
}

export function saveTheme(theme: Theme): void {
  try {
    localStorage.setItem(THEME_KEY, JSON.stringify(theme));
  } catch {
    /* ignore */
  }
  applyTheme(theme);
}

// initTheme applies the saved theme and, while the mode follows the system, re-applies it when the
// system flips. The returned function stops listening.
export function initTheme(): () => void {
  applyTheme(loadTheme());
  const query = globalThis.matchMedia?.(LIGHT_QUERY);
  const onChange = () => applyTheme(loadTheme());
  query?.addEventListener("change", onChange);
  return () => query?.removeEventListener("change", onChange);
}
