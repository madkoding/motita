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

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/schedule"
)

const (
	pullsOpen = "GET https://api.github.com/repos/o/r/pulls?state=open"
	pullsNew  = "POST https://api.github.com/repos/o/r/pulls"
	autoYAML  = "llm:\n  provider: openai\nautopilot:\n  weekly_audit: true\n  auto_pr: true\n  max_open_prs: 2\n"
)

// autopilotRig is a gateway with a config file, a schedule store, a note vault, a git host, and one
// project whose remote is github.com/o/r. The service answers with whatever the test sets.
type autopilotRig struct {
	srv     *Server
	host    *gitHost
	fs      *fakeService
	project Project
	pushed  []string
}

func newAutopilotRig(t *testing.T, yaml string, mutate ...func(*Options)) *autopilotRig {
	t.Helper()
	cfg := networkConfig(t, yaml)
	srv, host := gitServerWith(t, append([]func(*Options){func(o *Options) {
		o.ConfigPath = cfg
		o.ScheduleDir = t.TempDir()
		o.NotesDir = t.TempDir()
	}}, mutate...)...)
	fs := withProjectsAndFake(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	p.GitURL = "https://github.com/o/r.git"
	if err := srv.projects.save(*p); err != nil {
		t.Fatal(err)
	}
	connectGitHub(t, srv)
	host.set(pullsOpen, `[]`)
	host.set(pullsNew, `{"number":9,"html_url":"https://github.com/o/r/pull/9"}`)
	rig := &autopilotRig{srv: srv, host: host, fs: fs, project: *p}
	old := pushBranchFn
	pushBranchFn = func(_ context.Context, _ []string, dir, branch string) error {
		rig.pushed = append(rig.pushed, dir+" "+branch)
		return nil
	}
	t.Cleanup(func() { pushBranchFn = old })
	return rig
}

// agentWorks makes the fake agent commit a file in the worktree of the session it runs in.
func (r *autopilotRig) agentWorks(t *testing.T, work func(dir string)) {
	t.Helper()
	r.fs.task = func(_ context.Context, _ string, _ func(string, ...any)) (string, error) {
		for _, c := range r.srv.snapshot() {
			c.stateMu.Lock()
			mine := c.title == "Draft pull request"
			c.stateMu.Unlock()
			if mine {
				work(c.sessionRepoDir(context.Background(), ""))
			}
		}
		return "done", nil
	}
}

func commitFile(t *testing.T, dir, name, body, msg string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", dir, "add", name)
	mustRun(t, "git", "-C", dir, "commit", "-qm", msg)
}

func decodeAutopilot(t *testing.T, srv *Server, method, body string) autopilotView {
	t.Helper()
	w := sendNetwork(t, srv, method, "/v1/autopilot", body)
	var v autopilotView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %q: %v", w.Code, w.Body.String(), err)
	}
	return v
}

func TestTheAutopilotIsOffUntilTheSettingsTurnItOn(t *testing.T) {
	rig := newAutopilotRig(t, "llm:\n  provider: openai\n")
	v := decodeAutopilot(t, rig.srv, http.MethodGet, "")
	if v.WeeklyAudit || v.AutoPR || v.MaxOpenPRs != 3 || !v.CanSave || v.Audit != nil || v.Fixes != nil {
		t.Fatalf("default = %+v", v)
	}

	v = decodeAutopilot(t, rig.srv, http.MethodPut, `{"weekly_audit":true,"max_open_prs":5}`)
	if !v.WeeklyAudit || v.AutoPR || v.MaxOpenPRs != 5 || v.Audit == nil || v.Fixes != nil || v.Audit.Running {
		t.Fatalf("audit on = %+v", v)
	}
	rec, _ := rig.srv.schedules.Load(autopilotAuditID)
	if rec == nil || !rec.Enabled || rec.Kind != schedule.KindPlan || time.Duration(rec.Every) != autopilotEvery ||
		rec.Title != "Weekly audit" || rec.SessionID != schedule.DefaultSessionID {
		t.Fatalf("record = %+v", rec)
	}

	v = decodeAutopilot(t, rig.srv, http.MethodPut, `{"auto_pr":true}`)
	if !v.AutoPR || v.Fixes == nil {
		t.Fatalf("pull requests on = %+v", v)
	}
	if rec, _ = rig.srv.schedules.Load(autopilotFixesID); rec == nil || rec.Title != "Draft pull requests" {
		t.Fatalf("record = %+v", rec)
	}

	decodeAutopilot(t, rig.srv, http.MethodPut, `{"weekly_audit":false,"auto_pr":false}`)
	for _, id := range autopilotIDs {
		if rec, _ = rig.srv.schedules.Load(id); rec == nil || rec.Enabled {
			t.Errorf("%s must stay, paused: %+v", id, rec)
		}
	}
}

