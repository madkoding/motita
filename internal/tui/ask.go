package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/madkoding/starlight/internal/agent"
)

// askState is the navigable window the agent's questions are answered in.
//
// The agent asks when it cannot read a request well enough to act on it. Until now that question
// arrived as a sentence in the conversation and the user had to write a reply from nothing. Here
// the questions are a list they can move through, each with the answers the agent is choosing
// between, and one confirmation at the end covers all of them.
//
// Questions are answered ONE AT A TIME but confirmed TOGETHER: several independent gaps are a
// single round trip, which is the whole point of showing more than one. Asking them one turn at
// a time costs the user a re-explanation each time.
type askState struct {
	// items are the questions of this turn, in the order the agent put them.
	items []agent.AskItem
	// answers is what the user gave for each item, index-aligned with items. Empty means
	// unanswered, which is a normal state while moving around: the window opens with every
	// question unanswered and the user fills them in any order.
	answers []string
	// cur is the question being shown. Navigation moves it; it is always a valid index.
	cur int
	// origin is the request being clarified, kept so the follow-up can restate what is being
	// answered. Without it the answers arrive with nothing to attach them to.
	origin string
}

// newAsk builds the window for a turn that asked questions.
//
// A single question is a list of one, so the window always has the same shape: there is no
// special case for "only one", and navigation simply has nowhere to go.
func newAsk(items []agent.AskItem, origin string) *askState {
	if len(items) == 0 {
		return nil
	}
	return &askState{
		items:   items,
		answers: make([]string, len(items)),
		origin:  origin,
	}
}

// asking reports whether the window is open.
func (t *TUI) asking() bool {
	return t.ask != nil && len(t.ask.items) > 0
}

// askRows is how many rows the window needs, which the layout reserves before drawing.
//
// It counts what will actually be drawn: the header, the question, its assumption, one row per
// option, the free-answer line, and the keys hint. A window that drew more than the layout
// reserved would push the frame past the bottom of the terminal.
func (t *TUI) askRows() int {
	if !t.asking() {
		return 0
	}
	return len(t.askLines(0))
}

// askLines renders the window, capped to at most `max` rows (zero meaning no limit).
//
// The cap exists for the same reason the completion popup has one: on a short terminal the
// window would otherwise take the frame past the bottom of the window and scroll the interface
// on a keypress. When it is cut the LAST row says so, because a silently truncated list of
// options looks complete and the user would not know there was more.
func (t *TUI) askLines(max int) []string {
	if !t.asking() {
		return nil
	}
	a := t.ask
	width := t.bodyWidth()
	var out []string

	n := len(a.items)
	// Header: where they are in the list, so "next" has a meaning before pressing it.
	if n > 1 {
		out = append(out, t.askLine(t.muted(fmt.Sprintf("question %d of %d", a.cur+1, n)), width))
	}
	it := a.items[a.cur]
	out = append(out, t.askLine(t.color(colAccent, colBase, it.Text), width))
	if it.Assumption != "" {
		out = append(out, t.askLine(t.muted("(if you do not answer: "+it.Assumption+")"), width))
	}

	// One row per option, numbered by the key that picks it. The numbers are the interface's,
	// not the agent's: they are position, and the agent never sees them.
	for i, opt := range it.Options {
		mark := " "
		if a.answers[a.cur] == opt {
			mark = "*"
		}
		out = append(out, t.askLine(fmt.Sprintf("%s %d) %s", mark, i+1, opt), width))
	}

	// The answer line is ALWAYS drawn, even with no options: it is where a free answer goes, and
	// it is what makes the questions answerable when the agent could think of no choices.
	out = append(out, t.askAnswerLine(width))

	if n > 1 {
		hint := "← → switch question"
		if a.allAnswered() {
			hint += " · Enter confirms all"
		} else {
			hint += fmt.Sprintf(" · %d left", a.unanswered())
		}
		out = append(out, t.askLine(hint, width))
	} else if a.answers[0] != "" {
		out = append(out, t.askLine("Enter confirms", width))
	}

	if max > 0 && len(out) > max {
		out = out[:max]
		out[max-1] = t.askLine(fmt.Sprintf("… and %d more", len(t.askLines(0))-(max-1)), width)
	}
	return out
}

