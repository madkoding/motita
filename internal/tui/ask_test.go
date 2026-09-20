package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/session"
)

// askItems is a small set with the shapes that matter: one with options, one whose answer is
// open (no options), and one with an assumption to fall back on.
func askItems() []agent.AskItem {
	return []agent.AskItem{
		{Text: "¿qué carpeta?", Assumption: "la actual",
			Options: []string{"la actual", "/tmp", "todo el proyecto"}},
		{Text: "¿y qué quieres conseguir?", Assumption: ""},
		{Text: "¿lo borro o lo muevo?", Assumption: "moverlo",
			Options: []string{"borrarlo", "moverlo"}},
	}
}

// seen renders the window and returns it with the colour codes removed, which is what the user
// actually reads. Asserting on the raw frame would fail on a correct row whose label carries a
// colour, and pass on a row whose text is split by one — the question and the labels are both
// coloured, so the raw check is simply the wrong comparison.
func seen(tui *TUI) string {
	return stripANSI(strings.Join(tui.askLines(0), "\n"))
}

func newAskTUI(t *testing.T, items []agent.AskItem, origin string) *TUI {
	t.Helper()
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: &stubRunner{}}
	tui.ask = newAsk(items, origin)
	if tui.ask != nil {
		if i := tui.ask.firstUnanswered(); i >= 0 {
			tui.ask.cur = i
		}
	}
	return tui
}

// --- opening and closing ----------------------------------------------------

// The window opens only when the agent actually asked. An empty list is not a question, and
// opening a window on it would show a blank panel with nothing to answer.
func TestWindowOpensOnlyWithQuestions(t *testing.T) {
	if a := newAsk(nil, "x"); a != nil {
		t.Fatalf("no questions should not open a window, got %+v", a)
	}
	tui := &TUI{}
	if tui.asking() {
		t.Fatal("a TUI with no ask state is not asking")
	}
	if rows := tui.askRows(); rows != 0 {
		t.Fatalf("a closed window takes no rows, got %d", rows)
	}
	if lines := tui.askLines(0); lines != nil {
		t.Fatalf("a closed window draws nothing, got %q", lines)
	}
}

func TestWindowOpensWithQuestions(t *testing.T) {
	tui := newAskTUI(t, askItems(), "revisa el proyecto")
	if !tui.asking() {
		t.Fatal("the window should be open")
	}
	if rows := tui.askRows(); rows < 4 {
		t.Fatalf("a window with a question and its options needs several rows, got %d", rows)
	}
	lines := tui.askLines(0)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"¿qué carpeta?", "1) la actual", "respuesta:"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the window should show %q, got:\n%s", want, joined)
		}
	}
}

// A single question is a list of one: the same shape, no special case, and nothing to navigate.
func TestSingleQuestionHasNoNavigation(t *testing.T) {
	tui := newAskTUI(t, []agent.AskItem{{Text: "¿cuál?", Options: []string{"a", "b"}}}, "x")
	joined := seen(tui)
	if strings.Contains(joined, "pregunta 1 de 1") {
		t.Fatalf("a single question should not announce its position:\n%s", joined)
	}
	if strings.Contains(joined, "cambia de pregunta") {
		t.Fatalf("a single question has nowhere to navigate:\n%s", joined)
	}
	if !strings.Contains(joined, "¿cuál?") {
		t.Fatalf("the question should still be shown:\n%s", joined)
	}
}

// Several questions DO announce where the user is, or "next" has no meaning before pressing it.
func TestSeveralQuestionsShowPosition(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	joined := seen(tui)
	if !strings.Contains(joined, "pregunta 1 de 3") {
		t.Fatalf("the position should be shown:\n%s", joined)
	}
	if !strings.Contains(joined, "faltan 3") {
		t.Fatalf("the pending count should be shown:\n%s", joined)
	}
}

// --- picking ---------------------------------------------------------------

