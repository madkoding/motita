// The module hook the tests load the web UI's sources through: node imports each TSX file itself,
// compiled on its own as it is loaded, so a test drives the real module graph of the app.
//
// Each .tsx is compiled alone by esbuild (TSX to JS, inline source map) instead of being bundled:
// node's coverage merges what the test processes report only where a function maps to the same
// range of its file, and a bundle mixes every module into one script, so its lines neither merge
// with what other tests cover nor map the module's top level back to its file (a header comment
// reads as uncovered). The compile options and spanWholeFile below are those of the cov-*-helpers
// hooks, byte for byte, so the ranges every test reports agree and merge. Node loads .ts files
// itself (type stripping), and a .css import becomes an empty module.
//
// Extensionless relative imports resolve to .ts before .tsx, as esbuild and Vite try them, so
// "./settings" is settings.ts and not Settings.tsx on a case-sensitive filesystem.
//
// One module graph is shared per test file (node runs each in its own process), so a context such
// as AppCtx is the same object for every module that imports it, as it is in the app.
//
// `stub(specifier, source)` replaces what the sources import under that name with a small stand-in,
// so a component can be tested without the whole app under it.
import { registerHooks } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { existsSync, readFileSync } from "node:fs";
import { transformSync } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
const srcDir = join(here, "..");
export const srcPath = (f) => join(srcDir, f);
// Stand-ins get URLs under __tests__, which the coverage report leaves out (no file is written).
const STUB_DIR = join(here, ".tsx-hooks-stubs");
const stubs = new Map(); // specifier -> source
const stubUrl = (spec) =>
  pathToFileURL(join(STUB_DIR, spec.replace(/[^\w.-]/g, "_") + ".tsx")).href;

const isSource = (url) =>
  url.startsWith("file:") &&
  fileURLToPath(url).startsWith(srcDir) &&
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
// It must be called before the first module that imports it is loaded.
export function stub(specifier, source) {
  stubs.set(specifier, source);
}

// stubModule imports the stand-in registered for `specifier`, the same instance the sources get.
export function stubModule(specifier) {
  return import(stubUrl(specifier));
}

// load imports a source file by its path under src (e.g. "Settings.tsx").
export function load(file) {
  return import(pathToFileURL(srcPath(file)).href);
}
