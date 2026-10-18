// The network and disk access sections of the settings: who can connect (each mode, with its
// confirmation), the restart the page can ask for and its wait for the gateway (mocked timers),
// Tailscale in each of its states, and the disk access switch with the session's scope.
// Run with: node --test web/src/__tests__/cov-settings-network.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
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
const { NetworkSettings, restartTiming } = await load("NetworkSettings.tsx");
const { SecuritySettings } = await load("SecuritySettings.tsx");
const { h, render } = await import("preact");

const VIEW = {
  mode: "local",
  listen: "127.0.0.1:7777",
  active_mode: "local",
  active_listen: "127.0.0.1:7777",
  restart_needed: false,
  can_save: true,
  can_restart: true,
  tailscale: {
    installed: true,
    running: true,
    state: "Running",
    serving: false,
  },
};

async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(vnode, root);
  await settle();
  return {
    root,
    async rerender(v) {
      render(v, root);
      await settle();
    },
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}

const radio = (root, name, value) =>
  root.querySelector(`input[name=${name}][value=${value}]`);
async function pick(root, name, value) {
  const r = radio(root, name, value);
  r.checked = true;
  r.dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
}
const confirmBox = (root, id = "network-confirm") =>
  root.querySelector(`[data-testid=${id}]`);
const btn = (root, label) => byText(root, "button", label);
async function press(el) {
  click(el);
  await settle();
}

// ── network ─────────────────────────────────────────────────────────────────

test("nothing shows on a gateway without the network API, or before it answers", async () => {
  const f = fakeApi({ "GET /v1/network": json(404, {}) });
  const m = await mount(h(NetworkSettings, { api: f.api }));
  assert.equal(m.root.innerHTML, "");
  await m.unmount();

  // An answer that arrives after the section closed is dropped.
  let answer;
  const slow = fakeApi({
    "GET /v1/network": () => new Promise((r) => (answer = r)),
  });
  const s = await mount(h(NetworkSettings, { api: slow.api }));
  assert.equal(s.root.innerHTML, "");
  await s.unmount();
  answer(json(200, VIEW));
  await settle();
  assert.equal(s.root.innerHTML, "");
});

test("a failed read says why, unless the section closed meanwhile", async () => {
  const f = fakeApi({
    "GET /v1/network": json(500, { error: "network api down" }),
  });
  const m = await mount(h(NetworkSettings, { api: f.api }));
  assert.equal(text(m.root.querySelector("[role=alert]")), "network api down");
  assert.equal(m.root.querySelector("input[name=settings-network]"), null);
  await m.unmount();

  let fail;
  const slow = fakeApi({
    "GET /v1/network": () => new Promise((_, r) => (fail = r)),
  });
  const s = await mount(h(NetworkSettings, { api: slow.api }));
  await s.unmount();
  fail(new Error("late"));
  await settle();
  assert.equal(s.root.innerHTML, "");
});

