package tui

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/i18n"
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
	// needed are the proposals auto mode accepted on its own (agent.SplitNeeded). They are not
	// asked about; they ride in the same follow-up as whatever the user accepts.
	needed []agent.AskItem
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
// on a keypress. What gives way is the OPTIONS: the answer line and the keys hint are always
// kept, because without them the window cannot be answered or confirmed. Long options are cut to
// fewer wrapped rows each, ending in an ellipsis, and when even one row each is too many the last
// row says how many options are not shown, because a silently truncated list looks complete.
func (t *TUI) askLines(max int) []string {
	if !t.asking() {
		return nil
	}
	a := t.ask
	width := t.bodyWidth()
	it := a.items[a.cur]
	head := t.askHeadLines(it, width)
	blocks := t.askOptionBlocks(it, width)
	// The answer line is ALWAYS drawn, even with no options: it is where a free answer goes, and
	// it is what makes the questions answerable when the agent could think of no choices.
	tail := append([]string{t.askAnswerLine(width)}, t.askHintLines(width)...)

	var opts []string
	if max > 0 {
		if len(tail) > max {
			tail = tail[:max]
		}
		head, opts = t.fitAsk(head, blocks, max-len(tail), width)
	} else {
		opts = t.askOptionRows(blocks, optionLimits(blocks, math.MaxInt), width)
	}
	out := make([]string, 0, len(head)+len(opts)+len(tail))
	out = append(out, head...)
	out = append(out, opts...)
	return append(out, tail...)
}

// askHeadLines is what comes above the options: where the user is in the list, the question, the
// assumption the agent falls back on and any detail.
func (t *TUI) askHeadLines(it agent.AskItem, width int) []string {
	a := t.ask
	n := len(a.items)
	var out []string
	// Header: where they are in the list, so "next" has a meaning before pressing it. A proposal
	// always has one, even alone: it says the item is something the agent offers to do, not a
	// question about the request.
	switch {
	case it.Proposal:
		out = append(out, t.askLine(t.muted(t.trf("suggestion %d of %d", a.cur+1, n)), width))
	case n > 1:
		out = append(out, t.askLine(t.muted(t.trf("question %d of %d", a.cur+1, n)), width))
	default:
		// A single question needs no position.
	}
	// The question and its options are the model's text: their control characters are shown.
	out = append(out, t.askLine(t.color(colAccent, colBase, EscapeControls(it.Text)), width))
	if it.Assumption != "" {
		out = append(out, t.askLine(t.muted(t.trf("(if you do not answer: %s)", EscapeControls(it.Assumption))), width))
	}
	if it.Detail != "" {
		out = append(out, t.askLine(t.muted(EscapeControls(it.Detail)), width))
	}
	return out
}

// fitAsk fits the rows above the answer line into `room`: the question first, then as much of the
// options as is left. A question too tall for the room keeps its first rows and no options.
func (t *TUI) fitAsk(head []string, blocks [][]string, room, width int) (kept, opts []string) {
	if len(head) > room {
		return head[:room], nil
	}
	room -= len(head)
	if limits := optionLimits(blocks, room); limits != nil {
		return head, t.askOptionRows(blocks, limits, width)
	}
	if room == 0 || len(blocks) == 0 {
		return head, nil
	}
	// Not even one row per option fits: the first ones are shown, and the last row says how many
	// are not.
	shown := room - 1
	opts = t.askOptionRows(blocks[:shown], optionLimits(blocks[:shown], shown), width)
	return head, append(opts, t.askLine(t.trf("… and %d more", len(blocks)-shown), width))
}

// optionLimits says how many rows each option keeps within `room`: the same cap for all, the
// tallest that fits, with the rows still free going to the first options that were cut, so a cut
// window fills the rows the layout reserved for it. Nil when not even one row each fits.
func optionLimits(blocks [][]string, room int) []int {
	for k := longestBlock(blocks); k >= 1; k-- {
		used := blockRows(blocks, k)
		if used > room {
			continue
		}
		spare := room - used
		limits := make([]int, len(blocks))
		for i, b := range blocks {
			limits[i] = min(len(b), k)
			if spare > 0 && len(b) > k {
				limits[i]++
				spare--
			}
		}
		return limits
	}
	return nil
}

