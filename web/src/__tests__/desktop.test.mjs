// The Security tab's choice of whether Motita may control this computer's desktop: off is the
// default, turning it on needs a confirmation that spells out the risk, turning it off needs none,
// and a computer with no desktop says why.
// Run with: node --test web/src/__tests__/desktop.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  installDom,
  load,
  fakeApi,
  json,
  text,
  settle,
  click,
  byText,
} from "./cov-settings-helpers.mjs";

installDom();
const here = dirname(fileURLToPath(import.meta.url));
const { SecuritySettings } = await load("SecuritySettings.tsx");
const sec = await import("../securityApi.ts");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");

const SEC = {
  disk_access: "workspace",
  can_save: true,
  enforced: false,
  desktop_control: false,
};
const NAME = "settings-desktop";
const CONFIRM = "[data-testid=security-desktop-confirm]";
const ERROR = "[data-testid=security-desktop-error]";

async function mount(api) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(SecuritySettings, { api }), root);
  await settle();
  return {
    root,
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}

const radio = (root, value) =>
  root.querySelector(`input[name=${NAME}][value=${value}]`);
async function pick(root, value) {
  const r = radio(root, value);
  r.checked = true;
  r.dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
}
async function press(root, label) {
  click(byText(root.querySelector(CONFIRM), "button", label));
  await settle();
}

function gateway(initial) {
  let state = initial;
  return fakeApi({
    "GET /v1/security": () => json(200, state),
    "PUT /v1/security": (_p, init) => {
      const body = JSON.parse(init.body);
      if (body.desktop_control === true && body.confirm !== true) {
        return json(400, { error: "confirm is required" });
      }
      state = { ...state, desktop_control: body.desktop_control };
      return json(200, state);
    },
  });
}

const puts = (f) => f.calls.filter((c) => c.method === "PUT");

test("off is the default, and turning it on waits for a confirmation", async () => {
  const f = gateway(SEC);
  const m = await mount(f.api);
  assert.equal(radio(m.root, "off").checked, true);
  assert.match(text(m.root), /never sees your screen/);

  await pick(m.root, "off");
  assert.equal(m.root.querySelector(CONFIRM), null);

  await pick(m.root, "ask");
  const c = m.root.querySelector(CONFIRM);
  assert.match(text(c), /control this computer's desktop\?/);
  assert.match(text(c), /passwords on display/);
  assert.match(text(m.root), /asks before every step/);
  assert.equal(puts(f).length, 0, "nothing is saved before the confirmation");

  await press(m.root, "Cancel");
  assert.equal(m.root.querySelector(CONFIRM), null);
  assert.equal(radio(m.root, "off").checked, true);

  await pick(m.root, "ask");
  await pick(m.root, "off");
  assert.equal(m.root.querySelector(CONFIRM), null);
  assert.equal(puts(f).length, 0);

  await pick(m.root, "ask");
  await press(m.root, "Allow desktop control");
  assert.deepEqual(puts(f)[0].body, { desktop_control: true, confirm: true });
  assert.equal(radio(m.root, "ask").checked, true);

  await pick(m.root, "ask");
  assert.equal(puts(f).length, 1, "the current choice saves nothing");

  await pick(m.root, "off");
  assert.deepEqual(puts(f)[1].body, { desktop_control: false, confirm: false });
  assert.equal(radio(m.root, "off").checked, true);
  await m.unmount();
});

test("a computer without a desktop says why, an older gateway draws no choice", async () => {
  let m = await mount(
    gateway({ ...SEC, desktop_reason: "no graphical session" }).api,
  );
  assert.match(
    text(m.root.querySelector("[data-testid=security-desktop-reason]")),
    /no graphical session/,
  );
  await m.unmount();

  const { desktop_control: _, ...old } = SEC;
  m = await mount(gateway(old).api);
  assert.equal(radio(m.root, "off"), null);
  await m.unmount();

  m = await mount(gateway({ ...SEC, can_save: false }).api);
  assert.equal(radio(m.root, "ask").disabled, true);
  assert.equal(
    m.root.querySelector("[data-testid=security-desktop-reason]"),
    null,
  );
  await m.unmount();
});

test("a save the gateway refuses shows its error and keeps the choice", async () => {
  const f = fakeApi({
    "GET /v1/security": json(200, SEC),
    "PUT /v1/security": json(500, {
      error: "MOTITA_AGENT_DESKTOP_CONTROL overrides it",
    }),
  });
  const m = await mount(f.api);
  await pick(m.root, "ask");
  await press(m.root, "Allow desktop control");
  assert.match(text(m.root.querySelector(ERROR)), /overrides it/);
  assert.equal(radio(m.root, "off").checked, true);
  await pick(m.root, "ask");
  assert.equal(m.root.querySelector(ERROR), null, "a new pick clears it");
  await m.unmount();
});

test("the client confirms only turning it on", async () => {
  const f = fakeApi({ "PUT /v1/security": json(200, SEC) });
  assert.deepEqual(await sec.setDesktopControl(f.api, true), SEC);
  await sec.setDesktopControl(f.api, false);
  assert.deepEqual(f.calls[0].body, { desktop_control: true, confirm: true });
  assert.deepEqual(f.calls[1].body, { desktop_control: false, confirm: false });
});

test("every string of the choice has a Spanish translation", () => {
  const source = readFileSync(join(here, "..", "DesktopSetting.tsx"), "utf8");
  const keys = new Set();
  for (const m of source.matchAll(
    /\bt[fc]?\(\s*(['"])((?:(?!\1)[^\\]|\\.)*)\1/g,
  )) {
    keys.add(m[2]);
  }
  const MIN_STRINGS = 9;
  assert.ok(keys.size >= MIN_STRINGS, String(keys.size));
  assert.deepEqual(
    [...keys].filter((k) => !es[k]),
    [],
  );
});
