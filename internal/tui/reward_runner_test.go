package tui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/reward"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/skills"
	taskpkg "github.com/madkoding/starlight/internal/task"
)

// The verdict path end to end, through the real runner: the user marks a turn, the value lands
// on the skills that turn read, and it survives a restart. This is where the pieces have to
// agree, and each of them is tested in its own package — what matters here is the wiring.

// rewardRunner builds a runner whose skills live in a temp directory, so the ledger is a real
// file that can be reopened.
func rewardRunner(t *testing.T, used map[string]int) (*AppRunner, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Skills.Dir = dir
	cfg.Agent.WorkspaceDir = t.TempDir()

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	// The planner is faked out: what is under test is the runner's own bookkeeping, and a real
	// planner would need a provider.
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return &fakeAgent{}
	}
	r.rememberUsage(used, "a task")
	return r, dir
}

func TestAVerdictLandsOnTheSkillsTheTurnRead(t *testing.T) {
	r, dir := rewardRunner(t, map[string]int{"flash": 2, "zephyr": 1})

	out := r.RecordVerdict(false, "el paso 2 usa el puerto equivocado")

	if !strings.Contains(out, "flash") || !strings.Contains(out, "zephyr") {
		t.Errorf("the report must name the skills the value landed on:\n%s", out)
	}
	if !strings.Contains(out, "el paso 2 usa el puerto equivocado") {
		t.Errorf("the report must quote the note back:\n%s", out)
	}
	// The ledger is a real file next to the library, so it can be reopened.
	if _, err := os.Stat(filepath.Join(dir, ".scores.json")); err != nil {
		t.Errorf("the verdict must have been saved: %v", err)
	}
}

func TestTheValueSurvivesANewRunner(t *testing.T) {
	// "Long term" is the whole request: a new process must find what the last one learned.
	r, dir := rewardRunner(t, map[string]int{"flash": 1})
	r.RecordVerdict(true, "")

	cfg := config.Default()
	cfg.Skills.Dir = dir
	again := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())

	report := again.RewardReport()
	if !strings.Contains(report, "flash") || !strings.Contains(report, "1 good") {
		t.Errorf("the verdict must survive a restart:\n%s", report)
	}
}

func TestAVerdictWithNoSkillSaysSoInsteadOfDoingNothing(t *testing.T) {
	// A turn that consulted no skill has nothing for the verdict to land on. Saying only "ok"
	// would make the feature look like it works when it has nothing to learn from.
	r, _ := rewardRunner(t, nil)

	out := r.RecordVerdict(true, "that was good")

	if !strings.Contains(strings.ToLower(out), "no skill") {
		t.Errorf("the report must say there was nothing to learn from:\n%s", out)
	}
	if !strings.Contains(out, "a task") {
		t.Errorf("it must name the turn it refers to, so the user sees why:\n%s", out)
	}
}

func TestABareBadVerdictSuggestsTheNote(t *testing.T) {
	// The note is what makes the verdict fixable, and a user who has just used /bad is exactly
	// the person to tell. This is the one place the interface can teach it.
	r, _ := rewardRunner(t, map[string]int{"flash": 1})

	out := r.RecordVerdict(false, "")

	if !strings.Contains(out, "/bad") {
		t.Errorf("a bare bad verdict must point at the note form:\n%s", out)
	}
}

func TestAGoodVerdictDoesNotNag(t *testing.T) {
	// Nothing was wrong, so there is nothing to fix and no hint to give.
	r, _ := rewardRunner(t, map[string]int{"flash": 1})
	out := r.RecordVerdict(true, "")
	if strings.Contains(out, "what was wrong") {
		t.Errorf("a good verdict must not ask for a complaint:\n%s", out)
	}
}

func TestTheReportShowsEvidenceNotJustTheNumber(t *testing.T) {
	// "value 0.4" from one verdict and from forty are different claims, and the report is read
	// to decide whether to trust a skill.
	r, _ := rewardRunner(t, map[string]int{"flash": 1})
	r.RecordVerdict(false, "step two is wrong")
	r.RecordVerdict(true, "fixed now")

	out := r.RewardReport()
	for _, want := range []string{"flash", "1 good", "1 bad", "step two is wrong", "fixed now"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report must show %q:\n%s", want, out)
		}
	}
}