test("each mode explains itself, and a change asks first with what it implies", async () => {
  const f = fakeApi({
    "GET /v1/network": json(200, VIEW),
    "PUT /v1/network": (_p, init) =>
      json(200, {
        ...VIEW,
        mode: JSON.parse(init.body).mode,
        restart_needed: true,
      }),
  });
  const m = await mount(h(NetworkSettings, { api: f.api }));
  assert.match(text(m.root), /Safest: only this machine can open Motita/);
  assert.equal(radio(m.root, "settings-network", "local").checked, true);

  // Local network: the confirmation lists who gains access, and is a danger button.
  await pick(m.root, "settings-network", "lan");
  let c = confirmBox(m.root);
  assert.match(text(c), /Switch to “Local network”\?/);
  assert.match(
    text(c),
    /Devices on your local network will be able to reach the gateway/,
  );
  assert.match(text(c), /plain HTTP/);
  assert.match(
    text(m.root),
    /only private \(local network\) addresses may connect/,
  );
  assert.match(btn(c, "Confirm").className, /text-danger/);
  // Cancel has the focus, so Enter right away does not apply it.
  assert.equal(document.activeElement, btn(c, "Cancel"));

  // Everywhere: anyone who can reach the machine.
  await pick(m.root, "settings-network", "all");
  c = confirmBox(m.root);
  assert.match(text(c), /Switch to “Everywhere”\?/);
  assert.match(text(c), /Anyone who can reach this machine/);
  assert.match(text(m.root), /Listens on 0\.0\.0\.0/);

  // Picking the mode in force again drops the question.
  await pick(m.root, "settings-network", "local");
  assert.equal(confirmBox(m.root), null);

  // Cancel drops it too.
  await pick(m.root, "settings-network", "lan");
  await press(btn(confirmBox(m.root), "Cancel"));
  assert.equal(confirmBox(m.root), null);
  assert.equal(radio(m.root, "settings-network", "local").checked, true);

  // Confirm saves it; the saved mode waits for a restart.
  await pick(m.root, "settings-network", "all");
  await press(btn(confirmBox(m.root), "Confirm"));
  assert.deepEqual(f.calls.at(-1), {
    method: "PUT",
    path: "/v1/network",
    body: { mode: "all", confirm: true },
  });
  assert.equal(confirmBox(m.root), null);
  const notice = m.root.querySelector("[data-testid=network-restart]");
  assert.match(text(notice), /Saved\. Applies when the gateway restarts/);
  assert.match(
    text(notice),
    /Running now: This machine only \(127\.0\.0\.1:7777\)\./,
  );
  assert.equal(radio(m.root, "settings-network", "all").checked, true);
  await m.unmount();
});

test("going back to this machine only says other devices lose access; a refusal is shown", async () => {
  const f = fakeApi({
    "GET /v1/network": json(200, { ...VIEW, mode: "lan" }),
    "PUT /v1/network": json(409, { error: "config is read-only" }),
  });
  const m = await mount(h(NetworkSettings, { api: f.api }));
  await pick(m.root, "settings-network", "local");
  const c = confirmBox(m.root);
  assert.match(text(c), /Other devices will lose access/);
  assert.doesNotMatch(btn(c, "Confirm").className, /text-danger/);
  await press(btn(c, "Confirm"));
  assert.equal(
    text(m.root.querySelector("[data-testid=network-error]")),
    "config is read-only",
  );
  assert.equal(confirmBox(m.root), null);
  // A new pick clears the error.
  await pick(m.root, "settings-network", "all");
  assert.equal(m.root.querySelector("[data-testid=network-error]"), null);
  await m.unmount();
});

test("a custom address, a gateway without a configuration file, and one that cannot restart", async () => {
  const view = {
    ...VIEW,
    mode: "custom",
    listen: "10.1.2.3:9",
    active_mode: "custom",
    active_listen: "10.1.2.3:9",
    restart_needed: true,
    can_save: false,
    can_restart: false,
    tailscale: undefined,
  };
  const f = fakeApi({ "GET /v1/network": json(200, view) });
  const m = await mount(h(NetworkSettings, { api: f.api }));
  const t = text(m.root);
  assert.match(
    t,
    /Set by hand in the configuration: 10\.1\.2\.3:9\. Choosing an option replaces it\./,
  );
  assert.match(
    t,
    /without a configuration file, so this cannot be changed here/,
  );
  for (const v of ["local", "lan", "all"]) {
    assert.equal(radio(m.root, "settings-network", v).disabled, true);
    assert.equal(radio(m.root, "settings-network", v).checked, false);
  }
  assert.match(
    t,
    /Running now: Custom address \(10\.1\.2\.3:9\)\. Restart it with motita gateway stop/,
  );
  assert.equal(m.root.querySelector("[data-testid=network-restart-now]"), null);
  // Without Tailscale: where to get it.
  const get = byText(m.root, "a", "Get Tailscale");
  assert.equal(get.getAttribute("href"), "https://tailscale.com/download");
  assert.match(t, /Not installed/);
  await m.unmount();

  // A view without a mode at all is shown as a custom one.
  const g = fakeApi({
    "GET /v1/network": json(200, { ...VIEW, mode: undefined, listen: "x:1" }),
  });
  const n = await mount(h(NetworkSettings, { api: g.api }));
  assert.match(text(n.root), /Set by hand in the configuration: x:1/);
  await n.unmount();
});

