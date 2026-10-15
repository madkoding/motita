// Shared setup for the chat group's tests: a jsdom window as the globals, a module hook that lets
// node import the TSX sources one by one, a hook harness, and a manual animation-frame queue so
// scrolling code can be stepped frame by frame.
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { existsSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { JSDOM } from "jsdom";
import { transformSync } from "esbuild";

export const here = dirname(fileURLToPath(import.meta.url));
export const srcDir = join(here, "..");

// The compile hook, copied from cov-shell-helpers.mjs with the same esbuild options, so every
// group's tests load a source in the same form and node can merge their coverage: each .tsx file
// is compiled on its own (never bundled) with a source map spanning the whole file, and a .ts file
// is left to node, which strips its types in place.
const isSource = (url) =>
  url.startsWith("file:") &&
  fileURLToPath(url).startsWith(srcDir) &&
  !fileURLToPath(url).startsWith(here + "/cov-chat-") &&
  /\.(tsx|css)$/.test(url);

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (
      (specifier.startsWith("./") || specifier.startsWith("../")) &&
      context.parentURL?.startsWith("file:") &&
      !/\.(m?js|tsx?|css|json)$/.test(specifier)
    ) {
      const base = fileURLToPath(new URL(specifier, context.parentURL));
      for (const ext of [".ts", ".tsx"]) {
        if (existsSync(base + ext)) {
          return { url: pathToFileURL(base + ext).href, shortCircuit: true };
        }
      }
    }
    return nextResolve(specifier, context);
  },
  load(url, context, nextLoad) {
    if (isSource(url)) {
      if (url.endsWith(".css")) {
        return { format: "module", source: "export {};", shortCircuit: true };
      }
      const file = fileURLToPath(url);
      return {
        format: "module",
        source: compile(readFileSync(file, "utf8"), url),
        shortCircuit: true,
      };
    }
    return nextLoad(url, context);
  },
});

function compile(code, url) {
  const file = fileURLToPath(url);
  const out = transformSync(code, {
    loader: file.endsWith(".tsx") ? "tsx" : "ts",
    format: "esm",
    jsx: "automatic",
    jsxImportSource: "preact",
    sourcemap: "external",
    sourcefile: file,
    target: "es2022",
  });
  const map = JSON.parse(out.map);
  // One empty line on top, mapped to the file's first line; the source map comment at the bottom,
  // mapped to its last.
  const genCode = "\n" + out.code;
  const genLines = genCode.split("\n").length;
  const origLast = code.split(/(?<=\n)/).length - 1;
  map.mappings = spanWholeFile(map.mappings, genLines, origLast);
  const b64 = Buffer.from(JSON.stringify(map)).toString("base64");
  return genCode + "\n//# sourceMappingURL=data:application/json;base64," + b64 + "\n";
}

// spanWholeFile makes the start of the module map to the first line of its file and its end (the
// source map comment line) to the last one. Node's coverage credits a module's top-level lines
// from where its start maps to where its end maps; without this the header comment above the
// first import, and the type declarations after the last statement, read as never run.
function spanWholeFile(mappings, genLines, origLast) {
  const lines = decodeMappings(mappings);
  lines.unshift([[0, 0, 0, 0]]);
  while (lines.length < genLines + 1) {
    lines.push([]);
  }
  lines[genLines].push([0, 0, origLast, 0]);
  return encodeMappings(lines);
}

const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

function decodeVlq(str) {
  const out = [];
  let value = 0;
  let shift = 0;
  for (const ch of str) {
    const d = B64.indexOf(ch);
    value += (d & 31) << shift;
    if (d & 32) {
      shift += 5;
    } else {
      out.push(value & 1 ? -(value >>> 1) : value >>> 1);
      value = 0;
      shift = 0;
    }
  }
  return out;
}

function encodeVlq(n) {
  let v = n < 0 ? (-n << 1) | 1 : n << 1;
  let s = "";
  do {
    let d = v & 31;
    v >>>= 5;
    if (v) {
      d |= 32;
    }
    s += B64[d];
  } while (v);
  return s;
}

// Absolute segments [genCol, src, origLine, origCol], one array per generated line.
function decodeMappings(mappings) {
  let src = 0;
  let line = 0;
  let col = 0;
  return mappings.split(";").map((l) => {
    let gen = 0;
    return l
      .split(",")
      .filter(Boolean)
      .map((seg) => {
        const v = decodeVlq(seg);
        gen += v[0];
        if (v.length < 4) {
          return [gen];
        }
        src += v[1];
        line += v[2];
        col += v[3];
        return [gen, src, line, col];
      });
  });
}

function encodeMappings(lines) {
  let src = 0;
  let line = 0;
  let col = 0;
  return lines
    .map((segs) => {
      let gen = 0;
      return segs
        .map((s) => {
          let r = encodeVlq(s[0] - gen);
          gen = s[0];
          if (s.length === 4) {
            r += encodeVlq(s[1] - src) + encodeVlq(s[2] - line) + encodeVlq(s[3] - col);
            [src, line, col] = [s[1], s[2], s[3]];
          }
          return r;
        })
        .join(",");
    })
    .join(";");
}

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

// load imports one source file (by its name under src/) as its own module.
export const load = (file) => import(pathToFileURL(join(srcDir, file)).href);

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

export const text = (el) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();
export const tick = (ms = 0) => new Promise((r) => setTimeout(r, ms));

// json is a fetch Response with a JSON body.
export const json = (body, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

// fakeApi answers requests from a table of handlers: route(path, init) returns a Response, throws,
// or returns undefined (404). Every call is recorded.
export function fakeApi(route) {
  const calls = [];
  const api = async (path, init) => {
    calls.push({ path, init, body: init?.body ? JSON.parse(init.body) : undefined });
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
