// The git connections section of the settings: it lists every host with its state, connects,
// reconnects and disconnects one (asking first), and adds a self-hosted server.
// Run with: node --test web/src/__tests__/cov-git-connections.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  loadSources,
  fakeApi,
  json,
  text,
  flush,
} from "./cov-git-helpers.mjs";

installDom();
const { GitConnections } = await loadSources({
  GitConnections: "GitConnections.tsx",
});
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const ACCOUNTS = [
  {
    id: "github",
    name: "GitHub",
    kind: "github",
    host: "github.com",
    connected: true,
    username: "octo",
    methods: ["device", "token"],
    oauth_ready: true,
  },
  {
    id: "gitlab",
    name: "GitLab",
    kind: "gitlab",
    host: "gitlab.com",
    connected: true,
    methods: ["code", "token"],
    oauth_ready: false,
  },
  {
    id: "gitea@git.corp.io",
    name: "git.corp.io",
    kind: "gitea",
    host: "git.corp.io",
    connected: false,
    methods: ["token"],
    oauth_ready: false,
  },
  {
    id: "bitbucket",
    name: "Bitbucket",
    kind: "bitbucket",
    host: "bitbucket.org",
    connected: false,
    methods: ["token"],
    oauth_ready: false,
  },
];

async function mount(api, props = {}) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const connects = [];
  const notices = [];
  const all = {
    api,
    rev: 0,
    onConnect: (...a) => connects.push(a),
    onNotice: (...a) => notices.push(a),
    ...props,
  };
  const draw = (p) =>
    act(() => {
      render(h(GitConnections, { ...all, ...p }), root);
    });
  await draw({});
  await act(flush);
  const done = async () => {
    await act(() => render(null, root));
    root.remove();
  };
  return { root, connects, notices, draw, done };
}

const rowOf = (root, name) =>
  [...root.querySelectorAll("li")].find((li) => text(li).startsWith(name));
const button = (el, label) =>
  [...el.querySelectorAll("button")].find(
    (b) => text(b) === label || b.getAttribute("aria-label") === label,
  );
const click = (el) =>
  act(async () => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await flush();
  });

test("it says it is loading, then lists each host with its logo and state", async () => {
  let release;
  const f = fakeApi({
    "GET /v1/git/accounts": () =>
      new Promise((r) => {
        release = () => r(json(200, { accounts: ACCOUNTS }));
      }),
  });
  const m = await mount(f.api);
  assert.equal(text(m.root.querySelector("output")), "Loading…");
  await act(async () => {
    release();
    await flush();
  });
  assert.equal(m.root.querySelector("output"), null);
  assert.equal(m.root.querySelectorAll("li").length, 4);
  assert.match(text(rowOf(m.root, "GitHub")), /Connected as octo/);
  // A connected account whose user name is not known yet.
  assert.match(text(rowOf(m.root, "GitLab")), /Connected as …/);
  assert.match(text(rowOf(m.root, "git.corp.io")), /Not connected/);
  // Known hosts draw their own logo; Gitea gets the generic branch icon.
  const icon = (name) => rowOf(m.root, name).querySelector(".git-account-icon");
  assert.equal(
    icon("GitHub").querySelector("svg").getAttribute("fill"),
    "#e8e8ea",
  );
  assert.equal(
    icon("GitLab").querySelector("svg").getAttribute("fill"),
    "#fc6d26",
  );
  assert.equal(
    icon("Bitbucket").querySelector("svg").getAttribute("fill"),
    "#2684ff",
  );
  assert.equal(icon("git.corp.io").dataset.kind, "gitea");
  assert.ok(icon("git.corp.io").querySelector("svg"));
  assert.equal(
    icon("git.corp.io").querySelector("svg").getAttribute("fill"),
    "none",
  );
  await m.done();
});

test("a failed load shows the error and no list; a new rev reads the list again", async () => {
  let fail = true;
  const f = fakeApi({
    "GET /v1/git/accounts": () =>
      fail
        ? json(500, { error: "gateway down" })
        : json(200, { accounts: ACCOUNTS.slice(0, 1) }),
  });
  const m = await mount(f.api);
  assert.equal(text(m.root.querySelector("[role=alert]")), "gateway down");
  assert.equal(m.root.querySelector("ul"), null);
  fail = false;
  await m.draw({ rev: 1 });
  await act(flush);
  assert.equal(m.root.querySelector("[role=alert]"), null);
  assert.equal(m.root.querySelectorAll("li").length, 1);
  assert.equal(f.calls.length, 2);
  await m.done();
});

test("an answer that arrives after the section closed is dropped", async () => {
  let release;
  const f = fakeApi({
    "GET /v1/git/accounts": () =>
      new Promise((r) => {
        release = r;
      }),
  });
  const m = await mount(f.api);
  await m.done();
  release(json(200, { accounts: ACCOUNTS }));
  await flush();
  assert.equal(m.root.querySelectorAll("li").length, 0);

  // and so is a failure
  const f2 = fakeApi({
    "GET /v1/git/accounts": () =>
      new Promise((r) => {
        release = r;
      }),
  });
  const m2 = await mount(f2.api);
  await m2.done();
  release(json(500, { error: "late" }));
  await flush();
  assert.equal(m2.root.querySelector("[role=alert]"), null);
});

