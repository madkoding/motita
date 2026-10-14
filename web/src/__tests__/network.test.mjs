// The Network section of the settings dialog: who can connect to the gateway (saved for the next
// start, never without a confirmation that says what it implies) and Tailscale. Runs the real
// dialog in jsdom, bundled with esbuild like settingsmodal.test.mjs, against a fake gateway.
// Run with: node --test web/src/__tests__/network.test.mjs
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

const out = mkdtempSync(join(here, ".nw-"));
await build({
  entryPoints: [join(here, "..", "Settings.tsx")],
  outfile: join(out, "nw.mjs"),
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
const { SettingsModal, restartTiming } = await import(
  pathToFileURL(join(out, "nw.mjs")).href
);
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");
rmSync(out, { recursive: true });
const net = await import("../networkApi.ts");
const es = (await import("../i18n.es.ts")).default;

const HTTP_OK = 200;
const HTTP_ACCEPTED = 202;
const HTTP_BAD_REQUEST = 400;
const HTTP_NOT_FOUND = 404;
const HTTP_CONFLICT = 409;
const HTTP_BAD_GATEWAY = 502;
const LOCAL_LISTEN = "127.0.0.1:7477";
const ANY_LISTEN = "0.0.0.0:7477";
const CANNOT_RESTART = "cannot restart here";
const ARIA_LABELLEDBY = "aria-labelledby";
const NETWORK_PATH = "/v1/network";
const RESTART_NOTICE = "[data-testid=network-restart]";
const NETWORK_ERROR = "[data-testid=network-error]";
const TAILSCALE_URL = "[data-testid=tailscale-url]";
const RESTARTING = "[data-testid=network-restarting]";
const UNTIL_TRIES = 200;
const UNTIL_STEP_MS = 5;
const HEALTHY_AFTER = 3;
const REFUSED_WAIT_MS = 20;

const TS_RUNNING = {
  installed: true,
  running: true,
  state: "Running",
  dns_name: "box.tail1234.ts.net",
  serving: false,
  url: "https://box.tail1234.ts.net",
  error: "",
};

function netView(over = {}) {
  return {
    mode: "local",
    listen: LOCAL_LISTEN,
    active_mode: "local",
    active_listen: LOCAL_LISTEN,
    restart_needed: false,
    can_save: true,
    tailscale: TS_RUNNING,
    ...over,
  };
}

const json = (body, status = HTTP_OK) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

// parsedBody reads a request's JSON body, or undefined when it sent none.
function parsedBody(body) {
  if (!body) return undefined;
  return JSON.parse(body);
}

const LISTEN_OF = {
  local: LOCAL_LISTEN,
  lan: ANY_LISTEN,
  all: ANY_LISTEN,
};

// Each handler takes the gateway's state and the request body, and returns the next state and
// the response, the way the Go side answers.
const ROUTES = {
  "GET /v1/network": (state) => [state, json(state)],
  "PUT /v1/network": (state, body) => {
    if (body?.confirm !== true) {
      return [state, json({ error: "confirm is required" }, HTTP_BAD_REQUEST)];
    }
    const next = {
      ...state,
      mode: body.mode,
      listen: LISTEN_OF[body.mode],
      restart_needed: body.mode !== state.active_mode,
    };
    return [next, json(next)];
  },
  "POST /v1/network/restart": (state, body) => {
    if (body?.confirm !== true) {
      return [state, json({ error: "confirm is required" }, HTTP_BAD_REQUEST)];
    }
    if (!state.can_restart) {
      return [state, json({ error: CANNOT_RESTART }, HTTP_CONFLICT)];
    }
    return [state, json(state, HTTP_ACCEPTED)];
  },
  "POST /v1/network/tailscale": (state, body) => {
    const next = {
      ...state,
      tailscale: { ...state.tailscale, serving: body.enable },
    };
    return [next, json(next)];
  },
};

// fakeGateway answers the network API the way the Go side does, and records every request.
function fakeGateway(initial, overrides = {}) {
  let state = initial;
  const calls = [];
  const api = async (path, init = {}) => {
    const method = init.method || "GET";
    const body = parsedBody(init.body);
    calls.push({ path, method, body, headers: init.headers });
    const key = method + " " + path;
    if (overrides[key]) {
      return overrides[key](body);
    }
    const route = ROUTES[key];
    if (!route) {
      return json({ error: "not found" }, HTTP_NOT_FOUND);
    }
    const [next, response] = route(state, body);
    state = next;
    return response;
  };
  return { api, calls };
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 0)));

