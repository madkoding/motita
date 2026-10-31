package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// Task mode and Plan mode share ONE library. Task mode reaches it through its action protocol
// rather than tool calls, because its actions are JSON — the four operations are named by the
// action's "kind". These tests pin that path.

// libraryAgent builds an agent with a library, no engine needed for these paths.
func libraryAgent(t *testing.T, bodies map[string]string) *Agent {
	t.Helper()
	lib := skills.New(t.TempDir())
	for name, body := range bodies {
		if _, err := lib.Save(name, body); err != nil {
			t.Fatalf("Save(%s): %v", name, err)
		}
	}
	// A logger and a config are needed by the paths under test: nil ones panic, and the point
	// of these tests is the library, not a nil-check.
	a := &Agent{library: lib, log: logx.Global(), cfg: config.Default()}
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

// TestAProjectRepositorysSkillIsMarkedUntrusted: a document from the project's .motita/skills
// came with a clone, and the model must be told so wherever it sees it.
func TestAProjectRepositorysSkillIsMarkedUntrusted(t *testing.T) {
	project := t.TempDir()
	a := libraryAgent(t, map[string]string{"mine": "# Mine\nThe user's.\n"})
	a.library.Overlay = &skills.Overlay{
		Primary:   filepath.Join(project, ".motita", "skills"),
		Secondary: a.library.Dir,
		Base:      project,
	}
	if _, err := a.library.Save("repo-build", "# Repo build\nRun make.\n"); err != nil {
		t.Fatal(err)
	}
	for kind, arg := range map[string]string{
		"read_skill": "repo-build", "list_skills": "", "search_skills": "repo build",
	} {
		if out := act(t, a, kind, arg); !strings.Contains(out, "repository") {
			t.Errorf("%s must mark the project's document:\n%s", kind, out)
		}
	}
	if out := act(t, a, "read_skill", "mine"); strings.Contains(out, "repository") {
		t.Errorf("the user's own document is not marked:\n%s", out)
	}
}

func TestReadingAMissingSkillIsReportedNotSilent(t *testing.T) {
	a := libraryAgent(t, map[string]string{"real": "# Real\nBody.\n"})
	out := act(t, a, "read_skill", "imaginary")
	if !strings.Contains(out, "No skill named") {
		t.Errorf("a missing skill must be reported:\n%s", out)
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

// --- the failure branches -------------------------------------------------

func TestTheSettersInstall(t *testing.T) {
	// The runner reaches the agent through these, so they have to actually assign.
	a := &Agent{}
	lib := skills.New(t.TempDir())
	a.SetLibrary(lib)
	if a.library != lib {
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

func TestOnlyReadingCountsAndTheRunSavesOnce(t *testing.T) {
	a := libraryAgent(t, map[string]string{"deploy": "# Deploy\nShip it.\n"})
	path := filepath.Join(t.TempDir(), "usage.json")
	u, err := usage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a.SetUsage(u)

	act(t, a, "search_skills", "deploy")
	act(t, a, "read_skill", "deploy")
	if e := u.Get("deploy"); e.UseCount != 1 || e.ViewCount != 1 {
		t.Fatalf("use=%d view=%d, want 1 and 1: a search is not a use", e.UseCount, e.ViewCount)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("nothing is written until the run ends")
	}
	a.saveUsage()
	again, err := usage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get("deploy").UseCount; got != 1 {
		t.Errorf("persisted use count = %d, want 1", got)
	}
	(&Agent{}).saveUsage() // no ledger: nothing to do
}
