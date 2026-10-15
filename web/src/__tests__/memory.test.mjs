// Regression tests for what the web UI keeps alive: nodes that left the DOM must be released,
// and closed drawers must not keep animating. Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { load } from "./tsx-hooks.mjs";
import { JSDOM } from "jsdom";

const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(here, "..", "index.css"), "utf8");

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
globalThis.document = dom.window.document;
globalThis.window = dom.window;
globalThis.MutationObserver = dom.window.MutationObserver;
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

// useChatScroll.ts imports its siblings without extensions, which tsx-hooks.mjs resolves.
const { observeChildren } = await load("useChatScroll.ts");

// A ResizeObserver stand-in that records what it is currently holding.
function fakeObserver() {
  const held = new Set();
  return {
    held,
    observe: (el) => held.add(el),
    unobserve: (el) => held.delete(el),
  };
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

test("the conversation stops observing a turn once it leaves the DOM", async () => {
  const box = document.createElement("main");
  document.body.appendChild(box);
  const first = document.createElement("div");
  box.appendChild(first);
  const ro = fakeObserver();
  const mo = observeChildren(box, ro);
  assert.ok(ro.held.has(first), "a turn already mounted is observed");

  const second = document.createElement("div");
  box.appendChild(second);
  await flush();
  assert.ok(ro.held.has(second), "a turn that mounts later is observed");

  // A session switch clears the transcript: every turn leaves the container.
  box.replaceChildren();
  await flush();
  assert.equal(
    ro.held.size,
    0,
    "no detached turn is kept alive by the observer",
  );

  // The next transcript mounts into the same container and is followed again.
  const next = document.createElement("div");
  box.appendChild(next);
  await flush();
  assert.deepEqual(
    [...ro.held],
    [next],
    "the new transcript is observed, and only it",
  );
  mo.disconnect();
});

test("a turn that is moved stays observed", async () => {
  const box = document.createElement("main");
  const a = document.createElement("div");
  const b = document.createElement("div");
  box.append(a, b);
  const ro = fakeObserver();
  const mo = observeChildren(box, ro);
  a.before(b);
  await flush();
  assert.ok(
    ro.held.has(a) && ro.held.has(b),
    "reordering does not drop either turn",
  );
  mo.disconnect();
});

test("a closed drawer runs none of its CRT animations", () => {
  assert.match(
    css,
    /\.term-drawer:not\(\.is-open\) \.term-panel \*[^{]*\{\s*animation:\s*none;?\s*\}/,
    "the panel of a closed drawer turns its animations off",
  );
});
