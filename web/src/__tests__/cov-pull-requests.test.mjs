// The pull request overview: the client and its words, the store that opens it, and the modal that
// lists every project's open pull requests with their CI.
// Run with: node --test web/src/__tests__/cov-pull-requests.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  bundle,
  h,
  mount,
  act,
  click,
  flush,
  fakeApi,
  res,
} from "./cov-projects-helpers.mjs";

const {
  AppCtx,
  PullRequestsModal,
  fetchPullRequests,
  ciBadge,
  mergeNote,
  failingFirst,
  openPullRequests,
  closePullRequests,
} = await bundle({
  "PullRequestsModal.tsx": ["PullRequestsModal"],
  "pullRequestsApi.ts": [
    "fetchPullRequests",
    "ciBadge",
    "mergeNote",
    "failingFirst",
  ],
  "pullRequestsOpen.ts": ["openPullRequests", "closePullRequests"],
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
const checks = (...states) =>
  states.map((state, i) => ({ name: "c" + i, state }));
const q = (id) => document.querySelector(`[data-testid=${id}]`);
const qa = (id) => [...document.querySelectorAll(`[data-testid=${id}]`)];

test("the client asks for the overview, with refresh on demand, and reports a refusal", async () => {
  const api = fakeApi({
    "GET /v1/pull-requests?refresh=1": res(200, { pull_requests: [] }),
    "GET /v1/pull-requests": res(200, { pull_requests: [item(1)] }),
  });
  assert.equal((await fetchPullRequests(api)).pull_requests.length, 1);
  assert.equal((await fetchPullRequests(api, true)).pull_requests.length, 0);
  assert.deepEqual(
    api.calls.map((c) => c.path),
    ["/v1/pull-requests", "/v1/pull-requests?refresh=1"],
  );
  const bad = fakeApi({ "GET ": res(500, { error: "host down" }) });
  await assert.rejects(fetchPullRequests(bad), /host down/);
  const bare = fakeApi({ "GET ": res(502, new Error("not json")) });
  await assert.rejects(fetchPullRequests(bare), /HTTP 502/);
});

test("the CI badge says how it stands and how many checks passed", () => {
  assert.deepEqual(ciBadge(item(1)), {
    label: "none",
    detail: "",
    tone: "idle",
  });
  const of = (state, cs) => ciBadge(item(1, { ci: { state, checks: cs } }));
  assert.deepEqual(of("success", checks("success", "success")), {
    label: "success",
    detail: "2/2",
    tone: "good",
  });
  assert.deepEqual(of("failure", checks("success", "failure")), {
    label: "failure",
    detail: "1/2",
    tone: "bad",
  });
  assert.equal(of("pending", checks("pending")).tone, "wait");
  assert.deepEqual(of("none", []), { label: "none", detail: "", tone: "idle" });
});

test("only a reason the person can act on is a merge note", () => {
  const m = (code) => mergeNote(item(1, code ? { merge: { code } } : {}));
  assert.equal(m(undefined), null);
  assert.equal(m("ok"), null);
  assert.equal(m("unknown"), null);
  assert.equal(m("behind"), "behind");
  assert.equal(m("conflict"), "conflict");
});

test("failing pull requests come first, then conflicts, then the running ones", () => {
  const ci = (state) => ({ ci: { state, checks: [] } });
  const items = [
    item(1, ci("success")),
    item(2, ci("pending")),
    item(3, { merge: { code: "conflict" }, ...ci("success") }),
    item(4, ci("failure")),
    item(5),
    item(6, ci("failure")),
  ];
  assert.deepEqual(
    failingFirst(items).map((i) => i.pr.number),
    [4, 6, 3, 2, 1, 5],
  );
});

test("the modal is closed until it is opened, then lists the pull requests", async () => {
  const api = fakeApi({
    "GET /v1/pull-requests": res(200, {
      pull_requests: [
        item(1, {
          ci: { state: "success", checks: checks("success") },
          session: "s1",
        }),
        item(2, {
          ci: { state: "failure", checks: checks("failure", "success") },
          merge: { code: "behind" },
          session: "s2",
          watch: { status: "fixing", attempts: 1, max: 5 },
        }),
        { ...item(3), pr: { number: 3, url: "u", title: "bare" } },
        {
          ...item(4, { ci: { state: "pending", checks: [] } }),
          pr: { number: 4, url: "u", title: "new", head: "b4" },
        },
      ],
      problems: [{ project: "Other", error: "it has no remote named origin" }],
    }),
  });
  const m = await mount(AppCtx, { api }, h(PullRequestsModal));
  assert.equal(q("pull-requests"), null);
  await act(() => openPullRequests());
  await flush();
  const rows = qa("pr-row");
  assert.deepEqual(
    rows.map((r) => r.querySelector("a").textContent),
    ["#2 feat: 2", "#4 new", "#1 feat: 1", "#3 bare"],
    "the failing one first",
  );
  assert.equal(
    rows[0].querySelector("a").href,
    "https://github.com/o/r/pull/2",
  );
  assert.match(qa("pr-ci")[0].textContent, /failing 1\/2/);
  assert.match(qa("pr-ci")[1].textContent, /running/);
  assert.match(qa("pr-ci")[2].textContent, /passing 1\/1/);
  assert.match(qa("pr-ci")[3].textContent, /no checks/);
  assert.match(rows[1].textContent, /Motita · o\/r · b4 → $/);
  assert.match(q("pr-merge").textContent, /Out of date/);
  assert.match(qa("pr-follow")[0].textContent, /following: fixing/);
  assert.match(qa("pr-follow")[1].textContent, /has a session/);
  assert.equal(qa("pr-follow").length, 2);
  assert.match(rows[0].textContent, /Motita · o\/r · b2 → main/);
  assert.match(q("pr-problem").textContent, /Other: it has no remote/);
  assert.equal(q("pr-error"), null);
  await act(() => closePullRequests());
  assert.equal(q("pull-requests"), null);
  await m.unmount();
});

test("refresh asks the gateway again, and an empty or failed read says so", async () => {
  let answer = res(200, { pull_requests: [] });
  const api = fakeApi({ "GET /v1/pull-requests": () => answer });
  const m = await mount(AppCtx, { api }, h(PullRequestsModal));
  await act(() => openPullRequests());
  await flush();
  assert.match(q("pr-empty").textContent, /No open pull requests/);
  answer = res(500, { error: "host down" });
  await click(q("pr-refresh"));
  await flush();
  assert.match(q("pr-error").textContent, /host down/);
  assert.equal(api.calls.at(-1).path, "/v1/pull-requests?refresh=1");
  answer = res(200, { pull_requests: [item(1)] });
  await click(q("pr-refresh"));
  await flush();
  assert.equal(q("pr-error"), null);
  assert.equal(qa("pr-row").length, 1);
  assert.equal(q("pr-problem"), null);
  await click(document.querySelector("[aria-label=Close]"));
  assert.equal(q("pull-requests"), null);
  await m.unmount();
});

test("without a gateway connection nothing is read", async () => {
  const m = await mount(AppCtx, { api: null }, h(PullRequestsModal));
  await act(() => openPullRequests());
  await flush();
  assert.equal(qa("pr-row").length, 0);
  await act(() => closePullRequests());
  await m.unmount();
});
