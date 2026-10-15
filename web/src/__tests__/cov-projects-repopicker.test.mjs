// The repository picker: the person's repositories on a connected host, searched, paged, picked
// with the mouse or the keyboard, and what it does when the host refuses or fails.
// Run with: node --test web/src/__tests__/cov-projects-repopicker.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  bundle,
  h,
  act,
  mount,
  click,
  type,
  change,
  key,
  flush,
  res,
  buttonByText,
} from "./cov-projects-helpers.mjs";

const { AppCtx, RepoPicker, relTimeText } = await bundle({
  "RepoPicker.tsx": ["RepoPicker", "relTimeText"],
});

const ACCOUNTS = [
  { id: "github", name: "GitHub", username: "ada", connected: true },
  { id: "gitlab", name: "GitLab", connected: true },
];

const repo = (n, extra = {}) => ({
  full_name: "ada/" + n,
  clone_url: "https://github.com/ada/" + n + ".git",
  ...extra,
});

// picker mounts the picker over a gateway that answers each page with `pages(service, q, page)`.
async function picker(pages, props = {}) {
  const calls = [];
  const log = [];
  const api = async (path) => {
    const u = new URL(path, "http://x");
    const c = {
      service: u.searchParams.get("service"),
      q: u.searchParams.get("q") || "",
      page: Number(u.searchParams.get("page")),
    };
    calls.push(c);
    return pages(c.service, c.q, c.page);
  };
  const m = await mount(
    AppCtx,
    null,
    h(RepoPicker, {
      api,
      accounts: ACCOUNTS,
      onPick: (r, s) => log.push(["pick", r.full_name, s]),
      onConnectAnother: () => log.push(["connect"]),
      onAuthRequired: (s) => log.push(["auth", s]),
      onClose: () => log.push(["close"]),
      ...props,
    }),
  );
  await flush();
  return { m, calls, log };
}

const options = (m) => m.qa("[role=option]");
const search = (m) => m.q("input[type=search]");

test("how long ago a repository was updated", () => {
  const ago = (s) => new Date(Date.now() - s * 1000).toISOString();
  assert.equal(relTimeText(undefined), "");
  assert.equal(relTimeText("not a date"), "");
  assert.equal(relTimeText(ago(10)), "just now");
  assert.equal(relTimeText(ago(5 * 60)), "5m ago");
  assert.equal(relTimeText(ago(3 * 3600)), "3h ago");
  assert.equal(relTimeText(ago(2 * 86400)), "2d ago");
  assert.equal(relTimeText(ago(60 * 86400)), "2mo ago");
  assert.equal(relTimeText(ago(800 * 86400)), "2y ago");
});

test("the picker lists the first host's repositories, private ones marked", async () => {
  const p = await picker(() =>
    res(200, {
      repos: [
        repo("site", { private: true, description: "The site" }),
        repo("lib", { updated_at: new Date().toISOString() }),
      ],
    }),
  );
  assert.deepEqual(p.calls, [{ service: "github", q: "", page: 1 }]);
  const host = p.m.q("select");
  assert.equal(host.value, "github");
  assert.deepEqual(
    [...host.options].map((o) => o.textContent),
    ["GitHub (ada)", "GitLab", "Connect another host…"],
  );
  const rows = options(p.m);
  assert.equal(rows.length, 2);
  assert.match(rows[0].textContent, /ada\/site/);
  assert.match(rows[0].textContent, /The site/);
  assert.ok(rows[0].querySelector("[aria-label=Private]"));
  assert.equal(rows[1].querySelector("[aria-label=Private]"), null);
  assert.match(rows[1].textContent, /just now/);
  assert.equal(rows[0].getAttribute("aria-selected"), "true");
  assert.equal(search(p.m).getAttribute("aria-activedescendant"), "repo-opt-0");
  assert.equal(p.m.q("output"), null, "no longer loading");
  await p.m.unmount();
});

