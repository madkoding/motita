// The sidebar's menus: the "⋯" menu of a session or project row, and the menu a long press opens
// on a phone.
import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { choose } from './choose'
import type { ContextMenuAt, RowTarget } from './appTypes'
import { MENU_GAP, MENU_H, MENU_W, ROW_MENU_EXIT_MS } from './appConst'

// menuPlace computes where the menu goes for a given trigger, in viewport coordinates.
//
// Below the button by default, and ABOVE it when the menu would not fit below — which is the
// case that matters most, because the rows at the bottom of the list are exactly where the
// clipping was worst. The horizontal position is flush with the button's right edge (the menu
// is right-aligned) and kept inside the viewport, so a narrow sidebar cannot push it off.
function menuPlace(trigger: HTMLElement): { top: number; left: number; above: boolean } {
  const r = trigger.getBoundingClientRect()
  const below = r.bottom + MENU_GAP
  const fitsBelow = below + MENU_H <= window.innerHeight
  const top = choose(fitsBelow, below, Math.max(MENU_GAP, r.top - MENU_GAP - MENU_H))
  const left = Math.min(Math.max(MENU_GAP, r.right - MENU_W), Math.max(MENU_GAP, window.innerWidth - MENU_W - MENU_GAP))
  return { top, left, above: !fitsBelow }
}

// outsideMenu says whether a press landed outside the open menu and its own toggle.
function outsideMenu(target: HTMLElement | null): boolean {
  if (!target) { return true }
  return !target.closest('.row-menu') && !target.closest('button[data-row-menu-trigger]')
}

export function useRowMenus() {
  // Long-press context menu on mobile: when set, shows a small menu with Edit / Delete.
  const [contextMenu, setContextMenu] = useState<ContextMenuAt | null>(null)
  // Row dropdown menu: which session/project row has its "⋯" menu open.
  const [rowMenu, setRowMenu] = useState<RowTarget | null>(null)
  // rowMenuPos is where the open menu is drawn, in VIEWPORT coordinates.
  //
  // The menu used to be `absolute` inside the row, and it was clipped: the session list is
  // `overflow-y: auto`, and an element with a non-visible `overflow-y` cannot keep
  // `overflow-x: visible` — the used value is computed as `auto` too, so the list clips BOTH
  // axes. Measured on the served CSS: the list carries `overflow-y:auto` and the menu sits below
  // the row it belongs to, so the last rows (and any row near the bottom of the scroll area) had
  // the menu cut off.
  //
  // `position: fixed` escapes that clip, because a fixed element is laid out against the viewport
  // and no ancestor's overflow applies — which is exactly how the long-press menu in this file
  // already works, and why it never had the defect. The position therefore has to be COMPUTED
  // from the trigger's rect, and flipped above the row when there is no room below.
  const [rowMenuPos, setRowMenuPos] = useState<{ top: number; left: number } | null>(null)
  // rowMenuAbove says the menu was flipped above its row, which changes the animation's origin:
  // a menu growing downward from a top-right pivot looks wrong when it sits above the trigger.
  const [rowMenuAbove, setRowMenuAbove] = useState(false)
  const rowMenuAnchor = useRef<HTMLElement | null>(null)
  // Closing state: the menu keeps rendering for the length of its exit
  // animation. Without it the element would unmount on the same frame the
  // user clicked outside and the exit animation would never be seen.
  const [rowMenuClosing, setRowMenuClosing] = useState(false)
  const rowMenuTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const rowMenuClosingRef = useRef(false)

  // closeRowMenu plays the exit animation, then unmounts. Idempotent: a
  // second call while the timer runs would otherwise stack timers, and the
  // last one to fire would reset the state under a menu already reopened.
  const closeRowMenu = useCallback(() => {
    if (rowMenuClosingRef.current) { return }
    rowMenuClosingRef.current = true
    setRowMenuClosing(true)
    if (rowMenuTimer.current) { clearTimeout(rowMenuTimer.current) }
    rowMenuTimer.current = setTimeout(() => {
      setRowMenu(null)
      setRowMenuClosing(false)
      rowMenuClosingRef.current = false
      rowMenuTimer.current = null
    }, ROW_MENU_EXIT_MS)
  }, [])

  // openRowMenu opens the menu, cancelling a close that is mid-flight so
  // the new menu does not inherit the old one's exit state.
  const openRowMenu = useCallback((m: RowTarget | null) => {
    if (rowMenuTimer.current) { clearTimeout(rowMenuTimer.current); rowMenuTimer.current = null }
    rowMenuClosingRef.current = false
    setRowMenuClosing(false)
    setRowMenu(m)
  }, [])

  // placeRowMenu puts the menu next to the button that opens it.
  const placeRowMenu = useCallback((trigger: HTMLElement) => {
    const { top, left, above } = menuPlace(trigger)
    setRowMenuAbove(above)
    setRowMenuPos({ top, left })
  }, [])

  // toggleRowMenu is the "⋯" button of a row: it opens that row's menu, or closes it when it is
  // the one already open.
  const toggleRowMenu = (btn: HTMLElement, target: RowTarget) => {
    if (rowMenu?.id === target.id) { closeRowMenu(); return }
    rowMenuAnchor.current = btn
    placeRowMenu(btn)
    openRowMenu(target)
  }

  // Click-outside / Escape closes the row menu, WITH its exit animation.
  // Listening on `document` in the CAPTURE phase (not bubble) so the menu
  // closes even when the click lands on an element that stops propagation —
  // a session row does exactly that, which is why a bubble-phase listener
  // would leave the menu open when the user clicked a different row.
  //
  // The `⋯` toggle itself is excluded: `mousedown` fires BEFORE `click`, so
  // treating it as an outside click would flip the state twice (closed here,
  // then reopened by the button's own handler) and the button would seem dead.
  useEffect(() => {
    if (!rowMenu) { return undefined }
    const onDown = (e: Event) => {
      if (outsideMenu(e.target as HTMLElement | null)) { closeRowMenu() }
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { closeRowMenu() } }
    document.addEventListener('mousedown', onDown, true)
    document.addEventListener('touchstart', onDown, true)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown, true)
      document.removeEventListener('touchstart', onDown, true)
      document.removeEventListener('keydown', onKey)
    }
  }, [rowMenu, closeRowMenu])

  // A FIXED menu does not move with the page, so it would stay put while the row it belongs to
  // scrolls away — a menu pointing at nothing. Re-place it from its anchor on every scroll, in
  // the CAPTURE phase so it also sees the sidebar's own scroll container (a scroll does not
  // bubble, so a listener on `window` alone would miss it).
  //
  // The listener is attached only while a menu is open, which is what keeps it off the hot path
  // of ordinary scrolling.
  useEffect(() => {
    if (!rowMenu) { return undefined }
    const reposition = () => {
      const anchor = rowMenuAnchor.current
      if (anchor?.isConnected) { placeRowMenu(anchor) }
      else { closeRowMenu() } // the row was removed from under the menu
    }
    window.addEventListener('scroll', reposition, true)
    window.addEventListener('resize', reposition)
    return () => {
      window.removeEventListener('scroll', reposition, true)
      window.removeEventListener('resize', reposition)
    }
  }, [rowMenu, placeRowMenu, closeRowMenu])

  const closeContextMenu = useCallback(() => setContextMenu(null), [])

  return {
    contextMenu, setContextMenu, closeContextMenu, rowMenu, rowMenuPos, rowMenuAbove, rowMenuClosing,
    closeRowMenu, toggleRowMenu,
  }
}

export type RowMenus = ReturnType<typeof useRowMenus>
