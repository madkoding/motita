// Package agent implements the agent's main loop, which orchestrates the three
// layers:
//
//	Layer A (anchor)  validates deterministically, without reasoning
//	Layer B (llm)     analyses, plans and proposes actions
//	Layer C (sandbox) runs actions in isolation
//
// The loop is the one from the architecture document:
//
//	[1] read task             [6] LLM: generate the action
//	[2] extract context       [7] run in the sandbox
//	[3] LLM: analyse          [8] validate with the anchor
//	[4] LLM: plan             [9] PASS -> final action / FAIL -> retry
//	[5] split into subtasks       and once retries are exhausted -> escalate
//
// The agent never decides on its own that it has finished: only the anchor can
// declare PASS.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/policy"
	"github.com/madkoding/motita/internal/reward"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/task"
	"github.com/madkoding/motita/internal/template"
)

// Agent orchestrates the three layers.
type Agent struct {
	cfg     config.Config
	log     *logx.Logger
	engine  *llm.Client
	sandbox *sandbox.Sandbox
	source  task.Source
	http    *http.Client

	// Progress, if set, receives human-readable updates such as "analysing..." or
	// "running: git status". It is used by the TUI to keep the user informed.
	Progress func(format string, args ...any)

	// ExecCommand is the agent's command execution point. By default it uses the
	// sandbox (Layer C); it is injected so actions that depend on external
	// programs (git_commit, for instance) can be tested without depending on the
	// program existing, on its configuration or on its interactive behaviour.
	ExecCommand func(context.Context, execx.Request) (string, bool, int, error)

	// Interactive reports whether there is a user who can answer a question.
	//
	// It decides what to do when the request cannot be read well enough to act on. With a user on
	// the other end, the agent ASKS: the request is incomplete rather than impossible, and a
	// three-word answer unblocks it. Without one — a batch run, a cron job, a piped task — there is
	// nobody to ask, so asking would only stall: the agent proceeds on the assumption it stated.
	//
	// Both are better than refusing, which is what the agent used to do in either case.
	Interactive bool

	// library is the procedure library, when one is configured. It is the SAME library Plan
	// mode uses: one shelf of procedures, reachable from both modes, because a procedure is
	// not specific to how the work is executed.
	//
	// Task mode reaches it through the action protocol rather than tool calls — its actions are
	// JSON — so the four operations are named by the action's "kind" instead of by a tool name.
	library *skills.Library

	// reward is the long-term value per skill, and consulted counts what this run read.
	//
	// Same pair as the planner's, and for the same reason: a verdict has to land on specific
	// skills, and the moment that is knowable is the read.
	reward    *reward.Ledger
	consulted map[string]int

	// transcript is what has been said in this conversation, oldest first, and it is what makes
	// the agent conversational rather than stateless.
	//
	// Without it every message is the first message: "yes" or "the second one" has nothing to
	// refer to, so the agent can only re-ask what it already asked, and the user has to write
	// their request out again in full. The dialogue IS the context — each answer narrows down
	// what they want — and dropping it throws away the progress the user just made.
	//
	// It is guarded because a turn appends to it from the goroutine running the task while the
	// interface reads it to render the count.
	transcriptMu sync.Mutex
	transcript   []DialogueTurn

	// Observer, when not nil, receives the result of every task on completion.
	// It is the integration point for metrics or for whoever embeds the agent,
	// and it is also what the tests use to inspect the verdict without reading
	// the log.
	Observer func(TaskResult)

	// streamThoughts shows the model's reasoning WHILE it is being written, as LivePrefix
	// progress lines. Off by default: it changes the request to a streamed one, and only an
	// interface that renders the snapshots should pay for them. See SetLiveThinking.
	streamThoughts bool

	// approver asks the user before a consequential command runs, and nil means there is
	// nobody to ask — which is a real state, not a missing dependency: a task piped in from
	// a script has no user, and a command that needs approval in that situation is refused
	// rather than run on the user's behalf. See Approver.
	approver Approver
}

// SetObserver allows external callers (such as the TUI) to register a callback
// that receives the task result when it is produced. It is the same hook as the
// Observer field; the method exists so interfaces that wrap *agent.Agent can
// expose the hook without exporting the field itself.
func (a *Agent) SetObserver(fn func(TaskResult)) {
	a.Observer = fn
}

// SetLibrary installs the procedure library.
//
// It is the same library the planner receives, and sharing it is the point: a procedure
// written down in one mode is available in the other, which is what makes the library a single
// body of knowledge rather than two that drift apart.
func (a *Agent) SetLibrary(lib *skills.Library) { a.library = lib }

// SetReward installs the long-term value ledger.
func (a *Agent) SetReward(l *reward.Ledger) { a.reward = l }

// Consulted returns the skills this run read, and how many times each.
//
// It is what the caller needs to turn the user's verdict into value: the skills are known here
// and nowhere else.
func (a *Agent) Consulted() map[string]int {
	if len(a.consulted) == 0 {
		return nil
	}
	out := make(map[string]int, len(a.consulted))
	for k, v := range a.consulted {
		out[k] = v
	}
	return out
}

// consult records that a skill was read.
func (a *Agent) consult(name string) {
	if a.consulted == nil {
		a.consulted = map[string]int{}
	}
	a.consulted[name]++
}

// SetLiveThinking turns on the live view of the model's reasoning: while a phase is being
// generated, snapshots of what the model has written so far are reported as progress lines
// starting with LivePrefix, at most one per liveInterval.
//
// Reported from real use: "I still cannot see what the model decides, reasons or thinks while
// I wait - only states". The phases took tens of seconds each and all the interface could say
// was "deciding action...".
func (a *Agent) SetLiveThinking(on bool) { a.streamThoughts = on }

// LivePrefix marks a progress line that is a SNAPSHOT of the reasoning being written right
// now. Each one replaces the one before it, so an interface shows the latest and a log keeps
// none: the phase's final reasoning is reported again, whole, as a ThinkingPrefix line.
const LivePrefix = "live: "

// ThinkingPrefix marks the finished reasoning of a phase: what the model understood, planned
// or decided, in its own words.
const ThinkingPrefix = "thinking: "

// SetProgress registers the callback that receives the human-readable phase
// lines ("running: ls", "validating…"). It is the same hook as the Progress
// field, behind a method so an embedder can install it through an interface
// without reaching into the struct.
func (a *Agent) SetProgress(fn func(format string, args ...any)) {
	a.Progress = fn
}

