package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/task"
)

// DIRECT MODE (agent.direct). The phased loop answers each round with one JSON object (actions,
// final action, notes, done), rebuilt from a 20 KB prompt, between an analysis before it and a
// synthesis after it, with several layers that send a claim of "done" back. A strong model spends
// rounds fitting that protocol instead of doing the work. Direct mode is the loop a coding agent
// runs: one conversation, native tool calls (a shell, write a file, edit a file, recall, remember),
// and the turn ends when the model stops calling tools. Nothing safe is lost: every tool call runs
// through the same round as a phased action, so the policy, the approvals, the sandbox, the guards
// on fake checks and the write limits all apply. The project's gate runs once, when the model
// says it is finished and the tree changed; a red gate goes back into the same conversation.

// directGateRetries is how many times a red gate is handed back before the run ends with it.
const directGateRetries = 2

// toolStreamer is what direct mode needs from an engine: a tool-calling stream. *llm.Client is one.
type toolStreamer interface {
	CompleteToolsStream(ctx context.Context, messages []llm.Message, tools []llm.Tool) <-chan llm.StreamChunk
}

// directPrompt opens the conversation. It states the job and the few rules the code does not
// enforce on its own; the machine's layout follows it.
const directPrompt = `You are Motita, a coding agent working in the person's project with real tools.

Keep going until the request is resolved, then answer. Work the way an experienced engineer does:
look at what you need, change it, run the project's checks and a test for what you changed, fix
what fails, and stop when it is done. Use what you already know; use the tools for facts about
this machine and this code.

- Tools: shell runs one command line in the working directory; write_file and edit_file change
  files exactly as given (prefer them to heredocs or sed); recall and remember reach the person's
  memory, the procedures and this project's lessons.
- Several independent calls can go in one turn; they run in order.
- Stay in the working directory. A command the policy asks about waits for the person's approval;
  a refused or declined command is final: choose another way, do not repeat it.
- Never fake a check (no stand-in executables, no edited test scripts) and never say a check
  passed unless its output showed it. If something cannot run here, say so plainly.
- Commits and pull request titles are semantic: type(scope): description.
- When the work is done, stop calling tools and answer in the person's language: what you
  changed, how you checked it, and anything left or worth their decision. When the request only
  needed an answer, give it.

Working directory: %s
%s%s`

// directTools are the functions direct mode offers.
func directTools() []llm.Tool {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	tool := func(name, desc string, props map[string]any, required ...string) llm.Tool {
		props["description"] = str("3 to 8 words in the person's language: what this call is for")
		return llm.Tool{Type: "function", Function: llm.FunctionDef{Name: name, Description: desc,
			Parameters: map[string]any{"type": "object", "properties": props, "required": required}}}
	}
	return []llm.Tool{
		tool("shell", "Run one command line in the working directory and get its output and exit status.",
			map[string]any{"command": str("the exact command line")}, "command"),
		tool("write_file", "Create or replace a file with exactly this content.",
			map[string]any{"path": str("relative to the working directory"), "content": str("the whole file")},
			"path", "content"),
		tool("edit_file", "Replace text that appears exactly once in a file.",
			map[string]any{"path": str("relative to the working directory"),
				"search": str("the exact current text, indentation included"), "replace": str("the new text")},
			"path", "search", "replace"),
		tool("recall", "Look up the person's memory, the procedures, this project's lessons or earlier turns. "+
			"Start with in:memory, in:procedures, in:project or in:conversation to look in one place, "+
			"and open: <name> to read one in full.",
			map[string]any{"query": str("what to look for")}, "query"),
		tool("remember", "Keep something a later session needs: 'entry :: markdown' in the memory, "+
			"'in:procedures name :: the procedure', or 'in:project the fact'.",
			map[string]any{"text": str("what to keep, in the forms above")}, "text"),
	}
}

// directArgs are the arguments any direct tool may carry.
type directArgs struct {
	Command     string `json:"command"`
	Path        string `json:"path"`
	Content     string `json:"content"`
	Search      string `json:"search"`
	Replace     string `json:"replace"`
	Query       string `json:"query"`
	Text        string `json:"text"`
	Description string `json:"description"`
}

// directCommand turns a tool call into the action a round runs.
func directCommand(tc llm.ToolCall) (Command, error) {
	var a directArgs
	if err := tc.Function.DecodeArguments(&a); err != nil {
		return Command{}, fmt.Errorf("the arguments could not be read: %w", err)
	}
	c := Command{Kind: tc.Function.Name, Description: a.Description}
	switch tc.Function.Name {
	case "shell":
		c.Kind, c.Command = "command", a.Command
	case "write_file":
		c.Command = a.Path + "\n" + a.Content
	case "edit_file":
		c.Command = a.Path + "\n<<<<<<< SEARCH\n" + a.Search + "\n=======\n" + a.Replace + "\n>>>>>>> REPLACE"
	case "recall":
		c.Command = a.Query
	case "remember":
		c.Command = a.Text
	default:
		return Command{}, fmt.Errorf("there is no tool %q: use shell, write_file, edit_file, recall or remember",
			tc.Function.Name)
	}
	return c, nil
}

// directRun is one direct run: its conversation and what it has done so far.
type directRun struct {
	a        *Agent
	ctx      context.Context
	t        task.Task
	res      TaskResult
	msgs     []llm.Message
	baseline string
	before   *runBaseline
	used     bool
	gateTry  int
}

