package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// projectWithOrigin makes a project whose origin holds one commit the checkout does not have, and
// returns the project, its directory and the file that commit adds.
func projectWithOrigin(t *testing.T, srv *Server, ws string) (id, dir string) {
	t.Helper()
	id = makeProject(t, srv, "repo")
	dir = srv.projectOf(id).Dir
	remote := filepath.Join(ws, "origin.git")
	mustRun(t, "git", "clone", "--bare", "-q", dir, remote)
	mustRun(t, "git", "-C", dir, "remote", "add", "origin", remote)
	elsewhere := filepath.Join(ws, "elsewhere")
	mustRun(t, "git", "clone", "-q", remote, elsewhere)
	mustRun(t, "git", "-C", elsewhere, "config", "user.email", "o@example.com")
	mustRun(t, "git", "-C", elsewhere, "config", "user.name", "O")
	if err := os.WriteFile(filepath.Join(elsewhere, "upstream.txt"), []byte("up\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", elsewhere, "add", "upstream.txt")
	mustRun(t, "git", "-C", elsewhere, "commit", "-qm", "upstream change")
	mustRun(t, "git", "-C", elsewhere, "push", "-q", "origin", "main")
	return id, dir
}

func hasUpstream(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "upstream.txt"))
	return err == nil
}

// Opening a session updates the project's main branch, and the answer does not wait for it.
func TestOpeningASessionUpdatesTheMainBranchInTheBackground(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	pid, dir := projectWithOrigin(t, srv, ws)

	createSessionIn(t, srv, pid)
	srv.mainRefresh.wait()
	if !hasUpstream(dir) {
		t.Fatal("the project's main branch must have been brought up to date")
	}
}

// Closing the last session updates it too.
func TestClosingASessionUpdatesTheMainBranchInTheBackground(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	pid, dir := projectWithOrigin(t, srv, ws)
	ss := createSessionIn(t, srv, pid)
	srv.mainRefresh.wait()
	mustRun(t, "git", "-C", dir, "reset", "-q", "--hard", "HEAD~1")
	if hasUpstream(dir) {
		t.Fatal("setup: the checkout must be behind origin again")
	}

	if w := doDelete(t, srv, sessionPath(srv, ss.ID, "")); w.Code != http.StatusNoContent {
		t.Fatalf("delete session = %d: %s", w.Code, w.Body.String())
	}
	srv.mainRefresh.wait()
	if !hasUpstream(dir) {
		t.Fatal("closing a session must bring the main branch up to date")
	}
}

// A checkout with uncommitted work is never touched: the reason is a notice on the project, the
// session opens, and a later successful update clears the notice.
func TestMainBranchUpdateNeverTouchesUncommittedWork(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	pid, dir := projectWithOrigin(t, srv, ws)
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	createSessionIn(t, srv, pid)
	srv.mainRefresh.wait()
	if hasUpstream(dir) {
		t.Fatal("a dirty checkout must not be updated")
	}
	if w := srv.mainRefresh.warning(pid); !strings.Contains(w, "uncommitted") {
		t.Fatalf("warning = %q, want the uncommitted changes named", w)
	}
	w := get(t, srv, "/v1/projects", testToken)
	if !strings.Contains(w.Body.String(), `"main_warning"`) {
		t.Errorf("the project listing must carry the notice: %s", w.Body.String())
	}

	if err := os.Remove(filepath.Join(dir, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	createSessionIn(t, srv, pid)
	srv.mainRefresh.wait()
	if !hasUpstream(dir) || srv.mainRefresh.warning(pid) != "" {
		t.Errorf("once clean the update must work and clear the notice (%q)", srv.mainRefresh.warning(pid))
	}
}

// An origin that cannot be reached still gives a session; the reason is only a notice.
func TestUnreachableOriginIsANoticeNotAnError(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	mustRun(t, "git", "-C", srv.projectOf(pid).Dir, "remote", "add", "origin", filepath.Join(ws, "missing.git"))

	ss := createSessionIn(t, srv, pid)
	srv.mainRefresh.wait()
	if ss.ProjectID != pid || ss.Workspace == "" {
		t.Errorf("the session must still be placed in the project: %+v", ss)
	}
	if w := srv.mainRefresh.warning(pid); !strings.Contains(w, "could not fetch") {
		t.Errorf("warning = %q, want git's reason", w)
	}
}

// Only one update per project runs at a time, and nil or unknown projects are ignored.
func TestMainRefreshIsDeduplicated(t *testing.T) {
	var m mainRefreshState
	if !m.begin("p") || m.begin("p") {
		t.Fatal("a second update of a running project must be dropped")
	}
	m.finish("p", "why")
	if m.warning("p") != "why" || !m.begin("p") {
		t.Fatal("a finished update frees the project and keeps its notice")
	}
	srv := newTestServer(t, &fakeService{})
	srv.refreshMainInBackground(nil)
}
