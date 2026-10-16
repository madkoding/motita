// The Security tab's choice of whether Motita's commands run without a network: isolating is the
// default, giving them the network needs a confirmation that spells out the risk, and taking it
// back needs none.
// Run with: node --test web/src/__tests__/networkisolation.test.mjs
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
  enforced: true,
  ask_unconfined_runs: true,
  isolate_network: true,
  network_enforced: true,
};
const NAME = "settings-isolate-network";
const CONFIRM = "[data-testid=security-network-confirm]";
const ERROR = "[data-testid=security-network-error]";

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
      if (body.isolate_network === false && body.confirm !== true) {
        return json(400, { error: "confirm is required" });
      }
      state = { ...state, isolate_network: body.isolate_network };
      return json(200, state);
    },
  });
}

const puts = (f) => f.calls.filter((c) => c.method === "PUT");

test("isolating is the default, and giving the network waits for a confirmation", async () => {
  const f = gateway(SEC);
  const m = await mount(f.api);
  assert.equal(radio(m.root, "isolate").checked, true);
  assert.match(text(m.root), /run without a network/);
  assert.doesNotMatch(text(m.root), /cannot take the network away/);

  await pick(m.root, "isolate");
  assert.equal(m.root.querySelector(CONFIRM), null, "the current choice asks nothing");

  await pick(m.root, "open");
  const c = m.root.querySelector(CONFIRM);
  assert.match(text(c), /Give Motita's commands the network\?/);
  assert.match(text(c), /could send it out/);
  assert.match(text(m.root), /could send them anywhere/);
  assert.equal(puts(f).length, 0, "nothing is saved before the confirmation");

  await press(m.root, "Cancel");
  assert.equal(m.root.querySelector(CONFIRM), null);
  assert.equal(radio(m.root, "isolate").checked, true);

  await pick(m.root, "open");
  await pick(m.root, "isolate");
  assert.equal(m.root.querySelector(CONFIRM), null, "picking the current choice drops the question");
  assert.equal(puts(f).length, 0);

  await pick(m.root, "open");
  await press(m.root, "Allow the network");
  assert.deepEqual(puts(f)[0].body, { isolate_network: false, confirm: true });
  assert.equal(radio(m.root, "open").checked, true);

  await pick(m.root, "isolate");
  assert.deepEqual(puts(f)[1].body, { isolate_network: true, confirm: false });
  assert.equal(radio(m.root, "isolate").checked, true);
  await m.unmount();
});

test("where the machine cannot isolate the choice says it changes nothing", async () => {
  const m = await mount(gateway({ ...SEC, network_enforced: false }).api);
  assert.match(text(m.root), /cannot take the network away/);
  await m.unmount();
});

test("where the machine says why it cannot, the choice shows its words", async () => {
  const reason = "unprivileged user namespaces are refused";
  const m = await mount(gateway({ ...SEC, network_enforced: false, network_reason: reason }).api);
  assert.match(text(m.root), /cannot give a command a network of its own \(unprivileged user namespaces are refused\)/);
  assert.doesNotMatch(text(m.root), /it needs Linux/);
  await m.unmount();
});

test("where the profile would fix it the choice says which command to run", async () => {
  const hint = "to allow it, run once: sudo motita sandbox apparmor install";
  const m = await mount(gateway({ ...SEC, network_enforced: false, network_hint: hint }).api);
  assert.match(text(m.root), /sudo motita sandbox apparmor install/);
  assert.doesNotMatch(text(m.root), /it needs Linux/);
  await m.unmount();
});

test("an older gateway without the setting draws no choice, and no file means no change", async () => {
  const { isolate_network: _, network_enforced: __, ...old } = SEC;
  let m = await mount(gateway(old).api);
  assert.equal(radio(m.root, "isolate"), null);
  await m.unmount();

  m = await mount(gateway({ ...SEC, can_save: false }).api);
  assert.equal(radio(m.root, "open").disabled, true);
  await m.unmount();
});

test("a save the gateway refuses shows its error and keeps the choice", async () => {
  const f = fakeApi({
    "GET /v1/security": json(200, SEC),
    "PUT /v1/security": json(500, { error: "MOTITA_SANDBOX_ISOLATE_NETWORK overrides it" }),
  });
  const m = await mount(f.api);
  await pick(m.root, "open");
  await press(m.root, "Allow the network");
  assert.match(text(m.root.querySelector(ERROR)), /overrides it/);
  assert.equal(radio(m.root, "isolate").checked, true);
  await pick(m.root, "open");
  assert.equal(m.root.querySelector(ERROR), null, "a new pick clears it");
  await m.unmount();
});

test("the client confirms only giving the network", async () => {
  const f = fakeApi({ "PUT /v1/security": json(200, SEC) });
  assert.deepEqual(await sec.setIsolateNetwork(f.api, false), SEC);
  await sec.setIsolateNetwork(f.api, true);
  assert.deepEqual(f.calls[0].body, { isolate_network: false, confirm: true });
  assert.deepEqual(f.calls[1].body, { isolate_network: true, confirm: false });
});

test("every string of the choice has a Spanish translation", () => {
  const source = readFileSync(join(here, "..", "NetworkIsolationSetting.tsx"), "utf8");
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
