package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/madkoding/motita/internal/task"
)

// --- Conversation -----------------------------------------------------------

// DialogueTurn is one exchange in the conversation: what the user said and what the agent
// answered. Both are kept because the meaning lives in the pair — a user's "yes" is only
// interpretable next to the question it answers.
type DialogueTurn struct {
	User string
	// Agent is the reply, the question, or a one-line account of what was done. It is what the
	// agent said, in the agent's own words.
	Agent string
	// Kind records what the turn was, so the transcript can be summarised honestly: a chat turn
	// and a finished task are not the same thing to a reader.
	Kind string
	// Pending marks a turn that was written when its task STARTED and has not been closed by
	// an outcome. It is how a run that is cut off (a restart, a crash) still leaves the
	// request and the work done so far in the conversation, instead of nothing at all.
	Pending bool `json:",omitempty"`
}

// pendingText is what a turn says before its task has an outcome.
const pendingText = "(no result recorded yet: the run is still working, or it was interrupted before it finished)"

// pendingLimit caps the work trail kept in a pending turn.
const pendingLimit = 3000

// begin writes the turn for a task that is starting. A pending turn for the same request
// is reused: a run that resumes after a restart continues its own turn, it does not add a
// second one.
func (a *Agent) begin(t task.Task) {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	if n := len(a.transcript); n > 0 && a.transcript[n-1].Pending && a.transcript[n-1].User == t.Description {
		return
	}
	a.transcript = append(a.transcript, DialogueTurn{
		User: t.Description, Agent: pendingText, Kind: KindTask, Pending: true,
	})
}

// trail updates the pending turn with what the run has done so far.
func (a *Agent) trail(t task.Task, lines []string) {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	n := len(a.transcript)
	if n == 0 || !a.transcript[n-1].Pending || a.transcript[n-1].User != t.Description {
		return
	}
	text := strings.Join(lines, "\n")
	if len(text) > pendingLimit {
		start := len(text) - pendingLimit
		for !utf8.RuneStart(text[start]) {
			start++
		}
		text = "..." + text[start:]
	}
	a.transcript[n-1].Agent = pendingText + workMarker + text
}

// workSoFar is the trail of the pending turn for this request, or "" when there is none.
func (a *Agent) workSoFar(t task.Task) string {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	n := len(a.transcript)
	if n == 0 || !a.transcript[n-1].Pending || a.transcript[n-1].User != t.Description {
		return ""
	}
	_, work, _ := strings.Cut(a.transcript[n-1].Agent, workMarker)
	return work
}

// workMarker separates the pending text from the trail inside a pending turn.
const workMarker = "\nWork so far:\n"

// record appends a finished turn. It closes the pending turn of the same request, if there
// is one, instead of leaving it beside its own outcome.
func (a *Agent) record(turn DialogueTurn) {
	if n := len(a.transcript); n > 0 && a.transcript[n-1].Pending && a.transcript[n-1].User == turn.User {
		a.transcript[n-1] = turn
		return
	}
	a.transcript = append(a.transcript, turn)
}

// SetTranscript seeds the conversation, which is how an interface hands over what the user has
// already seen. It replaces anything held.
func (a *Agent) SetTranscript(turns []DialogueTurn) {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	a.transcript = append([]DialogueTurn(nil), turns...)
}

// Transcript returns a copy of the conversation.
func (a *Agent) Transcript() []DialogueTurn {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	return append([]DialogueTurn(nil), a.transcript...)
}

// converse appends the user's message and the agent's answer to the conversation.
func (a *Agent) converse(t task.Task, reply string) {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	a.record(DialogueTurn{
		User:  t.Description,
		Agent: reply,
		Kind:  KindChat,
	})
}

// note appends what the agent did about a task, so the next turn can refer to it.
//
// A task that ran is part of the conversation too: "and now do the same for the other one" only
// means something if the agent can see what it just did.
func (a *Agent) note(t task.Task, outcome string, kind string) {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	a.record(DialogueTurn{
		User:  t.Description,
		Agent: outcome,
		Kind:  kind,
	})
}