// askAnswerLine is the row showing what the user is answering with.
//
// It is drawn from the answer the window holds, so a typed answer and a picked option are the
// same thing by the time it is confirmed, and the user can see exactly what will be sent.
func (t *TUI) askAnswerLine(width int) string {
	return t.askLine(t.muted("answer: ")+t.ask.answers[t.ask.cur], width)
}

// askLine draws one window row with the interface margin and the window's colour.
func (t *TUI) askLine(s string, width int) string {
	return t.plainLine(clipLine("│ "+s, width))
}

// allAnswered reports whether every question has an answer, which is what enables confirming.
func (a *askState) allAnswered() bool {
	for i := range a.items {
		if a.answers[i] == "" {
			return false
		}
	}
	return true
}

// unanswered counts the questions still missing an answer.
func (a *askState) unanswered() int {
	n := 0
	for i := range a.items {
		if a.answers[i] == "" {
			n++
		}
	}
	return n
}

// pick answers the current question with the option at index i (zero-based), then advances to
// the next question that still needs an answer.
//
// Advancing after a pick is what makes the list quick to answer: with several questions the
// user moves forward by answering, and only reaches for the arrows to go back and change one.
func (a *askState) pick(i int) bool {
	if i < 0 || i >= len(a.items[a.cur].Options) {
		return false
	}
	a.answers[a.cur] = a.items[a.cur].Options[i]
	a.next()
	return true
}

// next moves to the next question, wrapping. It prefers an unanswered one, so after answering
// the user lands on what still needs them rather than on a question already dealt with.
func (a *askState) next() {
	a.move(+1)
}

// prev moves to the previous question, wrapping.
func (a *askState) prev() {
	a.move(-1)
}

func (a *askState) move(step int) {
	n := len(a.items)
	if n == 0 {
		return
	}
	for k := 1; k <= n; k++ {
		i := ((a.cur+step*k)%n + n) % n
		if a.answers[i] == "" {
			a.cur = i
			return
		}
	}
	// Every question is answered: plain movement, so the user can still review and change one.
	a.cur = ((a.cur+step)%n + n) % n
}

// firstUnanswered returns the index of the first question without an answer, or -1.
func (a *askState) firstUnanswered() int {
	for i := range a.items {
		if a.answers[i] == "" {
			return i
		}
	}
	return -1
}

// result pairs the questions with the answers, in order, which is how they are handed back.
func (a *askState) result() []agent.Answers {
	out := make([]agent.Answers, 0, len(a.items))
	for i, it := range a.items {
		if a.answers[i] == "" {
			continue
		}
		out = append(out, agent.Answers{Question: it.Text, Answer: a.answers[i]})
	}
	return out
}

// handleAskKey routes a key while the window is open, and reports whether it used the key.
//
// The window CAPTURES the keys rather than sharing them with the input, which is what makes a
// digit a choice instead of a character. That is a deliberate trade: while the questions are on
// screen they are the only thing the user is doing, so a key that reaches the input by accident
// is worse than one that does not reach it at all.
//
// A line that is not one of the keys below is a free answer for the question being shown. It is
// taken here rather than left to the reader because the window is the only thing on screen, and
// the alternative was worse: a question with no options could not be answered at all, and a
// typed answer to one with options was submitted as a brand-new request.
//
// Esc closes the window and gives the input back, which is the way out for a user who would
// rather answer in their own words than pick from a list.
func (t *TUI) handleAskKey(ctx context.Context, line string) bool {
	// A number picks the option at that position. Counting from one is for the user: the row is
	// drawn as "1)", so the key has to be the same number.
	if len(line) == 1 && line[0] >= '1' && line[0] <= '9' {
		if t.ask.pick(int(line[0] - '1')) {
			t.drawFrame()
		}
		return true
	}
	switch line {
	case keyLeft:
		t.ask.prev()
		t.drawFrame()
		return true
	case keyRight:
		t.ask.next()
		t.drawFrame()
		return true
	case keyEnter:
		// An empty line is the confirmation: with a draft present the reader would have
		// returned that draft instead, and this case would not be reached.
		t.confirmAsk()
		return true
	case keyEsc:
		// The window closes and the keys go back to the input. The questions are not lost —
		// they are in the conversation above — and the user can reply in their own words.
		t.ask = nil
		t.drawFrame()
		return true
	}
	// Anything else is an answer typed for the question being shown. It is recorded WITHOUT
	// advancing: the reader collects the whole line, so the next Enter would otherwise submit
	// it twice — once here and once as a new turn.
	if text := strings.TrimSpace(line); text != "" {
		t.ask.answers[t.ask.cur] = text
		t.draft = ""
		t.drawFrame()
		return true
	}
	return false
}

