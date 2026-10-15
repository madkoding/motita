// The Security tab's choice of whether Motita asks before the project's own code runs where nothing
// confines its commands: asking is the default, turning it off needs a confirmation that spells out
// the risk, and turning it back on needs none.
// Run with: node --test web/src/__tests__/unconfinedruns.test.mjs
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
  ask_unconfined_runs: true,
};
const NAME = "settings-unconfined-runs";
const CONFIRM = "[data-testid=security-runs-confirm]";

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

// gateway answers the security API as the Go side does and keeps the saved choice.
function gateway(initial) {
  let state = initial;
  return fakeApi({
    "GET /v1/security": () => json(200, state),
    "PUT /v1/security": (_p, init) => {
      const body = JSON.parse(init.body);
      if (body.ask_unconfined_runs === false && body.confirm !== true) {
        return json(400, { error: "confirm is required" });
      }
      state = { ...state, ask_unconfined_runs: body.ask_unconfined_runs };
      return json(200, state);
    },
  });
}

const puts = (f) => f.calls.filter((c) => c.method === "PUT");

test("asking is the default, and turning it off waits for a confirmation", async () => {
  const f = gateway(SEC);
  const m = await mount(f.api);
  assert.equal(radio(m.root, "ask").checked, true);
  assert.match(text(m.root), /asks before it runs the project's own code/);
  assert.doesNotMatch(text(m.root), /changes nothing here/);

  await pick(m.root, "ask");
  assert.equal(m.root.querySelector(CONFIRM), null, "the current choice asks nothing");

  await pick(m.root, "run");
  const c = m.root.querySelector(CONFIRM);
  assert.match(text(c), /Run the project's code without asking\?/);
  assert.match(text(c), /read your keys/);
  assert.match(text(m.root), /Reads outside the workspace are still asked about/);
  assert.equal(puts(f).length, 0, "nothing is saved before the confirmation");

  await press(m.root, "Cancel");
  assert.equal(m.root.querySelector(CONFIRM), null);
  assert.equal(radio(m.root, "ask").checked, true);

  await pick(m.root, "run");
  await pick(m.root, "ask");
  assert.equal(m.root.querySelector(CONFIRM), null, "picking the current choice drops the question");
  assert.equal(puts(f).length, 0);

  await pick(m.root, "run");
  await press(m.root, "Run without asking");
  assert.deepEqual(puts(f)[0].body, { ask_unconfined_runs: false, confirm: true });
  assert.equal(radio(m.root, "run").checked, true);

  await pick(m.root, "ask");
  assert.deepEqual(puts(f)[1].body, { ask_unconfined_runs: true, confirm: false });
  assert.equal(radio(m.root, "ask").checked, true);
  await m.unmount();
});

test("where commands are confined the choice says it changes nothing", async () => {
  const m = await mount(gateway({ ...SEC, enforced: true }).api);
  assert.match(text(m.root), /changes nothing here/);
  await m.unmount();
});

test("an older gateway without the setting draws no choice, and no file means no change", async () => {
  const { ask_unconfined_runs: _, ...old } = SEC;
  let m = await mount(gateway(old).api);
  assert.equal(radio(m.root, "ask"), null);
  await m.unmount();

  m = await mount(gateway({ ...SEC, can_save: false }).api);
  assert.equal(radio(m.root, "run").disabled, true);
  await m.unmount();
});

test("a save the gateway refuses shows its error and keeps the choice", async () => {
  const f = fakeApi({
    "GET /v1/security": json(200, SEC),
    "PUT /v1/security": json(500, { error: "MOTITA_SANDBOX_ASK_UNCONFINED_RUNS overrides it" }),
  });
  const m = await mount(f.api);
  await pick(m.root, "run");
  await press(m.root, "Run without asking");
  assert.match(text(m.root.querySelector("[data-testid=security-runs-error]")), /overrides it/);
  assert.equal(radio(m.root, "ask").checked, true);
  await pick(m.root, "run");
  assert.equal(m.root.querySelector("[data-testid=security-runs-error]"), null, "a new pick clears it");
  await m.unmount();
});

test("the client confirms only turning the question off", async () => {
  const f = fakeApi({ "PUT /v1/security": json(200, SEC) });
  assert.deepEqual(await sec.setAskUnconfinedRuns(f.api, false), SEC);
  await sec.setAskUnconfinedRuns(f.api, true);
  assert.deepEqual(f.calls[0].body, { ask_unconfined_runs: false, confirm: true });
  assert.deepEqual(f.calls[1].body, { ask_unconfined_runs: true, confirm: false });
});

test("every string of the choice has a Spanish translation", () => {
  const source = readFileSync(join(here, "..", "UnconfinedRunsSetting.tsx"), "utf8");
  const keys = new Set();
  for (const m of source.matchAll(/\bt[fc]?\(\s*(['"])((?:(?!\1)[^\\]|\\.)*)\1/g)) {
    keys.add(m[2]);
  }
  const MIN_STRINGS = 8;
  assert.ok(keys.size >= MIN_STRINGS, String(keys.size));
  assert.deepEqual(
    [...keys].filter((k) => !es[k]),
    [],
  );
});
