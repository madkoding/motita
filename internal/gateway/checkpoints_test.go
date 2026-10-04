package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/logx"
)

// runTask sends a task over the wire and waits until the run has ended.
func runTask(t *testing.T, srv *Server, task string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"task": task})
	collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), string(body))
	waitForNoRun(t, srv)
}

func checkpointsOf(t *testing.T, srv *Server) []checkpointView {
	t.Helper()
	w := get(t, srv, sessionPath(srv, DefaultSession, "/checkpoints"), testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("checkpoints answered %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Checkpoints []checkpointView `json:"checkpoints"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Checkpoints
}

// A service whose transcript grows like the real one: the turn exists once the run has ended.
func recordingService(work func(progress func(string, ...any))) *fakeService {
	svc := &fakeService{}
	svc.task = func(_ context.Context, task string, progress func(string, ...any)) (string, error) {
		if work != nil {
			work(progress)
		}
		svc.transcript = append(svc.transcript, agent.DialogueTurn{User: task, Agent: "done " + task, Kind: "task"})
		return "done " + task, nil
	}
	return svc
}

func restoreReq(t *testing.T, srv *Server, turn, body string) *httptest.ResponseRecorder {
	t.Helper()
	return post(t, srv, sessionPath(srv, DefaultSession, "/checkpoints/"+turn+"/restore"), body, testToken)
}

// defaultConv is the default session's conversation.
func defaultConv(t *testing.T, srv *Server) *conversation {
	t.Helper()
	c, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("no default session")
	}
	return c
}

func TestEveryInputBecomesACheckpointWithItsSteps(t *testing.T) {
	svc := recordingService(func(p func(string, ...any)) {
		p("thinking: plan the work")
		p("running: go test ./...")
		p("output (exit 1):\nFAIL pkg")
		p("running: ls")
		p("output (exit 0):\na.go")
		p("live: half a thought")
		p("subtask 1/2 done")
	})
	srv := newTestServer(t, svc)
	runTask(t, srv, "first")
	runTask(t, srv, "second")

	cps := checkpointsOf(t, srv)
	if len(cps) != 2 || cps[0].Turn != 0 || cps[1].Turn != 1 || cps[1].Task != "second" {
		t.Fatalf("checkpoints = %+v", cps)
	}
	steps := cps[0].Steps
	if len(steps) != 4 {
		t.Fatalf("steps = %+v, want thought, 2 commands and a plain step (the live snapshot is not one)", steps)
	}
	if steps[0].Kind != "thought" || steps[1].Kind != "command" || steps[3].Kind != "step" {
		t.Errorf("kinds = %+v", steps)
	}
	if steps[1].Exit == nil || *steps[1].Exit != 1 || steps[1].Out != "FAIL pkg" {
		t.Errorf("the failing command = %+v", steps[1])
	}
	if steps[2].Exit == nil || *steps[2].Exit != 0 || steps[2].Out != "a.go" {
		t.Errorf("the passing command = %+v", steps[2])
	}
}

func TestStepsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	svc := recordingService(func(p func(string, ...any)) {
		p("running: make test")
		p("output (exit 0):\nok")
	})
	srv := newTestServer(t, svc, func(o *Options) { o.SessionDir = dir })
	runTask(t, srv, "build it")
	if err := srv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	again := newTestServer(t, &fakeService{}, func(o *Options) { o.SessionDir = dir })
	cps := checkpointsOf(t, again)
	if len(cps) != 1 || len(cps[0].Steps) != 1 || cps[0].Steps[0].Text != "running: make test" {
		t.Fatalf("after a restart: %+v", cps)
	}
}

