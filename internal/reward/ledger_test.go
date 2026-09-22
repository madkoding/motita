package reward

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The reward signal is the USER's verdict and nothing else. These tests pin the three things
// that makes true: that no verdict means no movement, that a verdict lands on every skill the
// turn consulted in proportion to use, and that the user's own words survive to be acted on.

func ledgerAt(t *testing.T) *Ledger {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "scores.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

// --- nothing moves without the user --------------------------------------

func TestNoVerdictNoValue(t *testing.T) {
	// A ledger that was never given a verdict holds nothing. This is the whole policy: the
	// agent does not rate itself, so silence is silence and not a score.
	l := ledgerAt(t)

	if _, ok := l.Get("anything"); ok {
		t.Error("a skill with no verdict must have no score at all")
	}
	if v, ok := l.Value("anything"); ok || v != 0 {
		t.Errorf("no history must report as absent, got (%v, %v)", v, ok)
	}
}

func TestAVerdictWithNoSkillIsRefusedNotIgnored(t *testing.T) {
	// The caller must be told. A verdict that silently did nothing would make the feature look
	// like it works when there is nothing for it to learn from.
	l := ledgerAt(t)

	err := l.Attribute(nil, nil, true, "should have been good")
	if err != ErrNoSkill {
		t.Fatalf("expected ErrNoSkill, got %v", err)
	}
	if len(l.Scores) != 0 {
		t.Errorf("a refused verdict must change nothing, got %+v", l.Scores)
	}
}

func TestAVerdictWithCountsButNoNamesIsAlsoRefused(t *testing.T) {
	l := ledgerAt(t)
	if err := l.Attribute([]string{}, map[string]int{"ghost": 3}, false, "x"); err != ErrNoSkill {
		t.Fatalf("expected ErrNoSkill, got %v", err)
	}
}

// --- the value moves the right way ---------------------------------------

func TestGoodRaisesAndBadLowers(t *testing.T) {
	l := ledgerAt(t)

	if err := l.Attribute([]string{"build"}, map[string]int{"build": 1}, true, ""); err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	afterGood, _ := l.Value("build")
	if afterGood <= 0 {
		t.Errorf("a good verdict must raise the value, got %v", afterGood)
	}

	if err := l.Attribute([]string{"build"}, map[string]int{"build": 1}, false, ""); err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	afterBad, _ := l.Value("build")
	if afterBad >= afterGood {
		t.Errorf("a bad verdict must lower it: %v then %v", afterGood, afterBad)
	}
}

func TestTheValueStaysInRange(t *testing.T) {
	// The formula can only reach +/-1 asymptotically, so a long run of one verdict must not
	// drift past it: a value outside the range would break any ordering that assumes it.
	l := ledgerAt(t)
	for i := 0; i < 200; i++ {
		_ = l.Attribute([]string{"k"}, map[string]int{"k": 1}, true, "")
	}
	v, _ := l.Value("k")
	if v <= 0 || v > 1.0001 {
		t.Errorf("after 200 good verdicts the value is %v, outside (0, 1]", v)
	}
}

func TestRecentVerdictsWeighMore(t *testing.T) {
	// The decay is what stops a skill that worked long ago from staying on top forever.
	// Ten good verdicts and then one bad must leave it LOWER than ten good alone.
	l1 := ledgerAt(t)
	for i := 0; i < 10; i++ {
		_ = l1.Attribute([]string{"k"}, map[string]int{"k": 1}, true, "")
	}
	before, _ := l1.Value("k")

	l2 := ledgerAt(t)
	for i := 0; i < 10; i++ {
		_ = l2.Attribute([]string{"k"}, map[string]int{"k": 1}, true, "")
	}
	_ = l2.Attribute([]string{"k"}, map[string]int{"k": 1}, false, "")
	after, _ := l2.Value("k")

	if after >= before {
		t.Errorf("a recent bad verdict must pull the value down: %v then %v", before, after)
	}
	// ...but one bad turn must not wipe out ten good ones, or the score would be noise.
	if after < before*0.5 {
		t.Errorf("one bad verdict erased too much: %v then %v", before, after)
	}
}

// --- credit is shared, not duplicated ------------------------------------

func TestCreditIsSplitBetweenTheSkillsUsed(t *testing.T) {
	// A turn that read three skills must not move the ledger three times as far as a turn that
	// read one. Otherwise marking long tool chains would inflate the total.
	one := ledgerAt(t)
	_ = one.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")
	single, _ := one.Value("a")

	three := ledgerAt(t)
	_ = three.Attribute([]string{"a", "b", "c"}, map[string]int{"a": 1, "b": 1, "c": 1}, true, "")
	total := 0.0
	for _, n := range []string{"a", "b", "c"} {
		v, _ := three.Value(n)
		total += v
	}
	if total > single*1.01 {
		t.Errorf("the total credit must not grow with the number of skills: %v vs %v", total, single)
	}
}

func TestAHeavilyUsedSkillGetsMoreCredit(t *testing.T) {
	// Proportional, not equal: a turn that read one procedure five times leaned on it more
	// than on one it glanced at once.
	l := ledgerAt(t)
	_ = l.Attribute([]string{"main", "aside"}, map[string]int{"main": 5, "aside": 1}, true, "")

	main, _ := l.Value("main")
	aside, _ := l.Value("aside")
	if main <= aside {
		t.Errorf("the heavily used skill must gain more: main=%v aside=%v", main, aside)
	}
}

func TestASkillNamedWithNoCountStillCounts(t *testing.T) {
	// Defensive but reachable: a caller that reports the names and forgets the counts should
	// still get credit applied rather than a division by zero.
	l := ledgerAt(t)
	if err := l.Attribute([]string{"a"}, nil, true, ""); err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	v, ok := l.Value("a")
	if !ok || v <= 0 {
		t.Errorf("a named skill must gain something: (%v, %v)", v, ok)
	}
}

// --- the note is the actionable part -------------------------------------

func TestTheNoteIsKeptVerbatim(t *testing.T) {
	// The note is the user's own words about their own work, and it is what a repair is
	// written from. A paraphrase would lose the specific detail that makes the fix findable.
	l := ledgerAt(t)
	note := "the second step uses /dev/ttyUSB0 but my board enumerates as /dev/ttyACM0"
	if err := l.Attribute([]string{"flash"}, map[string]int{"flash": 1}, false, note); err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	s, _ := l.Get("flash")
	if len(s.Notes) != 1 {
		t.Fatalf("the note must be kept, got %+v", s.Notes)
	}
	if s.Notes[0].Text != note {
		t.Errorf("the note must be verbatim:\n got %q\nwant %q", s.Notes[0].Text, note)
	}
	if s.Notes[0].Good {
		t.Error("the note must remember it came with a bad verdict")
	}
}

func TestANoteIsAttachedToEverySkillOfTheTurn(t *testing.T) {
	// The user is describing the turn, and which of its skills is at fault is exactly what is
	// NOT known yet. Each skill carries the evidence; the score decides which is suspect.
	l := ledgerAt(t)
	_ = l.Attribute([]string{"a", "b"}, map[string]int{"a": 1, "b": 1}, false, "step 3 fails")

	for _, n := range []string{"a", "b"} {
		s, _ := l.Get(n)
		if len(s.Notes) != 1 || s.Notes[0].Text != "step 3 fails" {
			t.Errorf("%s must carry the note, got %+v", n, s.Notes)
		}
	}
}

func TestAnEmptyNoteIsNotRecorded(t *testing.T) {
	// A verdict with no words is a number only, and an empty note would show up as a blank
	// bullet in the report.
	l := ledgerAt(t)
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, false, "   ")
	s, _ := l.Get("a")
	if len(s.Notes) != 0 {
		t.Errorf("an empty note must not be stored, got %+v", s.Notes)
	}
}

