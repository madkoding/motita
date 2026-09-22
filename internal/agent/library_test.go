package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/reward"
	"github.com/madkoding/starlight/internal/skills"
)

// Task mode and Plan mode share ONE library. Task mode reaches it through its action protocol
// rather than tool calls, because its actions are JSON — the four operations are named by the
// action's "kind". These tests pin that path, and the attribution that makes a verdict land.

// libraryAgent builds an agent with a library, no engine needed for these paths.
func libraryAgent(t *testing.T, bodies map[string]string) *Agent {
	t.Helper()
	lib := skills.New(t.TempDir())
	for name, body := range bodies {
		if _, err := lib.Save(name, body); err != nil {
			t.Fatalf("Save(%s): %v", name, err)
		}
	}
	led, err := reward.Open("")
	if err != nil {
		t.Fatal(err)
	}
	lib.Scorer = led
	// A logger and a config are needed by the paths under test: nil ones panic, and the point
	// of these tests is the library, not a nil-check.
	a := &Agent{library: lib, reward: led, log: logx.Global(), cfg: config.Default()}
	return a
}

// act runs one library action and returns what the model would see.
func act(t *testing.T, a *Agent, kind, command string) string {
	t.Helper()
	handled, out := a.runLibraryAction(kind, Command{Kind: kind, Command: command, Description: "why"})
	if !handled {
		t.Fatalf("kind %q must be handled as a library action", kind)
	}
	return out
}

// --- the four operations --------------------------------------------------

func TestListSkillsReportsTheIndex(t *testing.T) {
	a := libraryAgent(t, map[string]string{
		"zephyr-build": "# Zephyr build\nHow to build the firmware.\n",
	})
	out := act(t, a, "list_skills", "")
	if !strings.Contains(out, "zephyr-build") || !strings.Contains(out, "Zephyr build") {
		t.Errorf("the index must name the skills:\n%s", out)
	}
}

func TestListSkillsOnAnEmptyLibraryInvitesSaving(t *testing.T) {
	// An empty library is where everyone starts, and the useful answer is what to do about it.
	a := libraryAgent(t, nil)
	out := act(t, a, "list_skills", "")
	if !strings.Contains(strings.ToLower(out), "empty") {
		t.Errorf("an empty library must say so:\n%s", out)
	}
	if !strings.Contains(out, "save") {
		t.Errorf("it must say what to do about it:\n%s", out)
	}
}

func TestSearchSkillsFindsByWhatTheWorkIsAbout(t *testing.T) {
	a := libraryAgent(t, map[string]string{
		"flash-board": "# Flash a board\nFlashing firmware over USB serial.\n",
	})
	out := act(t, a, "search_skills", "flash a board over usb")
	if !strings.Contains(out, "flash-board") {
		t.Errorf("the search must find the skill:\n%s", out)
	}
}

func TestSearchSkillsWithNothingFoundSaysSoUsefully(t *testing.T) {
	// "No matches" must come with what to do next, or the model stops looking.
	a := libraryAgent(t, map[string]string{"unrelated": "# Bread\nBaking.\n"})
	out := act(t, a, "search_skills", "quantum chromodynamics")
	if !strings.Contains(strings.ToLower(out), "no skill matches") {
		t.Errorf("a miss must be reported as a miss:\n%s", out)
	}
	if !strings.Contains(out, "save") {
		t.Errorf("a miss must invite saving what is learned:\n%s", out)
	}
}

func TestSearchSkillsWithNoQueryIsAnError(t *testing.T) {
	a := libraryAgent(t, map[string]string{"a": "# A\nBody.\n"})
	out := act(t, a, "search_skills", "   ")
	if !strings.HasPrefix(out, "Error") {
		t.Errorf("an empty query must be refused:\n%s", out)
	}
}

func TestReadSkillReturnsTheProcedure(t *testing.T) {
	a := libraryAgent(t, map[string]string{
		"zephyr-build": "# Zephyr build\nThe steps are these.\n1. source the env\n",
	})
	out := act(t, a, "read_skill", "zephyr-build")
	if !strings.Contains(out, "source the env") {
		t.Errorf("the whole procedure must come back:\n%s", out)
	}
	if !strings.Contains(out, "skill: zephyr-build") {
		t.Errorf("the read must name what it read:\n%s", out)
	}
}

