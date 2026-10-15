// The reveal of a streamed answer: text that grows after the message mounted is painted hidden
// with the CSS Custom Highlight API and shown at a steady pace, frame by frame, with a fading
// edge. Runs against jsdom with a stand-in highlight registry and a hand-driven animation clock.
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, source, flush } from "./cov-stream-helpers.mjs";

const dom = installDom();
globalThis.NodeFilter = dom.window.NodeFilter;
globalThis.StaticRange = dom.window.StaticRange;
const { streamReveal } = await source("streamReveal.ts");

// The animation clock: frames run only when the test says so.
let frames = new Map();
let nextFrame = 1;
globalThis.requestAnimationFrame = (cb) => {
  frames.set(nextFrame, cb);
  return nextFrame++;
};
globalThis.cancelAnimationFrame = (id) => frames.delete(id);
function frame(now) {
  const pending = [...frames.values()];
  frames = new Map();
  pending.forEach((cb) => cb(now));
  return pending.length;
}

// The highlight registry: each highlight keeps the ranges added to it.
class FakeHighlight {
  constructor() {
    this.ranges = new Set();
  }
  add(r) {
    this.ranges.add(r);
  }
  delete(r) {
    return this.ranges.delete(r);
  }
}
function withHighlights() {
  const reg = new Map();
  globalThis.CSS = { highlights: reg };
  globalThis.Highlight = FakeHighlight;
  return reg;
}
function withoutHighlights() {
  delete globalThis.CSS;
  delete globalThis.Highlight;
}

// spans lists a highlight's ranges as [start, end] character offsets inside `el`'s text.
function spans(reg, name, el) {
  const nodes = [];
  const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  for (let n = walk.nextNode(); n; n = walk.nextNode()) {
    nodes.push(n);
  }
  const at = (node, off) => {
    let sum = 0;
    for (const n of nodes) {
      if (n === node) {
        return sum + off;
      }
      sum += n.data.length;
    }
    return -1;
  };
  return [...reg.get(name).ranges].map((r) => [
    at(r.startContainer, r.startOffset),
    at(r.endContainer, r.endOffset),
  ]);
}

function message(html) {
  const el = document.createElement("div");
  el.innerHTML = html;
  document.body.appendChild(el);
  return el;
}

test("without the Highlight API the answer simply shows as it arrives", () => {
  withoutHighlights();
  const el = message("Hello");
  const r = streamReveal(el, "Hello");
  el.textContent = "Hello world";
  r.update("Hello world");
  assert.equal(frames.size, 0, "nothing is animated");
  r.dispose();
  globalThis.CSS = {};
  globalThis.Highlight = FakeHighlight;
  const r2 = streamReveal(el, "x");
  r2.update("xy");
  assert.equal(frames.size, 0, "a registry is needed too");
  r2.dispose();
  withoutHighlights();
  el.remove();
});

test("text that grows is hidden and revealed letter by letter, with a fading edge", async () => {
  const reg = withHighlights();
  const existing = new FakeHighlight();
  reg.set("stream-fade-2", existing);
  const el = message('Hel<b>lo</b><span class="copy-msg-btn">Copy</span>');
  const r = streamReveal(el, "Hello");
  assert.deepEqual(
    [...reg.keys()].sort(),
    ["stream-fade-1", "stream-fade-2", "stream-fade-3", "stream-hidden"],
  );
  assert.equal(reg.get("stream-fade-2"), existing, "a highlight already there is reused");
  assert.equal(reg.get("stream-hidden").ranges.size, 0, "what mounted shows at once");

  el.innerHTML =
    'Hel<b>lo</b> big world<span class="copy-msg-btn">Copy</span>';
  r.update("Hello big world");
  // Hello (5) is shown; " big world" (10 more) is hidden, the copy button's label is not counted.
  assert.deepEqual(spans(reg, "stream-hidden", el), [[5, 15]]);
  assert.deepEqual(spans(reg, "stream-fade-1", el), [[2, 5]]);
  assert.deepEqual(spans(reg, "stream-fade-2", el), [[0, 2]]);
  assert.deepEqual(spans(reg, "stream-fade-3", el), [[0, 0]]);
  assert.equal(frames.size, 1, "a frame is asked for");

  // More text while the reveal runs asks for no second frame.
  el.innerHTML = "Hel<b>lo</b> big world!";
  r.update("Hello big world!");
  assert.equal(frames.size, 1);

  assert.equal(frame(1000), 1, "the first frame starts the clock");
  assert.deepEqual(spans(reg, "stream-hidden", el), [[5, 16]]);
  frame(1050);
  // At least 90 characters a second, faster when far behind: 11 left / 0.6s ≈ 18/s → 90/s.
  const [[from]] = spans(reg, "stream-hidden", el);
  assert.ok(from > 5 && from < 16, `revealed up to ${from}`);
  frame(3000);
  assert.equal(reg.get("stream-hidden").ranges.size, 0, "all of it is shown");
  assert.equal(frames.size, 0, "and the animation stops");

  // Hydration replaces text under the reveal: the list is read again before the next paint.
  el.innerHTML = "Hel<b>lo</b> big world! And more";
  r.update("Hello big world! And more");
  el.querySelector("b").textContent = "LO";
  await flush(1);
  frame(4000);
  assert.deepEqual(spans(reg, "stream-hidden", el), [[16, 25]]);
  // An edit, not more of the same answer, shows at once.
  el.innerHTML = "Something else";
  r.update("Something else");
  assert.equal(reg.get("stream-hidden").ranges.size, 0);
  r.dispose();
  assert.equal(frames.size, 0, "dispose cancels the frame");
  el.remove();
});

test("text that shrinks under a reveal is clamped to what is left", async () => {
  const reg = withHighlights();
  const el = message("abc");
  const r = streamReveal(el, "abc");
  el.innerHTML = "abcdefghij";
  r.update("abcdefghij");
  assert.deepEqual(spans(reg, "stream-hidden", el), [[3, 10]]);
  el.innerHTML = "ab";
  await flush(1);
  frame(100);
  assert.equal(reg.get("stream-hidden").ranges.size, 0, "nothing left to hide");
  r.dispose();
  el.remove();
});

test("an empty message has nothing to hide", () => {
  const reg = withHighlights();
  const el = message("");
  const r = streamReveal(el, "");
  r.update("");
  r.update("x");
  assert.equal(reg.get("stream-hidden").ranges.size, 0);
  assert.equal(frames.size, 0);
  r.dispose();
  el.remove();
});

test("without MutationObserver a text node shortened in place still paints to its end", () => {
  const reg = withHighlights();
  const saved = globalThis.MutationObserver;
  delete globalThis.MutationObserver;
  try {
    const el = message("ab");
    const r = streamReveal(el, "ab");
    el.innerHTML = "ab<i>cdefgh</i>";
    r.update("abcdefgh");
    assert.deepEqual(spans(reg, "stream-hidden", el), [[2, 8]]);
    // Nobody reports the change: the reveal paints against the text as it is now.
    el.querySelector("i").firstChild.data = "c";
    frame(10);
    assert.deepEqual(spans(reg, "stream-hidden", el), [[2, 3]]);
    r.dispose();
    el.remove();
  } finally {
    globalThis.MutationObserver = saved;
  }
});