func TestGoingBackTakesTheConversationAndTheFilesBack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	file := filepath.Join(dir, "f.txt")

	svc := &fakeService{}
	svc.task = func(_ context.Context, task string, _ func(string, ...any)) (string, error) {
		if err := os.WriteFile(file, []byte("after "+task+"\n"), 0o644); err != nil {
			t.Error(err)
		}
		svc.transcript = append(svc.transcript, agent.DialogueTurn{User: task, Agent: "ok", Kind: "task"})
		return "ok", nil
	}
	srv := newTestServer(t, svc)
	defaultConv(t, srv).workspace = dir

	runTask(t, srv, "one")
	runTask(t, srv, "two")
	if cps := checkpointsOf(t, srv); len(cps) != 2 || !cps[0].Files || !cps[1].Files {
		t.Fatalf("the checkpoints did not keep the files: %+v", cps)
	}

	w := restoreReq(t, srv, "1", `{"files":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("restore answered %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Task          string `json:"task"`
		FilesRestored bool   `json:"files_restored"`
		UndoKept      bool   `json:"undo_kept"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Task != "two" || !got.FilesRestored || !got.UndoKept {
		t.Errorf("answer = %+v", got)
	}
	if b, _ := os.ReadFile(file); string(b) != "after one\n" {
		t.Errorf("file = %q, want the state before input two", b)
	}
	if len(svc.transcript) != 1 || svc.transcript[0].User != "one" {
		t.Errorf("transcript = %+v, want only the first turn", svc.transcript)
	}
	if cps := checkpointsOf(t, srv); len(cps) != 1 || cps[0].Task != "one" {
		t.Errorf("checkpoints after going back = %+v", cps)
	}

	// The conversation alone: the files stay as they are.
	if err := os.WriteFile(file, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := restoreReq(t, srv, "0", `{}`); w.Code != http.StatusOK {
		t.Fatalf("restore answered %d: %s", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(file); string(b) != "mine\n" {
		t.Errorf("files:false touched the files: %q", b)
	}
	if len(svc.transcript) != 0 {
		t.Errorf("transcript = %+v, want empty", svc.transcript)
	}
}

func TestGoingBackWithFilesNeedsACheckpointThatHasThem(t *testing.T) {
	srv := newTestServer(t, recordingService(nil)) // no workspace: nothing to snapshot
	runTask(t, srv, "one")
	if cps := checkpointsOf(t, srv); len(cps) != 1 || cps[0].Files {
		t.Fatalf("checkpoints = %+v, want one without files", cps)
	}
	if w := restoreReq(t, srv, "0", `{"files":true}`); w.Code != http.StatusConflict {
		t.Errorf("files:true without a snapshot answered %d, want 409", w.Code)
	}
}

func TestRestoreRefusesWhatItCannotDo(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	svc := &fakeService{task: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		close(started)
		<-release
		return "ok", nil
	}}
	srv := newTestServer(t, svc)

	if w := restoreReq(t, srv, "abc", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("a bad number answered %d", w.Code)
	}
	if w := restoreReq(t, srv, "-1", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("a negative number answered %d", w.Code)
	}
	if w := restoreReq(t, srv, "0", `not json`); w.Code != http.StatusBadRequest {
		t.Errorf("a bad body answered %d", w.Code)
	}
	if w := restoreReq(t, srv, "7", `{}`); w.Code != http.StatusNotFound {
		t.Errorf("an unknown checkpoint answered %d", w.Code)
	}

	go func() {
		req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+sessionPath(srv, DefaultSession, "/task"), strings.NewReader(`{"task":"slow"}`))
		req.Header.Set("Authorization", "Bearer "+testToken)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	<-started
	if w := restoreReq(t, srv, "0", `{}`); w.Code != http.StatusConflict {
		t.Errorf("going back during a run answered %d, want 409", w.Code)
	}
	close(release)
	waitForNoRun(t, srv)
}

func TestRestoreReportsAGitFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	srv := newTestServer(t, recordingService(nil))
	c := defaultConv(t, srv)
	c.workspace = dir
	runTask(t, srv, "one")

	// The repository stops being its own root: the checkpoint is refused, not applied.
	c.workspace = filepath.Join(dir, "nowhere")
	w := restoreReq(t, srv, "0", `{"files":true}`)
	if w.Code != http.StatusConflict {
		t.Errorf("answered %d, want 409", w.Code)
	}
	// A different failure - the reset itself - is a server error.
	c.workspace = dir
	if err := os.WriteFile(filepath.Join(dir, ".git", "index.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if w := restoreReq(t, srv, "0", `{"files":true}`); w.Code != http.StatusInternalServerError {
		t.Errorf("a locked index answered %d, want 500", w.Code)
	}
}

func TestResetAndDeleteForgetTheCheckpoints(t *testing.T) {
	srv := newTestServer(t, recordingService(nil))
	runTask(t, srv, "one")
	if w := post(t, srv, sessionPath(srv, DefaultSession, "/reset"), "", testToken); w.Code != http.StatusNoContent {
		t.Fatalf("reset answered %d", w.Code)
	}
	if cps := checkpointsOf(t, srv); len(cps) != 0 {
		t.Errorf("after /reset: %+v", cps)
	}

	dir := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	withProjects(t, srv, t.TempDir())
	c, err := srv.createSession()
	if err != nil {
		t.Fatal(err)
	}
	c.workspace = dir
	c.svc = recordingService(nil)
	c.openCheckpoint(0, "x", "task", false)
	srv.takeSnapshot(c, 0)
	c.setSnapshot(0, "", "") // the ref is what matters below
	if out := gitOut(t, dir, "for-each-ref", "refs/motita/"); out == "" {
		t.Fatal("the snapshot left no reference")
	}
	c.setSnapshot(0, "h", "s")
	if w := del(t, srv, "/v1/sessions/"+c.id, testToken); w.Code != http.StatusNoContent && w.Code != http.StatusConflict {
		t.Fatalf("delete answered %d: %s", w.Code, w.Body.String())
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCheckpointBookkeeping(t *testing.T) {
	c := &conversation{}
	// A resumed pending turn keeps its record; anything else replaces the one that is there.
	if !c.openCheckpoint(0, "a", "task", false) {
		t.Fatal("a new checkpoint must ask for a snapshot")
	}
	if c.openCheckpoint(0, "a", "task", true) {
		t.Error("a resumed run must keep its snapshot")
	}
	if !c.openCheckpoint(0, "b", "task", false) {
		t.Error("a new input under a used number replaces it")
	}
	if got := c.checkpointViews(); len(got) != 1 || got[0].Task != "b" {
		t.Errorf("views = %+v", got)
	}
	// Nothing to file under, or nothing to file.
	c.recordStep(9, "running: x")
	c.recordStep(0, "")
	c.recordStep(0, "output (exit 0):\nstray") // a result with no command open
	c.recordStep(0, "output (exit x):\nbody")
	c.recordStep(0, "running: a")
	c.recordStep(0, "output (exit zzz):\nbody") // an unreadable code closes it as 0
	if s := c.checkpointViews()[0].Steps; len(s) != 1 || s[0].Exit == nil || *s[0].Exit != 0 || s[0].Out != "body" {
		t.Errorf("steps = %+v", s)
	}
	// The cap keeps the newest steps.
	for i := 0; i < maxStepsPerTurn+20; i++ {
		c.recordStep(0, "step")
	}
	if n := len(c.checkpointViews()[0].Steps); n != maxStepsPerTurn {
		t.Errorf("steps kept = %d, want %d", n, maxStepsPerTurn)
	}
	c.setSnapshot(9, "h", "s") // unknown turn: nothing happens
	// Records round-trip, with a nil step list becoming an empty one.
	recs := c.checkpointRecords()
	recs[0].Steps = nil
	c.setCheckpoints(recs)
	if s := c.checkpointViews()[0].Steps; s == nil || len(s) != 0 {
		t.Errorf("steps after reload = %#v", s)
	}
	if cut := c.cutCheckpointsFrom(0); len(cut) != 1 || len(c.checkpointViews()) != 0 {
		t.Errorf("cut = %+v", cut)
	}
}

func TestClipMiddleKeepsTheEndsAndWholeRunes(t *testing.T) {
	if got := clipMiddle("short", 100); got != "short" {
		t.Errorf("short text changed: %q", got)
	}
	long := strings.Repeat("é", 400) + "END" // spanish-fixture: a multi-byte rune, so a cut could land inside one
	got := clipMiddle(long, 101)
	// The marker itself is extra: the cap bounds what is KEPT, not the note saying what was cut.
	if len(got) > 101+60 || !strings.HasSuffix(got, "END") || !strings.Contains(got, "bytes omitted") {
		t.Errorf("clip = %q (%d bytes)", got, len(got))
	}
	if !strings.Contains(got, fmt.Sprintf("of %d bytes", len(long))) {
		t.Errorf("the marker must say how long the whole was: %q", got)
	}
	// Whatever the byte offset, the cut never lands inside a rune, at either end.
	for max := 40; max < 60; max++ {
		for _, r := range clipMiddle(strings.Repeat("é", 200), max) { // spanish-fixture: the same multi-byte rune
			if r == '\uFFFD' {
				t.Fatalf("max %d: a rune was cut in half", max)
			}
		}
	}
}

func TestTurnForContinuesAPendingTurn(t *testing.T) {
	turns := []agent.DialogueTurn{{User: "a"}, {User: "b", Pending: true}}
	if got := turnFor(turns, "b"); got != 1 {
		t.Errorf("a resumed run = turn %d, want 1", got)
	}
	if got := turnFor(turns, "c"); got != 2 {
		t.Errorf("a new input = turn %d, want 2", got)
	}
	if got := turnFor(nil, "a"); got != 0 {
		t.Errorf("first input = turn %d, want 0", got)
	}
}

func TestSnapshotFailureIsLoggedNotFatal(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "g.log")
	log, err := logx.New(logx.Options{Path: logPath, Level: logx.Info})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	// A lock on the index makes `git add -A` fail: the turn must still run.
	if err := os.WriteFile(filepath.Join(dir, ".git", "index.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, recordingService(nil), func(o *Options) { o.Log = log })
	defaultConv(t, srv).workspace = dir
	runTask(t, srv, "one")
	if cps := checkpointsOf(t, srv); len(cps) != 1 || cps[0].Files {
		t.Fatalf("checkpoints = %+v, want one without files", cps)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(logPath); strings.Contains(string(b), "could not be checkpointed") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the failure was not logged")
}

// A free-standing session has no workspace of its own: its files are in the configured one,
// and that is what a checkpoint keeps.
func TestAFreeStandingSessionCheckpointsTheConfiguredWorkspace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	svc := recordingService(nil)
	svc.cfg.Agent.WorkspaceDir = dir
	srv := newTestServer(t, svc)
	runTask(t, srv, "one")
	if cps := checkpointsOf(t, srv); len(cps) != 1 || !cps[0].Files {
		t.Fatalf("checkpoints = %+v, want one with the files", cps)
	}
	if out := gitOut(t, dir, "for-each-ref", "--format=%(refname)", "refs/motita/"); !strings.Contains(out, "/default/0") {
		t.Errorf("refs = %q", out)
	}
	// Going back drops the reference of the checkpoint it removes.
	if w := restoreReq(t, srv, "0", `{}`); w.Code != http.StatusOK {
		t.Fatalf("restore answered %d", w.Code)
	}
	if out := gitOut(t, dir, "for-each-ref", "refs/motita/checkpoints/"); out != "" {
		t.Errorf("a removed checkpoint left its reference: %q", out)
	}
}