func TestTheReportSaysSomethingWhenNothingWasMarked(t *testing.T) {
	// An empty report looks like a bug. It has to explain what would make it non-empty.
	r, _ := rewardRunner(t, nil)
	out := r.RewardReport()
	if !strings.Contains(out, "/good") || !strings.Contains(out, "/bad") {
		t.Errorf("an empty report must say how to fill it:\n%s", out)
	}
}

func TestAnUnreadableLedgerTurnsTheFeatureOff(t *testing.T) {
	// A corrupt ledger must not be silently replaced: the history is the whole value, and
	// starting empty without a word would hide that it was lost. The feature goes off for the
	// session, and the runner says so.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".scores.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Skills.Dir = dir
	// A logger writing to a file in the temp dir, so its records can be read back.
	logPath := filepath.Join(t.TempDir(), "t.log")
	lg, err := logx.New(logx.Options{Path: logPath, Level: logx.Warn})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	defer lg.Close()
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, lg)

	out := r.RecordVerdict(true, "x")
	if !strings.Contains(strings.ToLower(out), "unavailable") {
		t.Errorf("an unusable ledger must be reported, not hidden:\n%s", out)
	}
	logged, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logged), "ledger") {
		t.Errorf("the problem must reach the log:\n%s", logged)
	}
}

func TestTheLibraryGetsTheScorer(t *testing.T) {
	// The value is only useful if it reaches the search, which happens through the library's
	// Scorer. Without this wiring the ledger would be written and never read.
	r, _ := rewardRunner(t, map[string]int{"flash": 1})
	if lib := r.library(); lib.Scorer == nil {
		t.Error("the library must be given the ledger, or the value never affects a search")
	}
}

func TestRememberUsageReplacesInsteadOfAppending(t *testing.T) {
	// A verdict applies to ONE turn — the last one. Accumulating would credit skills from
	// earlier turns that had nothing to do with the verdict.
	r, _ := rewardRunner(t, map[string]int{"first": 1})
	r.rememberUsage(map[string]int{"second": 1}, "the newer task")

	out := r.RecordVerdict(true, "")
	if !strings.Contains(out, "second") {
		t.Errorf("the verdict must apply to the most recent turn:\n%s", out)
	}
	if strings.Contains(out, "first") {
		t.Errorf("a skill from an earlier turn must not be credited:\n%s", out)
	}
}

// wiringAgent implements the optional interfaces RunTask type-asserts for, so the wiring can
// be asserted without a real agent.
type wiringAgent struct {
	lib     *skills.Library
	reward  *reward.Ledger
	read    map[string]int
	runFail error
}

func (w *wiringAgent) SetLibrary(l *skills.Library) { w.lib = l }
func (w *wiringAgent) SetReward(r *reward.Ledger)   { w.reward = r }
func (w *wiringAgent) Consulted() map[string]int {
	if w.read == nil {
		return nil
	}
	return w.read
}
func (w *wiringAgent) SetTranscript([]agent.DialogueTurn) {}
func (w *wiringAgent) Transcript() []agent.DialogueTurn   { return nil }
func (w *wiringAgent) Run(context.Context) error          { return w.runFail }
func (w *wiringAgent) RunCommand(context.Context, string) (string, int, error) {
	return "", 0, nil
}

func TestTaskModeGetsTheSameLibraryAndLedgerAsPlan(t *testing.T) {
	// Both modes reach ONE shelf of procedures and one ledger of what has been learned about
	// them. Two libraries would be two bodies of knowledge that drift apart, and a verdict
	// would land on whichever mode happened to be used.
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Skills.Dir = dir
	cfg.Agent.WorkspaceDir = t.TempDir()

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	wired := &wiringAgent{}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return wired
	}

	if _, err := r.RunTask(context.Background(), "a task", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if wired.lib == nil {
		t.Fatal("Task mode must be given the library, or it cannot consult any procedure")
	}
	if wired.reward == nil {
		t.Error("Task mode must be given the ledger, or a verdict has nothing to land on")
	}
	// The same instance Plan mode uses: this is what sharing means.
	if wired.lib != r.library() {
		t.Error("both modes must use the same library instance")
	}
}

func TestTaskModeReportsWhatItReadForTheVerdict(t *testing.T) {
	// The attribution has to come back out of the run, or the user's next /bad lands on nothing.
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Skills.Dir = dir
	cfg.Agent.WorkspaceDir = t.TempDir()

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	wired := &wiringAgent{read: map[string]int{"flash": 2}}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return wired
	}

	if _, err := r.RunTask(context.Background(), "flash the board", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	out := r.RecordVerdict(true, "")
	if !strings.Contains(out, "flash") {
		t.Errorf("the verdict must land on what the task read:\n%s", out)
	}
}

