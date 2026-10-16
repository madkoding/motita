// Runs the real Lightbox in jsdom (via tsx-hooks.mjs, since node cannot import TSX): zoom
// buttons, keyboard, pair navigation and closing.
// Run with: node --test web/src/__tests__/lightbox.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { load } from "./tsx-hooks.mjs";

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const k of [
  "document",
  "window",
  "KeyboardEvent",
  "HTMLElement",
  "Node",
]) {
  globalThis[k] = dom.window[k];
}
Object.defineProperty(globalThis, "navigator", {
  value: dom.window.navigator,
  configurable: true,
});
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const { Lightbox } = await load("Lightbox.tsx");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

// act() flushes Preact's pending renders AND effects before it resolves. A fixed timer was not
// enough: under load the effect that attaches the key listener could still be pending when the next
// key was sent, so the key was lost and the test failed now and then.
const key = (k) =>
  document.dispatchEvent(
    new dom.window.KeyboardEvent("keydown", { key: k, bubbles: true }),
  );

test("zoom, fit, pair navigation and close work", async () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  let closed = 0;
  await act(() => {
    render(
      h(Lightbox, {
        images: [
          { src: "a.png", label: "A" },
          { src: "b.png", label: "B" },
        ],
        start: 0,
        onClose: () => {
          closed++;
        },
      }),
      root,
    );
  });
  const zoom = () =>
    document.querySelector("[data-testid=lightbox-zoom]").textContent;
  const img = () => document.querySelector(".lb-img").getAttribute("src");
  assert.equal(zoom(), "100%");
  assert.equal(img(), "a.png");
  // A report opens it from inside a chat bubble, whose backdrop-filter would trap a fixed layer
  // in the bubble's box: it is drawn at the end of <body>, out of the tree that rendered it.
  assert.equal(root.querySelector(".lb"), null);
  assert.equal(document.querySelector(".lb").parentElement, document.body);

  await act(() => key("+"));
  assert.equal(zoom(), "125%");
  await act(() => {
    key("-");
    key("-");
  });
  assert.equal(zoom(), "80%");
  await act(() => key("0"));
  assert.equal(zoom(), "100%");

  await act(() => key("ArrowRight"));
  assert.equal(img(), "b.png");
  assert.match(document.querySelector(".lb-title").textContent, /B \(2\/2\)/);
  await act(() => key("ArrowRight"));
  assert.equal(img(), "a.png", "the set wraps around");

  await act(() => key("Escape"));
  assert.equal(closed, 1);
  await act(() => render(null, root));
  assert.equal(
    document.querySelector(".lb"),
    null,
    "unmounting takes the portal down",
  );
});

test("a single image has no navigation", async () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => {
    render(
      h(Lightbox, {
        images: [{ src: "a.png", label: "A" }],
        start: 0,
        onClose: () => {},
      }),
      root,
    );
  });
  assert.equal(document.querySelector('[aria-label="Next image"]'), null);
  assert.doesNotMatch(document.querySelector(".lb-title").textContent, /\(/);
});
