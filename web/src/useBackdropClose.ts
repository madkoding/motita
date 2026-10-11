import { useEffect, useRef } from 'preact/hooks'
import type { RefObject } from 'preact'

// useBackdropClose gives a modal's backdrop its click behaviour: a click that lands on the backdrop
// itself closes the modal, and a click that started inside the dialog stops there, so it never
// reaches the page's outside-click listeners. It is the same rule as backdropClick in a11y.ts,
// attached to the element directly: the backdrop is a plain container, not a control, and the
// keyboard closes the dialog through useDialog (Escape).
export function useBackdropClose(ref: RefObject<HTMLElement>, onClose: () => void) {
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  useEffect(() => {
    const el = ref.current
    if (!el) { return undefined }
    const onClick = (e: MouseEvent) => {
      if (e.target === e.currentTarget) { closeRef.current() }
      else { e.stopPropagation() }
    }
    el.addEventListener('click', onClick)
    return () => el.removeEventListener('click', onClick)
  }, [ref])
}
