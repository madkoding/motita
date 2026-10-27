// The Security tab's choice of how long a question about a command waits for an answer: waiting is
// the default, a limit is saved at once, and a failed save is shown.
// Run with: node --test web/src/__tests__/approvaltimeout.test.mjs
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
  approval_timeout_minutes: 0,
};
const NAME = "settings-approval-timeout";

async function mount(api) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(SecuritySettings, { api }), root);
  await settle();
  return root;
}
const radio = (root, value) =>
  root.querySelector(`input[name=${NAME}][value="${value}"]`);
async function pick(root, value) {
  const r = radio(root, value);
  r.checked = true;
  r.dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
}
function gateway(initial, fail) {
  let state = initial;
  return fakeApi({
    "GET /v1/security": () => json(200, state),
    "PUT /v1/security": (_p, init) => {
      if (fail) return json(500, { error: "the file is read-only" });
      state = {
        ...state,
        approval_timeout_minutes: JSON.parse(init.body)
          .approval_timeout_minutes,
      };
      return json(200, state);
    },
  });
}

test("waiting is the default and a limit is saved at once", async () => {
  const f = gateway(SEC);
  const root = await mount(f.api);
  assert.equal(radio(root, "0").checked, true);
  assert.match(text(root), /waits until you answer it/);
  await pick(root, "15");
  const puts = f.calls.filter((c) => c.method === "PUT");
  assert.equal(puts.length, 1);
  assert.deepEqual(puts[0].body, { approval_timeout_minutes: 15 });
  assert.equal(radio(root, "15").checked, true);
  assert.match(text(root), /within 15 minutes is refused/);
});

test("a limit from the file the page does not offer is still shown", async () => {
  const f = gateway({ ...SEC, approval_timeout_minutes: 7 });
  const root = await mount(f.api);
  assert.equal(radio(root, "7").checked, true);
});

test("a failed save is shown", async () => {
  const f = gateway(SEC, true);
  const root = await mount(f.api);
  await pick(root, "5");
  assert.match(
    text(root.querySelector("[data-testid=security-timeout-error]")),
    /read-only/,
  );
});
