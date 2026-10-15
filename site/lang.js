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
  const KEY = "motita-lang";
  const LANGS = new Set(["en", "es"]);

  const STR = {
    es: {
      Copy: "Copiar",
      "Copied ✓": "Copiado ✓",
      "Install command copied to clipboard.":
        "Comando de instalación copiado al portapapeles.",
      "Copy failed — select the command and copy it manually.":
        "No se pudo copiar: selecciona el comando y cópialo a mano.",
    },
  };
  const META = {
    es: {
      title: "Motita — el agente de IA que demuestra que terminó",
      description:
        "Dile lo que necesitas con palabras simples: motita escribe el cambio, ejecuta tus pruebas reales, lee los fallos, los corrige y solo dice listo cuando la verificación pasa. Funciona con tu suscripción de Claude, ChatGPT o Copilot, o con cualquier API. Un binario estático, cero dependencias.",
      ogTitle: "Motita — el agente de IA que demuestra que terminó",
      ogDescription:
        "Una verificación real, no el modelo, decide cuándo una tarea está terminada. Un binario, cualquier modelo, tu terminal, tu navegador o tu teléfono.",
    },
  };
  const SWITCH_LABEL = {
    es: { text: "Español", label: "Leer en español" },
    en: { text: "English", label: "Read in English" },
  };
  const DOCS_HREF = { en: "docs.html", es: "docs-es.html" };

  let lang = "en";
  const originals = new Map();

  function stored() {
    try {
      const v = localStorage.getItem(KEY);
      if (LANGS.has(v)) {
        return v;
      }
    } catch {
      // blocked storage: there is no saved choice
    }
    return null;
  }

  function remember(v) {
    try {
      localStorage.setItem(KEY, v);
    } catch {
      // private mode or blocked storage: the choice lasts until the page closes
    }
  }

  function detect() {
    const list = navigator.languages || [navigator.language || "en"];
    for (const entry of list) {
      const code = String(entry).slice(0, 2).toLowerCase();
      if (LANGS.has(code)) {
        return code;
      }
    }
    return "en";
  }

  // T translates a string the page's script builds at run time.
  function T(en) {
    return STR[lang]?.[en] || en;
  }
  window.motitaT = T;

  const targets = {
    description: ['meta[name="description"]', "content"],
    ogTitle: ['meta[property="og:title"]', "content"],
    ogDescription: ['meta[property="og:description"]', "content"],
  };
  const enMeta = { title: document.title };
  for (const [key, [selector, attr]] of Object.entries(targets)) {
    const el = document.querySelector(selector);
    if (el) {
      enMeta[key] = el.getAttribute(attr);
    }
  }

  function swapText(el) {
    if (lang === "en" && !originals.has(el)) {
      return;
    }
    if (!originals.has(el)) {
      originals.set(el, el.innerHTML);
    }
    const en = originals.get(el);
    if (lang === "en") {
      el.innerHTML = en;
      return;
    }
    // Icons are not translated: they are carried over from the English, in order.
    const svgs = en.match(/<svg[\s\S]*?<\/svg\s*>/g) || [];
    let next = 0;
    el.innerHTML = el.dataset.es.replaceAll("{svg}", () => {
      const svg = svgs[next] || "";
      next += 1;
      return svg;
    });
  }

  function swapAttr(el, attr) {
    const key = "data-orig-" + attr;
    if (lang === "en" && !el.hasAttribute(key)) {
      return;
    }
    if (!el.hasAttribute(key)) {
      el.setAttribute(key, el.getAttribute(attr) || "");
    }
    let value = el.getAttribute(key);
    if (lang !== "en") {
      value = el.getAttribute("data-es-" + attr);
    }
    el.setAttribute(attr, value);
  }

  function applyMeta() {
    document.title = enMeta.title;
    if (lang !== "en") {
      document.title = META[lang].title;
    }
    for (const [key, [selector, attr]] of Object.entries(targets)) {
      const el = document.querySelector(selector);
      if (el) {
        let value = enMeta[key];
        if (lang !== "en") {
          value = META[lang][key];
        }
        el.setAttribute(attr, value);
      }
    }
  }

  function applySwitch() {
    const button = document.getElementById("lang-switch");
    if (!button) {
      return;
    }
    let other = "es";
    if (lang === "es") {
      other = "en";
    }
    button.textContent = SWITCH_LABEL[other].text;
    button.setAttribute("lang", other);
    button.setAttribute("aria-label", SWITCH_LABEL[other].label);
  }

  function apply(next) {
    lang = next;
    document.documentElement.lang = lang;
    for (const el of document.querySelectorAll("[data-es]")) {
      swapText(el);
    }
    for (const attr of ["alt", "aria-label"]) {
      for (const el of document.querySelectorAll("[data-es-" + attr + "]")) {
        swapAttr(el, attr);
      }
    }
    applyMeta();
    for (const b of document.querySelectorAll(".copy")) {
      if (!b.classList.contains("done")) {
        b.textContent = T("Copy");
      }
    }
    for (const a of document.querySelectorAll(
      'a[href="docs.html"], a[href="docs-es.html"]',
    )) {
      a.setAttribute("href", DOCS_HREF[lang]);
    }
    applySwitch();
  }

  document.getElementById("lang-switch")?.addEventListener("click", () => {
    let next = "es";
    if (lang === "es") {
      next = "en";
    }
    remember(next);
    apply(next);
  });
  apply(stored() || detect());
})();
