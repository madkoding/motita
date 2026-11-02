// A project box shows its open pull requests as rows: each a link to the pull request, with its CI
// state, read from the overview the sidebar's badge reads and shares; while a CI runs it looks again
// sooner.
// Run with: node --test web/src/__tests__/cov-project-pull-requests.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  bundle,
  h,
  mount,
  act,
  flush,
  fakeApi,
  res,
} from "./cov-projects-helpers.mjs";

const {
  AppCtx,
  ProjectPullRequests,
  ciWord,
  publishPullRequests,
  ciRunning,
  usePullRequestBadge,
} = await bundle({
  "ProjectPullRequests.tsx": ["ProjectPullRequests", "ciWord"],
  "pullRequestsLive.ts": ["publishPullRequests", "ciRunning"],
  "usePullRequestBadge.ts": ["usePullRequestBadge"],
});

const item = (n, over = {}) => ({
  project_id: "p1",
  project: "Motita",
  repo: "o/r",
  pr: {
    number: n,
    url: "https://github.com/o/r/pull/" + n,
    title: "feat: " + n,
    head: "b" + n,
    base: "main",
  },
  ...over,
});
const ci = (state, ...states) => ({
  ci: { state, checks: states.map((s, i) => ({ name: "c" + i, state: s })) },
});
const qa = (sel) => [...document.querySelectorAll(sel)];

test("the CI state in words", () => {
  assert.equal(ciWord("success"), "passing");
  assert.equal(ciWord("failure"), "failing");
  assert.equal(ciWord("pending"), "running");
  assert.equal(ciWord("none"), "no checks");
});

test("a running CI is told apart from the rest", () => {
  assert.equal(
    ciRunning({ pull_requests: [item(1), item(2, ci("success"))] }),
    false,
  );
  assert.equal(ciRunning({ pull_requests: [item(1, ci("pending"))] }), true);
});

test("a project shows only its own pull requests, as links with their CI, failing first", async () => {
  publishPullRequests(null);
  const m = await mount(
    AppCtx,
    {},
    h(ProjectPullRequests, { projectId: "p1" }),
  );
  assert.equal(
    qa("[data-testid=project-pr]").length,
    0,
    "nothing known, no rows",
  );
  await act(() =>
    publishPullRequests({
      pull_requests: [
        item(1, ci("success", "success", "success")),
        item(2, ci("failure", "success", "failure")),
        item(3, ci("pending", "pending")),
        { ...item(4), pr: { number: 4, url: "u4", title: "bare" } },
        item(5, { project_id: "p2" }),
      ],
    }),
  );
  const rows = qa("[data-testid=project-pr]");
  assert.deepEqual(
    rows.map((r) => r.getAttribute("href")),
    [
      "https://github.com/o/r/pull/2",
      "https://github.com/o/r/pull/3",
      "https://github.com/o/r/pull/1",
      "u4",
    ],
  );
  assert.equal(rows[0].getAttribute("target"), "_blank");
  assert.equal(rows[0].getAttribute("rel"), "noopener noreferrer");
  assert.match(rows[0].textContent, /#2 feat: 2/);
  const cis = qa("[data-testid=project-pr-ci]").map((c) => c.textContent);
  assert.deepEqual(cis, [
    "CI failing 1/2",
    "CI running 0/1",
    "CI passing 2/2",
    "CI no checks",
  ]);
  assert.deepEqual(
    qa(".pr-ci-mark").map((m) => m.className),
    [
      "pr-ci-mark is-bad",
      "pr-ci-mark is-wait",
      "pr-ci-mark is-good",
      "pr-ci-mark is-idle",
    ],
  );
  assert.equal(
    rows[0].getAttribute("title"),
    "Open pull request #2 on its host\nb2 → main\nc0: passing\nc1: failing",
  );
  assert.equal(
    rows[3].getAttribute("title"),
    "Open pull request #4 on its host",
  );
  const half = {
    ...item(6, ci("none")),
    pr: { number: 6, url: "u6", head: "h" },
  };
  await act(() => publishPullRequests({ pull_requests: [half] }));
  assert.equal(
    qa("[data-testid=project-pr]")[0].getAttribute("title"),
    "Open pull request #6 on its host\nh → ",
  );
  await act(() => publishPullRequests(null));
  assert.equal(qa("[data-testid=project-pr]").length, 0, "forgotten, gone");
  await m.unmount();
});

test("the badge shares each read with the projects and looks again sooner while a CI runs", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let list = [item(1, ci("pending", "pending"))];
  const api = fakeApi({
    "GET /v1/pull-requests": () =>
      res(200, { pull_requests: list, failing: 0 }),
  });
  const Both = ({ api: a }) =>
    h(
      "div",
      {},
      h("b", {}, usePullRequestBadge(a)),
      h(ProjectPullRequests, { projectId: "p1" }),
    );
  const m = await mount(AppCtx, {}, h(Both, { api }));
  await flush();
  assert.match(qa("[data-testid=project-pr-ci]")[0].textContent, /running/);
  list = [item(1, ci("success", "success"))];
  await act(() => t.mock.timers.tick(30_000));
  await flush();
  assert.equal(api.calls.length, 2, "half a minute while it runs");
  assert.match(qa("[data-testid=project-pr-ci]")[0].textContent, /passing/);
  await act(() => t.mock.timers.tick(30_000));
  await flush();
  assert.equal(api.calls.length, 2, "a passed CI waits the full minute");
  await act(() => t.mock.timers.tick(30_000));
  await flush();
  assert.equal(api.calls.length, 3);
  await m.rerender({}, h(Both, { api: null }));
  assert.equal(qa("[data-testid=project-pr]").length, 0, "no gateway, no rows");
  await m.unmount();
});

test("a read that fails after the badge is gone schedules nothing", async () => {
  let fail;
  const api = fakeApi({
    "GET /v1/pull-requests": () =>
      new Promise((_, reject) => {
        fail = () => reject(new Error("down"));
      }),
  });
  const m = await mount(
    AppCtx,
    {},
    h(() => h("b", {}, usePullRequestBadge(api))),
  );
  await m.unmount();
  fail();
  await flush();
  assert.equal(api.calls.length, 1);
});
