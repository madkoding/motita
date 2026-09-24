package tui

import (
	"context"
	"strings"
	"testing"
)

// The verdict commands are the ONLY reward signal in the system, so what they pass to the
// runner matters: the wrong argument here means the user's feedback lands on nothing.

// lastVerdict returns the single verdict the fake was given, or fails.
func lastVerdict(t *testing.T, f *fakeRunner) verdictCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.verdicts) != 1 {
		t.Fatalf("expected exactly one verdict, got %d", len(f.verdicts))
	}
	return f.verdicts[0]
}

func TestGoodRecordsAGoodVerdict(t *testing.T) {
	f := &fakeRunner{}
	tu := newFakeTUI("/good\n", f)
	tu.Run(context.Background())

	v := lastVerdict(t, f)
	if !v.good {
		t.Error("/good must record a good verdict")
	}
	if v.note != "" {
		t.Errorf("/good without text must carry no note, got %q", v.note)
	}
}

func TestBadCarriesTheUsersWords(t *testing.T) {
	// This is the point of the note: the number says it failed, the words say what to fix.
	f := &fakeRunner{}
	tu := newFakeTUI("/bad step 2 uses the wrong port\n", f)
	tu.Run(context.Background())

	v := lastVerdict(t, f)
	if v.good {
		t.Error("/bad must record a bad verdict")
	}
	if v.note != "step 2 uses the wrong port" {
		t.Errorf("the note must reach the runner intact, got %q", v.note)
	}
}

func TestTheNoteKeepsItsCapitalisation(t *testing.T) {
	// The command word is lowercased for matching, and the note must NOT be: it is the user's
	// own sentence and it is quoted back to the model verbatim.
	f := &fakeRunner{}
	tu := newFakeTUI("/BAD Step 2 breaks the Build\n", f)
	tu.Run(context.Background())

	v := lastVerdict(t, f)
	if !strings.Contains(v.note, "Step") || !strings.Contains(v.note, "Build") {
		t.Errorf("the note must keep its original casing, got %q", v.note)
	}
}

func TestBadWithoutANoteIsStillRecorded(t *testing.T) {
	// A bare verdict is a number only. It must still work: requiring prose would make the
	// quick case — "that was wrong" — impossible.
	f := &fakeRunner{}
	tu := newFakeTUI("/bad\n", f)
	tu.Run(context.Background())

	v := lastVerdict(t, f)
	if v.good || v.note != "" {
		t.Errorf("expected a bad verdict with no note, got %+v", v)
	}
}

func TestAVerdictIsNotSentAsAMessage(t *testing.T) {
	// A command that also ran as a task would submit "/good" to the model as a request. The
	// chat must show the report, not a task.
	f := &fakeRunner{}
	tu := newFakeTUI("/bad something was wrong\n", f)
	tu.Run(context.Background())

	f.mu.Lock()
	called := f.taskCalled
	var last string
	if len(f.planProgress) > 0 {
		last = strings.Join(f.planProgress, " ")
	}
	f.mu.Unlock()

	if called {
		t.Error("a verdict is not a task and must not be run as one")
	}
	_ = last
	// And the user sees what happened.
	if !strings.Contains(lastFrame(t, tu), "recorded") {
		t.Errorf("the verdict report must be shown:\n%s", lastFrame(t, tu))
	}
}

func TestACommandWithAVerdictPrefixIsNotAVerdict(t *testing.T) {
	// "/goodbye" must not be read as "/good" with the note "bye". The prefix check requires a
	// space or the end of the line, and this is why.
	f := &fakeRunner{}
	tu := newFakeTUI("/goodbye\n", f)
	tu.Run(context.Background())

	f.mu.Lock()
	n := len(f.verdicts)
	f.mu.Unlock()
	if n != 0 {
		t.Errorf("/goodbye must not record a verdict, got %d", n)
	}
}

func TestValueShowsTheReport(t *testing.T) {
	f := &fakeRunner{rewardReport: "zephyr-build  value +0.80   3 good / 0 bad"}
	tu := newFakeTUI("/value\n", f)
	tu.Run(context.Background())

	if !strings.Contains(lastFrame(t, tu), "zephyr-build") {
		t.Errorf("the report must be shown:\n%s", lastFrame(t, tu))
	}
}

func TestTheAliasWorks(t *testing.T) {
	f := &fakeRunner{rewardReport: "marked"}
	tu := newFakeTUI("/v\n", f)
	tu.Run(context.Background())

	if !strings.Contains(lastFrame(t, tu), "marked") {
		t.Errorf("/v must be the same command:\n%s", lastFrame(t, tu))
	}
}

func TestTheVerdictCommandsAreInTheCatalogue(t *testing.T) {
	// The completion popup and the help screen are built from the catalogue, so a command that
	// is not in it is a command the user cannot discover.
	var found int
	for _, c := range commands {
		switch c.Name {
		case "/good", "/bad", "/value":
			found++
		}
	}
	if found != 3 {
		t.Errorf("expected /good, /bad and /value in the catalogue, found %d", found)
	}
	// And /bad documents that the note says what to fix, which is what makes it useful.
	for _, c := range commands {
		if c.Name == "/bad" && !strings.Contains(strings.ToLower(c.Help), "fix") {
			t.Errorf("/bad's help must say the note is what to fix, got %q", c.Help)
		}
	}
}

// TestModelsWithAnIDPicksTheModel: /models <id> picks the model for the session the way
// /reasoning picks the level, instead of listing the catalogue again.
func TestModelsWithAnIDPicksTheModel(t *testing.T) {
	f := &fakeRunner{}
	tu := newFakeTUI("/models haiku\n", f)
	tu.Run(context.Background())

	if got := f.Config().LLM.Model; got != "haiku" {
		t.Errorf("model = %q, want haiku", got)
	}
	f.mu.Lock()
	listed := f.modelsCalled
	f.mu.Unlock()
	if listed {
		t.Error("picking a model must not ask for the catalogue")
	}
	if !strings.Contains(lastFrame(t, tu), "model set to haiku") {
		t.Errorf("the change must be confirmed:\n%s", lastFrame(t, tu))
	}
}
