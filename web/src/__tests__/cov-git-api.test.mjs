// The git host, pull request and run-link clients and their pure rules: what each request sends,
// how each answer and refusal comes back, and the edge cases of the polling and the helpers.
// Run with: node --test web/src/__tests__/cov-git-api.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { fakeApi, json } from "./cov-git-helpers.mjs";

const g = await import("../gitApi.ts");
const p = await import("../prApi.ts");
const rl = await import("../runLink.ts");

// ── gitApi ──────────────────────────────────────────────────────────────────

test("GitError.authRequired is only a 409 with git_auth_required", () => {
  assert.equal(new g.GitError("x", 409, "git_auth_required").authRequired, true);
  assert.equal(new g.GitError("x", 409, "other").authRequired, false);
  assert.equal(new g.GitError("x", 500, "git_auth_required").authRequired, false);
  const e = new g.GitError("m", 409, "c", "github");
  assert.equal(e.message, "m");
  assert.equal(e.service, "github");
});

test("listAccounts returns the list, an empty one for a malformed body, and throws a refusal", async () => {
  const accts = [{ id: "github", connected: true }];
  let f = fakeApi({ "GET /v1/git/accounts": json(200, { accounts: accts }) });
  assert.deepEqual(await g.listAccounts(f.api), accts);
  assert.equal(f.calls[0].path, "/v1/git/accounts");

  f = fakeApi({ "GET /v1/git/accounts": json(200, { accounts: "nope" }) });
  assert.deepEqual(await g.listAccounts(f.api), []);

  f = fakeApi({
    "GET /v1/git/accounts": json(409, {
      error: "log in first",
      code: "git_auth_required",
      service: "gitlab",
    }),
  });
  await assert.rejects(g.listAccounts(f.api), (e) => {
    assert.ok(e instanceof g.GitError);
    assert.equal(e.message, "log in first");
    assert.equal(e.status, 409);
    assert.equal(e.authRequired, true);
    assert.equal(e.service, "gitlab");
    return true;
  });

  // A refusal without a JSON body is told by its status.
  f = fakeApi({
    "GET /v1/git/accounts": new Response("oops", { status: 500 }),
  });
  await assert.rejects(g.listAccounts(f.api), /HTTP 500/);
});

test("connect posts the request and tells a started flow from a connected account", async () => {
  const flow = { flow_id: "f1", method: "device", service: "github", url: "u" };
  let f = fakeApi({ "POST /v1/git/connect": json(202, flow) });
  const r = await g.connect(f.api, { service: "github", method: "device" });
  assert.deepEqual(r, { type: "flow", flow });
  assert.deepEqual(f.calls[0].body, { service: "github", method: "device" });

  // A 200 that still carries a flow id is a flow too.
  f = fakeApi({ "POST /v1/git/connect": json(200, flow) });
  assert.equal((await g.connect(f.api, {})).type, "flow");

  f = fakeApi({
    "POST /v1/git/connect": json(200, { account: "octo", service: "github" }),
  });
  assert.deepEqual(await g.connect(f.api, { method: "token" }), {
    type: "connected",
    account: "octo",
    service: "github",
  });

  // Missing fields fall back to the request's service, then to "".
  f = fakeApi({ "POST /v1/git/connect": json(200, {}) });
  assert.deepEqual(await g.connect(f.api, { service: "gitlab" }), {
    type: "connected",
    account: "",
    service: "gitlab",
  });
  assert.deepEqual(await g.connect(f.api, {}), {
    type: "connected",
    account: "",
    service: "",
  });

  f = fakeApi({ "POST /v1/git/connect": json(400, { error: "bad token" }) });
  await assert.rejects(g.connect(f.api, {}), /bad token/);
});