func TestALongNoteIsTrimmedAndStaysValidUTF8(t *testing.T) {
	// A note is a sentence, not a report: past the cap it would crowd out the procedure it is
	// read next to. The trim must land on a rune boundary — cutting a multibyte character in
	// half would put invalid UTF-8 into a file that is read back into a prompt.
	l := ledgerAt(t)
	long := ""
	for i := 0; i < 400; i++ {
		long += "ñ"
	}
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, false, long)
	s, _ := l.Get("a")
	got := s.Notes[0].Text
	if len([]rune(got)) > noteCap+1 {
		t.Errorf("the note was not trimmed: %d runes", len([]rune(got)))
	}
	for _, r := range got {
		if r == '\uFFFD' {
			t.Error("the trim split a rune and produced invalid UTF-8")
		}
	}
}

func TestAMultiLineNoteStaysOneLine(t *testing.T) {
	l := ledgerAt(t)
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, false, "line one\nline two\tline three")
	s, _ := l.Get("a")
	if got := s.Notes[0].Text; got != "line one line two line three" {
		t.Errorf("a note must fold to one line, got %q", got)
	}
}

func TestOnlyTheMostRecentNotesAreKept(t *testing.T) {
	// The ledger must not grow without bound, and the recent notes are the ones a model can
	// still act on.
	l := ledgerAt(t)
	for i := 0; i < MaxNotes+4; i++ {
		_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, false, "note")
	}
	s, _ := l.Get("a")
	if len(s.Notes) != MaxNotes {
		t.Errorf("expected %d notes kept, got %d", MaxNotes, len(s.Notes))
	}
}

