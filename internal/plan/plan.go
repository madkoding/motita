// Package plan implements the read-only plan/chat mode.
//
// In this mode the agent explores the local system (reads files and runs read-only
// commands) and delivers a plain-text plan of action. It does not change the system:
// every command is routed through agent.RunCommand, which applies the read-only
// policy before execution.
package plan

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/redact"
	"github.com/madkoding/motita/internal/review"
	"github.com/madkoding/motita/internal/reward"
	"github.com/madkoding/motita/internal/session"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// Literals used more than once in this file.
const (
	errNoProcedureLibrary = "Error: no procedure library is configured."
	errParsingArguments   = "Error parsing the arguments: "
	errPrefix             = "Error: "
)

//go:embed soul.md
var embeddedSoul string

// Resource limits tuned for low-memory systems (i386) and for safety.
const (
	maxFileBytes          = 1 << 20
	maxToolOutputBytes    = 64 << 10
	defaultCommandTimeout = 120 * time.Second
	defaultMaxLoops       = 5
)

// CommandRunner executes a single command line and is the only thing the planner
// needs from the agent layer. The real *agent.Agent implements it.
type CommandRunner interface {
	RunCommand(ctx context.Context, command string) (string, int, error)
}

// Engine is the reasoning client the planner talks to. The real *llm.Client
// implements it; the interface exists so the loop can be driven by a fake in tests.
type Engine interface {
	CompleteToolsStream(ctx context.Context, messages []llm.Message, tools []llm.Tool) <-chan llm.StreamChunk
	Complete(ctx context.Context, messages []llm.Message) (string, error)
}

// Planner runs the read-only plan/chat loop.
type Planner struct {
	engine Engine
	runner CommandRunner

	// maxLoops caps the number of tool turns per user message.
	maxLoops int
	// commandTimeout is the default timeout for execute_command.
	commandTimeout time.Duration
	// trace receives progress lines; nil means silent.
	trace func(string, ...any)
	// answer writes the final answer; nil means it is returned.
	answer func(string)
	// stream receives live model output (thinking fragments, tool call names).
	stream func(string)
	// steps receives the commands, their results and the reasoning of the run; nil means none.
	steps *Steps

	// library is the procedure library the skill tools read and write. Nil means the tools
	// answer that no library is configured, which is what a planner used as a plain one-shot
	// runner should say rather than failing.
	library *skills.Library

	// reward is the long-term value per skill, and consulted holds what this run read.
	//
	// The two together are what makes a verdict possible. A user's "this turn was good" has
	// to land on specific skills, and the only moment that is knowable is here, where the
	// tool call happens: afterwards the turn is a result and the skills it used are gone.
	//
	// consulted counts READS per skill, not distinct skills: a turn that read the same
	// procedure three times leaned on it three times, and the credit follows that.
	reward    *reward.Ledger
	consulted map[string]int

	// usage is the per-skill telemetry sidecar. Nil means telemetry is off: the
	// curator and the background review fork are disabled, and the skill tools
	// work exactly as they did before the feature existed.
	usage *usage.Ledger

	// IsReviewFork is true when this Planner is a background review fork. It
	// controls the provenance marker on save_skill: "agent" for the fork
	// (curator-managed), "foreground" for a normal conversation (user-owned).
	IsReviewFork bool

	// itersSinceSkill counts tool-call iterations since the last save_skill. The
	// caller (the Planner's Run loop) bumps it each iteration; save_skill resets
	// it. The background review fork uses it as a trigger: after enough
	// iterations without a save, the fork runs.
	itersSinceSkill int

	// review is the background self-improvement fork. Nil means the feature is
	// off: no post-turn review runs. Non-nil means MaybeRun is called after
	// finalize, with the session's messages and the current itersSinceSkill.
	review *review.Review
	// session is the conversation this planner continues. Nil means "create one on
	// first use": a planner built directly still works, and one that is handed a
	// session keeps the same conversation across runs.
	session *session.Session
	// soul is the system prompt this planner opens its conversations with. Empty
	// means "use the package SystemPrompt", which is the embedded default. A
	// planner whose caller resolved a user SOUL.md passes it here, so the session
	// is opened with the user's personality instead of the baked-in one.
	soul string
	// The window and the compaction policy, so a session created here honours the
	// configuration rather than the package defaults.
	model      string
	window     int
	reserve    int
	compactAt  float64
	keepRecent int
}

