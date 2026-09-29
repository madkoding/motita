package gateway

// A fresh session checkout has to be able to RUN, not merely to exist.
//
// `git worktree add` copies nothing git ignores. For a project whose dependencies live in
// an ignored directory - `node_modules/` is the common one - the session gets a checkout
// where `npm run lint`, `tsc` and `vitest` do not exist, so the project's own gate answers
// "failed checks: npm lint, npm typecheck, npm test".
//
// That message reads as broken CODE and is really a missing TOOLCHAIN. The user's report
// was a question, not a bug report - "no entiendo por que siguen fallando las
// validaciones, demasiado deterministico?" - and the answer measured here is that the
// gate was running in an empty tree: the three checks PASS in the same checkout once the
// dependencies are reachable (lint 28.8s, typecheck 6.5s, test 10.3s on the real project).
//
// The fix links the dependency directories the project already has. Measured for scale:
// one project's node_modules is 860 MB, so a copy per session would multiply that by the
// number of sessions; a link shares the bytes, and npm reads them without writing.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestAFreshSessionCanRunTheProjectsGate: the fix. The dependencies the project holds are
// reachable from the session's checkout, so the gate runs before anything is installed.
func TestAFreshSessionCanRunTheProjectsGate(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)

	// The project's dependencies, in the directory git ignores.
	deps := filepath.Join(projectDir, "node_modules")
	if err := os.MkdirAll(filepath.Join(deps, ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps, ".bin", "vitest"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A NEW session, created after the dependencies exist.
	created := post(t, srv, "/v1/sessions", `{"project_id":"`+projectIDOf(t, srv)+`"}`, testToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", created.Code, created.Body.String())
	}
	var conv SessionStatus
	if err := json.Unmarshal(created.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(srv.opts.WorkspaceDir, "worktrees", conv.ID)

	// The gate's own tool has to be reachable from the session's tree. This is exactly
	// what was missing, and what made three checks fail at once.
	if _, err := os.Stat(filepath.Join(fresh, "node_modules", ".bin", "vitest")); err != nil {
		t.Fatalf("a fresh session must be able to run the project's gate: %v", err)
	}
	_ = wt
}

// TestTheSharedDependenciesAreALinkNotACopy: a link is what makes this affordable. Copying
// 860 MB per session would be the disk multiplied by the session count.
func TestTheSharedDependenciesAreALinkNotACopy(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	deps := filepath.Join(projectDir, "node_modules")
	if err := os.MkdirAll(deps, 0o755); err != nil {
		t.Fatal(err)
	}

	created := post(t, srv, "/v1/sessions", `{"project_id":"`+projectIDOf(t, srv)+`"}`, testToken)
	var conv SessionStatus
	if err := json.Unmarshal(created.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(srv.opts.WorkspaceDir, "worktrees", conv.ID, "node_modules")

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("the link must exist: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the shared dependencies must be a SYMLINK: a copy per session multiplies the disk")
	}
	if target, err := os.Readlink(link); err != nil || target != deps {
		t.Errorf("the link must point at the project's own dependencies, got %q (%v)", target, err)
	}
}

// TestAProjectWithNothingToShareIsUnaffected: a project that has never installed anything
// gets no link, and that is not an error - the session installs its own, which is what
// happened before this existed.
func TestAProjectWithNothingToShareIsUnaffected(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)

	if _, err := os.Lstat(filepath.Join(wt, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("nothing to share means nothing to link, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "README.md")); err != nil {
		t.Fatalf("the project must be untouched: %v", err)
	}
}

// TestAnExistingDirectoryIsNotReplaced: a session that installed its OWN dependencies must
// keep them. Replacing the directory with a link would hand its work to another tree.
func TestAnExistingDirectoryIsNotReplaced(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)

	// The session already has its own, and the project has its own as well.
	if err := os.MkdirAll(filepath.Join(projectDir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(wt, "node_modules")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(own, "installed-here.txt")
	if err := os.WriteFile(marker, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Calling it directly is what a second sessionWorktree call does.
	linkSharedDirs(projectDir, wt)

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the session's own dependencies must survive: %v", err)
	}
	if info, err := os.Lstat(own); err == nil && info.Mode()&os.ModeSymlink != 0 {
		t.Error("an existing directory must never be replaced by a link")
	}
}

// TestOnlyDependencyDirectoriesAreShared: the list is deliberately short. An ignored
// directory can hold anything, and linking one the toolchain does not read would hand a
// session a directory that is not its own.
func TestOnlyDependencyDirectoriesAreShared(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)

	// Something ignored that is NOT a dependency tree.
	private := filepath.Join(projectDir, ".env.local")
	if err := os.WriteFile(private, []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(projectDir, "uploads")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}

	linkSharedDirs(projectDir, wt)

	for _, name := range []string{".env.local", "uploads"} {
		if _, err := os.Lstat(filepath.Join(wt, name)); !os.IsNotExist(err) {
			t.Errorf("%s must NOT be shared into a session: it is not a dependency tree", name)
		}
	}
}

// projectIDOf returns the id of the only project the test server holds.
func projectIDOf(t *testing.T, srv *Server) string {
	t.Helper()
	rec := send(t, srv, http.MethodGet, "/v1/projects", testToken, "")
	var out struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Projects) == 0 {
		t.Fatal("no project")
	}
	return out.Projects[0].ID
}

// TestNestedDependencyDirectoriesAreShared: a monorepo keeps its dependencies next to each
// package. Only the root used to be linked, so `web/node_modules` - this repository's own
// layout - was missing from every session, and so was the web gate's toolchain.
func TestNestedDependencyDirectoriesAreShared(t *testing.T) {
	project, wt := t.TempDir(), t.TempDir()
	for _, dir := range []string{"web", "packages/api", "a/b/c/d/e", ".git/hooks"} {
		if err := os.MkdirAll(filepath.Join(wt, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, dep := range []string{"web/node_modules", "packages/api/.venv", "a/b/c/d/e/node_modules",
		"node_modules/inner/node_modules", ".git/hooks/node_modules"} {
		if err := os.MkdirAll(filepath.Join(project, dep), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linkSharedDirs(project, wt)

	for _, want := range []string{"node_modules", "web/node_modules", "packages/api/.venv"} {
		if info, err := os.Lstat(filepath.Join(wt, want)); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s must be linked into the checkout: %v", want, err)
		}
	}
	// Past the depth bound, inside .git, and inside a linked dependency: never.
	for _, not := range []string{"a/b/c/d/e/node_modules", ".git/hooks/node_modules"} {
		if _, err := os.Lstat(filepath.Join(wt, not)); err == nil {
			t.Errorf("%s must not be linked", not)
		}
	}
}

// TestTheDependencyWalkIsBounded: a huge checkout stops being walked at the visit bound, and a
// checkout that cannot be read is left alone.
func TestTheDependencyWalkIsBounded(t *testing.T) {
	project, wt := t.TempDir(), t.TempDir()
	for _, dir := range []string{"one", "two"} {
		os.MkdirAll(filepath.Join(wt, dir), 0o755)
		os.MkdirAll(filepath.Join(project, dir, "node_modules"), 0o755)
	}
	old := sharedDirsMaxVisits
	sharedDirsMaxVisits = 2
	defer func() { sharedDirsMaxVisits = old }()
	linkSharedDirs(project, wt)
	if _, err := os.Lstat(filepath.Join(wt, "one", "node_modules")); err != nil {
		t.Errorf("the first directory is within the bound: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wt, "two", "node_modules")); err == nil {
		t.Error("the walk must stop at the bound")
	}
	// A checkout that does not exist: nothing to walk, and no panic.
	linkSharedDirs(project, filepath.Join(wt, "missing"))
}
