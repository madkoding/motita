package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
)

// prSession is a gateway with a git host, a project, and a session in its own
// worktree. origin, when not empty, is added to the worktree's repository.
func prSession(t *testing.T, origin string) (*Server, *gitHost, SessionStatus) {
	t.Helper()
	srv, host := gitServer(t)
	withProjectsAndFake(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		mustRun(t, "git", "-C", ss.Workspace, "remote", "add", "origin", origin)
	}
	return srv, host, ss
}

func connectGitHub(t *testing.T, srv *Server) {
	t.Helper()
	if w := postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"github","token":"gho_x"}`); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
}

func TestPRNeedsAProjectSession(t *testing.T) {
	srv, _ := gitServer(t)
	if w := get(t, srv, sessionPath(srv, DefaultSession, "/pr"), testToken); w.Code != http.StatusConflict {
		t.Fatalf("a session with no project has no pull request: %d %s", w.Code, w.Body.String())
	}
}

func TestPRReasonsItCannotBeRead(t *testing.T) {
	srv, _, ss := prSession(t, "")
	if w := get(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken); w.Code != http.StatusConflict || decode(t, w)["code"] != errPRNoRemote {
		t.Errorf("no origin: %d %s", w.Code, w.Body.String())
	}

	srv, _, ss = prSession(t, "https://nowhere.example/o/r.git")
	if w := get(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken); w.Code != http.StatusConflict || decode(t, w)["code"] != errPRNoHost {
		t.Errorf("unknown host: %d %s", w.Code, w.Body.String())
	}

	srv, _, ss = prSession(t, "https://github.com/o/r.git")
	w := get(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken)
	if m := decode(t, w); w.Code != http.StatusConflict || m["code"] != errGitAuthRequired || m["service"] != "github" {
		t.Errorf("not connected: %d %s", w.Code, w.Body.String())
	}
}

func TestPRReportsTheCIOfTheBranch(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	path := sessionPath(srv, ss.ID, "/pr")

	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	if m := decode(t, get(t, srv, path, testToken)); m["state"] != "none" || m["pr"] != nil {
		t.Fatalf("a branch with no pull request: %v", m)
	}

	host.set("GET https://api.github.com/repos/o/r/pulls?state=open",
		`[{"number":7,"html_url":"https://github.com/o/r/pull/7","title":"feat: x","head":{"ref":"`+sessionBranch(ss.ID)+`"},"base":{"ref":"main"}}]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7", `{"head":{"sha":"abc123"}}`)
	host.set("GET https://api.github.com/repos/o/r/commits/abc123/check-runs", `{"check_runs":[{"id":1,"name":"test","status":"completed","conclusion":"failure"}]}`)
	host.set("GET https://api.github.com/repos/o/r/commits/abc123/status", `{"statuses":[]}`)
	m := decode(t, get(t, srv, path, testToken))
	ci, _ := m["ci"].(map[string]any)
	if m["state"] != "open" || ci["state"] != "failure" || ci["rev"] != "abc123" || m["pr"].(map[string]any)["number"] != float64(7) {
		t.Fatalf("an open pull request with a failing CI: %v", m)
	}

	// The refusal of a pull request already open is the same answer, whoever asks.
	w := postJSON(t, srv, path, testToken, `{}`)
	if m := decode(t, w); w.Code != http.StatusConflict || m["code"] != "pr_exists" {
		t.Errorf("opening a second pull request: %d %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, srv, path+"/fix", testToken, `{}`); w.Code != http.StatusAccepted {
		t.Errorf("fixing the CI: %d %s", w.Code, w.Body.String())
	}
}

func TestPRFixNeedsAPullRequest(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	if w := postJSON(t, srv, sessionPath(srv, ss.ID, "/pr/fix"), testToken, `{}`); w.Code != http.StatusConflict {
		t.Fatalf("nothing to fix: %d %s", w.Code, w.Body.String())
	}
}

func TestPRRefusesASessionWithNoWork(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	if w := postJSON(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken, `{}`); w.Code != http.StatusConflict {
		t.Fatalf("nothing to propose: %d %s", w.Code, w.Body.String())
	}
}
