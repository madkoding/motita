// The settings dialog itself: which tabs it shows for the sections the page hands it, moving
// between them with the mouse and the arrow keys, closing it, and the appearance tab (language,
// font size, density). The notification, voice, speech and git sections are other groups' work,
// stood in for here by small stubs.
// Run with: node --test web/src/__tests__/cov-settings-modal.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  stub,
  fakeApi,
  json,
  text,
  settle,
  click,
  key,
} from "./cov-settings-helpers.mjs";

installDom();
localStorage.setItem(
  "motita.ui.settings",
  JSON.stringify({ fontSize: "large", density: "compact" }),
);
stub(
  "./NotifySettings",
  `
export function NotifySetting({ push }) {
  return <p data-testid="notify-stub">{push ? "push" : "no push"}</p>;
}
export function unblockSteps() {
  return ["stub step"];
}
`,
);
stub(
  "./VoiceSettings",
  `
export function VoiceSettings(p) {
  return <p data-testid="voice-stub">{p.label}</p>;
}
`,
);
stub(
  "./SpeechSettings",
  `
export function SpeechSettings(p) {
  return <p data-testid="speech-stub">{p.label}</p>;
}
`,
);
stub(
  "./GitConnections",
  `
export function GitConnections(p) {
  return <p data-testid="git-stub">{p.label}</p>;
}
`,
);
const S = await load("Settings.tsx");
const { h, render } = await import("preact");

// Loading the dialog applies the saved look.
test("loading the dialog applies the saved look and re-exports its helpers", () => {
  assert.equal(document.documentElement.dataset.fontSize, "large");
  assert.equal(document.documentElement.dataset.density, "compact");
  assert.deepEqual(S.unblockSteps(), ["stub step"]);
  assert.equal(typeof S.restartTiming.health, "function");
  assert.equal(typeof S.InsecureWarning, "function");
});

const api = fakeApi({
  "GET /v1/security": json(200, {
    disk_access: "workspace",
    can_save: true,
    enforced: true,
  }),
  "GET /v1/network": json(200, {
    mode: "local",
    listen: "127.0.0.1:1",
    active_mode: "local",
    active_listen: "127.0.0.1:1",
    restart_needed: false,
    can_save: true,
  }),
  "GET /v1/llm": json(200, {
    provider: "p",
    model: "m",
    writable: true,
    providers: [],
  }),
}).api;

const base = { langSetting: "auto", langAvailable: false, onLang() {} };

async function mount(props) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  let closed = 0;
  render(
    h(S.SettingsModal, { ...base, onClose: () => closed++, ...props }),
    root,
  );
  await settle();
  return {
    root,
    closed: () => closed,
    tabs: () =>
      [...root.querySelectorAll("[role=tab]")].map((t) =>
        t.id.replace("settings-tab-", ""),
      ),
    selected: () =>
      root
        .querySelector("[role=tab][aria-selected=true]")
        .id.replace("settings-tab-", ""),
    pane: () => root.querySelector("#settings-pane"),
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}

test("a page with no extra sections gets appearance and notifications", async () => {
  const m = await mount({});
  assert.deepEqual(m.tabs(), ["appearance", "themes", "notifications"]);
  assert.equal(m.selected(), "appearance");
  assert.equal(text(m.root.querySelector("#settings-title")), "Settings");
  assert.equal(
    m.root.querySelector("[role=tab][aria-selected=true]").tabIndex,
    0,
  );
  assert.equal(
    m.root.querySelector("#settings-tab-notifications").tabIndex,
    -1,
  );
  click(m.root.querySelector("#settings-tab-notifications"));
  await settle();
  assert.equal(m.selected(), "notifications");
  assert.equal(text(m.pane()), "no push");
  assert.equal(
    m.pane().getAttribute("aria-labelledby"),
    "settings-tab-notifications",
  );
  await m.unmount();
});

test("each section the page hands adds its tab, in order, and draws its pane", async () => {
  const props = {
    voice: { label: "voice in" },
    speech: { label: "speech out" },
    security: { api, sessionId: "" },
    codeIndex: { api },
    autopilot: { api },
    network: { api },
    llm: { api, onNotice() {} },
    git: { label: "git hosts" },
    push: { api },
  };
  const m = await mount(props);
  assert.deepEqual(m.tabs(), [
    "appearance",
    "themes",
    "notifications",
    "voice",
    "speech",
    "security",
    "codeindex",
    "autopilot",
    "network",
    "providers",
    "models",
    "git",
  ]);
  const shown = {};
  for (const id of m.tabs()) {
    click(m.root.querySelector(`#settings-tab-${id}`));
    await settle();
    assert.equal(m.selected(), id);
    shown[id] = m.pane();
  }
  assert.equal(text(shown.notifications), "push");
  assert.equal(text(shown.voice), "voice in");
  assert.equal(text(shown.speech), "speech out");
  assert.ok(shown.security.querySelector("[data-testid=security-setting]"));
  assert.ok(shown.codeindex.querySelector("section"));
  assert.ok(shown.autopilot.querySelector("section"));
  assert.ok(shown.network.querySelector("[data-testid=network-setting]"));
  assert.ok(shown.providers.querySelector("[data-testid=provider-settings]"));
  assert.ok(shown.models.querySelector("[data-testid=model-settings]"));
  assert.equal(text(shown.git), "git hosts");
  await m.unmount();

  // Voice alone, and speech alone, each give their own tab.
  for (const [p, tab, want] of [
    [{ voice: { label: "only voice" } }, "voice", "only voice"],
    [{ speech: { label: "only speech" } }, "speech", "only speech"],
  ]) {
    const n = await mount({ ...p, tab });
    assert.deepEqual(n.tabs(), ["appearance", "themes", "notifications", tab]);
    assert.equal(text(n.pane()), want);
    await n.unmount();
  }
});