// confirmAsk sends the answers as the next turn.
//
// Confirming with questions still unanswered is allowed and deliberate: the agent stated what it
// would assume for each one, so an unanswered question is an accepted assumption rather than a
// dropped one. Refusing to continue until every question is answered would turn a list of
// suggestions into a form to fill in, which is not what the agent is offering.
func (t *TUI) confirmAsk() {
	a := t.ask
	if a == nil {
		return
	}
	// An answer typed into the field but not confirmed with Enter is still an answer: the user
	// wrote it for this question, and dropping it because they reached for the arrows would
	// lose work they can see on screen.
	if free := strings.TrimSpace(t.draft); free != "" {
		a.answers[a.cur] = free
	}
	t.draft = ""
	answers := a.result()
	t.ask = nil

	// The answers go back as a reply to the request being clarified, so the agent re-reads the
	// request WITH the gaps filled instead of treating the answer as a new, unrelated message.
	t.submitAnswers(a.origin, answers)
}

// submitAnswers runs the turn that carries the answers back.
func (t *TUI) submitAnswers(origin string, answers []agent.Answers) {
	prompt := composeAnswers(origin, answers)
	if strings.TrimSpace(prompt) == "" {
		t.drawFrame()
		return
	}
	switch t.screen {
	case ScreenPlan:
		t.runPlan(t.runningCtxOr(context.Background()), prompt)
	default:
		t.runTask(t.runningCtxOr(context.Background()), prompt)
	}
}

// composeAnswers writes the request and its answers as one message.
//
// The answers are attached to the questions they answer rather than sent as a bare list, so the
// agent cannot pair them wrongly: a reply of "yes, /tmp, all" means nothing on its own, while
// the same three values against their questions are a complete request.
//
// Unanswered questions are left out. They are not gaps any more — the agent said what it would
// assume — and restating them would ask the agent to decide again what it already decided.
func composeAnswers(origin string, answers []agent.Answers) string {
	var b strings.Builder
	if strings.TrimSpace(origin) != "" {
		b.WriteString(strings.TrimSpace(origin))
		b.WriteString("\n\n")
	}
	if len(answers) == 0 {
		// Nothing was answered and nothing was assumed: the request goes back unchanged, so the
		// agent can re-read it. An empty turn would be silence.
		return strings.TrimSpace(b.String())
	}
	b.WriteString("Answers to what you asked:")
	for _, qa := range answers {
		b.WriteString("\n- ")
		b.WriteString(qa.Question)
		b.WriteString(" -> ")
		b.WriteString(qa.Answer)
	}
	return b.String()
}

// runningCtxOr returns the context of the run in flight, or the fallback.
//
// The window can be answered while a run is still finishing (the agent asked, and the turn ended,
// but a repaint is pending), so the caller cannot assume a run context exists.
func (t *TUI) runningCtxOr(fallback context.Context) context.Context {
	if t.runningCtx != nil {
		return t.runningCtx
	}
	return fallback
}
