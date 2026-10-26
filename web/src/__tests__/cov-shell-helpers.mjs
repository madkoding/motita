// Shared setup of the shell group's tests: a jsdom page with the browser APIs jsdom lacks, and the
// module hook of tsx-hooks.mjs, through which node imports the TSX sources themselves (each
// compiled on its own, `stub` replacing what they import with a stand-in).
import { JSDOM } from "jsdom";

export { stub, stubModule, load, unreachable } from "./tsx-hooks.mjs";
export { text, flush, jsonResponse } from "./dom-helpers.mjs";

// APP_STORE_STUB stands in for ./appStore: the real context, and a useAppStore the test supplies
// (as globalThis.covShellUseAppStore), instead of every hook of the app.
export const APP_STORE_STUB = `
import { createContext } from "preact";
import { useContext } from "preact/hooks";
export const AppCtx = createContext(null);
export function useApp() {
  return useContext(AppCtx);
}
export function useAppStore(api) {
  return globalThis.covShellUseAppStore(api);
}
`;

// setupDom installs a jsdom window as the globals the sources and preact read.
export function setupDom(url = "http://localhost/") {
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
    "Node",
    "localStorage",
    "history",
    "location",
    "MutationObserver",
    "getComputedStyle",
    "NodeFilter",
    "Element",
    "Text",
    "DocumentFragment",
    "HTMLTemplateElement",
    "DOMParser",
    "SVGElement",
  ]) {
    Object.defineProperty(globalThis, k, {
      value: w[k],
      configurable: true,
      writable: true,
    });
  }
  Object.defineProperty(globalThis, "navigator", {
    value: w.navigator,
    configurable: true,
  });
  w.matchMedia = () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
  });
  globalThis.matchMedia = w.matchMedia;
  // A frame reports a moment far ahead, so whatever animates by the clock (the typed terminal)
  // finishes on its first frame and a test never waits on it.
  globalThis.requestAnimationFrame = (cb) =>
    setTimeout(() => cb(performance.now() + 60_000), 0);
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id);
  w.requestAnimationFrame = globalThis.requestAnimationFrame;
  w.cancelAnimationFrame = globalThis.cancelAnimationFrame;
  w.scrollTo = () => {};
  w.HTMLElement.prototype.scrollTo = function () {};
  w.HTMLElement.prototype.scrollIntoView = function () {};
  return dom;
}

// mounter renders vnodes into fresh roots, inside act().
export async function mounter() {
  const { render } = await import("preact");
  const { act } = await import("preact/test-utils");
  return async function mount(vnode) {
    const root = document.createElement("div");
    document.body.appendChild(root);
    await act(() => render(vnode, root));
    return {
      root,
      rerender: (v) => act(() => render(v, root)),
      done: async () => {
        await act(() => render(null, root));
        root.remove();
      },
    };
  };
}
