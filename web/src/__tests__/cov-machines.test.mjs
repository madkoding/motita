// The computers: the client of /v1/peers and the proxy path, the hook that keeps the list and the
// computer the page shows, the boxes of the sidebar and the dialog that connects a computer.
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  source,
  text,
  flush,
  renderHook,
} from "./cov-stream-helpers.mjs";

installDom();
const api = await source("machinesApi.ts");
const hook = await source("useMachines.ts");
const { MachineList } = await source("Machines.tsx");
const { ConnectMachineModal } = await source("ConnectMachineModal.tsx");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const SELF = {
  id: "self0001",
  name: "taller",
  hostname: "taller",
  reachable: true,
};
const DESK = {
  id: "desk0001",
  name: "desk",
  address: "[fd00::2]:7477",
  source: "lan",
  paired: true,
  online: true,
};
const NAS = {
  ...DESK,
  id: "nas00001",
  name: "nas",
  alias: "Storage",
  online: false,
};
const FOUND = {
  ...DESK,
  id: "lap00001",
  name: "laptop",
  paired: false,
  source: "tailscale",
};
const ODD = { ...FOUND, id: "odd00001", name: "odd", source: "carrier-pigeon" };
const REQUEST = {
  id: "lap00001",
  name: "laptop",
  address: "fd00::5",
  code: "123 456",
};
const GRANT = { id: "tab00001", name: "tablet", at: "2026-10-18T10:00:00Z" };

// fakeApi answers like the gateway, recording the requests; fail makes the next one fail.
function fakeApi(view) {
  const calls = [];
  const state = { view, fail: null, gate: null };
  const fn = async (path, init = {}) => {
    calls.push([init.method || "GET", path, init.body]);
    if (state.gate) {
      const gate = state.gate;
      state.gate = null;
      await gate;
    }
    if (state.fail) {
      const f = state.fail;
      state.fail = null;
      return {
        ok: false,
        status: f.status,
        json: async () => {
          if (f.body instanceof Error) {
            throw f.body;
          }
          return f.body;
        },
      };
    }
    return { ok: true, status: 200, json: async () => state.view };
  };
  return { fn, calls, state };
}

test("a connected computer's API is reached through this gateway", async () => {
  assert.equal(api.proxyPath("", "/v1/sessions"), "/v1/sessions");
  assert.equal(api.proxyPath("a b", "/emoji/x.webp"), "/emoji/x.webp");
  assert.equal(
    api.proxyPath("a b", "/v1/sessions"),
    "/v1/peers/a%20b/proxy/v1/sessions",
  );
  const seen = [];
  const base = async (p, i) => seen.push([p, i]);
  assert.equal(api.machineApi(base, ""), base);
  await api.machineApi(base, "desk0001")("/v1/ui", { method: "PUT" });
  assert.deepEqual(seen, [
    ["/v1/peers/desk0001/proxy/v1/ui", { method: "PUT" }],
  ]);
  assert.equal(api.machineName(NAS), "Storage");
  assert.equal(api.machineName(DESK), "desk");
});

test("every change goes to /v1/peers and a refusal says why", async () => {
  const view = {
    self: SELF,
    peers: [],
    discovery: true,
    requests: [],
    grants: [],
  };
  const f = fakeApi(view);
  assert.deepEqual(await api.getMachines(f.fn), view);
  await api.renameSelf(f.fn, "a");
  await api.renameMachine(f.fn, "d/1", "b");
  await api.connectMachine(f.fn, "d1", "t");
  await api.addMachine(f.fn, "fd00::1", "t");
  await api.forgetMachine(f.fn, "d1");
  await api.askMachine(f.fn, "d1");
  await api.answerRequest(f.fn, "l/1", true);
  await api.revokeGrant(f.fn, "l1");
  assert.deepEqual(f.calls, [
    ["GET", "/v1/peers", undefined],
    ["PUT", "/v1/peers/self", '{"name":"a"}'],
    ["PUT", "/v1/peers/d%2F1", '{"alias":"b"}'],
    ["PUT", "/v1/peers/d1", '{"token":"t"}'],
    ["POST", "/v1/peers", '{"address":"fd00::1","token":"t"}'],
    ["DELETE", "/v1/peers/d1", undefined],
    ["POST", "/v1/peers/d1/ask", undefined],
    ["POST", "/v1/peer-requests/l%2F1", '{"allow":true}'],
    ["DELETE", "/v1/peer-grants/l1", undefined],
  ]);
  f.state.fail = { status: 422, body: { error: "bad token" } };
  await assert.rejects(api.getMachines(f.fn), /bad token/);
  f.state.fail = { status: 502, body: null };
  await assert.rejects(api.getMachines(f.fn), /HTTP 502/);
  f.state.fail = { status: 500, body: new Error("not json") };
  await assert.rejects(api.getMachines(f.fn), /HTTP 500/);
});

