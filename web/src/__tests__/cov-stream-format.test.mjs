// The pure helpers the page words things with: errorMessage, choose, the i18n lookups and the
// formatters and parsers of appFormat.ts and appParse.ts, in English and in Spanish.
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { installDom, source } from "./cov-stream-helpers.mjs";

installDom("http://localhost/chat?x=1#top");

const m = {
  ...(await source("appFormat.ts")),
  ...(await source("appParse.ts")),
  ...(await source("errors.ts")),
  ...(await source("choose.ts")),
};
const i18n = await source("i18n.ts");

test("errorMessage shows an Error's message and anything else as text", () => {
  assert.equal(m.errorMessage(new Error("boom")), "boom");
  assert.equal(m.errorMessage("plain"), "plain");
  assert.equal(m.errorMessage(42), "42");
});

test("choose picks by the truthiness of its condition", () => {
  assert.equal(m.choose(1, "a", "b"), "a");
  assert.equal(m.choose("", "a", "b"), "b");
});

test("token counts are added up and humanised", () => {
  assert.equal(m.tokenTotal(undefined), 0);
  assert.equal(m.tokenTotal({}), 0);
  assert.equal(
    m.tokenTotal({ input: 1, output: 2, cache_read: 3, cache_write: 4 }),
    10,
  );
  assert.equal(m.formatTokens(950), "950");
  assert.equal(m.formatTokens(1000), "1k");
  assert.equal(m.formatTokens(1234), "1.2k");
  assert.equal(m.formatTokens(12345), "12k");
  assert.equal(m.formatTokens(1_000_000), "1M");
  assert.equal(m.formatTokens(1_234_567), "1.2M");
  assert.equal(m.tokenBreakdown({}), "in 0 · out 0");
  assert.equal(
    m.tokenBreakdown({
      input: 1500,
      output: 20,
      cache_read: 7,
      cache_write: 9,
    }),
    "in 1.5k · out 20 · cache read 7 · cache write 9",
  );
  assert.deepEqual(
    m.sumTokens([
      { tokens: { input: 1, output: 2, cache_read: 3, cache_write: 4 } },
      { tokens: {} },
      {},
    ]),
    { input: 1, output: 2, cache_read: 3, cache_write: 4 },
  );
});

test("elapsed times read as a chronometer", () => {
  assert.equal(m.formatElapsed(-5), "0s");
  assert.equal(m.formatElapsed(8000), "8s");
  assert.equal(m.formatElapsed(185_000), "3m 05s");
  assert.equal(m.formatElapsed(3_720_000), "1h 02m");
  const start = "2024-01-01T00:00:00Z";
  const t0 = Date.parse(start);
  assert.equal(m.agentElapsed({ started: "bad", elapsed_ms: 7 }, 0), 7);
  assert.equal(m.agentElapsed({ started: "bad" }, 0), 0);
  assert.equal(
    m.agentElapsed(
      { started: start, state: "running", elapsed_ms: 1 },
      t0 + 5000,
    ),
    5000,
  );
  assert.equal(
    m.agentElapsed({ started: start, state: "running" }, t0 - 10),
    0,
  );
  assert.equal(
    m.agentElapsed({ started: start, state: "passed", elapsed_ms: 3 }, 0),
    3,
  );
  assert.equal(m.agentElapsed({ started: start, state: "passed" }, 0), 0);
  assert.equal(
    m.agentElapsed(
      { started: start, state: "passed", finished: "2024-01-01T00:00:02Z" },
      0,
    ),
    2000,
  );
});

test("agents are sorted main first, then by start, and marked by how they ended", () => {
  assert.deepEqual(m.sortAgents("nope"), []);
  const list = [
    { id: "b", parent: "a", started: "2024-01-01T00:00:02Z" },
    null,
    { id: 3 },
    { id: "c", parent: "a", started: "2024-01-01T00:00:01Z" },
    { id: "a", started: "x" },
    { id: "d", parent: "a", started: "x" },
  ];
  assert.deepEqual(
    m.sortAgents(list).map((a) => a.id),
    ["a", "d", "c", "b"],
  );
  // An agent whose start cannot be read counts as started first.
  const ok = { id: "ok", parent: "a", started: "2024-01-01T00:00:01Z" };
  const odd = { id: "odd", parent: "a", started: "?" };
  assert.deepEqual(
    m.sortAgents([ok, odd]).map((a) => a.id),
    ["odd", "ok"],
  );
  assert.deepEqual(
    m.sortAgents([odd, ok]).map((a) => a.id),
    ["odd", "ok"],
  );
  assert.equal(m.agentMark("running"), null);
  assert.equal(m.agentMark("passed"), "✓");
  assert.equal(m.agentMark("failed"), "✗");
  assert.equal(m.agentMark("cancelled"), "⊘");
});