test("the picker opens on the host asked for, when it is connected", async () => {
  const p = await picker(() => res(200, { repos: [] }), {
    initialService: "gitlab",
  });
  assert.equal(p.calls[0].service, "gitlab");
  assert.match(p.m.root.textContent, /No repositories found in GitLab\./);
  assert.equal(search(p.m).getAttribute("aria-activedescendant"), null);
  await p.m.unmount();
  const q = await picker(() => res(200, { repos: [] }), {
    initialService: "bitbucket",
  });
  assert.equal(q.calls[0].service, "github");
  await q.m.unmount();
});

test("with no connected host nothing is asked and it keeps loading", async () => {
  const p = await picker(() => res(200, { repos: [] }), { accounts: [] });
  assert.equal(p.calls.length, 0);
  assert.match(p.m.q("output").textContent, /Loading repositories…/);
  await p.m.unmount();
});

test("a host with no name says it found nothing there", async () => {
  const p = await picker(() => res(200, { repos: [] }), {
    accounts: [{ id: "self", name: "", connected: true }],
  });
  assert.match(p.m.root.textContent, /No repositories found in \./);
  await p.m.unmount();
});

test("the keyboard moves through the list and Enter picks", async () => {
  const scrolled = [];
  window.HTMLElement.prototype.scrollIntoView = function () {
    scrolled.push(this.id);
  };
  try {
    const p = await picker(() =>
      res(200, { repos: [repo("a"), repo("b"), repo("c")] }),
    );
    const input = search(p.m);
    await key(input, "ArrowDown");
    await key(input, "ArrowDown");
    await key(input, "ArrowDown");
    assert.equal(input.getAttribute("aria-activedescendant"), "repo-opt-2");
    await key(input, "ArrowUp");
    await key(input, "ArrowUp");
    await key(input, "ArrowUp");
    assert.equal(input.getAttribute("aria-activedescendant"), "repo-opt-0");
    await key(input, "ArrowDown");
    await key(input, "x");
    await key(input, "Enter");
    assert.deepEqual(p.log, [["pick", "ada/b", "github"]]);
    assert.ok(scrolled.includes("repo-opt-2"));
    await p.m.unmount();
  } finally {
    delete window.HTMLElement.prototype.scrollIntoView;
  }
});

test("Enter on an empty list picks nothing", async () => {
  const p = await picker(() => res(200, { repos: [] }));
  await key(search(p.m), "Enter");
  assert.deepEqual(p.log, []);
  await p.m.unmount();
});

test("the mouse highlights and picks a row; Enter on a focused row picks it", async () => {
  const p = await picker(() => res(200, { repos: [repo("a"), repo("b")] }));
  const [a, b] = options(p.m);
  await act(() => {
    b.dispatchEvent(new MouseEvent("mouseenter"));
    b.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
  });
  assert.equal(options(p.m)[1].getAttribute("aria-selected"), "true");
  await click(a);
  await key(b, "Enter");
  assert.deepEqual(p.log, [
    ["pick", "ada/a", "github"],
    ["pick", "ada/b", "github"],
  ]);
  await p.m.unmount();
});

test("more pages are loaded on demand, without repeating a repository", async () => {
  const p = await picker((s, q, page) => {
    if (page === 1) {
      return res(200, { repos: [repo("a"), repo("b")], more: true });
    }
    return res(200, { repos: [repo("b"), repo("c")], more: false });
  });
  await key(search(p.m), "ArrowDown");
  await click(buttonByText(p.m.root, "Load more"));
  await flush();
  assert.deepEqual(p.calls.at(-1), { service: "github", q: "", page: 2 });
  assert.deepEqual(
    options(p.m).map((o) => o.textContent.replace(/[^a-z/]/g, "")),
    ["ada/a", "ada/b", "ada/c"],
  );
  assert.equal(
    search(p.m).getAttribute("aria-activedescendant"),
    "repo-opt-1",
    "the highlighted row stays where it was",
  );
  assert.equal(buttonByText(p.m.root, "Load more"), undefined);
  await p.m.unmount();
});

