// The Security section of the settings dialog: how much of the disk Motita reaches. The workspace
// is the default, the whole disk is never given without a confirmation that spells out the risk,
// and going back needs none. Runs the real dialog in jsdom, bundled with esbuild like
// network.test.mjs, against a fake gateway.
// Run with: node --test web/src/__tests__/disksecurity.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { JSDOM } from "jsdom";
import { build } from "esbuild";

const here = dirname(fileURLToPath(import.meta.url));
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
globalThis.Notification = { permission: "granted" };
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(Date.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const out = mkdtempSync(join(here, ".sec-"));
await build({
  sourcemap: "inline",
  entryPoints: [join(here, "..", "Settings.tsx")],
  outfile: join(out, "sec.mjs"),
  bundle: true,
  format: "esm",
  jsx: "automatic",
  jsxImportSource: "preact",
  platform: "node",
  external: ["preact", "preact/*"],
  loader: { ".css": "empty" },
  resolveExtensions: [".ts", ".tsx", ".mjs", ".js"],
  logLevel: "silent",
});
const { SettingsModal } = await import(
  pathToFileURL(join(out, "sec.mjs")).href
);
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });
const secApi = await import("../securityApi.ts");
const es = (await import("../i18n.es.ts")).default;

const HTTP_OK = 200;
const HTTP_BAD_REQUEST = 400;
const HTTP_NOT_FOUND = 404;
const HTTP_INTERNAL = 500;
const SECURITY_PATH = "/v1/security";
const CARD = "[data-testid=security-setting]";
const CONFIRM = "[data-testid=security-confirm]";
const ERROR = "[data-testid=security-error]";
const NOT_ENFORCED = "[data-testid=security-not-enforced]";

const json = (body, status = HTTP_OK) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const secView = (over = {}) => ({
  disk_access: "workspace",
  can_save: true,
  enforced: true,
  ...over,
});

// fakeGateway answers the security API the way the Go side does, and records every request.
function fakeGateway(initial, overrides = {}) {
  let state = initial;
  const calls = [];
  const api = async (path, init = {}) => {
    const method = init.method || "GET";
    let body;
    if (init.body) {
      body = JSON.parse(init.body);
    }
    calls.push({ path, method, body });
    const key = method + " " + path;
    if (overrides[key]) {
      return overrides[key](body);
    }
    if (key === "GET " + SECURITY_PATH) {
      return json(state);
    }
    if (key === "PUT " + SECURITY_PATH) {
      if (body.disk_access === "full" && body.confirm !== true) {
        return json({ error: "confirm is required" }, HTTP_BAD_REQUEST);
      }
      state = { ...state, disk_access: body.disk_access };
      return json(state);
    }
    return json({ error: "not found" }, HTTP_NOT_FOUND);
  };
  return { api, calls };
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 0)));

async function open(api, sessionId) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => {
    render(
      h(SettingsModal, {
        langSetting: "auto",
        langAvailable: true,
        onLang: () => {},
        onClose: () => {},
        security: { api, sessionId },
        tab: "security",
      }),
      root,
    );
  });
  await settle();
  const done = async () => {
    await act(() => render(null, root));
    root.remove();
  };
  return { root, done };
}

const radio = (root, value) =>
  root.querySelector(`input[type=radio][name=settings-disk][value=${value}]`);
const buttonNamed = (root, label) =>
  [...root.querySelectorAll(`${CONFIRM} button`)].find(
    (b) => b.textContent === label,
  );

async function pick(root, value) {
  const r = radio(root, value);
  r.checked = true;
  await act(() => r.dispatchEvent(new Event("change", { bubbles: true })));
  await settle();
}

async function click(el) {
  await act(() => el.click());
  await settle();
}

const puts = (calls) => calls.filter((c) => c.method === "PUT");

test("the Security tab offers the workspace (selected, the safest) and the whole disk", async () => {
  const gw = fakeGateway(secView());
  const { root, done } = await open(gw.api);
  assert.ok(root.querySelector("#settings-tab-security"));
  assert.ok(root.querySelector(CARD));
  assert.equal(radio(root, "workspace").checked, true);
  assert.equal(radio(root, "full").checked, false);
  assert.match(root.querySelector(CARD).textContent, /Safest/);
  assert.equal(root.querySelector(NOT_ENFORCED), null);
  assert.match(root.querySelector(CARD).textContent, /logins and keys/);
  await done();
});

