package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestADraftPullRequestIsOpenedForASmallSafeChange(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	rig.agentWorks(t, func(dir string) {
		commitFile(t, dir, "docs/typo.md", "fixed\n", "docs(readme): fix a typo in the install steps")
	})
	out, err := rig.srv.runAutopilot(context.Background(), autopilotFixesID)
	if err != nil || out != "opened 1 draft pull requests: https://github.com/o/r/pull/9" {
		t.Fatalf("outcome = %q, %v", out, err)
	}
	if len(rig.pushed) != 1 || !strings.Contains(rig.pushed[0], " "+autopilotBranchPrefix) {
		t.Fatalf("pushed = %v", rig.pushed)
	}
	body := createdPullRequest(rig)
	if body["draft"] != true || body["base"] != "main" {
		t.Fatalf("the pull request must be a draft into main: %v", body)
	}
	if !strings.HasPrefix(body["head"].(string), autopilotBranchPrefix) {
		t.Fatalf("the branch is not the autopilot's: %v", body["head"])
	}
	if body["title"] != "docs(readme): fix a typo in the install steps" {
		t.Fatalf("title = %v", body["title"])
	}
	if !strings.Contains(body["body"].(string), "never merges") {
		t.Fatalf("body = %v", body["body"])
	}
	assertNeverMerged(t, rig)
	// The work is on a branch of its own: the project's main checkout is untouched.
	history, _ := exec.Command("git", "-C", rig.project.Dir, "log", "--oneline").Output()
	if strings.Contains(string(history), "typo") {
		t.Error("the change reached the project's own branch")
	}
}

func TestNothingIsPublishedForAChangeThatBreaksTheRules(t *testing.T) {
	long := strings.Repeat("line\n", autopilotMaxLines+1)
	cases := map[string]struct {
		work func(dir string)
		want string
	}{
		"it made no commit": {func(string) {}, "made no commit"},
		"uncommitted": {func(dir string) {
			commitFile(t, dir, "a.md", "a\n", "docs: add a")
			_ = os.WriteFile(filepath.Join(dir, "left.md"), []byte("x"), 0o644)
		}, "not committed"},
		"too many lines": {func(dir string) { commitFile(t, dir, "big.md", long, "docs: add a lot") }, "too big"},
		"too many files": {func(dir string) {
			for i := 0; i <= autopilotMaxFiles; i++ {
				commitFile(t, dir, "f"+string(rune('a'+i))+".md", "x\n", "docs: add a file")
			}
		}, "too big"},
		"a denied file": {func(dir string) {
			commitFile(t, dir, ".github/workflows/ci.yml", "x\n", "ci: change it")
		}, "never changes"},
		"not semantic": {func(dir string) { commitFile(t, dir, "a.md", "a\n", "tidy up a bit") }, "not semantic"},
		"another branch": {func(dir string) {
			mustRun(t, "git", "-C", dir, "switch", "-c", "elsewhere")
			commitFile(t, dir, "a.md", "a\n", "docs: add a")
		}, "not on the branch"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { checkNothingPublished(t, c.work, c.want) })
	}
}

// checkNothingPublished runs the fix routine with an agent that does work, and checks that the
// outcome says want and that neither a push nor a pull request happened.
func checkNothingPublished(t *testing.T, work func(dir string), want string) {
	t.Helper()
	rig := newAutopilotRig(t, autoYAML)
	rig.agentWorks(t, work)
	out, err := rig.srv.runAutopilot(context.Background(), autopilotFixesID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "opened 0 draft pull requests") || !strings.Contains(out, want) {
		t.Fatalf("outcome = %q", out)
	}
	if len(rig.pushed) != 0 {
		t.Errorf("pushed %v", rig.pushed)
	}
	for _, s := range rig.host.seen {
		if strings.HasPrefix(s, pullsNew) {
			t.Errorf("a pull request was opened: %s", s)
		}
	}
}

