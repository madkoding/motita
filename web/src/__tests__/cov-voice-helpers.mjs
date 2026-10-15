// Shared setup of the voice group's coverage tests: a jsdom page for the globals a component
// needs, and module hooks that let node import the TS/TSX sources one by one (see useSourceHooks).
import { registerHooks } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { existsSync, readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { transformSync } from "esbuild";

export const here = dirname(fileURLToPath(import.meta.url));
export const srcDir = join(here, "..");

// setupDom installs a jsdom window's globals on globalThis and returns the JSDOM.
export function setupDom(url = "https://localhost/") {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url });
  for (const k of [
    "document",
    "window",
    "Event",
    "CustomEvent",
    "KeyboardEvent",
    "MouseEvent",
    "HTMLElement",
    "HTMLInputElement",
    "HTMLSelectElement",
    "Element",
    "Node",
    "NodeFilter",
    "Range",
    "localStorage",
  ]) {
    globalThis[k] = dom.window[k];
  }
  Object.defineProperty(globalThis, "navigator", {
    value: dom.window.navigator,
    configurable: true,
    writable: true,
  });
  globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id);
  return dom;
}

// settle lets pending promises and preact's re-renders run.
export async function settle(act, rounds = 3) {
  for (let i = 0; i < rounds; i++) {
    await act(() => new Promise((r) => setImmediate(r)));
  }
}

export const json = (body, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

// SOURCES ONE BY ONE. useSourceHooks lets node import the sources themselves: a .ts file as it is
// (node strips its types), a .tsx compiled on its own by esbuild with an inline source map, and an
// import without an extension resolved as the bundler would. Unlike a bundle, every file then maps
// as a whole, its header and top-level lines included. The compilation is the same as
// cov-shell-helpers.mjs's, so the coverage of a file loaded both ways adds up.
// packages maps a package specifier to a stand-in file (an absolute path).
let hooked = false;
export function useSourceHooks(packages = {}) {
  if (hooked) {
    return;
  }
  hooked = true;
  const isSource = (url) =>
    url.startsWith("file:") &&
    fileURLToPath(url).startsWith(srcDir) &&
    !fileURLToPath(url).startsWith(here) &&
    /\.(tsx|css)$/.test(url);
  registerHooks({
    resolve(specifier, context, nextResolve) {
      if (packages[specifier]) {
        return { url: pathToFileURL(packages[specifier]).href, shortCircuit: true };
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
      if (isSource(url)) {
        if (url.endsWith(".css")) {
          return { format: "module", source: "export {};", shortCircuit: true };
        }
        const file = fileURLToPath(url);
        return { format: "module", source: compile(readFileSync(file, "utf8"), url), shortCircuit: true };
      }
      return nextLoad(url, context);
    },
  });
}

// source imports a source file (e.g. "Subtitles.tsx") through the hooks.
export function source(file) {
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
  // mapped to its last: node credits a module's top-level lines from where its start maps to
  // where its end maps.
  const genCode = "\n" + out.code;
  const genLines = genCode.split("\n").length;
  const origLast = code.split(/(?<=\n)/).length - 1;
  map.mappings = spanWholeFile(map.mappings, genLines, origLast);
  const b64 = Buffer.from(JSON.stringify(map)).toString("base64");
  return genCode + "\n//# sourceMappingURL=data:application/json;base64," + b64 + "\n";
}

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

