// The language the interface speaks: English, or Spanish.
//
// A translation is a lookup keyed by the ENGLISH text, the same way the Go side's internal/i18n
// works: t('New session') returns the Spanish dictionary's entry, and any text the dictionary does
// not have comes back in English. A missing translation is therefore never a broken screen, only
// an English line among Spanish ones.
//
// The Spanish dictionary is loaded with a dynamic import() only when Spanish is chosen, so every
// visitor reading English downloads none of it: it stays out of the shell the page budget guards.
//
// Interpolation: tf('{n} files changed', { n: 3 }). The dictionary is keyed by the FORMAT, with its
// {placeholders}, never by one rendered instance of it.

import { useEffect, useState } from 'preact/hooks'

export type Lang = 'en' | 'es'

// The ui.language setting as the gateway stores it: auto follows the browser.
export type LangSetting = 'auto' | Lang

let current: Lang = 'en'
let spanish: Record<string, string> | null = null
const listeners = new Set<() => void>()

// browserLang is what `auto` means in a browser: Spanish when the reader's first language that is
// set names it (es, es-CL, ...), English otherwise.
export function browserLang(): Lang {
  try {
    const list = (navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language]) || []
    for (const l of list) {
      if (!l) continue
      return /^es(\b|[-_])/i.test(l) ? 'es' : 'en'
    }
  } catch { /* no navigator: English */ }
  return 'en'
}

// resolveLang turns the setting into a language.
export function resolveLang(setting: string | null | undefined): Lang {
  if (setting === 'en' || setting === 'es') return setting
  return browserLang()
}

export function lang(): Lang {
  return current
}

// setLang switches the interface, loading the Spanish dictionary the first time it is needed, and
// re-renders every component that called useLang().
export async function setLang(next: Lang): Promise<void> {
  if (next === 'es' && !spanish) {
    try {
      spanish = (await import('./i18n.es')).default
    } catch {
      // The chunk could not be fetched (an old cached shell, a dropped connection): English is
      // the honest fallback, not a half-translated page.
      next = 'en'
    }
  }
  current = next
  try { document.documentElement.lang = next } catch { /* ignore */ }
  listeners.forEach(fn => fn())
}

// t returns text in the current language.
export function t(text: string): string {
  if (current === 'es' && spanish) {
    const v = spanish[text]
    if (v) return v
  }
  return text
}

// tc is t for a word whose translation depends on where it is used - "off" is a reasoning level
// ("apagado") and also a skill that is turned off ("desactivada"). The dictionary is asked for
// "context|text" first, then for the text alone, the way gettext's msgctxt works.
export function tc(context: string, text: string): string {
  if (current === 'es' && spanish) {
    const v = spanish[context + '|' + text]
    if (v) return v
  }
  return t(text)
}

// tf is t for a format with {name} placeholders: the format is translated, then filled in.
export function tf(format: string, args: Record<string, string | number>): string {
  return t(format).replace(/\{(\w+)\}/g, (m, k) => (k in args ? String(args[k]) : m))
}

// plural picks the singular or the plural format by n, then fills {n} (and any other args) in.
export function plural(n: number, one: string, many: string, args: Record<string, string | number> = {}): string {
  return tf(n === 1 ? one : many, { n, ...args })
}

// useLang subscribes a component to language changes and returns the current language.
export function useLang(): Lang {
  const [, bump] = useState(0)
  useEffect(() => {
    const fn = () => bump(v => v + 1)
    listeners.add(fn)
    return () => { listeners.delete(fn) }
  }, [])
  return current
}
