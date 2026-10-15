// Shared setup of the shell group's tests: a jsdom page with the browser APIs jsdom lacks, and a
// module hook that lets node import the TSX sources themselves.
//
// Each source is compiled on its own by esbuild (TSX to JS, inline source map) as node loads it,
// instead of being bundled: a bundle mixes every module into one script, and node's coverage then
// cannot map a module's top-level lines back to its file (a header comment reads as uncovered).
// Compiled one by one, every file maps as a whole.
//
// `stub(specifier, source)` replaces what the sources import under that name with a small stand-in,
// so a component can be tested without the whole app under it.
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
const STUB_DIR = join(here, ".cov-shell-stubs");
const stubs = new Map(); // specifier -> source
const stubUrl = (spec) =>
  pathToFileURL(join(STUB_DIR, spec.replace(/[^\w.-]/g, "_") + ".tsx")).href;

const isSource = (url) =>
  url.startsWith("file:") &&
  fileURLToPath(url).startsWith(srcDir) &&
  !fileURLToPath(url).startsWith(here + "/cov-shell-") &&
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

// load imports a source file (e.g. "App.tsx").
export function load(file) {
  return import(pathToFileURL(srcPath(file)).href);
}

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

export const text = (el) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();

// flush lets pending promises and zero-delay timers run.
export async function flush(n = 3) {
  for (let i = 0; i < n; i++) {
    await new Promise((r) => setTimeout(r, 0));
  }
}

// jsonResponse is a minimal fetch Response.
export function jsonResponse(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
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
