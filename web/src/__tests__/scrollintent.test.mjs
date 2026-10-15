// The conversation lets the reader scroll up while an answer is being written, and follows the end
// again when they come back to it. Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";

const { newIntent, readerActs, scrolled, scrollKey, USER_SCROLL_MS } =
  await import("../scrollIntent.ts");

const SLACK = 120;
// The scroll position at the end of a long conversation, and when the reader first and next acts.
const END_TOP = 5000;
const ACT_AT = 1000;
const NEXT_ACT_AT = 2000;
// A scroll event this soon after the reader acted, or a frame later for a finger, is theirs.
const SOON_MS = 10;
const FRAME_MS = 16;
// Moves well inside the slack: a flick of the wheel, a nudge of a finger.
const FLICK_PX = 50;
const NUDGE_PX = 10;
// A screen's height, half of it, and a gap from the end within the slack.
const SCREEN_PX = 1000;
const HALF_SCREEN_PX = 500;
const NEAR_PX = 100;
// Content that shrank under a reader who did nothing for a while.
const SHRUNK_TOP = 4511;
const SHRINK_PX = 40;
const LONG_AFTER_MS = 5000;

test("the first wheel up from the end lets go of the end at once", () => {
  const s = newIntent(END_TOP);
  readerActs(s, ACT_AT, true, END_TOP);
  assert.equal(s.pinned, false, "unpinned before any scroll event");
  // The flick lands inside the slack, which used to keep the reader pinned.
  scrolled(s, END_TOP - FLICK_PX, FLICK_PX, ACT_AT + SOON_MS, SLACK);
  assert.equal(s.pinned, false);
});

test("a finger dragging the view up from the end lets go of it", () => {
  const s = newIntent(END_TOP);
  s.holding = true;
  readerActs(s, ACT_AT, false, END_TOP);
  assert.equal(s.pinned, true, "a touch alone says nothing yet");
  scrolled(s, END_TOP - NUDGE_PX, NUDGE_PX, ACT_AT + FRAME_MS, SLACK);
  assert.equal(s.pinned, false);
});

test("the content growing or shrinking never unpins the reader", () => {
  const s = newIntent(SHRUNK_TOP + SHRINK_PX);
  scrolled(s, SHRUNK_TOP, SHRINK_PX, LONG_AFTER_MS, SLACK);
  assert.equal(s.pinned, true);
});

test("coming back down near the end takes it again", () => {
  const s = newIntent(END_TOP);
  const screenUp = END_TOP - SCREEN_PX;
  readerActs(s, ACT_AT, true, END_TOP);
  scrolled(s, screenUp, SCREEN_PX, ACT_AT + SOON_MS, SLACK);
  assert.equal(s.pinned, false);
  readerActs(s, NEXT_ACT_AT, false, screenUp);
  scrolled(s, END_TOP - NEAR_PX, NEAR_PX, NEXT_ACT_AT + SOON_MS, SLACK);
  assert.equal(s.pinned, true);
});

test("a move down that stops far from the end leaves the reader where they are", () => {
  const s = newIntent(END_TOP);
  const twoScreensUp = END_TOP - 2 * SCREEN_PX;
  readerActs(s, ACT_AT, true, END_TOP);
  scrolled(s, twoScreensUp, 2 * SCREEN_PX, ACT_AT + SOON_MS, SLACK);
  readerActs(s, NEXT_ACT_AT, false, twoScreensUp);
  scrolled(
    s,
    twoScreensUp + HALF_SCREEN_PX,
    2 * SCREEN_PX - HALF_SCREEN_PX,
    NEXT_ACT_AT + SOON_MS,
    SLACK,
  );
  assert.equal(s.pinned, false);
});

test("landing exactly on the end re-pins, whoever moved", () => {
  const s = newIntent(END_TOP);
  const screenUp = END_TOP - SCREEN_PX;
  readerActs(s, ACT_AT, true, END_TOP);
  scrolled(s, screenUp, SCREEN_PX, ACT_AT + SOON_MS, SLACK);
  scrolled(s, screenUp, 0, ACT_AT + SOON_MS + USER_SCROLL_MS * 4, SLACK);
  assert.equal(s.pinned, true);
});

test("a wheel up on a conversation that does not scroll keeps following", () => {
  const s = newIntent(0);
  readerActs(s, 1000, true, 0);
  assert.equal(s.pinned, true);
});

test("the scroll keys count as scrolling, but not while typing in a field", () => {
  for (const key of [
    "ArrowUp",
    "ArrowDown",
    "PageUp",
    "PageDown",
    "Home",
    "End",
    " ",
  ]) {
    assert.equal(scrollKey(key, false), true, key);
    assert.equal(scrollKey(key, true), false, key + " in a field");
  }
  assert.equal(scrollKey("a", false), false);
  assert.equal(scrollKey("Enter", false), false);
});