// --- the fix is tracked ---------------------------------------------------

func TestAddressedMarksEveryOutstandingNote(t *testing.T) {
	// A note whose fix has been attempted stops being handed out as work to do. Whether the
	// fix WORKED is the next verdict's business, not a checkbox.
	l := ledgerAt(t)
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, false, "broken")

	if got := len(l.mustGet(t, "a").Unaddressed()); got != 1 {
		t.Fatalf("precondition: expected 1 outstanding note, got %d", got)
	}
	if !l.Addressed("a") {
		t.Error("Addressed must report that it changed something")
	}
	if got := len(l.mustGet(t, "a").Unaddressed()); got != 0 {
		t.Errorf("the note must no longer be outstanding, got %d", got)
	}
	// Idempotent: a second call has nothing to mark.
	if l.Addressed("a") {
		t.Error("a second Addressed call must report no change")
	}
	// The note itself survives — it is history, and the report shows it as fixed.
	s := l.mustGet(t, "a")
	if len(s.Notes) != 1 || !s.Notes[0].Addressed {
		t.Errorf("the note must be kept and marked fixed, got %+v", s.Notes)
	}
}

func TestAddressedOnAnUnknownSkillIsFalse(t *testing.T) {
	l := ledgerAt(t)
	if l.Addressed("nobody") {
		t.Error("marking an unknown skill must report no change")
	}
}

func TestOutstandingListsOnlySkillsWithUnfixedReports(t *testing.T) {
	l := ledgerAt(t)
	_ = l.Attribute([]string{"broken"}, map[string]int{"broken": 1}, false, "does not work")
	_ = l.Attribute([]string{"fixed"}, map[string]int{"fixed": 1}, false, "used to fail")
	_ = l.Attribute([]string{"good"}, map[string]int{"good": 1}, true, "")
	l.Addressed("fixed")

	out := l.Outstanding()
	if len(out) != 1 || out[0].Name != "broken" {
		t.Fatalf("expected only the unfixed report, got %+v", out)
	}
}

