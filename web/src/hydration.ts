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

/** One observer for the whole page, created when the first message registers. */
let observer: IntersectionObserver | null = null
let unsupported = false

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

/** Announce that the visible conversation is ready, whenever that becomes true. */
function announce(): void {
  if (visiblePending()) return
  document.dispatchEvent(new CustomEvent('motita:chat-idle'))
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
      // A message that scrolled AWAY while it was pending is no longer something the reader
      // is waiting for, so the conversation can count as ready.
      if (started || records.some((r) => !r.isIntersecting)) announce()
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
    announce()
  }
}

/** Mark a message's deferred work as over, on either outcome. */
function finish(el: HTMLElement): void {
  const entry = entries.get(el)
  if (entry) entry.finished = true
  el.removeAttribute('data-hydrating')
  announce()
}

/** How many messages are registered, in view, and still unbuilt. For the gate and the log. */
export function hydrationState(): { registered: number; pending: number; visiblePending: boolean } {
  let pending = 0
  for (const e of entries.values()) if (!e.finished) pending++
  return { registered: entries.size, pending, visiblePending: visiblePending() }
}
