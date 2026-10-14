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
// The browser already allowed notifications, so the dialog shows the on/off switch. Each test
// that needs another answer sets it, and what requestPermission resolves to.
const fakeNotification = {
  permission: "granted",
  answer: "granted",
  asked: 0,
  requestPermission() {
    this.asked++;
    this.permission = this.answer;
    return Promise.resolve(this.answer);
  },
};
globalThis.Notification = fakeNotification;
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
const { SettingsModal, unblockSteps } = await import(
  pathToFileURL(join(out, "st.mjs")).href
);
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });

const ACCOUNTS = [
  {
    id: "github",
    name: "GitHub",
    kind: "github",
    host: "github.com",
    connected: true,
    username: "octo",
  },
  {
    id: "gitlab",
    name: "GitLab",
    kind: "gitlab",
    host: "gitlab.com",
    connected: false,
  },
  { id: "forge", name: "Forgejo", kind: "gitea", host: "git.corp.io" },
];
const SWITCH = "input[role=switch]";
const NOTIFY_KEY = "motita.ui.notifications";
const NOTIFY_CARD = "[data-testid=notifications-setting]";
const NOTIFY_STEPS = "[data-testid=notify-steps]";
const CI_TOAST_KEY = "motita.ui.ciToast";
const INSECURE_WARNING = "[data-testid=insecure-warning]";
const ARIA_SELECTED = "aria-selected";
const SPEAK_GROUP = "settings-speak";

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
        onClose: () => {
          calls.close += 1;
        },
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

test("the appearance tab offers every choice as one tap and applies it", async () => {
  const { root, calls, done } = await open();
  const tabs = [...root.querySelectorAll("[role=tab]")];
  assert.deepEqual(
    tabs.map((t) => [t.id, t.getAttribute(ARIA_SELECTED)]),
    [
      ["settings-tab-appearance", "true"],
      ["settings-tab-notifications", "false"],
      ["settings-tab-git", "false"],
    ],
    "one tab per kind of setting; appearance is the first, and it is open",
  );
  const languageFontAndDensity = 3;
  assert.equal(
    root.querySelectorAll("[role=radiogroup]").length,
    languageFontAndDensity,
  );
  assert.equal(root.querySelector("select"), null, "no dropdowns are left");
  assert.equal(radio(root, "settings-density", "comfortable").checked, true);

  await act(() => radio(root, "settings-density", "compact").click());
  assert.equal(document.documentElement.dataset.density, "compact");
  assert.match(localStorage.getItem("motita.ui.settings"), /"compact"/);
  await act(() => radio(root, "settings-font", "large").click());
  assert.equal(document.documentElement.dataset.fontSize, "large");
  await act(() => radio(root, "settings-lang", "es").click());
  assert.deepEqual(calls.lang, ["es"]);
  assert.equal(
    root.querySelector(NOTIFY_CARD),
    null,
    "notifications have their own tab",
  );

  await act(() => root.querySelector("#settings-tab-notifications").click());
  const sw = root.querySelector(`${NOTIFY_CARD} ${SWITCH}`);
  assert.ok(sw, "the desktop notifications are an on/off switch");
  assert.equal(sw.checked, true);
  await act(() => sw.click());
  assert.equal(localStorage.getItem(NOTIFY_KEY), "off");
  await done();
});

const settle = () => act(() => new Promise((r) => setTimeout(r, 0)));

test("notifications not yet asked: a button asks the browser, and allowing turns the switch on", async () => {
  Object.assign(fakeNotification, {
    permission: "default",
    answer: "granted",
    asked: 0,
  });
  localStorage.removeItem(NOTIFY_KEY);
  const { root, done } = await open({ tab: "notifications" });
  const card = root.querySelector(NOTIFY_CARD);
  assert.equal(card.querySelector(SWITCH), null);
  const btn = [...card.querySelectorAll("button")].find(
    (b) => b.textContent === "Allow notifications",
  );
  assert.ok(btn, "there is a button that asks for the permission");
  await act(() => btn.click());
  await settle();
  assert.equal(fakeNotification.asked, 1);
  const sw = card.querySelector(SWITCH);
  assert.ok(sw, "once allowed, the on/off switch takes the button's place");
  assert.equal(sw.checked, true);
  await done();
});

