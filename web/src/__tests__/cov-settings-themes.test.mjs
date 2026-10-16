// The Themes tab and the theme module behind it: the saved choice, how the mode is resolved from the
// system, what lands on <html> and in the browser's meta tags, and the picker with its scheme cards.
// Run with: node --test web/src/__tests__/cov-settings-themes.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, load, text, settle } from "./cov-settings-helpers.mjs";

installDom();
const root = document.documentElement;
for (const [name, content] of [
  ["color-scheme", "dark"],
  ["theme-color", "#0a0a0f"],
]) {
  const m = document.createElement("meta");
  m.setAttribute("name", name);
  m.setAttribute("content", content);
  document.head.append(m);
}
const T = await load("theme.ts");
const { ThemeSettings } = await load("ThemeSettings.tsx");
const { h, render } = await import("preact");
const meta = (name) =>
  document.querySelector(`meta[name="${name}"]`).getAttribute("content");

// A matchMedia whose answer, and whose listeners, the tests drive.
function fakeSystem(light) {
  const state = { light, listeners: new Set() };
  const mm = () => ({
    get matches() {
      return state.light;
    },
    addEventListener: (_, f) => state.listeners.add(f),
    removeEventListener: (_, f) => state.listeners.delete(f),
  });
  globalThis.matchMedia = mm;
  window.matchMedia = mm;
  state.flip = (to) => {
    state.light = to;
    state.listeners.forEach((f) => f());
  };
  return state;
}

test("the default theme follows the system with the Motita scheme; a saved choice is read back, bad values are not", () => {
  localStorage.clear();
  assert.deepEqual(T.loadTheme(), { mode: "system", scheme: "motita" });
  localStorage.setItem(
    T.THEME_KEY,
    JSON.stringify({ mode: "light", scheme: "rose" }),
  );
  assert.deepEqual(T.loadTheme(), { mode: "light", scheme: "rose" });
  localStorage.setItem(
    T.THEME_KEY,
    JSON.stringify({ mode: "sepia", scheme: "neon" }),
  );
  assert.deepEqual(T.loadTheme(), T.DEFAULT_THEME);
  localStorage.setItem(T.THEME_KEY, "{not json");
  assert.deepEqual(T.loadTheme(), T.DEFAULT_THEME);
  assert.notEqual(
    T.loadTheme(),
    T.DEFAULT_THEME,
    "a copy, so nobody edits the default",
  );
});

test("the mode that is drawn: system follows the page's preference, light and dark are themselves", () => {
  assert.equal(T.resolveMode("system", true), "light");
  assert.equal(T.resolveMode("system", false), "dark");
  assert.equal(T.resolveMode("light", false), "light");
  assert.equal(T.resolveMode("dark", true), "dark");
  fakeSystem(true);
  assert.equal(T.systemLight(), true);
  assert.equal(T.resolveMode("system"), "light");
  fakeSystem(false);
  assert.equal(T.systemLight(), false);
  const real = globalThis.matchMedia;
  globalThis.matchMedia = undefined;
  assert.equal(T.systemLight(), false, "a browser without matchMedia is dark");
  globalThis.matchMedia = real;
});

test("applying a theme sets the attributes, the colour-scheme hint and the browser bar colour", () => {
  T.applyTheme({ mode: "light", scheme: "forest" });
  assert.equal(root.dataset.mode, "light");
  assert.equal(root.dataset.scheme, "forest");
  assert.equal(root.style.colorScheme, "light");
  assert.equal(meta("color-scheme"), "light");
  assert.notEqual(meta("theme-color"), "#0a0a0f");
  T.applyTheme({ mode: "dark", scheme: "motita" });
  assert.equal(root.dataset.mode, "dark");
  assert.equal(meta("theme-color"), "#0a0a0f");
  // A page without those meta tags is themed all the same.
  const metas = [...document.head.querySelectorAll("meta")];
  metas.forEach((m) => m.remove());
  T.applyTheme({ mode: "light", scheme: "amber" });
  assert.equal(root.dataset.scheme, "amber");
  document.head.append(...metas);
});