test("ids, branches and worktrees are shortened the way git does", () => {
  mock.timers.enable({ apis: ["Date"], now: 1_000_000_000_000 });
  try {
    assert.equal(
      m.daysUntil(new Date(Date.now() + 1.5 * 86_400_000).toISOString()),
      2,
    );
  } finally {
    mock.timers.reset();
  }
  assert.equal(m.shortID("abcdefghij"), "abcdefg");
  assert.equal(m.shortID("abc"), "abc");
});

test("timestamps are written as dd/mm/yyyy :: HH:mm:ss and in full for the tooltip", () => {
  const iso = new Date(2024, 0, 2, 3, 4, 5).toISOString();
  assert.equal(m.formatUpdated(iso), "02/01/2024 :: 03:04:05");
  assert.equal(m.formatUpdated(""), "");
  assert.equal(m.formatUpdated("garbage"), "");
  assert.equal(m.fullUpdated(iso), new Date(iso).toLocaleString());
  assert.equal(m.fullUpdated(undefined), "");
  assert.equal(m.fullUpdated("garbage"), "");
  assert.equal(m.changesTitle(1), "1 uncommitted change in this working tree");
  assert.equal(m.changesTitle(3), "3 uncommitted changes in this working tree");
});

test("a session's and a project's tooltips list the facts that are there", () => {
  const iso = new Date(2024, 0, 2, 3, 4, 5).toISOString();
  assert.equal(m.sessionTooltip({ id: "s" }), undefined);
  assert.equal(
    m.sessionTooltip({
      id: "s",
      last_used: iso,
      agents_running: 2,
      changes: 1,
    }),
    [
      `Last used ${new Date(iso).toLocaleString()}`,
      "2 background agents running",
      "1 uncommitted change in this working tree",
    ].join("\n"),
  );
  assert.equal(
    m.sessionTooltip({ id: "s", agents_running: 1 }),
    "1 background agent running",
  );
  assert.equal(
    m.sessionTooltip({ id: "s", awaiting_approval: true }),
    "Waiting for your approval",
  );
  assert.equal(m.projectTooltip({}), undefined);
  assert.equal(
    m.projectTooltip({ branch: "main", changes: 2 }),
    [
      "Project checkout is on branch: main",
      "The project checkout has 2 uncommitted changes in this working tree",
    ].join("\n"),
  );
});

test("the open session is kept in the address", () => {
  assert.equal(m.sessionFromUrl(), "");
  m.writeSessionUrl("s1", "push");
  assert.equal(window.location.search, "?x=1&session=s1");
  assert.equal(window.location.hash, "#top");
  assert.equal(m.sessionFromUrl(), "s1");
  const before = history.length;
  m.writeSessionUrl("s1", "push");
  assert.equal(history.length, before, "the same session adds no entry");
  m.writeSessionUrl("s2", "replace");
  assert.equal(history.length, before, "replace adds no entry");
  assert.equal(m.sessionFromUrl(), "s2");
  m.clearSessionUrl();
  assert.equal(window.location.search, "?x=1");
  m.clearSessionUrl();
  assert.equal(window.location.search, "?x=1");
  // Without a usable window, the helpers say nothing and change nothing.
  const saved = globalThis.window;
  globalThis.window = undefined;
  try {
    assert.equal(m.sessionFromUrl(), "");
    m.writeSessionUrl("s3", "push");
    m.clearSessionUrl();
  } finally {
    globalThis.window = saved;
  }
  assert.equal(window.location.search, "?x=1");
});

