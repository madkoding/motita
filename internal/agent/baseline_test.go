package agent

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/config"
)

// inRepo turns the run's workspace into a repository with one commit, which is what a session
// worktree is, and what a baseline needs.
func inRepo(t *testing.T) func(*config.Config) {
	t.Helper()
	return func(c *config.Config) {
		dir := c.Agent.WorkspaceDir
		for _, args := range [][]string{
			{"init", "-q", "-b", "main"},
			{"config", "user.email", "test@example.com"},
			{"config", "user.name", "Test"},
			{"config", "commit.gpgsign", "false"},
		} {
			repoCmd(t, dir, args...)
		}
		if err := os.WriteFile(dir+"/README.md", []byte("project\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		repoCmd(t, dir, "add", "README.md")
		repoCmd(t, dir, "commit", "-qm", "init")
		c.Agent.MaxRetries = 3
		c.Anchor.Baseline = true
	}
}

// projectGate makes the run's anchor the PROJECT'S OWN gate, the only kind the baseline compares:
// kind auto, reading the given lines from .motita/anchor, committed like a real project's file.
func projectGate(t *testing.T, lines ...string) func(*config.Config) {
	t.Helper()
	return func(c *config.Config) {
		inRepo(t)(c)
		dir := c.Agent.WorkspaceDir
		if err := os.MkdirAll(dir+"/.motita", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir+"/.motita/anchor", []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		repoCmd(t, dir, "add", ".motita/anchor")
		repoCmd(t, dir, "commit", "-qm", "gate")
		c.Anchor = config.Anchor{Kind: "auto", Timeout: 10 * time.Second, Baseline: true}
	}
}

// idleServer is an LLM nobody asks: these tests call the baseline directly.
func idleServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer((&scriptServer{}).handler(t))
	t.Cleanup(srv.Close)
	return srv
}

func repoCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestACheckThatFailedBeforeTheRunDoesNotBlockIt replays the real session: the project's gate
// fails on the untouched commit, so no change could ever pass it. The claim is accepted, and the
// verdict says - in words - which check was already failing; it is never a plain green.
func TestACheckThatFailedBeforeTheRunDoesNotBlockIt(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		return step(true, "echo feature > feature.txt")
	}}
	_, result, err := runScript(t, s, "unused", projectGate(t, "echo 'FAIL: needs a tool this machine lacks'; exit 1"))
	if err != nil || !result.Pass {
		t.Fatalf("an old failure must not block the run: err=%v reason=%s", err, result.Reason)
	}
	if len(s.executes) != 1 {
		t.Errorf("the run must end on its first claim, it took %d rounds", len(s.executes))
	}
	if !strings.Contains(result.Reason, "ALREADY FAILING BEFORE THIS CHANGE") || !strings.Contains(result.Reason, "declared") {
		t.Errorf("the verdict must name the old failure: %s", result.Reason)
	}
	if result.Validation == nil || len(result.Validation.PreExisting) != 1 {
		t.Errorf("the validation must carry the pre-existing check: %+v", result.Validation)
	}
}

// TestAnAlreadyFailingGateDoesNotHideANewFailure: the gate was red before the run for a reason
// that is not the run's, and the run then breaks a test inside the SAME check. The name alone
// matches the baseline; the failure does not, so the claim is refused until the new failure is
// gone, and only then accepted with the caveat.
func TestAnAlreadyFailingGateDoesNotHideANewFailure(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return step(true, "touch broken.flag")
		}
		return step(true, "rm broken.flag")
	}}
	_, result, err := runScript(t, s, "unused", projectGate(t,
		"echo 'FAIL: needs a tool this machine lacks'; test -f broken.flag && echo '--- FAIL: TestBroken'; exit 1"))
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if len(s.executes) != 2 {
		t.Fatalf("the claim with a new failure must be refused: rounds = %d, want 2", len(s.executes))
	}
	if !strings.Contains(s.executes[1], "differently now: declared") {
		t.Errorf("the rejection must say the check fails differently:\n%s", s.executes[1])
	}
	if !strings.Contains(result.Reason, "ALREADY FAILING BEFORE THIS CHANGE") {
		t.Errorf("the final verdict must keep the caveat: %s", result.Reason)
	}
}