test("saving keeps the choice and applies it; a storage that refuses still applies it", () => {
  localStorage.clear();
  T.saveTheme({ mode: "dark", scheme: "violet" });
  assert.deepEqual(JSON.parse(localStorage.getItem(T.THEME_KEY)), {
    mode: "dark",
    scheme: "violet",
  });
  assert.equal(root.dataset.scheme, "violet");
  const real = window.Storage.prototype.setItem;
  window.Storage.prototype.setItem = () => {
    throw new Error("quota");
  };
  T.saveTheme({ mode: "light", scheme: "graphite" });
  window.Storage.prototype.setItem = real;
  assert.equal(root.dataset.mode, "light");
  assert.equal(root.dataset.scheme, "graphite");
});

test("starting applies the saved theme, follows the system while the mode says so, and stops on request", () => {
  localStorage.clear();
  const sys = fakeSystem(false);
  const stop = T.initTheme();
  assert.equal(root.dataset.mode, "dark");
  sys.flip(true);
  assert.equal(root.dataset.mode, "light", "the system went light");
  T.saveTheme({ mode: "dark", scheme: "motita" });
  sys.flip(false);
  sys.flip(true);
  assert.equal(
    root.dataset.mode,
    "dark",
    "an explicit mode ignores the system",
  );
  stop();
  assert.equal(sys.listeners.size, 0);
  // No matchMedia at all: applied once, nothing to listen to or stop.
  globalThis.matchMedia = undefined;
  window.matchMedia = undefined;
  T.initTheme()();
  assert.equal(root.dataset.mode, "dark");
});

async function mount() {
  const host = document.createElement("div");
  document.body.append(host);
  render(h(ThemeSettings, {}), host);
  await settle();
  return {
    host,
    radio: (name, v) => host.querySelector(`input[name=${name}][value=${v}]`),
    unmount: async () => {
      render(null, host);
      host.remove();
    },
  };
}

test("the Themes tab shows the three modes and every scheme as a card, the saved ones checked", async () => {
  localStorage.setItem(
    T.THEME_KEY,
    JSON.stringify({ mode: "light", scheme: "forest" }),
  );
  const m = await mount();
  assert.deepEqual(
    [...m.host.querySelectorAll("input[name=settings-mode]")].map((r) =>
      text(r.parentElement),
    ),
    ["Follow the system", "Light", "Dark"],
  );
  assert.equal(m.radio("settings-mode", "light").checked, true);
  const cards = [...m.host.querySelectorAll(".theme-card")];
  assert.deepEqual(
    cards.map((c) => c.dataset.scheme),
    T.SCHEMES,
  );
  assert.deepEqual(
    cards.map((c) => text(c)),
    [
      "Motita",
      "Violet",
      "Forest",
      "Amber",
      "Rose",
      "Graphite",
      "Slate",
      "Midnight",
      "Dusk",
      "Coffee",
      "Ember",
      "Olive",
      "Sand",
    ],
  );
  assert.equal(m.radio("settings-scheme", "forest").checked, true);
  assert.equal(m.radio("settings-scheme", "motita").checked, false);
  assert.ok(
    cards.every((c) => c.querySelector(".theme-preview")),
    "each card previews its colours",
  );
  await m.unmount();
});

test("picking a mode or a scheme saves it and re-themes the page at once", async () => {
  localStorage.clear();
  const m = await mount();
  m.radio("settings-mode", "dark").click();
  await settle();
  assert.equal(root.dataset.mode, "dark");
  m.radio("settings-scheme", "rose").click();
  await settle();
  assert.equal(root.dataset.scheme, "rose");
  assert.deepEqual(JSON.parse(localStorage.getItem(T.THEME_KEY)), {
    mode: "dark",
    scheme: "rose",
  });
  m.radio("settings-mode", "system").click();
  await settle();
  assert.deepEqual(JSON.parse(localStorage.getItem(T.THEME_KEY)), {
    mode: "system",
    scheme: "rose",
  });
  assert.equal(m.radio("settings-mode", "system").checked, true);
  await m.unmount();
});