func TestTheAutopilotSettingsRefuseWhatTheyCannotSave(t *testing.T) {
	rig := newAutopilotRig(t, "llm:\n  provider: openai\n")
	for name, body := range map[string]string{
		"nothing": `{}`, "not json": `{`, "limit too high": `{"max_open_prs":11}`, "limit zero": `{"max_open_prs":0}`,
	} {
		if w := sendNetwork(t, rig.srv, http.MethodPut, "/v1/autopilot", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}

	noFile := newTestServer(t, &fakeService{})
	if w := sendNetwork(t, noFile, http.MethodPut, "/v1/autopilot", `{"auto_pr":true}`); w.Code != http.StatusConflict {
		t.Errorf("no file to save in: %d", w.Code)
	}
	if v := decodeAutopilot(t, noFile, http.MethodGet, ""); v.CanSave || v.WeeklyAudit {
		t.Errorf("no file: %+v", v)
	}
	// Without a schedule store a switch is saved and no routine is made.
	noStore := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, "llm:\n  provider: openai\n") })
	if v := decodeAutopilot(t, noStore, http.MethodPut, `{"auto_pr":true}`); !v.AutoPR || v.Fixes != nil {
		t.Errorf("no store: %+v", v)
	}

	broken := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, "llm: [\n") })
	if w := sendNetwork(t, broken, http.MethodGet, "/v1/autopilot", ""); w.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable file: %d", w.Code)
	}
	if w := sendNetwork(t, broken, http.MethodPost, "/v1/autopilot/run", `{"what":"audit"}`); w.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable file on run: %d", w.Code)
	}
	broken.syncAutopilot() // nothing to sync without a store, and no panic with a broken file
	broken.schedules, _ = schedule.Open(t.TempDir())
	broken.syncAutopilot()
}

