// Shared setup for the chat group's tests: a jsdom window as the globals, the module hook of
// tsx-hooks.mjs that lets node import the TSX sources one by one, a hook harness, and a manual
// animation-frame queue so scrolling code can be stepped frame by frame.
import { JSDOM } from "jsdom";
import { load } from "./tsx-hooks.mjs";
import { jsonResponse } from "./dom-helpers.mjs";

export { load };
export { text } from "./dom-helpers.mjs";

export const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
  pretendToBeVisual: true,
});
const w = dom.window;
for (const k of [
  "window",
  "document",
  "history",
  "localStorage",
  "HTMLElement",
  "HTMLTextAreaElement",
  "Element",
  "Node",
  "Event",
  "KeyboardEvent",
  "MouseEvent",
  "FocusEvent",
  "InputEvent",
  "CustomEvent",
  "MutationObserver",
  "SVGElement",
  "DocumentFragment",
  "Text",
  "NodeFilter",
]) {
  globalThis[k] = k === "window" ? w : w[k];
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

// The frame queue: requestAnimationFrame only queues, and flushFrames runs what is queued.
let frameSeq = 0;
const frameQueue = new Map();
export const frames = {
  get pending() {
    return frameQueue.size;
  },
  cancelled: [],
  flush(now = performance.now()) {
    const due = [...frameQueue.entries()];
    frameQueue.clear();
    for (const [, cb] of due) {
      cb(now);
    }
    return due.length;
  },
  clear() {
    frameQueue.clear();
  },
};
globalThis.requestAnimationFrame = (cb) => {
  frameSeq += 1;
  frameQueue.set(frameSeq, cb);
  return frameSeq;
};
globalThis.cancelAnimationFrame = (id) => {
  frames.cancelled.push(id);
  frameQueue.delete(id);
};
w.requestAnimationFrame = globalThis.requestAnimationFrame;
w.cancelAnimationFrame = globalThis.cancelAnimationFrame;

// copies loads the named sources, one module per file, and returns them as the one form to test.
export async function copies(_tag, lines) {
  const own = {};
  for (const [, file] of lines) {
    Object.assign(own, await load(file));
  }
  own.form = "module";
  return [own];
}

export const { h, render, Fragment } = await import("preact");
export const { act } = await import("preact/test-utils");

// renderHook mounts a component that calls `hook(props)` and keeps its latest result in
// `result.current`. rerender(props) renders it again with new props.
export async function renderHook(hook, props = {}) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const result = { current: undefined, renders: 0 };
  function H(p) {
    result.current = hook(p);
    result.renders += 1;
    return null;
  }
  const api = {
    result,
    root,
    async rerender(next = props) {
      props = next;
      await act(() => render(h(H, props), root));
    },
    async unmount() {
      await act(() => render(null, root));
      root.remove();
    },
  };
  await act(() => render(h(H, props), root));
  return api;
}

// mount renders a vnode in a fresh container.
export async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(vnode, root));
  return {
    root,
    async update(next) {
      await act(() => render(next, root));
    },
    async unmount() {
      await act(() => render(null, root));
      root.remove();
    },
  };
}

export const tick = (ms = 0) => new Promise((r) => setTimeout(r, ms));

// json is a fetch Response with a JSON body.
export const json = jsonResponse;

// fakeApi answers requests from a table of handlers: route(path, init) returns a Response, throws,
// or returns undefined (404). Every call is recorded.
export function fakeApi(route) {
  const calls = [];
  const api = async (path, init) => {
    calls.push({
      path,
      init,
      body: init?.body ? JSON.parse(init.body) : undefined,
    });
    const r = await route(path, init);
    return r ?? json({ error: "not found" }, 404);
  };
  api.calls = calls;
  return api;
}

// fakeRunState is the part of useRunState the hooks read, with state kept in plain fields and
// every setter accepting a value or an updater.
export function fakeRunState(init = {}) {
  let id = 0;
  const rs = {
    messages: [],
    stepsByTurn: {},
    cpInfo: {},
    shellLog: [],
    termSeenId: 0,
    agents: [],
    approval: null,
    approvalSel: 0,
    sessionId: "",
    noSession: false,
    cleared: 0,
    sessionRef: { current: "" },
    runningRef: { current: false },
    liveTurnRef: { current: null },
    lastIdRef: { current: 0 },
    runIdRef: { current: "" },
    runSessionRef: { current: "" },
    approvalRef: { current: null },
    nextId: () => ++id,
    clearConversation() {
      rs.cleared += 1;
      rs.messages = [];
    },
    ...init,
  };
  const setter = (field) => (v) => {
    rs[field] = typeof v === "function" ? v(rs[field]) : v;
  };
  for (const f of [
    "messages",
    "stepsByTurn",
    "cpInfo",
    "shellLog",
    "termSeenId",
    "agents",
    "approval",
    "approvalSel",
    "sessionId",
    "noSession",
  ]) {
    const name = "set" + f[0].toUpperCase() + f.slice(1);
    if (!(name in init)) {
      rs[name] = setter(f);
    }
  }
  return rs;
}