test("mostRecent is the session used last", () => {
  assert.equal(m.mostRecent([]), undefined);
  const a = { id: "a", last_used: "2024-01-01T00:00:00Z" };
  const b = { id: "b", last_used: "2024-02-01T00:00:00Z" };
  const c = { id: "c" };
  assert.equal(m.mostRecent([a, b, c]).id, "b");
  assert.equal(m.mostRecent([c, a]).id, "a");
});

test("scheduled tasks count down against the gateway's clock", () => {
  mock.timers.enable({ apis: ["Date"], now: 1_700_000_000_000 });
  try {
    const now = Date.now();
    assert.equal(m.clockSkewOf([]), 0);
    // The gateway is a minute ahead: the next run is a minute later than one cadence away.
    const ahead = {
      next_run: new Date(now + 3_600_000 + 60_000).toISOString(),
      every: "1h0m0s",
    };
    const bad = { next_run: "never", every: "1h" };
    assert.equal(m.clockSkewOf([bad, ahead]), -60_000);
    const behind = {
      next_run: new Date(now + 3_600_000 - 5000).toISOString(),
      every: "1h",
    };
    assert.equal(m.clockSkewOf([behind]), 0);

    const at = (ms, extra = {}) => ({
      enabled: true,
      next_run: new Date(now + ms).toISOString(),
      ...extra,
    });
    assert.equal(m.taskCountdown(at(1000, { enabled: false }), now, 0), null);
    assert.equal(
      m.taskCountdown({ enabled: true, next_run: "x" }, now, 0),
      null,
    );
    assert.deepEqual(m.taskCountdown(at(65_000), now, 0), {
      text: "1:05",
      overdue: false,
    });
    assert.deepEqual(m.taskCountdown(at(615_000), now, 0), {
      text: "10:15",
      overdue: false,
    });
    assert.deepEqual(m.taskCountdown(at(-5000), now, 0), {
      text: "0:05",
      overdue: true,
    });
    assert.deepEqual(m.taskCountdown(at(3_725_000), now, 0), {
      text: "1:02:05",
      overdue: false,
    });
    assert.deepEqual(
      m.taskCountdown(at(2 * 86_400_000 + 3 * 3_600_000), now, 0),
      {
        text: "2d 3h",
        overdue: false,
      },
    );
    assert.deepEqual(m.taskCountdown(at(65_000), now, 60_000), {
      text: "0:05",
      overdue: false,
    });
  } finally {
    mock.timers.reset();
  }
});

test("small lookups: reasoning, titles, models, statuses, skills", () => {
  assert.equal(m.reasoningOf(null), "medium");
  assert.equal(m.reasoningOf({ reasoning_enabled: true }), "medium");
  assert.equal(
    m.reasoningOf({ reasoning_enabled: true, reasoning: "high" }),
    "high",
  );
  assert.equal(
    m.reasoningOf({ reasoning_enabled: false, reasoning: "high" }),
    "off",
  );

  assert.equal(m.sessionTitle({ id: "s1" }), "s1");
  assert.equal(
    m.sessionTitle({ id: "s1", title: "New session" }),
    "New session",
  );
  assert.equal(m.sessionTitle({ id: "s1", title: "Fix it" }), "Fix it");
  assert.equal(m.chatTitle(null), "Motita");
  assert.equal(m.chatTitle({ id: "s1", title: "T" }), "T");

  const all = [
    { id: "p1", models: ["a", "b"] },
    { id: "p2", models: ["c"] },
    { id: "p3" },
  ];
  assert.deepEqual(m.modelOptions("p1", "z", "p1", ["b", "x", ""], all), [
    "b",
    "x",
    "a",
    "z",
  ]);
  assert.deepEqual(m.modelOptions("p2", "c", "p1", ["b"], all), ["c"]);
  assert.deepEqual(m.modelOptions("p9", "", "p1", [], all), []);
  assert.equal(m.firstModelOf("p2", all), "c");
  assert.equal(m.firstModelOf("p3", all), "");
  assert.equal(m.firstModelOf("p9", all), "");

  assert.equal(m.statusLabel("added"), "new");
  assert.equal(m.statusLabel("modified"), "edited");
  assert.equal(m.statusLabel("deleted"), "removed");
  assert.equal(m.statusLabel("renamed"), "moved");
  assert.equal(m.statusLabel("copied"), "copied");

  const item = { name: "a", on: false };
  assert.equal(m.patchNamed(item, "b", { on: true }), item);
  assert.deepEqual(m.patchNamed(item, "a", { on: true }), {
    name: "a",
    on: true,
  });

  const skills = [
    { name: "deploy", title: "Ship", summary: "push it" },
    { name: "x", title: "Review", summary: "read code" },
    { name: "y", title: "Z", summary: "Write TESTS" },
  ];
  assert.equal(m.filterSkills(skills, ""), skills);
  assert.deepEqual(
    m.filterSkills(skills, "deploy").map((s) => s.name),
    ["deploy"],
  );
  assert.deepEqual(
    m.filterSkills(skills, "review").map((s) => s.name),
    ["x"],
  );
  assert.deepEqual(
    m.filterSkills(skills, "tests").map((s) => s.name),
    ["y"],
  );
  assert.deepEqual(m.filterSkills(skills, "nothing"), []);

  assert.equal(m.lastExit([]), null);
  assert.equal(m.lastExit([{ exit: 1 }, { exit: 2 }]), 2);
  assert.deepEqual(m.branchChoices(["a", "b"], "b"), ["a", "b"]);
  assert.deepEqual(m.branchChoices(["a"], "c"), ["c", "a"]);
});