test("flowStatus, pasteCode and cancelFlow address the flow by its escaped id", async () => {
  let f = fakeApi({
    "GET /v1/git/flows/": json(200, { status: "pending" }),
    "POST /v1/git/flows/": json(200, {}),
    "DELETE /v1/git/flows/": json(204),
  });
  assert.deepEqual(await g.flowStatus(f.api, "a/b"), { status: "pending" });
  await g.pasteCode(f.api, "a/b", "CODE");
  await g.cancelFlow(f.api, "a/b");
  assert.deepEqual(
    f.calls.map((c) => [c.method, c.path, c.body]),
    [
      ["GET", "/v1/git/flows/a%2Fb", undefined],
      ["POST", "/v1/git/flows/a%2Fb/paste", { code: "CODE" }],
      ["DELETE", "/v1/git/flows/a%2Fb", undefined],
    ],
  );

  f = fakeApi({
    "GET /v1/git/flows/": json(404, { error: "gone" }),
    "POST /v1/git/flows/": json(400, { error: "wrong code" }),
    "DELETE /v1/git/flows/": new Error("offline"),
  });
  await assert.rejects(g.flowStatus(f.api, "x"), (e) => e.status === 404);
  await assert.rejects(g.pasteCode(f.api, "x", "c"), /wrong code/);
  // Cancelling is best effort: a network failure is swallowed.
  assert.equal(await g.cancelFlow(f.api, "x"), undefined);
});

test("disconnect accepts 200 and 204 and throws anything else", async () => {
  let f = fakeApi({ "DELETE /v1/git/accounts/": json(200, {}) });
  await g.disconnect(f.api, "gitlab@git.corp.io");
  assert.equal(f.calls[0].path, "/v1/git/accounts/gitlab%40git.corp.io");
  f = fakeApi({ "DELETE /v1/git/accounts/": json(204) });
  await g.disconnect(f.api, "x");
  f = fakeApi({ "DELETE /v1/git/accounts/": json(500, { error: "boom" }) });
  await assert.rejects(g.disconnect(f.api, "x"), /boom/);
});

test("listRepos sends the service, page and trimmed query, and normalises the page", async () => {
  const repos = [{ full_name: "o/a", clone_url: "u/a" }];
  let f = fakeApi({
    "GET /v1/git/repos": json(200, { repos, more: 1, account: "octo" }),
  });
  assert.deepEqual(await g.listRepos(f.api, "github", "  foo ", 2), {
    repos,
    more: true,
    account: "octo",
  });
  assert.equal(f.calls[0].path, "/v1/git/repos?service=github&page=2&q=foo");

  f = fakeApi({ "GET /v1/git/repos": json(200, {}) });
  assert.deepEqual(await g.listRepos(f.api, "gitlab", "   ", 1), {
    repos: [],
    more: false,
    account: undefined,
  });
  assert.equal(f.calls[0].path, "/v1/git/repos?service=gitlab&page=1");

  f = fakeApi({ "GET /v1/git/repos": json(502, { error: "host down" }) });
  await assert.rejects(g.listRepos(f.api, "x", "", 1), /host down/);
});

test("reduceFlow takes direct connected and failed events", () => {
  assert.deepEqual(
    g.reduceFlow(
      { phase: "starting" },
      { type: "connected", account: "a", service: "s" },
    ),
    { phase: "connected", account: "a", service: "s" },
  );
  assert.deepEqual(
    g.reduceFlow({ phase: "starting" }, { type: "failed", error: "e" }),
    { phase: "failed", error: "e" },
  );
  // A connected poll without account or service keeps the flow's service.
  const flow = { flow_id: "f", method: "code", service: "gitlab", url: "u" };
  assert.deepEqual(
    g.reduceFlow(
      { phase: "pending", flow },
      { type: "poll", status: { status: "connected", service: "other" } },
    ),
    { phase: "connected", account: "", service: "other" },
  );
  assert.deepEqual(
    g.reduceFlow(
      { phase: "pending", flow },
      { type: "poll", status: { status: "connected" } },
    ),
    { phase: "connected", account: "", service: "gitlab" },
  );
});

