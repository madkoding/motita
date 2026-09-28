// When an answer's expensive parts get built, and when the conversation counts as READY.
//
// An answer is rendered in two stages. The first is cheap and happens for every message: the
// Markdown parse produces inert placeholders. The second is the expensive half — formulas,
// diagrams, code colours, emoji — and it used to run for EVERY message on the mount, all at
// once, regardless of whether the reader would ever scroll to it.
//
// That does not scale to a conversation. Measured here, one message with four diagrams and a
// handful of formulas grows the page from 1749px to 2377px; a hundred turns of that renders
// every diagram the reader has ever received, in one burst, on every session switch. The
// expensive nodes are not free to keep either — a mermaid SVG is a tree of hundreds of elements
// and KaTeX's MathML is another — so the page ends up holding all of them at once, which is what
// makes a long conversation grind.
//
// So the second stage waits until its message is about to be READ. Everything stays in the DOM
// (this is NOT virtualization: nodes are never unmounted, and the reader can select, search and
// scroll through the whole conversation as one document), but only what comes near the viewport
// is paid for. One shared observer serves every message, because a hundred observers watching a
// hundred elements is the same mistake in a different place; and each message leaves the
// observer once it has been built, so scrolling up and down again costs nothing the second time.
//
// `rootMargin` is what makes it feel instant: a message starts building 600px before it arrives,
// which is roughly the distance a flick of the wheel covers, so the work is almost always done
// by the time it is on screen.

/** A message registered for deferred work, and what state it is in. */
type Entry = {
  /** Whether the message is currently near the viewport. */
  inView: boolean
  /**
   * Whether the work has been STARTED. Separate from `finished` on purpose: the two are a task
   * apart, and conflating them is a bug with teeth — `visiblePending()` would stop counting a
   * message the moment its work began, so the conversation would call itself ready while the
   * formulas and diagrams in front of the reader were still being built.
   */
  started: boolean
  /** Whether the work has actually FINISHED (on either outcome). */
  finished: boolean
  /** The work itself, run on first sight and never again. May be async. */
  task: () => void | Promise<void>
}

const entries = new Map<Element, Entry>()

/** One observer for the whole page, created on first use and kept for the session. */
let observer: IntersectionObserver | null = null
let unsupported = false

/**
 * Every transition of the "is the conversation ready" question, with the reason.
 *
 * Kept because the failure this replaced was a FLICKER — the modal went away and came back, and
 * that is invisible in a single end-state reading. A log with timestamps is the only way to see
 * that a clear was followed by a re-open.
 */
type Transition = { at: number; idle: boolean; pending: number; registered: number; reason: string }
const transitions: Transition[] = []

function record(idle: boolean, reason: string): void {
  let pending = 0
  for (const e of entries.values()) if (!e.finished) pending++
  const last = transitions[transitions.length - 1]
  // Only the CHANGES are kept: the observer fires often and a log of identical rows would hide
  // the flicker inside its own noise.
  if (last && last.idle === idle && last.pending === pending) return
  transitions.push({ at: Math.round(performance.now()), idle, pending, registered: entries.size, reason })
  if (transitions.length > 60) transitions.shift()
}

/** How far ahead of the viewport a message starts building. */
const MARGIN = '600px 0px'

/**
 * Whether anything the reader can currently see is still being built.
 *
 * This is what the loading spinner waits on, and the pairing with `inView` is the whole point:
 * a message far above the fold may carry `data-hydrating` for the entire session — it is
 * deliberately unbuilt — and counting it would hold the spinner open forever.
 */
function visiblePending(): boolean {
  for (const e of entries.values()) {
    if (e.inView && !e.finished) return true
  }
  return false
}

/**
 * The QUIET WINDOW: how long the page must be silent before the parse is considered over.
 *
 * This is the shape the whole thing was missing. Every completion is a separate event, and a
 * conversation arrives as many of them — so asking "are we done?" after each one gives an answer
 * that is true and useless: it flips, and whatever is watching flips with it. Waiting for a
 * window with NO activity answers the question the reader actually has, because a parse that is
 * still running cannot be silent for two seconds.
 *
 * 1s is the operator's number, and the shape is what matters: it has to be longer than the gap
 * between one message finish and the next message registering, and shorter than the point where a
 * reader calls the interface stuck. A parse that is still running cannot be silent for a second.
 */
const QUIET_MS = 1000
let quietTimer: ReturnType<typeof setTimeout> | null = null

/**
 * Report that something happened. Callers: a message registering, a message finishing, and the
 * app when it starts loading a transcript. The timer is RESTARTED on each one, which is what makes
 * the window quiet rather than fixed.
 */
export function noteActivity(): void {
  if (quietTimer !== null) clearTimeout(quietTimer)
  quietTimer = setTimeout(() => {
    quietTimer = null
    document.dispatchEvent(new CustomEvent('motita:chat-quiet'))
  }, QUIET_MS)
}

