package gateway

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/gitx"
)

func nestedFixture(t *testing.T) (srv *Server, ws, id, api string) {
	t.Helper()
	srv = newTestServer(t, &fakeService{})
	ws = t.TempDir()
	withProjectsAndFake(t, srv, ws)
	id, dir := plainProject(t, srv, ws, "services")
	api = filepath.Join(dir, "api")
	repoWithOrigin(t, ws, api)
	return srv, ws, id, api
}

func writeIn(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNestedStatusTellsWhatTheRepositoryHolds(t *testing.T) {
	srv, _, id, api := nestedFixture(t)
	url := "/v1/projects/" + id + "/repo?repo="
	if w := get(t, srv, "/v1/projects/nope/repo?repo=api", testToken); w.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d", w.Code)
	}
	if w := get(t, srv, url+"..%2F..", testToken); w.Code != http.StatusNotFound {
		t.Errorf("a path outside the folder = %d", w.Code)
	}
	mustRun(t, "git", "-C", api, "branch", "done")
	mustRun(t, "git", "-C", api, "switch", "-q", "-c", "work")
	writeIn(t, api, "wip.txt")
	w := get(t, srv, url+"api", testToken)
	body := w.Body.String()
	wants := []string{
		`"branch":"work"`, `"main_branch":"main"`, `"wip.txt"`,
		`"subject":"init"`, `"merged":["done"]`, `"behind":0`,
	}
	for _, want := range wants {
		if w.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("status %d lacks %s: %s", w.Code, want, body)
		}
	}
	// On the main branch there is nothing to be ahead or behind of.
	mustRun(t, "git", "-C", api, "stash", "-u", "-q")
	mustRun(t, "git", "-C", api, "switch", "-q", "main")
	if w := get(t, srv, url+"api", testToken); !strings.Contains(w.Body.String(), `"changes":[]`) {
		t.Errorf("a clean checkout lists no changes: %s", w.Body.String())
	}
}

func TestNestedStatusOfARepositoryWithNoMainBranch(t *testing.T) {
	srv, _, id, api := nestedFixture(t)
	mustRun(t, "git", "-C", api, "branch", "-m", "trunk")
	w := get(t, srv, "/v1/projects/"+id+"/repo?repo=api", testToken)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"main_branch"`) {
		t.Errorf("no main branch to name: %d %s", w.Code, w.Body.String())
	}
}

func TestNestedActionsRefuseWhatTheyCannotDo(t *testing.T) {
	srv, _, id, _ := nestedFixture(t)
	url := "/v1/projects/" + id + "/repo/"
	if w := post(t, srv, url+"explode?repo=api", "{}", testToken); w.Code != http.StatusNotFound {
		t.Errorf("unknown action = %d", w.Code)
	}
	if w := post(t, srv, url+"pull?repo=nope", "{}", testToken); w.Code != http.StatusNotFound {
		t.Errorf("unknown repo = %d", w.Code)
	}
	if w := post(t, srv, url+"branch?repo=api", "{not json", testToken); w.Code != http.StatusBadRequest {
		t.Errorf("a broken body = %d", w.Code)
	}
	if w := post(t, srv, url+"branch?repo=api", `{"branch":"bad name"}`, testToken); w.Code != http.StatusBadRequest {
		t.Errorf("a name git refuses = %d", w.Code)
	}
	if w := post(t, srv, url+"discard?repo=api", "{}", testToken); w.Code != http.StatusBadRequest {
		t.Errorf("discarding without confirmation = %d", w.Code)
	}
	if w := post(t, srv, url+"stash?repo=api", "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("nothing to put aside = %d", w.Code)
	}
}

func TestNestedPullBringsInOriginOnlyToACleanCheckout(t *testing.T) {
	srv, _, id, api := nestedFixture(t)
	url := "/v1/projects/" + id + "/repo/pull?repo=api"
	writeIn(t, api, "wip.txt")
	if w := post(t, srv, url, "{}", testToken); w.Code != http.StatusConflict || hasUpstream(api) {
		t.Fatalf("dirty pull = %d, upstream=%v", w.Code, hasUpstream(api))
	}
	if err := os.Remove(filepath.Join(api, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	w := post(t, srv, url, "{}", testToken)
	if w.Code != http.StatusOK || !hasUpstream(api) {
		t.Fatalf("clean pull = %d, upstream=%v", w.Code, hasUpstream(api))
	}
	if !strings.Contains(w.Body.String(), `"output":"Brought in:\n`) {
		t.Errorf("a pull that brought commits lists them: %s", w.Body.String())
	}
	if w := post(t, srv, url, "{}", testToken); !strings.Contains(w.Body.String(), `"output":"Already up to date."`) {
		t.Errorf("a pull that brought nothing says so: %s", w.Body.String())
	}
}

