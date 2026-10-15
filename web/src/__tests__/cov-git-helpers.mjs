// Shared setup for the cov-git-* tests: a jsdom page installed as the globals, a loader for the
// TSX components (through the module hook of tsx-hooks.mjs), and a fake api that answers from a
// route table and records what was asked.
import { JSDOM } from "jsdom";
import { load } from "./tsx-hooks.mjs";
import { flush as flushTimers } from "./dom-helpers.mjs";

export { text } from "./dom-helpers.mjs";

export function installDom() {
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
  globalThis.requestAnimationFrame = (cb) =>
    setTimeout(() => cb(Date.now()), 0);
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id);
  return dom;
}

// loadSources imports the named exports ({ name: "File.tsx" } relative to src). The sources are
// not bundled: tsx-hooks.mjs registers a hook that compiles each .tsx on its own (the .ts
// files load natively), so node's coverage maps every module back to its own file.
export async function loadSources(exports) {
  const out = {};
  for (const [name, file] of Object.entries(exports)) {
    out[name] = (await load(file))[name];
  }
  return out;
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
    calls.push({
      method,
      path,
      body: init.body ? JSON.parse(init.body) : undefined,
    });
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

// flush lets one turn of zero-delay timers run.
export const flush = () => flushTimers(1);
