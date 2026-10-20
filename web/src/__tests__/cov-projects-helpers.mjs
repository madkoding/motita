// Shared setup for the cov-projects-* tests: a jsdom window, the module hook of tsx-hooks.mjs that
// lets node import the TSX sources (with the app store replaced by a bare context, so a test hands
// each component exactly the store it reads), a fake gateway answer and a mount helper.
import { JSDOM } from "jsdom";
import { load, stub, stubModule } from "./tsx-hooks.mjs";

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
  pretendToBeVisual: true,
});
for (const k of [
  "document",
  "window",
  "MouseEvent",
  "KeyboardEvent",
  "Event",
  "CustomEvent",
  "HTMLElement",
  "HTMLInputElement",
  "HTMLSelectElement",
  "Node",
  "localStorage",
]) {
  globalThis[k] = dom.window[k];
}
Object.defineProperty(globalThis, "navigator", {
  value: dom.window.navigator,
  configurable: true,
});
window.matchMedia = () => ({
  matches: false,
  addEventListener() {},
  removeEventListener() {},
});
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

// The app store is replaced by a bare context, so a component reads the store a test builds.
stub(
  "./appStore",
  `
import { createContext } from "preact";
import { useContext } from "preact/hooks";
export const AppCtx = createContext(null);
export function useApp() {
  return useContext(AppCtx);
}
`,
);

// bundle imports the named exports of the given source files, each loaded as its own module
// through tsx-hooks.mjs, plus the stand-in store's AppCtx.
export async function bundle(exportsByFile) {
  const mods = { AppCtx: (await stubModule("./appStore")).AppCtx };
  for (const [file, names] of Object.entries(exportsByFile)) {
    const m = await load(file);
    for (const name of names) {
      mods[name] = m[name];
    }
  }
  return mods;
}

export const { h, render } = await import("preact");
export const { act } = await import("preact/test-utils");

// res is a gateway answer: ok for a 2xx status, json() the body (or a rejection when it is an Error).
export function res(status, body) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () =>
      body instanceof Error ? Promise.reject(body) : Promise.resolve(body),
  };
}

// fakeApi answers each request with the first route whose prefix matches "METHOD path", and
// records every call.
export function fakeApi(routes = {}) {
  const calls = [];
  const api = async (path, init = {}) => {
    const method = init.method || "GET";
    calls.push({
      path,
      method,
      body: init.body ? JSON.parse(init.body) : undefined,
    });
    const key = Object.keys(routes).find((k) =>
      (method + " " + path).startsWith(k),
    );
    if (!key) {
      throw new Error("no route for " + method + " " + path);
    }
    const r = routes[key];
    return typeof r === "function" ? r(path, init) : r;
  };
  api.calls = calls;
  return api;
}

// flush lets pending promises and effects run.
export async function flush(n = 5) {
  for (let i = 0; i < n; i++) {
    await act(async () => {
      // setImmediate, not setTimeout: a test may have mocked the timers.
      await new Promise((r) => setImmediate(r));
    });
  }
}

// mount renders a vnode into a fresh container under the given context value.
export async function mount(AppCtx, value, vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const draw = (v, node = vnode) =>
    act(() => {
      render(h(AppCtx.Provider, { value: v }, node), root);
    });
  await draw(value);
  return {
    root,
    rerender: (v, node) => draw(v, node),
    q: (sel) => root.querySelector(sel) ?? document.querySelector(sel),
    qa: (sel) => {
      const inside = [...root.querySelectorAll(sel)];
      return inside.length ? inside : [...document.querySelectorAll(sel)];
    },
    unmount: async () => {
      await act(() => render(null, root));
      root.remove();
    },
  };
}

// buttonByText finds the button whose text contains the given string.
export function buttonByText(root, text) {
  return [...root.querySelectorAll("button")].find((b) =>
    b.textContent.includes(text),
  );
}

// click clicks an element inside act.
export async function click(el) {
  await act(() => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
}

// type sets an input's value and fires input.
export async function type(el, value) {
  await act(() => {
    el.value = value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// change sets a select's or checkbox's value and fires change.
export async function change(el, value) {
  await act(() => {
    if (el.type === "checkbox") {
      el.checked = value;
    } else {
      el.value = value;
    }
    el.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

// key fires a keydown on an element.
export async function key(el, k) {
  await act(() => {
    el.dispatchEvent(
      new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }),
    );
  });
}