test("a browser that ignores the request gets the steps to allow it by hand", async () => {
  Object.assign(fakeNotification, {
    permission: "default",
    answer: "default",
    asked: 0,
  });
  const { root, done } = await open({ tab: "notifications" });
  const card = root.querySelector(NOTIFY_CARD);
  assert.equal(card.querySelector(NOTIFY_STEPS), null);
  await act(() => card.querySelector("button").click());
  await settle();
  assert.match(card.textContent, /did not ask/);
  assert.ok(card.querySelector("[data-testid=notify-steps] li"));
  await done();
});

test("blocked notifications explain how to allow them again, and pick it up once allowed", async () => {
  Object.assign(fakeNotification, {
    permission: "denied",
    answer: "denied",
    asked: 0,
  });
  const { root, done } = await open({ tab: "notifications" });
  const card = root.querySelector(NOTIFY_CARD);
  assert.match(card.textContent, /Blocked in this browser/);
  const steps = [...card.querySelectorAll("[data-testid=notify-steps] li")];
  assert.ok(steps.length >= 2, "the steps to unblock them are listed");
  assert.equal(card.querySelector(SWITCH), null);
  // The person allows them in the browser's settings and comes back.
  fakeNotification.permission = "granted";
  const again = [...card.querySelectorAll("button")].find(
    (b) => b.textContent === "Check again",
  );
  await act(() => again.click());
  assert.ok(card.querySelector(SWITCH));
  assert.equal(card.querySelector(NOTIFY_STEPS), null);
  await done();
});

test("the CI toast has its own switch, on by default, and it works without https", async () => {
  localStorage.removeItem(CI_TOAST_KEY);
  Object.defineProperty(window, "isSecureContext", {
    value: false,
    configurable: true,
  });
  const { root, done } = await open({ tab: "notifications" });
  const card = root.querySelector("[data-testid=ci-toast-setting]");
  assert.ok(card, "the toast switch is drawn");
  const sw = card.querySelector("input[role=switch]");
  assert.equal(sw.disabled, false, "an insecure page does not grey it out");
  assert.equal(sw.checked, true);
  assert.equal(card.querySelector(INSECURE_WARNING), null);
  await act(() => sw.click());
  assert.equal(localStorage.getItem(CI_TOAST_KEY), "off");
  await act(() => sw.click());
  assert.equal(localStorage.getItem(CI_TOAST_KEY), "on");
  delete window.isSecureContext;
  await done();
});

