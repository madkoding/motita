package gateway

// The refusals and edge cases of the two session-lifecycle handlers (merge and
// continue) that the happy-path tests never take. They are grouped here rather
// than in handlers_paths_test.go because they need a real project store and, in
// several cases, a real git repository - they are about git's behaviour and the
// project's state, not about the wire.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/logx"
)

// addOrigin gives a repository a real origin remote with its current branch
// pushed, so PullFastForward has something to fetch. Without it every continue
// fails at the fetch and the branches below the pull are never reached.
func addOrigin(t *testing.T, dir, branch string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "origin.git")
	mustRun(t, "git", "clone", "--bare", "-q", dir, remote)
	mustRun(t, "git", "-C", dir, "remote", "add", "origin", remote)
	mustRun(t, "git", "-C", dir, "push", "-q", "origin", branch)
}

// callHandler drives one of the session handlers directly against a conversation,
// bypassing the router so a test can put the conversation in exactly the state it
// is testing.
func callHandler(t *testing.T, srv *Server, c *conversation, fn func(http.ResponseWriter, *http.Request), path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req = req.WithContext(context.WithValue(req.Context(), conversationKey, c))
	w := httptest.NewRecorder()
	fn(w, req)
	return w
}

// A session whose work is already integrated refuses a second integration: the
// merge would run against a branch whose history is already in the project.
func TestAMergeOnAnAlreadyMergedSessionIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjectStore(t, srv)
	c, _ := srv.lookup(DefaultSession)
	c.setProjectID("p", t.TempDir(), t.TempDir())
	c.setMerged("abc123")

	w := callHandler(t, srv, c, srv.handleMergeSession, "/v1/sessions/"+DefaultSession+"/merge")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "already been integrated") {
		t.Errorf("body = %q", w.Body.String())
	}
}

// A project whose directory is not a git repository has no base branch, so there
// is nothing to merge into; the refusal says so rather than letting the run start
// and fail inside git.
func TestAMergeWithoutABaseBranchIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ps := withProjectStore(t, srv)
	dir := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ps.save(Project{ID: "p-plain", Title: "plain", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	c, _ := srv.lookup(DefaultSession)
	c.setProjectID("p-plain", dir, dir)

	w := callHandler(t, srv, c, srv.handleMergeSession, "/v1/sessions/"+DefaultSession+"/merge")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no base branch") {
		t.Errorf("body = %q, want it to blame the missing base branch", w.Body.String())
	}
}

// A session that has made no commits and has no uncommitted work has nothing to
// integrate, and the refusal says exactly that instead of spending a run.
func TestAMergeWithNoWorkIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	ss := createSessionIn(t, srv, pid)
	c, _ := srv.lookup(ss.ID)

	w := callHandler(t, srv, c, srv.handleMergeSession, sessionPath(srv, ss.ID, "/merge"))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no work to integrate") {
		t.Errorf("body = %q, want it to say there is nothing to integrate", w.Body.String())
	}
}

// Continue on a merged session that has no workspace has nothing to continue
// FROM, and the refusal names the missing project rather than half-creating one.
func TestAContinueWithoutAWorkspaceIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjectStore(t, srv)
	c, _ := srv.lookup(DefaultSession)
	c.setMerged("abc123")

	w := callHandler(t, srv, c, srv.handleContinueSession, "/v1/sessions/"+DefaultSession+"/continue")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "nothing to continue from") {
		t.Errorf("body = %q", w.Body.String())
	}
}

// A merged session whose project record is gone is refused with 404: continuing
// would branch from whatever the id resolves to now.
func TestAContinueForAMissingProjectIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjectStore(t, srv)
	c, _ := srv.lookup(DefaultSession)
	c.setProjectID("a-project-that-was-deleted", t.TempDir(), t.TempDir())
	c.setMerged("abc123")

	w := callHandler(t, srv, c, srv.handleContinueSession, "/v1/sessions/"+DefaultSession+"/continue")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
}