test("Tailscale installed but not connected says so, with its state when known", async () => {
  for (const [state, want] of [
    ["NeedsLogin", "Tailscale is installed but not connected (NeedsLogin)."],
    ["", "Tailscale is installed but not connected."],
  ]) {
    const f = fakeApi({
      "GET /v1/network": json(200, {
        ...VIEW,
        tailscale: { installed: true, running: false, state, serving: false },
      }),
    });
    const m = await mount(h(NetworkSettings, { api: f.api }));
    assert.ok(text(m.root).includes(want));
    assert.equal(m.root.querySelector("input[role=switch]"), null);
    await m.unmount();
  }
});

test("publishing in the tailnet asks first; turning it off does not; the address is linked only when https", async () => {
  let serving = false;
  const f = fakeApi({
    "GET /v1/network": json(200, VIEW),
    "POST /v1/network/tailscale": (_p, init) => {
      serving = JSON.parse(init.body).enable;
      return json(200, {
        ...VIEW,
        tailscale: {
          ...VIEW.tailscale,
          serving,
          url: "https://box.tail.ts.net/",
        },
      });
    },
  });
  const m = await mount(h(NetworkSettings, { api: f.api }));
  assert.match(text(m.root), /Only the devices in your tailnet can open it/);
  const sw = () => m.root.querySelector("input[role=switch]");
  assert.equal(sw().checked, false);

  // On: a confirmation; the switch shows on while it asks. Turning it back off cancels.
  sw().checked = true;
  sw().dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
  assert.match(text(confirmBox(m.root)), /Publish in your tailnet\?/);
  assert.equal(sw().checked, true);
  sw().checked = false;
  sw().dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
  assert.equal(confirmBox(m.root), null);
  assert.equal(f.calls.length, 1);

  // Ask again and publish.
  sw().checked = true;
  sw().dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
  await press(btn(confirmBox(m.root), "Publish"));
  assert.deepEqual(f.calls.at(-1).body, { enable: true });
  const link = m.root.querySelector("[data-testid=tailscale-url]");
  assert.equal(link.getAttribute("href"), "https://box.tail.ts.net/");
  assert.match(text(m.root), /Published at https:\/\/box\.tail\.ts\.net\//);

  // Off: at once.
  sw().checked = false;
  sw().dispatchEvent(new window.Event("change", { bubbles: true }));
  await settle();
  assert.deepEqual(f.calls.at(-1).body, { enable: false });
  assert.equal(m.root.querySelector("[data-testid=tailscale-url]"), null);
  await m.unmount();

  // A served address that is not https is not linked.
  for (const url of ["http://box:7777/", undefined]) {
    const g = fakeApi({
      "GET /v1/network": json(200, {
        ...VIEW,
        tailscale: { ...VIEW.tailscale, serving: true, url },
      }),
    });
    const n = await mount(h(NetworkSettings, { api: g.api }));
    assert.equal(n.root.querySelector("[data-testid=tailscale-url]"), null);
    assert.equal(n.root.querySelector("input[role=switch]").checked, true);
    assert.match(text(n.root), /Only the devices in your tailnet/);
    await n.unmount();
  }
});

test("tailscale's own errors are shown, and the operator command can be copied", async () => {
  const realClip = Object.getOwnPropertyDescriptor(
    window.navigator,
    "clipboard",
  );
  const copied = [];
  Object.defineProperty(window.navigator, "clipboard", {
    configurable: true,
    value: { writeText: async (s) => copied.push(s) },
  });
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const opErr =
      "Access denied: serve config denied. To fix it run: sudo tailscale set --operator=$USER";
    const f = fakeApi({
      "GET /v1/network": json(200, {
        ...VIEW,
        tailscale: { ...VIEW.tailscale, error: "not logged in" },
      }),
      "POST /v1/network/tailscale": json(500, { error: opErr }),
    });
    const m = await mount(h(NetworkSettings, { api: f.api }));
    assert.match(text(m.root), /Tailscale: not logged in/);
    const sw = m.root.querySelector("input[role=switch]");
    sw.checked = true;
    sw.dispatchEvent(new window.Event("change", { bubbles: true }));
    await settle();
    await press(btn(confirmBox(m.root), "Publish"));
    const e = m.root.querySelector("[data-testid=network-error]");
    // The command is set apart from the message, with a copy button and the next step.
    assert.equal(
      e.firstChild.textContent,
      "Access denied: serve config denied. To fix it run:",
    );
    assert.match(text(e), /Then turn the switch on again\.$/);
    assert.equal(
      text(e.querySelector("[data-testid=operator-command]")),
      "sudo tailscale set --operator=$USER",
    );
    await press(btn(e, "Copy"));
    assert.deepEqual(copied, ["sudo tailscale set --operator=$USER"]);
    assert.ok(btn(e, "Copied"));
    mock.timers.tick(1800);
    await settle();
    assert.ok(btn(e, "Copy"));
    await m.unmount();
  } finally {
    mock.timers.reset();
    if (realClip) {
      Object.defineProperty(window.navigator, "clipboard", realClip);
    } else {
      delete window.navigator.clipboard;
    }
  }
});

