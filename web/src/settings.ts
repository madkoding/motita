// Visual settings of the chat, saved in localStorage and applied to <html> as data attributes.
export const SETTINGS_KEY = "motita.ui.settings";
export type FontSize = "small" | "normal" | "large";
export type Density = "comfortable" | "compact";
export interface UISettings {
  fontSize: FontSize;
  density: Density;
}
export const DEFAULT_SETTINGS: UISettings = {
  fontSize: "normal",
  density: "comfortable",
};

// oneOf is the saved value when it is one of the allowed ones, the default otherwise.
function oneOf<T extends string>(allowed: T[], value: unknown, fallback: T): T {
  if ((allowed as unknown[]).includes(value)) {
    return value as T;
  }
  return fallback;
}

// onOff spells a switch the way it is stored and shown.
function onOff(on: boolean): "on" | "off" {
  if (on) {
    return "on";
  }
  return "off";
}

export function loadSettings(): UISettings {
  try {
    const raw = JSON.parse(localStorage.getItem(SETTINGS_KEY) || "{}");
    const fontSize = oneOf<FontSize>(
      ["small", "normal", "large"],
      raw.fontSize,
      DEFAULT_SETTINGS.fontSize,
    );
    const density = oneOf<Density>(
      ["comfortable", "compact"],
      raw.density,
      DEFAULT_SETTINGS.density,
    );
    return { fontSize, density };
  } catch {
    return { ...DEFAULT_SETTINGS };
  }
}

export function applySettings(s: UISettings): void {
  const root = document.documentElement;
  root.dataset.fontSize = s.fontSize;
  root.dataset.density = s.density;
}

export function saveSettings(s: UISettings): void {
  try {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(s));
  } catch {
    /* ignore */
  }
  applySettings(s);
}

export function initSettings(): UISettings {
  const s = loadSettings();
  applySettings(s);
  return s;
}

// Whether the browser may be asked to show a notification when a pull request's CI ends while the tab
// is in the background. It is the user's own switch, kept apart from the look of the chat, and on by
// default: the browser's permission is what actually decides.
export const NOTIFY_KEY = "motita.ui.notifications";

export function loadNotify(): boolean {
  try {
    return localStorage.getItem(NOTIFY_KEY) !== "off";
  } catch {
    return true;
  }
}

export function saveNotify(on: boolean): void {
  try {
    localStorage.setItem(NOTIFY_KEY, onOff(on));
  } catch {
    /* ignore */
  }
}

// Whether the page shows a toast when a pull request's CI ends. On by default: unlike a desktop
// notification it needs no permission and no secure address, so it works everywhere.
export const CI_TOAST_KEY = "motita.ui.ciToast";

export function loadCiToast(): boolean {
  try {
    return localStorage.getItem(CI_TOAST_KEY) !== "off";
  } catch {
    return true;
  }
}

export function saveCiToast(on: boolean): void {
  try {
    localStorage.setItem(CI_TOAST_KEY, onOff(on));
  } catch {
    /* ignore */
  }
}

// Whether the empty composer hints at the next request after a turn. It costs one small model call
// per finished turn, so it is the user's switch, on by default.
export const NEXT_HINT_KEY = "motita.ui.nextHint";

export function loadNextHint(): boolean {
  try {
    return localStorage.getItem(NEXT_HINT_KEY) !== "off";
  } catch {
    return true;
  }
}

export function saveNextHint(on: boolean): void {
  try {
    localStorage.setItem(NEXT_HINT_KEY, onOff(on));
  } catch {
    /* ignore */
  }
}

export type NotifyState =
  | "unsupported"
  | "insecure"
  | "blocked"
  | "ask"
  | "on"
  | "off";

// notifyState is what the settings screen says about notifications: the browser's permission and
// the user's switch together. A page served over plain http (not localhost) is never allowed to
// notify, and Chrome reports that as "denied": it is told apart, because no browser setting fixes it.
export function notifyState(
  permission: string | undefined,
  enabled: boolean,
  secure = true,
): NotifyState {
  if (!secure && permission !== "granted") {
    return "insecure";
  }
  if (permission === undefined) {
    return "unsupported";
  }
  if (permission === "denied") {
    return "blocked";
  }
  if (permission === "default") {
    return "ask";
  }
  return onOff(enabled);
}

// Where the page runs, for the steps that turn blocked notifications back on: each browser keeps
// the permission in a different place, and an installed app keeps it in the system's settings.
export type NotifyPlatform =
  | "ios"
  | "android"
  | "edge"
  | "chrome"
  | "firefox"
  | "safari"
  | "other";

// notifyPlatform reads the platform from the user agent. An iPad asking for the desktop site says
// it is a Mac, so a Mac with a touch screen is taken for one.
export function notifyPlatform(ua: string, touchPoints = 0): NotifyPlatform {
  if (
    /iPhone|iPad|iPod/.test(ua) ||
    (/Macintosh/.test(ua) && touchPoints > 1)
  ) {
    return "ios";
  }
  if (/Android/.test(ua)) {
    return "android";
  }
  if (/Edg\//.test(ua)) {
    return "edge";
  }
  if (/Firefox\//.test(ua)) {
    return "firefox";
  }
  if (/Chrome\/|Chromium\//.test(ua)) {
    return "chrome";
  }
  if (/Safari\//.test(ua)) {
    return "safari";
  }
  return "other";
}

// How the providers' usage is drawn in the sidebar: a bar each, or a circular gauge each. The
// panel listens for USAGE_STYLE_EVENT, so a change made in Settings shows at once.
export type UsageStyle = "bars" | "gauge";
export const USAGE_STYLE_KEY = "motita.ui.usageStyle";
export const USAGE_STYLE_EVENT = "motita-usage-style";

export function loadUsageStyle(): UsageStyle {
  try {
    return oneOf<UsageStyle>(
      ["bars", "gauge"],
      localStorage.getItem(USAGE_STYLE_KEY),
      "bars",
    );
  } catch {
    return "bars";
  }
}

export function saveUsageStyle(style: UsageStyle): void {
  try {
    localStorage.setItem(USAGE_STYLE_KEY, style);
  } catch {
    /* ignore */
  }
  window.dispatchEvent(new Event(USAGE_STYLE_EVENT));
}

// Whether the provider usage in the sidebar is open. It starts closed, showing only each provider and
// its percentage, and stays as the user left it.
export const USAGE_OPEN_KEY = "motita.ui.usageOpen";

export function loadUsageOpen(): boolean {
  try {
    return localStorage.getItem(USAGE_OPEN_KEY) === "open";
  } catch {
    return false;
  }
}

export function saveUsageOpen(open: boolean): void {
  try {
    localStorage.setItem(USAGE_OPEN_KEY, open ? "open" : "closed");
  } catch {
    /* ignore */
  }
}