// Picking an option answers the question and moves on, so answering a list is a sequence of
// keystrokes rather than a sequence of navigations.
func TestPickAnswersAndAdvances(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	if !tui.ask.pick(1) {
		t.Fatal("picking a listed option should work")
	}
	if got := tui.ask.answers[0]; got != "/tmp" {
		t.Fatalf("answer = %q, want /tmp", got)
	}
	if tui.ask.cur != 1 {
		t.Fatalf("should advance to the next unanswered question, cur = %d", tui.ask.cur)
	}
}

// An out-of-range pick is refused rather than silently answering with whatever is at that
// index: a key that does nothing visible must not record an answer.
func TestPickOutOfRangeIsRefused(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	if tui.ask.pick(-1) {
		t.Fatal("a negative index must not pick")
	}
	if tui.ask.pick(9) {
		t.Fatal("an index past the options must not pick")
	}
	if tui.ask.answers[0] != "" {
		t.Fatalf("nothing should have been answered, got %q", tui.ask.answers[0])
	}
	// A question with no options cannot be picked at all, which is why the free-answer row
	// always exists.
	tui.ask.cur = 1
	if tui.ask.pick(0) {
		t.Fatal("a question with no options has nothing to pick")
	}
}

// --- typed answers ---------------------------------------------------------

func TestTypedAnswerIsRecorded(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	if !tui.ask.answer("  el escritorio  ") {
		t.Fatal("a typed answer should be accepted")
	}
	if got := tui.ask.answers[0]; got != "el escritorio" {
		t.Fatalf("answer = %q, want it trimmed", got)
	}
}

// A blank answer is not an answer: it would count as answered while saying nothing, and the
// confirmation would send an empty value to the question the agent asked for.
func TestBlankAnswerIsRefused(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	if tui.ask.answer("   ") {
		t.Fatal("a blank answer must be refused")
	}
	if tui.ask.answers[0] != "" {
		t.Fatalf("nothing should have been recorded, got %q", tui.ask.answers[0])
	}
}

// --- navigation ------------------------------------------------------------

// Next skips questions that are already answered, so a user who answered the first and third
// does not land back on them while moving forward.
func TestNavigationPrefersUnanswered(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	tui.ask.answers[1] = "ya respondida"
	tui.ask.cur = 0
	tui.ask.next()
	if tui.ask.cur != 2 {
		t.Fatalf("next should skip the answered one, cur = %d", tui.ask.cur)
	}
	tui.ask.prev()
	if tui.ask.cur != 0 {
		t.Fatalf("prev should skip the answered one going back, cur = %d", tui.ask.cur)
	}
}

// Once everything is answered, movement is plain: the user is reviewing, and jumping around
// their answers would be worse than walking through them in order.
func TestNavigationWrapsWhenAllAnswered(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	for i := range tui.ask.answers {
		tui.ask.answers[i] = "algo"
	}
	tui.ask.cur = 0
	tui.ask.next()
	if tui.ask.cur != 1 {
		t.Fatalf("with everything answered next moves by one, cur = %d", tui.ask.cur)
	}
	tui.ask.cur = 0
	tui.ask.prev()
	if tui.ask.cur != 2 {
		t.Fatalf("prev should wrap to the end, cur = %d", tui.ask.cur)
	}
}

// An empty question list has nothing to move to. It is unreachable through the window, which
// only opens with at least one question, but the guard is what keeps the modulo from dividing
// by zero if the state is ever built another way.
func TestNavigationWithNoQuestionsDoesNothing(t *testing.T) {
	a := &askState{}
	a.move(1)
	if a.cur != 0 {
		t.Fatalf("moving an empty list must not change the index, cur = %d", a.cur)
	}
}

func TestFirstUnanswered(t *testing.T) {
	a := &askState{items: askItems(), answers: make([]string, 3), free: make([]string, 3)}
	if got := a.firstUnanswered(); got != 0 {
		t.Fatalf("firstUnanswered = %d, want 0", got)
	}
	a.answers[0] = "x"
	if got := a.firstUnanswered(); got != 1 {
		t.Fatalf("firstUnanswered = %d, want 1", got)
	}
	for i := range a.answers {
		a.answers[i] = "x"
	}
	if got := a.firstUnanswered(); got != -1 {
		t.Fatalf("firstUnanswered = %d, want -1 when all are answered", got)
	}
}