// New builds a Planner with the given engine and command runner.
// The runner must enforce read-only semantics; the planner assumes every command
// it submits is safe to run.
func New(engine Engine, runner CommandRunner) *Planner {
	if engine == nil {
		panic("plan.New: engine is nil")
	}
	return &Planner{
		engine:         engine,
		runner:         runner,
		maxLoops:       defaultMaxLoops,
		commandTimeout: defaultCommandTimeout,
	}
}

// WithLoops sets the maximum tool iterations per turn.
func (p *Planner) WithLoops(n int) *Planner {
	if n < 1 {
		n = 1
	}
	p.maxLoops = n
	return p
}

// WithTimeout sets the default command timeout.
func (p *Planner) WithTimeout(d time.Duration) *Planner {
	if d <= 0 {
		d = defaultCommandTimeout
	}
	p.commandTimeout = d
	return p
}

// WithTrace sets the trace function.
func (p *Planner) WithTrace(fn func(string, ...any)) *Planner {
	p.trace = fn
	return p
}

// WithAnswer sets the answer writer.
func (p *Planner) WithAnswer(fn func(string)) *Planner {
	p.answer = fn
	return p
}

// WithStream sets the stream writer used for live model output (thinking, tool
// call announcements and streamed text fragments).
func (p *Planner) WithStream(fn func(string)) *Planner {
	p.stream = fn
	return p
}

// Steps is where a plan run reports what it does in the terms a task run uses for the same things:
// the commands it runs with their results, and the reasoning it writes. Every field must be set.
//
// Reported from real use: "the think and the tty are empty when I open the drawers". A question is
// answered in plan mode, and plan mode only announced its tools ("[using tool: ...]") and dropped the
// model's reasoning, so the terminal and thinking drawers - which read the task path's lines - stayed
// empty for every question, however many commands it ran.
type Steps struct {
	// Command is a command about to run.
	Command func(command string)
	// Output is the result of the command reported last.
	Output func(exit int, output string)
	// Thought is reasoning the model finished: its reasoning tokens, or what it said before using
	// its tools.
	Thought func(text string)
	// Live is the reasoning being written, whole so far: each one replaces the one before.
	Live func(text string)
}

// WithSteps sets where the run's commands and reasoning are reported.
func (p *Planner) WithSteps(s Steps) *Planner {
	p.steps = &s
	return p
}

// liveThoughtEvery is the least time between two live snapshots of the reasoning. A variable so a
// test can take every one.
var liveThoughtEvery = 250 * time.Millisecond

// WithSoul sets the system prompt this planner opens conversations with, so a
// caller that resolved a user's SOUL.md can pass it in. Empty keeps the default.
func (p *Planner) WithSoul(s string) *Planner {
	p.soul = s
	return p
}

// defaultSoul is the personality and identity baked into the binary. It is the
// agent's character — warm, supportive, brilliant — and it is what every motita
// starts with. A user who wants a different personality writes ~/.motita/SOUL.md,
// and resolveSoul reads that file instead, so the soul is owned by the person
// running the agent, not the binary.
//
// The soul is the FIRST thing the model hears. It sets the tone, the language
// adaptation rules, and the boundaries of the agent's character. The operational
// instructions that follow it (how to use tools, when to ask, what read-only
// means) are the same whatever the soul is, because they describe the machinery,
// not the person operating it.
var defaultSoul = embeddedSoul

