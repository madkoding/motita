// The colour themes (themes.css): every scheme defines the same tokens in both modes, the text and
// accent colours clear WCAG AA (4.5:1) on every surface of their palette, the picker's names are
// translated, and the script that applies the saved theme before the first paint agrees with
// theme.ts. Run with: node --test web/src/__tests__/themes.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { JSDOM } from "jsdom";

const here = dirname(fileURLToPath(import.meta.url));
const read = (...p) => readFileSync(join(here, "..", "..", ...p), "utf8");
const themes = read("src", "themes.css");
const index = read("src", "index.css");
const themeTs = read("src", "theme.ts");

const AA = 4.5;
const TOKENS = [
  "--color-accent", "--accent-rgb", "--neon-cyan", "--neon-rgb", "--s0", "--s1", "--s2", "--s3",
  "--bg-rgb", "--ov-rgb", "--t1", "--t2", "--t3", "--t4", "--c-red", "--c-green", "--c-amber",
  "--c-violet", "--c-sky", "--color-danger", "--color-warning", "--color-surface-solid",
  "--color-surface-opaque", "--veil", "--well-k", "--on-accent",
];
const SCHEMES = ["motita", "violet", "forest", "amber", "rose", "graphite"];

// palettes returns, per "mode/scheme", the declarations of its block.
function palettes() {
  const out = {};
  const re = /\/\* (\w+), (dark|light) \*\/\n:is\([^{]*\{([^}]*)\}/g;
  for (const [, scheme, mode, body] of themes.matchAll(re)) {
    out[`${mode}/${scheme}`] = Object.fromEntries(
      [...body.matchAll(/(--[\w-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]),
    );
  }
  return out;
}
const P = palettes();

function luminance(hex) {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((s) =>
    s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4,
  );
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
const contrast = (a, b) => {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
};

test("every scheme has a dark and a light palette, each defining every token", () => {
  assert.deepEqual(Object.keys(P).sort(), SCHEMES.flatMap((s) => [`dark/${s}`, `light/${s}`]).sort());
  for (const [name, decl] of Object.entries(P)) {
    assert.deepEqual(Object.keys(decl).sort(), [...TOKENS].sort(), name);
  }
  // The default, before any attribute is set, is the original dark Motita palette.
  const root = /:root \{([^}]*)\}/.exec(themes)[1];
  for (const t of TOKENS) {
    assert.ok(root.includes(`${t}:`), `:root defines ${t}`);
  }
  assert.equal(/--color-accent:\s*([^;]+);/.exec(root)[1], P["dark/motita"]["--color-accent"]);
});

test("text, signal colours and the accent clear AA on every surface of every palette", () => {
  for (const [name, d] of Object.entries(P)) {
    for (const surface of ["--s0", "--s1", "--s2", "--s3"]) {
      for (const fg of ["--t1", "--t2", "--t3", "--t4", "--c-red", "--c-green", "--c-amber", "--c-violet", "--c-sky", "--color-accent"]) {
        const ratio = contrast(d[fg], d[surface]);
        assert.ok(ratio >= AA, `${name}: ${fg} ${d[fg]} on ${surface} ${d[surface]} is ${ratio.toFixed(2)}:1`);
      }
    }
    const onAccent = contrast(d["--on-accent"], d["--color-accent"]);
    if (name !== "dark/motita") {
      assert.ok(onAccent >= AA, `${name}: text on the accent is ${onAccent.toFixed(2)}:1`);
    }
  }
});

test("light palettes are light and dark ones are dark", () => {
  for (const [name, d] of Object.entries(P)) {
    const l = luminance(d["--s1"]);
    if (name.startsWith("light/")) {
      assert.ok(l > 0.5, name);
      assert.equal(d["--ov-rgb"], "0 0 0");
    } else {
      assert.ok(l < 0.05, name);
      assert.equal(d["--ov-rgb"], "255 255 255");
    }
  }
});

test("the dark pale-text colours index.css spells out each have a light tone, and code and terminals reset them", () => {
  const used = [...new Set([...index.matchAll(/var\((--k-[0-9a-f]{6}),/g)].map((m) => m[1]))];
  assert.ok(used.length > 10);
  const light = /html\[data-mode="light"\] \{([^}]*)\}/.exec(themes)[1];
  const reset = /term-drawer\) \{([^}]*)\}/.exec(themes)[1];
  for (const k of used) {
    const line = light.split("\n").find((l) => l.trim().startsWith(`${k}:`));
    assert.ok(line, `${k} has a light tone`);
    const tone = line.split(":")[1].replace(";", "").trim();
    assert.ok(contrast(tone, "#dee1e6") >= AA, `${k} ${tone} is readable on a light surface`);
    assert.ok(reset.includes(`${k}: initial;`), `${k} goes back inside code and terminals`);
  }
});

test("the page loads theme-init.js before anything paints, and the two theme lists agree", () => {
  const html = read("index.html");
  assert.match(html, /src="\/theme-init\.js"/);
  assert.ok(
    html.indexOf("theme-init.js") < html.indexOf("/src/main.tsx"),
    "a blocking script ahead of the bundle",
  );
  const init = read("public", "theme-init.js");
  const listed = /\^\(([\w|]+)\)\$/.exec(init)[1].split("|");
  const ts = /SCHEMES: Scheme\[\] = \[([^\]]*)\]/.exec(themeTs)[1].match(/"(\w+)"/g).map((s) => s.slice(1, -1));
  assert.deepEqual(listed, ts);
  assert.deepEqual(ts, SCHEMES);
});