func TestAnswerCounts(t *testing.T) {
	a := &askState{items: askItems(), answers: make([]string, 3), free: make([]string, 3)}
	if a.allAnswered() {
		t.Fatal("nothing answered yet")
	}
	if got := a.unanswered(); got != 3 {
		t.Fatalf("unanswered = %d, want 3", got)
	}
	for i := range a.answers {
		a.answers[i] = "x"
	}
	if !a.allAnswered() {
		t.Fatal("everything answered")
	}
	if got := a.unanswered(); got != 0 {
		t.Fatalf("unanswered = %d, want 0", got)
	}
}

// --- keys ------------------------------------------------------------------

// A digit picks, and the number is the one drawn next to the option.
func TestDigitKeyPicksOption(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	if !tui.handleAskKey(context.Background(), "3") {
		t.Fatal("the digit should be handled")
	}
	if got := tui.ask.answers[0]; got != "todo el proyecto" {
		t.Fatalf("answer = %q, want the third option", got)
	}
}

// A digit that matches no option is still consumed: it was meant for the window, and letting it
// fall through would send it to the agent as a message.
func TestDigitWithNoOptionIsStillCaptured(t *testing.T) {
	tui := newAskTUI(t, []agent.AskItem{{Text: "¿cuál?", Options: []string{"a"}}}, "x")
	if !tui.handleAskKey(context.Background(), "7") {
		t.Fatal("a digit must be captured even when it picks nothing")
	}
	if tui.ask.answers[0] != "" {
		t.Fatalf("nothing should be answered, got %q", tui.ask.answers[0])
	}
}

// The arrows move between questions. They cannot be Tab: Tab is the Task/Plan switch, and a
// key that both navigated questions and changed mode would do two things at once.
func TestArrowKeysNavigate(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	if !tui.handleAskKey(context.Background(), keyRight) {
		t.Fatal("right should be handled")
	}
	if tui.ask.cur != 1 {
		t.Fatalf("right should move forward, cur=%d", tui.ask.cur)
	}
	if !tui.handleAskKey(context.Background(), keyLeft) {
		t.Fatal("left should be handled")
	}
	if tui.ask.cur != 0 {
		t.Fatalf("left should move back, cur=%d", tui.ask.cur)
	}
}

// Escape closes the window and gives the keys back to the input, which is the way out for a
// user who would rather write their own answer.
func TestEscapeClosesTheWindow(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	if !tui.handleAskKey(context.Background(), keyEsc) {
		t.Fatal("escape should be handled")
	}
	if tui.asking() {
		t.Fatal("escape should close the window")
	}
}

// A key the window does not use is left for the input, so ordinary typing is not swallowed.
func TestUnusedKeyIsNotHandled(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	if tui.handleAskKey(context.Background(), "h") {
		t.Fatal("a letter is not a window key and must reach the input")
	}
	if tui.ask == nil {
		t.Fatal("the window should still be open")
	}
}

// --- confirming ------------------------------------------------------------

// Confirming closes the window and clears the draft: the answers have left, and leaving the
// window up would invite the user to answer questions already sent.
func TestConfirmClosesTheWindow(t *testing.T) {
	items := []agent.AskItem{{Text: "q1"}, {Text: "q2"}}
	tui := newAskTUI(t, items, "la petición original")
	tui.ask.answers[0] = "r1"
	tui.ask.answers[1] = "r2"
	tui.ask = nil // as confirmAsk leaves it; the send itself needs a real runner
	if tui.asking() {
		t.Fatal("confirming closes the window")
	}
}

