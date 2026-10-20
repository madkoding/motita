package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/schedule"
)

const auditNoticeFormat = "The weekly audit of %s has a new report: look at the notes."

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
	if v.WeeklyAudit || v.AutoPR || v.MaxOpenPRs != 3 || !v.CanSave {
		t.Fatalf("default = %+v", v)
	}
	if v.Audit != nil || v.Fixes != nil {
		t.Fatalf("no routine before a switch is on: %+v", v)
	}

	v = decodeAutopilot(t, rig.srv, http.MethodPut, `{"weekly_audit":true,"max_open_prs":5}`)
	if !v.WeeklyAudit || v.AutoPR || v.MaxOpenPRs != 5 {
		t.Fatalf("audit on = %+v", v)
	}
	if v.Audit == nil || v.Fixes != nil || v.Audit.Running {
		t.Fatalf("audit routine = %+v, fixes = %+v", v.Audit, v.Fixes)
	}
	rec, _ := rig.srv.schedules.Load(autopilotAuditID)
	if rec == nil || !rec.Enabled || rec.Kind != schedule.KindPlan || rec.Title != autopilotAuditTitle {
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
	checkRoutinesPaused(t, rig.srv)
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
	plain := networkConfig(t, "llm:\n  provider: openai\n")
	noStore := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = plain })
	if v := decodeAutopilot(t, noStore, http.MethodPut, `{"auto_pr":true}`); !v.AutoPR || v.Fixes != nil {
		t.Errorf("no store: %+v", v)
	}

	broken := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, "llm: [\n") })
	if w := sendNetwork(t, broken, http.MethodGet, "/v1/autopilot", ""); w.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable file: %d", w.Code)
	}
	if code := postCode(t, broken, "/v1/autopilot/run", `{"what":"audit"}`); code != http.StatusInternalServerError {
		t.Errorf("an unreadable file on run: %d", code)
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
	if code := postCode(t, rig.srv, "/v1/autopilot/run", `{"what":"nope"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown routine: %d", code)
	}
	w := sendNetwork(t, rig.srv, http.MethodPost, "/v1/autopilot/run", `{"what":"audit"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", w.Code, w.Body.String())
	}
	rig.srv.scheduleWG.Wait()

	rec, _ := rig.srv.schedules.Load(autopilotAuditID)
	if rec.RunCount != 1 || rec.LastRun.IsZero() {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(rec.LastOutcome, "audited 1 of 1 projects: 2 new findings") {
		t.Fatalf("outcome = %q", rec.LastOutcome)
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
	rig.fs.plan = markRan(&ran)
	rig.fs.task = markRan(&ran)
	for _, id := range autopilotIDs {
		out, err := rig.srv.schedulerFirer()(context.Background(), schedule.Schedule{ID: id})
		if err != nil || !strings.Contains(out, "switched off") {
			t.Errorf("%s: %q, %v", id, out, err)
		}
		what := map[string]string{autopilotAuditID: "audit", autopilotFixesID: "fixes"}[id]
		if code := postCode(t, rig.srv, "/v1/autopilot/run", `{"what":"`+what+`"}`); code != http.StatusConflict {
			t.Errorf("%s by hand: %d", id, code)
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
	if code := postCode(t, rig.srv, "/v1/autopilot/run", `{"what":"audit"}`); code != http.StatusConflict {
		t.Errorf("by hand: %d", code)
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
	if code := postCode(t, rig.srv, "/v1/schedules/"+autopilotAuditID+"/run", ``); code != http.StatusConflict {
		t.Errorf("switched off: %d", code)
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
	_, err := rig.srv.auditProject(ctx, cfg, rig.project)
	if err == nil {
		t.Fatal("a failed audit must say so")
	}
	if !strings.Contains(err.Error(), "did not finish") {
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

// postCode sends a POST and answers the status.
func postCode(t *testing.T, srv *Server, path, body string) int {
	t.Helper()
	return sendNetwork(t, srv, http.MethodPost, path, body).Code
}

// markRan is an agent that does nothing but note that it was asked.
func markRan(ran *bool) func(context.Context, string, func(string, ...any)) (string, error) {
	return func(context.Context, string, func(string, ...any)) (string, error) {
		*ran = true
		return "", nil
	}
}

// checkRoutinesPaused checks both records stay, paused, once their switches are off.
func checkRoutinesPaused(t *testing.T, srv *Server) {
	t.Helper()
	for _, id := range autopilotIDs {
		if rec, _ := srv.schedules.Load(id); rec == nil || rec.Enabled {
			t.Errorf("%s must stay, paused: %+v", id, rec)
		}
	}
}
