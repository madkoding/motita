// Keyboard and pointer helpers shared by the clickable non-button elements.

type KeyLike = { key: string; target: unknown; currentTarget: unknown; preventDefault(): void }

// onActivate gives a clickable element the keyboard equivalent of its click: Enter or Space runs the
// same action. It ignores keys that come from a control INSIDE the element (the rename field, a
// button in a row), which keep their own meaning.
export function onActivate(action: () => void) {
  return (e: KeyLike) => {
    if (e.target !== e.currentTarget) return
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      action()
    }
  }
}

type ClickLike = { target: unknown; currentTarget: unknown; stopPropagation(): void }

// backdropClick closes a modal when the click lands on the backdrop itself. A click that started inside
// the dialog bubbles up to here and is stopped, as the dialog's own handler used to do, so it never
// reaches the page's outside-click listeners. Escape and Tab are handled by useDialog.
export function backdropClick(close: () => void) {
  return (e: ClickLike) => {
    if (e.target === e.currentTarget) close()
    else e.stopPropagation()
  }
}