test("gitApi basics: the flow's normal path, an Error's message, repo names and merging", async () => {
  const flow = { flow_id: "f", method: "device", service: "github", url: "u" };
  let s = g.reduceFlow({ phase: "idle" }, { type: "start" });
  assert.deepEqual(s, { phase: "starting" });
  s = g.reduceFlow(s, { type: "started", flow });
  assert.deepEqual(s, { phase: "pending", flow });
  assert.equal(g.reduceFlow(s, { type: "poll", status: { status: "pending" } }), s);
  assert.deepEqual(
    g.reduceFlow(s, { type: "poll", status: { status: "failed" } }),
    { phase: "failed", error: "The host did not authorize the login." },
  );
  assert.deepEqual(g.reduceFlow(s, { type: "reset" }), { phase: "idle" });
  // A late poll answer after the flow ended changes nothing.
  const ended = { phase: "failed", error: "x" };
  assert.equal(
    g.reduceFlow(ended, { type: "poll", status: { status: "connected" } }),
    ended,
  );

  // Never polled when already cancelled.
  let asked = 0;
  assert.equal(
    await g.pollFlow({
      get: async () => {
        asked++;
        return { status: "pending" };
      },
      sleep: async () => {},
      cancelled: () => true,
    }),
    null,
  );
  assert.equal(asked, 0);
  assert.deepEqual(
    await g.pollFlow({
      get: async () => {
        throw new Error("down");
      },
      sleep: async () => {},
      cancelled: () => false,
      maxErrors: 1,
    }),
    { status: "failed", error: "down" },
  );

  assert.equal(g.repoNameFromUrl("git@github.com:user/repo.git"), "repo");
  assert.equal(g.repoNameFromUrl("plain"), "");
  assert.deepEqual(
    g
      .mergeRepos(
        [{ full_name: "o/a", clone_url: "u/a" }],
        [
          { full_name: "o/a", clone_url: "u/a" },
          { full_name: "o/b", clone_url: "u/b" },
        ],
      )
      .map((r) => r.full_name),
    ["o/a", "o/b"],
  );
});

test("pollFlow: cancelled while an answer or an error was on its way gives null", async () => {
  let cancelled = false;
  assert.equal(
    await g.pollFlow({
      get: async () => {
        cancelled = true;
        return { status: "connected" };
      },
      sleep: async () => {},
      cancelled: () => cancelled,
    }),
    null,
  );
  cancelled = false;
  assert.equal(
    await g.pollFlow({
      get: async () => {
        cancelled = true;
        throw new Error("net");
      },
      sleep: async () => {},
      cancelled: () => cancelled,
    }),
    null,
  );
});

test("pollFlow: a 410 is an expired login; a thrown non-Error is reported as text; waits the interval", async () => {
  const gone = await g.pollFlow({
    get: async () => {
      throw new g.GitError("gone", 410);
    },
    sleep: async () => {},
    cancelled: () => false,
  });
  assert.deepEqual(gone, {
    status: "failed",
    error: "The login expired. Try again.",
  });

  const slept = [];
  const r = await g.pollFlow({
    get: async () => {
      throw "plain failure";
    },
    sleep: async (ms) => {
      slept.push(ms);
    },
    cancelled: () => false,
    maxErrors: 2,
  });
  assert.deepEqual(r, { status: "failed", error: "plain failure" });
  assert.deepEqual(slept, [2000]);

  const custom = [];
  let n = 0;
  await g.pollFlow({
    get: async () => (++n < 2 ? { status: "pending" } : { status: "failed" }),
    sleep: async (ms) => {
      custom.push(ms);
    },
    cancelled: () => false,
    intervalMs: 5,
  });
  assert.deepEqual(custom, [5]);
});

