// Shared set-up for the cov-stream-* tests: a jsdom window installed as the globals the web UI
// reads, and the module hook of tsx-hooks.mjs, through which node imports the sources one module
// at a time (`stub` replaces what they import with a stand-in, and `unreachable` makes a source
// file fail to load, the way a chunk that could not be fetched does).
import { JSDOM } from "jsdom";
import { flush as flushTimers } from "./dom-helpers.mjs";

export { stub, unreachable, load as source } from "./tsx-hooks.mjs";
export { text } from "./dom-helpers.mjs";

// installDom puts a fresh jsdom window behind the browser globals and returns it.
export function installDom(url = "http://localhost/") {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url });
  for (const k of [
    "document",
    "window",
    "MouseEvent",
    "KeyboardEvent",
    "FocusEvent",
    "Event",
    "CustomEvent",
    "HTMLElement",
    "HTMLInputElement",
    "Element",
    "Node",
    "localStorage",
    "sessionStorage",
    "history",
    "location",
    "DOMParser",
    "MutationObserver",
    "NodeFilter",
    "StaticRange",
    "Text",
  ]) {
    Object.defineProperty(globalThis, k, {
      value: dom.window[k],
      configurable: true,
      writable: true,
    });
  }
  Object.defineProperty(globalThis, "navigator", {
    value: dom.window.navigator,
    configurable: true,
    writable: true,
  });
  window.matchMedia = () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
  });
  globalThis.requestAnimationFrame = (cb) =>
    setTimeout(() => cb(Date.now()), 0);
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id);
  window.requestAnimationFrame = globalThis.requestAnimationFrame;
  window.cancelAnimationFrame = globalThis.cancelAnimationFrame;
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  window.ResizeObserver = globalThis.ResizeObserver;
  globalThis.IntersectionObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  window.IntersectionObserver = globalThis.IntersectionObserver;
  return dom;
}

// flush lets pending promises and preact's effects run.
export const flush = (times = 5) => flushTimers(times);

// renderHook mounts a component that calls `fn` with the current props and keeps what it
// returned in result.current. rerender(props) renders it again; unmount() takes it down.
export async function renderHook(fn, props) {
  const { h, render } = await import("preact");
  const { act } = await import("preact/test-utils");
  const result = { current: null, renders: 0 };
  function Probe(p) {
    result.renders++;
    result.current = fn(p.props);
    return null;
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(h(Probe, { props }), root));
  return {
    result,
    act,
    rerender: (next) => act(() => render(h(Probe, { props: next }), root)),
    unmount: async () => {
      await act(() => render(null, root));
      root.remove();
    },
  };
}

// res is a stand-in for a fetch Response.
export function res(status, body, extra = {}) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => {
      if (body instanceof Error) {
        throw body;
      }
      return body;
    },
    ...extra,
  };
}
