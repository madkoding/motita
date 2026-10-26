package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A session of a plain folder holding two repositories gets a worktree of each, on its branch, and
// deleting it removes both and its folder. Uncommitted work in one of them still refuses the deletion.
func TestASessionOfAPlainFolderGetsAWorktreeOfEachRepository(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "proj")
	repos := []string{"api", "group/web"}
	for _, rel := range repos {
		dir := filepath.Join(projectDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitRepo(t, dir)
	}
	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: workspace,
		ProjectDir:   filepath.Join(workspace, "projects"),
		SessionDir:   filepath.Join(workspace, "sessions"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})
	rec := post(t, srv, "/v1/projects", `{"title":"proj","dir":"proj"}`, testToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating the project answered %d: %s", rec.Code, rec.Body.String())
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &proj); err != nil {
		t.Fatal(err)
	}
	created := post(t, srv, "/v1/sessions", `{"project_id":"`+proj.ID+`","isolate":true}`, testToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("creating the session answered %d: %s", created.Code, created.Body.String())
	}
	var conv SessionStatus
	if err := json.Unmarshal(created.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(workspace, "worktrees", conv.ID)
	for _, rel := range repos {
		repo := filepath.Join(projectDir, filepath.FromSlash(rel))
		dest := filepath.Join(wt, filepath.FromSlash(rel))
		if !worktreeRegistered(t, repo, dest) {
			t.Fatalf("%s must have a worktree at %s", rel, dest)
		}
		if got := gitOut(t, dest, "branch", "--show-current"); got != sessionBranch(conv.ID) {
			t.Errorf("%s worktree is on %q, want the session's branch", rel, got)
		}
	}

	dirty := filepath.Join(wt, "api", "wip.txt")
	if err := os.WriteFile(dirty, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := doDelete(t, srv, "/v1/sessions/"+conv.ID); rec.Code != http.StatusConflict {
		t.Fatalf("uncommitted work must refuse the deletion, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}

	if rec := doDelete(t, srv, "/v1/sessions/"+conv.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("deleting a clean session answered %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the session's folder must be gone: %v", err)
	}
	for _, rel := range repos {
		repo := filepath.Join(projectDir, filepath.FromSlash(rel))
		if worktreeRegistered(t, repo, filepath.Join(wt, filepath.FromSlash(rel))) {
			t.Errorf("%s still registers the session's worktree", rel)
		}
	}
}

// A session of a plain folder links what the folder holds outside its repositories, reports its
// folder and its work, and is integrated repository by repository: the run is told about each one
// with work, and the session is merged once one of them moved.
func TestASessionOfAPlainFolderLinksLooseFilesAndIntegratesEachRepository(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	fs := withProjectsAndFake(t, srv, ws)
	pid, dir := plainProject(t, srv, ws, "services")
	for _, rel := range []string{"api", "group/web"} {
		d := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		gitInit(t, d)
	}
	for _, rel := range []string{"README.md", "group/notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`","isolate":true}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"README.md", "group/notes.txt"} {
		if got, err := os.Readlink(filepath.Join(ss.Workspace, filepath.FromSlash(rel))); err != nil ||
			got != filepath.Join(dir, filepath.FromSlash(rel)) {
			t.Errorf("%s must link to the project's file, got %q %v", rel, got, err)
		}
	}
	if ss.Worktree == "" || ss.Mergeable {
		t.Errorf("a fresh session has its own folder and nothing to integrate: %+v", ss)
	}
	if rec := post(t, srv, sessionPath(srv, ss.ID, "/merge"), "{}", testToken); rec.Code != http.StatusConflict {
		t.Errorf("integrating no work must be refused, got %d", rec.Code)
	}

	web := filepath.Join(ss.Workspace, "group", "web")
	if err := os.WriteFile(filepath.Join(web, "new.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, srv, ss.ID); !got.Mergeable || got.Changes != 1 {
		t.Errorf("work in a nested repository makes the session integrable: %+v", got)
	}

	var asked string
	fs.task = func(_ context.Context, task string, _ func(string, ...any)) (string, error) {
		asked = task
		mustRun(t, "git", "-C", web, "add", "new.txt")
		mustRun(t, "git", "-C", web, "commit", "-qm", "work")
		mustRun(t, "git", "-C", filepath.Join(dir, "group", "web"), "merge", "--no-ff", "-qm", "integrate",
			sessionBranch(ss.ID))
		return "merged", nil
	}
	st := doMerge(t, srv, ss, nil)
	if !strings.Contains(asked, web) || strings.Contains(asked, filepath.Join(ss.Workspace, "api")) {
		t.Errorf("the task must name the repository with work and only it:\n%s", asked)
	}
	if st.MergedSHA != gitOut(t, filepath.Join(dir, "group", "web"), "rev-parse", "HEAD") {
		t.Errorf("merged sha = %q, want the new HEAD of the repository that moved", st.MergedSHA)
	}
}

func TestMovedRev(t *testing.T) {
	for _, c := range []struct{ start, now, want string }{
		{"a", "a", ""}, {"a", "b", "b"}, {"a", "", ""}, {"a,b", "a,c", "c"}, {"a", "a,b", "b"}, {"a,b", "a", "a"},
	} {
		if got := movedRev(c.start, c.now); got != c.want {
			t.Errorf("movedRev(%q, %q) = %q, want %q", c.start, c.now, got, c.want)
		}
	}
}

// Filling a session's folder again leaves a repository already checked out there as it is, and a
// repository that cannot be branched (no commits yet) is skipped while the others still get theirs.
func TestNestedSessionWorktreeReusesCheckoutsAndSkipsAnEmptyRepository(t *testing.T) {
	ctx := context.Background()
	projectDir := filepath.Join(t.TempDir(), "proj")
	api, empty := filepath.Join(projectDir, "api"), filepath.Join(projectDir, "empty")
	for _, d := range []string{api, empty} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitRepo(t, api)
	mustRun(t, "git", "init", "-q", "-b", "main", empty)
	path := filepath.Join(t.TempDir(), "session")
	for range 2 {
		got, err := nestedSessionWorktree(ctx, projectDir, path, "motita/s1")
		if err != nil || got != path {
			t.Fatalf("nestedSessionWorktree = %q, %v; want %q", got, err, path)
		}
	}
	if !worktreeRegistered(t, api, filepath.Join(path, "api")) {
		t.Error("api must have a worktree in the session's folder")
	}
	if _, err := os.Stat(filepath.Join(path, "empty")); !os.IsNotExist(err) {
		t.Errorf("the empty repository must be skipped: %v", err)
	}
}

// The agent of a plain folder that holds repositories is told not to make the folder one.
func TestNestedReposNoteForbidsGitInitOnAContainerFolder(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if got := nestedReposNote(ctx, dir); got != "" {
		t.Fatalf("a folder with no repository needs no note, got %q", got)
	}
	if got := nestedReposNote(ctx, ""); got != "" {
		t.Fatalf("no folder needs no note, got %q", got)
	}
	for _, rel := range []string{"api", "group/web"} {
		sub := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		gitRepo(t, sub)
	}
	note := nestedReposNote(ctx, dir)
	for _, want := range []string{"NOT a git repository", "Never run `git init`", "api", "group/web"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note must mention %q, got %q", want, note)
		}
	}
	gitRepo(t, dir)
	if got := nestedReposNote(ctx, dir); got != "" {
		t.Fatalf("a folder that is a repository itself needs no note, got %q", got)
	}
}