// run executes theme-init.js on a bare page, with what the browser would hold.
function run({ saved, prefersLight = false, broken = false }) {
  const dom = new JSDOM(
    '<!doctype html><html style="color-scheme: dark"><head><meta name="color-scheme" content="dark"><meta name="theme-color" content="#0a0a0f"></head></html>',
    { url: "http://localhost/", runScripts: "dangerously" },
  );
  const w = dom.window;
  w.matchMedia = () => ({ matches: prefersLight });
  if (saved !== undefined) {
    w.localStorage.setItem("motita.ui.theme", saved);
  }
  if (broken) {
    Object.defineProperty(w, "localStorage", { get() { throw new Error("denied"); } });
  }
  const script = w.document.createElement("script");
  script.textContent = read("public", "theme-init.js");
  w.document.head.append(script);
  const root = w.document.documentElement;
  return {
    mode: root.dataset.mode,
    scheme: root.dataset.scheme,
    scheme_hint: root.style.colorScheme,
    meta: w.document.querySelector('meta[name="color-scheme"]').getAttribute("content"),
    bar: w.document.querySelector('meta[name="theme-color"]').getAttribute("content"),
  };
}

test("theme-init.js applies the saved theme before the first paint, like theme.ts", () => {
  assert.deepEqual(run({}), { mode: "dark", scheme: "motita", scheme_hint: "dark", meta: "dark", bar: "#0a0a0f" });
  const light = run({ saved: JSON.stringify({ mode: "light", scheme: "rose" }) });
  assert.deepEqual(light, { mode: "light", scheme: "rose", scheme_hint: "light", meta: "light", bar: "#f4f6fa" });
  assert.equal(run({ saved: JSON.stringify({ mode: "dark", scheme: "amber" }), prefersLight: true }).mode, "dark");
  assert.equal(run({ saved: JSON.stringify({ mode: "system" }), prefersLight: true }).mode, "light");
  assert.equal(run({ prefersLight: true }).mode, "light", "no choice follows the system");
  assert.equal(run({ prefersLight: false }).mode, "dark");
  const odd = run({ saved: JSON.stringify({ mode: "sepia", scheme: "neon" }) });
  assert.deepEqual([odd.mode, odd.scheme], ["dark", "motita"], "unknown values fall back");
  assert.deepEqual(run({ saved: "{not json" }).scheme, "motita");
  assert.equal(run({ broken: true, prefersLight: true }).mode, "light", "storage that throws is no reason to fail");
});

test("the names the picker shows are translated", () => {
  const es = read("src", "i18n.es.ts");
  for (const key of ["Themes", "Mode", "Color scheme", "Follow the system", "Light", "Dark", "Violet", "Forest", "Amber", "Rose", "Graphite"]) {
    assert.ok(es.includes(`  ${key}: "`) || es.includes(`  "${key}": "`), key);
  }
});
