// The git connect modal: it lists the hosts, connects one with a pasted token or a one-click login
// (device code or browser redirect), follows the login until the host approves it, and cancels a
// login that is still pending when the modal goes away.
// Run with: node --test web/src/__tests__/cov-git-connect.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  loadSources,
  fakeApi,
  json,
  text,
} from "./cov-git-helpers.mjs";

installDom();
const { GitConnectModal, Spinner } = await loadSources({
  GitConnectModal: "GitConnect.tsx",
  Spinner: "GitConnect.tsx",
});
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

// setImmediate, so the flush still works while setTimeout is mocked.
const flush = () => new Promise((r) => setImmediate(r));
const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await act(flush);
  }
};

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
    setup_hint: "never shown once ready",
  },
  {
    id: "gitlab",
    name: "GitLab",
    kind: "gitlab",
    host: "gitlab.com",
    connected: false,
    methods: ["code", "token"],
    oauth_ready: true,
  },
  {
    id: "gitea@git.corp.io",
    name: "git.corp.io",
    kind: "gitea",
    host: "git.corp.io",
    connected: false,
    methods: ["token"],
    oauth_ready: false,
    setup_hint: "Ask your admin for OAuth.",
    token_url: "https://git.corp.io/user/settings/applications",
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
  {
    id: "gitlab@lab.corp.io",
    name: "lab.corp.io",
    kind: "gitlab",
    host: "lab.corp.io",
    connected: true,
    methods: ["code", "token"],
    oauth_ready: false,
    setup_hint: "Register an OAuth app to sign in with one click.",
  },
];

const accountsRoute = {
  "GET /v1/git/accounts": json(200, { accounts: ACCOUNTS }),
};

async function mount(routes, props = {}) {
  const f = fakeApi({ ...accountsRoute, ...routes });
  const root = document.createElement("div");
  document.body.appendChild(root);
  const closed = [];
  const connected = [];
  await act(() => {
    render(
      h(GitConnectModal, {
        api: f.api,
        onClose: () => closed.push(true),
        onConnected: (...a) => connected.push(a),
        ...props,
      }),
      root,
    );
  });
  await settle();
  let gone = false;
  const done = async () => {
    if (!gone) {
      gone = true;
      await act(() => render(null, root));
      root.remove();
    }
  };
  return { root, f, closed, connected, done };
}

const buttons = (root) => [...root.querySelectorAll("button")];
const button = (root, label) =>
  buttons(root).find(
    (b) => text(b) === label || b.getAttribute("aria-label") === label,
  );