test("an operator error without the command spelled out keeps its text; a failed copy stays Copy", async () => {
  const realClip = Object.getOwnPropertyDescriptor(
    window.navigator,
    "clipboard",
  );
  Object.defineProperty(window.navigator, "clipboard", {
    configurable: true,
    value: {
      writeText: async () => {
        throw new Error("denied");
      },
    },
  });
  document.execCommand = () => false;
  try {
    const f = fakeApi({
      "GET /v1/network": json(200, {
        ...VIEW,
        tailscale: { ...VIEW.tailscale, error: "use --operator to allow it" },
      }),
    });
    const m = await mount(h(NetworkSettings, { api: f.api }));
    const note = byText(m.root, "p", "Tailscale:");
    // The whole message stays, and the command follows it.
    assert.equal(
      note.firstChild.textContent,
      "Tailscale: use --operator to allow it",
    );
    assert.equal(
      text(note.querySelector("[data-testid=operator-command]")),
      "sudo tailscale set --operator=$USER",
    );
    await press(btn(note, "Copy"));
    assert.ok(btn(note, "Copy"));
    assert.equal(btn(note, "Copied"), undefined);
    await m.unmount();
  } finally {
    delete document.execCommand;
    if (realClip) {
      Object.defineProperty(window.navigator, "clipboard", realClip);
    } else {
      delete window.navigator.clipboard;
    }
  }
});

// restartView is a saved change waiting for a restart the gateway can do itself.
const restartView = { ...VIEW, mode: "lan", restart_needed: true };

function fakeHealth(answers) {
  const real = { ...restartTiming };
  const asked = [];
  let reloads = 0;
  restartTiming.health = async () => {
    asked.push(Date.now());
    const a = answers.shift();
    if (a instanceof Error) {
      throw a;
    }
    return a;
  };
  restartTiming.reload = () => reloads++;
  return {
    asked,
    reloads: () => reloads,
    restore() {
      Object.assign(restartTiming, real);
    },
  };
}

test("restart now asks, then waits for the gateway and reloads once it answers", async () => {
  mock.timers.enable({ apis: ["setTimeout", "Date"], now: 1_000_000 });
  const hz = fakeHealth([
    new Error("down"),
    new Response("", { status: 503 }),
    new Response("ok", { status: 200 }),
  ]);
  try {
    const f = fakeApi({
      "GET /v1/network": json(200, restartView),
      "POST /v1/network/restart": json(202, restartView),
    });
    const m = await mount(h(NetworkSettings, { api: f.api }));
    const now = m.root.querySelector("[data-testid=network-restart-now]");
    assert.equal(now.getAttribute("aria-expanded"), "false");
    assert.match(
      text(m.root.querySelector("[data-testid=network-restart]")),
      /Running now: This machine only \(127\.0\.0\.1:7777\)\.Restart now$/,
    );
    await press(now);
    assert.equal(now.getAttribute("aria-expanded"), "true");
    const c = confirmBox(m.root);
    assert.match(text(c), /Restart the gateway now\?/);
    assert.match(text(c), /Running work is interrupted/);
    // Cancel, then ask again and restart.
    await press(btn(c, "Cancel"));
    assert.equal(confirmBox(m.root), null);
    await press(now);
    await press(btn(confirmBox(m.root), "Restart"));
    assert.deepEqual(f.calls.at(-1), {
      method: "POST",
      path: "/v1/network/restart",
      body: { confirm: true },
    });
    assert.equal(
      text(m.root.querySelector("[data-testid=network-restarting]")),
      "Restarting the gateway…",
    );
    assert.equal(now.disabled, true);

    mock.timers.tick(1500);
    await settle();
    assert.equal(hz.asked.length, 1);
    mock.timers.tick(1000);
    await settle();
    mock.timers.tick(1000);
    await settle();
    assert.equal(hz.asked.length, 3);
    assert.equal(hz.reloads(), 1);
    mock.timers.tick(5000);
    await settle();
    assert.equal(hz.asked.length, 3);
    await m.unmount();
  } finally {
    hz.restore();
    mock.timers.reset();
  }
});