test("small lookups: previews, pull requests, toasts, artifacts, tags, notes", () => {
  assert.equal(m.previewKind(true, null), "loading");
  assert.equal(
    m.previewKind(false, { count: 0, inspection_failed: true }),
    "failed",
  );
  assert.equal(m.previewKind(false, null), "none");
  assert.equal(m.previewKind(false, { count: 2 }), "changes");
  assert.equal(m.previewKind(false, { count: 0 }), "clean");

  assert.equal(m.mergedOffer(undefined, undefined), "");
  assert.equal(m.mergedOffer({ status: "open" }, {}), "");
  assert.equal(m.mergedOffer({ status: "merged", next: "s2" }, {}), "open");
  assert.equal(
    m.mergedOffer({ status: "merged" }, { merged: true, continuable: true }),
    "continue",
  );
  assert.equal(m.mergedOffer({ status: "merged" }, { merged: true }), "");
  assert.equal(m.mergedOffer({ status: "merged" }, undefined), "");

  assert.equal(m.toastIcon("error"), "error");
  assert.equal(m.toastIcon("warning"), "warning");
  assert.equal(m.toastIcon("info", true, { update_available: true }), "update");
  assert.equal(m.toastIcon("info", false, { update_available: true }), "done");
  assert.equal(m.toastIcon("info"), "done");

  assert.equal(m.artifactShape({ type: "application/pdf", url: "/a" }), "pdf");
  assert.equal(m.artifactShape({ type: "image/png", url: "/a" }), "image");
  assert.equal(
    m.artifactShape({ type: "text/markdown; charset=utf-8" }),
    "markdown",
  );
  assert.equal(m.artifactShape({ type: "text/html" }), "html");
  assert.equal(m.artifactShape({ type: "text/plain" }), "text");

  assert.equal(m.tagGroupClass("mode"), "bg-accent/20 text-accent");
  assert.equal(m.tagGroupClass("action"), "bg-[#f0a040]/20 text-[#f0a040]");
  assert.equal(m.tagGroupClass("session"), "bg-[#40f0a0]/20 text-[#40f0a0]");
  assert.equal(m.tagGroupClass("other"), "bg-[#a0a0f0]/20 text-[#a0a0f0]");

  assert.match(m.deleteWarning("session"), /^This conversation/);
  assert.match(m.deleteWarning("skill"), /^This skill/);
  assert.match(m.deleteWarning("project"), /^This project/);

  assert.equal(m.toastTone("error", 1, 2, 3), 1);
  assert.equal(m.toastTone("warning", 1, 2, 3), 2);
  assert.equal(m.toastTone("info", 1, 2, 3), 3);

  assert.equal(m.rowMenuStyle(null), undefined);
  assert.deepEqual(m.rowMenuStyle({ top: 4, left: 9 }), {
    top: "4px",
    left: "9px",
    maxHeight: "calc(100vh - 4px - 8px)",
  });

  assert.equal(m.skillHeading(null), "Skill library");
  assert.equal(m.skillHeading({ name: "n", title: "T" }), "T");
  assert.equal(m.skillHeading({ name: "n", title: "" }), "n");

  assert.equal(m.prWatchKeyOf(undefined), "");
  assert.equal(m.prWatchKeyOf({ status: "fixing", attempts: 2 }), "fixing:2");
  assert.equal(
    m.ciFailedLabel({ status: "gave_up", attempts: 3 }),
    "CI failed — Motita gave up after 3 attempts",
  );
  assert.equal(
    m.ciFailedLabel({ status: "fixing", attempts: 1, max: 3 }),
    "CI failed — Motita is fixing it (1/3)",
  );
  assert.equal(m.ciFailedLabel(undefined), "CI failed");

  assert.equal(
    m.mergeBlockedLabel("blocked"),
    "Waiting for approvals or required checks",
  );
  assert.equal(
    m.mergeBlockedLabel("conflict"),
    "Conflicts with the base branch",
  );
  assert.equal(m.mergeBlockedLabel("draft"), "Still a draft");
  assert.equal(
    m.mergeBlockedLabel("behind"),
    "Out of date with the base branch",
  );
});

