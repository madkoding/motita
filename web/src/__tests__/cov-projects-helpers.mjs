// Shared setup for the cov-projects-* tests: a jsdom window, a module hook that lets node import
// the TSX sources (with the app store replaced by a bare context, so a test hands each component
// exactly the store it reads), a fake gateway answer and a mount helper.
// The hook below is the shell group's (cov-shell-helpers.mjs), with the same compile options:
// each source is compiled on its own by esbuild as node loads it (.ts loads natively), so every
// file maps as a whole and coverage merges exactly across test processes.
import { registerHooks } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { existsSync, readFileSync } from "node:fs";
import { transformSync } from "esbuild";
import { JSDOM } from "jsdom";

export const here = dirname(fileURLToPath(import.meta.url));
const srcDir = join(here, "..");
export const srcPath = (f) => join(srcDir, f);
// Stand-ins live under __tests__, which the coverage report leaves out.
const STUB_DIR = join(here, ".cov-projects-stubs");
const stubs = new Map(); // specifier -> source
const stubUrl = (spec) =>
  pathToFileURL(join(STUB_DIR, spec.replace(/[^\w.-]/g, "_") + ".tsx")).href;

const isSource = (url) =>
  url.startsWith("file:") &&
  fileURLToPath(url).startsWith(srcDir) &&
  !fileURLToPath(url).startsWith(here + "/cov-projects-") &&
  /\.(tsx|css)$/.test(url);

registerHooks({
  resolve(specifier, context, nextResolve) {
    const fromSource =
      context.parentURL?.startsWith("file:") &&
      fileURLToPath(context.parentURL).startsWith(srcDir) &&
      !fileURLToPath(context.parentURL).startsWith(STUB_DIR);
    if (specifier.startsWith(pathToFileURL(STUB_DIR).href)) {
      return { url: specifier, shortCircuit: true };
    }
    if (fromSource && stubs.has(specifier)) {
      return { url: stubUrl(specifier), shortCircuit: true };
    }
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
    if (url.startsWith(pathToFileURL(STUB_DIR).href)) {
      const spec = [...stubs.keys()].find((s) => stubUrl(s) === url);
      return { format: "module", source: compile(stubs.get(spec), url), shortCircuit: true };
    }
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

// stub replaces a module the sources import (by its specifier, e.g. "./Sidebar") with `source`.
export function stub(specifier, source) {
  stubs.set(specifier, source);
}

// stubModule imports the stand-in registered for `specifier`, the same instance the sources get.
export function stubModule(specifier) {
  return import(stubUrl(specifier));
}


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
// through the hook above, plus the stand-in store's AppCtx.
export async function bundle(exportsByFile) {
  const mods = { AppCtx: (await stubModule("./appStore")).AppCtx };
  for (const [file, names] of Object.entries(exportsByFile)) {
    const m = await import(pathToFileURL(srcPath(file)).href);
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
    q: (sel) => root.querySelector(sel),
    qa: (sel) => [...root.querySelectorAll(sel)],
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