// longestBlock is how many rows the tallest option wraps to.
func longestBlock(blocks [][]string) int {
	n := 0
	for _, b := range blocks {
		n = max(n, len(b))
	}
	return n
}

// blockRows is how many rows the options take when each is cut to at most k rows.
func blockRows(blocks [][]string, k int) int {
	n := 0
	for _, b := range blocks {
		n += min(len(b), k)
	}
	return n
}

// askOptionRows draws the options, each cut to its limit. A cut option ends in an ellipsis, so it
// does not read as complete.
func (t *TUI) askOptionRows(blocks [][]string, limits []int, width int) []string {
	var out []string
	for i, b := range blocks {
		n := limits[i]
		for j, line := range b[:n] {
			if j == n-1 && len(b) > n {
				line += "…"
			}
			out = append(out, t.askLine(line, width))
		}
	}
	return out
}

// askOptionBlocks is the rows of each option of it, numbered by the key that picks it, before the
// window's margin is drawn. The numbers are the interface's, not the agent's: they are position,
// and the agent never sees them.
func (t *TUI) askOptionBlocks(it agent.AskItem, width int) [][]string {
	a := t.ask
	out := make([][]string, 0, len(it.Options))
	for i, opt := range it.Options {
		mark := " "
		if a.answers[a.cur] == opt {
			mark = "*"
		}
		label := EscapeControls(opt)
		if it.Proposal {
			// The answers of a proposal are the interface's own, so they are in its language.
			label = t.tr(opt)
		}
		// A long option wraps under its own text instead of being clipped at the edge: the end of
		// an option is often what tells two of them apart.
		lead := fmt.Sprintf("%s %d) ", mark, i+1)
		indent := strings.Repeat(" ", len(lead))
		wrapped := wordWrap(label, width-visibleLen("│ "+lead))
		block := make([]string, 0, len(wrapped))
		for j, line := range wrapped {
			if j == 0 {
				block = append(block, lead+line)
				continue
			}
			block = append(block, indent+line)
		}
		out = append(out, block)
	}
	return out
}

// askHintLines is the keys hint under the answer line: how to move between several questions and
// how many are left, or that Enter confirms the one answered question.
func (t *TUI) askHintLines(width int) []string {
	a := t.ask
	if len(a.items) > 1 {
		hint := t.tr("← → switch question")
		if a.allAnswered() {
			hint += " · " + t.tr("Enter confirms all")
		} else {
			hint += " · " + t.trf("%d left", a.unanswered())
		}
		return []string{t.askLine(hint, width)}
	} else if a.answers[0] != "" {
		return []string{t.askLine(t.tr("Enter confirms"), width)}
	} else {
		// One question with no answer yet: there is nothing to confirm.
		return nil
	}
}

// askAnswerLine is the row showing what the user is answering with.
//
// It is drawn from the answer the window holds, so a typed answer and a picked option are the
// same thing by the time it is confirmed, and the user can see exactly what will be sent.
func (t *TUI) askAnswerLine(width int) string {
	return t.askLine(t.muted(t.tr("answer:")+" ")+EscapeControls(t.ask.answers[t.ask.cur]), width)
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
	if a.items[a.cur].Proposal {
		a.settleAfter(a.cur)
	}
	a.next()
	return true
}

// settleAfter carries a proposal's answer to the proposals after it when the answer says so:
// "this one and the following" applies every later one, and "none" applies none of them. It is
// what makes a list of suggestions one keypress to accept or dismiss instead of one per item.
func (a *askState) settleAfter(i int) {
	var fill string
	switch a.answers[i] {
	case agent.ProposalApplyRest:
		fill = agent.ProposalApply
	case agent.ProposalSkipAll:
		fill = agent.ProposalSkipAll
	default:
		return
	}
	for k := i + 1; k < len(a.items); k++ {
		if a.items[k].Proposal {
			a.answers[k] = fill
		}
	}
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
	default:
		// Anything else is an answer, read below.
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
	t.ask = nil
	if a.items[0].Proposal {
		t.submitProposals(a)
		return
	}
	answers := a.result()

	// The answers go back as a reply to the request being clarified, so the agent re-reads the
	// request WITH the gaps filled instead of treating the answer as a new, unrelated message.
	t.submitAnswers(a.origin, answers)
}

