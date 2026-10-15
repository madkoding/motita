// Applies the saved colour theme before the first paint, so a page set to light never flashes dark
// (and the other way round). A blocking script from the same origin: the page's CSP allows no
// inline script. The same rules as src/theme.ts, which keeps it up to date afterwards.
(function () {
  var mode = "system";
  var scheme = "motita";
  try {
    var raw = JSON.parse(localStorage.getItem("motita.ui.theme") || "{}");
    if (raw.mode === "light" || raw.mode === "dark") {
      mode = raw.mode;
    }
    if (/^(motita|violet|forest|amber|rose|graphite)$/.test(raw.scheme)) {
      scheme = raw.scheme;
    }
  } catch (e) {
    /* the defaults */
  }
  if (mode === "system") {
    mode = matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
  }
  var root = document.documentElement;
  root.dataset.mode = mode;
  root.dataset.scheme = scheme;
  root.style.colorScheme = mode;
  var bar = document.querySelector('meta[name="theme-color"]');
  if (bar) {
    bar.setAttribute("content", mode === "light" ? "#f4f6fa" : "#0a0a0f");
  }
  var cs = document.querySelector('meta[name="color-scheme"]');
  if (cs) {
    cs.setAttribute("content", mode);
  }
})();