func TestTheAgentSayingThereIsNothingToFixOpensNothing(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	rig.fs.task = func(context.Context, string, func(string, ...any)) (string, error) { return "NOTHING TO FIX", nil }
	out, err := rig.srv.runAutopilot(context.Background(), autopilotFixesID)
	if err != nil || out != "opened 0 draft pull requests" {
		t.Fatalf("outcome = %q, %v", out, err)
	}
}

func TestNoNewPullRequestIsOpenedAtTheLimit(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	rig.host.set(pullsOpen, `[
		{"number":1,"html_url":"u1","title":"a","head":{"ref":"`+autopilotBranchPrefix+`a"},"base":{"ref":"main"}},
		{"number":2,"html_url":"u2","title":"b","head":{"ref":"`+autopilotBranchPrefix+`b"},"base":{"ref":"main"}},
		{"number":3,"html_url":"u3","title":"c","head":{"ref":"someone-elses"},"base":{"ref":"main"}}]`)
	ran := false
	rig.fs.task = markRan(&ran)
	out, err := rig.srv.runAutopilot(context.Background(), autopilotFixesID)
	if err != nil || !strings.Contains(out, "limit of 2 open draft pull requests reached") {
		t.Fatalf("outcome = %q, %v", out, err)
	}
	if ran || len(rig.srv.snapshot()) > 1 {
		t.Error("the agent must not even start at the limit")
	}

	// One below the limit allows exactly one more, even with two projects on the repository.
	ref := `{"ref":"` + autopilotBranchPrefix + `a"}`
	one := `[{"number":1,"html_url":"u1","title":"a","head":` + ref + `,"base":{"ref":"main"}}]`
	rig.host.set(pullsOpen, one)
	second := makeProject(t, rig.srv, "repo2")
	p2 := rig.srv.projectOf(second)
	p2.GitURL = "https://github.com/o/r.git"
	if err := rig.srv.projects.save(*p2); err != nil {
		t.Fatal(err)
	}
	n := 0
	rig.agentWorks(t, func(dir string) {
		n++
		commitFile(t, dir, "docs/a.md", "a\n", "docs: add a note")
	})
	out, err = rig.srv.runAutopilot(context.Background(), autopilotFixesID)
	if err != nil || n != 1 {
		t.Fatalf("outcome = %q, %v (agent ran %d times)", out, err, n)
	}
	if !strings.Contains(out, "opened 1 draft pull requests") || !strings.Contains(out, "limit of 2") {
		t.Fatalf("outcome = %q", out)
	}
}

func TestEachProjectThatCannotGetADraftPullRequestSaysWhy(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	ctx := context.Background()
	cfg, _, _ := rig.srv.securityConfig()
	add := func(name, remote string, git bool) {
		dir := filepath.Join(rig.srv.opts.WorkspaceDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if git {
			gitInit(t, dir)
		}
		if remote != "" {
			mustRun(t, "git", "-C", dir, "remote", "add", "origin", remote)
		}
		p := Project{ID: "p-" + name, Title: name, Dir: dir, Created: time.Now()}
		if err := rig.srv.projects.save(p); err != nil {
			t.Fatal(err)
		}
	}
	add("plain", "", false)
	add("noremote", "", true)
	add("unknownhost", "https://nowhere.example/o/r.git", true)
	add("gitlab", "https://gitlab.com/o/r.git", true)
	// The seeded project is not what this test is about: its repository answers an error.
	rig.host.set(pullsOpen, `STATUS 500 {"message":"down"}`)
	out := rig.srv.fixProjects(ctx, cfg)
	for _, want := range []string{
		"plain: it is not a git repository", "noremote: it has no remote named origin",
		"unknownhost: nowhere.example is not a git host", "gitlab: GitLab is not connected", "repo:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("outcome lacks %q: %q", want, out)
		}
	}

	noStore := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, autoYAML) })
	if _, err := noStore.forgeOf(ctx, rig.project); err == nil {
		t.Error("no place for git logins")
	}
}