test("the whole disk is never saved without a confirmation that spells out the risk", async () => {
  const gw = fakeGateway(secView());
  const { root, done } = await open(gw.api);
  await pick(root, "full");
  const confirm = root.querySelector(CONFIRM);
  assert.ok(confirm, "a confirmation is shown");
  assert.match(confirm.textContent, /whole disk/);
  assert.match(confirm.textContent, /delete any file/);
  assert.match(confirm.textContent, /leak your files/);
  assert.equal(puts(gw.calls).length, 0, "nothing is saved before Confirm");
  assert.equal(document.activeElement?.textContent, "Cancel");

  await click(buttonNamed(root, "Cancel"));
  assert.equal(root.querySelector(CONFIRM), null);
  assert.equal(radio(root, "workspace").checked, true);
  assert.equal(puts(gw.calls).length, 0);

  await pick(root, "full");
  await click(buttonNamed(root, "Allow the whole disk"));
  assert.deepEqual(puts(gw.calls)[0].body, {
    disk_access: "full",
    confirm: true,
  });
  assert.equal(root.querySelector(CONFIRM), null);
  assert.equal(radio(root, "full").checked, true);
  assert.match(root.querySelector(CARD).textContent, /any file your user/);
  await done();
});

test("going back to the workspace applies at once, without a question", async () => {
  const gw = fakeGateway(secView({ disk_access: "full" }));
  const { root, done } = await open(gw.api);
  assert.equal(radio(root, "full").checked, true);
  await pick(root, "full");
  assert.equal(
    root.querySelector(CONFIRM),
    null,
    "the current choice asks nothing",
  );
  await pick(root, "workspace");
  assert.equal(root.querySelector(CONFIRM), null);
  assert.deepEqual(puts(gw.calls)[0].body, {
    disk_access: "workspace",
    confirm: false,
  });
  assert.equal(radio(root, "workspace").checked, true);
  await done();
});

test("picking the current choice while asking closes the question", async () => {
  const gw = fakeGateway(secView());
  const { root, done } = await open(gw.api);
  await pick(root, "full");
  assert.ok(root.querySelector(CONFIRM));
  await pick(root, "workspace");
  assert.equal(root.querySelector(CONFIRM), null);
  assert.equal(puts(gw.calls).length, 0);
  await done();
});

test("without kernel confinement the page says only file edits are limited", async () => {
  const gw = fakeGateway(secView({ enforced: false }));
  const { root, done } = await open(gw.api);
  assert.match(root.querySelector(NOT_ENFORCED).textContent, /Landlock/);
  assert.doesNotMatch(root.querySelector(CARD).textContent, /logins and keys/);
  await done();
});

test("without a configuration file the choice is read-only, with a hint", async () => {
  const gw = fakeGateway(secView({ can_save: false }));
  const { root, done } = await open(gw.api);
  assert.equal(radio(root, "full").disabled, true);
  assert.match(root.querySelector(CARD).textContent, /built-in defaults/);
  await done();
});

test("a save the gateway refuses shows its error inline", async () => {
  const gw = fakeGateway(secView(), {
    ["PUT " + SECURITY_PATH]: () =>
      json({ error: "MOTITA_SANDBOX_DISK_ACCESS overrides it" }, HTTP_INTERNAL),
  });
  const { root, done } = await open(gw.api);
  await pick(root, "full");
  await click(buttonNamed(root, "Allow the whole disk"));
  assert.match(root.querySelector(ERROR).textContent, /overrides it/);
  assert.equal(radio(root, "workspace").checked, true);
  await done();
});

test("a view that cannot be read shows the error, and an error without a message its status", async () => {
  const gw = fakeGateway(secView(), {
    ["GET " + SECURITY_PATH]: () =>
      new Response("nope", { status: HTTP_INTERNAL }),
  });
  const { root, done } = await open(gw.api);
  assert.match(root.querySelector(CARD).textContent, /HTTP 500/);
  assert.equal(radio(root, "workspace"), null);
  await done();
});

