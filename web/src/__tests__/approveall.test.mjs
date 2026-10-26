// The Security tab's choice of whether Motita applies the suggestions of a finished task without
// asking: asking is the default, turning allowing all on needs a confirmation that spells out the risk,
// and turning it off needs none.
// Run with: node --test web/src/__tests__/autoapply.test.mjs
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
  approve_all_commands: false,
};
const NAME = "settings-approve-all";
const CONFIRM = "[data-testid=security-approve-confirm]";

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
      if (body.approve_all_commands === true && body.confirm !== true) {
        return json(400, { error: "confirm is required" });
      }
      state = { ...state, approve_all_commands: body.approve_all_commands };
      return json(200, state);
    },
  });
}

const puts = (f) => f.calls.filter((c) => c.method === "PUT");

test("asking is the default, and allowing all waits for a confirmation", async () => {
  const f = gateway(SEC);
  const m = await mount(f.api);
  assert.equal(radio(m.root, "ask").checked, true);
  assert.match(text(m.root), /Motita asks before a command that installs/);

  await pick(m.root, "ask");
  assert.equal(m.root.querySelector(CONFIRM), null);

  await pick(m.root, "all");
  const c = m.root.querySelector(CONFIRM);
  assert.match(text(c), /Run every command without asking\?/);
  assert.match(text(c), /never saw/);
  assert.match(text(m.root), /sandbox still limits what a command can reach/);
  assert.equal(puts(f).length, 0, "nothing is saved before the confirmation");

  await press(m.root, "Cancel");
  assert.equal(m.root.querySelector(CONFIRM), null);
  assert.equal(radio(m.root, "ask").checked, true);

  await pick(m.root, "all");
  await pick(m.root, "ask");
  assert.equal(m.root.querySelector(CONFIRM), null);
  assert.equal(puts(f).length, 0);

  await pick(m.root, "all");
  await press(m.root, "Allow all commands");
  assert.deepEqual(puts(f)[0].body, {
    approve_all_commands: true,
    confirm: true,
  });
  assert.equal(radio(m.root, "all").checked, true);

  await pick(m.root, "all");
  assert.equal(puts(f).length, 1, "the current choice saves nothing");

  await pick(m.root, "ask");
  assert.deepEqual(puts(f)[1].body, {
    approve_all_commands: false,
    confirm: false,
  });
  assert.equal(radio(m.root, "ask").checked, true);
  await m.unmount();
});

test("an older gateway without the setting draws no choice, and no file means no change", async () => {
  const { approve_all_commands: _, ...old } = SEC;
  let m = await mount(gateway(old).api);
  assert.equal(radio(m.root, "ask"), null);
  await m.unmount();

  m = await mount(gateway({ ...SEC, can_save: false }).api);
  assert.equal(radio(m.root, "all").disabled, true);
  await m.unmount();
});

test("a save the gateway refuses shows its error and keeps the choice", async () => {
  const f = fakeApi({
    "GET /v1/security": json(200, SEC),
    "PUT /v1/security": json(500, {
      error: "MOTITA_AGENT_APPROVE_ALL_COMMANDS overrides it",
    }),
  });
  const m = await mount(f.api);
  await pick(m.root, "all");
  await press(m.root, "Allow all commands");
  assert.match(
    text(m.root.querySelector("[data-testid=security-approve-error]")),
    /overrides it/,
  );
  assert.equal(radio(m.root, "ask").checked, true);
  await pick(m.root, "all");
  assert.equal(
    m.root.querySelector("[data-testid=security-approve-error]"),
    null,
    "a new pick clears it",
  );
  await m.unmount();
});

test("the client confirms only turning allowing all on", async () => {
  const f = fakeApi({ "PUT /v1/security": json(200, SEC) });
  assert.deepEqual(await sec.setApproveAllCommands(f.api, true), SEC);
  await sec.setApproveAllCommands(f.api, false);
  assert.deepEqual(f.calls[0].body, {
    approve_all_commands: true,
    confirm: true,
  });
  assert.deepEqual(f.calls[1].body, {
    approve_all_commands: false,
    confirm: false,
  });
});

test("every string of the choice has a Spanish translation", () => {
  const source = readFileSync(
    join(here, "..", "ApproveAllSetting.tsx"),
    "utf8",
  );
  const keys = new Set();
  for (const m of source.matchAll(
    /\bt[fc]?\(\s*(['"])((?:(?!\1)[^\\]|\\.)*)\1/g,
  )) {
    keys.add(m[2]);
  }
  const MIN_STRINGS = 8;
  assert.ok(keys.size >= MIN_STRINGS, String(keys.size));
  assert.deepEqual(
    [...keys].filter((k) => !es[k]),
    [],
  );
});

test("a gateway started with NO_NEW_PRIVS says sudo cannot work and how to fix it", async () => {
  let m = await mount(gateway({ ...SEC, no_new_privs: true }).api);
  const note = m.root.querySelector("[data-testid=security-nnp]");
  assert.match(text(note), /sudo cannot work here/);
  assert.match(text(note), /NoNewPrivileges=yes/);
  assert.ok(es[note.textContent.trim()], "the note has a Spanish text");
  await m.unmount();

  m = await mount(gateway(SEC).api);
  assert.equal(m.root.querySelector("[data-testid=security-nnp]"), null);
  await m.unmount();
});
