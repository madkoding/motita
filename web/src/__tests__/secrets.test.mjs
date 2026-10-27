// Settings → Secrets: the variables kept for the agent. A value goes to the gateway and never comes
// back, a name is validated before it is sent, and a gateway without the API hides the section.
// Run with: node --test web/src/__tests__/secrets.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  fakeApi,
  json,
  text,
  settle,
  click,
  type,
} from "./cov-settings-helpers.mjs";

installDom();
const { SecretsSettings } = await load("SecretsSettings.tsx");
const { SettingsModal } = await load("Settings.tsx");
const api = await import("../secretsApi.ts");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");

const VIEW = {
  can_save: true,
  max: 64,
  secrets: [{ name: "DEPLOY_TOKEN", hint: "1234" }, { name: "PIN" }],
};

async function mount(a) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(SecretsSettings, { api: a }), root);
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

function gateway(initial) {
  let state = structuredClone(initial);
  return fakeApi({
    "GET /v1/secrets": () => json(200, state),
    "PUT /v1/secrets/": (p, init) => {
      const name = decodeURIComponent(p.split("/").pop());
      if (name === "BAD_ONE") {
        return json(400, { error: "BAD_ONE is reserved for the sandbox" });
      }
      state.secrets = [
        ...state.secrets.filter((s) => s.name !== name),
        { name },
      ];
      return json(200, state);
    },
    "DELETE /v1/secrets/": (p) => {
      const name = decodeURIComponent(p.split("/").pop());
      state.secrets = state.secrets.filter((s) => s.name !== name);
      return json(200, state);
    },
  });
}

const q = (root, id) => root.querySelector(`[data-testid="${id}"]`);

test("it lists names with a hint, adds, replaces and removes a variable", async () => {
  const f = gateway(VIEW);
  const m = await mount(f.api);
  assert.match(
    text(q(m.root, "secrets-list")),
    /DEPLOY_TOKENSaved \(…1234\)Remove/,
  );
  assert.match(text(q(m.root, "secrets-list")), /PINSavedRemove/);
  assert.equal(q(m.root, "secret-save").disabled, true);

  type(q(m.root, "secret-name"), "1bad");
  type(q(m.root, "secret-value"), "x");
  await settle();
  assert.equal(
    q(m.root, "secret-save").disabled,
    true,
    "a bad name is not sent",
  );

  type(q(m.root, "secret-name"), "NEW_KEY");
  await settle();
  click(q(m.root, "secret-save"));
  await settle();
  const put = f.calls.find((c) => c.method === "PUT");
  assert.equal(put.path, "/v1/secrets/NEW_KEY");
  assert.deepEqual(put.body, { value: "x" });
  assert.match(text(q(m.root, "secrets-list")), /NEW_KEY/);
  assert.equal(q(m.root, "secret-name").value, "");
  assert.equal(q(m.root, "secret-value").value, "");

  click(q(m.root, "secret-remove-PIN"));
  await settle();
  assert.doesNotMatch(text(q(m.root, "secrets-list")), /PIN/);
  await m.unmount();
});

test("a refused save shows the reason and keeps what was typed", async () => {
  const m = await mount(gateway(VIEW).api);
  type(q(m.root, "secret-name"), "BAD_ONE");
  type(q(m.root, "secret-value"), "x");
  await settle();
  q(m.root, "secret-save").form.dispatchEvent(
    new window.Event("submit", { bubbles: true, cancelable: true }),
  );
  await settle();
  assert.match(text(q(m.root, "secrets-error")), /reserved for the sandbox/);
  assert.equal(q(m.root, "secret-name").value, "BAD_ONE");
  await m.unmount();
});

test("an empty list says so, and no file means no saving", async () => {
  const m = await mount(gateway({ can_save: false, max: 64, secrets: [] }).api);
  assert.ok(q(m.root, "secrets-empty"));
  assert.equal(q(m.root, "secret-name").disabled, true);
  assert.match(text(m.root), /cannot be saved here/);
  await m.unmount();
});

test("a gateway without the API hides the section, and a failing one says why", async () => {
  let m = await mount(fakeApi({ "GET /v1/secrets": json(404, {}) }).api);
  assert.equal(q(m.root, "secrets-setting"), null);
  await m.unmount();

  m = await mount(
    fakeApi({ "GET /v1/secrets": json(500, { error: "boom" }) }).api,
  );
  assert.match(text(q(m.root, "secrets-error")), /boom/);
  await m.unmount();

  m = await mount(fakeApi({ "GET /v1/secrets": json(500, {}) }).api);
  assert.match(text(q(m.root, "secrets-error")), /HTTP 500/);
  await m.unmount();
});

test("an answer that is not JSON still gives an error", async () => {
  const f = fakeApi({
    "GET /v1/secrets": new Response("oops", { status: 502 }),
  });
  await assert.rejects(api.getSecrets(f.api), /HTTP 502/);
});

test("the client encodes the name and the dictionary speaks Spanish", async () => {
  const f = fakeApi({
    "PUT /v1/secrets/": json(200, VIEW),
    "DELETE /v1/secrets/": json(200, VIEW),
  });
  await api.saveSecret(f.api, "A B", "v");
  await api.deleteSecret(f.api, "A B");
  assert.deepEqual(
    f.calls.map((c) => c.path),
    ["/v1/secrets/A%20B", "/v1/secrets/A%20B"],
  );
  for (const k of [
    "Secrets",
    "Variables for the agent",
    "No variables yet.",
    "Variable name",
    "Paste the value",
    "Remove {name}",
  ]) {
    assert.ok(es[k], k);
  }
});

test("the Secrets tab appears in Settings only when the page hands it an API", async () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const f = gateway(VIEW);
  render(
    h(SettingsModal, { onClose() {}, secrets: { api: f.api }, tab: "secrets" }),
    root,
  );
  await settle();
  assert.ok(root.querySelector("#settings-tab-secrets"));
  assert.ok(q(root, "secrets-setting"));
  render(h(SettingsModal, { onClose() {} }), root);
  await settle();
  assert.equal(root.querySelector("#settings-tab-secrets"), null);
  render(null, root);
});