// submitProposals runs the turn that applies the accepted proposals, and only those.
//
// Nothing is applied that the user did not accept: a proposal left unanswered, or answered with
// "none", stays out of the request. An answer typed instead of picked is the user's own words
// about that proposal, and it goes to the agent as such rather than being read as a yes or a no.
// With nothing accepted and nothing said there is no turn at all.
func (t *TUI) submitProposals(a *askState) {
	accepted := append([]agent.AskItem(nil), a.needed...)
	var notes []agent.Answers
	for i, it := range a.items {
		switch a.answers[i] {
		case agent.ProposalApply, agent.ProposalApplyRest:
			accepted = append(accepted, it)
		case "", agent.ProposalSkipAll:
			// Not accepted: it is not applied.
		default:
			notes = append(notes, agent.Answers{Question: it.Text, Answer: a.answers[i]})
		}
	}
	var b strings.Builder
	b.WriteString(agent.ProposalRequest(t.tr(proposalLead), accepted))
	if len(notes) > 0 {
		b.WriteString("\n\n")
		b.WriteString(t.tr("About your other suggestions:"))
		for _, qa := range notes {
			b.WriteString("\n- " + qa.Question + " -> " + qa.Answer)
		}
	}
	prompt := strings.TrimSpace(b.String())
	if prompt == "" {
		// Nothing to apply: no turn, but the conversation still says what was decided.
		t.addMessage(AuthorSystem, t.tr("No suggestions applied:")+"\n"+proposalMarks(a))
		t.drawFrame()
		return
	}
	// The prompt is for the agent. The conversation shows what the user chose instead: a short
	// label and each suggestion marked as applied or not.
	t.submitPromptAs(prompt, t.tr("Applying suggestions")+"\n"+proposalMarks(a))
}

// proposalLead opens the turn that applies accepted suggestions, in the interface's language.
const proposalLead = "Apply these suggestions you made, and nothing else:"

// proposalMarks is each suggestion with what was decided for it: ✓ applied, ✗ not applied, or the
// user's own words when they typed an answer.
func proposalMarks(a *askState) string {
	lines := make([]string, 0, len(a.needed)+len(a.items))
	for _, it := range a.needed {
		lines = append(lines, "✓ "+it.Text)
	}
	for i, it := range a.items {
		switch a.answers[i] {
		case agent.ProposalApply, agent.ProposalApplyRest:
			lines = append(lines, "✓ "+it.Text)
		case "", agent.ProposalSkipAll:
			lines = append(lines, "✗ "+it.Text)
		default:
			lines = append(lines, "• "+it.Text+" -> "+a.answers[i])
		}
	}
	return strings.Join(lines, "\n")
}

// shownTurn is how a turn of the user's is drawn when a conversation is read back: the turn that
// applied suggestions by its label, in either language it may have been sent in, and any other
// turn as it was written.
func (t *TUI) shownTurn(text string) string {
	for _, lang := range []i18n.Lang{i18n.EN, i18n.ES} {
		if strings.HasPrefix(text, i18n.T(lang, proposalLead)) {
			return t.tr("Applying suggestions")
		}
	}
	return text
}

// submitAnswers runs the turn that carries the answers back.
func (t *TUI) submitAnswers(origin string, answers []agent.Answers) {
	t.submitPrompt(composeAnswers(origin, answers))
}

// submitPrompt runs the turn that carries a window's answers back, on the screen it was asked from.
// An empty prompt runs nothing: the window closes and the frame is redrawn.
func (t *TUI) submitPrompt(prompt string) {
	t.submitPromptAs(prompt, prompt)
}

// submitPromptAs is submitPrompt with `shown` drawn in the conversation instead of the prompt.
//
// The turn it starts is a follow-up, never an auto-mode message of the user's: its own needed
// suggestions are asked about, so applying suggestions cannot chain into more of itself.
func (t *TUI) submitPromptAs(prompt, shown string) {
	t.autoTurn = false
	if strings.TrimSpace(prompt) == "" {
		t.drawFrame()
		return
	}
	switch t.screen {
	case ScreenPlan:
		t.runPlanAs(t.runningCtxOr(t.loopCtxOr(context.Background())), prompt, shown)
	default:
		t.runTaskAs(t.runningCtxOr(t.loopCtxOr(context.Background())), prompt, shown)
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
