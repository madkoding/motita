// Shared setup for the cov-settings-* tests: a jsdom page installed as the globals, a module hook
// that lets node import the TSX sources themselves, and a fake api that answers from a route table
// and records what was asked.
//
// Each .tsx source is compiled on its own by esbuild (inline source map) as node loads it, instead
// of being bundled: in a bundle node's coverage cannot map a module's top-level lines back to its
// file, and differently built bundles do not merge. Compiled one by one, every file maps as a
// whole (the same hook and compile options as cov-shell-helpers.mjs). The .ts sources load
// natively, through node's type stripping.
//
// `stub(specifier, source)` replaces what the sources import under that name with a stand-in.
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
const STUB_DIR = join(here, ".cov-settings-stubs");
const stubs = new Map(); // specifier -> source
const stubUrl = (spec) =>
  pathToFileURL(join(STUB_DIR, spec.replace(/[^\w.-]/g, "_") + ".tsx")).href;

const isSource = (url) =>
  url.startsWith("file:") &&
  fileURLToPath(url).startsWith(srcDir) &&
  !fileURLToPath(url).startsWith(here + "/cov-settings-") &&
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


// load imports a source file (e.g. "Settings.tsx"), and makes preact run effects on the next turn
// of the event loop (not after a frame and a timeout), so they run the same way whether or not a
// test mocks setTimeout.
export async function load(file) {
  const { options } = await import("preact");
  options.requestAnimationFrame = (cb) => setImmediate(cb);
  return import(pathToFileURL(srcPath(file)).href);
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

export const text = (el) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();

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

