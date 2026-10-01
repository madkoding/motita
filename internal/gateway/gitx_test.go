package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gitx"
)

// mergeIntegrationTask returns a fake RunTask implementation that carries out
// the git work the agent would do. Tests use it because the transport test suite
// runs a fake service, but integration is now a task: without this helper the run
// would finish with no result and the project's HEAD would never move.
func mergeIntegrationTask(t *testing.T, srv *Server, ss SessionStatus, p *Project) func(context.Context, string, func(string, ...any)) (string, error) {
	return func(_ context.Context, task string, _ func(string, ...any)) (string, error) {
		if !strings.Contains(task, "Integrate this session's work") {
			return "", nil
		}
		branch := sessionBranch(ss.ID)
		base := gitOut(t, p.Dir, "branch", "--show-current")

		// Read the conversation's CURRENT workspace: a test may move the session onto the
		// project's own checkout to verify that such a session is not integrated.
		c, _ := srv.lookup(ss.ID)
		wt := ""
		if c != nil {
			wt = c.workspace
		}
		if wt == "" {
			wt = ss.Workspace
		}

		// A session that has no own worktree is working on the user's branch. The agent must not
		// commit there, so the integration fails cleanly.
		if wt == "" || gitx.SamePath(wt, p.Dir) {
			return "", fmt.Errorf("the session has no dedicated worktree, so its work cannot be isolated from the project branch")
		}

		// Commit pending work on the session branch, skipping tool caches the same way the agent's
		// change report does. The subject mirrors what the gateway used to commit with.
		if out := gitOut(t, wt, "status", "-z"); out != "" {
			// -z separates entries with NUL and uses no quoting, so paths are exact and there is no
			// ambiguity with renames. Each entry is two status letters followed by the path.
			for _, entry := range strings.Split(out, "\x00") {
				entry = strings.TrimSpace(entry)
				if len(entry) < 3 {
					continue
				}
				path := strings.TrimSpace(entry[2:])
				if gitx.IsToolHome(path) {
					continue
				}
				mustRun(t, "git", "-C", wt, "add", "--", path)
			}
			if out := gitOut(t, wt, "status", "--short"); out != "" {
				mustRun(t, "git", "-C", wt, "commit", "-qm", "session work")
			}
		}

		// Merge into the project's base branch.
		if err := exec.Command("git", "-C", p.Dir, "merge", "--no-ff", "-m", "motita: integrate session "+ss.ID, branch).Run(); err != nil {
			_ = exec.Command("git", "-C", p.Dir, "merge", "--abort").Run()
			return "", fmt.Errorf("merge failed: %w", err)
		}
		sha := gitOut(t, p.Dir, "rev-parse", "--short", "HEAD")
		return fmt.Sprintf("Integrated session %s into %s as commit %s", ss.ID, base, sha), nil
	}
}