const click = async (el) => {
  assert.ok(el, "the button is there");
  await act(() => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  await settle();
};
const type = async (el, value) => {
  await act(() => {
    el.value = value;
    el.dispatchEvent(new window.Event("input", { bubbles: true }));
  });
};
const submit = async (form) => {
  await act(() => {
    form.dispatchEvent(
      new window.Event("submit", { bubbles: true, cancelable: true }),
    );
  });
  await settle();
};
const title = (root) => text(root.querySelector("h2"));
const alertText = (root) => text(root.querySelector("[role=alert]"));

test("Spinner draws at the size it is given", () => {
  const root = document.createElement("div");
  render(h(Spinner, { size: 24 }), root);
  assert.equal(root.querySelector("svg").getAttribute("width"), "24");
  render(h(Spinner, {}), root);
  assert.equal(root.querySelector("svg").getAttribute("width"), "16");
  render(null, root);
});

test("it loads the hosts, lists them with their state, and picking one opens its panel", async () => {
  let release;
  const m = await mount({
    "GET /v1/git/accounts": () =>
      new Promise((r) => {
        release = () => r(json(200, { accounts: ACCOUNTS }));
      }),
  });
  assert.equal(title(m.root), "Connect a git host");
  assert.match(text(m.root), /Loading…/);
  await act(() => release());
  await settle();
  assert.doesNotMatch(text(m.root), /Loading…/);
  assert.match(text(m.root), /Which git host do you want to connect\?/);
  const items = [...m.root.querySelectorAll("li")].map(text);
  assert.equal(items.length, ACCOUNTS.length);
  assert.match(items[0], /GitHub github\.com ?Connected as octo/);
  assert.doesNotMatch(items[1], /Connected/);
  // A connected host whose user is not known says so without a name.
  assert.match(items[4], /Connected as$/);

  await click(m.root.querySelectorAll("li button")[1]);
  assert.equal(title(m.root), "Connect GitLab");
  assert.match(text(m.root), /Sign in with GitLab to let motita clone/);

  await click(button(m.root, "Close"));
  assert.equal(m.closed.length, 1);
  await m.done();
});

test("a failed load is shown above the empty list, worded for each kind of failure", async () => {
  const cases = [
    [
      json(501, { error: "no login" }),
      "This server cannot sign in to git hosts. Ask whoever runs it to enable login.",
    ],
    [json(500, { error: "gateway exploded" }), "gateway exploded"],
    [new Error("network down"), "network down"],
  ];
  for (const [answer, want] of cases) {
    const m = await mount({ "GET /v1/git/accounts": answer });
    assert.equal(alertText(m.root), want);
    assert.equal(m.root.querySelectorAll("li").length, 0);
    await m.done();
  }
});

test("an answer after the modal closed is dropped", async () => {
  for (const late of [json(200, { accounts: ACCOUNTS }), json(500, {})]) {
    let release;
    const m = await mount({
      "GET /v1/git/accounts": () =>
        new Promise((r) => {
          release = () => r(late);
        }),
    });
    await m.done();
    release();
    await flush();
    await flush();
    assert.equal(m.root.querySelector("li"), null);
    assert.equal(m.root.querySelector("[role=alert]"), null);
  }
});

test("a token host: the form asks for the token and connects with it", async () => {
  let answer = json(200, { account: "me" });
  const m = await mount(
    { "POST /v1/git/connect": () => answer },
    { service: "gitea@git.corp.io" },
  );
  assert.equal(title(m.root), "Connect git.corp.io");
  assert.match(text(m.root), /Ask your admin for OAuth\./);
  assert.match(text(m.root), /Create a personal access token/);
  const link = m.root.querySelector("a[href]");
  assert.equal(link.href, ACCOUNTS[2].token_url);
  assert.match(text(link), /Create a token/);
  // No one-click login here, so no way to switch to it.
  assert.equal(button(m.root, "Sign in with the browser instead"), undefined);
  const form = m.root.querySelector("form");
  const connectBtn = button(form, "Connect");
  assert.equal(connectBtn.disabled, true);
  // Submitting an empty form sends nothing.
  await submit(form);
  assert.equal(m.f.calls.filter((c) => c.method === "POST").length, 0);

  // An answer that is not a connected account is an error.
  answer = json(202, { flow_id: "x", method: "code", url: "u" });
  await type(form.querySelector("input[type=password]"), "  tok  ");
  assert.equal(connectBtn.disabled, false);
  await submit(form);
  assert.equal(alertText(m.root), "Unexpected answer from the server.");
  assert.equal(m.connected.length, 0);

  answer = json(200, { account: "me" });
  await submit(form);
  const post = m.f.calls.filter((c) => c.method === "POST").at(-1);
  assert.deepEqual(post.body, {
    service: "gitea@git.corp.io",
    method: "token",
    token: "tok",
  });
  // The gateway named no service: the one asked for stands for it.
  assert.deepEqual(m.connected, [["gitea@git.corp.io", "me"]]);
  assert.equal(m.closed.length, 1);
  await m.done();
});

test("Bitbucket needs the account name and an app password; a refusal is shown; one submit at a time", async () => {
  let release;
  let answer;
  const m = await mount(
    {
      "POST /v1/git/connect": () =>
        new Promise((r) => {
          release = () => r(answer);
        }),
    },
    { service: "bitbucket" },
  );
  assert.match(text(m.root), /Bitbucket needs an app password/);
  assert.match(text(m.root), /Account name/);
  assert.match(text(m.root), /App password/);
  const form = m.root.querySelector("form");
  const [userIn, tokenIn] = form.querySelectorAll("input");
  await type(tokenIn, "apppass");
  assert.equal(
    button(form, "Connect").disabled,
    true,
    "the user is still missing",
  );
  await type(userIn, " bob ");
  assert.equal(button(form, "Connect").disabled, false);

  answer = json(401, { error: "bad credentials" });
  await act(() => {
    form.dispatchEvent(new window.Event("submit", { cancelable: true }));
  });
  assert.equal(text(button(form, "Checking…")), "Checking…");
  // A second submit while the first is on its way sends nothing more.
  await act(() => {
    form.dispatchEvent(new window.Event("submit", { cancelable: true }));
  });
  await act(() => release());
  await settle();
  const posts = m.f.calls.filter((c) => c.method === "POST");
  assert.equal(posts.length, 1);
  assert.deepEqual(posts[0].body, {
    service: "bitbucket",
    method: "token",
    token: "apppass",
    username: "bob",
  });
  assert.equal(alertText(m.root), "bad credentials");
  assert.equal(text(button(form, "Connect")), "Connect");

  answer = json(200, { account: "bob", service: "bitbucket" });
  await act(() => {
    form.dispatchEvent(new window.Event("submit", { cancelable: true }));
  });
  await act(() => release());
  await settle();
  assert.deepEqual(m.connected, [["bitbucket", "bob"]]);
  await m.done();
});

test("a self-hosted server not connected yet: the token form, sent by kind and host", async () => {
  const m = await mount(
    {
      "POST /v1/git/connect": json(200, {
        account: "ann",
        service: "gitea@git.example.com",
      }),
    },
    { selfHosted: { kind: "gitea", host: "git.example.com" } },
  );
  assert.equal(title(m.root), "Connect git.example.com");
  assert.equal(m.root.querySelector("a[href]"), null, "no token link is known");
  const form = m.root.querySelector("form");
  await type(form.querySelector("input"), "t0k");
  await submit(form);
  const post = m.f.calls.find((c) => c.method === "POST");
  assert.deepEqual(post.body, {
    kind: "gitea",
    host: "git.example.com",
    method: "token",
    token: "t0k",
  });
  assert.deepEqual(m.connected, [["gitea@git.example.com", "ann"]]);
  await m.done();
});

test("a known self-hosted server can switch between the token and the browser login", async () => {
  const m = await mount(
    {
      "POST /v1/git/connect": json(200, { account: "ann", service: "" }),
    },
    { selfHosted: { kind: "gitlab", host: "lab.corp.io" } },
  );
  assert.equal(title(m.root), "Connect lab.corp.io");
  assert.match(text(m.root), /Register an OAuth app/);
  // Not oauth_ready, so the token comes first; but a self-hosted host may still try the browser.
  assert.ok(m.root.querySelector("form"));
  await click(button(m.root, "Sign in with the browser instead"));
  assert.equal(m.root.querySelector("form"), null);
  await click(button(m.root, "Connect lab.corp.io"));
  const post = m.f.calls.find((c) => c.method === "POST");
  assert.deepEqual(post.body, {
    service: "gitlab@lab.corp.io",
    method: "code",
  });
  // The gateway connected it at once (no flow) and named no service.
  assert.deepEqual(m.connected, [["gitlab@lab.corp.io", "ann"]]);
  assert.equal(m.closed.length, 1);
  await m.done();
});

test("a host the gateway connects at once is reported under the service it names", async () => {
  const m = await mount(
    {
      "POST /v1/git/connect": json(200, { account: "lab", service: "gitlab" }),
    },
    { service: "gitlab" },
  );
  await click(button(m.root, "Connect GitLab"));
  assert.deepEqual(m.connected, [["gitlab", "lab"]]);
  await m.done();
});

test("device login: contacting, the code to enter, copying it, then connected by the poll", async () => {
  let releaseConnect;
  const writes = [];
  Object.defineProperty(navigator, "clipboard", {
    value: { writeText: async (t) => writes.push(t) },
    configurable: true,
  });
  const m = await mount(
    {
      "POST /v1/git/connect": () =>
        new Promise((r) => {
          releaseConnect = () =>
            r(
              json(202, {
                flow_id: "fl/1",
                method: "device",
                service: "github",
                url: "https://github.com/login/device",
                code: "ABCD-1234",
              }),
            );
        }),
      "GET /v1/git/flows/": json(200, {
        status: "connected",
        account: "octo",
        service: "github",
      }),
    },
    { service: "github" },
  );
  assert.doesNotMatch(text(m.root), /never shown once ready/);
  await click(button(m.root, "Connect GitHub"));
  assert.match(text(m.root.querySelector("output")), /Contacting GitHub…/);
  // Stop the poll from answering before the pending screen is checked.
  const hold = m.f.routes["GET /v1/git/flows/"];
  let releasePoll;
  m.f.routes["GET /v1/git/flows/"] = () =>
    new Promise((r) => {
      releasePoll = () => r(hold.clone());
    });
  await act(() => releaseConnect());
  await settle();
  assert.equal(
    text(m.root.querySelector("[data-testid=device-code]")),
    "ABCD-1234",
  );
  assert.match(
    text(m.root),
    /Open GitHub, enter this code and approve the access\./,
  );
  assert.match(text(m.root), /Waiting for you to approve in GitHub…/);
  assert.equal(
    m.root.querySelector("a[href]").href,
    "https://github.com/login/device",
  );
  const copy = button(m.root, "Copy code");
  assert.equal(text(copy), "Copy");
  await click(copy);
  assert.deepEqual(writes, ["ABCD-1234"]);
  assert.equal(text(copy), "Copied");

  await act(() => releasePoll());
  await settle();
  assert.ok(m.f.calls.some((c) => c.path === "/v1/git/flows/fl%2F1"));
  assert.deepEqual(m.connected, [["github", "octo"]]);
  assert.equal(m.closed.length, 1);
  // Done: closing the modal now does not cancel anything.
  await m.done();
  assert.equal(m.f.calls.filter((c) => c.method === "DELETE").length, 0);
  delete navigator.clipboard;
});

test("the Copied label goes back to Copy; a clipboard that refuses keeps Copy", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const writes = [];
  Object.defineProperty(navigator, "clipboard", {
    value: { writeText: async (s) => writes.push(s) },
    configurable: true,
  });
  const m = await mount(
    {
      "POST /v1/git/connect": json(202, {
        flow_id: "f2",
        method: "device",
        service: "github",
        url: "https://github.com/login/device",
      }),
      "GET /v1/git/flows/": () => new Promise(() => {}),
    },
    { service: "github" },
  );
  await click(button(m.root, "Connect GitHub"));
  // The host sent no code: the box is empty and copying copies nothing.
  assert.equal(text(m.root.querySelector("[data-testid=device-code]")), "");
  const copy = button(m.root, "Copy code");
  await click(copy);
  assert.deepEqual(writes, [""]);
  assert.equal(text(copy), "Copied");
  await act(() => t.mock.timers.tick(1800));
  assert.equal(text(copy), "Copy");

  // No clipboard API and the legacy copy fails: nothing changes.
  delete navigator.clipboard;
  document.execCommand = () => false;
  await click(copy);
  assert.equal(text(copy), "Copy");
  delete document.execCommand;
  await m.done();
});

