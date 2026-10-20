// The git panel of a repository inside a project's folder.
// Run with: node --test web/src/__tests__/cov-projects-repogitpanel.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { bundle, h, mount, click } from "./cov-projects-helpers.mjs";

const { AppCtx, RepoGitPanel } = await bundle({
  "RepoGitPanel.tsx": ["RepoGitPanel"],
});

const REF = { projectId: "p1", repo: "api" };
const STATUS = {
  path: "api",
  branch: "work",
  main_branch: "main",
  changes: [
    { path: "a.txt", kind: "modified" },
    { path: "b.txt", kind: "untracked" },
  ],
  log: [{ sha: "abc1234", subject: "feat: thing" }],
  ahead: 2,
  behind: 1,
  unpublished: 3,
  merged: ["old"],
};

function store(status) {
  const log = [];
  return {
    log,
    value: {
      repoGit: {
        status,
        closeRepoGit: () => log.push(["close"]),
        act: (...a) => log.push(["act", ...a]),
      },
    },
  };
}

const type = async (el, value) => {
  el.value = value;
  el.dispatchEvent(new window.Event("input", { bubbles: true }));
  await new Promise((r) => setTimeout(r, 0));
};

test("the panel waits for the repository before it shows anything", async () => {
  const s = store(null);
  const m = await mount(AppCtx, s.value, h(RepoGitPanel, { r: REF }));
  assert.match(m.q("dialog").textContent, /Reading…/);
  assert.equal(m.q("[data-testid=repo-changes]"), null);
  await click(m.q("button[aria-label=Close]"));
  assert.deepEqual(s.log, [["close"]]);
  await m.unmount();
});

test("the panel shows the branch, its distance, the changes and the history", async () => {
  const s = store(STATUS);
  const m = await mount(AppCtx, s.value, h(RepoGitPanel, { r: REF }));
  const text = m.q("dialog").textContent;
  assert.match(text, /work/);
  assert.equal(
    m.q("[data-testid=repo-distance]").textContent,
    "2 ahead, 1 behind main · 3 not published",
  );
  assert.match(
    m.q("[data-testid=repo-changes]").textContent,
    /modified.*a\.txt/s,
  );
  assert.match(
    m.q("[data-testid=repo-log]").textContent,
    /abc1234.*feat: thing/s,
  );
  assert.equal(
    m.q("[data-testid=panel-clean]").textContent,
    "Delete merged branches (1)",
  );
  await m.unmount();
});

test("a long list of changes is cut, and a repository with none says so", async () => {
  const many = {
    ...STATUS,
    main_branch: undefined,
    changes: Array.from({ length: 35 }, (_, i) => ({
      path: `f${i}`,
      kind: "added",
    })),
  };
  const s = store(many);
  const m = await mount(AppCtx, s.value, h(RepoGitPanel, { r: REF }));
  assert.equal(m.qa("[data-testid=repo-changes] li").length, 30);
  assert.match(m.q("dialog").textContent, /and 5 more/);
  assert.match(m.q("[data-testid=repo-distance]").textContent, /behind main/);
  await m.rerender(
    store({ ...STATUS, changes: [], merged: [] }).value,
    h(RepoGitPanel, { r: REF }),
  );
  assert.match(m.q("dialog").textContent, /Nothing changed/);
  assert.equal(m.q("[data-testid=panel-stash]").disabled, true);
  assert.equal(m.q("[data-testid=panel-clean]").disabled, true);
  assert.equal(m.q("[data-testid=repo-discard]").disabled, true);
  await m.unmount();
});

test("the tools create a branch, put changes aside and delete merged branches", async () => {
  const s = store(STATUS);
  const m = await mount(AppCtx, s.value, h(RepoGitPanel, { r: REF }));
  assert.equal(m.q("[data-testid=repo-branch-create]").disabled, true);
  await type(m.q("[data-testid=repo-branch-name]"), "  feat/x ");
  await click(m.q("[data-testid=repo-branch-create]"));
  await click(m.q("[data-testid=panel-stash]"));
  await click(m.q("[data-testid=panel-clean]"));
  assert.deepEqual(s.log, [
    ["act", REF, "branch", { branch: "feat/x" }],
    ["act", REF, "stash"],
    ["act", REF, "clean"],
  ]);
  await m.unmount();
});

test("discarding asks a second time, and can be taken back", async () => {
  const s = store(STATUS);
  const m = await mount(AppCtx, s.value, h(RepoGitPanel, { r: REF }));
  assert.equal(m.q("[data-testid=repo-discard-sure]"), null);
  await click(m.q("[data-testid=repo-discard]"));
  assert.match(
    m.q("[data-testid=repo-discard-sure]").textContent,
    /cannot be undone/,
  );
  assert.equal(
    m.q("[data-testid=repo-discard]"),
    null,
    "the first button gives way",
  );
  await click(m.q("[data-testid=repo-discard-sure] button:last-child"));
  assert.equal(m.q("[data-testid=repo-discard-sure]"), null);
  assert.deepEqual(s.log, []);
  await click(m.q("[data-testid=repo-discard]"));
  await click(m.q("[data-testid=repo-discard-yes]"));
  assert.deepEqual(s.log, [["act", REF, "discard", { confirm: true }]]);
  assert.equal(m.q("[data-testid=repo-discard-sure]"), null);
  await m.unmount();
});
