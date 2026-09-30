// Smooth "follow the end" for a scrolling box that keeps growing.
//
// The native `scroll-behavior: smooth` is the wrong tool here and this repo measured why: it
// animates towards the height the box had when the scroll started, so the next growth lands on
// top of a flight already under way and the view ends up short of the end. This follower
// instead re-reads the target on EVERY frame and moves a fraction of the remaining gap, so a
// box that grows while it is being followed is simply followed further.
//
// The ease is exponential (a fixed share of the gap per unit of TIME, not per frame), which is
// what makes it feel the same on a 60 Hz and a 144 Hz screen: fast when far, and settling
// softly on the last few pixels instead of stopping dead.

export interface Follower {
  // kick starts following if it is not already; cheap to call on every change.
  kick(): void
  // jump goes to the end at once and stops any flight in progress.
  jump(): void
  stop(): void
}

export function createFollower(
  getEl: () => HTMLElement | null,
  // active is asked on every frame: the moment it says no (the reader took the scroll), the
  // follower lets go without a last nudge.
  active: () => boolean,
  // tau is the time constant in ms: the gap shrinks by 63% every tau.
  tau = 130,
): Follower {
  let raf = 0
  let last = 0

  const end = (el: HTMLElement) => Math.max(0, el.scrollHeight - el.clientHeight)

  const step = (now: number) => {
    raf = 0
    const el = getEl()
    if (!el || !active()) return
    const dt = Math.min(64, Math.max(1, now - last))
    last = now
    const gap = end(el) - el.scrollTop
    if (gap <= 0.5) {
      // Arrived. It is not left running: the next growth kicks it again.
      el.scrollTop = end(el)
      return
    }
    // The time constant SHORTENS with the distance: near the end the ease is soft, and when the
    // content outruns it (a burst of lines) the view catches up faster instead of leaving the
    // newest text below the fold for a second. Halved at ~120px behind, a third at ~240px.
    const k = tau / (1 + gap / 120)
    // A floor of 1px per frame so the tail of the ease finishes instead of asymptoting.
    const move = Math.min(gap, Math.max(1, gap * (1 - Math.exp(-dt / k))))
    el.scrollTop += move
    raf = requestAnimationFrame(step)
  }

  return {
    kick() {
      if (raf || !active()) return
      last = performance.now()
      raf = requestAnimationFrame(step)
    },
    jump() {
      if (raf) cancelAnimationFrame(raf)
      raf = 0
      const el = getEl()
      if (el) el.scrollTop = end(el)
    },
    stop() {
      if (raf) cancelAnimationFrame(raf)
      raf = 0
    },
  }
}