func TestADraftPullRequestThatFailsHalfwayIsReportedNotPublished(t *testing.T) {
	good := func(dir string) { commitFile(t, dir, "docs/a.md", "a\n", "docs: add a note") }
	t.Run("the push is refused", func(t *testing.T) {
		rig := newAutopilotRig(t, autoYAML)
		pushBranchFn = func(context.Context, []string, string, string) error { return errors.New("denied") }
		rig.agentWorks(t, good)
		out, _ := rig.srv.runAutopilot(context.Background(), autopilotFixesID)
		if !strings.Contains(out, "could not be pushed: denied") {
			t.Errorf("outcome = %q", out)
		}
	})
	t.Run("the host refuses the pull request", func(t *testing.T) {
		rig := newAutopilotRig(t, autoYAML)
		rig.host.set(pullsNew, `STATUS 500 {"message":"down"}`)
		rig.agentWorks(t, good)
		out, _ := rig.srv.runAutopilot(context.Background(), autopilotFixesID)
		if !strings.Contains(out, "could not be opened") {
			t.Errorf("outcome = %q", out)
		}
	})
	t.Run("the agent fails", func(t *testing.T) {
		rig := newAutopilotRig(t, autoYAML)
		rig.fs.task = func(context.Context, string, func(string, ...any)) (string, error) {
			return "", errors.New("model down")
		}
		out, _ := rig.srv.runAutopilot(context.Background(), autopilotFixesID)
		if !strings.Contains(out, "did not finish") {
			t.Errorf("outcome = %q", out)
		}
	})
	t.Run("the gateway is full", func(t *testing.T) {
		rig := newAutopilotRig(t, autoYAML)
		rig.srv.opts.MaxSessions = 1
		rig.srv.sessions = map[string]*conversation{"x": newConversation("x", &fakeService{})}
		out, _ := rig.srv.runAutopilot(context.Background(), autopilotFixesID)
		if !strings.Contains(out, "ceiling") {
			t.Errorf("outcome = %q", out)
		}
	})
	t.Run("the gateway stops", func(t *testing.T) {
		rig := newAutopilotRig(t, autoYAML)
		cfg, _, _ := rig.srv.securityConfig()
		ctx, cancel := context.WithCancel(context.Background())
		rig.fs.task = func(c context.Context, _ string, _ func(string, ...any)) (string, error) {
			cancel()
			<-c.Done()
			return "", c.Err()
		}
		tgt, err := rig.srv.forgeOf(ctx, rig.project)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rig.srv.fixProject(ctx, rig.project, tgt); err == nil {
			t.Error("a stopped gateway must end the change")
		}
		if out := rig.srv.fixProjects(ctx, cfg); !strings.HasPrefix(out, "opened 0") {
			t.Errorf("outcome = %q", out)
		}
	})
}