// operationalPrompt is the part of the system prompt that is NOT personality: it
// is the procedure the agent follows regardless of who it is. Skills, read-only
// constraints, investigation discipline, verification — these are the same whether
// the soul is Motita or something a user wrote themselves.
const operationalPrompt = `## What that means in practice

A user describes a goal and leaves most of it unsaid. They are not being lazy: they assume
you will fill in what any competent engineer would. Your job is to supply that missing
intelligence, not to ask them to supply it.

When a request is thin, work out the unstated parts yourself before acting:

- WHAT the goal implies, not just what was literally asked. "Is the disk full?" means
  "tell me whether the disk is the reason something is failing, and what to do about it".
- WHERE it applies, when the answer is discoverable. If no path is given, find the likely
  ones in the working directory and the files mentioned. When the session belongs to a
  project, the project is the answer to "where": what it asks about is inside it, and other
  folders are out of scope unless the user names one. Do not ask for a path you can find in
  two tool calls.
- WHICH constraints are obvious and unstated: do not modify what you were not asked to
  modify; do not delete; do not touch anything outside the scope of the goal.
- WHAT "done" looks like for this kind of task, and then produce that: a diagnosis that
  ends in a recommendation, an inventory that ends in a total, an error that ends in a
  cause and a fix.
- The DIFFERENCE between a symptom and the thing behind it. If the user reports one broken
  file, check whether its neighbours are broken too.

Then verify against the machine. Every claim you make must rest on a tool result: an
assumption you did not check is the one that will be wrong.

## When to ask, and when not to

Ask only when the missing information is genuinely unknowable from here, and then ask ONE
precise question with the options you can see. A question that a tool call could have
answered is a failure, not diligence. If you can state a reasonable assumption and act on
it, do that and say what you assumed.

Never stall. Never hand the user a menu when one approach is clearly better: choose, say why
in one line, and proceed. But when a decision is genuinely theirs (it changes scope, interface,
architecture or risk), name it, put your recommendation first, and let them decide. A goal
handed over with "/goal" delegates every decision: take the recommended one and report it.

## Your library of procedures

You have a library of skills: documents that describe how to do a particular kind of work,
including the pitfalls already paid for by whoever wrote them. Three tools use it:

- list_skills — the index: names, titles and what each one is for. Read this when you are
  starting something that may have been done before.
- search_skills — finds skills by what they are about. It searches the whole text, not only
  the titles, and matches on the words you use, so describe the work in plain language
  ("flash a board over usb") rather than in one long phrase.
- read_skill — the procedure itself, in full, by name.
- save_skill — writes a skill, creating or replacing one.

The library is NOT part of your instructions: it is a shelf you reach for. Nothing is in
your context until you look it up, and you should look it up.

When to search: before starting anything that sounds like a procedure — configuring
something, debugging a class of failure, building or deploying, handling a file format,
following a workflow in a repository. One search costs one tool call; re-deriving a
procedure that is already written costs many, and gets it wrong again.

Two of these are about the SHAPE of your answer rather than about a tool, and they are the
ones that decide whether the user can see what you did: search for the diagrams-and-reports
procedure before laying out a plan, showing a branch or file change, or reporting a
before/after; and search for verifying-a-change before claiming that something works. Both
apply to almost every task that ends in a result.

When to save, and this matters as much: after you have worked something out that you did
not know at the start, and that would help the next time. Write it when the knowledge is
fresh and specific: the commands that worked, the ones that failed and why, the file that
had to be edited, the order the steps have to happen in. Name it after the work, not after
this session. Write it as instructions to someone who knows less than you do now, because
that is who will read it.

Never save a summary of what you did in this conversation. A diary is not a skill. Save the
general procedure, with the details that were hard to find.

### Skill format and pathways

The name is lowercase, hyphenated, CLASS-level — never a PR number, error string, or date.
Structure: # Title, one line when to use, ## Procedure (numbered steps), ## Pitfalls
(rule + WHY, imperative). No PR numbers, dates, or quoted chat as content. The same lesson
twice is ONE rule. Never save environment-dependent failures, negative tool claims, or
unresolved dead ends.

Three pathways create and maintain skills:
1. FOREGROUND: save/patch during the conversation when you learn a reusable procedure,
   the user corrects your approach, or a loaded skill was wrong. Patch: read_skill then
   save_skill (same name = replace).
2. BACKGROUND REVIEW: after your answer, a background pass replays the conversation and
   patches the library automatically. You do not control it.
3. CURATOR: periodically, skills unused 14d become stale, unused 30d are archived
   (recoverable). Pinned skills are exempt.

## How to work

1. Read the request for what it implies, as above.
2. Look for a skill covering the work. If one exists, follow it and say that you did.
3. Investigate: list_directory, read_file, search_in_files, execute_command. Facts before
   conclusions, and prefer reading a file over guessing what is in it.
4. If something contradicts your assumption, say so and correct course rather than
   justifying the assumption.
5. Deliver the answer: what you found, what it means, and what you would do next. Cite the
   evidence — the path, the line, the command output. A number without a source is a guess.
6. If the work taught you a procedure, save it.

## Constraints

- This mode is READ-ONLY. You may not modify anything. Destructive commands (rm, mv, cp,
  redirection, chaining with ; && |, command substitution) are refused by the guardrails
  before they run, and insisting on them wastes the user's time.
- If a command is refused, do not repeat it or work around it. Choose a read-only route.
- Keep each command single-purpose. A compound command that fails tells you nothing about
  which part failed.
- Report what you actually observed. If you could not determine something, say that plainly
  instead of producing a plausible answer. An honest gap is useful; an invented fact is not.

## Style

Answer in the language the user wrote in. Be direct and concrete: findings first, then what
they mean, then the recommendation. No preamble, no restating the question, no filler. Use
a short list when it is genuinely a list and prose when it is not.`

