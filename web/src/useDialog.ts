import { useEffect, useRef } from 'preact/hooks'
import type { RefObject } from 'preact'

const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),summary,[tabindex]:not([tabindex="-1"])'

// useDialog gives a modal its keyboard behaviour: focus moves inside when it opens (to the element
// marked data-autofocus, else the first focusable one), Tab stays inside, Escape closes, and focus
// returns to whatever had it before. The listener runs in the capture phase and stops the event, so
// Escape closes only the topmost dialog when one is stacked on another.
export function useDialog(ref: RefObject<HTMLElement>, onClose: () => void) {
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null
    const root = ref.current
    if (root) {
      const first = root.querySelector<HTMLElement>('[data-autofocus]') || root.querySelector<HTMLElement>(FOCUSABLE)
      first?.focus()
    }
    const onKey = (e: KeyboardEvent) => {
      const el = ref.current
      if (!el) return
      // Only the topmost dialog reacts: later siblings in the document are on top.
      const all = document.querySelectorAll('[data-dialog-root]')
      if (all.length && all[all.length - 1] !== el) return
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        closeRef.current()
        return
      }
      if (e.key !== 'Tab') return
      const items = Array.from(el.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(n => n.offsetParent !== null || n === document.activeElement)
      if (!items.length) return
      const a = items[0], z = items[items.length - 1]
      if (e.shiftKey && (document.activeElement === a || !el.contains(document.activeElement))) { e.preventDefault(); z.focus() }
      else if (!e.shiftKey && (document.activeElement === z || !el.contains(document.activeElement))) { e.preventDefault(); a.focus() }
    }
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      try { prev?.focus() } catch { /* gone */ }
    }
  }, [])
}
