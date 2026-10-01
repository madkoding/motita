// Follow the end of a scrolling box that keeps growing, until the reader takes the scroll.
//
// It is what both retro drawers (the terminal and the thinking one) do, so it lives once. The
// rules were each learned by measurement, and they are kept exactly as they were in App.tsx:
//   - a scroll event does not say who caused it, so the box is taken off the end only while the
//     READER is acting on it (wheel, touch, or a drag on the bar), never because it grew;
//   - whatever changes the box's content - a typed character, a new entry - is what asks to
//     follow, so no writer has to remember to;
//   - opening the drawer lands on the end at once; the glide is for what arrives afterwards.
import { useCallback, useEffect, useRef } from 'preact/hooks'
import { createFollower } from './smoothscroll'

export function useFollowEnd(open: boolean, tau = 110) {
  const ref = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)
  const userAt = useRef(0)
  const dragging = useRef(false)
  const follower = useRef(createFollower(() => ref.current, () => pinned.current, tau))

  const stick = useCallback(() => { follower.current.kick() }, [])
  const markUser = useCallback(() => { userAt.current = performance.now() }, [])
  const onScroll = useCallback(() => {
    const el = ref.current
    if (!el) return
    const gap = el.scrollHeight - el.scrollTop - el.clientHeight
    if (dragging.current || performance.now() - userAt.current < 800) {
      pinned.current = gap < 24
      // The reader has taken the scroll: whatever glide is under way stops with it.
      if (!pinned.current) follower.current.stop()
    } else if (gap < 2) pinned.current = true
  }, [])
  const onPointerDown = useCallback(() => { dragging.current = true; markUser() }, [markUser])

  useEffect(() => {
    const up = () => { dragging.current = false }
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', up)
    return () => { window.removeEventListener('pointerup', up); window.removeEventListener('pointercancel', up) }
  }, [])

  useEffect(() => {
    const el = ref.current
    if (!el || typeof MutationObserver === 'undefined') return
    const mo = new MutationObserver(() => stick())
    mo.observe(el, { childList: true, subtree: true, characterData: true })
    return () => mo.disconnect()
  }, [stick])

  useEffect(() => {
    if (!open) return
    pinned.current = true
    follower.current.jump()
  }, [open])

  return {
    ref,
    stick,
    handlers: { onScroll, onWheel: markUser, onTouchStart: markUser, onTouchMove: markUser, onPointerDown },
  }
}