test("the computer shown is remembered, and a browser that keeps nothing shows this one", async () => {
  localStorage.removeItem(hook.MACHINE_KEY);
  const a = await renderHook(() => hook.useMachineChoice());
  assert.equal(a.result.current[0], "");
  await a.act(() => a.result.current[1]("desk0001"));
  assert.equal(a.result.current[0], "desk0001");
  assert.equal(localStorage.getItem(hook.MACHINE_KEY), "desk0001");
  await a.unmount();
  const b = await renderHook(() => hook.useMachineChoice());
  assert.equal(b.result.current[0], "desk0001");
  await b.unmount();
  const real = globalThis.localStorage;
  globalThis.localStorage = {
    getItem() {
      throw new Error("blocked");
    },
    setItem() {
      throw new Error("blocked");
    },
  };
  try {
    const c = await renderHook(() => hook.useMachineChoice());
    assert.equal(c.result.current[0], "");
    await c.act(() => c.result.current[1]("x"));
    assert.equal(c.result.current[0], "x");
    await c.unmount();
  } finally {
    globalThis.localStorage = real;
  }
});

// useHost renders the hook with a settable shown computer.
async function useHost(f, { enabled = true, active = "" } = {}) {
  const shown = { active };
  const r = await renderHook(
    (p) =>
      hook.useMachines(f.fn, p.enabled, p.active, (id) => {
        shown.active = id;
      }),
    { enabled, active },
  );
  await flush();
  return { ...r, shown };
}

test("the list is read once signed in, kept current, and a lost computer is left", async () => {
  const f = fakeApi({
    self: SELF,
    peers: [DESK, FOUND],
    discovery: true,
    requests: [],
    grants: [],
  });
  const off = await useHost(f, { enabled: false });
  assert.equal(f.calls.length, 0, "nothing is asked before signing in");
  await off.unmount();

  const realSet = globalThis.setInterval;
  const ticks = [];
  globalThis.setInterval = (fn, ms) => {
    ticks.push([fn, ms]);
    return 7;
  };
  try {
    const r = await useHost(f, { active: "desk0001" });
    assert.equal(r.result.current.view.self.name, "taller");
    assert.equal(ticks[0][1], hook.MACHINES_POLL_MS);
    f.state.fail = { status: 500, body: {} };
    await r.act(() => ticks[0][0]());
    await flush();
    assert.equal(
      r.result.current.view.peers.length,
      2,
      "a failed read keeps the list",
    );
    assert.equal(
      r.shown.active,
      "desk0001",
      "a connected computer stays shown",
    );
    // Shown, but only found (not connected): back to this one.
    await r.rerender({ enabled: true, active: "lap00001" });
    assert.equal(r.shown.active, "");
    // A request waiting, here or there, is checked on more often.
    for (const view of [
      {
        self: SELF,
        peers: [],
        discovery: true,
        requests: [REQUEST],
        grants: [],
      },
      {
        self: SELF,
        peers: [{ ...FOUND, ask: { code: "1", state: "waiting" } }],
        discovery: true,
        requests: [],
        grants: [],
      },
    ]) {
      f.state.view = view;
      await r.act(() => ticks.at(-1)[0]());
      await flush();
      assert.equal(ticks.at(-1)[1], hook.MACHINES_ASK_POLL_MS);
      f.state.view = {
        self: SELF,
        peers: [],
        discovery: true,
        requests: [],
        grants: [],
      };
      await r.act(() => ticks.at(-1)[0]());
      await flush();
      assert.equal(ticks.at(-1)[1], hook.MACHINES_POLL_MS);
    }
    await r.unmount();
  } finally {
    globalThis.setInterval = realSet;
  }
});

test("picking a computer shows it, and picking it again folds its box", async () => {
  const f = fakeApi({
    self: SELF,
    peers: [DESK],
    discovery: true,
    requests: [],
    grants: [],
  });
  const r = await useHost(f);
  const m = () => r.result.current;
  assert.equal(m().isOpen(""), true);
  assert.equal(m().isOpen("desk0001"), false);
  await r.act(() => m().select(""));
  assert.equal(m().isOpen(""), false);
  await r.act(() => m().select(""));
  assert.equal(m().isOpen(""), true);
  await r.act(() => m().select("desk0001"));
  assert.equal(r.shown.active, "desk0001");
  await r.unmount();
});