test("on a plain http address the setting says https is needed, with nothing to press", async () => {
  Object.assign(fakeNotification, { permission: "denied", asked: 0 });
  Object.defineProperty(window, "isSecureContext", {
    value: false,
    configurable: true,
  });
  const { root, done } = await open({ tab: "notifications" });
  const card = root.querySelector(NOTIFY_CARD);
  const pill = card.querySelector(INSECURE_WARNING);
  assert.ok(pill, "the reason is a warning pill");
  assert.equal(pill.getAttribute("role"), "note");
  assert.match(pill.textContent, /Connection not secure/);
  assert.match(pill.textContent, /blocks notifications/);
  assert.match(pill.querySelector("details summary").textContent, /Why\?/);
  assert.match(pill.querySelector("details").textContent, /https:\/\//);
  assert.equal(pill.querySelector("a").rel, "noopener noreferrer");
  assert.equal(card.querySelector("button"), null);
  assert.equal(card.querySelector(NOTIFY_STEPS), null);
  delete window.isSecureContext;
  fakeNotification.permission = "granted";
  await done();
});

test("once allowed, a test notification goes up through the service worker", async () => {
  Object.assign(fakeNotification, { permission: "granted" });
  localStorage.removeItem(NOTIFY_KEY);
  const shown = [];
  Object.defineProperty(navigator, "serviceWorker", {
    value: {
      getRegistration: async () => ({
        showNotification: async (title, opts) => shown.push([title, opts.tag]),
      }),
    },
    configurable: true,
  });
  const { root, done } = await open({ tab: "notifications" });
  const card = root.querySelector(NOTIFY_CARD);
  const send = [...card.querySelectorAll("button")].find(
    (b) => b.textContent === "Send",
  );
  assert.ok(send, "there is a button to send a test notification");
  await act(() => send.click());
  await settle();
  assert.deepEqual(shown, [["Motita notifications work", "motita-test"]]);
  assert.match(card.textContent, /Sent\./);
  delete navigator.serviceWorker;
  await done();
});

test("the unblock steps fit the platform and whether Motita is installed", () => {
  const androidApp = unblockSteps("android", true).join(" ");
  assert.match(androidApp, /App info/);
  assert.match(unblockSteps("android", false).join(" "), /left of the address/);
  assert.match(unblockSteps("ios", true).join(" "), /Settings app/);
  assert.match(unblockSteps("firefox", false).join(" "), /Send notifications/);
  assert.match(unblockSteps("safari", false).join(" "), /Websites/);
  assert.match(unblockSteps("chrome", true).join(" "), /App info/);
  assert.match(unblockSteps("edge", false).join(" "), /Reset permission/);
  assert.ok(unblockSteps("other", false).length > 0);
});

test("the git tab lists the accounts, and the arrow keys move between tabs", async () => {
  const { root, calls, done } = await open();
  const first = root.querySelector("#settings-tab-appearance");
  first.focus();
  await act(() =>
    first.dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }),
    ),
  );
  assert.equal(
    root
      .querySelector("#settings-tab-notifications")
      .getAttribute(ARIA_SELECTED),
    "true",
    "the right arrow goes to the next tab",
  );
  await act(() =>
    document.activeElement.dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }),
    ),
  );
  await act(() =>
    document.activeElement.dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }),
    ),
  );
  const git = root.querySelector("#settings-tab-git");
  assert.equal(git.getAttribute(ARIA_SELECTED), "true");
  assert.equal(document.activeElement, git, "focus follows the chosen tab");
  await act(() => new Promise((r) => setTimeout(r, 0)));
  const rows = [...root.querySelectorAll(".git-account")];
  assert.equal(rows.length, ACCOUNTS.length);
  const icons = rows.map((r) => r.querySelector(".git-account-icon"));
  assert.deepEqual(
    icons.map((i) => i.dataset.kind),
    ["github", "gitlab", "gitea"],
    "every account shows its host's icon",
  );
  assert.equal(icons[0].querySelector("svg").getAttribute("fill"), "#e8e8ea");
  assert.equal(icons[1].querySelector("svg").getAttribute("fill"), "#fc6d26");
  assert.ok(
    icons[2].querySelector("circle"),
    "a host without a logo gets the branch icon",
  );
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
  const escapeCloseAndOutside = 3;
  assert.equal(calls.close, escapeCloseAndOutside);
  await done();
});

