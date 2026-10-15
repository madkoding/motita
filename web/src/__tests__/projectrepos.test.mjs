// Tests for a project's repositories: reading a URL typed to add one, refusing a name the list
// already has, how the main repository is shown, and that every string of the screens that list
// them has a Spanish translation.
// Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const src = (f) => readFileSync(join(here, "..", f), "utf8");
const r = await import("../projectRepos.ts");
const es = (await import("../i18n.es.ts")).default;

test("a URL typed to add a repository is read into its name", () => {
  assert.deepEqual(r.repoDraftOf(" https://github.com/u/web-client.git "), {
    name: "web-client",
    git_url: "https://github.com/u/web-client.git",
  });
  assert.deepEqual(r.repoDraftOf("git@github.com:u/api.git"), {
    name: "api",
    git_url: "git@github.com:u/api.git",
  });
  assert.deepEqual(r.repoDraftOf("/srv/repos/lib/"), {
    name: "lib",
    git_url: "/srv/repos/lib/",
  });
  assert.equal(r.repoDraftOf("not a url"), null);
  assert.equal(r.repoDraftOf("https://github.com/u/.hidden"), null);
  assert.equal(r.repoDraftOf(""), null);
});

test("a repository cannot join the list twice, nor something that is not one", () => {
  assert.equal(r.addRepoProblem("https://x.io/u/api.git", []), "");
  assert.equal(r.addRepoProblem("https://x.io/u/api.git", ["api"]), "taken");
  assert.equal(r.addRepoProblem("nope", []), "not-a-repo");
});

test("the main repository is shown by its repository name, or its folder", () => {
  assert.deepEqual(
    r.mainRepoOf({
      dir: "/ws/motita",
      git_url: "https://github.com/u/motita.git",
    }),
    { name: "motita", source: "https://github.com/u/motita.git" },
  );
  assert.deepEqual(r.mainRepoOf({ dir: "/ws/notes/" }), {
    name: "notes",
    source: "/ws/notes/",
  });
  assert.deepEqual(r.mainRepoOf({ dir: "" }), { name: "", source: "" });
});

test("the edit dialog lists a main repository only while the folder is one", () => {
  // Cloned from a URL: it is one, and it can be moved.
  assert.deepEqual(
    r.shownMainOf({
      dir: "/ws/a",
      git_url: "https://x.io/u/app.git",
      repos: [{}],
    }),
    { name: "app", source: "https://x.io/u/app.git", movable: true },
  );
  // A plain folder with nothing inside: it is listed, and there is nothing to move.
  assert.deepEqual(r.shownMainOf({ dir: "/ws/notes" }), {
    name: "notes",
    source: "/ws/notes",
    movable: false,
  });
  // A folder that holds repositories and has no URL of its own is not a repository.
  assert.equal(
    r.shownMainOf({ dir: "/ws/suite", repos: [{ name: "api" }] }),
    null,
  );
});

test("a project from before the other repositories has none", () => {
  assert.deepEqual(r.reposOf({}), []);
  const repos = [{ name: "api", git_url: "u", dir: "d" }];
  assert.equal(r.reposOf({ repos }), repos);
});

test("every string of the repository screens has a Spanish translation", () => {
  const keys = new Set();
  for (const f of [
    "ReposField.tsx",
    "NewProjectModal.tsx",
    "EditProjectModal.tsx",
    "useProjects.ts",
  ]) {
    for (const m of src(f).matchAll(
      /\bt[fc]?\(\s*(['"])((?:(?!\1)[^\\]|\\.)*)\1/g,
    )) {
      keys.add(m[2].replaceAll(/\\(['"])/g, "$1"));
    }
  }
  const mine = [...keys].filter((k) =>
    /repositor|Remove \{name\}|Add\b|folder/i.test(k),
  );
  assert.ok(mine.length > 8);
  assert.deepEqual(
    mine.filter((k) => !es[k]),
    [],
  );
});