// dialogue renders the conversation for the prompt.
//
// It is bounded on purpose. The transcript is fed to the analysis phase on every turn, so an
// unbounded one would grow the prompt until it crowded out the task itself — and an old greeting
// is not worth more than the request being made now. The most recent turns are what a short
// answer refers to, so the tail is kept and the beginning is dropped with a note that says so.
func (a *Agent) dialogue() string {
	turns := a.Transcript()
	if len(turns) == 0 {
		return "(this is the first message: there is no earlier conversation)"
	}
	const keep = 12
	dropped := 0
	if len(turns) > keep {
		dropped = len(turns) - keep
		turns = turns[dropped:]
	}
	var b strings.Builder
	if dropped > 0 {
		fmt.Fprintf(&b, "(%d earlier exchanges omitted)\n", dropped)
	}
	for _, turn := range turns {
		fmt.Fprintf(&b, "user: %s\n", truncate(collapse(turn.User), 500))
		if turn.Pending {
			// Not an outcome: the work trail would read as something the agent said.
			b.WriteString("you: (that request was cut off before it finished)\n")
			continue
		}
		said := truncate(collapse(turn.Agent), 500)
		switch turn.Kind {
		case KindChat:
			fmt.Fprintf(&b, "you: %s\n", said)
		case KindTask:
			fmt.Fprintf(&b, "you (did it): %s\n", said)
		default:
			fmt.Fprintf(&b, "you: %s\n", said)
		}
	}
	return b.String()
}

// collapse folds a multi-line string into one line, so one turn stays one line in the prompt.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// maxQuestions bounds how many questions are put at once. Past a few, the user is not being
// asked to clarify a request any more; they are filling in a form, and the request should have
// been read more generously first.
const maxQuestions = 5

// buildQuestions normalises the questions of a turn.
//
// The model may answer with a list (several gaps) or with the single question field (one gap).
// Both are valid and both must work, so this folds them into one shape for the interface: the
// list when there is one, otherwise the single question. It also drops empty questions and
// trims the list, so the interface never has to draw a blank row.
//
// An EMPTY result means the agent has nothing to ask — the turn is not an ask at all — and the
// caller must treat that as no question rather than as one empty question.
func buildQuestions(a Analysis) []AskItem {
	out := make([]AskItem, 0, len(a.Questions)+1)
	for _, q := range a.Questions {
		text := collapse(q.Text)
		if text == "" {
			continue
		}
		out = append(out, AskItem{
			Text:       text,
			Assumption: cleanAssumption(q.Assumption),
			Options:    cleanOptions(q.Options),
		})
		if len(out) == maxQuestions {
			break
		}
	}
	// The single-question fields are still asked, so a prompt that predates the list keeps
	// working. Only added when the list did not already carry something: a model that filled
	// both meant the same gap twice.
	if len(out) == 0 {
		if text := collapse(a.Question); text != "" {
			out = append(out, AskItem{
				Text:       text,
				Assumption: cleanAssumption(a.Assumption),
				Options:    cleanOptions(a.Options),
			})
		}
	}
	return out
}

// AskedText writes several questions as one message: each numbered, with its options under it and
// the assumption it falls back to in brackets. It is "" for fewer than two, which the single
// question already says in full.
//
// No label of the program's own is added: the questions, options and assumptions are the model's
// words in the user's language, and an English lead in front of them would mix two languages.
func AskedText(items []AskItem) string {
	if len(items) < 2 {
		return ""
	}
	blocks := make([]string, 0, len(items))
	for i, it := range items {
		var b strings.Builder
		fmt.Fprintf(&b, "%d. %s", i+1, it.Text)
		for _, opt := range it.Options {
			b.WriteString("\n   - " + opt)
		}
		if it.Assumption != "" {
			b.WriteString("\n   (" + it.Assumption + ")")
		}
		blocks = append(blocks, b.String())
	}
	return strings.Join(blocks, "\n\n")
}

// assumptionLead is the phrase the interface puts in front of an assumption, and which the model
// also tends to put there itself.
//
// cleanAssumption reduces an assumption to the ACTION it describes.
//
// The model reliably opens its assumption with a lead of its own: a verb announcing it, a
// conditional clause, or both. In Spanish, which is what the runs happened to emit:
//
//	spanish-fixture: "asumiré: reviso el proyecto", "asumiendo: la actual"
//	spanish-fixture: "Si no me dices otra cosa, asumiré: reviso el proyecto"
//
// The model is asked to write the action alone and in the user's language; this is the safety
// net under that instruction, not the mechanism.
//
// Chasing each phrasing in a list does not work — the first version listed whole sentences and
// the next run produced one that was not on it — so a clause is recognised by SHAPE: any leading
// conditional phrase up to the first comma, when something follows it.
//
// That rule is deliberately blunt and it has a known cost: a real conditional ACTION, "Si borras
// eso, pierdes datos", matches the same shape and loses its condition, leaving "pierdes datos".
// The condition is the point of such a sentence, and it matters because the user confirms an
// assumption in one word. Tightening it was tried and rejected: the repo's tests require
// "Si no me dices otra cosa, reviso ./workspace" to become "reviso ./workspace", so a conditional
// with no announcing verb is expected to lose its clause. Recognising the preamble by shape is
// the contract; the way out is upstream, where the PROMPT tells the model to write the action
// alone. Only the leading clause goes, and only when the comma leaves something behind, so a
// field that is ENTIRELY a conditional is kept whole rather than emptied.
//
// The strip repeats because a model emits both shapes in one field and removing one exposes the
// other. spanish-fixture, and the marker is repeated on each of these lines because the gate
// reads them one at a time:
//
//	spanish-fixture: "Si no me dices otra cosa, asumiré: reviso el proyecto"
//	spanish-fixture: becomes "asumiré: reviso el proyecto" after the clause goes,
//	spanish-fixture: and still needs the announcement removed.
//
// stripAnnouncement drops a verb that only announces the assumption. It is a small, closed set
// of phrasings, which is why this one can be a list where the clause of stripConditionalClause
// cannot.
func stripAnnouncement(s string) string {
	for _, verb := range assumptionAnnouncements {
		if rest, ok := cutPrefixFold(s, verb); ok {
			if r := collapse(rest); r != "" {
				s = r
			}
			break
		}
	}
	return s
}