// direct runs a task in direct mode.
func (a *Agent) direct(ctx context.Context, t task.Task) TaskResult {
	start := time.Now()
	r := &directRun{a: a, ctx: ctx, t: t, res: TaskResult{Task: t.Description, Kind: KindTask}}
	r.baseline, _ = treeFingerprint(a.cfg.Agent.WorkspaceDir, true)
	r.before = a.startBaseline(ctx)
	defer r.before.close(ctx)
	a.steer.begin()
	defer a.steer.end()
	r.msgs = []llm.Message{
		{Role: "system", Content: a.directSystem(ctx, t)},
		{Role: "user", Content: t.Description, Images: llm.AttachedImages(a.cfg.Agent.WorkspaceDir, t.Description)},
	}
	a.report("working directly...")
	defer func() { r.res.DurationMS = time.Since(start).Milliseconds() }()
	for round := 1; round <= a.maxSteps()*maxBudgetExtensions; round++ {
		r.res.Attempts = round
		if done := r.step(); done {
			return r.res
		}
	}
	r.res.Reason = fmt.Sprintf("the task is not finished after %d rounds of tool calls", r.res.Attempts)
	a.report("%s", r.res.Reason)
	return r.res
}

// directSystem is the system prompt: the job, the machine's layout and what the turn knows.
func (a *Agent) directSystem(ctx context.Context, t task.Task) string {
	vars := a.baseVariables(t)
	machine := strings.TrimSpace(vars["project"] + vars["tools"])
	if machine != "" {
		machine += "\n"
	}
	known := "\n## Conversation so far\n" + a.dialogue() + "\n"
	if b := a.contextBrief(ctx, t, 0); b != "" {
		known += "\n" + b + "\n"
	}
	return fmt.Sprintf(directPrompt, a.cfg.Agent.WorkspaceDir, machine, known)
}

// step asks the model for its next turn and acts on it. It reports whether the run ended.
func (r *directRun) step() bool {
	for _, n := range r.a.steer.take() {
		r.a.report("you: %s", n)
		r.msgs = append(r.msgs, llm.Message{Role: "user", Content: "(the person added while you worked) " + n})
	}
	reply, err := r.a.directReply(r.ctx, r.msgs)
	if err != nil {
		r.res.Reason = "could not reach the reasoning engine: " + err.Error()
		r.a.report("%s", r.res.Reason)
		return true
	}
	r.msgs = append(r.msgs, llm.Message{Role: "assistant", Content: reply.Content, ToolCalls: reply.Calls})
	if reply.WantsTools() {
		r.used = true
		for _, tc := range reply.Calls {
			r.msgs = append(r.msgs, llm.Message{Role: "tool", Content: r.a.directRun(r.ctx, tc), ToolCallID: tc.ID})
		}
		return false
	}
	return r.answer(strings.TrimSpace(reply.Content))
}

// answer closes the run on the model's answer: a reply when nothing changed, the gate otherwise.
func (r *directRun) answer(text string) bool {
	a := r.a
	if cur, ok := treeFingerprint(a.cfg.Agent.WorkspaceDir, true); !ok || cur == r.baseline {
		r.res.Kind, r.res.Pass, r.res.Reply, r.res.Summary = KindChat, true, text, text
		r.res.Reason = "answered; no file changed"
		a.converse(r.t, text)
		return true
	}
	a.report("validating with anchor...")
	v := a.validateClaim(r.ctx, r.before)
	r.res.Validation, r.res.Summary = &v, text
	if v.Pass || r.gateTry >= directGateRetries {
		r.res.Pass, r.res.Reason = v.Pass, v.Reason
		a.report("task finished: %s", v.Reason)
		return true
	}
	r.gateTry++
	a.report(directGatePrefix+" (%d/%d)", r.gateTry, directGateRetries)
	r.msgs = append(r.msgs, llm.Message{Role: "user", Content: gateFeedback(v)})
	return false
}

// gateFeedback is what a red gate tells the model.
func gateFeedback(v anchor.Result) string {
	var b strings.Builder
	b.WriteString("The project's checks ran on your change and did not pass: " + v.Reason + "\n")
	for _, c := range v.Checks {
		if c.Pass {
			continue
		}
		fmt.Fprintf(&b, "\n$ %s (exit %d)\n%s\n", c.Command, c.Exit, truncateMiddle(c.Output+c.Error, 4000))
	}
	b.WriteString("\nFix what they name and run them again, or, if a failure is not yours and cannot be " +
		"fixed here, say so in your answer.")
	return b.String()
}

// directReply reads one streamed turn of the model.
func (a *Agent) directReply(ctx context.Context, msgs []llm.Message) (llm.Reply, error) {
	eng := a.toolEngine
	if eng == nil {
		eng = a.engine
	}
	var acc llm.StreamResult
	for chunk := range eng.CompleteToolsStream(ctx, msgs, directTools()) {
		switch chunk.Event {
		case llm.StreamError:
			return llm.Reply{}, chunk.Error
		case llm.StreamText, llm.StreamToolCall:
			acc.Handle(chunk)
		default:
			// Thinking and the end of the stream carry nothing the turn keeps.
		}
	}
	return acc.FinalReply(), nil
}

// directRun runs one tool call as a round of one action and returns what it printed.
func (a *Agent) directRun(ctx context.Context, tc llm.ToolCall) string {
	c, err := directCommand(tc)
	if err != nil {
		return "Error: " + err.Error()
	}
	out, runErr := a.runActions(ctx, []Command{c}, "")
	if runErr != nil {
		out += "\n" + runErr.Error()
	}
	return out
}