test("durations are read in milliseconds and in their short form", () => {
  assert.deepEqual(m.durationOf("24h0m0s"), { ms: 86_400_000, short: "24h" });
  assert.deepEqual(m.durationOf("1h30m"), { ms: 5_400_000, short: "1h30m" });
  assert.deepEqual(m.durationOf("1.5s"), { ms: 1500, short: "1.5s" });
  assert.deepEqual(m.durationOf("250ms"), { ms: 250, short: "250ms" });
  assert.deepEqual(m.durationOf("2000us"), { ms: 2, short: "2000us" });
  assert.deepEqual(m.durationOf("3000µs"), { ms: 3, short: "3000µs" });
  assert.deepEqual(m.durationOf("1000000ns"), { ms: 1, short: "1000000ns" });
  assert.deepEqual(m.durationOf("weekly"), { ms: 0, short: "weekly" });
  // A number without a unit is skipped, and "1." is not a fraction.
  assert.deepEqual(m.durationOf("5 then 2m"), { ms: 120_000, short: "2m" });
  assert.deepEqual(m.durationOf("1.m"), { ms: 0, short: "1.m" });
});

test("modes, switched messages and progress kinds are recognised", () => {
  assert.equal(m.modeOf("mode: plan (because)"), "plan");
  assert.equal(m.modeOf("mode: task (x)"), "task");
  assert.equal(m.modeOf("hello"), null);
  assert.equal(m.switchedText("/plan do it", "plan"), "/task do it");
  assert.equal(m.switchedText("do it", "task"), "/plan do it");
  assert.equal(m.switchedText("/T", "task"), "/plan ");

  const kinds = {
    "thinking: hmm": "thought",
    "output (exit 0):": "output",
    "approved (all commands allowed)": "check",
    "running: ls": "command",
    "action: x": "reasoning",
    "mode: plan (x)": "analysis",
    "validating with anchor...": "check",
    "planning...": "plan",
    "subtask 1/2: a": "plan",
    "analysing the task...": "analysis",
    "deciding action": "reasoning",
    "deciding the next step after: ls": "reasoning",
    "starting with: read the test": "reasoning",
    "round 2 done; more": "reasoning",
    "synthesizing answer...": "synthesis",
    "something else": null,
  };
  for (const [line, kind] of Object.entries(kinds)) {
    assert.equal(m.classifyProgress(line), kind, line);
  }
});