// SystemPrompt is the instruction that opens every plan conversation. It is exported
// because the session that carries the conversation between turns must be opened with
// exactly this text: a second copy would drift from the one the requests actually use.
//
// It is the soul (personality) followed by the operational prompt (procedure), so the
// model knows WHO it is before it learns WHAT it does. The soul is the embedded default
// unless resolveSoul has replaced it with a user's ~/.motita/SOUL.md.
var SystemPrompt = defaultSoul + "\n\n" + operationalPrompt

// ResolveSoul loads a user's SOUL.md from the motita home directory, falling back to
// the embedded default when the file does not exist or cannot be read. The soul is
// the personality — the operational instructions stay the same whatever it says.
//
// The path is ~/.motita/SOUL.md: it sits beside the config and the token, so a user
// who wants a different agent writes one file and restarts. An empty file is treated
// as "use the default", because a zero-length soul produces a model with no identity,
// which is not what anyone who created the file intended.
func ResolveSoul(home string) string {
	if home == "" {
		return SystemPrompt
	}
	path := filepath.Join(home, ".motita", "SOUL.md")
	data, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return SystemPrompt
	}
	return string(data) + "\n\n" + operationalPrompt
}

// tools returns the tool definitions exposed to the model.
func (p *Planner) tools() []llm.Tool {
	return []llm.Tool{
		llm.NewTool("list_directory", "Lists the contents of a directory (files and subdirectories).",
			llm.ObjectSchema(map[string]any{
				"path": llm.StringProperty(
					"Path of the directory to list, relative to the working directory or absolute. " +
						"Defaults to the working directory."),
			})),
		llm.NewTool("read_file", "Reads the contents of a text file from the local system (maximum 1 MiB).",
			llm.ObjectSchema(map[string]any{
				"path": llm.StringProperty("Path of the file to read, relative to the working directory or absolute."),
			}, "path")),
		llm.NewTool("execute_command", "Runs a read-only command on the local system and returns stdout and stderr "+
			"combined. Destructive commands are refused by guardrails.",
			llm.ObjectSchema(map[string]any{
				"command": llm.StringProperty("The command to run. Pipes, redirections, shell metacharacters and " +
					"multi-command chains are not allowed in read-only mode."),
				"timeout_seconds": llm.IntegerProperty("Maximum run time in seconds (defaults to 120)."),
			}, "command")),
		llm.NewTool("search_in_files", "Searches for a pattern inside text files under a directory using grep/ripgrep.",
			llm.ObjectSchema(map[string]any{
				"pattern": llm.StringProperty("The regular expression or literal string to search for."),
				"path": llm.StringProperty("Directory or file to search in, relative to the working directory. " +
					"Defaults to the working directory."),
				"literal": llm.StringProperty(
					"Set to true to search for a literal string instead of a regex (default: false)."),
			}, "pattern")),
		llm.NewTool("list_skills", "Lists the skills in your procedure library: the name, the title and what each "+
			"one is for. Read this when you are starting work that may have been done before.",
			llm.ObjectSchema(map[string]any{})),
		llm.NewTool("search_skills", "Searches your procedure library by what the work is about, looking through the "+
			"whole text and not only the titles. Use a plain description of what you are doing.",
			llm.ObjectSchema(map[string]any{
				"query": llm.StringProperty(
					"What the work is about, in plain words (for example \"flash a firmware image over USB\")."),
				"limit": llm.StringProperty("Maximum number of entries to return (default 10)."),
			}, "query")),
		llm.NewTool("read_skill", "Reads one skill in full by name. The list and the search return the summaries; "+
			"this returns the procedure.",
			llm.ObjectSchema(map[string]any{
				"name": llm.StringProperty("The skill name, as returned by list_skills or search_skills."),
			}, "name")),
		llm.NewTool("save_skill", "Writes a skill to your procedure library, creating it or replacing it. Use it "+
			"after working something out that would help next time: the commands that worked, the ones that failed "+
			"and why, the order the steps go in. Write the PROCEDURE, not a report of this session.",
			llm.ObjectSchema(map[string]any{
				"name": llm.StringProperty(
					"A short name describing the work, not this session (for example \"zephyr-nrf-build\")."),
				"body": llm.StringProperty("The whole document in markdown. Start with a heading naming the skill, " +
					"then one line saying when to use it, then the procedure with its pitfalls."),
			}, "name", "body")),
	}
}