func TestPullOutputListsWhatWasBroughtIn(t *testing.T) {
	got := pullOutput(false, []gitx.Commit{{SHA: "abc1234", Subject: "feat: x"}, {SHA: "def5678", Subject: "fix: y"}})
	if got != "Brought in:\nabc1234 feat: x\ndef5678 fix: y" {
		t.Errorf("pullOutput = %q", got)
	}
}

func TestCleanOutputNamesTheBranchesOrSaysThereWereNone(t *testing.T) {
	if got := cleanOutput(nil); got != "No merged branches to delete." {
		t.Errorf("cleanOutput(nil) = %q", got)
	}
	if got := cleanOutput([]string{"a", "b"}); got != "Deleted branch a\nDeleted branch b" {
		t.Errorf("cleanOutput = %q", got)
	}
}

func TestNestedMainBranchAndNewBranch(t *testing.T) {
	srv, ws, id, api := nestedFixture(t)
	url := "/v1/projects/" + id + "/repo/"
	if w := post(t, srv, url+"branch?repo=api", `{"branch":"feat/x"}`, testToken); w.Code != http.StatusOK {
		t.Fatalf("new branch = %d %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(mustOutput(t, "git", "-C", api, "branch", "--show-current")); got != "feat/x" {
		t.Fatalf("on %q", got)
	}
	writeIn(t, api, "wip.txt")
	if w := post(t, srv, url+"main?repo=api", "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("back to main with changes = %d, want 409", w.Code)
	}
	if w := post(t, srv, url+"stash?repo=api", "{}", testToken); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), "Saved in the stash as: motita: api") {
		t.Fatalf("stash = %d %s", w.Code, w.Body.String())
	}
	// main held by another checkout cannot be checked out here.
	other := filepath.Join(ws, "main-elsewhere")
	mustRun(t, "git", "-C", api, "worktree", "add", "-q", other, "main")
	if w := post(t, srv, url+"main?repo=api", "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("main held elsewhere = %d, want 409", w.Code)
	}
	if w := post(t, srv, url+"branch?repo=api", `{"branch":"main"}`, testToken); w.Code != http.StatusConflict {
		t.Errorf("a branch another checkout holds = %d, want 409", w.Code)
	}
	mustRun(t, "git", "-C", api, "worktree", "remove", "--force", other)
	if w := post(t, srv, url+"main?repo=api", "{}", testToken); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), "Switched to branch 'main'") {
		t.Errorf("back to main = %d %s", w.Code, w.Body.String())
	}
}

func TestNestedMainActionsNeedAMainBranch(t *testing.T) {
	srv, _, id, api := nestedFixture(t)
	mustRun(t, "git", "-C", api, "branch", "-m", "trunk")
	for _, action := range []string{"main", "clean"} {
		if w := post(t, srv, "/v1/projects/"+id+"/repo/"+action+"?repo=api", "{}", testToken); w.Code != http.StatusConflict {
			t.Errorf("%s without a main branch = %d, want 409", action, w.Code)
		}
	}
}

func TestNestedCleanDeletesOnlyWhatGitCallsMerged(t *testing.T) {
	srv, _, id, api := nestedFixture(t)
	url := "/v1/projects/" + id + "/repo/clean?repo=api"
	mustRun(t, "git", "-C", api, "branch", "done")
	w := post(t, srv, url, "{}", testToken)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":["done"]`) {
		t.Fatalf("clean = %d %s", w.Code, w.Body.String())
	}
	// A branch merged into main but not into the checked-out one is refused by `git branch -d`.
	mustRun(t, "git", "-C", api, "switch", "-q", "-c", "side")
	mustRun(t, "git", "-C", api, "switch", "-q", "main")
	writeIn(t, api, "next.txt")
	mustRun(t, "git", "-C", api, "add", "next.txt")
	mustRun(t, "git", "-C", api, "commit", "-qm", "next")
	mustRun(t, "git", "-C", api, "branch", "kept")
	mustRun(t, "git", "-C", api, "switch", "-q", "side")
	w = post(t, srv, url, "{}", testToken)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":[]`) {
		t.Errorf("an unmerged-into-HEAD branch must stay: %d %s", w.Code, w.Body.String())
	}
}

func TestNestedDiscardNeedsConfirmationAndHandlesAFailingGit(t *testing.T) {
	srv, _, id, api := nestedFixture(t)
	url := "/v1/projects/" + id + "/repo/discard?repo=api"
	writeIn(t, api, "wip.txt")
	if w := post(t, srv, url, `{"confirm":true}`, testToken); w.Code != http.StatusOK {
		t.Fatalf("discard = %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(api, "wip.txt")); err == nil {
		t.Error("the new file must be gone")
	}
	writeIn(t, api, "again.txt")
	if err := os.WriteFile(filepath.Join(api, ".git", "index.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if w := post(t, srv, url, `{"confirm":true}`, testToken); w.Code != http.StatusConflict {
		t.Errorf("a locked index = %d, want 409", w.Code)
	}
}

func mustOutput(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}
