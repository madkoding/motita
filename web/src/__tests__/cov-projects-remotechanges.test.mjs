// News on origin: which changes are told (once per state), the toast's wording, and the page
// re-reading the projects while signed in.
// Run with: node --test web/src/__tests__/cov-projects-remotechanges.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, load, settle } from "./cov-settings-helpers.mjs";

installDom("http://localhost/");

const { useRemoteChanges, freshChanges, remoteMessage } = await load(
  "useRemoteChanges.ts",
);
const { h, render } = await import("preact");

const own = (id, commit, behind = 1) => ({
  id,
  title: "Motita " + id,
  remote_changes: [{ branch: "main", behind, commit }],
});

test("a change is told once, and again only when origin moves or after catching up", () => {
  const seen = new Map();
  const first = freshChanges([own("a", "c1", 2)], seen);
  assert.deepEqual(first, [{ name: "Motita a", count: 2 }]);
  assert.deepEqual(freshChanges([own("a", "c1", 2)], seen), []);
  assert.equal(freshChanges([own("a", "c2", 3)], seen)[0].count, 3);
  assert.deepEqual(freshChanges([{ id: "a", title: "Motita a" }], seen), []);
  assert.equal(seen.size, 0, "a repository that caught up is forgotten");
  assert.equal(freshChanges([own("a", "c2", 3)], seen).length, 1);
});

test("another repository of the project is named", () => {
  const p = {
    id: "p",
    title: "Shop",
    remote_changes: [{ repo: "api", branch: "main", behind: 1, commit: "x" }],
  };
  assert.equal(freshChanges([p], new Map())[0].name, "Shop / api");
});

test("the toast says what arrived and what to do", () => {
  assert.match(
    remoteMessage([{ name: "Shop", count: 2 }]),
    /Shop has 2 new commit\(s\) on origin.*Update main branch/,
  );
  assert.match(
    remoteMessage([
      { name: "a", count: 1 },
      { name: "b", count: 1 },
    ]),
    /^2 repositories have new changes on origin/,
  );
});

function mount(lists, authOk = true) {
  const toasts = [];
  const setToast = (x) => toasts.push(x);
  function Harness(props) {
    useRemoteChanges(props.lists, setToast, props.authOk);
    return null;
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(Harness, { lists, authOk }), root);
  return {
    toasts,
    rerender: (l, a = authOk) =>
      render(h(Harness, { lists: l, authOk: a }), root),
    async unmount() {
      render(null, root);
      await settle();
    },
  };
}

test("the page re-reads the projects only while signed in, and toasts each new state once", async (t) => {
  t.mock.timers.enable({ apis: ["setInterval"] });
  let reads = 0;
  const fetchProjects = async () => {
    reads++;
  };
  const off = mount({ projects: [], fetchProjects }, false);
  await settle();
  t.mock.timers.tick(10 * 60 * 1000);
  assert.equal(reads, 0);
  await off.unmount();

  const m = mount({ projects: [], fetchProjects });
  await settle();
  t.mock.timers.tick(300_000);
  assert.equal(reads, 1);
  assert.equal(m.toasts.length, 0);
  m.rerender({ projects: [own("a", "c1")], fetchProjects });
  await settle();
  assert.equal(m.toasts.length, 1);
  assert.equal(m.toasts[0].type, "success");
  m.rerender({ projects: [own("a", "c1")], fetchProjects });
  await settle();
  assert.equal(m.toasts.length, 1, "the same state is not told again");
  await m.unmount();
});
