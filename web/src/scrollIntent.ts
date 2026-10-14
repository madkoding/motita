// Whether the conversation keeps following its end, decided from the reader's own input. Kept free
// of imports so the tests can load it as it is.
//
// Reported from real use: "I try to scroll up but it goes back to the end of the conversation".
// The old rule unpinned the reader only when a wheel or a touch began while they were already
// more than the slack away from the end. The first flick from the end starts INSIDE the slack, so
// it never unpinned anyone, and during a run the follower glided them back before the next flick
// could: the reader could not leave the end at all while the answer was being written.
//
// The rule now is about DIRECTION and WHO moved: the reader moving up lets go of the end at once,
// the reader moving down to near the end takes it again, and a scroll nobody asked for (content
// that grew or shrank, the follower itself) changes nothing, except landing exactly on the end.

// How long after the reader's own wheel, touch, key or drag a scroll is still taken as theirs.
export const USER_SCROLL_MS = 800;

// How close to the end a scroll nobody asked for has to land to re-pin the reader.
const AT_END_PX = 2;

export interface ScrollIntent {
  // Whether the view follows the end.
  pinned: boolean;
  // When the reader last acted on the scroll (ms, performance.now()).
  userAt: number;
  // Whether the reader is holding the scroll now: a drag on the bar, a finger on the screen.
  holding: boolean;
  // The scroll position at the last scroll event, so a move has a direction.
  lastTop: number;
}

export function newIntent(top = 0): ScrollIntent {
  return { pinned: true, userAt: -Infinity, holding: false, lastTop: top };
}

// readerActs records the reader's own input. up says it moves the view up (a wheel up, an arrow
// up); when the box can move up, that lets go of the end at once, before any scroll event, so a
// follower already gliding stops on its next frame.
export function readerActs(
  s: ScrollIntent,
  now: number,
  up: boolean,
  top: number,
): void {
  s.userAt = now;
  if (up && top > 0) {
    s.pinned = false;
  }
}

// scrolled updates the intent for a scroll event at position top, gap pixels from the end.
export function scrolled(
  s: ScrollIntent,
  top: number,
  gap: number,
  now: number,
  slack: number,
): void {
  const theirs = s.holding || now - s.userAt < USER_SCROLL_MS;
  const delta = top - s.lastTop;
  s.lastTop = top;
  if (gap <= AT_END_PX) {
    s.pinned = true;
    return;
  }
  if (!theirs) {
    return;
  }
  if (delta < 0) {
    s.pinned = false;
  } else if (delta > 0 && gap <= slack) {
    s.pinned = true;
  }
}