test("a gateway that does not come back says so and frees the controls", async () => {
  mock.timers.enable({ apis: ["setTimeout", "Date"], now: 1_000_000 });
  const hz = fakeHealth(Array.from({ length: 40 }, () => new Error("down")));
  try {
    const f = fakeApi({
      "GET /v1/network": json(200, restartView),
      "POST /v1/network/restart": json(202, restartView),
    });
    const m = await mount(h(NetworkSettings, { api: f.api }));
    await press(m.root.querySelector("[data-testid=network-restart-now]"));
    await press(btn(confirmBox(m.root), "Restart"));
    mock.timers.tick(1500);
    await settle();
    for (let i = 0; i < 30; i++) {
      mock.timers.tick(1000);
      await settle();
    }
    const note = m.root.querySelector("[data-testid=network-restarting]");
    assert.match(text(note), /did not come back here/);
    assert.match(note.className, /text-danger/);
    assert.equal(hz.reloads(), 0);
    const asked = hz.asked.length;
    mock.timers.tick(5000);
    await settle();
    assert.equal(hz.asked.length, asked);
    assert.equal(
      m.root.querySelector("[data-testid=network-restart-now]").disabled,
      false,
    );
    // Asking again clears the note.
    await press(m.root.querySelector("[data-testid=network-restart-now]"));
    assert.equal(
      m.root.querySelector("[data-testid=network-restarting]"),
      null,
    );
    await m.unmount();
  } finally {
    hz.restore();
    mock.timers.reset();
  }
});

test("closing the section stops the wait, even with a health check under way", async () => {
  mock.timers.enable({ apis: ["setTimeout", "Date"], now: 1_000_000 });
  let answer;
  const asked = [];
  const real = { ...restartTiming };
  let reloads = 0;
  restartTiming.health = () => {
    asked.push(1);
    return new Promise((r) => (answer = r));
  };
  restartTiming.reload = () => reloads++;
  try {
    const f = fakeApi({
      "GET /v1/network": json(200, restartView),
      "POST /v1/network/restart": json(202, restartView),
    });
    const m = await mount(h(NetworkSettings, { api: f.api }));
    await press(m.root.querySelector("[data-testid=network-restart-now]"));
    await press(btn(confirmBox(m.root), "Restart"));
    mock.timers.tick(1500);
    await settle();
    assert.equal(asked.length, 1);
    await m.unmount();
    answer(new Response("ok", { status: 200 }));
    await settle();
    assert.equal(reloads, 0);
    mock.timers.tick(10_000);
    await settle();
    assert.equal(asked.length, 1);

    // Closed before the first check: none runs.
    const n = await mount(h(NetworkSettings, { api: f.api }));
    await press(n.root.querySelector("[data-testid=network-restart-now]"));
    await press(btn(confirmBox(n.root), "Restart"));
    await n.unmount();
    mock.timers.tick(10_000);
    await settle();
    assert.equal(asked.length, 1);
  } finally {
    Object.assign(restartTiming, real);
    mock.timers.reset();
  }
});

test("a refused restart is shown and nothing waits", async () => {
  const f = fakeApi({
    "GET /v1/network": json(200, restartView),
    "POST /v1/network/restart": json(409, { error: "not a service" }),
  });
  const m = await mount(h(NetworkSettings, { api: f.api }));
  await press(m.root.querySelector("[data-testid=network-restart-now]"));
  await press(btn(confirmBox(m.root), "Restart"));
  assert.equal(
    text(m.root.querySelector("[data-testid=network-error]")),
    "not a service",
  );
  assert.equal(m.root.querySelector("[data-testid=network-restarting]"), null);
  assert.equal(confirmBox(m.root), null);
  assert.equal(
    m.root.querySelector("[data-testid=network-restart-now]").disabled,
    false,
  );
  await m.unmount();
});

