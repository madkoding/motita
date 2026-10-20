// The modal that follows a git action chosen in a menu.
// Run with: node --test web/src/__tests__/cov-projects-gitactivity.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { bundle, h, mount, act, click } from "./cov-projects-helpers.mjs";

const {
  AppCtx,
  GitActivityModal,
  beginGitActivity,
  endGitActivity,
  closeGitActivity,
} = await bundle({
  "GitActivityModal.tsx": ["GitActivityModal"],
  "gitActivity.ts": ["beginGitActivity", "endGitActivity", "closeGitActivity"],
});

const q = (id) => document.querySelector(`[data-testid=${id}]`);

test("nothing is shown until an action starts, and ending with none open does nothing", async () => {
  endGitActivity(true, "ignored", "");
  const m = await mount(AppCtx, {}, h(GitActivityModal));
  assert.equal(q("git-activity"), null);
  await m.unmount();
});

test("the modal says what git is doing while it runs, then how it ended", async () => {
  const m = await mount(AppCtx, {}, h(GitActivityModal));
  await act(() =>
    beginGitActivity("Pulling api", "git fetch, then a fast-forward"),
  );
  assert.equal(
    q("git-activity-detail").textContent,
    "git fetch, then a fast-forward",
  );
  assert.match(q("git-activity-state").textContent, /Working/);

  await act(() =>
    endGitActivity(true, "api is up to date", "Brought in:\nabc feat"),
  );
  assert.equal(q("git-activity-state").textContent, "api is up to date");
  assert.equal(q("git-activity-output").textContent, "Brought in:\nabc feat");

  await act(() => beginGitActivity("Pulling api", "d"));
  await act(() => endGitActivity(false, "uncommitted changes", ""));
  assert.equal(q("git-activity-state").textContent, "uncommitted changes");
  assert.equal(q("git-activity-output"), null, "no output, no box");
  await m.unmount();
});

test("the close button and the backdrop dismiss it", async () => {
  const m = await mount(AppCtx, {}, h(GitActivityModal));
  await act(() => beginGitActivity("Stash", "d"));
  await click(document.querySelector("[aria-label=Close]"));
  assert.equal(q("git-activity"), null);
  await act(() => beginGitActivity("Stash", "d"));
  await act(() => closeGitActivity());
  assert.equal(q("git-activity"), null);
  await m.unmount();
});
