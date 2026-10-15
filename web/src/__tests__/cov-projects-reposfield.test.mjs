// The repositories block of the New project and Edit project dialogs: the main repository first,
// the others each with a remove button (and the question the gateway asks when one holds work),
// and the field that adds one more, refusing what is not a repository or a name already taken.
// Run with: node --test web/src/__tests__/cov-projects-reposfield.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  bundle,
  h,
  mount,
  click,
  type,
  key,
  flush,
} from "./cov-projects-helpers.mjs";

const { AppCtx, ReposSection, RepoList, setLang } = await bundle({
  "ReposField.tsx": ["ReposSection", "RepoList", "AddRepo"],
  "i18n.ts": ["setLang"],
});

function section(props) {
  return h(ReposSection, {
    main: { name: "motita", source: "https://x.io/u/motita.git" },
    repos: [],
    busy: "",
    onRemove: () => {},
    onAdd: () => true,
    hint: "the hint",
    ...props,
  });
}

test("nothing is listed when there is no main repository and no other", async () => {
  const m = await mount(
    AppCtx,
    null,
    h(RepoList, { main: null, repos: [], busy: "", onRemove() {} }),
  );
  assert.equal(m.q("[data-testid=repo-list]"), null);
  await m.unmount();
});

test("the main repository is shown first, with its source and the main mark", async () => {
  const m = await mount(AppCtx, null, section({}));
  const list = m.q("[data-testid=repo-list]");
  assert.match(list.textContent, /motita/);
  assert.match(list.textContent, /main/);
  assert.match(list.textContent, /https:\/\/x\.io\/u\/motita\.git/);
  assert.equal(m.qa("[data-testid=repo-row]").length, 0);
  assert.match(m.root.textContent, /the hint/);
  await m.unmount();
});

test("a main repository with no source shows only its name", async () => {
  const m = await mount(
    AppCtx,
    null,
    section({ main: { name: "fresh", source: "" } }),
  );
  const list = m.q("[data-testid=repo-list]");
  assert.equal(list.querySelectorAll("[title]").length, 0);
  assert.match(list.textContent, /fresh/);
  await m.unmount();
});

test("the other repositories show their branch, changes and a remove button", async () => {
  const removed = [];
  const m = await mount(
    AppCtx,
    null,
    section({
      main: null,
      repos: [
        { name: "api", git_url: "u/api", branch: "dev", changes: 3 },
        { name: "lib", git_url: "u/lib" },
      ],
      onRemove: (n, f) => removed.push([n, f]),
    }),
  );
  const rows = m.qa("[data-testid=repo-row]");
  assert.equal(rows.length, 2);
  assert.match(rows[0].textContent, /api/);
  assert.match(rows[0].textContent, /dev · 3 changes/);
  assert.doesNotMatch(rows[1].textContent, /·|changes/);
  const btn = rows[1].querySelector("[data-testid=repo-remove]");
  assert.equal(btn.getAttribute("aria-label"), "Remove lib");
  await click(btn);
  assert.deepEqual(removed, [["lib", false]]);
  await m.unmount();
});

test("the repository being removed shows a spinner and cannot be removed twice", async () => {
  const m = await mount(
    AppCtx,
    null,
    section({ repos: [{ name: "api", git_url: "u/api" }], busy: "api" }),
  );
  const btn = m.q("[data-testid=repo-remove]");
  assert.equal(btn.disabled, true);
  assert.ok(btn.querySelector(".animate-spin"));
  await m.unmount();
});

test("a removal refused for uncommitted work asks before discarding it", async () => {
  const removed = [];
  const m = await mount(
    AppCtx,
    null,
    section({
      repos: [
        { name: "api", git_url: "u/api" },
        { name: "lib", git_url: "u/lib" },
      ],
      conflict: { name: "lib", message: "lib has 2 changes" },
      onRemove: (n, f) => removed.push([n, f]),
    }),
  );
  const conflicts = m.qa("[data-testid=repo-conflict]");
  assert.equal(conflicts.length, 1);
  assert.match(conflicts[0].textContent, /lib has 2 changes/);
  await click(conflicts[0].querySelector("button"));
  assert.deepEqual(removed, [["lib", true]]);
  await m.unmount();
});

