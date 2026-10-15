// Shared setup for the cov-render-*.test.mjs files: a jsdom page wired into the globals the
// components read, and the module hook of tsx-hooks.mjs, through which node imports the TSX
// sources themselves (each compiled on its own).
//
// `overrides` (given to setupDom, before the first import) replaces what the sources import under
// a specifier: { mermaid: <source of a fake> } serves a fake, and the value FAIL makes the import
// fail, the way a lazily loaded chunk that cannot be fetched does.
import { JSDOM } from "jsdom";
import { stub } from "./tsx-hooks.mjs";
import { flush as flushTimers } from "./dom-helpers.mjs";

export { FAIL, load } from "./tsx-hooks.mjs";

export function setupDom({ overrides = {}, url = "http://localhost/" } = {}) {
  for (const [spec, source] of Object.entries(overrides)) {
    stub(spec, source);
  }
  const dom = new JSDOM("<!doctype html><html><body></body></html>", {
    url,
    pretendToBeVisual: true,
  });
  const w = dom.window;
  for (const k of [
    "document",
    "window",
    "MouseEvent",
    "KeyboardEvent",
    "Event",
    "CustomEvent",
    "HTMLElement",
    "HTMLInputElement",
    "Element",
    "Node",
    "NodeFilter",
    "Text",
    "DocumentFragment",
    "localStorage",
  ]) {
    globalThis[k] = w[k];
  }
  Object.defineProperty(globalThis, "navigator", {
    value: w.navigator,
    configurable: true,
  });
  w.matchMedia = () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
  });
  return dom;
}

// flush lets pending promises and zero-delay timers run.
export const flush = (n = 5) => flushTimers(n);

export const text = (el) => el.textContent.replace(/\s+/g, " ").trim();

// waitFor polls `cond` until it holds (a lazily imported module may take a while to compile).
export async function waitFor(cond, what = "condition", ms = 5000) {
  const end = Date.now() + ms;
  while (!cond()) {
    if (Date.now() > end) {
      throw new Error(`timed out waiting for ${what}`);
    }
    await new Promise((r) => setTimeout(r, 5));
  }
}