// Confirming with an unanswered question is allowed: the agent stated what it would assume, so
// leaving one unanswered is accepting that assumption rather than dropping the question.
func TestConfirmLeavesUnansweredOutOfTheAnswers(t *testing.T) {
	a := &askState{items: askItems(), answers: make([]string, 3), free: make([]string, 3)}
	a.answers[0] = "r1"
	res := a.result()
	if len(res) != 1 {
		t.Fatalf("only answered questions are sent, got %d", len(res))
	}
	if res[0].Question != "¿qué carpeta?" || res[0].Answer != "r1" {
		t.Fatalf("the pair should keep its question, got %+v", res[0])
	}
}

// A typed draft that was never confirmed with Enter is still an answer when the user confirms:
// it is on screen, and dropping it would lose what they wrote.
func TestDraftIsTakenOnConfirm(t *testing.T) {
	items := []agent.AskItem{{Text: "q1"}}
	tui := newAskTUI(t, items, "origen")
	tui.draft = "  escrito a mano  "
	tui.confirmAsk()
	if got := tui.ask; got != nil {
		t.Fatal("confirming should close the window")
	}
	if tui.draft != "" {
		t.Fatalf("the draft should be cleared, got %q", tui.draft)
	}
}

// --- composing the reply ---------------------------------------------------

// The answers go back attached to their questions, so the agent re-reads the request with the
// gaps filled instead of receiving an unrelated message.
func TestComposeAnswersAttachesToQuestions(t *testing.T) {
	got := composeAnswers("revisa el proyecto", []agent.Answers{
		{Question: "¿qué carpeta?", Answer: "/tmp"},
	})
	for _, want := range []string{"revisa el proyecto", "¿qué carpeta?", "/tmp"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the reply should contain %q, got:\n%s", want, got)
		}
	}
}

// With nothing answered the request goes back unchanged: an empty turn would be silence, and
// the agent could not tell it apart from the user saying nothing.
func TestComposeAnswersWithNoAnswersKeepsTheRequest(t *testing.T) {
	got := composeAnswers("solo la petición", nil)
	if got != "solo la petición" {
		t.Fatalf("got %q, want the request unchanged", got)
	}
	if q := composeAnswers("", nil); q != "" {
		t.Fatalf("nothing at all composes to nothing, got %q", q)
	}
}

// --- drawing ---------------------------------------------------------------

// The answer row is always drawn, even with no options: it is where a free answer goes, and it
// is what makes an open question answerable at all.
func TestAnswerRowIsAlwaysDrawn(t *testing.T) {
	tui := newAskTUI(t, []agent.AskItem{{Text: "¿qué quieres conseguir?"}}, "x")
	joined := seen(tui)
	if !strings.Contains(joined, "respuesta:") {
		t.Fatalf("the answer row must exist with no options:\n%s", joined)
	}
}

// A picked option is shown as the answer, so what will be sent is visible before confirming.
func TestAnswerRowShowsThePickedOption(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	tui.ask.answers[0] = "/tmp"
	joined := seen(tui)
	if !strings.Contains(joined, "respuesta: /tmp") {
		t.Fatalf("the answer row should show the pick:\n%s", joined)
	}
	if !strings.Contains(joined, "* 2) /tmp") {
		t.Fatalf("the picked option should be marked:\n%s", joined)
	}
}

// The assumption is shown next to its question: it is what lets the user confirm in one word
// instead of writing the request again.
func TestAssumptionIsShown(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	joined := seen(tui)
	if !strings.Contains(joined, "si no respondes: la actual") {
		t.Fatalf("the assumption should be shown:\n%s", joined)
	}
}

// A window taller than the space it was given is cut, and the cut SAYS SO: a silently truncated
// list of options looks complete, and the user would not know there was more.
func TestWindowIsCappedAndSaysSo(t *testing.T) {
	items := []agent.AskItem{{Text: "¿cuál?", Options: []string{"a", "b", "c", "d"}}}
	tui := newAskTUI(t, items, "x")
	full := tui.askRows()
	capped := tui.askLines(3)
	if len(capped) != 3 {
		t.Fatalf("a cap of 3 must return 3 rows, got %d", len(capped))
	}
	if !strings.Contains(stripANSI(strings.Join(capped, "\n")), "más") {
		t.Fatalf("a cut window must say there is more:\n%s", stripANSI(strings.Join(capped, "\n")))
	}
	if full <= 3 {
		t.Fatalf("this case should not fit in 3 rows, it needs %d", full)
	}
}