test("the default health check and reload use the page's own fetch and location", async () => {
  const realFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (url, init) => {
    calls.push([url, init]);
    return new Response("ok");
  };
  try {
    const r = await restartTiming.health();
    assert.equal(r.ok, true);
    assert.deepEqual(calls, [
      ["/v1/health", { credentials: "same-origin", cache: "no-store" }],
    ]);
  } finally {
    globalThis.fetch = realFetch;
  }
  // jsdom does not navigate; reload only has to reach location.reload without throwing.
  assert.doesNotThrow(() => restartTiming.reload());
});

// ── security ────────────────────────────────────────────────────────────────

const SEC = { disk_access: "workspace", can_save: true, enforced: true };

test("disk access: hidden without the API, and a failed read says why", async () => {
  let f = fakeApi({ "GET /v1/security": json(404, {}) });
  let m = await mount(h(SecuritySettings, { api: f.api }));
  assert.equal(m.root.innerHTML, "");
  // No session: its scope is not asked for.
  assert.deepEqual(
    f.calls.map((c) => c.path),
    ["/v1/security"],
  );
  await m.unmount();

  f = fakeApi({ "GET /v1/security": json(500, { error: "security down" }) });
  m = await mount(h(SecuritySettings, { api: f.api }));
  assert.equal(text(m.root.querySelector("[role=alert]")), "security down");
  assert.equal(m.root.querySelector("input[name=settings-disk]"), null);
  await m.unmount();

  // Answers that arrive after the section closed are dropped.
  let ok;
  let bad;
  let scope;
  const slow = fakeApi({
    "GET /v1/security": () => new Promise((r, j) => ((ok = r), (bad = j))),
    "GET /v1/sessions/": () => new Promise((r) => (scope = r)),
  });
  m = await mount(h(SecuritySettings, { api: slow.api, sessionId: "s1" }));
  await m.unmount();
  ok(json(200, SEC));
  scope(
    json(200, {
      workspace: "/w",
      project_dir: "",
      home: "",
      private_roots: [],
    }),
  );
  await settle();
  assert.equal(m.root.innerHTML, "");
  m = await mount(h(SecuritySettings, { api: slow.api, sessionId: "s1" }));
  await m.unmount();
  bad(new Error("late"));
  await settle();
  assert.equal(m.root.innerHTML, "");
});

test("the whole disk is given only after a confirmation; the workspace comes back at once", async () => {
  const f = fakeApi({
    "GET /v1/security": json(200, SEC),
    "PUT /v1/security": (_p, init) =>
      json(200, { ...SEC, disk_access: JSON.parse(init.body).disk_access }),
  });
  const m = await mount(h(SecuritySettings, { api: f.api }));
  assert.match(
    text(m.root),
    /Safest: Motita reads and writes only inside the folder/,
  );
  assert.match(text(m.root), /logins and keys stay hidden from its commands/);
  assert.equal(
    m.root.querySelector("[data-testid=security-not-enforced]"),
    null,
  );

  await pick(m.root, "settings-disk", "full");
  const c = confirmBox(m.root, "security-confirm");
  assert.match(text(c), /Give Motita access to the whole disk\?/);
  assert.match(text(c), /could damage or leak your files/);
  assert.match(
    text(m.root),
    /can read and write any file your user account can/,
  );
  assert.equal(f.calls.filter((c) => c.path === "/v1/security").length, 1);
  // Cancel, or the workspace picked again, drops the question.
  await press(btn(c, "Cancel"));
  assert.equal(confirmBox(m.root, "security-confirm"), null);
  assert.equal(radio(m.root, "settings-disk", "workspace").checked, true);
  await pick(m.root, "settings-disk", "full");
  await pick(m.root, "settings-disk", "workspace");
  assert.equal(confirmBox(m.root, "security-confirm"), null);
  assert.equal(f.calls.filter((c) => c.path === "/v1/security").length, 1);

  await pick(m.root, "settings-disk", "full");
  await press(
    btn(confirmBox(m.root, "security-confirm"), "Allow the whole disk"),
  );
  assert.deepEqual(f.calls.at(-1).body, { disk_access: "full", confirm: true });
  assert.equal(radio(m.root, "settings-disk", "full").checked, true);
  assert.equal(confirmBox(m.root, "security-confirm"), null);

  // Back to the workspace: saved at once.
  await pick(m.root, "settings-disk", "workspace");
  assert.deepEqual(f.calls.at(-1).body, {
    disk_access: "workspace",
    confirm: false,
  });
  assert.equal(radio(m.root, "settings-disk", "workspace").checked, true);
  await m.unmount();
});