// stripConditionalClause drops a leading conditional clause, up to its first comma, when
// something follows it.
func stripConditionalClause(s string) string {
	if isConditionalClause(s) {
		if c := strings.Index(s, ","); c > 0 && strings.TrimSpace(s[c+1:]) != "" {
			s = collapse(s[c+1:])
		}
	}
	return s
}

func cleanAssumption(in string) string {
	s := collapse(in)
	if s == "" {
		return s
	}
	for {
		before := s

		s = stripConditionalClause(stripAnnouncement(s))
		if s == before {
			break
		}
	}
	return s
}

// assumptionAnnouncements are the openers that only restate what the interface says anyway,
// matched case-insensitively at the start of the field.
//
// Spanish is what the runs emitted; the other languages are here because the model is asked to
// write this field in the language of the request, so a question in English produces an
// announcement in English. A language missing from this list leaves its "I will assume:" visible
// to the user, which is the bug this list prevents.
var assumptionAnnouncements = []string{
	// spanish-fixture: the phrasings the real runs emitted.
	"asumiré:", "asumiré", "asumiendo:", // spanish-fixture: stripped from the model's output
	"asumo que", "supongo que", "supondré:", // spanish-fixture: stripped from the model's output
	"i will assume:", "i'll assume:", "i will assume", "i'll assume",
	"assuming that", "assuming:", "i am assuming:", "i'm assuming:",
	"vou assumir:", "assumindo:", "suponho que", // spanish-fixture: stripped from the model's output
}

// conditionalOpeners are how a conditional clause starts, per language. The clause is stripped
// when a comma separates it from something that follows.
//
// See the note on cleanAssumption for the cost of recognising this by shape instead of by
// meaning. The list is what makes it work in more than one language; the shape is what fails to
// tell a preamble from a real condition, in any of them.
var conditionalOpeners = []string{
	// Spanish, the language the runs produced.
	"si ", "si no ", "si quieres", "a menos que ", "salvo que ", "en caso de ",
	// English, the language the interface and the prompts are written in.
	"if ", "if you ", "if not ", "unless ", "in case ",
	// Portuguese and French, other languages this interface will meet.
	"se ", "caso ", "a menos que ", "a não ser que ",
	"si vous ", "sauf si ", "à moins que ",
}

// isConditionalClause reports whether the text opens with a conditional marker.
func isConditionalClause(s string) bool {
	low := strings.ToLower(s)
	for _, opener := range conditionalOpeners {
		if strings.HasPrefix(low, opener) {
			return true
		}
	}
	return false
}

// cutPrefixFold removes a prefix ignoring case, and reports whether it was there.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) {
		return "", false
	}
	if !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}

// maxOptions bounds how many answers are offered: past a handful the list stops being a
// shortcut and becomes a menu to read.
const maxOptions = 4

// cleanOptions keeps the options that can actually be picked.
//
// The model is asked for short candidate answers, but a long one is still one of the choices it is
// weighing: it is kept, collapsed to one line, and only an absurdly long one is cut short with an
// ellipsis. Dropping it hid an alternative from the user, so a question could arrive with fewer
// choices than the agent meant, or none. Empty options and duplicates go: two identical choices
// look like a bug in the interface.
func cleanOptions(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, o := range in {
		one := collapse(o)
		if r := []rune(one); len(r) > optionMaxRunes {
			one = strings.TrimSpace(string(r[:optionMaxRunes-1])) + "…"
		}
		if one == "" || seen[one] {
			continue
		}
		seen[one] = true
		out = append(out, one)
		if len(out) == maxOptions {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// optionMaxRunes is the longest an option is shown: a couple of lines, which both interfaces wrap.
const optionMaxRunes = 240