// --- the layout reservation ------------------------------------------------

// The window takes the popup's reservation and the popup does not draw under it. Both drawing
// would make the frame taller than the layout measured, and the interface would scroll on a
// keypress.
func TestWindowTakesThePopupReservation(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	tui.draft = "/"
	if got := tui.popupRows(); got != tui.askRows() {
		t.Fatalf("the reservation should be the window's, got %d want %d", got, tui.askRows())
	}
	lines := stripANSI(strings.Join(tui.composerLinesCapped(0), "\n"))
	if strings.Contains(lines, "/quit") {
		t.Fatalf("the completion popup must not draw under the window:\n%s", lines)
	}
	if !strings.Contains(lines, "¿qué") {
		t.Fatalf("the window should draw:\n%s", lines)
	}
	// The input box stays, and it stays BELOW the window: it is where a free answer is written.
	if !strings.Contains(lines, "respuesta:") {
		t.Fatalf("the answer row should be drawn with the input:\n%s", lines)
	}
}

// --- the runner round trip -------------------------------------------------

// The questions are kept on the runner because the agent that asked is discarded when its run
// returns. Taking them CLEARS them: a repaint must not reopen a window the user closed.
func TestPendingQuestionsAreTakenOnce(t *testing.T) {
	r := &AppRunner{}
	items := askItems()
	r.setPendingQuestions(items, "el origen")

	got, origin := r.TakePendingQuestions()
	if len(got) != len(items) || origin != "el origen" {
		t.Fatalf("got %d questions and origin %q", len(got), origin)
	}
	again, _ := r.TakePendingQuestions()
	if again != nil {
		t.Fatalf("the questions must be taken only once, got %d", len(again))
	}
}

// A runner that cannot ask keeps working: the interface checks the capability by assertion, so
// a test double without it is a first-class case rather than a broken one.
func TestOpenAskWithNoQuestionsDoesNothing(t *testing.T) {
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: &stubRunner{}}
	tui.openAskIfPending()
	if tui.asking() {
		t.Fatal("a runner that does not report questions must not open the window")
	}
}

// --- the whole cycle -------------------------------------------------------

// The full path a user takes: the agent asks, the window opens on the first unanswered question,
// a digit answers it, and confirming hands the pairs back.
func TestFullCycle(t *testing.T) {
	r := &AppRunner{}
	r.setPendingQuestions(askItems(), "revisa el proyecto")
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: r}

	tui.openAskIfPending()
	if !tui.asking() {
		t.Fatal("the window should have opened on the pending questions")
	}
	if tui.ask.cur != 0 {
		t.Fatalf("it opens on the first unanswered question, cur = %d", tui.ask.cur)
	}

	// Answer the first by picking, the second by writing.
	if !tui.handleAskKey(context.Background(), "2") {
		t.Fatal("the digit should be handled")
	}
	if tui.ask.cur != 1 {
		t.Fatalf("picking should advance to the open question, cur = %d", tui.ask.cur)
	}
	if !tui.ask.answer("un informe") {
		t.Fatal("the typed answer should be accepted")
	}

	res := tui.ask.result()
	if len(res) != 2 {
		t.Fatalf("two answered questions, got %d", len(res))
	}
	reply := composeAnswers("revisa el proyecto", res)
	for _, want := range []string{"revisa el proyecto", "¿qué carpeta?", "/tmp", "¿y qué quieres conseguir?", "un informe"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("the reply should carry %q, got:\n%s", want, reply)
		}
	}
}

// stubRunner is the minimum Runner the interface needs for a test that never runs a turn.
type stubRunner struct{}

