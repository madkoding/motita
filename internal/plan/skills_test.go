package plan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/review"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// raw spells a tool argument the way the model sends it: JSON, not a Go struct.
func raw(s string) json.RawMessage { return json.RawMessage(s) }

// writeFileAt puts a plain file where a directory is expected, which is the cheapest way to
// make a library unreadable for a reason other than a permission bit.
func writeFileAt(path, contents string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(contents), 0o644)
}

// The skill tools as the model meets them: the text that comes back is what the model reasons
// about next, so these tests assert on the wording as much as on the behaviour. A tool that
// returns an empty string, or JSON the model has to strip, or an error where a plain "nothing
// here" is the honest answer, costs a round trip at best and a wrong decision at worst.

func libOf(t *testing.T, docs map[string]string) *skills.Library {
	t.Helper()
	lib := skills.New(t.TempDir())
	for name, body := range docs {
		if _, err := lib.Save(name, body); err != nil {
			t.Fatalf("saving %s: %v", name, err)
		}
	}
	return lib
}

// TestListSkillsReportsTheIndex: the model reads this to decide what to look up, so it needs
// every name and a line about each.
func TestListSkillsReportsTheIndex(t *testing.T) {
	p := (&Planner{}).WithLibrary(libOf(t, map[string]string{
		"zephyr-build": "# Zephyr build\n\nUse west to build the image.\n",
		"serial-debug": "# Serial debug\n\nAttach to the port and watch the boot log.\n",
	}))

	got := p.toolListSkills()
	for _, want := range []string{"zephyr-build", "Zephyr build", "Use west", "serial-debug", "Serial debug"} {
		if !strings.Contains(got, want) {
			t.Errorf("the index must mention %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Use west to build the image") == false {
		t.Error("the index must carry the summary")
	}
}

// TestListSkillsOnAnEmptyLibrary says what to do next rather than reporting a bare zero: the
// model has to know it should carry on under its own knowledge.
func TestListSkillsOnAnEmptyLibrary(t *testing.T) {
	p := (&Planner{}).WithLibrary(libOf(t, nil))

	got := p.toolListSkills()
	if !strings.Contains(got, "empty") {
		t.Errorf("an empty library must be stated plainly:\n%s", got)
	}
	if !strings.Contains(got, "knowledge") {
		t.Errorf("the model must be told to proceed on its own knowledge:\n%s", got)
	}
}

// TestSearchSkillsFindsByDescription: the point of searching the whole text is that the model
// describes the work in its own words, which rarely match a title.
func TestSearchSkillsFindsByDescription(t *testing.T) {
	p := (&Planner{}).WithLibrary(libOf(t, map[string]string{
		"nrf": "# NRF firmware\n\nUse west and the SDK to produce a UF2 image for the board.\n",
	}))

	got := p.toolSearchSkills(raw(`{"query":"flash a board over USB"}`))
	if !strings.Contains(got, "nrf") {
		t.Errorf("the search must find the skill by its body:\n%s", got)
	}
	if !strings.Contains(got, "read_skill") {
		t.Errorf("the result must say how to read it in full:\n%s", got)
	}
}

// TestSearchSkillsWithNoMatchInvitesProceeding: a miss is normal, and the answer has to send
// the model forward instead of making it retry.
func TestSearchSkillsWithNoMatchInvitesProceeding(t *testing.T) {
	p := (&Planner{}).WithLibrary(libOf(t, map[string]string{
		"nrf": "# NRF\n\nUse west.\n",
	}))

	got := p.toolSearchSkills(raw(`{"query":"kubernetes operators"}`))
	if !strings.Contains(got, "No skill matches") {
		t.Errorf("the miss must be stated:\n%s", got)
	}
	if !strings.Contains(got, "knowledge") || !strings.Contains(got, "save") {
		t.Errorf("the model must be told to proceed and to save what it learns:\n%s", got)
	}
}

// TestSearchSkillsRefusesAnEmptyQuery: with nothing to rank, the answer would be arbitrary, so
// the model is told to describe the work.
func TestSearchSkillsRefusesAnEmptyQuery(t *testing.T) {
	p := (&Planner{}).WithLibrary(libOf(t, nil))

	got := p.toolSearchSkills(raw(`{"query":"   "}`))
	if !strings.Contains(got, "Error") {
		t.Errorf("an empty query must be refused:\n%s", got)
	}

	// And a malformed argument must not panic the run.
	if got := p.toolSearchSkills(raw(`{not json`)); !strings.Contains(got, "Error") {
		t.Errorf("a bad argument must be reported:\n%s", got)
	}
}

// TestReadSkillReturnsTheProcedure: the whole point of the library is that the model can get
// the full text, so this is the tool that changes what it does.
func TestReadSkillReturnsTheProcedure(t *testing.T) {
	body := "# Zephyr build\n\nUse west build -b nrf52840dk.\n\n## Pitfall\n\nErase before flashing.\n"
	p := (&Planner{}).WithLibrary(libOf(t, map[string]string{"zephyr-build": body}))

	got := p.toolReadSkill(raw(`{"name":"zephyr-build"}`))
	for _, want := range []string{"zephyr-build", "west build -b nrf52840dk", "Erase before flashing"} {
		if !strings.Contains(got, want) {
			t.Errorf("the procedure must be returned in full, missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "source:") {
		t.Errorf("the result must name where it read it from:\n%s", got)
	}
}

// TestReadSkillSaysWhenThereIsNoSuchSkill: the model has to be able to recover by listing what
// there is, so the answer points at the index rather than reporting an I/O failure.
func TestReadSkillSaysWhenThereIsNoSuchSkill(t *testing.T) {
	p := (&Planner{}).WithLibrary(libOf(t, map[string]string{"nrf": "# NRF\n\nwest\n"}))

	// A malformed argument is a different failure from a missing name, and both must be
	// reported rather than panicking the run.
	if got := p.toolReadSkill(raw(`{not json`)); !strings.Contains(got, "Error") {
		t.Errorf("a bad argument must be reported:\n%s", got)
	}

	got := p.toolReadSkill(raw(`{"name":"nothing-here"}`))
	if !strings.Contains(got, "No skill named") {
		t.Errorf("a missing skill must read as a plain miss:\n%s", got)
	}
	if !strings.Contains(got, "list_skills") {
		t.Errorf("the answer must point at the index:\n%s", got)
	}
}

// TestSaveSkillReportsWhereItWent: the model tells the user what it wrote down, and the path
// makes that traceable.
func TestSaveSkillReportsWhereItWent(t *testing.T) {
	lib := skills.New(t.TempDir())
	p := (&Planner{}).WithLibrary(lib)

	got := p.toolSaveSkill(raw(`{"name":"My New Skill","body":"# My New Skill\n\nDo the thing.\n"}`))
	if !strings.Contains(got, "my-new-skill") {
		t.Errorf("the sanitised name must be reported:\n%s", got)
	}
	if !strings.Contains(got, ".md") {
		t.Errorf("the path must be reported:\n%s", got)
	}
	if !strings.Contains(got, "later sessions") {
		t.Errorf("the model must know the skill outlives this session:\n%s", got)
	}

	// And it really is readable afterwards.
	if _, err := lib.Get("my-new-skill"); err != nil {
		t.Errorf("the saved skill must be readable: %v", err)
	}
	if _, err := lib.Get("My New Skill"); err != nil {
		t.Errorf("the name must be resolvable as the model wrote it: %v", err)
	}
}

// TestSaveSkillReportsWhatItRefused: an oversized document or a malformed argument reaches the
// model as a reason, not as a silent success.
func TestSaveSkillReportsWhatItRefused(t *testing.T) {
	lib := skills.New(t.TempDir())
	lib.MaxFileBytes = 32
	p := (&Planner{}).WithLibrary(lib)

	if got := p.toolSaveSkill(raw(`{"name":"x","body":"` + strings.Repeat("a", 200) + `"}`)); !strings.Contains(got, "Error") {
		t.Errorf("an oversized skill must be refused:\n%s", got)
	}
	if got := p.toolSaveSkill(raw(`{"name":"x","body":"   "}`)); !strings.Contains(got, "Error") {
		t.Errorf("an empty body must be refused:\n%s", got)
	}
	if got := p.toolSaveSkill(raw(`{bad`)); !strings.Contains(got, "Error") {
		t.Errorf("a bad argument must be reported:\n%s", got)
	}
}

// TestTheSkillToolsWithoutALibrary: a planner used as a one-shot runner has no library, and the
// tools must say so instead of panicking on a nil dereference.
func TestTheSkillToolsWithoutALibrary(t *testing.T) {
	p := &Planner{}
	for name, got := range map[string]string{
		"list":   p.toolListSkills(),
		"search": p.toolSearchSkills(raw(`{"query":"x"}`)),
		"read":   p.toolReadSkill(raw(`{"name":"x"}`)),
		"save":   p.toolSaveSkill(raw(`{"name":"x","body":"y"}`)),
	} {
		if !strings.Contains(got, "Error") {
			t.Errorf("%s without a library must report it:\n%s", name, got)
		}
		if !strings.Contains(got, "library") {
			t.Errorf("%s must name what is missing:\n%s", name, got)
		}
	}
}

// TestListSkillsReportsAnUnreadableLibrary: a broken library is a problem, and a problem the
// model cannot see is one it will work around blindly.
func TestListSkillsReportsAnUnreadableLibrary(t *testing.T) {
	lib := skills.New(filepath.Join(t.TempDir(), "missing"))
	// A file where the directory should be.
	if err := writeFileAt(lib.Dir, "blocking"); err != nil {
		t.Fatal(err)
	}
	p := (&Planner{}).WithLibrary(lib)

	got := p.toolListSkills()
	if !strings.Contains(got, "Error") {
		t.Errorf("an unreadable library must be reported:\n%s", got)
	}

	got = p.toolSearchSkills(raw(`{"query":"x"}`))
	if !strings.Contains(got, "Error") {
		t.Errorf("the search must report it too:\n%s", got)
	}
}

// TestTheLibraryToolsAreAdvertised: a tool the model is not told about is a tool it will never
// call, so the definitions must be there and must say what the library is for.
func TestTheLibraryToolsAreAdvertised(t *testing.T) {
	var names []string
	for _, t2 := range (&Planner{}).tools() {
		names = append(names, t2.Function.Name)
	}
	for _, want := range []string{"list_skills", "search_skills", "read_skill", "save_skill"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the tool %q must be advertised, got %v", want, names)
		}
	}
	if len(names) != 8 {
		t.Errorf("the read-only mode advertises %d tools, want 8", len(names))
	}
}

// promptText is the system prompt with its line wrapping removed.
//
// The prompt is prose wrapped at 90-ish columns, so a phrase the tests look for can straddle a
// newline. Normalising here keeps the assertions readable as sentences instead of riddles full
// of \n, and it is the only transformation: the words themselves are still checked exactly.
func promptText() string {
	return strings.Join(strings.Fields(SystemPrompt), " ")
}

// TestTheSystemPromptSaysTheSkillsExistButAreNotInContext: the library only works if the model
// knows two things — that it can look procedures up, and that nothing is in its context until
// it does. Both are stated, because a model that assumes it has already read them will not.
func TestTheSystemPromptSaysTheSkillsExistButAreNotInContext(t *testing.T) {
	for _, want := range []string{
		"search_skills", "save_skill", "list_skills", "read_skill",
		"NOT part of your instructions", "reach for",
		"before starting anything that sounds like a procedure",
		"A diary is not a skill",
	} {
		if !strings.Contains(promptText(), want) {
			t.Errorf("the system prompt must contain %q", want)
		}
	}
}

// TestTheSystemPromptTellsTheModelToFillInWhatIsUnsaid: the user omits most of what they mean,
// and the prompt has to make supplying it the model's job.
func TestTheSystemPromptTellsTheModelToFillInWhatIsUnsaid(t *testing.T) {
	for _, want := range []string{
		"leaves most of it unsaid",
		"supply that missing intelligence",
		"WHAT the goal implies",
		"WHERE it applies",
		"Ask only when the missing information is genuinely unknowable",
		"A question that a tool call could have answered",
		"Never stall",
	} {
		if !strings.Contains(promptText(), want) {
			t.Errorf("the system prompt must contain %q", want)
		}
	}
}

// TestTheSkillToolsAreReachableThroughTheDispatch: the tools were exercised through their
// methods, which proves the methods work but says nothing about whether the model can reach
// them. A typo in the switch would leave four working methods that no tool call ever finds.
func TestTheSkillToolsAreReachableThroughTheDispatch(t *testing.T) {
	lib := libOf(t, map[string]string{"nrf": "# NRF\n\nUse west to build.\n"})
	p := (&Planner{}).WithLibrary(lib)
	ctx := context.Background()

	for _, tc := range []struct {
		tool string
		args string
		want string
	}{
		{"list_skills", `{}`, "nrf"},
		{"search_skills", `{"query":"west build"}`, "nrf"},
		{"read_skill", `{"name":"nrf"}`, "Use west to build"},
		{"save_skill", `{"name":"brand new","body":"# Brand new\n\nSteps.\n"}`, "brand-new"},
	} {
		got := p.runTool(ctx, llm.ToolCall{
			Function: llm.FunctionCall{Name: tc.tool, Arguments: json.RawMessage(tc.args)},
		})
		if !strings.Contains(got, tc.want) {
			t.Errorf("the dispatch of %q returned %q, want it to contain %q", tc.tool, got, tc.want)
		}
	}
}

// TestAnUnknownToolIsRefusedThroughTheDispatch: the planner must say what it does not know
// rather than silently doing nothing, which is how a model learns to stop calling it.
func TestAnUnknownToolIsRefusedThroughTheDispatch(t *testing.T) {
	p := (&Planner{}).WithLibrary(libOf(t, nil))

	got := p.runTool(context.Background(), llm.ToolCall{
		Function: llm.FunctionCall{Name: "run_command", Arguments: json.RawMessage(`{}`)},
	})
	if !strings.Contains(got, "unknown tool") {
		t.Errorf("an unknown tool must be named as unknown: %q", got)
	}
}

// TestReadingASkillThatCannotBeReadIsReported: a document that exists but cannot be opened is
// not a missing skill, and the model must be told the difference — one means "look again", the
// other means "something is wrong with the library".
func TestReadingASkillThatCannotBeReadIsReported(t *testing.T) {
	// A DIRECTORY named like a skill is the reliable way to make a read fail: it exists, so
	// the lookup finds it, and reading it fails. The permission-bit approach depends on the
	// user, and a test that silently stops failing under a different user is worse than none.
	lib := skills.New(t.TempDir())
	dir := filepath.Join(lib.Dir, "locked.md")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := (&Planner{}).WithLibrary(lib)

	got := p.toolReadSkill(raw(`{"name":"locked"}`))
	if !strings.Contains(got, "Error") {
		t.Errorf("an unreadable skill must be reported: %q", got)
	}
	if strings.Contains(got, "No skill named") {
		t.Error("unreadable is not the same as missing, and saying so would send the model looking for a name")
	}
}

// TestWithUsageInstallsTheLedger: WithUsage must set the usage ledger and return
// the planner so it chains, the same pattern as the other With* builders.
func TestWithUsageInstallsTheLedger(t *testing.T) {
	l, err := usage.Open("")
	if err != nil {
		t.Fatalf("opening empty-path ledger: %v", err)
	}
	p := (&Planner{}).WithUsage(l)
	if p.usage != l {
		t.Fatal("WithUsage must store the ledger on the planner")
	}
	// WithUsage returns *Planner, so a chained call compiles and the ledger
	// survives into the chained builder.
	chained := (&Planner{}).WithUsage(l).WithLibrary(libOf(t, nil))
	if chained.usage != l {
		t.Fatal("WithUsage must chain: the ledger must survive into the next builder")
	}
}

// TestSetUsageReplacesTheLedger: SetUsage is the non-chaining setter used by
// the review fork after construction; it must overwrite the field.
func TestSetUsageReplacesTheLedger(t *testing.T) {
	first, err := usage.Open("")
	if err != nil {
		t.Fatalf("opening first ledger: %v", err)
	}
	second, err := usage.Open("")
	if err != nil {
		t.Fatalf("opening second ledger: %v", err)
	}
	p := (&Planner{}).WithUsage(first)
	if p.usage != first {
		t.Fatal("WithUsage must install the first ledger")
	}
	p.SetUsage(second)
	if p.usage != second {
		t.Fatal("SetUsage must replace the ledger")
	}
}

// TestWithReviewInstallsTheReview: WithReview must set the review fork and
// return the planner for chaining.
func TestWithReviewInstallsTheReview(t *testing.T) {
	r := &review.Review{}
	p := (&Planner{}).WithReview(r)
	if p.review != r {
		t.Fatal("WithReview must store the review on the planner")
	}
}

// TestReadSkillBumpsUsageWhenLedgerIsSet: when a usage ledger is installed,
// reading a skill must bump its view and use counters — the telemetry that
// drives curation. This is the branch toolReadSkill covers only when
// p.usage != nil.
func TestReadSkillBumpsUsageWhenLedgerIsSet(t *testing.T) {
	l, err := usage.Open(filepath.Join(t.TempDir(), "usage.json"))
	if err != nil {
		t.Fatalf("opening usage ledger: %v", err)
	}
	p := (&Planner{}).
		WithLibrary(libOf(t, map[string]string{"zephyr-build": "# Zephyr build\n\nwest\n"})).
		WithUsage(l)

	got := p.toolReadSkill(raw(`{"name":"zephyr-build"}`))
	if !strings.Contains(got, "west") {
		t.Fatalf("the procedure must still be returned:\n%s", got)
	}
	e := l.Get("zephyr-build")
	if e.ViewCount == 0 {
		t.Error("reading a skill must bump the view counter")
	}
	if e.UseCount == 0 {
		t.Error("reading a skill must bump the use counter")
	}
}

// TestSearchSkillsBumpsUsageWhenLedgerIsSet: when a usage ledger is installed,
// searching skills bumps the use counter for each hit — the same telemetry
// read_skill records, but for the index rather than the full procedure.
func TestSearchSkillsBumpsUsageWhenLedgerIsSet(t *testing.T) {
	l, err := usage.Open(filepath.Join(t.TempDir(), "usage.json"))
	if err != nil {
		t.Fatalf("opening usage ledger: %v", err)
	}
	p := (&Planner{}).
		WithLibrary(libOf(t, map[string]string{"nrf": "# NRF firmware\n\nwest and the SDK\n"})).
		WithUsage(l)

	got := p.toolSearchSkills(raw(`{"query":"firmware"}`))
	if !strings.Contains(got, "nrf") {
		t.Fatalf("the search must still return hits:\n%s", got)
	}
	e := l.Get("nrf")
	if e.UseCount == 0 {
		t.Error("searching skills must bump the use counter")
	}
}

// TestSaveSkillBumpsPatchUsageWhenLedgerIsSet: when a usage ledger is installed,
// saving a skill records the provenance marker — foreground for a user-directed
// save, agent when the review fork is doing it.
func TestSaveSkillBumpsPatchUsageWhenLedgerIsSet(t *testing.T) {
	t.Run("foreground", func(t *testing.T) {
		l, err := usage.Open(filepath.Join(t.TempDir(), "usage.json"))
		if err != nil {
			t.Fatalf("opening usage ledger: %v", err)
		}
		p := (&Planner{}).
			WithLibrary(skills.New(t.TempDir())).
			WithUsage(l)

		got := p.toolSaveSkill(raw(`{"name":"my-skill","body":"# My Skill\n\ndo stuff\n"}`))
		if !strings.Contains(got, "Saved") {
			t.Fatalf("the save must still succeed:\n%s", got)
		}
		e := l.Get("my-skill")
		if e.PatchCount == 0 {
			t.Error("saving a skill must bump the patch counter")
		}
		if e.CreatedBy != usage.ByForeground {
			t.Errorf("created_by = %q, want %q", e.CreatedBy, usage.ByForeground)
		}
	})

	t.Run("agent", func(t *testing.T) {
		l, err := usage.Open(filepath.Join(t.TempDir(), "usage.json"))
		if err != nil {
			t.Fatalf("opening usage ledger: %v", err)
		}
		p := (&Planner{}).
			WithLibrary(skills.New(t.TempDir())).
			WithUsage(l)
		p.IsReviewFork = true

		got := p.toolSaveSkill(raw(`{"name":"agent-skill","body":"# Agent Skill\n\ndo stuff\n"}`))
		if !strings.Contains(got, "Saved") {
			t.Fatalf("the save must still succeed:\n%s", got)
		}
		e := l.Get("agent-skill")
		if e.PatchCount == 0 {
			t.Error("saving a skill must bump the patch counter")
		}
		if e.CreatedBy != usage.ByAgent {
			t.Errorf("created_by = %q, want %q", e.CreatedBy, usage.ByAgent)
		}
	})
}

// TestRunTriggersReviewAfterFinalAnswer: when a review fork is installed, Run
// calls review.MaybeRun after the model delivers a final answer (no tool calls)
// during the normal loop.
func TestRunTriggersReviewAfterFinalAnswer(t *testing.T) {
	a, _ := makeAgent(t, true)
	engine := &fakeEngine{
		chunks: []llm.StreamChunk{{Event: llm.StreamText, Text: "here is my plan"}},
	}
	p := New(engine, a).WithReview(&review.Review{})
	out, err := p.Run(context.Background(), "question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "here is my plan" {
		t.Errorf("output = %q, want the model's answer", out)
	}
}

// TestRunTriggersReviewAfterForcedAnswer: when a review fork is installed and
// the model never stops calling tools, Run calls review.MaybeRun after the
// safety-net Complete call that forces the final answer.
func TestRunTriggersReviewAfterForcedAnswer(t *testing.T) {
	a, _ := makeAgent(t, true)
	call := llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "list_directory", Arguments: json.RawMessage(`{"path":"."}`)}}
	engine := &fakeEngine{
		chunks: []llm.StreamChunk{{Event: llm.StreamToolCall, Call: &call}},
	}
	p := New(engine, a).WithLoops(1).WithReview(&review.Review{})
	out, err := p.Run(context.Background(), "question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "forced answer" {
		t.Errorf("output = %q, want the forced answer", out)
	}
}
