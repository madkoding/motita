// The conversation's scroll and its loading modal: the view follows the end while the reader is
// there, and the "Loading session" card stays up until the page has stopped changing.
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { createFollower } from "./smoothscroll";
import { newIntent, readerActs, scrolled } from "./scrollIntent";
import { hydrationState, hydrationLog, noteActivity } from "./hydration";
import type { Message, StepView } from "./appTypes";
import { BOTTOM_SLACK, CHAT_QUIET_EVENT, MODAL_EXIT_MS } from "./appConst";

// GateWindow is the window with the hooks the browser gates read: the loading modal's history.
type GateWindow = Window & {
  __chatLoadingLog?: () => unknown;
  __modalLog?: { at: number; loading: boolean }[];
};

// The keys that move the view up: pressing one is the reader leaving the end.
const UP_KEYS = new Set(["ArrowUp", "PageUp", "Home"]);

// gapToEnd is how far the box is from the end of what it holds.
function gapToEnd(el: HTMLElement): number {
  return el.scrollHeight - el.scrollTop - el.clientHeight;
}

// useLoadingModal is the ONLY way the modal is opened or closed. Closing runs the exit FIRST and
// holds the state until the fade has finished, so the modal leaves the way a reader expects instead
// of blinking out of existence. Both directions are idempotent: a conversation that starts more work
// while the modal is on its way out cancels the exit rather than stacking timers under a modal that
// is already returning.
function useLoadingModal() {
  // Whether the conversation on screen is still being PUT there: the transcript is being
  // fetched, or the messages are on screen but their formulas, diagrams and emoji are still
  // being built. Both are the same thing to the reader — the conversation is not ready to
  // read yet — and both are shown by one spinner over the message area.
  const [chatLoading, setChatLoading] = useState(false);
  // The modal's exit runs BEFORE `chatLoading` is allowed to go false, because a node that is
  // removed from the DOM cannot transition: the reader saw it vanish in one frame. This timer is
  // the one that holds the state during `MODAL_EXIT_MS`, so the fade-out is real.
  const [modalLeaving, setModalLeaving] = useState(false);
  const modalTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Whether the modal is mounted right now, tracked OUTSIDE React state so `requestModal` can be
  // idempotent without being re-created: the quiet window fires once per settle, and a close that
  // arrives while the modal is already on its way out must not restart the timer.
  const modalOpen = useRef(false);
  // The exit class is applied ONE FRAME after the scrim mounts. React commits the DOM before the
  // next paint, so hiding the new element in the same commit would give the browser no starting
  // opacity to interpolate from - and the fade-out would be a fade from 0 to 0, i.e. a blink.
  const modalExitFrame = useRef<number | null>(null);

  const cancelExitFrame = () => {
    if (modalExitFrame.current !== null) {
      cancelAnimationFrame(modalExitFrame.current);
    }
  };

  const requestModal = useCallback((open: boolean) => {
    if (modalTimerRef.current) {
      clearTimeout(modalTimerRef.current);
      modalTimerRef.current = null;
    }
    if (open) {
      modalOpen.current = true;
      cancelExitFrame();
      modalExitFrame.current = null;
      setModalLeaving(false);
      setChatLoading(true);
      return;
    }
    if (!modalOpen.current) {
      return;
    }
    modalOpen.current = false;
    cancelExitFrame();
    // Two frames, and both are needed. The FIRST mounts the scrim with no exit class, so the
    // browser has a real starting opacity; the SECOND adds `.chat-modal-leaving`, which is what
    // animates. This also handles the case that bit me: React batches, so setting both in one
    // handler produced a single commit (scrim + exit class together = a blink from 0 to 0).
    const leave = () => {
      modalExitFrame.current = null;
      setModalLeaving(true);
    };
    modalExitFrame.current = requestAnimationFrame(() => {
      modalExitFrame.current = requestAnimationFrame(leave);
    });
    modalTimerRef.current = setTimeout(() => {
      modalTimerRef.current = null;
      setChatLoading(false);
      setModalLeaving(false);
    }, MODAL_EXIT_MS);
  }, []);
  useEffect(
    () => () => {
      if (modalTimerRef.current) {
        clearTimeout(modalTimerRef.current);
      }
      cancelExitFrame();
    },
    [],
  );
  return { chatLoading, modalLeaving, requestModal };
}