test("adding a repository: refused when not a URL or taken, emptied once added", async () => {
  const added = [];
  let answer = false;
  const m = await mount(
    AppCtx,
    null,
    section({
      repos: [{ name: "api", git_url: "u/api" }],
      onAdd: async (d) => {
        added.push(d);
        return answer;
      },
    }),
  );
  const input = m.q("[data-testid=repo-add-url]");
  const add = m.q("[data-testid=repo-add]");
  assert.equal(add.disabled, true, "nothing typed, nothing to add");
  assert.equal(add.textContent, "Add");

  await type(input, "nope");
  await click(add);
  assert.equal(
    m.q("[data-testid=repo-add-problem]").textContent,
    "That is not a repository URL.",
  );
  assert.equal(added.length, 0);

  await type(input, "https://x.io/u/api.git");
  assert.equal(m.q("[data-testid=repo-add-problem]"), null, "typing clears it");
  await key(input, "Enter");
  assert.equal(
    m.q("[data-testid=repo-add-problem]").textContent,
    "The project already has a repository with that name.",
  );

  // Another key does nothing.
  await type(input, "https://x.io/u/web.git");
  await key(input, "a");
  assert.equal(added.length, 0);

  // Refused by the caller: the field keeps the URL.
  await key(input, "Enter");
  await flush();
  assert.deepEqual(added, [{ name: "web", git_url: "https://x.io/u/web.git" }]);
  assert.equal(input.value, "https://x.io/u/web.git");

  // A trailing slash is not part of the name.
  await type(input, "https://x.io/u/web/");
  await key(input, "Enter");
  await flush();
  assert.deepEqual(added[1], { name: "web", git_url: "https://x.io/u/web/" });

  answer = true;
  await click(add);
  await flush();
  assert.equal(added.length, 3);
  assert.equal(input.value, "");
  await m.unmount();
});

test("while one is being added the field and its button are locked", async () => {
  const m = await mount(AppCtx, null, section({ busy: "+" }));
  assert.equal(m.q("[data-testid=repo-add-url]").disabled, true);
  const add = m.q("[data-testid=repo-add]");
  assert.equal(add.disabled, true);
  assert.ok(add.querySelector(".animate-spin"));
  await m.unmount();
});

test("in Spanish the block speaks Spanish", async () => {
  await setLang("es");
  try {
    const m = await mount(AppCtx, null, section({}));
    assert.match(m.root.textContent, /Repositorios/);
    assert.equal(m.q("[data-testid=repo-add]").textContent, "Agregar");
    await m.unmount();
  } finally {
    await setLang("en");
  }
});

test("a main repository that can move asks first, then moves", async () => {
  const moved = [];
  const m = await mount(
    AppCtx,
    null,
    section({
      main: {
        name: "motita",
        source: "https://x.io/u/motita.git",
        movable: true,
      },
      onMoveMain: () => moved.push("moved"),
    }),
  );
  assert.equal(m.q("[data-testid=repo-main-ask]"), null);
  await click(m.q("[data-testid=repo-main-remove]"));
  assert.match(
    m.q("[data-testid=repo-main-ask]").textContent,
    /motita moves into its own folder/,
  );
  assert.equal(m.q("[data-testid=repo-main-remove]").disabled, true);
  // Changing their mind moves nothing.
  await click(
    [...m.root.querySelectorAll("button")].find(
      (b) => b.textContent === "Cancel",
    ),
  );
  assert.equal(m.q("[data-testid=repo-main-ask]"), null);
  assert.deepEqual(moved, []);
  await click(m.q("[data-testid=repo-main-remove]"));
  await click(m.q("[data-testid=repo-main-move]"));
  assert.deepEqual(moved, ["moved"]);
  assert.equal(m.q("[data-testid=repo-main-ask]"), null);
  await m.unmount();
});