/** Stop the window without announcing. For tests and teardown. */
export function cancelQuiet(): void {
  if (quietTimer !== null) clearTimeout(quietTimer)
  quietTimer = null
}

/** The transition log, for the gate and for a bug report. */
export function hydrationLog(): Transition[] {
  return transitions.slice()
}

function ensureObserver(): IntersectionObserver | null {
  if (unsupported) return null
  if (observer) return observer
  if (typeof IntersectionObserver === 'undefined') {
    // No observer: fall back to building everything immediately. The reader gets the old
    // behaviour rather than a conversation whose diagrams never appear — a slow page is a
    // worse failure than an expensive one.
    unsupported = true
    return null
  }
  observer = new IntersectionObserver(
    (records) => {
      let started = false
      for (const rec of records) {
        const entry = entries.get(rec.target)
        if (!entry) continue
        // The visibility flag is kept CURRENT for every registered message, including ones
        // already built. Unobserving on first sight is cheaper, but it freezes this attribute at
        // its value when the work started — so it stops describing where the message is and
        // silently reports "in view" for the rest of the session. The cost that matters is the
        // nodes a hydrated message holds (a mermaid SVG is hundreds of elements), not one
        // IntersectionObserver registration; this is the cheap half.
        entry.inView = rec.isIntersecting
        ;(rec.target as HTMLElement).setAttribute('data-inview', rec.isIntersecting ? '1' : '0')
        if (rec.isIntersecting && !entry.started) {
          entry.started = true
          entry.task()
          started = true
        }
      }
      // A message that scrolled AWAY while it was pending is no longer something the reader is
      // waiting for — but the transition is still activity, and the quiet window is what decides
      // when the page has stopped, so it is restarted here too.
      if (started || records.some((r) => !r.isIntersecting)) noteActivity()
    },
    { root: null, rootMargin: MARGIN, threshold: 0 },
  )
  return observer
}

/**
 * Register a message whose deferred work should run once it is about to be read.
 *
 * Returns the cleanup for the effect that registered it. The element is marked `data-hydrating`
 * immediately rather than when its turn comes: as far as the reader is concerned the message is
 * not finished from the moment it appears until its formulas and diagrams are there, and the
 * spinner is waiting on exactly that.
 */
export function watchForHydration(el: HTMLElement, task: () => void): () => void {
  const obs = ensureObserver()
  // No optimistic default. The element starts OUT of view and the observer promotes it: assuming
  // visible until the first callback would make a message that is never reported look built, and
  // — worse — would make the spinner's "is anything visible still pending" question answer itself
  // with a yes that nothing ever corrects.
  el.setAttribute('data-hydrating', '1')
  el.setAttribute('data-inview', '0')
  record(!visiblePending(), 'register')
  // A message appearing is activity: it restarts the quiet window, so the window cannot expire
  // between two messages that are still arriving.
  noteActivity()
  entries.set(el, {
    inView: false,
    started: false,
    finished: false,
    task: () => void Promise.resolve(task()).then(() => finish(el), () => finish(el)),
  })
  if (obs) {
    obs.observe(el)
  } else {
    // No IntersectionObserver: nothing will ever promote it, so build now. A slow page is a
    // worse failure than an expensive one.
    const fallback = entries.get(el)!
    fallback.inView = true
    fallback.started = true
    el.setAttribute('data-inview', '1')
    fallback.task()
  }

  return () => {
    entries.delete(el)
    obs?.unobserve(el)
    // Deliberately NO announce() here, and this is the flicker that was reported: a cleanup runs
    // when the effect re-runs (the message's content changed) and when the message is unmounted
    // (a session switch clears the transcript before fetching the next one). Announcing from here
    // reports the conversation READY at the exact moment its work is about to restart — so the
    // modal closed, and reopened when the messages mounted again. The gap is not short: a session
    // switch clears the transcript, fetches, and mounts, which is hundreds of milliseconds — long
    // enough that no settling delay can paper over it. The state belongs to the caller that
    // started the load, which is `switchSession`.
  }
}

/** Mark a message's deferred work as over, on either outcome. */
function finish(el: HTMLElement): void {
  const entry = entries.get(el)
  if (entry) entry.finished = true
  el.removeAttribute('data-hydrating')
  record(!visiblePending(), 'finish')
  // A message finishing is activity too, and this is the one that matters: it is what keeps the
  // window open while the conversation is still being built, and what lets it close when the
  // finishing stops.
  noteActivity()
}

/** How many messages are registered, in view, and still unbuilt. For the gate and the log. */
export function hydrationState(): { registered: number; pending: number; visiblePending: boolean } {
  let pending = 0
  for (const e of entries.values()) if (!e.finished) pending++
  return { registered: entries.size, pending, visiblePending: visiblePending() }
}
