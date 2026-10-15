// The workspace catalog in the Git tab of the settings: it lists each shared clone with the
// projects that use it, and deletes one no project uses, asking first and again when the clone
// holds uncommitted work.
// Run with: node --test web/src/__tests__/cov-repo-catalog.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  loadSources,
  fakeApi,
  json,
  text,
  flush,
} from "./cov-git-helpers.mjs";

installDom();
const { RepoCatalogSettings, sizeText, sortedCatalog } = await loadSources({
  RepoCatalogSettings: "RepoCatalogSettings.tsx",
  sizeText: "RepoCatalogSettings.tsx",
  sortedCatalog: "RepoCatalogSettings.tsx",
});
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const REPOS = [
  {
    name: "api",
    git_url: "https://x.io/u/api.git",
    dir: "/w/repos/api",
    projects: [
      { id: "p1", title: "Shop" },
      { id: "p2", title: "Admin" },
    ],
  },
  {
    name: "old",
    git_url: "https://x.io/u/old.git",
    dir: "/w/repos/old",
    projects: [],
  },
];

async function mount(api) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const notices = [];
  await act(() => {
    render(
      h(RepoCatalogSettings, { api, onNotice: (...a) => notices.push(a) }),
      root,
    );
  });
  await act(flush);
  const done = async () => {
    await act(() => render(null, root));
    root.remove();
  };
  return { root, notices, done };
}

const click = (el) =>
  act(async () => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await flush();
  });
const q = (root, id) => root.querySelector(`[data-testid=${id}]`);

test("it says it is loading, then lists each clone and who uses it", async () => {
  let release;
  const f = fakeApi({
    "GET /v1/repos": () =>
      new Promise((r) => {
        release = () => r(json(200, { repos: REPOS }));
      }),
  });
  const m = await mount(f.api);
  assert.equal(text(m.root.querySelector("output")), "Loading…");
  await act(async () => {
    release();
    await flush();
  });
  const rows = [...m.root.querySelectorAll("[data-testid=catalog-repo]")];
  assert.equal(rows.length, 2);
  assert.match(text(rows[0]), /No project uses it/, "the unused one first");
  assert.match(text(rows[1]), /Used by Shop, Admin/);
  assert.equal(
    rows[1].querySelector("[data-testid=catalog-delete]"),
    null,
    "one in use cannot be deleted",
  );
  assert.match(text(m.root.querySelector("h3")), /2$/, "the title counts them");
  assert.equal(q(m.root, "catalog-unused"), null, "one unused needs no bulk");
  await m.done();
});

test("an empty or unreadable catalog says there is nothing yet", async () => {
  for (const route of [json(200, {}), json(500, {}), new Error("offline")]) {
    const m = await mount(fakeApi({ "GET /v1/repos": route }).api);
    assert.match(text(m.root), /No repositories in your workspace yet/);
    await m.done();
  }
});

test("an answer that arrives after the section is gone is dropped", async () => {
  for (const answer of [json(200, { repos: REPOS }), new Error("late")]) {
    let release;
    const f = fakeApi({
      "GET /v1/repos": () =>
        new Promise((r) => {
          release = () => r(answer);
        }),
    });
    const m = await mount(f.api);
    await m.done();
    await act(async () => {
      release();
      await flush();
    });
    assert.equal(
      m.root.isConnected,
      false,
      "nothing is drawn after the section is gone",
    );
    assert.deepEqual(m.notices, []);
  }
});

test("deleting asks first, can be cancelled, and reads the list again", async () => {
  let listed = REPOS;
  const f = fakeApi({
    "GET /v1/repos": () => json(200, { repos: listed }),
    "DELETE /v1/repos/old": () => {
      listed = REPOS.slice(0, 1);
      return { ok: true, status: 204, json: async () => ({}) };
    },
  });
  const m = await mount(f.api);
  await click(q(m.root, "catalog-delete"));
  assert.match(
    text(q(m.root, "catalog-ask")),
    /local branches leave your workspace/,
  );
  const cancel = [...m.root.querySelectorAll("button")].find(
    (b) => text(b) === "Cancel",
  );
  await click(cancel);
  assert.equal(q(m.root, "catalog-ask"), null);
  await click(q(m.root, "catalog-delete"));
  await click(q(m.root, "catalog-confirm"));
  assert.deepEqual(
    f.calls.filter((c) => c.method === "DELETE").map((c) => c.path),
    ["/v1/repos/old"],
  );
  await act(flush);
  assert.deepEqual(m.notices, [["Deleted old"]]);
  assert.equal(m.root.querySelectorAll("[data-testid=catalog-repo]").length, 1);
  await m.done();
});