test("browser login: the redirect link, a code pasted by hand, and Cancel", async () => {
  let pasteAnswer = json(400, { error: "that code is wrong" });
  const m = await mount(
    {
      "POST /v1/git/connect": json(202, {
        flow_id: "c1",
        method: "code",
        service: "gitlab",
        url: "https://gitlab.com/oauth/authorize?x=1",
      }),
      "GET /v1/git/flows/": () => new Promise(() => {}),
      "POST /v1/git/flows/": () => pasteAnswer,
      "DELETE /v1/git/flows/": json(204),
    },
    { service: "gitlab" },
  );
  await click(button(m.root, "Connect GitLab"));
  assert.match(
    text(m.root),
    /Authorize motita in GitLab\. This page notices when you are done\./,
  );
  const a = m.root.querySelector("a[href]");
  assert.equal(a.href, "https://gitlab.com/oauth/authorize?x=1");
  assert.match(text(a), /Open GitLab to authorize/);

  const details = m.root.querySelector("details");
  assert.equal(details.open, false);
  await act(() => {
    details.open = true;
    details.dispatchEvent(new window.Event("toggle"));
  });
  assert.equal(m.root.querySelector("details").open, true);

  const send = button(m.root, "Send code");
  assert.equal(send.disabled, true);
  await type(m.root.querySelector("textarea"), "  https://x/callback?code=1  ");
  assert.equal(send.disabled, false);
  await click(send);
  assert.equal(alertText(m.root), "that code is wrong");
  const paste = m.f.calls.find((c) => c.path.endsWith("/paste"));
  assert.deepEqual(paste.body, { code: "https://x/callback?code=1" });

  pasteAnswer = json(200, {});
  await click(send);
  assert.equal(m.root.querySelector("[role=alert]"), null);
  assert.match(text(m.root), /Code sent\. Finishing the login…/);

  await click(button(m.root, "Cancel"));
  assert.ok(
    m.f.calls.some(
      (c) => c.method === "DELETE" && c.path === "/v1/git/flows/c1",
    ),
  );
  // Back to the start: the one-click login is offered again.
  assert.ok(button(m.root, "Connect GitLab"));
  await m.done();
  // Already cancelled, so closing does not cancel it again.
  assert.equal(m.f.calls.filter((c) => c.method === "DELETE").length, 1);
});

