// Tests for the pull request watch: when the user is told the CI passed or failed, that a failure
// is answered once per push, and that a session nobody opened a pull request from is left alone.
// Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const p = await import("../prApi.ts");
const es = (await import("../i18n.es.ts")).default;

// How many times the gateway tries to fix a red CI before it gives up.
const MAX_ATTEMPTS = 5;
const HTTP_CONFLICT = 409;
// The pull request bar says at least this many different things.
const MIN_BAR_STRINGS = 5;

const w = (status, attempts = 0) => ({
  status,
  attempts,
  max: MAX_ATTEMPTS,
  number: 7,
});

test("nothing seen before is news: a session met for the first time is quiet", () => {
  assert.equal(p.prNotice(undefined, w("passed")), null);
  assert.equal(p.prNotice(undefined, w("fixing", 1)), null);
});

test("the CI passing is told once", () => {
  assert.equal(p.prNotice(w("following"), w("passed")), "passed");
  assert.equal(p.prNotice(w("passed"), w("passed")), null);
});

test("each attempt to fix the CI is told, the same one is not", () => {
  assert.equal(p.prNotice(w("following"), w("fixing", 1)), "fixing");
  assert.equal(p.prNotice(w("fixing", 1), w("fixing", 1)), null);
  assert.equal(p.prNotice(w("following", 1), w("fixing", 2)), "fixing");
  assert.equal(p.prNotice(w("fixing", 1), w("fixing", 2)), "fixing");
});

test("each time the agent is sent to a review or a conflict is told", () => {
  assert.equal(p.prNotice(w("passed"), w("reviewing", 1)), "reviewing");
  assert.equal(p.prNotice(w("reviewing", 1), w("reviewing", 1)), null);
  assert.equal(p.prNotice(w("reviewing", 1), w("reviewing", 2)), "reviewing");
  assert.equal(p.prNotice(w("following"), w("resolving", 1)), "resolving");
  assert.equal(p.shouldPoll(w("reviewing")), true);
  assert.equal(p.shouldPoll(w("resolving")), true);
});

test("giving up, no CI and merged are told", () => {
  assert.equal(
    p.prNotice(w("fixing", MAX_ATTEMPTS), w("gave_up", MAX_ATTEMPTS)),
    "gave_up",
  );
  assert.equal(p.prNotice(w("following"), w("no_ci")), "no_ci");
  assert.equal(p.prNotice(w("passed"), w("merged")), "merged");
  assert.equal(p.prNotice(w("following"), w("base_red")), "base_red");
  assert.equal(p.prNotice(w("passed"), w("closed")), "closed");
  assert.equal(p.prNotice(w("following"), null), null);
  assert.equal(p.prNotice(null, w("following")), null);
  assert.equal(
    p.prNotice(null, w("passed")),
    "passed",
    "a watch that appeared after the session was seen is news",
  );
});

test("the bar asks the gateway only while it is following the CI", () => {
  assert.equal(p.shouldPoll(w("following")), true);
  assert.equal(p.shouldPoll(w("fixing")), true);
  assert.equal(p.shouldPoll(w("passed")), false);
  assert.equal(p.shouldPoll(undefined), false);
});

test("a merge the host would refuse is not offered", () => {
  const v = (code) => ({ state: "open", branch: "b", merge: { code } });
  for (const c of ["blocked", "conflict", "draft"]) {
    assert.equal(p.mergeRefused(v(c)), true, c);
  }
  for (const c of ["ok", "behind", "unknown"]) {
    assert.equal(p.mergeRefused(v(c)), false, c);
  }
  assert.equal(p.mergeRefused(null), false);
  assert.equal(p.mergeRefused({ state: "open", branch: "b" }), false);
});

test("a browser notification is for a hidden tab the user allowed", () => {
  assert.equal(p.shouldDesktopNotify(true, "granted"), true);
  assert.equal(
    p.shouldDesktopNotify(false, "granted"),
    false,
    "the toast is already in front of them",
  );
  assert.equal(p.shouldDesktopNotify(true, "denied"), false);
  assert.equal(p.shouldDesktopNotify(true, "default"), false);
  assert.equal(
    p.shouldDesktopNotify(true, undefined),
    false,
    "no Notification API",
  );
  assert.equal(
    p.shouldDesktopNotify(true, "granted", false),
    false,
    "the user switched them off",
  );
  assert.equal(p.wantsDesktopPermission("default"), true);
  assert.equal(p.wantsDesktopPermission("granted"), false);
  assert.equal(p.wantsDesktopPermission("denied"), false);
});

test("several pull requests moving at once become one toast, as loud as the loudest", () => {
  assert.equal(p.worstToast(["success", "success"]), "success");
  assert.equal(p.worstToast(["success", "warning"]), "warning");
  assert.equal(p.worstToast(["warning", "error", "success"]), "error");
  assert.equal(p.worstToast([]), "success");
});

test("ciSummary counts the jobs that passed", () => {
  const v = {
    state: "open",
    branch: "b",
    ci: {
      state: "pending",
      checks: [
        { name: "a", state: "success" },
        { name: "b", state: "pending" },
      ],
    },
  };
  assert.deepEqual(p.ciSummary(v), { passed: 1, total: 2 });
});

test("retryFix, mergePR and getPR speak to the gateway and carry its refusals", async () => {
  const calls = [];
  const api = async (path, init) => {
    calls.push([path, init?.method]);
    if (path.endsWith("/fix")) {
      return { ok: true, json: async () => ({ started: false, queued: true }) };
    }
    return {
      ok: false,
      status: HTTP_CONFLICT,
      json: async () => ({
        error: "not connected",
        code: "git_auth_required",
        service: "github",
      }),
    };
  };
  assert.deepEqual(await p.retryFix(api, "s 1"), { started: false });
  assert.deepEqual(calls[0], ["/v1/sessions/s%201/pr/fix", "POST"]);
  await p.mergePR(async (path, init) => {
    calls.push([path, init?.method]);
    return { ok: true, json: async () => ({}) };
  }, "s1");
  assert.deepEqual(calls[1], ["/v1/sessions/s1/pr/merge", "POST"]);
  await assert.rejects(
    p.getPR(api, "s1"),
    (e) =>
      e.status === HTTP_CONFLICT &&
      e.code === "git_auth_required" &&
      e.service === "github",
  );
});

test("every string of the pull request bar has a Spanish translation", () => {
  const app = readFileSync(join(here, "..", "App.tsx"), "utf8");
  // The bar is two delimited stretches of App.tsx: the logic and the markup.
  let block = "";
  for (
    let at = app.indexOf("── Pull request bar");
    at >= 0;
    at = app.indexOf("── Pull request bar", at + 1)
  ) {
    block += app.slice(at, app.indexOf("── end of the pull request bar", at));
  }
  assert.ok(block.length > 1000, "the bar is delimited in App.tsx");
  const keys = [
    ...block.matchAll(/\btf?\(\s*(['"])((?:(?!\1)[^\\]|\\.)*)\1/g),
  ].map((m) => m[2].replaceAll(/\\(['"])/g, "$1"));
  assert.ok(keys.length > MIN_BAR_STRINGS);
  for (const k of keys) {
    assert.ok(es[k], `missing Spanish for: ${k}`);
  }
});