func TestAFailedTaskStillReportsWhatItRead(t *testing.T) {
	// A turn that failed is exactly the one a user marks, so the attribution must survive the
	// error path.
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Skills.Dir = dir
	cfg.Agent.WorkspaceDir = t.TempDir()

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	wired := &wiringAgent{read: map[string]int{"broken-skill": 1}, runFail: errors.New("the run stopped")}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return wired
	}

	if _, err := r.RunTask(context.Background(), "doomed", func(string, ...any) {}); err == nil {
		t.Fatal("the error must still be reported")
	}
	out := r.RecordVerdict(false, "it broke")
	if !strings.Contains(out, "broken-skill") {
		t.Errorf("a failed turn must still report what it read:\n%s", out)
	}
}

func TestVerdictCallsThatFailAreReported(t *testing.T) {
	// Both failure paths must be reported rather than swallowed: a verdict the user gave and
	// the program dropped is worse than refusing it, because they will believe it landed.
	t.Run("the ledger cannot be written", func(t *testing.T) {
		r, dir := rewardRunner(t, map[string]int{"flash": 1})
		_ = r.rewardOrNil()
		// Turn the ledger's parent into something that cannot take a write.
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		out := r.RecordVerdict(true, "")
		if !strings.Contains(strings.ToLower(out), "could not be saved") {
			t.Errorf("a failed save must be reported:\n%s", out)
		}
	})

	t.Run("the ledger is unavailable", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".scores.json"), []byte("{broken"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default()
		cfg.Skills.Dir = dir
		r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
		if out := r.RewardReport(); !strings.Contains(strings.ToLower(out), "unavailable") {
			t.Errorf("the report must say the ledger is unusable:\n%s", out)
		}
	})
}

func TestTheReportMarksAFixedComplaint(t *testing.T) {
	// The report is read to see what still needs attention, so a complaint whose fix was
	// attempted has to be distinguishable from one that has not been touched.
	r, _ := rewardRunner(t, map[string]int{"flash": 1})
	r.RecordVerdict(false, "step two was wrong")
	led := r.rewardOrNil()
	led.Addressed("flash")

	out := r.RewardReport()
	if !strings.Contains(out, "[fixed]") {
		t.Errorf("a fixed complaint must be marked as fixed:\n%s", out)
	}
}

func TestTruncateLineFoldsAndBounds(t *testing.T) {
	// The one-line report has to stay one line, and it has to be bounded: a pasted document as
	// a note would otherwise fill the chat.
	if got := truncateLine("a\nb\tc", 50); got != "a b c" {
		t.Errorf("truncateLine must fold whitespace, got %q", got)
	}
	long := strings.Repeat("á", 200)
	got := truncateLine(long, 10)
	if len([]rune(got)) != 11 { // 10 runes plus the ellipsis
		t.Errorf("expected a bounded line, got %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a trimmed line must say so, got %q", got)
	}
	if got := truncateLine("short", 50); got != "short" {
		t.Errorf("a short line must pass through, got %q", got)
	}
}

func TestTheLedgerIsCreatedOnce(t *testing.T) {
	// rewardOrNil is called on every turn; it must build one ledger and keep it, or each turn
	// would re-read the file and the in-memory value would drift from the saved one.
	r, _ := rewardRunner(t, nil)
	first := r.rewardOrNil()
	if first == nil {
		t.Fatal("expected a ledger")
	}
	if second := r.rewardOrNil(); second != first {
		t.Error("the ledger must be built once and reused")
	}
}

func TestVerdictCallsIntoTheInterfaceReachTheRunner(t *testing.T) {
	// The commands and the runner, joined: this is the path a user actually takes.
	r, dir := rewardRunner(t, map[string]int{"flash": 3})
	_ = context.Background()

	out := r.RecordVerdict(false, "broken step")
	if !strings.Contains(out, "recorded") {
		t.Fatalf("a real verdict must report itself:\n%s", out)
	}
	report := r.RewardReport()
	if !strings.Contains(report, "flash") || !strings.Contains(report, "broken step") {
		t.Errorf("the report must show the verdict and its note:\n%s", report)
	}
	if _, err := os.Stat(filepath.Join(dir, ".scores.json")); err != nil {
		t.Errorf("the verdict must be on disk: %v", err)
	}
}
