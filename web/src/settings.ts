// Visual settings of the chat, saved in localStorage and applied to <html> as data attributes.
export const SETTINGS_KEY = 'motita.ui.settings'
export type FontSize = 'small' | 'normal' | 'large'
export type Density = 'comfortable' | 'compact'
export interface UISettings { fontSize: FontSize; density: Density }
export const DEFAULT_SETTINGS: UISettings = { fontSize: 'normal', density: 'comfortable' }

export function loadSettings(): UISettings {
  try {
    const raw = JSON.parse(localStorage.getItem(SETTINGS_KEY) || '{}')
    const fontSize: FontSize = ['small', 'normal', 'large'].includes(raw.fontSize) ? raw.fontSize : DEFAULT_SETTINGS.fontSize
    const density: Density = ['comfortable', 'compact'].includes(raw.density) ? raw.density : DEFAULT_SETTINGS.density
    return { fontSize, density }
  } catch {
    return { ...DEFAULT_SETTINGS }
  }
}

export function applySettings(s: UISettings): void {
  const root = document.documentElement
  root.setAttribute('data-font-size', s.fontSize)
  root.setAttribute('data-density', s.density)
}

export function saveSettings(s: UISettings): void {
  try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(s)) } catch { /* ignore */ }
  applySettings(s)
}

export function initSettings(): UISettings {
  const s = loadSettings()
  applySettings(s)
  return s
}

// Whether the browser may be asked to show a notification when a pull request's CI ends while the tab
// is in the background. It is the user's own switch, kept apart from the look of the chat, and on by
// default: the browser's permission is what actually decides.
export const NOTIFY_KEY = 'motita.ui.notifications'

export function loadNotify(): boolean {
  try { return localStorage.getItem(NOTIFY_KEY) !== 'off' } catch { return true }
}

export function saveNotify(on: boolean): void {
  try { localStorage.setItem(NOTIFY_KEY, on ? 'on' : 'off') } catch { /* ignore */ }
}

export type NotifyState = 'unsupported' | 'blocked' | 'ask' | 'on' | 'off'

// notifyState is what the settings screen says about notifications: the browser's permission and
// the user's switch together.
export function notifyState(permission: string | undefined, enabled: boolean): NotifyState {
  if (permission === undefined) return 'unsupported'
  if (permission === 'denied') return 'blocked'
  if (permission === 'default') return 'ask'
  return enabled ? 'on' : 'off'
}