// TestACheckTheRunBrokeIsNamedAsSuch: the gate passed before the run, the run broke it. The claim
// is refused as usual, the rejection says the check passed before, and a second claim over the
// same failure is judged from what was already measured.
func TestACheckTheRunBrokeIsNamedAsSuch(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		switch round {
		case 1:
			return step(true, "touch broken.flag")
		case 2:
			return step(true, "true")
		}
		return step(true, "rm broken.flag")
	}}
	_, result, err := runScript(t, s, "unused", projectGate(t, "test ! -f broken.flag"))
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if len(s.executes) != 3 {
		t.Fatalf("rounds = %d, want 3", len(s.executes))
	}
	for _, i := range []int{1, 2} {
		if !strings.Contains(s.executes[i], "This check passed before your change") {
			t.Errorf("round %d must be told the run broke the check:\n%s", i+1, s.executes[i])
		}
	}
	if strings.Contains(result.Reason, "ALREADY FAILING") {
		t.Errorf("a clean pass must not carry a caveat: %s", result.Reason)
	}
}

// TestAnOldFailureBesideANewOneIsNamedButStillBlocks: one check was already red, the run broke
// another. The claim is refused for the new one, and the rejection names both, so the run fixes
// what is its own instead of chasing what is not.
func TestAnOldFailureBesideANewOneIsNamedButStillBlocks(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return step(true, "touch broken.flag")
		}
		return step(true, "rm broken.flag")
	}}
	_, result, err := runScript(t, s, "unused", projectGate(t, "test ! -f broken.flag", "false"))
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	second := s.executes[1]
	if !strings.Contains(second, "passed before your change") || !strings.Contains(second, "broke it: declared") ||
		!strings.Contains(second, "Already failing before your change, the same way (not yours; do not spend rounds on them): declared 2") {
		t.Errorf("the rejection must name the new failure and the old one:\n%s", second)
	}
	if !strings.Contains(result.Reason, "ALREADY FAILING BEFORE THIS CHANGE") || !strings.Contains(result.Reason, "declared 2") {
		t.Errorf("the final verdict must keep the caveat about the old failure: %s", result.Reason)
	}
}

// TestTheRunCannotPassAGateItRewrote: with kind auto the gate lives in files the model can edit.
// A run that replaces it with one that passes is refused (there is nobody to ask), and the claim
// is accepted only once the original gate is back and passes.
func TestTheRunCannotPassAGateItRewrote(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return step(true, "echo true > .motita/anchor")
		}
		return step(true, "echo 'test -f done.txt' > .motita/anchor && touch done.txt")
	}}
	_, result, err := runScript(t, s, "unused", projectGate(t, "test -f done.txt"))
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if len(s.executes) != 2 {
		t.Fatalf("a PASS from the rewritten gate must be refused: rounds = %d, want 2", len(s.executes))
	}
	if !strings.Contains(s.executes[1], "gate changed during this run") || !strings.Contains(s.executes[1], "nobody to ask") {
		t.Errorf("the rejection must say the gate changed:\n%s", s.executes[1])
	}
}