test("closing the modal while a login is pending cancels that login", async () => {
  const m = await mount(
    {
      "POST /v1/git/connect": json(202, {
        flow_id: "p1",
        method: "device",
        service: "github",
        url: "u",
        code: "C",
      }),
      "GET /v1/git/flows/": () => new Promise(() => {}),
      "DELETE /v1/git/flows/": json(204),
    },
    { service: "github" },
  );
  await click(button(m.root, "Connect GitHub"));
  await m.done();
  await flush();
  assert.deepEqual(
    m.f.calls.filter((c) => c.method === "DELETE").map((c) => c.path),
    ["/v1/git/flows/p1"],
  );
  assert.equal(m.connected.length, 0);
});

test("a host that cannot start the login offers the token, with a way back to the browser", async () => {
  const m = await mount(
    { "POST /v1/git/connect": json(502, { error: "upstream 500" }) },
    { service: "gitlab" },
  );
  await click(button(m.root, "Connect GitLab"));
  assert.match(
    text(m.root.querySelector("output")),
    /GitLab could not start the login: upstream 500\. You can connect with a token instead\./,
  );
  assert.ok(m.root.querySelector("form"));
  await click(button(m.root, "Sign in with the browser instead"));
  assert.equal(m.root.querySelector("form"), null);
  await click(button(m.root, "Use a token instead"));
  assert.ok(m.root.querySelector("form"));
  await m.done();
});