// usePinnedFollow keeps track of whether the reader is at the end of the conversation, from their
// own input, and moves the view to the end when they are.
function usePinnedFollow(scrollRef: { current: HTMLDivElement | null }) {
  // Whether the reader is at the end of the conversation, tracked from scroll events rather than
  // measured on demand. Measuring at the moment a message finishes is WRONG and was the reason the
  // view stopped following: by then the container has already grown, so the gap is large BECAUSE
  // of the growth, and the check reads a reader who never moved as one who scrolled away. The
  // question is where the reader was before the growth, and only a scroll event knows that.
  const pinnedRef = useRef(true);
  // chatLoading as a ref, so the follower can ask it without being rebuilt.
  const chatLoadingNow = useRef(false);

  // The smooth follower for what grows WHILE A RUN IS WORKING (a step, a thought, an answer):
  // the view glides to the new end instead of jumping to it. It re-reads the end on every
  // frame, so growth that lands mid-glide is followed rather than left behind - which is the
  // reason native smooth scrolling was ruled out below. Loading a session and the hydration
  // anchoring keep the exact, instant jump: there the height is still changing under a modal.
  const chatFollower = useRef(
    createFollower(
      () => scrollRef.current,
      () => pinnedRef.current,
    ),
  );
  const followEnd = useCallback(() => {
    if (chatLoadingNow.current) {
      chatFollower.current.jump();
    } else {
      chatFollower.current.kick();
    }
  }, []);

  const scrollToBottom = useCallback(() => {
    const el = scrollRef.current;
    if (!el) {
      return;
    }
    // `behavior: 'instant'` and not 'auto'. 'auto' defers to the CSS `scroll-behavior`, and this
    // container carries Tailwind's `scroll-smooth` — so "instant" was in fact an animation,
    // hundreds of milliseconds long, which the next 200px of formulas would land on top of. That
    // is the "it does not go all the way down when it finishes" symptom: the last animation was
    // interrupted by the next growth. 'instant' overrides the CSS and lands exactly where aimed.
    chatFollower.current.stop();
    el.scrollTo({
      top: el.scrollHeight,
      behavior: "instant" as ScrollBehavior,
    });
  }, []);
  return { pinnedRef, chatLoadingNow, chatFollower, followEnd, scrollToBottom };
}

// useReaderIntent lets go of the end when the reader moves up on their own, and takes it again when
// they come back down to it (see scrollIntent.ts for the rule and why).
function useReaderIntent(
  scrollRef: { current: HTMLDivElement | null },
  pinnedRef: { current: boolean },
) {
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) {
      return undefined;
    }
    // Only the READER'S OWN INPUT unpins them. Scroll events also fire when CONTENT changes:
    // measured, the container's `scrollTop` went 4551 -> 4511 on its own while the deferred work
    // replaced a placeholder with a smaller drawing, and a rule that read every move up as the
    // reader going back left the conversation 181px short of the end, every single run.
    const intent = newIntent(el.scrollTop);
    intent.pinned = pinnedRef.current;
    const sync = () => {
      pinnedRef.current = intent.pinned;
    };
    const act = (up: boolean) => {
      intent.pinned = pinnedRef.current;
      readerActs(intent, performance.now(), up, el.scrollTop);
      sync();
    };
    const onWheel = (e: WheelEvent) => act(e.deltaY < 0);
    const onKey = (e: KeyboardEvent) => act(UP_KEYS.has(e.key));
    const hold = () => {
      intent.holding = true;
      act(false);
    };
    // A press on the container itself (its scroll bar, not a message) starts a drag of the bar.
    const onPointerDown = (e: PointerEvent) => {
      if (e.target === el) {
        hold();
      }
    };
    const release = () => {
      if (intent.holding) {
        intent.holding = false;
        intent.userAt = performance.now();
      }
    };
    const onScroll = () => {
      intent.pinned = pinnedRef.current;
      scrolled(
        intent,
        el.scrollTop,
        gapToEnd(el),
        performance.now(),
        BOTTOM_SLACK,
      );
      sync();
    };
    onScroll();
    el.addEventListener("wheel", onWheel, { passive: true });
    el.addEventListener("touchstart", hold, { passive: true });
    el.addEventListener("pointerdown", onPointerDown);
    el.addEventListener("keydown", onKey);
    el.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("touchend", release);
    window.addEventListener("touchcancel", release);
    window.addEventListener("pointerup", release);
    window.addEventListener("pointercancel", release);
    return () => {
      el.removeEventListener("wheel", onWheel);
      el.removeEventListener("touchstart", hold);
      el.removeEventListener("pointerdown", onPointerDown);
      el.removeEventListener("keydown", onKey);
      el.removeEventListener("scroll", onScroll);
      window.removeEventListener("touchend", release);
      window.removeEventListener("touchcancel", release);
      window.removeEventListener("pointerup", release);
      window.removeEventListener("pointercancel", release);
    };
  }, []);
}

/**
 * Keeps `ro` observing exactly the children of `el`: each new one is observed as it mounts, and
 * each removed one is UNOBSERVED as it leaves.
 *
 * The second half is the one that matters for memory. A ResizeObserver holds its targets
 * strongly, so a turn that was only ever observed stays alive after it leaves the DOM — with its
 * whole subtree, mermaid SVGs and KaTeX included. The conversation container lives for the whole
 * session, so without this every session switch left the previous transcript's nodes behind,
 * detached and unreachable from the page but never collected.
 */