// Run continues the session with one instruction, looping until the model delivers a
// final answer or the loop limit is reached.
//
// The conversation lives in p.session, not in a list built here. That is the difference
// between an agent that can be asked a follow-up question and one that starts from
// nothing every time: the earlier turns are still there, and the summary of whatever had
// to be compacted travels with them.
//
// Before each request the session is given the chance to compact. Doing it HERE, at the
// boundary, rather than after a provider error, is what keeps the agent on task: it
// compacts while it still has room to think, instead of discovering the overflow halfway
// through an answer and losing the thread.
func (p *Planner) Run(ctx context.Context, input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("the instruction is empty")
	}
	// The counts the skill tools accumulate are written once, when the turn ends.
	defer p.saveUsage()

	sess := p.sessionFor()
	sess.Append(llm.Message{Role: "user", Content: input})

	if err := p.compactIfNeeded(ctx, sess); err != nil {
		return "", err
	}

	// WithLoops floors the limit at one, so a planner built through New always has
	// a usable value. A zero-value Planner skips the loop entirely and falls
	// through to the forced answer below, which is the same graceful outcome
	// without a second, unreachable clamp here.
	loops := p.maxLoops
	for i := 1; i <= loops; i++ {
		p.tracef("[thinking...]")
		// Bump the skill-nudge counter: each tool iteration that does NOT call
		// save_skill brings the background review closer to firing. save_skill
		// itself resets it (in toolSaveSkill), so a turn that saves a skill
		// does not also trigger a review of itself.
		p.itersSinceSkill++
		reply, err := p.streamTools(ctx, sess.Messages())
		if err != nil {
			return "", fmt.Errorf("could not reach the reasoning engine: %w", err)
		}

		// Assistant message carries text and/or tool calls.
		sess.Append(llm.Message{
			Role:      "assistant",
			Content:   reply.Content,
			ToolCalls: reply.Calls,
		})

		if !reply.WantsTools() {
			answer := p.finalize(reply.Content)
			// Background self-improvement: after the answer is delivered, a
			// separate goroutine replays the transcript and patches the skill
			// library. Best-effort: if the process exits, the review is lost.
			// The fork runs only when enough tool-iterations elapsed without a
			// save_skill, so a turn that already saved a skill is not re-reviewed.
			if p.review != nil {
				p.review.MaybeRun(p.reviewStore(), sess.Messages(), p.itersSinceSkill)
			}
			return answer, nil
		}

		for _, tc := range reply.Calls {
			result := p.runTool(ctx, tc)
			sess.Append(llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}

		// If this was the last allowed loop, add a strong instruction to produce text.
		if i == loops {
			sess.Append(llm.Message{
				Role: "user",
				Content: "You have reached the tool-call limit. Now deliver the final answer as plain text. " +
					"Do not call any more tools.",
			})
		}

		// The tool results are what makes a plan run grow fastest, so the check is
		// made after each round rather than only at the start.
		if err := p.compactIfNeeded(ctx, sess); err != nil {
			return "", err
		}
	}

	// Should not be reached because the last loop adds the force message, but keep as
	// a safety net in case the model still calls tools.
	p.tracef("[limit of %d iterations reached; forcing final answer]", p.maxLoops)
	reply, err := p.engine.Complete(ctx, sess.Messages())
	if err != nil {
		return "", fmt.Errorf("could not reach the reasoning engine: %w", err)
	}
	// The forced answer is part of the conversation like any other. Left out, the session
	// ended on the "you have reached the tool-call limit" instruction, the next turn opened
	// with a second user message in a row, and the answer the user read was not in the
	// history the model saw next.
	answer := p.finalize(reply)
	sess.Append(llm.Message{Role: "assistant", Content: answer})
	if p.review != nil {
		p.review.MaybeRun(p.reviewStore(), sess.Messages(), p.itersSinceSkill)
	}
	return answer, nil
}