test("a failed start shows the error with Retry and Use a token instead", async () => {
  const m = await mount(
    { "POST /v1/git/connect": json(500, { error: "gateway broke" }) },
    { service: "github" },
  );
  await click(button(m.root, "Connect GitHub"));
  assert.equal(alertText(m.root), "gateway broke");
  await click(button(m.root, "Retry"));
  assert.ok(button(m.root, "Connect GitHub"), "back to the one-click login");
  assert.equal(m.root.querySelector("[role=alert]"), null);

  await click(button(m.root, "Connect GitHub"));
  await click(button(m.root, "Use a token instead"));
  assert.ok(m.root.querySelector("form"));
  assert.equal(m.root.querySelector("[role=alert]"), null);
  await m.done();
});

test("a login the host refused ends in the error, and closing then cancels nothing", async () => {
  const m = await mount(
    {
      "POST /v1/git/connect": json(202, {
        flow_id: "r1",
        method: "device",
        service: "github",
        url: "u",
        code: "C",
      }),
      "GET /v1/git/flows/": json(200, {
        status: "failed",
        error: "access_denied",
      }),
      "DELETE /v1/git/flows/": json(204),
    },
    { service: "github" },
  );
  await click(button(m.root, "Connect GitHub"));
  assert.equal(alertText(m.root), "access_denied");
  assert.equal(m.connected.length, 0);
  await m.done();
  assert.equal(m.f.calls.filter((c) => c.method === "DELETE").length, 0);
});