func (s *stubRunner) RunPlan(context.Context, string, func(string, ...any)) (string, error) {
	return "", nil
}
func (s *stubRunner) RunTask(context.Context, string, func(string, ...any)) (string, error) {
	return "", nil
}
func (s *stubRunner) RunConfig(context.Context) error           { return nil }
func (s *stubRunner) ConversationReport() string                { return "" }
func (s *stubRunner) ConversationSummary() session.Snapshot     { return session.Snapshot{} }
func (s *stubRunner) ResetConversation()                        {}
func (s *stubRunner) RunModels(context.Context) (string, error) { return "", nil }
func (s *stubRunner) Config() config.Config                     { return config.Config{} }
func (s *stubRunner) SetReasoning(string)                       {}
func (s *stubRunner) RecordVerdict(bool, string) string         { return "" }
func (s *stubRunner) RewardReport() string                      { return "" }

// --- the send path ---------------------------------------------------------

// recordingRunner remembers the prompts it was given, which is how the answers are verified to
// arrive as a request WITH the gaps filled rather than as a bare list of values.
type recordingRunner struct {
	stubRunner
	tasks []string
	plans []string
}

func (r *recordingRunner) RunTask(_ context.Context, task string, _ func(string, ...any)) (string, error) {
	r.tasks = append(r.tasks, task)
	return "hecho", nil
}

func (r *recordingRunner) RunPlan(_ context.Context, prompt string, _ func(string, ...any)) (string, error) {
	r.plans = append(r.plans, prompt)
	return "plan", nil
}

// Confirming sends the request and its answers as one prompt, so the agent re-reads the request
// with the gaps filled instead of treating the answers as an unrelated message.
func TestConfirmSendsComposedAnswers(t *testing.T) {
	rr := &recordingRunner{}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr}
	items := []agent.AskItem{{Text: "¿qué carpeta?"}, {Text: "¿y qué hago?"}}
	tui.ask = newAsk(items, "revisa el proyecto")
	tui.ask.answers[0] = "/tmp"
	tui.ask.answers[1] = "solo mirar"

	tui.confirmAsk()

	if len(rr.tasks) != 1 {
		t.Fatalf("one turn should have been sent, got %d", len(rr.tasks))
	}
	got := rr.tasks[0]
	for _, want := range []string{"revisa el proyecto", "¿qué carpeta?", "/tmp", "¿y qué hago?", "solo mirar"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the prompt should carry %q, got:\n%s", want, got)
		}
	}
}

// Confirming in Plan mode stays in Plan mode: the answers belong to the turn that asked, and
// silently switching modes would run the request somewhere the user did not put it.
func TestConfirmInPlanModeRunsPlan(t *testing.T) {
	rr := &recordingRunner{}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr, screen: ScreenPlan}
	tui.ask = newAsk([]agent.AskItem{{Text: "q"}}, "plan original")
	tui.ask.answers[0] = "r"
	tui.confirmAsk()
	if len(rr.plans) != 1 || len(rr.tasks) != 0 {
		t.Fatalf("plan mode should run a plan, got plans=%d tasks=%d", len(rr.plans), len(rr.tasks))
	}
}

// Confirming with nothing at all — no origin and no answers — sends nothing. An empty turn would
// be silence, which the agent cannot tell apart from the user saying nothing.
func TestConfirmWithNothingSendsNothing(t *testing.T) {
	rr := &recordingRunner{}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr}
	tui.ask = newAsk([]agent.AskItem{{Text: "q"}}, "")
	tui.confirmAsk()
	if len(rr.tasks) != 0 {
		t.Fatalf("nothing to say should send nothing, got %q", rr.tasks)
	}
}

// Confirming with no window open is a no-op rather than a panic: the key can arrive after the
// window closed.
func TestConfirmWithNoWindowDoesNothing(t *testing.T) {
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: &stubRunner{}}
	tui.confirmAsk()
	if tui.asking() {
		t.Fatal("nothing should have opened")
	}
}