test("uncommitted work asks again, and only then is discarded", async () => {
  const f = fakeApi({
    "GET /v1/repos": json(200, { repos: REPOS }),
    "DELETE /v1/repos/old?force=1": json(500, {}),
    "DELETE /v1/repos/old": json(409, {
      error: "the repository has uncommitted changes",
    }),
  });
  const m = await mount(f.api);
  await click(q(m.root, "catalog-delete"));
  await click(q(m.root, "catalog-confirm"));
  assert.equal(
    text(q(m.root, "catalog-ask")),
    "the repository has uncommitted changes",
  );
  assert.equal(
    text(q(m.root, "catalog-confirm")),
    "Discard the changes and delete it",
  );
  await click(q(m.root, "catalog-confirm"));
  assert.equal(
    f.calls.filter((c) => c.method === "DELETE").at(-1).path,
    "/v1/repos/old?force=1",
  );
  assert.deepEqual(m.notices, [["Could not delete the repository", true]]);
  assert.equal(q(m.root, "catalog-ask"), null);
  await m.done();
});

test("a refusal with a reason says it; one whose body cannot be read says what failed", async () => {
  for (const [answer, want] of [
    [
      json(404, { error: "the workspace has no repository with that name" }),
      "the workspace has no repository with that name",
    ],
    [
      {
        ok: false,
        status: 502,
        json: async () => {
          throw new Error("bad");
        },
      },
      "Could not delete the repository",
    ],
    [json(409, {}), "Could not delete the repository"],
  ]) {
    const f = fakeApi({
      "GET /v1/repos": json(200, { repos: REPOS }),
      "DELETE /v1/repos/old": () => answer,
    });
    const m = await mount(f.api);
    await click(q(m.root, "catalog-delete"));
    await click(q(m.root, "catalog-confirm"));
    if (answer.status === 409) {
      assert.equal(text(q(m.root, "catalog-ask")), want);
    } else {
      assert.deepEqual(m.notices, [[want, true]]);
    }
    await m.done();
  }
});

test("sizes read the way a person reads them", () => {
  assert.equal(sizeText(0), "1 KB");
  assert.equal(sizeText(300 * 1024), "300 KB");
  assert.equal(sizeText(5.25 * 1024 * 1024), "5.3 MB");
  assert.equal(sizeText(2 * 1024 * 1024 * 1024), "2.0 GB");
});

test("the unused come first, then each group by name", () => {
  const r = (name, used) => ({ name, projects: used ? [{ id: "p" }] : [] });
  assert.deepEqual(
    sortedCatalog([
      r("b", true),
      r("z", false),
      r("a", true),
      r("c", false),
    ]).map((x) => x.name),
    ["c", "z", "a", "b"],
  );
});

test("a row shows its size, and updating says it is up to date or why not", async () => {
  const f = fakeApi({
    "GET /v1/repos": json(200, {
      repos: [{ ...REPOS[0], size: 3 * 1024 * 1024 }],
    }),
    "POST /v1/repos/api/update": {
      ok: true,
      status: 204,
      json: async () => ({}),
    },
  });
  const m = await mount(f.api);
  assert.match(text(q(m.root, "catalog-repo")), /3\.0 MB/);
  await click(q(m.root, "catalog-update"));
  assert.deepEqual(m.notices, [["api is up to date"]]);
  await m.done();
  for (const [answer, want] of [
    [
      json(409, { error: "not possible to fast-forward" }),
      "not possible to fast-forward",
    ],
    [json(500, {}), "Could not update the repository"],
  ]) {
    const g = fakeApi({
      "GET /v1/repos": json(200, { repos: REPOS }),
      "POST /v1/repos/old/update": answer,
    });
    const n = await mount(g.api);
    await click(q(n.root, "catalog-update"));
    assert.deepEqual(n.notices, [[want, true]]);
    await n.done();
  }
});

test("the unused can go all at once, keeping one with work in it", async () => {
  const many = [
    ...REPOS,
    {
      name: "dirty",
      git_url: "https://x.io/u/dirty.git",
      dir: "/w/repos/dirty",
      projects: [],
    },
  ];
  const f = fakeApi({
    "GET /v1/repos": json(200, { repos: many }),
    "DELETE /v1/repos/old": { ok: true, status: 204, json: async () => ({}) },
    "DELETE /v1/repos/dirty": json(409, { error: "uncommitted" }),
  });
  const m = await mount(f.api);
  assert.equal(text(q(m.root, "catalog-unused-delete")), "Delete the 2 unused");
  await click(q(m.root, "catalog-unused-delete"));
  assert.match(
    text(q(m.root, "catalog-unused")),
    /The 2 repositories no project uses/,
  );
  const cancel = [
    ...q(m.root, "catalog-unused").querySelectorAll("button"),
  ].find((b) => text(b) === "Cancel");
  await click(cancel);
  assert.equal(q(m.root, "catalog-unused-confirm"), null);
  await click(q(m.root, "catalog-unused-delete"));
  await click(q(m.root, "catalog-unused-confirm"));
  assert.deepEqual(
    f.calls
      .filter((c) => c.method === "DELETE")
      .map((c) => c.path)
      .sort(),
    ["/v1/repos/dirty", "/v1/repos/old"],
  );
  assert.deepEqual(m.notices, [["Deleted 1 of 2 unused repositories"]]);
  await m.done();
});