func TestAnAuditRunByHandWritesTheReportAndCountsAsTheWeeksRun(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	decodeAutopilot(t, rig.srv, http.MethodPut, `{"weekly_audit":true}`)
	rig.fs.plan = func(context.Context, string, func(string, ...any)) (string, error) {
		return "Finding one\nFinding two\n\nNEW FINDINGS: 2", nil
	}
	if w := sendNetwork(t, rig.srv, http.MethodPost, "/v1/autopilot/run", `{"what":"nope"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("an unknown routine: %d", w.Code)
	}
	w := sendNetwork(t, rig.srv, http.MethodPost, "/v1/autopilot/run", `{"what":"audit"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", w.Code, w.Body.String())
	}
	rig.srv.scheduleWG.Wait()

	rec, _ := rig.srv.schedules.Load(autopilotAuditID)
	if rec.RunCount != 1 || rec.LastRun.IsZero() || !strings.Contains(rec.LastOutcome, "audited 1 of 1 projects: 2 new findings") {
		t.Fatalf("record = %+v", rec)
	}
	note := filepath.Join(rig.srv.opts.NotesDir, "Weekly audits", "repo", time.Now().Format("2006-01-02")+".md")
	body, err := os.ReadFile(note)
	if err != nil || !strings.Contains(string(body), "# repo") || !strings.Contains(string(body), "NEW FINDINGS: 2") {
		t.Fatalf("note = %q, %v", body, err)
	}

	// The second week reuses the project's audit session, so the agent remembers what it reported.
	if _, err := rig.srv.runAutopilot(context.Background(), autopilotAuditID); err != nil {
		t.Fatal(err)
	}
	audits := 0
	for _, c := range rig.srv.snapshot() {
		if c.title == autopilotAuditTitle {
			audits++
		}
	}
	if audits != 1 {
		t.Errorf("%d audit sessions, want one per project", audits)
	}
}

func TestARoutineSwitchedOffDoesNothingEvenWhenItsRecordIsEnabled(t *testing.T) {
	rig := newAutopilotRig(t, "llm:\n  provider: openai\n")
	ran := false
	rig.fs.plan = func(context.Context, string, func(string, ...any)) (string, error) { ran = true; return "", nil }
	rig.fs.task = func(context.Context, string, func(string, ...any)) (string, error) { ran = true; return "", nil }
	for _, id := range autopilotIDs {
		out, err := rig.srv.schedulerFirer()(context.Background(), schedule.Schedule{ID: id})
		if err != nil || !strings.Contains(out, "switched off") {
			t.Errorf("%s: %q, %v", id, out, err)
		}
		if w := sendNetwork(t, rig.srv, http.MethodPost, "/v1/autopilot/run",
			`{"what":"`+map[string]string{autopilotAuditID: "audit", autopilotFixesID: "fixes"}[id]+`"}`); w.Code != http.StatusConflict {
			t.Errorf("%s by hand: %d", id, w.Code)
		}
	}
	if ran {
		t.Error("the agent ran for a switch that is off")
	}
}

func TestARoutineCannotRunTwiceAtOnce(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	if !rig.srv.autopilotBegin(autopilotAuditID) {
		t.Fatal("the first start must succeed")
	}
	if v := decodeAutopilot(t, rig.srv, http.MethodPut, `{"weekly_audit":true}`); v.Audit == nil || !v.Audit.Running {
		t.Errorf("the page must see it running: %+v", v.Audit)
	}
	if _, err := rig.srv.runAutopilot(context.Background(), autopilotAuditID); !errors.Is(err, errAutopilotBusy) {
		t.Errorf("by the clock: %v", err)
	}
	if w := sendNetwork(t, rig.srv, http.MethodPost, "/v1/autopilot/run", `{"what":"audit"}`); w.Code != http.StatusConflict {
		t.Errorf("by hand: %d", w.Code)
	}
	rig.srv.autopilotEnd(autopilotAuditID)
	if rig.srv.autopilotRunning(autopilotAuditID) {
		t.Error("it must be free again")
	}
}

func TestTheScheduledTasksListRunsAnAutopilotRoutineThroughTheAutopilot(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	decodeAutopilot(t, rig.srv, http.MethodPut, `{"weekly_audit":true}`)
	rig.fs.plan = func(context.Context, string, func(string, ...any)) (string, error) { return "NEW FINDINGS: 0", nil }
	w := sendNetwork(t, rig.srv, http.MethodPost, "/v1/schedules/"+autopilotAuditID+"/run", ``)
	if w.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", w.Code, w.Body.String())
	}
	rig.srv.scheduleWG.Wait()
	if rec, _ := rig.srv.schedules.Load(autopilotAuditID); rec.RunCount != 1 {
		t.Errorf("record = %+v", rec)
	}
	decodeAutopilot(t, rig.srv, http.MethodPut, `{"weekly_audit":false}`)
	if w := sendNetwork(t, rig.srv, http.MethodPost, "/v1/schedules/"+autopilotAuditID+"/run", ``); w.Code != http.StatusConflict {
		t.Errorf("switched off: %d", w.Code)
	}
}

