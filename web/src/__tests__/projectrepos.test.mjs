// Tests for the folder name a repository address gives.
// Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";

const { repoFolderOf } = await import("../projectRepos.ts");

test("the folder of a repository is the last element of its address", () => {
  assert.equal(
    repoFolderOf(" https://github.com/u/web-client.git "),
    "web-client",
  );
  assert.equal(repoFolderOf("git@github.com:u/api.git"), "api");
  assert.equal(repoFolderOf("https://github.com/u/lib//"), "lib");
  assert.equal(repoFolderOf("/srv/git/lib.git"), "lib");
  assert.equal(repoFolderOf("C:\\git\\tool"), "tool");
  assert.equal(repoFolderOf(""), "");
});