// TestAChangedGateIsPutToTheUser: the guard asks, a no keeps the FAIL, a yes makes the new gate
// the one the task is held to (and is not asked again), and a background agent inherits the
// gate its main agent started with. A failing result, or an anchor that is not auto, is untouched.
func TestAChangedGateIsPutToTheUser(t *testing.T) {
	e := mount(t, idleServer(t), config.Anchor{Kind: "auto", Timeout: 10 * time.Second}, nil)
	a := e.agent
	write := func(gate string) {
		t.Helper()
		if err := os.MkdirAll(e.dir+"/.motita", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.dir+"/.motita/anchor", []byte(gate+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	write("false")
	a.gate = a.gateFingerprint()
	write("true")

	var asked []ApprovalRequest
	answer := false
	a.SetApprover(func(_ context.Context, req ApprovalRequest) (bool, error) {
		asked = append(asked, req)
		return answer, nil
	})
	if got := a.validateClaim(ctx, nil); got.Pass || !strings.Contains(got.Reason, "did not approve") {
		t.Errorf("a no must keep the FAIL: %+v", got)
	}
	if len(asked) != 1 || asked[0].Rule != "anchor-gate-changed" || !strings.Contains(asked[0].Command, "sh -c true") {
		t.Fatalf("asked = %+v", asked)
	}
	// A background agent started now is held to the original gate too.
	child := a.newChild(childPlace{dir: e.dir, branch: "b"})
	if child.gate != a.gate {
		t.Error("a background agent must inherit the main agent's gate")
	}
	answer = true
	if got := a.validateClaim(ctx, nil); !got.Pass {
		t.Errorf("a yes must accept the PASS: %+v", got)
	}
	if got := a.validateClaim(ctx, nil); !got.Pass || len(asked) != 2 {
		t.Errorf("an approved gate must not be asked about again: %+v, asked %d", got, len(asked))
	}
	// A failing result is not asked about.
	write("false")
	if got := a.validateClaim(ctx, nil); got.Pass || len(asked) != 2 {
		t.Errorf("a FAIL must not be put to the user: %+v, asked %d", got, len(asked))
	}
	// Nor is a gate the configuration wrote.
	a.cfg.Anchor = config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}
	if a.gateFingerprint() != "" {
		t.Error("a configured anchor has no fingerprint to guard")
	}
}

// TestNoBaselineIsTakenWhereThereIsNothingToCompare: turned off, no anchor, an anchor the
// configuration wrote, or not a repository - the run behaves exactly as before.
func TestNoBaselineIsTakenWhereThereIsNothingToCompare(t *testing.T) {
	e := mount(t, idleServer(t), config.Anchor{Kind: "auto", Baseline: true}, nil)
	ctx := context.Background()
	if e.agent.startBaseline(ctx) != nil {
		t.Error("a workspace that is not a repository has no baseline")
	}
	inRepo(t)(&e.agent.cfg)
	e.agent.cfg.Anchor = config.Anchor{Kind: "command", Command: "true", Baseline: true}
	if e.agent.startBaseline(ctx) != nil {
		t.Error("an anchor the configuration wrote states the goal, and must not be compared")
	}
	e.agent.cfg.Anchor.Baseline = false
	if e.agent.startBaseline(ctx) != nil {
		t.Error("a baseline that is turned off must not be taken")
	}
	e.agent.cfg.Anchor.Baseline = true
	e.agent.cfg.Anchor.Kind = "none"
	if e.agent.startBaseline(ctx) != nil {
		t.Error("with no anchor there is nothing to compare")
	}
	// A nil baseline is a no-op everywhere.
	var b *runBaseline
	b.close(ctx)
	if got := b.judge(ctx, anchor.Result{Reason: "failed checks: x"}); got.Pass || got.Reason != "failed checks: x" {
		t.Errorf("a nil baseline must not change the verdict: %+v", got)
	}
}

// TestABaselineThatCannotBeMadeChangesNoVerdict: every step that can fail on a real machine leaves
// the anchor's verdict exactly as it was.
func TestABaselineThatCannotBeMadeChangesNoVerdict(t *testing.T) {
	ctx := context.Background()
	e := mount(t, idleServer(t), config.Anchor{Kind: "command", Command: "false", Baseline: true}, projectGate(t, "false"))
	failed := anchor.Result{Reason: "failed checks: main", Checks: []anchor.CheckLog{{Name: "main"}}}

	old := baselineSnapshot
	baselineSnapshot = func(context.Context, string, string) (string, string, error) {
		return "", "", errors.New("index.lock exists")
	}
	if e.agent.startBaseline(ctx) != nil {
		t.Error("a snapshot that failed must leave no baseline")
	}
	baselineSnapshot = old

	b := e.agent.startBaseline(ctx)
	if b == nil {
		t.Fatal("a repository must get a baseline")
	}
	defer b.close(ctx)

	oldTemp := baselineTempDir
	baselineTempDir = func(string, string) (string, error) { return "", errors.New("disk full") }
	if got := b.judge(ctx, failed); got.Pass || got.Reason != failed.Reason {
		t.Errorf("no place to check out must change nothing: %+v", got)
	}
	baselineTempDir = oldTemp

	oldCheckout := baselineCheckout
	baselineCheckout = func(context.Context, string, string, string) error { return errors.New("pruned") }
	if got := b.judge(ctx, failed); got.Pass || got.Reason != failed.Reason {
		t.Errorf("a checkout that failed must change nothing: %+v", got)
	}
	baselineCheckout = oldCheckout

	// And a passing verdict is never re-checked.
	if got := b.judge(ctx, anchor.Result{Pass: true, Reason: "ok"}); !got.Pass || len(b.known) != 0 {
		t.Errorf("a pass must not be measured: %+v %v", got, b.known)
	}
}

// TestAGoalTheConfigurationStatesIsNeverCalledAlreadyFailing replays a real run: the anchor was the
// task's own acceptance check (`test -s a.txt`), which fails on the starting tree by design. The
// baseline called it "already failing" and a claim of done went through with nothing written.
// Without a baseline for configured checks, that claim is refused until the file exists.
func TestAGoalTheConfigurationStatesIsNeverCalledAlreadyFailing(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return step(true, "true") // claims done having written nothing
		}
		return step(true, "echo alpha > a.txt")
	}}
	_, result, err := runScript(t, s, "test -s a.txt", inRepo(t))
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if len(s.executes) != 2 {
		t.Fatalf("rounds = %d, want 2: the empty claim must be refused", len(s.executes))
	}
	if strings.Contains(result.Reason, "ALREADY FAILING") {
		t.Errorf("a stated goal must never be excused as pre-existing: %s", result.Reason)
	}
}
