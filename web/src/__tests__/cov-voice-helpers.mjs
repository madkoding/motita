// Shared setup of the voice group's coverage tests: a jsdom page for the globals a component
// needs, and the module hook of tsx-hooks.mjs, through which node imports the TS/TSX sources one by
// one (`stub` replaces a package they import, such as onnxruntime, with a stand-in).
import { JSDOM } from "jsdom";

export { stub, load as source } from "./tsx-hooks.mjs";
export { jsonResponse as json } from "./dom-helpers.mjs";

// setupDom installs a jsdom window's globals on globalThis and returns the JSDOM.
export function setupDom(url = "https://localhost/") {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url });
  for (const k of [
    "document",
    "window",
    "Event",
    "CustomEvent",
    "KeyboardEvent",
    "MouseEvent",
    "HTMLElement",
    "HTMLInputElement",
    "HTMLSelectElement",
    "Element",
    "Node",
    "NodeFilter",
    "Range",
    "localStorage",
  ]) {
    globalThis[k] = dom.window[k];
  }
  Object.defineProperty(globalThis, "navigator", {
    value: dom.window.navigator,
    configurable: true,
    writable: true,
  });
  globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id);
  return dom;
}

// settle lets pending promises and preact's re-renders run.
export async function settle(act, rounds = 3) {
  for (let i = 0; i < rounds; i++) {
    await act(() => new Promise((r) => setImmediate(r)));
  }
}
