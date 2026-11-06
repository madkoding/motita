package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// Close waits for the goroutines that follow a pull request: one still settling a merge has git
// running in the project's folder, and a folder removed under it left the test (and a process
// leaving) with "directory not empty".
func TestCloseWaitsForThePullRequestWatchers(t *testing.T) {
	srv, host, ss := isolatedPRSession(t, "https://github.com/o/r.git")
	srv.opts.PRPollEvery = 10 * time.Millisecond
	connectGitHub(t, srv)
	prHost(host, sessionBranch(ss.ID), "pending", "clean")
	if w := postJSON(t, srv, sessionPath(srv, ss.ID, "/pr/fix"), testToken, `{}`); w.Code != http.StatusAccepted {
		t.Fatalf("starting: %d %s", w.Code, w.Body.String())
	}
	for i := 0; i < 400 && !srv.watching(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.watching() {
		t.Fatal("the watch never started")
	}
	// From here every answer of the host takes a while: the watcher is in the middle of a poll
	// when the server closes, and Close has to wait for it rather than return past it.
	host.mu.Lock()
	host.slow = 300 * time.Millisecond
	host.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	if err := srv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.watching() {
		t.Error("Close returned while a pull request watcher was still running")
	}
}

// watching reports whether any pull request watcher is still registered.
func (s *Server) watching() bool {
	s.prMu.Lock()
	defer s.prMu.Unlock()
	return len(s.prWatching) > 0
}
