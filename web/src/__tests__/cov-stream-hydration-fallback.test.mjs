// Without IntersectionObserver nothing would ever say a message came into view, so every message
// is built as soon as it is registered: a slow page is a better failure than diagrams that
// never appear.
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, source } from "./cov-stream-helpers.mjs";

installDom();
delete globalThis.IntersectionObserver;
const h = await source("hydration.ts");

test("every message is built at once when the browser cannot observe the viewport", async () => {
  const built = [];
  const els = [document.createElement("div"), document.createElement("div")];
  const stops = els.map((el, i) =>
    h.watchForHydration(
      el,
      () => {
        built.push(i);
      },
      () => true,
    ),
  );
  assert.deepEqual(built, [0, 1]);
  assert.deepEqual(
    els.map((el) => el.dataset.inview),
    ["1", "1"],
  );
  await Promise.resolve();
  await Promise.resolve();
  assert.deepEqual(
    els.map((el) => el.dataset.hydrating),
    [undefined, undefined],
  );
  assert.deepEqual(h.hydrationState(), {
    registered: 2,
    pending: 0,
    visiblePending: false,
  });
  // Nothing is dropped later either: every message counts as in view.
  h.releaseFar(els[0]);
  assert.equal(els[0].dataset.released, undefined);
  stops.forEach((s) => s());
  assert.equal(h.hydrationState().registered, 0);
  h.cancelQuiet();
});