func TestReadingAMissingSkillIsReportedNotSilent(t *testing.T) {
	a := libraryAgent(t, map[string]string{"real": "# Real\nBody.\n"})
	out := act(t, a, "read_skill", "imaginary")
	if !strings.Contains(out, "No skill named") {
		t.Errorf("a missing skill must be reported:\n%s", out)
	}
	// And nothing was credited for it.
	if got := a.Consulted(); len(got) != 0 {
		t.Errorf("a missing skill must not be recorded, got %+v", got)
	}
}

func TestSaveSkillWritesIt(t *testing.T) {
	a := libraryAgent(t, nil)
	out := act(t, a, "save_skill", "my-procedure :: # My procedure\nThe steps.\n")
	if !strings.Contains(out, "Saved") {
		t.Errorf("a save must report itself:\n%s", out)
	}
	back, err := a.library.Get("my-procedure")
	if err != nil {
		t.Fatalf("the skill must be readable: %v", err)
	}
	if !strings.Contains(back.Body, "The steps.") {
		t.Errorf("the body must be written: %q", back.Body)
	}
}

func TestSaveSkillNeedsTheSeparator(t *testing.T) {
	// The argument is one string, so the name and the body need a separator. Without it the
	// call is refused rather than writing a skill whose name is the whole document.
	a := libraryAgent(t, nil)
	out := act(t, a, "save_skill", "just a name with no body")
	if !strings.HasPrefix(out, "Error") {
		t.Errorf("a missing separator must be refused:\n%s", out)
	}
	if !strings.Contains(out, "::") {
		t.Errorf("the error must show the expected form:\n%s", out)
	}
}

// --- the attribution ------------------------------------------------------

func TestReadingInTaskModeIsRecorded(t *testing.T) {
	// The verdict has to land somewhere, and this is where the somewhere is decided.
	a := libraryAgent(t, map[string]string{"flash": "# Flash\nBody.\n"})
	_ = act(t, a, "read_skill", "flash")
	_ = act(t, a, "read_skill", "flash")

	used := a.Consulted()
	if used["flash"] != 2 {
		t.Errorf("each read must count, got %+v", used)
	}
}

func TestConsultedIsNilWhenNothingWasRead(t *testing.T) {
	a := libraryAgent(t, map[string]string{"a": "# A\nBody.\n"})
	_ = act(t, a, "list_skills", "")
	if got := a.Consulted(); got != nil {
		t.Errorf("listing is not reading, got %+v", got)
	}
}

func TestConsultedIsACopy(t *testing.T) {
	a := libraryAgent(t, map[string]string{"flash": "# F\nBody.\n"})
	_ = act(t, a, "read_skill", "flash")
	got := a.Consulted()
	got["flash"] = 99
	if a.Consulted()["flash"] != 1 {
		t.Error("Consulted must return a copy")
	}
}

// --- what the model is shown ---------------------------------------------

func TestTheValueIsShownInTheIndex(t *testing.T) {
	a := libraryAgent(t, map[string]string{"flash": "# Flash\nBody.\n"})
	_ = a.reward.Attribute([]string{"flash"}, map[string]int{"flash": 1}, true, "")

	out := act(t, a, "list_skills", "")
	if !strings.Contains(out, "[used 1, value +") {
		t.Errorf("the value must be visible with its counts:\n%s", out)
	}
	for _, label := range []string{"RELIABLE", "GOOD", "BAD", "TRUST"} {
		if strings.Contains(out, label) {
			t.Errorf("the listing must carry no interpretation, found %q:\n%s", label, out)
		}
	}
}

