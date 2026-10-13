// Runs the real settings dialog in jsdom (bundled with esbuild, since node cannot import TSX): its
// sections as tabs, the segmented choices, the notification switch, the git accounts and closing.
// Also checks that the app, not the sidebar, draws it, and the CSS that lays it out on a phone.
// Run with: node --test web/src/__tests__/settingsmodal.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
const src = (f) => readFileSync(join(here, "..", f), "utf8");
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const k of [
  "document",
  "window",
  "KeyboardEvent",
  "MouseEvent",
  "Event",
  "HTMLElement",
  "Node",
  "localStorage",
]) {
  globalThis[k] = dom.window[k];
}
Object.defineProperty(globalThis, "navigator", {
  value: dom.window.navigator,
  configurable: true,
});
// The browser already allowed notifications, so the dialog shows the on/off switch.
globalThis.Notification = { permission: "granted" };
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const out = mkdtempSync(join(here, ".st-"));
await build({
  entryPoints: [join(here, "..", "Settings.tsx")],
  outfile: join(out, "st.mjs"),
  bundle: true,
  format: "esm",
  jsx: "automatic",
  jsxImportSource: "preact",
  platform: "node",
  external: ["preact", "preact/*"],
  loader: { ".css": "empty" },
  // "./settings" is settings.ts; esbuild would try Settings.tsx (this very file) first.
  resolveExtensions: [".ts", ".tsx", ".mjs", ".js"],
  logLevel: "silent",
});
const { SettingsModal } = await import(pathToFileURL(join(out, "st.mjs")).href);
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });

const ACCOUNTS = [
  {
    id: "github",
    name: "GitHub",
    host: "github.com",
    connected: true,
    username: "octo",
  },
  { id: "gitlab", name: "GitLab", host: "gitlab.com", connected: false },
];
const fakeApi = async () =>
  new Response(JSON.stringify({ accounts: ACCOUNTS }), { status: 200 });

async function open(extra = {}) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const calls = { close: 0, lang: [], connect: [] };
  await act(() => {
    render(
      h(SettingsModal, {
        langSetting: "auto",
        langAvailable: true,
        onLang: (s) => calls.lang.push(s),
        onClose: () => calls.close++,
        git: {
          api: fakeApi,
          rev: 0,
          onConnect: (id) => calls.connect.push(id),
          onNotice: () => {},
        },
        ...extra,
      }),
      root,
    );
  });
  const done = async () => {
    await act(() => render(null, root));
    root.remove();
  };
  return { root, calls, done };
}

const radio = (root, group, value) =>
  root.querySelector(`input[type=radio][name=${group}][value=${value}]`);

test("the general tab offers every choice as one tap and applies it", async () => {
  const { root, calls, done } = await open();
  const tabs = [...root.querySelectorAll("[role=tab]")];
  assert.deepEqual(
    tabs.map((t) => t.getAttribute("aria-selected")),
    ["true", "false"],
    "general is the first tab, and it is open",
  );
  assert.equal(root.querySelectorAll("[role=radiogroup]").length, 3);
  assert.equal(root.querySelector("select"), null, "no dropdowns are left");
  assert.equal(radio(root, "settings-density", "comfortable").checked, true);

  await act(() => radio(root, "settings-density", "compact").click());
  assert.equal(document.documentElement.dataset.density, "compact");
  assert.match(localStorage.getItem("motita.ui.settings"), /"compact"/);
  await act(() => radio(root, "settings-font", "large").click());
  assert.equal(document.documentElement.dataset.fontSize, "large");
  await act(() => radio(root, "settings-lang", "es").click());
  assert.deepEqual(calls.lang, ["es"]);

  const sw = root.querySelector("input[role=switch]");
  assert.ok(sw, "the CI notifications are an on/off switch");
  assert.equal(sw.checked, true);
  await act(() => sw.click());
  assert.equal(localStorage.getItem("motita.ui.notifications"), "off");
  await done();
});

test("the git tab lists the accounts, and the arrow keys move between tabs", async () => {
  const { root, calls, done } = await open();
  const general = root.querySelector("#settings-tab-general");
  general.focus();
  await act(() =>
    general.dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }),
    ),
  );
  const git = root.querySelector("#settings-tab-git");
  assert.equal(git.getAttribute("aria-selected"), "true");
  assert.equal(document.activeElement, git, "focus follows the chosen tab");
  await act(() => new Promise((r) => setTimeout(r, 0)));
  const rows = [...root.querySelectorAll(".git-account")];
  assert.equal(rows.length, 2);
  assert.match(rows[0].textContent, /octo/);
  assert.ok(root.querySelector("[aria-label='Reconnect GitHub']"));
  await act(() => root.querySelector("[aria-label='Connect GitLab']").click());
  assert.deepEqual(calls.connect, ["gitlab"]);
  await done();
});

test("Escape, the ✕ and a click outside close the dialog", async () => {
  const { root, calls, done } = await open();
  document.dispatchEvent(
    new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
  );
  root.querySelector("[aria-label=Close]").click();
  root
    .querySelector(".settings-backdrop")
    .dispatchEvent(new MouseEvent("click", { bubbles: true }));
  assert.equal(calls.close, 3);
  await done();
});

test("without a git API there are no tabs, only the general settings", async () => {
  const { root, done } = await open({ git: undefined, langAvailable: false });
  assert.equal(root.querySelector("[role=tablist]"), null);
  assert.equal(root.querySelectorAll("[role=radiogroup]").length, 2);
  await done();
});

test("the app draws the dialog, outside the sidebar's frosted glass", () => {
  // A `fixed` element inside the sidebar is placed against the sidebar (its backdrop-filter is a
  // containing block), which squeezed the old dialog into the sidebar's width.
  assert.doesNotMatch(src("Sidebar.tsx"), /<SettingsModal/);
  const app = src("App.tsx");
  const host = app.indexOf("<SettingsHost />");
  assert.ok(host > 0, "App renders the settings");
  assert.ok(
    host < app.indexOf("<ProjectModals />", host - 200),
    "it comes before the git connect dialog, which stacks on top of it",
  );
});

test("the dialog fills a phone and is a centred two-column window from md up", () => {
  const css = src("index.css");
  assert.match(
    css,
    /\.settings-panel \{[^}]*height: var\(--app-h, 100dvh\);[^}]*env\(safe-area-inset-top\)/,
    "on a phone it is the height the reader can see, clear of the notch",
  );
  const md = css.slice(css.indexOf("@media (min-width: 768px) {\n  .settings-backdrop"));
  assert.match(md, /grid-template-columns: 12\.5rem minmax\(0, 1fr\)/);
  assert.match(
    css,
    /@media \(pointer: coarse\) \{\s*\.settings-seg-opt,\s*\.settings-switch-hit \{\s*min-height: 44px;/,
    "the segmented choices and the switch get a finger's 44px",
  );
  assert.match(
    css,
    /html\[data-density="compact"\] :is\(\.settings-seg-opt, \.settings-switch-hit\) \{\s*min-height: 36px;/,
    "and 36px in compact density, like every button",
  );
  assert.match(
    css,
    /@media \(hover: hover\) \{\s*\.settings-tab:not\(\[aria-selected="true"\]\):hover/,
    "the hover highlight is only for a mouse",
  );
});