// A project that is not a repository has no base branch to pull, so continuing
// is refused before a new session is minted.
func TestAContinueWithoutABaseBranchIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ps := withProjectStore(t, srv)
	dir := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ps.save(Project{ID: "p-plain", Title: "plain", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	c, _ := srv.lookup(DefaultSession)
	c.setProjectID("p-plain", dir, dir)
	c.setMerged("abc123")

	w := callHandler(t, srv, c, srv.handleContinueSession, "/v1/sessions/"+DefaultSession+"/continue")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no base branch") {
		t.Errorf("body = %q", w.Body.String())
	}
}

// A project with no origin remote cannot be updated to the latest base branch, so
// continuing is refused with the git error rather than branching from stale code.
func TestAContinueRefusesWhenThePullFails(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	ss := createSessionIn(t, srv, pid)
	c, _ := srv.lookup(ss.ID)
	c.setMerged("abc123")

	w := callHandler(t, srv, c, srv.handleContinueSession, sessionPath(srv, ss.ID, "/continue"))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "could not fetch") {
		t.Errorf("body = %q, want the fetch failure named", w.Body.String())
	}
}

// A gateway at its ceiling cannot mint one more conversation. It is asked with
// MaxSessions raised after the session exists, because creating the session is
// itself a mint: the ceiling is lowered under it to reach the branch.
func TestAContinueAtTheCeilingIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	addOrigin(t, p.Dir, "main")
	ss := createSessionIn(t, srv, pid)
	c, _ := srv.lookup(ss.ID)
	c.setMerged("abc123")
	// Now there is one session; closing the ceiling to it makes the next mint fail.
	srv.opts.MaxSessions = srv.sessionCount()

	w := callHandler(t, srv, c, srv.handleContinueSession, sessionPath(srv, ss.ID, "/continue"))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ceiling") {
		t.Errorf("body = %q, want the ceiling named", w.Body.String())
	}
}

// A gateway started without a way to build another conversation answers 501:
// continuing is a capability this process does not have.
func TestAContinueWithoutAServiceFactoryIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	addOrigin(t, p.Dir, "main")
	ss := createSessionIn(t, srv, pid)
	c, _ := srv.lookup(ss.ID)
	c.setMerged("abc123")
	srv.opts.NewService = nil

	w := callHandler(t, srv, c, srv.handleContinueSession, sessionPath(srv, ss.ID, "/continue"))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", w.Code, w.Body.String())
	}
}

// When the continued session's own worktree cannot be created the session still
// starts - in the project's own directory - and the reason is logged rather than
// failing the request.
func TestAContinueFallsBackToTheProjectDirectoryAndLogsIt(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "gw.log")
	log, err := logx.New(logx.Options{Path: logPath, Level: logx.Info})
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Log = log })
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	addOrigin(t, p.Dir, "main")
	ss := createSessionIn(t, srv, pid)
	c, _ := srv.lookup(ss.ID)
	c.setMerged("abc123")
	// Sabotage the worktree root: a worktree cannot be created under a path whose
	// parent is a FILE, which is a failure git reports rather than works around.
	if err := os.RemoveAll(filepath.Join(ws, "worktrees")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "worktrees"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := callHandler(t, srv, c, srv.handleContinueSession, sessionPath(srv, ss.ID, "/continue"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var next SessionStatus
	decodeJSON(t, w, &next)
	if next.Workspace != p.Dir {
		t.Errorf("workspace = %q, want the project directory %q", next.Workspace, p.Dir)
	}
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "could not be created") {
		t.Errorf("the fallback must be logged; log:\n%s", body)
	}
}

// A continued session whose old session had a real title carries it, prefixed so
// the origin is legible.
func TestAContinuedSessionCarriesItsPredecessorsTitle(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	addOrigin(t, p.Dir, "main")
	ss := createSessionIn(t, srv, pid)
	c, _ := srv.lookup(ss.ID)
	c.setMerged("abc123")
	c.setTitle("a real title")

	w := callHandler(t, srv, c, srv.handleContinueSession, sessionPath(srv, ss.ID, "/continue"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var next SessionStatus
	decodeJSON(t, w, &next)
	if next.Title != "continue: a real title" {
		t.Errorf("title = %q, want it to say where it came from", next.Title)
	}
}
