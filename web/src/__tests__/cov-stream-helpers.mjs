// Shared set-up for the cov-stream-* tests: a jsdom window installed as the globals the web UI
// reads, and a module hook that lets node import the sources one module at a time.
//
// The sources import each other without an extension ("./i18n"), so a relative import is
// resolved to the .ts or .tsx file beside it. A .ts file is loaded by node itself (it strips the
// types); a .tsx file is compiled on its own by esbuild with an inline source map (compile() is
// the same as in cov-shell-helpers.mjs, so every group's coverage of a file maps identically);
// a stylesheet is empty. Nothing is bundled, so coverage is reported for exactly each file.
import { fileURLToPath, pathToFileURL } from "node:url";
import { registerHooks } from "node:module";
import { dirname, join } from "node:path";
import { existsSync, readFileSync } from "node:fs";
import { transformSync } from "esbuild";
import { JSDOM } from "jsdom";

export const here = dirname(fileURLToPath(import.meta.url));
export const srcDir = join(here, "..");

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

// Source files whose loading fails, the way a chunk that could not be fetched does.
let unreachable = [];
let registered = false;

// Stand-ins for modules the sources import, by specifier (e.g. "./useVoiceConversation"). They
// live under __tests__, which the coverage report leaves out.
const STUB_DIR = join(here, ".cov-stream-stubs");
const stubs = new Map();
const stubUrl = (spec) =>
  pathToFileURL(join(STUB_DIR, spec.replace(/[^\w.-]/g, "_") + ".ts")).href;

// stub makes the sources import `code` (TypeScript-free JS) where they import `specifier`.
export function stub(specifier, code) {
  stubs.set(specifier, code);
}

const isSource = (url) =>
  url.startsWith("file:") &&
  fileURLToPath(url).startsWith(srcDir) &&
  !fileURLToPath(url).startsWith(here) &&
  /\.(tsx|css)$/.test(url);

// useSources installs the module hooks. `unreachable` names source files (e.g. "i18n.es.ts")
// whose loading fails.
export function useSources(opts = {}) {
  unreachable = opts.unreachable || unreachable;
  if (registered) {
    return;
  }
  registered = true;
  registerHooks({
    resolve(specifier, context, nextResolve) {
      if (stubs.has(specifier) && context.parentURL?.startsWith(pathToFileURL(srcDir).href)) {
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
      if (unreachable.some((name) => url.endsWith("/" + name))) {
        throw new Error("Failed to fetch dynamically imported module: " + url);
      }
      const spec = [...stubs.keys()].find((k) => stubUrl(k) === url);
      if (spec) {
        return { format: "module", source: stubs.get(spec), shortCircuit: true };
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
}

// source imports one file of src/ (for example "appFormat.ts") as its own module.
export async function source(file) {
  useSources();
  return import(pathToFileURL(join(srcDir, file)).href);
}

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

// flush lets pending promises and preact's effects run.
export async function flush(times = 5) {
  for (let i = 0; i < times; i++) {
    await new Promise((r) => setTimeout(r, 0));
  }
}

export const text = (el) => (el?.textContent || "").replace(/\s+/g, " ").trim();

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