test("the dialog opens on the tab asked for; a tab whose section is absent shows nothing", async () => {
  for (const tab of [
    "voice",
    "speech",
    "security",
    "network",
    "providers",
    "models",
    "git",
  ]) {
    const m = await mount({ tab });
    assert.equal(
      m.pane().getAttribute("aria-labelledby"),
      `settings-tab-${tab}`,
    );
    assert.equal(m.pane().innerHTML, "");
    assert.equal(m.root.querySelector("[role=tab][aria-selected=true]"), null);
    await m.unmount();
  }
});

test("the arrow keys move between the tabs and wrap; other keys do nothing", async () => {
  const m = await mount({ network: { api }, tab: "notifications" });
  const list = m.root.querySelector("[role=tablist]");
  key(list, "ArrowRight");
  await settle();
  assert.equal(m.selected(), "network");
  assert.equal(document.activeElement.id, "settings-tab-network");
  key(list, "ArrowDown");
  await settle();
  assert.equal(m.selected(), "appearance");
  key(list, "ArrowLeft");
  await settle();
  assert.equal(m.selected(), "network");
  key(list, "ArrowUp");
  await settle();
  assert.equal(m.selected(), "notifications");
  const e = new window.KeyboardEvent("keydown", {
    key: "Home",
    bubbles: true,
    cancelable: true,
  });
  list.dispatchEvent(e);
  await settle();
  assert.equal(e.defaultPrevented, false);
  assert.equal(m.selected(), "notifications");
  await m.unmount();
});

test("the close button, Escape and a click on the backdrop close it", async () => {
  const m = await mount({});
  click(m.root.querySelector("button[aria-label=Close]"));
  assert.equal(m.closed(), 1);
  key(m.root.querySelector("dialog"), "Escape");
  assert.equal(m.closed(), 2);
  click(m.root.querySelector(".settings-backdrop"));
  assert.equal(m.closed(), 3);
  // A click inside the dialog does not.
  click(m.root.querySelector("#settings-title"));
  assert.equal(m.closed(), 3);
  await m.unmount();
});

test("appearance: the language when the page offers it, and the font size and density saved", async () => {
  const langs = [];
  const m = await mount({
    langAvailable: true,
    langSetting: "es",
    onLang: (l) => langs.push(l),
  });
  const radio = (name, v) =>
    m.root.querySelector(`input[name=${name}][value=${v}]`);
  assert.equal(radio("settings-lang", "es").checked, true);
  assert.deepEqual(
    [...m.root.querySelectorAll("input[name=settings-lang]")].map((r) =>
      text(r.parentElement),
    ),
    ["Automatic", "English", "Español"],
  );
  radio("settings-lang", "en").click();
  assert.deepEqual(langs, ["en"]);

  assert.equal(radio("settings-font", "large").checked, true);
  assert.equal(radio("settings-density", "compact").checked, true);
  radio("settings-font", "small").click();
  await settle();
  assert.equal(document.documentElement.dataset.fontSize, "small");
  radio("settings-density", "comfortable").click();
  await settle();
  assert.equal(document.documentElement.dataset.density, "comfortable");
  assert.deepEqual(JSON.parse(localStorage.getItem("motita.ui.settings")), {
    fontSize: "small",
    density: "comfortable",
  });
  assert.equal(radio("settings-font", "small").checked, true);
  // The hint of the next request is on until it is switched off.
  const hint = m.root.querySelector("[aria-labelledby=settings-next-hint]");
  assert.equal(hint.checked, true);
  hint.click();
  await settle();
  assert.equal(localStorage.getItem("motita.ui.nextHint"), "off");
  hint.click();
  await settle();
  assert.equal(localStorage.getItem("motita.ui.nextHint"), "on");
  // The sidebar's usage is bars until the gauge is picked, and the choice is announced.
  assert.equal(radio("settings-usage-style", "bars").checked, true);
  let heard = 0;
  window.addEventListener("motita-usage-style", () => (heard += 1));
  radio("settings-usage-style", "gauge").click();
  await settle();
  assert.equal(localStorage.getItem("motita.ui.usageStyle"), "gauge");
  assert.equal(heard, 1);
  localStorage.removeItem("motita.ui.usageStyle");
  await m.unmount();

  // Without a language choice the row is left out.
  const n = await mount({});
  assert.equal(n.root.querySelector("input[name=settings-lang]"), null);
  assert.equal(
    n.root.querySelector("input[name=settings-font][value=small]").checked,
    true,
  );
  await n.unmount();
});