test("the poll waits between questions, and a status without names falls back to the account", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let polls = 0;
  const m = await mount(
    {
      "POST /v1/git/connect": json(202, {
        flow_id: "w1",
        method: "device",
        service: "github",
        url: "u",
        code: "C",
      }),
      "GET /v1/git/flows/": () => {
        polls++;
        return polls === 1
          ? json(200, { status: "pending" })
          : json(200, { status: "connected" });
      },
    },
    { service: "github" },
  );
  await click(button(m.root, "Connect GitHub"));
  assert.equal(polls, 1);
  assert.equal(m.connected.length, 0);
  await act(() => t.mock.timers.tick(1999));
  await settle();
  assert.equal(polls, 1, "not asked again before the interval");
  await act(() => t.mock.timers.tick(1));
  await settle();
  assert.equal(polls, 2);
  assert.deepEqual(m.connected, [["github", ""]]);
  await m.done();
});

test("whenever the modal closes during the last answer, that answer connects nothing", async () => {
  for (let hops = 0; hops < 12; hops++) {
    let release;
    const m = await mount(
      {
        "POST /v1/git/connect": json(202, {
          flow_id: "z" + hops,
          method: "device",
          service: "github",
          url: "u",
          code: "C",
        }),
        "GET /v1/git/flows/": () =>
          new Promise((r) => {
            release = () =>
              r(json(200, { status: "connected", account: "octo" }));
          }),
        "DELETE /v1/git/flows/": json(204),
      },
      { service: "github" },
    );
    await click(button(m.root, "Connect GitHub"));
    release();
    for (let i = 0; i < hops; i++) {
      await Promise.resolve();
    }
    if (m.connected.length > 0) {
      // The answer landed before the close, so this hop is past the boundary the test is
      // about: the modal was still open when the connection arrived, and connecting there is
      // the right thing. How many microtasks that takes depends on the engine, so stop at the
      // boundary instead of asserting on a count that only holds on some Node versions.
      assert.ok(hops > 0, "the answer landed before the first microtask");
      m.root.remove();
      break;
    }
    // act flushes the unmount's effect cleanups at once instead of after a paint.
    const closing = act(() => render(null, m.root));
    await closing;
    await flush();
    await flush();
    assert.deepEqual(m.connected, [], `closed after ${hops} microtasks`);
    assert.equal(m.closed.length, 0);
    m.root.remove();
  }
});

test("a malformed accounts answer is an empty list", async () => {
  const m = await mount({
    "GET /v1/git/accounts": json(200, { accounts: null }),
  });
  assert.match(text(m.root), /Which git host do you want to connect\?/);
  assert.equal(m.root.querySelectorAll("li").length, 0);
  assert.equal(m.root.querySelector("[role=alert]"), null);
  await m.done();
});

test("an answer that names neither account nor service is passed on as empty names", async () => {
  // A new self-hosted server: no account id, so nothing to fall back on.
  const m = await mount(
    { "POST /v1/git/connect": json(200, {}) },
    { selfHosted: { kind: "gitlab", host: "new.corp.io" } },
  );
  const form = m.root.querySelector("form");
  await type(form.querySelector("input"), "tok");
  await submit(form);
  assert.deepEqual(m.connected, [["", ""]]);
  await m.done();

  // A listed host: the one-click login that connects at once keeps the service asked for.
  const m2 = await mount(
    { "POST /v1/git/connect": json(200, {}) },
    { service: "gitlab" },
  );
  await click(button(m2.root, "Connect GitLab"));
  assert.deepEqual(m2.connected, [["gitlab", ""]]);
  await m2.done();
});