func TestTheComplaintReachesTheModelOnRead(t *testing.T) {
	// This is what makes a verdict actionable: the model reads the procedure AND what the user
	// said was wrong with it, in the user's words.
	a := libraryAgent(t, map[string]string{"flash": "# Flash\n1. use /dev/ttyUSB0\n"})
	note := "my board shows up as /dev/ttyACM0, step 1 is wrong"
	_ = a.reward.Attribute([]string{"flash"}, map[string]int{"flash": 1}, false, note)

	out := act(t, a, "read_skill", "flash")
	if !strings.Contains(out, note) {
		t.Errorf("the complaint must reach the model verbatim:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "not been revised") {
		t.Errorf("it must say the procedure has not been fixed:\n%s", out)
	}
	if !strings.Contains(out, "save_skill") {
		t.Errorf("it must point at the tool that fixes it:\n%s", out)
	}
}

func TestSavingASkillMarksTheComplaintAddressed(t *testing.T) {
	// The fix is the observable event: once the procedure has been rewritten the complaint is
	// answered, and repeating it would send the model to re-fix what is already fixed.
	a := libraryAgent(t, map[string]string{"flash": "# Flash\n1. wrong step\n"})
	_ = a.reward.Attribute([]string{"flash"}, map[string]int{"flash": 1}, false, "step 1 is wrong")

	out := act(t, a, "save_skill", "flash :: # Flash\n1. corrected step\n")
	if !strings.Contains(strings.ToLower(out), "addressed") {
		t.Errorf("the save must say the complaint was answered:\n%s", out)
	}

	// And it is no longer handed out.
	again := act(t, a, "read_skill", "flash")
	if strings.Contains(again, "step 1 is wrong") {
		t.Errorf("an addressed complaint must not be repeated:\n%s", again)
	}
}

func TestSavingDoesNotInventAComplaint(t *testing.T) {
	// A skill nobody complained about is just saved: no note, no mention.
	a := libraryAgent(t, nil)
	out := act(t, a, "save_skill", "fresh :: # Fresh\nBody.\n")
	if strings.Contains(strings.ToLower(out), "addressed") {
		t.Errorf("there was no complaint to address:\n%s", out)
	}
}

// --- without a library ----------------------------------------------------

func TestLibraryActionsWithoutALibrarySaySo(t *testing.T) {
	// A task run built without a library must answer plainly rather than panicking, which is
	// what an embedder that does not want the feature gets.
	a := &Agent{}
	for _, kind := range []string{"read_skill", "search_skills", "list_skills", "save_skill"} {
		handled, out := a.runLibraryAction(kind, Command{Kind: kind, Command: "x"})
		if !handled {
			t.Errorf("%s must still be recognised as a library action", kind)
		}
		if !strings.Contains(out, "no procedure library") {
			t.Errorf("%s without a library must say so, got %q", kind, out)
		}
	}
}

func TestAnUnknownKindIsNotALibraryAction(t *testing.T) {
	// Anything else falls through to the command path, where a kind nothing recognises is
	// reported as a step with no command — the honest outcome.
	a := libraryAgent(t, nil)
	handled, out := a.runLibraryAction("teleport", Command{Kind: "teleport"})
	if handled {
		t.Error("an unknown kind must not be swallowed by the library")
	}
	if out != "" {
		t.Errorf("an unhandled kind must say nothing, got %q", out)
	}
}

func TestTheSuffixesAreEmptyWithoutALedger(t *testing.T) {
	a := &Agent{}
	if got := a.historySuffix("a"); got != "" {
		t.Errorf("no ledger means no value, got %q", got)
	}
	if got := a.feedbackSuffix("a"); got != "" {
		t.Errorf("no ledger means no complaint, got %q", got)
	}
}

func TestTheSuffixesAreEmptyForAnUnknownSkill(t *testing.T) {
	a := libraryAgent(t, map[string]string{"a": "# A\nBody.\n"})
	if got := a.historySuffix("nobody"); got != "" {
		t.Errorf("an unknown skill has no value, got %q", got)
	}
	if got := a.feedbackSuffix("nobody"); got != "" {
		t.Errorf("an unknown skill has no complaint, got %q", got)
	}
}

// --- the failure branches -------------------------------------------------

func TestTheSettersInstall(t *testing.T) {
	// The runner reaches the agent through these, so they have to actually assign.
	a := &Agent{}
	lib := skills.New(t.TempDir())
	led, _ := reward.Open("")
	a.SetLibrary(lib)
	a.SetReward(led)
	if a.library != lib || a.reward != led {
		t.Error("the setters must install what they are given")
	}
}

func TestLibraryActionsReportAnUnreadableLibrary(t *testing.T) {
	// The library directory is removed behind the agent's back, which is the only way these
	// branches are reached: the errors come from the filesystem, not from the caller.
	a := libraryAgent(t, map[string]string{"a": "# A\nBody.\n"})
	if err := os.RemoveAll(a.library.Dir); err != nil {
		t.Fatal(err)
	}
	// A directory that is gone reads as an empty library, which is not an error — so make it
	// unreadable instead, by pointing the library at a path that is a FILE.
	if err := os.WriteFile(a.library.Dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if out := act(t, a, "list_skills", ""); !strings.HasPrefix(out, "Error") {
		t.Errorf("an unreadable library must be reported:\n%s", out)
	}
	if out := act(t, a, "search_skills", "anything"); !strings.HasPrefix(out, "Error") {
		t.Errorf("an unreadable library must be reported on search too:\n%s", out)
	}
}

func TestReadingASkillThatCannotBeReadIsReported(t *testing.T) {
	// Not "not found" — found and unreadable. The two need different messages: one invites a
	// different name, the other says the disk is the problem.
	a := libraryAgent(t, map[string]string{"a": "# A\nBody.\n"})
	path := filepath.Join(a.library.Dir, "a.md")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// A directory where the file was: it exists, so the not-found branch is skipped, and
	// reading it fails.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	out := act(t, a, "read_skill", "a")
	if !strings.HasPrefix(out, "Error") {
		t.Errorf("an unreadable skill must be reported:\n%s", out)
	}
	if strings.Contains(out, "No skill named") {
		t.Errorf("it must not be reported as missing:\n%s", out)
	}
}

func TestSavingASkillThatCannotBeWrittenIsReported(t *testing.T) {
	a := libraryAgent(t, nil)
	// The library directory becomes a file, so the save cannot create the document.
	if err := os.RemoveAll(a.library.Dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.library.Dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := act(t, a, "save_skill", "name :: # Name\nBody.\n")
	if !strings.HasPrefix(out, "Error") {
		t.Errorf("a save that cannot be written must be reported:\n%s", out)
	}
}

func TestASaveThatAddressesNothingSaysNothing(t *testing.T) {
	// The "addressed" note is only added when a complaint was actually marked, so a save over
	// an already-addressed complaint must not claim to have addressed it again.
	a := libraryAgent(t, map[string]string{"flash": "# Flash\nBody.\n"})
	_ = a.reward.Attribute([]string{"flash"}, map[string]int{"flash": 1}, false, "broken")

	first := act(t, a, "save_skill", "flash :: # Flash\nFixed.\n")
	if !strings.Contains(strings.ToLower(first), "addressed") {
		t.Errorf("the first save must mark the complaint:\n%s", first)
	}
	second := act(t, a, "save_skill", "flash :: # Flash\nFixed again.\n")
	if strings.Contains(strings.ToLower(second), "addressed") {
		t.Errorf("a second save has nothing left to address:\n%s", second)
	}
}

// --- the action protocol --------------------------------------------------

func TestRunActionsDispatchesALibraryAction(t *testing.T) {
	// The integration point: the model asks for a skill by naming the kind, and the result
	// comes back as the output of that step — without a shell ever being involved.
	a := libraryAgent(t, map[string]string{"flash": "# Flash\nThe procedure.\n"})

	out, err := a.runActions(context.Background(), []Command{
		{Kind: "read_skill", Description: "read the procedure", Command: "flash"},
	}, "")
	if err != nil {
		t.Fatalf("runActions: %v", err)
	}
	if !strings.Contains(out, "The procedure.") {
		t.Errorf("the procedure must come back as the step output:\n%s", out)
	}
	if !strings.Contains(out, "[read_skill]") {
		t.Errorf("the step must say what it was:\n%s", out)
	}
	if used := a.Consulted(); used["flash"] != 1 {
		t.Errorf("the read must be recorded for a later verdict, got %+v", used)
	}
}

func TestRunActionsStillRunsCommands(t *testing.T) {
	// The command path is untouched: an action whose kind is "command" — or empty, which is
	// what every prompt before this feature produced — goes to the executor.
	a := libraryAgent(t, nil)
	called := false
	a.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) {
		called = true
		return "ran\n", false, 0, nil
	}

	for _, kind := range []string{"command", ""} {
		called = false
		out, err := a.runActions(context.Background(), []Command{
			{Kind: kind, Command: "true"},
		}, "")
		if err != nil {
			t.Fatalf("kind %q: %v", kind, err)
		}
		if !called {
			t.Errorf("kind %q must be executed as a command", kind)
		}
		if !strings.Contains(out, "ran") {
			t.Errorf("kind %q: the output must be reported:\n%s", kind, out)
		}
	}
}