// sessionFor returns the conversation this planner is working in, creating one on first
// use. A planner built without a session still works — it simply gets one per instance,
// which for a single run is the same as owning it.
func (p *Planner) sessionFor() *session.Session {
	if p.session == nil {
		// New already applied the package defaults; the policy overrides only where the
		// configuration actually said something. Assigning zeroes here would disable the
		// reserve and the trigger, which is the failure that lets a context overflow in
		// silence.
		prompt := p.soul
		if prompt == "" {
			prompt = SystemPrompt
		}
		s := session.New(p.model, prompt, p.window)
		if p.reserve > 0 {
			s.Reserve = p.reserve
		}
		if p.compactAt > 0 {
			s.CompactAt = p.compactAt
		}
		if p.keepRecent > 0 {
			s.KeepRecent = p.keepRecent
		}
		s.Summariser = p.engine
		p.session = s
	}
	return p.session
}

// compactIfNeeded compacts before the context overflows, and reports what it did.
//
// Failure is returned rather than swallowed. The alternative — carrying on with a
// context that is about to be rejected — turns a recoverable condition into a failed task
// in the middle of an answer, which is exactly the behaviour this is meant to prevent.
func (p *Planner) compactIfNeeded(ctx context.Context, sess *session.Session) error {
	if !sess.NeedsCompaction() {
		return nil
	}
	before := sess.Len()
	if err := sess.Compact(ctx); err != nil {
		return fmt.Errorf("the context is full and could not be compacted: %w", err)
	}
	// The interface says so: a user who sees the conversation get shorter without an
	// explanation assumes the agent has lost its place.
	p.tracef("[context compacted: %d messages folded into a summary, %d kept]", before-sess.Len(), sess.Len())
	return nil
}

// Session exposes the conversation, so the caller can report on it and so a later run
// continues in the same one.
func (p *Planner) Session() *session.Session { return p.sessionFor() }

// WithSession continues in an existing conversation instead of starting a new one.
func (p *Planner) WithSession(s *session.Session) *Planner {
	p.session = s
	return p
}

func (p *Planner) tracef(format string, args ...any) {
	if p.trace != nil {
		p.trace(format, args...)
	}
}