test("helpers: trimTrailingSlashes, repoShortName fallbacks, connectedAccounts, primaryMethod", () => {
  assert.equal(g.trimTrailingSlashes("a///"), "a");
  assert.equal(g.trimTrailingSlashes("///"), "");
  assert.equal(g.repoShortName({ full_name: "o/name", clone_url: "nourl" }), "name");
  assert.equal(g.repoShortName({ full_name: "", clone_url: "" }), "");
  assert.deepEqual(
    g.connectedAccounts([
      { id: "a", connected: true },
      { id: "b", connected: false },
    ]).map((a) => a.id),
    ["a"],
  );
  assert.equal(
    g.primaryMethod({ methods: ["token", "code"], oauth_ready: true }),
    "code",
  );
  assert.equal(
    g.primaryMethod({ methods: ["device"], oauth_ready: false }),
    "token",
  );
  assert.equal(g.primaryMethod({ methods: ["token"], oauth_ready: true }), "token");
});

test("relativeTime buckets an age into now, minutes, hours, days, months and years", () => {
  const now = Date.parse("2026-01-01T00:00:00Z");
  const ago = (s) => new Date(now - s * 1000).toISOString();
  assert.equal(g.relativeTime(undefined, now), null);
  assert.equal(g.relativeTime("not a date", now), null);
  assert.deepEqual(g.relativeTime(ago(-50), now), { unit: "now", n: 0 });
  assert.deepEqual(g.relativeTime(ago(120), now), { unit: "minute", n: 2 });
  assert.deepEqual(g.relativeTime(ago(7200), now), { unit: "hour", n: 2 });
  assert.deepEqual(g.relativeTime(ago(86400 * 3), now), { unit: "day", n: 3 });
  assert.deepEqual(g.relativeTime(ago(86400 * 65), now), { unit: "month", n: 2 });
  assert.deepEqual(g.relativeTime(ago(86400 * 800), now), { unit: "year", n: 2 });
  assert.ok(g.relativeTime(new Date().toISOString()));
});

// ── prApi ───────────────────────────────────────────────────────────────────

test("getPR reads the view and carries a refusal's code, service and pull request", async () => {
  let f = fakeApi({
    "GET /v1/sessions/": json(200, { state: "none", branch: "b" }),
  });
  assert.deepEqual(await p.getPR(f.api, "s/1"), { state: "none", branch: "b" });
  assert.equal(f.calls[0].path, "/v1/sessions/s%2F1/pr");

  const pr = { number: 3, url: "u" };
  f = fakeApi({
    "GET /v1/sessions/": json(409, {
      error: "exists",
      code: "pr_exists",
      service: "github",
      pr,
    }),
  });
  await assert.rejects(p.getPR(f.api, "s"), (e) => {
    assert.ok(e instanceof p.PRError);
    assert.equal(e.status, 409);
    assert.equal(e.code, "pr_exists");
    assert.equal(e.service, "github");
    assert.deepEqual(e.pr, pr);
    return true;
  });
  f = fakeApi({ "GET /v1/sessions/": new Response("x", { status: 503 }) });
  await assert.rejects(p.getPR(f.api, "s"), /HTTP 503/);
});

test("retryFix posts to /fix and says whether the agent started", async () => {
  let f = fakeApi({ "POST /v1/sessions/": json(200, { started: true }) });
  assert.deepEqual(await p.retryFix(f.api, "s"), { started: true });
  assert.equal(f.calls[0].path, "/v1/sessions/s/pr/fix");
  assert.deepEqual(f.calls[0].body, {});

  f = fakeApi({ "POST /v1/sessions/": new Response("", { status: 200 }) });
  assert.deepEqual(await p.retryFix(f.api, "s"), { started: false });

  f = fakeApi({ "POST /v1/sessions/": json(500, { error: "busy" }) });
  await assert.rejects(p.retryFix(f.api, "s"), /busy/);
});

test("mergePR posts to /merge and throws a refusal", async () => {
  let f = fakeApi({ "POST /v1/sessions/": json(200, {}) });
  await p.mergePR(f.api, "s");
  assert.equal(f.calls[0].path, "/v1/sessions/s/pr/merge");
  f = fakeApi({ "POST /v1/sessions/": json(409, { error: "red CI" }) });
  await assert.rejects(p.mergePR(f.api, "s"), /red CI/);
});