func TestAnAuditThatCannotRunOrFinishIsReportedAndTheRestGoOn(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	ctx := context.Background()
	cfg, _, _ := rig.srv.securityConfig()

	rig.fs.plan = func(context.Context, string, func(string, ...any)) (string, error) { return "no count here", nil }
	if n, err := rig.srv.auditProject(ctx, cfg, rig.project); err != nil || n != -1 {
		t.Errorf("a report without a count: %d, %v", n, err)
	}
	rig.fs.plan = func(context.Context, string, func(string, ...any)) (string, error) {
		return "", errors.New("model down")
	}
	if _, err := rig.srv.auditProject(ctx, cfg, rig.project); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Errorf("a failed audit: %v", err)
	}
	out := rig.srv.auditProjects(ctx, cfg)
	if !strings.Contains(out, "audited 0 of 1 projects") || !strings.Contains(out, "repo:") {
		t.Errorf("outcome = %q", out)
	}

	// A session someone is running in cannot take the audit.
	rig.fs.plan = nil
	c, _ := rig.srv.auditSessionOf(rig.project)
	if !c.takeRunSlot() {
		t.Fatal("could not take the slot")
	}
	if _, err := rig.srv.auditProject(ctx, cfg, rig.project); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Errorf("a busy session: %v", err)
	}
	c.releaseRunSlot()

	// At the ceiling of sessions a new audit session cannot be made.
	rig.srv.opts.MaxSessions = 1
	rig.srv.sessions = map[string]*conversation{"x": newConversation("x", &fakeService{})}
	if _, err := rig.srv.auditProject(ctx, cfg, rig.project); !errors.Is(err, ErrCeilingReached) {
		t.Errorf("the ceiling: %v", err)
	}

	// A stopped gateway ends the audit without waiting for the agent.
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	rig.srv.opts.MaxSessions = 0
	rig.srv.sessions = map[string]*conversation{}
	rig.fs.plan = func(c context.Context, _ string, _ func(string, ...any)) (string, error) {
		<-c.Done()
		return "", c.Err()
	}
	if _, err := rig.srv.auditProject(stopped, cfg, rig.project); err == nil {
		t.Error("a cancelled audit must say so")
	}
	if out := rig.srv.auditProjects(stopped, cfg); !strings.Contains(out, "audited 0 of 1") {
		t.Errorf("a cancelled pass audits nothing: %q", out)
	}
}

func TestAnAuditHasNowhereToWriteWithoutAVaultAndNothingToAuditWithoutProjects(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML, func(o *Options) { o.NotesDir = "" })
	rig.fs.plan = func(context.Context, string, func(string, ...any)) (string, error) { return "NEW FINDINGS: 0", nil }
	cfg, _, _ := rig.srv.securityConfig()
	if _, err := rig.srv.auditProject(context.Background(), cfg, rig.project); err != nil {
		t.Fatal(err)
	}
	if rig.srv.latestAudit(rig.project) != "" {
		t.Error("no vault, no earlier report")
	}

	bare := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, autoYAML) })
	if out := bare.auditProjects(context.Background(), cfg); out != "there are no projects to audit" {
		t.Errorf("%q", out)
	}
	if out := bare.fixProjects(context.Background(), cfg); out != "there are no projects" {
		t.Errorf("%q", out)
	}
}

func TestAReportThatCannotBeWrittenIsAWarning(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rig.srv.opts.NotesDir = blocked // a file where the vault folder should be
	rig.srv.writeAuditNote(rig.project, "report")
	if _, err := os.Stat(filepath.Join(blocked, "Weekly audits")); err == nil {
		t.Error("nothing can be written under a file")
	}
}