// waitForMerged polls the session until it reports merged or a timeout passes.
func waitForMerged(t *testing.T, srv *Server, id string) SessionStatus {
	t.Helper()
	for i := 0; i < 50; i++ {
		st := statusOf(t, srv, id)
		if st.Merged {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session %s was not marked merged in time", id)
	return SessionStatus{}
}

// doMerge starts an integration run and waits for it to finish.
func doMerge(t *testing.T, srv *Server, ss SessionStatus, p *Project) SessionStatus {
	t.Helper()
	events := collect(t, srv, http.MethodPost, sessionPath(srv, ss.ID, "/merge"), "{}")
	var done *event
	for i := range events {
		if events[i].Event == "done" || events[i].Event == "error" {
			done = &events[i]
			break
		}
	}
	if done == nil {
		t.Fatalf("merge run produced no terminal event: %+v", events)
	}
	if done.Event == "error" {
		t.Fatalf("merge run failed: %s", done.Data)
	}
	return waitForMerged(t, srv, ss.ID)
}

// These tests exercise the gateway's integration with git: the branch a
// session reports, the branch a project reports, and the merge that integrates
// a session's work back into the project's checkout. They use REAL git in a
// temporary repository, for the same reason internal/gitx does: the behaviour
// being tested is git's behaviour, and a fake would only assert what the author
// believed git does.

// gitInit creates a repository with one commit on main at dir.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	mustRun(t, "git", "init", "-q", "-b", "main", dir)
	mustRun(t, "git", "-C", dir, "config", "user.email", "test@example.com")
	mustRun(t, "git", "-C", dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", dir, "add", "f.txt")
	mustRun(t, "git", "-C", dir, "commit", "-qm", "init")
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// withProjects configures a test server with a project store, workspace and
// the ability to create new sessions.
func withProjects(t *testing.T, srv *Server, workspace string) {
	t.Helper()
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	srv.projects = ps
	srv.opts.WorkspaceDir = workspace
	srv.opts.NewService = func() (Service, error) { return &fakeService{}, nil }
}

// withProjectsAndFake is the same as withProjects, but returns the shared fake
// service so a test can install a custom RunTask (used by merge tests, where the
// agent must carry out real git work).
func withProjectsAndFake(t *testing.T, srv *Server, workspace string) *fakeService {
	t.Helper()
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	srv.projects = ps
	srv.opts.WorkspaceDir = workspace
	fs := &fakeService{}
	srv.opts.NewService = func() (Service, error) { return fs, nil }
	return fs
}

// makeProject creates a git repo under the workspace and registers it.
func makeProject(t *testing.T, srv *Server, name string) string {
	t.Helper()
	dir := filepath.Join(srv.opts.WorkspaceDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	id := "p-test-" + name
	if err := srv.projects.save(Project{
		ID:      id,
		Title:   name,
		Dir:     dir,
		Created: time.Now(),
	}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	return id
}

// TestSessionReportsItsBranch is the first half of the feature: a session that
// belongs to a project is checked out on its OWN branch (motita/<id>), because
// the session has its own worktree, and a free-standing session draws none.
func TestSessionReportsItsBranch(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")

	// Create a session that belongs to the project.
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := sessionBranch(ss.ID); ss.Branch != want {
		t.Errorf("branch = %q, want %q: a session with its own worktree is checked out on its own branch", ss.Branch, want)
	}

	// A free-standing session has no branch.
	w = post(t, srv, "/v1/sessions", `{}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create free session: %d %s", w.Code, w.Body.String())
	}
	var free SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &free); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if free.Branch != "" {
		t.Errorf("branch = %q, want empty: a free-standing session has no branch", free.Branch)
	}
}

// TestProjectReportsItsBranch is the other half: the project list shows the
// branch each project is on.
func TestProjectReportsItsBranch(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	makeProject(t, srv, "alpha")

	w := get(t, srv, "/v1/projects", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("list projects: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(resp.Projects))
	}
	if resp.Projects[0].Branch != "main" {
		t.Errorf("branch = %q, want main", resp.Projects[0].Branch)
	}
}

// TestProjectReportsNoBranchForNonRepo: a project whose directory is not a git
// repository reports no branch, and the list does not fail.
func TestProjectReportsNoBranchForNonRepo(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	dir := filepath.Join(ws, "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := srv.projects.save(Project{ID: "p-plain", Title: "plain", Dir: dir, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}

	w := get(t, srv, "/v1/projects", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Projects[0].Branch != "" {
		t.Errorf("branch = %q, want empty for a non-repo project", resp.Projects[0].Branch)
	}
}

// TestMergeSessionIntegratesTheBranch: a session's branch is merged back into
// the project's checkout. The agent carries out the merge; the session is then
// marked as merged and its work appears in the project.
func TestMergeSessionIntegratesTheBranch(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	fs := withProjectsAndFake(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	// Create a session and make a commit on its branch.
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	// The session already has its OWN worktree, created when it was created -
	// that is the feature. Commit the work there, in the session's checkout.
	wtPath := ss.Workspace
	if wtPath == "" {
		t.Fatal("the session must have a worktree to work in")
	}
	if err := os.WriteFile(filepath.Join(wtPath, "new.txt"), []byte("session work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", wtPath, "add", "new.txt")
	mustRun(t, "git", "-C", wtPath, "commit", "-qm", "session work")

	// Wire the fake to perform the integration like the agent would.
	fs.task = mergeIntegrationTask(t, srv, ss, p)

	// Merge it back and wait for the run to mark the session merged.
	st := doMerge(t, srv, ss, p)
	if st.MergedSHA == "" {
		t.Error("the merge must report the commit it produced")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "new.txt")); err != nil {
		t.Errorf("the session's work must be in the project's checkout: %v", err)
	}
}

// TestMergeSessionOnAConflictRollsBack: a conflicting merge is reported as a
// failure, and the project's checkout is returned to what it was. The agent is
// the one that detects and aborts the conflict.
func TestMergeSessionOnAConflictRollsBack(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	fs := withProjectsAndFake(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	// The session already has its own worktree: that is the feature.
	wtPath := ss.Workspace
	if wtPath == "" {
		t.Fatal("the session must have a worktree to work in")
	}
	// Create a conflicting change.
	if err := os.WriteFile(filepath.Join(wtPath, "f.txt"), []byte("from session\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", wtPath, "commit", "-qam", "session edit")
	if err := os.WriteFile(filepath.Join(p.Dir, "f.txt"), []byte("from main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", p.Dir, "commit", "-qam", "main edit")

	fs.task = mergeIntegrationTask(t, srv, ss, p)

	events := collect(t, srv, http.MethodPost, sessionPath(srv, ss.ID, "/merge"), "{}")
	var failed bool
	for _, e := range events {
		if e.Event == "error" {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatalf("a conflicting merge must finish with an error event: %+v", events)
	}
	if st := statusOf(t, srv, ss.ID); st.Merged {
		t.Errorf("a failed merge must not mark the session as merged: %+v", st)
	}
	// The checkout must be back to its own content.
	got, err := os.ReadFile(filepath.Join(p.Dir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "from main" {
		t.Errorf("the checkout must be back to its own content, got %q", string(got))
	}
}

// TestMergeSessionWithoutProjectIs409: a session that does not belong to a
// project cannot be merged, and the error says so.
func TestMergeSessionWithoutProjectIs409(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := post(t, srv, sessionPath(srv, DefaultSession, "/merge"), "{}", testToken)
	if w.Code != http.StatusConflict {
		t.Fatalf("merge without project: %d, want 409: %s", w.Code, w.Body.String())
	}
}

// TestMergeSessionOnItsOwnBranchIsNotASilentSuccess: when the project's checkout
// is already ON the session's branch, git accepts the merge and answers "Already
// up to date" with exit 0 - so the endpoint used to respond 200 with a sha for a
// merge that changed nothing. Measured against git 2.47.3.
//
// The state only arises when the project took a session's branch, which the
// isolation guard refuses; but a repository can be put there by hand, and the
// answer must then say that nothing was integrated rather than reporting a
// success.
func TestMergeSessionOnItsOwnBranchIsNotASilentSuccess(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	ss := createSessionIn(t, srv, pid)
	// Move the session's worktree off its branch, then put the PROJECT's
	// checkout on it: the state git permits and reports as a no-op merge.
	mustRun(t, "git", "-C", p.Dir, "branch", "feature")
	mustRun(t, "git", "-C", ss.Workspace, "checkout", "-q", "feature")
	mustRun(t, "git", "-C", p.Dir, "checkout", "-q", sessionBranch(ss.ID))

	w := post(t, srv, sessionPath(srv, ss.ID, "/merge"), "{}", testToken)
	if w.Code == http.StatusOK {
		t.Fatalf("a merge that integrated nothing must not report success: %d %s", w.Code, w.Body.String())
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "nothing to integrate") && !strings.Contains(w.Body.String(), "no work to integrate") {
		t.Errorf("the refusal must say nothing was integrated, got %s", w.Body.String())
	}
}

// write puts a file in place, creating its folders.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// statusOf reads one session's status the way the sidebar does.
func statusOf(t *testing.T, srv *Server, id string) SessionStatus {
	t.Helper()
	c, _ := srv.lookup(id)
	if c == nil {
		t.Fatalf("no session %s", id)
	}
	return c.status()
}

// Reported from a real session: the agent finished its work, the files were in the session's
// worktree, and Integrate stayed disabled. Nothing commits what an agent writes, so the session's
// branch had no commits ahead of the project's - the only thing "mergeable" used to ask about.
func TestASessionWithUncommittedWorkCanBeIntegrated(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	fs := withProjectsAndFake(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	ss := createSessionIn(t, srv, pid)

	if statusOf(t, srv, ss.ID).Mergeable {
		t.Fatal("a session that has done nothing has nothing to integrate")
	}
	if err := os.WriteFile(filepath.Join(ss.Workspace, "feature.txt"), []byte("the agent wrote this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ss.Workspace, "f.txt"), []byte("base\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(t, srv, ss.ID); !st.Mergeable {
		t.Fatalf("a session whose worktree holds the agent's files must offer Integrate: %+v", st)
	}

	fs.task = mergeIntegrationTask(t, srv, ss, p)
	doMerge(t, srv, ss, p)

	if b, err := os.ReadFile(filepath.Join(p.Dir, "feature.txt")); err != nil || string(b) != "the agent wrote this\n" {
		t.Errorf("the new file must be in the project: %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(p.Dir, "f.txt")); string(b) != "base\nedited\n" {
		t.Errorf("the edit must be in the project: %q", b)
	}
	// One commit for the work, on the SESSION's branch, and one merge commit that names the session.
	log := gitOut(t, p.Dir, "log", "--format=%s", "-3")
	if !strings.Contains(log, "motita: integrate session "+ss.ID) {
		t.Errorf("the merge must name the session:\n%s", log)
	}
	if n := strings.TrimSpace(gitOut(t, p.Dir, "rev-list", "--count", "main.."+sessionBranch(ss.ID))); n != "0" {
		t.Errorf("after the merge the session has %s commits the project lacks", n)
	}
	if st := statusOf(t, srv, ss.ID); st.Mergeable {
		t.Errorf("an integrated session has nothing left to integrate: %+v", st)
	}
}

// The commit records the work and only the work: what a tool wrote in its HOME never travels into
// the project, and the commit's subject says what the session was for.
func TestIntegratingLeavesToolCachesOutAndNamesTheTask(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	fs := withProjectsAndFake(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	ss := createSessionIn(t, srv, pid)
	c, _ := srv.lookup(ss.ID)
	c.stateMu.Lock()
	c.lastTask = "add a section\nto create cards"
	c.stateMu.Unlock()

	write(t, filepath.Join(ss.Workspace, ".npm", "_cacache", "x"), "cache")
	write(t, filepath.Join(ss.Workspace, ".cache", "pip", "y"), "cache")
	write(t, filepath.Join(ss.Workspace, "src", ".cache", "mine.txt"), "the project's own")
	write(t, filepath.Join(ss.Workspace, "real.txt"), "work")

	fs.task = mergeIntegrationTask(t, srv, ss, p)
	doMerge(t, srv, ss, p)

	for _, leaked := range []string{".npm", ".cache"} {
		if _, err := os.Stat(filepath.Join(p.Dir, leaked)); err == nil {
			t.Errorf("%s travelled into the project", leaked)
		}
	}
	for _, kept := range []string{"real.txt", "src/.cache/mine.txt"} {
		if _, err := os.Stat(filepath.Join(p.Dir, kept)); err != nil {
			t.Errorf("%s is the session's work and must arrive: %v", kept, err)
		}
	}
	if subj := gitOut(t, p.Dir, "log", "--format=%s", "-1", sessionBranch(ss.ID)); subj == "" {
		t.Errorf("the session branch must have a commit with the agent's work")
	}
	mergeSubj := gitOut(t, p.Dir, "log", "--format=%s", "-1")
	if !strings.Contains(mergeSubj, "motita: integrate session "+ss.ID) {
		t.Errorf("the merge commit must name the session: %q", mergeSubj)
	}
}

// With nothing to integrate the answer says so, instead of a merge that reports success.
func TestIntegratingASessionWithNoWorkIsRefusedPlainly(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	ss := createSessionIn(t, srv, pid)
	write(t, filepath.Join(ss.Workspace, ".npm", "_cacache", "x"), "cache") // only a tool's cache
	w := post(t, srv, sessionPath(srv, ss.ID, "/merge"), "{}", testToken)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "no work to integrate") {
		t.Fatalf("merge = %d %s", w.Code, w.Body.String())
	}
}

// A session that has no worktree of its own works ON the user's branch. Integrating it must never
// commit the agent's files there: that is the user's branch, and the files were not asked to land.
func TestIntegratingNeverCommitsOnTheUsersOwnBranch(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	fs := withProjectsAndFake(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	ss := createSessionIn(t, srv, pid)
	c, _ := srv.lookup(ss.ID)
	c.stateMu.Lock()
	c.workspace = p.Dir // the fallback: the session runs in the project's own checkout
	c.stateMu.Unlock()
	write(t, filepath.Join(p.Dir, "user-wip.txt"), "uncommitted, the user's")
	before := gitOut(t, p.Dir, "rev-parse", "HEAD")

	fs.task = mergeIntegrationTask(t, srv, ss, p)

	events := collect(t, srv, http.MethodPost, sessionPath(srv, ss.ID, "/merge"), "{}")
	var failed bool
	for _, e := range events {
		if e.Event == "error" {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatalf("integrating a session with no own worktree must fail: %+v", events)
	}
	if after := gitOut(t, p.Dir, "rev-parse", "HEAD"); after != before {
		t.Errorf("a commit landed on the user's branch: %s -> %s", before, after)
	}
	if out := gitOut(t, p.Dir, "status", "--short"); !strings.Contains(out, "user-wip.txt") {
		t.Errorf("the user's uncommitted file must stay uncommitted:\n%s", out)
	}
}

// TestMergeMarksTheSessionReadOnly: once a session's work is in the project,
// the session must refuse new tasks and plans so it cannot rewrite history.
func TestMergeMarksTheSessionReadOnly(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	fs := withProjectsAndFake(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	ss := createSessionIn(t, srv, pid)
	write(t, filepath.Join(ss.Workspace, "feature.txt"), "work")

	fs.task = mergeIntegrationTask(t, srv, ss, p)
	doMerge(t, srv, ss, p)

	if st := statusOf(t, srv, ss.ID); !st.Merged || st.Mergeable {
		t.Errorf("after merge, Merged=true and Mergeable=false: %+v", st)
	}
	if st := statusOf(t, srv, ss.ID); !st.Continuable {
		t.Errorf("the merged session must be continuable: %+v", st)
	}

	for _, path := range []string{"/task", "/plan"} {
		var body string
		if path == "/task" {
			body = `{"task":"do more"}`
		} else {
			body = `{"prompt":"do more"}`
		}
		w := post(t, srv, sessionPath(srv, ss.ID, path), body, testToken)
		if w.Code != http.StatusConflict {
			t.Errorf("%s on a merged session = %d, want 409", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "already been integrated") {
			t.Errorf("%s must tell the user to continue in a new session: %s", path, w.Body.String())
		}
	}
}

// TestContinueSessionPullsMainAndStartsAFreshSession: after integration, the
// user can create a new session from the project's current base branch.
func TestContinueSessionPullsMainAndStartsAFreshSession(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	fs := withProjectsAndFake(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	ss := createSessionIn(t, srv, pid)
	write(t, filepath.Join(ss.Workspace, "feature.txt"), "work")

	fs.task = mergeIntegrationTask(t, srv, ss, p)
	doMerge(t, srv, ss, p)

	// Simulate another client pushing to origin: add a remote repo, push a commit
	// from the project, then continue. The project's main now has that commit.
	remote := filepath.Join(ws, "remote-repo.git")
	mustRun(t, "git", "-C", p.Dir, "clone", "--bare", "-q", p.Dir, remote)
	mustRun(t, "git", "-C", p.Dir, "remote", "add", "origin", remote)
	mustRun(t, "git", "-C", p.Dir, "push", "-q", "origin", "main")
	write(t, filepath.Join(p.Dir, "upstream.txt"), "from upstream")
	mustRun(t, "git", "-C", p.Dir, "add", "upstream.txt")
	mustRun(t, "git", "-C", p.Dir, "commit", "-qm", "upstream change")
	mustRun(t, "git", "-C", p.Dir, "push", "-q", "origin", "main")

	// Roll the project's main back so the pull actually has work to do.
	mustRun(t, "git", "-C", p.Dir, "reset", "--hard", "HEAD~1")

	w := post(t, srv, sessionPath(srv, ss.ID, "/continue"), "{}", testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("continue: %d %s", w.Code, w.Body.String())
	}
	var next SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.ProjectID != pid {
		t.Errorf("new session project = %q, want %q", next.ProjectID, pid)
	}
	if next.Workspace == "" || next.Workspace == p.Dir {
		t.Errorf("new session must have its own worktree: %q", next.Workspace)
	}
	if next.Worktree == "" {
		t.Errorf("new session must report its worktree")
	}

	if strings.Contains(next.Title, "continue:") {
		t.Logf("continued session title: %q", next.Title)
	}
	// The new session branches from the project's current main, which now
	// includes the integrated work AND the upstream change. It must be on its
	// own branch and have no uncommitted changes.
	if got := gitOut(t, next.Workspace, "rev-parse", "--abbrev-ref", "HEAD"); got != sessionBranch(next.ID) {
		t.Errorf("new session branch = %q, want %q", got, sessionBranch(next.ID))
	}
	if log := gitOut(t, next.Workspace, "log", "--format=%s"); !strings.Contains(log, "upstream change") {
		t.Errorf("the new session must include the pulled upstream change; log:\n%s", log)
	}
	if got := gitOut(t, next.Workspace, "status", "--short"); got != "" {
		t.Errorf("the new session must have no uncommitted changes:\n%s", got)
	}
	if n := strings.TrimSpace(gitOut(t, next.Workspace, "rev-list", "--count", "main.."+sessionBranch(next.ID))); n != "0" {
		t.Errorf("the new session must not be ahead of main: %s commits", n)
	}

	// Continuing is only valid for merged sessions.
	unmerged := createSessionIn(t, srv, pid)
	w = post(t, srv, sessionPath(srv, unmerged.ID, "/continue"), "{}", testToken)
	if w.Code != http.StatusConflict {
		t.Errorf("continue on unmerged session = %d, want 409", w.Code)
	}
}
