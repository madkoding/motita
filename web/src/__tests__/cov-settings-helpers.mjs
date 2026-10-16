// Shared setup for the cov-settings-* tests: a jsdom page installed as the globals, the module
// hook of tsx-hooks.mjs that lets node import the TSX sources themselves (each compiled on its own,
// `stub` replacing what they import with a stand-in), and a fake api that answers from a route
// table and records what was asked.
import { JSDOM } from "jsdom";
import { load as loadSource } from "./tsx-hooks.mjs";
import { text } from "./dom-helpers.mjs";

export { stub } from "./tsx-hooks.mjs";
export { text };

// load imports a source file (e.g. "Settings.tsx"), and makes preact run effects on the next turn
// of the event loop (not after a frame and a timeout), so they run the same way whether or not a
// test mocks setTimeout.
export async function load(file) {
  const { options } = await import("preact");
  options.requestAnimationFrame = (cb) => setImmediate(cb);
  return loadSource(file);
}

export function installDom(url = "http://localhost/", opts = {}) {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", {
    url,
    pretendToBeVisual: true,
    ...opts,
  });
  for (const k of [
    "document",
    "window",
    "MouseEvent",
    "KeyboardEvent",
    "PointerEvent",
    "FocusEvent",
    "InputEvent",
    "Event",
    "CustomEvent",
    "HTMLElement",
    "HTMLInputElement",
    "HTMLDialogElement",
    "Element",
    "Node",
    "localStorage",
    "sessionStorage",
    "getComputedStyle",
  ]) {
    if (dom.window[k] !== undefined) {
      globalThis[k] = dom.window[k];
    }
  }
  Object.defineProperty(globalThis, "navigator", {
    value: dom.window.navigator,
    configurable: true,
  });
  window.matchMedia = () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
  });
  globalThis.matchMedia = window.matchMedia;
  // Frames run on setImmediate, so preact's effects still run while a test mocks setTimeout.
  globalThis.requestAnimationFrame = (cb) => setImmediate(() => cb(Date.now()));
  globalThis.cancelAnimationFrame = (id) => clearImmediate(id);
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

export const json = (status, body) =>
  new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

// fakeApi answers each request with the first handler whose key ("METHOD path-prefix") matches.
// A handler is a Response, a function (path, init) => Response|Promise, or an Error to throw.
export function fakeApi(routes = {}) {
  const calls = [];
  const api = async (path, init = {}) => {
    const method = init.method || "GET";
    let body;
    if (typeof init.body === "string") {
      try {
        body = JSON.parse(init.body);
      } catch {
        body = init.body;
      }
    }
    calls.push({ method, path, body });
    for (const [key, h] of Object.entries(routes)) {
      const [m, p] = key.split(" ");
      if (m === method && path.startsWith(p)) {
        const r = typeof h === "function" ? await h(path, init) : h;
        if (r instanceof Error) {
          throw r;
        }
        return typeof r?.clone === "function" ? r.clone() : r;
      }
    }
    throw new Error("unrouted " + method + " " + path);
  };
  return { api, calls, routes };
}

// flush waits one turn of the event loop; it uses setImmediate so it still works while the tests
// mock setTimeout and setInterval.
export const flush = () => new Promise((r) => setImmediate(r));

// settle lets pending promises and effects run a few rounds.
export async function settle(n = 5) {
  for (let i = 0; i < n; i++) {
    await flush();
  }
}

// click dispatches a bubbling click on el.
export function click(el) {
  if (!el) {
    throw new Error("click: no element");
  }
  el.dispatchEvent(
    new window.MouseEvent("click", { bubbles: true, cancelable: true }),
  );
}

// type sets an input's value and fires input (and change) the way preact listens to it.
export function type(el, value) {
  if (!el) {
    throw new Error("type: no element");
  }
  el.value = value;
  el.dispatchEvent(new window.Event("input", { bubbles: true }));
  el.dispatchEvent(new window.Event("change", { bubbles: true }));
}

export function key(el, k, extra = {}) {
  el.dispatchEvent(
    new window.KeyboardEvent("keydown", {
      key: k,
      bubbles: true,
      cancelable: true,
      ...extra,
    }),
  );
}

// byText finds the first element matching sel whose text includes t.
export function byText(root, sel, t) {
  return [...root.querySelectorAll(sel)].find((e) => text(e).includes(t));
}
