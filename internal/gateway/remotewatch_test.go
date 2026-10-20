package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pushUpstream adds one more commit to the project's origin, from another clone.
func pushUpstream(t *testing.T, ws, file string) {
	t.Helper()
	elsewhere := filepath.Join(ws, "elsewhere")
	if err := os.WriteFile(filepath.Join(elsewhere, file), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", elsewhere, "add", file)
	mustRun(t, "git", "-C", elsewhere, "commit", "-qm", "more "+file)
	mustRun(t, "git", "-C", elsewhere, "push", "-q", "origin", "main")
}

// A look at origin only reads: the news appears on the project, tells the phones once per state,
// and leaves the working tree exactly as it was.
func TestRemoteWatchReportsNewCommitsOnceAndTouchesNothing(t *testing.T) {
	srv, tr, _ := newPushServer(t)
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	_ = pushCall(t, srv, "POST", "/v1/push/subscriptions", subscriptionJSON(t, testEndpoint, "es"))
	pid, dir := projectWithOrigin(t, srv, ws)
	ctx := context.Background()

	srv.lookForRemoteChanges(ctx)
	srv.pushWG.Wait()
	got := srv.remoteWatch.of(pid)
	if len(got) != 1 || got[0].Behind != 1 || got[0].Branch != "main" || got[0].Repo != "" ||
		len(got[0].Latest) != 1 || !strings.Contains(got[0].Latest[0], "upstream change") {
		t.Fatalf("changes = %+v", got)
	}
	if hasUpstream(dir) {
		t.Fatal("a look must not bring the commits in")
	}
	if tr.count() != 1 {
		t.Fatalf("sent %d, want 1", tr.count())
	}
	if body := get(t, srv, "/v1/projects", testToken).Body.String(); !strings.Contains(body, `"remote_changes"`) {
		t.Errorf("the listing must carry the changes: %s", body)
	}

	// The same state again is not news.
	srv.lookForRemoteChanges(ctx)
	srv.pushWG.Wait()
	if tr.count() != 1 {
		t.Fatalf("a repeated state was announced again: %d", tr.count())
	}
	// Another commit is.
	pushUpstream(t, ws, "second.txt")
	srv.lookForRemoteChanges(ctx)
	srv.pushWG.Wait()
	if c := srv.remoteWatch.of(pid); len(c) != 1 || c[0].Behind != 2 || len(c[0].Latest) != 2 || tr.count() != 2 {
		t.Fatalf("changes = %+v, sent %d", c, tr.count())
	}

	// Updating the main branch brings them in and clears the news, without announcing anything.
	post(t, srv, "/v1/projects/"+pid+"/main/refresh", "{}", testToken)
	srv.mainRefresh.wait()
	srv.pushWG.Wait()
	if c := srv.remoteWatch.of(pid); len(c) != 0 || !hasUpstream(dir) || tr.count() != 2 {
		t.Fatalf("after the update: %+v, upstream %v, sent %d", c, hasUpstream(dir), tr.count())
	}
	// And the next news after catching up is announced again.
	pushUpstream(t, ws, "third.txt")
	srv.lookForRemoteChanges(ctx)
	srv.pushWG.Wait()
	if tr.count() != 3 {
		t.Fatalf("sent %d, want 3", tr.count())
	}
}

// A project with no origin, an origin that cannot be reached, or a folder that is not a repository
// has nothing to report, and a repository shared by two projects is asked once.
func TestRemoteWatchStaysQuietWhenThereIsNothingToSay(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	plain := makeProject(t, srv, "plain")
	broken := makeProject(t, srv, "broken")
	mustRun(t, "git", "-C", srv.projectOf(broken).Dir, "remote", "add", "origin", filepath.Join(ws, "missing.git"))
	srv.lookForRemoteChanges(context.Background())
	if len(srv.remoteWatch.of(plain)) != 0 || len(srv.remoteWatch.of(broken)) != 0 {
		t.Fatal("nothing to report was reported")
	}
	// A cancelled round stops at once.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv.lookForRemoteChanges(ctx)
}

// Another repository of the project is watched on its own branch and named in the change.
func TestRemoteWatchCoversTheProjectsOtherRepositories(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	pid, dir := projectWithOrigin(t, srv, ws)
	p := srv.projectOf(pid)
	p.Repos = []ProjectRepo{{Name: "api", Dir: dir}}
	srv.watchRemote(context.Background(), p, map[string]error{})
	got := srv.remoteWatch.of(pid)
	if len(got) != 2 || got[1].Repo != "api" {
		t.Fatalf("changes = %+v", got)
	}
}

func TestRemoteWatchTicksOnItsOwnAndCanBeSwitchedOff(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.RemoteWatchInterval = 10 * time.Millisecond })
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	pid, _ := projectWithOrigin(t, srv, ws)
	deadline := time.Now().Add(5 * time.Second)
	for len(srv.remoteWatch.of(pid)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(srv.remoteWatch.of(pid)) == 0 {
		t.Fatal("the periodic look never found the change")
	}
	off := newTestServer(t, &fakeService{}, func(o *Options) { o.RemoteWatchInterval = -1 })
	off.startRemoteWatch() // returns at once: nothing to wait for
}