test("connecting, renaming and forgetting keep the answer or the refusal", async () => {
  const f = fakeApi({
    self: SELF,
    peers: [DESK],
    discovery: true,
    requests: [],
    grants: [],
  });
  const r = await useHost(f);
  const m = () => r.result.current;
  // No dialog open: an address is connected.
  assert.equal(await m().connect("t", "fd00::9"), "");
  assert.deepEqual(f.calls.at(-1)[1], "/v1/peers");
  await r.act(() => m().openConnect({ machine: FOUND }));
  f.state.fail = {
    status: 422,
    body: { error: "the other computer did not accept that token" },
  };
  let msg;
  await r.act(async () => {
    msg = await m().connect("bad", "");
  });
  assert.match(msg, /did not accept/);
  assert.match(m().error, /did not accept/);
  assert.ok(m().connecting, "the dialog stays open on a refusal");
  await r.act(async () => {
    msg = await m().connect("good", "");
  });
  assert.equal(msg, "");
  assert.equal(m().connecting, null);
  assert.equal(f.calls.at(-1)[1], "/v1/peers/lap00001");
  // No token: the computer is asked to let this one in.
  await r.act(() => m().openConnect({ machine: FOUND }));
  await r.act(async () => {
    msg = await m().connect("", "");
  });
  assert.equal(f.calls.at(-1)[1], "/v1/peers/lap00001/ask");
  await r.act(() => m().answer("lap00001", false));
  await r.act(() => m().revoke("tab00001"));
  assert.deepEqual(
    f.calls.slice(-2).map((c) => c[0] + " " + c[1]),
    ["POST /v1/peer-requests/lap00001", "DELETE /v1/peer-grants/tab00001"],
  );
  await r.act(() => m().openConnect({}));
  await r.act(() => m().closeConnect());
  assert.equal(m().connecting, null);
  await r.act(() => m().renameSelf("x"));
  await r.act(() => m().rename("desk0001", "y"));
  await r.act(() => m().forget("desk0001"));
  assert.deepEqual(
    f.calls.slice(-3).map((c) => c[0] + " " + c[1]),
    [
      "PUT /v1/peers/self",
      "PUT /v1/peers/desk0001",
      "DELETE /v1/peers/desk0001",
    ],
  );
  await r.unmount();
});

// ── The sidebar's boxes and the dialog ───────────────────────────────────────

// mount draws the computers over a fake gateway, with a stand-in sidebar inside the shown box.
async function mount(view, active = "") {
  const f = fakeApi(view);
  const shown = { active };
  function Host() {
    const m = hook.useMachines(f.fn, true, shown.active, (id) => {
      shown.active = id;
    });
    return h(
      hook.MachinesCtx.Provider,
      { value: m },
      h(MachineList, null, h("p", { "data-testid": "inside" }, "sidebar")),
      m.connecting && h(ConnectMachineModal),
    );
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(h(Host), root));
  await act(() => flush());
  return {
    root,
    f,
    shown,
    q: (sel) => root.querySelector(sel),
    qa: (sel) => [...root.querySelectorAll(sel)],
    rerender: () => act(() => render(h(Host), root)),
    done: async () => {
      await act(() => render(null, root));
      root.remove();
    },
  };
}
const click = (el) =>
  act(() => el.dispatchEvent(new MouseEvent("click", { bubbles: true })));
const key = (el, k) =>
  act(() =>
    el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })),
  );