export function observeChildren(
  el: Element,
  ro: Pick<ResizeObserver, "observe" | "unobserve">,
): MutationObserver {
  const seen = new WeakSet<Element>();
  const watch = (node: Node) => {
    if (node.nodeType !== 1 || seen.has(node as Element)) {
      return;
    }
    seen.add(node as Element);
    ro.observe(node as Element);
  };
  for (const child of Array.from(el.children)) {
    watch(child);
  }
  // The messages arrive as Preact renders them, so the set to observe changes over time.
  const mo = new MutationObserver((records) => {
    for (const rec of records) {
      rec.removedNodes.forEach((node) => {
        if (!seen.has(node as Element)) {
          return;
        }
        seen.delete(node as Element);
        ro.unobserve(node as Element);
      });
      rec.addedNodes.forEach(watch);
    }
  });
  mo.observe(el, { childList: true });
  return mo;
}

/**
 * Follows the growth that NO event announces.
 *
 * Measured on a real 44-message conversation at 1400x900: the content climbed in steps
 * (58118 -> 58158 -> ... -> 59176) and the LAST step landed at 5486ms, while the modal came
 * down at 6826ms. The anchoring that runs on the quiet window therefore aims at a height the
 * page has already left, and anything that grows afterwards — an image decoding, a font
 * swapping in, mermaid replacing its own drawing — raises no event at all, so `motita:hydrated`
 * (which only covers work a message ANNOUNCES) never fires for it. The reader sees exactly what
 * they reported: it finishes loading and looks right, then something changes the height.
 *
 * A ResizeObserver on the messages is the honest instrument: it fires as part of layout, so it
 * sees the real size of the element rather than a guess made from an event. It is also the
 * reason this is not a timer — a slower machine, a bigger image or a cold font cache all just
 * work, instead of needing the interval retuned.
 *
 * Re-anchoring stays conditional on `pinnedRef`: a reader who deliberately scrolled back is
 * never dragged down by a message above them finishing.
 */
function useResizeFollow(
  scrollRef: { current: HTMLDivElement | null },
  pinnedRef: { current: boolean },
  followEnd: () => void,
) {
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) {
      return undefined;
    }
    let frame = 0;
    const settle = () => {
      if (!pinnedRef.current) {
        return;
      }
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        if (pinnedRef.current) {
          followEnd();
        }
      });
    };
    if (typeof ResizeObserver === "undefined") {
      return undefined;
    }
    const ro = new ResizeObserver(settle);
    ro.observe(el);
    const mo = observeChildren(el, ro);
    return () => {
      cancelAnimationFrame(frame);
      ro.disconnect();
      mo.disconnect();
    };
  }, [followEnd]);
}

/**
 * Follows the deferred work while it grows the page.
 *
 * A formula, a diagram and an emoji image are all built AFTER the message is rendered, and
 * every one of them changes the height of the container. None of that changes any state,
 * so the effect that reacts to state never runs again: the page finished rendering and stayed
 * wherever it was, which is the "it does not go all the way down" symptom.
 *
 * The listener is on the container with `capture`, and the signal is the `motita:hydrated`
 * event one message dispatches when its own deferred work is done. Capture is what keeps it
 * to THIS conversation: the container is the element the messages are rendered into, so an
 * event from inside it is ours, and events do not cross between conversations.
 *
 * `scrollHeight` is therefore read twice — synchronously, and once more on the next frame.
 * The synchronous read is the correct one for a change that has already been laid out; the
 * rAF read catches an image or a font that only has a size after layout, and it is
 * scheduled rather than awaited so it cannot delay the answer's own work.
 */
function useHydratedFollow(
  scrollRef: { current: HTMLDivElement | null },
  pinnedRef: { current: boolean },
  scrollToBottom: () => void,
) {
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) {
      return undefined;
    }
    let frame = 0;
    const onHydrated = () => {
      // A message finishing its own deferred work does NOT clear the modal: a conversation is
      // ready when nothing visible is still pending, which the modal effect reads from
      // `hydrationState` on a settle. This listener is only about keeping the view at the end.
      if (!pinnedRef.current) {
        return;
      }
      scrollToBottom();
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => scrollToBottom());
    };
    el.addEventListener("motita:hydrated", onHydrated, true);
    return () => {
      cancelAnimationFrame(frame);
      el.removeEventListener("motita:hydrated", onHydrated, true);
    };
  }, [scrollToBottom]);
}