test("repository names, slugs and shell lines are read", () => {
  const names = {
    "git@github.com:user/repo.git": "repo",
    "https://github.com/user/repo.git": "repo",
    "https://github.com/user/repo/": "repo",
    "  https://github.com/user/repo  ": "repo",
    "https://github.com/user/.git": ".git",
    "git@host:repo": "",
    "host:a/repo": "repo",
    repo: "",
    "https://github.com/": "",
    "/repo": "",
    "x:/repo": "",
    "a:b/repo": "repo",
  };
  for (const [url, name] of Object.entries(names)) {
    assert.equal(m.repoNameOf(url), name, url);
  }
  assert.equal(m.slugOf("  My Project!.v2 "), "my-project-v2");
  assert.equal(m.slugOf("--a--"), "a");
  assert.equal(m.slugOf("---"), "");
  assert.equal(m.shellCommandOf("!ls -la "), "ls -la");
  assert.equal(m.shellCommandOf("/shell   pwd"), "pwd");
  assert.equal(m.shellCommandOf("hello"), null);
  assert.deepEqual(m.splitOutput("output (exit 2):\nboom"), {
    exit: 2,
    body: "boom",
  });
  assert.deepEqual(m.splitOutput("output (exit -1):"), { exit: -1, body: "" });
  assert.deepEqual(m.splitOutput("other"), { exit: null, body: "other" });
  assert.equal(m.commandOf("running:   ls"), "ls");
  assert.equal(m.oneLine("a\n  b"), "a b");
  assert.equal(m.oneLine("abcdef", 4), "abc…");
});

test("the agent's phase lines and upgrade stages are worded", () => {
  assert.equal(m.progressText("plan ready: 3 steps"), "plan ready: 3 steps");
  assert.equal(
    m.progressText("subtask 1/2: write it"),
    "subtask 1/2: write it",
  );
  assert.equal(
    m.progressText("analysing the task..."),
    "analysing the task...",
  );
  assert.equal(m.progressText("free text"), "free text");
  for (const line of [
    "deciding the first step...",
    "deciding the next step...",
    "deciding the next step after: go test",
    "starting with: read the test",
  ]) {
    assert.equal(m.progressText(line), line);
    assert.ok(m.isPhaseLine(line), line);
  }
  assert.equal(
    m.progressText("waiting for 3 background agent(s)..."),
    "waiting for 3 background agent(s)...",
  );
  assert.equal(m.upgradeMessage("Downloaded 40%"), "Downloaded 40%");
  assert.equal(m.upgradeMessage("Downloading v2"), "Downloading v2");
  assert.equal(m.upgradeMessage("Installed v2"), "Installed v2");
  assert.equal(m.upgradeMessage("Restarting"), "Restarting");
  assert.equal(m.upgradeMessage(), "");
});

test("server-sent event frames are read", () => {
  assert.equal(m.sseData("event: x\ndata: 1\ndata: 2"), "2");
  assert.equal(m.sseData("event: x"), "");
  assert.deepEqual(
    m.parseFrame("id: 7 \nevent: progress\ndata: {}\n: comment"),
    {
      id: "7",
      event: "progress",
      data: "{}",
    },
  );
  assert.deepEqual(m.parseFrame(""), { id: null, event: null, data: null });
});

test("the browser's language decides what auto means", () => {
  const setNav = (v) =>
    Object.defineProperty(globalThis, "navigator", {
      value: v,
      configurable: true,
      writable: true,
    });
  const saved = globalThis.navigator;
  try {
    setNav({ languages: ["es-CL", "en"] });
    assert.equal(i18n.browserLang(), "es");
    setNav({ languages: ["", "en-US"] });
    assert.equal(i18n.browserLang(), "en");
    setNav({ languages: [], language: "es" });
    assert.equal(i18n.browserLang(), "es");
    setNav({ language: "" });
    assert.equal(i18n.browserLang(), "en");
    setNav({
      get languages() {
        throw new Error("no navigator");
      },
    });
    assert.equal(i18n.browserLang(), "en");
    setNav({ languages: ["es_ES"] });
    assert.equal(i18n.resolveLang("auto"), "es");
    assert.equal(i18n.resolveLang(null), "es");
    assert.equal(i18n.resolveLang("en"), "en");
    assert.equal(i18n.resolveLang("es"), "es");
  } finally {
    setNav(saved);
  }
});