test("the main repository has no button when it cannot move, and spins while it does", async () => {
  const fixed = await mount(
    AppCtx,
    null,
    section({
      main: { name: "notes", source: "/ws/notes", movable: false },
      onMoveMain: () => {},
    }),
  );
  assert.equal(fixed.q("[data-testid=repo-main-remove]"), null);
  await fixed.unmount();

  const noHandler = await mount(
    AppCtx,
    null,
    section({ main: { name: "a", source: "u", movable: true } }),
  );
  assert.equal(noHandler.q("[data-testid=repo-main-remove]"), null);
  await noHandler.unmount();

  const busy = await mount(
    AppCtx,
    null,
    section({
      busy: "*",
      main: { name: "a", source: "u", movable: true },
      onMoveMain: () => {},
    }),
  );
  assert.equal(busy.q("[data-testid=repo-main-remove]").disabled, true);
  assert.ok(busy.q("[data-testid=repo-main-remove] svg"));
  await busy.unmount();
});

test("the repositories the workspace already has are offered, except the ones the list has", async () => {
  const added = [];
  const catalog = [
    {
      name: "api",
      git_url: "https://X.io/u/API.git/",
      dir: "/w/repos/api",
      projects: [],
    },
    {
      name: "lib-2",
      git_url: "https://x.io/o/lib.git",
      dir: "/w/repos/lib-2",
      projects: [],
    },
    { name: "odd", git_url: "host:", dir: "/w/repos/odd", projects: [] },
    {
      name: "web",
      git_url: "https://x.io/u/web.git",
      dir: "/w/repos/web",
      projects: [],
    },
  ];
  const props = {
    main: null,
    repos: [
      { name: "api", git_url: "https://x.io/u/api" },
      { name: "lib", git_url: "https://x.io/other/lib" },
    ],
    catalog,
    onAdd: (d) => {
      added.push(d);
      return true;
    },
  };
  const m = await mount(AppCtx, null, section(props));
  const picks = m.qa("[data-testid=repo-catalog-pick]");
  assert.deepEqual(
    picks.map((b) => b.textContent),
    ["+ odd", "+ web"],
    "the same repository, or one whose name is taken, is not offered",
  );
  await click(picks[1]);
  assert.deepEqual(added, [{ name: "web", git_url: "https://x.io/u/web.git" }]);
  await m.rerender(null, section({ ...props, busy: "+" }));
  assert.equal(m.q("[data-testid=repo-catalog-pick]").disabled, true);
  await m.rerender(null, section({ ...props, catalog: catalog.slice(0, 2) }));
  assert.equal(
    m.q("[data-testid=repo-catalog]"),
    null,
    "nothing left to offer",
  );
  await m.unmount();
});

test("a repository other projects share says which ones", async () => {
  const catalog = [
    {
      name: "api",
      git_url: "u/api",
      dir: "/w/repos/api",
      projects: [
        { id: "p1", title: "Shop" },
        { id: "p2", title: "Admin" },
        { id: "p3", title: "Docs" },
      ],
    },
  ];
  const repos = [
    { name: "api", git_url: "u/api", dir: "/w/repos/api" },
    { name: "own", git_url: "u/own", dir: "/w/shop/own" },
  ];
  const m = await mount(
    AppCtx,
    null,
    h(RepoList, {
      main: null,
      repos,
      busy: "",
      catalog,
      projectId: "p1",
      onRemove() {},
    }),
  );
  const also = m.qa("[data-testid=repo-also-in]");
  assert.equal(also.length, 1);
  assert.equal(also[0].textContent, "Also in Admin, Docs");
  await m.unmount();
});

test("a shared repository behind its main branch says by how much", async () => {
  const m = await mount(
    AppCtx,
    null,
    section({
      main: null,
      repos: [
        {
          name: "api",
          git_url: "u/api",
          branch: "project/shop",
          behind: 3,
          main_branch: "develop",
        },
        { name: "web", git_url: "u/web", behind: 1 },
      ],
    }),
  );
  const rows = m.qa("[data-testid=repo-row]");
  assert.match(rows[0].textContent, /project\/shop · 3 behind develop/);
  assert.match(rows[1].textContent, /1 behind main/);
  await m.unmount();
});