test("searching waits for the box to stay still, and says when nothing matches", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const p = await picker((s, q) =>
    res(200, { repos: q ? [] : [repo("a")] }),
  );
  await type(search(p.m), "zz");
  await act(() => t.mock.timers.tick(100));
  await type(search(p.m), "zzz ");
  await act(() => t.mock.timers.tick(300));
  await flush();
  assert.deepEqual(
    p.calls.map((c) => c.q),
    ["", "zzz"],
  );
  assert.match(p.m.root.textContent, /No repositories match "zzz"\./);
  await p.m.unmount();
});

test("an answer overtaken by a newer request is dropped", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const pending = [];
  const p = await picker(
    () => new Promise((ok, no) => pending.push({ ok, no })),
  );
  await type(search(p.m), "a");
  await act(() => t.mock.timers.tick(300));
  await type(search(p.m), "ab");
  await act(() => t.mock.timers.tick(300));
  assert.equal(pending.length, 3);
  pending[0].ok(res(200, { repos: [repo("old")] }));
  pending[1].no(new Error("old failure"));
  await flush();
  assert.equal(options(p.m).length, 0);
  assert.equal(p.m.q("[role=alert]"), null);
  pending[2].ok(res(200, { repos: [repo("new")] }));
  await flush();
  assert.equal(options(p.m).length, 1);
  await p.m.unmount();
});

test("an answer that arrives once the picker is closed is dropped", async () => {
  let answer;
  const p = await picker(() => new Promise((ok) => (answer = ok)));
  await p.m.unmount();
  answer(res(200, { repos: [repo("late")] }));
  await flush();
  assert.deepEqual(p.log, []);
});

test("a failure is shown with a retry", async () => {
  let n = 0;
  const p = await picker(() => {
    n++;
    if (n === 1) {
      return res(500, { error: "host down" });
    }
    if (n === 2) {
      throw new Error("offline");
    }
    return res(200, { repos: [repo("a")], more: true });
  });
  assert.equal(p.m.q("[role=alert]").textContent, "host downRetry");
  assert.equal(buttonByText(p.m.root, "Load more"), undefined);
  await click(buttonByText(p.m.q("[role=alert]"), "Retry"));
  await flush();
  assert.match(p.m.q("[role=alert]").textContent, /offline/);
  await click(buttonByText(p.m.q("[role=alert]"), "Retry"));
  await flush();
  assert.equal(p.m.q("[role=alert]"), null);
  assert.equal(options(p.m).length, 1);
  await p.m.unmount();
});

test("a host that refuses the saved credentials hands over to the connect flow", async () => {
  const p = await picker((s) =>
    res(409, {
      code: "git_auth_required",
      service: s === "github" ? "github-enterprise" : undefined,
    }),
  );
  assert.deepEqual(p.log, [["auth", "github-enterprise"]]);
  await change(p.m.q("select"), "gitlab");
  await flush();
  assert.deepEqual(p.log.at(-1), ["auth", "gitlab"]);
  await p.m.unmount();
});

test("another host is picked from the selector, or connected", async () => {
  const p = await picker((s) => res(200, { repos: [repo(s)] }));
  const sel = p.m.q("select");
  await change(sel, "gitlab");
  await flush();
  assert.equal(p.calls.at(-1).service, "gitlab");
  assert.match(options(p.m)[0].textContent, /ada\/gitlab/);
  await change(sel, "__connect");
  assert.equal(sel.value, "gitlab", "the selector stays on the host");
  assert.deepEqual(p.log, [["connect"]]);
  await p.m.unmount();
});

test("the picker closes from its button, Escape or the backdrop", async () => {
  const p = await picker(() => res(200, { repos: [] }));
  await click(p.m.q("[aria-label=Close]"));
  await act(() => {
    document.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
    );
  });
  await click(p.m.q("[data-testid=repo-picker]"));
  assert.deepEqual(p.log, [["close"], ["close"], ["close"]]);
  await p.m.unmount();
});