const type = (el, value) =>
  act(() => {
    el.value = value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
const box = (ui, id) => ui.q(`[data-machine="${id}"]`);

test("before the list arrives, this computer holds the sidebar under a plain name", async () => {
  const ui = await mount(null);
  ui.f.state.view = null;
  assert.equal(
    text(box(ui, "self").querySelector("[data-testid=machine-toggle]")),
    "This computerthis computer",
  );
  assert.ok(box(ui, "self").querySelector("[data-testid=inside]"));
  assert.equal(ui.qa("[data-testid=machine-found]").length, 0);
  await ui.done();
});

test("this computer, the connected ones and the found ones each have their place", async () => {
  const ui = await mount({
    self: { ...SELF, reachable: false },
    peers: [DESK, NAS, FOUND, ODD],
    discovery: true,
    requests: [],
    grants: [],
  });
  assert.deepEqual(
    ui.qa("[data-testid=machine]").map((b) => b.dataset.machine),
    ["self", "desk0001", "nas00001"],
  );
  const self = box(ui, "self");
  assert.match(text(self), /tallerthis computer/);
  assert.ok(
    self.querySelector("[data-testid=inside]"),
    "the shown computer holds the sidebar",
  );
  assert.equal(
    self.querySelector("[data-testid=machine-forget]"),
    null,
    "this computer is not disconnected",
  );
  assert.equal(box(ui, "desk0001").querySelector("[data-testid=inside]"), null);
  assert.equal(
    box(ui, "nas00001").querySelector("[data-testid=machine-toggle]").disabled,
    true,
    "an offline computer cannot be shown",
  );
  assert.equal(
    box(ui, "nas00001").querySelector("[data-testid=machine-status]").title,
    "Offline",
  );
  assert.equal(
    box(ui, "desk0001").querySelector("[data-testid=machine-status]").title,
    "Online",
  );
  assert.match(text(box(ui, "nas00001")), /Storage/);
  const found = ui.qa("[data-testid=machine-found]").map(text);
  assert.deepEqual(found, [
    "laptopTailscaleConnect",
    "oddcarrier-pigeonConnect",
  ]);
  assert.ok(
    ui.q("[data-testid=machine-unreachable]"),
    "a computer no one can reach says so",
  );
  // Folding the shown box, then showing another computer.
  await click(self.querySelector("[data-testid=machine-toggle]"));
  assert.equal(box(ui, "self").querySelector("[data-testid=inside]"), null);
  await click(
    box(ui, "desk0001").querySelector("[data-testid=machine-toggle]"),
  );
  assert.equal(ui.shown.active, "desk0001");
  await ui.done();
});

test("the shown remote computer can be folded even while it does not answer", async () => {
  const ui = await mount(
    {
      self: SELF,
      peers: [{ ...DESK, online: false }],
      discovery: true,
      requests: [],
      grants: [],
    },
    "desk0001",
  );
  const toggle = box(ui, "desk0001").querySelector(
    "[data-testid=machine-toggle]",
  );
  assert.equal(toggle.disabled, false);
  assert.ok(box(ui, "desk0001").querySelector("[data-testid=inside]"));
  assert.equal(ui.q("[data-testid=machine-unreachable]"), null);
  await ui.done();
});

test("a computer is renamed in place: Enter or leaving saves, Escape cancels, the same name sends nothing", async () => {
  const ui = await mount({
    self: SELF,
    peers: [DESK],
    discovery: true,
    requests: [],
    grants: [],
  });
  const rename = async (id) => {
    await click(
      box(ui, id).querySelector("[data-testid=machine-rename-button]"),
    );
    return box(ui, id).querySelector("[data-testid=machine-rename]");
  };
  let field = await rename("self");
  assert.equal(field.value, "taller");
  await key(field, "a");
  await key(field, "Escape");
  assert.equal(
    box(ui, "self").querySelector("[data-testid=machine-rename]"),
    null,
  );
  field = await rename("self");
  await key(field, "Enter");
  assert.equal(
    ui.f.calls.filter((c) => c[0] === "PUT").length,
    0,
    "the same name is not sent",
  );
  field = await rename("self");
  await type(field, "  Taller  ");
  await key(field, "Enter");
  await act(() => flush());
  field = await rename("desk0001");
  await type(field, "Office");
  await act(() => field.dispatchEvent(new FocusEvent("blur")));
  await act(() => flush());
  assert.deepEqual(
    ui.f.calls.filter((c) => c[0] === "PUT").map((c) => [c[1], c[2]]),
    [
      ["/v1/peers/self", '{"name":"Taller"}'],
      ["/v1/peers/desk0001", '{"alias":"Office"}'],
    ],
  );
  await ui.done();
});

test("disconnecting asks a second click, and leaving the button disarms it", async () => {
  const ui = await mount({
    self: SELF,
    peers: [DESK],
    discovery: true,
    requests: [],
    grants: [],
  });
  const desk = () => box(ui, "desk0001");
  await click(desk().querySelector("[data-testid=machine-forget]"));
  const confirm = desk().querySelector("[data-testid=machine-forget-confirm]");
  assert.equal(text(confirm), "Disconnect");
  await act(() => confirm.dispatchEvent(new FocusEvent("blur")));
  assert.equal(
    desk().querySelector("[data-testid=machine-forget-confirm]"),
    null,
  );
  await click(desk().querySelector("[data-testid=machine-forget]"));
  await click(desk().querySelector("[data-testid=machine-forget-confirm]"));
  await act(() => flush());
  assert.deepEqual(ui.f.calls.at(-1).slice(0, 2), [
    "DELETE",
    "/v1/peers/desk0001",
  ]);
  await ui.done();
});

test("a found computer is connected with its token, and a refusal is shown in the dialog", async () => {
  const ui = await mount({
    self: SELF,
    peers: [FOUND],
    discovery: true,
    requests: [],
    grants: [],
  });
  await click(ui.q("[data-testid=machine-connect]"));
  const dialog = () => document.querySelector("[data-testid=connect-machine]");
  assert.equal(text(dialog().querySelector("h2")), "Connect laptop");
  assert.equal(
    dialog().querySelector("#machine-address"),
    null,
    "a found computer needs no address",
  );
  const submit = () => dialog().querySelector("button[type=submit]");
  assert.equal(submit().disabled, false, "no token asks for access");
  assert.equal(text(submit()), "Ask for access");
  await type(dialog().querySelector("#machine-token"), " secret ");
  assert.equal(text(submit()), "Connect");
  ui.f.state.fail = {
    status: 422,
    body: { error: "the other computer did not accept that token" },
  };
  await act(() =>
    dialog().dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    ),
  );
  await act(() => flush());
  assert.match(text(dialog().querySelector("[role=alert]")), /did not accept/);
  await act(() =>
    dialog().dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    ),
  );
  await act(() => flush());
  assert.equal(dialog(), null, "connected: the dialog closes");
  assert.deepEqual(ui.f.calls.at(-1), [
    "PUT",
    "/v1/peers/lap00001",
    '{"token":"secret"}',
  ]);
  await ui.done();
});