func TestUnaddressedAndRecentOrder(t *testing.T) {
	l := ledgerAt(t)
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, false, "first")
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, false, "second")
	s := l.mustGet(t, "a")
	u := s.Unaddressed()
	if len(u) != 2 || u[0].Text != "second" {
		t.Errorf("unaddressed must be most-recent-first, got %+v", u)
	}
	// A GOOD verdict leaves a comment, not a complaint: telling the agent to repair a
	// procedure that just worked would send it to break something that is fine.
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "this worked well")
	if got := l.mustGet(t, "a").Unaddressed(); len(got) != 2 {
		t.Errorf("a good note must not become a complaint, got %+v", got)
	}
	// It is kept as history, and the report can show it.
	if got := l.mustGet(t, "a").Complaints(); len(got) != 2 {
		t.Errorf("Complaints must list only the bad notes, got %+v", got)
	}
	r := s.Recent(1)
	if len(r) != 1 || r[0].Text != "second" {
		t.Errorf("Recent(1) must return the newest, got %+v", r)
	}
	if got := s.Recent(99); len(got) != 2 {
		t.Errorf("Recent past the end must return what there is, got %d", len(got))
	}
	if got := (Score{}).Recent(2); got != nil {
		t.Errorf("Recent on an empty score must be nil, got %+v", got)
	}
}

// --- persistence ----------------------------------------------------------

func TestTheLedgerSurvivesARestart(t *testing.T) {
	// "Long term" is the point: the value must outlive the process that produced it.
	path := filepath.Join(t.TempDir(), "scores.json")
	l, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = l.Attribute([]string{"build"}, map[string]int{"build": 1}, true, "worked well")
	_ = l.Attribute([]string{"flaky"}, map[string]int{"flaky": 1}, false, "fails on the second step")
	if err := l.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	v, ok := again.Value("build")
	if !ok || v <= 0 {
		t.Errorf("the good verdict did not survive: (%v, %v)", v, ok)
	}
	s, ok := again.Get("flaky")
	if !ok || len(s.Notes) != 1 || s.Notes[0].Text != "fails on the second step" {
		t.Errorf("the note did not survive: %+v", s)
	}
}

func TestAnAbsentLedgerStartsEmpty(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "nothing-here.json"))
	if err != nil {
		t.Fatalf("a missing ledger is not an error: %v", err)
	}
	if len(l.Scores) != 0 {
		t.Errorf("expected an empty ledger, got %+v", l.Scores)
	}
}

func TestACorruptLedgerIsReportedNotReplaced(t *testing.T) {
	// Silently starting empty would hide that the history was lost, which is the whole value
	// of the feature — so a corrupt file is an error the caller has to see.
	path := filepath.Join(t.TempDir(), "scores.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("a corrupt ledger must be reported")
	}
}