// The run context is used when there is one, so the answers join the run in flight instead of
// starting a second, unrelated one.
func TestRunningCtxPrefersTheLiveRun(t *testing.T) {
	type key struct{}
	live := context.WithValue(context.Background(), key{}, "live")
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: &stubRunner{}, runningCtx: live}
	if got := tui.runningCtxOr(context.Background()); got != live {
		t.Fatal("the live run context should win")
	}
	tui.runningCtx = nil
	fallback := context.Background()
	if got := tui.runningCtxOr(fallback); got != fallback {
		t.Fatal("with no run in flight the fallback is used")
	}
}

// The window opens through the runner's pending questions, and a window already holding answers
// is not replaced by a new one: the user's work in progress is not thrown away by a repaint.
func TestOpenAskIsIdempotentWhileOpen(t *testing.T) {
	r := &AppRunner{}
	r.setPendingQuestions(askItems(), "origen")
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: r}
	tui.openAskIfPending()
	if !tui.asking() {
		t.Fatal("the window should open")
	}
	tui.ask.answers[0] = "respondida"
	// A second pass finds nothing pending — it was taken — so the open window is untouched.
	tui.openAskIfPending()
	if tui.ask == nil || tui.ask.answers[0] != "respondida" {
		t.Fatal("an open window must not be replaced")
	}
}

// --- the remaining drawn states --------------------------------------------

// With every question answered the hint changes: it says the confirmation is available, which is
// only true at the end and would be misleading before it.
func TestHintSaysConfirmWhenAllAnswered(t *testing.T) {
	tui := newAskTUI(t, askItems(), "x")
	for i := range tui.ask.answers {
		tui.ask.answers[i] = "algo"
	}
	joined := seen(tui)
	if !strings.Contains(joined, "Enter confirma todo") {
		t.Fatalf("the hint should offer the confirmation:\n%s", joined)
	}
	if strings.Contains(joined, "faltan") {
		t.Fatalf("nothing is missing, so nothing should be counted:\n%s", joined)
	}
}

// A single question that has been answered says how to confirm it: with no list there are no
// arrows to suggest, and the only thing left to do is send it.
func TestSingleAnsweredQuestionOffersConfirm(t *testing.T) {
	tui := newAskTUI(t, []agent.AskItem{{Text: "¿cuál?"}}, "x")
	if strings.Contains(seen(tui), "Enter confirma") {
		t.Fatalf("an unanswered question should not offer the confirmation yet:\n%s", seen(tui))
	}
	tui.ask.answers[0] = "esta"
	if !strings.Contains(seen(tui), "Enter confirma") {
		t.Fatalf("an answered question should offer the confirmation:\n%s", seen(tui))
	}
}

// Enter confirms, and it is the empty line the reader returns rather than a byte of its own.
func TestEnterConfirms(t *testing.T) {
	r := &AppRunner{}
	r.setPendingQuestions([]agent.AskItem{{Text: "q"}}, "origen")
	rr := &recordingRunner{}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr}
	tui.ask = newAsk([]agent.AskItem{{Text: "q"}}, "origen")
	tui.ask.answers[0] = "r"
	if !tui.handleAskKey(context.Background(), keyEnter) {
		t.Fatal("Enter should be handled")
	}
	if tui.asking() {
		t.Fatal("Enter should have confirmed and closed the window")
	}
	if len(rr.tasks) != 1 {
		t.Fatalf("the answers should have been sent, got %d turns", len(rr.tasks))
	}
}

// The question list of a turn that asked is recorded on the runner, together with the request it
// clarifies — without the origin the answers arrive with nothing to attach them to.
func TestObserverRecordsQuestionsWithTheirOrigin(t *testing.T) {
	r := &AppRunner{}
	items := []agent.AskItem{{Text: "¿qué carpeta?"}}
	r.setPendingQuestions(items, "la petición")
	got, origin := r.TakePendingQuestions()
	if len(got) != 1 || got[0].Text != "¿qué carpeta?" {
		t.Fatalf("the questions should be kept, got %+v", got)
	}
	if origin != "la petición" {
		t.Fatalf("origin = %q, want the request being clarified", origin)
	}
}
