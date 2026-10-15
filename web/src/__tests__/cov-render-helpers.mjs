// Shared setup for the cov-render-*.test.mjs files: a jsdom page wired into the globals the
// components read, and a module hook that lets node import the TSX sources themselves.
//
// The hook is the one in cov-shell-helpers.mjs, copied with the same esbuild options so every
// group's tests compile a source to the same script and node merges their coverage: each .tsx is
// compiled on its own (never bundled), its source map spanning the whole file; a .ts file loads
// natively through type stripping.
//
// `overrides` (given to setupDom, before the first import) replaces what the sources import under
// a specifier: { mermaid: <source of a fake> } serves a fake, and the value FAIL makes the import
// fail, the way a lazily loaded chunk that cannot be fetched does.
import { registerHooks } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { existsSync, readFileSync } from "node:fs";
import { transformSync } from "esbuild";
import { JSDOM } from "jsdom";

export const here = dirname(fileURLToPath(import.meta.url));
const srcDir = join(here, "..");
export const srcPath = (f) => join(srcDir, f);
export const FAIL = 'throw new Error("the module could not be loaded");';
// Stand-ins are virtual modules under __tests__, which the coverage report leaves out.
const STUB_DIR = join(here, ".cov-render-stubs");
const stubs = new Map(); // specifier -> source
const stubUrl = (spec) =>
  pathToFileURL(join(STUB_DIR, spec.replace(/[^\w.-]/g, "_") + ".tsx")).href;

const isSource = (url) =>
  url.startsWith("file:") &&
  fileURLToPath(url).startsWith(srcDir) &&
  !fileURLToPath(url).startsWith(here + "/cov-render-") &&
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

// setupDom installs a jsdom page as the globals the source reads, and the module loader.
export function setupDom({ overrides = {}, url = "http://localhost/" } = {}) {
  for (const [spec, source] of Object.entries(overrides)) {
    stubs.set(spec, source);
  }
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
    "Element",
    "Node",
    "NodeFilter",
    "Text",
    "DocumentFragment",
    "localStorage",
  ]) {
    globalThis[k] = w[k];
  }
  Object.defineProperty(globalThis, "navigator", {
    value: w.navigator,
    configurable: true,
  });
  w.matchMedia = () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
  });
  return dom;
}

// load imports one source file (its name under src/) as its own module.
export const load = (file) => import(pathToFileURL(srcPath(file)).href);

// flush lets pending promises and zero-delay timers run.
export const flush = (n = 5) =>
  new Promise((resolve) => {
    let i = 0;
    const tick = () => (++i >= n ? resolve() : setTimeout(tick, 0));
    setTimeout(tick, 0);
  });

export const text = (el) => el.textContent.replace(/\s+/g, " ").trim();

// waitFor polls `cond` until it holds (a lazily imported module may take a while to compile).
export async function waitFor(cond, what = "condition", ms = 5000) {
  const end = Date.now() + ms;
  while (!cond()) {
    if (Date.now() > end) {
      throw new Error(`timed out waiting for ${what}`);
    }
    await new Promise((r) => setTimeout(r, 5));
  }
}