// New builds the agent with all of its dependencies already constructed.
func New(cfg config.Config, log *logx.Logger, engine *llm.Client, box *sandbox.Sandbox, source task.Source) *Agent {
	if log == nil {
		log = logx.Global()
	}
	a := &Agent{
		cfg:     cfg,
		log:     log,
		engine:  engine,
		sandbox: box,
		source:  source,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
	if box != nil {
		a.ExecCommand = box.Run
	}
	return a
}

// exec runs a command with the agent's executor (the sandbox by default). It
// returns a clear error when none is configured.
func (a *Agent) exec(ctx context.Context, p execx.Request) (string, bool, int, error) {
	if a.ExecCommand == nil {
		return "", false, -1, errors.New("the agent has no command executor configured")
	}
	return a.ExecCommand(ctx, p)
}

// RunCommand executes a single command line under the agent's policy and sandbox.
// It is the entry point used by interactive modes (plan/chat) where the model
// requests a tool call instead of emitting JSON.
func (a *Agent) RunCommand(ctx context.Context, command string) (string, int, error) {
	p := a.planRequest(command)
	if p.Verdict == policy.Deny {
		return fmt.Sprintf("[refused: %s]\n", p.Reason), 1, fmt.Errorf("command was refused: %s", p.Reason)
	}
	if p.Verdict == policy.Ask {
		approved, err := a.approve(ctx, p, command)
		if err != nil {
			return fmt.Sprintf("[not approved: %s]\n", err), 1,
				fmt.Errorf("command needs approval and it could not be obtained: %w", err)
		}
		if !approved {
			return "[not approved: the user declined]\n", 1,
				fmt.Errorf("the user declined to run: %s", command)
		}
	}
	output, _, exit, err := a.exec(ctx, p.Request)
	return output, exit, err
}

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
func cleanAssumption(in string) string {
	s := collapse(in)
	if s == "" {
		return s
	}
	for {
		before := s

		// A verb that only announces the assumption. It is a small, closed set of phrasings,
		// which is why this one can be a list where the clause below cannot.
		for _, verb := range assumptionAnnouncements {
			if rest, ok := cutPrefixFold(s, verb); ok {
				if r := collapse(rest); r != "" {
					s = r
				}
				break
			}
		}

		// A leading conditional clause, up to its first comma, when something follows it.
		if isConditionalClause(s) {
			if c := strings.Index(s, ","); c > 0 && strings.TrimSpace(s[c+1:]) != "" {
				s = collapse(s[c+1:])
			}
		}

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
	"asumiré:", "asumiré", "asumiendo:", "asumo que", "supongo que", "supondré:", // spanish-fixture: stripped from the model's output
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
// The model is asked for short candidate answers; anything empty, multi-line, or absurdly long
// is dropped rather than shown, because an option is a one-line reply the user selects, not a
// document. Duplicates go too: two identical choices look like a bug in the interface.
func cleanOptions(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, o := range in {
		one := collapse(o)
		if one == "" || seen[one] {
			continue
		}
		if len([]rune(one)) > optionMaxRunes {
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

// optionMaxRunes is the longest option worth showing on one row next to its key.
const optionMaxRunes = 60

// --- Result of each phase ---------------------------------------------------

// Analysis is the structured output of phase [3].
type Analysis struct {
	// Kind decides what the agent does with the message, and it is resolved BEFORE anything
	// else. It is the difference between a conversational agent and a task runner:
	//
	//	"task" — there is something to do; analyse it and run the loop.
	//	"chat" — there is nothing to do (a greeting, a question about the conversation, an
	//	         observation). Answer in Reply and stop, without inventing work.
	//	"ask"  — the request cannot be read well enough to act on and guessing risks the
	//	         wrong thing. Ask in Question.
	//
	// Without this an agent has only one response to every message, which is to plan work:
	// "hello" produces a task plan, and a question about what it just did produces another
	// task plan. The user asked to be talked to.
	//
	// An empty Kind means the model did not answer, and is treated as "task": that is the
	// behaviour of every prompt that predates this field, so an existing configuration keeps
	// working unchanged.
	Kind string `json:"kind"`

	Understandable bool     `json:"understandable"`
	Summary        string   `json:"summary"`
	SuccessCrit    []string `json:"success_criteria"`
	Risks          []string `json:"risks"`
	NeedsSubtasks  bool     `json:"needs_subtasks"`

	// Question is what to ask the user when the request cannot be understood well enough to act
	// on. It is the difference between an agent that stops and one that asks.
	//
	// Users mistype, abbreviate, and leave out what they consider obvious. A request the model
	// can only half interpret is not a failure: it is incomplete information, and the interface
	// has a user on the other end who can complete it. Refusing outright — which is what this
	// used to do — throws away the turn and makes the user rephrase from scratch.
	Question string `json:"question"`
	// Assumption is what the agent WOULD do if it had to proceed without an answer. Asking with
	// a stated assumption lets the user confirm in one word instead of writing a new request, and
	// it gives the agent a defensible reading if nobody answers.
	Assumption string `json:"assumption"`

	// Questions are the questions to ask, in order, when more than one thing is unclear.
	//
	// The single Question/Comment pair above covers the common case; this covers the case where
	// the request has several independent gaps. Asking them one turn at a time costs a round
	// trip each and makes the user re-explain the request three times; asking them together lets
	// the user move between them and confirm once.
	//
	// When Questions is empty the interface falls back to the single Question, so a prompt that
	// does not produce a list keeps working exactly as before.
	Questions []AskItem `json:"questions"`

	// Options are concrete answers the user can pick from instead of typing.
	//
	// A question is easier to answer when the plausible answers are already on screen: "which
	// folder?" with [the current one, /tmp, tell me] is one keypress, and the user can still
	// write something else. Without them the interface can only echo the question and leave the
	// user to compose a reply from nothing.
	//
	// They are only meaningful with Question, and an EMPTY list is the normal case: a question
	// whose answer is genuinely open ("what are you trying to achieve?") has nothing to offer, and
	// inventing options there would push the user toward answers they did not mean.
	Options []string `json:"options"`

	// Reply is the conversational answer, used when Kind is "chat".
	//
	// It is prose for a person, in their language, and it is NOT a task summary: it answers
	// what they said. Keeping it a separate field is what lets the interface show a reply
	// without pretending a task ran.
	Reply string `json:"reply"`
}

// AskItem is one question put to the user, with the answers they could pick.
//
// It is what the interface needs to draw a navigable list: the question, what the agent will
// assume if the user says nothing, and the concrete answers it is choosing between.
type AskItem struct {
	// Text is the question, in the user's language.
	Text string `json:"text"`
	// Assumption is what the agent will do if this one is left unanswered. It is shown next to
	// the question so the user can accept it without typing anything.
	Assumption string `json:"assumption,omitempty"`
	// Options are the concrete answers to offer, in the user's words. Empty is normal: it means
	// the question has no short list of plausible answers.
	Options []string `json:"options,omitempty"`
}

// Kinds of message the analysis can report.
const (
	// KindTask means there is work to do.
	KindTask = "task"
	// KindChat means there is nothing to do: answer and stop.
	KindChat = "chat"
	// KindAsk means the agent cannot tell what to do and must ask.
	KindAsk = "ask"
)

// resolveKind returns the kind with the default applied.
//
// An unrecognised or empty kind is "task", which is what every prompt written before this
// field existed meant. That default is deliberate: a misconfigured or older prompt must keep
// the previous behaviour rather than silently turn every request into a chat.
func (a Analysis) resolveKind() string {
	switch strings.ToLower(strings.TrimSpace(a.Kind)) {
	case KindChat:
		return KindChat
	case KindAsk:
		return KindAsk
	default:
		return KindTask
	}
}

// Plan is the structured output of phase [4].
type Plan struct {
	Steps          []Step   `json:"plan"`
	Subtasks       []string `json:"subtasks"`
	ExpectedResult string   `json:"expected_result"`
}

// Step is one step of the plan.
type Step struct {
	Number  int    `json:"step"`
	Action  string `json:"action"`
	Command string `json:"command"`
}

// Action is the structured output of phase [6].
type Action struct {
	Reasoning string    `json:"reasoning"`
	Actions   []Command `json:"actions"`
	Final     Command   `json:"final_action"`
	// Done is the model's own report that the task is FINISHED, and it is what turns the
	// loop from a retry loop into a progress loop.
	//
	// Without it the loop returned the moment the anchor was happy — and the anchor checks
	// the state of the PROJECT, not how much of the plan was carried out, so a healthy
	// repository passed before any work had happened. Measured: a plan of eleven steps, one
	// batch of four actions, "task completed" in twelve seconds.
	//
	// It defaults to TRUE when absent, which is what keeps every existing configuration
	// working: a prompt that predates this field does not send it, and reading that as "not
	// finished" would spend the whole step budget on a task that was already done.
	Done *bool `json:"done"`
	// Notes is the model's own running summary: what it has decided, what is done, what is
	// left. It is kept whole between rounds and replaced whenever a reply carries new ones.
	Notes string `json:"notes"`
}

// isDone reports whether the model considers the task finished.
//
// A nil pointer means the field was absent, and absent means finished: the behaviour of
// every prompt that predates it.
func (a Action) isDone() bool {
	return a.Done == nil || *a.Done
}

// Command is an executable action.
type Command struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Command     string `json:"command"`
}

// TaskResult is the final verdict of one task.
type TaskResult struct {
	Task        string         `json:"task"`
	Pass        bool           `json:"pass"`
	Attempts    int            `json:"attempts"`
	Subtasks    int            `json:"subtasks"`
	DurationMS  int64          `json:"duration_ms"`
	Validation  *anchor.Result `json:"validation,omitempty"`
	FinalAction string         `json:"final_action,omitempty"`
	Reason      string         `json:"reason"`
	// Summary is a human-readable answer produced by the model and shown in the UI.
	Summary string `json:"summary,omitempty"`
	// Report is the same account as structured data, for a front end that lays it out itself.
	// It is nil when the model produced no summary; Summary is then empty too.
	Report *Report `json:"report,omitempty"`
	// NeedsInput is set when the agent could not interpret the request well enough to act and is
	// asking the user instead of guessing. Question carries what to ask and Assumption what it
	// would do without an answer.
	//
	// This is not a failure and must not be reported as one: nothing went wrong, information is
	// missing, and the user is the one holding it.
	NeedsInput bool   `json:"needs_input,omitempty"`
	Question   string `json:"question,omitempty"`
	Assumption string `json:"assumption,omitempty"`

	// Kind is what the message turned out to be: "task", "chat" or "ask". The interface uses it
	// to decide how to present the result — a reply is shown as a reply, not as a task that ran.
	Kind string `json:"kind,omitempty"`
	// Reply is the conversational answer when Kind is "chat". It is the whole output of that
	// turn: no task ran, and nothing was validated, because there was nothing to do.
	Reply string `json:"reply,omitempty"`

	// Options are the concrete answers offered with a Question, so the interface can show them
	// as a pickable list. Empty means the question has no short list of plausible answers, which
	// is normal and is not a failure.
	Options []string `json:"options,omitempty"`

	// Questions are the questions of a turn that needed several answered together. It is empty
	// when there is a single Question, so a caller can read Question alone and still work.
	Questions []AskItem `json:"questions,omitempty"`
}

// Answers pairs a question with what the user replied, and is how the interface hands a
// multi-question turn back to the agent.
//
// Kept as question-and-answer rather than a bare list of strings so the answers cannot drift out
// of step with the questions they belong to: the agent re-reads the request with the answers
// attached to the exact gaps they fill.
type Answers struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// --- Main loop --------------------------------------------------------------

// report sends a progress line to the conversational UI when one is attached.
func (a *Agent) report(format string, args ...any) {
	if a.Progress != nil {
		a.Progress(format, args...)
	}
}

// Run processes tasks from the source until it is exhausted (io.EOF) or the
// context is cancelled (graceful shutdown).
func (a *Agent) Run(ctx context.Context) error {
	if strings.EqualFold(a.cfg.Anchor.Kind, "none") || a.cfg.Anchor.Kind == "" {
		// Without an anchor there is no authority to declare PASS, so every task
		// would end up escalated after burning attempts against the LLM. Failing
		// now, with the concrete fix, is cheaper and more honest.
		return errors.New("anchor.kind=none: this agent only declares a task complete " +
			"when a deterministic validator gives PASS, and none is configured.\n" +
			"   Configure a real anchor, for example:\n" +
			"     anchor:\n       kind: command\n       command: make\n       args: [test]\n" +
			"   If you only want to exercise the loop without validating anything, make it explicit:\n" +
			"     anchor:\n       kind: command\n       command: true\n" +
			"   Check the configuration with: motita -config <file> -validate-config")
	}

	a.log.Info("agent started",
		"source", a.source.Describe(),
		"provider", a.cfg.LLM.Provider,
		"model", a.cfg.LLM.Model,
		"anchor", a.cfg.Anchor.Kind,
		"sandbox", strings.Join(modesToStrings(a.sandbox.Isolation()), ", "),
		"workspace", a.cfg.Agent.WorkspaceDir,
		"max_retries", a.cfg.Agent.MaxRetries)

	defer a.source.Close()

	total := 0
	failed := 0
	// awaiting counts the tasks that stopped to ask a question.
	awaiting := 0
	reasons := []string{}
	// Consecutive source failures are counted separately: a source that fails
	// forever (a directory that disappeared, an unreachable API) must not spin
	// the loop at full speed burning CPU and log. After a few in a row the agent
	// gives up with a clear error instead of hanging silently.
	consecutive := 0
	sourceFailures := 0
	const maxConsecutive = 5

	for {
		select {
		case <-ctx.Done():
			a.log.Info("shutdown requested, finishing", "tasks_processed", total)
			return nil
		default:
		}

		if a.cfg.Agent.MaxTasks > 0 && total >= a.cfg.Agent.MaxTasks {
			a.log.Info("agent.max_tasks reached", "tasks", total)
			break
		}

		t, err := a.source.Next(ctx)
		if errors.Is(err, io.EOF) {
			a.log.Info("no more tasks", "tasks_processed", total)
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// A source failure is NOT a failed task: no task was taken. Counting
			// it as one produced nonsense totals such as "2 of 1 tasks failed".
			// It is tracked on its own and surfaced through the error below.
			sourceFailures++
			consecutive++
			a.log.Error("could not obtain the task", "error", err,
				"consecutive", consecutive, "source_failures", sourceFailures)

			if consecutive >= maxConsecutive {
				// The source is broken, not just momentarily empty. Returning
				// makes the failure visible and actionable (cron/systemd see a
				// non-zero exit) instead of looping forever.
				return fmt.Errorf("the task source failed %d times in a row, giving up: %w", consecutive, err)
			}
			continue
		}
		consecutive = 0
		if strings.TrimSpace(t.Description) == "" {
			continue
		}

		total++
		result := a.processTask(ctx, t, 0)

		// A task that ended in a QUESTION is not a task that failed.
		//
		// It did not run, and nothing went wrong: the agent could not read the request well
		// enough and is asking. Counting it as a failure is what turned a perfectly good question
		// into "1 of 1 tasks failed" in the interface, which tells the user they did something
		// wrong at the exact moment the agent is asking for their help.
		//
		// It is reported as a non-failure so the interface can show the question, and it does not
		// make the run exit non-zero: the run is WAITING, not broken.
		if result.NeedsInput {
			awaiting++
			a.log.Info("task is waiting for the user", "task", truncate(t.Description, 120),
				"question", truncate(result.Question, 200))
			continue
		}

		if result.Pass {
			a.log.Info("task completed",
				"task", truncate(t.Description, 120),
				"attempts", result.Attempts,
				"duration_ms", result.DurationMS,
				"final_action", result.FinalAction)
		} else {
			failed++
			reasons = append(reasons, fmt.Sprintf("%q: %s", truncate(t.Description, 80), result.Reason))
			a.log.Error("task failed",
				"task", truncate(t.Description, 120),
				"attempts", result.Attempts,
				"reason", result.Reason)
		}
	}

	a.log.Info("agent finished", "tasks", total, "failed", failed, "awaiting_input", awaiting)
	if failed > 0 {
		// The reason of every failure is included: whoever reads the error (or
		// cron's output) must be able to act without digging through the log.
		return fmt.Errorf("%d of %d tasks failed: %s", failed, total, strings.Join(reasons, " | "))
	}
	return nil
}

// processTask runs the 9-step loop for one task and notifies the observer of the
// result (exactly once, whatever the exit path).
func (a *Agent) processTask(ctx context.Context, t task.Task, depth int) TaskResult {
	if depth == 0 {
		a.begin(t)
	}
	r := a.loop(ctx, t, depth)
	if a.Observer != nil {
		a.Observer(r)
	}
	// A task that RAN is part of the conversation, whatever it ended up doing: it passed, it failed,
	// or it stopped to ask something. Recording it here rather than on the success path is what
	// makes that true on every exit - `loop` returns from seven places, and a turn that failed is
	// exactly the one the next turn needs to be able to see.
	//
	// Only the OUTERMOST call records. A task that split into subtasks goes through here once per
	// subtask, and five subtasks would otherwise read back to the user as five separate things they
	// asked for, when they asked for one.
	//
	// The turns that are already recorded keep their own record: a chat reply and a clarifying
	// question are written where they are decided, because what they said is known only there. What
	// this adds is the outcome of work that was actually done.
	if depth == 0 && r.Kind != KindChat && !r.NeedsInput {
		outcome := taskOutcome(r)
		// A run that did not finish keeps what it had done: the result alone ("cancelled",
		// "stalled") would erase the only record of the work, and the next turn - or a
		// person reading the conversation - starts from nothing.
		if !r.Pass {
			if work := a.workSoFar(t); work != "" {
				outcome += "\nWork done before it stopped:\n" + work
			}
		}
		a.note(t, outcome, KindTask)
	}
	return r
}

// taskOutcome is the one line a finished task leaves in the conversation.
//
// It prefers what the agent said in its own words - the synthesised summary - and falls back to the
// verdict, so a turn is never recorded as having done something without saying what it was.
func taskOutcome(r TaskResult) string {
	if s := strings.TrimSpace(r.Summary); s != "" {
		return s
	}
	if s := strings.TrimSpace(r.Reply); s != "" {
		return s
	}
	if s := strings.TrimSpace(r.Reason); s != "" {
		return s
	}
	// A result with nothing to say is not a reason to record an empty turn: an empty turn would read
	// back as a message the user never sent.
	if r.Pass {
		return "the task completed"
	}
	return "the task did not complete"
}

// loop is the 9-step loop itself.
func (a *Agent) loop(ctx context.Context, t task.Task, depth int) TaskResult {
	start := time.Now()
	res := TaskResult{Task: t.Description, Attempts: 0}

	// proceededOnAssumption records the reading the agent adopted when it had nobody to ask, so
	// the final report can say what it assumed instead of presenting the result as unqualified.
	proceededOnAssumption := ""

	prefix := strings.Repeat("  ", depth)
	a.log.Info(prefix+"task received", "origin", t.Origin, "depth", depth, "description", truncate(t.Description, 200))

	// [2] Extract context and success criteria: the statement of the validation
	// rules shown to the LLM is built here, so it knows what it will be measured
	// against.
	rules := a.describeRules()

	// [3] Analysis.
	a.report("analysing the task...")
	analysis := a.analysisPhase(ctx, t, rules, depth)

	// A SUBTASK is work by construction: the plan that declared it already decided there is
	// something to do. It must not be answered as chat - that counted as a PASS for work never
	// done - and it cannot stop to ask: its question would surface as the parent's failure
	// ("only 0 of 3 subtasks passed") with the question itself lost, because the user is
	// talking to the PARENT turn. It proceeds on the reading the analysis stated instead.
	if depth > 0 {
		if analysis.resolveKind() != KindTask || !analysis.Understandable || analysis.Question != "" {
			a.log.Info(prefix+"a subtask cannot chat or ask: proceeding as work",
				"kind", analysis.Kind, "question", truncate(analysis.Question, 160))
			if assumed := cleanAssumption(analysis.Assumption); assumed != "" {
				proceededOnAssumption = assumed
			}
		}
		analysis.Kind = KindTask
		analysis.Understandable = true
		analysis.Question = ""
		analysis.Questions = nil
	}

	// The message is not always a task. A greeting, a question about what just happened, or
	// thinking out loud has nothing to do, and running the loop for it would plan work nobody
	// asked for and report a result nobody read.
	//
	// This is checked FIRST, before the ask/assume logic below, because it is a different
	// question: "is there work here?" comes before "can I read the work well enough?".
	switch analysis.resolveKind() {
	case KindChat:
		reply := strings.TrimSpace(analysis.Reply)
		if reply == "" {
			// The model classified it as chat but wrote nothing. Saying so is better than
			// falling through to the loop: the classification is still the model's judgement,
			// and a task plan is the one answer the user did not ask for.
			//
			// The line is in English because everything else the interface says is: the
			// prompts, the help screen, the errors. One sentence in another language would
			// be the only one of its kind, and there is no language setting to make it
			// correct for the user reading it.
			reply = "There is nothing to run in your message."
		}
		res.Kind = KindChat
		res.Reply = reply
		res.Pass = true
		res.Reason = "conversational reply"
		res.DurationMS = time.Since(start).Milliseconds()
		a.report("%s", reply)
		a.converse(t, reply)
		a.log.Info(prefix+"answered as chat", "reply", truncate(reply, 200))
		return res
	case KindAsk:
		// Forced into the asking path even when the model left "understandable" true: the kind
		// is the newer signal and saying "ask" is unambiguous.
		analysis.Understandable = false
	}

	if !analysis.Understandable || analysis.Question != "" {
		// The request could not be read well enough to act on. ASK, do not refuse.
		//
		// This used to end the turn with "the task was declared not understandable", which throws
		// away everything the user typed and makes them write it again — for a request they can
		// usually clarify in three words. A user mistypes, abbreviates, and omits what they think
		// is obvious; the agent's job is to close that gap, not to report it.
		//
		// The question goes back through the interface as a normal result, so the user sees it in
		// the conversation and answers with their next message. The assumption travels with it:
		// it is what the agent would do without an answer, which lets the user confirm with a
		// single word, and it is also the honest fallback if the interface has nobody to ask.
		res.NeedsInput = true
		res.Question = strings.TrimSpace(analysis.Question)
		// Cleaned here as well as in the question, because this field is the one summarise
		// prints its own "If you do not tell me otherwise, I will assume:" in front of. Left
		// raw it produced the stutter the window showed. spanish-fixture: the model emitted
		// spanish-fixture: the run produced the lead twice over, and it has to come out once:
		// spanish-fixture: "Si no me dices otra cosa, asumiré: Si no me dices otra cosa,
		// spanish-fixture: reviso el proyecto".
		// The two paths (the single question and
		// the list) both need the cleaned form, and this is the one that reaches the chat.
		res.Assumption = cleanAssumption(analysis.Assumption)
		res.Options = cleanOptions(analysis.Options)
		res.Questions = buildQuestions(analysis)
		res.Reason = strings.Join(analysis.Risks, "; ")
		res.DurationMS = time.Since(start).Milliseconds()

		// A turn may report its gap in EITHER shape — the single fields or the list — and the
		// rest of this function reads the single fields. Left alone, a turn that used the list
		// looked like it had asked nothing: question empty, assumption empty, and the dead-end
		// check below failed a turn that had two questions ready to show. That is what the
		// hardware run reported as "task not understandable:" followed by "failed:", and it is
		// the failure mode the list form makes common, because the prompt tells the model to use
		// it whenever a request has several gaps.
		//
		// So the single fields are derived from the list when they are empty, rather than the
		// list being a second, parallel path. One source of truth, and every reader below keeps
		// working whether the model answered in one shape or the other.
		if len(res.Questions) > 0 {
			if res.Question == "" {
				res.Question = res.Questions[0].Text
			}
			if res.Assumption == "" {
				res.Assumption = res.Questions[0].Assumption
			}
		}

		// Nothing to ask AND nothing to assume is a genuine dead end, and it stays a failure.
		//
		// The difference is worth being precise about. A QUESTION is a request the agent can
		// name and the user can answer; an ASSUMPTION is a reading the agent can act on. With
		// neither, there is nothing to ask and nothing to do, and inventing a generic "what did
		// you mean?" would be asking the user to repeat what they just said — the report is
		// clearer, and a batch run still exits non-zero for cron to notice.
		if res.Question == "" && res.Assumption == "" {
			res.NeedsInput = false
			res.Question = ""
			res.Reason = strings.Join(analysis.Risks, "; ")
			a.report("task not understandable: %s", res.Reason)
			return res
		}
		if res.Question == "" {
			// The model named an assumption but no question. Asking is still right — the user can
			// confirm or correct the reading in one word — so the question is derived from it.
			res.Question = "Am I on the right track? If not, tell me exactly what you want."
		}

		// Nobody to ask: proceed on the assumption instead of stalling.
		//
		// A batch run has no user at the other end, so a question would wait forever. The
		// assumption the model itself proposed is the reading to act on — and acting on a stated
		// assumption is what an engineer does when the ticket is thin, not refusing to work.
		if !a.Interactive && res.Assumption != "" {
			a.log.Info("no user to ask: proceeding on the stated assumption",
				"question", truncate(res.Question, 160), "assumption", truncate(res.Assumption, 200))
			a.report("no user to ask; assuming: %s", res.Assumption)
			analysis.Understandable = true
			analysis.Question = ""
			// No fallback for an empty summary is needed: analysisPhase already replaced it with
			// the task description when the model left it blank, so by this point it always has
			// something. A branch here would be unreachable.
			//
			// The result describes the run that is now PROCEEDING, not the question that was
			// skipped: leaving these set would report a finished run as one that is still waiting
			// for an answer. They are re-applied below if the run later fails.
			res.NeedsInput = false
			res.Question = ""
			res.Assumption = ""
			proceededOnAssumption = analysis.Summary
		} else {
			// The phase line carries the question itself, in the user's language (the model
			// writes it), so no English label is put in front of it. spanish-fixture: putting
			// spanish-fixture: an English label in front of the question a model emitted,
			// spanish-fixture: "¿qué carpeta?", mixed the two languages in one line and read
			// like a system error rather than the agent asking something.
			// mixed two languages in one line and read like a system error rather than the
			// agent asking something.
			a.report("%s", res.Question)
			a.log.Info("asking the user instead of guessing", "question", truncate(res.Question, 200),
				"assumption", truncate(res.Assumption, 200))
			// The question is recorded in the conversation. Their next message answers it, and
			// without this the answer arrives with nothing to refer to — which is exactly the
			// amnesia that makes a clarifying question useless.
			said := res.Question
			if res.Assumption != "" {
				// Printed with no lead of the interface's own, for the same reason as in the
				// TUI: the assumption is already a sentence in the user's language, and a
				// hardcoded lead would fix the conversation's language from the program.
				said += "\n\n" + res.Assumption
			}
			a.note(t, said, KindAsk)
			return res
		}
	}
	a.report("understood: %s", analysis.Summary)
	if len(analysis.SuccessCrit) > 0 {
		a.report("%sdone means:\n- %s", ThinkingPrefix, strings.Join(analysis.SuccessCrit, "\n- "))
	}

	// [4] Plan.
	a.report("planning...")
	plan := a.planPhase(ctx, t, analysis, depth)
	a.report("plan ready: %d steps", len(plan.Steps))
	if len(plan.Steps) > 0 {
		a.report("%splan:\n%s", ThinkingPrefix, strings.TrimRight(describePlan(plan), "\n"))
	}

	// [5] Splitting into subtasks: each one re-enters the same flow, one level
	// further down. The limit is respected to avoid infinite recursion.
	//
	// The PLAN is what decides. It used to require a second vote from the analysis phase
	// (`analysis.NeedsSubtasks`), and when the two disagreed the plan's own subtasks were
	// silently dropped: measured in a real run, `plan generated` reported five subtasks and
	// the line below never executed, so five declared pieces of work were thrown away.
	// A model that wrote down subtasks has said there are subtasks; asking it twice only
	// adds a way to lose them.
	//
	// Blank entries are dropped BEFORE deciding: a plan whose subtasks are all blank has no
	// subtasks, and it used to end as "only 0 of 0 subtasks passed validation" - a failure
	// for a task nobody ever tried.
	if subs := nonBlank(plan.Subtasks); len(subs) > 0 {
		if depth >= a.cfg.Agent.SubtaskDepth {
			a.log.Warn(prefix+"subtask splitting reached the configured limit; continuing as a single task",
				"depth", depth, "limit", a.cfg.Agent.SubtaskDepth,
				"subtasks", len(subs))
		} else {
			return a.runSubtasks(ctx, t, analysis, subs, depth, start, prefix)
		}
	}

	// [6]-[9] Execution and validation cycle.
	//
	// THE LOOP CONTRACT. Every round ends in exactly one of these, and each has its own bound:
	//
	//  - PROGRESS: the model ran actions and reports work left ("done": false). It is work,
	//    not a failure, so it spends only the step budget. The anchor does NOT run on these
	//    rounds: the state halfway through a change routinely breaks the project's gate (a
	//    renamed function before its callers are updated), and charging that to max_retries
	//    killed multi-step tasks at the fourth intermediate round. It also cost the whole
	//    gate - measured at ~45 s on a real project - on every round of a long task.
	//  - CLAIM: the model reports the task done. The anchor runs, always, and it alone
	//    decides: PASS ends the task, a refusal is a REJECTED claim, and those are what
	//    max_retries bounds.
	//  - UNUSABLE: the reply could not be used (malformed JSON, or nothing to run while
	//    claiming work is left). One bad reply used to end the whole task; it is now
	//    told to the model, and only maxUnusableReplies IN A ROW end the run.
	//  - STALL: a progress round identical to the one before it, same actions and same
	//    output. The model is warned, and a run that keeps repeating itself is stopped
	//    instead of spending the rest of the budget going nowhere.
	//
	// Every round is recorded in the journal the next round reads, labelled with what it
	// WAS. It used to be labelled "failed attempt" whatever happened, so a model that made
	// progress was told it had failed and redid the work.
	journal := []roundRecord{}
	// mem is what the run remembers besides the journal: the whole output of every read
	// still valid, and the model's own notes. See workmemory.go for why the journal alone
	// made a run re-read the same files a hundred times.
	mem := &workMemory{}
	// The sources as the run found them. It is what a claim of "done" is measured against: a
	// project's own checks are green on a tree nobody touched, so the anchor alone cannot tell
	// "finished" from "never started". See tree.go.
	baseline, baselineOK := treeFingerprint(a.cfg.Agent.WorkspaceDir, true)
	lastSources := baseline
	readOnlyRounds := 0
	unbackedDone := 0
	verifyChallenges := 0
	toolingRetries := 0
	var trail []string
	// A run that RESUMES after an interruption (a restart, a crash) begins with the work its
	// earlier life recorded, instead of an empty journal. The files it changed are on disk,
	// but the reasons and the order are not: without this the model reads the request as new,
	// re-reads the project and rebuilds what it already built.
	if prior := a.workSoFar(t); prior != "" {
		trail = []string{prior}
		journal = append(journal, roundRecord{
			round: 0, kind: roundProgress, commands: "(before the interruption)",
			detail: "This task was already being worked on when the run was interrupted. What " +
				"it had done, oldest first:\n" + prior + "\nThe changes are in the working " +
				"directory. Check it with `git status` and `git diff` FIRST, then continue " +
				"with what is left: do not start over.",
		})
	}
	// workLog is what every round ran and printed, for the final answer: the synthesis used
	// to see only the LAST round, which on a long task is a final `git status` and nothing
	// of the work.
	var workLog strings.Builder
	rejected := 0
	unusable := 0
	repeats := 0
	lastSignature := ""
	maxSteps := a.maxSteps()
	// The budget is a CHECKPOINT, not a wall. Measured on a real request: the agent was
	// still working when 24 rounds ran out, and the run ended with "the task is not
	// finished" and nothing else - the user had to start over and hope the next attempt
	// got further. The work it had done was in the checkout; the DECISION left to the
	// user was simply never offered.
	//
	// So when the budget runs out with work left, the loop asks. Yes means another
	// maxSteps rounds on the SAME run, continuing from everything already done, which
	// is the difference between "keep going" and "start again".
	//
	// The number of times it may ask is not bounded here on purpose: each ask is a
	// human answering, and a human who keeps saying yes is not a runaway loop.
	round := 0
	for {
		if rounded := round / maxSteps; rounded > 0 && round%maxSteps == 0 {
			// One full budget has been spent. Ask before spending another.
			cont, err := a.continueBudget(ctx, round, maxSteps, prefix)
			if err != nil || !cont {
				res.Reason = fmt.Sprintf(
					"the task is not finished: %d rounds were used and the model still reports work left. "+
						"Raise agent.max_steps, or split the request into smaller tasks", round)
				res.DurationMS = time.Since(start).Milliseconds()
				a.report("%s", res.Reason)
				a.log.Warn(prefix+"the step budget ran out and the user did not continue",
					"max_steps", maxSteps, "rounds", round)
				a.escalate(ctx, prefix)
				return res
			}
			a.report("continuing with another %d rounds...", maxSteps)
		}
		round++
		if round > maxSteps*maxBudgetExtensions {
			// A ceiling on the ceiling. A gateway whose approver answers "yes" without a
			// human (a script, a test) would otherwise loop for ever; the limit is high
			// enough that no real session reaches it, because reaching it means 100
			// answered questions.
			res.Reason = fmt.Sprintf(
				"the task is not finished after %d rounds and %d continuations, so this run stops here. "+
					"Split the request into smaller tasks", round-1, maxBudgetExtensions-1)
			res.DurationMS = time.Since(start).Milliseconds()
			a.report("%s", res.Reason)
			a.escalate(ctx, prefix)
			return res
		}
		res.Attempts = round

		// [6] The LLM proposes the concrete action.
		a.report("deciding action (round %d/%d)...", round, maxSteps*(1+(round-1)/maxSteps))
		action, err := a.actionPhase(ctx, t, analysis, plan, journal, mem, round, prefix)
		if err == nil && len(action.Actions) == 0 && !action.isDone() {
			// Nothing to run while claiming there is work left is not a round of work: it is
			// a reply the loop cannot act on, and the model is told so rather than being
			// credited with progress it did not make.
			err = errors.New(`the reply proposed no actions but reported "done": false; ` +
				`propose the next actions, or report "done": true if the task is complete`)
		}
		if err != nil {
			if ctx.Err() != nil {
				res.Reason = "the run was cancelled"
				res.DurationMS = time.Since(start).Milliseconds()
				return res
			}
			unusable++
			a.log.Warn(prefix+"the action phase produced nothing usable",
				"round", round, "in_a_row", unusable, "error", err.Error())
			if unusable >= maxUnusableReplies {
				// The CAUSE is carried, and it is the LLM's own message. The old prefix said
				// "could not obtain the action from the LLM" and left the reader to guess: the
				// action was not missing, the reply arrived shaped in a way this phase cannot
				// use, and the message below now says which shape and which tool.
				res.Reason = "could not obtain the action from the LLM: " + err.Error()
				res.DurationMS = time.Since(start).Milliseconds()
				a.report("failed to get an action: %v", err)
				a.escalate(ctx, prefix)
				return res
			}
			a.report("the reply could not be used, asking again: %s", truncate(err.Error(), 160))
			journal = append(journal, roundRecord{
				round: round, kind: roundUnusable, commands: "(no usable reply)",
				detail: "The reply could not be used: " + truncate(err.Error(), 1000) +
					"\nAnswer with the JSON object exactly as the ACTION section shows it.",
			})
			continue
		}
		unusable = 0
		// The model's reasoning for this round, whole. It used to be cut at 120 characters,
		// which is where the reasoning usually starts to say something.
		if r := strings.TrimSpace(action.Reasoning); r != "" {
			a.report("%s%s", ThinkingPrefix, r)
		}

		// [7] Run in the sandbox.
		mem.setNotes(action.Notes)
		mem.executed, mem.recalled = 0, 0
		runOutput, runErr := a.runRound(ctx, action.Actions, prefix, mem, round)
		if strings.TrimSpace(runOutput) != "" {
			fmt.Fprintf(&workLog, "## Round %d\n%s\n", round, runOutput)
		}

		if !action.isDone() {
			// PROGRESS. The anchor is not consulted - see the loop contract above - and
			// nothing is charged to max_retries. The round is recorded so the next one
			// continues from it instead of proposing the same first step again.
			detail := "Actions:\n" + commandList(action) + "\nOutput:\n" + truncate(runOutput, 3000)
			if runErr != nil {
				detail += "\nExecution error: " + runErr.Error()
			}
			signature := proposedCommands(action) + "\x00" + runOutput
			// A round that only asked for things it already holds did nothing, whatever
			// its commands were: it counts as a repeat even when the wording differs.
			if signature == lastSignature || (mem.recalled > 0 && mem.executed == 0) {
				repeats++
			} else {
				repeats = 0
			}
			lastSignature = signature
			if repeats >= stallStopAt {
				res.Reason = fmt.Sprintf("the run stalled: the same actions produced the same output %d rounds in a row "+
					"without the task being reported done. Rephrase the request, or split it into smaller tasks", repeats+1)
				res.DurationMS = time.Since(start).Milliseconds()
				a.report("%s", res.Reason)
				a.log.Warn(prefix+"the run stalled", "round", round, "repeats", repeats+1,
					"commands", proposedCommands(action))
				a.escalate(ctx, prefix)
				return res
			}
			if repeats >= stallWarnAt {
				detail += "\n!! This round repeated the previous one exactly: same actions, same output. " +
					"Repeating it again will not change anything. Do something DIFFERENT, or report " +
					`"done": true if the task is already complete.`
			}
			// Rounds that only READ. Reported from a real session: thirteen rounds of reading and no
			// writing, each one re-reading what the last had already shown. The model is told how
			// long it has been reading, and that what it read is in front of it.
			if cur, ok := treeFingerprint(a.cfg.Agent.WorkspaceDir, true); ok && cur != lastSources {
				lastSources, readOnlyRounds = cur, 0
			} else {
				readOnlyRounds++
			}
			if readOnlyRounds >= readOnlyWarnAt {
				detail += fmt.Sprintf("\n!! %d rounds in a row have only READ; no file has changed. What you read "+
					"is kept in full under \"WHAT YOU HAVE ALREADY READ\". Stop exploring and WRITE the change "+
					"now: create or edit the files this request needs, in this round. If one specific thing is "+
					"still missing, read only that, in the same round as the writing.", readOnlyRounds)
			}
			// A check that failed this round is the next round's starting point.
			if failed := mem.failedInRound(round); len(failed) > 0 {
				detail += failedCheckNote(failed)
			}
			journal = append(journal, roundRecord{
				round: round, kind: roundProgress, commands: proposedCommands(action), detail: detail,
			})
			trail = append(trail, fmt.Sprintf("round %d: %s", round, truncate(collapse(proposedCommands(action)), 200)))
			if depth == 0 {
				a.trail(t, trail)
			}
			a.report("round %d done; the model reports more to do", round)
			a.log.Info(prefix+"continuing: the model reports work left",
				"round", round, "max_steps", maxSteps,
				"reasoning", truncate(action.Reasoning, 200))
			continue
		}
		repeats, lastSignature = 0, ""

		// [7b] A claim of "done" over a tree that has not changed. Reported from a real session:
		// thirteen rounds of reading, no file written, "done" - and the anchor PASSED, because the
		// project's lint, typecheck and tests are green on code nobody touched. The run closed as
		// complete and the answer said, truthfully, that nothing had been changed.
		//
		// It is told ONCE, and it is not a rejection: nothing is charged to max_retries and the
		// anchor is not spent. A request that only needed an answer (an investigation, a question
		// about the code) is not blocked - the model says so and claims done again, and that claim
		// goes through. What it can no longer do is finish a change it never made without being
		// asked whether that is what it meant.
		if cur, ok := treeFingerprint(a.cfg.Agent.WorkspaceDir, true); ok && baselineOK && cur == baseline && unbackedDone == 0 && readOnlyRounds >= doneGuardAfter {
			unbackedDone++
			a.report("done claimed but nothing has been changed yet; asking the model to confirm")
			a.log.Warn(prefix+"done claimed over an unchanged working tree", "round", round)
			journal = append(journal, roundRecord{
				round: round, kind: roundProgress, commands: proposedCommands(action),
				detail: "You reported \"done\": true, but NO FILE in the working directory has changed since " +
					"the task began. If the request asked you to add, change, fix or remove something, " +
					"then nothing has been done yet: you already hold what you read under \"WHAT YOU HAVE " +
					"ALREADY READ\", so WRITE the change in this round (create or edit the files), then " +
					"run the project's checks and a test for what you added. If the request only needed " +
					"an answer or an investigation and no file should change, report \"done\": true again " +
					"and say in \"notes\" that nothing had to change and why.",
			})
			continue
		}

		// [7c] A claim of "done" while a check the run ran ITSELF is still failing. Reported from a
		// real session: the server's tests failed 22 of 45, the run called them pre-existing without
		// comparing, never tested the endpoint it had written, and claimed done - with the anchor
		// green, because the anchor only runs what the project declares. It is sent back, without
		// charging the anchor or max_retries, up to maxVerifyChallenges times: a run that truly cannot
		// fix a check (a service that is not here) still ends, with the gap in its report.
		if open := mem.openFailures(); len(open) > 0 && verifyChallenges < maxVerifyChallenges {
			verifyChallenges++
			a.report("done claimed while a check is still failing; sending the run back to it (%d/%d)", verifyChallenges, maxVerifyChallenges)
			a.log.Warn(prefix+"done claimed over a failing check", "round", round,
				"check", truncate(open[0].command, 160), "challenge", verifyChallenges)
			journal = append(journal, roundRecord{
				round: round, kind: roundProgress, commands: proposedCommands(action),
				detail: unresolvedCheckChallenge(open, verifyChallenges),
			})
			continue
		}

		// [8] The model claims the task is done: validate with the anchor, always.
		a.report("validating with anchor...")
		validation := anchor.New(a.cfg.Anchor, a.cfg.Agent.WorkspaceDir, a.sandbox).Validate(ctx)
		res.Validation = &validation

		if validation.Pass && runErr == nil {
			// [9] Validation PASS and the model reports the task finished: now the final action.
			a.report("validation passed; running final action...")
			finalAction, finalErr := a.runFinalAction(ctx, action.Final, prefix)
			res.FinalAction = finalAction
			if finalErr != nil {
				detail := fmt.Sprintf("validation passed but the final action failed: %v\nOutput: %s", finalErr, finalAction)
				journal = append(journal, roundRecord{
					round: round, kind: roundRejected, commands: proposedCommands(action), detail: detail,
				})
				rejected++
				a.report("final action failed: %v", finalErr)
				a.log.Error(prefix+"final action failed", "round", round, "final_action", finalAction, "error", finalErr)
				if rejected > a.cfg.Agent.MaxRetries {
					res.Reason = detail
					res.DurationMS = time.Since(start).Milliseconds()
					a.escalate(ctx, prefix)
					return res
				}
				continue
			}
			res.Pass = true
			res.Reason = validation.Reason
			res.DurationMS = time.Since(start).Milliseconds()
			a.report("task complete: %s", validation.Reason)
			if a.cfg.Prompts.Synthesize.User != "" && a.cfg.Prompts.Synthesize.System != "" {
				a.report("synthesizing answer...")
				if rep := a.synthesizePhase(ctx, t, workLog.String(), validation); rep != nil {
					res.Summary, res.Report = rep.Summary, rep
					a.report("%s", rep.Summary)
				}
			}
			// When there was nobody to ask, the run proceeded on a reading the agent chose. Saying
			// so is what makes the result honest: the answer is correct GIVEN that reading, and a
			// user reading it later needs to know which one was taken — otherwise a reasonable
			// assumption looks like a wrong answer.
			if proceededOnAssumption != "" {
				res.Assumption = proceededOnAssumption
				a.report("(proceeded assuming: %s)", proceededOnAssumption)
			}
			a.log.Info(prefix+"validation passed", "round", round, "final_action", finalAction,
				"assumed", truncate(proceededOnAssumption, 160))
			return res
		}

		// The anchor refused a claim of "done". THAT is a correction, and max_retries is
		// what bounds it — separately from the rounds counter above, because the two answer
		// different questions.
		//
		// A gate that failed because a TOOL IS NOT INSTALLED (no node_modules, no venv) says
		// nothing about the code, so it is not charged: the run is told to install the project's
		// dependencies and try again. Bounded, so a machine that can never install them still ends.
		tooling := missingTooling(validation) && toolingRetries < maxToolingRetries
		if tooling {
			toolingRetries++
		} else {
			rejected++
		}
		journal = append(journal, roundRecord{
			round: round, kind: roundRejected, commands: proposedCommands(action),
			detail: a.summariseFailure(action, runOutput, validation, runErr),
		})
		a.report("attempt failed: %s", validation.Reason)
		a.log.Warn(prefix+"attempt failed",
			"attempt", rejected, "max_attempts", a.cfg.Agent.MaxRetries+1,
			"round", round, "max_steps", maxSteps,
			"commands", proposedCommands(action),
			"validation", validation.Reason)

		if !tooling && rejected > a.cfg.Agent.MaxRetries {
			// The retries ran out. This is a DIFFERENT outcome from the step budget
			// running out, and the message has to say which one it was, or the reader
			// goes looking for a broken check.
			res.Reason = fmt.Sprintf("all %d attempts were exhausted without passing validation: %s",
				a.cfg.Agent.MaxRetries+1, validation.Reason)
			res.DurationMS = time.Since(start).Milliseconds()
			a.report("%s", res.Reason)
			a.escalate(ctx, prefix)
			return res
		}
	}
}

// readOnlyWarnAt is how many progress rounds in a row may only read before the model is told to
// write. Five is enough to explore a real project and too few to re-read it twice.
const readOnlyWarnAt = 5

// doneGuardAfter is how many rounds that only read must have gone by before a claim of "done"
// over an unchanged tree is questioned. A run that claims done at once is not second-guessed
// here (its check is the anchor and the synthesis, which says plainly what was changed); the
// pattern this exists for is the one that explores for many rounds and then closes.
const doneGuardAfter = 2

// maxToolingRetries is how many refusals caused by a missing tool are not charged to max_retries.
const maxToolingRetries = 3

// missingTooling reports whether every check that failed did so because a program or module it
// needs is not installed, which is a fact about the machine and not about the change.
func missingTooling(v anchor.Result) bool {
	failed := 0
	for _, c := range v.Checks {
		if c.Pass {
			continue
		}
		failed++
		out := strings.ToLower(c.Output + " " + c.Error)
		hit := false
		for _, sig := range []string{"command not found", ": not found", "cannot find module", "no module named",
			"module_not_found", "cannot find package", "executable file not found", "is not recognized as",
			"err_module_not_found", "could not find a package.json", "eslint: command not found"} {
			if strings.Contains(out, sig) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return failed > 0
}

// maxUnusableReplies is how many replies IN A ROW may be unusable before the run stops.
// One malformed reply is noise a model recovers from when told; three in a row is a model
// that cannot follow the format, and more rounds will not change that.
const maxUnusableReplies = 5

// stallWarnAt and stallStopAt bound a run that repeats itself: after stallWarnAt identical
// progress rounds in a row the model is warned, and at stallStopAt the run stops. Identical
// means the same actions AND the same output, so a poll whose output changes is not a stall.
const (
	stallWarnAt = 2
	stallStopAt = 4
)

// keepRoundsInFull is how many of the latest rounds the next round reads in full. Older
// rounds shrink to one line each: a long task would otherwise grow the prompt by up to 3 KB
// per round until the history crowded out the task - a hundred rounds is 300 KB.
const keepRoundsInFull = 6

// roundKind is what a round of the loop turned out to be. See the loop contract in loop.
type roundKind int

const (
	roundProgress roundKind = iota
	roundRejected
	roundUnusable
)

// label is the one-word name of the kind, for a round compacted to one line.
func (k roundKind) label() string {
	switch k {
	case roundProgress:
		return "progress"
	case roundRejected:
		return "rejected"
	default:
		return "unusable reply"
	}
}

// heading is what the model is told the round WAS. Getting this right is the point of the
// journal: a progress round labelled as a failure is how a model ends up undoing its own work.
func (k roundKind) heading() string {
	switch k {
	case roundProgress:
		return "PROGRESS: these actions ran and you reported more work left"
	case roundRejected:
		return "REJECTED: you reported the task done and it was NOT accepted; fix what this says"
	default:
		return "UNUSABLE: your reply could not be used"
	}
}

// roundRecord is one round, as the rounds after it are told about it.
type roundRecord struct {
	round    int
	kind     roundKind
	commands string // one line, for when the round is compacted
	detail   string // the full account, for the latest rounds
}

// renderRounds is the journal the execute phase reads as {{history}}.
func renderRounds(rounds []roundRecord) string {
	if len(rounds) == 0 {
		return "## PREVIOUS ROUNDS\n(none: this is the first round)"
	}
	var b strings.Builder
	b.WriteString("## PREVIOUS ROUNDS\n")
	b.WriteString("Everything already done for this task, oldest first. Continue from where it " +
		"stands: do not redo a round that made progress.\n")
	older := len(rounds) - keepRoundsInFull
	for i, r := range rounds {
		if i < older {
			fmt.Fprintf(&b, "- Round %d (%s): %s\n", r.round, r.kind.label(), r.commands)
			continue
		}
		fmt.Fprintf(&b, "\n### Round %d - %s\n%s\n", r.round, r.kind.heading(), r.detail)
	}
	return b.String()
}

// commandList renders the actions of a round, one per line.
func commandList(action Action) string {
	var b strings.Builder
	for _, c := range action.Actions {
		line := strings.TrimSpace(c.Command)
		if k := strings.ToLower(strings.TrimSpace(c.Kind)); k != "" && k != "command" {
			line = "[" + k + "] " + line
		}
		fmt.Fprintf(&b, "- %s\n", line)
	}
	return b.String()
}

// nonBlank returns the entries that carry text, trimmed.
func nonBlank(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// runSubtasks carries out a task the plan split, one subtask at a time, in order.
//
// THE SUBTASK CONTRACT:
//
//   - A subtask is told what it is part of. It used to receive its own line and nothing
//     else - "update the callers" with no idea of which function, which request, or what
//     the subtask before it had just done - so it was analysed and planned from scratch,
//     and often re-did or contradicted its siblings.
//   - The subtasks run IN ORDER and the first failure stops the rest. The plan lists them
//     in the order the work goes, so a later one routinely builds on an earlier one, and
//     running it on top of a failure spends its whole budget on a broken base.
//   - The parent passes only when every subtask passed, and its answer is what they did,
//     not "3 subtasks completed".
func (a *Agent) runSubtasks(ctx context.Context, t task.Task, analysis Analysis, subs []string,
	depth int, start time.Time, prefix string) TaskResult {
	res := TaskResult{Task: t.Description, Attempts: 1}
	a.log.Info(prefix+"splitting into subtasks", "count", len(subs))
	var outcomes []string
	for i, sub := range subs {
		if ctx.Err() != nil {
			res.Reason = "cancelled during the subtasks"
			res.DurationMS = time.Since(start).Milliseconds()
			return res
		}
		res.Subtasks++
		a.report("subtask %d/%d: %s", i+1, len(subs), truncate(sub, 160))
		subResult := a.processTask(ctx, task.Task{
			Description: subtaskBrief(t.Description, analysis.Summary, subs, i, outcomes),
			Origin:      t.Origin + " (subtask)",
			Context:     t.Context,
		}, depth+1)
		res.Validation = subResult.Validation
		if !subResult.Pass {
			skipped := len(subs) - i - 1
			res.Reason = fmt.Sprintf("subtask %d of %d did not pass (%s): %s", i+1, len(subs),
				truncate(sub, 120), subResult.Reason)
			if skipped > 0 {
				res.Reason += fmt.Sprintf("; the %d after it were not started, because later subtasks build on earlier ones", skipped)
			}
			res.Summary = subtaskSummary(subs, outcomes)
			res.DurationMS = time.Since(start).Milliseconds()
			a.report("%s", res.Reason)
			return res
		}
		outcomes = append(outcomes, taskOutcome(subResult))
	}
	res.Pass = true
	res.Reason = fmt.Sprintf("%d subtasks completed", len(subs))
	res.Summary = subtaskSummary(subs, outcomes)
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

// subtaskBrief is the description a subtask is analysed, planned and executed from.
//
// The subtask's own line goes FIRST, so a log line or a truncated prompt still shows what it
// is; the request it belongs to and the state of its siblings follow.
func subtaskBrief(parent, summary string, subs []string, i int, outcomes []string) string {
	var b strings.Builder
	b.WriteString(subs[i])
	fmt.Fprintf(&b, "\n\n(This is subtask %d of %d of a larger request. Do ONLY this subtask: "+
		"the others are carried out separately, before or after it.)\n", i+1, len(subs))
	fmt.Fprintf(&b, "Overall request: %s\n", truncate(collapse(parent), 1000))
	if s := strings.TrimSpace(summary); s != "" && s != strings.TrimSpace(parent) {
		fmt.Fprintf(&b, "What the request has to achieve: %s\n", truncate(collapse(s), 500))
	}
	b.WriteString("Subtasks:\n")
	for j, s := range subs {
		switch {
		case j < len(outcomes):
			fmt.Fprintf(&b, "%d. [done] %s - %s\n", j+1, s, truncate(collapse(outcomes[j]), 300))
		case j == i:
			fmt.Fprintf(&b, "%d. [THIS ONE] %s\n", j+1, s)
		default:
			fmt.Fprintf(&b, "%d. [later] %s\n", j+1, s)
		}
	}
	return b.String()
}

// subtaskSummary is the parent's answer: what each finished subtask did.
func subtaskSummary(subs []string, outcomes []string) string {
	if len(outcomes) == 0 {
		return ""
	}
	var b strings.Builder
	for j, o := range outcomes {
		fmt.Fprintf(&b, "%d. **%s** - %s\n", j+1, subs[j], strings.TrimSpace(o))
	}
	return strings.TrimRight(b.String(), "\n")
}

// maxBudgetExtensions bounds how many times the loop may ASK to continue. It is a
// ceiling on the ceiling: a gateway whose approver answers "yes" without a human
// behind it (a script, a test) would otherwise loop for ever. Reaching it means a
// hundred answered questions, which no real session does.
const maxBudgetExtensions = 10

// continueBudget asks the user whether the run should keep going with another full
// budget, after one has been spent with work left.
//
// It is the same approval mechanism a consequential command uses, which is the point:
// "may I keep working" is a question the user answers, and the interface that already
// knows how to ask it is this one. No approver (a run from a script) means no answer,
// and no answer means stop - the same conservative default the command approval uses.
func (a *Agent) continueBudget(ctx context.Context, rounds, maxSteps int, prefix string) (bool, error) {
	if a.approver == nil {
		return false, nil
	}
	ok, err := a.approver(ctx, ApprovalRequest{
		Command: fmt.Sprintf("continue for another %d rounds (%d used, work left)", maxSteps, rounds),
		Reason: "the step budget ran out and the model still reports work left; " +
			"continuing keeps everything already done instead of starting over",
		Rule: BudgetRule,
	})
	if err != nil || !ok {
		a.log.Warn(prefix+"the budget continuation was not approved", "rounds", rounds)
		return false, err
	}
	a.log.Info(prefix+"continuing with another budget", "rounds", rounds, "max_steps", maxSteps)
	return true, nil
}

// BudgetRule is the Rule of the question "may this run spend another budget?". It is not a
// command, so an approver that approves every COMMAND on the user's behalf must not answer it:
// the user said yes to running commands, not to working for ever.
const BudgetRule = "agent.max_steps"

// maxSteps is the round budget in force, defaulted when none was configured.
func (a *Agent) maxSteps() int {
	if a.cfg.Agent.MaxSteps > 0 {
		return a.cfg.Agent.MaxSteps
	}
	return defaultMaxSteps
}

// defaultMaxSteps is how many rounds one task may take when the configuration names no
// bound. It is generous because "do what I asked" is usually a plan and not a single step,
// and it is a CHECKPOINT rather than a wall: a run that reaches it with work left asks the
// user before spending another, so the bound protects against a loop that cannot converge
// without cutting off honest work.
const defaultMaxSteps = 100

// --- Phases -----------------------------------------------------------------

func (a *Agent) analysisPhase(ctx context.Context, t task.Task, rules string, depth int) Analysis {
	vars := a.baseVariables(t)
	vars["rules"] = rules
	vars["attempt"] = "1"
	vars["max_attempts"] = fmt.Sprint(a.cfg.Agent.MaxRetries + 1)

	text, err := a.ask(ctx, a.cfg.Prompts.Analyze, vars, "analyze")
	if err != nil {
		a.log.Error("analysis phase failed", "error", err)
		return Analysis{Understandable: false, Risks: []string{err.Error()}}
	}

	var analysis Analysis
	if err := llm.DecodeJSON(text, &analysis); err != nil {
		// A malformed analysis is OUR problem, not the user's: the model failed to follow the
		// format, and reporting the parse error to the user asks them to fix something they did
		// not do. The honest answer is that the request could not be read, with a question that
		// lets them proceed.
		a.log.Error("the analysis has an unexpected format", "error", err, "response", truncate(text, 300))
		return Analysis{
			Understandable: false,
			Risks:          []string{"the analysis could not be parsed: " + err.Error()},
			Question:       "I could not read the request. Could you tell me, in one sentence, what you want done and to what?",
		}
	}
	if analysis.Summary == "" {
		analysis.Summary = truncate(t.Description, 200)
	}
	a.log.Info("analysis completed", "summary", truncate(analysis.Summary, 160), "criteria", len(analysis.SuccessCrit))
	return analysis
}

func (a *Agent) planPhase(ctx context.Context, t task.Task, analysis Analysis, depth int) Plan {
	vars := a.baseVariables(t)
	vars["analysis"] = describeAnalysis(analysis)

	text, err := a.ask(ctx, a.cfg.Prompts.Plan, vars, "plan")
	if err != nil {
		a.log.Error("planning phase failed", "error", err)
		return Plan{}
	}
	var plan Plan
	if err := llm.DecodeJSON(text, &plan); err != nil {
		a.log.Error("the plan has an unexpected format", "error", err, "response", truncate(text, 300))
		return Plan{}
	}
	a.log.Info("plan generated", "steps", len(plan.Steps), "subtasks", len(plan.Subtasks))
	return plan
}

// actionPhase asks for the next round's actions.
//
// The analysis travels with the plan. Without it the execute phase saw only the TASK, which on a
// turn that answers a question is the answer itself - "yes, do it" - and the success criteria the
// work is judged by were never in front of the phase that does the work.
//
// An empty list of actions is NOT an error here: "done": true with nothing left to run is how a
// finished task is reported, and it used to fail the whole task with "the LLM proposed no
// action". The loop decides what an empty list means, because only the loop knows "done".
func (a *Agent) actionPhase(ctx context.Context, t task.Task, analysis Analysis, plan Plan,
	journal []roundRecord, mem *workMemory, round int, prefix string) (Action, error) {
	vars := a.baseVariables(t)
	vars["analysis"] = describeAnalysis(analysis)
	vars["plan"] = describePlan(plan)
	vars["history"] = renderRounds(journal) + mem.render()
	vars["attempt"] = fmt.Sprint(round)
	vars["max_attempts"] = fmt.Sprint(a.cfg.Agent.MaxRetries + 1)

	text, err := a.ask(ctx, a.cfg.Prompts.Execute, vars, "execute")
	if err != nil {
		return Action{}, err
	}
	var action Action
	if err := llm.DecodeJSON(text, &action); err != nil {
		return Action{}, err
	}
	a.log.Info(prefix+"action proposed", "reasoning", truncate(action.Reasoning, 200),
		"actions", len(action.Actions), "done", action.isDone())
	return action, nil
}

// describeAnalysis renders the analysis for the phases after it.
func describeAnalysis(analysis Analysis) string {
	var sb strings.Builder
	if analysis.Summary != "" {
		fmt.Fprintf(&sb, "- Summary: %s\n", analysis.Summary)
	}
	for _, c := range analysis.SuccessCrit {
		fmt.Fprintf(&sb, "- Success criterion: %s\n", c)
	}
	for _, r := range analysis.Risks {
		fmt.Fprintf(&sb, "- Risk: %s\n", r)
	}
	return sb.String()
}

// synthesizePhase asks the LLM for a concise, evidence-based answer after the
// actions have run and the anchor has validated them.
func (a *Agent) synthesizePhase(ctx context.Context, t task.Task, output string, validation anchor.Result) *Report {
	vars := a.baseVariables(t)
	// The output of every round, so the head AND the tail are kept: the start says what was
	// found, the end says where the work landed.
	vars["output"] = truncateMiddle(output, roundOutputChars)
	vars["validation"] = validation.Reason

	text, err := a.ask(ctx, a.cfg.Prompts.Synthesize, vars, "synthesize")
	if err != nil {
		a.log.Warn("synthesis phase failed", "error", err)
		return nil
	}
	var rep Report
	if err := llm.DecodeJSON(text, &rep); err != nil {
		a.log.Warn("synthesis response has an unexpected format", "error", err, "response", truncate(text, 300))
		return nil
	}
	rep.normalize(validation.Pass)
	if rep.Summary == "" {
		return nil
	}
	return &rep
}

// ask renders the prompt and calls the LLM, logging any variables that were
// missing (unfilled gaps in a user template).
func (a *Agent) ask(ctx context.Context, p config.Template, vars map[string]string, phase string) (string, error) {
	system, missingS := template.Render(p.System, vars)
	user, missingU := template.Render(p.User, vars)
	if len(missingS)+len(missingU) > 0 {
		missing := append(append([]string{}, missingS...), missingU...)
		a.log.Warn("the template has variables with no value", "phase", phase, "variables", strings.Join(missing, ","))
	}

	messages := []llm.Message{}
	if strings.TrimSpace(system) != "" {
		messages = append(messages, llm.Message{Role: "system", Content: system})
	}
	messages = append(messages, llm.Message{Role: "user", Content: user})

	start := time.Now()
	var text string
	var err error
	if a.streamThoughts && a.Progress != nil {
		live := &liveView{a: a, phase: phase}
		text, err = a.engine.CompleteStream(ctx, messages, live.add)
	} else {
		text, err = a.engine.Complete(ctx, messages)
	}
	if errors.Is(err, llm.ErrToolCallForText) && ctx.Err() == nil {
		// Reported from real use: "I ask it for things and the agent does nothing". The model
		// answered a phase that wants a JSON TEXT with a tool call (search_skills, ...); every
		// round asked the same way and got the same call, so each subtask died on round 1
		// with no work done. One retry that SAYS what went wrong is what makes it answerable
		// - the tools it reached for are still available as actions inside the JSON.
		a.log.Warn("the model answered with a tool call; asking once more for text", "phase", phase)
		retry := append(append([]llm.Message{}, messages...), llm.Message{Role: "user", Content: "Your last reply was a " +
			"tool call, and this step cannot run tool calls. Reply with ONLY the JSON object the instructions " +
			"describe. If you wanted to use a tool, put it in the \"actions\" list of that JSON (kind and command)."})
		text, err = a.engine.Complete(ctx, retry)
	}
	if err != nil {
		return "", err
	}
	a.log.Debug("LLM response", "phase", phase, "ms", time.Since(start).Milliseconds(), "bytes", len(text))
	return text, nil
}

// baseVariables are the variables available in every template.
func (a *Agent) baseVariables(t task.Task) map[string]string {
	vars := map[string]string{
		"task":         t.Description,
		"workspace":    a.cfg.Agent.WorkspaceDir,
		"origin":       t.Origin,
		"attempt":      "1",
		"max_attempts": fmt.Sprint(a.cfg.Agent.MaxRetries + 1),
		"rules":        a.describeRules(),
		"analysis":     "",
		"plan":         "",
		"history":      a.dialogue(),
		"model":        a.cfg.LLM.Model,
		"provider":     a.cfg.LLM.Provider,
	}
	for k, v := range t.Context {
		vars["context_"+k] = v
	}
	return vars
}

// describeRules turns the anchor into text for the prompt: the LLM must know
// exactly what it will be measured against.
func (a *Agent) describeRules() string {
	switch strings.ToLower(a.cfg.Anchor.Kind) {
	case "command":
		var sb strings.Builder
		fmt.Fprintf(&sb, "- It will run: %s %s\n", a.cfg.Anchor.Command, strings.Join(a.cfg.Anchor.Args, " "))
		fmt.Fprintf(&sb, "- It must finish with exit code %d.\n", a.cfg.Anchor.ExpectExit)
		if a.cfg.Anchor.ExpectOutput != "" {
			fmt.Fprintf(&sb, "- Its output must match the regular expression: %s\n", a.cfg.Anchor.ExpectOutput)
		}
		for _, c := range a.cfg.Anchor.Checks {
			fmt.Fprintf(&sb, "- In addition: %s %s (expected exit code %d)\n", c.Command, strings.Join(c.Args, " "), c.ExpectExit)
		}
		return sb.String()
	case "auto":
		return a.describeAutoRules()
	default:
		return "- No deterministic validation is configured (anchor.kind=none). The agent will not declare PASS on its own: review the configuration."
	}
}

// describeAutoRules names the gate the PROJECT declares, which is what kind=auto runs. The model
// is told the exact commands, because the difference between a run that finishes and one that
// spends its attempts is whether it ran those same commands itself before claiming done.
func (a *Agent) describeAutoRules() string {
	checks := anchor.New(a.cfg.Anchor, a.cfg.Agent.WorkspaceDir, nil).Planned()
	var sb strings.Builder
	if len(checks) == 0 {
		sb.WriteString("- This project declares NO gate yet (looked for .motita/anchor, a Makefile with check or test, " +
			"go.mod, package.json lint/typecheck/test, Cargo.toml, pyproject.toml), so a claim of done will be REFUSED.\n" +
			"- Declare it: find how the project checks itself (README, CI workflow, package.json scripts, Makefile) " +
			"and write those commands, one per line, in .motita/anchor. Run them yourself before claiming done.\n")
		return sb.String()
	}
	sb.WriteString("- The project's own gate runs after you claim done, in the project directory. Each of these must pass:\n")
	for _, c := range checks {
		fmt.Fprintf(&sb, "  - %s %s (must exit %d)\n", c.Command, strings.Join(c.Args, " "), c.ExpectExit)
	}
	sb.WriteString("- Run these same commands yourself BEFORE claiming done and fix what they report: a claim the gate " +
		"refuses costs one of your limited attempts, and running them first costs nothing.\n")
	return sb.String()
}

func describePlan(p Plan) string {
	if len(p.Steps) == 0 {
		return "(no plan)"
	}
	var sb strings.Builder
	for _, step := range p.Steps {
		fmt.Fprintf(&sb, "%d. %s", step.Number, step.Action)
		if step.Command != "" {
			fmt.Fprintf(&sb, "  ->  %s", step.Command)
		}
		sb.WriteString("\n")
	}
	if p.ExpectedResult != "" {
		fmt.Fprintf(&sb, "Expected result: %s\n", p.ExpectedResult)
	}
	return sb.String()
}

// --- Execution --------------------------------------------------------------

// runActions runs every action in the sandbox and returns their combined output.
// runLibraryAction answers one library operation, and reports whether it was one.
//
// An unknown kind returns handled=false so it falls through to the command path, where it is
// reported as a command with no command — the honest outcome for a kind nothing recognises.
func (a *Agent) runLibraryAction(kind string, action Command) (bool, string) {
	switch kind {
	case "read_skill", "search_skills", "list_skills", "save_skill":
	default:
		return false, ""
	}
	if a.library == nil {
		return true, "Error: no procedure library is configured."
	}
	// The kind is validated above, so this switch is exhaustive over what can arrive: a final
	// "not handled" return would be unreachable. Adding an operation means adding it to both
	// switches, and the compiler will not let the second one fall through.
	switch kind {
	case "list_skills":
		return true, a.listSkills()
	case "search_skills":
		return true, a.searchSkills(action.Command)
	case "read_skill":
		return true, a.readSkill(action.Command)
	default: // save_skill
		return true, a.saveSkill(action.Command)
	}
}

// listSkills reports the index, with each skill's accumulated value when there is one.
func (a *Agent) listSkills() string {
	all, err := a.library.List()
	if err != nil {
		return fmt.Sprintf("Error: could not read the library: %v", err)
	}
	if len(all) == 0 {
		return "The library is empty. Work from your own knowledge, and save what you learn."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s). Use read_skill with a name to read one in full.\n", len(all))
	for _, s := range all {
		fmt.Fprintf(&b, "\n- %s: %s\n  %s%s%s", s.Name, s.Title, s.Summary,
			a.historySuffix(s.Name), a.feedbackSuffix(s.Name))
	}
	return b.String()
}

// searchSkills finds procedures by what the work is about.
func (a *Agent) searchSkills(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "Error: search_skills needs what the work is about, in plain words."
	}
	hits, err := a.library.Search(query, 10)
	if err != nil {
		return fmt.Sprintf("Error: could not search the library: %v", err)
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No skill matches %q. Work from your own knowledge, and save a skill afterwards if what you work out is worth keeping.", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s) match %q.\n", len(hits), query)
	// Same obligation as the planner's search, and for the same measured reason: a summary is
	// one line, and a model that treats the list as the answer acts on a title. See the comment
	// in internal/plan/plan.go for the numbers that made this wording.
	b.WriteString("A summary above is NOT the procedure: read the one that fits with read_skill BEFORE you act.\n")
	for _, s := range hits {
		fmt.Fprintf(&b, "\n- %s: %s\n  %s%s", s.Name, s.Title, s.Summary, a.historySuffix(s.Name))
	}
	for _, s := range hits {
		b.WriteString(a.feedbackSuffix(s.Name))
	}
	return b.String()
}

// readSkill returns one procedure in full, and records that it was relied on.
func (a *Agent) readSkill(name string) string {
	s, err := a.library.Get(strings.TrimSpace(name))
	if err != nil {
		if errors.Is(err, skills.ErrNotFound) {
			return fmt.Sprintf("No skill named %q. Use list_skills to see what the library holds.", name)
		}
		return fmt.Sprintf("Error: %v", err)
	}
	// The read is the moment the credit becomes knowable, and the only one.
	a.consult(s.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "# skill: %s\n(source: %s)\n\n", s.Name, s.Path)
	b.WriteString(s.Body)
	b.WriteString(a.feedbackSuffix(s.Name))
	return b.String()
}

// saveSkill writes a procedure. The argument is "name :: body", because this mode's actions
// carry a single string rather than structured arguments.
func (a *Agent) saveSkill(arg string) string {
	name, body, ok := strings.Cut(arg, "::")
	if !ok {
		return "Error: save_skill expects \"name :: the document in markdown\"."
	}
	s, err := a.library.Save(strings.TrimSpace(name), strings.TrimSpace(body))
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	// A save that replaces a skill with an outstanding complaint is the fix being attempted.
	// Marking it here is what stops the same complaint being handed out on every later turn.
	marked := ""
	if a.reward != nil && a.reward.Addressed(s.Name) {
		if err := a.reward.Save(); err == nil {
			marked = " The complaint that was recorded against this skill is now marked as addressed."
		}
	}
	return fmt.Sprintf("Saved %q (%d bytes).%s", s.Name, len(body), marked)
}

// historySuffix renders a skill's accumulated value, or nothing when it has no history.
//
// Numbers and counts, with no adjective: "RELIABLE" would be this program's interpretation
// presented as evidence. An unconsulted skill shows nothing rather than "0.0", which would
// read as "this failed".
func (a *Agent) historySuffix(name string) string {
	if a.reward == nil {
		return ""
	}
	s, ok := a.reward.Get(name)
	if !ok || s.Uses() == 0 {
		return ""
	}
	return fmt.Sprintf("  [used %d, value %+.2f]", s.Uses(), s.Value)
}

// feedbackSuffix states the user's outstanding complaints for a skill, quoted verbatim.
//
// The number says a skill failed; the user's own words say what was wrong with it, which is
// the only form of the complaint a repair can be written from. Only complaints with no fix
// attempted are shown: once it has been rewritten the complaint is answered.
func (a *Agent) feedbackSuffix(name string) string {
	if a.reward == nil {
		return ""
	}
	s, ok := a.reward.Get(name)
	if !ok {
		return ""
	}
	notes := s.Unaddressed()
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n  !! the user reported this skill failing, and the procedure has NOT been revised since:")
	for i, n := range notes {
		fmt.Fprintf(&b, "\n     %d. %s", i+1, n.Text)
	}
	b.WriteString("\n     Read the procedure again, work out which step the report is about, and save the")
	b.WriteString("\n     corrected version with save_skill. Fixing it is worth more than avoiding it.")
	return b.String()
}

func (a *Agent) runActions(ctx context.Context, actions []Command, prefix string) (string, error) {
	return a.runRound(ctx, actions, prefix, nil, 0)
}

// runRound is runActions with the run's working memory. A read the run already holds, and
// that nothing has invalidated, is answered from memory instead of being executed; a read
// that ran is kept; a write forgets every kept read. mem == nil is the plain behaviour.
func (a *Agent) runRound(ctx context.Context, actions []Command, prefix string, mem *workMemory, round int) (string, error) {
	var sb strings.Builder
	var lastErr error
	// The tree as this round finds it. Anything that changed it since the last look (the
	// anchor ran a build, a test wrote a file) makes the kept reads untrustworthy.
	if mem != nil {
		mem.sync(a.cfg.Agent.WorkspaceDir)
	}

	for i, action := range actions {
		// A library action is not a shell command: it is answered from the procedure library and
		// its result goes back to the model as the output of this step. It is dispatched by
		// kind, because that is how this mode names things — the same four operations the
		// planner exposes as tools, reachable from here too.
		if kind := strings.ToLower(strings.TrimSpace(action.Kind)); kind != "" && kind != "command" {
			if handled, out := a.runLibraryAction(kind, action); handled {
				fmt.Fprintf(&sb, "[%s] %s\n%s\n", kind, action.Description, out)
				a.log.Info(prefix+"library action", "kind", kind, "description",
					truncate(action.Description, 80))
				continue
			}
			// A kind this mode does not know, carrying text the model meant as ARGUMENTS.
			//
			// Falling through to the command path treats that text as a program name and runs
			// it: a model that asks for plan mode's `read_file` arrives here with a path, and
			// the path is executed as a shell command — `/etc/hostname: Permission denied` is
			// the shape of it. It is refused by name instead, because guessing which part is a
			// program and which is an argument is exactly the mistake: the model named an
			// operation that does not exist, and saying so is what lets it choose one that does.
			//
			// The refusal is not an error of the run: the turn continues, the model reads this
			// and answers with a kind that exists.
			if strings.TrimSpace(action.Command) != "" || strings.TrimSpace(action.Description) != "" {
				fmt.Fprintf(&sb, "[%s] %s\n[refused: %q is not an action this mode has. "+
					"Available: command, list_skills, search_skills, read_skill, save_skill. "+
					"To run a shell command, use kind \"command\".]\n",
					kind, action.Description, kind)
				a.log.Warn(prefix+"action with an unknown kind",
					"kind", kind, "command", truncate(action.Command, 80))
				lastErr = fmt.Errorf("action %d names the unknown kind %q", i+1, kind)
				continue
			}
		}
		if strings.TrimSpace(action.Command) == "" {
			a.log.Debug(prefix+"action with no command (descriptive)", "description", action.Description)
			continue
		}
		if mem != nil && isRead(action) {
			if rec, ok := mem.recall(action.Command); ok {
				fmt.Fprintf(&sb, "$ %s\n[not run again: the same read was made in round %d and nothing has "+
					"changed since. Its full output is under \"WHAT YOU HAVE ALREADY READ\" in this prompt]\n",
					action.Command, rec.round)
				a.log.Info(prefix+"read answered from memory", "command", truncate(action.Command, 120),
					"round", rec.round)
				mem.recalled++
				continue
			}
		}
		a.report("running: %s", action.Command)
		a.log.Info(prefix+"running in sandbox", "n", i+1, "command", action.Command, "description", truncate(action.Description, 120))

		// How the action is executed depends on the mode, and it is the difference
		// between a request and a guarantee:
		//
		//  - read-only (plan mode): the line is split into program and arguments, the
		//    policy checks the program, and NO shell runs. Without a shell there is no
		//    `>`, `>>`, `;`, `&&` or `$(...)`: redirection cannot happen because
		//    nothing interprets it.
		//  - otherwise: the line goes to the shell, because that is what lets the
		//    model use pipes, redirections and globs to do real work — but only once the
		//    policy has had its say. A consequential action is put in front of the user
		//    here, mid-run, because this is the moment it is about to happen.
		plan := a.planRequest(action.Command)
		if plan.Verdict == policy.Deny {
			fmt.Fprintf(&sb, "$ %s\n[refused: %s]\n", action.Command, plan.Reason)
			a.log.Warn(prefix+"action refused by the policy",
				"command", action.Command, "rule", plan.Rule, "mandatory", plan.Mandatory, "reason", plan.Reason)
			lastErr = fmt.Errorf("action %d (%s) was refused: %s", i+1, action.Command, plan.Reason)
			continue
		}
		if plan.Verdict == policy.Ask {
			a.report("asking you to approve: %s", action.Command)
			approved, err := a.approve(ctx, plan, action.Command)
			if err != nil {
				// There is nobody to ask. This is not a failure of the command, it is the
				// absence of the only thing that could authorise it, and the output says
				// so in those words so the model does not retry the same line.
				fmt.Fprintf(&sb, "$ %s\n[not approved: %s]\n", action.Command, err)
				a.log.Warn(prefix+"action needs approval and there is nobody to ask",
					"command", action.Command, "rule", plan.Rule)
				lastErr = fmt.Errorf("action %d (%s) needs approval and there is nobody to ask: %w",
					i+1, action.Command, err)
				continue
			}
			if !approved {
				fmt.Fprintf(&sb, "$ %s\n[not approved: the user declined this command]\n", action.Command)
				a.log.Info(prefix+"action declined by the user",
					"command", action.Command, "rule", plan.Rule)
				lastErr = fmt.Errorf("action %d (%s) was declined by the user", i+1, action.Command)
				continue
			}
			a.log.Info(prefix+"action approved by the user", "command", action.Command)
		}

		output, truncated, exit, err := a.exec(ctx, plan.Request)
		// What the command printed, so the user sees what the agent is looking at, not only
		// that it ran something.
		//
		// It is reported even when the command printed nothing: the exit code is what tells
		// the reader whether it worked, and a silent success (`printf > file`) would
		// otherwise never be marked as finished.
		// What the reader and the terminal see is what the command actually printed, up to a cap
		// that is generous on purpose: a `grep` of a large tree is exactly the output a person
		// asks for, and cutting it to a handful of lines made the terminal useless as a record.
		// What is kept is the whole result of a normal command; only an enormous one is cut, and
		// then the cut says so, with how much was left out.
		a.report("output (exit %d):\n%s", exit,
			truncateMiddle(strings.TrimSpace(output), terminalOutputChars))

		fmt.Fprintf(&sb, "$ %s\n", action.Command)
		if output != "" {
			sb.WriteString(output)
			if !strings.HasSuffix(output, "\n") {
				sb.WriteString("\n")
			}
		}
		if truncated {
			sb.WriteString("[output truncated by the sandbox limit]\n")
		}
		fmt.Fprintf(&sb, "[exit=%d]\n", exit)

		if mem != nil {
			mem.executed++
			mem.noteVerification(action.Command, round, exit, err, output)
			switch {
			case isWrite(action):
				// It looks like it could write; whether it DID is a question about the files.
				mem.sync(a.cfg.Agent.WorkspaceDir)
			case isRead(action) && err == nil && exit == 0 && !truncated:
				mem.remember(action.Command, round, output)
			}
		}

		if err != nil {
			lastErr = fmt.Errorf("action %d (%s) could not run: %w", i+1, action.Command, err)
		}
	}
	return sb.String(), lastErr
}

// summariseFailure builds the block handed to the LLM on the next attempt.
func (a *Agent) summariseFailure(action Action, execution string, validation anchor.Result, runErr error) string {
	var sb strings.Builder
	sb.WriteString("Proposed actions:\n")
	for _, c := range action.Actions {
		fmt.Fprintf(&sb, "- %s\n", c.Command)
	}
	if execution != "" {
		sb.WriteString("\nOutput of the execution in the sandbox:\n")
		sb.WriteString(truncate(execution, 3000))
		sb.WriteString("\n")
	}
	if runErr != nil {
		fmt.Fprintf(&sb, "\nExecution error: %s\n", runErr)
	}
	if missingTooling(validation) {
		sb.WriteString("\nEvery failed check failed because a tool or module is NOT INSTALLED here, which says " +
			"nothing about your change. Install the project's dependencies the way the project does " +
			"(npm ci, pip install -r requirements.txt, go mod download, bundle install...) and claim done again. " +
			"This attempt is not counted against you.\n")
	}
	sb.WriteString("\nResult of the deterministic validation (JSON):\n")
	sb.WriteString(validation.JSON())
	sb.WriteString("\n")
	return sb.String()
}

// proposedCommands renders the commands of an attempt as a single line for the log.
//
// The retry loop stops when the counter runs out, and its whole design assumes that a retry can
// converge. Whether it does is a question about the DATA, not about the code: a run that is
// correcting itself proposes different commands on each attempt, while a thrashing one proposes
// the same thing and collects the same failure. Only the commands tell those two apart — the
// validation reason is identical for both. This is what makes that question answerable from the
// log after a few weeks of real use, instead of by guessing.
func proposedCommands(action Action) string {
	parts := make([]string, 0, len(action.Actions))
	for _, c := range action.Actions {
		command := strings.TrimSpace(c.Command)
		if command == "" {
			// A command-less entry (an empty proposal, or one that only carries a description)
			// still happened; dropping it would make the line look like fewer commands ran.
			command = "(empty)"
		}
		parts = append(parts, command)
	}
	if len(parts) == 0 {
		return "(no commands proposed)"
	}
	// Truncated because a proposal can be arbitrarily long and this record is for counting and
	// comparing, not for reading in full: summariseFailure already carries the complete text.
	return truncate(strings.Join(parts, " | "), 500)
}

// --- Final action and escalation --------------------------------------------

// runConfigured runs a command the OPERATOR wrote: the `final_action`, the `on_failure`
// escalation, the fixed `git add` / `git commit` sequence.
//
// These used to go straight to `/bin/sh -c`, which made them the hole in the policy: the
// operator's own `final_action` ran whatever it said while the model's actions were being
// checked, and the mandatory floor — `rm -rf /`, `mkfs`, `shutdown` — was reachable through
// a configuration field. They are checked now.
//
// One difference from an action the MODEL proposed, and it is deliberate: a line that would
// be asked about runs, because the operator is the one who wrote it. Nobody is surprised by
// their own configuration, and a task that cannot notify, commit or escalate because a
// prompt had no user at the keyboard would be broken by the very mechanism meant to protect
// it. The floor still applies — an operator does not get to `mkfs` their own filesystem
// either — and that is the line this function will not cross.
//
// It is one function rather than three checks because it is one decision, and three copies
// of a check is how one of them ends up missing an approval.
func (a *Agent) runConfigured(ctx context.Context, req execx.Request, line string) (string, int, error) {
	plan := a.planRequest(line)
	switch plan.Verdict {
	case policy.Deny:
		a.log.Error("a configured action was refused by the policy",
			"command", line, "rule", plan.Rule, "mandatory", plan.Mandatory, "reason", plan.Reason)
		return "", 1, fmt.Errorf("the configured command %q was refused by the policy: %s", line, plan.Reason)
	case policy.Ask:
		a.log.Info("a configured action runs without confirming: the operator wrote it",
			"command", line, "rule", plan.Rule, "reason", plan.Reason)
	}
	// The request the operator configured is used as written — program and arguments — so
	// an interpreter with its own flags (`sh -c …`) keeps working. The policy was applied to
	// its LINE, which is what the checks can read.
	req.Timeout = a.cfg.Sandbox.Timeout
	output, _, exit, err := a.exec(ctx, req)
	return output, exit, err
}

// runModelLine is the composition of a `final_action` whose payload the MODEL wrote.
//
// It is kept as its own function because the line it checks is the model's, not the
// operator's, even though the interpreter around it is the operator's: `final_action` with
// a command is a configuration that says "when the work is validated, do this", and when the
// model supplies the text, the thing being checked is that text. The mandatory floor is what
// makes the difference moot for anything unrecoverable, and it is applied to the composed
// line so that a payload of `rm -rf /` is caught where it actually lives — inside the quotes.
func (a *Agent) runModelLine(ctx context.Context, line string) (string, int, error) {
	return a.runConfigured(ctx,
		execx.Request{Command: shellFor(a.cfg), Args: []string{"-c", line}}, line)
}

// runFinalAction runs the action planned for after a PASS. It returns a readable
// description and an error when the action could not be completed (including a
// non-zero exit code, which is a failure even with no execution error).
func (a *Agent) runFinalAction(ctx context.Context, c Command, prefix string) (string, error) {
	final := a.cfg.FinalAction

	switch strings.ToLower(final.Kind) {
	case "", "none":
		return "none", nil

	case "command":
		var output string
		var exit int
		var err error
		if strings.TrimSpace(c.Command) != "" {
			// The model proposed the concrete final command: the operator's configuration
			// supplied the slot, and the text in it is the model's.
			a.log.Info(prefix+"running the final action the model proposed", "command", c.Command)
			output, exit, err = a.runModelLine(ctx, c.Command)
		} else {
			if strings.TrimSpace(final.Command) == "" {
				a.log.Warn(prefix + "final_action.kind=command with no command: nothing is done")
				return "none", nil
			}
			a.log.Info(prefix+"running the final action", "command", final.Command)
			output, exit, err = a.runConfigured(ctx,
				execx.Request{Command: final.Command, Args: final.Args}, final.Command)
		}
		description := fmt.Sprintf("command exit=%d output=%s", exit, truncate(output, 300))
		if err != nil {
			a.log.Error(prefix+"the final action failed", "error", err, "output", truncate(output, 500))
			return description, fmt.Errorf("the final action could not run: %w", err)
		}
		if exit != 0 {
			return description, fmt.Errorf("the final action finished with exit code %d", exit)
		}
		return description, nil

	case "api":
		body := map[string]any{
			"task":    c.Description,
			"command": c.Command,
			"status":  "pass",
		}
		data, _ := json.Marshal(body)
		method := final.Method
		if method == "" {
			method = http.MethodPost
		}
		req, err := http.NewRequestWithContext(ctx, method, final.URL, bytes.NewReader(data))
		if err != nil {
			a.log.Error(prefix+"invalid final request", "error", err)
			return "error: " + err.Error(), fmt.Errorf("the final action (API) has an invalid URL: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.http.Do(req)
		if err != nil {
			a.log.Error(prefix+"the final notification failed", "error", err)
			return "error: " + err.Error(), fmt.Errorf("the final action (API) failed: %w", err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		description := fmt.Sprintf("api %s %s -> HTTP %d", method, final.URL, resp.StatusCode)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return description, fmt.Errorf("the final action (API) returned HTTP %d", resp.StatusCode)
		}
		return description, nil

	case "git_commit":
		message, _ := template.Render(final.CommitMessage, map[string]string{"task": c.Description})
		if strings.TrimSpace(message) == "" {
			message = "agent: validated changes"
		}
		// The staging line is a fixed command of this program, and it is checked like any
		// other so that a workspace pointed at a strange place cannot turn it into
		// something else. Nothing here is configurable as a command: the operator chooses
		// that a commit happens, not how.
		sequence := []string{
			"git add -A",
			fmt.Sprintf("git commit -m %s", shellQuote(message)),
		}
		var outputs []string
		for _, cmd := range sequence {
			output, exit, err := a.runConfigured(ctx,
				execx.Request{Command: shellFor(a.cfg), Args: []string{"-c", cmd}}, cmd)
			outputs = append(outputs, fmt.Sprintf("%s -> exit=%d %s", cmd, exit, truncate(output, 200)))
			if err != nil {
				a.log.Error(prefix+"git_commit failed", "command", cmd, "error", err)
				return "git_commit: " + strings.Join(outputs, " | "), fmt.Errorf("git_commit: %s failed: %w", cmd, err)
			}
			if exit != 0 {
				return "git_commit: " + strings.Join(outputs, " | "), fmt.Errorf("git_commit: %s finished with exit code %d", cmd, exit)
			}
		}
		return "git_commit: " + strings.Join(outputs, " | "), nil

	default:
		return "none", fmt.Errorf("unknown final_action.kind: %q", final.Kind)
	}
}

// escalate fires the configured escalation action when the task does not pass.
func (a *Agent) escalate(ctx context.Context, prefix string) {
	if a.cfg.Agent.OnFailure.Kind != "command" || strings.TrimSpace(a.cfg.Agent.OnFailure.Command) == "" {
		return
	}
	a.log.Warn(prefix+"escalating after exhausting the attempts", "command", a.cfg.Agent.OnFailure.Command)
	output, exit, err := a.runConfigured(ctx,
		execx.Request{Command: shellFor(a.cfg), Args: []string{"-c", a.cfg.Agent.OnFailure.Command}},
		a.cfg.Agent.OnFailure.Command)
	if err != nil {
		a.log.Error(prefix+"the escalation action failed", "error", err)
		return
	}
	a.log.Info(prefix+"escalation executed", "exit", exit, "output", truncate(output, 300))
}

// liveInterval is the least time between two live snapshots. A model writes dozens of
// fragments a second; a snapshot per fragment would flood the interface and the socket for no
// visible gain. A variable so a test can take every snapshot.
var liveInterval = 250 * time.Millisecond

// liveView turns a streamed completion into snapshots of what the model is saying.
type liveView struct {
	a       *Agent
	phase   string
	answer  strings.Builder
	thought strings.Builder
	last    time.Time
	sent    string
}

// add takes one fragment and reports a snapshot when enough time has passed.
func (l *liveView) add(fragment string, thinking bool) {
	if thinking {
		l.thought.WriteString(fragment)
	} else {
		l.answer.WriteString(fragment)
	}
	if time.Since(l.last) < liveInterval {
		return
	}
	view := liveText(l.phase, l.thought.String(), l.answer.String())
	if view == "" || view == l.sent {
		return
	}
	l.sent, l.last = view, time.Now()
	l.a.report("%s%s", LivePrefix, view)
}

// liveFields are, per phase, the JSON fields a person wants to read while the reply is being
// written, and what to put in front of each. The rest of the reply is structure.
var liveFields = map[string][]struct{ name, lead string }{
	"analyze":    {{"summary", ""}, {"reply", ""}, {"question", ""}},
	"plan":       {{"action", "- "}},
	"execute":    {{"reasoning", ""}, {"command", "$ "}},
	"synthesize": {{"summary", ""}},
}

// liveMaxRunes bounds one snapshot: the TAIL is kept, because it is what is being written.
const liveMaxRunes = 1500

// liveText is the readable part of a reply that is still being written.
//
// The model answers in JSON, so the text is read out of the fields it is filling in, even
// half-written. Reasoning tokens the provider streamed apart are shown while the answer has
// nothing readable yet, and prose - a model thinking out loud before its JSON - is shown as is.
func liveText(phase, thought, answer string) string {
	var parts []string
	for _, f := range liveFields[phase] {
		for _, v := range partialStrings(answer, f.name) {
			if v = strings.TrimSpace(v); v != "" {
				parts = append(parts, f.lead+v)
			}
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		trimmed := strings.TrimSpace(answer)
		if trimmed != "" && !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "`") {
			text = trimmed
		}
	}
	if text == "" {
		text = strings.TrimSpace(thought)
	}
	if r := []rune(text); len(r) > liveMaxRunes {
		text = "…" + string(r[len(r)-liveMaxRunes:])
	}
	return text
}

// partialStrings returns every value of the string field `name` in a JSON text that may be
// cut off anywhere: the last value is returned as far as it goes.
func partialStrings(text, name string) []string {
	var out []string
	key := `"` + name + `"`
	for i := 0; ; {
		at := strings.Index(text[i:], key)
		if at < 0 {
			return out
		}
		j := i + at + len(key)
		i = j
		for j < len(text) && (text[j] == ' ' || text[j] == '\t' || text[j] == '\n' || text[j] == '\r') {
			j++
		}
		if j >= len(text) || text[j] != ':' {
			continue
		}
		j++
		for j < len(text) && (text[j] == ' ' || text[j] == '\t' || text[j] == '\n' || text[j] == '\r') {
			j++
		}
		if j >= len(text) || text[j] != '"' {
			continue
		}
		value, end := decodePartial(text[j+1:])
		out = append(out, value)
		i = j + 1 + end
	}
}

// decodePartial decodes a JSON string body up to its closing quote or the end of the text,
// and returns the value and how many bytes it consumed. An escape cut off at the end stops the
// value there rather than guessing.
func decodePartial(s string) (string, int) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			return b.String(), i + 1
		case c != '\\':
			b.WriteByte(c)
		case i+1 >= len(s):
			return b.String(), len(s)
		default:
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
			case 'u':
				if i+4 >= len(s) {
					return b.String(), len(s)
				}
				var r rune
				if _, err := fmt.Sscanf(s[i+1:i+5], "%04x", &r); err == nil {
					b.WriteRune(r)
				}
				i += 4
			default:
				b.WriteByte(s[i])
			}
		}
	}
	return b.String(), len(s)
}

// shellQuote quotes text for passing to sh -c.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func modesToStrings(modes []sandbox.Mode) []string {
	out := make([]string, 0, len(modes))
	for _, m := range modes {
		out = append(out, string(m))
	}
	return out
}

// truncate shortens s to at most max bytes plus an ellipsis, never cutting a character in
// half. A byte cut used to split a multi-byte rune - any accented letter of a Spanish request -
// and the invalid UTF-8 went into prompts, logs and the chat.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// Caps on how much of one command's output is passed on. See the two uses in runActions:
// terminalOutputChars is what the user reads (the chat step and the terminal drawer, which is
// the record of the run), and roundOutputChars is what the model is handed to work with.
//
// The terminal's cap is the larger of the two: a person asking for a `grep` wants the lines,
// and a drawer that is silently cut is a worse record than a long one. The model's is smaller
// because it goes into a prompt, where the same text costs tokens on every round.
const (
	terminalOutputChars = 32 << 10
	roundOutputChars    = 16 << 10
)

// truncateMiddle keeps the first and the last part of s and drops the middle, when s is
// longer than max bytes. Both ends are cut on a character boundary, and the marker says how
// many bytes are missing, so a reader can tell a short answer from a cut one.
func truncateMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	head := max / 3
	tail := max - head
	start := len(s) - tail
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return truncate(s, head) + fmt.Sprintf("\n[... %d of %d bytes omitted ...]\n", len(s)-head-(len(s)-start), len(s)) + s[start:]
}