func emitToolCall(trace func(string, ...any), name string, args json.RawMessage) {
	if trace == nil {
		return
	}
	// Values are decoded as ANY: a map of strings failed the whole decode on the first
	// non-string argument (search_in_files' "literal": true), and the trace then showed the
	// tool with no arguments at all.
	var kvMap map[string]any
	_ = json.Unmarshal(args, &kvMap)
	var kv []string
	for k, v := range kvMap {
		kv = append(kv, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(kv)
	trace("[using tool: %s %s]", name, strings.Join(kv, " "))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (p *Planner) finalize(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "(the model did not deliver a final answer)"
	}
	if p.answer != nil {
		p.answer(content)
	}
	return content
}

// streamTools calls the engine in streaming mode, forwarding live text and tool-call
// announcements through the stream callback. It returns the accumulated reply.
func (p *Planner) streamTools(ctx context.Context, messages []llm.Message) (llm.Reply, error) {
	streamCh := p.engine.CompleteToolsStream(ctx, messages, p.tools())
	var acc llm.StreamResult
	var pendingTool string
	var thought strings.Builder
	var lastLive time.Time
	for chunk := range streamCh {
		switch chunk.Event {
		case llm.StreamError:
			return llm.Reply{}, chunk.Error
		case llm.StreamDone:
			return p.reasoned(acc.FinalReply(), thought.String()), nil
		case llm.StreamText:
			p.writeStream(chunk.Text)
			acc.Handle(chunk)
		case llm.StreamToolCall:
			// Announce each distinct tool once: the stream repeats the call while
			// its arguments are still arriving, and forwarding every repetition
			// would fill the screen with the same line.
			if chunk.Call != nil && chunk.Call.Function.Name != "" && chunk.Call.Function.Name != pendingTool {
				pendingTool = chunk.Call.Function.Name
				p.writeStream(fmt.Sprintf("[using tool: %s]", chunk.Call.Function.Name))
			}
			acc.Handle(chunk)
		case llm.StreamThinking:
			// Reasoning tokens are not part of a plan's answer: they are shown while they are
			// written, and kept as a thought once the reply is complete.
			thought.WriteString(chunk.Text)
			if p.steps != nil && time.Since(lastLive) >= liveThoughtEvery {
				lastLive = time.Now()
				p.steps.Live(thought.String())
			}
		default:
			// No other event carries anything for a plan.
		}
	}
	// Channel closed without a done event: use what we accumulated.
	return p.reasoned(acc.FinalReply(), thought.String()), nil
}

// reasoned reports the reasoning of a complete reply as thoughts, and returns the reply. Two things
// are reasoning: the tokens the provider streamed apart, and the text of a reply that goes on to use
// tools - the model saying what it is about to do, which is not the answer.
func (p *Planner) reasoned(reply llm.Reply, thought string) llm.Reply {
	if p.steps == nil {
		return reply
	}
	if t := strings.TrimSpace(thought); t != "" {
		p.steps.Thought(t)
	}
	if t := strings.TrimSpace(reply.Content); t != "" && reply.WantsTools() {
		p.steps.Thought(t)
	}
	return reply
}

func (p *Planner) writeStream(s string) {
	if p.stream != nil {
		p.stream(s)
	}
}

// runTool executes one tool call and returns a model-readable result.
//
// Every tool result goes into the conversation sent to the model provider, so this is the one
// place where secrets in it are masked: read_file on a .env, a native search hit on a line holding
// a token, a skill that quotes a key. Command output is already masked by the agent; masking it
// again is harmless, and covering every tool here means a new one cannot forget it.
func (p *Planner) runTool(ctx context.Context, tc llm.ToolCall) string {
	return redact.String(p.dispatchTool(ctx, tc))
}

// dispatchTool runs the tool a call names and returns its unmasked result.
func (p *Planner) dispatchTool(ctx context.Context, tc llm.ToolCall) string {
	// A command reported as a step is announced by that step, not twice.
	if p.steps == nil || tc.Function.Name != "execute_command" {
		emitToolCall(p.trace, tc.Function.Name, tc.Function.Arguments)
	}
	switch tc.Function.Name {
	case "list_directory":
		return p.toolListDirectory(tc.Function.Arguments)
	case "read_file":
		return p.toolReadFile(tc.Function.Arguments)
	case "execute_command":
		return p.toolExecuteCommand(ctx, tc.Function.Arguments)
	case "search_in_files":
		return p.toolSearchInFiles(ctx, tc.Function.Arguments)
	case "list_skills":
		return p.toolListSkills()
	case "search_skills":
		return p.toolSearchSkills(tc.Function.Arguments)
	case "read_skill":
		return p.toolReadSkill(tc.Function.Arguments)
	case "save_skill":
		return p.toolSaveSkill(tc.Function.Arguments)
	default:
		return fmt.Sprintf("Error: unknown tool %q", tc.Function.Name)
	}
}

// WithSessionPolicy configures the conversation a planner keeps: the model whose window
// it must respect, and the compaction policy from the configuration.
//
// The values are applied to the session when it is created, so a session shared between
// planners keeps the policy it was built with — which is what a conversation spanning many
// turns needs.
func (p *Planner) WithSessionPolicy(model string, window, reserve int, compactAt float64, keepRecent int) *Planner {
	p.model = model
	p.window = window
	p.reserve = reserve
	p.compactAt = compactAt
	p.keepRecent = keepRecent
	return p
}
