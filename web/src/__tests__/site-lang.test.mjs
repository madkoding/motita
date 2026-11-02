// site/lang.js, the English / Spanish switch of the public site's front page.
//
// The script runs inside a jsdom window through vm with its real file URL as the filename, so
// the coverage run measures the file itself (Sonar reads it from web/coverage/lcov.info).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import vm from "node:vm";
import { JSDOM } from "jsdom";

const here = dirname(fileURLToPath(import.meta.url));
const file = join(here, "..", "..", "..", "site", "lang.js");
const source = readFileSync(file, "utf8");

const PAGE = `<!doctype html><html lang="en"><head>
<title>Motita</title>
<meta name="description" content="en description">
<meta property="og:title" content="en og title">
<meta property="og:description" content="en og description">
</head><body>
<button id="lang-switch" type="button">Español</button>
<h1 id="h" data-es="Hola {svg} y {svg} {svg}">Hello <svg id="a"></svg> and <svg id="b"></svg></h1>
<p id="plain" data-es="Sin iconos">No icons</p>
<img id="img" alt="a picture" data-es-alt="una imagen">
<img id="noalt" data-es-alt="sin alt">
<a id="aria" aria-label="open" data-es-aria-label="abrir" href="docs.html">Docs</a>
<a id="docs-es" href="docs-es.html">Docs</a>
<button class="copy" type="button">Copy</button>
<button class="copy done" type="button">Copied ✓</button>
</body></html>`;

// open loads the page with what the browser would hold, then runs lang.js on it.
function open({
  html = PAGE,
  saved,
  languages,
  language,
  brokenStorage = false,
} = {}) {
  const dom = new JSDOM(html, {
    url: "https://motita.example/",
    runScripts: "outside-only",
  });
  const w = dom.window;
  if (saved !== undefined) {
    w.localStorage.setItem("motita-lang", saved);
  }
  Object.defineProperty(w.navigator, "languages", {
    value: languages,
    configurable: true,
  });
  Object.defineProperty(w.navigator, "language", {
    value: language,
    configurable: true,
  });
  if (brokenStorage) {
    Object.defineProperty(w, "localStorage", {
      get() {
        throw new Error("denied");
      },
    });
  }
  vm.runInContext(source, dom.getInternalVMContext(), {
    filename: pathToFileURL(file).href,
  });
  return w;
}

const meta = (w, sel) => w.document.querySelector(sel).getAttribute("content");

test("an English browser keeps the page as written", () => {
  const w = open({ languages: ["en-GB", "es"] });
  const d = w.document;
  assert.equal(d.documentElement.lang, "en");
  assert.equal(d.title, "Motita");
  assert.equal(
    d.getElementById("h").innerHTML,
    'Hello <svg id="a"></svg> and <svg id="b"></svg>',
  );
  assert.equal(d.getElementById("img").getAttribute("alt"), "a picture");
  assert.equal(d.getElementById("img").hasAttribute("data-orig-alt"), false);
  assert.equal(meta(w, 'meta[name="description"]'), "en description");
  assert.equal(d.getElementById("aria").getAttribute("href"), "docs.html");
  assert.equal(d.getElementById("docs-es").getAttribute("href"), "docs.html");
  const sw = d.getElementById("lang-switch");
  assert.equal(sw.textContent, "Español");
  assert.equal(sw.getAttribute("lang"), "es");
  assert.equal(sw.getAttribute("aria-label"), "Leer en español");
  assert.equal(w.motitaT("Copy"), "Copy");
});

test("a Spanish browser reads the page in Spanish, icons carried over in order", () => {
  const w = open({ languages: ["fr-FR", "es-CL"] });
  const d = w.document;
  assert.equal(d.documentElement.lang, "es");
  assert.equal(d.title, "Motita — el agente de IA que demuestra que terminó");
  assert.equal(
    d.getElementById("h").innerHTML,
    'Hola <svg id="a"></svg> y <svg id="b"></svg> ',
    "a {svg} with no icon left becomes nothing",
  );
  assert.equal(d.getElementById("plain").textContent, "Sin iconos");
  assert.equal(d.getElementById("img").getAttribute("alt"), "una imagen");
  assert.equal(d.getElementById("noalt").getAttribute("alt"), "sin alt");
  assert.equal(d.getElementById("aria").getAttribute("aria-label"), "abrir");
  assert.match(meta(w, 'meta[property="og:title"]'), /^Motita — el agente/);
  assert.match(
    meta(w, 'meta[property="og:description"]'),
    /^Una verificación real/,
  );
  assert.match(meta(w, 'meta[name="description"]'), /^Dile lo que necesitas/);
  const [copy, done] = d.querySelectorAll(".copy");
  assert.equal(copy.textContent, "Copiar");
  assert.equal(
    done.textContent,
    "Copied ✓",
    "a button showing its result is left alone",
  );
  assert.equal(d.getElementById("aria").getAttribute("href"), "docs-es.html");
  assert.equal(d.getElementById("lang-switch").textContent, "English");
  assert.equal(w.motitaT("Copied ✓"), "Copiado ✓");
  assert.equal(w.motitaT("not translated"), "not translated");
});

test("the switch flips the language both ways, keeps the choice and restores the English", () => {
  const w = open({ languages: ["en"] });
  const d = w.document;
  const sw = d.getElementById("lang-switch");
  sw.click();
  assert.equal(d.documentElement.lang, "es");
  assert.equal(w.localStorage.getItem("motita-lang"), "es");
  assert.equal(d.getElementById("img").getAttribute("alt"), "una imagen");
  sw.click();
  assert.equal(d.documentElement.lang, "en");
  assert.equal(w.localStorage.getItem("motita-lang"), "en");
  assert.equal(d.title, "Motita");
  assert.equal(
    d.getElementById("h").innerHTML,
    'Hello <svg id="a"></svg> and <svg id="b"></svg>',
  );
  assert.equal(d.getElementById("img").getAttribute("alt"), "a picture");
  assert.equal(d.getElementById("noalt").getAttribute("alt"), "");
  assert.equal(meta(w, 'meta[name="description"]'), "en description");
  assert.equal(d.querySelector(".copy").textContent, "Copy");
});

test("a saved choice wins over the browser's languages; an unknown one is ignored", () => {
  assert.equal(
    open({ saved: "es", languages: ["en"] }).document.documentElement.lang,
    "es",
  );
  assert.equal(
    open({ saved: "de", languages: ["es"] }).document.documentElement.lang,
    "es",
  );
});

test("without navigator.languages the single language decides, then English", () => {
  assert.equal(open({ language: "es-MX" }).document.documentElement.lang, "es");
  assert.equal(open({}).document.documentElement.lang, "en");
  assert.equal(
    open({ languages: ["de", "pt"] }).document.documentElement.lang,
    "en",
  );
});

test("blocked storage leaves the browser's language and a click still switches", () => {
  const w = open({ languages: ["es"], brokenStorage: true });
  assert.equal(w.document.documentElement.lang, "es");
  w.document.getElementById("lang-switch").click();
  assert.equal(w.document.documentElement.lang, "en");
});

test("a page without the switch or the meta tags still translates", () => {
  const w = open({
    html: '<!doctype html><title>t</title><p data-es="hola">hello</p>',
    languages: ["es"],
  });
  assert.equal(w.document.querySelector("p").textContent, "hola");
  assert.equal(
    w.document.title,
    "Motita — el agente de IA que demuestra que terminó",
  );
});