async function open(api) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => {
    render(
      h(SettingsModal, {
        langSetting: "auto",
        langAvailable: true,
        onLang: () => {},
        onClose: () => {},
        network: { api },
        tab: "network",
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

const CARD = "[data-testid=network-setting]";
const CONFIRM = "[data-testid=network-confirm]";
const radio = (root, value) =>
  root.querySelector(
    `input[type=radio][name=settings-network][value=${value}]`,
  );
const buttonNamed = (root, label) =>
  [...root.querySelectorAll(`${CONFIRM} button`)].find(
    (b) => b.textContent === label,
  );

async function pick(root, value) {
  const r = radio(root, value);
  r.checked = true;
  await act(() => r.dispatchEvent(new Event("change", { bubbles: true })));
}

async function click(el) {
  await act(() => el.click());
  await settle();
}

test("the section offers the three modes, local (the safest) selected", async () => {
  const gw = fakeGateway(netView());
  const { root, done } = await open(gw.api);
  const card = root.querySelector(CARD);
  assert.ok(card, "the network section is drawn");
  const group = card.querySelector("[role=radiogroup]");
  assert.equal(group.getAttribute(ARIA_LABELLEDBY), "settings-network");
  assert.ok(root.querySelector("#settings-network"), "the label it names");
  const radios = [...card.querySelectorAll("input[type=radio]")];
  assert.deepEqual(
    radios.map((r) => r.value),
    ["local", "lan", "all"],
  );
  assert.deepEqual(
    radios.map((r) => r.parentElement.textContent),
    ["This machine only", "Local network", "Everywhere"],
  );
  assert.equal(radio(root, "local").checked, true);
  assert.match(card.textContent, /Safest/);
  assert.equal(root.querySelector(CONFIRM), null, "nothing asked yet");
  assert.deepEqual(
    gw.calls.map((c) => c.method + " " + c.path),
    ["GET /v1/network"],
  );
  await done();
});

test("a mode is never saved without a confirmation that says what it implies", async () => {
  const gw = fakeGateway(netView());
  const { root, done } = await open(gw.api);
  await pick(root, "all");
  const confirm = root.querySelector(CONFIRM);
  assert.ok(confirm, "picking another mode asks first");
  assert.equal(confirm.tagName, "FIELDSET");
  assert.ok(confirm.getAttribute(ARIA_LABELLEDBY));
  const text = confirm.textContent;
  assert.match(text, /plain HTTP/);
  assert.match(text, /unencrypted/);
  assert.match(text, /Anyone who can reach this machine/);
  assert.match(text, /only thing protecting/);
  assert.match(text, /motita gateway stop, then motita gateway start/);
  assert.equal(
    document.activeElement,
    buttonNamed(root, "Cancel"),
    "Cancel has the focus: Enter alone never applies it",
  );
  assert.ok(
    gw.calls.every((c) => c.method === "GET"),
    "nothing was sent before the confirmation",
  );

  // Cancel puts the saved mode back and sends nothing.
  await click(buttonNamed(root, "Cancel"));
  assert.equal(root.querySelector(CONFIRM), null);
  assert.equal(radio(root, "local").checked, true);
  assert.equal(radio(root, "all").checked, false);
  assert.ok(gw.calls.every((c) => c.method === "GET"));
  await done();
});

test("Confirm sends PUT with confirm:true and the restart notice names the running mode", async () => {
  const gw = fakeGateway(netView());
  const { root, done } = await open(gw.api);
  await pick(root, "lan");
  assert.match(
    root.querySelector(CONFIRM).textContent,
    /Devices on your local network/,
  );
  await click(buttonNamed(root, "Confirm"));
  const put = gw.calls.find((c) => c.method === "PUT");
  assert.ok(put, "the change is sent once confirmed");
  assert.equal(put.path, NETWORK_PATH);
  assert.deepEqual(put.body, { mode: "lan", confirm: true });
  assert.equal(
    new Headers(put.headers).get("Content-Type"),
    "application/json",
  );
  assert.equal(root.querySelector(CONFIRM), null);
  assert.equal(radio(root, "lan").checked, true, "the saved mode is shown");
  const notice = root.querySelector(RESTART_NOTICE);
  assert.ok(notice, "the restart notice shows");
  assert.equal(notice.tagName, "OUTPUT");
  assert.match(notice.textContent, /Saved\. Applies when the gateway restarts/);
  assert.match(notice.textContent, /This machine only \(127\.0\.0\.1:7477\)/);
  await done();
});

test("going back to this machine only warns that other devices lose access", async () => {
  const gw = fakeGateway(
    netView({
      mode: "all",
      listen: ANY_LISTEN,
      active_mode: "all",
      active_listen: ANY_LISTEN,
    }),
  );
  const { root, done } = await open(gw.api);
  assert.equal(radio(root, "all").checked, true);
  await pick(root, "local");
  assert.match(
    root.querySelector(CONFIRM).textContent,
    /Other devices will lose access/,
  );
  await click(buttonNamed(root, "Confirm"));
  assert.deepEqual(gw.calls.find((c) => c.method === "PUT").body, {
    mode: "local",
    confirm: true,
  });
  assert.ok(root.querySelector(RESTART_NOTICE));
  await done();
});

test("a custom address selects nothing and says it was set by hand", async () => {
  const gw = fakeGateway(
    netView({ mode: "custom", listen: "192.168.1.5:9000" }),
  );
  const { root, done } = await open(gw.api);
  for (const v of ["local", "lan", "all"]) {
    assert.equal(radio(root, v).checked, false, v);
  }
  assert.match(
    root.querySelector(CARD).textContent,
    /Set by hand in the configuration: 192\.168\.1\.5:9000/,
  );
  await done();
});

test("without a configuration file the choice is read-only, with a hint", async () => {
  const gw = fakeGateway(netView({ can_save: false }));
  const { root, done } = await open(gw.api);
  assert.ok(radio(root, "local").disabled);
  assert.ok(radio(root, "all").disabled);
  assert.match(
    root.querySelector(CARD).textContent,
    /without a configuration file/,
  );
  await done();
});

test("a save the gateway refuses shows its error inline", async () => {
  const gw = fakeGateway(netView(), {
    "PUT /v1/network": () =>
      json({ error: "no configuration file" }, HTTP_CONFLICT),
  });
  const { root, done } = await open(gw.api);
  await pick(root, "lan");
  await click(buttonNamed(root, "Confirm"));
  const err = root.querySelector(NETWORK_ERROR);
  assert.ok(err);
  assert.equal(err.getAttribute("role"), "alert");
  assert.equal(err.textContent, "no configuration file");
  assert.equal(radio(root, "local").checked, true, "the saved mode is back");
  assert.equal(root.querySelector(RESTART_NOTICE), null);
  await done();
});

test("an older gateway without the network API draws no section", async () => {
  const gw = fakeGateway(netView(), {
    "GET /v1/network": () => json({ error: "not found" }, HTTP_NOT_FOUND),
  });
  const { root, done } = await open(gw.api);
  assert.equal(root.querySelector(CARD), null);
  assert.equal(root.querySelector("#settings-network-h"), null);
  await done();
});

test("Tailscale not installed: says so, with a safe link to get it", async () => {
  const gw = fakeGateway(
    netView({
      tailscale: {
        installed: false,
        running: false,
        state: "",
        serving: false,
      },
    }),
  );
  const { root, done } = await open(gw.api);
  const card = root.querySelector(CARD);
  assert.match(card.textContent, /Not installed/);
  assert.match(card.textContent, /private https access/);
  const a = card.querySelector('a[href="https://tailscale.com/download"]');
  assert.ok(a, "the download link");
  assert.equal(a.getAttribute("target"), "_blank");
  assert.match(a.getAttribute("rel"), /noopener/);
  assert.equal(card.querySelector("input[role=switch]"), null);
  await done();
});

test("Tailscale installed but not connected: says so with its state", async () => {
  const gw = fakeGateway(
    netView({
      tailscale: {
        installed: true,
        running: false,
        state: "NeedsLogin",
        serving: false,
      },
    }),
  );
  const { root, done } = await open(gw.api);
  const card = root.querySelector(CARD);
  assert.match(
    card.textContent,
    /Tailscale is installed but not connected \(NeedsLogin\)/,
  );
  assert.equal(card.querySelector("input[role=switch]"), null);
  await done();
});

test("Tailscale running: publishing asks first, then shows the URL; off applies at once", async () => {
  const gw = fakeGateway(netView());
  const { root, done } = await open(gw.api);
  const sw = () => root.querySelector(`${CARD} input[role=switch]`);
  assert.ok(sw(), "the publish switch");
  assert.equal(sw().getAttribute(ARIA_LABELLEDBY), "settings-tailscale");
  assert.equal(
    root.querySelector("#settings-tailscale").textContent,
    "Publish in my tailnet (https)",
  );
  assert.equal(sw().checked, false);

  sw().checked = true;
  await act(() => sw().dispatchEvent(new Event("change", { bubbles: true })));
  const confirm = root.querySelector(CONFIRM);
  assert.ok(confirm, "turning it on asks first");
  assert.match(confirm.textContent, /tailscale serve/);
  assert.match(confirm.textContent, /only to the devices in your tailnet/);
  assert.match(confirm.textContent, /not to the internet/);
  assert.equal(gw.calls.filter((c) => c.method === "POST").length, 0);

  await click(buttonNamed(root, "Publish"));
  const post = gw.calls.find((c) => c.method === "POST");
  assert.equal(post.path, "/v1/network/tailscale");
  assert.deepEqual(post.body, { enable: true });
  assert.equal(sw().checked, true);
  const link = root.querySelector(TAILSCALE_URL);
  assert.equal(link.getAttribute("href"), "https://box.tail1234.ts.net");
  assert.match(link.getAttribute("rel"), /noopener/);

  sw().checked = false;
  await act(() => sw().dispatchEvent(new Event("change", { bubbles: true })));
  await settle();
  const posts = gw.calls.filter((c) => c.method === "POST");
  assert.equal(posts.length, 2, "turning it off needs no confirmation");
  assert.deepEqual(posts[1].body, { enable: false });
  assert.equal(root.querySelector(TAILSCALE_URL), null);
  await done();
});

test("a failed tailscale command shows the gateway's message", async () => {
  const msg = "tailscale serve: not running";
  const gw = fakeGateway(netView(), {
    "POST /v1/network/tailscale": () => json({ error: msg }, HTTP_BAD_GATEWAY),
  });
  const { root, done } = await open(gw.api);
  const sw = root.querySelector(`${CARD} input[role=switch]`);
  sw.checked = true;
  await act(() => sw.dispatchEvent(new Event("change", { bubbles: true })));
  await click(buttonNamed(root, "Publish"));
  assert.equal(root.querySelector(NETWORK_ERROR).textContent, msg);
  assert.equal(root.querySelector("[data-testid=operator-command]"), null);
  assert.equal(root.querySelector(`${CARD} input[role=switch]`).checked, false);
  await done();
});

test("a denied tailscale serve sets the operator command apart, with Copy", async () => {
  const msg =
    "tailscale serve: access denied. Allow your user to change it, once: sudo tailscale set --operator=$USER";
  const gw = fakeGateway(netView(), {
    "POST /v1/network/tailscale": () => json({ error: msg }, HTTP_BAD_GATEWAY),
  });
  const { root, done } = await open(gw.api);
  const sw = root.querySelector(`${CARD} input[role=switch]`);
  sw.checked = true;
  await act(() => sw.dispatchEvent(new Event("change", { bubbles: true })));
  await click(buttonNamed(root, "Publish"));
  const err = root.querySelector(NETWORK_ERROR);
  assert.equal(err.getAttribute("role"), "alert");
  assert.ok(
    err.textContent.startsWith(
      "tailscale serve: access denied. Allow your user to change it, once:",
    ),
  );
  const code = err.querySelector("code[data-testid=operator-command]");
  assert.ok(code, "the command is in a code element");
  assert.equal(code.textContent, "sudo tailscale set --operator=$USER");
  assert.match(err.textContent, /Then turn the switch on again\./);
  const copy = [...err.querySelectorAll("button")].find(
    (b) => b.textContent === "Copy",
  );
  assert.ok(copy, "a Copy button next to the command");
  let copied = "";
  Object.defineProperty(globalThis.navigator, "clipboard", {
    value: {
      writeText: async (t) => {
        copied = t;
      },
    },
    configurable: true,
  });
  try {
    await click(copy);
    assert.equal(copied, "sudo tailscale set --operator=$USER");
    assert.equal(copy.textContent, "Copied");
  } finally {
    delete globalThis.navigator.clipboard;
  }
  await done();
});

test("a URL that is not https is never linked", async () => {
  const gw = fakeGateway(
    netView({
      tailscale: { ...TS_RUNNING, serving: true, url: "javascript:alert(1)" },
    }),
  );
  const { root, done } = await open(gw.api);
  assert.equal(root.querySelector(TAILSCALE_URL), null);
  assert.equal(root.querySelector(`${CARD} a[href^=javascript]`), null);
  await done();
});

test("the client hits the documented endpoints", async () => {
  const gw = fakeGateway(netView());
  assert.equal((await net.getNetwork(gw.api)).mode, "local");
  assert.equal((await net.setNetworkMode(gw.api, "all")).mode, "all");
  assert.equal((await net.setTailscale(gw.api, true)).tailscale.serving, true);
  assert.deepEqual(
    gw.calls.map((c) => [c.method, c.path, c.body]),
    [
      ["GET", NETWORK_PATH, undefined],
      ["PUT", NETWORK_PATH, { mode: "all", confirm: true }],
      ["POST", "/v1/network/tailscale", { enable: true }],
    ],
  );
  const old = fakeGateway(netView(), {
    "GET /v1/network": () => json({}, HTTP_NOT_FOUND),
  });
  assert.equal(await net.getNetwork(old.api), null, "404 means no section");
  const refused = fakeGateway(netView(), {
    "PUT /v1/network": () => json({ error: "nope" }, HTTP_CONFLICT),
  });
  await assert.rejects(net.setNetworkMode(refused.api, "lan"), (e) => {
    assert.equal(e.status, HTTP_CONFLICT);
    assert.equal(e.message, "nope");
    return true;
  });
});

// fastRestart makes the wait after a restart quick, and stands in for the health check and the
// page reload. It returns what it saw, and a function that puts the real ones back.
function fastRestart({ healthy = () => true, waitMs = 1000 } = {}) {
  const saved = { ...restartTiming };
  const seen = { health: 0, reloads: 0 };
  Object.assign(restartTiming, {
    firstPollMs: 1,
    pollMs: 1,
    waitMs,
    health: async () => {
      seen.health++;
      if (!healthy(seen.health)) {
        throw new TypeError("Failed to fetch");
      }
      return { ok: true };
    },
    reload: () => {
      seen.reloads++;
    },
  });
  return { seen, restore: () => Object.assign(restartTiming, saved) };
}

async function until(cond, what) {
  for (let i = 0; i < UNTIL_TRIES; i++) {
    if (cond()) {
      return;
    }
    await act(() => new Promise((r) => setTimeout(r, UNTIL_STEP_MS)));
  }
  assert.fail("timed out waiting for " + what);
}

const RESTART_NOW = "[data-testid=network-restart-now]";
const PENDING = {
  mode: "lan",
  listen: ANY_LISTEN,
  restart_needed: true,
};

test("Restart now shows only when a restart is needed and the gateway can restart itself", async () => {
  for (const [over, want] of [
    [{ ...PENDING, can_restart: true }, true],
    [{ ...PENDING, can_restart: false }, false],
    [{ ...PENDING }, false],
    [{ can_restart: true }, false],
  ]) {
    const gw = fakeGateway(netView(over));
    const { root, done } = await open(gw.api);
    assert.equal(!!root.querySelector(RESTART_NOW), want, JSON.stringify(over));
    const notice = root.querySelector(RESTART_NOTICE);
    if (over.restart_needed && !over.can_restart) {
      assert.match(
        notice.textContent,
        /motita gateway stop, then motita gateway start/,
        "without a self restart it says how to restart by hand",
      );
    }
    await done();
  }
});

test("Restart now asks first, sends confirm:true, waits for the gateway and reloads", async () => {
  const fast = fastRestart({ healthy: (n) => n >= HEALTHY_AFTER });
  try {
    const gw = fakeGateway(netView({ ...PENDING, can_restart: true }));
    const { root, done } = await open(gw.api);
    await click(root.querySelector(RESTART_NOW));
    const confirm = root.querySelector(CONFIRM);
    assert.ok(confirm, "it asks first");
    assert.match(confirm.textContent, /restarts to apply the saved setting/);
    assert.match(confirm.textContent, /saved sessions resume/);
    assert.match(confirm.textContent, /This machine only/);
    assert.match(confirm.textContent, /open it from the gateway's machine/);
    assert.equal(document.activeElement, buttonNamed(root, "Cancel"));
    assert.ok(
      gw.calls.every((c) => c.method === "GET"),
      "nothing sent yet",
    );

    await click(buttonNamed(root, "Cancel"));
    assert.equal(root.querySelector(CONFIRM), null);
    assert.ok(gw.calls.every((c) => c.method === "GET"));

    await click(root.querySelector(RESTART_NOW));
    await click(buttonNamed(root, "Restart"));
    const post = gw.calls.find((c) => c.method === "POST");
    assert.equal(post.path, "/v1/network/restart");
    assert.deepEqual(post.body, { confirm: true });
    assert.equal(
      new Headers(post.headers).get("Content-Type"),
      "application/json",
    );
    const status = root.querySelector(RESTARTING);
    assert.ok(status);
    assert.equal(status.tagName, "OUTPUT");
    assert.equal(status.textContent, "Restarting the gateway…");
    await until(() => fast.seen.reloads > 0, "the reload");
    assert.equal(
      fast.seen.health,
      HEALTHY_AFTER,
      "it polled until the gateway answered",
    );
    assert.equal(fast.seen.reloads, 1);
    await done();
  } finally {
    fast.restore();
  }
});

test("a gateway that does not come back says where to open it", async () => {
  const fast = fastRestart({ healthy: () => false, waitMs: 30 });
  try {
    const gw = fakeGateway(netView({ ...PENDING, can_restart: true }));
    const { root, done } = await open(gw.api);
    await click(root.querySelector(RESTART_NOW));
    await click(buttonNamed(root, "Restart"));
    const status = () => root.querySelector(RESTARTING);
    await until(
      () => /did not come back/.test(status().textContent),
      "giving up",
    );
    assert.equal(
      status().textContent,
      "The gateway did not come back here. If you changed who can reach it, open it from an address that the new setting allows.",
    );
    assert.ok(fast.seen.health > 0);
    assert.equal(fast.seen.reloads, 0);
    await done();
  } finally {
    fast.restore();
  }
});

test("a restart the gateway refuses shows its error and does not wait", async () => {
  const fast = fastRestart();
  try {
    const gw = fakeGateway(netView({ ...PENDING, can_restart: true }), {
      "POST /v1/network/restart": () =>
        json({ error: CANNOT_RESTART }, HTTP_CONFLICT),
    });
    const { root, done } = await open(gw.api);
    await click(root.querySelector(RESTART_NOW));
    await click(buttonNamed(root, "Restart"));
    const err = root.querySelector(NETWORK_ERROR);
    assert.ok(err);
    assert.equal(err.textContent, CANNOT_RESTART);
    assert.equal(root.querySelector(CONFIRM), null);
    assert.equal(root.querySelector(RESTARTING), null);
    await act(() => new Promise((r) => setTimeout(r, REFUSED_WAIT_MS)));
    assert.equal(fast.seen.health, 0);
    assert.equal(root.querySelector(RESTART_NOW).disabled, false);
    await done();
  } finally {
    fast.restore();
  }
});

test("restartGateway posts confirm:true and surfaces a refusal", async () => {
  const gw = fakeGateway(netView({ can_restart: true }));
  assert.equal((await net.restartGateway(gw.api)).mode, "local");
  assert.deepEqual(gw.calls.at(-1), {
    path: "/v1/network/restart",
    method: "POST",
    body: { confirm: true },
    headers: { "Content-Type": "application/json" },
  });
  const refused = fakeGateway(netView({ can_restart: false }));
  await assert.rejects(net.restartGateway(refused.api), (e) => {
    assert.equal(e.status, HTTP_CONFLICT);
    assert.equal(e.message, CANNOT_RESTART);
    return true;
  });
});

test("every string of the network section has a Spanish translation", () => {
  const network = readFileSync(join(here, "..", "NetworkSettings.tsx"), "utf8");
  const keys = new Set();
  for (const m of network.matchAll(
    /\bt[fc]?\(\s*(['"])((?:(?!\1)[^\\]|\\.)*)\1/g,
  )) {
    keys.add(m[2]);
  }
  const MIN_NETWORK_STRINGS = 25;
  assert.ok(keys.size > MIN_NETWORK_STRINGS, String(keys.size));
  assert.deepEqual(
    [...keys].filter((k) => !es[k]),
    [],
  );
  for (const k of keys) {
    const ph = (s) => (s.match(/\{\w+\}/g) || []).sort().join();
    assert.equal(ph(es[k]), ph(k), k);
  }
});