test("Spanish is loaded on demand and English stays the fallback", async () => {
  assert.equal(i18n.lang(), "en");
  assert.equal(i18n.t("New session"), "New session");
  assert.equal(i18n.tc("skill", "off"), "off");
  let heard = 0;
  // A component subscribed with useLang re-renders when the language changes.
  const { h, render } = await import("preact");
  const { act } = await import("preact/test-utils");
  const root = document.createElement("div");
  function Probe() {
    const l = i18n.useLang();
    heard++;
    return h("span", null, l + ":" + i18n.t("New session"));
  }
  await act(() => render(h(Probe), root));
  assert.equal(root.textContent, "en:New session");

  await act(() => i18n.setLang("es"));
  assert.equal(i18n.lang(), "es");
  assert.equal(document.documentElement.lang, "es");
  assert.equal(root.textContent, "es:Sesión nueva");
  assert.ok(heard >= 2);
  assert.equal(i18n.t("New session"), "Sesión nueva");
  assert.equal(i18n.t("not in the dictionary"), "not in the dictionary");
  assert.equal(i18n.tc("skill", "off"), "desactivada");
  assert.equal(i18n.tc("nowhere", "off"), "apagado");
  assert.equal(i18n.tf("in {n}", { n: 3 }), "entrada 3");
  assert.equal(i18n.tf("{a} and {b}", { a: 1 }), "1 and {b}");
  assert.equal(i18n.plural(1, "{n} file", "{n} files"), "1 file");
  assert.equal(i18n.plural(2, "{n} file", "{n} files", { x: 1 }), "2 files");
  assert.equal(
    m.sessionTitle({ id: "s", title: "New session" }),
    "Sesión nueva",
  );

  // The dictionary is loaded once; switching back and forth keeps it.
  await act(() => i18n.setLang("en"));
  assert.equal(root.textContent, "en:New session");
  await act(() => i18n.setLang("es"));
  assert.equal(root.textContent, "es:Sesión nueva");

  // Without a document the language still changes.
  const savedDoc = globalThis.document;
  globalThis.document = undefined;
  try {
    await i18n.setLang("en");
  } finally {
    globalThis.document = savedDoc;
  }
  assert.equal(i18n.lang(), "en");
  await act(() => render(null, root));
});

test("a background agent's step is read from its line, and named by its tag", () => {
  const cmd = m.parseSub(
    '{"agent":"a2","role":"review","kind":"command","text":"ls","exit":1}',
  );
  assert.equal(cmd.text, "ls");
  assert.equal(m.subTag(cmd), "a2 · review");
  assert.equal(m.subTag({ agent: "a1" }), "a1");
  assert.equal(m.parseSub("{not json"), null);
  assert.equal(m.parseSub("null"), null);
  assert.equal(
    m.parseSub('{"kind":"command"}'),
    null,
    "a step without its agent is nothing",
  );
});

test("workerError puts the few messages a worker can send in the reader's language", () => {
  for (const said of [
    "this browser has no WebGPU in its workers",
    "the browser gave no graphics card to use",
    "the local listener stopped",
  ]) {
    assert.notEqual(
      m.workerError(said),
      said,
      said + " is worded for the reader",
    );
  }
  assert.equal(
    m.workerError("HTTP 404 https://x"),
    "HTTP 404 https://x",
    "anything else as it came",
  );
});

test("the memory lines are classed, worded and read back", async () => {
  assert.equal(m.classifyProgress("saved to memory: A (a.md)"), "memory");
  assert.equal(
    m.classifyProgress("[recalled 2 passages from memory]"),
    "memory",
  );
  assert.equal(
    m.progressText("saved to memory: A (a.md)"),
    "saved to memory: A (a.md)",
  );
  assert.equal(
    m.progressText("[recalled 2 passages from memory]"),
    "[recalled 2 passages from memory]",
  );
  assert.equal(
    m.progressText("[recalled 3 passages from earlier]"),
    "[recalled 3 passages from earlier]",
  );
  const es = (await import("../i18n.es.ts")).default;
  for (const k of [
    "saved to memory: {x}",
    "[recalled {n} passages from memory]",
    "[recalled {n} passages from earlier]",
    "Used the memory ({n})",
    "Saved to memory: {name}",
  ]) {
    assert.ok(es[k], "missing Spanish for: " + k);
  }
  assert.deepEqual(
    m.memoryUse([
      { text: "saved to memory: A b (x/A b.md)" },
      { text: "running: ls" },
      { text: "[recalled 4 passages from memory]" },
    ]),
    { saved: [{ title: "A b", path: "x/A b.md" }], recalled: 4 },
  );
});