test("an older gateway without the security API draws no section", async () => {
  const gw = fakeGateway(secView(), {
    ["GET " + SECURITY_PATH]: () => json({}, HTTP_NOT_FOUND),
  });
  const { root, done } = await open(gw.api);
  assert.equal(root.querySelector(CARD), null);
  await done();
});

test("a page without the security API has no Security tab", async () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => {
    render(
      h(SettingsModal, {
        langSetting: "auto",
        langAvailable: true,
        onLang: () => {},
        onClose: () => {},
      }),
      root,
    );
  });
  assert.equal(root.querySelector("#settings-tab-security"), null);
  await act(() => render(null, root));
  root.remove();
});

test("the client sends confirm only for the whole disk", async () => {
  const gw = fakeGateway(secView());
  await secApi.setDiskAccess(gw.api, "full");
  await secApi.setDiskAccess(gw.api, "workspace");
  assert.deepEqual(
    gw.calls.map((c) => [c.method, c.path, c.body]),
    [
      ["PUT", SECURITY_PATH, { disk_access: "full", confirm: true }],
      ["PUT", SECURITY_PATH, { disk_access: "workspace", confirm: false }],
    ],
  );
  assert.equal(
    await secApi.getSecurity(async () => json({}, HTTP_NOT_FOUND)),
    null,
  );
});

test("every string of the security section has a Spanish translation", () => {
  const source = readFileSync(join(here, "..", "SecuritySettings.tsx"), "utf8");
  const keys = new Set();
  for (const m of source.matchAll(
    /\bt[fc]?\(\s*(['"])((?:(?!\1)[^\\]|\\.)*)\1/g,
  )) {
    keys.add(m[2]);
  }
  const MIN_SECURITY_STRINGS = 12;
  assert.ok(keys.size > MIN_SECURITY_STRINGS, String(keys.size));
  assert.deepEqual(
    [...keys].filter((k) => !es[k]),
    [],
  );
  assert.equal(es.Security, "Seguridad");
});

const SESSION_SCOPE = "[data-testid=security-session-scope]";

test("the Security tab shows where the open session works and what is kept from it", async () => {
  const scope = {
    workspace: "/w/worktrees/s1",
    project_dir: "/w/shop",
    home: "/t/homes/shop-1a2b",
    private_roots: ["/w"],
  };
  const gw = fakeGateway(secView(), {
    "GET /v1/sessions/s%201/scope": () => json(scope),
  });
  const { root, done } = await open(gw.api, "s 1");
  const note = root.querySelector(SESSION_SCOPE);
  assert.ok(note, "the session's scope is shown");
  for (const want of [scope.workspace, scope.project_dir, scope.home, "/w"]) {
    assert.ok(note.textContent.includes(want), want);
  }
  await done();

  const bare = fakeGateway(secView(), {
    "GET /v1/sessions/s2/scope": () =>
      json({
        workspace: "/w/sessions/s2",
        project_dir: "",
        home: "",
        private_roots: [],
      }),
  });
  const second = await open(bare.api, "s2");
  const items = second.root.querySelectorAll(`${SESSION_SCOPE} li`);
  assert.equal(
    items.length,
    1,
    "a free session without a tools HOME shows its folder only",
  );
  await second.done();

  const none = fakeGateway(secView());
  const third = await open(none.api, "gone");
  assert.equal(
    third.root.querySelector(SESSION_SCOPE),
    null,
    "a session the gateway cannot describe shows nothing",
  );
  await third.done();
  const noSession = await open(none.api);
  assert.equal(noSession.root.querySelector(SESSION_SCOPE), null);
  assert.equal(
    none.calls.filter((c) => c.path.endsWith("/scope")).length,
    1,
    "no session is not asked about",
  );
  await noSession.done();
});

test("getSessionScope passes a network failure on and asks nothing without a session", async () => {
  const failing = async () => {
    throw new Error("offline");
  };
  await assert.rejects(secApi.getSessionScope(failing, "s"));
  assert.equal(await secApi.getSessionScope(failing, ""), null);
});
