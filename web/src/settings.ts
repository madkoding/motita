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
