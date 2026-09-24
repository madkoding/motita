package plan

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/reward"
	"github.com/madkoding/motita/internal/skills"
)

// The reward changes two things and must not change a third: it breaks ties by experience, it
// shows the agent the user's own complaints so a fix can be written, and it NEVER outranks
// relevance. Each of those is asserted here.

// rewardPlanner builds a planner with a library of named skills and a ledger.
func rewardPlanner(t *testing.T, bodies map[string]string) (*Planner, *reward.Ledger) {
	t.Helper()
	lib := skills.New(t.TempDir())
	for name, body := range bodies {
		if _, err := lib.Save(name, body); err != nil {
			t.Fatalf("Save(%s): %v", name, err)
		}
	}
	led, err := reward.Open("") // in-memory
	if err != nil {
		t.Fatal(err)
	}
	lib.Scorer = led
	p := &Planner{library: lib, reward: led}
	return p, led
}

// --- relevance still comes first -----------------------------------------

func TestValueDoesNotOutrankRelevance(t *testing.T) {
	// The failure mode of every popularity ranking: return whatever has been used most. A skill
	// that has always worked is still the wrong answer to a question it is not about.
	p, led := rewardPlanner(t, map[string]string{
		"about-zephyr":    "# Zephyr build\nHow to build the zephyr firmware.\n",
		"about-unrelated": "# Sourdough\nHow to bake bread with a zephyr of flour.\n",
	})
	// The unrelated one is loaded with credit...
	for i := 0; i < 10; i++ {
		_ = led.Attribute([]string{"about-unrelated"}, map[string]int{"about-unrelated": 1}, true, "")
	}
	// ...and the relevant one has none.

	hits, err := p.library.Search("zephyr build firmware", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 1 {
		t.Fatal("expected a match")
	}
	if hits[0].Name != "about-zephyr" {
		t.Errorf("relevance must win over accumulated value, got %q first", hits[0].Name)
	}
}

func TestValueBreaksATieBetweenEquals(t *testing.T) {
	// The case the score exists for: two skills the text cannot separate. This is where
	// experience is the only signal there is.
	p, led := rewardPlanner(t, map[string]string{
		"proven":   "# Flash board\nHow to flash the board over serial.\n",
		"unproven": "# Flash board\nHow to flash the board over serial.\n",
	})

	// Before any verdict the tie is broken by name, deterministically.
	before, err := p.library.Search("flash board serial", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 {
		t.Fatalf("expected both skills, got %d", len(before))
	}

	_ = led.Attribute([]string{"unproven"}, map[string]int{"unproven": 1}, true, "")

	after, err := p.library.Search("flash board serial", 5)
	if err != nil {
		t.Fatal(err)
	}
	if after[0].Name != "unproven" {
		t.Errorf("the skill with good verdicts must win the tie, got %q first", after[0].Name)
	}
}

func TestABadValueLosesATie(t *testing.T) {
	p, led := rewardPlanner(t, map[string]string{
		"failed": "# Flash board\nHow to flash the board over serial.\n",
		"other":  "# Flash board\nHow to flash the board over serial.\n",
	})
	_ = led.Attribute([]string{"failed"}, map[string]int{"failed": 1}, false, "step 2 is wrong")

	hits, err := p.library.Search("flash board serial", 5)
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].Name != "other" {
		t.Errorf("the skill with a bad verdict must lose the tie, got %q first", hits[0].Name)
	}
}

func TestWithNoLedgerTheOrderIsUnchanged(t *testing.T) {
	// The feature is optional: a planner with no ledger must rank exactly as it did before,
	// which is what an embedder that does not want it gets.
	lib := skills.New(t.TempDir())
	for _, n := range []string{"bravo", "alpha"} {
		if _, err := lib.Save(n, "# Flash board\nHow to flash the board.\n"); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := lib.Search("flash board", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Name != "alpha" {
		t.Errorf("with no scorer the order must be by name, got %+v", names(hits))
	}
}

// --- the agent sees the number, and the complaint ------------------------

func TestTheIndexShowsTheValueAndTheCounts(t *testing.T) {
	// The user chose the bare numbers: "used 4, value 0.7". No adjectives, because a label is
	// this program's interpretation presented as evidence.
	p, led := rewardPlanner(t, map[string]string{
		"zephyr-build": "# Zephyr build\nHow to build it.\n",
	})
	for i := 0; i < 3; i++ {
		_ = led.Attribute([]string{"zephyr-build"}, map[string]int{"zephyr-build": 1}, true, "")
	}

	got := p.toolListSkills()
	if !strings.Contains(got, "[used 3, value +") {
		t.Errorf("the index must show the value with its counts:\n%s", got)
	}
	// And no interpretation: a word like RELIABLE would be the program's judgement.
	for _, label := range []string{"RELIABLE", "BAD", "GOOD", "UNPROVEN", "TRUST"} {
		if strings.Contains(got, label) {
			t.Errorf("the listing must not carry the label %q:\n%s", label, got)
		}
	}
}

func TestASkillWithNoHistoryShowsNoValue(t *testing.T) {
	// "0.0" would read as "this failed". A skill nobody has judged is not a failed skill.
	p, _ := rewardPlanner(t, map[string]string{"fresh": "# Fresh\nNothing yet.\n"})
	got := p.toolListSkills()
	if strings.Contains(got, "value") {
		t.Errorf("a skill with no verdicts must show no value at all:\n%s", got)
	}
}

func TestTheOutstandingComplaintIsShownToTheAgent(t *testing.T) {
	// This is what makes a verdict actionable rather than just a demotion: the model is told
	// what the user said, in the user's words, and asked to repair the procedure.
	p, led := rewardPlanner(t, map[string]string{
		"flash": "# Flash board\nHow to flash the board.\n",
	})
	note := "step 2 uses /dev/ttyUSB0 but my board shows up as /dev/ttyACM0"
	_ = led.Attribute([]string{"flash"}, map[string]int{"flash": 1}, false, note)

	got := p.toolSearchSkills(rawArgs(t, map[string]any{"query": "flash board"}))
	if !strings.Contains(got, note) {
		t.Errorf("the user's note must reach the agent verbatim:\n%s", got)
	}
	if !strings.Contains(strings.ToLower(got), "not been revised") {
		t.Errorf("the note must say the procedure has not been fixed yet:\n%s", got)
	}
	if !strings.Contains(got, "save_skill") {
		t.Errorf("the agent must be pointed at the tool that fixes it:\n%s", got)
	}
}

func TestAFixedSkillIsNotNaggedAboutAgain(t *testing.T) {
	// Once the procedure has been rewritten the complaint is answered: repeating it would send
	// the model to re-fix what has already been fixed.
	p, led := rewardPlanner(t, map[string]string{
		"flash": "# Flash board\nHow to flash the board.\n",
	})
	_ = led.Attribute([]string{"flash"}, map[string]int{"flash": 1}, false, "step 2 is wrong")
	led.Addressed("flash")

	got := p.toolSearchSkills(rawArgs(t, map[string]any{"query": "flash board"}))
	if strings.Contains(got, "step 2 is wrong") {
		t.Errorf("a fixed complaint must no longer be shown:\n%s", got)
	}
	// The value is still shown: the verdict stands, only the complaint is answered.
	if !strings.Contains(got, "value") {
		t.Errorf("the value must still be shown after a fix:\n%s", got)
	}
}

func TestAGoodVerdictCarriesNoRepairInstruction(t *testing.T) {
	// "Fix this" makes no sense about something that worked. A good verdict with a note still
	// records the note, and is not presented as a complaint.
	p, led := rewardPlanner(t, map[string]string{"a": "# A\nDoes a thing.\n"})
	_ = led.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "esto estuvo bien")

	got := p.toolSearchSkills(rawArgs(t, map[string]any{"query": "does a thing"}))
	if strings.Contains(strings.ToLower(got), "not been revised") {
		t.Errorf("a good verdict is not a complaint:\n%s", got)
	}
}

func TestTheComplaintSuffixIsEmptyWithoutALedger(t *testing.T) {
	lib := skills.New(t.TempDir())
	if _, err := lib.Save("a", "# A\nBody.\n"); err != nil {
		t.Fatal(err)
	}
	p := &Planner{library: lib}
	if got := p.feedbackSuffix("a"); got != "" {
		t.Errorf("with no ledger there is nothing to report, got %q", got)
	}
	if got := p.historySuffix("a"); got != "" {
		t.Errorf("with no ledger there is no value, got %q", got)
	}
}

func TestTheSuffixesAreEmptyForAnUnknownSkill(t *testing.T) {
	p, _ := rewardPlanner(t, map[string]string{"a": "# A\nBody.\n"})
	if got := p.historySuffix("nobody"); got != "" {
		t.Errorf("an unknown skill has no value, got %q", got)
	}
	if got := p.feedbackSuffix("nobody"); got != "" {
		t.Errorf("an unknown skill has no complaint, got %q", got)
	}
}

func TestASkillWithVerdictsButNoComplaintEndsCleanly(t *testing.T) {
	// A good verdict leaves a value and no complaint, so the suffix must be empty rather than
	// an empty "!!" header.
	p, led := rewardPlanner(t, map[string]string{"a": "# A\nBody.\n"})
	_ = led.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")
	if got := p.feedbackSuffix("a"); got != "" {
		t.Errorf("no outstanding complaint means no suffix, got %q", got)
	}
}

// --- the attribution ------------------------------------------------------

func TestReadingASkillIsRecorded(t *testing.T) {
	// The moment the credit becomes knowable is the read. Without this a verdict has nothing to
	// land on, and the feature silently does nothing.
	p, _ := rewardPlanner(t, map[string]string{"flash": "# Flash\nBody.\n"})

	_ = p.toolReadSkill(rawArgs(t, map[string]any{"name": "flash"}))
	used := p.Consulted()
	if used["flash"] != 1 {
		t.Errorf("reading a skill must record one use, got %+v", used)
	}

	// Reading it again counts again: the turn leaned on it twice.
	_ = p.toolReadSkill(rawArgs(t, map[string]any{"name": "flash"}))
	if used := p.Consulted(); used["flash"] != 2 {
		t.Errorf("a second read must count again, got %+v", used)
	}
}

func TestAFailedReadIsNotRecorded(t *testing.T) {
	// A skill that was asked for and does not exist was not relied on: crediting it would put
	// value on a typo.
	p, _ := rewardPlanner(t, map[string]string{"real": "# Real\nBody.\n"})
	_ = p.toolReadSkill(rawArgs(t, map[string]any{"name": "imaginary"}))

	if got := p.Consulted(); len(got) != 0 {
		t.Errorf("a missing skill must not be recorded, got %+v", got)
	}
}

func TestConsultedIsACopy(t *testing.T) {
	// The caller must not be able to mutate the planner's record through the map it is handed,
	// or a verdict would be applied to something the turn never read.
	p, _ := rewardPlanner(t, map[string]string{"flash": "# F\nBody.\n"})
	_ = p.toolReadSkill(rawArgs(t, map[string]any{"name": "flash"}))

	got := p.Consulted()
	got["flash"] = 999
	if p.Consulted()["flash"] != 1 {
		t.Error("Consulted must return a copy, not the map itself")
	}
}

func TestConsultedIsNilWhenNothingWasRead(t *testing.T) {
	p, _ := rewardPlanner(t, map[string]string{"a": "# A\nBody.\n"})
	if got := p.Consulted(); got != nil {
		t.Errorf("a turn that read nothing must report nothing, got %+v", got)
	}
}

func TestALedgerCanBeInstalledAfterTheFact(t *testing.T) {
	// WithReward is what the runner uses, and it must work on a planner built without one.
	lib := skills.New(t.TempDir())
	led, _ := reward.Open("")
	p := (&Planner{library: lib}).WithReward(led)
	if p.reward != led {
		t.Error("WithReward must install the ledger")
	}
}

// rawArgs encodes tool arguments the way the model sends them, so the tests drive the same
// entry points the dispatch does rather than calling the internals directly.
func rawArgs(t *testing.T, in map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("encoding arguments: %v", err)
	}
	return b
}

// names lists the skill names of a result, for a readable failure message.
func names(hits []skills.Skill) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Name)
	}
	return out
}