func TestTheLatestAuditIsQuotedToTheAgentCut(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	day := func(d string) string { return auditNoteFile(rig.project, mustDay(t, d)) }
	v := rig.srv.opts.NotesDir
	write := func(rel, body string) {
		full := filepath.Join(v, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if rig.srv.latestAudit(rig.project) != "" {
		t.Error("no report yet")
	}
	write(day("2026-10-06"), "old")
	write(day("2026-10-13"), strings.Repeat("x", autopilotReportBytes+50))
	write("Weekly audits/other/2026-10-20.md", "another project's")
	got := rig.srv.latestAudit(rig.project)
	if !strings.HasPrefix(got, "xxx") || !strings.HasSuffix(got, "…") || len(got) > autopilotReportBytes+10 {
		t.Errorf("got %d bytes", len(got))
	}
}

func mustDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTheTextsTheAgentIsGivenCarryTheRules(t *testing.T) {
	for _, want := range []string{"READ-ONLY", "NEW FINDINGS: <number>", "Spanish"} {
		if !strings.Contains(auditTask("Spanish"), want) {
			t.Errorf("the audit task lacks %q", want)
		}
	}
	task := fixTask("motita/auto-s1", "earlier report")
	for _, want := range []string{"ONE small", "Do not push", "do not merge", "NOTHING TO FIX", "motita/auto-s1",
		"earlier report", ".github/", "at most 8 files and 200 changed lines"} {
		if !strings.Contains(task, want) {
			t.Errorf("the fix task lacks %q", want)
		}
	}
	if strings.Contains(fixTask("b", ""), "latest weekly audit of this project said") {
		t.Error("no earlier report, nothing quoted")
	}
	if languageOf(config.Config{UI: config.UI{Language: "es"}}) != "Spanish" ||
		languageOf(config.Config{UI: config.UI{Language: "en"}}) != "English" {
		t.Error("the language of the report follows the interface language")
	}
}

func TestDeniedPaths(t *testing.T) {
	for p, want := range map[string]bool{
		".github/workflows/ci.yml": true, "sub/.github/x.yml": true, "go.mod": true, "web/package.json": true,
		"web/package-lock.json": true, "Dockerfile": true, "docker/Dockerfile.dev": true, ".env": true,
		".gitlab-ci.yml": true, "a\\.githooks\\pre-push": true, "README.md": false, "internal/x.go": false,
		"docs/github/notes.md": false, "x_test.go": false,
	} {
		if deniedPath(p) != want {
			t.Errorf("deniedPath(%q) = %v", p, !want)
		}
	}
}

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
	var create string
	for _, s := range rig.host.sent {
		if strings.HasPrefix(s, pullsNew) {
			create = s
		}
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(strings.TrimPrefix(create, pullsNew+" ")), &body)
	if body["draft"] != true || body["base"] != "main" || !strings.HasPrefix(body["head"].(string), autopilotBranchPrefix) ||
		body["title"] != "docs(readme): fix a typo in the install steps" ||
		!strings.Contains(body["body"].(string), "never merges") {
		t.Fatalf("the pull request = %v", body)
	}
	for _, s := range rig.host.seen {
		if strings.Contains(s, "/merge") {
			t.Errorf("the autopilot asked the host to merge: %s", s)
		}
	}
	// The work is on a branch of its own: the project's main checkout is untouched.
	if out, _ := exec.Command("git", "-C", rig.project.Dir, "log", "--oneline").Output(); strings.Contains(string(out), "typo") {
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
		"a denied file": {func(dir string) { commitFile(t, dir, ".github/workflows/ci.yml", "x\n", "ci: change it") }, "never changes"},
		"not semantic":  {func(dir string) { commitFile(t, dir, "a.md", "a\n", "tidy up a bit") }, "not semantic"},
		"another branch": {func(dir string) {
			mustRun(t, "git", "-C", dir, "switch", "-c", "elsewhere")
			commitFile(t, dir, "a.md", "a\n", "docs: add a")
		}, "not on the branch"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newAutopilotRig(t, autoYAML)
			rig.agentWorks(t, c.work)
			out, err := rig.srv.runAutopilot(context.Background(), autopilotFixesID)
			if err != nil || !strings.Contains(out, "opened 0 draft pull requests") || !strings.Contains(out, c.want) {
				t.Fatalf("outcome = %q, %v", out, err)
			}
			if len(rig.pushed) != 0 {
				t.Errorf("pushed %v", rig.pushed)
			}
			for _, s := range rig.host.seen {
				if strings.HasPrefix(s, pullsNew) {
					t.Errorf("a pull request was opened: %s", s)
				}
			}
		})
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
	rig.fs.task = func(context.Context, string, func(string, ...any)) (string, error) { ran = true; return "", nil }
	out, err := rig.srv.runAutopilot(context.Background(), autopilotFixesID)
	if err != nil || !strings.Contains(out, "limit of 2 open draft pull requests reached") {
		t.Fatalf("outcome = %q, %v", out, err)
	}
	if ran || len(rig.srv.snapshot()) > 1 {
		t.Error("the agent must not even start at the limit")
	}

	// One below the limit allows exactly one more, even with two projects on the repository.
	rig.host.set(pullsOpen, `[{"number":1,"html_url":"u1","title":"a","head":{"ref":"`+autopilotBranchPrefix+`a"},"base":{"ref":"main"}}]`)
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
	if err != nil || !strings.Contains(out, "opened 1 draft pull requests") || !strings.Contains(out, "limit of 2") || n != 1 {
		t.Fatalf("outcome = %q, %v (agent ran %d times)", out, err, n)
	}
}

func TestEachProjectThatCannotGetADraftPullRequestSaysWhy(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	ctx := context.Background()
	cfg, _, _ := rig.srv.securityConfig()
	add := func(name, remote string, git bool) {
		dir := filepath.Join(rig.srv.opts.WorkspaceDir, name)
		if git {
			gitInit(t, dir)
			if remote != "" {
				mustRun(t, "git", "-C", dir, "remote", "add", "origin", remote)
			}
		} else if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := rig.srv.projects.save(Project{ID: "p-" + name, Title: name, Dir: dir, Created: time.Now()}); err != nil {
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
	srv.notifyAutopilot("motita-test-tag", "Weekly audit", "The weekly audit of %s has a new report: look at the notes.", "repo")
	srv.pushWG.Wait()
	if tr.count() != 1 {
		t.Fatalf("sent %d", tr.count())
	}
	tr.err = errors.New("offline")
	srv.notifyAutopilot("motita-test-tag", "Weekly audit", "The weekly audit of %s has a new report: look at the notes.", "repo")
	srv.pushWG.Wait()
	// With no push service nothing is sent and nothing fails.
	newTestServer(t, &fakeService{}).notifyAutopilot("t", "Weekly audit", "x")
}

func TestARoutineRecordThatCannotBeUpdatedIsAWarning(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	// A record the store cannot parse: updating it fails, and the sync says so instead of dying.
	if err := os.WriteFile(rig.srv.schedules.Path(autopilotAuditID), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	rig.srv.syncAutopilot()
	if rig.srv.routineOf(autopilotAuditID) != nil {
		t.Error("a broken record is no routine")
	}
	// A store that cannot write cannot create the record.
	_ = os.Remove(rig.srv.schedules.Path(autopilotAuditID))
	if err := os.Chmod(rig.srv.opts.ScheduleDir, 0o500); err == nil {
		defer os.Chmod(rig.srv.opts.ScheduleDir, 0o700)
		if os.Geteuid() != 0 {
			rig.srv.syncAutopilot()
		}
	}
	// And a run by hand whose record is gone is only logged.
	rig.srv.recordAutopilot("autopilot-gone", "done")
	(&Server{}).recordAutopilot(autopilotAuditID, "done")
	if routine := (&Server{}).routineOf(autopilotAuditID); routine != nil {
		t.Error("no store, no routine")
	}
}

var _ = gitforge.KindGitHub

func TestARunByHandNeedsAReadableRequestAndConfig(t *testing.T) {
	rig := newAutopilotRig(t, autoYAML)
	if w := sendNetwork(t, rig.srv, http.MethodPost, "/v1/autopilot/run", `{`); w.Code != http.StatusBadRequest {
		t.Errorf("not json: %d", w.Code)
	}
	broken := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, "llm: [\n") })
	if _, err := broken.runAutopilot(context.Background(), autopilotAuditID); err == nil {
		t.Error("an unreadable configuration is an error, not a silent skip")
	}
}

func TestARoutineRecordThatCannotBeCreatedIsAWarning(t *testing.T) {
	rig := newAutopilotRig(t, "llm:\n  provider: openai\n")
	if err := os.RemoveAll(rig.srv.opts.ScheduleDir); err != nil {
		t.Fatal(err)
	}
	rig.srv.syncRoutine(autopilotAuditID, true)
	if rig.srv.routineOf(autopilotAuditID) != nil {
		t.Error("nothing could be written")
	}
}