test("a refused change is shown; a gateway without a file or Landlock says what holds", async () => {
  const f = fakeApi({
    "GET /v1/security": json(200, {
      ...SEC,
      disk_access: "full",
      enforced: false,
    }),
    "PUT /v1/security": json(409, { error: "no config file" }),
  });
  const m = await mount(h(SecuritySettings, { api: f.api }));
  assert.ok(m.root.querySelector("[data-testid=security-not-enforced]"));
  assert.doesNotMatch(text(m.root), /logins and keys stay hidden/);
  await pick(m.root, "settings-disk", "workspace");
  assert.equal(
    text(m.root.querySelector("[data-testid=security-error]")),
    "no config file",
  );
  assert.equal(radio(m.root, "settings-disk", "full").checked, true);
  await m.unmount();

  const g = fakeApi({
    "GET /v1/security": json(200, { can_save: false, enforced: true }),
  });
  const n = await mount(h(SecuritySettings, { api: g.api }));
  // A view without a value is shown as the workspace.
  assert.equal(radio(n.root, "settings-disk", "workspace").checked, true);
  assert.equal(radio(n.root, "settings-disk", "full").disabled, true);
  assert.match(
    text(n.root),
    /without a configuration file, so this cannot be changed here/,
  );
  await n.unmount();
});

test("the open session's scope is listed, and follows the session", async () => {
  const scopes = {
    s1: {
      workspace: "/w/s1",
      project_dir: "/p/app",
      home: "/tools/home",
      private_roots: ["/home/me/.ssh", "/home/me/.motita"],
    },
    s2: { workspace: "/w/s2", project_dir: "", home: "", private_roots: [] },
  };
  const f = fakeApi({
    "GET /v1/security": json(200, SEC),
    "GET /v1/sessions/s3": new Error("offline"),
    "GET /v1/sessions/": (p) => json(200, scopes[p.split("/")[3]]),
  });
  const m = await mount(h(SecuritySettings, { api: f.api, sessionId: "s1" }));
  let note = m.root.querySelector("[data-testid=security-session-scope]");
  assert.deepEqual([...note.querySelectorAll("li")].map(text), [
    "This session works in /w/s1",
    "Its project is /p/app",
    "Its commands' HOME is /tools/home",
    "Kept from its commands: /home/me/.ssh",
    "Kept from its commands: /home/me/.motita",
  ]);
  await m.rerender(h(SecuritySettings, { api: f.api, sessionId: "s2" }));
  note = m.root.querySelector("[data-testid=security-session-scope]");
  assert.deepEqual([...note.querySelectorAll("li")].map(text), [
    "This session works in /w/s2",
  ]);
  // A scope that cannot be read is left out.
  await m.rerender(h(SecuritySettings, { api: f.api, sessionId: "s3" }));
  assert.equal(
    m.root.querySelector("[data-testid=security-session-scope]"),
    null,
  );
  assert.ok(m.root.querySelector("[data-testid=security-setting]"));
  await m.unmount();
});

test("a scope read that fails after the section closed is dropped", async () => {
  let fail;
  const f = fakeApi({
    "GET /v1/security": json(200, SEC),
    "GET /v1/sessions/": () => new Promise((_, r) => (fail = r)),
  });
  const m = await mount(h(SecuritySettings, { api: f.api, sessionId: "s9" }));
  await m.unmount();
  fail(new Error("late"));
  await settle();
  assert.equal(m.root.innerHTML, "");
});