test("a computer across a VPN is added by its address, or the dialog is cancelled", async () => {
  const ui = await mount({
    self: SELF,
    peers: [],
    discovery: true,
    requests: [],
    grants: [],
  });
  await click(ui.q("[data-testid=sidebar-add-machine]"));
  const dialog = () => document.querySelector("[data-testid=connect-machine]");
  assert.equal(text(dialog().querySelector("h2")), "Add a computer");
  const submit = () => dialog().querySelector("button[type=submit]");
  await type(dialog().querySelector("#machine-token"), "secret");
  assert.equal(submit().disabled, true, "the address is needed");
  await type(dialog().querySelector("#machine-address"), "fd7a:115c:a1e0::2");
  assert.equal(submit().disabled, false);
  // While it is being connected the button says so.
  let release;
  ui.f.state.gate = new Promise((r) => (release = r));
  const calls = ui.f.calls;
  await act(() =>
    dialog().dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    ),
  );
  assert.equal(text(submit()), "Connecting…");
  assert.equal(submit().disabled, true);
  release();
  await act(() => flush());
  assert.deepEqual(calls.at(-1), [
    "POST",
    "/v1/peers",
    '{"address":"fd7a:115c:a1e0::2","token":"secret"}',
  ]);
  await click(ui.q("[data-testid=sidebar-add-machine]"));
  await click(
    [...dialog().querySelectorAll("button")].find((b) => text(b) === "Cancel"),
  );
  assert.equal(dialog(), null);
  await ui.done();
});

test("each computer says how many runs are in flight, and a request to come in waits with its code", async () => {
  const ui = await mount({
    self: { ...SELF, running: 2 },
    peers: [
      { ...DESK, running: 1 },
      { ...FOUND, ask: { code: "654 321", state: "waiting" } },
      { ...ODD, ask: { code: "1", state: "denied" } },
    ],
    discovery: true,
    requests: [REQUEST],
    grants: [GRANT],
  });
  assert.equal(
    text(box(ui, "self").querySelector("[data-testid=machine-running]")),
    "2",
  );
  assert.equal(
    box(ui, "desk0001").querySelector("[data-testid=machine-running]").title,
    "1 running",
  );
  assert.equal(text(ui.q("[data-testid=machine-asking]")), "654 321");
  assert.equal(text(ui.q("[data-testid=machine-denied]")), "Refused");
  assert.equal(
    ui.qa("[data-testid=machine-connect]").length,
    1,
    "a refused computer can be asked again; a waiting one cannot",
  );
  const card = ui.q("[data-testid=machine-request]");
  assert.match(text(card), /laptop wants to open this computer/);
  assert.equal(
    text(card.querySelector("[data-testid=machine-request-code]")),
    "123 456",
  );
  await click(card.querySelector("[data-testid=machine-allow]"));
  await click(card.querySelector("[data-testid=machine-deny]"));
  assert.match(text(ui.q("[data-testid=machine-grant]")), /tablet/);
  await click(ui.q("[data-testid=machine-revoke]"));
  await act(() => flush());
  assert.deepEqual(
    ui.f.calls.slice(-3).map((c) => [c[0], c[1], c[2]]),
    [
      ["POST", "/v1/peer-requests/lap00001", '{"allow":true}'],
      ["POST", "/v1/peer-requests/lap00001", '{"allow":false}'],
      ["DELETE", "/v1/peer-grants/tab00001", undefined],
    ],
  );
  await ui.done();
});