test("mergeRefused, shouldPoll, ciSummary and the notification rules", () => {
  for (const code of ["blocked", "conflict", "draft"]) {
    assert.equal(p.mergeRefused({ merge: { code } }), true);
  }
  assert.equal(p.mergeRefused({ merge: { code: "ok" } }), false);
  assert.equal(p.mergeRefused(null), false);
  assert.equal(p.mergeRefused(undefined), false);

  for (const status of ["following", "fixing", "reviewing", "resolving"]) {
    assert.equal(p.shouldPoll({ status }), true);
  }
  assert.equal(p.shouldPoll({ status: "passed" }), false);
  assert.equal(p.shouldPoll(null), false);

  assert.deepEqual(
    p.ciSummary({
      ci: {
        state: "pending",
        checks: [
          { name: "a", state: "success" },
          { name: "b", state: "pending" },
        ],
      },
    }),
    { passed: 1, total: 2 },
  );
  assert.deepEqual(p.ciSummary({}), { passed: 0, total: 0 });

  assert.equal(p.shouldDesktopNotify(true, "granted"), true);
  assert.equal(p.shouldDesktopNotify(true, "granted", false), false);
  assert.equal(p.shouldDesktopNotify(false, "granted"), false);
  assert.equal(p.shouldDesktopNotify(true, "denied"), false);
  assert.equal(p.wantsDesktopPermission("default"), true);
  assert.equal(p.wantsDesktopPermission("granted"), false);

  assert.equal(p.worstToast(["success", "error", "warning"]), "error");
  assert.equal(p.worstToast(["success", "warning"]), "warning");
  assert.equal(p.worstToast(["success"]), "success");
});

test("prNotice: new attempts of the agent are news, repeats and first sights are not", () => {
  const w = (status, attempts = 1) => ({ status, attempts, max: 3 });
  assert.equal(p.prNotice(undefined, w("passed")), null);
  assert.equal(p.prNotice(null, null), null);
  for (const s of ["fixing", "reviewing", "resolving"]) {
    assert.equal(p.prNotice(null, w(s)), s);
    assert.equal(p.prNotice(w("following"), w(s)), s);
    assert.equal(p.prNotice(w(s, 1), w(s, 2)), s);
    assert.equal(p.prNotice(w(s, 2), w(s, 2)), null);
  }
  assert.equal(p.prNotice(null, w("following")), null);
  assert.equal(p.prNotice(w("passed"), w("passed")), null);
  assert.equal(p.prNotice(w("following"), w("passed")), "passed");
  assert.equal(p.prNotice(null, w("merged")), "merged");
});

// ── runLink ─────────────────────────────────────────────────────────────────

test("runLink: opening a link cuts the previous one; only the current, on-screen link is live", () => {
  const holder = { current: null };
  const a = rl.openLink(holder, "s1");
  assert.equal(holder.current, a);
  assert.equal(rl.isLive(a, holder, "s1"), true);
  assert.equal(rl.isLive(a, holder, "s2"), false);

  const b = rl.openLink(holder, "s2");
  assert.equal(a.ctrl.signal.aborted, true);
  assert.equal(rl.isLive(a, holder, "s1"), false);
  assert.equal(rl.isLive(b, holder, "s2"), true);

  // A link that was aborted while still current is not live.
  b.ctrl.abort();
  assert.equal(rl.isLive(b, holder, "s2"), false);

  assert.equal(rl.cutLink(holder), "s2");
  assert.equal(holder.current, null);
  assert.equal(rl.cutLink(holder), "");

  const c = rl.openLink(holder, "s3");
  assert.equal(rl.cutLink(holder), "s3");
  assert.equal(c.ctrl.signal.aborted, true);
});