test("Connect and Reconnect ask the app to connect that host", async () => {
  const f = fakeApi({
    "GET /v1/git/accounts": json(200, { accounts: ACCOUNTS }),
  });
  const m = await mount(f.api);
  await click(button(m.root, "Reconnect GitHub"));
  await click(button(m.root, "Connect git.corp.io"));
  assert.deepEqual(m.connects, [["github"], ["gitea@git.corp.io"]]);
  await m.done();
});

test("Disconnect asks first; Cancel keeps the account, Confirm removes it and reloads", async () => {
  let disconnected = false;
  const f = fakeApi({
    "GET /v1/git/accounts": () =>
      json(200, {
        accounts: disconnected
          ? [{ ...ACCOUNTS[0], connected: false }]
          : ACCOUNTS.slice(0, 1),
      }),
    "DELETE /v1/git/accounts/": () => {
      disconnected = true;
      return json(204);
    },
  });
  const m = await mount(f.api);
  const row = () => rowOf(m.root, "GitHub");
  await click(button(row(), "Disconnect GitHub"));
  assert.ok(button(row(), "Confirm"));
  assert.equal(button(row(), "Reconnect GitHub"), undefined);
  await click(button(row(), "Cancel"));
  assert.equal(button(row(), "Confirm"), undefined);
  assert.ok(button(row(), "Disconnect GitHub"));

  await click(button(row(), "Disconnect GitHub"));
  await click(button(row(), "Confirm"));
  await act(flush);
  assert.deepEqual(m.notices, [["Disconnected from GitHub"]]);
  assert.ok(
    f.calls.some(
      (c) => c.method === "DELETE" && c.path === "/v1/git/accounts/github",
    ),
  );
  assert.match(text(row()), /Not connected/);
  assert.ok(button(row(), "Connect GitHub"));
  await m.done();
});

test("a refused disconnect is told as an error", async () => {
  const f = fakeApi({
    "GET /v1/git/accounts": json(200, { accounts: ACCOUNTS.slice(0, 1) }),
    "DELETE /v1/git/accounts/": json(500, { error: "cannot forget it" }),
  });
  const m = await mount(f.api);
  await click(button(m.root, "Disconnect GitHub"));
  await click(button(m.root, "Confirm"));
  await act(flush);
  assert.deepEqual(m.notices, [["cannot forget it", true]]);
  assert.equal(button(m.root, "Confirm"), undefined);
  await m.done();
});

test("a refusal without a message is told by its HTTP status", async () => {
  const f = fakeApi({
    "GET /v1/git/accounts": new Response("not json", { status: 503 }),
  });
  const m = await mount(f.api);
  assert.equal(text(m.root.querySelector("[role=alert]")), "HTTP 503");
  await m.done();
});

test("adding a self-hosted server: kind and cleaned-up address go to the app", async () => {
  const f = fakeApi({ "GET /v1/git/accounts": json(200, { accounts: [] }) });
  const m = await mount(f.api);
  assert.equal(m.root.querySelector("ul"), null);
  await click(button(m.root, "Add self-hosted server"));
  const form = m.root.querySelector("form");
  assert.ok(form);
  assert.equal(button(m.root, "Add self-hosted server"), undefined);
  const submit = button(form, "Continue");
  assert.equal(submit.disabled, true);

  // An empty address is not sent even if the form is submitted.
  await act(() => {
    form.dispatchEvent(
      new window.Event("submit", { bubbles: true, cancelable: true }),
    );
  });
  assert.deepEqual(m.connects, []);

  const select = form.querySelector("select");
  const input = form.querySelector("input");
  await act(() => {
    select.value = "gitea";
    select.dispatchEvent(new window.Event("change", { bubbles: true }));
    input.value = "  https://git.example.com// ";
    input.dispatchEvent(new window.Event("input", { bubbles: true }));
  });
  assert.equal(button(form, "Continue").disabled, false);
  await act(() => {
    form.dispatchEvent(
      new window.Event("submit", { bubbles: true, cancelable: true }),
    );
  });
  assert.deepEqual(m.connects, [
    [undefined, { kind: "gitea", host: "git.example.com" }],
  ]);
  assert.equal(m.root.querySelector("form"), null);

  // Opened again, the address is empty; Cancel closes it without connecting.
  await click(button(m.root, "Add self-hosted server"));
  assert.equal(m.root.querySelector("form input").value, "");
  await click(button(m.root.querySelector("form"), "Cancel"));
  assert.equal(m.root.querySelector("form"), null);
  assert.equal(m.connects.length, 1);
  await m.done();
});
