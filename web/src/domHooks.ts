// Listeners attached to an element of the page directly, for the elements that are not controls
// themselves but have to react to what happens inside them: a menu that keeps its clicks to
// itself, a panel that answers the arrow keys, a row that opens a menu on a long press.
import { useEffect, useRef } from 'preact/hooks'
import type { RefObject } from 'preact'
import { LONG_PRESS_MS } from './appConst'

// useListener attaches `handler` to the element for as long as the component is mounted. The
// handler is read when the event fires, so it always sees the values of the latest render.
export function useListener<K extends keyof HTMLElementEventMap>(ref: RefObject<HTMLElement>, type: K, handler: (e: HTMLElementEventMap[K]) => void) {
  const handlerRef = useRef(handler)
  handlerRef.current = handler
  useEffect(() => {
    const el = ref.current
    if (!el) { return undefined }
    const listener = (e: HTMLElementEventMap[K]) => handlerRef.current(e)
    el.addEventListener(type, listener)
    return () => el.removeEventListener(type, listener)
  }, [ref, type])
}

// stopClick keeps a click inside the element it happened in.
function stopClick(e: Event) {
  e.stopPropagation()
}

// useStopClicks keeps the clicks inside the element from reaching the elements around it and the
// page's own click listeners.
export function useStopClicks(ref: RefObject<HTMLElement>) {
  useListener(ref, 'click', stopClick)
}

// useLongPress opens something at the touch position when a finger rests on the element: the
// timer starts on touchstart and a move or a lift cancels it.
export function useLongPress(ref: RefObject<HTMLElement>, onLongPress: (x: number, y: number) => void) {
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const cancel = () => {
    if (timer.current) {
      clearTimeout(timer.current)
      timer.current = null
    }
  }
  useListener(ref, 'touchstart', (e: TouchEvent) => {
    const touch = e.touches[0]
    const x = touch.clientX
    const y = touch.clientY
    timer.current = setTimeout(() => {
      onLongPress(x, y)
      // Prevent the subsequent click from firing.
      if (timer.current) { timer.current = null }
    }, LONG_PRESS_MS)
  })
  useListener(ref, 'touchmove', cancel)
  useListener(ref, 'touchend', cancel)
}