func TestAnEmptyLedgerFileIsNotCorrupt(t *testing.T) {
	// A file created but never written is empty, which is not a parse error.
	path := filepath.Join(t.TempDir(), "scores.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Open(path)
	if err != nil {
		t.Fatalf("an empty file is a fresh ledger: %v", err)
	}
	if l.Scores == nil {
		t.Error("the map must be initialised")
	}
}

func TestAFileOfNullScoresIsTolerated(t *testing.T) {
	// JSON "null" unmarshals into a nil map, and every later write would panic on it.
	path := filepath.Join(t.TempDir(), "scores.json")
	if err := os.WriteFile(path, []byte(`{"scores":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, ""); err != nil {
		t.Fatalf("a nil map must have been replaced: %v", err)
	}
}

func TestSaveWithoutChangesDoesNotRewrite(t *testing.T) {
	// Nothing moved, so there is nothing to write: rewriting the file on every turn would put
	// a write on the disk for no reason.
	path := filepath.Join(t.TempDir(), "scores.json")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("an unchanged ledger must not create a file")
	}
}

func TestAnInMemoryLedgerSavesNothing(t *testing.T) {
	// Path empty means "no persistence", which is what an embedder or a test wants.
	l, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "x"); err != nil {
		t.Fatal(err)
	}
	if err := l.Save(); err != nil {
		t.Fatalf("Save on an in-memory ledger must be a no-op, got %v", err)
	}
}

func TestTheSavedFileIsReadableJSON(t *testing.T) {
	// The ledger is a file a person may open. It has to be readable and it has to carry the
	// counts and the notes, not only the number.
	path := filepath.Join(t.TempDir(), "scores.json")
	l, _ := Open(path)
	_ = l.Attribute([]string{"build"}, map[string]int{"build": 2}, false, "step two is wrong")
	if err := l.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Scores map[string]struct {
			Value float64 `json:"value"`
			Good  int     `json:"good"`
			Bad   int     `json:"bad"`
			Notes []struct {
				Text      string `json:"text"`
				Addressed bool   `json:"addressed"`
			} `json:"notes"`
		} `json:"scores"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("the saved ledger must be valid JSON: %v\n%s", err, data)
	}
	e := got.Scores["build"]
	if e.Value >= 0 || e.Bad != 1 || len(e.Notes) != 1 || e.Notes[0].Text != "step two is wrong" {
		t.Errorf("the file must carry the evidence, got %+v", e)
	}
}

func TestSortedPutsTheWorstFirst(t *testing.T) {
	// The report is read to find what needs attention, so the bottom of the ranking comes out
	// at the top of the list.
	l := ledgerAt(t)
	_ = l.Attribute([]string{"good"}, map[string]int{"good": 1}, true, "")
	_ = l.Attribute([]string{"bad"}, map[string]int{"bad": 1}, false, "")
	_ = l.Attribute([]string{"mid"}, map[string]int{"mid": 1}, true, "")

	sorted := l.Sorted()
	if len(sorted) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(sorted))
	}
	if sorted[0].Name != "good" || sorted[2].Name != "bad" {
		t.Errorf("expected best first, got %v, %v, %v", sorted[0].Name, sorted[1].Name, sorted[2].Name)
	}
}

func TestSortedBreaksTiesByName(t *testing.T) {
	// Deterministic output: two skills the same value must not swap places between runs, or
	// the report a user reads would shuffle for no reason.
	l := ledgerAt(t)
	_ = l.Attribute([]string{"zeta"}, map[string]int{"zeta": 1}, true, "")
	_ = l.Attribute([]string{"alpha"}, map[string]int{"alpha": 1}, true, "")

	sorted := l.Sorted()
	if sorted[0].Name != "alpha" || sorted[1].Name != "zeta" {
		t.Errorf("equal values must sort by name, got %v then %v", sorted[0].Name, sorted[1].Name)
	}
}

func TestUseCountsAddUp(t *testing.T) {
	l := ledgerAt(t)
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, false, "")
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")
	s, _ := l.Get("a")
	if s.Uses() != 3 || s.Good != 2 || s.Bad != 1 {
		t.Errorf("the counts must add up: %+v", s)
	}
}

func TestAttributeUsesTheInjectedClock(t *testing.T) {
	// The clock is injected so the timestamps can be asserted instead of assumed, which is what
	// makes "last used" testable.
	l := ledgerAt(t)
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	l.Now = func() time.Time { return when }
	// A note is passed so both timestamps exist: with no note there is nothing to stamp, and
	// indexing it would panic — which is what the first version of this test did.
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "worked")
	s, _ := l.Get("a")
	if !s.Updated.Equal(when) {
		t.Errorf("Updated = %v, want %v", s.Updated, when)
	}
	if len(s.Notes) != 1 {
		t.Fatalf("the note must be stored, got %+v", s.Notes)
	}
	if !s.Notes[0].At.Equal(when) {
		t.Errorf("note At = %v, want %v", s.Notes[0].At, when)
	}
}

// mustGet fetches a score that a test has just written, failing loudly if it is missing.
func (l *Ledger) mustGet(t *testing.T, name string) Score {
	t.Helper()
	s, ok := l.Get(name)
	if !ok {
		t.Fatalf("no score for %q", name)
	}
	return s
}