export function useChatScroll(view: {
  messages: Message[];
  activity: string | null;
  liveThought: string | null;
  stepsByTurn: Record<number, StepView[]>;
}) {
  const { messages, activity, liveThought, stepsByTurn } = view;
  const scrollRef = useRef<HTMLDivElement>(null);
  const { chatLoading, modalLeaving, requestModal } = useLoadingModal();
  const { pinnedRef, chatLoadingNow, chatFollower, followEnd, scrollToBottom } =
    usePinnedFollow(scrollRef);

  /**
   * The last thing that happens: the bottom is computed and the modal comes down.
   *
   * This is the step that needs the page to have STOPPED, and it is why it hangs off the quiet
   * window rather than off a completion. Measured on the way here: the conversation grew 5431px ->
   * 5611px AFTER the last completion, with `scrollTop` unmoved, because what changes the height
   * last — the images decoding, the font being laid out — raises no event at all. An anchor
   * applied before that growth leaves the reader 181px short of the end, every time. Waiting for
   * two seconds of silence is what makes the final height knowable.
   */
  useEffect(() => {
    const onQuiet = () => {
      if (pinnedRef.current) {
        scrollToBottom();
      }
      // The height has settled, so this one lands. A second pass on the next frame costs nothing
      // and covers a layout that the browser had not committed when the first was measured.
      requestAnimationFrame(() => {
        if (pinnedRef.current) {
          scrollToBottom();
        }
      });
    };
    document.addEventListener(CHAT_QUIET_EVENT, onQuiet);
    return () => document.removeEventListener(CHAT_QUIET_EVENT, onQuiet);
  }, [scrollToBottom]);

  useReaderIntent(scrollRef, pinnedRef);

  useEffect(() => {
    // Following along only if the reader was at the end before this message arrived. Scrolling on
    // every change would drag someone who deliberately went back up to an earlier answer.
    if (!pinnedRef.current) {
      return;
    }
    followEnd();
  }, [messages, activity, liveThought, stepsByTurn, followEnd]);

  useResizeFollow(scrollRef, pinnedRef, followEnd);
  useHydratedFollow(scrollRef, pinnedRef, scrollToBottom);

  /**
   * The session modal follows the conversation, and comes down on a QUIET WINDOW.
   *
   * Every completion is a separate event, and a conversation arrives as many of them, so asking
   * "are we done?" after each one gives an answer that is true and useless: it flips, and the
   * modal flips with it — the flicker. What the reader means by "until everything is parsed" is
   * that the page has STOPPED WORKING, and the only way to know that is to wait for a window with
   * no activity at all. `hydration.ts` restarts that window on every registration and every
   * completion, and raises `motita:chat-quiet` when it has run out.
   *
   * Only then is the bottom computed and the modal taken down, in that order: the final height is
   * not known until the work is over, so scrolling before this is scrolling towards a guess.
   *
   * It also OPENS on the same signal, so a conversation that starts more work while the reader
   * waits puts the modal back up instead of hiding it.
   */
  useEffect(() => {
    // Exposed for the gate: the flicker this replaced was invisible at any single instant, so what
    // has to be measurable is the SEQUENCE of times the modal was put up and taken down.
    (window as GateWindow).__chatLoadingLog = () => ({
      transitions: hydrationLog(),
      visiblePending: hydrationState().visiblePending,
      registered: hydrationState().registered,
    });
    const onQuiet = () => requestModal(hydrationState().visiblePending);
    document.addEventListener(CHAT_QUIET_EVENT, onQuiet);
    return () => document.removeEventListener(CHAT_QUIET_EVENT, onQuiet);
  }, [requestModal]);

  // A transcript that is replaced has to be re-read even if no message ever reports: a
  // conversation whose messages are all ABOVE the fold never starts their work, so no completion
  // would ever arrive to close the window that `switchSession` opened.
  useEffect(() => {
    noteActivity();
  }, [messages]);

  // Every time the modal itself is put up or taken down. This is what "flickering" means, and it is
  // what the gate counts: the underlying readiness can change many times without the reader seeing
  // anything, because the quiet window stands between the two.
  useEffect(() => {
    const log = ((window as GateWindow).__modalLog ||= []);
    log.push({ at: Math.round(performance.now()), loading: chatLoading });
    chatLoadingNow.current = chatLoading;
    if (chatLoading) {
      chatFollower.current.stop();
    }
  }, [chatLoading]);

  useEffect(() => {
    if (!chatLoading) {
      return undefined;
    }
    if (messages.length === 0) {
      return undefined;
    }
    const id = requestAnimationFrame(() => {
      const el = scrollRef.current;
      if (el?.querySelector("[data-hydrating]") === null) {
        requestModal(false);
      }
    });
    return () => cancelAnimationFrame(id);
  }, [messages, chatLoading]);

  return { scrollRef, chatLoading, modalLeaving, requestModal };
}