test("the poll rides out a failing gateway, and a login it no longer knows has expired", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const answers = [
    json(500, { error: "blip" }),
    json(404, { error: "no such flow" }),
  ];
  const m = await mount(
    {
      "POST /v1/git/connect": json(202, {
        flow_id: "e1",
        method: "device",
        service: "github",
        url: "u",
        code: "C",
      }),
      "GET /v1/git/flows/": () => answers.shift(),
    },
    { service: "github" },
  );
  await click(button(m.root, "Connect GitHub"));
  assert.match(text(m.root), /Waiting for you to approve in GitHub…/);
  assert.equal(m.root.querySelector("[role=alert]"), null);
  await act(() => t.mock.timers.tick(2000));
  await settle();
  assert.equal(alertText(m.root), "The login expired. Try again.");
  assert.equal(answers.length, 0);
  await m.done();
});

test("closing the modal between two questions stops the poll", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let polls = 0;
  const m = await mount(
    {
      "POST /v1/git/connect": json(202, {
        flow_id: "s1",
        method: "device",
        service: "github",
        url: "u",
        code: "C",
      }),
      "GET /v1/git/flows/": () => {
        polls++;
        return json(200, { status: "pending" });
      },
      "DELETE /v1/git/flows/": json(204),
    },
    { service: "github" },
  );
  await click(button(m.root, "Connect GitHub"));
  assert.equal(polls, 1);
  await m.done();
  await act(() => t.mock.timers.tick(2000));
  await settle();
  assert.equal(polls, 1, "not asked again once closed");
  assert.ok(
    m.f.calls.some(
      (c) => c.method === "DELETE" && c.path === "/v1/git/flows/s1",
    ),
  );
});

for (const [last, shown] of [
  [json(503, { error: "gateway busy" }), "gateway busy"],
  ["connection reset", "connection reset"], // thrown, and not even an Error
]) {
  test(`five failed questions in a row end the login with the last error: ${shown}`, async (t) => {
    t.mock.timers.enable({ apis: ["setTimeout"] });
    let polls = 0;
    const m = await mount(
      {
        "POST /v1/git/connect": json(202, {
          flow_id: "m1",
          method: "device",
          service: "github",
          url: "u",
          code: "C",
        }),
        "GET /v1/git/flows/": () => {
          polls++;
          if (polls < 5) {
            return json(503, { error: "busy " + polls });
          }
          if (typeof last === "string") {
            throw last;
          }
          return last;
        },
      },
      { service: "github" },
    );
    await click(button(m.root, "Connect GitHub"));
    for (let i = 1; i < 5; i++) {
      assert.equal(
        m.root.querySelector("[role=alert]"),
        null,
        `still waiting after ${i}`,
      );
      await act(() => t.mock.timers.tick(2000));
      await settle();
    }
    assert.equal(polls, 5);
    assert.equal(alertText(m.root), shown);
    await m.done();
  });
}

test("a failing answer that arrives after the modal closed changes nothing", async () => {
  let release;
  const m = await mount(
    {
      "POST /v1/git/connect": json(202, {
        flow_id: "q1",
        method: "device",
        service: "github",
        url: "u",
        code: "C",
      }),
      "GET /v1/git/flows/": () =>
        new Promise((r) => {
          release = () => r(json(500, { error: "late failure" }));
        }),
      "DELETE /v1/git/flows/": json(204),
    },
    { service: "github" },
  );
  await click(button(m.root, "Connect GitHub"));
  await m.done();
  release();
  await flush();
  await flush();
  const gets = m.f.calls.filter(
    (c) => c.path === "/v1/git/flows/q1" && c.method === "GET",
  );
  assert.equal(gets.length, 1, "no second question");
  assert.equal(m.root.querySelector("[role=alert]"), null);
  assert.deepEqual(m.connected, []);
});