test("a tab whose API is absent is not offered", async () => {
  const { root, done } = await open({ git: undefined, langAvailable: false });
  assert.deepEqual(
    [...root.querySelectorAll("[role=tab]")].map((t) => t.id),
    ["settings-tab-appearance", "settings-tab-notifications"],
  );
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
  const lookBehind = 200;
  assert.ok(
    host < app.indexOf("<ProjectModals />", host - lookBehind),
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
  const md = css.slice(
    css.indexOf("@media (min-width: 768px) {\n  .settings-backdrop"),
  );
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

test("the voice switches are greyed out until the page and the gateway allow them", async () => {
  const saved = [];
  const voice = (state, hands, prefs) => ({
    state,
    hands,
    phrases: ["hey motita", "motita"],
    prefs: prefs ?? { mic: true, listen: false },
    setPrefs: (p) => saved.push(p),
  });
  let v = await open({ tab: "voice", voice: voice("insecure", "insecure") });
  const section = v.root.querySelector("[data-testid=voice-setting]");
  assert.ok(section, "the voice section is drawn");
  const [mic, listen] = section.querySelectorAll(SWITCH);
  assert.equal(mic.disabled, true);
  assert.equal(listen.disabled, true);
  assert.equal(mic.checked, false, "an unusable switch never reads as on");
  const pills = section.querySelectorAll(INSECURE_WARNING);
  assert.equal(pills.length, 2, "each blocked switch shows the warning pill");
  assert.match(pills[0].textContent, /without a secure connection/);
  assert.match(pills[0].querySelector("details").textContent, /https:\/\//);
  assert.match(pills[0].querySelector("a").href, /REFERENCE\.md#voice-input$/);
  await v.done();

  v = await open({ tab: "voice", voice: voice("ready", "no-phrases") });
  const [mic2, listen2] = v.root.querySelectorAll(
    "[data-testid=voice-setting] input[role=switch]",
  );
  assert.equal(mic2.disabled, false);
  assert.equal(mic2.checked, true);
  assert.equal(listen2.disabled, true);
  assert.match(v.root.textContent, /voice\.wake_phrases/);
  await act(() => mic2.click());
  assert.deepEqual(saved.pop(), { mic: false, listen: false });
  await v.done();

  v = await open({ tab: "voice", voice: voice("ready", "ready") });
  const listen3 = v.root.querySelectorAll(
    "[data-testid=voice-setting] input[role=switch]",
  )[1];
  assert.equal(listen3.disabled, false);
  assert.match(v.root.textContent, /“hey motita”, “motita”/);
  await act(() => listen3.click());
  assert.deepEqual(saved.pop(), { mic: true, listen: true });
  await v.done();

  for (const [state, text] of [
    ["unsupported", /cannot record audio/],
    ["off", /voice\.enabled/],
  ]) {
    v = await open({ tab: "voice", voice: voice(state, state) });
    assert.match(v.root.textContent, text);
    await v.done();
  }
});

test("read aloud: when, which voice, how fast and how much, and a button to try it", async () => {
  const saved = [];
  let tried = 0;
  const prefs = { mode: "voice", voiceURI: "", rate: 1, maxChars: 600 };
  const speech = (supported, voices) => ({
    supported,
    prefs,
    setPrefs: (p) => saved.push(p),
    voices,
    test: () => {
      tried += 1;
    },
  });
  let v = await open({
    tab: "voice",
    speech: speech(true, [{ voiceURI: "ana", name: "Ana", lang: "es-CL" }]),
  });
  const card = v.root.querySelector("[data-testid=speech-setting]");
  assert.ok(card, "the read-aloud section is drawn");
  assert.equal(radio(card, SPEAK_GROUP, "voice").checked, true);
  assert.match(card.textContent, /Alt\+L/, "the shortcut is named");
  await act(() => radio(card, SPEAK_GROUP, "always").click());
  assert.deepEqual(saved.pop(), { ...prefs, mode: "always" });
  await act(() => radio(card, "settings-speech-rate", `"1.5"`).click());
  assert.deepEqual(saved.pop(), { ...prefs, rate: 1.5 });
  await act(() => radio(card, "settings-speech-length", `"0"`).click());
  assert.deepEqual(saved.pop(), { ...prefs, maxChars: 0 });
  const select = card.querySelector("select");
  assert.deepEqual(
    [...select.options].map((o) => o.value),
    ["", "ana"],
    "the device's choice first, then the voices of the language",
  );
  select.value = "ana";
  await act(() =>
    select.dispatchEvent(new window.Event("change", { bubbles: true })),
  );
  assert.deepEqual(saved.pop(), { ...prefs, voiceURI: "ana" });
  await act(() => card.querySelector("[data-testid=speech-test]").click());
  assert.equal(tried, 1);
  await v.done();

  v = await open({ tab: "voice", speech: speech(true, []) });
  assert.match(v.root.textContent, /no voice for the interface language/);
  await v.done();

  v = await open({ tab: "voice", speech: speech(false, []) });
  const off = v.root.querySelector("[data-testid=speech-setting]");
  assert.match(off.textContent, /cannot read aloud/);
  assert.equal(off.querySelector("select").disabled, true);
  assert.equal(off.querySelector("[data-testid=speech-test]").disabled, true);
  assert.equal(radio(off, SPEAK_GROUP, "off").disabled, true);
  await v.done();
});

test("the insecure-connection pill is translated, amber, and tighter in compact density", () => {
  const es = readFileSync(join(here, "..", "i18n.es.ts"), "utf8");
  for (const k of [
    "Connection not secure",
    "Why?",
    "How to set it up",
    "Your browser blocks the microphone because Motita was opened without a secure connection.",
    "Your browser blocks notifications because Motita was opened without a secure connection.",
  ]) {
    assert.ok(es.includes(JSON.stringify(k)) || es.includes(`${k}:`), k);
  }
  const css = src("index.css");
  assert.match(css, /\.settings-warn \{[^}]*rgba\(237, 181, 88/);
  assert.match(css, /html\[data-density="compact"\] \.settings-warn \{/);
});

test("speech input is turned on from the settings, after saying where recordings go", async () => {
  const sent = [];
  const off = {
    enabled: false,
    wake_phrases: [],
    max_audio_bytes: 1,
    can_save: true,
    saved: {
      enabled: false,
      base_url: "",
      endpoint: "",
      inherits: false,
      provider: "gemini",
      model: "whisper-1",
      has_key: false,
      wake_phrases: [],
    },
  };
  const on = {
    ...off,
    enabled: true,
    wake_phrases: ["oye motita"],
    saved: {
      ...off.saved,
      enabled: true,
      base_url: "https://api.groq.com/openai/v1",
      endpoint: "https://api.groq.com/openai/v1",
      model: "whisper-large-v3-turbo",
      has_key: true,
      wake_phrases: ["oye motita"],
    },
  };
  const api = async (path, init) => {
    sent.push({ path, body: JSON.parse(init.body) });
    return new Response(JSON.stringify(on), { status: 200 });
  };
  const infos = [];
  const v = await open({
    tab: "voice",
    voice: {
      state: "off",
      hands: "off",
      phrases: [],
      prefs: { mic: true, listen: false, deviceId: "" },
      setPrefs: () => {},
      info: off,
      api,
      onInfo: (i) => infos.push(i),
    },
  });
  const card = v.root.querySelector("[data-testid=voice-setting]");
  assert.match(card.textContent, /Turn on speech input above first/);
  await act(() =>
    card.querySelector("[data-testid=voice-gateway-switch]").click(),
  );
  const confirm = card.querySelector("[data-testid=voice-confirm]");
  assert.ok(confirm, "turning it on asks first");
  assert.match(confirm.textContent, /no transcription service of its own/);
  const yes = confirm.querySelector("[data-testid=voice-confirm-yes]");
  assert.equal(yes.disabled, true, "nowhere to send recordings yet");
  const groq = [...confirm.querySelectorAll("button")].find(
    (b) => b.textContent === "Groq",
  );
  await act(() => groq.click());
  const key = confirm.querySelector("[data-testid=voice-api-key]");
  key.value = "gsk-1";
  await act(() => key.dispatchEvent(new Event("input", { bubbles: true })));
  assert.match(confirm.textContent, /sent to https:\/\/api\.groq\.com/);
  await act(() =>
    confirm.querySelector("[data-testid=voice-confirm-yes]").click(),
  );
  await act(() => new Promise((r) => setTimeout(r, 0)));
  assert.deepEqual(sent.pop(), {
    path: "/v1/voice",
    body: {
      enabled: true,
      confirm: true,
      base_url: "https://api.groq.com/openai/v1",
      model: "whisper-large-v3-turbo",
      api_key: "gsk-1",
    },
  });
  assert.deepEqual(
    infos.pop(),
    on,
    "the composer learns the new state at once",
  );
  await v.done();
});

test("the microphone picker lists this device's microphones and keeps the choice", async () => {
  const before = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  let asked = 0;
  Object.defineProperty(globalThis, "navigator", {
    value: {
      ...dom.window.navigator,
      mediaDevices: {
        enumerateDevices: async () => [
          { kind: "audioinput", deviceId: "default", label: "Default" },
          { kind: "audioinput", deviceId: "usb", label: "USB Mic" },
        ],
        getUserMedia: async () => {
          asked += 1;
          return { getTracks: () => [] };
        },
        addEventListener: () => {},
        removeEventListener: () => {},
      },
    },
    configurable: true,
  });
  const saved = [];
  const v = await open({
    tab: "voice",
    voice: {
      state: "ready",
      hands: "no-phrases",
      phrases: [],
      prefs: { mic: true, listen: false, deviceId: "gone" },
      setPrefs: (p) => saved.push(p),
    },
  });
  await act(() => new Promise((r) => setTimeout(r, 0)));
  const select = v.root.querySelector("[data-testid=mic-device]");
  assert.deepEqual(
    [...select.options].map((o) => o.textContent),
    ["System default", "Not connected", "USB Mic"],
  );
  assert.match(v.root.textContent, /chosen microphone is not connected/);
  select.value = "usb";
  await act(() => select.dispatchEvent(new Event("change", { bubbles: true })));
  assert.equal(saved.pop().deviceId, "usb");
  assert.equal(asked, 0, "the names were already visible: nothing was asked");
  await v.done();
  Object.defineProperty(globalThis, "navigator", before);
});