func TestTheWorktreeIsRequiredAndAnUnbranchableOneIsRefused(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	ctx := context.Background()
	tgt, _ := rig.srv.forgeOf(ctx, rig.project)

	// The project's folder stops being a repository after the forge was resolved: no worktree can be made.
	if err := os.RemoveAll(filepath.Join(rig.project.Dir, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.srv.fixProject(ctx, rig.project, tgt); err == nil {
		t.Error("without a worktree the autopilot must not work in the project's folder")
	}
}

func TestVetChangeNeverLetsAnUnnamedBranchThrough(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	ctx := context.Background()
	dir := rig.project.Dir
	for _, branch := range []string{"main", "feature"} {
		if _, _, err := rig.srv.vetChange(ctx, dir, "main", branch, ""); err == nil ||
			!strings.Contains(err.Error(), "branch of its own") {
			t.Errorf("%s: %v", branch, err)
		}
	}
	if _, _, err := rig.srv.vetChange(ctx, dir, "main", autopilotBranchPrefix+"x", ""); err == nil ||
		!strings.Contains(err.Error(), "not on the branch") {
		t.Errorf("a branch that is not checked out: %v", err)
	}
	mustRun(t, "git", "-C", dir, "switch", "-c", autopilotBranchPrefix+"y")
	commitFile(t, dir, "a.md", "a\n", "docs: add a")
	if _, _, err := rig.srv.vetChange(ctx, dir, "main", autopilotBranchPrefix+"y", "not-a-rev"); err == nil ||
		!strings.Contains(err.Error(), "measured") {
		t.Errorf("a change that cannot be measured: %v", err)
	}
}

func TestOnlyTheNamedBranchIsPushed(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "origin.git")
	mustRun(t, "git", "init", "-q", "--bare", "-b", "main", origin)
	work := filepath.Join(t.TempDir(), "work")
	gitInit(t, work)
	mustRun(t, "git", "-C", work, "remote", "add", "origin", origin)
	mustRun(t, "git", "-C", work, "push", "-q", "origin", "main")
	mustRun(t, "git", "-C", work, "switch", "-qc", autopilotBranchPrefix+"one")
	commitFile(t, work, "a.md", "a\n", "docs: add a")
	mustRun(t, "git", "-C", work, "switch", "-q", "main")
	commitFile(t, work, "local.md", "x\n", "docs: stays local")
	mustRun(t, "git", "-C", work, "switch", "-q", autopilotBranchPrefix+"one")

	if err := gitPushBranch(context.Background(), os.Environ(), work, autopilotBranchPrefix+"one"); err != nil {
		t.Fatal(err)
	}
	remote, _ := exec.Command("git", "-C", origin, "branch", "--format=%(refname:short)").Output()
	if got := strings.Fields(string(remote)); len(got) != 2 {
		t.Fatalf("the origin has %v: main and the one branch, nothing else", got)
	}
	mainOnOrigin, _ := exec.Command("git", "-C", origin, "log", "--format=%s", "main").Output()
	if strings.Contains(string(mainOnOrigin), "stays local") {
		t.Error("main moved on the origin")
	}
	if err := gitPushBranch(context.Background(), os.Environ(), work, "does-not-exist"); err == nil {
		t.Error("a branch that does not exist cannot be pushed")
	}
}

func TestAutopilotNotificationsReachTheSubscribedPhones(t *testing.T) {
	srv, tr, _ := newPushServer(t)
	_ = pushCall(t, srv, http.MethodPost, "/v1/push/subscriptions", subscriptionJSON(t, testEndpoint, "es"))
	srv.notifyAutopilot("motita-test-tag", "Weekly audit", auditNoticeFormat, "repo")
	srv.pushWG.Wait()
	if tr.count() != 1 {
		t.Fatalf("sent %d", tr.count())
	}
	tr.err = errors.New("offline")
	srv.notifyAutopilot("motita-test-tag", "Weekly audit", auditNoticeFormat, "repo")
	srv.pushWG.Wait()
	// With no push service nothing is sent and nothing fails.
	newTestServer(t, &fakeService{}).notifyAutopilot("t", "Weekly audit", "x")
}

// createdPullRequest is the body of the pull request the autopilot asked the host to create.
func createdPullRequest(rig *autopilotRig) map[string]any {
	var create string
	for _, s := range rig.host.sent {
		if strings.HasPrefix(s, pullsNew) {
			create = s
		}
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(strings.TrimPrefix(create, pullsNew+" ")), &body)
	return body
}

// assertNeverMerged checks the autopilot never asked the host to merge anything.
func assertNeverMerged(t *testing.T, rig *autopilotRig) {
	t.Helper()
	for _, s := range rig.host.seen {
		if strings.Contains(s, "/merge") {
			t.Errorf("the autopilot asked the host to merge: %s", s)
		}
	}
}
