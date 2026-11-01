// The Security tab's choice of how a task runs: in phases (the default) or direct, one conversation
// with native tools. Neither way needs a confirmation.
// Run with: node --test web/src/__tests__/directsetting.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  fakeApi,
  json,
  text,
  settle,
} from "./cov-settings-helpers.mjs";

installDom();
const { SecuritySettings } = await load("SecuritySettings.tsx");
const { h, render } = await import("preact");

const SEC = {
  disk_access: "workspace",
  can_save: true,
  enforced: false,
  direct: false,
};
const NAME = "settings-direct";

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

function gateway(initial, refuse = false) {
  let state = initial;
  return fakeApi({
    "GET /v1/security": () => json(200, state),
    "PUT /v1/security": (_p, init) => {
      if (refuse) {
        return json(500, { error: "the file is read-only" });
      }
      state = { ...state, direct: JSON.parse(init.body).direct };
      return json(200, state);
    },
  });
}

const puts = (f) => f.calls.filter((c) => c.method === "PUT");

test("phases are the default, and direct is saved without a confirmation", async () => {
  const f = gateway(SEC);
  const m = await mount(f.api);
  assert.equal(radio(m.root, "phased").checked, true);
  assert.match(text(m.root), /analysed and planned first/);
  await pick(m.root, "phased");
  assert.equal(puts(f).length, 0, "the current choice saves nothing");
  await pick(m.root, "direct");
  assert.deepEqual(puts(f)[0].body, { direct: true });
  assert.equal(radio(m.root, "direct").checked, true);
  assert.match(text(m.root), /like Claude Code/);
  await pick(m.root, "phased");
  assert.deepEqual(puts(f)[1].body, { direct: false });
  await m.unmount();
});

test("an older gateway draws no choice, and a refused save shows why", async () => {
  const { direct: _, ...old } = SEC;
  let m = await mount(gateway(old).api);
  assert.equal(radio(m.root, "phased"), null);
  await m.unmount();

  m = await mount(gateway(SEC, true).api);
  await pick(m.root, "direct");
  const err = m.root.querySelector("[data-testid=security-direct-error]");
  assert.match(text(err), /read-only/);
  assert.equal(radio(m.root, "phased").checked, true);
  await m.unmount();
});
