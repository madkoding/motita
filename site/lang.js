// English / Spanish for site/index.html.
//
// The English is the page itself. Each translatable element carries its Spanish
// in a data-es attribute (alt and aria-label in data-es-alt and data-es-aria-label),
// so a copy edit and its translation sit side by side, and
// scripts/index_units.py fails the check when a text element has none.
//
// Which language: the visitor's choice (kept in this browser only) wins, then the
// first of the browser's languages that is English or Spanish; English otherwise.
// Without scripts, or if this file does not load, the page is simply English.
(function () {
  "use strict";
  var KEY = "motita-lang";
  var LANGS = ["en", "es"];

  var STR = {
    es: {
      Copy: "Copiar",
      "Copied ✓": "Copiado ✓",
      "Install command copied to clipboard.":
        "Comando de instalación copiado al portapapeles.",
      "Copy failed — select the command and copy it manually.":
        "No se pudo copiar: selecciona el comando y cópialo a mano.",
    },
  };
  var META = {
    es: {
      title: "Motita — el agente de IA que demuestra que terminó",
      description:
        "Dile lo que necesitas con palabras simples: motita escribe el cambio, ejecuta tus pruebas reales, lee los fallos, los corrige y solo dice listo cuando la verificación pasa. Funciona con tu suscripción de Claude, ChatGPT o Copilot, o con cualquier API. Un binario estático, cero dependencias.",
      ogTitle: "Motita — el agente de IA que demuestra que terminó",
      ogDescription:
        "Una verificación real, no el modelo, decide cuándo una tarea está terminada. Un binario, cualquier modelo, tu terminal, tu navegador o tu teléfono.",
    },
  };

  var lang = "en";
  var originals = new Map();

  function stored() {
    try {
      var v = localStorage.getItem(KEY);
      return LANGS.indexOf(v) >= 0 ? v : null;
    } catch (e) {
      return null;
    }
  }
  function remember(v) {
    try {
      localStorage.setItem(KEY, v);
    } catch (e) {
      // private mode or blocked storage: the choice lasts until the page closes
    }
  }
  function detect() {
    var list = navigator.languages || [navigator.language || "en"];
    for (var i = 0; i < list.length; i++) {
      var code = String(list[i]).slice(0, 2).toLowerCase();
      if (LANGS.indexOf(code) >= 0) {
        return code;
      }
    }
    return "en";
  }

  // T translates a string the page's script builds at run time.
  function T(en) {
    return (STR[lang] && STR[lang][en]) || en;
  }
  window.motitaT = T;

  function meta(sel, attr) {
    var el = document.querySelector(sel);
    return el ? { el: el, attr: attr } : null;
  }
  var targets = {
    description: meta('meta[name="description"]', "content"),
    ogTitle: meta('meta[property="og:title"]', "content"),
    ogDescription: meta('meta[property="og:description"]', "content"),
  };
  var enMeta = { title: document.title };
  Object.keys(targets).forEach(function (k) {
    if (targets[k]) {
      enMeta[k] = targets[k].el.getAttribute(targets[k].attr);
    }
  });

  function swapText(el) {
    if (lang === "en" && !originals.has(el)) {
      return;
    }
    if (!originals.has(el)) {
      originals.set(el, el.innerHTML);
    }
    var en = originals.get(el);
    if (lang === "en") {
      el.innerHTML = en;
      return;
    }
    // Icons are not translated: they are carried over from the English, in order.
    var svgs = en.match(/<svg[\s\S]*?<\/svg\s*>/g) || [];
    var i = 0;
    el.innerHTML = el.getAttribute("data-es").replace(/\{svg\}/g, function () {
      return svgs[i++] || "";
    });
  }

  function swapAttr(el, attr) {
    var key = "data-orig-" + attr;
    if (lang === "en" && !el.hasAttribute(key)) {
      return;
    }
    if (!el.hasAttribute(key)) {
      el.setAttribute(key, el.getAttribute(attr) || "");
    }
    el.setAttribute(
      attr,
      lang === "en" ? el.getAttribute(key) : el.getAttribute("data-es-" + attr),
    );
  }

  function apply(next) {
    lang = next;
    document.documentElement.lang = lang;
    document.querySelectorAll("[data-es]").forEach(swapText);
    ["alt", "aria-label"].forEach(function (attr) {
      document.querySelectorAll("[data-es-" + attr + "]").forEach(function (el) {
        swapAttr(el, attr);
      });
    });
    var m = META[lang] || enMeta;
    document.title = lang === "en" ? enMeta.title : m.title;
    Object.keys(targets).forEach(function (k) {
      if (targets[k]) {
        targets[k].el.setAttribute(
          targets[k].attr,
          lang === "en" ? enMeta[k] : m[k],
        );
      }
    });
    document.querySelectorAll(".copy").forEach(function (b) {
      if (!b.classList.contains("done")) {
        b.textContent = T("Copy");
      }
    });
    document.querySelectorAll('a[href="docs.html"], a[href="docs-es.html"]').forEach(function (a) {
      a.setAttribute("href", lang === "es" ? "docs-es.html" : "docs.html");
    });
    var sw = document.getElementById("lang-switch");
    if (sw) {
      var other = lang === "es" ? "en" : "es";
      sw.textContent = other === "es" ? "Español" : "English";
      sw.setAttribute("lang", other);
      sw.setAttribute(
        "aria-label",
        other === "es" ? "Leer en español" : "Read in English",
      );
    }
  }

  var sw = document.getElementById("lang-switch");
  if (sw) {
    sw.addEventListener("click", function () {
      var next = lang === "es" ? "en" : "es";
      remember(next);
      apply(next);
    });
  }
  var first = stored() || detect();
  apply(first);
})();
