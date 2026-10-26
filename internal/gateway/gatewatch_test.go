package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests that disappear between two looks are shown on the project and told to the phones once,
// until the person accepts the lower count; tests that appear raise the floor and are never news.
func TestGateWatchReportsLostTestsOnceUntilAccepted(t *testing.T) {
	srv, tr, _ := newPushServer(t)
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	_ = pushCall(t, srv, "POST", "/v1/push/subscriptions", subscriptionJSON(t, testEndpoint, "es"))
	pid := makeProject(t, srv, "repo")
	dir := srv.projectOf(pid).Dir
	putFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	putFile(t, filepath.Join(dir, "a_test.go"), "package x\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n")
	ctx := context.Background()

	srv.lookForRemoteChanges(ctx)
	srv.pushWG.Wait()
	if p := srv.projectOf(pid); p.TestsFloor != 2 || srv.remoteWatch.gate.of(pid) != nil || tr.count() != 0 {
		t.Fatalf("floor %d, lost %v, sent %d", p.TestsFloor, srv.remoteWatch.gate.of(pid), tr.count())
	}

	if err := os.Remove(filepath.Join(dir, "a_test.go")); err != nil {
		t.Fatal(err)
	}
	srv.lookForRemoteChanges(ctx)
	srv.pushWG.Wait()
	if l := srv.remoteWatch.gate.of(pid); l == nil || l.Was != 2 || l.Now != 0 || tr.count() != 1 {
		t.Fatalf("lost %+v, sent %d", l, tr.count())
	}
	if body := get(t, srv, "/v1/projects", testToken).Body.String(); !strings.Contains(body, `"tests_lost"`) {
		t.Errorf("the listing must carry the drop: %s", body)
	}
	srv.lookForRemoteChanges(ctx)
	srv.pushWG.Wait()
	if tr.count() != 1 {
		t.Fatalf("a repeated drop was announced again: %d", tr.count())
	}
	putFile(t, filepath.Join(dir, "b_test.go"), "package x\nfunc TestC(t *testing.T) {}\n")
	srv.lookForRemoteChanges(ctx)
	srv.pushWG.Wait()
	if l := srv.remoteWatch.gate.of(pid); l == nil || l.Now != 1 || tr.count() != 2 {
		t.Fatalf("a further change of the count is news: %+v, sent %d", l, tr.count())
	}

	post(t, srv, "/v1/projects/"+pid+"/tests/accept", "{}", testToken)
	if p := srv.projectOf(pid); p.TestsFloor != 1 || srv.remoteWatch.gate.of(pid) != nil {
		t.Fatalf("after accepting: floor %d, lost %v", p.TestsFloor, srv.remoteWatch.gate.of(pid))
	}
	if r := post(t, srv, "/v1/projects/nope/tests/accept", "{}", testToken); r.Code != 404 {
		t.Errorf("unknown project: %d", r.Code)
	}
	putFile(t, filepath.Join(srv.projects.dir, pid+".json"), "not json")
	if r := post(t, srv, "/v1/projects/"+pid+"/tests/accept", "{}", testToken); r.Code != 500 {
		t.Errorf("a project file that cannot be read: %d", r.Code)
	}
	srv.projects = nil
	if r := post(t, srv, "/v1/projects/"+pid+"/tests/accept", "{}", testToken); r.Code != 404 {
		t.Errorf("a gateway without projects: %d", r.Code)
	}
}

// A project whose folder declares no gate says so in its listing; one that declares it does not.
func TestProjectListingFlagsAMissingGate(t *testing.T) {
	srv, _, _ := newPushServer(t)
	withProjectsAndFake(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	body := get(t, srv, "/v1/projects", testToken).Body.String()
	if !strings.Contains(body, `"no_gate":true`) {
		t.Fatalf("no gate was not flagged: %s", body)
	}
	putFile(t, filepath.Join(srv.projectOf(pid).Dir, "go.mod"), "module x\n")
	if body := get(t, srv, "/v1/projects", testToken).Body.String(); strings.Contains(body, `"no_gate"`) {
		t.Fatalf("a declared gate was flagged: %s", body)
	}
}
